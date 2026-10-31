package ledgerstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

// clockMS returns, in Unix milliseconds, the reading of testDeps's clock at
// its k-th call.
func clockMS(k int64) int64 { return (1_800_000_000 + k) * 1000 }

// commandID returns a UUIDv7 dated ms Unix milliseconds, made unique by n.
func commandID(ms int64, n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("%08x-%04x-7000-8000-%012x", ms>>16, ms&0xffff, n))
}

// commandDraft returns a ledger.command v1 draft of the agent for command id
// with the submission digest dg.
func commandDraft(t testing.TB, id uuid.UUID, dg ledger.Digest) ledger.Draft {
	t.Helper()
	p, err := ledger.Command{CommandID: id, SubmissionSHA256: dg}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Draft{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorAgent, Payload: p}
}

// submissionOf returns the submission digest that the tests give to tag.
func submissionOf(tag string) ledger.Digest { return digestOf("submission " + tag) }

func mustSubmit(t testing.TB, s *Store, command ledger.Draft, drafts ...ledger.Draft) Receipt {
	t.Helper()
	r, err := s.Submit(context.Background(), command, drafts)
	if err != nil {
		t.Fatalf("Submit = %v", err)
	}
	return r
}

// commandsOnce returns the commit range of each command ID that the durable
// log names in a ledger.command record, and fails the test for an ID that it
// names more than once.
func commandsOnce(t testing.TB, s *Store) map[uuid.UUID]appender.Committed {
	t.Helper()
	ranges := map[uuid.UUID]appender.Committed{}
	for _, c := range readCommits(t, s.LogDir(), s.Colony(), s.Durable()) {
		for _, r := range c.Records {
			if r.Type != ledger.TypeCommand {
				continue
			}
			p, err := ledger.DecodeCommand(r.Payload)
			if err != nil {
				t.Fatalf("seq %d: %v", r.Seq, err)
			}
			if prior, dup := ranges[p.CommandID]; dup {
				t.Errorf("command %s is in the log twice, at %+v and at %d-%d", p.CommandID, prior, c.First, c.Last)
			}
			ranges[p.CommandID] = appender.Committed{First: c.First, Last: c.Last}
		}
	}
	return ranges
}

// segmentFirsts returns the first sequences of the log's segments.
func segmentFirsts(t testing.TB, s *Store) []uint64 {
	t.Helper()
	entries, err := os.ReadDir(s.LogDir())
	if err != nil {
		t.Fatal(err)
	}
	var firsts []uint64
	for _, e := range entries {
		first, ok := ledger.ParseSegmentName(e.Name())
		if !ok {
			t.Fatalf("%s is not a segment name", e.Name())
		}
		firsts = append(firsts, first)
	}
	return firsts
}

// segmentOf returns the first sequence of the segment holding seq, among
// firsts.
func segmentOf(firsts []uint64, seq uint64) uint64 {
	var in uint64
	for _, f := range firsts {
		if f <= seq {
			in = f
		}
	}
	return in
}

func TestSubmitDeduplicates(t *testing.T) {
	ctx := context.Background()
	s := newColony(t, testDeps(nil))
	id1, id2 := commandID(clockMS(0), 1), commandID(clockMS(0), 2)
	a, b := submissionOf("a"), submissionOf("b")

	first := mustSubmit(t, s, commandDraft(t, id1, a), sample("effect"))
	if want := (Receipt{Committed: appender.Committed{First: 1, Last: 2}}); first != want {
		t.Fatalf("first Submit = %+v, want %+v", first, want)
	}
	if got := commandsOnce(t, s); len(got) != 1 || got[id1] != first.Committed {
		t.Errorf("commands in the log = %v, want %s at %+v", got, id1, first.Committed)
	}
	before := snapshot(t, s.LogDir())

	// The same ID and digest is the original commit, whatever drafts come
	// with the retry.
	for _, drafts := range [][]ledger.Draft{{sample("effect")}, {sample("another effect")}, nil} {
		got, err := s.Submit(ctx, commandDraft(t, id1, a), drafts)
		if want := (Receipt{Committed: first.Committed, Duplicate: true}); err != nil || got != want {
			t.Errorf("retry of the command = %+v, %v; want %+v", got, err, want)
		}
	}
	assertSnapshot(t, s.LogDir(), before)
	if s.Durable() != 2 {
		t.Errorf("Durable after the retries = %d, want 2", s.Durable())
	}

	// Another digest under the same ID is refused, and writes nothing.
	_, err := s.Submit(ctx, commandDraft(t, id1, b), []ledger.Draft{sample("effect")})
	if !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrCommandMismatch) {
		t.Errorf("Submit of another digest under the ID = %v, want ErrRefused and ErrCommandMismatch", err)
	}
	assertSnapshot(t, s.LogDir(), before)
	if s.Durable() != 2 {
		t.Errorf("Durable after the mismatch = %d, want 2", s.Durable())
	}

	// A new ID is a new commit; a command with no effect is a commit of its
	// record alone.
	if got, want := mustSubmit(t, s, commandDraft(t, id2, a)), (Receipt{Committed: appender.Committed{First: 3, Last: 3}}); got != want {
		t.Errorf("Submit of a new ID = %+v, want %+v", got, want)
	}
	// The command record heads its commit.
	for _, c := range readCommits(t, s.LogDir(), s.Colony(), s.Durable()) {
		if r := c.Records[0]; r.Type != ledger.TypeCommand || r.CommitIndex != 0 {
			t.Errorf("commit %d-%d starts with %s at commit_index %d, want ledger.command at 0", c.First, c.Last, r.Type, r.CommitIndex)
		}
	}

	// A Submit that the encoder refuses leaves its ID unrecorded: the same ID
	// with an effect that fits is a new command, not a duplicate.
	id3 := commandID(clockMS(0), 3)
	before = snapshot(t, s.LogDir())
	if _, err := s.Submit(ctx, commandDraft(t, id3, a), []ledger.Draft{padded(ledger.MaxRecordBytes)}); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ledger.ErrTooLarge) {
		t.Errorf("Submit of an effect over the record cap = %v, want ErrRefused and ErrTooLarge", err)
	}
	assertSnapshot(t, s.LogDir(), before)
	if got, want := mustSubmit(t, s, commandDraft(t, id3, a), sample("fits")), (Receipt{Committed: appender.Committed{First: 4, Last: 5}}); got != want {
		t.Errorf("Submit after the refusal = %+v, want %+v", got, want)
	}

	t.Run("concurrent submissions of each ID write it once", func(t *testing.T) {
		// The hot index holds two IDs, so that retries of the others meet
		// the log while other goroutines append.
		d := testDeps(nil)
		d.hotCommands = 2
		d.segmentTarget = 2 << 10
		s := newColony(t, d)
		const goroutines, commands = 8, 12
		receipts := make([][]Receipt, goroutines)
		var wg sync.WaitGroup
		for g := range goroutines {
			wg.Go(func() {
				for i := range commands {
					r, err := s.Submit(ctx, commandDraft(t, commandID(clockMS(0), i), submissionOf(fmt.Sprint(i))), []ledger.Draft{sample(fmt.Sprint(g, "/", i))})
					if err != nil {
						t.Errorf("goroutine %d Submit of command %d = %v", g, i, err)
						return
					}
					receipts[g] = append(receipts[g], r)
				}
			})
		}
		wg.Wait()
		if t.Failed() {
			t.FailNow()
		}
		for i := range commands {
			var fresh int
			for g := range goroutines {
				if got := receipts[g][i]; got.Committed != receipts[0][i].Committed {
					t.Errorf("command %d: goroutine %d got %+v, goroutine 0 got %+v", i, g, got, receipts[0][i])
				} else if !got.Duplicate {
					fresh++
				}
			}
			if fresh != 1 {
				t.Errorf("command %d was written by %d goroutines, want 1", i, fresh)
			}
		}
		logged := commandsOnce(t, s)
		if len(logged) != commands {
			t.Errorf("the log holds %d commands, want %d", len(logged), commands)
		}
		for i := range commands {
			if got := logged[commandID(clockMS(0), i)]; got != receipts[0][i].Committed {
				t.Errorf("command %d is in the log at %+v, Submit said %+v", i, got, receipts[0][i].Committed)
			}
		}
	})
}

func TestSubmitDuplicateIgnoresCurrentDrafts(t *testing.T) {
	ctx := context.Background()
	s := newColony(t, testDeps(nil))
	body := publish(t, s, "the effect's body")
	id := commandID(clockMS(0), 1)
	first := mustSubmit(t, s, commandDraft(t, id, submissionOf("a")), ref("effect", body))

	rep := collect(t, s, CollectPolicy{Actor: ledger.ActorCore, BeforeNS: math.MaxInt64})
	if rep.Recorded != 1 {
		t.Fatalf("Collect = %+v, want the body recorded", rep)
	}
	// The drafts, which name the collected body, are refused for a new command
	// ID.
	before := snapshot(t, s.LogDir())
	if _, err := s.Submit(ctx, commandDraft(t, commandID(clockMS(0), 2), submissionOf("a")), []ledger.Draft{ref("effect", body)}); !errors.Is(err, ErrBodyUnpublished) {
		t.Errorf("Submit of a new ID naming a collected body = %v, want ErrBodyUnpublished", err)
	}
	assertSnapshot(t, s.LogDir(), before)

	// But the original command's retry is a duplicate, not a refusal.
	got, err := s.Submit(ctx, commandDraft(t, id, submissionOf("a")), []ledger.Draft{ref("effect", body)})
	if want := (Receipt{Committed: first.Committed, Duplicate: true}); err != nil || got != want {
		t.Errorf("retry naming the collected body = %+v, %v; want %+v", got, err, want)
	}
	assertSnapshot(t, s.LogDir(), before)
}

func TestSubmitAtWritesOnlyAtTheExpectedTail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newColony(t, testDeps(nil))
	id1, id2, id3 := commandID(clockMS(0), 1), commandID(clockMS(0), 2), commandID(clockMS(0), 3)
	a, b := submissionOf("a"), submissionOf("b")

	// At the log's tail SubmitAt is Submit.
	first, err := s.SubmitAt(ctx, s.Durable(), commandDraft(t, id1, a), []ledger.Draft{sample("effect")})
	if want := (Receipt{Committed: appender.Committed{First: 1, Last: 2}}); err != nil || first != want {
		t.Fatalf("SubmitAt at the tail = %+v, %v; want %+v", first, err, want)
	}
	if got := commandsOnce(t, s); len(got) != 1 || got[id1] != first.Committed {
		t.Errorf("commands in the log = %v, want %s at %+v", got, id1, first.Committed)
	}

	// Another writer's commit moves the tail. A group planned at the old tail
	// is refused before its drafts are judged, even drafts that Submit would
	// refuse, and so is a tail the log has not reached; nothing is written.
	planned := s.Durable()
	mustAppend(t, s, sample("another writer"))
	durable := s.Durable()
	before := snapshot(t, s.LogDir())
	for _, row := range []struct {
		name   string
		tail   uint64
		drafts []ledger.Draft
	}{
		{"behind", planned, []ledger.Draft{sample("effect")}},
		{"ahead", durable + 1, []ledger.Draft{sample("effect")}},
		{"behind with a refused draft", planned, []ledger.Draft{padded(ledger.MaxRecordBytes)}},
	} {
		_, err := s.SubmitAt(ctx, row.tail, commandDraft(t, id2, a), row.drafts)
		if !errors.Is(err, ErrTailMoved) || errors.Is(err, appender.ErrRefused) {
			t.Errorf("%s: SubmitAt = %v, want ErrTailMoved without ErrRefused", row.name, err)
		}
	}
	assertSnapshot(t, s.LogDir(), before)
	if s.Durable() != durable {
		t.Errorf("Durable after the refusals = %d, want %d", s.Durable(), durable)
	}
	// The refusal recorded no identity: Submit writes the same command anew.
	if got, want := mustSubmit(t, s, commandDraft(t, id2, a), sample("effect")), (Receipt{Committed: appender.Committed{First: durable + 1, Last: durable + 2}}); got != want {
		t.Errorf("Submit after the refusal = %+v, want %+v", got, want)
	}

	// A logged command answers as for Submit whatever the tail.
	before = snapshot(t, s.LogDir())
	if got, err := s.SubmitAt(ctx, planned, commandDraft(t, id1, a), nil); err != nil || got != (Receipt{Committed: first.Committed, Duplicate: true}) {
		t.Errorf("SubmitAt of a logged command at an old tail = %+v, %v; want a duplicate of %+v", got, err, first.Committed)
	}
	if _, err := s.SubmitAt(ctx, planned, commandDraft(t, id1, b), nil); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrCommandMismatch) || errors.Is(err, ErrTailMoved) {
		t.Errorf("SubmitAt of another digest under a logged ID at an old tail = %v, want ErrRefused and ErrCommandMismatch", err)
	}

	// A ctx that ended before the writer turn answers its own error.
	ended, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SubmitAt(ended, planned, commandDraft(t, id3, a), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("SubmitAt with an ended ctx = %v, want context.Canceled", err)
	}
	assertSnapshot(t, s.LogDir(), before)
}

func TestSubmitRefusesFutureCommandID(t *testing.T) {
	ctx := context.Background()
	// The clock reads a time with a part of a millisecond, so that the limit
	// is the time plus five minutes rounded down.
	const t0 = int64(1_800_000_000_123_456_789)
	var now atomic.Int64
	now.Store(t0)
	d := testDeps(nil)
	d.clock = func() time.Time { return time.Unix(0, now.Load()) }
	s := newColony(t, d)
	limit := (t0 + int64(5*time.Minute)) / int64(time.Millisecond)
	if limit != 1_800_000_300_123 {
		t.Fatalf("limit = %d; the test's arithmetic is wrong", limit)
	}
	refuses := func(t *testing.T, id uuid.UUID) {
		t.Helper()
		before, seq := snapshot(t, s.LogDir()), s.Durable()
		_, err := s.Submit(ctx, commandDraft(t, id, submissionOf("a")), nil)
		if !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrCommandTime) {
			t.Errorf("Submit of an ID dated %d ms = %v, want ErrRefused and ErrCommandTime", idMS(id), err)
		}
		assertSnapshot(t, s.LogDir(), before)
		if s.Durable() != seq {
			t.Errorf("Durable after the refusal = %d, want %d", s.Durable(), seq)
		}
	}

	over := commandID(limit+1, 1)
	refuses(t, over)
	if got := mustSubmit(t, s, commandDraft(t, commandID(limit, 2), submissionOf("a"))); got.Committed != (appender.Committed{First: 1, Last: 1}) {
		t.Errorf("Submit of an ID dated at the limit = %+v, want 1-1", got)
	}

	// The refusal depends on the clock: a millisecond later the same request
	// succeeds, with nothing recorded in between.
	now.Add(int64(time.Millisecond))
	if got := mustSubmit(t, s, commandDraft(t, over, submissionOf("a"))); got.Duplicate || got.Committed != (appender.Committed{First: 2, Last: 2}) {
		t.Errorf("retry of the refused ID a millisecond later = %+v, want a new commit 2-2", got)
	}

	// A clock that moves backwards is held at the previous commit's time, and
	// the limit follows the held time, not the reading.
	now.Store(t0 - int64(time.Hour))
	late := commandID(limit+1+1, 3) // the limit of the held time, rounded, is limit+1
	refuses(t, late)
	if got := mustSubmit(t, s, commandDraft(t, commandID(limit+1, 4), submissionOf("a"))); got.Committed != (appender.Committed{First: 3, Last: 3}) {
		t.Errorf("Submit of an ID dated at the held limit = %+v, want 3-3", got)
	}
}

func TestIssueLimitMS(t *testing.T) {
	const ms = int64(time.Millisecond)
	for _, tc := range []struct {
		ns   int64
		lead time.Duration
	}{
		{1, 5 * time.Minute},
		{1_800_000_000_123_456_789, 5 * time.Minute},
		{math.MaxInt64, 5 * time.Minute},
		{math.MaxInt64, time.Duration(math.MaxInt64)},
		{ms - 1, 1},                    // the remainders carry into the next millisecond
		{ms - 1, 2 * time.Microsecond}, // and here
		{999_999_999, 1_500 * time.Microsecond},
	} {
		sum := new(big.Int).Add(big.NewInt(tc.ns), big.NewInt(int64(tc.lead)))
		want := new(big.Int).Quo(sum, big.NewInt(ms)).Int64()
		if got := issueLimitMS(tc.ns, tc.lead); got != want {
			t.Errorf("issueLimitMS(%d, %v) = %d, want %d", tc.ns, tc.lead, got, want)
		}
	}
}

func TestOldCommandResolvesAfterEviction(t *testing.T) {
	ctx := context.Background()
	d := testDeps(nil)
	d.hotCommands = 4
	d.segmentTarget = 4 << 10
	s := newColony(t, d)

	type command struct {
		id     uuid.UUID
		digest ledger.Digest
		r      appender.Committed
	}
	var recorded []command // in log order
	record := func(t *testing.T, s *Store, id uuid.UUID, tag string, drafts ...ledger.Draft) {
		t.Helper()
		dg := submissionOf(tag)
		r := mustSubmit(t, s, commandDraft(t, id, dg), drafts...)
		if r.Duplicate {
			t.Fatalf("Submit of the new ID %s was a duplicate", id)
		}
		recorded = append(recorded, command{id, dg, r.Committed})
	}
	// The IDs are dated increasingly, up to four minutes after their commits,
	// so that the ID of every evicted command is within the issuance lead of
	// the newest evicted commit.
	const n = 40
	for i := range n {
		record(t, s, commandID(clockMS(0)+int64(i)*1000+240_000, i), fmt.Sprint(i), padded(100+10*i))
	}
	if firsts := segmentFirsts(t, s); len(firsts) < 6 {
		t.Fatalf("the log has %d segments, want several", len(firsts))
	}

	// check retries every recorded command, with its digest and with another,
	// and expects the commands past the newest four to come from the log.
	check := func(t *testing.T, s *Store) {
		t.Helper()
		durable := s.Durable()
		hot := len(recorded) - d.hotCommands
		for i, c := range recorded {
			scans := s.coldScans.Load()
			got, err := s.Submit(ctx, commandDraft(t, c.id, c.digest), []ledger.Draft{sample("retry")})
			if want := (Receipt{Committed: c.r, Duplicate: true}); err != nil || got != want {
				t.Errorf("retry of command %d (%s) = %+v, %v; want %+v", i, c.id, got, err, want)
			}
			if fromLog := s.coldScans.Load() > scans; fromLog != (i < hot) {
				t.Errorf("command %d of %d: found in the log = %v, want %v", i, len(recorded), fromLog, i < hot)
			}
			if i%7 == 0 || i == hot-1 || i == hot {
				_, err := s.Submit(ctx, commandDraft(t, c.id, submissionOf("another")), nil)
				if !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrCommandMismatch) {
					t.Errorf("command %d under another digest = %v, want ErrRefused and ErrCommandMismatch", i, err)
				}
			}
		}
		if s.Durable() != durable {
			t.Errorf("the retries moved Durable from %d to %d", durable, s.Durable())
		}
	}
	check(t, s)

	// IDs the log never held. One between two commands of one segment lies
	// in that segment's range alone, which is read and found not to hold it.
	// One older than every command lies in no segment's range, so that
	// nothing is read. Both are then recorded as new, the newer first, so
	// that the older one's recording does not widen the range of the segment
	// that the newer one's lookup reads.
	firsts := segmentFirsts(t, s)
	between := -1
	for i := 0; i+1 < n && between < 0; i++ {
		if i > 3 && segmentOf(firsts, recorded[i].r.First) == segmentOf(firsts, recorded[i+1].r.First) {
			between = i
		}
	}
	if between < 0 {
		t.Fatal("no two neighbouring commands share a segment")
	}
	middle := commandID(clockMS(0)+int64(between)*1000+240_000+500, 100)
	oldest := commandID(clockMS(0)-60_000, 101)
	for _, tc := range []struct {
		name  string
		id    uuid.UUID
		scans int64
	}{{"between two commands of a segment", middle, 1}, {"older than every command", oldest, 0}} {
		scans := s.coldScans.Load()
		record(t, s, tc.id, tc.name, sample(tc.name))
		if got := s.coldScans.Load() - scans; got != tc.scans {
			t.Errorf("the new ID %s read %d segments, want %d", tc.name, got, tc.scans)
		}
	}
	check(t, s)

	s2, _ := reopen(t, s, d)
	check(t, s2)
	s3, _ := reopen(t, s2, d)
	check(t, s3)
	logged := commandsOnce(t, s3)
	for i, c := range recorded {
		if logged[c.id] != c.r {
			t.Errorf("command %d is in the log at %+v, Submit said %+v", i, logged[c.id], c.r)
		}
	}
}

// countdownCtx is a context that reports itself ended from its (n+1)-th call
// of Err on.
type countdownCtx struct {
	context.Context
	left atomic.Int64
}

func (c *countdownCtx) Err() error {
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

func TestSubmitLookupHonoursCtx(t *testing.T) {
	d := testDeps(nil)
	d.hotCommands = 1
	d.segmentTarget = 1 // every commit starts a segment
	s := newColony(t, d)
	var ids []uuid.UUID
	for i := range 5 {
		ids = append(ids, commandID(clockMS(0)+int64(i), i))
		mustSubmit(t, s, commandDraft(t, ids[i], submissionOf("a")))
	}
	before := snapshot(t, s.LogDir())
	// The first Err call is Submit's own, before it takes the turn; the next
	// belongs to the read of the log for command 0, which is no longer hot.
	ctx := &countdownCtx{Context: context.Background()}
	ctx.left.Store(1)
	if _, err := s.Submit(ctx, commandDraft(t, ids[0], submissionOf("a")), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit with a ctx that ends during the lookup = %v, want context.Canceled", err)
	}
	assertSnapshot(t, s.LogDir(), before)
	// The store is still usable, and the command is still known.
	if got := mustSubmit(t, s, commandDraft(t, ids[0], submissionOf("a"))); !got.Duplicate || got.Committed != (appender.Committed{First: 1, Last: 1}) {
		t.Errorf("retry after the abort = %+v, want a duplicate of 1-1", got)
	}
}

func TestSubmitFailsClosedOnAnUndecodableCommandRecord(t *testing.T) {
	// Open verified every command record, so one that cannot be decoded when
	// a lookup reads it means that the log changed under the store. The
	// lookup says so and writes nothing.
	d := testDeps(nil)
	d.hotCommands = 1
	s := newColony(t, d)
	a, b := commandID(clockMS(0), 1), commandID(clockMS(0), 2)
	mustSubmit(t, s, commandDraft(t, a, submissionOf("a")))
	mustSubmit(t, s, commandDraft(t, b, submissionOf("a")))
	broken := commandDraft(t, a, submissionOf("a"))
	broken.Payload = []byte(`{"command_id":"` + a.String() + `"}`)
	writeLog(t, s, []ledger.Draft{broken}, []ledger.Draft{commandDraft(t, b, submissionOf("a"))})
	durable := s.Durable()
	_, err := s.Submit(context.Background(), commandDraft(t, a, submissionOf("a")), nil)
	if !errors.Is(err, ErrInconsistent) || !strings.Contains(err.Error(), "seq 1:") {
		t.Errorf("Submit of a command whose record cannot be decoded = %v, want ErrInconsistent naming seq 1", err)
	}
	if s.Durable() != durable {
		t.Errorf("Durable after the failed lookup = %d, want %d", s.Durable(), durable)
	}
}

func TestFreshCommandIDsStayOffTheDisk(t *testing.T) {
	// Four commands share one segment, so that its range of ID times spans
	// them all. The first two are then evicted from the hot index, and the
	// commit time has moved on far past the lead.
	var now atomic.Int64
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	d := testDeps(nil)
	d.clock = func() time.Time { return time.Unix(0, now.Load()) }
	d.hotCommands = 2
	s := newColony(t, d)
	ms := func(ns int64) int64 { return ns / int64(time.Millisecond) }
	submit := func(at int64, id uuid.UUID) Receipt {
		now.Store(at)
		return mustSubmit(t, s, commandDraft(t, id, submissionOf("a")))
	}
	later := start + int64(time.Hour)
	submit(start, commandID(ms(start), 1))
	submit(start+int64(time.Second), commandID(ms(start)+1000, 2))
	submit(later, commandID(ms(later), 3))
	submit(later+int64(time.Second), commandID(ms(later)+1000, 4))

	// An ID dated between commands 3 and 4 is in the segment's range, but
	// later than the newest evicted commit (command 2) allows, so it is new
	// without any reading.
	scans := s.coldScans.Load()
	if got := submit(later+2*int64(time.Second), commandID(ms(later)+500, 5)); got.Duplicate {
		t.Fatalf("Submit of a fresh ID = %+v, want a new commit", got)
	}
	if got := s.coldScans.Load() - scans; got != 0 {
		t.Errorf("a fresh ID read %d segments, want none", got)
	}

	// Command 2 is within the lead of the newest evicted commit, so the
	// segment that can hold it is read.
	scans = s.coldScans.Load()
	if got := submit(later+3*int64(time.Second), commandID(ms(start)+1000, 2)); !got.Duplicate || got.Committed != (appender.Committed{First: 2, Last: 2}) {
		t.Errorf("retry of command 2 = %+v, want a duplicate of 2-2", got)
	}
	if got := s.coldScans.Load() - scans; got != 1 {
		t.Errorf("an old ID read %d segments, want the one segment that can hold it", got)
	}
}

func TestCommandLookupMatchesModel(t *testing.T) {
	ctx := context.Background()
	var ticks atomic.Int64
	d := testDeps(nil)
	d.clock = func() time.Time { return time.Unix(1_800_000_000+ticks.Add(1), 0) }
	d.hotCommands = 8
	d.segmentTarget = 2 << 10
	s := newColony(t, d)
	rng := rand.New(rand.NewPCG(20261009, 3))

	type known struct {
		digest ledger.Digest
		r      appender.Committed
	}
	model := map[uuid.UUID]known{}
	var ids []uuid.UUID // the model's IDs in recording order
	serial := 0
	// newID returns an ID that no earlier operation used, dated for the
	// commit that Submit is about to stamp: usually just before it, sometimes
	// up to four minutes after it, sometimes anywhere in the log's history.
	newID := func() uuid.UUID {
		stamp := clockMS(ticks.Load() + 1)
		ms := stamp - rng.Int64N(3000)
		switch k := rng.IntN(100); {
		case k < 2:
			ms = stamp + rng.Int64N(240_000)
		case k < 3:
			ms = clockMS(0) + rng.Int64N(stamp-clockMS(0))
		}
		serial++
		return commandID(ms, serial)
	}
	// pick returns a known ID: half the time one of the newest sixteen,
	// which straddle the edge of the hot index, otherwise any.
	pick := func() uuid.UUID {
		if rng.IntN(2) == 0 {
			return ids[len(ids)-1-rng.IntN(min(len(ids), 16))]
		}
		return ids[rng.IntN(len(ids))]
	}

	const operations = 2000
	for op := range operations {
		switch k := rng.IntN(100); {
		case k < 10 || len(ids) == 0: // a new command, with an effect of up to one draft
			id, dg := newID(), submissionOf(fmt.Sprint("model ", op))
			var drafts []ledger.Draft
			for range rng.IntN(2) {
				drafts = append(drafts, padded(20+rng.IntN(60)))
			}
			got, err := s.Submit(ctx, commandDraft(t, id, dg), drafts)
			if err != nil || got.Duplicate {
				t.Fatalf("op %d: Submit of the new ID %s = %+v, %v", op, id, got, err)
			}
			model[id] = known{dg, got.Committed}
			ids = append(ids, id)
		case k < 55: // a retry, with drafts of its own
			id := pick()
			got, err := s.Submit(ctx, commandDraft(t, id, model[id].digest), []ledger.Draft{sample(fmt.Sprint("retry ", op))})
			if want := (Receipt{Committed: model[id].r, Duplicate: true}); err != nil || got != want {
				t.Fatalf("op %d: retry of %s = %+v, %v; want %+v", op, id, got, err, want)
			}
		case k < 98: // another digest under a known ID
			id, seq := pick(), s.Durable()
			_, err := s.Submit(ctx, commandDraft(t, id, submissionOf(fmt.Sprint("other ", op))), nil)
			if !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrCommandMismatch) || s.Durable() != seq {
				t.Fatalf("op %d: Submit of %s under another digest = %v, Durable %d -> %d; want ErrRefused and ErrCommandMismatch", op, id, err, seq, s.Durable())
			}
		default: // a restart
			s, _ = reopen(t, s, d)
		}
	}

	// The log holds every command once, where the model put it, and a
	// restarted store resolves the oldest and the newest of them.
	s, _ = reopen(t, s, d)
	logged := commandsOnce(t, s)
	if len(logged) != len(model) {
		t.Errorf("the log holds %d commands, the model %d", len(logged), len(model))
	}
	for id, m := range model {
		if logged[id] != m.r {
			t.Errorf("command %s is in the log at %+v, the model has %+v", id, logged[id], m.r)
		}
	}
	for _, id := range slices.Concat(ids[:20], ids[len(ids)-20:]) {
		got, err := s.Submit(ctx, commandDraft(t, id, model[id].digest), nil)
		if want := (Receipt{Committed: model[id].r, Duplicate: true}); err != nil || got != want {
			t.Errorf("after the run, retry of %s = %+v, %v; want %+v", id, got, err, want)
		}
	}
}

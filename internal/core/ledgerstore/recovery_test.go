package ledgerstore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

func TestOpenTruncatesTornFinalCommit(t *testing.T) {
	d := testDeps(nil)
	s := newColony(t, d)
	mustAppend(t, s, sample("before"))
	path := filepath.Join(s.LogDir(), ledger.SegmentName(1))
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	boundary := fi.Size()
	mustAppend(t, s, sample("a"), sample("b"), sample("c"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Each of the torn commit's three lines is cut where its structure
	// changes: at the end of the hash, just after the TAB, in the middle of
	// the JSON, before the LF and after it, except after the last LF, where
	// the commit is complete. The cut at boundary is the control, with
	// nothing torn.
	type cutPoint struct {
		name string
		at   int64
	}
	cuts := []cutPoint{{"boundary", boundary}, {"boundary+1", boundary + 1}}
	start := boundary
	for i, line := 1, full[boundary:]; len(line) > 0; i++ {
		n := int64(bytes.IndexByte(line, '\n') + 1)
		if n == 0 {
			t.Fatalf("line %d of the torn commit has no LF", i)
		}
		end := start + n
		cuts = append(cuts,
			cutPoint{fmt.Sprintf("line%d/hash-end", i), start + 64},
			cutPoint{fmt.Sprintf("line%d/after-tab", i), start + 65},
			cutPoint{fmt.Sprintf("line%d/json-middle", i), start + 65 + (n-66)/2},
			cutPoint{fmt.Sprintf("line%d/before-lf", i), end - 1},
		)
		if end < int64(len(full)) {
			cuts = append(cuts, cutPoint{fmt.Sprintf("line%d/after-lf", i), end})
		}
		start, line = end, line[n:]
	}
	controls := 0
	for _, cut := range cuts {
		if cut.at < boundary || cut.at >= int64(len(full)) {
			t.Fatalf("%s: cut at %d is outside the torn commit [%d, %d)", cut.name, cut.at, boundary, len(full))
		}
		if cut.at == boundary {
			controls++
		}
	}
	if controls != 1 {
		t.Fatalf("%d cuts at the commit boundary, want the one control", controls)
	}

	for _, cut := range cuts {
		t.Run(cut.name, func(t *testing.T) {
			if err := os.WriteFile(path, full[:cut.at], 0o600); err != nil {
				t.Fatal(err)
			}
			s2, rec, err := open(s.dir, s.cat, d)
			if err != nil {
				t.Fatalf("open = %v", err)
			}
			want := Recovery{}
			if cut.at > boundary {
				want = Recovery{Segment: ledger.SegmentName(1), TruncatedBytes: cut.at - boundary}
			}
			if rec != want {
				t.Errorf("recovery %+v, want %+v", rec, want)
			}
			if size := fileSize(t, path); size != boundary {
				t.Errorf("segment size %d after open, want %d", size, boundary)
			}
			c, err := s2.Append(context.Background(), []ledger.Draft{sample("next")})
			if cerr := s2.Close(); err == nil {
				err = cerr
			}
			if err != nil || c != (appender.Committed{First: 2, Last: 2}) {
				t.Fatalf("Append after open = %+v, %v; want 2-2", c, err)
			}
			res, err := ledger.Check(s2.LogDir(), s2.Colony())
			if err != nil || res.Last.Seq != 2 || res.TornBytes != 0 {
				t.Fatalf("Check after the next Append = %+v, %v; want seq 2 and no torn bytes", res, err)
			}
		})
	}

	t.Run("fully torn later segment", func(t *testing.T) {
		d := testDeps(nil)
		d.segmentTarget = 1 // every commit after the first starts a segment
		s := newColony(t, d)
		mustAppend(t, s, sample("one"))
		mustAppend(t, s, sample("a"), sample("b"))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		name := ledger.SegmentName(2)
		path := filepath.Join(s.LogDir(), name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		torn := int64(len(data) / 2)
		if err := os.Truncate(path, torn); err != nil {
			t.Fatal(err)
		}
		s2, rec, err := open(s.dir, s.cat, d)
		if err != nil {
			t.Fatalf("open = %v", err)
		}
		t.Cleanup(func() { s2.Close() })
		if want := (Recovery{Segment: name, TruncatedBytes: torn}); rec != want {
			t.Errorf("recovery %+v, want %+v", rec, want)
		}
		if size := fileSize(t, path); size != 0 {
			t.Errorf("%s holds %d bytes after open, want 0", name, size)
		}
		if c := mustAppend(t, s2, sample("c")); c != (appender.Committed{First: 2, Last: 2}) {
			t.Errorf("Append after open = %+v, want 2-2", c)
		}
		if got := snapshot(t, s2.LogDir()); len(got) != 2 || len(got[name]) == 0 {
			t.Errorf("segments %v, want the commit in the emptied %s", slices.Sorted(maps.Keys(got)), name)
		}
	})
}

func fileSize(t testing.TB, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func TestOpenFailsClosedOnCorruption(t *testing.T) {
	// colony returns a closed colony with one segment per commit.
	colony := func(t *testing.T) *Store {
		d := testDeps(nil)
		d.segmentTarget = 1
		s := newColony(t, d)
		for i := range 3 {
			mustAppend(t, s, sample(fmt.Sprint(i)))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, s *Store)
	}{
		{"flipped byte in a non-final commit", func(t *testing.T, s *Store) {
			path := filepath.Join(s.LogDir(), ledger.SegmentName(1))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[100] ^= 1 // inside the first record's JSON
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"renamed segment", func(t *testing.T, s *Store) {
			if err := os.Rename(filepath.Join(s.LogDir(), ledger.SegmentName(3)), filepath.Join(s.LogDir(), ledger.SegmentName(4))); err != nil {
				t.Fatal(err)
			}
		}},
		{"log of another colony", func(t *testing.T, s *Store) {
			other := colony(t)
			for name, data := range snapshot(t, other.LogDir()) {
				if err := os.WriteFile(filepath.Join(s.LogDir(), name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := colony(t)
			tc.corrupt(t, s)
			before := snapshot(t, s.dir)
			if _, _, err := Open(s.dir, s.cat); !errors.Is(err, ledger.ErrCorrupt) {
				t.Errorf("Open = %v, want ErrCorrupt", err)
			}
			assertSnapshot(t, s.dir, before)
			assertUnlocked(t, s.dir)
		})
	}
}

// writeLog writes commits, each a list of drafts, as the whole log of the
// closed colony s, which holds none, with ledger.EncodeCommit, as a writer
// that ignores the F0 record rules would.
func writeLog(t testing.TB, s *Store, commits ...[]ledger.Draft) {
	t.Helper()
	var data []byte
	chain := ledger.Genesis()
	for i, drafts := range commits {
		h := ledger.CommitHeader{ColonyID: s.Colony(), CommitID: uuid.NewV7(), EventTimeNS: 1_800_000_000_000_000_000 + int64(i)*int64(time.Second)}
		enc, next, err := ledger.EncodeCommit(chain, h, drafts)
		if err != nil {
			t.Fatalf("EncodeCommit of commit %d = %v", i+1, err)
		}
		data = append(data, enc...)
		chain = next
	}
	writeFile(t, filepath.Join(s.LogDir(), ledger.SegmentName(1)), data)
}

func TestOpenFailsClosedOnF0RuleViolation(t *testing.T) {
	p1, p2 := uuid.NewV7(), uuid.NewV7()
	x := digestOf("a body")
	cmd := func(n int) ledger.Draft { return commandDraft(t, commandID(clockMS(0), n), submissionOf("a")) }
	pin := func(id uuid.UUID) ledger.Draft { return pinDraft(t, id, x) }
	collected := func(actor ledger.Actor) ledger.Draft {
		p, err := ledger.BodiesCollected{BeforeNS: 1, Bodies: []ledger.CollectedBody{{SHA256: x, Bytes: 1}}}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return ledger.Draft{Type: ledger.TypeBodiesCollected, TypeVersion: 1, Actor: actor, Payload: p}
	}
	payload := func(d ledger.Draft, json string) ledger.Draft { d.Payload = []byte(json); return d }
	version := func(d ledger.Draft, v int) ledger.Draft { d.TypeVersion = v; return d }
	refs := func(d ledger.Draft) ledger.Draft { d.BodyRefs = []ledger.Digest{x}; return d }
	one := func(ds ...ledger.Draft) []ledger.Draft { return ds }

	// Each log is complete and verified by Check, and breaks one rule of an
	// F0 record type at the record seq.
	for _, tc := range []struct {
		name    string
		commits [][]ledger.Draft
		seq     uint64
	}{
		{"an unpin of an unknown pin", [][]ledger.Draft{one(sample("a")), one(unpinDraft(t, p1))}, 2},
		{"an unpin before its pin in one commit", [][]ledger.Draft{one(unpinDraft(t, p1), pin(p1))}, 1},
		{"a pin of an active pin", [][]ledger.Draft{one(pin(p1)), one(pin(p1))}, 2},
		{"a pin that lists no body", [][]ledger.Draft{one(pinDraft(t, p1))}, 1},
		{"an unpin that lists a body", [][]ledger.Draft{one(pin(p1)), one(refs(unpinDraft(t, p1)))}, 2},
		{"a pin whose payload does not decode", [][]ledger.Draft{one(payload(pin(p1), `{}`))}, 1},
		{"an unpin whose payload does not decode", [][]ledger.Draft{one(pin(p1)), one(payload(unpinDraft(t, p1), `{"pin_id":3}`))}, 2},
		{"a command that is not first in its commit", [][]ledger.Draft{one(sample("a"), cmd(1))}, 2},
		{"two commands in one commit", [][]ledger.Draft{one(cmd(1), cmd(2))}, 2},
		{"a command that lists a body", [][]ledger.Draft{one(refs(cmd(1)))}, 1},
		{"a command whose payload does not decode", [][]ledger.Draft{one(payload(cmd(1), `{}`))}, 1},
		{"a command ID recorded twice", [][]ledger.Draft{one(cmd(1)), one(sample("a")), one(cmd(1))}, 3},
		{"a collection that lists a body in body_refs", [][]ledger.Draft{one(refs(collected(ledger.ActorOwner)))}, 1},
		{"a collection ordered by the agent", [][]ledger.Draft{one(sample("a")), one(collected(ledger.ActorAgent))}, 2},
		{"a collection ordered by a runner", [][]ledger.Draft{one(collected(ledger.ActorRunner))}, 1},
		{"a collection whose payload does not decode", [][]ledger.Draft{one(payload(collected(ledger.ActorOwner), `{"before_ns":0}`))}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newColony(t, testDeps(nil))
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			writeLog(t, s, tc.commits...)
			before := snapshot(t, s.dir)
			_, _, err := Open(s.dir, s.cat)
			if !errors.Is(err, ErrInconsistent) || !strings.Contains(err.Error(), fmt.Sprintf("seq %d:", tc.seq)) {
				t.Errorf("Open = %v, want ErrInconsistent naming seq %d", err, tc.seq)
			}
			assertSnapshot(t, s.dir, before)
			assertUnlocked(t, s.dir)
		})
	}

	// A newer version of an F0 type is the reader's error, not corruption.
	for _, tc := range []struct {
		typ     string
		draft   ledger.Draft
		commits int // the commits before the record
	}{
		{ledger.TypeCommand, cmd(1), 0},
		{ledger.TypeBodyPin, pin(p1), 1},
		{ledger.TypeBodyUnpin, unpinDraft(t, p1), 2},
		{ledger.TypeBodiesCollected, collected(ledger.ActorOwner), 0},
	} {
		t.Run(tc.typ+" at version 2", func(t *testing.T) {
			s := newColony(t, testDeps(nil))
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			commits := slices.Repeat([][]ledger.Draft{one(sample("before"))}, tc.commits)
			writeLog(t, s, append(commits, one(version(tc.draft, 2)))...)
			before := snapshot(t, s.dir)
			_, _, err := Open(s.dir, s.cat)
			var uve *ledger.UnsupportedVersionError
			if !errors.As(err, &uve) || uve.Type != tc.typ || uve.Version != 2 || uve.Seq != uint64(tc.commits+1) {
				t.Errorf("Open = %v, want *ledger.UnsupportedVersionError for %s version 2 at seq %d", err, tc.typ, tc.commits+1)
			}
			assertSnapshot(t, s.dir, before)
			assertUnlocked(t, s.dir)
		})
	}

	// The rules are the log's alone: a log that keeps them all opens, with
	// the commands, pins and collections the records show.
	t.Run("a log that follows every rule", func(t *testing.T) {
		s := newColony(t, testDeps(nil))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		writeLog(t, s,
			one(sample("a")),
			one(pin(p1)),
			one(cmd(1), sample("effect")),
			one(collected(ledger.ActorOwner), sample("beside a collection")),
			one(unpinDraft(t, p1), pin(p2)),
			one(cmd(2)),
		)
		s2, _, err := open(s.dir, s.cat, testDeps(nil))
		if err != nil {
			t.Fatalf("Open = %v", err)
		}
		t.Cleanup(func() { s2.Close() })
		if got, want := mustSubmit(t, s2, cmd(1)), (Receipt{Committed: appender.Committed{First: 3, Last: 4}, Duplicate: true}); got != want {
			t.Errorf("Submit of the recorded command = %+v, want %+v", got, want)
		}
		mustAppend(t, s2, unpinDraft(t, p2))
		if _, err := s2.Append(context.Background(), one(unpinDraft(t, p1))); !errors.Is(err, appender.ErrRefused) {
			t.Errorf("Append of the unpin of a released pin = %v, want ErrRefused", err)
		}
	})
}

// childDirEnv names, to the child writer, the colony directory to create.
const childDirEnv = "DWARF_LEDGERSTORE_CHILD_DIR"

// TestHelperChildWriter is the writer process of
// TestKilledWriterReleasesLockAndRecovers. It creates the colony named by
// childDirEnv with a 4 KiB segment target and appends until it is killed or
// its standard output fails, printing each acknowledged range as
// "ack <First> <Last>".
func TestHelperChildWriter(t *testing.T) {
	dir := os.Getenv(childDirEnv)
	if dir == "" {
		t.Skip("runs only as the child of TestKilledWriterReleasesLockAndRecovers")
	}
	d := realDeps()
	d.segmentTarget = 4 << 10
	s, err := create(dir, testCatalogue(t), d)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		var drafts []ledger.Draft
		for range 1 + i%3 {
			drafts = append(drafts, padded(200))
		}
		c, err := s.Append(context.Background(), drafts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Printf("ack %d %d\n", c.First, c.Last); err != nil {
			return
		}
	}
}

func TestKilledWriterReleasesLockAndRecovers(t *testing.T) {
	cat := testCatalogue(t)
	dir := filepath.Join(t.TempDir(), "colony")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChildWriter$")
	cmd.Env = append(os.Environ(), childDirEnv+"="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewReader(stdout)
	var acks []appender.Committed
	var trailing []string // complete lines that are not acknowledgements
	// read records the next complete line and reports whether there was
	// one; a final line without its LF was cut by the kill and is ignored.
	read := func() bool {
		line, err := lines.ReadString('\n')
		if err != nil {
			return false
		}
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "ack" {
			first, err1 := strconv.ParseUint(f[1], 10, 64)
			last, err2 := strconv.ParseUint(f[2], 10, 64)
			if err1 == nil && err2 == nil {
				acks = append(acks, appender.Committed{First: first, Last: last})
				return true
			}
		}
		trailing = append(trailing, line)
		return true
	}
	// stop kills the child, reads what it printed before dying and waits
	// for its exit, which releases its handles.
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cmd.Process.Kill()
		for read() {
		}
		cmd.Wait()
	}
	t.Cleanup(stop)

	const n = 40
	for len(acks) < n && len(trailing) == 0 && read() {
		if len(acks) == 1 {
			// The child holds the lock from before its first
			// acknowledgement.
			if _, _, err := Open(dir, cat); !errors.Is(err, ErrLocked) {
				t.Errorf("Open while the child writes = %v, want ErrLocked", err)
			}
		}
	}
	stop()
	if len(acks) < n || len(trailing) > 0 {
		t.Fatalf("child stopped after %d acknowledgements; other output %q; stderr:\n%s", len(acks), trailing, stderr.Bytes())
	}
	s, rec, err := Open(dir, cat)
	if err != nil {
		t.Fatalf("Open after the kill = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	t.Logf("%d acknowledgements; recovery %+v", len(acks), rec)
	last := acks[len(acks)-1].Last
	if s.Durable() < last {
		t.Fatalf("Durable after the kill = %d, below the acknowledged %d", s.Durable(), last)
	}
	// The reader verifies that the log is gapless and chained from seq 1.
	commits := readCommits(t, s.LogDir(), s.Colony(), s.Durable())
	got := ranges(commits)
	if len(got) < len(acks) || !slices.Equal(got[:len(acks)], acks) {
		t.Fatalf("commits after the kill %v, want them to begin with the acknowledged %v", got, acks)
	}
	c := mustAppend(t, s, sample("after"))
	if c.First != got[len(got)-1].Last+1 {
		t.Errorf("Append after the kill = %+v, want it to follow seq %d", c, got[len(got)-1].Last)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := ledger.Check(s.LogDir(), s.Colony())
	if err != nil || res.Last.Seq != c.Last || res.TornBytes != 0 {
		t.Errorf("Check after the next Append = %+v, %v; want seq %d and no torn bytes", res, err, c.Last)
	}
}

// childSubmitDirEnv names, to the child submitter, the colony directory to
// create.
const childSubmitDirEnv = "DWARF_LEDGERSTORE_CHILD_SUBMIT_DIR"

// TestHelperChildSubmitter is the writer process of
// TestDuplicateCommandsAfterCrash. It creates the colony named by
// childSubmitDirEnv with a 4 KiB segment target and a hot index of four
// commands, and submits commands until it is killed or its standard output
// fails. Each has a fresh ID, a submission digest that the parent can compute
// from the ID and an effect of one record. The child prints "try <id>" before
// each Submit and "ack <id> <First> <Last>" after it.
func TestHelperChildSubmitter(t *testing.T) {
	dir := os.Getenv(childSubmitDirEnv)
	if dir == "" {
		t.Skip("runs only as the child of TestDuplicateCommandsAfterCrash")
	}
	d := realDeps()
	d.segmentTarget = 4 << 10
	d.hotCommands = 4
	s, err := create(dir, testCatalogue(t), d)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		id := uuid.NewV7()
		if _, err := fmt.Printf("try %s\n", id); err != nil {
			return
		}
		r, err := s.Submit(context.Background(), commandDraft(t, id, submissionOf(id.String())), []ledger.Draft{padded(100 + 100*(i%4))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Printf("ack %s %d %d\n", id, r.First, r.Last); err != nil {
			return
		}
	}
}

func TestDuplicateCommandsAfterCrash(t *testing.T) {
	ctx := context.Background()
	cat := testCatalogue(t)
	dir := filepath.Join(t.TempDir(), "colony")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChildSubmitter$")
	cmd.Env = append(os.Environ(), childSubmitDirEnv+"="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	type acked struct {
		id uuid.UUID
		r  appender.Committed
	}
	lines := bufio.NewReader(stdout)
	var acks []acked
	var inFlight uuid.UUID // the ID of the last "try" that has no "ack"; zero if none
	var trailing []string  // complete lines that are neither
	// read records the next complete line and reports whether there was
	// one; a final line without its LF was cut by the kill and is ignored.
	read := func() bool {
		line, err := lines.ReadString('\n')
		if err != nil {
			return false
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "try" && inFlight == (uuid.UUID{}):
			if id, err := uuid.Parse(f[1]); err == nil {
				inFlight = id
				return true
			}
		case len(f) == 4 && f[0] == "ack":
			id, err := uuid.Parse(f[1])
			first, err1 := strconv.ParseUint(f[2], 10, 64)
			last, err2 := strconv.ParseUint(f[3], 10, 64)
			if err == nil && err1 == nil && err2 == nil && id == inFlight {
				acks = append(acks, acked{id, appender.Committed{First: first, Last: last}})
				inFlight = uuid.UUID{}
				return true
			}
		}
		trailing = append(trailing, line)
		return true
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cmd.Process.Kill()
		for read() {
		}
		cmd.Wait()
	}
	t.Cleanup(stop)

	const n = 40
	for len(acks) < n && len(trailing) == 0 && read() {
	}
	stop()
	if len(acks) < n || len(trailing) > 0 {
		t.Fatalf("child stopped after %d acknowledgements; other output %q; stderr:\n%s", len(acks), trailing, stderr.Bytes())
	}

	d := realDeps()
	d.hotCommands = 4 // all but the newest acknowledged commands are looked up in the log
	s, rec, err := open(dir, cat, d)
	if err != nil {
		t.Fatalf("Open after the kill = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	t.Logf("%d acknowledgements; command in flight %v; recovery %+v", len(acks), inFlight != (uuid.UUID{}), rec)

	// Every acknowledged command resolves to the range its child printed,
	// whatever drafts the retry brings: this one's would be refused.
	unpublished := []ledger.Draft{ref("retry", digestOf("a body that was never published"))}
	for _, a := range acks {
		got, err := s.Submit(ctx, commandDraft(t, a.id, submissionOf(a.id.String())), unpublished)
		if want := (Receipt{Committed: a.r, Duplicate: true}); err != nil || got != want {
			t.Errorf("retry of the acknowledged command %s = %+v, %v; want %+v", a.id, got, err, want)
		}
	}

	// The command in flight when the child died is in the log or it is not,
	// and its retry says which; the answer then stands.
	if inFlight != (uuid.UUID{}) {
		retry := func() Receipt {
			got, err := s.Submit(ctx, commandDraft(t, inFlight, submissionOf(inFlight.String())), []ledger.Draft{padded(100)})
			if err != nil {
				t.Fatalf("retry of the command in flight = %v", err)
			}
			return got
		}
		first := retry()
		t.Logf("the command in flight was in the log already: %v", first.Duplicate)
		if first.First <= acks[len(acks)-1].r.Last {
			t.Errorf("the command in flight is at %+v, not after the acknowledged command at %+v", first.Committed, acks[len(acks)-1].r)
		}
		if again := retry(); again != (Receipt{Committed: first.Committed, Duplicate: true}) {
			t.Errorf("second retry of the command in flight = %+v, want a duplicate of the first outcome %+v", again, first)
		}
		acks = append(acks, acked{inFlight, first.Committed})
	}

	// The log holds each command once, where it was told.
	logged := commandsOnce(t, s)
	if len(logged) != len(acks) {
		t.Errorf("the log holds %d commands, want %d", len(logged), len(acks))
	}
	for _, a := range acks {
		if logged[a.id] != a.r {
			t.Errorf("command %s is in the log at %+v, want %+v", a.id, logged[a.id], a.r)
		}
	}
}

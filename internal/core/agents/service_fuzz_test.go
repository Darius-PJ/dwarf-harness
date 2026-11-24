package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// serviceFuzzPrefixes builds each shape's prefix once, inside the fuzz function,
// for whichever seed needs it first. Everything a build creates belongs to f:
// directories under root, closers run in f's cleanup, and the lent context.
type serviceFuzzPrefixes struct {
	root    string
	ctx     context.Context
	mu      sync.Mutex
	closers []func()
	shapes  []serviceFuzzShape
}

// A build that fails or never finishes leaves its first cause for every later
// seed: sync.Once also counts a Goexit as done.
type serviceFuzzShape struct {
	once    sync.Once
	build   func(testing.TB) loopFuzzPrefix
	failure string
	prefix  loopFuzzPrefix
}

func newServiceFuzzPrefixes(f *testing.F, builds []func(testing.TB) loopFuzzPrefix) *serviceFuzzPrefixes {
	f.Helper()
	p := &serviceFuzzPrefixes{root: testRoot(f), shapes: make([]serviceFuzzShape, len(builds))}
	for i, build := range builds {
		p.shapes[i].build = build
	}
	var cancel context.CancelFunc
	p.ctx, cancel = context.WithCancel(context.Background())
	// As testing orders its own: cancel, then close LIFO, then f removes root.
	f.Cleanup(func() {
		cancel()
		p.mu.Lock()
		closers := p.closers
		p.closers = nil
		p.mu.Unlock()
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	})
	return p
}

func (p *serviceFuzzPrefixes) get(t *testing.T, shape int) loopFuzzPrefix {
	t.Helper()
	shape %= len(p.shapes)
	s := &p.shapes[shape]
	s.once.Do(func() {
		s.failure = "service fuzz prefix build abandoned"
		b := &serviceFuzzBuild{TB: t, prefixes: p, shape: s}
		prefix := s.build(b)
		if !b.failed && !t.Failed() {
			s.prefix, s.failure = prefix, ""
		}
	})
	if s.failure != "" {
		t.Fatalf("service fuzz prefix %d: %s", shape, s.failure)
	}
	return s.prefix
}

// serviceFuzzBuild lends the triggering seed's reports to a shared build, while
// nothing the build creates is bound to that seed's lifetime. Every failure
// records the first cause before it reaches the seed; process state is refused
// because other seeds run alongside.
type serviceFuzzBuild struct {
	testing.TB
	prefixes *serviceFuzzPrefixes
	shape    *serviceFuzzShape
	failed   bool
}

func (b *serviceFuzzBuild) cause(text string) {
	if !b.failed {
		b.shape.failure, b.failed = text, true
	}
}

func (b *serviceFuzzBuild) TempDir() string {
	b.Helper()
	dir, err := os.MkdirTemp(b.prefixes.root, "prefix-")
	if err != nil {
		b.Fatal(err)
	}
	return dir
}

func (b *serviceFuzzBuild) Cleanup(fn func()) {
	b.prefixes.mu.Lock()
	defer b.prefixes.mu.Unlock()
	b.prefixes.closers = append(b.prefixes.closers, fn)
}

func (b *serviceFuzzBuild) Context() context.Context { return b.prefixes.ctx }

func (b *serviceFuzzBuild) Setenv(string, string) {
	b.Helper()
	b.Fatal("Setenv forbidden in a shared build")
}

func (b *serviceFuzzBuild) Chdir(string) {
	b.Helper()
	b.Fatal("Chdir forbidden in a shared build")
}

func (b *serviceFuzzBuild) ArtifactDir() string {
	b.Helper()
	b.Fatal("ArtifactDir forbidden in a shared build")
	return ""
}

func (b *serviceFuzzBuild) Error(args ...any) {
	b.Helper()
	b.cause(fmt.Sprint(args...))
	b.TB.Error(args...)
}

func (b *serviceFuzzBuild) Errorf(format string, args ...any) {
	b.Helper()
	b.cause(fmt.Sprintf(format, args...))
	b.TB.Errorf(format, args...)
}

func (b *serviceFuzzBuild) Fail() {
	b.Helper()
	b.cause("Fail called")
	b.TB.Fail()
}

func (b *serviceFuzzBuild) FailNow() {
	b.Helper()
	b.cause("FailNow called")
	b.TB.FailNow()
}

func (b *serviceFuzzBuild) Fatal(args ...any) {
	b.Helper()
	b.cause(fmt.Sprint(args...))
	b.TB.Fatal(args...)
}

func (b *serviceFuzzBuild) Fatalf(format string, args ...any) {
	b.Helper()
	b.cause(fmt.Sprintf(format, args...))
	b.TB.Fatalf(format, args...)
}

func (b *serviceFuzzBuild) Skip(args ...any) {
	b.Helper()
	b.cause("skipped: " + fmt.Sprint(args...))
	b.TB.Skip(args...)
}

func (b *serviceFuzzBuild) Skipf(format string, args ...any) {
	b.Helper()
	b.cause("skipped: " + fmt.Sprintf(format, args...))
	b.TB.Skipf(format, args...)
}

func (b *serviceFuzzBuild) SkipNow() {
	b.Helper()
	b.cause("skipped")
	b.TB.SkipNow()
}

func serviceWithdrawalFuzzPrefix(t testing.TB) loopFuzzPrefix {
	t.Helper()
	fixture := newPaidFixture(t)
	s := fixture.service
	now := s.p.reviewFloor + int64(time.Hour)
	e := servicePaidFailure(t, fixture, "overloaded", now)
	serviceRecord(t, s, e)
	completion := serviceSuccessfulCall(t, fixture, e.CauseID, now+1)
	stop := errors.New("retained withdrawal prefix")
	var withdrawal ledger.RuntimeServiceObserved
	s.deps.afterServiceCommit = func(g *serviceGroup) {
		if g.observed != nil && g.observed.Source.Kind == "demand_withdrawn" {
			withdrawal = *g.observed
			s.deps.beforeServiceSubmit = func() error { return stop }
		}
	}
	if _, err := s.recordServiceEvidence(context.Background(), completion.ObservationID, completion); !errors.Is(err, stop) {
		t.Fatalf("withdrawal prefix boundary: %v", err)
	}
	s.mu.Lock()
	sub, err := s.serviceStateSubmission("service.withdraw", withdrawal.AgentID, withdrawal.CauseID, withdrawal.Source)
	var drafts []ledger.Draft
	if err == nil {
		var plan servicePlan
		plan, err = s.deriveService(context.Background(), nil, sub, s.p.through+1)
		if err == nil {
			drafts, err = serviceDrafts(uuid.NewV7(), sub, plan, s.p.through+1)
		}
	}
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prefix := retainLoopFuzzPrefix(t, fixture.serviceFixture)
	prefix.draft = drafts
	return prefix
}

func FuzzServiceGroupReplay(f *testing.F) {
	_, commits := scopedGoldenCommits(f, "91")
	var builds []func(testing.TB) loopFuzzPrefix
	// members and causes say which shapes the member and cause mutations change.
	var members, causes []bool
	operations, shapes := map[string]bool{}, map[string]bool{}
	for index, commit := range commits {
		if commit.Records[len(commit.Records)-1].Type != ledger.TypeRuntimeServiceReceipt {
			continue
		}
		g, err := decodeServiceFamily(draftsOf(commit), commit.First)
		if err != nil || g == nil {
			f.Fatal("invalid frozen service seed", err)
		}
		operations[g.receipt.Submission.Operation] = true
		// One prefix per distinct record shape: the operation and its record types.
		shape := g.receipt.Submission.Operation
		for _, record := range commit.Records {
			shape += " " + record.Type
		}
		if shapes[shape] {
			continue
		}
		shapes[shape] = true
		members, causes = append(members, g.member != nil), append(causes, g.cause != nil)
		builds = append(builds, func(t testing.TB) loopFuzzPrefix {
			fixture, _ := scopedGoldenFixture(t, "91", index)
			prefix := retainLoopFuzzPrefix(t, fixture)
			prefix.draft = draftsOf(commit)
			return prefix
		})
	}
	builds = append(builds, serviceWithdrawalFuzzPrefix)
	members, causes = append(members, false), append(causes, false)
	operations["service.withdraw"] = true
	for _, operation := range []string{"service.record", "service.observe", "service.expire", "service.clear", "service.withdraw", "service.complete"} {
		if !operations[operation] {
			f.Fatal("missing reachable service operation", operation)
		}
	}
	prefixes := newServiceFuzzPrefixes(f, builds)
	// Every shape replays once unchanged. Each mutation runs once, on a shape
	// it changes, rotating so parallel seeds spread across the prefix Services.
	for shape := range builds {
		f.Add(byte(shape), byte(0), uint64(1), []byte(`{}`))
	}
	for mode := byte(1); mode < 12; mode++ {
		var carriers []int
		for shape := range builds {
			if (mode != 4 && mode != 5 || members[shape]) && (mode != 6 || causes[shape]) {
				carriers = append(carriers, shape)
			}
		}
		if len(carriers) == 0 {
			f.Fatal("no frozen service shape carries mutation", mode)
		}
		f.Add(byte(carriers[int(mode)%len(carriers)]), mode, uint64(1), []byte(`{}`))
	}
	f.Fuzz(func(t *testing.T, shape, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxServicePayloadBytes {
			return
		}
		prefix := prefixes.get(t, int(shape))
		drafts := make([]ledger.Draft, len(prefix.draft))
		for i, d := range prefix.draft {
			drafts[i] = d
			drafts[i].Payload = bytes.Clone(d.Payload)
		}
		g, err := decodeServiceFamily(drafts, prefix.through+1)
		if err != nil || g == nil {
			t.Fatal("invalid retained service seed", err)
		}
		valid := mode%12 == 0
		switch mode % 12 {
		case 1:
			drafts[1].Payload = bytes.Clone(input)
		case 2:
			drafts[len(drafts)-1].Payload = bytes.Clone(input)
		case 3:
			g.receipt.SourceThrough = int64(value & uint64(^uint64(0)>>1))
			drafts[len(drafts)-1].Payload, err = g.receipt.Encode()
		case 4:
			if g.member != nil {
				g.member.FirstSource.ObservedUTCNS += int64(value % 4)
				for i := range drafts {
					if drafts[i].Type == ledger.TypeRuntimeServiceAgentChanged {
						drafts[i].Payload, err = g.member.Encode()
					}
				}
			}
		case 5:
			if g.member != nil {
				g.member.Phase = "future_phase"
				for i := range drafts {
					if drafts[i].Type == ledger.TypeRuntimeServiceAgentChanged {
						drafts[i].Payload, err = g.member.Encode()
					}
				}
			}
		case 6:
			if g.cause != nil {
				g.cause.Condition = "future_condition"
				for i := range drafts {
					if drafts[i].Type == ledger.TypeRuntimeServiceCauseChanged {
						drafts[i].Payload, err = g.cause.Encode()
					}
				}
			}
		case 7:
			drafts = append(drafts, drafts[1])
		case 8:
			drafts = drafts[:len(drafts)-1]
		case 9:
			if g.receipt.Submission.ExpectedGeneration != nil {
				g.receipt.Submission.ExpectedGeneration = new(*g.receipt.Submission.ExpectedGeneration + int64(value%4))
			} else {
				g.receipt.Submission.AgentID[15] ^= byte(value)
			}
			var raw []byte
			raw, err = g.receipt.Submission.Encode()
			if err == nil {
				g.receipt.SubmissionSHA256 = ledger.Digest(sha256.Sum256(raw))
				g.command.SubmissionSHA256 = g.receipt.SubmissionSHA256
				drafts[0].Payload, err = g.command.Encode()
			}
			if err == nil {
				drafts[len(drafts)-1].Payload, err = g.receipt.Encode()
			}
		case 10:
			drafts[1].Actor = ledger.ActorOwner
		case 11:
			drafts[1].AgentID = uuid.NewV7()
		}
		if err != nil {
			return
		}
		s := prefix.fixture.service
		s.mu.Lock()
		accepted, err := decodeServiceFamily(drafts, prefix.through+1)
		var plan serviceProjection
		if err == nil {
			if accepted == nil {
				err = ErrInvalid
			} else {
				plan, err = s.planService(context.Background(), nil, accepted)
			}
		}
		unchanged := sameProjectionState(s.p, prefix.baseline.p)
		if err != nil {
			_, replayErr := s.acceptCommitLocked(context.Background(), replayDraftCommit(drafts, prefix.through+1), nil)
			unchanged = unchanged && sameProjectionState(s.p, prefix.baseline.p)
			s.mu.Unlock()
			if valid || replayErr == nil || !unchanged || prefix.fixture.store.Durable() != prefix.through {
				t.Fatalf("rejected service group mutated native or service state: valid=%v preflight=%v replay=%v unchanged=%v", valid, err, replayErr, unchanged)
			}
			return
		}
		s.mu.Unlock()
		if !unchanged || prefix.fixture.store.Durable() != prefix.through {
			t.Fatal("service preflight installed a transition")
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prefix.prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err := store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		through := store.Durable()
		reopened, _, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if !reflect.DeepEqual(plan, reopened.p.service) || store.Durable() != through {
			t.Fatal("accepted service transition changed on inert replay")
		}
	})
}

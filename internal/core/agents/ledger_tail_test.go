package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// One ledger store has two writers: the agents Service and the
// workspace.Service that the turn loop and the host write through. The rows
// here put the real workspace producer's commit between an agents producer's
// refresh and its Submit, as the criterion-8 measurement met it; before the
// conditional Submit the producer committed a group its own reducer refused.

// tailForeign is one commit of the ledger's other writer.
type tailForeign struct {
	first, last uint64
	timeNS      int64
}

// tailBarrier is a deps.beforeSubmit seam. It runs under Service.mu, after
// the producer's refresh and guards and before its Submit, on every attempt
// whose second draft has type effect; on the first fires of them it makes
// the real workspace.Service commit a new scratch to the same store. Its
// clock is the producers' review clock, which never reads earlier than the
// latest foreign commit's event time, so a retry's sample is valid at the
// tail it is planned at without spinning on the wall clock.
type tailBarrier struct {
	effect string
	fires  int
	armed  atomic.Bool
	after  atomic.Int64

	// Set before arming.
	t         *testing.T
	service   *Service
	ws        *workspace.Service
	workspace uuid.UUID
	contexts  []uuid.UUID
	armedAt   uint64

	mu       sync.Mutex
	attempts int
	planned  [][]json.RawMessage // each attempt's payloads but its receipt's
	foreign  []tailForeign
}

func (b *tailBarrier) clock() (int64, error) {
	return max(time.Now().UTC().UnixNano(), b.after.Load()), nil
}

func (b *tailBarrier) submit(d []ledger.Draft) []ledger.Draft {
	if !b.armed.Load() || len(d) < 3 || d[1].Type != b.effect {
		return d
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.attempts++
	var planned []json.RawMessage
	for _, draft := range d[:len(d)-1] {
		planned = append(planned, draft.Payload)
	}
	b.planned = append(b.planned, planned)
	if len(b.foreign) == b.fires {
		return d
	}
	ctx := context.Background()
	before := b.service.store.Durable()
	if _, err := b.ws.Scratch(ctx, b.contexts[len(b.foreign)], b.workspace); err != nil {
		b.t.Error(err)
		return d
	}
	foreign := tailForeign{first: before + 1, last: b.service.store.Durable()}
	err := b.service.scan(ctx, before, foreign.last, func(c ledger.Commit) error {
		for _, r := range c.Records {
			foreign.timeNS = max(foreign.timeNS, r.EventTimeNS)
		}
		return nil
	})
	if err != nil || foreign.last <= before {
		b.t.Errorf("the other writer's commit is unreadable: %d-%d, %v", foreign.first, foreign.last, err)
	}
	b.foreign = append(b.foreign, foreign)
	b.after.Store(foreign.timeNS)
	return d
}

// prepare registers a context for each foreign commit and points the
// barrier at the workspace that makes them.
func (b *tailBarrier) prepare(t *testing.T, f *serviceFixture, ws *workspace.Service, registered uuid.UUID) {
	t.Helper()
	b.t, b.service, b.ws, b.workspace = t, f.service, ws, registered
	for k := range b.fires {
		b.contexts = append(b.contexts, registerPrivate(t, f.service, 700000+10*k).ContextID)
	}
}

func (b *tailBarrier) arm(through uint64) {
	b.armedAt = through
	b.armed.Store(true)
}

// landed requires that the service was never poisoned, that a fresh Open
// accepts the whole history with no pending loop, that the barrier fired as
// often as asked and saw one more attempt, and that exactly one group of the
// barrier's effect was committed since arming, after every foreign commit.
// It returns that group's effect record and the commits since arming; the
// fixture is left holding the reopened service.
func (b *tailBarrier) landed(t *testing.T, f *serviceFixture) (ledger.Record, []ledger.Commit) {
	t.Helper()
	ctx := context.Background()
	b.armed.Store(false)
	f.service.mu.Lock()
	poisoned := f.service.poisoned
	f.service.mu.Unlock()
	if poisoned {
		t.Fatal("the agents Service refused its own group after the other writer's commit")
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, report, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatalf("a fresh Open refused the history: %v", err)
	}
	f.service = reopened
	if len(report.PendingLoops) != 0 {
		t.Fatalf("a fresh Open reports %d pending loops", len(report.PendingLoops))
	}
	var commits []ledger.Commit
	if err := reopened.scan(ctx, b.armedAt, f.store.Durable(), func(c ledger.Commit) error {
		commits = append(commits, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.foreign) != b.fires || b.attempts != b.fires+1 {
		t.Fatalf("barrier made %d foreign commits over %d attempts, want %d over %d", len(b.foreign), b.attempts, b.fires, b.fires+1)
	}
	var groups []ledger.Record
	for _, c := range commits {
		for _, r := range c.Records {
			if r.Type == b.effect {
				groups = append(groups, r)
			}
		}
	}
	if len(groups) != 1 {
		t.Fatalf("%d %s records were committed, want 1", len(groups), b.effect)
	}
	if last := b.foreign[len(b.foreign)-1]; groups[0].Seq <= last.last {
		t.Fatalf("%s at %d does not follow the other writer's commit %d-%d", b.effect, groups[0].Seq, last.first, last.last)
	}
	return groups[0], commits
}

// sampled requires that the producer's first sample preceded the first
// foreign commit, which is the hazard, and that the committed sample is not
// earlier than the last foreign commit, which the reducer requires.
func (b *tailBarrier) sampled(t *testing.T, group ledger.Record, sample func(json.RawMessage) (int64, error)) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	first, err := sample(b.planned[0][1])
	if err != nil {
		t.Fatal(err)
	}
	committed, err := sample(group.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if first >= b.foreign[0].timeNS {
		t.Fatalf("the first sample %d is not before the other writer's commit at %d: nothing interleaved", first, b.foreign[0].timeNS)
	}
	if last := b.foreign[len(b.foreign)-1].timeNS; committed < last {
		t.Fatalf("committed sample %d is earlier than the other writer's commit at %d", committed, last)
	}
}

func paidReservedSample(p json.RawMessage) (int64, error) {
	v, err := ledger.DecodeRuntimePaidReserved(p)
	return v.AdmissionUTCNS, err
}

func paidIntentSample(p json.RawMessage) (int64, error) {
	v, err := ledger.DecodeRuntimePaidDispatchIntent(p)
	return v.ObservedUTCNS, err
}

func paidRouteSample(p json.RawMessage) (int64, error) {
	v, err := ledger.DecodeRuntimePaidRouteConfigured(p)
	return v.ObservedUTCNS, err
}

func reviewRequestSample(p json.RawMessage) (int64, error) {
	v, err := ledger.DecodeRuntimeReviewRequested(p)
	return v.ObservedUTCNS, err
}

// tailTurn runs one turn through the loop with the barrier as the service's
// beforeSubmit seam and review clock. With tool set, the model first calls
// file_write_v1 once in the context's dedicated scratch. It returns the turn's
// result and the commits since arming.
func tailTurn(t *testing.T, b *tailBarrier, tool bool, sample func(json.RawMessage) (int64, error)) (uuid.UUID, ledger.RuntimeTurnResult, []ledger.Commit) {
	t.Helper()
	var write atomic.Pointer[string]
	var posts atomic.Int32
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if args := write.Load(); args != nil && posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", *args), ""))
			return
		}
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Done."))
	}, func(deps *serviceDeps) {
		deps.beforeSubmit, deps.reviewClock = b.submit, b.clock
	})
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	ws, registered := privateWorkspace(t, f.serviceFixture)
	f.workspace = ws
	if tool {
		f.finishBusyFiles(t, registered, bindingOf(f.origin))
		args := finishBusyWrite(registered.ID, "tail")
		write.Store(&args)
	}
	b.prepare(t, f.serviceFixture, ws, registered.ID)
	f.enable(t)
	b.arm(f.store.Durable())
	start, err := f.view.Do(context.Background(), turnMessage(f.origin.ContextID, "Answer."))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := f.finished(t, start.TurnID)
	if result.Outcome != "finished" {
		t.Fatalf("turn ended %s/%s", result.Outcome, result.Reason)
	}
	group, commits := b.landed(t, f.serviceFixture)
	if sample != nil {
		b.sampled(t, group, sample)
	}
	return start.TurnID, result, commits
}

// tailToolEffects returns the local admissions of the turn's tools and the
// endings of that work, and the outcome of each tool.result.
func tailToolEffects(t *testing.T, commits []ledger.Commit, turn uuid.UUID) (admitted, ended int, outcomes []string) {
	t.Helper()
	work := map[uuid.UUID]bool{}
	for _, c := range commits {
		for _, r := range c.Records {
			switch r.Type {
			case ledger.TypeRuntimeWorkAdmitted:
				p := scopedDecode(t, ledger.DecodeRuntimeWorkAdmitted, r.Payload)
				if p.Kind == "local" && p.ParentTurnID == turn {
					admitted++
					work[p.WorkID] = true
				}
			case ledger.TypeRuntimeWorkEnded:
				if work[scopedDecode(t, ledger.DecodeRuntimeWorkEnded, r.Payload).WorkID] {
					ended++
				}
			case ledger.TypeRuntimeToolResult:
				outcomes = append(outcomes, scopedDecode(t, ledger.DecodeRuntimeToolResult, r.Payload).Outcome)
			}
		}
	}
	return admitted, ended, outcomes
}

// TestLedgerTailInterleave is the promoted t15 diagnostic reproduction. Each
// row lets the other writer commit inside one agents producer's
// refresh-to-Submit window, once, or three times for tool.result and
// loop.end so that the quantum runs again at the pending journal write.
// Before the conditional Submit every row poisoned the service and left a
// group that Open refuses.
func TestLedgerTailInterleave(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, effect string
		fires        int
		tool         bool
		sample       func(json.RawMessage) (int64, error)
	}{
		{"loop.bind", ledger.TypeRuntimeTurnBound, 1, false, nil},
		{"tool.begin", ledger.TypeRuntimeToolStarted, 1, true, nil},
		{"tool.result", ledger.TypeRuntimeToolResult, 3, true, nil},
		{"loop.end", ledger.TypePrivateTurnRevision, 3, false, nil},
		{"paid.admit", ledger.TypeRuntimePaidReserved, 1, false, paidReservedSample},
		{"paid.dispatch", ledger.TypeRuntimePaidDispatchIntent, 1, false, paidIntentSample},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			b := &tailBarrier{effect: row.effect, fires: row.fires}
			turn, result, commits := tailTurn(t, b, row.tool, row.sample)
			if row.tool {
				// The tool ran once, and the model saw its real result.
				admitted, ended, outcomes := tailToolEffects(t, commits, turn)
				if admitted != 1 || ended != 1 || len(outcomes) != 1 || outcomes[0] != "ok" || result.ToolDispatches != 1 {
					t.Fatalf("tool ran with %d admissions and %d endings, results %v, %d dispatches", admitted, ended, outcomes, result.ToolDispatches)
				}
			}
			if row.effect == ledger.TypePrivateTurnRevision {
				// The finish ran again after the busy and planned the same
				// loop.end: command, manifest revision and turn result;
				// only the receipt's predicted sequence follows the tail.
				b.mu.Lock()
				defer b.mu.Unlock()
				for i, planned := range b.planned[1:] {
					if !slices.EqualFunc(planned, b.planned[0], func(x, y json.RawMessage) bool { return bytes.Equal(x, y) }) {
						t.Fatalf("attempt %d planned another loop.end than the first", i+2)
					}
				}
			}
		})
	}
	t.Run("review", func(t *testing.T) {
		t.Parallel()
		b := &tailBarrier{effect: ledger.TypeRuntimeReviewRequested, fires: 1}
		f, history := openReviewFixture(t, 17)
		q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, history[17].Records[1].Payload)
		f.service.deps.beforeSubmit, f.service.deps.reviewClock = b.submit, b.clock
		ws, registered := privateWorkspace(t, f)
		b.prepare(t, f, ws, registered.ID)
		q.Binding.DeadlineUTCNS = time.Now().Add(time.Hour).UnixNano()
		b.arm(f.store.Durable())
		if _, err := f.service.RequestReview(context.Background(), reviewOwner(), ReviewRequest{CommandID: privateID(1100), RequestID: privateID(1101), Binding: q.Binding}); err != nil {
			t.Fatal(err)
		}
		group, _ := b.landed(t, f)
		b.sampled(t, group, reviewRequestSample)
	})
	t.Run("route", func(t *testing.T) {
		t.Parallel()
		b := &tailBarrier{effect: ledger.TypeRuntimePaidRouteConfigured, fires: 1}
		f := newLivePaidFixture(t)
		f.service.deps.beforeSubmit, f.service.deps.reviewClock = b.submit, b.clock
		ws, registered := privateWorkspace(t, f.serviceFixture)
		b.prepare(t, f.serviceFixture, ws, registered.ID)
		p := providerProfile(f.route.Profile)
		p.CapabilityVersion += "-tail"
		raw, err := p.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		b.arm(f.store.Durable())
		if _, err := f.service.ConfigurePaidRoute(context.Background(), bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw}); err != nil {
			t.Fatal(err)
		}
		group, _ := b.landed(t, f.serviceFixture)
		b.sampled(t, group, paidRouteSample)
	})
}

// A tail that moves on every attempt bounds the retries: a direct caller gets
// the typed busy with nothing written, and the loop runs the quantum again.
func TestLedgerTailBusy(t *testing.T) {
	t.Parallel()
	t.Run("admit_paid", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		b := &tailBarrier{effect: ledger.TypeRuntimePaidReserved, fires: 3}
		f := newLivePaidFixture(t)
		f.service.deps.beforeSubmit, f.service.deps.reviewClock = b.submit, b.clock
		ws, registered := privateWorkspace(t, f.serviceFixture)
		b.prepare(t, f.serviceFixture, ws, registered.ID)
		r := f.prepare(t)
		a := scopedBound(t, f.service, bindingOf(f.origin))
		before := f.store.Durable()
		b.arm(before)
		_, bound, err := a.AdmitPaid(ctx, r)
		b.armed.Store(false)
		var busy *BusyError
		if !errors.As(err, &busy) || busy.Reason != "ledger_tail" || bound != nil {
			t.Fatalf("AdmitPaid at a tail that always moves = %v, want BusyError{ledger_tail}", err)
		}
		b.mu.Lock()
		attempts, foreign := b.attempts, b.foreign
		b.mu.Unlock()
		if attempts != 3 {
			t.Fatalf("AdmitPaid made %d attempts, want 3", attempts)
		}
		written := uint64(0)
		for _, c := range foreign {
			written += c.last - c.first + 1
		}
		if got := f.store.Durable(); got != before+written {
			t.Fatalf("the log grew to %d; the other writer's commits end at %d", got, before+written)
		}
		f.service.mu.Lock()
		poisoned := f.service.poisoned
		match, err := f.service.findCommand(ctx, r.CommandID, 0, f.service.p.through)
		f.service.mu.Unlock()
		if poisoned || err != nil || match.found {
			t.Fatalf("busy left poisoned=%v, command found=%v, %v", poisoned, match.found, err)
		}
		// Nothing was consumed: the same preparation and command admit now.
		if _, bound, err := a.AdmitPaid(ctx, r); err != nil || bound == nil {
			t.Fatalf("the preparation is unusable after the busy: %v", err)
		}
	})
	t.Run("tool_begin", func(t *testing.T) {
		t.Parallel()
		// Three refusals in the first quantum, then the quantum runs again.
		b := &tailBarrier{effect: ledger.TypeRuntimeToolStarted, fires: 3}
		turn, result, commits := tailTurn(t, b, true, nil)
		admitted, ended, outcomes := tailToolEffects(t, commits, turn)
		if admitted != 1 || ended != 1 || len(outcomes) != 1 || outcomes[0] != "ok" || result.ToolDispatches != 1 {
			t.Fatalf("tool ran with %d admissions and %d endings, results %v, %d dispatches", admitted, ended, outcomes, result.ToolDispatches)
		}
	})
}

// A busy after the native ending parks the finish: the manifest's object read
// finds no process slot. A cancellation while it is parked must not replace
// the committed released ending, and turn.cancel answers as it does whenever
// the ending wins.
func TestTurnLoopCommittedEndingStands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	readDone := make(chan error, 1)
	var f *turnFixture
	var holder Binding
	var reader *Agent
	var read ScopedPrivateRead
	var armed, claimed atomic.Bool
	var ending atomic.Pointer[ScopedReceipt]
	f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Ended before the busy."))
	}, func(deps *serviceDeps) {
		deps.beforeRead = func(binding Binding, scope string) error {
			if binding != holder || scope != "context" || !claimed.CompareAndSwap(false, true) {
				return nil
			}
			close(entered)
			<-release
			return ErrDenied // the holder abandons its read without a receipt
		}
		deps.afterScopedCommit = func(receipt ScopedReceipt) error {
			if receipt.Operation != "turn.end" || !armed.CompareAndSwap(true, false) {
				return nil
			}
			ending.Store(&receipt)
			// The finish's next process slot is the manifest's read: let a
			// real private read hold the last one.
			f.service.mu.Lock()
			f.service.limits.MaxConcurrentWrites = f.service.preparing + 1
			f.service.mu.Unlock()
			go func() {
				_, err := reader.ReadPrivate(context.Background(), read)
				readDone <- err
			}()
			select {
			case <-entered:
			case err := <-readDone:
				t.Errorf("holder was not admitted: %v", err)
			}
			return nil
		}
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	holder = scopedWorkContext(t, f.service, bindingOf(f.origin), 63000, 63001)
	grant := uuid.NewV7()
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	if _, err := f.service.IssueGrant(ctx, bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grant, Grantee: holder, Operations: []string{"private.read", "private.write"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(holder), Scope: "context", Slots: []string{"memory"}}}, DataClass: "private", Recipients: []Binding{holder}, Limits: capacity, Lifetime: "until_revoked"}}); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.Snapshot(ctx, ReadBinding{Binding: holder, Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	reader = scopedBound(t, f.service, holder)
	written, err := reader.WritePrivate(ctx, ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: grant, Scope: "context", ExpectedVersion: view.Revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("Synthetic held body.")}}})
	if err != nil {
		t.Fatal(err)
	}
	read = ScopedPrivateRead{CommandID: uuid.NewV7(), GrantID: grant, Scope: "context", Slot: "memory", Revision: uint64(written.Version), MaxBytes: min(f.service.limits.MaxReadBytes, 4<<20)}
	f.enable(t)
	armed.Store(true)
	start, err := f.view.Do(ctx, turnMessage(f.origin.ContextID, "Answer."))
	if err != nil {
		t.Fatal(err)
	}
	turn := start.TurnID
	finishBusyState(t, f.view, f.origin.ContextID, turn, "waiting_resource", "")
	cancelled := make(chan error, 1)
	go func() {
		_, err := f.view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}})
		cancelled <- err
	}()
	// The cancellation reruns the finish once; it keeps the ending and parks.
	finishBusyState(t, f.view, f.origin.ContextID, turn, "waiting_resource", "cancelled")
	close(release)
	if err := <-readDone; !errors.Is(err, ErrDenied) {
		t.Fatalf("holder did not abandon its read: %v", err)
	}
	if err := <-cancelled; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("turn.cancel after the committed ending = %v, want ErrUnavailable", err)
	}
	result, _ := f.finished(t, turn)
	if result.Outcome != "finished" || result.Reason != "natural_stop" || result.FinalMessageObject == nil {
		t.Fatalf("turn ended %s/%s, final %v; want the committed released ending", result.Outcome, result.Reason, result.FinalMessageObject)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, report, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	f.service = reopened
	if len(report.PendingLoops) != 0 {
		t.Fatalf("fresh Open reports %d pending loops", len(report.PendingLoops))
	}
	var ends []ledger.LoopEndArguments
	cancels := 0
	if err := reopened.scan(ctx, 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, r := range c.Records {
			switch r.Type {
			case ledger.TypeRuntimeLoopReceipt:
				receipt := scopedDecode(t, ledger.DecodeRuntimeLoopReceipt, r.Payload)
				if end, ok := receipt.Submission.Arguments.(ledger.LoopEndArguments); ok && receipt.Submission.TurnID == turn {
					ends = append(ends, end)
				}
			case ledger.TypeRuntimeWorkEnded:
				if scopedDecode(t, ledger.DecodeRuntimeWorkEnded, r.Payload).Outcome == "cancelled" {
					cancels++
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	committed := ending.Load()
	if committed == nil || len(ends) != 1 || ends[0].EndingCommandID != committed.CommandID || ends[0].Outcome != "finished" || cancels != 0 {
		t.Fatalf("loop.end records %+v for the ending %+v, %d cancelled work endings", ends, committed, cancels)
	}
}

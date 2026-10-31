package agents

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// finishBusyWorkspace replaces the fixture's workspace with a registered
// repository whose capture budget admits one capture at a time.
func (f *turnFixture) finishBusyWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	registeredService, registered := privateWorkspace(t, f.serviceFixture)
	if err := registeredService.Close(); err != nil {
		t.Fatal(err)
	}
	ws, _, err := workspace.Open(context.Background(), f.store, f.root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = ws
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { _ = f.service.Close() })
	return registered
}

// finishBusyFiles gives a context its dedicated scratch and a file grant at
// the safe prefix, returning the scratch.
func (f *turnFixture) finishBusyFiles(t *testing.T, registered workspace.Workspace, binding Binding) workspace.Scratch {
	t.Helper()
	scratch, err := f.workspace.Scratch(context.Background(), binding.ContextID, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(scratch.Dir, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	capacity := paidPhysicalCapacity()
	capacity.MaxOperationBytes = 4 << 20
	_, err = f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: uuid.NewV7(), Grantee: binding, Operations: []string{"file.read", "file.search", "file.write", "file.edit"}, Resources: []ledger.Resource{{Kind: "workspace", Binding: ledger.Binding(binding), WorkspaceID: registered.ID, PathPrefix: "safe"}}, DataClass: "workspace", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	return scratch
}

func finishBusyWrite(workspaceID uuid.UUID, label string) string {
	return fmt.Sprintf(`{"schema":1,"workspace_id":%q,"path":"safe/%s.txt","expected_sha256":"absent","text":%q}`, workspaceID, label, "synthetic finish write "+label)
}

// finishBusyStatus fails when the turn has left the scheduler without its
// recorded ending, which is how a stranded finish appears to its owner.
func finishBusyStatus(t *testing.T, view session.Session, contextID, turn uuid.UUID) session.TurnView {
	t.Helper()
	result, err := view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: contextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: turn}})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.Data.(session.TurnView)
	if !ok {
		t.Fatal("turn status type unavailable")
	}
	if status.State == "recovery_pending" {
		t.Fatalf("turn %s left the scheduler without a recorded ending", turn)
	}
	return status
}

// finishGate holds the stranding row's turn-end commit attempts: each turn's
// first attempt waits for its round, and every retry waits to be released
// unless the gate is free.
type finishGate struct {
	mu       sync.Mutex
	free     bool // retries proceed at once
	attempts map[uuid.UUID]int
	held     map[uuid.UUID]chan struct{}
	round    chan struct{}
	arrival  chan struct{} // closed and replaced at every attempt
	stop     chan struct{}
}

func (g *finishGate) attempt(turn uuid.UUID) error {
	g.mu.Lock()
	g.attempts[turn]++
	wait := g.round
	if g.attempts[turn] > 1 {
		wait = make(chan struct{})
		if g.free {
			close(wait)
		} else {
			g.held[turn] = wait
		}
	}
	close(g.arrival)
	g.arrival = make(chan struct{})
	g.mu.Unlock()
	select {
	case <-wait:
	case <-g.stop:
	}
	return nil
}

// arrived waits until every turn of the round has made its first attempt.
func (g *finishGate) arrived(round map[uuid.UUID]uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		missing := false
		for turn := range round {
			missing = missing || g.attempts[turn] == 0
		}
		if !missing {
			return
		}
		next := g.arrival
		g.mu.Unlock()
		<-next
		g.mu.Lock()
	}
}

// results waits on the owner's view for every turn's turn_result. A held
// retry is released only while every unfinished turn is held, one at a time,
// so no retried observation lands inside another turn's loop.end submission.
func (g *finishGate) results(t *testing.T, view session.Session, cursor uint64, round map[uuid.UUID]uuid.UUID) map[uuid.UUID]ledger.RuntimeTurnResult {
	t.Helper()
	results := map[uuid.UUID]ledger.RuntimeTurnResult{}
	for len(results) < len(round) {
		page, err := view.Since(context.Background(), cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Events {
			if result, ok := event.Data.(ledger.RuntimeTurnResult); ok {
				if _, mine := round[event.TurnID]; mine {
					results[event.TurnID] = result
				}
			}
		}
		if page.ThroughSequence != cursor {
			cursor = page.ThroughSequence
			continue
		}
		g.mu.Lock()
		var next chan struct{}
		if len(g.held) != 0 && len(g.held) == len(round)-len(results) {
			for turn, release := range g.held {
				next = release
				delete(g.held, turn)
				break
			}
		}
		arrival := g.arrival
		g.mu.Unlock()
		if next != nil {
			close(next)
			continue
		}
		for turn, contextID := range round {
			if _, ended := results[turn]; !ended {
				finishBusyStatus(t, view, contextID, turn)
			}
		}
		select {
		case <-page.Wake:
		case <-arrival:
		}
	}
	return results
}

// finishBusyState waits until the live turn shows state and reason.
func finishBusyState(t *testing.T, view session.Session, contextID, turn uuid.UUID, state, reason string) {
	t.Helper()
	var cursor uint64
	for {
		page, err := view.Since(context.Background(), cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		status := finishBusyStatus(t, view, contextID, turn)
		if status.Result != nil {
			t.Fatalf("turn ended %s/%s instead of parking", status.State, status.Reason)
		}
		if status.State == state && status.Reason == reason {
			return
		}
		cursor = page.ThroughSequence
		<-page.Wake
	}
}

// finishBusyLedger counts loop.end receipts per turn and observations per
// scratch through a fresh Open, which must report no pending loop.
func finishBusyLedger(t *testing.T, f *turnFixture) (map[uuid.UUID]int, map[uuid.UUID]int) {
	t.Helper()
	ctx := context.Background()
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
	ended, observed := map[uuid.UUID]int{}, map[uuid.UUID]int{}
	reopened.mu.Lock()
	defer reopened.mu.Unlock()
	err = reopened.scan(ctx, 0, reopened.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			switch record.Type {
			case ledger.TypeRuntimeLoopReceipt:
				receipt, err := ledger.DecodeRuntimeLoopReceipt(record.Payload)
				if err != nil {
					return err
				}
				if receipt.Submission.Operation == "loop.end" {
					ended[receipt.Submission.TurnID]++
				}
			case ledger.TypeScratchChanged:
				change, err := ledger.DecodeScratchChanged(record.Payload)
				if err != nil {
					return err
				}
				observed[change.ScratchID]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ended, observed
}

func TestTurnLoopFinishBusy(t *testing.T) {
	t.Parallel()
	t.Run("concurrent_captures_park_not_strand", func(t *testing.T) { testLoopFinishBusyStranding(t, true) })
	t.Run("concurrent_captures_and_ledger_writes", func(t *testing.T) { testLoopFinishBusyStranding(t, false) })
	t.Run("cancel_while_parked", testLoopFinishBusyCancel)
}

// Three turns with file writes are held before their turn-end observation and
// released together into a workspace that admits one capture at a time. Real
// captures collide; a collision cannot be scheduled from this package, so up
// to eight rounds run until one does, and every round must end every turn.
// Gated, retries are held and released one at a time (finishGate.results),
// which isolates the capture busy from the ledger's two writers. Ungated,
// every retry proceeds at once, so the turns' captures, native endings and
// loop.end writes overlap freely; the conditional Submit keeps every group
// valid where it lands.
func testLoopFinishBusyStranding(t *testing.T, gated bool) {
	t.Parallel()
	const maxRounds, turns = 8, 3
	var mu sync.Mutex
	labels := map[string]string{}
	posts := map[string]int{}
	gate := &finishGate{free: !gated, attempts: map[uuid.UUID]int{}, held: map[uuid.UUID]chan struct{}{}, arrival: make(chan struct{}), stop: make(chan struct{})}
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		label, arguments := "", ""
		for candidate, args := range labels {
			if bytes.Contains(raw, []byte(candidate)) {
				label, arguments = candidate, args
			}
		}
		posts[label]++
		first := posts[label] == 1
		mu.Unlock()
		switch {
		case label == "":
			t.Error("model request without a current round label")
		case first:
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", arguments), ""))
		default:
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Finish-busy answer."))
		}
	}, func(deps *serviceDeps) {
		// Only a busy re-enters finish, so a second attempt for a turn is the
		// observed busy.
		deps.beforeFinishCommit = gate.attempt
	})
	registered := f.finishBusyWorkspace(t)
	// Registered after the fixture's closes, so it runs first: a failed row
	// must not leave a finish waiting in the gate while Close drains.
	t.Cleanup(func() { close(gate.stop) })
	bindings := []Binding{bindingOf(f.origin)}
	var settings []LoopContextSettings
	for i := 1; i < turns; i++ {
		setting := f.workSetting(t, 52000+10*i, fmt.Sprintf("Finish work %d", i))
		settings = append(settings, setting)
		bindings = append(bindings, setting.Binding)
	}
	scratches := map[uuid.UUID]uuid.UUID{}
	for _, binding := range bindings {
		scratches[binding.ContextID] = f.finishBusyFiles(t, registered, binding).ID
	}
	f.enable(t, settings...)
	ctx := context.Background()
	var all []uuid.UUID
	busy, rounds := 0, 0
	for rounds < maxRounds && busy == 0 {
		mu.Lock()
		clear(labels)
		for k := range bindings {
			label := fmt.Sprintf("finish-busy-%d-%d-end", rounds, k)
			labels[label] = finishBusyWrite(registered.ID, label)
		}
		mu.Unlock()
		gate.mu.Lock()
		gate.round = make(chan struct{})
		release := gate.round
		gate.mu.Unlock()
		cursor := f.store.Durable()
		round := map[uuid.UUID]uuid.UUID{}
		for k, binding := range bindings {
			start, err := f.view.Do(ctx, turnMessage(binding.ContextID, fmt.Sprintf("finish-busy-%d-%d-end", rounds, k)))
			if err != nil {
				t.Fatal(err)
			}
			round[start.TurnID] = binding.ContextID
			all = append(all, start.TurnID)
		}
		gate.arrived(round)
		close(release)
		for turn, result := range gate.results(t, f.view, cursor, round) {
			if result.Outcome != "finished" || result.ToolDispatches != 1 {
				t.Fatalf("turn %s ended %s/%s with %d tools", turn, result.Outcome, result.Reason, result.ToolDispatches)
			}
		}
		gate.mu.Lock()
		for turn := range round {
			if gate.attempts[turn] > 1 {
				busy++
			}
		}
		gate.mu.Unlock()
		rounds++
	}
	t.Logf("rounds=%d busy finishes=%d", rounds, busy)
	if busy == 0 {
		t.Fatal("no capture collision observed")
	}
	ended, observed := finishBusyLedger(t, f)
	for _, turn := range all {
		if ended[turn] != 1 {
			t.Fatalf("turn %s has %d loop.end receipts", turn, ended[turn])
		}
	}
	for _, scratch := range scratches {
		if observed[scratch] != rounds {
			t.Fatalf("scratch has %d observations over %d rounds", observed[scratch], rounds)
		}
	}
}

// The test answers a real workspace busy for the turn's observation while it
// holds the budget, cancels the parked turn, then stops answering and releases
// a real writer lease on another scratch: a release with no ledger commit, so
// only the workspace release notifier can wake the turn.
func testLoopFinishBusyCancel(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	holding, answered, passed := true, 0, 0
	answer := make(chan struct{})
	var posts atomic.Int32
	var arguments string
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Answer the cancellation replaces."))
		}
	}, func(deps *serviceDeps) {
		deps.beforeFinishCommit = func(uuid.UUID) error {
			mu.Lock()
			defer mu.Unlock()
			if !holding {
				passed++
				return nil
			}
			answered++
			close(answer)
			answer = make(chan struct{})
			return &workspace.BusyError{Reason: "capture_budget"}
		}
	})
	// waitAnswers returns once more than n busy answers have been given.
	waitAnswers := func(n int) int {
		mu.Lock()
		for answered <= n {
			next := answer
			mu.Unlock()
			<-next
			mu.Lock()
		}
		defer mu.Unlock()
		return answered
	}
	registered := f.finishBusyWorkspace(t)
	// Runs before the fixture's Close: a failed row must not leave the turn
	// parked on an answer that never stops.
	t.Cleanup(func() {
		mu.Lock()
		holding = false
		mu.Unlock()
	})
	scratch := f.finishBusyFiles(t, registered, bindingOf(f.origin))
	arguments = finishBusyWrite(registered.ID, "cancel-while-parked")
	other := scopedWorkContext(t, f.service, bindingOf(f.origin), 53000, 53001)
	otherScratch, err := f.workspace.Scratch(context.Background(), other.ContextID, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := f.workspace.BeginWrite(context.Background(), workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: other.ContextID, AgentID: other.AgentID, TaskAgentID: other.AgentID, TaskScratchID: otherScratch.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	releaseLease, err := holder.Lease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLease()
	f.enable(t)
	_, start := f.send(t)
	finishBusyState(t, f.view, f.origin.ContextID, start.TurnID, "waiting_resource", "")
	parked := waitAnswers(0)
	cancelled := make(chan loopCancelReply, 1)
	command := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: start.TurnID}}
	go func() {
		result, err := f.view.Do(context.Background(), command)
		cancelled <- loopCancelReply{command, result, err}
	}()
	waitAnswers(parked)
	finishBusyState(t, f.view, f.origin.ContextID, start.TurnID, "waiting_resource", "cancelled")
	mu.Lock()
	spun := answered != parked+1
	holding = false
	mu.Unlock()
	if spun {
		t.Fatal("cancellation did not rerun the parked finish exactly once")
	}
	select {
	case <-cancelled:
		t.Fatal("cancellation acknowledged before the capture budget was released")
	default:
	}
	releaseLease()
	reply := <-cancelled
	if reply.err != nil || reply.result.Outcome != "applied" {
		t.Fatalf("cancellation did not acknowledge its recorded ending: %v", reply.err)
	}
	f.service.mu.Lock()
	effect, effectFound, effectErr := f.service.sourceRecord(context.Background(), int64(reply.result.EffectSequence), f.service.p.through)
	receipt, receiptFound, receiptErr := f.service.sourceRecord(context.Background(), int64(reply.result.ReceiptSequence), f.service.p.through)
	f.service.mu.Unlock()
	if effectErr != nil || receiptErr != nil || !effectFound || !receiptFound || effect.Type != ledger.TypeRuntimeWorkEnded || receipt.Type != ledger.TypeRuntimeScopedReceipt {
		t.Fatal("cancellation acknowledgement did not name CancelTurn's effect and receipt")
	}
	native, err := ledger.DecodeRuntimeWorkEnded(effect.Payload)
	if err != nil || native.WorkID != start.TurnID || native.Outcome != "cancelled" || effect.OperationID != command.CommandID {
		t.Fatal("native ending is not the turn's cancellation")
	}
	result, _ := f.finished(t, start.TurnID)
	if result.Outcome != "cancelled" || result.Reason != "cancelled" || result.ToolDispatches != 1 {
		t.Fatalf("parked turn ended %s/%s", result.Outcome, result.Reason)
	}
	mu.Lock()
	commits := passed
	mu.Unlock()
	ended, observed := finishBusyLedger(t, f)
	if commits != 1 || observed[scratch.ID] != 1 || ended[start.TurnID] != 1 {
		t.Fatalf("writer commits=%d observations=%d loop.end=%d", commits, observed[scratch.ID], ended[start.TurnID])
	}
}

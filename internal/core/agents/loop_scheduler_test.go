package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func (f *turnFixture) contextSetting(t *testing.T, binding Binding) LoopContextSettings {
	t.Helper()
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	scope := ledger.ScopeKey{Kind: "agent", ID: binding.AgentID}
	_, err := f.service.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: capacity})
	if err != nil {
		t.Fatal(err)
	}
	model := uuid.NewV7()
	for _, spec := range []GrantSpec{
		{GrantID: model, Grantee: binding, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
		{GrantID: uuid.NewV7(), Grantee: binding, Operations: []string{"context.inspect", "private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "agent", Slots: []string{"instructions", "memory"}}, {Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
	} {
		_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []ledger.PaidScope{{Kind: "agent", ID: binding.AgentID}, {Kind: "grant", ID: model}} {
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
	}
	setting := f.setting
	setting.Binding = binding
	return setting
}
func (f *turnFixture) workSetting(t *testing.T, id int, title string) LoopContextSettings {
	t.Helper()
	binding := scopedWorkContext(t, f.service, bindingOf(f.origin), id, id+1)
	setting := f.contextSetting(t, binding)
	task, assignment := uuid.NewV7(), uuid.NewV7()
	_, err := f.service.CreateTask(context.Background(), bindingOf(f.origin), TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("private task payload excluded from other-work notes"), AcceptanceSHA256: sha256.Sum256([]byte("synthetic acceptance")), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{binding.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(binding)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.TransitionTask(context.Background(), bindingOf(f.origin), TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "start"})
	if err != nil {
		t.Fatal(err)
	}
	setting.DisplayTitle, setting.TaskBinding = title, &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
	return setting
}
func turnMessage(contextID uuid.UUID, text string) session.Command {
	return session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: contextID, Operation: "message.send", Arguments: session.MessageSend{TurnID: uuid.NewV7(), Text: text}}
}
func waitLoopState(t *testing.T, view session.Session, contextID, turn uuid.UUID, state string) {
	t.Helper()
	ctx := context.Background()
	var cursor uint64
	for {
		page, err := view.Since(ctx, cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		result, err := view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: contextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: turn}})
		if err != nil {
			t.Fatal(err)
		}
		status, ok := result.Data.(session.TurnView)
		if !ok {
			t.Fatal("turn status type unavailable")
		}
		if status.State == state {
			return
		}
		if status.Result != nil {
			t.Fatalf("turn ended instead of entering %s: %s", state, status.Reason)
		}
		cursor = page.ThroughSequence
		<-page.Wake
	}
}

func TestTurnLoopAdmissionAndFairness(t *testing.T) {
	t.Parallel()
	t.Run("physical_release_without_commit", func(t *testing.T) { testLoopPhysicalRelease(t, false) })
	gates := map[string]chan struct{}{"schedule-general": make(chan struct{}), "schedule-first": make(chan struct{}), "schedule-second": make(chan struct{}), "schedule-peer": make(chan struct{})}
	type request struct {
		label string
		raw   []byte
	}
	entered := make(chan request, 8)
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		label := ""
		for candidate := range gates {
			if bytes.Contains(raw, []byte(candidate)) {
				label = candidate
				break
			}
		}
		if label == "" {
			t.Error("unexpected retained/busy input reached provider")
			return
		}
		// Headers go out before the gate so a held exchange waits on its body,
		// never on the transport's response-header deadline.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		entered <- request{label, raw}
		select {
		case <-gates[label]:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Bound scheduler answer."))
	}, nil)
	t.Cleanup(func() {
		for _, gate := range gates {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	})
	capacity := paidPhysicalCapacity()
	capacity.TurnSlots, capacity.BufferBytes, capacity.MaxOperationBytes = 10, 1<<30, 4<<20
	scope := ledger.ScopeKey{Kind: "colony"}
	_, err := f.service.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: capacity})
	if err != nil {
		t.Fatal(err)
	}
	first := f.workSetting(t, 51000, "First task")
	second := f.workSetting(t, 51010, "Second task")
	fourth := f.workSetting(t, 51020, "Fourth task")
	peerBinding := registerPrivate(t, f.service, 61000)
	peer := f.contextSetting(t, peerBinding)
	f.enable(t, first, second, fourth, peer)
	peerView, err := f.service.OpenSession(context.Background(), peerBinding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peerView.Close)
	ctx := context.Background()
	receive := func() request { return <-entered }
	generalCommand := turnMessage(f.origin.ContextID, "schedule-general")
	general, err := f.view.Do(ctx, generalCommand)
	if err != nil {
		t.Fatal(err)
	}
	if receive().label != "schedule-general" {
		t.Fatal("wrong first execution")
	}
	retry, err := f.view.Do(ctx, generalCommand)
	if err != nil || retry.ReceiptSequence != general.ReceiptSequence {
		t.Fatal("same-ID start did not return its actual binding receipt")
	}
	sameContext := turnMessage(f.origin.ContextID, "rejected-same-context")
	_, err = f.view.Do(ctx, sameContext)
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatal("one-context admission retained a second active turn")
	}
	one, err := f.view.Do(ctx, turnMessage(first.Binding.ContextID, "schedule-first"))
	if err != nil {
		t.Fatal(err)
	}
	if receive().label != "schedule-first" {
		t.Fatal("wrong first work execution")
	}
	two, err := f.view.Do(ctx, turnMessage(second.Binding.ContextID, "schedule-second"))
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := receive()
	if secondRequest.label != "schedule-second" {
		t.Fatal("wrong second work execution")
	}
	if !bytes.Contains(secondRequest.raw, []byte("First task")) || !bytes.Contains(secondRequest.raw, []byte("General work")) || bytes.Contains(secondRequest.raw, []byte("private task payload")) {
		t.Fatal("other-work note omitted the admitted titles or disclosed task content")
	}
	fourthCommand := turnMessage(fourth.Binding.ContextID, "rejected-fourth-context")
	_, err = f.view.Do(ctx, fourthCommand)
	if !errors.As(err, &busy) {
		t.Fatal("fourth agent turn was admitted or queued instead of busy")
	}
	other, err := peerView.Do(ctx, turnMessage(peerBinding.ContextID, "schedule-peer"))
	if err != nil {
		t.Fatal(err)
	}
	waitLoopState(t, peerView, peerBinding.ContextID, other.TurnID, "waiting_resource")
	f.service.mu.Lock()
	err = f.service.scan(ctx, 0, f.service.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.TurnID == other.TurnID && record.Type == ledger.TypeRuntimePaidReserved {
				t.Error("waiting exchange acquired a monetary hold")
			}
		}
		return nil
	})
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	close(gates["schedule-first"])
	otherRequest := receive()
	if otherRequest.label != "schedule-peer" {
		t.Fatal("resource release did not wake the waiting agent")
	}
	if bytes.Contains(otherRequest.raw, []byte("First task")) || bytes.Contains(otherRequest.raw, []byte("Second task")) || bytes.Contains(otherRequest.raw, []byte("private task payload")) {
		t.Fatal("other-work note crossed agent boundaries")
	}
	close(gates["schedule-peer"])
	result, peerEvents := f.finishedFor(t, peerView, other.TurnID)
	if result.Outcome != "finished" {
		t.Fatal("unblocked peer did not finish")
	}
	for _, event := range peerEvents {
		if event.AgentID != peerBinding.AgentID || event.ContextID != peerBinding.ContextID || event.TurnID != other.TurnID {
			t.Fatal("peer event inherited another view's binding")
		}
	}
	// The original agent's other two exchanges remain blocked while its peer
	// finishes, so this is execution fairness rather than a draining barrier.
	close(gates["schedule-general"])
	close(gates["schedule-second"])
	for _, turn := range []uuid.UUID{general.TurnID, one.TurnID, two.TurnID} {
		result, _ := f.finished(t, turn)
		if result.Outcome != "finished" {
			t.Fatalf("admitted context did not finish: outcome=%s reason=%s models=%d", result.Outcome, result.Reason, result.ModelCalls)
		}
	}
	f.service.mu.Lock()
	err = f.service.scan(ctx, 0, f.service.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeTurnBound && (record.TurnID == sameContext.Arguments.(session.MessageSend).TurnID || record.TurnID == fourthCommand.Arguments.(session.MessageSend).TurnID) {
				t.Error("busy input retained a latent start")
			}
		}
		return nil
	})
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

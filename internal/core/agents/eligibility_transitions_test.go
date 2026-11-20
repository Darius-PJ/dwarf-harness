package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func holdEligibility(t testing.TB, s *Service, agent uuid.UUID) EligibilityReceipt {
	t.Helper()
	view, err := s.EligibilityStatus(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	op := view.OperationID
	if view.State == "eligible" {
		op = uuid.UUID{}
	}
	r, err := s.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: uuid.NewV7(), AgentID: agent, ExpectedVersion: view.Version, ExpectedOperationID: op, Reason: "owner_hold"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func releaseEligibility(t testing.TB, s *Service, agent uuid.UUID, held EligibilityReceipt) EligibilityReceipt {
	t.Helper()
	r, err := s.ReleaseEligibility(context.Background(), EligibilityRelease{CommandID: uuid.NewV7(), AgentID: agent, ExpectedVersion: held.Version, ExpectedOperationID: held.OperationID})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func requireEligibilityHeld(t testing.TB, err error) *EligibilityHeldError {
	t.Helper()
	var held *EligibilityHeldError
	var busy *BusyError
	if !errors.As(err, &held) || !errors.Is(err, session.ErrEligibilityHeld) || errors.As(err, &busy) || errors.Is(err, ErrDenied) {
		t.Fatalf("not a distinct held refusal: %v", err)
	}
	return held
}

func TestEligibilityTransitions(t *testing.T) {
	t.Parallel()
	t.Run("declaring_command_cannot_consume_first_identity", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("colliding declaration dispatched") }, nil)
		f.enable(t)
		command := turnMessage(f.origin.ContextID, "first self collision")
		request := firstActionRequest(f.origin, command, nil)
		for _, field := range []string{"start", "admission"} {
			t.Run("require_first_"+field, func(t *testing.T) {
				candidate := request
				if field == "start" {
					candidate.FirstActionTurn.StartCommandID = candidate.CommandID
				} else {
					candidate.FirstActionTurn.AdmissionCommandID = candidate.CommandID
				}
				before := f.store.Durable()
				if _, err := f.service.RequireFirstAction(ctx, candidate); !errors.Is(err, ErrEligibilityStale) || f.store.Durable() != before {
					t.Fatal("require_first consumed its own reserved command identity", field, err)
				}
			})
		}
		requireFirstAction(t, f.service, request)
		revokeFirstGrant(t, f, f.privateGrant)
		if _, err := f.view.Do(ctx, command); err != ErrDenied {
			t.Fatal(err)
		}
		view := firstActionStatus(t, f.service, f.origin.AgentID)
		next := EligibilityFirstNext{CommandID: uuid.NewV7(), AgentID: f.origin.AgentID, ExpectedVersion: view.Version, ExpectedOperationID: view.OperationID, ActionID: request.ActionID, FirstActionTurn: firstActionRequest(f.origin, turnMessage(f.origin.ContextID, "next self collision"), nil).FirstActionTurn}
		for _, field := range []string{"start", "admission"} {
			t.Run("first_next_"+field, func(t *testing.T) {
				candidate := next
				if field == "start" {
					candidate.FirstActionTurn.StartCommandID = candidate.CommandID
				} else {
					candidate.FirstActionTurn.AdmissionCommandID = candidate.CommandID
				}
				before := f.store.Durable()
				if _, err := f.service.DeclareNextFirstAction(ctx, candidate); !errors.Is(err, ErrEligibilityStale) || f.store.Durable() != before {
					t.Fatal("first_next consumed its own reserved command identity", field, err)
				}
			})
		}
	})
	t.Run("first_terminal_next_and_explicit_release", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "completed"))
		}, nil)
		f.enable(t)
		command := turnMessage(f.origin.ContextID, "initial input")
		request := firstActionRequest(f.origin, command, nil)
		receipt := requireFirstAction(t, f.service, request)
		nextCommand := turnMessage(f.origin.ContextID, "next input")
		nextTurn := firstActionRequest(f.origin, nextCommand, nil).FirstActionTurn
		next := EligibilityFirstNext{CommandID: uuid.NewV7(), AgentID: f.origin.AgentID, ExpectedVersion: receipt.Version, ExpectedOperationID: receipt.OperationID, ActionID: request.ActionID, FirstActionTurn: nextTurn}
		if _, err := f.service.DeclareNextFirstAction(ctx, next); !errors.Is(err, ErrEligibilityStale) {
			t.Fatal("first_next before native ending was permitted", err)
		}
		if _, err := f.view.Do(ctx, command); err != nil {
			t.Fatal(err)
		}
		f.finished(t, request.FirstActionTurn.TurnID)
		terminal := firstActionStatus(t, f.service, f.origin.AgentID)
		if terminal.State != "first_action_required" || terminal.FirstAction.Disposition != "terminal" || terminal.FirstAction.NativeEndingSequence == 0 || terminal.FirstAction.FirstAdmittedTurn == nil {
			t.Fatal("completed first turn did not retain required state and sources", terminal)
		}
		first := *terminal.FirstAction.FirstAdmittedTurn
		applied, err := f.service.DeclareNextFirstAction(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
		declared := firstActionStatus(t, f.service, f.origin.AgentID)
		if declared.FirstAction.Disposition != "not_admitted" || declared.FirstAction.Declaration.InitialTurn != terminal.FirstAction.Declaration.InitialTurn || *declared.FirstAction.FirstAdmittedTurn != first || declared.FirstAction.NativeSequence != 0 || declared.FirstAction.BoundSequence != 0 || declared.FirstAction.Declaration.Refusal != nil {
			t.Fatal("next lost initial/first-admitted identity or retained current sources", declared)
		}
		before := f.store.Durable()
		retry, err := f.service.DeclareNextFirstAction(ctx, next)
		if err != nil || retry != applied || f.store.Durable() != before {
			t.Fatal("same-ID next appended", err)
		}
		if _, err := f.view.Do(ctx, nextCommand); err != nil {
			t.Fatal(err)
		}
		f.finished(t, nextTurn.TurnID)
		final := firstActionStatus(t, f.service, f.origin.AgentID)
		if *final.FirstAction.FirstAdmittedTurn != first || final.State != "first_action_required" {
			t.Fatal("second completion replaced first admitted identity or released gate")
		}
		ordinary := turnMessage(f.origin.ContextID, "explicit ordinary retry")
		_, err = f.view.Do(ctx, ordinary)
		requireEligibilityHeld(t, err)
		releaseEligibility(t, f.service, f.origin.AgentID, applied)
		if _, err := f.view.Do(ctx, ordinary); err != nil {
			t.Fatal("explicit post-release retry failed", err)
		}
		f.finished(t, ordinary.Arguments.(session.MessageSend).TurnID)
	})
	t.Run("first_next_cold_command_lookup_and_corrupt_directory_fallback", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("refused first posted") }, nil)
		f.enable(t)
		initial := turnMessage(f.origin.ContextID, "initial refused input")
		request := firstActionRequest(f.origin, initial, nil)
		requireFirstAction(t, f.service, request)
		revokeFirstGrant(t, f, f.privateGrant)
		if _, err := f.view.Do(ctx, initial); err != ErrDenied {
			t.Fatal(err)
		}
		observed := firstActionStatus(t, f.service, f.origin.AgentID)
		command := turnMessage(f.origin.ContextID, "second refused input")
		next := EligibilityFirstNext{CommandID: uuid.NewV7(), AgentID: f.origin.AgentID, ExpectedVersion: observed.Version, ExpectedOperationID: observed.OperationID, ActionID: request.ActionID, FirstActionTurn: firstActionRequest(f.origin, command, nil).FirstActionTurn}
		if _, err := f.service.DeclareNextFirstAction(ctx, next); err != nil {
			t.Fatal(err)
		}
		cleared := firstActionStatus(t, f.service, f.origin.AgentID)
		if cleared.FirstAction.Disposition != "not_admitted" || cleared.FirstAction.Declaration.Refusal != nil || cleared.FirstAction.FirstAdmittedTurn != nil {
			t.Fatal("next retained refusal or invented native admission", cleared)
		}
		if _, err := f.view.Do(ctx, command); err != ErrDenied {
			t.Fatal(err)
		}
		observed = firstActionStatus(t, f.service, f.origin.AgentID)
		next.ExpectedVersion = observed.Version
		before := f.store.Durable()
		for _, mode := range []string{"warm", "cold", "generation", "row", "boundary"} {
			if mode == "cold" {
				f.service.mu.Lock()
				f.service.history = newHistoryIndex()
				f.service.mu.Unlock()
			} else if mode != "warm" {
				damageCommandDirectory(f.service, mode)
			}
			for _, field := range []string{"turn", "start", "admission"} {
				candidate := next
				candidate.CommandID = uuid.NewV7()
				candidate.FirstActionTurn = firstActionRequest(f.origin, turnMessage(f.origin.ContextID, "fresh input"), nil).FirstActionTurn
				switch field {
				case "turn":
					candidate.FirstActionTurn.TurnID = request.FirstActionTurn.TurnID
					candidate.FirstActionTurn.Input.TurnID = request.FirstActionTurn.TurnID
				case "start":
					candidate.FirstActionTurn.StartCommandID = request.FirstActionTurn.AdmissionCommandID
				case "admission":
					candidate.FirstActionTurn.AdmissionCommandID = request.FirstActionTurn.StartCommandID
				}
				if _, err := f.service.DeclareNextFirstAction(ctx, candidate); !errors.Is(err, ErrEligibilityStale) || f.store.Durable() != before {
					t.Fatal("prior never-admitted declaration identity reused", mode, field, err)
				}
			}
		}
		next.CommandID = uuid.NewV7()
		next.FirstActionTurn = firstActionRequest(f.origin, turnMessage(f.origin.ContextID, "fresh third input"), nil).FirstActionTurn
		damageCommandDirectory(f.service, "row")
		if _, err := f.service.DeclareNextFirstAction(ctx, next); err != nil {
			t.Fatal("corrupt directory rejected genuinely fresh next", err)
		}
		reopenScoped(t, f.serviceFixture, DefaultLimits())
		if _, err := f.service.DeclareNextFirstAction(ctx, next); err != nil {
			t.Fatal("replayed next disagreed with authoritative preflight", err)
		}
	})
	t.Run("first_task_binding_readiness_and_start", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("task start posted") }, nil)
		binding := scopedWorkContext(t, f.service, bindingOf(f.origin), 53000, 53001)
		setting := f.contextSetting(t, binding)
		task, assignment := uuid.NewV7(), uuid.NewV7()
		if _, err := f.service.CreateTask(ctx, bindingOf(f.origin), TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("first task"), AcceptanceSHA256: sha256.Sum256([]byte("done")), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{binding.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(binding)}}); err != nil {
			t.Fatal(err)
		}
		setting.TaskBinding = &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
		if _, err := f.service.IssueGrant(ctx, bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: uuid.NewV7(), Grantee: binding, Operations: []string{"task.start"}, Resources: []ledger.Resource{{Kind: "task", TaskID: task}}, DataClass: "task", Recipients: []Binding{binding}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked", TaskBinding: setting.TaskBinding}}); err != nil {
			t.Fatal(err)
		}
		origin := f.origin
		origin.ContextID = binding.ContextID
		request := firstActionRequest(origin, turnMessage(binding.ContextID, "first task input"), setting.TaskBinding)
		requireFirstAction(t, f.service, request)
		ready, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: task})
		if err != nil || ready.Readiness != "ready" {
			t.Fatal("declared first task remained held", ready, err)
		}
		if _, err := f.service.TransitionTask(ctx, bindingOf(f.origin), TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "start"}); err != nil {
			t.Fatal("declared first task could not start", err)
		}
	})
	t.Run("oversized_owner_request_is_bounded", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t, serviceDeps{})
		binding := registerPrivate(t, f.service, 10)
		through := f.store.Durable()
		command := uuid.NewV7()
		_, err := f.service.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: command, AgentID: binding.AgentID, Reason: strings.Repeat("x", ledger.MaxEligibilityPayloadBytes+1)})
		if !errors.Is(err, ErrTooLarge) || f.store.Durable() != through {
			t.Fatal("oversized owner request appended or lost bounded error", err)
		}
		if _, err := f.service.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: command, AgentID: binding.AgentID, Reason: "owner_hold"}); err != nil {
			t.Fatal("invalid owner request consumed command", err)
		}
	})
	t.Run("owner_cas_receipt_and_no_effect", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		initial, err := f.service.EligibilityStatus(ctx, binding.AgentID)
		if err != nil || initial.State != "eligible" || initial.Version != 0 || initial.OperationID != (uuid.UUID{}) {
			t.Fatalf("initial projection: %+v %v", initial, err)
		}
		request := EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "future_owner_reason"}
		held, err := f.service.HoldOrdinaryWork(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		through := f.store.Durable()
		view, err := f.service.EligibilityStatus(ctx, binding.AgentID)
		if err != nil || view.State != "ordinary_work_ineligible" || view.Reason != request.Reason || view.Version != 1 || view.OperationID != request.CommandID || view.ThroughSequence != through {
			t.Fatalf("hold projection: %+v %v", view, err)
		}
		start := TurnAdmission{CommandID: uuid.NewV7(), TurnID: uuid.NewV7(), GrantID: grant.GrantID}
		_, err = agent.StartTurn(ctx, start)
		refusal := requireEligibilityHeld(t, err)
		if refusal.ThroughSequence != through || f.store.Durable() != through {
			t.Fatal("refusal consumed work or returned another prefix")
		}
		repeated, err := f.service.HoldOrdinaryWork(ctx, request)
		if err != nil || !reflect.DeepEqual(repeated, held) || f.store.Durable() != through {
			t.Fatal("same-ID hold was not receipt-only", err)
		}
		changed := request
		changed.Reason = "different"
		if _, err := f.service.HoldOrdinaryWork(ctx, changed); !errors.Is(err, ErrCommandMismatch) {
			t.Fatal("changed meaning reused command", err)
		}
		unchanged := request
		unchanged.CommandID, unchanged.ExpectedVersion, unchanged.ExpectedOperationID = uuid.NewV7(), 1, held.OperationID
		if _, err := f.service.HoldOrdinaryWork(ctx, unchanged); !errors.Is(err, ErrNoEffect) || f.store.Durable() != through {
			t.Fatal("no-effect hold appended", err)
		}
		stale := EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "hold"}
		if _, err := f.service.HoldOrdinaryWork(ctx, stale); !errors.Is(err, ErrRevision) {
			t.Fatal("stale version changed hold", err)
		}
		released := releaseEligibility(t, f.service, binding.AgentID, held)
		through = f.store.Durable()
		if _, err := f.service.ReleaseEligibility(ctx, EligibilityRelease{CommandID: uuid.NewV7(), AgentID: binding.AgentID, ExpectedVersion: released.Version, ExpectedOperationID: released.OperationID}); !errors.Is(err, ErrNoEffect) || f.store.Durable() != through {
			t.Fatal("distinct repeated release appended", err)
		}
		if replay, err := f.service.HoldOrdinaryWork(ctx, request); err != nil || replay != held || f.store.Durable() != through {
			t.Fatal("old receipt lost after release", err)
		}
		if _, err := agent.StartTurn(ctx, start); err != nil {
			t.Fatal("explicit retry of refused identity failed", err)
		}
		if _, err := f.service.HoldOrdinaryWork(ctx, EligibilityHold{CommandID: start.CommandID, AgentID: binding.AgentID, ExpectedVersion: released.Version, Reason: "hold"}); !errors.Is(err, ErrCommandMismatch) {
			t.Fatal("cross-family command reused", err)
		}
		reopenScoped(t, f, DefaultLimits())
		if replay, err := f.service.HoldOrdinaryWork(ctx, request); err != nil || replay != held {
			t.Fatal("cold receipt lost after reopen", err)
		}
	})
	t.Run("release_old_operation_at_current_version", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		old := holdEligibility(t, f.service, binding.AgentID)
		releaseEligibility(t, f.service, binding.AgentID, old)
		current := holdEligibility(t, f.service, binding.AgentID)
		through := f.store.Durable()
		_, releaseErr := f.service.ReleaseEligibility(ctx, EligibilityRelease{CommandID: uuid.NewV7(), AgentID: binding.AgentID, ExpectedVersion: current.Version, ExpectedOperationID: old.OperationID})
		_, startErr := agent.StartTurn(ctx, TurnAdmission{CommandID: uuid.NewV7(), TurnID: uuid.NewV7(), GrantID: grant.GrantID})
		if !errors.Is(releaseErr, ErrEligibilityStale) || !errors.Is(startErr, session.ErrEligibilityHeld) || f.store.Durable() != through {
			t.Fatalf("old operation released newer hold or admitted fresh work: release=%v start=%v", releaseErr, startErr)
		}
		requireEligibilityHeld(t, startErr)
	})
	t.Run("concurrent_holds_have_one_winner", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t, serviceDeps{})
		binding := registerPrivate(t, f.service, 10)
		before := f.store.Durable()
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				<-start
				_, err := f.service.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "hold"})
				errs <- err
			})
		}
		close(start)
		wg.Wait()
		close(errs)
		wins, stale := 0, 0
		for err := range errs {
			if err == nil {
				wins++
			} else if errors.Is(err, ErrRevision) {
				stale++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || stale != 1 || f.store.Durable() != before+3 {
			t.Fatalf("concurrent hold counts: %d/%d", wins, stale)
		}
	})
	t.Run("hold_from_first_required_retains_declaration", func(t *testing.T) {
		t.Parallel()
		f, _ := eligibilityGolden(t, 78)
		var agent uuid.UUID
		for id, a := range f.service.p.agents {
			if a.eligibility.current().State == "first_action_required" {
				agent = id
			}
		}
		before, err := f.service.EligibilityStatus(context.Background(), agent)
		if err != nil || before.FirstAction == nil {
			t.Fatal("missing frozen declaration", err)
		}
		held := holdEligibility(t, f.service, agent)
		after, err := f.service.EligibilityStatus(context.Background(), agent)
		if err != nil || after.State != "ordinary_work_ineligible" || after.OperationID != before.OperationID || !reflect.DeepEqual(after.FirstAction, before.FirstAction) {
			t.Fatal("hold discarded first-action history", err)
		}
		releaseEligibility(t, f.service, agent, held)
		fresh := holdEligibility(t, f.service, agent)
		after, err = f.service.EligibilityStatus(context.Background(), agent)
		if err != nil || after.FirstAction != nil || after.OperationID == before.OperationID || fresh.OperationID != fresh.CommandID {
			t.Fatal("new hold retained an executable old declaration", err)
		}
	})
}

func eligibilityGolden(t testing.TB, through uint64) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	_, commits := scopedGoldenFixture(t, "90", 0)
	count := 0
	for _, c := range commits {
		if c.Last <= through {
			count++
		}
	}
	f, commits := scopedGoldenFixture(t, "90", count)
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return f, commits
}

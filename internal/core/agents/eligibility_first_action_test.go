package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func firstActionRequest(origin ledger.Origin, command session.Command, task *ledger.TaskBinding) EligibilityRequireFirst {
	return EligibilityRequireFirst{CommandID: uuid.NewV7(), AgentID: origin.AgentID, Reason: "first_action", ActionID: uuid.NewV7(), FirstActionTurn: FirstActionTurn{ContextID: command.ContextID, TurnID: command.Arguments.(session.MessageSend).TurnID, StartCommandID: command.CommandID, AdmissionCommandID: uuid.NewV7(), Input: command.Arguments.(session.MessageSend), TaskBinding: task}}
}

func requireFirstAction(t testing.TB, s *Service, request EligibilityRequireFirst) EligibilityReceipt {
	t.Helper()
	receipt, err := s.RequireFirstAction(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func firstActionStatus(t testing.TB, s *Service, agent uuid.UUID) session.EligibilityView {
	t.Helper()
	view, err := s.EligibilityStatus(context.Background(), agent)
	if err != nil || view.FirstAction == nil {
		t.Fatal("first action unavailable", err)
	}
	return view
}

func revokeFirstGrant(t testing.TB, f *turnFixture, id uuid.UUID) {
	t.Helper()
	_, err := f.service.RevokeGrant(context.Background(), bindingOf(f.origin), GrantRevocation{CommandID: uuid.NewV7(), RevokeGrantID: id, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEligibilityFirstAction(t *testing.T) {
	t.Parallel()
	t.Run("paid_budget_stays_ordinary", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { posts.Add(1) }, nil)
		f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000001", PeriodKind: "utc_day"})
		f.enable(t)
		command := turnMessage(f.origin.ContextID, "first with no paid capacity")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		before := f.store.Durable()
		if _, err := f.view.Do(ctx, command); err != nil {
			t.Fatal(err)
		}
		waitLoopState(t, f.view, f.origin.ContextID, request.FirstActionTurn.TurnID, "waiting_resource")
		view := firstActionStatus(t, f.service, f.origin.AgentID)
		if view.State != "first_action_required" || view.FirstAction.Disposition != "bound" || view.FirstAction.Declaration.Refusal != nil || posts.Load() != 0 || eligibilityCount(t, f.service, before, ledger.TypeRuntimePaidReserved) != 0 {
			t.Fatal("declaration bypassed budget or recorded post-bind refusal", view, posts.Load())
		}
		if _, err := f.view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: request.FirstActionTurn.TurnID}}); err != nil {
			t.Fatal(err)
		}
		f.finished(t, request.FirstActionTurn.TurnID)
		if posts.Load() != 0 {
			t.Fatal("cancellation of budget-waiting first turn posted")
		}
	})
	t.Run("turn_grant_absent", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "first answer"))
		}, nil)
		f.enable(t)
		command := turnMessage(f.origin.ContextID, "declared request")
		request := firstActionRequest(f.origin, command, nil)
		receipt := requireFirstAction(t, f.service, request)
		revokeFirstGrant(t, f, f.privateGrant)
		before := f.store.Durable()
		result, err := f.view.Do(context.Background(), command)
		if !errors.Is(err, ErrDenied) || result.Outcome != "refused" {
			t.Fatal("declaration bypassed ordinary turn authorization", err)
		}
		view := firstActionStatus(t, f.service, f.origin.AgentID)
		if view.State != "first_action_required" || view.Version != receipt.Version+1 || view.FirstAction.Disposition != "admission_refused" || view.FirstAction.Declaration.Refusal.Code != "denied" || eligibilityCount(t, f.service, before, ledger.TypeRuntimeWorkAdmitted) != 0 || posts.Load() != 0 {
			t.Fatalf("turn refusal admitted work or unlocked gate: %+v", view)
		}
		if got, ok := result.Data.(session.EligibilityView); !ok || got.State != "first_action_required" || got.FirstAction != nil {
			t.Fatal("refusal did not carry current redacted gate", result.Data)
		}
		before = f.store.Durable()
		_, err = f.view.Do(context.Background(), command)
		if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
			t.Fatal("identical refusal appended again", err)
		}
		agent := scopedBound(t, f.service, bindingOf(f.origin))
		_, err = agent.StartTurn(context.Background(), TurnAdmission{CommandID: request.FirstActionTurn.AdmissionCommandID, TurnID: request.FirstActionTurn.TurnID, GrantID: f.privateGrant})
		if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
			t.Fatal("raw first action bypassed authorization or wrote refusal", err)
		}
		// New ordinary authority permits an explicit retry of the same declaration.
		f.contextSetting(t, bindingOf(f.origin))
		result, err = f.view.Do(context.Background(), command)
		if err != nil {
			t.Fatal("explicit unadmitted retry failed", err)
		}
		ended, _ := f.finished(t, result.TurnID)
		view = firstActionStatus(t, f.service, f.origin.AgentID)
		if ended.Outcome != "finished" || posts.Load() != 1 || view.State != "first_action_required" || view.FirstAction.Disposition != "terminal" || view.FirstAction.FirstAdmittedTurn.AdmissionCommandID != request.FirstActionTurn.AdmissionCommandID {
			t.Fatal("first retry duplicated work or lost admitted identity")
		}
		_, err = f.view.Do(context.Background(), turnMessage(f.origin.ContextID, "ordinary still held"))
		requireEligibilityHeld(t, err)
	})
	t.Run("model_grant_revoked", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { posts.Add(1) }, nil)
		f.enable(t)
		command := turnMessage(f.origin.ContextID, "no model permission")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		revokeFirstGrant(t, f, f.grant)
		before := f.store.Durable()
		result, err := f.view.Do(context.Background(), command)
		if err != nil {
			t.Fatal("ordinary turn admission was not independent of model authority", err)
		}
		ended, _ := f.finished(t, result.TurnID)
		view := firstActionStatus(t, f.service, f.origin.AgentID)
		if ended.Reason != "authority_lost" || posts.Load() != 0 || eligibilityCount(t, f.service, before, ledger.TypeRuntimePaidReserved) != 0 || view.State != "first_action_required" || view.FirstAction.Declaration.Refusal != nil {
			t.Fatal("first model refusal bypassed grant or invented a paid-stage refusal")
		}
	})
	t.Run("raw_native_requires_binding", func(t *testing.T) {
		t.Parallel()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("unbound first turn posted") }, nil)
		command := turnMessage(f.origin.ContextID, "raw first")
		request := firstActionRequest(f.origin, command, nil)
		requireFirstAction(t, f.service, request)
		f.turn = request.FirstActionTurn.TurnID
		agent := scopedBound(t, f.service, bindingOf(f.origin))
		start := TurnAdmission{CommandID: request.FirstActionTurn.AdmissionCommandID, TurnID: f.turn, GrantID: f.privateGrant}
		for _, wrong := range []TurnAdmission{{CommandID: uuid.NewV7(), TurnID: f.turn, GrantID: f.privateGrant}, {CommandID: start.CommandID, TurnID: uuid.NewV7(), GrantID: f.privateGrant}} {
			_, err := agent.StartTurn(context.Background(), wrong)
			requireEligibilityHeld(t, err)
		}
		native, err := agent.StartTurn(context.Background(), start)
		if err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		retry, err := agent.StartTurn(context.Background(), start)
		if err != nil || !reflect.DeepEqual(native, retry) || f.store.Durable() != before {
			t.Fatal("raw admission was duplicated", err)
		}
		prepared := f.prepare(t)
		_, _, err = agent.AdmitPaid(context.Background(), prepared)
		requireEligibilityHeld(t, err)
		view := firstActionStatus(t, f.service, f.origin.AgentID)
		if view.FirstAction.Disposition != "admitted_unbound" || view.FirstAction.FirstAdmittedTurn.TurnID != f.turn || eligibilityCount(t, f.service, before, ledger.TypeRuntimePaidReserved) != 0 {
			t.Fatal("unbound declaration dispatched or lost source")
		}
		f.enable(t)
		_, err = f.view.Do(context.Background(), command)
		if !errors.Is(err, ErrUnavailable) || eligibilityCount(t, f.service, before, ledger.TypeRuntimeTurnBound) != 0 {
			t.Fatal("Session rebound raw native admission", err)
		}
	})
	t.Run("raw_local_requires_binding", func(t *testing.T) {
		t.Parallel()
		f, agent, start, local := scopedLocalFixture(t, DefaultLimits())
		command := turnMessage(agent.ids.context, "raw local first")
		input := command.Arguments.(session.MessageSend)
		input.TurnID = start.TurnID
		command.Arguments = input
		request := firstActionRequest(originOf(agent.ids), command, start.TaskBinding)
		request.FirstActionTurn.AdmissionCommandID = start.CommandID
		requireFirstAction(t, f.service, request)
		wrong := start
		wrong.TaskBinding = nil
		if _, err := agent.StartTurn(context.Background(), wrong); err == nil {
			t.Fatal("raw native substituted task binding")
		}
		if _, err := agent.StartTurn(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		_, err := agent.AdmitLocal(context.Background(), local)
		requireEligibilityHeld(t, err)
		if f.store.Durable() != before {
			t.Fatal("unbound first turn acquired local work")
		}
	})
}

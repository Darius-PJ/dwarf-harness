package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// serviceRefusedAdmission admits a fresh call that the book must refuse.
func serviceRefusedAdmission(t *testing.T, l *serviceLive, r PaidAdmission) {
	t.Helper()
	var busy *BusyError
	if _, _, err := scopedBound(t, l.service, bindingOf(l.origin)).AdmitPaid(context.Background(), r); !errors.As(err, &busy) || busy.Reason != "budget_exposure" {
		t.Fatalf("book did not refuse with budget_exposure: %v", err)
	}
}

// TestServiceMoney: an exhausted owner ceiling stops all of the agent's paid
// calls and is the only money cause; repeats append nothing.
func TestServiceMoney(t *testing.T) {
	t.Parallel()
	t.Run("colony_exhausted_repeats_append_nothing", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		l.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "utc_day"})
		r := paidPrepare(t, l.paidFixture)
		serviceRefusedAdmission(t, l, r)
		es := serviceObservations(t, l.service)
		if len(es) != 1 || es[0].Source.Kind != "money_refusal" || es[0].Cause.ScopeKind != "colony" || es[0].Source.Money.OccupiedMicros != "0" || *es[0].Source.Money.CeilingMicros != "0" || es[0].Source.ResetUTCNS == nil || es[0].Source.CommandID != r.CommandID {
			t.Fatalf("colony exhaustion witness: %+v", es)
		}
		v := l.status(t)
		if len(v.Pending) != 1 || *v.Pending[0].DueUTCNS != es[0].Source.ObservedUTCNS+ledger.ServiceGraceNS {
			t.Fatalf("UTC-day exhaustion did not pend for grace: %+v", v)
		}
		through := l.store.Durable()
		serviceRefusedAdmission(t, l, r)
		other := paidPrepare(t, l.paidFixture)
		serviceRefusedAdmission(t, l, other)
		if l.store.Durable() != through {
			t.Fatal("a repeated refusal of the open generation appended")
		}
	})
	t.Run("quote_too_large_is_no_cause", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		l.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000100", PeriodKind: "utc_day"})
		before := l.store.Durable()
		serviceRefusedAdmission(t, l, paidPrepare(t, l.paidFixture))
		if l.store.Durable() != before {
			t.Fatal("occupied+quote > ceiling alone recorded money loss")
		}
	})
	for _, scope := range []string{"agent", "credential", "grant"} {
		t.Run(scope+"_scope", func(t *testing.T) {
			t.Parallel()
			l := newServiceLive(t, "anthropic")
			target := map[string]ledger.PaidScope{"agent": {Kind: "agent", ID: l.origin.AgentID}, "credential": {Kind: "credential", ID: l.credential.ScopeID}, "grant": {Kind: "grant", ID: l.grant}}[scope]
			l.liveSpend(t, target, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "utc_day"})
			serviceRefusedAdmission(t, l, paidPrepare(t, l.paidFixture))
			es := serviceObservations(t, l.service)
			if scope == "agent" {
				if len(es) != 1 || es[0].Cause.ScopeKind != "agent" || es[0].Cause.AgentID != l.origin.AgentID || es[0].Source.Money.Coverage != "mandatory_scope" {
					t.Fatalf("agent ceiling witness: %+v", es)
				}
				return
			}
			// Without an enabled exhaustive registry a credential ceiling does
			// not stop all calls; a grant ceiling is never a money cause.
			if len(es) != 0 {
				t.Fatalf("%s ceiling recorded money loss: %d", scope, len(es))
			}
		})
	}
	for _, row := range []string{"parked_turn_repeats", "parked_turn_lost_at_due", "owner_cancel_before_due", "owner_cancel_limit_raised_before_due", "raised_limit_restores"} {
		t.Run(row, func(t *testing.T) { t.Parallel(); testServiceParkedMoney(t, row) })
	}
	t.Run("disabled_scope_expires_and_second_member_at_same_wake", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		colony, agent := ledger.PaidScope{Kind: "colony"}, ledger.PaidScope{Kind: "agent", ID: l.origin.AgentID}
		l.liveSpend(t, colony, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "utc_day"})
		serviceRefusedAdmission(t, l, paidPrepare(t, l.paidFixture))
		l.liveSpend(t, colony, SpendTermsInput{Enabled: true, Mode: "finite", USD: "1.000000", PeriodKind: "utc_day"})
		l.liveSpend(t, agent, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "utc_day"})
		l.offset.Add(int64(time.Second))
		serviceRefusedAdmission(t, l, paidPrepare(t, l.paidFixture))
		if v := l.status(t); len(v.Pending) != 2 {
			t.Fatalf("two money members did not pend: %+v", v)
		}
		l.liveSpend(t, colony, SpendTermsInput{Enabled: false, Mode: "finite", USD: "1.000000", PeriodKind: "utc_day"})
		l.offset.Add(ledger.ServiceGraceNS + int64(time.Second))
		if _, err := l.service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if v := l.status(t); v.State != "lost" || len(v.Active) != 2 || len(v.Pending) != 0 {
			t.Fatalf("disabled scope or later member did not expire at the same wake: %+v", v)
		}
	})
}

// TestServiceUnknownMoney: a paid call with an unknown outcome stays held
// through loss and restoration, and is never sent again.
func TestServiceUnknownMoney(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			l := newServiceLive(t, provider)
			l.respond(serviceProviderError(provider, http.StatusUnauthorized, "authentication_error", nil))
			denied := l.call(t)
			if denied.err != nil {
				t.Fatal(denied.err)
			}
			unknown := l.view(t, denied.admission.AttemptID)
			if unknown.Result == nil || unknown.Result.Kind != "unknown" || unknown.HeldMicros == "0" || l.status(t).State != "lost" {
				t.Fatalf("refused call did not stay unknown and held while lost: %+v", unknown)
			}
			admission := unknown.Admission
			l.respond(serviceSuccess(t, provider))
			success := l.call(t)
			if success.err != nil {
				t.Fatal(success.err)
			}
			settled := l.view(t, success.admission.AttemptID)
			check := func(stage string) {
				t.Helper()
				if v := l.view(t, denied.admission.AttemptID); v.State != unknown.State || v.HeldMicros != unknown.HeldMicros || v.SettledMicros != unknown.SettledMicros || v.Version != unknown.Version {
					t.Fatalf("%s: unknown hold changed: %+v", stage, v)
				}
				for _, scope := range admission.Scopes {
					budget, err := l.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: scope, Period: admission.AdmissionPeriod})
					if err != nil || budget.HeldMicros != unknown.HeldMicros || budget.SettledMicros != settled.SettledMicros {
						t.Fatalf("%s: %+v held=%s settled=%s err=%v", stage, scope, budget.HeldMicros, budget.SettledMicros, err)
					}
				}
				if l.posts.Load() != 2 {
					t.Fatalf("%s: original attempt was resent: %d posts", stage, l.posts.Load())
				}
			}
			if l.status(t).State != "available" {
				t.Fatal("successful call did not restore")
			}
			check("restored")
			var err error
			deps := l.service.deps
			if err = l.service.Close(); err != nil {
				t.Fatal(err)
			}
			l.service, _, err = openPaidService(context.Background(), l.store, l.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			check("reopened")
		})
	}
}

// serviceParkedOnlyAfter reports whether every group committed after the
// sequence is exactly an owner continuation group (ledger.command,
// runtime.continuation_changed, runtime.continuation_receipt) that keeps the
// turn waiting_resource or model_pending with no stop reason, or, when service
// is set, a complete F3-91 group as serviceOnlyAfter requires. Any paid, stop,
// cancel or ending record fails it.
func serviceParkedOnlyAfter(t *testing.T, s *Service, after uint64, service bool) bool {
	t.Helper()
	only := true
	if err := s.scan(context.Background(), after, s.store.Durable(), func(c ledger.Commit) error {
		r := c.Records
		if len(r) == 3 && r[0].Type == ledger.TypeCommand && r[1].Type == ledger.TypeRuntimeContinuationChanged && r[2].Type == ledger.TypeRuntimeContinuationReceipt {
			v, err := ledger.DecodeRuntimeContinuationChanged(r[1].Payload)
			if err != nil {
				return err
			}
			only = only && v.StopReason == "" && (v.State == "waiting_resource" || v.State == "model_pending")
			return nil
		}
		commands := 0
		for _, record := range r {
			switch {
			case record.Type == ledger.TypeCommand:
				commands++
			case !isServiceType(record.Type):
				only = false
			}
		}
		only = only && service && commands == 1 && len(r) > 2 && r[len(r)-1].Type == ledger.TypeRuntimeServiceReceipt
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return only
}

// serviceWaitParked waits on the loop's change channel until the turn is
// parked and not running.
func serviceWaitParked(t *testing.T, f *turnFixture, turn uuid.UUID) {
	t.Helper()
	for {
		f.service.mu.Lock()
		r := f.service.loop
		f.service.mu.Unlock()
		r.mu.Lock()
		current := r.turns[turn]
		parked := current != nil && !current.running && current.state == "waiting_resource"
		changed := r.changed
		r.mu.Unlock()
		if parked {
			return
		}
		if current == nil {
			t.Fatal("parked turn ended")
		}
		<-changed
	}
}

// testServiceParkedMoney drives a real loop turn parked by an exhausted
// colony ceiling (Answer 9): its repeats, its due, an owner cancel, and the
// raised limit that lets the same turn's admission pass.
func testServiceParkedMoney(t *testing.T, row string) {
	var posts atomic.Int64
	prepared := make(chan uuid.UUID, 64)
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
	}, func(deps *serviceDeps) {
		deps.afterLoopPrepare = func(turn uuid.UUID) {
			select {
			case prepared <- turn:
			default:
			}
		}
	})
	var offset atomic.Int64
	f.service.deps.reviewClock = func() (int64, error) { return time.Now().UTC().UnixNano() + offset.Load(), nil }
	colony := ledger.PaidScope{Kind: "colony"}
	f.liveSpend(t, colony, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "utc_day"})
	f.enable(t)
	_, start := f.send(t)
	continuationWait(t, f, start.TurnID, "paid_prepare")
	es := serviceObservations(t, f.service)
	if len(es) != 1 || es[0].Source.Kind != "money_refusal" || es[0].Cause.ScopeKind != "colony" || es[0].Source.TurnID != start.TurnID {
		t.Fatalf("parked refusal witness: %+v", es)
	}
	due := es[0].Source.ObservedUTCNS + ledger.ServiceGraceNS
	status := func() ServiceView {
		v, err := f.service.ServiceStatus(context.Background(), f.origin.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	atDue := func() {
		offset.Store(due - time.Now().UTC().UnixNano() + int64(time.Second))
		if _, err := f.service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	parked := f.store.Durable()
	switch row {
	case "parked_turn_repeats":
		for range 3 {
			for len(prepared) > 0 {
				<-prepared
			}
			f.service.mu.Lock()
			f.service.loop.notify()
			f.service.mu.Unlock()
			if <-prepared != start.TurnID {
				t.Fatal("another turn ran")
			}
			serviceWaitParked(t, f, start.TurnID)
			// Each wake's native resume and re-park are owner continuation
			// groups; the repeated refusal itself appends nothing.
			if !serviceParkedOnlyAfter(t, f.service, parked, false) {
				t.Fatal("a repeated wake appended more than its owner park")
			}
		}
		if n := len(serviceObservations(t, f.service)); n != 1 || posts.Load() != 0 {
			t.Fatalf("repeats: observations=%d posts=%d", n, posts.Load())
		}
	case "parked_turn_lost_at_due":
		atDue()
		serviceWaitParked(t, f, start.TurnID)
		if v := status(); v.State != "lost" || !serviceParkedOnlyAfter(t, f.service, parked, true) || posts.Load() != 0 {
			t.Fatalf("at due: %+v (only F3-91 groups and owner parks may follow the park)", v)
		}
	case "owner_cancel_before_due", "owner_cancel_limit_raised_before_due":
		if _, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: start.TurnID}}); err != nil {
			t.Fatal(err)
		}
		if result, _ := f.finished(t, start.TurnID); result.Outcome != "cancelled" {
			t.Fatalf("cancel outcome: %+v", result)
		}
		if v := status(); len(v.Pending) != 1 {
			t.Fatalf("an owner cancel withdrew the member: %+v", v)
		}
		want := "lost"
		if row == "owner_cancel_limit_raised_before_due" {
			f.liveSpend(t, colony, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
			want = "available"
		}
		atDue()
		v := status()
		if v.State != want || len(v.Pending) != 0 {
			t.Fatalf("at due after cancel: %+v, want %s", v, want)
		}
		if row == "owner_cancel_limit_raised_before_due" {
			withdrawn := false
			for _, e := range serviceObservations(t, f.service) {
				withdrawn = withdrawn || e.Source.Reason == "budget_available" && e.Source.Origin == nil
			}
			if !withdrawn {
				t.Fatal("the book recheck did not withdraw the member")
			}
		}
	case "raised_limit_restores":
		atDue()
		if status().State != "lost" {
			t.Fatal("exhausted book at due did not lose the member")
		}
		f.liveSpend(t, colony, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
		f.service.mu.Lock()
		f.service.loop.notify()
		f.service.mu.Unlock()
		if result, _ := f.finished(t, start.TurnID); result.Outcome != "finished" || posts.Load() != 1 {
			t.Fatalf("raised limit did not let the same turn finish: %+v posts=%d", result, posts.Load())
		}
		clears, restorations := 0, 0
		for _, e := range serviceObservations(t, f.service) {
			if e.Source.Reason == "budget_available" {
				clears++
				if e.Source.TurnID != start.TurnID {
					t.Fatal("admission clear names another turn")
				}
			}
			if e.Source.Kind == "successful_call" {
				restorations++
			}
		}
		if clears != 1 || restorations != 1 || status().State != "available" {
			t.Fatalf("clears=%d restorations=%d state=%s", clears, restorations, status().State)
		}
		admissions := loopPaidAdmissions(t, f.service)
		if len(admissions) != 1 {
			t.Fatalf("admissions: %d", len(admissions))
		}
		attempt, err := f.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: admissions[0].AttemptID})
		if err != nil || attempt.HeldMicros != "0" || attempt.Result == nil || attempt.Result.Kind != "usage" {
			t.Fatalf("restoring call money: %+v %v", attempt, err)
		}
		budget, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: colony, Period: admissions[0].AdmissionPeriod})
		if err != nil || budget.HeldMicros != "0" || budget.SettledMicros != attempt.SettledMicros {
			t.Fatalf("colony money: %+v %v", budget, err)
		}
	}
}

package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestEligibilityMoney(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"first_before_intent", "first_after_intent"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			testEligibilityFirstRestart(t, boundary)
		})
	}
	ctx := context.Background()
	var posts atomic.Int32
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":1,"output_tokens":1}`, `{"input_tokens":3,"output_tokens":4}`, "answer", "end_turn", "", true))
	})
	deps := paidServerDeps(server, nil)
	f := newLivePaidFixture(t, deps)
	unsent, sent := f.prepare(t), f.prepare(t)
	f.admit(t, unsent)
	f.admit(t, sent)
	out, err := f.service.DispatchPaid(ctx, PaidDispatch{CommandID: uuid.NewV7(), AttemptID: sent.AttemptID, ExpectedVersion: 1})
	if err != nil || out.ObservedPaidCall == nil || posts.Load() != 1 {
		t.Fatal("live paid fixture did not send once", err)
	}
	evidence, err := out.ObservedPaidCall.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	held := holdEligibility(t, f.service, f.origin.AgentID)
	if _, err := f.service.ReconcilePaid(ctx, PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: sent.AttemptID, ExpectedVersion: 2}); err != nil {
		t.Fatal("hold fenced unknown reconciliation", err)
	}
	admission := f.view(t, sent.AttemptID).Admission
	if admission.Quote.MaximumMicros != 120 {
		t.Fatalf("fixture quote changed: %d", admission.Quote.MaximumMicros)
	}
	check := func(unsentState, unsentHeld, sentState, sentHeld, totalHeld, settled string) {
		t.Helper()
		a, b := f.view(t, unsent.AttemptID), f.view(t, sent.AttemptID)
		if a.State != unsentState || a.HeldMicros != unsentHeld || b.State != sentState || b.HeldMicros != sentHeld || b.SettledMicros != settled {
			t.Fatalf("per-attempt exposure changed: %+v / %+v", a, b)
		}
		for _, scope := range admission.Scopes {
			budget, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: scope, Period: admission.AdmissionPeriod})
			if err != nil || budget.HeldMicros != totalHeld || budget.SettledMicros != settled {
				t.Fatalf("%+v money changed: held=%s settled=%s err=%v", scope, budget.HeldMicros, budget.SettledMicros, err)
			}
		}
		if posts.Load() != 1 {
			t.Fatal("query or administration resent paid work")
		}
	}
	check("reserved", "120", "unknown", "120", "240", "0")
	f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000240", PeriodKind: "utc_day"})
	for range 2 {
		if _, err := f.service.EligibilityStatus(ctx, f.origin.AgentID); err != nil {
			t.Fatal(err)
		}
		check("reserved", "120", "unknown", "120", "240", "0")
	}
	releaseEligibility(t, f.service, f.origin.AgentID, held)
	check("reserved", "120", "unknown", "120", "240", "0")
	follow := f.prepare(t)
	agent := scopedBound(t, f.service, bindingOf(f.origin))
	through := f.store.Durable()
	var busy *BusyError
	if _, _, err := agent.AdmitPaid(ctx, follow); !errors.As(err, &busy) || busy.Reason != "budget_exposure" {
		t.Fatal("release created spend permission beyond retained exposure", err)
	}
	// The exhausted colony ceiling is service evidence (F3-91); the refusal
	// itself still writes no native record.
	if !serviceOnlyAfter(t, f.service, through) {
		t.Fatal("refusal wrote a native record")
	}
	through = f.store.Durable()
	for range 2 {
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		f.service, _, err = openPaidService(ctx, f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
		if err != nil {
			t.Fatal(err)
		}
		if f.store.Durable() != through {
			t.Fatal("Open reconciled money implicitly")
		}
		check("reserved", "120", "unknown", "120", "240", "0")
	}
	holdEligibility(t, f.service, f.origin.AgentID)
	if _, err := f.service.ReconcilePaid(ctx, PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: unsent.AttemptID, ExpectedVersion: 1}); err != nil {
		t.Fatal("hold fenced unsent release", err)
	}
	check("released_unsent", "0", "unknown", "120", "120", "0")
	late := PaidObservation{CommandID: uuid.NewV7(), AttemptID: sent.AttemptID, ExpectedVersion: 3, TrustedEvidence: &evidence}
	first, err := f.service.RecordPaidObservation(ctx, late)
	if err != nil {
		t.Fatal("hold fenced late exact evidence", err)
	}
	check("released_unsent", "0", "settled", "0", "0", "7")
	through = f.store.Durable()
	retry, err := f.service.RecordPaidObservation(ctx, late)
	if err != nil || retry.Sequence != first.Sequence || f.store.Durable() != through {
		t.Fatal("held late settlement repeated", err)
	}
}

package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func TestPaidOverrunAcknowledgement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		model := paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "request-one")
		_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":121,"output_tokens":4}`, `{"output_tokens":4}`, "answer", "end_turn", "", true))
	})
	f := newLivePaidFixture(t, paidServerDeps(server, nil))
	alias := f.id()
	profile, _ := json.Marshal(f.route.Profile)
	if _, err := f.service.ConfigurePaidRoute(ctx, bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: alias, ExpectedVersion: 0, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: profile}); err != nil {
		t.Fatal(err)
	}
	first := f.prepare(t)
	f.admit(t, first)
	second := f.prepare(t)
	f.admit(t, second)
	var dispatches []PaidDispatch
	var observations []PaidDispatchOutcome
	for _, r := range []PaidAdmission{first, second} {
		d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
		out, err := f.service.DispatchPaid(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		dispatches = append(dispatches, d)
		observations = append(observations, out)
	}
	for i, d := range dispatches {
		paidRecord(t, f, d, observations[i])
	}
	period := f.view(t, first.AttemptID).Admission.AdmissionPeriod
	if v := f.budget(t, period); v.SettledMicros != "250" || !v.Fenced || v.HeldMicros != "0" {
		t.Fatal("truthful overrun was clamped or un-fenced")
	}
	versions := map[string]int64{f.route.RouteID.String(): f.service.p.paidRoutes[f.route.RouteID].Version, alias.String(): f.service.p.paidRoutes[alias].Version}
	f.service.deps.reviewClock = func() (int64, error) { t.Error("acknowledgement sampled clock"); return 0, ErrPaidClock }
	for i, r := range []PaidAdmission{first, second} {
		v := f.view(t, r.AttemptID)
		ack := PaidOverrunAcknowledgement{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: v.Version, ResultSequence: int64(v.ResultSequence), EvidenceSHA256: v.Result.EvidenceSHA256, RetiredProfileSHA256: v.Admission.ProfileSHA256}
		bad := ack
		bad.ResultSequence--
		before := f.store.Durable()
		if _, err := f.service.AcknowledgePaidOverrun(ctx, bindingOf(f.origin), bad); err == nil || f.store.Durable() != before {
			t.Fatal("stale evidence cleared fence")
		}
		receipt, err := f.service.AcknowledgePaidOverrun(ctx, bindingOf(f.origin), ack)
		if err != nil {
			t.Fatal(err)
		}
		before = f.store.Durable()
		retry, err := f.service.AcknowledgePaidOverrun(ctx, bindingOf(f.origin), ack)
		if err != nil || retry.Sequence != receipt.Sequence || f.store.Durable() != before {
			t.Fatal("acknowledgement retry recommitted")
		}
		if book := f.budget(t, period); book.SettledMicros != "250" || book.HeldMicros != "0" || book.Fenced != (i == 0) {
			t.Fatal("acknowledgement erased actual money or another fence")
		}
		if !f.view(t, r.AttemptID).Acknowledged {
			t.Fatal("acknowledgement not visible")
		}
	}
	f.service.deps.reviewClock = func() (int64, error) { return f.service.p.reviewFloor, nil }
	for _, route := range []ledger.RuntimePaidRouteConfigured{f.service.p.paidRoutes[f.route.RouteID], f.service.p.paidRoutes[alias]} {
		if route.Version != versions[route.RouteID.String()] || !route.Enabled {
			t.Fatal("retirement forged route state or its CAS version")
		}
		before := f.store.Durable()
		_, err := f.service.ConfigurePaidRoute(ctx, bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: route.RouteID, ExpectedVersion: route.Version, Enabled: true, CredentialScopeID: route.CredentialScopeID, Profile: profile})
		if err == nil || f.store.Durable() != before {
			t.Fatal("retired profile was reusable through an alias route")
		}
	}
	reopenScoped(t, f.serviceFixture, DefaultLimits())
	if v := f.budget(t, period); v.SettledMicros != "250" || v.Fenced {
		t.Fatal("acknowledgement replay changed money or fence")
	}
}

func TestPaidNoChargeRuleSet(t *testing.T) {
	t.Parallel()
	for _, inside := range []bool{true, false} {
		t.Run(map[bool]string{true: "unfamiliar-declared-rule-holds", false: "undeclared-rule-refused"}[inside], func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { paidSuccess(t, w, r) })
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			f.liveProfile(t, func(p *providers.Profile) {
				p.ModelID = providers.Sonnet
				p.RequestedTier = "auto"
				p.NoChargeRules = []string{}
				if inside {
					p.NoChargeRules = []string{"future_rule"}
				}
			})
			p, _, _ := paidRun(t, f)
			r := f.result(t, p, 0, 0)
			r.Kind, r.Usage, r.ErrorClass = "no_charge", nil, "refused"
			r.NoCharge = &ledger.NoChargeEvidence{Rule: "future_rule", StopReason: "refusal", StopDetailsType: "refusal", CategoryKind: "null", OutputTokens: new(int64), Complete: true}
			reopenScoped(t, f.serviceFixture, DefaultLimits())
			if !inside {
				f.refuse(t, f.drafts(t, r))
				return
			}
			f.append(t, r)
			reopenScoped(t, f.serviceFixture, DefaultLimits())
			v := f.view(t, p.AttemptID)
			if v.State != "unknown" || v.HeldMicros == "0" || v.SettledMicros != "0" {
				t.Fatal("unfamiliar rule inferred a refund")
			}
		})
	}
}

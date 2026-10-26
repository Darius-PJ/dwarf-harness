package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func (f *paidFixture) liveProfile(t *testing.T, change func(*providers.Profile)) {
	t.Helper()
	p := providerProfile(f.route.Profile)
	p.CapabilityVersion += "-changed"
	change(&p)
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.ConfigurePaidRoute(context.Background(), bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw})
	if err != nil {
		t.Fatal(err)
	}
	f.route = f.service.p.paidRoutes[f.route.RouteID]
}

func TestAnthropicRefusalBilling(t *testing.T) {
	t.Parallel()
	t.Run("late-output-witness-prevents-refund", paidLateRefusalWitnessRow)
	for _, row := range []struct {
		name, category, text, output, model, state, cost string
		complete                                         bool
	}{
		{"null-free", "null", "", "0", providers.Sonnet, "no_charge", "0", true},
		{"cyber-free", `"cyber"`, "", "0", providers.Opus, "no_charge", "0", true},
		{"absent-output-free", "null", "", "", providers.Sonnet, "no_charge", "0", true},
		{"bio-billed", `"bio"`, "", "0", providers.Sonnet, "settled", "7", true},
		{"frontier-billed", `"frontier_llm"`, "", "0", providers.Opus, "settled", "7", true},
		{"reasoning-billed", `"reasoning_extraction"`, "", "0", providers.Sonnet, "settled", "7", true},
		{"midstream-billed", `"cyber"`, "partial", "4", providers.Sonnet, "settled", "11", true},
		{"midstream-zero-inconsistent", `"cyber"`, "partial", "0", providers.Sonnet, "unknown", "0", true},
		{"missing-category", "", "", "0", providers.Sonnet, "unknown", "0", true},
		{"incomplete", "null", "", "0", providers.Sonnet, "unknown", "0", false},
		{"haiku-held", "null", "", "0", providers.Haiku, "unknown", "0", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "request-one")
				details := `{"type":"refusal"}`
				if row.category != "" {
					details = `{"type":"refusal","category":` + row.category + `}`
				}
				usage := `{"input_tokens":7}`
				if row.output != "" {
					usage = `{"input_tokens":7,"output_tokens":` + row.output + `}`
				}
				_, _ = io.WriteString(w, paidSSE(model, usage, usage, row.text, "refusal", details, row.complete))
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			f.liveProfile(t, func(p *providers.Profile) {
				p.ModelID = row.model
				p.RequestedTier = "auto"
				p.NoChargeRules = []string{"anthropic_refusal_before_output_2026_09_24"}
				if row.model == providers.Haiku {
					p.RequestedTier = "standard_only"
					p.NoChargeRules = []string{}
				}
			})
			_, d, out := paidRun(t, f)
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			if v.State != row.state || v.SettledMicros != row.cost || (v.HeldMicros != "0") != (row.state == "unknown") || len(v.Result.MessageObjects) != 0 {
				t.Fatalf("refusal billing mismatch: state=%s cost=%s hold=%s", v.State, v.SettledMicros, v.HeldMicros)
			}
			if row.text != "" && (v.Result.OutputStarted == nil || !*v.Result.OutputStarted) {
				t.Fatal("refusal erased prior output witness")
			}
		})
	}
}

// A late object is an explicit host attestation, not a second dispatch or a
// caller price. Start with the actual capture so all request/identity bindings
// remain those of the single loopback exchange.
func paidLateEvidence(t *testing.T, f *paidFixture, out PaidDispatchOutcome, change func(*paidObservationObject)) ledger.ObjectRef {
	t.Helper()
	ref, err := out.ObservedPaidCall.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
	if err != nil {
		t.Fatal(err)
	}
	var o paidObservationObject
	if err = json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	change(&o)
	raw, err = json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := scope.publishObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.ObjectRef{SHA256: digest, Bytes: int64(len(raw))}
}
func paidLiveSettlementRows(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"one-delta-original-period", "stale-predecessor", "wrong-message", "wrong-request", "overflow-holds", "output-witness-monotone"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { paidSuccess(t, w, r) })
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			admission, d, out := paidRun(t, f)
			firstRequest := PaidObservation{CommandID: f.id(), AttemptID: d.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall}
			first, err := f.service.RecordPaidObservation(context.Background(), firstRequest)
			if err != nil {
				t.Fatal(err)
			}
			v := f.view(t, d.AttemptID)
			if _, err := f.service.EndTurn(context.Background(), TurnEnding{CommandID: f.id(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
				t.Fatal(err)
			}
			// Rotate the tariff/period and revoke authority: factual correction must
			// still use the captured admission, never reauthorize or sample time.
			f.service.deps.reviewClock = func() (int64, error) { return admission.AdmissionUTCNS + int64(24*time.Hour), nil }
			f.liveProfile(t, func(p *providers.Profile) {
				p.TariffVersion += "-new"
				for i := range p.Tariff.Rates {
					p.Tariff.Rates[i].Numerator = "10"
				}
			})
			_, err = f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "anthropic", CredentialVersion: f.credential.CredentialVersion})
			if err != nil {
				t.Fatal(err)
			}
			f.service.deps.reviewClock = func() (int64, error) { t.Error("factual settlement sampled clock"); return 0, ErrPaidClock }
			late := paidLateEvidence(t, f, out, func(o *paidObservationObject) {
				o.RawUsageMerged["input_tokens"] = json.RawMessage(`9`)
				switch name {
				case "wrong-message":
					o.ProviderMessageID = "other-message"
				case "wrong-request":
					o.ProviderRequestID = "other-request"
				case "overflow-holds":
					o.RawUsageMerged["input_tokens"] = json.RawMessage(`9223372036854775807`)
				case "output-witness-monotone":
					o.OutputStarted = false
				}
			})
			request := PaidCorrection{PaidObservation: PaidObservation{CommandID: f.id(), AttemptID: d.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}, PreviousResultSequence: int64(v.ResultSequence), PreviousEvidenceSHA256: v.Result.EvidenceSHA256}
			if name == "stale-predecessor" {
				request.PreviousResultSequence--
			}
			before := f.store.Durable()
			receipt, err := f.service.CorrectPaidSettlement(context.Background(), request)
			if name == "stale-predecessor" || name == "wrong-message" || name == "wrong-request" || name == "overflow-holds" {
				if err == nil || f.store.Durable() != before || f.view(t, d.AttemptID).SettledMicros != "11" {
					t.Fatal("invalid correction replaced known settlement")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := f.budget(t, admission.AdmissionPeriod); got.SettledMicros != "13" || got.HeldMicros != "0" {
				t.Fatal("late usage added twice or used current tariff")
			}
			if got := f.budget(t, ""); got.SettledMicros != "0" {
				t.Fatal("correction charged the new period")
			}
			if got := f.view(t, d.AttemptID); got.Result.OutputStarted == nil || !*got.Result.OutputStarted {
				t.Fatal("late evidence cleared output witness")
			}
			before = f.store.Durable()
			retry, err := f.service.CorrectPaidSettlement(context.Background(), request)
			if err != nil || retry.Sequence != receipt.Sequence || f.store.Durable() != before {
				t.Fatal("same correction charged twice")
			}
			retry, err = f.service.RecordPaidObservation(context.Background(), firstRequest)
			if err != nil || retry.Sequence != first.Sequence || f.store.Durable() != before {
				t.Fatal("old result retry changed after correction")
			}
			oldRef, _ := out.ObservedPaidCall.Evidence()
			duplicate := PaidObservation{CommandID: f.id(), AttemptID: d.AttemptID, ExpectedVersion: 4, TrustedEvidence: &oldRef}
			retry, err = f.service.RecordPaidObservation(context.Background(), duplicate)
			if err != nil || retry.Sequence != first.Sequence || f.store.Durable() != before {
				t.Fatal("duplicate earlier evidence acquired financial effect")
			}
			// The no-effect duplicate did not consume its caller command ID.
			_, err = f.service.AcknowledgePaidOverrun(context.Background(), bindingOf(f.origin), PaidOverrunAcknowledgement{CommandID: duplicate.CommandID, AttemptID: d.AttemptID, ExpectedVersion: 4})
			if errors.Is(err, ErrCommandMismatch) {
				t.Fatal("duplicate evidence consumed command identity")
			}
		})
	}
	t.Run("overflow-initial-retains-entire-hold", func(t *testing.T) {
		t.Parallel()
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
			model := paidRequestModel(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":9223372036854775807,"output_tokens":1}`, `{"output_tokens":1}`, "", "end_turn", "", true))
		})
		f := newLivePaidFixture(t, paidServerDeps(server, nil))
		a, d, out := paidRun(t, f)
		paidRecord(t, f, d, out)
		v := f.view(t, d.AttemptID)
		if v.State != "unknown" || v.HeldMicros != strconv.FormatInt(a.Quote.MaximumMicros, 10) || v.SettledMicros != "0" || !v.Fenced {
			t.Fatal("overflow fabricated settlement or released hold")
		}
	})
}

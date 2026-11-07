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

func TestAgentTailPaidSelectionRace(t *testing.T) {
	t.Parallel()
	t.Run("preparation_retry_during_read", testColdPaidPreparationRetry)
	t.Run("preparation_failed_read", testColdPaidPreparationFailure)
	for _, mode := range []string{"attempt_fold_revoke", "containing_commit_correction", "old_evidence_revoke", "post_read_revoke"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			posts := make(chan struct{}, 8)
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts <- struct{}{}
				paidSuccess(t, w, r)
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			admission, dispatch, out := paidRun(t, f)
			observation := PaidObservation{CommandID: f.id(), AttemptID: dispatch.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall}
			first, err := f.service.RecordPaidObservation(ctx, observation)
			if err != nil {
				t.Fatal(err)
			}
			view := f.view(t, dispatch.AttemptID)
			binding := bindingOf(f.origin)
			reader := privateScopedGrant(binding, 0xf00, paidPhysicalCapacity(), "private.read")
			issueScopedRoot(t, f.service, binding, 0xf03, reader)
			agent := scopedBound(t, f.service, binding)
			request := PaidObjectLookup{AttemptID: dispatch.AttemptID, GrantID: reader.GrantID, Object: *view.Result.EvidenceObject, MaxBytes: maxObjectBytes}
			original, err := agent.ReadPaidObject(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			late := paidLateEvidence(t, f, out, func(o *paidObservationObject) { o.RawUsageMerged["input_tokens"] = json.RawMessage(`9`) })
			correction := PaidCorrection{PaidObservation: PaidObservation{CommandID: f.id(), AttemptID: dispatch.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}, PreviousResultSequence: int64(view.ResultSequence), PreviousEvidenceSHA256: view.Result.EvidenceSHA256}
			kind := needPaid
			wantSettled := "11"
			if mode == "old_evidence_revoke" {
				if _, err := f.service.CorrectPaidSettlement(ctx, correction); err != nil {
					t.Fatal(err)
				}
				kind, wantSettled = needDirectoryScan, "13"
			} else if mode != "post_read_revoke" {
				f.service.mu.Lock()
				f.service.p.paidHotLimit = 0
				f.service.p.prunePaid()
				f.service.mu.Unlock()
			}
			if mode == "containing_commit_correction" {
				kind, wantSettled = needCommit, "13"
				damageHistoryCache(f.service, "evicted")
			} else {
				damageHistoryCache(f.service, "disabled")
			}
			if mode == "post_read_revoke" {
				f.service.deps.afterRead = func(_ Binding, operation string) {
					if operation == "paid_object" {
						f.service.mu.Lock()
						f.service.p.paidHotLimit = 0
						f.service.p.prunePaid()
						f.service.mu.Unlock()
					}
				}
			}
			arrived, release := stopColdHistory(f.service, kind)
			defer release()
			type answer struct {
				data []byte
				err  error
			}
			done := make(chan answer, 1)
			go func() { data, err := agent.ReadPaidObject(ctx, request); done <- answer{data, err} }()
			select {
			case <-arrived:
			case got := <-done:
				t.Fatalf("paid selection did not suspend: bytes=%d err=%v", len(got.data), got.err)
			}
			if mode == "containing_commit_correction" {
				if _, err := f.service.CorrectPaidSettlement(ctx, correction); err != nil {
					t.Fatal(err)
				}
			} else {
				f.service.mu.Lock()
				authority := f.service.p.authority
				f.service.mu.Unlock()
				if _, err := f.service.RevokeGrant(ctx, binding, GrantRevocation{CommandID: f.id(), RevokeGrantID: reader.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(authority)}); err != nil {
					t.Fatal(err)
				}
			}
			before := f.store.Durable()
			release()
			got := <-done
			if mode == "containing_commit_correction" {
				if got.err != nil || string(got.data) != string(original) {
					t.Fatalf("correction replaced selected historical evidence: bytes=%d err=%v", len(got.data), got.err)
				}
				retry, err := f.service.RecordPaidObservation(ctx, observation)
				if err != nil || retry.Sequence != first.Sequence {
					t.Fatalf("old observation lost its one receipt: %+v %v", retry, err)
				}
				foreign := request
				foreign.AttemptID = f.id()
				if data, err := agent.ReadPaidObject(ctx, foreign); data != nil || !errors.Is(err, ErrDenied) {
					t.Fatalf("foreign attempt selected historical bytes: bytes=%d err=%v", len(data), err)
				}
			} else if got.data != nil || !errors.Is(got.err, ErrDenied) {
				t.Fatalf("revoked selection disclosed bytes: bytes=%d err=%v", len(got.data), got.err)
			}
			budget := f.budget(t, admission.AdmissionPeriod)
			if budget.SettledMicros != wantSettled || budget.HeldMicros != "0" || f.store.Durable() != before || len(posts) != 1 {
				t.Fatalf("cold selection changed money or sent again: %+v posts=%d", budget, len(posts))
			}
		})
	}
}

func testColdPaidTotals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	posts := make(chan struct{}, 8)
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts <- struct{}{}
		paidSuccess(t, w, r)
	})
	f := newLivePaidFixture(t, paidServerDeps(server, nil))
	admission, dispatch, out := paidRun(t, f)
	observation := PaidObservation{CommandID: f.id(), AttemptID: dispatch.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall}
	if _, err := f.service.RecordPaidObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	view := f.view(t, dispatch.AttemptID)
	binding := bindingOf(f.origin)
	reader := privateScopedGrant(binding, 0xf00, paidPhysicalCapacity(), "turn.inspect")
	issueScopedRoot(t, f.service, binding, 0xf03, reader)
	agent := scopedBound(t, f.service, binding)
	request := PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}, Period: admission.AdmissionPeriod, GrantID: reader.GrantID}
	late := paidLateEvidence(t, f, out, func(o *paidObservationObject) { o.RawUsageMerged["input_tokens"] = json.RawMessage(`9`) })
	correction := PaidCorrection{PaidObservation: PaidObservation{CommandID: f.id(), AttemptID: dispatch.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}, PreviousResultSequence: int64(view.ResultSequence), PreviousEvidenceSHA256: view.Result.EvidenceSHA256}
	damageHistoryCache(f.service, "evicted")
	arrived, release := stopColdHistory(f.service, needPaidTotal)
	defer release()
	type answer struct {
		view PaidBudgetView
		err  error
	}
	done := make(chan answer, 1)
	go func() { view, err := agent.PaidBudgetStatus(ctx, request); done <- answer{view, err} }()
	select {
	case <-arrived:
	case got := <-done:
		t.Fatalf("filtered totals did not suspend: %+v %v", got.view, got.err)
	}
	if _, err := f.service.CorrectPaidSettlement(ctx, correction); err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	release()
	got := <-done
	if got.err != nil || got.view.HeldMicros != "0" || got.view.SettledMicros != "13" || f.store.Durable() != before || len(posts) != 1 {
		t.Fatalf("cold totals ignored concurrent correction or repeated a send: %+v %v posts=%d", got.view, got.err, len(posts))
	}
}

func coldPaidPreparation(t *testing.T) (*paidFixture, *Agent, PaidAdmission) {
	t.Helper()
	ctx := context.Background()
	f := newLivePaidFixture(t)
	owner := bindingOf(f.origin)
	work := scopedWorkContext(t, f.service, owner, 0xf100, 0xf101)
	turnGrant := privateScopedGrant(work, 0xf110, paidPhysicalCapacity(), "turn.start")
	issueScopedRoot(t, f.service, owner, 0xf111, turnGrant)
	agent := scopedBound(t, f.service, work)
	f.turn = privateID(0xf113)
	if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(0xf112), TurnID: f.turn, GrantID: turnGrant.GrantID}); err != nil {
		t.Fatal(err)
	}
	f.grant = privateID(0xf120)
	spec := GrantSpec{GrantID: f.grant, Grantee: work, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{work}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked"}
	issueScopedRoot(t, f.service, owner, 0xf121, spec)
	f.origin.ContextID = work.ContextID
	f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: f.grant}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000120", PeriodKind: "utc_day"})
	request := f.prepare(t)
	damageContextAssociations(f.service, "missing")
	return f, agent, request
}

func testColdPaidPreparationRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, agent, first := coldPaidPreparation(t)
	second := first
	second.CommandID = f.id()
	arrived, release := stopColdHistory(f.service, needContextTask)
	defer release()
	done := make(chan error, 1)
	go func() { _, _, err := agent.AdmitPaid(ctx, first); done <- err }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("paid admission did not suspend: %v", err)
	}
	// Amendment 5 forbids a reservation before the pure phase completes
	// under an unchanged prefix: the suspended retry owns no in-use claim.
	winner, bound, err := agent.AdmitPaid(ctx, second)
	if err != nil || bound == nil {
		t.Fatalf("pure first round blocked its retry: %+v %v", winner, err)
	}
	release()
	if err := <-done; err != ErrNoEffect {
		t.Fatalf("consumed preparation retry: %v, want ErrNoEffect", err)
	}
	view := f.view(t, first.AttemptID)
	budget := f.budget(t, view.Admission.AdmissionPeriod)
	if view.State != "reserved" || view.Version != 1 || view.HeldMicros != "120" || budget.HeldMicros != "120" || budget.SettledMicros != "0" || f.store.Durable() != winner.Sequence {
		t.Fatalf("cold retry acquired multiple holds: %+v %+v", view, budget)
	}
}

func testColdPaidPreparationFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, agent, request := coldPaidPreparation(t)
	readErr := errors.New("cold paid preparation read")
	f.service.deps.historyResolveError = func(kind historyNeedKind) error {
		if kind == needContextTask {
			return readErr
		}
		return nil
	}
	before := f.store.Durable()
	if _, _, err := agent.AdmitPaid(ctx, request); err != readErr || f.store.Durable() != before {
		t.Fatalf("failed read changed its error or committed: %v", err)
	}
	f.service.deps.historyResolveError = nil
	// Amendment 5 forbids a reservation before the pure phase completes
	// under an unchanged prefix: an unsuccessful read cannot strand inUse.
	receipt, bound, err := agent.AdmitPaid(ctx, request)
	if err != nil || bound == nil {
		t.Fatalf("failed read stranded its preparation: %+v %v", receipt, err)
	}
	view := f.view(t, request.AttemptID)
	budget := f.budget(t, view.Admission.AdmissionPeriod)
	if view.State != "reserved" || view.Version != 1 || view.HeldMicros != "120" || budget.HeldMicros != "120" || budget.SettledMicros != "0" || f.store.Durable() != receipt.Sequence {
		t.Fatalf("read retry changed one-hold accounting: %+v %+v", view, budget)
	}
}

func testColdCredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLivePaidFixture(t)
	owner := bindingOf(f.origin)
	current, err := f.service.CredentialScopeStatus(ctx, CredentialScopeLookup{ScopeID: f.credential.ScopeID})
	if err != nil {
		t.Fatal(err)
	}
	request := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: current.Version, Provider: "anthropic", CredentialVersion: paidGoldenID(0x404), SourceKind: "environment", SourceLocator: paidTestEnvironment}
	arrived, release := stopColdHistory(f.service, needCredential)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := f.service.BindCredential(ctx, owner, request); done <- err }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("credential identity did not suspend: %v", err)
	}
	winner := request
	winner.CommandID, winner.CredentialVersion = f.id(), f.id()
	receipt, err := f.service.BindCredential(ctx, owner, winner)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, ErrRevision) || f.store.Durable() != receipt.Sequence {
		t.Fatalf("cold credential bypassed current scope CAS: %v", err)
	}
	request.ExpectedVersion++
	retry, err := f.service.BindCredential(ctx, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.service.CredentialScopeStatus(ctx, CredentialScopeLookup{ScopeID: f.credential.ScopeID})
	found := false
	for _, version := range view.ActiveVersions {
		found = found || version == request.CredentialVersion
	}
	if err != nil || !found || view.Version != current.Version+2 || f.store.Durable() != retry.Sequence {
		t.Fatalf("refused cold bind consumed its unused credential ID: %+v %v", view, err)
	}
}

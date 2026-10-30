package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func newMixedPaidFixture(t *testing.T, deps serviceDeps, period string) (*paidFixture, *paidFixture) {
	t.Helper()
	f := newLivePaidFixturePeriod(t, period, deps)
	f.liveProfile(t, func(p *providers.Profile) {
		p.ContextMax, p.InputMax, p.OutputMax = 200000, 200000, 64000
		p.TariffVersion += "-haiku-vector"
		for i := range p.Tariff.Rates {
			p.Tariff.Rates[i].Numerator, p.Tariff.Rates[i].Denominator = "1", "1"
			if p.Tariff.Rates[i].Class == providers.Generated {
				p.Tariff.Rates[i].Numerator = "5"
			}
		}
	})
	binding := bindingOf(f.origin)
	b := CredentialBinding{CommandID: f.id(), ScopeID: f.id(), Provider: "openai", CredentialVersion: f.id(), SourceKind: "environment", SourceLocator: paidTestEnvironment}
	if _, err := f.service.BindCredential(context.Background(), binding, b); err != nil {
		t.Fatal(err)
	}
	p := paidOpenAIProfile(providerProfile(f.route.Profile))
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	route := f.id()
	if _, err = f.service.ConfigurePaidRoute(context.Background(), binding, PaidRouteConfiguration{CommandID: f.id(), RouteID: route, Enabled: true, CredentialScopeID: b.ScopeID, Profile: raw}); err != nil {
		t.Fatal(err)
	}
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes = 1 << 30
	for _, scope := range []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}, {Kind: "context", ID: f.origin.ContextID}} {
		if _, err := f.service.ConfigureCapacity(context.Background(), binding, CapacitySpec{CommandID: f.id(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
	}
	f.grant = f.id()
	spec := GrantSpec{GrantID: f.grant, Grantee: binding, Operations: []string{"grant.issue", "model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}, {Kind: "external", ResourceKey: "paid-route/" + route.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: capacity, DelegationDepth: 4, Lifetime: "until_revoked"}
	if _, err := f.service.IssueGrant(context.Background(), binding, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}, {Kind: "credential", ID: f.credential.ScopeID}, {Kind: "credential", ID: b.ScopeID}, {Kind: "grant", ID: f.grant}} {
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100", PeriodKind: period})
	}
	other := *f
	other.route = f.service.p.paidRoutes[route]
	other.credential = f.service.p.paidCredentials[b.CredentialVersion]
	other.next += 0x100000 // Disjoint synthetic command identities in the same book.
	return f, &other
}

func paidMixedPrepare(t *testing.T, f *paidFixture) PaidAdmission {
	t.Helper()
	if f.route.Profile.Provider == "openai" {
		return paidOpenAIPrepare(t, f, 16)
	}
	input := f.input()
	input.Request.MaxTokens = 16
	p, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: p}
}

func TestPaidMixedProviderScopes(t *testing.T) {
	t.Parallel()
	// A per-provider wallet or omitted common scope admits both fixed quotes;
	// independent scope rows prevent another cap from hiding that bug.
	for _, kind := range []string{"colony", "agent", "grant"} {
		t.Run("ceiling-"+kind, func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(http.ResponseWriter, *http.Request) { t.Error("admission sent a request") })
			a, o := newMixedPaidFixture(t, paidServerDeps(server, nil), "utc_day")
			root := a.grant
			a.grant = a.child(t, root, 1000000)
			o.grant = o.child(t, root, 1000000)
			scope := ledger.PaidScope{Kind: kind}
			if kind == "agent" {
				scope.ID = a.origin.AgentID
			}
			if kind == "grant" {
				scope.ID = root
			}
			a.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.430591", PeriodKind: "utc_day"})
			requests := []PaidAdmission{paidMixedPrepare(t, a), paidMixedPrepare(t, o)}
			if requests[0].Prepared.state.admission.Quote.MaximumMicros != 200080 || requests[1].Prepared.state.admission.Quote.MaximumMicros != 230512 {
				t.Fatal("fixed quotes changed")
			}
			start := make(chan struct{})
			type result struct {
				index int
				err   error
			}
			done := make(chan result, 2)
			for i, r := range requests {
				go func() {
					<-start
					_, _, err := scopedBound(t, a.service, bindingOf(a.origin)).AdmitPaid(context.Background(), r)
					done <- result{i, err}
				}()
			}
			close(start)
			admitted, refused, want := 0, 0, int64(0)
			for range 2 {
				got := <-done
				if got.err == nil {
					admitted++
					want = requests[got.index].Prepared.state.admission.Quote.MaximumMicros
				} else {
					var busy *BusyError
					if !errors.As(got.err, &busy) {
						t.Fatalf("not a capacity refusal: %v", got.err)
					}
					refused++
				}
			}
			view, err := a.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: scope})
			if err != nil || admitted != 1 || refused != 1 || view.HeldMicros != strconv.FormatInt(want, 10) || view.SettledMicros != "0" {
				t.Fatal("shared ceiling double-spent or lost a hold")
			}
			// An unrelated colony using the same source owns a separate allocation.
			other := newOpenAIPaidFixture(t, paidServerDeps(server, nil))
			other.admit(t, paidOpenAIPrepare(t, other, 16))
			if other.budget(t, "").HeldMicros != "230512" || a.budget(t, "").HeldMicros != strconv.FormatInt(want, 10) {
				t.Fatal("colony-local credential allocations were conflated")
			}
		})
	}
	t.Run("global-exchanges", func(t *testing.T) {
		t.Parallel()
		// Serial secret borrowing, separate active pools or a second monetary
		// reservation at dispatch would prevent this exact three-request set.
		entered, release := make(chan string, 4), make(chan struct{})
		var posts atomic.Int64
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model := paidRequestModel(t, r)
			entered <- r.URL.Path
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if r.URL.Path == "/v1/responses" {
				_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", paidOpenAIUsage(20, 4, 8, 16), paidOpenAIText, "", 1))
			} else {
				_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":7,"output_tokens":4}`, `{"output_tokens":4}`, "answer", "end_turn", "", true))
			}
		})
		a, o := newMixedPaidFixture(t, paidServerDeps(server, nil), "utc_day")
		owners := []*paidFixture{o, o, a, a}
		requests := make([]PaidAdmission, 4)
		dispatches := make([]PaidDispatch, 4)
		for i, f := range owners {
			requests[i] = paidMixedPrepare(t, f)
			f.admit(t, requests[i])
			dispatches[i] = PaidDispatch{CommandID: f.id(), AttemptID: requests[i].AttemptID, ExpectedVersion: 1}
		}
		type result struct {
			index int
			out   PaidDispatchOutcome
			err   error
		}
		done := make(chan result, 3)
		for i := range 3 {
			go func() {
				out, err := a.service.DispatchPaid(context.Background(), dispatches[i])
				done <- result{i, out, err}
			}()
		}
		counts := map[string]int{}
		for range 3 {
			select {
			case path := <-entered:
				counts[path]++
			case got := <-done:
				t.Fatalf("exchange ended before barrier: %v", got.err)
			}
		}
		if counts["/v1/responses"] != 2 || counts["/v1/messages"] != 1 {
			t.Fatal("shared credential serialized OpenAI exchanges")
		}
		before := a.store.Durable()
		_, err := a.service.DispatchPaid(context.Background(), dispatches[3])
		var busy *BusyError
		if !errors.As(err, &busy) || busy.Reason != "paid_active" || a.store.Durable() != before || posts.Load() != 3 {
			t.Fatal("fourth exchange escaped shared active limit")
		}
		close(release)
		for range 3 {
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			paidRecord(t, owners[got.index], dispatches[got.index], got.out)
		}
		if book := a.budget(t, ""); book.SettledMicros != "47" || book.HeldMicros != "200080" {
			t.Fatal("mixed exchanges double-charged or released unsent hold")
		}
	})
	for _, period := range []string{"utc_day", "utc_month"} {
		t.Run("original-period-"+period, func(t *testing.T) {
			t.Parallel()
			// Unknown holds cross period boundaries; duplicate results and
			// predecessor-bound corrections alter the original period once.
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if r.URL.Path == "/v1/responses" {
					_, _ = io.WriteString(w, paidOpenAIEvent(model, "in_progress", "null", "[]", "", 1))
				} else {
					_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":7,"output_tokens":1}`, `{}`, "", "end_turn", "", false))
				}
			})
			a, o := newMixedPaidFixture(t, paidServerDeps(server, nil), period)
			owners := []*paidFixture{a, o}
			var dispatches []PaidDispatch
			var observations []PaidDispatchOutcome
			original := ""
			for _, f := range owners {
				r := paidMixedPrepare(t, f)
				_, bound := f.admit(t, r)
				admission, _ := bound.Admission()
				original = admission.AdmissionPeriod
				d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
				out, err := f.service.DispatchPaid(context.Background(), d)
				if err != nil {
					t.Fatal(err)
				}
				paidRecord(t, f, d, out)
				dispatches = append(dispatches, d)
				observations = append(observations, out)
			}
			if a.budget(t, "").HeldMicros != "430592" {
				t.Fatal("unknowns lost their shared holds")
			}
			next := time.Unix(0, a.service.p.reviewFloor).UTC().AddDate(0, 1, 1).UnixNano()
			a.service.deps.reviewClock = func() (int64, error) { return next, nil }
			a.liveProfile(t, func(p *providers.Profile) {
				p.VerifiedUTCNS, p.ExpiresUTCNS = next-1, next+int64(7*24*time.Hour)
				p.TariffVersion += "-later"
				for i := range p.Tariff.Rates {
					p.Tariff.Rates[i].Numerator = "100"
				}
			})
			a.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.430591", PeriodKind: period})
			if book := a.budget(t, ""); book.Period == original || book.HeldMicros != "430592" || book.SettledMicros != "0" {
				t.Fatal("rollover or lower ceiling freed unknown exposure")
			}
			// No live authority is consulted for late facts, even after expiry.
			a.service.deps.reviewClock = func() (int64, error) { t.Error("factual result sampled clock"); return 0, ErrPaidClock }
			for i, f := range owners {
				late := paidLateEvidence(t, f, observations[i], func(o *paidObservationObject) {
					o.Complete = true
					o.Model = f.route.Profile.ModelID
					o.ErrorClass = ""
					o.StopReason = "end_turn"
					// Attest a complete late text block, like the existing
					// Anthropic late-evidence fixtures. An empty selected list
					// is not a version-1 paid result's message-object encoding.
					scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
					if err != nil {
						t.Fatal(err)
					}
					attempt := paidAttempt{admission: f.view(t, dispatches[i].AttemptID).Admission, origin: f.origin, profile: f.route.Profile}
					o.MessageObjects, err = f.service.publishPaidOutputs(scope, attempt, []providers.OutputBlock{{Type: "text", Text: "Synthetic late answer."}})
					if err != nil {
						t.Fatal(err)
					}
					o.ContentBlocks, o.OutputStarted = 1, true
					o.RawUsageFinal = json.RawMessage(`{"input_tokens":7,"output_tokens":4}`)
					o.RawUsageMerged = providers.Usage{"input_tokens": json.RawMessage(`7`), "output_tokens": json.RawMessage(`4`)}
					if f.route.Profile.Provider == "openai" {
						o.StopReason = "completed"
						o.ServedTier = json.RawMessage(`"default"`)
						o.RawUsageFinal = json.RawMessage(paidOpenAIUsage(20, 4, 8, 16))
						o.RawUsageMerged = providers.Usage{"input_tokens": json.RawMessage(`999999`)}
					}
				})
				r := PaidObservation{CommandID: f.id(), AttemptID: dispatches[i].AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}
				receipt, err := f.service.RecordPaidObservation(context.Background(), r)
				if err != nil {
					t.Fatal(err)
				}
				before := f.store.Durable()
				again, err := f.service.RecordPaidObservation(context.Background(), r)
				if err != nil || again.Sequence != receipt.Sequence || f.store.Durable() != before {
					t.Fatal("duplicate late result released twice")
				}
				v := f.view(t, r.AttemptID)
				lateOut := PaidDispatchOutcome{ObservedPaidCall: &ObservedPaidCall{service: f.service, attempt: r.AttemptID, origin: f.origin, evidence: late}}
				correction := paidLateEvidence(t, f, lateOut, func(o *paidObservationObject) {
					if f.route.Profile.Provider == "openai" {
						o.RawUsageFinal = json.RawMessage(paidOpenAIUsage(30, 4, 8, 16))
					} else {
						o.RawUsageMerged["input_tokens"] = json.RawMessage(`9`)
					}
				})
				c := PaidCorrection{PaidObservation: PaidObservation{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 4, TrustedEvidence: &correction}, PreviousResultSequence: int64(v.ResultSequence), PreviousEvidenceSHA256: v.Result.EvidenceSHA256}
				bad := c
				bad.PreviousResultSequence--
				before = f.store.Durable()
				if _, err = f.service.CorrectPaidSettlement(context.Background(), bad); err == nil || f.store.Durable() != before {
					t.Fatal("stale predecessor corrected money")
				}
				receipt, err = f.service.CorrectPaidSettlement(context.Background(), c)
				if err != nil {
					t.Fatal(err)
				}
				before = f.store.Durable()
				again, err = f.service.CorrectPaidSettlement(context.Background(), c)
				if err != nil || again.Sequence != receipt.Sequence || f.store.Durable() != before {
					t.Fatal("correction delta applied twice")
				}
			}
			if book := a.budget(t, original); book.SettledMicros != "40" || book.HeldMicros != "0" {
				t.Fatalf("original period cost=%s hold=%s", book.SettledMicros, book.HeldMicros)
			}
			if book := a.budget(t, ""); book.SettledMicros != "0" || book.HeldMicros != "0" {
				t.Fatal("late facts charged current period")
			}
			reopenScoped(t, a.serviceFixture, DefaultLimits())
			if a.budget(t, original).SettledMicros != "40" {
				t.Fatal("replay changed original-period corrections")
			}
		})
	}
	for _, overrun := range []string{"input", "cost"} {
		t.Run("intersecting-fence-"+overrun, func(t *testing.T) {
			t.Parallel()
			// An actual input overrun below the quote still fences both providers;
			// acknowledgement retires the old profile without erasing the charge.
			usage, cost := paidOpenAIUsage(922001, 0, 0, 16), "184413"
			if overrun == "cost" {
				usage, cost = paidOpenAIUsage(922000, 0, 922000, 17), "230513"
			}
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { paidOpenAIReply(t, w, r, usage) })
			a, o := newMixedPaidFixture(t, paidServerDeps(server, nil), "utc_day")
			_, d, out := paidOpenAIRun(t, o, 16)
			paidRecord(t, o, d, out)
			v := o.view(t, d.AttemptID)
			if v.SettledMicros != cost || !v.Fenced {
				t.Fatal("actual input was clamped or not fenced")
			}
			for _, f := range []*paidFixture{a, o} {
				input := f.input()
				input.Request.MaxTokens = 16
				var p *PreparedPaidCall
				var err error
				before := f.store.Durable()
				if f.route.Profile.Provider == "openai" {
					p, err = f.service.PrepareOpenAI(context.Background(), bindingOf(f.origin), OpenAIInput(input))
				} else {
					p, err = f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
				}
				if p != nil {
					_, _, err = scopedBound(t, f.service, bindingOf(f.origin)).AdmitPaid(context.Background(), PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: p})
					_ = p.Close()
				}
				if err == nil || f.store.Durable() != before {
					t.Fatal("intersecting provider bypassed overrun fence")
				}
			}
			ack := PaidOverrunAcknowledgement{CommandID: o.id(), AttemptID: d.AttemptID, ExpectedVersion: v.Version, ResultSequence: int64(v.ResultSequence), EvidenceSHA256: v.Result.EvidenceSHA256, RetiredProfileSHA256: v.Admission.ProfileSHA256}
			if _, err := o.service.AcknowledgePaidOverrun(context.Background(), bindingOf(o.origin), ack); err != nil {
				t.Fatal(err)
			}
			if book := a.budget(t, ""); book.SettledMicros != cost || book.Fenced {
				t.Fatal("acknowledgement erased money or retained resolved fence")
			}
			a.admit(t, paidMixedPrepare(t, a))
			if a.budget(t, "").HeldMicros != "200080" {
				t.Fatal("resolved intersection still blocked other provider")
			}
		})
	}
}

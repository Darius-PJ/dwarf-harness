package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// serviceBindCredential binds a new version at the fixture's scope; revoke
// names the version it retires, or zero for a parallel binding.
func serviceBindCredential(t *testing.T, l *serviceLive, revoke uuid.UUID) ledger.RuntimeCredentialChanged {
	t.Helper()
	var expected int64
	for _, c := range l.service.p.paidCredentials {
		if c.ScopeID == l.credential.ScopeID {
			expected = max(expected, c.Version)
		}
	}
	version := uuid.NewV7()
	if _, err := l.service.BindCredential(context.Background(), bindingOf(l.origin), CredentialBinding{CommandID: uuid.NewV7(), ScopeID: l.credential.ScopeID, ExpectedVersion: expected, Provider: l.route.Profile.Provider, CredentialVersion: version, SourceKind: "environment", SourceLocator: paidTestEnvironment, RevokePreviousVersion: revoke}); err != nil {
		t.Fatal(err)
	}
	return l.service.p.paidCredentials[version]
}

// serviceReopenLive closes and reopens the live service over the same store.
// A new incarnation holds no credential secret, so it binds a replacement
// version for later calls.
func serviceReopenLive(t *testing.T, l *serviceLive) {
	t.Helper()
	deps := l.service.deps
	if err := l.service.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	l.service, _, err = openPaidService(context.Background(), l.store, l.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	l.credential = serviceBindCredential(t, l, l.credential.CredentialVersion)
}

// TestServiceRestoration: only the member's own qualifying completion after a
// current clear or ceased-applying witness restores it.
func TestServiceRestoration(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, provider string
		restores       bool
		handler        func(*testing.T) http.HandlerFunc
		observe        TextObserver
	}{
		{"terminal_max_tokens", "anthropic", true, func(t *testing.T) http.HandlerFunc { return serviceAnthropicStop(t, "max_tokens") }, nil},
		{"refusal", "anthropic", false, func(t *testing.T) http.HandlerFunc { return serviceAnthropicStop(t, "refusal") }, nil},
		{"content_error_usage", "anthropic", false, func(t *testing.T) http.HandlerFunc { return serviceSuccess(t, "anthropic") }, func(string) error { return errors.New("synthetic observer refusal") }},
		{"substituted_capture", "anthropic", false, func(t *testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, `{"input_tokens":7,"output_tokens":4}`, "synthetic-paid-integration-value", "end_turn", "", true)))
			}
		}, nil},
		{"openai_tariff_ambiguous", "openai", true, func(t *testing.T) http.HandlerFunc {
			return serviceOpenAIStatus(t, "completed", paidOpenAIUsage(272001, 0, 0, 16), "")
		}, nil},
		{"openai_incomplete", "openai", false, func(t *testing.T) http.HandlerFunc {
			return serviceOpenAIStatus(t, "incomplete", paidOpenAIUsage(7, 0, 0, 4), `,"incomplete_details":{"reason":"max_output_tokens"}`)
		}, nil},
		{"openai_failed", "openai", false, func(t *testing.T) http.HandlerFunc {
			return serviceOpenAIStatus(t, "failed", paidOpenAIUsage(7, 0, 0, 4), "")
		}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := newServiceLive(t, row.provider)
			l.serviceLostByDenial(t)
			l.respond(row.handler(t))
			c := l.callWith(t, context.Background(), row.observe)
			if c.err != nil {
				t.Fatal(c.err)
			}
			if got := l.status(t).State == "available"; got != row.restores {
				t.Fatalf("restoration = %v, want %v", got, row.restores)
			}
			v := l.view(t, c.admission.AttemptID)
			switch row.name {
			case "openai_tariff_ambiguous":
				if v.Result == nil || v.Result.Kind != "unknown" || v.HeldMicros != strconv.FormatInt(v.Admission.Quote.MaximumMicros, 10) {
					t.Fatalf("tariff-ambiguous money changed by restoration: %+v", v)
				}
			case "terminal_max_tokens", "content_error_usage":
				if v.Result == nil || v.Result.Kind != "usage" {
					t.Fatalf("native result changed: %+v", v.Result)
				}
			}
		})
	}
	for _, left := range []string{"", "binding"} {
		name := "all_work_moved"
		if left != "" {
			name += "_" + left + "_left"
		}
		t.Run(name, func(t *testing.T) { t.Parallel(); testServiceAllWorkMoved(t, left) })
	}
	t.Run("other_credential_version", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		refused := l.credential.CredentialVersion
		l.serviceLostByDenial(t)
		l.respond(serviceSuccess(t, "anthropic"))
		l.credential = serviceBindCredential(t, l, uuid.UUID{})
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		if l.status(t).State != "lost" {
			t.Fatal("a parallel credential version restored the refused credential's member")
		}
		l.credential = serviceBindCredential(t, l, refused)
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		if v := l.status(t); v.State != "available" {
			t.Fatalf("credential replacement followed by success did not restore: %+v", v)
		}
	})
	for _, row := range []string{"integrity_same_incarnation", "integrity_verified_after_reopen", "integrity_unrepaired_reopen", "integrity_recurs_before_success"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			l := newServiceLive(t, "anthropic")
			repair := serviceIntegrityLoss(t, l)
			reports := &ServiceHostReports{}
			count := func() int { return len(serviceObservations(t, l.service)) }
			switch row {
			case "integrity_same_incarnation":
				repair()
				before := count()
				if _, err := l.service.ReconcileServiceEvidence(context.Background(), reports); err != nil {
					t.Fatal(err)
				}
				if count() != before || l.status(t).State != "lost" {
					t.Fatal("a repair seen only on disk cleared the cause in its own incarnation")
				}
			case "integrity_unrepaired_reopen":
				serviceReopenLive(t, l)
				before := count()
				if _, err := l.service.ReconcileServiceEvidence(context.Background(), reports); err != nil {
					t.Fatal(err)
				}
				if count() != before || l.status(t).State != "lost" {
					t.Fatal("an unrepaired scope was verified")
				}
			default:
				repair()
				serviceReopenLive(t, l)
				before := l.store.Durable()
				if _, err := l.service.ReconcileServiceEvidence(context.Background(), nil); err != nil || l.store.Durable() != before {
					t.Fatalf("drain-only reconcile wrote a clear: %v", err)
				}
				if _, err := l.service.ReconcileServiceEvidence(context.Background(), reports); err != nil {
					t.Fatal(err)
				}
				if serviceCountRecords(t, l.service, ledger.TypeRuntimeServiceCauseChanged) != 2 || l.status(t).State != "lost" {
					t.Fatal("reports reconcile did not write exactly one clear without restoring")
				}
				clears := 0
				for _, e := range serviceObservations(t, l.service) {
					if e.Source.Reason == "integrity_verified" {
						clears++
					}
				}
				if clears != 1 {
					t.Fatalf("integrity_verified clears: %d", clears)
				}
				after := l.store.Durable()
				if _, err := l.service.ReconcileServiceEvidence(context.Background(), reports); err != nil || l.store.Durable() != after {
					t.Fatalf("second reports reconcile appended: %v", err)
				}
				if row == "integrity_recurs_before_success" {
					serviceCorruptMemory(t, l)
					// The live write inspects the scope and finds a new issue.
					_, _ = l.service.WritePrivate(context.Background(), PrivateWrite{Binding: bindingOf(l.origin), CommandID: uuid.NewV7(), Scope: "context", ExpectedVersion: l.service.p.scopes[scopeKey{l.origin.AgentID, l.origin.ContextID}].revision, Changes: []Change{{"handoff", "replace", []byte("next")}}})
				}
				l.respond(serviceSuccess(t, "anthropic"))
				if c := l.call(t); c.err != nil {
					t.Fatal(c.err)
				}
				want := "available"
				if row == "integrity_recurs_before_success" {
					want = "lost"
				}
				if v := l.status(t); v.State != want {
					t.Fatalf("after success: %+v, want %s", v, want)
				}
			}
		})
	}
}

// serviceCorruptMemory replaces the context's memory materialization with
// foreign bytes.
func serviceCorruptMemory(t *testing.T, l *serviceLive) {
	t.Helper()
	p, err := l.service.scopePath(submissionIDs{l.origin.AgentID, l.origin.SessionID, l.origin.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	file, _ := p.markdownPath("memory")
	writeTestFile(t, file, []byte("foreign"))
}

// serviceIntegrityLoss writes context memory, plants a foreign
// materialization, reopens so Open records the scope issue, then has a turn
// admission refused by it. It returns the on-disk repair.
func serviceIntegrityLoss(t *testing.T, l *serviceLive) func() {
	t.Helper()
	key := scopeKey{l.origin.AgentID, l.origin.ContextID}
	revision := l.service.p.scopes[key].revision
	if _, err := l.service.WritePrivate(context.Background(), PrivateWrite{Binding: bindingOf(l.origin), CommandID: uuid.NewV7(), Scope: "context", ExpectedVersion: revision, Changes: []Change{{"memory", "replace", []byte("current")}}}); err != nil {
		t.Fatal(err)
	}
	p, err := l.service.scopePath(submissionIDs{l.origin.AgentID, l.origin.SessionID, l.origin.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	file, _ := p.markdownPath("memory")
	serviceCorruptMemory(t, l)
	serviceReopenLive(t, l)
	agent := scopedBound(t, l.service, bindingOf(l.origin))
	turn := uuid.NewV7()
	if _, err := agent.StartTurn(context.Background(), TurnAdmission{CommandID: uuid.NewV7(), GrantID: l.inspect, TurnID: turn}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("issue did not refuse the turn admission: %v", err)
	}
	es := serviceObservations(t, l.service)
	if len(es) != 1 || es[0].Source.Kind != "integrity_refusal" || es[0].Source.TurnID != turn || es[0].Cause.PrivateScope != "context" || es[0].Cause.ContextID != l.origin.ContextID || l.status(t).State != "lost" {
		t.Fatalf("refused admission did not record its integrity loss: %+v", es)
	}
	return func() { writeTestFile(t, file, []byte("current")) }
}

// testServiceRetainedEvidence: a definite E append failure keeps the live
// source for this incarnation only. Here both the call's own E and the paid
// path's immediate retry fail; the next catch-up retries it once, and a new
// incarnation never fabricates it (PM answer 7).
func testServiceRetainedEvidence(t *testing.T, reopen bool) {
	l := newServiceLive(t, "anthropic")
	l.serviceLostByDenial(t)
	l.respond(serviceSuccess(t, "anthropic"))
	var failures atomic.Int32
	failures.Store(2)
	l.service.deps.beforeServiceSubmit = func() error {
		if failures.Add(-1) >= 0 {
			return errors.New("synthetic definite append failure")
		}
		return nil
	}
	c := l.call(t)
	if c.err != nil {
		t.Fatal(c.err)
	}
	native := l.view(t, c.admission.AttemptID)
	if v := l.status(t); native.Result == nil || native.Result.Kind != "usage" || v.State != "lost" || v.Recovery.LastErrorClass == "" || len(serviceObservations(t, l.service)) != 1 {
		t.Fatalf("definite E failure changed the native result or hid the condition: %+v", v)
	}
	if reopen {
		serviceReopenLive(t, l)
	}
	if _, err := l.service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	es := serviceObservations(t, l.service)
	if reopen {
		if len(es) != 1 || l.status(t).State != "lost" {
			t.Fatal("a new incarnation invented the lost source")
		}
	} else {
		e := es[len(es)-1]
		if len(es) != 2 || e.Source.Completion == nil || e.Source.Completion.NativeResultSequence == nil || uint64(*e.Source.Completion.NativeResultSequence) != native.ResultSequence || l.status(t).State != "available" {
			t.Fatalf("next drain did not record E with the native result: %+v", e.Source)
		}
	}
	through := l.store.Durable()
	if _, err := l.service.ReconcileServiceEvidence(context.Background(), nil); err != nil || l.store.Durable() != through {
		t.Fatalf("retained source retried twice: %v", err)
	}
}

// testServiceSameHandleRetry: a financial append refused by the store leaves
// the handle usable; its retry records the one E and one result.
func testServiceSameHandleRetry(t *testing.T) {
	l := newServiceLive(t, "anthropic")
	l.serviceLostByDenial(t)
	l.respond(serviceSuccess(t, "anthropic"))
	r := paidPrepare(t, l.paidFixture)
	l.admit(t, r)
	out, err := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	var refuse atomic.Bool
	refuse.Store(true)
	l.service.deps.beforeSubmit = func(drafts []ledger.Draft) []ledger.Draft {
		for _, d := range drafts {
			if d.Type == ledger.TypeRuntimePaidResult && refuse.CompareAndSwap(true, false) {
				return nil
			}
		}
		return drafts
	}
	request := PaidObservation{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall}
	if _, err := l.service.RecordPaidObservation(context.Background(), request); err == nil {
		t.Fatal("refused financial append succeeded")
	}
	request.CommandID = uuid.NewV7()
	if _, err := l.service.RecordPaidObservation(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	successes := 0
	for _, e := range serviceObservations(t, l.service) {
		if e.Source.Kind == "successful_call" {
			successes++
		}
	}
	if successes != 1 || l.status(t).State != "available" || l.posts.Load() != 2 {
		t.Fatalf("same-handle retry: successes=%d state=%s posts=%d", successes, l.status(t).State, l.posts.Load())
	}
}

// serviceRouteOnNewScope binds a credential at a new scope, configures a route
// on it with the fixture's profile, grants the origin binding paid calls on it
// and returns the fixture's loop setting moved onto that route and scope.
func serviceRouteOnNewScope(t *testing.T, f *turnFixture) LoopContextSettings {
	t.Helper()
	ctx, owner := context.Background(), bindingOf(f.origin)
	scope := uuid.NewV7()
	if _, err := f.service.BindCredential(ctx, owner, CredentialBinding{CommandID: uuid.NewV7(), ScopeID: scope, ExpectedVersion: 0, Provider: f.route.Profile.Provider, CredentialVersion: uuid.NewV7(), SourceKind: "environment", SourceLocator: paidTestEnvironment}); err != nil {
		t.Fatal(err)
	}
	profile := providerProfile(f.route.Profile)
	profile.CapabilityVersion += "-moved"
	raw, err := profile.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	routeID := uuid.NewV7()
	if _, err := f.service.ConfigurePaidRoute(ctx, owner, PaidRouteConfiguration{CommandID: uuid.NewV7(), RouteID: routeID, ExpectedVersion: 0, Enabled: true, CredentialScopeID: scope, Profile: raw}); err != nil {
		t.Fatal(err)
	}
	grant := uuid.NewV7()
	if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grant, Grantee: owner, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + routeID.String()}}, DataClass: "external", Recipients: []Binding{owner}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked"}}); err != nil {
		t.Fatal(err)
	}
	for _, spend := range []ledger.PaidScope{{Kind: "credential", ID: scope}, {Kind: "grant", ID: grant}} {
		f.liveSpend(t, spend, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
	}
	setting := f.setting
	setting.RouteID, setting.ExpectedRouteVersion, setting.CredentialScopeID = routeID, f.service.p.paidRoutes[routeID].Version, scope
	return setting
}

// testServiceAllWorkMoved: a provider cause on credential scope A, then a
// qualifying success on scope B. The member is restored by all_work_moved
// only when no configured binding and no admitted, undispatched attempt of
// the agent is left on A; with a binding left the permit is blocked.
func testServiceAllWorkMoved(t *testing.T, left string) {
	var fail atomic.Bool
	fail.Store(true)
	denied := serviceProviderError("anthropic", http.StatusUnauthorized, "authentication_error", nil)
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			denied(w, r)
			return
		}
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer on the moved scope"))
	}, nil)
	// The failure on scope A: a direct call from the agent's other context.
	other := serviceWorkView(t, f.paidFixture, serviceOtherContext(t, f.paidFixture), 1)
	r := paidPrepare(t, other)
	other.admit(t, r)
	out, err := f.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
	if err == nil {
		_, err = f.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall})
	}
	if err != nil {
		t.Fatal(err)
	}
	status := func() string {
		v, err := f.service.ServiceStatus(context.Background(), f.origin.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		return v.State
	}
	if status() != "lost" {
		t.Fatal("the denial on scope A did not lose the member")
	}
	fail.Store(false)
	onA := f.setting
	f.setting = serviceRouteOnNewScope(t, f)
	var extra []LoopContextSettings
	if left == "binding" {
		onA.Binding = bindingOf(other.origin)
		extra = append(extra, onA)
	}
	f.enable(t, extra...)
	_, start := f.send(t)
	if result, _ := f.finished(t, start.TurnID); result.Outcome != "finished" {
		t.Fatalf("turn on scope B: %+v", result)
	}
	var successes []ledger.RuntimeServiceObserved
	for _, e := range serviceObservations(t, f.service) {
		if e.Source.Kind == "successful_call" {
			successes = append(successes, e)
		}
	}
	if left != "" {
		if status() != "lost" || len(successes) != 0 {
			t.Fatalf("all_work_moved selected with a %s still on the failed scope: state=%s successes=%d", left, status(), len(successes))
		}
		return
	}
	if status() != "available" || len(successes) != 1 {
		t.Fatalf("moved work did not restore: state=%s successes=%d", status(), len(successes))
	}
	src := successes[0].Source
	if src.Reason != "all_work_moved" || src.FailedWorkCount == nil || *src.FailedWorkCount != 0 || src.WorksetSHA256 == (ledger.Digest{}) {
		t.Fatalf("all_work_moved witness: reason=%s failed=%v digest=%v", src.Reason, serviceCount(src.FailedWorkCount), src.WorksetSHA256)
	}
}

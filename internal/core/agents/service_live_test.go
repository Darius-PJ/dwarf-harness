package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// TestServiceCauses drives every provider diagnosis through the real dispatch,
// capture and record path against a loopback provider.
func TestServiceCauses(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, provider, kind, code, scope, provenance string
		status                                        int
		headers                                       map[string]string
		grace, reset                                  bool
	}{
		{"credential_denied", "anthropic", "authentication_error", "credential_denied", "credential", "provider_response", 401, nil, false, false},
		{"scoped_denied", "anthropic", "permission_error", "scoped_denied", "account_route", "provider_response", 403, nil, false, false},
		{"quota_without_reset", "anthropic", "billing_error", "quota_exhausted", "account", "provider_response", 402, nil, false, false},
		{"quota_with_reset", "anthropic", "billing_error", "quota_exhausted", "account", "provider_response", 402, map[string]string{"Retry-After": "60"}, true, true},
		{"rate_limited", "anthropic", "rate_limit_error", "rate_limited", "account_route", "provider_response", 429, map[string]string{"Retry-After": "30"}, true, true},
		{"overloaded", "anthropic", "overloaded_error", "overloaded", "provider_route", "provider_response", 529, nil, true, false},
		{"openai_quota", "openai", "insufficient_quota", "quota_exhausted", "account", "provider_response", 429, nil, false, false},
		{"openai_overloaded", "openai", "server_is_overloaded", "overloaded", "provider_route", "provider_response", 503, nil, true, false},
		{"provider_error", "anthropic", "api_error", "", "", "", 500, nil, false, false},
		{"gateway_timeout", "anthropic", "timeout_error", "", "", "", 504, nil, false, false},
		{"invalid_request", "anthropic", "invalid_request_error", "", "", "", 400, nil, false, false},
		{"unavailable", "anthropic", "not_found_error", "", "", "", 404, nil, false, false},
		{"conflict", "anthropic", "conflict_error", "", "", "", 409, nil, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := newServiceLive(t, row.provider)
			l.respond(serviceProviderError(row.provider, row.status, row.kind, row.headers))
			c := l.call(t)
			if c.err != nil {
				t.Fatal(c.err)
			}
			es := serviceObservations(t, l.service)
			if row.code == "" {
				if len(es) != 0 || l.status(t).State != "available" {
					t.Fatalf("non-cause failure recorded service evidence: %d", len(es))
				}
				return
			}
			serviceCheckFailure(t, l, es, row.code, row.scope, row.provenance, row.grace, row.reset)
		})
	}
	t.Run("refused_connection", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		l.server.Close()
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		serviceCheckFailure(t, l, serviceObservations(t, l.service), "unreachable", "endpoint", "transport", true, false)
	})
	t.Run("reset_after_headers", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		entered, release := make(chan struct{}), make(chan struct{})
		l.respond(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
			w.(http.Flusher).Flush()
			close(entered)
			<-release
		})
		go func() { <-entered; l.resetConnections(); close(release) }()
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		serviceCheckFailure(t, l, serviceObservations(t, l.service), "unreachable", "endpoint", "transport", true, false)
	})
	t.Run("caller_cancel_mid_exchange", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		ctx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		defer close(release)
		l.respond(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
			w.(http.Flusher).Flush()
			cancel()
			<-release
		})
		l.callWith(t, ctx, nil)
		if n := len(serviceObservations(t, l.service)); n != 0 {
			t.Fatalf("caller cancellation became service evidence: %d", n)
		}
	})
	t.Run("local_deadline", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		serviceShortDispatch(t, l)
		release := make(chan struct{})
		defer close(release)
		l.respond(func(w http.ResponseWriter, r *http.Request) { <-release })
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		if n := len(serviceObservations(t, l.service)); n != 0 {
			t.Fatalf("local deadline became unreachable evidence: %d", n)
		}
	})
	t.Run("service_close_mid_exchange", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		entered, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		l.respond(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })
		r := paidPrepare(t, l.paidFixture)
		l.admit(t, r)
		done := make(chan PaidDispatchOutcome, 1)
		go func() {
			out, _ := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
			done <- out
		}()
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- l.service.Close() }()
		out := <-done
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if out.ObservedPaidCall != nil && out.ObservedPaidCall.provenance != "" {
			t.Fatal("closed exchange kept a service token")
		}
		var err error
		l.service, _, err = openPaidService(context.Background(), l.store, l.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, l.service.deps)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if n := len(serviceObservations(t, l.service)); n != 0 {
			t.Fatalf("closed exchange became service evidence: %d", n)
		}
	})
	t.Run("observer_refusal_and_secret_echo", func(t *testing.T) {
		t.Parallel()
		for _, mode := range []string{"observer_error", "secret_echo"} {
			l := newServiceLive(t, "anthropic")
			text := "answer"
			if mode == "secret_echo" {
				text = "echo synthetic-paid-integration-value"
			}
			l.respond(func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, `{"input_tokens":7,"output_tokens":4}`, text, "end_turn", "", true))
			})
			observe := func(string) error { return nil }
			if mode == "observer_error" {
				observe = func(string) error { return errors.New("synthetic observer refusal") }
			}
			c := l.callWith(t, context.Background(), observe)
			if c.err != nil {
				t.Fatal(c.err)
			}
			if v := l.view(t, c.admission.AttemptID); v.Result == nil || v.Result.Kind != "usage" {
				t.Fatalf("%s: native result changed: %+v", mode, v.Result)
			}
			if n := len(serviceObservations(t, l.service)); n != 0 {
				t.Fatalf("%s produced service evidence: %d", mode, n)
			}
		}
	})
	t.Run("substituted_rate_limit_capture", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		l.respond(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"synthetic-paid-integration-value"}}`)
		})
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		if n := len(serviceObservations(t, l.service)); n != 0 {
			t.Fatalf("substituted capture became rate-limit evidence: %d", n)
		}
	})
}

// serviceCheckFailure asserts exactly one paid_call E at the evidence-bounded
// scope, its onset, and the frozen grace presence.
func serviceCheckFailure(t *testing.T, l *serviceLive, es []ledger.RuntimeServiceObserved, code, scope, provenance string, grace, reset bool) {
	t.Helper()
	if len(es) != 1 {
		t.Fatalf("want one service observation, got %d", len(es))
	}
	e := es[0]
	src := e.Source
	if e.Cause == nil || e.Cause.Code != code || e.Cause.ScopeKind != scope || e.Cause.Provider != l.route.Profile.Provider || src.Kind != "paid_call" || src.Reason != code || src.Provenance != provenance || src.Origin == nil || *src.Origin != l.origin || src.Object == nil {
		t.Fatalf("wrong service observation: %+v %+v", e.Cause, src)
	}
	if (src.ResetUTCNS != nil) != reset || reset && *src.ResetUTCNS <= src.ObservedUTCNS {
		t.Fatalf("reset evidence mismatch: %v", src.ResetUTCNS)
	}
	authority := ledger.Digest(sha256.Sum256([]byte(l.route.Profile.Authority)))
	if (e.Cause.AuthoritySHA256 != ledger.Digest{}) && e.Cause.AuthoritySHA256 != authority || e.Cause.CredentialScopeID != (uuid.UUID{}) && e.Cause.CredentialScopeID != l.credential.ScopeID {
		t.Fatal("cause scope is not the failed request's sealed scope")
	}
	v := l.status(t)
	if grace {
		if v.State != "available" || len(v.Pending) != 1 || v.Pending[0].DueUTCNS == nil || *v.Pending[0].DueUTCNS != src.ObservedUTCNS+ledger.ServiceGraceNS {
			t.Fatalf("grace cause did not pend for its due: %+v", v)
		}
		return
	}
	if v.State != "lost" || len(v.Active) != 1 || v.Active[0].DueUTCNS != nil {
		t.Fatalf("immediate cause did not enter loss: %+v", v)
	}
}

// serviceShortDispatch bounds the exchange tightly so a held server reaches
// the owner's own local deadline; no test waits on wall time itself.
func serviceShortDispatch(t *testing.T, l *serviceLive) {
	t.Helper()
	l.service.mu.Lock()
	defer l.service.mu.Unlock()
	limits := l.service.paid.limits
	limits.DispatchTimeout = 200 * time.Millisecond
	transport, err := providers.NewTransportWithRoots(providers.Limits{MaxActivePaid: limits.MaxActivePaid, DispatchTimeout: limits.DispatchTimeout, TLSHandshakeTimeout: limits.TLSHandshakeTimeout, ResponseHeaderTimeout: limits.ResponseHeaderTimeout}, l.service.deps.paidTestRoots, l.service.deps.paidWrapConn)
	if err != nil {
		t.Fatal(err)
	}
	l.service.paid.transport.CloseIdleConnections()
	l.service.paid.limits = limits
	l.service.paid.transport = transport
}

// TestServiceNoAuthorityTransfer: service evidence never moves authority or
// restoration between handles, agents or attempts.
func TestServiceNoAuthorityTransfer(t *testing.T) {
	t.Parallel()
	t.Run("zero_or_foreign_handle", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		other := newServiceLive(t, "anthropic")
		l.respond(serviceProviderError("anthropic", 401, "authentication_error", nil))
		other.respond(serviceProviderError("anthropic", 401, "authentication_error", nil))
		r := paidPrepare(t, l.paidFixture)
		l.admit(t, r)
		if _, err := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1}); err != nil {
			t.Fatal(err)
		}
		r2 := paidPrepare(t, other.paidFixture)
		other.admit(t, r2)
		foreign, err := other.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r2.AttemptID, ExpectedVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		before := l.store.Durable()
		for _, handle := range []*ObservedPaidCall{{}, foreign.ObservedPaidCall} {
			if _, err := l.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 2, Observed: handle}); !errors.Is(err, ErrDenied) {
				t.Fatalf("zero or foreign handle accepted: %v", err)
			}
		}
		if l.store.Durable() != before || len(serviceObservations(t, l.service)) != 0 {
			t.Fatal("a refused handle wrote a record")
		}
	})
	t.Run("cross_agent_success", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		second := serviceSecondAgent(t, l)
		l.respond(serviceProviderError("anthropic", 401, "authentication_error", nil))
		l.callFor(t, l.paidFixture)
		l.callFor(t, second)
		statusOf := func(agent uuid.UUID) string {
			v, err := l.service.ServiceStatus(context.Background(), agent)
			if err != nil {
				t.Fatal(err)
			}
			return v.State
		}
		es := serviceObservations(t, l.service)
		if len(es) != 2 || statusOf(l.origin.AgentID) != "lost" || statusOf(second.origin.AgentID) != "lost" {
			t.Fatalf("shared credential refusal did not lose both agents: %d", len(es))
		}
		l.respond(serviceSuccess(t, "anthropic"))
		l.callFor(t, second)
		if statusOf(second.origin.AgentID) != "available" || statusOf(l.origin.AgentID) != "lost" {
			t.Fatal("another agent's completion restored this member")
		}
		l.callFor(t, l.paidFixture)
		if statusOf(l.origin.AgentID) != "available" {
			t.Fatal("the member's own completion after the clear did not restore")
		}
	})
	t.Run("agent_handle_refused", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		l.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000000", PeriodKind: "utc_day"})
		r := paidPrepare(t, l.paidFixture)
		var elsewhere Binding
		for context, agent := range l.service.p.contexts {
			if agent == l.origin.AgentID && context != l.origin.ContextID {
				elsewhere = Binding{AgentID: agent, ContextID: context}
			}
		}
		if elsewhere == (Binding{}) {
			t.Fatal("fixture has no second context")
		}
		before := l.store.Durable()
		if _, _, err := scopedBound(t, l.service, elsewhere).AdmitPaid(context.Background(), r); !errors.Is(err, ErrDenied) {
			t.Fatalf("another binding's Agent handle reached the call: %v", err)
		}
		if l.store.Durable() != before || len(serviceObservations(t, l.service)) != 0 {
			t.Fatal("a refused Agent handle reached the service producer")
		}
		// The call's own handle meets the same refusal and records its evidence.
		var busy *BusyError
		if _, _, err := scopedBound(t, l.service, bindingOf(l.origin)).AdmitPaid(context.Background(), r); !errors.As(err, &busy) || busy.Reason != "budget_exposure" {
			t.Fatalf("owning handle refusal changed: %v", err)
		}
		if es := serviceObservations(t, l.service); len(es) != 1 || es[0].Source.Kind != "money_refusal" {
			t.Fatalf("owning refusal did not record its money evidence: %d", len(es))
		}
	})
}

// serviceSecondAgent registers another agent that can make paid calls on the
// same route and credential, and returns a fixture view bound to it.
func serviceSecondAgent(t *testing.T, l *serviceLive) *paidFixture {
	t.Helper()
	ctx, owner := context.Background(), bindingOf(l.origin)
	origin := ledger.Origin{AgentID: uuid.NewV7(), SessionID: uuid.NewV7(), ContextID: uuid.NewV7()}
	b := bindingOf(origin)
	if _, err := l.service.RegisterAgent(ctx, AgentRegistration{CommandID: uuid.NewV7(), AgentID: origin.AgentID, SessionID: origin.SessionID, ContextID: origin.ContextID, Directory: "Scout-second", AgentMarkdown: []byte("# Instructions\r\n")}); err != nil {
		t.Fatal(err)
	}
	scope := ledger.ScopeKey{Kind: "agent", ID: origin.AgentID}
	if _, err := l.service.ConfigureCapacity(ctx, owner, CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(l.service.p.book.accounts[scope].version), Scope: scope, Limits: paidPhysicalCapacity()}); err != nil {
		t.Fatal(err)
	}
	second := *l.paidFixture
	second.origin, second.grant, second.turn = origin, uuid.NewV7(), uuid.NewV7()
	// Golden IDs come from a per-fixture counter; keep the views' sequences
	// apart so neither reuses the other's command IDs.
	second.next += 1 << 20
	turns := uuid.NewV7()
	for _, spec := range []GrantSpec{
		{GrantID: second.grant, Grantee: b, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + l.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{b}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked"},
		{GrantID: turns, Grantee: b, Operations: []string{"turn.start"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(b), Scope: "context", Slots: []string{}}}, DataClass: "private", Recipients: []Binding{b}, Limits: scopedCapacity(), Lifetime: "until_revoked"},
	} {
		if _, err := l.service.IssueGrant(ctx, owner, GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(l.service.p.authority), Spec: spec}); err != nil {
			t.Fatal(err)
		}
	}
	for _, spend := range []ledger.PaidScope{{Kind: "agent", ID: origin.AgentID}, {Kind: "grant", ID: second.grant}} {
		l.liveSpend(t, spend, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
	}
	if _, err := scopedBound(t, l.service, b).StartTurn(ctx, TurnAdmission{CommandID: uuid.NewV7(), GrantID: turns, TurnID: second.turn}); err != nil {
		t.Fatal(err)
	}
	return &second
}

// callFor makes one complete paid call for the given fixture view.
func (l *serviceLive) callFor(t *testing.T, f *paidFixture) {
	t.Helper()
	r := paidPrepare(t, f)
	f.admit(t, r)
	out, err := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
	if err == nil && out.ObservedPaidCall != nil {
		_, err = l.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall})
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestServiceCredentialHygiene: no credential value, locator, header or raw
// provider message enters an F3-91 record.
func TestServiceCredentialHygiene(t *testing.T) {
	t.Parallel()
	l := newServiceLive(t, "anthropic")
	l.serviceLostByDenial(t)
	l.respond(serviceSuccess(t, "anthropic"))
	if c := l.call(t); c.err != nil {
		t.Fatal(c.err)
	}
	if l.status(t).State != "available" {
		t.Fatal("hygiene fixture did not restore")
	}
	var raw strings.Builder
	if err := l.service.scan(context.Background(), 0, l.store.Durable(), func(c ledger.Commit) error {
		for _, r := range c.Records {
			if isServiceType(r.Type) {
				raw.Write(r.Payload)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"synthetic-paid-integration-value", paidTestEnvironment, "Synthetic refusal", "authentication_error", "request-one", l.route.Profile.Authority} {
		if strings.Contains(raw.String(), forbidden) {
			t.Fatalf("service record disclosed %q", forbidden)
		}
	}
}

// testServiceLiveSameHandleRace records one live handle twice concurrently,
// with one shared or two distinct command IDs: one E, one result, one
// restoration, no second POST.
func testServiceLiveSameHandleRace(t *testing.T, shared bool) {
	l := newServiceLive(t, "anthropic")
	l.serviceLostByDenial(t)
	l.respond(serviceSuccess(t, "anthropic"))
	r := paidPrepare(t, l.paidFixture)
	l.admit(t, r)
	out, err := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	commands := [2]uuid.UUID{uuid.NewV7(), uuid.NewV7()}
	if shared {
		commands[1] = commands[0]
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, command := range commands {
		go func() {
			<-start
			_, err := l.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: command, AttemptID: r.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall})
			errs <- err
		}()
	}
	close(start)
	succeeded := 0
	for range commands {
		if err := <-errs; err == nil {
			succeeded++
		} else if !errors.Is(err, ErrRevision) && !errors.Is(err, ErrCommandMismatch) && !errors.Is(err, ErrPaidSent) && !errors.Is(err, ErrDenied) {
			t.Fatalf("racing record failed unexpectedly: %v", err)
		}
	}
	successes := 0
	for _, e := range serviceObservations(t, l.service) {
		if e.Source.Kind == "successful_call" {
			successes++
		}
	}
	if succeeded == 0 || successes != 1 || serviceCountRecords(t, l.service, ledger.TypeRuntimePaidResult) != 2 || l.status(t).State != "available" || l.posts.Load() != 2 {
		t.Fatalf("same-handle race: ok=%d successes=%d state=%s posts=%d", succeeded, successes, l.status(t).State, l.posts.Load())
	}
}

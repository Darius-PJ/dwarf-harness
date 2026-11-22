package agents

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// serviceLive is a real loopback-TLS paid service whose provider behaviour,
// POST count, client connections and review clock each row controls.
type serviceLive struct {
	*paidFixture
	server  *httptest.Server
	handler atomic.Pointer[http.HandlerFunc]
	posts   atomic.Int64
	offset  atomic.Int64
	connMu  sync.Mutex
	conns   []net.Conn
}

func newServiceLive(t testing.TB, provider string) *serviceLive {
	t.Helper()
	l := &serviceLive{}
	l.server = paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		l.posts.Add(1)
		h := l.handler.Load()
		if h == nil {
			t.Error("provider called without a row behaviour")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		(*h)(w, r)
	})
	deps := paidServerDeps(l.server, func(c net.Conn) net.Conn {
		l.connMu.Lock()
		l.conns = append(l.conns, c)
		l.connMu.Unlock()
		return c
	})
	deps.reviewClock = func() (int64, error) { return time.Now().UTC().UnixNano() + l.offset.Load(), nil }
	l.paidFixture = paidProviderFixture(t, provider, deps)
	return l
}

func (l *serviceLive) respond(h http.HandlerFunc) { l.handler.Store(&h) }

// resetConnections closes every client connection under the TLS layer, as a
// transport reset does: the client's pending read fails with a net.Error.
func (l *serviceLive) resetConnections() {
	l.connMu.Lock()
	defer l.connMu.Unlock()
	for _, c := range l.conns {
		_ = c.Close()
	}
}

// paidCall is one real call: prepare, admit, dispatch, record the handle.
type paidCall struct {
	admission PaidAdmission
	dispatch  PaidDispatch
	outcome   PaidDispatchOutcome
	receipt   PaidReceipt
	err       error
}

func (l *serviceLive) call(t *testing.T) paidCall {
	t.Helper()
	return l.callWith(t, context.Background(), nil)
}

func (l *serviceLive) callWith(t *testing.T, ctx context.Context, observe TextObserver) paidCall {
	t.Helper()
	c := paidCall{admission: paidPrepare(t, l.paidFixture)}
	l.admit(t, c.admission)
	c.dispatch = PaidDispatch{CommandID: uuid.NewV7(), AttemptID: c.admission.AttemptID, ExpectedVersion: 1}
	if observe != nil {
		c.outcome, c.err = l.service.DispatchPaidStreaming(ctx, c.dispatch, observe)
	} else {
		c.outcome, c.err = l.service.DispatchPaid(ctx, c.dispatch)
	}
	if c.outcome.ObservedPaidCall != nil {
		c.receipt, c.err = l.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: c.dispatch.AttemptID, ExpectedVersion: 2, Observed: c.outcome.ObservedPaidCall})
	}
	return c
}

func (l *serviceLive) status(t testing.TB) ServiceView {
	t.Helper()
	v, err := l.service.ServiceStatus(context.Background(), l.origin.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// serviceObservations returns every recorded E in ledger order.
func serviceObservations(t testing.TB, s *Service) []ledger.RuntimeServiceObserved {
	t.Helper()
	var out []ledger.RuntimeServiceObserved
	if err := s.scan(context.Background(), 0, s.store.Durable(), func(c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type == ledger.TypeRuntimeServiceObserved {
				e, err := ledger.DecodeRuntimeServiceObserved(r.Payload)
				if err != nil {
					return err
				}
				out = append(out, e)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// serviceProviderError answers with a provider error status and body.
func serviceProviderError(provider string, status int, kind string, headers map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if provider == "openai" {
			_, _ = fmt.Fprintf(w, `{"error":{"type":%q,"code":%q,"message":"Synthetic refusal."}}`, kind, kind)
			return
		}
		_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"Synthetic refusal."}}`, kind)
	}
}

// serviceAnthropicStop answers a complete stream with the given stop reason.
func serviceAnthropicStop(t *testing.T, stop string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		model := paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, `{"input_tokens":7,"output_tokens":4}`, "answer", stop, "", true))
	}
}

// serviceOpenAIStatus answers a terminal Responses event with the status.
func serviceOpenAIStatus(t *testing.T, status, usage, extra string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		model := paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, paidOpenAIEvent(model, status, usage, paidOpenAIText, extra, 1))
	}
}

func serviceSuccess(t *testing.T, provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { paidProviderSuccess(t, provider, w, r) }
}

// serviceLostByDenial records one immediate credential_denied loss through
// the real path and returns its cause.
func (l *serviceLive) serviceLostByDenial(t *testing.T) ledger.RuntimeServiceObserved {
	t.Helper()
	l.respond(serviceProviderError(l.route.Profile.Provider, http.StatusUnauthorized, "authentication_error", nil))
	if c := l.call(t); c.err != nil {
		t.Fatal(c.err)
	}
	es := serviceObservations(t, l.service)
	if len(es) != 1 || es[0].Source.Reason != "credential_denied" || l.status(t).State != "lost" {
		t.Fatalf("denial did not record one immediate loss: %d %+v", len(es), l.status(t))
	}
	return es[0]
}

// serviceOnlyAfter reports whether everything committed after the given
// sequence is complete F3-91 service groups: each commit holds exactly one
// ledger.command record, otherwise only runtime.service_* records, and ends
// with its service receipt. Any other record kind (a paid intent, exchange or
// spend) or a group without its command or receipt fails it, so a refusal that
// became service evidence is still proved to have written no native record.
func serviceOnlyAfter(t testing.TB, s *Service, after uint64) bool {
	t.Helper()
	only := true
	if err := s.scan(context.Background(), after, s.store.Durable(), func(c ledger.Commit) error {
		commands := 0
		for _, r := range c.Records {
			switch {
			case r.Type == ledger.TypeCommand:
				commands++
			case !isServiceType(r.Type):
				only = false
			}
		}
		only = only && commands == 1 && len(c.Records) > 2 && c.Records[len(c.Records)-1].Type == ledger.TypeRuntimeServiceReceipt
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return only
}

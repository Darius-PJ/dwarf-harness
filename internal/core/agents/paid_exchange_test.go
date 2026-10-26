package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func paidSSE(model, start, final, text, stop, details string, complete bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"message-one\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":%s}}\n\n", model, start)
	if text != "" {
		b.WriteString("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprintf(&b, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text)
		b.WriteString("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	}
	if details == "" {
		details = "null"
	}
	fmt.Fprintf(&b, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q,\"stop_details\":%s},\"usage\":%s}\n\n", stop, details, final)
	if complete {
		b.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	}
	return b.String()
}

func paidServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
func paidServerDeps(server *httptest.Server, wrap func(net.Conn) net.Conn) serviceDeps {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return serviceDeps{paidTestAuthority: strings.TrimPrefix(server.URL, "https://"), paidTestRoots: roots, paidWrapConn: wrap}
}
func paidRequestModel(t *testing.T, r *http.Request) string {
	t.Helper()
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error("invalid request body")
	}
	return body.Model
}
func paidSuccess(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	model := paidRequestModel(t, r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Request-Id", "request-one")
	_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, `{"input_tokens":7,"output_tokens":4}`, "answer", "end_turn", "", true))
}
func paidRun(t *testing.T, f *paidFixture) (ledger.RuntimePaidReserved, PaidDispatch, PaidDispatchOutcome) {
	t.Helper()
	r := f.prepare(t)
	_, bound := f.admit(t, r)
	admission, err := bound.Admission()
	if err != nil {
		t.Fatal(err)
	}
	dispatch := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
	out, err := f.service.DispatchPaid(context.Background(), dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if out.IntentReceipt.Outcome != "sent" || out.ObservedPaidCall == nil {
		t.Fatal("missing durable intent or observation")
	}
	return admission, dispatch, out
}
func paidRecord(t *testing.T, f *paidFixture, id PaidDispatch, out PaidDispatchOutcome) PaidReceipt {
	t.Helper()
	r, err := f.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: f.id(), AttemptID: id.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type paidObservedConn struct {
	net.Conn
	before func()
}

func (c *paidObservedConn) Write(b []byte) (int, error) { c.before(); return c.Conn.Write(b) }

func TestPaidIntentBeforeBytes(t *testing.T) {
	t.Parallel()
	t.Run("durable-before-tls-and-duplicate-never-resends", func(t *testing.T) {
		t.Parallel()
		var posts, writes atomic.Int64
		var f *paidFixture
		var minimum atomic.Uint64
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			if f.store.Durable() < minimum.Load() {
				t.Error("server saw request before durable intent")
			}
			paidSuccess(t, w, r)
		})
		f = newLivePaidFixture(t, paidServerDeps(server, func(c net.Conn) net.Conn {
			return &paidObservedConn{c, func() {
				writes.Add(1)
				if f.store.Durable() < minimum.Load() {
					t.Error("first connection bytes preceded durable intent")
				}
			}}
		}))
		r := f.prepare(t)
		_, bound := f.admit(t, r)
		admission, err := bound.Admission()
		if err != nil {
			t.Fatal(err)
		}
		f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: fmt.Sprintf("0.%06d", admission.Quote.MaximumMicros), PeriodKind: "utc_day"})
		minimum.Store(f.store.Durable() + 3)
		d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
		out, err := f.service.DispatchPaid(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		if posts.Load() != 1 || writes.Load() == 0 {
			t.Fatal("real connection was not exercised")
		}
		before := writes.Load()
		f.service.deps.reviewClock = func() (int64, error) { t.Error("duplicate sampled clock"); return 0, ErrPaidClock }
		retry, err := f.service.DispatchPaid(context.Background(), d)
		if err != nil || retry.IntentReceipt.Sequence != out.IntentReceipt.Sequence || retry.ObservedPaidCall != nil || writes.Load() != before {
			t.Fatalf("duplicate acquired a send token: %v", err)
		}
		paidRecord(t, f, d, out)
		retry, err = f.service.DispatchPaid(context.Background(), d)
		if err != nil || retry.Result == nil || retry.Result.State != "settled" || posts.Load() != 1 {
			t.Fatalf("settled retry reran I/O: %v", err)
		}
	})
	for _, name := range []string{"changed-body", "revoked", "lowered-cap", "expired", "expires-before-submit", "recovered"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int64
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { posts.Add(1); paidSuccess(t, w, r) })
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			r := f.prepare(t)
			f.admit(t, r)
			switch name {
			case "changed-body":
				r.Prepared.state.body[0] = 'x'
			case "revoked":
				_, err := f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: "anthropic", CredentialVersion: f.credential.CredentialVersion})
				if err != nil {
					t.Fatal(err)
				}
			case "lowered-cap":
				f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0", PeriodKind: "utc_day"})
			case "expired":
				f.service.deps.reviewClock = func() (int64, error) { return f.route.Profile.ExpiresUTCNS, nil }
			case "expires-before-submit":
				samples := 0
				f.service.deps.reviewClock = func() (int64, error) {
					samples++
					if samples == 1 {
						return f.service.p.reviewFloor, nil
					}
					return f.route.Profile.ExpiresUTCNS, nil
				}
			case "recovered":
				reopenScoped(t, f.serviceFixture, DefaultLimits())
			}
			before := f.store.Durable()
			_, err := f.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1})
			if err == nil || f.store.Durable() != before || posts.Load() != 0 || f.view(t, r.AttemptID).HeldMicros == "0" {
				t.Fatal("invalid or recovered admission dispatched or released money")
			}
		})
	}
}

func TestAnthropicStreamAndUsage(t *testing.T) {
	t.Parallel()
	t.Run("oversize-stop-details", func(t *testing.T) {
		t.Parallel()
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
			model := paidRequestModel(t, r)
			// The delta fits one SSE event. Retaining its 900 KiB stop_details
			// plus both final and merged 80 KiB usage exceeds one private object.
			details := `{"padding":"` + strings.Repeat("x", 900<<10) + `"}`
			final := `{"input_tokens":7,"output_tokens":4,"padding":"` + strings.Repeat("x", 80<<10) + `"}`
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Request-Id", "request-one")
			_, _ = io.WriteString(w, paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, final, "answer", "end_turn", details, true))
		})
		f := newLivePaidFixture(t, paidServerDeps(server, nil))
		_, request, out := paidRun(t, f)
		paidRecord(t, f, request, out)
		view := f.view(t, request.AttemptID)
		if view.State != "unknown" || view.HeldMicros == "0" || len(view.Result.MessageObjects) != 0 {
			t.Fatal("oversize observation did not retain the hold without output")
		}
		raw, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: request.AttemptID, Object: *view.Result.EvidenceObject, MaxBytes: maxObjectBytes})
		if err != nil {
			t.Fatal(err)
		}
		var observation paidObservationObject
		if err := json.Unmarshal(raw, &observation); err != nil {
			t.Fatal(err)
		}
		if observation.Error == nil || observation.Error.Type != "capture_refused" || observation.ErrorClass != "protocol_error" || observation.Complete || !observation.OutputStarted || observation.HTTPStatus != 0 || len(observation.Headers) != 0 || len(observation.StopDetails) != 0 || len(observation.RawUsageFinal) != 0 || len(observation.RawUsageMerged) != 0 {
			t.Fatal("oversize provider detail did not degrade to the fixed unknown observation")
		}
	})
	t.Run("canonical-blocks-and-cache", paidStreamFeatureRows)
	for _, name := range []string{"complete-artifacts", "eof", "malformed", "missing-final-usage", "http-error", "credential-echo"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wireBody := make(chan []byte, 1)
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				wireBody <- raw
				var request struct {
					Model string `json:"model"`
				}
				if json.Unmarshal(raw, &request) != nil {
					t.Error("invalid wire JSON")
				}
				model := request.Model
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "request-one")
				w.Header().Set("Set-Cookie", "excluded")
				w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "2")
				if name == "http-error" {
					w.WriteHeader(503)
					_, _ = io.WriteString(w, `{"error":{"type":"api_error","message":"unavailable"}}`)
					return
				}
				final := `{"input_tokens":7,"output_tokens":4}`
				if name == "missing-final-usage" {
					final = `{}`
				}
				text := "answer"
				if name == "credential-echo" {
					text = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				}
				body := paidSSE(model, `{"input_tokens":3,"output_tokens":1}`, final, text, "end_turn", "", name != "eof")
				if name == "malformed" {
					body = "data: {broken}\n\n"
				}
				// Fragment every UTF-8/JSON/SSE boundary over real writes; no sleeps.
				for i := 0; i < len(body); i += 7 {
					_, _ = io.WriteString(w, body[i:min(i+7, len(body))])
				}
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			admission, d, out := paidRun(t, f)
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			if name != "complete-artifacts" {
				if v.State != "unknown" || v.HeldMicros == "0" || len(v.Result.MessageObjects) != 0 {
					t.Fatal("incomplete or unsafe observation released money or published output")
				}
				return
			}
			if v.State != "settled" || v.SettledMicros != "11" || v.HeldMicros != "0" {
				t.Fatalf("cumulative usage was not exclusive replacement: %+v", v)
			}
			if len(v.Result.MessageObjects) != 1 {
				t.Fatal("completed output missing")
			}
			root := filepath.Dir(paidInputPath(f, admission))
			input := readTestFile(t, filepath.Join(root, admission.InputObject.SHA256.String()))
			if !bytes.Equal(input, <-wireBody) || ledger.Digest(sha256.Sum256(input)) != admission.Request.BodySHA256 || int64(len(input)) != admission.Request.BodyBytes {
				t.Fatal("input object bytes differ from the single wire request")
			}
			expected := fmt.Sprintf(`{"schema":1,"kind":"paid_output","attempt_id":%q,"origin":{"agent_id":%q,"session_id":%q,"context_id":%q},"parent_turn_id":%q,"block_index":0,"chunk_index":0,"block":{"kind":"text","text":"answer"}}`, admission.AttemptID.String(), f.origin.AgentID.String(), f.origin.SessionID.String(), f.origin.ContextID.String(), admission.ParentTurnID.String())
			output := readTestFile(t, filepath.Join(root, v.Result.MessageObjects[0].SHA256.String()))
			if string(output) != expected || ledger.Digest(sha256.Sum256(output)) != v.Result.MessageObjects[0].SHA256 || int64(len(output)) != v.Result.MessageObjects[0].Bytes {
				t.Fatalf("output bytes/reference disagree: %s", output)
			}
			raw := readTestFile(t, filepath.Join(root, v.Result.EvidenceSHA256.String()))
			var observation paidObservationObject
			if json.Unmarshal(raw, &observation) != nil {
				t.Fatal("invalid observation")
			}
			expectedObservation := paidObservationObject{Schema: 1, Kind: "paid_observation", AttemptID: admission.AttemptID, Origin: f.origin, ParentTurnID: admission.ParentTurnID, RequestSHA256: admission.Quote.RequestSHA256, ProviderRequestID: "request-one", ProviderMessageID: "message-one", Model: admission.Request.Model, HTTPStatus: 200, Headers: []paidResponseHeader{{"anthropic-ratelimit-requests-remaining", []string{"2"}}, {"content-type", []string{"text/event-stream"}}, {"request-id", []string{"request-one"}}}, StopReason: "end_turn", StopDetails: json.RawMessage(`null`), RawUsageStart: json.RawMessage(`{"input_tokens":3,"output_tokens":1}`), RawUsageFinal: json.RawMessage(`{"input_tokens":7,"output_tokens":4}`), RawUsageMerged: map[string]json.RawMessage{"input_tokens": json.RawMessage(`7`), "output_tokens": json.RawMessage(`4`)}, OutputStarted: true, Complete: true, MessageObjects: v.Result.MessageObjects, UnknownEventTypes: []string{}, ContentBlocks: 1}
			want, _ := json.Marshal(expectedObservation)
			if !bytes.Equal(raw, want) || ledger.Digest(sha256.Sum256(raw)) != v.Result.EvidenceSHA256 || int64(len(raw)) != v.Result.EvidenceObject.Bytes {
				t.Fatalf("observation bytes/reference disagree: %s", raw)
			}
			if _, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: d.AttemptID, Object: *v.Result.EvidenceObject, MaxBytes: maxObjectBytes}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaidCloseDrainsAndReconcileHolds(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	})
	f := newLivePaidFixture(t, paidServerDeps(server, nil))
	r := f.prepare(t)
	f.admit(t, r)
	d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
	result := make(chan error, 1)
	go func() { _, err := f.service.DispatchPaid(context.Background(), d); result <- err }()
	<-entered
	_, err := f.service.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 2})
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("active exchange reconciled: %v", err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(f.service.paid.exchanges) != 0 || len(f.service.paid.calls) != 0 {
		t.Fatal("Close returned before exchange buffers drained")
	}
	reopenScoped(t, f.serviceFixture, DefaultLimits())
	reconcile := PaidReconciliation{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 2}
	first, err := f.service.ReconcilePaid(context.Background(), reconcile)
	if err != nil {
		t.Fatal(err)
	}
	v := f.view(t, r.AttemptID)
	if v.State != "unknown" || v.HeldMicros == "0" || !v.Ended {
		t.Fatal("sent recovery released money or left child active")
	}
	before := f.store.Durable()
	retry, err := f.service.ReconcilePaid(context.Background(), reconcile)
	if err != nil || retry.Sequence != first.Sequence || f.store.Durable() != before {
		t.Fatal("recovery retry changed accounting")
	}
	old, err := f.service.DispatchPaid(context.Background(), d)
	if err != nil || old.IntentReceipt.Sequence == 0 || old.Result == nil || old.Result.State != "unknown" || old.ObservedPaidCall != nil || f.store.Durable() != before {
		t.Fatal("recovered old dispatch acquired a send permit")
	}
}

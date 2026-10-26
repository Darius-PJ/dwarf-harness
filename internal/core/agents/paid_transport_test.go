package agents

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type paidFaultConn struct {
	net.Conn
	mode   string
	armed  atomic.Bool
	faults atomic.Int64
	writes atomic.Int64
}

func (c *paidFaultConn) Write(b []byte) (int, error) {
	if !c.armed.Load() {
		return c.Conn.Write(b)
	}
	n := c.writes.Add(1)
	if c.mode == "partial_body" && n == 1 {
		return c.Conn.Write(b)
	}
	if !c.armed.CompareAndSwap(true, false) {
		return c.Conn.Write(b)
	}
	c.faults.Add(1)
	switch c.mode {
	case "partial_header", "partial_body":
		written, err := c.Conn.Write(b[:max(1, len(b)/2)])
		_ = c.Conn.Close()
		if err != nil {
			return written, err
		}
		_, err = c.Conn.Write(b[written:])
		return written, err
	case "drop":
		_ = c.Conn.Close()
		return len(b), nil
	default:
		_ = c.Conn.Close()
		return c.Conn.Write(b)
	}
}

func TestPaidHTTP1OneShot(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 301, 302, 303, 307, 308, 429, 529, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var posts, dest atomic.Int64
			target := paidServer(t, func(w http.ResponseWriter, r *http.Request) { dest.Add(1); _, _ = io.Copy(io.Discard, r.Body) })
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if r.ProtoMajor != 1 || r.TLS.NegotiatedProtocol != "http/1.1" {
					t.Error("HTTP/2 reached server")
				}
				if _, ok := r.Header["Idempotency-Key"]; ok {
					t.Error("idempotency header reached server")
				}
				if _, ok := r.Header["X-Idempotency-Key"]; ok {
					t.Error("idempotency header reached server")
				}
				if r.Header.Get("Accept-Encoding") != "" || r.Header.Get("X-Api-Key") != "" {
					t.Error("unsealed wire header")
				}
				if status == 200 {
					paidSuccess(t, w, r)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if status < 400 {
					w.Header().Set("Location", target.URL+"/destination")
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"type":"api_error","message":"synthetic refusal"}}`)
			})
			deps := paidServerDeps(server, nil)
			deps.paidTestRoots.AddCert(target.Certificate())
			f := newLivePaidFixture(t, deps)
			_, d, out := paidRun(t, f)
			paidRecord(t, f, d, out)
			if posts.Load() != 1 || dest.Load() != 0 {
				t.Fatal("exchange retried or followed redirect")
			}
			v := f.view(t, d.AttemptID)
			if status != 200 && (v.State != "unknown" || v.HeldMicros == "0") {
				t.Fatal("HTTP status inferred a refund")
			}
		})
	}
	for _, mode := range []string{"zero_write", "partial_header", "partial_body", "drop", "read_eof"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var requests, complete, dials atomic.Int64
			terminal := make(chan struct{}, 1)
			var once sync.Once
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				_, err := io.ReadAll(r.Body)
				if err != nil {
					return
				}
				complete.Add(1)
				if mode == "read_eof" && n == 2 {
					c, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error("hijack failed")
						return
					}
					_ = c.Close()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, paidSSE("claude-sonnet-4-5-20250929", `{"input_tokens":1,"output_tokens":1}`, `{"output_tokens":1}`, "", "end_turn", "", true))
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateClosed || state == http.StateHijacked {
					once.Do(func() { terminal <- struct{}{} })
				}
			}
			server.StartTLS()
			defer server.Close()
			var connection atomic.Pointer[paidFaultConn]
			deps := paidServerDeps(server, func(c net.Conn) net.Conn {
				dials.Add(1)
				wrapped := &paidFaultConn{Conn: c, mode: mode}
				connection.CompareAndSwap(nil, wrapped)
				return wrapped
			})
			f := newLivePaidFixture(t, deps)
			// Even an unpriceable complete response warms a real HTTP connection.
			_, warm, out := paidRun(t, f)
			paidRecord(t, f, warm, out)
			if dials.Load() != 1 || complete.Load() != 1 || connection.Load() == nil {
				t.Fatal("warm connection missing")
			}
			input := f.input()
			if mode == "partial_body" {
				input.Request.Messages[0].Content[0].Text = strings.Repeat("x", 64<<10)
			}
			p, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
			if err != nil {
				t.Fatal(err)
			}
			r := PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: p}
			f.admit(t, r)
			if mode != "read_eof" {
				connection.Load().armed.Store(true)
			}
			var reused atomic.Bool
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) { reused.Store(i.Reused) }})
			d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
			out, err = f.service.DispatchPaid(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			paidRecord(t, f, d, out)
			f.service.paid.transport.CloseIdleConnections()
			<-terminal
			server.Close()
			if !reused.Load() || dials.Load() != 1 || mode != "read_eof" && connection.Load().faults.Load() != 1 {
				t.Fatal("fault missed warm connection or triggered redial")
			}
			wantRequests, wantComplete := int64(1), int64(1)
			if mode == "read_eof" {
				wantRequests, wantComplete = 2, 2
			}
			if mode == "partial_body" {
				wantRequests = 2
			}
			if requests.Load() != wantRequests || complete.Load() != wantComplete {
				t.Fatalf("after fault requests=%d complete=%d", requests.Load(), complete.Load())
			}
			if v := f.view(t, d.AttemptID); v.State != "unknown" || v.HeldMicros == "0" {
				t.Fatal("transport fault released money")
			}
		})
	}
}

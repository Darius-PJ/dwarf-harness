package providers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// faultConn wraps an actual, verified TLS connection. It faults a single write
// only after one complete warm exchange; no RoundTripper/response is faked.
type faultConn struct {
	net.Conn
	mode        string
	armed       atomic.Bool
	writes      atomic.Int64
	faultWrites atomic.Int64
}

func (c *faultConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	if !c.armed.CompareAndSwap(true, false) {
		return c.Conn.Write(p)
	}
	c.faultWrites.Add(1)
	switch c.mode {
	case "zero_write":
		_ = c.Conn.Close()
		return 0, io.ErrClosedPipe
	case "partial_header":
		n, e := c.Conn.Write(p[:min(8, len(p))])
		_ = c.Conn.Close()
		if e != nil {
			return n, e
		}
		return n, io.ErrClosedPipe
	case "partial_body":
		end := bytes.Index(p, []byte("\r\n\r\n"))
		if end < 0 {
			_ = c.Conn.Close()
			return 0, io.ErrClosedPipe
		}
		n, e := c.Conn.Write(p[:end+4+(len(p)-end-4)/2])
		_ = c.Conn.Close()
		if e != nil {
			return n, e
		}
		return n, io.ErrClosedPipe
	case "drop":
		_ = c.Conn.Close()
		return len(p), nil
	default:
		return c.Conn.Write(p)
	}
}
func trustedDeps(server *httptest.Server, dial func(context.Context, string, string, *tls.Config) (net.Conn, error)) transportDeps {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return transportDeps{roots: roots, dialTLS: dial}
}
func dialVerified(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
	return (&tls.Dialer{Config: cfg}).DialContext(ctx, network, addr)
}
func serverProfile(t *testing.T, server *httptest.Server) Profile {
	t.Helper()
	p := fixtureProfile()
	p.Authority = strings.TrimPrefix(server.URL, "https://")
	return p
}
func drain(t *testing.T, response *http.Response) {
	t.Helper()
	_, e := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if e != nil || closeErr != nil {
		t.Fatal("response drain failed")
	}
}

// Install before StartTLS. Observe the first real connection's terminal state
// without blocking httptest's ConnState hook (which runs under its mutex).
// Client completion alone does not mean the server has consumed a partial
// request: Server.Close would discard it while the connection is still idle.
func firstConnectionTerminal(server *httptest.Server) <-chan http.ConnState {
	terminal := make(chan http.ConnState, 1)
	var first net.Conn
	var once sync.Once
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		once.Do(func() { first = conn })
		if conn == first && (state == http.StateClosed || state == http.StateHijacked) {
			terminal <- state
		}
	}
	return terminal
}

func TestPaidHTTP1OneShot(t *testing.T) {
	t.Parallel()
	// A replayable OpenAI body must fail on the same warm connections as Anthropic.
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"zero_write", "read_eof", "partial_header", "partial_body", "drop"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					var requests, complete, dials atomic.Int64
					var wrongProtocol, idempotency atomic.Bool
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n := requests.Add(1)
						if r.ProtoMajor != 1 {
							wrongProtocol.Store(true)
						}
						if _, ok := r.Header["Idempotency-Key"]; ok {
							idempotency.Store(true)
						}
						if _, ok := r.Header["X-Idempotency-Key"]; ok {
							idempotency.Store(true)
						}
						if _, e := io.ReadAll(r.Body); e != nil {
							return
						}
						complete.Add(1)
						if mode == "read_eof" && n == 2 {
							conn, _, e := w.(http.Hijacker).Hijack()
							if e != nil {
								t.Error("hijack failed")
								return
							}
							_ = conn.Close()
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "warm-response")
					}))
					server.EnableHTTP2 = true
					server.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
					terminal := firstConnectionTerminal(server)
					server.StartTLS()
					defer server.Close()
					var connection *faultConn
					tr, e := newTransport(DefaultLimits(), trustedDeps(server, func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
						dials.Add(1)
						conn, e := dialVerified(ctx, network, addr, cfg)
						if e != nil {
							return nil, e
						}
						wrapped := &faultConn{Conn: conn, mode: mode}
						if dials.Load() == 1 {
							connection = wrapped
						}
						return wrapped, nil
					}))
					if e != nil {
						t.Fatal(e)
					}
					defer tr.CloseIdleConnections()
					envelope := transportEnvelope(t, provider, server)
					secret := []byte("synthetic-transport-value")
					response, e := tr.Do(context.Background(), envelope, secret)
					if e != nil {
						t.Fatal(e)
					}
					drain(t, response)
					if dials.Load() != 1 || complete.Load() != 1 || connection == nil {
						t.Fatal("warm exchange did not use one real connection")
					}
					connection.armed.Store(true)
					var reused atomic.Bool
					ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
						if info.Reused {
							reused.Store(true)
						}
					}})
					response, e = tr.Do(ctx, envelope, secret)
					diagStatus, diagProto := "none", "none"
					if response != nil {
						diagStatus, diagProto = response.Status, response.Proto
						_, copyErr := io.Copy(io.Discard, response.Body)
						closeErr := response.Body.Close()
						if copyErr != nil || closeErr != nil {
							t145Drain.Add(1)
							t145Leaf(provider, mode)
							t.Fatal("response drain failed")
						}
					}
					tr.CloseIdleConnections()
					if !reused.Load() || connection.faultWrites.Load() != 1 {
						t.Fatalf("fault did not hit warm connection DIAG reused=%v dials=%d writes=%d faultWrites=%d err=%v", reused.Load(), dials.Load(), connection.writes.Load(), connection.faultWrites.Load(), e)
					}
					if e == nil {
						t145Nil400.Add(1)
						t145Leaf(provider, mode)
						t145Max(&t145DialsMax, dials.Load())
						t145Max(&t145RequestsMax, requests.Load())
						t.Fatalf("faulted attempt unexpectedly succeeded (possible replay) DIAG dials=%d requests=%d complete=%d writes=%d faultWrites=%d status=%q proto=%q wrongProtocol=%v", dials.Load(), requests.Load(), complete.Load(), connection.writes.Load(), connection.faultWrites.Load(), diagStatus, diagProto, wrongProtocol.Load())
					}
					if dials.Load() != 1 {
						t.Fatalf("redial after fault: %d", dials.Load())
					}
					state := <-terminal
					server.Close()
					wantRequests, wantComplete := int64(1), int64(1)
					if mode == "read_eof" {
						wantRequests = 2
						wantComplete = 2
					}
					if mode == "partial_body" {
						wantRequests = 2
					}
					if requests.Load() != wantRequests || complete.Load() != wantComplete {
						t.Fatalf("server terminal=%s: requests=%d want=%d complete=%d want=%d", state, requests.Load(), wantRequests, complete.Load(), wantComplete)
					}
					if wrongProtocol.Load() {
						t.Fatal("server negotiated HTTP/2")
					}
					if idempotency.Load() {
						t.Fatal("unsealed idempotency header reached server")
					}
				})
			}
		})
	}
}

func TestTransportUsesOnlySealedHeaderSetAndBody(t *testing.T) {
	t.Parallel()
	// An extra header or rewindable body would escape the sealed envelope.
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			envelope := transportEnvelope(t, provider, nil)
			request, e := envelope.request(context.Background(), []byte("synthetic-header-value"))
			if e != nil {
				t.Fatal(e)
			}
			defer request.Body.Close()
			keys := make([]string, 0, len(request.Header))
			for k := range request.Header {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			want := []string{"Accept", "Anthropic-Version", "Authorization", "Content-Type"}
			if provider == "openai" {
				want = []string{"Accept", "Authorization", "Content-Type"}
			}
			if !slices.Equal(keys, want) {
				t.Fatal("built request contains an unsealed key, including an empty-valued key")
			}
			body, e := io.ReadAll(request.Body)
			if e != nil || !bytes.Equal(body, envelope.Body()) || request.ContentLength != int64(len(body)) || request.GetBody != nil {
				t.Fatal("sealed bytes or one-shot body changed")
			}
		})
	}
}
func TestTransportALPNHeadersAndStatusesNeverRepeat(t *testing.T) {
	t.Parallel()
	// Negotiating h2 or retrying a status would create an unquoted physical send.
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, status := range []int{200, 429, 529, 500, 502, 503, 504} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					t.Parallel()
					var count atomic.Int64
					var invalid atomic.Bool
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						if r.ProtoMajor != 1 || r.TLS.NegotiatedProtocol != "http/1.1" {
							invalid.Store(true)
						}
						if _, ok := r.Header["Idempotency-Key"]; ok {
							invalid.Store(true)
						}
						if _, ok := r.Header["X-Idempotency-Key"]; ok {
							invalid.Store(true)
						}
						version := APIVersion
						if provider == "openai" {
							version = ""
						}
						if r.Header.Get("Accept-Encoding") != "" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Anthropic-Version") != version {
							invalid.Store(true)
						}
						_, _ = io.Copy(io.Discard, r.Body)
						w.WriteHeader(status)
					}))
					server.EnableHTTP2 = true
					server.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
					terminal := firstConnectionTerminal(server)
					server.StartTLS()
					defer server.Close()
					tr, e := newTransport(DefaultLimits(), trustedDeps(server, nil))
					if e != nil {
						t.Fatal(e)
					}
					defer tr.CloseIdleConnections()
					response, e := tr.Do(context.Background(), transportEnvelope(t, provider, server), []byte("synthetic-status-value"))
					if e != nil {
						t.Fatal(e)
					}
					drain(t, response)
					tr.CloseIdleConnections()
					<-terminal
					server.Close()
					if response.StatusCode != status || count.Load() != 1 || invalid.Load() {
						t.Fatal("wire profile or one-shot status changed")
					}
				})
			}
		})
	}
}
func TestTransportRedirectDestinationReceivesNothing(t *testing.T) {
	t.Parallel()
	// Any redirect, including a rewritten GET, violates the one-send contract.
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, status := range []int{301, 302, 303, 307, 308} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					t.Parallel()
					var destination, source atomic.Int64
					target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						destination.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						w.WriteHeader(200)
					}))
					defer target.Close()
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						source.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("Location", target.URL+"/destination")
						w.WriteHeader(status)
					}))
					terminal := firstConnectionTerminal(server)
					server.StartTLS()
					defer server.Close()
					deps := trustedDeps(server, nil)
					deps.roots.AddCert(target.Certificate())
					tr, e := newTransport(DefaultLimits(), deps)
					if e != nil {
						t.Fatal(e)
					}
					defer tr.CloseIdleConnections()
					r, e := tr.Do(context.Background(), transportEnvelope(t, provider, server), []byte("synthetic-redirect-value"))
					if e != nil {
						t.Fatal(e)
					}
					drain(t, r)
					tr.CloseIdleConnections()
					<-terminal
					server.Close()
					target.Close()
					if r.StatusCode != status || source.Load() != 1 || destination.Load() != 0 {
						t.Fatal("redirect destination received a request, including GET")
					}
				})
			}
		})
	}
}
func TestTransportRejectsUntrustedCertificate(t *testing.T) {
	t.Parallel()
	var count atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { count.Add(1) }))
	terminal := firstConnectionTerminal(server)
	server.StartTLS()
	defer server.Close()
	tr, e := NewTransport(DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	defer tr.CloseIdleConnections()
	r, e := tr.Do(context.Background(), fixtureEnvelope(t, serverProfile(t, server)), []byte("synthetic-trust-value"))
	if r != nil {
		r.Body.Close()
	}
	tr.CloseIdleConnections()
	<-terminal
	server.Close()
	if e == nil || count.Load() != 0 {
		t.Fatal("TLS verification bypassed")
	}
}

// t145Leaf and t145Max feed ticket 145's temporary hosted stress diagnostic
// (zz_t145_hosted_stress_test.go). This instrumentation never lands.
func t145Leaf(provider, mode string) {
	t145Leaves.Lock()
	defer t145Leaves.Unlock()
	t145Leaves.m[provider+"."+mode]++
}

func t145Max(max *atomic.Int64, v int64) {
	for {
		cur := max.Load()
		if v <= cur || max.CompareAndSwap(cur, v) {
			return
		}
	}
}

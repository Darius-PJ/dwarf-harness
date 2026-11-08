package providers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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
	cut         func() error
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
		_ = c.cut()
		return 0, io.ErrClosedPipe
	case "partial_header":
		n, e := c.Conn.Write(p[:min(8, len(p))])
		_ = c.cut()
		if e != nil {
			return n, e
		}
		return n, io.ErrClosedPipe
	case "partial_body":
		// net/http flushes headers first for this body, so the forwarded
		// header block normally has zero body bytes following it.
		end := bytes.Index(p, []byte("\r\n\r\n"))
		if end < 0 {
			_ = c.cut()
			return 0, io.ErrClosedPipe
		}
		n, e := c.Conn.Write(p[:end+4+(len(p)-end-4)/2])
		_ = c.cut()
		if e != nil {
			return n, e
		}
		return n, io.ErrClosedPipe
	case "drop":
		_ = c.cut()
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

// closeWindowConn holds only the fault's first graceful close open. A raw cut
// marks it first so net/http's later Close, called under its own lock, cannot wait.
type closeWindowConn struct {
	net.Conn
	tlsConn *tls.Conn
	armed   atomic.Bool
	cut     atomic.Bool
	started atomic.Bool
	release <-chan struct{}
}

func (c *closeWindowConn) Close() error {
	if c.armed.Load() && !c.cut.Load() && c.started.CompareAndSwap(false, true) {
		_ = c.tlsConn.CloseWrite()
		<-c.release
	}
	return c.Conn.Close()
}

// Keep only scalar evidence, never requests, headers or response bodies.
type oneShotObservation struct {
	dials, requests, complete, writes, faultWrites int64
	reused, wrongProtocol, idempotency             bool
	terminal                                       http.ConnState
	attemptFailed                                  bool
	attemptError                                   string
	responsePresent                                bool
	status                                         int
	proto                                          string
	drainFailed, closeFailed                       bool
}

func exerciseOneShotFault(t *testing.T, provider, mode, closeMode, extraActivity string) oneShotObservation {
	t.Helper()
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
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseClose := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseClose)
	var connection *faultConn
	var window *closeWindowConn
	countedDial := func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
		n := dials.Add(1)
		conn, e := dialVerified(ctx, network, addr, cfg)
		if e != nil {
			return nil, e
		}
		secured, ok := conn.(*tls.Conn)
		if !ok {
			_ = conn.Close()
			return nil, fmt.Errorf("verified dial returned %T, not TLS", conn)
		}
		raw := secured.NetConn()
		cut := raw.Close
		var interposer *closeWindowConn
		if closeMode == "raw_window" || closeMode == "graceful_control" {
			interposer = &closeWindowConn{Conn: conn, tlsConn: secured, release: release}
			conn = interposer
			cut = func() error {
				interposer.cut.Store(true)
				return raw.Close()
			}
			if closeMode == "graceful_control" {
				cut = interposer.Close
			}
		}
		wrapped := &faultConn{Conn: conn, mode: mode, cut: cut}
		if n == 1 {
			connection = wrapped
			window = interposer
		}
		return wrapped, nil
	}
	tr, e := newTransport(DefaultLimits(), trustedDeps(server, countedDial))
	if e != nil {
		t.Fatal(e)
	}
	defer tr.CloseIdleConnections()
	envelope := transportEnvelope(t, provider, server)
	syntheticValue := []byte("synthetic-transport-value")
	response, e := tr.Do(context.Background(), envelope, syntheticValue)
	if e != nil {
		t.Fatal(e)
	}
	drain(t, response)
	if dials.Load() != 1 || requests.Load() != 1 || complete.Load() != 1 || connection == nil {
		t.Fatal("warm exchange did not use one real connection")
	}
	if extraActivity == "extra_complete" {
		response, e = tr.Do(context.Background(), envelope, syntheticValue)
		if e != nil {
			t.Fatal(e)
		}
		drain(t, response)
		if dials.Load() != 1 || requests.Load() != 2 || complete.Load() != 2 {
			t.Fatal("extra exchange did not complete on the warm connection")
		}
	}
	if window != nil {
		window.armed.Store(true)
	}
	connection.armed.Store(true)
	var reused atomic.Bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if info.Reused {
			reused.Store(true)
		}
	}})
	response, e = tr.Do(ctx, envelope, syntheticValue)
	releaseClose()
	observation := oneShotObservation{attemptFailed: e != nil}
	if e != nil {
		observation.attemptError = e.Error()
	}
	if response != nil {
		observation.responsePresent = true
		observation.status = response.StatusCode
		observation.proto = response.Proto
		_, drainErr := io.Copy(io.Discard, response.Body)
		observation.drainFailed = drainErr != nil
		observation.closeFailed = response.Body.Close() != nil
	}
	if extraActivity == "extra_dial" {
		conn, err := countedDial(context.Background(), "tcp", strings.TrimPrefix(server.URL, "https://"), tr.transport.TLSClientConfig.Clone())
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	tr.CloseIdleConnections()
	observation.terminal = <-terminal
	server.Close()
	observation.dials = dials.Load()
	observation.requests = requests.Load()
	observation.complete = complete.Load()
	observation.writes = connection.writes.Load()
	observation.faultWrites = connection.faultWrites.Load()
	observation.reused = reused.Load()
	observation.wrongProtocol = wrongProtocol.Load()
	observation.idempotency = idempotency.Load()
	return observation
}

var errOneShotCounts = errors.New("one-shot counts changed")

func checkOneShot(observation oneShotObservation, mode, closeMode string) error {
	wantRequests, wantComplete := int64(1), int64(1)
	if mode == "read_eof" {
		wantRequests = 2
		wantComplete = 2
	}
	if mode == "partial_body" {
		wantRequests = 2
	}
	if observation.dials != 1 || observation.requests != wantRequests || observation.complete != wantComplete {
		return fmt.Errorf("%w: want dials=1 requests=%d complete=%d; observed %+v", errOneShotCounts, wantRequests, wantComplete, observation)
	}
	if !observation.reused || observation.faultWrites != 1 {
		return fmt.Errorf("fault did not hit warm connection: %+v", observation)
	}
	if observation.wrongProtocol || observation.idempotency {
		return fmt.Errorf("HTTP/1 or sealed header profile changed: %+v", observation)
	}
	if closeMode == "graceful_control" {
		if mode != "partial_header" || !observation.responsePresent || observation.status != http.StatusBadRequest || observation.proto != "HTTP/1.1" || observation.attemptFailed {
			return fmt.Errorf("graceful close did not expose the early 400: %+v", observation)
		}
	} else if !observation.attemptFailed {
		return fmt.Errorf("faulted attempt returned a nil error after valid counts: %+v", observation)
	}
	return nil
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
					observation := exerciseOneShotFault(t, provider, mode, "raw", "none")
					if err := checkOneShot(observation, mode, "raw"); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestPaidHTTP1OneShotCloseWindow(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, variant := range []string{"raw", "graceful_control"} {
				t.Run(variant, func(t *testing.T) {
					t.Parallel()
					closeMode := variant
					if variant == "raw" {
						closeMode = "raw_window"
					}
					observation := exerciseOneShotFault(t, provider, "partial_header", closeMode, "none")
					t.Logf("close window: %+v", observation)
					if err := checkOneShot(observation, "partial_header", closeMode); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestPaidHTTP1OneShotCounts(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, control := range []string{"extra_dial", "extra_complete", "counts_before_outcome"} {
				t.Run(control, func(t *testing.T) {
					t.Parallel()
					mode, closeMode, extraActivity := "zero_write", "raw", control
					wantDials, wantRequests, wantComplete := int64(1), int64(2), int64(2)
					if control == "extra_dial" {
						wantDials, wantRequests, wantComplete = 2, 1, 1
					}
					if control == "counts_before_outcome" {
						mode, closeMode, extraActivity = "partial_header", "graceful_control", "extra_complete"
					}
					observation := exerciseOneShotFault(t, provider, mode, closeMode, extraActivity)
					if observation.dials != wantDials || observation.requests != wantRequests || observation.complete != wantComplete {
						t.Fatalf("control did not produce its exact excess counts: %+v", observation)
					}
					if control == "counts_before_outcome" {
						if observation.attemptFailed || !observation.responsePresent || observation.status != http.StatusBadRequest || observation.proto != "HTTP/1.1" {
							t.Fatalf("dual-fault control did not receive the early 400: %+v", observation)
						}
					} else if !observation.attemptFailed {
						t.Fatalf("count control lost its valid error outcome: %+v", observation)
					}
					if err := checkOneShot(observation, mode, "raw"); !errors.Is(err, errOneShotCounts) {
						t.Fatalf("count stage did not reject %s: %v; observed %+v", control, err, observation)
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

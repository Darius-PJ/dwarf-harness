package providers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"time"
)

// Limits are provisional host policy, not billing or no-charge evidence.
type Limits struct {
	MaxActivePaid         int
	DispatchTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

func DefaultLimits() Limits { return Limits{3, 120 * time.Second, 10 * time.Second, 30 * time.Second} }

type transportDeps struct {
	roots   *x509.CertPool
	dialTLS func(context.Context, string, string, *tls.Config) (net.Conn, error)
}

// Transport owns one dedicated HTTP/1-only pool. Core, not this package, owns
// authority, send permits, active slots and durable intent before calling Do.
type Transport struct {
	client    *http.Client
	transport *http.Transport
}

func NewTransport(l Limits) (*Transport, error) { return newTransport(l, transportDeps{}) }

// NewTransportWithRoots is the loopback-test seam. Production uses NewTransport.
// wrap observes real TCP connections before TLS; dialing, the bounded handshake
// and certificate verification remain owned here, never by the caller.
func NewTransportWithRoots(l Limits, roots *x509.CertPool, wrap func(net.Conn) net.Conn) (*Transport, error) {
	return newTransport(l, transportDeps{roots: roots, dialTLS: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, l.TLSHandshakeTimeout)
		defer cancel()
		raw, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		conn := raw
		if wrap != nil {
			conn = wrap(raw)
			if conn == nil {
				_ = raw.Close()
				return nil, ErrInvalid
			}
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			_ = conn.Close()
			return nil, ErrInvalid
		}
		cfg.ServerName = host
		secured := tls.Client(conn, cfg)
		if err := secured.HandshakeContext(ctx); err != nil {
			_ = secured.Close()
			return nil, err
		}
		return secured, nil
	}})
}
func newTransport(l Limits, d transportDeps) (*Transport, error) {
	if l.MaxActivePaid <= 0 || l.DispatchTimeout <= 0 || l.TLSHandshakeTimeout <= 0 || l.ResponseHeaderTimeout <= 0 {
		return nil, ErrInvalid
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	config := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, RootCAs: d.roots}
	tr := &http.Transport{
		Proxy: nil, DisableCompression: true, Protocols: protocols, ForceAttemptHTTP2: false, TLSClientConfig: config,
		MaxIdleConns: l.MaxActivePaid, MaxIdleConnsPerHost: l.MaxActivePaid, MaxConnsPerHost: l.MaxActivePaid, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: l.TLSHandshakeTimeout, ResponseHeaderTimeout: l.ResponseHeaderTimeout, MaxResponseHeaderBytes: 64 << 10,
	}
	if d.dialTLS != nil {
		tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.dialTLS(ctx, network, addr, config.Clone())
		}
	}
	return &Transport{client: &http.Client{Transport: tr, Timeout: l.DispatchTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: tr}, nil
}

type requestBody struct {
	data   []byte
	offset int
}

func (b *requestBody) Read(p []byte) (int, error) {
	if b.offset == len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

// There is no resource to release. Close may race with Read in net/http, so
// it must not mutate the immutable bytes or the reader's cursor.
func (*requestBody) Close() error { return nil }

func (e Envelope) request(ctx context.Context, secret []byte) (*http.Request, error) {
	if len(e.body) == 0 || len(secret) == 0 || len(secret) > 4096 {
		return nil, ErrInvalid
	}
	for _, c := range secret {
		if c < 0x21 || c > 0x7e {
			return nil, ErrInvalid
		}
	}
	u, err := endpoint(e.descriptor.Provider, e.descriptor.Authority, e.descriptor.Path)
	if err != nil {
		return nil, err
	}
	body := &requestBody{data: e.body}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return nil, ErrInvalid
	}
	req.ContentLength = int64(len(e.body))
	for _, h := range e.descriptor.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	req.Header.Set("Authorization", "Bearer "+string(secret))
	return req, nil
}

// Do performs exactly one Client.Do, with no redirect, replayable body,
// idempotency header, SDK retry, proxy, compression or alternate protocol.
// The caller must close the response body. Authentication is never logged.
func (t *Transport) Do(ctx context.Context, e Envelope, secret []byte) (*http.Response, error) {
	req, err := e.request(ctx, secret)
	if err != nil {
		return nil, err
	}
	return t.client.Do(req)
}

func (t *Transport) CloseIdleConnections() { t.transport.CloseIdleConnections() }

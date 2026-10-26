package providers

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRefusalPreservesStartedBlockEvidence(t *testing.T) {
	t.Parallel()
	for _, began := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-block", true: "empty-block"}[began], func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			stream := streamStart(`{"input_tokens":1,"output_tokens":0}`)
			if began {
				stream += eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + eventJSON(`{"type":"content_block_stop","index":0}`)
			}
			stream += eventJSON(`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":null}},"usage":{"output_tokens":0}}`) + streamStop()
			r, err := ReadAnthropicStream(strings.NewReader(stream), p, fixtureCompiled(t, p))
			if err != nil || !r.Complete || len(r.Blocks) != 0 || (r.ContentBlocks != 0) != began {
				t.Fatal("refusal lost whether a block began")
			}
		})
	}
}
func TestNormalizeSealedCacheEvidence(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		classes []string
		raw     string
		want    []Units
	}{
		{"uncached-omission", []string{Uncached}, `{"input_tokens":7,"output_tokens":4}`, []Units{{CacheRead, 0}, {CacheWrite1h, 0}, {CacheWrite5m, 0}, {Uncached, 7}, {Generated, 4}}},
		{"five-minute-ambiguous", []string{CacheRead, CacheWrite5m, Uncached}, `{"input_tokens":7,"output_tokens":4}`, nil},
		{"hour-ambiguous", []string{CacheRead, CacheWrite1h, Uncached}, `{"input_tokens":7,"output_tokens":4}`, nil},
		{"exclusive-both-ttl", []string{CacheRead, CacheWrite1h, CacheWrite5m, Uncached}, `{"input_tokens":7,"output_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":8,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":5},"output_tokens_details":{"thinking_tokens":2}}`, []Units{{CacheRead, 2}, {CacheWrite1h, 5}, {CacheWrite5m, 3}, {Uncached, 7}, {Generated, 4}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var raw Usage
			_ = json.Unmarshal([]byte(row.raw), &raw)
			got, _, err := NormalizeSealedUsage(fixtureProfile(), row.classes, raw)
			if row.want == nil {
				if err == nil {
					t.Fatal("possible cache usage silently became zero")
				}
				return
			}
			if err != nil || !slices.Equal(got, row.want) {
				t.Fatal("sealed usage changed exclusive billing")
			}
		})
	}
}

type observedTCP struct {
	net.Conn
	writes *atomic.Int64
}

func (c *observedTCP) Write(b []byte) (int, error) { c.writes.Add(1); return c.Conn.Write(b) }
func TestLoopbackRootsKeepTLSVerification(t *testing.T) {
	t.Parallel()
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "untrusted", true: "trusted"}[trusted], func(t *testing.T) {
			t.Parallel()
			var posts, dials, writes atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); _, _ = io.Copy(io.Discard, r.Body) }))
			defer server.Close()
			roots := x509.NewCertPool()
			if trusted {
				roots.AddCert(server.Certificate())
			}
			transport, err := NewTransportWithRoots(DefaultLimits(), roots, func(c net.Conn) net.Conn { dials.Add(1); return &observedTCP{c, &writes} })
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			response, err := transport.Do(context.Background(), fixtureEnvelope(t, serverProfile(t, server)), []byte("synthetic-loopback-credential"))
			if trusted {
				if err != nil {
					t.Fatal("trusted TLS handshake failed")
				}
				drain(t, response)
				if posts.Load() != 1 {
					t.Fatal("trusted request missing")
				}
			} else if err == nil || posts.Load() != 0 {
				t.Fatal("test wrapper bypassed certificate verification")
			}
			if dials.Load() != 1 || writes.Load() == 0 {
				t.Fatal("wrapper did not observe raw TLS handshake")
			}
		})
	}
}

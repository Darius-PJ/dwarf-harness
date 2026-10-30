package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAIResponseClassificationAndHeaders(t *testing.T) {
	t.Parallel()
	// Real HTTPS failures cannot be retried or priced as free; code/type, not
	// message wording, controls classification, and arbitrary headers never escape.
	for _, row := range []struct {
		name        string
		status      int
		body, class string
	}{
		{"denied", 401, `{"error":{"type":null,"code":null,"param":null,"message":"synthetic"}}`, "denied"},
		{"forbidden", 403, `{"error":{}}`, "denied"},
		{"quota", 429, `{"error":{"type":"insufficient_quota"}}`, "exhausted"},
		{"credit", 429, `{"error":{"code":"credit_balance_exhausted"}}`, "exhausted"},
		{"organization_spend", 429, `{"error":{"code":"organization_spend_limit_exceeded"}}`, "exhausted"},
		{"project_spend", 429, `{"error":{"code":"project_spend_limit_exceeded"}}`, "exhausted"},
		{"organization_usage", 429, `{"error":{"code":"organization_usage_limit_exceeded"}}`, "exhausted"},
		{"rate", 429, `{"error":{"code":"slow_down"}}`, "rate_limited"},
		{"wording_not_code", 429, `{"error":{"message":"insufficient_quota"}}`, "rate_limited"},
		{"overload", 503, `{"error":{"code":"server_is_overloaded"}}`, "overloaded"},
		{"bad_request", 400, `{"error":{}}`, "invalid_request"},
		{"large", 413, `{"error":{}}`, "invalid_request"},
		{"missing", 404, `{"error":{}}`, "unavailable"},
		{"conflict", 409, `{"error":{}}`, "conflict"},
		{"timeout_status", 408, `{"error":{}}`, "provider_error"},
		{"gateway_status", 504, `{"error":{}}`, "provider_error"},
		{"failure", 500, `{"error":{}}`, "provider_error"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "synthetic-request")
				w.Header().Set("OpenAI-Organization", "synthetic-organization")
				w.Header().Set("OpenAI-Processing-Ms", "7")
				w.Header().Set("OpenAI-Version", "synthetic-version")
				w.Header().Set("Retry-After", "2")
				w.Header().Set("X-RateLimit-Reset-Tokens", "6m0s")
				w.Header().Set("X-Arbitrary", "excluded")
				w.Header().Set("Request-ID", "excluded")
				w.WriteHeader(row.status)
				_, _ = io.WriteString(w, row.body)
			}))
			defer server.Close()
			p := openAIProfile()
			p.Authority = strings.TrimPrefix(server.URL, "https://")
			c := openAICompiled(t, p, 16)
			envelope, err := Seal(p, c, "scope", "version", openAIHeaders())
			if err != nil {
				t.Fatal(err)
			}
			tr, err := newTransport(DefaultLimits(), trustedDeps(server, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer tr.CloseIdleConnections()
			resp, err := tr.Do(context.Background(), envelope, []byte("synthetic-response-value"))
			if err != nil {
				t.Fatal("exchange failed")
			}
			e, err := ReadOpenAIResponse(resp, p, c)
			if err != nil || e.ErrorClass != row.class || e.Stream.Priceable || requests.Load() != 1 || string(e.ErrorBody) != row.body {
				t.Fatalf("classification=%s error=%v", e.ErrorClass, err)
			}
			names := make([]string, 0, len(e.Headers))
			for _, h := range e.Headers {
				names = append(names, h.Name)
				if h.Name == "x-ratelimit-reset-tokens" && !slices.Equal(h.Values, []string{"6m0s"}) {
					t.Fatal("reset metadata changed")
				}
			}
			if !slices.Equal(names, []string{"content-type", "openai-organization", "openai-processing-ms", "openai-version", "retry-after", "x-ratelimit-reset-tokens", "x-request-id"}) {
				t.Fatal("retained header allowlist changed")
			}
		})
	}
}

func TestOpenAIResponseTerminalClosesWithoutEOF(t *testing.T) {
	t.Parallel()
	// Waiting for EOF would deadlock while the server waits for cancellation.
	// The parser must deliver terminal usage once and close the response body.
	for _, input := range []int64{20, 272001} {
		t.Run(openAIUsage(input, 0, 0, 16), func(t *testing.T) {
			t.Parallel()
			closed := make(chan struct{})
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, openAITerminal("completed", openAIUsage(input, 0, 0, 16), openAITextOutput))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			p := openAIProfile()
			p.Authority = strings.TrimPrefix(server.URL, "https://")
			c := openAICompiled(t, p, 16)
			envelope, err := Seal(p, c, "scope", "version", openAIHeaders())
			if err != nil {
				t.Fatal(err)
			}
			tr, err := newTransport(DefaultLimits(), trustedDeps(server, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer tr.CloseIdleConnections()
			resp, err := tr.Do(context.Background(), envelope, []byte("synthetic-terminal-value"))
			if err != nil {
				t.Fatal("exchange failed")
			}
			e, err := ReadOpenAIResponse(resp, p, c)
			<-closed
			if !e.Stream.Complete || requests.Load() != 1 {
				t.Fatal("terminal not captured once")
			}
			if input == 272001 {
				if !errors.Is(err, ErrTariffAmbiguous) || e.ErrorClass != "tariff_ambiguous" || e.Stream.ProfileViolation || string(e.Stream.ProviderError) != `{"type":"threshold_ambiguous"}` {
					t.Fatal("ambiguity misclassified as provider failure")
				}
			} else if err != nil || !e.Stream.Priceable {
				t.Fatal("terminal not priceable")
			}
		})
	}
}

func TestOpenAIResponseProtocolAndErrorEvents(t *testing.T) {
	t.Parallel()
	// Unsupported media/encoding and malformed errors cannot masquerade as a
	// supported terminal; SSE/terminal error fields retain null versus absence.
	valid := openAITerminal("completed", openAIUsage(20, 4, 8, 16), openAITextOutput)
	for _, row := range []struct {
		name, media, encoding, body, class string
		status                             int
		wantError                          bool
	}{
		{"media", "application/json", "", valid, "protocol_error", 200, true},
		{"charset", "text/event-stream; charset=latin1", "", valid, "protocol_error", 200, true},
		{"encoding", "text/event-stream", "gzip", valid, "protocol_error", 200, true},
		{"malformed_error", "application/json", "", `{"error":{"code":"a","code":"b"}}`, "protocol_error", 400, true},
		{"sse_error", "text/event-stream", "", eventJSON(`{"type":"error","code":"slow_down","param":null,"message":"synthetic"}`), "rate_limited", 200, true},
		{"terminal_error", "text/event-stream", "", strings.Replace(openAITerminal("failed", openAIUsage(20, 4, 8, 16), "[]"), `"service_tier":"default"`, `"service_tier":"default","error":{"code":"server_error","param":null,"message":"synthetic"}`, 1), "provider_error", 200, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", row.media)
				if row.encoding != "" {
					w.Header().Set("Content-Encoding", row.encoding)
				}
				w.WriteHeader(row.status)
				_, _ = io.WriteString(w, row.body)
			}))
			defer server.Close()
			p := openAIProfile()
			p.Authority = strings.TrimPrefix(server.URL, "https://")
			c := openAICompiled(t, p, 16)
			envelope, err := Seal(p, c, "scope", "version", openAIHeaders())
			if err != nil {
				t.Fatal(err)
			}
			tr, err := newTransport(DefaultLimits(), trustedDeps(server, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer tr.CloseIdleConnections()
			resp, err := tr.Do(context.Background(), envelope, []byte("synthetic-protocol-value"))
			if err != nil {
				t.Fatal("exchange failed")
			}
			e, err := ReadOpenAIResponse(resp, p, c)
			if (err != nil) != row.wantError || e.ErrorClass != row.class {
				t.Fatalf("class=%s error=%v", e.ErrorClass, err)
			}
			if row.name == "terminal_error" || row.name == "sse_error" {
				m, _ := rawObject(e.Stream.ProviderError)
				if string(m["param"]) != "null" || len(m["code"]) == 0 {
					t.Fatal("error fields lost")
				}
			}
		})
	}
}

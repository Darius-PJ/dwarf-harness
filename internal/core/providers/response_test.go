package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRealTLSStreamAndErrorInterpretation(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                               string
		status                             int
		contentType, encoding, body, class string
		complete, parseError               bool
	}{
		{"complete", 200, "text/event-stream; charset=utf-8", "", plainStream(), "", true, false},
		{"wrong_content_type", 200, "application/json", "", plainStream(), "protocol_error", false, true},
		{"compressed", 200, "text/event-stream", "gzip", plainStream(), "protocol_error", false, true},
		{"stream_overloaded", 200, "text/event-stream", "", streamStart(`{"input_tokens":2,"output_tokens":1}`) + eventJSON(`{"type":"error","error":{"type":"overloaded_error","message":"synthetic"}}`), "overloaded", false, true},
		{"denied", 401, "application/json", "", `{"error":{"type":"authentication_error","message":"synthetic"}}`, "denied", false, false},
		{"billing", 402, "application/json", "", `{"error":{"type":"billing_error","message":"synthetic"}}`, "exhausted", false, false},
		{"spend_limit", 429, "application/json", "", `{"error":{"type":"rate_limit_error","message":"synthetic","details":{"error_code":"enforced_spend_limit_reached"}}}`, "exhausted", false, false},
		{"rate_without_retry_after", 429, "application/json", "", `{"error":{"type":"rate_limit_error","message":"synthetic"}}`, "rate_limited", false, false},
		{"usage_prefix", 400, "application/json", "", `{"error":{"type":"invalid_request_error","message":"You have reached your specified API usage limits; synthetic"}}`, "exhausted", false, false},
		{"bad_request", 400, "application/json", "", `{"error":{"type":"invalid_request_error","message":"synthetic"}}`, "invalid_request", false, false},
		{"not_found", 404, "application/json", "", `{"error":{"type":"not_found_error","message":"synthetic"}}`, "unavailable", false, false},
		{"conflict", 409, "application/json", "", `{"error":{"type":"conflict_error","message":"synthetic"}}`, "conflict", false, false},
		{"overloaded", 529, "application/json", "", `{"error":{"type":"overloaded_error","message":"synthetic"}}`, "overloaded", false, false},
		{"provider_error", 500, "application/json", "", `{"error":{"type":"api_error","message":"synthetic"}}`, "provider_error", false, false},
		{"timeout", 504, "application/json", "", `{"error":{"type":"timeout_error","message":"synthetic"}}`, "timeout", false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", row.contentType)
				if row.encoding != "" {
					w.Header().Set("Content-Encoding", row.encoding)
				}
				w.Header().Set("request-id", "local-request")
				w.Header().Set("Set-Cookie", "private-response-value")
				w.WriteHeader(row.status)
				_, _ = io.WriteString(w, row.body)
			}))
			defer server.Close()
			p := serverProfile(t, server)
			c := fixtureCompiled(t, p)
			envelope, e := Seal(p, c, "scope", "version", fixtureHeaders())
			if e != nil {
				t.Fatal(e)
			}
			transport, e := newTransport(DefaultLimits(), trustedDeps(server, nil))
			if e != nil {
				t.Fatal(e)
			}
			defer transport.CloseIdleConnections()
			response, e := transport.Do(context.Background(), envelope, []byte("synthetic-response-value"))
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			evidence, e := ReadAnthropicResponse(response, p, c)
			if (e != nil) != row.parseError || evidence.ErrorClass != row.class || evidence.Stream.Complete != row.complete || attempts.Load() != 1 {
				t.Fatalf("response interpretation class=%s complete=%v err=%v", evidence.ErrorClass, evidence.Stream.Complete, e)
			}
			for _, h := range evidence.Headers {
				if h.Name == "set-cookie" || strings.Contains(strings.Join(h.Values, ""), "private-response-value") {
					t.Fatal("unapproved response header captured")
				}
			}
			if row.complete {
				price, e := PriceUsage(p, evidence.Stream.Units)
				if e != nil || price != 9 || !evidence.Stream.Priceable {
					t.Fatal("real SSE charge differs from exclusive cumulative usage")
				}
			}
		})
	}
}

package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func openAIProfile() Profile {
	p := fixtureProfile()
	p.Provider, p.Path, p.APIVersion, p.ModelID = "openai", "/v1/responses", "v1", "gpt-6-luna"
	p.RequestedTier, p.AllowedServedTiers = "default", []string{"default"}
	p.ContextMax, p.InputMax, p.OutputMax = 1050000, 922000, 128000
	p.UsageSchema = openAIUsageSchema
	p.CacheClasses = []string{"input.cached.long", "input.cached.short", "input.write.long", "input.write.short"}
	p.Tariff.Rates = []Rate{
		{"input.cached.long", "1", "50"}, {"input.cached.short", "1", "100"},
		{"input.uncached.long", "1", "5"}, {"input.uncached.short", "1", "10"},
		{"input.write.long", "1", "4"}, {"input.write.short", "1", "8"},
		{"output.generated.long", "3", "4"}, {"output.generated.short", "1", "2"},
	}
	return p
}

func openAIProfileV2() Profile {
	p := openAIProfile()
	p.UsageSchema = openAIUsageSchemaV2
	p.CapabilityVersion += "-v2"
	return p
}

func openAICompiled(t *testing.T, p Profile, output int64) Compiled {
	t.Helper()
	r := fixtureRequest()
	r.MaxTokens = output
	c, err := CompileOpenAI(p, r)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func openAIHeaders() []Header {
	return []Header{{"accept", "text/event-stream"}, {"content-type", "application/json"}}
}

func transportEnvelope(t *testing.T, provider string, server *httptest.Server) Envelope {
	t.Helper()
	p := fixtureProfile()
	if provider == "openai" {
		p = openAIProfile()
	}
	if server != nil {
		p.Authority = strings.TrimPrefix(server.URL, "https://")
	}
	if provider == "anthropic" {
		return fixtureEnvelope(t, p)
	}
	c := openAICompiled(t, p, 16)
	e, err := Seal(p, c, "scope-fixture", "version-fixture", openAIHeaders())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOpenAICompilerExactWireAndOwnership(t *testing.T) {
	t.Parallel()
	// Wrong part types, defaults, field order or borrowed request storage change
	// the actual sealed POST, not merely its in-memory construction.
	p := openAIProfile()
	r := Request{MaxTokens: 16, Messages: []Message{{Role: "assistant", Content: []Block{{Type: "text", Text: "earlier"}}}, {Role: "user", Content: []Block{{Type: "text", Text: "snow 雪 & <"}}}}, System: []Text{{Type: "text", Text: "one"}, {Type: "text", Text: "two"}}, Tools: []Tool{{Name: "local", Description: "description", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}}
	c, err := CompileOpenAI(p, r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"gpt-6-luna","max_output_tokens":16,"input":[{"type":"message","role":"assistant","content":[{"type":"input_text","text":"earlier"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"snow 雪 & <"}]}],"stream":true,"store":false,"service_tier":"default","truncation":"disabled","background":false,"include":["reasoning.encrypted_content"],"prompt_cache_options":{"mode":"implicit","prewarm":false,"ttl":"30m"},"instructions":"one\n\ntwo","text":{"format":{"type":"text"}},"tools":[{"type":"function","name":"local","description":"description","parameters":{"type":"object","properties":{}},"strict":false}],"tool_choice":"auto","parallel_tool_calls":false}`
	if string(c.Body()) != want {
		t.Fatal("wire body differs from fixed vector")
	}
	r.Messages[1].Content[0].Text = "changed"
	r.Tools[0].InputSchema[0] = '['
	body, classes := c.Body(), c.InputClasses()
	body[0], classes[0] = '[', "changed"
	if string(c.Body()) != want || !slices.Equal(c.InputClasses(), openAIClasses[:6]) {
		t.Fatal("caller mutation changed compiled input")
	}
	plain := openAICompiled(t, p, 16)
	if bytes.Contains(plain.Body(), []byte(`"tools"`)) || bytes.Contains(plain.Body(), []byte(`"instructions"`)) || bytes.Contains(plain.Body(), []byte(`"parallel_tool_calls"`)) {
		t.Fatal("optional wire controls were not omitted")
	}
}

func TestOpenAICompilerRefusesUnsupportedInput(t *testing.T) {
	t.Parallel()
	// Each mutation represents a forbidden feature that must never become a POST.
	for name, change := range map[string]func(*Request){
		"below_minimum":  func(r *Request) { r.MaxTokens = 15 },
		"above_limit":    func(r *Request) { r.MaxTokens = 128001 },
		"empty":          func(r *Request) { r.Messages = nil },
		"assistant_tail": func(r *Request) { r.Messages[0].Role = "assistant" },
		"developer":      func(r *Request) { r.Messages[0].Role = "developer" },
		"empty_content":  func(r *Request) { r.Messages[0].Content = nil },
		"empty_text":     func(r *Request) { r.Messages[0].Content[0].Text = "" },
		"invalid_utf8":   func(r *Request) { r.Messages[0].Content[0].Text = string([]byte{255}) },
		"cache_marker":   func(r *Request) { r.Messages[0].Content[0].CacheControl = &CacheControl{"ephemeral", "5m"} },
		"system_cache": func(r *Request) {
			r.System = []Text{{Type: "text", Text: "system", CacheControl: &CacheControl{"ephemeral", "1h"}}}
		},
		"tool_cache": func(r *Request) {
			r.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`), CacheControl: &CacheControl{"ephemeral", "5m"}}}
		},
		"duplicate_tools": func(r *Request) {
			tool := Tool{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`)}
			r.Tools = []Tool{tool, tool}
		},
		"text_id":              func(r *Request) { r.Messages[0].Content[0].ID = "hidden" },
		"text_name":            func(r *Request) { r.Messages[0].Content[0].Name = "hidden" },
		"text_input":           func(r *Request) { r.Messages[0].Content[0].Input = json.RawMessage(`{}`) },
		"text_tool_id":         func(r *Request) { r.Messages[0].Content[0].ToolUseID = "hidden" },
		"text_content":         func(r *Request) { r.Messages[0].Content[0].Content = []Text{} },
		"text_error":           func(r *Request) { r.Messages[0].Content[0].IsError = new(bool) },
		"choice_without_tools": func(r *Request) { r.ToolChoice = &ToolChoice{"auto"} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := fixtureRequest()
			change(&r)
			if _, err := CompileOpenAI(openAIProfile(), r); err == nil {
				t.Fatal("unsupported request accepted")
			}
		})
	}
	for _, kind := range []string{"tool_use", "tool_result", "image", "audio", "video", "file", "document", "thinking", "opaque_provider", "function_call_output"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := fixtureRequest()
			r.Messages[0].Content[0].Type = kind
			if _, err := CompileOpenAI(openAIProfile(), r); err == nil {
				t.Fatal("unsupported block accepted")
			}
		})
	}
	for _, key := range []string{"model", "store", "previous_response_id", "conversation", "background", "image", "audio", "video", "file", "context_management", "text", "temperature", "top_p", "reasoning", "logprobs", "metadata", "organization", "project", "prompt_cache_key", "prompt_cache_options", "prompt_cache_retention", "service_tier", "truncation", "include"} {
		t.Run("raw_"+key, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseRequest([]byte(`{"max_tokens":16,"messages":[],"` + key + `":null}`)); err == nil {
				t.Fatal("raw provider option accepted")
			}
		})
	}
	for _, schema := range []string{`null`, `[]`, `{"type":"array"}`, `{"type":"object","type":"object"}`, `{"type":"object","properties":{"x":{"type":"string","type":"number"}}}`, `{"type":"object","x":` + strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`} {
		r := fixtureRequest()
		r.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(schema)}}
		if _, err := CompileOpenAI(openAIProfile(), r); err == nil {
			t.Fatal("invalid function schema accepted")
		}
	}
	for _, choice := range []string{"required", "function", "web_search", "custom", "mcp"} {
		r := fixtureRequest()
		r.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`)}}
		r.ToolChoice = &ToolChoice{choice}
		if _, err := CompileOpenAI(openAIProfile(), r); err == nil {
			t.Fatal("unsupported tool choice accepted")
		}
	}
}

func TestOpenAIProviderIsolation(t *testing.T) {
	t.Parallel()
	// A validator accepting both providers must not let the wrong compiler,
	// reader, header set, class universe or destination cross the provider seam.
	a, o := fixtureProfile(), openAIProfile()
	ac, oc := fixtureCompiled(t, a), openAICompiled(t, o, 16)
	if _, err := CompileAnthropic(o, fixtureRequest()); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cross-provider compiler")
	}
	if _, err := CompileOpenAI(a, fixtureRequest()); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cross-provider compiler")
	}
	if _, err := ReadAnthropicStream(strings.NewReader(""), o, oc); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cross-provider stream")
	}
	if _, err := ReadOpenAIStream(strings.NewReader(""), a, ac); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cross-provider stream")
	}
	if _, err := ReadAnthropicResponse(nil, o, oc); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cross-provider response")
	}
	if _, err := ReadOpenAIResponse(nil, a, ac); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cross-provider response")
	}
	for _, row := range []struct {
		p       Profile
		c       Compiled
		headers []Header
		classes []string
		units   []Units
	}{
		{o, ac, fixtureHeaders(), inputClasses, []Units{{Uncached, 1}}},
		{a, oc, openAIHeaders(), openAIClasses[:6], []Units{{"input.uncached.short", 1}}},
	} {
		if _, err := Seal(row.p, row.c, "scope", "version", row.headers); err == nil {
			t.Fatal("cross-provider seal")
		}
		if _, _, err := NormalizeUsage(row.p, row.c, Usage{}); err == nil {
			t.Fatal("cross-provider usage")
		}
		if _, _, err := NormalizeSealedUsage(row.p, row.classes, Usage{}); err == nil {
			t.Fatal("cross-provider sealed usage")
		}
		if _, _, err := Bound(row.p, 16, row.classes); err == nil {
			t.Fatal("cross-provider bound")
		}
		if _, _, err := BoundHistorical(row.p, 16, row.classes); err == nil {
			t.Fatal("cross-provider historical bound")
		}
		if _, err := PriceUsage(row.p, row.units); err == nil {
			t.Fatal("cross-provider pricing")
		}
		if _, err := PriceHistoricalUsage(row.p, row.units); err == nil {
			t.Fatal("cross-provider historical pricing")
		}
	}
	if _, err := Seal(o, oc, "scope", "version", fixtureHeaders()); err == nil {
		t.Fatal("Anthropic header accepted by OpenAI")
	}
	if _, err := Seal(a, ac, "scope", "version", openAIHeaders()); err == nil {
		t.Fatal("OpenAI headers accepted by Anthropic")
	}
	for _, provider := range []string{"anthropic", "openai", "future"} {
		for _, authority := range []string{"api.anthropic.com", "api.openai.com", "127.0.0.1:443", "[::1]:443", "localhost:443", "127.0.0.1", "api.openai.com:443"} {
			for _, path := range []string{"/v1/messages", "/v1/responses"} {
				want := provider == "anthropic" && path == "/v1/messages" && authority == "api.anthropic.com" || provider == "openai" && path == "/v1/responses" && authority == "api.openai.com" || (authority == "127.0.0.1:443" || authority == "[::1]:443") && (provider == "anthropic" && path == "/v1/messages" || provider == "openai" && path == "/v1/responses")
				_, err := endpoint(provider, authority, path)
				if (err == nil) != want {
					t.Fatal("endpoint/provider pair escaped")
				}
			}
		}
	}
	if _, err := Maximum([]Component{{[]string{Uncached}, 1, "1", "1"}, {[]string{"output.generated.short"}, 1, "1", "1"}}); err == nil {
		t.Fatal("mixed provider maximum")
	}
}

func TestOpenAIProfileRefusals(t *testing.T) {
	t.Parallel()
	// Unsupported live facts or lookalike source hosts cannot authorize a request.
	for name, change := range map[string]func(*Profile){
		"model":     func(p *Profile) { p.ModelID = "other" },
		"provider":  func(p *Profile) { p.Provider = "future" },
		"path":      func(p *Profile) { p.Path = "/v1/messages" },
		"host":      func(p *Profile) { p.Authority = "api.anthropic.com" },
		"api":       func(p *Profile) { p.APIVersion = APIVersion },
		"region":    func(p *Profile) { p.Region = "us" },
		"tier":      func(p *Profile) { p.RequestedTier = "auto" },
		"served":    func(p *Profile) { p.AllowedServedTiers = []string{"priority"} },
		"speed":     func(p *Profile) { p.Speed = "fast" },
		"usage":     func(p *Profile) { p.UsageSchema = "other" },
		"cache":     func(p *Profile) { p.CacheClasses = []string{"input.write.12h"} },
		"no_charge": func(p *Profile) { p.NoChargeRules = []string{"refusal"} },
		"source":    func(p *Profile) { p.Authority = "api.openai.com" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := openAIProfile()
			change(&p)
			if p.Validate() == nil {
				t.Fatal("unsupported profile accepted")
			}
		})
	}
	for _, host := range []string{"developers.openai.com", "api.openai.com", "developers.openai.com.evil.invalid", "sub.developers.openai.com", "developers.openai.com:443", "developers.openai.com?credential=synthetic"} {
		p := openAIProfile()
		p.Authority = "api.openai.com"
		for i := range p.Sources {
			p.Sources[i].URL = "https://" + host + "/facts/" + p.Sources[i].Topic
		}
		want := host == "developers.openai.com" || host == "api.openai.com"
		if (p.Validate() == nil) != want {
			t.Fatal("source host boundary changed")
		}
	}
}

func TestOpenAIProfileUsageVersions(t *testing.T) {
	t.Parallel()
	// Both installation and replay select the named interpretation; a future
	// version cannot silently inherit either one.
	for _, schema := range []string{openAIUsageSchema, openAIUsageSchemaV2, "openai-responses-text-30m-kguard/v3", "other"} {
		t.Run(schema, func(t *testing.T) {
			t.Parallel()
			p := openAIProfile()
			p.UsageSchema = schema
			want := schema == openAIUsageSchema || schema == openAIUsageSchemaV2
			if (p.Validate() == nil) != want || (p.ValidateHistorical() == nil) != want {
				t.Fatal("usage version installation or replay policy changed")
			}
			raw, _ := json.Marshal(p)
			if _, err := ParseProfile(raw); (err == nil) != want {
				t.Fatal("strict profile parser disagrees with validation")
			}
			if want {
				a, err := p.SHA256()
				q := p
				q.UsageSchema = openAIUsageSchemaV2
				if schema == openAIUsageSchemaV2 {
					q.UsageSchema = openAIUsageSchema
				}
				b, other := q.SHA256()
				if err != nil || other != nil || a == b {
					t.Fatal("usage interpretation not bound into profile digest")
				}
			}
		})
	}
}

// The compiler shares RequestLimit; Core owns the smaller admission guard.
// A provider-specific cap or an off-by-one would reject the boundary request.
func TestOpenAICompilerRequestLimit(t *testing.T) {
	t.Parallel()
	p := openAIProfile()
	r := fixtureRequest()
	c, err := CompileOpenAI(p, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Messages[0].Content[0].Text += strings.Repeat("x", RequestLimit-len(c.Body()))
	c, err = CompileOpenAI(p, r)
	if err != nil || len(c.Body()) != RequestLimit {
		t.Fatal("shared request-size boundary rejected")
	}
	r.Messages[0].Content[0].Text += "x"
	if _, err := CompileOpenAI(p, r); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized request accepted")
	}
}

func FuzzOpenAIRequest(f *testing.F) {
	for _, raw := range [][]byte{[]byte(`{"max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`), []byte(`{"max_tokens":16,"max_tokens":17}`), []byte(`{"messages":[{"content":null}]}`), {255}, []byte(`{"max_tokens":9223372036854775808}`), []byte(`{"max_tokens":16`), []byte(`{"store":true}`)} {
		f.Add(raw)
	}
	f.Add([]byte(`{"max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("x", RequestLimit/2) + `"}]}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, err := ParseRequest(raw)
		if err != nil {
			return
		}
		p := openAIProfile()
		c, err := CompileOpenAI(p, r)
		if err != nil {
			return
		}
		if len(c.Body()) > RequestLimit || checkJSON(c.Body(), RequestLimit, false, false) != nil {
			t.Fatal("invalid compiled body")
		}
		again, err := CompileOpenAI(p, r)
		if err != nil || !bytes.Equal(c.Body(), again.Body()) {
			t.Fatal("nondeterministic compilation")
		}
		if _, _, err := Bound(p, c.OutputMaximum(), c.InputClasses()); err != nil {
			t.Fatal("compiled request has no bound")
		}
	})
}

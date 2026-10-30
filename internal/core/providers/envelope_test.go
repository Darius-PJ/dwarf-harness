package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestRequestFramingBindsEveryField(t *testing.T) {
	t.Parallel()
	d := Descriptor{"anthropic", "127.0.0.1:443", Haiku, "global", "POST", "/v1/messages", "standard_only", "scope-fixture", "version-fixture", fixtureHeaders()}
	hs, e := semanticHeaders(d.Provider, d.Headers)
	if e != nil {
		t.Fatal(e)
	}
	d.Headers = hs
	body := []byte(`{"sample":"body"}`)
	// Independent framing oracle: append fixed-width integers, never the
	// production frame helper. This catches framing and order changes as well.
	var want []byte
	appendFrame := func(b []byte) { want = binary.BigEndian.AppendUint64(want, uint64(len(b))); want = append(want, b...) }
	for _, s := range []string{"router-provider-request/v1", "anthropic", "127.0.0.1:443", Haiku, "global", "POST", "/v1/messages", "standard_only", "scope-fixture", "version-fixture"} {
		appendFrame([]byte(s))
	}
	want = binary.BigEndian.AppendUint32(want, 3)
	for _, h := range []Header{{"accept", "text/event-stream"}, {"anthropic-version", APIVersion}, {"content-type", "application/json"}} {
		appendFrame([]byte(h.Name))
		appendFrame([]byte(h.Value))
	}
	appendFrame(body)
	got := requestPreimage(d, body)
	if !bytes.Equal(got, want) {
		t.Fatal("request preimage differs from specified framing")
	}
	base := sha256.Sum256(got)
	v := reflect.ValueOf(&d).Elem()
	for i := range v.NumField() - 1 {
		name := v.Type().Field(i).Name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := d
			field := reflect.ValueOf(&q).Elem().Field(i)
			field.SetString(field.String() + "x")
			if sha256.Sum256(requestPreimage(q, body)) == base {
				t.Fatal("unbound request field")
			}
		})
	}
	for i := range hs {
		t.Run("header_"+hs[i].Name, func(t *testing.T) {
			t.Parallel()
			q := d
			q.Headers = slices.Clone(hs)
			q.Headers[i].Value += "x"
			if sha256.Sum256(requestPreimage(q, body)) == base {
				t.Fatal("unbound header")
			}
		})
	}
	if sha256.Sum256(requestPreimage(d, append(bytes.Clone(body), ' '))) == base {
		t.Fatal("unbound exact body")
	}
}

func TestQuoteFramingUsesHexTariffAndDisjointSortedComponents(t *testing.T) {
	t.Parallel()
	request := sha256.Sum256([]byte("request"))
	tariff := sha256.Sum256([]byte("tariff"))
	cs := []Component{{[]string{Uncached, CacheRead}, 2, "1", "3"}, {[]string{Generated}, 1, "1", "3"}}
	got, e := QuoteSHA256(request, "cap", "tariff", tariff, 1, cs)
	if e != nil {
		t.Fatal(e)
	}
	var pre []byte
	add := func(b []byte) { pre = binary.BigEndian.AppendUint64(pre, uint64(len(b))); pre = append(pre, b...) }
	for _, b := range [][]byte{[]byte("router-cost-quote/v1"), request[:], []byte("cap"), []byte("tariff"), []byte(hex.EncodeToString(tariff[:])), []byte("USD"), []byte("1")} {
		add(b)
	}
	var encoded [][]byte
	for _, row := range []struct {
		classes []string
		values  []string
	}{{[]string{CacheRead, Uncached}, []string{"2", "1", "3"}}, {[]string{Generated}, []string{"1", "1", "3"}}} {
		b := binary.BigEndian.AppendUint32(nil, uint32(len(row.classes)))
		for _, s := range append(slices.Clone(row.classes), row.values...) {
			b = binary.BigEndian.AppendUint64(b, uint64(len(s)))
			b = append(b, s...)
		}
		encoded = append(encoded, b)
	}
	slices.SortFunc(encoded, bytes.Compare)
	pre = binary.BigEndian.AppendUint32(pre, 2)
	for _, b := range encoded {
		add(b)
	}
	if got != sha256.Sum256(pre) {
		t.Fatal("quote framing mismatch")
	}
	slices.Reverse(cs)
	again, e := QuoteSHA256(request, "cap", "tariff", tariff, 1, cs)
	if e != nil || again != got {
		t.Fatal("component order changed identity")
	}
	for _, row := range []struct {
		name   string
		change func(*[32]byte, *string, *string, *[32]byte, []Component)
	}{
		{"request", func(r *[32]byte, _ *string, _ *string, _ *[32]byte, _ []Component) { r[0] ^= 1 }},
		{"capability", func(_ *[32]byte, c *string, _ *string, _ *[32]byte, _ []Component) { *c += "x" }},
		{"tariff_version", func(_ *[32]byte, _ *string, v *string, _ *[32]byte, _ []Component) { *v += "x" }},
		{"tariff_digest", func(_ *[32]byte, _ *string, _ *string, h *[32]byte, _ []Component) { h[0] ^= 1 }},
		{"units", func(_ *[32]byte, _ *string, _ *string, _ *[32]byte, c []Component) { c[0].MaxUnits++ }},
		{"numerator", func(_ *[32]byte, _ *string, _ *string, _ *[32]byte, c []Component) { c[0].Numerator = "2" }},
		{"denominator", func(_ *[32]byte, _ *string, _ *string, _ *[32]byte, c []Component) { c[0].Denominator = "4" }},
		{"classes", func(_ *[32]byte, _ *string, _ *string, _ *[32]byte, c []Component) { c[1].Classes = []string{Uncached} }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			r, h := request, tariff
			c, v := "cap", "tariff"
			parts := slices.Clone(cs)
			row.change(&r, &c, &v, &h, parts)
			n, e := Maximum(parts)
			if e != nil {
				t.Fatal(e)
			}
			changed, e := QuoteSHA256(r, c, v, h, n, parts)
			if e != nil || changed == got {
				t.Fatalf("unbound quote field: %v", e)
			}
		})
	}
}

func TestEnvelopeRefusesAmbiguousURLAndUnsealedHeaders(t *testing.T) {
	t.Parallel()
	for _, authority := range []string{"api.anthropic.com:443", "user@api.anthropic.com", "api.anthropic.com?x", "api.anthropic.com#x", "example.com", "127.0.0.1:0", "127.0.0.1:0443", "127.0.0.1:99999", "127.0.0.1:443\\evil"} {
		t.Run(authority, func(t *testing.T) {
			t.Parallel()
			if _, e := endpoint("anthropic", authority, "/v1/messages"); e == nil {
				t.Fatal("ambiguous authority accepted")
			}
		})
	}
	for _, path := range []string{"", "//v1/messages", "/v1/%6dessages", "/v1/../messages", "/v1/messages?", "/v1/messages#", "https://example.com/v1/messages"} {
		t.Run("path_"+path, func(t *testing.T) {
			t.Parallel()
			if _, e := endpoint("anthropic", "127.0.0.1:443", path); e == nil {
				t.Fatal("ambiguous path accepted")
			}
		})
	}
	for _, row := range []struct {
		name   string
		header Header
	}{
		{"beta", Header{"anthropic-beta", "beta"}},
		{"authorization", Header{"Authorization", "synthetic"}},
		{"case_folded_duplicate", Header{"Accept", "text/event-stream"}},
		{"control", Header{"content-type", "application/json\r\nx: y"}},
		{"nul", Header{"accept", "text/event-stream\x00"}},
		{"wrong_version", Header{"anthropic-version", "2023-06-01"}},
	} {
		t.Run("header_"+row.name, func(t *testing.T) {
			t.Parallel()
			headers := fixtureHeaders()
			headers[0] = row.header
			if _, e := semanticHeaders("anthropic", headers); e == nil {
				t.Fatal("unsealed header accepted")
			}
		})
	}
	t.Run("extra_header", func(t *testing.T) {
		t.Parallel()
		if _, e := semanticHeaders("anthropic", append(fixtureHeaders(), Header{"x-extra", "value"})); e == nil {
			t.Fatal("extra semantic header accepted")
		}
	})
	p := fixtureProfile()
	c := fixtureCompiled(t, p)
	e, err := Seal(p, c, "scope", "version", fixtureHeaders())
	if err != nil {
		t.Fatal(err)
	}
	body := e.Body()
	body[0] = '!'
	d := e.Descriptor()
	d.Headers[0].Value = "changed"
	if e.Body()[0] == '!' || e.Descriptor().Headers[0].Value == "changed" {
		t.Fatal("envelope alias")
	}
	p.OutputMax++
	if _, err = Seal(p, c, "scope", "version", fixtureHeaders()); err == nil {
		t.Fatal("post_quote_change_before_intent accepted")
	}
}

func TestCompilerRejectsUnsupportedStaticFeatures(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"temperature", "top_p", "top_k", "thinking", "effort", "output_config", "cache_control", "container", "metadata", "stop_sequences", "fallback", "iterations", "stream", "model", "inference_geo", "speed"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			b := []byte(`{"max_tokens":1,"messages":[],"` + field + `":0}`)
			if _, e := ParseRequest(b); e == nil {
				t.Fatal("unsupported request field accepted")
			}
		})
	}
	for _, typ := range []string{"image", "document", "search_result", "server_tool_use", "web_search_tool_result", "thinking", "redacted_thinking", "compaction", "container_upload"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			r := fixtureRequest()
			r.Messages[0].Content[0].Type = typ
			if _, e := CompileAnthropic(fixtureProfile(), r); e == nil {
				t.Fatal("unsupported content accepted")
			}
		})
	}
	for _, row := range []struct {
		name   string
		mutate func(*Request, *Profile)
	}{
		{"zero_output", func(r *Request, _ *Profile) { r.MaxTokens = 0 }},
		{"excess_output", func(r *Request, p *Profile) { r.MaxTokens = p.OutputMax + 1 }},
		{"prefill", func(r *Request, _ *Profile) { r.Messages[0].Role = "assistant" }},
		{"empty_text", func(r *Request, _ *Profile) { r.Messages[0].Content[0].Text = "" }},
		{"invalid_utf8", func(r *Request, _ *Profile) { r.Messages[0].Content[0].Text = string([]byte{0xff}) }},
		{"tool_without_definition", func(r *Request, _ *Profile) { r.ToolChoice = &ToolChoice{"auto"} }},
		{"forced_tool", func(r *Request, _ *Profile) {
			r.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`)}}
			r.ToolChoice = &ToolChoice{"any"}
		}},
		{"cache_type", func(r *Request, _ *Profile) {
			r.Messages[0].Content[0].CacheControl = &CacheControl{"persistent", "5m"}
		}},
		{"cache_ttl", func(r *Request, _ *Profile) { r.Messages[0].Content[0].CacheControl = &CacheControl{"ephemeral", "2h"} }},
		{"duplicate_tools", func(r *Request, _ *Profile) {
			r.Tools = []Tool{{Name: "x", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "x", InputSchema: json.RawMessage(`{"type":"object"}`)}}
		}},
		{"bad_tool_schema", func(r *Request, _ *Profile) {
			r.Tools = []Tool{{Name: "x", InputSchema: json.RawMessage(`{"type":"array"}`)}}
		}},
		{"unbound_result", func(r *Request, _ *Profile) {
			r.Messages[0].Content = []Block{{Type: "tool_result", ToolUseID: "missing", Content: []Text{{Type: "text", Text: "result"}}}}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			r, p := fixtureRequest(), fixtureProfile()
			row.mutate(&r, &p)
			if _, e := CompileAnthropic(p, r); e == nil {
				t.Fatal("unsupported variant accepted")
			}
		})
	}
	for _, model := range []string{Haiku, Sonnet, Opus} {
		t.Run("prior_tools_"+model, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			p.ModelID = model
			if model != Haiku {
				p.RequestedTier = "auto"
			}
			r := fixtureRequest()
			r.Messages = append([]Message{{Role: "assistant", Content: []Block{{Type: "tool_use", ID: "call", Name: "local", Input: json.RawMessage(`{}`)}}}}, Message{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "call", Content: []Text{{Type: "text", Text: "result"}}}}})
			_, e := CompileAnthropic(p, r)
			if (e == nil) != (model == Haiku) {
				t.Fatalf("prior tool boundary: %v", e)
			}
		})
	}
}

func TestCompilerCanonicalBodyAndCacheHierarchy(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	r := fixtureRequest()
	c, e := CompileAnthropic(p, r)
	if e != nil {
		t.Fatal(e)
	}
	want := `{"model":"claude-haiku-4-5-20251001","max_tokens":20,"messages":[{"role":"user","content":[{"type":"text","text":"local arithmetic fixture"}]}],"stream":true,"service_tier":"standard_only"}`
	if string(c.Body()) != want {
		t.Fatal("canonical request differs")
	}
	r.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`), CacheControl: &CacheControl{"ephemeral", "1h"}}}
	r.System = []Text{{Type: "text", Text: "system", CacheControl: &CacheControl{"ephemeral", ""}}}
	c, e = CompileAnthropic(p, r)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(c.Body(), []byte(`"tool_choice":{"type":"auto"}`)) || !bytes.Contains(c.Body(), []byte(`"ttl":"5m"`)) || r.System[0].CacheControl.TTL != "" {
		t.Fatal("explicit defaults or ownership broken")
	}
	if !slices.Equal(c.InputClasses(), inputClasses) {
		t.Fatal("cache write class absent from quote")
	}
	r.Tools[0].CacheControl.TTL = "5m"
	r.System[0].CacheControl.TTL = "1h"
	if _, e = CompileAnthropic(p, r); e == nil {
		t.Fatal("cache hierarchy order ignored")
	}
	r.Tools = nil
	r.System = nil
	r.Messages[0].Content = nil
	for range 5 {
		r.Messages[0].Content = append(r.Messages[0].Content, Block{Type: "text", Text: "break", CacheControl: &CacheControl{"ephemeral", "5m"}})
	}
	if _, e = CompileAnthropic(p, r); e == nil {
		t.Fatal("excess cache breakpoints accepted")
	}
}

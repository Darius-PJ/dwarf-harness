package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"testing"
)

func eventJSON(s string) string { return "data: " + s + "\n\n" }
func streamStart(usage string) string {
	return eventJSON(`{"type":"message_start","message":{"id":"local-message","type":"message","role":"assistant","content":[],"model":"` + Haiku + `","usage":` + usage + `}}`)
}
func streamDelta(usage string) string {
	return eventJSON(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + usage + `}`)
}
func streamStop() string { return eventJSON(`{"type":"message_stop"}`) }
func textEvents() string {
	return eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"initial "}}`) + eventJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"snow 雪"}}`) + eventJSON(`{"type":"content_block_stop","index":0}`)
}
func plainStream() string {
	return streamStart(`{"input_tokens":2,"output_tokens":1}`) + textEvents() + streamDelta(`{"output_tokens":7}`) + streamStop()
}

type fragmentReader struct {
	data []byte
	step int
}

func (r *fragmentReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(r.data), r.step)
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestSSEFramingAndCumulativeReplacement(t *testing.T) {
	t.Parallel()
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		t.Run(fmt.Sprintf("separator_%x", sep), func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			c := fixtureCompiled(t, p)
			stream := "\ufeff: comment\nretry: 1\nid: ignored\n\n" + plainStream()
			stream = strings.ReplaceAll(stream, "\n", sep)
			result, e := ReadAnthropicStream(&fragmentReader{[]byte(stream), 1}, p, c)
			if e != nil || !result.Complete || !result.Priceable || !result.OutputStarted || len(result.Blocks) != 1 || result.Blocks[0].Text != "initial snow 雪" {
				t.Fatalf("fragmented stream: %v", e)
			}
			for _, u := range result.Units {
				if u.Class == Generated && u.Count != 7 {
					t.Fatal("cumulative output summed")
				}
			}
		})
	}
	t.Run("multiline_and_unknown", func(t *testing.T) {
		t.Parallel()
		p := fixtureProfile()
		c := fixtureCompiled(t, p)
		start := strings.Replace(streamStart(`{"input_tokens":2,"output_tokens":1}`), `,"message":`, ",\ndata: \"message\":", 1)
		stream := start + eventJSON(`{"type":"future_event","anything":1}`) + eventJSON(`{"type":"ping"}`) + streamDelta(`{"output_tokens":7}`) + streamStop()
		r, e := ReadAnthropicStream(strings.NewReader(stream), p, c)
		if e != nil || !r.Complete || !slices.Equal(r.UnknownEventTypes, []string{"future_event"}) || r.EventCount != 5 {
			t.Fatalf("multiline/unknown handling: %v", e)
		}
	})
}

func TestSSERejectsIncompleteAndInvalidTransitions(t *testing.T) {
	t.Parallel()
	valid := plainStream()
	start := streamStart(`{"input_tokens":2,"output_tokens":1}`)
	for _, row := range []struct{ name, stream string }{
		{"eof_is_not_stop", strings.TrimSuffix(valid, streamStop())},
		{"unterminated_event", strings.TrimSuffix(valid, "\n\n")},
		{"duplicate_start", start + valid},
		{"stop_without_delta", start + streamStop()},
		{"delta_before_start", streamDelta(`{"output_tokens":1}`) + streamStop()},
		{"block_gap", start + strings.Replace(textEvents(), `"index":0`, `"index":1`, 1)},
		{"reopened_block", start + textEvents() + textEvents()},
		{"delta_wrong_type", strings.Replace(valid, `"text_delta"`, `"thinking_delta"`, 1)},
		{"unknown_block", strings.Replace(valid, `"type":"text"`, `"type":"future_block"`, 1)},
		{"event_type_mismatch", "event: message_stop\n" + valid},
		{"duplicate_json", strings.Replace(valid, `"input_tokens":2`, `"input_tokens":2,"input_tokens":9`, 1)},
		{"invalid_utf8", start + "data: \xff\n\n"},
		{"provider_error", start + eventJSON(`{"type":"error","error":{"type":"overloaded_error","message":"synthetic"}}`)},
		{"unsupported_stop", strings.Replace(valid, `"end_turn"`, `"pause_turn"`, 1)},
		{"trailing_event", valid + eventJSON(`{"type":"message_stop"}`)},
		{"null_start_content", strings.Replace(valid, `"content":[]`, `"content":null`, 1)},
		{"wrong_model", strings.Replace(valid, Haiku, Sonnet, 1)},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			r, e := ReadAnthropicStream(strings.NewReader(row.stream), p, fixtureCompiled(t, p))
			if e == nil || r.Complete || r.Priceable || len(r.Blocks) != 0 {
				t.Fatal("invalid stream became a completed message")
			}
		})
	}
}

func cacheCompiled(t *testing.T, p Profile, five, hour bool) Compiled {
	t.Helper()
	r := fixtureRequest()
	if hour {
		r.System = []Text{{Type: "text", Text: "hour", CacheControl: &CacheControl{"ephemeral", "1h"}}}
	}
	if five {
		r.Messages[0].Content[0].CacheControl = &CacheControl{"ephemeral", "5m"}
	}
	c, e := CompileAnthropic(p, r)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestUsageNullOmissionAndFinalCounter(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, start, middle, final string
		priceable                  bool
		input                      int64
	}{
		{"replace", `{"input_tokens":2,"output_tokens":1}`, `{"input_tokens":5,"output_tokens":3}`, `{"output_tokens":7}`, true, 5},
		{"null_invalidates", `{"input_tokens":2,"output_tokens":1}`, `{"input_tokens":null,"output_tokens":3}`, `{"output_tokens":7}`, false, 0},
		{"null_replaced", `{"input_tokens":null,"output_tokens":1}`, `{"input_tokens":5,"output_tokens":3}`, `{"output_tokens":7}`, true, 5},
		{"terminal_output_missing", `{"input_tokens":2,"output_tokens":1}`, `{"output_tokens":3}`, `{}`, false, 0},
		{"terminal_output_null", `{"input_tokens":2,"output_tokens":1}`, `{"output_tokens":3}`, `{"output_tokens":null}`, false, 0},
		{"null_cache_not_zero", `{"input_tokens":2,"output_tokens":1,"cache_read_input_tokens":null}`, `{}`, `{"output_tokens":7}`, false, 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			r, e := ReadAnthropicStream(strings.NewReader(streamStart(row.start)+streamDelta(row.middle)+streamDelta(row.final)+streamStop()), p, fixtureCompiled(t, p))
			if e != nil || !r.Complete || r.Priceable != row.priceable {
				t.Fatalf("usage availability: %v", e)
			}
			if r.Priceable {
				for _, u := range r.Units {
					if u.Class == Uncached && u.Count != row.input {
						t.Fatal("cumulative input did not replace")
					}
				}
			}
		})
	}
}
func TestUsageExclusiveCacheAndReasoning(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                 string
		five, hour           bool
		usage                string
		want                 int64
		available, violation bool
	}{
		{"cache_aggregate_not_added_to_ttl_children", true, true, `{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":11,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":6}}`, 23, true, false},
		{"reasoning_not_added_to_output", false, false, `{"input_tokens":2,"output_tokens":7,"output_tokens_details":{"thinking_tokens":5}}`, 9, true, false},
		{"single_ttl_aggregate", true, false, `{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":11}`, 23, true, false},
		{"mixed_split_missing", true, true, `{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":11}`, 0, false, false},
		{"zero_aggregate", true, true, `{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":0}`, 12, true, false},
		{"aggregate_disagrees", true, true, `{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":6}}`, 0, false, true},
		{"unexpected_priced_write", false, false, `{"input_tokens":2,"output_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":0}}`, 14, true, true},
		{"unknown_dimension", false, false, `{"input_tokens":2,"output_tokens":7,"future_tokens":1}`, 0, false, true},
		{"unknown_null", false, false, `{"input_tokens":2,"output_tokens":7,"future_tokens":null}`, 9, true, false},
		{"wrong_tier", false, false, `{"input_tokens":2,"output_tokens":7,"service_tier":"priority"}`, 0, false, true},
		{"server_tool", false, false, `{"input_tokens":2,"output_tokens":7,"server_tool_use":{"web_search_requests":1}}`, 0, false, true},
		{"reasoning_exceeds_output", false, false, `{"input_tokens":2,"output_tokens":7,"output_tokens_details":{"thinking_tokens":8}}`, 0, false, true},
		{"null_split", true, false, `{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":5,"cache_creation":null}`, 0, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			c := cacheCompiled(t, p, row.five, row.hour)
			var usage Usage
			_ = json.Unmarshal([]byte(row.usage), &usage)
			units, violation, e := NormalizeUsage(p, c, usage)
			if (e == nil) != row.available || violation != row.violation {
				t.Fatalf("normalization availability=%v violation=%v", e == nil, violation)
			}
			if e == nil {
				price, e := PriceUsage(p, units)
				if e != nil || price != row.want {
					t.Fatalf("exclusive price %d: %v", price, e)
				}
			}
		})
	}
	t.Run("sum_above_int64_keeps_counters", func(t *testing.T) {
		t.Parallel()
		p := fixtureProfile()
		c := cacheCompiled(t, p, true, true)
		raw := Usage{}
		_ = json.Unmarshal([]byte(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":1,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}`, math.MaxInt64, math.MaxInt64, math.MaxInt64)), &raw)
		units, _, e := NormalizeUsage(p, c, raw)
		if e != nil {
			t.Fatal("lost truthful overrun counters")
		}
		if _, e = PriceUsage(p, units); e != ErrOverflow {
			t.Fatal("overflow price clamped")
		}
	})
	t.Run("nested_cumulative_merge", func(t *testing.T) {
		t.Parallel()
		p := fixtureProfile()
		c := cacheCompiled(t, p, true, true)
		r, e := ReadAnthropicStream(strings.NewReader(streamStart(`{"input_tokens":2,"output_tokens":1,"cache_read_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":6}}`)+streamDelta(`{"cache_creation":{"ephemeral_5m_input_tokens":8},"cache_creation_input_tokens":14,"output_tokens":7}`)+streamStop()), p, c)
		if e != nil || !r.Priceable {
			t.Fatal("nested omission lost prior sibling")
		}
		price, e := PriceUsage(p, r.Units)
		if e != nil || price != 26 {
			t.Fatal("nested snapshots summed or overwritten")
		}
	})
}

func TestSSEPreservesOpaqueAndDrainsInvalidToolJSON(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	request := fixtureRequest()
	request.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	c, err := CompileAnthropic(p, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			t.Parallel()
			partial := `{"x":1}`
			if invalid {
				partial = `{"x":`
			}
			encoded, _ := json.Marshal(partial)
			stream := streamStart(`{"input_tokens":2,"output_tokens":1}`) + eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"opaque ","signature":""}}`) + eventJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thought"}}`) + eventJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed"}}`) + eventJSON(`{"type":"content_block_stop","index":0}`) + eventJSON(`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-data"}}`) + eventJSON(`{"type":"content_block_stop","index":1}`) + eventJSON(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-fixture","name":"local","input":{}}}`) + eventJSON(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":`+string(encoded)+`}}`) + eventJSON(`{"type":"content_block_stop","index":2}`) + streamDelta(`{"output_tokens":7}`) + streamStop()
			r, e := ReadAnthropicStream(strings.NewReader(stream), p, c)
			if e != nil || !r.Complete || !r.Priceable || r.ContentError {
				t.Fatalf("opaque/tool completion: %v", e)
			}
			if invalid {
				if len(r.Blocks) != 3 || r.Blocks[2].Type != "local_tool_request" || r.Blocks[2].ArgumentsText == nil || *r.Blocks[2].ArgumentsText != partial || r.Replay != nil {
					t.Fatal("invalid arguments lost correction evidence or became replayable")
				}
			} else {
				if len(r.Blocks) != 3 || r.Blocks[0].Thinking != "opaque thought" || r.Blocks[0].Signature != "signed" || r.Blocks[1].Data != "opaque-data" || string(r.Blocks[2].Input) != `{"x":1}` {
					t.Fatal("opaque payload changed")
				}
				if string(r.Blocks[0].OpaquePayload) != `{"type":"thinking","thinking":"opaque thought","signature":"signed"}` || string(r.Blocks[1].OpaquePayload) != `{"type":"redacted_thinking","data":"opaque-data"}` {
					t.Fatal("completed opaque provider payload changed")
				}
			}
		})
	}
}

func TestSSEBoundsBeforeSuccessfulCompletion(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name  string
		build func() string
	}{
		{"line", func() string { return ":" + strings.Repeat("x", streamLineLimit) + "\n\n" }},
		{"event", func() string { return strings.Repeat(": comments\n", streamLineLimit/8) + "\n" }},
		{"depth", func() string {
			return eventJSON(`{"type":"future","data":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + `}`)
		}},
		{"events", func() string { return strings.Repeat(eventJSON(`{"type":"ping"}`), streamEventLimit+1) }},
		{"body", func() string { return strings.Repeat(": comment\n\n", streamBodyLimit/10+1) }},
		{"blocks", func() string {
			var b strings.Builder
			b.WriteString(streamStart(`{"input_tokens":1,"output_tokens":1}`))
			for i := range streamBlockLimit + 1 {
				b.WriteString(eventJSON(fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i)))
				b.WriteString(eventJSON(fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i)))
			}
			return b.String()
		}},
		{"output", func() string {
			var b strings.Builder
			b.WriteString(streamStart(`{"input_tokens":1,"output_tokens":1}`))
			b.WriteString(eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
			for range 10 {
				b.WriteString(eventJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + strings.Repeat("x", 900000) + `"}}`))
			}
			return b.String()
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			r, e := ReadAnthropicStream(strings.NewReader(row.build()), p, fixtureCompiled(t, p))
			if e == nil || r.Complete || r.Blocks != nil {
				t.Fatal("limit became successful truncation")
			}
		})
	}
}

func FuzzProviderSSE(f *testing.F) {
	for _, s := range []string{plainStream(), strings.ReplaceAll(plainStream(), "\n", "\r\n"), strings.TrimSuffix(plainStream(), streamStop()), streamStart(`{"input_tokens":null,"output_tokens":1}`) + streamDelta(`{"output_tokens":null}`) + streamStop(), strings.Replace(plainStream(), `"index":0`, `"index":0,"index":0`, 1)} {
		f.Add([]byte(s), uint8(1))
	}
	for _, args := range []string{`{"n":1}`, `{"n":1,"n":2}`, `[]`, `{`} {
		encoded, _ := json.Marshal(args)
		stream := streamStart(`{"input_tokens":2,"output_tokens":1}`) + eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"synthetic-call","name":"undeclared","input":{}}}`) + eventJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+string(encoded)+`}}`) + eventJSON(`{"type":"content_block_stop","index":0}`) + streamDelta(`{"output_tokens":7}`) + streamStop()
		f.Add([]byte(stream), uint8(1))
	}
	f.Fuzz(func(t *testing.T, b []byte, step uint8) {
		p := fixtureProfile()
		c := fixtureCompiled(t, p)
		a, e := ReadAnthropicStream(&fragmentReader{bytes.Clone(b), int(step) + 1}, p, c)
		z, other := ReadAnthropicStream(bytes.NewReader(b), p, c)
		if (e == nil) != (other == nil) || a.Complete != z.Complete || a.Priceable != z.Priceable {
			t.Fatal("fragment-dependent interpretation")
		}
		if e != nil && len(a.Blocks) != 0 {
			t.Fatal("incomplete canonical output")
		}
	})
}

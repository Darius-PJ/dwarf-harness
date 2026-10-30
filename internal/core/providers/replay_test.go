package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func replayRequest(provider string) Request {
	r := fixtureRequest()
	raw := `[{"type":"thinking","thinking":"","signature":"syntheticValue"},{"type":"redacted_thinking","data":"opaque"}, {"input":{"x":"\u96ea"},"name":"local","id":"call-a","type":"tool_use"},{"type":"tool_use","id":"call-b","name":"local","input":{}}]`
	if provider == "openai" {
		raw = `[{"type":"reasoning","id":"reasoning-item","summary":[],"encrypted_content":"syntheticValue"}, {"type":"message","id":"message-item","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"\u96ea","annotations":[],"logprobs":[]}]},{"type":"function_call","id":"item-a","call_id":"call-a","name":"local","arguments":"{\"x\":1}"},{"type":"function_call","id":"item-b","call_id":"call-b","name":"local","arguments":"{}"}]`
	}
	r.Messages = append(r.Messages, Message{Role: "assistant", Content: []Block{{Type: provider + "_replay", Replay: json.RawMessage(raw)}}}, Message{Role: "user", Content: []Block{
		{Type: "tool_result", ToolUseID: "call-a", Content: []Text{{Type: "text", Text: `{"schema":1,"status":"ok"}`}}},
		{Type: "tool_result", ToolUseID: "call-b", Content: []Text{{Type: "text", Text: `{"schema":1,"status":"error"}`}}},
	}})
	return r
}

func TestCompilerReplayMatchedBatch(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			p, compile := fixtureProfile(), CompileAnthropic
			if provider == "openai" {
				p, compile = openAIProfile(), CompileOpenAI
			}
			r := replayRequest(provider)
			c, err := compile(p, r)
			if err != nil {
				t.Fatal(err)
			}
			var items []json.RawMessage
			if json.Unmarshal(r.Messages[1].Content[0].Replay, &items) != nil {
				t.Fatal("fixture")
			}
			for _, item := range items {
				if !bytes.Contains(c.Body(), item) {
					t.Fatal("stored item bytes changed")
				}
			}
			if provider == "anthropic" && !bytes.Contains(c.Body(), r.Messages[1].Content[0].Replay) {
				t.Fatal("stored assistant array changed")
			}
			if provider == "openai" && !bytes.Contains(c.Body(), []byte(`{"type":"function_call_output","call_id":"call-a","output":"{\"schema\":1,\"status\":\"ok\"}"}`)) {
				t.Fatal("result identity or encoding changed")
			}
			for name, mutate := range map[string]func(*Request){
				"unmatched":     func(r *Request) { r.Messages[2].Content[0].ToolUseID = "other" },
				"half_matched":  func(r *Request) { r.Messages[2].Content = r.Messages[2].Content[:1] },
				"missing_batch": func(r *Request) { r.Messages[2].Content = []Block{{Type: "text", Text: "next"}} },
				"reordered": func(r *Request) {
					r.Messages[2].Content[0], r.Messages[2].Content[1] = r.Messages[2].Content[1], r.Messages[2].Content[0]
				},
				"duplicate_result": func(r *Request) { r.Messages[2].Content[1] = r.Messages[2].Content[0] },
				"duplicate_call": func(r *Request) {
					r.Messages[1].Content[0].Replay = bytes.ReplaceAll(r.Messages[1].Content[0].Replay, []byte("call-b"), []byte("call-a"))
				},
				"text_before_result": func(r *Request) {
					r.Messages[2].Content = append([]Block{{Type: "text", Text: "next"}}, r.Messages[2].Content...)
				},
				"intervening_assistant": func(r *Request) {
					r.Messages = append(r.Messages[:2], append([]Message{{Role: "assistant", Content: []Block{{Type: "text", Text: "next"}}}}, r.Messages[2:]...)...)
				},
				"mixed_replay":   func(r *Request) { r.Messages[1].Content[0].Text = "mixed" },
				"user_replay":    func(r *Request) { r.Messages[1].Role = "user" },
				"cross_provider": func(r *Request) { r.Messages[1].Content[0].Type = "foreign_replay" },
				"hosted_item": func(r *Request) {
					r.Messages[1].Content[0].Replay = json.RawMessage(`[{"type":"server_tool_use","id":"call-a"}]`)
				},
				"duplicate_input": func(r *Request) {
					r.Messages[1].Content[0].Replay = bytes.ReplaceAll(r.Messages[1].Content[0].Replay, []byte(`"type":"reasoning"`), []byte(`"type":"reasoning","type":"reasoning"`))
					if provider == "anthropic" {
						r.Messages[1].Content[0].Replay = bytes.ReplaceAll(r.Messages[1].Content[0].Replay, []byte(`"input":{}`), []byte(`"input":{"x":1,"x":2}`))
					}
				},
			} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					r := replayRequest(provider)
					mutate(&r)
					if _, err := compile(p, r); err == nil {
						t.Fatal("unsafe batch compiled")
					}
				})
			}
		})
	}
	for _, model := range []string{Haiku, Sonnet, Opus} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			p.ModelID = model
			if model != Haiku {
				p.RequestedTier = "auto"
			}
			if _, err := CompileAnthropic(p, replayRequest("anthropic")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnthropicUntrustedToolRequests(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, args              string
		invalidName, renderable bool
	}{
		{"local", `{"x":1,"x":2}`, false, false},
		{"local", `[]`, false, false},
		{"local", `null`, false, false},
		{"undeclared", `{}`, false, true},
		{strings.Repeat("a", 64), `{}`, false, true},
		{strings.Repeat("a", 65), `{}`, true, false},
		{"dot.name", `{}`, true, false},
		{"space name", `{}`, true, false},
	} {
		t.Run(fmt.Sprintf("%s_%s", row.name, row.args), func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			c := fixtureCompiled(t, p)
			stream := streamStart(`{"input_tokens":2,"output_tokens":1}`) + eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-a","name":"`+row.name+`","input":`+row.args+`}}`) + eventJSON(`{"type":"content_block_stop","index":0}`) + strings.Replace(streamDelta(`{"output_tokens":7}`), "end_turn", "tool_use", 1) + streamStop()
			r, err := ReadAnthropicStream(strings.NewReader(stream), p, c)
			if row.invalidName {
				if err == nil || r.Blocks != nil {
					t.Fatal("invalid name accepted")
				}
				return
			}
			if err != nil || r.ContentError || !r.Priceable || len(r.Blocks) != 1 || r.Blocks[0].Type != "local_tool_request" || r.Blocks[0].ArgumentsText == nil || *r.Blocks[0].ArgumentsText != row.args || (r.Replay != nil) != row.renderable {
				t.Fatal("argument failure erased evidence or became executable")
			}
			encoded, _ := compact(r.Blocks[0])
			var candidate map[string]json.RawMessage
			if json.Unmarshal(encoded, &candidate) != nil || candidate["arguments_text"] == nil {
				t.Fatal("candidate omitted empty arguments")
			}
		})
	}
}

func TestOpenAIUntrustedToolRequests(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, args              string
		invalidName, renderable bool
	}{
		{"local", "", false, false},
		{"local", `{"x":`, false, false}, {"local", `{"x":1,"x":2}`, false, false}, {"local", `[]`, false, false},
		{"undeclared", `{}`, false, true}, {strings.Repeat("a", 64), `{}`, false, true},
		{strings.Repeat("a", 65), `{}`, true, false}, {"dot.name", `{}`, true, false}, {"space name", `{}`, true, false},
	} {
		t.Run(fmt.Sprintf("%s_%s", row.name, row.args), func(t *testing.T) {
			t.Parallel()
			p := openAIProfile()
			args, _ := json.Marshal(row.args)
			output := `[{"type":"function_call","call_id":"call-a","name":"` + row.name + `","arguments":` + string(args) + `}]`
			r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), output)), p, openAICompiled(t, p, 16))
			if err != nil || !r.Priceable {
				t.Fatal("lost terminal usage")
			}
			if row.invalidName {
				if !r.ContentError || r.Blocks != nil {
					t.Fatal("invalid name accepted")
				}
				return
			}
			if r.ContentError || len(r.Blocks) != 1 || r.Blocks[0].Type != "local_tool_request" || r.Blocks[0].ArgumentsText == nil || *r.Blocks[0].ArgumentsText != row.args || (r.Replay != nil) != row.renderable {
				t.Fatal("argument failure erased evidence or became executable")
			}
			encoded, _ := compact(r.Blocks[0])
			var candidate map[string]json.RawMessage
			if json.Unmarshal(encoded, &candidate) != nil || candidate["arguments_text"] == nil {
				t.Fatal("candidate omitted empty arguments")
			}
		})
	}
}

func TestAnthropicObserverAndReplay(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	c := fixtureCompiled(t, p)
	var text strings.Builder
	stream := streamStart(`{"input_tokens":2,"output_tokens":1}`) + textEvents() + eventJSON(`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":"","extension":{"value":"opaque"}}}`) + eventJSON(`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"a"}}`) + eventJSON(`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"b"}}`) + eventJSON(`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"one"}}`) + eventJSON(`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"two"}}`) + eventJSON(`{"type":"content_block_stop","index":1}`) + streamDelta(`{"output_tokens":7}`) + streamStop()
	r, err := readAnthropicStream(strings.NewReader(stream), p, c, func(s string) error { text.WriteString(s); return nil })
	if err != nil || text.String() != "initial snow 雪" || !bytes.Contains(r.Replay, []byte(`"signature":"onetwo"`)) || !bytes.Contains(r.Replay, []byte(`"thinking":"ab"`)) || !bytes.Contains(r.Replay, []byte(`"extension":{"value":"opaque"}`)) {
		t.Fatal("presentation or replay lost source content")
	}
	calls := 0
	r, err = readAnthropicStream(strings.NewReader(stream), p, c, func(string) error { calls++; return ErrInvalid })
	if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil || r.Replay != nil || calls != 1 {
		t.Fatal("observer failure fabricated money or output")
	}
}

func TestOpenAIObserverAndReplay(t *testing.T) {
	t.Parallel()
	p := openAIProfile()
	c := openAICompiled(t, p, 16)
	stream := eventJSON(`{"type":"response.output_text.delta","delta":"visible"}`) + eventJSON(`{"type":"response.reasoning_text.delta","delta":"opaque"}`) + openAITerminal("completed", openAIUsage(20, 4, 8, 16), openAITextOutput)
	var text strings.Builder
	r, err := readOpenAIStream(strings.NewReader(stream), p, c, func(s string) error { text.WriteString(s); return nil })
	if err != nil || text.String() != "visible" || !bytes.Equal(r.Replay, []byte(openAITextOutput)) {
		t.Fatal("terminal output replaced by provisional deltas")
	}
	r, err = readOpenAIStream(strings.NewReader(stream), p, c, func(string) error { return ErrInvalid })
	if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil || r.Replay != nil {
		t.Fatal("observer failure fabricated money or output")
	}
}

func TestAnthropicObserverStopsAfterContentFailure(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	c := fixtureCompiled(t, p)
	calls := 0
	stream := streamStart(`{"input_tokens":2,"output_tokens":1}`) + eventJSON(`{"type":"future_event"}`) + textEvents() + streamDelta(`{"output_tokens":7}`) + streamStop()
	r, err := readAnthropicStream(strings.NewReader(stream), p, c, func(string) error { calls++; return nil })
	if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil || calls != 0 {
		t.Fatal("invalid content continued presentation or lost money")
	}
}

func TestOpenAIObserverStopsAfterContentFailure(t *testing.T) {
	t.Parallel()
	p := openAIProfile()
	c := openAICompiled(t, p, 16)
	calls := 0
	stream := eventJSON(`{"type":"response.output_text.annotation.added"}`) + eventJSON(`{"type":"response.output_text.delta","delta":"ineligible"}`) + openAITerminal("completed", openAIUsage(20, 4, 8, 16), openAITextOutput)
	r, err := readOpenAIStream(strings.NewReader(stream), p, c, func(string) error { calls++; return nil })
	if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil || calls != 0 {
		t.Fatal("invalid content continued presentation or lost money")
	}
}

func FuzzAnthropicRequest(f *testing.F) {
	replay, _ := compact(replayRequest("anthropic"))
	f.Add(replay)
	for _, seed := range [][]byte{[]byte(`{"max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"synthetic"}]}]}`), []byte(`{"max_tokens":16,"max_tokens":17}`), []byte(`{"messages":null}`), {255}, bytes.ReplaceAll(replay, []byte("call-b"), []byte("call-a")), bytes.ReplaceAll(replay, []byte(`"input":{}`), []byte(`"input":{"n":1,"n":2}`))} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		request, err := ParseRequest(raw)
		if err != nil {
			return
		}
		for _, model := range []string{Haiku, Sonnet, Opus} {
			p := fixtureProfile()
			p.ModelID = model
			compiled, err := CompileAnthropic(p, request)
			if err != nil {
				continue
			}
			if len(compiled.Body()) > RequestLimit || checkJSON(compiled.Body(), RequestLimit, false, false) != nil {
				t.Fatal("invalid compiled body")
			}
			again, err := CompileAnthropic(p, request)
			if err != nil || !bytes.Equal(compiled.Body(), again.Body()) {
				t.Fatal("nondeterministic replay compilation")
			}
			if _, _, err := Bound(p, compiled.OutputMaximum(), compiled.InputClasses()); err != nil {
				t.Fatal("compiled request has no bound")
			}
		}
	})
}

func TestAnthropicUntrustedEmptyArguments(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	c := fixtureCompiled(t, p)
	stream := streamStart(`{"input_tokens":2,"output_tokens":1}`) + eventJSON(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-a","name":"local","input":{}}}`) + eventJSON(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`) + eventJSON(`{"type":"content_block_stop","index":0}`) + streamDelta(`{"output_tokens":7}`) + streamStop()
	r, err := ReadAnthropicStream(strings.NewReader(stream), p, c)
	if err != nil || r.ContentError || !r.Priceable || r.Replay != nil || len(r.Blocks) != 1 {
		t.Fatal("empty arguments lost the correctable request")
	}
	raw, _ := compact(r.Blocks[0])
	if !bytes.Contains(raw, []byte(`"arguments_text":""`)) {
		t.Fatal("empty arguments were omitted or reinterpreted")
	}
}

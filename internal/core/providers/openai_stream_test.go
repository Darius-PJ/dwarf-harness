package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

const openAITextOutput = `[{"id":"item-text","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}]}]`
const openAIReasoningOutput = `[{"id":"item-reasoning","type":"reasoning","summary":[{"type":"summary_text","text":"summary"}],"encrypted_content":"opaque synthetic content","extension":{"future":null}}]`

// Sanitized terminal shape: only response/item IDs and output text are synthetic.
const openAILiveUsage = `{"input_tokens":9,"input_tokens_details":{"cache_write_tokens":0,"cache_write_tokens_details":{"ttl_12h_tokens":0,"ttl_30m_tokens":0},"cached_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":14}`
const openAILiveToolUsage = `{"image_gen":{"input_tokens":0,"input_tokens_details":{"image_tokens":0,"text_tokens":0},"output_tokens":0,"output_tokens_details":{"image_tokens":0,"text_tokens":0},"total_tokens":0},"web_search":{"num_requests":0}}`

func openAILiveTerminal() string {
	return eventJSON(`{"type":"response.completed","response":{"id":"response-live-shape","model":"gpt-6-luna","status":"completed","service_tier":"default","incomplete_details":null,"output":` + openAITextOutput + `,"usage":` + openAILiveUsage + `,"tool_usage":` + openAILiveToolUsage + `}}`)
}

func TestOpenAILiveUsageSchema(t *testing.T) {
	t.Parallel()
	// The same provider bytes settle only under the new interpretation; v1
	// keeps the hold and withholds output, rather than changing shipped policy.
	for _, schema := range []string{openAIUsageSchema, openAIUsageSchemaV2} {
		t.Run(schema, func(t *testing.T) {
			t.Parallel()
			p := openAIProfile()
			p.UsageSchema = schema
			c := openAICompiled(t, p, 16)
			var raw Usage
			if err := json.Unmarshal([]byte(openAILiveUsage), &raw); err != nil {
				t.Fatal(err)
			}
			units, violation, normalizeErr := NormalizeUsage(p, c, raw)
			r, err := ReadOpenAIStream(strings.NewReader(openAILiveTerminal()), p, c)
			if !r.Complete || string(r.ToolUsage) != openAILiveToolUsage {
				t.Fatal("live-shaped terminal lost finality or tool evidence")
			}
			if schema == openAIUsageSchema {
				if !errors.Is(err, ErrUsage) || r.Priceable || !r.ProfileViolation || r.Blocks != nil || !errors.Is(normalizeErr, ErrUsage) || !violation {
					t.Fatal("v1 reinterpreted the split instead of holding it")
				}
				return
			}
			want := []Units{{"input.cached.long", 0}, {"input.cached.short", 0}, {"input.uncached.long", 0}, {"input.uncached.short", 9}, {"input.write.long", 0}, {"input.write.short", 0}, {"output.generated.long", 0}, {"output.generated.short", 5}}
			if err != nil || normalizeErr != nil || !r.Priceable || r.ProfileViolation || violation || !reflect.DeepEqual(r.Units, want) || !reflect.DeepEqual(units, want) {
				t.Fatalf("v2 live usage: stream=%v normalize=%v units=%v", err, normalizeErr, r.Units)
			}
			if len(r.Blocks) != 1 || r.Blocks[0].Type != "text" || r.Blocks[0].Text != "answer" {
				t.Fatal("v2 dropped terminal output")
			}
			if cost, err := PriceUsage(p, r.Units); err != nil || cost != 4 {
				t.Fatalf("live-shaped settlement=%d: %v", cost, err)
			}
		})
	}
}

func TestOpenAIUsageSplitScalarZeroIntentionalVersionDifference(t *testing.T) {
	t.Parallel()
	// Intentional exception to monotonic tolerance: v1 sees an unknown scalar
	// zero; v2 recognizes the member and requires null or an object.
	stream := openAITerminal("completed", strings.Replace(openAILiveUsage, `{"ttl_12h_tokens":0,"ttl_30m_tokens":0}`, "0", 1), openAITextOutput)
	for _, p := range []Profile{openAIProfile(), openAIProfileV2()} {
		r, err := ReadOpenAIStream(strings.NewReader(stream), p, openAICompiled(t, p, 16))
		if p.UsageSchema == openAIUsageSchema {
			if err != nil || !r.Priceable || r.ProfileViolation {
				t.Fatal("v1 scalar-zero tolerance changed")
			}
		} else if !errors.Is(err, ErrUsage) || r.Priceable || !r.ProfileViolation || r.Blocks != nil {
			t.Fatal("v2 accepted a scalar split")
		}
	}
}

func openAITerminal(status, usage, output string) string {
	details := "null"
	if status == "incomplete" {
		details = `{"reason":"max_output_tokens"}`
	}
	return eventJSON(`{"type":"response.` + status + `","response":{"id":"response-fixture","model":"gpt-6-luna","status":"` + status + `","service_tier":"default","incomplete_details":` + details + `,"output":` + output + `,"usage":` + usage + `}}`)
}

func TestOpenAIResponsesEvidence(t *testing.T) {
	t.Parallel()
	// A cumulative diagnostic snapshot must neither overcharge nor fill a null
	// terminal counter. Incomplete reasoning is billed without a successful answer.
	p := openAIProfile()
	c := openAICompiled(t, p, 16)
	usage := openAIUsage(20, 4, 8, 16)
	usage = usage[:len(usage)-1] + `,"total_tokens":36,"output_tokens_details":{"reasoning_tokens":12}}`
	start := eventJSON(`{"type":"response.created","sequence_number":5,"response":{"id":"response-fixture","model":"gpt-6-luna","usage":` + openAIUsage(10, 2, 3, 4) + `,"output":[]}}`)
	for _, status := range []string{"completed", "incomplete", "failed"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			output := openAITextOutput
			if status != "completed" {
				output = openAIReasoningOutput
			}
			r, err := ReadOpenAIStream(&fragmentReader{[]byte(start + openAITerminal(status, usage, output)), 1}, p, c)
			if err != nil || !r.Complete || !r.Priceable || !r.OutputStarted || r.StopReason != status || r.MessageID != "response-fixture" || string(r.ServedTier) != `"default"` {
				t.Fatalf("terminal state: %v", err)
			}
			cost, err := PriceUsage(p, r.Units)
			if err != nil || cost != 10 {
				t.Fatalf("settlement %d: %v", cost, err)
			}
			if string(r.RawUsageStart) != openAIUsage(10, 2, 3, 4) || string(r.RawUsageFinal) != usage {
				t.Fatal("snapshot provenance changed")
			}
			if len(r.Blocks) != 1 {
				t.Fatal("terminal output not selected")
			}
			if status == "completed" && r.Blocks[0].Text != "answer" {
				t.Fatal("terminal text changed")
			}
			if status != "completed" && (r.Blocks[0].Type != "responses_reasoning" || string(r.Blocks[0].OpaquePayload) != openAIReasoningOutput[1:len(openAIReasoningOutput)-1]) {
				t.Fatal("opaque reasoning changed")
			}
		})
	}
	for _, counter := range []string{"cached_tokens", "cache_write_tokens"} {
		for _, replacement := range []string{"null", "omit"} {
			t.Run(counter+"_"+replacement, func(t *testing.T) {
				t.Parallel()
				var final Usage
				_ = json.Unmarshal([]byte(usage), &final)
				details, _ := rawObject(final["input_tokens_details"])
				if replacement == "omit" {
					delete(details, counter)
				} else {
					details[counter] = json.RawMessage("null")
				}
				final["input_tokens_details"], _ = json.Marshal(details)
				raw, _ := json.Marshal(final)
				r, err := ReadOpenAIStream(strings.NewReader(start+openAITerminal("completed", string(raw), openAITextOutput)), p, c)
				if !errors.Is(err, ErrUsage) || !r.Complete || r.Priceable || r.ProfileViolation || r.Blocks != nil {
					t.Fatal("partial terminal borrowed stale usage")
				}
			})
		}
	}
	for _, input := range []int64{272000, 272001, 278528, 278529} {
		r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(input, 0, 0, 16), openAITextOutput)), p, c)
		ambiguous := input == 272001 || input == 278528
		if errors.Is(err, ErrTariffAmbiguous) != ambiguous || r.Priceable == ambiguous || !r.Complete || r.ProfileViolation {
			t.Fatal("band edge changed terminal state")
		}
	}
}

func TestOpenAIStreamFramingIdentityAndFinality(t *testing.T) {
	t.Parallel()
	// EOF, duplicate keys, sequence rewind and retargeting cannot become final
	// evidence; a valid terminal does not wait for or consume later stream data.
	p := openAIProfile()
	c := openAICompiled(t, p, 16)
	valid := openAITerminal("completed", openAIUsage(20, 4, 8, 16), openAITextOutput)
	for _, sep := range []string{"\n", "\r", "\r\n"} {
		stream := "\ufeff: comment\nretry: 0\nid: ignored\n" + valid
		stream = strings.ReplaceAll(stream, "\n", sep)
		r, err := ReadOpenAIStream(&fragmentReader{[]byte(stream), 1}, p, c)
		if err != nil || !r.Priceable {
			t.Fatal("valid fragmented SSE refused")
		}
	}
	for name, stream := range map[string]string{
		"empty": "", "truncated": strings.TrimSuffix(valid, "\n\n"),
		"snapshot_only":     eventJSON(`{"type":"response.created","response":{"id":"response-fixture","model":"gpt-6-luna","usage":` + openAIUsage(20, 4, 8, 16) + `}}`),
		"duplicate":         strings.Replace(valid, `"input_tokens":20`, `"input_tokens":20,"input_tokens":20`, 1),
		"nested_duplicate":  strings.Replace(valid, `"cached_tokens":4`, `"cached_tokens":4,"cached_tokens":4`, 1),
		"invalid_utf8":      strings.Replace(valid, "answer", string([]byte{255}), 1),
		"event_name":        "event: response.failed\n" + valid,
		"wrong_status":      strings.Replace(valid, `"status":"completed"`, `"status":"failed"`, 1),
		"negative_sequence": strings.Replace(valid, `"type":"response.completed"`, `"type":"response.completed","sequence_number":-1`, 1),
		"sequence_overflow": strings.Replace(valid, `"type":"response.completed"`, `"type":"response.completed","sequence_number":9223372036854775808`, 1),
		"sequence_rewind":   eventJSON(`{"type":"response.output_text.delta","delta":"x","sequence_number":3}`) + strings.Replace(valid, `"type":"response.completed"`, `"type":"response.completed","sequence_number":3`, 1),
		"response_identity": eventJSON(`{"type":"response.output_text.delta","delta":"x","response_id":"another"}`) + valid,
		"item_identity":     eventJSON(`{"type":"response.output_text.delta","delta":"x","output_index":0,"item_id":"other-item"}`) + valid,
		"index_identity":    eventJSON(`{"type":"response.output_text.delta","delta":"x","output_index":1,"item_id":"item-text"}`) + valid,
		"snapshot_identity": eventJSON(`{"type":"response.in_progress","response":{"id":"resp-fixture","output":[{"type":"message","id":"other-item"}]}}`) + valid,
		"snapshot_hosted":   eventJSON(`{"type":"response.in_progress","response":{"id":"resp-fixture","output":[{"type":"web_search_call","id":"hosted"}]}}`) + valid,
		"missing_model":     strings.Replace(valid, `"model":"gpt-6-luna",`, "", 1),
		"unknown_event":     eventJSON(`{"type":"future_billable","units":1}`) + valid,
		"hosted_event":      eventJSON(`{"type":"response.web_search_call.completed"}`) + valid,
		"error":             eventJSON(`{"type":"error","code":"slow_down","param":null,"message":"synthetic"}`) + valid,
		"event_limit":       "data: " + strings.Repeat(" ", streamLineLimit) + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, err := ReadOpenAIStream(strings.NewReader(stream), p, c)
			if err == nil || r.Priceable || r.Blocks != nil {
				t.Fatal("invalid stream yielded usable evidence")
			}
		})
	}
	r, err := ReadOpenAIStream(io.MultiReader(strings.NewReader(valid), errorReader{}), p, c)
	if err != nil || !r.Priceable {
		t.Fatal("terminal waited for EOF")
	}
	r, err = ReadOpenAIStream(strings.NewReader(valid+eventJSON(`{"type":"error"}`)), p, c)
	if err != nil || !r.Priceable {
		t.Fatal("later event replaced first terminal")
	}
	r, err = ReadOpenAIStream(strings.NewReader(strings.Replace(valid, "gpt-6-luna", "gpt-6-luna-snapshot", 1)), p, c)
	if err == nil || r.Priceable || r.Model != "gpt-6-luna-snapshot" {
		t.Fatal("served model mismatch was not retained and held")
	}
	witness := eventJSON(`{"type":"response.reasoning_text.delta","delta":"started","sequence_number":9}`)
	r, err = ReadOpenAIStream(io.MultiReader(strings.NewReader(witness), errorReader{}), p, c)
	if err == nil || !r.OutputStarted || r.Priceable {
		t.Fatal("disconnect erased begun output")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestOpenAIStreamTierToolAndContentPolicies(t *testing.T) {
	t.Parallel()
	// Missing tiers and unpriced tools retain the hold. Invalid content cannot
	// erase independent complete usage or become a completed tool command.
	p := openAIProfile()
	c := openAICompiled(t, p, 16)
	valid := openAITerminal("completed", openAIUsage(20, 4, 8, 16), openAITextOutput)
	for _, tier := range []string{"omit", "null", `"priority"`, `"auto"`} {
		t.Run("tier_"+tier, func(t *testing.T) {
			t.Parallel()
			stream := strings.Replace(valid, `"service_tier":"default",`, "", 1)
			if tier != "omit" {
				stream = strings.Replace(valid, `"service_tier":"default"`, `"service_tier":`+tier, 1)
			}
			r, err := ReadOpenAIStream(strings.NewReader(stream), p, c)
			if err == nil || r.Priceable || r.Blocks != nil {
				t.Fatal("missing/unsupported tier priced")
			}
		})
	}
	for _, location := range []string{"response", "usage"} {
		for _, tool := range []string{"null", `{}`, `{"web_search":{"num_requests":0}}`, `{"x":{}}`, `0`, `[]`, `{"x":null}`, `{"x":0.0}`, `{"x":1}`} {
			t.Run(location+"_tool_"+tool, func(t *testing.T) {
				t.Parallel()
				stream := strings.Replace(valid, `"service_tier":"default"`, `"service_tier":"default","tool_usage":`+tool, 1)
				want := tool == "null" || tool == `{}` || tool == `{"web_search":{"num_requests":0}}` || tool == `{"x":{}}`
				if location == "usage" {
					stream = strings.Replace(valid, `"usage":{`, `"usage":{"tool_usage":`+tool+`,`, 1)
					usage, _ := rawObject([]byte(openAIUsage(20, 4, 8, 16)))
					usage["tool_usage"] = json.RawMessage(tool)
					_, violation, err := NormalizeUsage(p, c, usage)
					if (err == nil) != want || violation == want {
						t.Fatal("nested tool usage changed settlement availability")
					}
				}
				r, err := ReadOpenAIStream(strings.NewReader(stream), p, c)
				if (err == nil) != want || r.Priceable != want || string(r.ToolUsage) != tool {
					t.Fatal("tool usage tolerance or retained evidence changed")
				}
				if !want && !r.ProfileViolation {
					t.Fatal("unbounded tool evidence not fenced")
				}
			})
		}
	}
	for name, output := range map[string]string{
		"annotations":     strings.Replace(openAITextOutput, `"annotations":[]`, `"annotations":[{"type":"citation"}]`, 1),
		"wrong_role":      strings.Replace(openAITextOutput, `"role":"assistant"`, `"role":"user"`, 1),
		"unknown_content": strings.Replace(openAITextOutput, `"type":"output_text"`, `"type":"image"`, 1),
		"partial_message": strings.Replace(openAITextOutput, `"status":"completed"`, `"status":"in_progress"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), output)), p, c)
			if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil {
				t.Fatal("content error changed financial evidence")
			}
		})
	}
	refusal := strings.Replace(openAITextOutput, `{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}`, `{"type":"refusal","refusal":"declined"}`, 1)
	r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), refusal)), p, c)
	if err != nil || !r.Priceable || len(r.Blocks) != 0 || !r.OutputStarted {
		t.Fatal("refusal became successful content or free usage")
	}
	rq := fixtureRequest()
	rq.Tools = []Tool{{Name: "local", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	toolCompiled, err := CompileOpenAI(p, rq)
	if err != nil {
		t.Fatal(err)
	}
	tool := `[{"type":"function_call","id":"item-call","call_id":"call-fixture","name":"local","arguments":"{\"x\":1}","status":"completed"}]`
	r, err = ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), tool)), p, toolCompiled)
	if err != nil || !r.Priceable || len(r.Blocks) != 1 || r.Blocks[0].ID != "call-fixture" || string(r.Blocks[0].Input) != `{"x":1}` {
		t.Fatal("declared function call not canonical")
	}
	for _, row := range []struct {
		name, field string
		local       bool
	}{
		{"async_false", `"async":false`, true},
		{"async_true", `"async":true`, false},
		{"async_null", `"async":null`, false},
		{"caller_null", `"caller":null`, true},
		{"caller_nonnull", `"caller":{"type":"hosted"}`, false},
		{"namespace_null", `"namespace":null`, true},
		{"namespace_nonnull", `"namespace":"hosted"`, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			output := strings.Replace(tool, `"type":"function_call"`, `"type":"function_call",`+row.field, 1)
			r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), output)), p, toolCompiled)
			if err != nil || !r.Priceable || r.ContentError == row.local {
				t.Fatal("function routing fields changed independent usage")
			}
			if row.local {
				if len(r.Blocks) != 1 || r.Blocks[0].Type != "tool_use" {
					t.Fatal("local function call lost")
				}
			} else if len(r.Blocks) != 0 {
				t.Fatal("nonlocal function became an executable command")
			}
		})
	}
	for _, bad := range []string{strings.Replace(tool, `"status":"completed"`, `"status":"incomplete"`, 1), strings.Replace(tool, `"status":"completed"`, `"status":null`, 1), strings.Replace(tool, `"call_id":"call-fixture"`, `"call_id":""`, 1)} {
		r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), bad)), p, toolCompiled)
		if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil {
			t.Fatal("invalid local command escaped")
		}
	}
	for _, bad := range []string{strings.Replace(tool, `"name":"local"`, `"name":"undeclared"`, 1), strings.Replace(tool, `{\"x\":1}`, `{\"x\":1,\"x\":2}`, 1)} {
		r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), bad)), p, toolCompiled)
		if err != nil || !r.Priceable || r.ContentError || len(r.Blocks) != 1 || r.Blocks[0].Type != "local_tool_request" {
			t.Fatal("argument correction lost independently complete usage")
		}
	}
	// Ordinary incomplete assistant/tool output must not erase the separately
	// complete encrypted reasoning item, nor become a successful command.
	for _, partial := range []string{openAITextOutput, tool} {
		partial = strings.Replace(partial, `"status":"completed"`, `"status":"incomplete"`, 1)
		output := openAIReasoningOutput[:len(openAIReasoningOutput)-1] + "," + partial[1:]
		r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("incomplete", openAIUsage(20, 4, 8, 16), output)), p, toolCompiled)
		if err != nil || !r.Priceable || len(r.Blocks) != 1 || r.Blocks[0].Type != "responses_reasoning" {
			t.Fatal("partial output erased complete reasoning")
		}
	}
}

func FuzzOpenAIResponsesSSE(f *testing.F) {
	valid := openAITerminal("completed", openAIUsage(20, 4, 8, 16), openAITextOutput)
	for _, seed := range []string{valid, "", strings.TrimSuffix(valid, "\n\n"), strings.Replace(valid, `"cached_tokens":4`, `"cached_tokens":null`, 1), strings.Replace(valid, `"input_tokens":20`, `"input_tokens":20,"input_tokens":20`, 1), strings.Replace(valid, "answer", string([]byte{255}), 1), eventJSON(`{"type":"response.output_text.delta","sequence_number":2,"delta":"x"}`) + eventJSON(`{"type":"response.output_text.delta","sequence_number":1,"delta":"x"}`) + valid, eventJSON(`{"type":"response.output_text.delta","response_id":"other","delta":"x"}`) + valid, strings.Replace(valid, `"output_tokens":16`, `"output_tokens":9223372036854775808`, 1), eventJSON(`{"type":"future"}`) + valid} {
		f.Add([]byte(seed), uint8(1))
	}
	for _, tool := range []string{`{"web_search":{"num_requests":0}}`, `{"web_search":{"num_requests":1}}`} {
		f.Add([]byte(strings.Replace(valid, `"usage":{`, `"usage":{"tool_usage":`+tool+`,`, 1)), uint8(1))
	}
	f.Add([]byte(openAILiveTerminal()), uint8(1))
	for _, split := range []string{`{"ttl_30m_tokens":8,"ttl_12h_tokens":0}`, `{"ttl_30m_tokens":7,"ttl_12h_tokens":1}`, `{"ttl_30m_tokens":7,"ttl_12h_tokens":0}`, `0`} {
		usage := strings.Replace(openAIUsage(20, 4, 8, 16), `"cache_write_tokens":8`, `"cache_write_tokens":8,"cache_write_tokens_details":`+split, 1)
		f.Add([]byte(openAITerminal("completed", usage, openAITextOutput)), uint8(1))
	}
	for _, args := range []string{`{"n":1}`, `{"n":1,"n":2}`, `[]`, `{`} {
		encoded, _ := json.Marshal(args)
		output := `[{"type":"function_call","id":"synthetic-item","call_id":"synthetic-call","name":"undeclared","arguments":` + string(encoded) + `}]`
		f.Add([]byte(openAITerminal("completed", openAIUsage(20, 4, 8, 16), output)), uint8(1))
	}
	f.Fuzz(func(t *testing.T, b []byte, step uint8) {
		if len(b) > streamBodyLimit+1 {
			return
		}
		p := openAIProfile()
		c := openAICompiled(t, p, 16)
		a, e := ReadOpenAIStream(&fragmentReader{bytes.Clone(b), int(step) + 1}, p, c)
		z, other := ReadOpenAIStream(bytes.NewReader(b), p, c)
		if (e == nil) != (other == nil) || !reflect.DeepEqual(a, z) {
			t.Fatal("framing fragmentation changed evidence")
		}
		if a.Priceable {
			if !a.Complete {
				t.Fatal("unterminated usage priced")
			}
			if _, err := PriceUsage(p, a.Units); err != nil && !errors.Is(err, ErrOverflow) {
				t.Fatal("priceable units are invalid")
			}
		}
		p2 := openAIProfileV2()
		c2 := openAICompiled(t, p2, 16)
		v2, e2 := ReadOpenAIStream(&fragmentReader{bytes.Clone(b), int(step) + 1}, p2, c2)
		z2, other2 := ReadOpenAIStream(bytes.NewReader(b), p2, c2)
		if (e2 == nil) != (other2 == nil) || !reflect.DeepEqual(v2, z2) {
			t.Fatal("v2 framing fragmentation changed evidence")
		}
		if v2.Priceable {
			if !v2.Complete {
				t.Fatal("v2 unterminated usage priced")
			}
			if _, err := PriceUsage(p2, v2.Units); err != nil && !errors.Is(err, ErrOverflow) {
				t.Fatal("v2 priceable units are invalid")
			}
		}
		details, _ := rawObject(a.RawUsageMerged["input_tokens_details"])
		split := bytes.TrimSpace(details["cache_write_tokens_details"])
		_, splitErr := rawObject(split)
		if len(split) == 0 || bytes.Equal(split, []byte("null")) || splitErr == nil {
			// Equivalent to v2 held => v1 held; scalar 0 intentionally differs.
			if a.Priceable && (!v2.Priceable || !reflect.DeepEqual(a.Units, v2.Units) || a.ProfileViolation != v2.ProfileViolation) {
				t.Fatal("v2 changed priceable v1 evidence")
			}
		}
	})
}

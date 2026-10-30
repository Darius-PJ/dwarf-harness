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
	for _, tool := range []string{"null", `{}`, `{"web_search":{"num_requests":0}}`, `{"x":{}}`, `0`, `[]`, `{"x":null}`, `{"x":0.0}`, `{"x":1}`} {
		t.Run("tool_"+tool, func(t *testing.T) {
			t.Parallel()
			stream := strings.Replace(valid, `"service_tier":"default"`, `"service_tier":"default","tool_usage":`+tool, 1)
			r, err := ReadOpenAIStream(strings.NewReader(stream), p, c)
			want := tool == "null" || tool == `{}` || tool == `{"web_search":{"num_requests":0}}` || tool == `{"x":{}}`
			if (err == nil) != want || r.Priceable != want || string(r.ToolUsage) != tool {
				t.Fatal("tool usage tolerance changed")
			}
			if !want && !r.ProfileViolation {
				t.Fatal("unbounded tool evidence not fenced")
			}
		})
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
	for _, bad := range []string{strings.Replace(tool, `"name":"local"`, `"name":"undeclared"`, 1), strings.Replace(tool, `{\"x\":1}`, `{\"x\":1,\"x\":2}`, 1), strings.Replace(tool, `"status":"completed"`, `"status":"incomplete"`, 1), strings.Replace(tool, `"status":"completed"`, `"status":null`, 1), strings.Replace(tool, `"call_id":"call-fixture"`, `"call_id":""`, 1)} {
		r, err := ReadOpenAIStream(strings.NewReader(openAITerminal("completed", openAIUsage(20, 4, 8, 16), bad)), p, toolCompiled)
		if err != nil || !r.Priceable || !r.ContentError || r.Blocks != nil {
			t.Fatal("invalid local command escaped")
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
	})
}

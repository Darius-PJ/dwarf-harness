package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func paidStreamFeatureRows(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"mixed-cache-exclusive", "text-chunk-boundaries", "opaque-blocks", "malformed-tool-still-billed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			text := "answer"
			if mode == "text-chunk-boundaries" {
				text = strings.Repeat("雪\"", 100000)
			}
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				start := `{"input_tokens":3,"output_tokens":1}`
				final := `{"input_tokens":7,"output_tokens":4}`
				if mode == "mixed-cache-exclusive" {
					start = `{"input_tokens":3,"output_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":4,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":3}}`
					final = `{"input_tokens":7,"output_tokens":4,"cache_creation_input_tokens":8,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":5},"output_tokens_details":{"thinking_tokens":2}}`
				}
				body := paidSSE(model, start, final, text, "end_turn", "", true)
				if mode == "opaque-blocks" {
					blocks := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"thought\",\"signature\":\"sig\"}}\n\n" +
						"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\" 雪\"}}\n\n" +
						"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"nature\"}}\n\n" +
						"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
						"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"opaque\",\"extra\":{\"x\":1}}}\n\n" +
						"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n"
					body = paidSSE(model, start, final, "", "end_turn", "", true)
					body = strings.Replace(body, "data: {\"type\":\"message_delta\"", blocks+"data: {\"type\":\"message_delta\"", 1)
				}
				if mode == "malformed-tool-still-billed" {
					blocks := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"local\",\"name\":\"local\",\"input\":{}}}\n\n" +
						"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{broken\"}}\n\n" +
						"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
					body = paidSSE(model, start, final, "", "tool_use", "", true)
					body = strings.Replace(body, "data: {\"type\":\"message_delta\"", blocks+"data: {\"type\":\"message_delta\"", 1)
				}
				_, _ = io.WriteString(w, body)
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			input := f.input()
			if mode == "mixed-cache-exclusive" {
				input.Request.System = []providers.Text{{Type: "text", Text: "hour", CacheControl: &providers.CacheControl{Type: "ephemeral", TTL: "1h"}}}
				input.Request.Messages[0].Content[0].CacheControl = &providers.CacheControl{Type: "ephemeral", TTL: "5m"}
			}
			prepared, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
			if err != nil {
				t.Fatal(err)
			}
			r := PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: prepared}
			f.admit(t, r)
			d := PaidDispatch{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
			out, err := f.service.DispatchPaid(context.Background(), d)
			if err != nil {
				t.Fatal(err)
			}
			paidRecord(t, f, d, out)
			v := f.view(t, d.AttemptID)
			want := "11"
			if mode == "mixed-cache-exclusive" {
				want = "21"
			}
			if v.State != "settled" || v.SettledMicros != want || v.HeldMicros != "0" {
				t.Fatalf("exclusive block billing: %s/%s", v.State, v.SettledMicros)
			}
			if mode == "malformed-tool-still-billed" {
				if len(v.Result.MessageObjects) != 0 || v.Result.ErrorClass != "protocol_error" {
					t.Fatal("malformed tool output was usable or refunded")
				}
				return
			}
			var rebuilt strings.Builder
			for i, ref := range v.Result.MessageObjects {
				raw, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: d.AttemptID, Object: ref, MaxBytes: paidChunkLimit})
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) > paidChunkLimit || !utf8.Valid(raw) {
					t.Fatal("output chunk exceeded bound or split UTF-8")
				}
				var output paidOutputObject
				if json.Unmarshal(raw, &output) != nil {
					t.Fatal("invalid canonical output")
				}
				block := output.Block.(map[string]any)
				if mode == "opaque-blocks" {
					payload := `{"type":"thinking","thinking":"thought 雪","signature":"signature"}`
					kind := "thinking"
					if i == 1 {
						payload = `{"type":"redacted_thinking","data":"opaque","extra":{"x":1}}`
						kind = "redacted_thinking"
					}
					var rawBlock struct {
						Block json.RawMessage `json:"block"`
					}
					_ = json.Unmarshal(raw, &rawBlock)
					expected := fmt.Sprintf(`{"kind":"opaque_provider","provider":"anthropic","model":%q,"artifact_kind":%q,"payload":%s}`, v.Admission.Request.Model, kind, payload)
					if string(rawBlock.Block) != expected {
						t.Fatal("opaque bytes were interpreted, discarded or mutated")
					}
				} else {
					rebuilt.WriteString(block["text"].(string))
				}
			}
			if mode == "opaque-blocks" {
				if len(v.Result.MessageObjects) != 2 {
					t.Fatal("opaque block lost")
				}
			} else if rebuilt.String() != text {
				t.Fatal("chunked text changed consumer bytes")
			}
		})
	}
}

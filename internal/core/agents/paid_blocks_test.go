package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
				if len(v.Result.MessageObjects) != 1 || v.Result.ErrorClass != "" {
					t.Fatal("malformed tool request lost correction evidence")
				}
				raw, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: d.AttemptID, Object: v.Result.MessageObjects[0], MaxBytes: paidChunkLimit})
				if err != nil {
					t.Fatal(err)
				}
				var output struct {
					Block map[string]any `json:"block"`
				}
				if json.Unmarshal(raw, &output) != nil || len(output.Block) != 4 || output.Block["kind"] != "local_tool_request" || output.Block["id"] != "local" || output.Block["name"] != "local" || output.Block["arguments_text"] != "{broken" {
					t.Fatal("malformed arguments became executable or changed evidence")
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

func TestPaidCaptureUntrustedArguments(t *testing.T) {
	t.Parallel()
	pinned := []byte("syntheticValue")
	arguments := `{"text":"ordinary"}`
	block := providers.OutputBlock{Type: "local_tool_request", ID: "synthetic-call", Name: "local", ArgumentsText: &arguments}
	if !paidCaptureAllowed(paidObservationObject{}, []providers.OutputBlock{block}, pinned) {
		t.Fatal("ordinary untrusted arguments refused")
	}
	for _, field := range []string{"id", "name", "arguments_text"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			candidate := block
			switch field {
			case "id":
				candidate.ID = "prefix-" + string(pinned)
			case "name":
				candidate.Name = string(pinned) + "-suffix"
			case "arguments_text":
				text := `{"text":"` + string(pinned) + `"}`
				candidate.ArgumentsText = &text
			}
			if paidCaptureAllowed(paidObservationObject{}, []providers.OutputBlock{candidate}, pinned) {
				t.Fatal("untrusted request disclosed the pinned value")
			}
		})
	}
}

func TestPaidCaptureUntrustedShape(t *testing.T) {
	t.Parallel()
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { paidSuccess(t, w, r) })
	f := newLivePaidFixture(t, paidServerDeps(server, nil))
	_, dispatch, out := paidRun(t, f)
	scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(block map[string]any) PaidObservation {
		evidence := paidLateEvidence(t, f, out, func(o *paidObservationObject) {
			ref := o.MessageObjects[0]
			raw, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
			if err != nil {
				t.Fatal(err)
			}
			var object paidOutputObject
			if json.Unmarshal(raw, &object) != nil {
				t.Fatal("invalid captured output")
			}
			object.Block = block
			raw, err = json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			ref.SHA256, err = scope.publishObject(raw)
			if err != nil {
				t.Fatal(err)
			}
			ref.Bytes = int64(len(raw))
			o.MessageObjects[0] = ref
		})
		return PaidObservation{CommandID: f.id(), AttemptID: dispatch.AttemptID, ExpectedVersion: 2, TrustedEvidence: &evidence}
	}
	for _, row := range []struct {
		field string
		value any
	}{
		{"id", ""}, {"id", strings.Repeat("a", 257)}, {"id", "with space"}, {"id", 1},
		{"name", ""}, {"name", strings.Repeat("a", 65)}, {"name", "with.dot"}, {"name", "with space"},
		{"arguments_text", nil}, {"arguments_text", map[string]any{}}, {"input", map[string]any{}},
	} {
		block := map[string]any{"kind": "local_tool_request", "id": "synthetic-call", "name": "local", "arguments_text": "{broken"}
		block[row.field] = row.value
		before := f.store.Durable()
		if _, err := f.service.RecordPaidObservation(context.Background(), publish(block)); err != ErrIntegrity || f.store.Durable() != before || f.view(t, dispatch.AttemptID).State != "sent" {
			t.Fatal("malformed candidate changed paid state")
		}
	}
	block := map[string]any{"kind": "local_tool_request", "id": strings.Repeat("a", 256), "name": strings.Repeat("A", 64), "arguments_text": ""}
	if _, err := f.service.RecordPaidObservation(context.Background(), publish(block)); err != nil {
		t.Fatal(err)
	}
	if view := f.view(t, dispatch.AttemptID); view.State != "settled" || view.SettledMicros != "11" || view.HeldMicros != "0" {
		t.Fatal("valid candidate changed usage settlement")
	}
}

func TestPaidCaptureMalformedEscape(t *testing.T) {
	t.Parallel()
	for _, escaped := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain-malformed", true: "escaped-malformed"}[escaped], func(t *testing.T) {
			t.Parallel()
			server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
				model := paidRequestModel(t, r)
				arguments := `{"text":"ordinary"`
				if escaped {
					pinned := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					arguments = `{"text":"` + strings.ReplaceAll(pinned, "a", `\u0061`) + `"`
				}
				raw, err := json.Marshal(arguments)
				if err != nil {
					t.Error(err)
					return
				}
				blocks := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"local\",\"name\":\"local\",\"input\":{}}}\n\n" +
					"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":" + string(raw) + "}}\n\n" +
					"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
				body := paidSSE(model, `{"input_tokens":7,"output_tokens":1}`, `{"output_tokens":4}`, "", "tool_use", "", true)
				body = strings.Replace(body, "data: {\"type\":\"message_delta\"", blocks+"data: {\"type\":\"message_delta\"", 1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, body)
			})
			f := newLivePaidFixture(t, paidServerDeps(server, nil))
			_, dispatch, out := paidRun(t, f)
			paidRecord(t, f, dispatch, out)
			view := f.view(t, dispatch.AttemptID)
			if escaped {
				if view.State != "unknown" || view.SettledMicros != "0" || view.HeldMicros == "0" || len(view.Result.MessageObjects) != 0 {
					t.Fatal("ambiguous escaped arguments were published or settled")
				}
				paidAssertNoSecret(t, f.root, os.Getenv(paidTestEnvironment))
			} else {
				if view.State != "settled" || view.SettledMicros != "11" || view.HeldMicros != "0" || len(view.Result.MessageObjects) != 1 {
					t.Fatal("plain malformed candidate lost usage settlement")
				}
				raw, err := f.service.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: dispatch.AttemptID, Object: view.Result.MessageObjects[0], MaxBytes: paidChunkLimit})
				var object struct {
					Block map[string]any `json:"block"`
				}
				if err != nil || json.Unmarshal(raw, &object) != nil || object.Block["kind"] != "local_tool_request" || object.Block["arguments_text"] != `{"text":"ordinary"` {
					t.Fatal("plain malformed arguments changed evidence")
				}
			}
		})
	}
}

package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func loopArtifactSSE(model, provider, name, arguments string, round int) string {
	if provider == "openai" {
		output := fmt.Sprintf(`[{"type":"reasoning","id":"reasoning-%d","summary":[],"encrypted_content":"synthetic-encrypted-%d","extension":{"future":null}},{"type":"message","id":"commentary-%d","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"Visible preface.","annotations":[],"logprobs":[]}]},{"type":"function_call","id":"function-%d","call_id":"artifact-call-%d","name":%q,"arguments":%q,"status":"completed"}]`, round, round, round, round, round, name, arguments)
		return paidOpenAIEvent(model, "completed", paidOpenAIUsage(100, 0, 0, 20), output, "", 1)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"artifact-message-%d\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n", round, model)
	out.WriteString("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Visible preface.\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	out.WriteString("data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\",\"extension\":{\"retained\":true}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"opaque \"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"synthetic thought\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"synthetic-\"}}\n\n")
	fmt.Fprintf(&out, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signature-%d\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"synthetic-redaction-%d\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n", round, round)
	fmt.Fprintf(&out, "data: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"artifact-call-%d\",\"name\":%q,\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":3,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":3}\n\n", round, name, arguments)
	out.WriteString("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_details\":null},\"usage\":{\"input_tokens\":100,\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	return out.String()
}

func testLoopArtifactReplay(t *testing.T) {
	t.Parallel()
	for _, model := range []string{providers.Sonnet, providers.Opus, "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			provider := "anthropic"
			if model == "gpt-6-luna" {
				provider = "openai"
			}
			requests := make(chan []byte, 3)
			var posts atomic.Int32
			var arguments string
			f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				actual, raw := turnRequest(t, r)
				requests <- raw
				n := posts.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				switch n {
				case 1:
					_, _ = io.WriteString(w, loopArtifactSSE(actual, provider, "core_memory_v1", arguments, 1))
				case 2:
					_, _ = io.WriteString(w, loopArtifactSSE(actual, provider, "core_status_v1", `{"schema":1,"subject":"context"}`, 2))
				case 3:
					if provider == "anthropic" {
						_, _ = io.WriteString(w, turnAnthropic(actual, nil, "answer"))
					} else {
						_, _ = io.WriteString(w, paidOpenAIEvent(actual, "completed", paidOpenAIUsage(100, 0, 0, 20), paidOpenAIText, "", 1))
					}
				default:
					t.Error("artifact continuation repeated a model dispatch")
				}
			}, nil)
			if provider == "anthropic" {
				f.liveProfile(t, func(profile *providers.Profile) { profile.ModelID, profile.RequestedTier = model, "auto" })
				f.setting.ExpectedRouteVersion = f.route.Version
			}
			revision := f.seedMemory(t, "Frozen artifact-loop source prefix.")
			arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"Stored synthetic result 雪."}`, revision)
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || result.ModelCalls != 3 || result.ToolDispatches != 2 || posts.Load() != 3 {
				t.Fatalf("artifact loop stopped or dropped a matched call: %s/%s", result.Outcome, result.Reason)
			}
			raw := [][]byte{<-requests, <-requests, <-requests}
			var replays [][]byte
			agent := scopedBound(t, f.service, bindingOf(f.origin))
			for _, event := range events {
				paid, ok := event.Data.(ledger.RuntimePaidResult)
				if !ok {
					continue
				}
				data, err := agent.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: paid.AttemptID, GrantID: f.privateGrant, Object: *paid.EvidenceObject, MaxBytes: maxObjectBytes})
				if err != nil {
					t.Fatal(err)
				}
				var observation paidObservationObject
				if json.Unmarshal(data, &observation) != nil || observation.ReplayObject == nil {
					t.Fatal("no selected replay evidence")
				}
				replay, err := agent.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: paid.AttemptID, GrantID: f.privateGrant, Object: *observation.ReplayObject, MaxBytes: maxObjectBytes})
				if err != nil {
					t.Fatal(err)
				}
				replays = append(replays, replay)
			}
			if len(replays) != 3 {
				t.Fatal("artifact attempts lost their selected observation")
			}
			for n := range 2 {
				marker := fmt.Sprintf("synthetic-signature-%d", n+1)
				if provider == "openai" {
					marker = fmt.Sprintf("synthetic-encrypted-%d", n+1)
				}
				if !bytes.Contains(replays[n], []byte(marker)) || !bytes.Contains(replays[n], []byte(`"extension"`)) {
					t.Fatal("assembled replay omitted opaque content")
				}
			}
			if provider == "anthropic" {
				type wire struct {
					System, Tools json.RawMessage
					Messages      []struct {
						Role    string
						Content json.RawMessage
					}
				}
				var decoded [3]wire
				for n := range 3 {
					if json.Unmarshal(raw[n], &decoded[n]) != nil {
						t.Fatal("bad request")
					}
					if !bytes.Equal(decoded[0].System, decoded[n].System) || !bytes.Equal(decoded[0].Tools, decoded[n].Tools) || !bytes.Equal(decoded[0].Messages[0].Content, decoded[n].Messages[0].Content) {
						t.Fatal("artifact loop changed its frozen source/tool prefix")
					}
				}
				if len(decoded[1].Messages) != 3 || len(decoded[2].Messages) != 5 || !bytes.Equal(decoded[1].Messages[1].Content, replays[0]) || !bytes.Equal(decoded[2].Messages[1].Content, replays[0]) || !bytes.Equal(decoded[2].Messages[3].Content, replays[1]) || !bytes.Equal(decoded[1].Messages[2].Content, decoded[2].Messages[2].Content) {
					t.Fatal("signed/redacted replay or a completed call result was edited, dropped or reordered")
				}
			} else {
				type wire struct {
					Instructions, Tools, Include json.RawMessage
					Input                        []json.RawMessage
				}
				var decoded [3]wire
				for n := range 3 {
					if json.Unmarshal(raw[n], &decoded[n]) != nil {
						t.Fatal("bad request")
					}
					if !bytes.Equal(decoded[0].Instructions, decoded[n].Instructions) || !bytes.Equal(decoded[0].Tools, decoded[n].Tools) || !bytes.Equal(decoded[n].Include, []byte(`["reasoning.encrypted_content"]`)) || !bytes.Equal(decoded[0].Input[0], decoded[n].Input[0]) {
						t.Fatal("encrypted replay changed frozen inputs or omitted include")
					}
				}
				if len(decoded[1].Input) != 5 || len(decoded[2].Input) != 9 {
					t.Fatal("stateless continuation lost output items or a matched result")
				}
				for n := range 2 {
					var items []json.RawMessage
					if json.Unmarshal(replays[n], &items) != nil || len(items) != 3 {
						t.Fatal("bad recorded output items")
					}
					for i, item := range items {
						if !bytes.Equal(decoded[2].Input[1+4*n+i], item) || n == 0 && !bytes.Equal(decoded[1].Input[1+i], item) {
							t.Fatal("encrypted reasoning, phase or function item was reserialized or reordered")
						}
					}
				}
				if !bytes.Equal(decoded[1].Input[4], decoded[2].Input[4]) {
					t.Fatal("earlier result was edited during replay")
				}
			}
		})
	}
}

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestTurnLoopBothProviders(t *testing.T) {
	t.Parallel()
	t.Run("retained_artifacts", testLoopArtifactReplay)
	t.Run("source_bindings", testLoopSourceBindings)
	t.Run("one_shot_observation_unchanged", testLoopOneShotObservation)
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int64
			var arguments string
			f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				model, raw := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				switch posts.Add(1) {
				case 1:
					if provider == "anthropic" {
						_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
					} else {
						output := fmt.Sprintf(`[{"type":"function_call","id":"function-one","call_id":"call-one","name":"core_memory_v1","arguments":%q,"status":"completed"}]`, arguments)
						_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", paidOpenAIUsage(100, 0, 0, 20), output, "", 1))
					}
				case 2:
					if !bytes.Contains(raw, []byte("call-one")) || !bytes.Contains(raw, []byte("private.write")) {
						t.Error("continuation did not carry the original call and actual native receipt")
					}
					if provider == "anthropic" {
						_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
					} else {
						_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", paidOpenAIUsage(100, 0, 0, 20), paidOpenAIText, "", 1))
					}
				default:
					t.Error("unexpected replacement model call")
				}
			}, nil)
			revision := f.service.p.scopes[keyFor(submissionIDs{f.origin.AgentID, f.origin.SessionID, f.origin.ContextID}, "context")].revision
			arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"stored synthetic result"}`, revision)
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || result.Reason != "natural_stop" || result.ModelCalls != 2 || result.ToolDispatches != 1 || result.FinalMessageObject == nil || posts.Load() != 2 {
				t.Fatalf("incorrect turn outcome/counters: %s/%s model=%d tools=%d posts=%d", result.Outcome, result.Reason, result.ModelCalls, result.ToolDispatches, posts.Load())
			}
			var toolStarted *ledger.RuntimeToolStarted
			var toolResult *ledger.RuntimeToolResult
			var usage []ledger.RuntimePaidResult
			for _, event := range events {
				if event.AgentID != f.origin.AgentID || event.ExecutionSessionID != f.origin.SessionID || event.ContextID != f.origin.ContextID || event.TurnID != start.TurnID {
					t.Fatal("event attributed to another execution")
				}
				switch value := event.Data.(type) {
				case ledger.RuntimeToolStarted:
					toolStarted = &value
				case ledger.RuntimeToolResult:
					toolResult = &value
				}
				if value, ok := event.Data.(ledger.RuntimePaidResult); ok {
					usage = append(usage, value)
				}
			}
			if toolStarted == nil || toolResult == nil || toolResult.EffectReceiptSequence == 0 || toolStarted.ProviderCallID != "call-one" {
				t.Fatal("native tool effect lost its required start/result receipt linkage")
			}
			view, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
			if err != nil {
				t.Fatal(err)
			}
			page, err := f.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{Binding: bindingOf(f.origin), Scope: "context"}, Slot: "memory", Revision: view.Revision, MaxBytes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if string(page.Bytes) != "stored synthetic result" {
				t.Fatal("tool did not mutate private memory")
			}
			final, err := f.service.readLoopObject(context.Background(), f.origin, *result.FinalMessageObject)
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]json.RawMessage
			if json.Unmarshal(final, &manifest) != nil || string(manifest["kind"]) != `"final_message"` {
				t.Fatal("natural answer did not select a final-message manifest")
			}
			agent := scopedBound(t, f.service, bindingOf(f.origin))
			if len(usage) != 2 {
				t.Fatal("missing paid result observations")
			}
			var replayRefs []ledger.ObjectRef
			for _, paid := range usage {
				evidence, err := agent.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: paid.AttemptID, GrantID: f.privateGrant, Object: *paid.EvidenceObject, MaxBytes: maxObjectBytes})
				if err != nil {
					t.Fatal(err)
				}
				var observation paidObservationObject
				if json.Unmarshal(evidence, &observation) != nil || observation.ReplayObject == nil {
					t.Fatal("selected observation lost provider replay")
				}
				replayRefs = append(replayRefs, *observation.ReplayObject)
			}
			for _, unselected := range []ledger.ObjectRef{replayRefs[1], {SHA256: sha256.Sum256([]byte("unknown synthetic object")), Bytes: 17}} {
				_, err := agent.ReadPaidObject(context.Background(), PaidObjectLookup{AttemptID: usage[0].AttemptID, GrantID: f.privateGrant, Object: unselected, MaxBytes: maxObjectBytes})
				if !errors.Is(err, ErrDenied) {
					t.Fatal("unbound replay reference was not refused like an unknown object")
				}
			}
			foreign := scopedWorkContext(t, f.service, bindingOf(f.origin), 64000, 64001)
			foreignAgent := scopedBound(t, f.service, foreign)
			lookup := PaidObjectLookup{AttemptID: usage[0].AttemptID, GrantID: f.privateGrant, Object: replayRefs[0], MaxBytes: maxObjectBytes}
			_, foreignErr := foreignAgent.ReadPaidObject(context.Background(), lookup)
			lookup.AttemptID = privateID(64999)
			_, missingErr := foreignAgent.ReadPaidObject(context.Background(), lookup)
			if !errors.Is(foreignErr, ErrDenied) || foreignErr != missingErr {
				t.Fatal("a different context borrowed the source's grant identity to read provider replay")
			}
		})
	}
}

// testLoopOneShotObservation runs, in one fixture, a DispatchPaid attempt whose
// observation must keep main's exact bytes and a loop attempt whose observation
// must carry the provider's replay bytes into its continuation.
func testLoopOneShotObservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	requests := make(chan []byte, 3)
	var posts atomic.Int32
	var arguments string
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		requests <- raw
		w.Header().Set("Content-Type", "text/event-stream")
		switch posts.Add(1) {
		case 1:
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "One-shot answer."))
		case 2:
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
		case 3:
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Loop answer."))
		default:
			t.Error("unexpected provider exchange")
		}
	}, nil)
	f.turn = uuid.NewV7()
	agent := scopedBound(t, f.service, bindingOf(f.origin))
	if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: uuid.NewV7(), GrantID: f.privateGrant, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	admission, dispatch, out := paidRun(t, f.paidFixture)
	paidRecord(t, f.paidFixture, dispatch, out)
	if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: uuid.NewV7(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "released"}); err != nil {
		t.Fatal(err)
	}
	<-requests
	view := f.paidFixture.view(t, dispatch.AttemptID)
	if view.Result == nil || view.Result.EvidenceObject == nil || len(view.Result.MessageObjects) != 1 {
		t.Fatal("one-shot attempt lost its settled evidence")
	}
	raw, err := f.service.ReadPaidObject(ctx, PaidObjectLookup{AttemptID: dispatch.AttemptID, Object: *view.Result.EvidenceObject, MaxBytes: maxObjectBytes})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"schema":1,"kind":"paid_observation","attempt_id":%q,"origin":{"agent_id":%q,"session_id":%q,"context_id":%q},"parent_turn_id":%q,"request_sha256":%q,"provider_message_id":"message-one","model":%q,"http_status":200,"headers":[{"name":"content-type","values":["text/event-stream"]}],"stop_reason":"end_turn","stop_details":null,"raw_usage_start":{"input_tokens":100,"output_tokens":1},"raw_usage_final":{"input_tokens":100,"output_tokens":20},"raw_usage_merged":{"input_tokens":100,"output_tokens":20},"output_started":true,"complete":true,"message_objects":[{"sha256":%q,"bytes":%d}],"unknown_event_types":[],"unknown_event_type_overflow":0,"content_blocks":1,"content_error":false,"profile_violation":false}`, admission.AttemptID.String(), f.origin.AgentID.String(), f.origin.SessionID.String(), f.origin.ContextID.String(), admission.ParentTurnID.String(), admission.Quote.RequestSHA256.String(), admission.Request.Model, view.Result.MessageObjects[0].SHA256.String(), view.Result.MessageObjects[0].Bytes)
	if string(raw) != want {
		t.Fatal("DispatchPaid observation gained a replay object or left main's exact encoding")
	}
	revision := f.service.p.scopes[keyFor(submissionIDs{f.origin.AgentID, f.origin.SessionID, f.origin.ContextID}, "context")].revision
	arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"stored synthetic result"}`, revision)
	f.enable(t)
	_, start := f.send(t)
	result, events := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || result.ModelCalls != 2 || result.ToolDispatches != 1 || posts.Load() != 3 {
		t.Fatalf("loop turn outcome=%s reason=%s models=%d tools=%d posts=%d", result.Outcome, result.Reason, result.ModelCalls, result.ToolDispatches, posts.Load())
	}
	<-requests
	continuation := <-requests
	var first *ledger.RuntimePaidResult
	for _, event := range events {
		if value, ok := event.Data.(ledger.RuntimePaidResult); ok && first == nil {
			first = &value
		}
	}
	if first == nil || first.EvidenceObject == nil {
		t.Fatal("loop attempt lost its settled evidence")
	}
	evidence, err := agent.ReadPaidObject(ctx, PaidObjectLookup{AttemptID: first.AttemptID, GrantID: f.privateGrant, Object: *first.EvidenceObject, MaxBytes: maxObjectBytes})
	if err != nil {
		t.Fatal(err)
	}
	var observation paidObservationObject
	if json.Unmarshal(evidence, &observation) != nil || observation.ReplayObject == nil {
		t.Fatal("loop attempt observation did not retain its replay object")
	}
	replay, err := agent.ReadPaidObject(ctx, PaidObjectLookup{AttemptID: first.AttemptID, GrantID: f.privateGrant, Object: *observation.ReplayObject, MaxBytes: maxObjectBytes})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(continuation, &wire) != nil || len(wire.Messages) != 3 || wire.Messages[1].Role != "assistant" || !bytes.Equal(wire.Messages[1].Content, replay) {
		t.Fatal("continuation did not replay the loop attempt's retained bytes")
	}
}

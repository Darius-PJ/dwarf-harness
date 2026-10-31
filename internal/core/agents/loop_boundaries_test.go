package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"
)

func (f *turnFixture) seedMemory(t *testing.T, text string) uint64 {
	t.Helper()
	view, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := f.service.BindAgent(context.Background(), bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := agent.WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: f.privateGrant, Scope: "context", ExpectedVersion: view.Revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte(text)}}})
	if err != nil {
		t.Fatal(err)
	}
	return uint64(receipt.Version)
}

func TestTurnLoopBoundaryPrecedence(t *testing.T) {
	t.Parallel()
	t.Run("running_exchange", testLoopDeadlinePrecedence)
	for _, row := range []struct {
		name            string
		models, tools   int64
		calls           int
		outcome, reason string
		wantPosts       int32
		wantTools       int64
	}{
		{"last_allowed_answer", 1, 1, 0, "finished", "", 1, 0},
		{"no_continuation_room_no_effect", 1, 1, 1, "failed", "loop_limit", 1, 0},
		{"whole_batch_does_not_fit", 2, 1, 2, "failed", "loop_limit", 1, 0},
		{"correction_consumes_only_tool_slot", 2, 1, -1, "finished", "", 2, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int32
			var revision uint64
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				model, _ := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if posts.Add(1) == 1 && row.calls != 0 {
					calls := turnCalls("core_memory_v1", fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"must not be written"}`, revision))
					if row.calls == -1 {
						calls[0].Arguments = strings.Replace(calls[0].Arguments, `"schema":1`, `"schema":2`, 1)
					}
					if row.calls == 2 {
						calls = append(calls, struct{ ID, Name, Arguments string }{"call-two", calls[0].Name, calls[0].Arguments})
					}
					_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
				} else {
					_, _ = io.WriteString(w, turnAnthropic(model, nil, "Final answer at the exact bound."))
				}
			}, nil)
			before, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
			if err != nil {
				t.Fatal(err)
			}
			revision = before.Revision
			f.setting.Policy.MaxModelCalls, f.setting.Policy.MaxToolCalls = row.models, row.tools
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != row.outcome || row.reason != "" && result.Reason != row.reason || result.ModelCalls != int64(row.wantPosts) || result.ToolDispatches != row.wantTools || posts.Load() != row.wantPosts {
				t.Fatalf("boundary outcome=%s reason=%s models=%d tools=%d posts=%d", result.Outcome, result.Reason, result.ModelCalls, result.ToolDispatches, posts.Load())
			}
			view, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
			if err != nil {
				t.Fatal(err)
			}
			if view.Heads["memory"] != before.Heads["memory"] {
				t.Fatal("batch or correction boundary admitted a forbidden private effect")
			}
			outputs := f.toolResults(t, events)
			if row.calls == -1 && (len(outputs) != 1 || outputs[0].Error == nil || outputs[0].Error.Code != "invalid_argument") {
				t.Fatal("renderable correction did not produce its one static result")
			}
			if row.wantTools == 0 && len(outputs) != 0 {
				t.Fatal("unadmitted batch generated partial tool results")
			}
		})
	}
	t.Run("sealed_next_request_too_large", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int32
		var revision uint64
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if posts.Add(1) != 1 {
				t.Error("oversize continuation made a provider call")
				return
			}
			calls := turnCalls("core_memory_v1", fmt.Sprintf(`{"schema":1,"action":"read","scope":"context","slot":"memory","revision":%d,"max_bytes":61440}`, revision))
			for i := 1; i < 9; i++ {
				calls = append(calls, struct{ ID, Name, Arguments string }{fmt.Sprintf("call-%d", i+1), calls[0].Name, calls[0].Arguments})
			}
			_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
		}, nil)
		f.setting.Policy.MaxModelCalls, f.setting.Policy.MaxToolCalls = 2, 9
		revision = f.seedMemory(t, strings.Repeat("x", 60<<10))
		f.enable(t)
		_, start := f.send(t)
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "failed" || result.Reason != "request_too_large" || result.ModelCalls != 1 || result.ToolDispatches != 9 || posts.Load() != 1 {
			t.Fatalf("oversize outcome=%s reason=%s models=%d tools=%d posts=%d", result.Outcome, result.Reason, result.ModelCalls, result.ToolDispatches, posts.Load())
		}
		outputs := f.toolResults(t, events)
		if len(outputs) != 9 {
			t.Fatal("bounded batch lost accepted results at the request limit")
		}
		for _, output := range outputs {
			if output.Status != "ok" {
				t.Fatal("request-size refusal disguised a tool failure")
			}
		}
	})
}

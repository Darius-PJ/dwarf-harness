package agents

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testLoopNativeConflicts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"memory_revision", "task_version"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int32
			var name, args string
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				model, _ := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if posts.Add(1) == 1 {
					_, _ = io.WriteString(w, turnAnthropic(model, turnCalls(name, args), ""))
				} else {
					_, _ = io.WriteString(w, turnAnthropic(model, nil, "Conflict was not retried."))
				}
			}, nil)
			if kind == "memory_revision" {
				revision := f.seedMemory(t, "Keep this actual selected revision.")
				name, args = "core_memory_v1", fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"must not replace newer content"}`, revision-1)
			} else {
				work := scopedWorkContext(t, f.service, bindingOf(f.origin), 49100, 49101)
				task, assignment := uuid.NewV7(), uuid.NewV7()
				_, err := f.service.CreateTask(context.Background(), bindingOf(f.origin), TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("Synthetic version conflict"), AcceptanceSHA256: sha256.Sum256([]byte("acceptance")), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{f.origin.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(work)}})
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.service.TransitionTask(context.Background(), bindingOf(f.origin), TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "cancel"})
				if err != nil {
					t.Fatal(err)
				}
				f.issueToolGrant(t, uuid.NewV7(), []string{"task.cancel"}, []ledger.Resource{{Kind: "task", TaskID: task}})
				name, args = "core_task_v1", fmt.Sprintf(`{"schema":1,"action":"cancel","task_id":%q,"expected_version":1,"assignment_id":%q,"generation":1}`, task, assignment)
			}
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || result.ToolDispatches != 1 || posts.Load() != 2 {
				t.Fatal("native refusal changed turn continuation or retried the effect")
			}
			outputs := f.toolResults(t, events)
			if len(outputs) != 1 || outputs[0].Error == nil || outputs[0].Error.Code != "conflict" {
				t.Fatal("native stale-version refusal was collapsed into a different outcome")
			}
			for _, event := range events {
				switch value := event.Data.(type) {
				case ledger.RuntimeToolStarted:
					if value.EffectCommandID == (uuid.UUID{}) {
						t.Fatal("native conflict lacked its pre-minted command")
					}
					f.service.mu.Lock()
					match, err := f.service.findCommand(context.Background(), value.EffectCommandID, 0, f.service.p.through)
					f.service.mu.Unlock()
					if err != nil || match.found {
						t.Fatal("precommit conflict acquired a native effect")
					}
				case ledger.RuntimeToolResult:
					if value.Outcome != "conflict" || value.EffectReceiptSequence != 0 {
						t.Fatal("definite native conflict invented an ending receipt")
					}
				}
			}
		})
	}
}

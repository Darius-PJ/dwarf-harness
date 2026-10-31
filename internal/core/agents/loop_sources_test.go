package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func testLoopSourceBindings(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			const legacy = `{"schema":1,"entries":[],"legacy_marker":"Legacy opaque transcript."}`
			const artifact = "Selected context-bound artifact body."
			var posts atomic.Int32
			var arguments string
			f := newTurnFixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				model, raw := turnRequest(t, r)
				n := posts.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if !bytes.Contains(raw, []byte("Legacy opaque transcript.")) {
					t.Error("earlier-family transcript bytes were reinterpreted or dropped")
				}
				if n == 1 && !bytes.Contains(raw, []byte(artifact)) {
					t.Error("authorized selected artifact was omitted")
				}
				if n == 3 && bytes.Count(raw, []byte("Previous durable answer.")) != 1 {
					t.Error("completed turn manifest did not render its selected answer once")
				}
				if n == 1 || n == 3 {
					name, args := "core_memory_v1", arguments
					if n == 3 {
						name, args = "core_status_v1", `{"schema":1,"subject":"context"}`
					}
					calls := turnCalls(name, args)
					calls[0].ID = fmt.Sprintf("source-call-%d", n)
					if provider == "anthropic" {
						_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
					} else {
						output := fmt.Sprintf(`[{"type":"function_call","id":"source-item-%d","call_id":%q,"name":%q,"arguments":%q,"status":"completed"}]`, n, calls[0].ID, name, args)
						_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", paidOpenAIUsage(100, 0, 0, 20), output, "", 1))
					}
				} else if n == 2 || n == 4 {
					if provider == "anthropic" {
						_, _ = io.WriteString(w, turnAnthropic(model, nil, "Previous durable answer."))
					} else {
						output := `[{"type":"message","id":"source-answer","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Previous durable answer.","annotations":[],"logprobs":[]}]}]`
						_, _ = io.WriteString(w, paidOpenAIEvent(model, "completed", paidOpenAIUsage(100, 0, 0, 20), output, "", 1))
					}
				} else {
					t.Error("refused source produced a model exchange")
				}
			}, nil)
			task := uuid.NewV7()
			_, err := f.service.CreateTask(context.Background(), bindingOf(f.origin), TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte(artifact), AcceptanceSHA256: sha256.Sum256([]byte("synthetic acceptance")), Audience: []uuid.UUID{f.origin.AgentID}})
			if err != nil {
				t.Fatal(err)
			}
			taskView, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: task})
			if err != nil {
				t.Fatal(err)
			}
			ref := taskView.DescriptionRef
			f.issueToolGrant(t, uuid.NewV7(), []string{"artifact.read"}, []ledger.Resource{{Kind: "artifact", Artifact: ref}})
			peerBinding := registerPrivate(t, f.service, 65000)
			peer := f.contextSetting(t, peerBinding)
			view, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
			if err != nil {
				t.Fatal(err)
			}
			agent := scopedBound(t, f.service, bindingOf(f.origin))
			written, err := agent.WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: f.privateGrant, Scope: "context", ExpectedVersion: view.Revision, Changes: []Change{{Slot: "transcript", Mode: "append", Bytes: []byte(legacy)}}})
			if err != nil {
				t.Fatal(err)
			}
			arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"Native history data."}`, written.Version)
			f.enable(t, peer)
			peerView, err := f.service.OpenSession(context.Background(), peerBinding)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(peerView.Close)
			missing := ref
			missing.SourceSequence += 1000000
			var denied error
			for _, selected := range []ledger.ArtifactRef{ref, missing} {
				_, err := peerView.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: peerBinding.ContextID, Operation: "message.send", Arguments: session.MessageSend{TurnID: uuid.NewV7(), Text: "Do not disclose an inaccessible source.", Artifacts: []ledger.ArtifactRef{selected}}})
				if !errors.Is(err, ErrDenied) || denied != nil && err != denied {
					t.Fatal("foreign source and missing artifact were distinguishable")
				}
				denied = err
			}
			if posts.Load() != 0 || len(loopPaidAdmissions(t, f.service)) != 0 {
				t.Fatal("inaccessible artifact reached paid admission")
			}
			command := turnMessage(f.origin.ContextID, "Use the selected artifact.")
			input := command.Arguments.(session.MessageSend)
			input.Artifacts = []ledger.ArtifactRef{ref}
			command.Arguments = input
			start, err := f.view.Do(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || result.ModelCalls != 2 || result.ToolDispatches != 1 {
				t.Fatal("authorized sources did not complete the native tool turn")
			}
			for _, result := range f.toolResults(t, events) {
				if result.Status != "ok" {
					t.Fatal("source-bound native write failed")
				}
			}
			// Await the Session's historical result, not a scheduler-private flag.
			for {
				page, err := f.view.Since(context.Background(), 0, 128)
				if err != nil {
					t.Fatal(err)
				}
				status, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: start.TurnID}})
				if err != nil {
					t.Fatal(err)
				}
				if status.Data.(session.TurnView).Result != nil {
					break
				}
				<-page.Wake
			}
			_, next := f.send(t)
			repeated, _ := f.finished(t, next.TurnID)
			if repeated.Outcome != "finished" || repeated.ModelCalls != 2 || repeated.ToolDispatches != 1 || posts.Load() != 4 {
				t.Fatal("shared transcript heads could not drive the next complete turn")
			}
		})
	}
}

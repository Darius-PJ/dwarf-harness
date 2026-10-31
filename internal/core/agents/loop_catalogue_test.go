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
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (f *turnFixture) issueToolGrant(t *testing.T, id uuid.UUID, operations []string, resources []ledger.Resource, recipients ...Binding) {
	t.Helper()
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	class := resources[0].Kind
	if class == "context" {
		class = "private"
	}
	if len(recipients) == 0 {
		recipients = []Binding{bindingOf(f.origin)}
	}
	_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: id, Grantee: bindingOf(f.origin), Operations: operations, Resources: resources, DataClass: class, Recipients: recipients, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTurnLoopToolCatalogue(t *testing.T) {
	t.Parallel()
	t.Run("revoked_after_native_effect_before_disclosure", testLoopCatalogueFinalDisclosure)
	t.Run("one_chain_page_other_chain_exact_invocation", func(t *testing.T) {
		t.Parallel()
		task := uuid.NewV7()
		assignment := uuid.NewV7()
		var f *turnFixture
		var work Binding
		var posts atomic.Int32
		f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, raw := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			switch posts.Add(1) {
			case 1:
				create := fmt.Sprintf(`{"schema":1,"tool_path":"core.task.create","operation_schema":1,"arguments":{"schema":1,"task_id":%q,"description":"Synthetic catalogue task","acceptance_sha256":%q,"dependencies":[],"audience":[%q],"assignment":{"assignment_id":%q,"assignee":{"agent_id":%q,"context_id":%q}}}}`, task, strings.Repeat("1", 64), f.origin.AgentID, assignment, work.AgentID, work.ContextID)
				calls := turnCalls("tools_discover_v1", `{"schema":1,"query":"","limit":8}`)
				calls = append(calls, struct{ ID, Name, Arguments string }{"call-create", "tools_invoke_v1", create})
				_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
			case 2:
				if !bytes.Contains(raw, []byte("Synthetic catalogue task")) {
					t.Error("exact task invocation was not replayed")
				}
				list := `{"schema":1,"tool_path":"core.task.list","operation_schema":1,"arguments":{"schema":1,"limit":1}}`
				calls := turnCalls("tools_invoke_v1", list)
				calls[0].ID = "call-list"
				_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
			case 3:
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "Catalogue task recorded."))
			default:
				t.Error("unexpected catalogue continuation")
			}
		}, func(deps *serviceDeps) {
			deps.afterScopedCommit = func(receipt ScopedReceipt) error {
				if receipt.Operation != "task.create" {
					return nil
				}
				capacity := paidPhysicalCapacity()
				capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
				_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: uuid.NewV7(), Grantee: bindingOf(f.origin), Operations: []string{"task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: task}}, DataClass: "task", Recipients: []Binding{bindingOf(f.origin)}, Limits: capacity, Lifetime: "until_revoked"}})
				return err
			}
		})
		work = scopedWorkContext(t, f.service, bindingOf(f.origin), 49000, 49001)
		other := uuid.NewV7()
		f.issueToolGrant(t, other, []string{"task.create"}, []ledger.Resource{{Kind: "task"}}, work)
		f.enable(t)
		_, start := f.send(t)
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "finished" || result.ToolDispatches != 3 || posts.Load() != 3 {
			t.Fatalf("catalogue turn outcome=%s reason=%s tools=%d posts=%d", result.Outcome, result.Reason, result.ToolDispatches, posts.Load())
		}
		outputs := f.toolResults(t, events)
		var page loopCataloguePage
		if json.Unmarshal(modelJSON(outputs[0].Data), &page) != nil || len(page.Entries) != 2 || page.Entries[0].ToolPath != "core.context.handoff.write" || page.Entries[1].ToolPath != "core.context.read" || page.Next != "" {
			t.Fatal("discovery unioned another chain or exposed an inaccessible page count")
		}
		for _, entry := range page.Entries {
			if !json.Valid(entry.ResultSchema) || bytes.Contains(entry.ResultSchema, []byte(`"data":{}`)) {
				t.Fatal("catalogue result schema is not concrete")
			}
		}
		for _, event := range events {
			if start, ok := event.Data.(ledger.RuntimeToolStarted); ok && start.Operation == "tools.discover" && start.GrantID != f.privateGrant {
				t.Fatal("discovery did not record its one authorizing leaf")
			}
		}
		var created struct {
			Receipt struct {
				GrantID   uuid.UUID
				Operation string
				Version   int64
			}
			State string
		}
		if json.Unmarshal(modelJSON(outputs[1].Data), &created) != nil || created.Receipt.GrantID != other || created.Receipt.Operation != "task.create" || created.Receipt.Version != 1 || created.State == "" {
			t.Fatalf("native creation result status=%s error=%s operation=%s version=%d state=%s", outputs[1].Status, modelJSON(outputs[1].Error), created.Receipt.Operation, created.Receipt.Version, created.State)
		}
		var listed struct {
			WaitingBlocked []struct{ TaskID uuid.UUID } `json:"waiting_blocked"`
			Counts         TaskFactCounts               `json:"counts"`
			Through        uint64                       `json:"through_sequence"`
			Incomplete     bool                         `json:"incomplete"`
		}
		listRaw := modelJSON(outputs[2].Data)
		if json.Unmarshal(listRaw, &listed) != nil || len(listed.WaitingBlocked) != 1 || listed.WaitingBlocked[0].TaskID != task || listed.Counts.WaitingBlocked != 1 || listed.Counts.WaitingReady != 0 || listed.Through == 0 || listed.Incomplete || bytes.Contains(listRaw, []byte(`"Next"`)) {
			t.Fatal("task list did not return authorized blocked facts or leaked an unusable native cursor")
		}
	})
	t.Run("unknown_and_unauthorized_fixed_denial", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if posts.Add(1) == 1 {
				calls := turnCalls("core_status_v1", `{"schema":1,"subject":"context"}`)
				calls = append(calls, struct{ ID, Name, Arguments string }{"call-hidden", "tools_invoke_v1", `{"schema":1,"tool_path":"core.task.list","operation_schema":1,"arguments":{"schema":1,"limit":1}}`}, struct{ ID, Name, Arguments string }{"call-unknown", "tools_invoke_v1", `{"schema":1,"tool_path":"core.unknown","operation_schema":1,"arguments":{"schema":1}}`})
				_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
			} else {
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "No information disclosed."))
			}
		}, nil)
		old := f.privateGrant
		f.privateGrant = uuid.NewV7()
		f.issueToolGrant(t, f.privateGrant, []string{"private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, []ledger.Resource{{Kind: "context", Binding: ledger.Binding(bindingOf(f.origin)), Scope: "agent", Slots: []string{"instructions", "memory"}}, {Kind: "context", Binding: ledger.Binding(bindingOf(f.origin)), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}})
		f.revoke(t, old)
		f.enable(t)
		_, start := f.send(t)
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "finished" || result.ToolDispatches != 3 || posts.Load() != 2 {
			t.Fatal("denial stopped the turn or caused a hidden call")
		}
		outputs := f.toolResults(t, events)
		for _, output := range outputs {
			if output.Status != "error" || string(modelJSON(output.Error)) != `{"code":"denied"}` || output.Data != nil {
				t.Fatal("unknown and inaccessible targets disclosed different refusal facts")
			}
		}
		for _, event := range events {
			if started, ok := event.Data.(ledger.RuntimeToolStarted); ok {
				if started.Operation != "unrecognized" || started.Target != nil || started.GrantID != (uuid.UUID{}) || started.EffectCommandID != (uuid.UUID{}) {
					t.Fatal("denied target was exposed in a recognized start")
				}
				raw, err := f.service.readLoopObject(context.Background(), f.origin, started.RequestObject)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte("core_status")) || bytes.Contains(raw, []byte("core.task")) {
					t.Fatal("rejected request was retained as content rather than its digest")
				}
			}
		}
	})
}

// testLoopCatalogueFinalDisclosure revokes the creating chain right after
// tools_invoke's native task.create commits. That entrance has no later
// authority check of its own, so only the runtime's final current-chain
// recheck stands between its receipt and the next model request.
func testLoopCatalogueFinalDisclosure(t *testing.T) {
	t.Parallel()
	var f *turnFixture
	var posts atomic.Int32
	var arguments string
	var creator uuid.UUID
	var created atomic.Pointer[ScopedReceipt]
	f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if receipt := created.Load(); receipt != nil && bytes.Contains(raw, []byte(receipt.SubmissionSHA256.String())) {
			t.Error("revoked catalogue invocation disclosed its native receipt to the model")
		}
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("tools_invoke_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Nothing further disclosed."))
		}
	}, func(deps *serviceDeps) {
		deps.afterScopedCommit = func(receipt ScopedReceipt) error {
			if receipt.Operation == "task.create" && receipt.GrantID == creator && created.CompareAndSwap(nil, &receipt) {
				f.revoke(t, creator)
			}
			return nil
		}
	})
	task := uuid.NewV7()
	arguments = fmt.Sprintf(`{"schema":1,"tool_path":"core.task.create","operation_schema":1,"arguments":{"schema":1,"task_id":%q,"description":"Synthetic revoked creation","acceptance_sha256":%q,"dependencies":[],"audience":[%q]}}`, task, strings.Repeat("2", 64), f.origin.AgentID)
	creator = uuid.NewV7()
	f.issueToolGrant(t, creator, []string{"task.create"}, []ledger.Resource{{Kind: "task"}})
	f.enable(t)
	_, start := f.send(t)
	result, events := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || result.ToolDispatches != 1 || posts.Load() != 2 {
		t.Fatalf("final-disclosure revocation outcome=%s reason=%s tools=%d posts=%d", result.Outcome, result.Reason, result.ToolDispatches, posts.Load())
	}
	receipt := created.Load()
	if receipt == nil {
		t.Fatal("the native creation was not authorized by the separate creating chain")
	}
	outputs := f.toolResults(t, events)
	if len(outputs) != 1 || outputs[0].Status != "error" || outputs[0].Data != nil || string(modelJSON(outputs[0].Error)) != `{"code":"denied"}` {
		t.Fatal("revocation after the native effect did not end in the fixed denial")
	}
	for _, event := range events {
		switch value := event.Data.(type) {
		case ledger.RuntimeToolStarted:
			if value.Operation != "task.create" || value.EffectCommandID != receipt.CommandID {
				t.Fatal("catalogue invocation did not bind the committed native effect")
			}
		case ledger.RuntimeToolResult:
			if value.Outcome != "denied" || value.EffectReceiptSequence != int64(receipt.Sequence) {
				t.Fatal("denied disclosure did not name the native effect's actual receipt")
			}
		}
	}
}

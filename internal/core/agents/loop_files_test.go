package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func (f *turnFixture) files(t *testing.T, prefix string) (workspace.Scratch, uuid.UUID) {
	t.Helper()
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	ws, registered := privateWorkspace(t, f.serviceFixture)
	f.workspace = ws
	t.Cleanup(func() { _ = f.service.Close() })
	scratch, err := ws.Scratch(context.Background(), f.origin.ContextID, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant := uuid.NewV7()
	capacity := paidPhysicalCapacity()
	capacity.MaxOperationBytes = 4 << 20
	_, err = f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grant, Grantee: bindingOf(f.origin), Operations: []string{"file.read", "file.search", "file.write", "file.edit"}, Resources: []ledger.Resource{{Kind: "workspace", Binding: ledger.Binding(bindingOf(f.origin)), WorkspaceID: registered.ID, PathPrefix: prefix}}, DataClass: "workspace", Recipients: []Binding{bindingOf(f.origin)}, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	return scratch, grant
}
func (f *turnFixture) toolResults(t *testing.T, events []session.Event) []loopToolEnvelope {
	t.Helper()
	var results []loopToolEnvelope
	for _, event := range events {
		result, ok := event.Data.(ledger.RuntimeToolResult)
		if !ok {
			continue
		}
		raw, err := f.service.readLoopObject(context.Background(), f.origin, result.ResultObject)
		if err != nil {
			t.Fatal(err)
		}
		var value loopToolEnvelope
		if json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid tool result object")
		}
		results = append(results, value)
	}
	return results
}
func TestTurnLoopScopedFiles(t *testing.T) {
	t.Parallel()
	t.Run("write_edit_read_search", func(t *testing.T) {
		t.Parallel()
		var calls []struct{ ID, Name, Arguments string }
		var posts atomic.Int64
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, raw := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if posts.Add(1) == 1 {
				_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
			} else {
				if !bytes.Contains(raw, []byte("after synthetic edit")) {
					t.Error("actual edited file result absent from continuation")
				}
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
			}
		}, nil)
		scratch, _ := f.files(t, "safe")
		if err := os.Mkdir(filepath.Join(scratch.Dir, "safe"), 0700); err != nil {
			t.Fatal(err)
		}
		base := fmt.Sprintf(`"workspace_id":%q,"path":"safe/value.txt"`, scratch.WorkspaceID)
		before := "before synthetic edit"
		calls = []struct{ ID, Name, Arguments string }{
			{"write-one", "file_write_v1", fmt.Sprintf(`{"schema":1,%s,"expected_sha256":"absent","text":%q}`, base, before)},
			{"edit-one", "file_edit_v1", fmt.Sprintf(`{"schema":1,%s,"expected_sha256":%q,"old_text":"before","new_text":"after"}`, base, ledger.Digest(sha256.Sum256([]byte(before))).String())},
			{"read-one", "file_read_v1", fmt.Sprintf(`{"schema":1,%s,"max_bytes":1024}`, base)},
			{"search-one", "file_search_v1", fmt.Sprintf(`{"schema":1,"workspace_id":%q,"path":"safe","mode":"text","query":"synthetic","max_bytes":4096}`, scratch.WorkspaceID)},
		}
		f.enable(t)
		_, start := f.send(t)
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "finished" || result.ToolDispatches != 4 || posts.Load() != 2 {
			t.Fatalf("file turn: %s/%s tools=%d", result.Outcome, result.Reason, result.ToolDispatches)
		}
		actual, err := os.ReadFile(filepath.Join(scratch.Dir, "safe", "value.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(actual) != "after synthetic edit" {
			t.Fatal("rooted file edit did not occur")
		}
		for _, value := range f.toolResults(t, events) {
			if value.Status != "ok" {
				t.Fatalf("concrete file operation refused: %v", value.Error)
			}
		}
		for _, event := range events {
			value, ok := event.Data.(ledger.RuntimeToolResult)
			if !ok {
				continue
			}
			f.service.mu.Lock()
			record, found, err := f.service.sourceRecord(context.Background(), value.EffectReceiptSequence, f.service.p.through)
			f.service.mu.Unlock()
			if err != nil || !found || record.Type != ledger.TypeRuntimeScopedReceipt || record.TurnID != start.TurnID {
				t.Fatal("file result did not name a real local ending")
			}
			receipt, err := ledger.DecodeRuntimeScopedReceipt(record.Payload)
			if err != nil || receipt.Submission.Operation != "local.end" {
				t.Fatal("file effect selected admission rather than its actual ending")
			}
		}
		observed, err := f.workspace.Scratch(context.Background(), f.origin.ContextID, scratch.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if observed.ObservationID == (uuid.UUID{}) || observed.TipCommit == scratch.TipCommit {
			t.Fatal("writer was not observed at turn end")
		}
	})
	for _, kind := range []string{"git_component", "link", "foreign_prefix"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var args string
			var posts atomic.Int64
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				model, raw := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if bytes.Contains(raw, []byte("outside synthetic sentinel")) {
					t.Error("out-of-scope file content reached a model")
				}
				if posts.Add(1) == 1 {
					_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_read_v1", args), ""))
				} else {
					_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
				}
			}, nil)
			scratch, _ := f.files(t, "safe")
			if err := os.MkdirAll(filepath.Join(scratch.Dir, "safe"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(scratch.Dir, "outside"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(scratch.Dir, "outside", "sentinel.txt"), []byte("outside synthetic sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			path := "outside/sentinel.txt"
			if kind == "git_component" {
				if err := os.MkdirAll(filepath.Join(scratch.Dir, "safe", ".GiT"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(scratch.Dir, "safe", ".GiT", "sentinel.txt"), []byte("outside synthetic sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
				path = "safe/.GiT/sentinel.txt"
			} else if kind == "link" {
				if err := loopTestDirectoryLink(filepath.Join(scratch.Dir, "safe", "linked"), filepath.Join(scratch.Dir, "outside")); err != nil {
					t.Skip("native link fixture unavailable; rooted refusal still required")
				}
				path = "safe/linked/sentinel.txt"
			}
			args = fmt.Sprintf(`{"schema":1,"workspace_id":%q,"path":%q,"max_bytes":4096}`, scratch.WorkspaceID, path)
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || posts.Load() != 2 {
				t.Fatalf("refusal did not continue correctly: %s/%s", result.Outcome, result.Reason)
			}
			results := f.toolResults(t, events)
			code := "denied"
			if kind == "git_component" {
				code = "invalid_argument"
			}
			if len(results) != 1 || results[0].Status != "error" || results[0].Error == nil || results[0].Error.Code != code {
				t.Fatal("scoped file escape did not produce a nondisclosing refusal")
			}
		})
	}
}

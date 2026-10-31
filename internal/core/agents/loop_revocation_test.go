package agents

import (
	"bytes"
	"context"
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
)

func (f *turnFixture) revoke(t *testing.T, id uuid.UUID) {
	t.Helper()
	f.service.mu.Lock()
	version := f.service.p.authority
	f.service.mu.Unlock()
	_, err := f.service.RevokeGrant(context.Background(), bindingOf(f.origin), GrantRevocation{CommandID: uuid.NewV7(), RevokeGrantID: id, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(version)})
	if err != nil {
		t.Error("synthetic grant revocation failed")
	}
}
func TestTurnLoopRevocationAndRecords(t *testing.T) {
	t.Parallel()
	t.Run("native_conflicts", testLoopNativeConflicts)
	for _, stage := range []string{"before_native", "after_admission", "before_effect", "before_disclosure", "cancel_before_native"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			var f *turnFixture
			var grant uuid.UUID
			var arguments string
			var posts atomic.Int64
			var triggered atomic.Bool
			cancelled := make(chan error, 1)
			boundary := func(turn uuid.UUID) {
				if !triggered.CompareAndSwap(false, true) {
					return
				}
				if stage == "cancel_before_native" {
					f.service.loop.mu.Lock()
					done := f.service.loop.turns[turn].ctx.Done()
					f.service.loop.mu.Unlock()
					go func() {
						_, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}})
						cancelled <- err
					}()
					<-done
				} else {
					f.revoke(t, grant)
				}
			}
			f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				model, raw := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if bytes.Contains(raw, []byte("protected synthetic file bytes")) {
					t.Error("revoked file disclosure reached continuation")
				}
				if posts.Add(1) == 1 {
					_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_read_v1", arguments), ""))
				} else {
					_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
				}
			}, func(deps *serviceDeps) {
				deps.afterLoopCommit = func(receipt ledger.RuntimeLoopReceipt) {
					if receipt.Submission.Operation == "tool.begin" && (stage == "before_native" || stage == "cancel_before_native") {
						boundary(receipt.Submission.TurnID)
					}
				}
				deps.afterScopedCommit = func(receipt ScopedReceipt) error {
					operation := map[string]string{"after_admission": "local.admit", "before_effect": "local.begin", "before_disclosure": "local.end"}[stage]
					if operation != "" && receipt.Operation == operation {
						f.service.mu.Lock()
						work, _, err := f.service.loadWork(context.Background(), receipt.WorkID, f.service.p.through)
						f.service.mu.Unlock()
						if err != nil {
							return err
						}
						boundary(work.admission.ParentTurnID)
					}
					return nil
				}
			})
			scratch, fileGrant := f.files(t, "safe")
			grant = fileGrant
			if err := os.Mkdir(filepath.Join(scratch.Dir, "safe"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(scratch.Dir, "safe", "value.txt"), []byte("protected synthetic file bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			arguments = fmt.Sprintf(`{"schema":1,"workspace_id":%q,"path":"safe/value.txt","max_bytes":4096}`, scratch.WorkspaceID)
			f.enable(t)
			_, start := f.send(t)
			result, events := f.finished(t, start.TurnID)
			if !triggered.Load() {
				t.Fatal("effect boundary was not exercised")
			}
			if stage == "cancel_before_native" {
				if err := <-cancelled; err != nil {
					t.Fatal(err)
				}
				if result.Outcome != "cancelled" || posts.Load() != 1 {
					t.Fatal("cancellation did not stop before the native entrance")
				}
			} else if result.Outcome != "finished" || posts.Load() != 2 {
				t.Fatalf("revoked target stopped unrelated work: %s/%s", result.Outcome, result.Reason)
			}
			var started *ledger.RuntimeToolStarted
			var finished *ledger.RuntimeToolResult
			for _, event := range events {
				switch value := event.Data.(type) {
				case ledger.RuntimeToolStarted:
					started = &value
				case ledger.RuntimeToolResult:
					finished = &value
				}
			}
			if started == nil || finished == nil || started.EffectCommandID == (uuid.UUID{}) {
				t.Fatal("accepted native invocation lost its durable pair")
			}
			f.service.mu.Lock()
			native, err := f.service.findCommand(context.Background(), started.EffectCommandID, 0, f.service.p.through)
			f.service.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before_native" || stage == "cancel_before_native" {
				if native.found || finished.EffectReceiptSequence != 0 {
					t.Fatal("uncommitted native command acquired an invented receipt")
				}
			} else {
				if !native.found || native.scoped == nil || native.scoped.admission == nil || finished.EffectReceiptSequence == 0 {
					t.Fatal("admitted native work lost its actual ending")
				}
				f.service.mu.Lock()
				record, found, err := f.service.sourceRecord(context.Background(), finished.EffectReceiptSequence, f.service.p.through)
				f.service.mu.Unlock()
				if err != nil || !found {
					t.Fatal("native ending missing")
				}
				receipt, err := ledger.DecodeRuntimeScopedReceipt(record.Payload)
				if err != nil || receipt.Submission.Operation != "local.end" {
					t.Fatal("refusal did not select local.end")
				}
			}
		})
	}
}

package agents

import (
	"context"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func continuationLocalCrashChild(t *testing.T, boundary string, barrier func(*turnFixture, uuid.UUID)) {
	t.Helper()
	f := newContinuationLocal(t, "local_slot", false)
	var turn uuid.UUID
	if boundary == "local_claim" {
		f.service.deps.afterScopedClaim = func(sub ledger.ScopedSubmission) {
			if sub.Origin == f.origin && sub.Operation == "private.write" {
				barrier(f.turnFixture, turn)
			}
		}
	} else {
		f.service.deps.afterScopedCommit = func(receipt ScopedReceipt) error {
			if receipt.Origin == f.origin && receipt.Operation == "private.write" {
				barrier(f.turnFixture, turn)
			}
			return nil
		}
	}
	f.enable(t)
	command := turnMessage(f.origin.ContextID, "local reservation crash")
	turn = command.Arguments.(session.MessageSend).TurnID
	if _, err := f.view.Do(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	continuationWait(t, f.turnFixture, turn, "local_slot")
	f.free()
	f.finished(t, turn)
	t.Fatal("local-claim crash boundary was not reached")
}

func testContinuationLocalRestart(t *testing.T) {
	for _, row := range []struct {
		name, boundary string
		executed       bool
	}{
		{"restart_local_wait", "local_claim", false},
		{"restart_tool_effect_without_result", "local_effect", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := continuationKilled(t, row.boundary)
			owner := f.owner(t)
			if !row.executed && (owner.Snapshot.State != "waiting_resource" || owner.Snapshot.ResumeState != "tool_pending" || owner.Snapshot.WaitReason != "local_slot") || row.executed && owner.Snapshot.State != "tool_pending" {
				t.Fatalf("atomic crash lost its durable phase: %+v", owner.Snapshot)
			}
			var effect uuid.UUID
			for _, record := range f.records(t) {
				if record.Type == ledger.TypeRuntimeToolStarted {
					v := scopedDecode(t, ledger.DecodeRuntimeToolStarted, record.Payload)
					effect = v.EffectCommandID
				}
			}
			if !validID(effect) {
				t.Fatal("kill preceded the started atomic tool")
			}
			match, err := f.service.findCommand(context.Background(), nil, effect, 0, f.store.Durable())
			if err != nil || match.found != row.executed {
				t.Fatalf("wrong native kill boundary: found=%v err=%v", match.found, err)
			}
			var effectSequence int64
			if row.executed {
				effectSequence = int64(match.scoped.first) + int64(match.scoped.receiptIndex)
			}
			binding := ReadBinding{Binding: bindingOf(f.signal.Origin), Scope: "context"}
			before, err := f.service.Snapshot(context.Background(), binding)
			if err != nil {
				t.Fatal(err)
			}
			budget, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
			receipt, err := f.service.ReconcileLoop(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			f.assertEnded(t, "interrupted", 0)
			if match, err := f.service.findCommand(context.Background(), nil, effect, 0, f.store.Durable()); err != nil || match.found != row.executed {
				t.Fatalf("recovery replayed or lost the atomic effect: found=%v err=%v", match.found, err)
			}
			after, err := f.service.Snapshot(context.Background(), binding)
			if err != nil || after.Heads["memory"] != before.Heads["memory"] {
				t.Fatalf("recovery re-executed the memory write: before=%+v after=%+v err=%v", before.Heads["memory"], after.Heads["memory"], err)
			}
			afterBudget, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || afterBudget.Used.LocalUnits != budget.Used.LocalUnits {
				t.Fatalf("recovery repeated the atomic charge: before=%+v after=%+v err=%v", budget.Used, afterBudget.Used, err)
			}
			results := 0
			for _, record := range f.records(t) {
				if record.Type == ledger.TypeRuntimeToolResult {
					v := scopedDecode(t, ledger.DecodeRuntimeToolResult, record.Payload)
					results++
					if v.Outcome != "unavailable" || v.EffectReceiptSequence != effectSequence {
						t.Fatalf("recovery invented or lost a local effect: %+v", v)
					}
				}
			}
			if results != 1 || f.owner(t).ToolDispatches != 1 {
				t.Fatalf("recovered tool counted more than once: results=%d owner=%+v", results, f.owner(t))
			}
			through := f.store.Durable()
			if retry, err := f.service.ReconcileLoop(context.Background(), request); err != nil || retry != receipt || f.store.Durable() != through {
				t.Fatalf("atomic recovery retry repeated effects: %+v %v", retry, err)
			}
		})
	}
}

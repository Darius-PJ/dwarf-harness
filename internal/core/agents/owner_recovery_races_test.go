package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testContinuationRecoveryBindings(t *testing.T) {
	t.Run("reconcile_stale_and_mismatch", func(t *testing.T) {
		t.Parallel()
		f := continuationKilled(t, "ready")
		ctx := context.Background()
		owner := f.owner(t)
		request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version - 1}
		before := f.store.Durable()
		if receipt, err := f.service.ReconcileLoop(ctx, request); !errors.Is(err, ErrContinuationStale) || receipt != (ContinuationReceipt{}) || f.store.Durable() != before {
			t.Fatalf("stale preimage was normalized: %+v %v", receipt, err)
		}
		request.ExpectedOwnerVersion++
		receipt, err := f.service.ReconcileLoop(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		before = f.store.Durable()
		wrong := request
		wrong.ExpectedOwnerVersion++
		if _, err := f.service.ReconcileLoop(ctx, wrong); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != before {
			t.Fatalf("changed retry preimage accepted: %v", err)
		}
		wrong = request
		wrong.TurnID = uuid.NewV7()
		if _, err := f.service.ReconcileLoop(ctx, wrong); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != before {
			t.Fatalf("changed retry turn accepted: %v", err)
		}
		if retry, err := f.service.ReconcileLoop(ctx, request); err != nil || !reflect.DeepEqual(retry, receipt) {
			t.Fatalf("original retry lost: %+v %v", retry, err)
		}
		terminal := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		if receipt, err := f.service.ReconcileLoop(ctx, terminal); !errors.Is(err, ErrRecoveryPending) || receipt != (ContinuationReceipt{}) || f.store.Durable() != before {
			t.Fatalf("terminal no-transition call consumed an ID: %+v %v", receipt, err)
		}
	})
	t.Run("reconcile_same_id_retry", func(t *testing.T) {
		t.Parallel()
		f := continuationKilled(t, "paid_sent")
		var observations int
		f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			if len(d) != 0 && d[len(d)-1].Type == ledger.TypeRuntimePaidReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, d[len(d)-1].Payload)
				if v.Submission.Operation == "paid.observe" {
					observations++
					return nil
				}
			}
			return d
		}
		request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		receipt, err := f.service.ReconcileLoop(context.Background(), request)
		if !errors.Is(err, ErrRecoveryPending) || receipt.CommandID != request.CommandID || observations != 1 {
			t.Fatalf("child refusal did not retain one accepted entry: %+v %v attempts=%d", receipt, err, observations)
		}
		before := f.store.Durable()
		if retry, err := f.service.ReconcileLoop(context.Background(), request); err != nil || !reflect.DeepEqual(retry, receipt) || observations != 1 || f.store.Durable() != before {
			t.Fatalf("same-ID retry reran incomplete child bookkeeping: %+v %v attempts=%d", retry, err, observations)
		}
		f.service.deps.beforeSubmit = nil
		next := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		if _, err := f.service.ReconcileLoop(context.Background(), next); err != nil {
			t.Fatal(err)
		}
		f.assertEnded(t, "interrupted", 0)
	})
}

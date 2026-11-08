package agents

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type continuationRecoveryResult struct {
	receipt ContinuationReceipt
	err     error
}

func testContinuationRecoveryBindings(t *testing.T) {
	t.Run("reconcile_stale_and_mismatch", func(t *testing.T) {
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
	t.Run("reconcile_concurrent_distinct", func(t *testing.T) {
		f := continuationKilled(t, "ready")
		owner := f.owner(t)
		start := make(chan struct{})
		results := make(chan continuationRecoveryResult, 2)
		for range 2 {
			request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
			go func() {
				<-start
				receipt, err := f.service.ReconcileLoop(context.Background(), request)
				results <- continuationRecoveryResult{receipt, err}
			}()
		}
		close(start)
		accepted, stale := 0, 0
		for range 2 {
			result := <-results
			if result.err == nil {
				accepted++
			} else if errors.Is(result.err, ErrContinuationStale) && result.receipt == (ContinuationReceipt{}) {
				stale++
			} else {
				t.Fatalf("unexpected concurrent recovery result: %+v", result)
			}
		}
		if accepted != 1 || stale != 1 {
			t.Fatalf("distinct callers both consumed the same preimage: accepted=%d stale=%d", accepted, stale)
		}
		f.assertEnded(t, "interrupted", 0)
	})
	t.Run("already_reconciling_close_is_callers_command", func(t *testing.T) {
		f := continuationKilled(t, "ready")
		f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			if len(d) != 0 && d[len(d)-1].Type == ledger.TypeRuntimeContinuationReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, d[len(d)-1].Payload)
				if v.Submission.Operation == "owner.close" {
					return nil
				}
			}
			return d
		}
		request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		accepted, err := f.service.ReconcileLoop(context.Background(), request)
		if !errors.Is(err, ErrRecoveryPending) || accepted.CommandID != request.CommandID {
			t.Fatalf("accepted entry was lost on close refusal: %+v %v", accepted, err)
		}
		owner := f.owner(t)
		if owner.Snapshot.State != "reconciling" {
			t.Fatalf("wrong deferred-close state: %+v", owner.Snapshot)
		}
		f.service.deps.beforeSubmit = nil
		before := f.store.Durable()
		closeRequest := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
		closed, err := f.service.ReconcileLoop(context.Background(), closeRequest)
		if err != nil || closed.CommandID != closeRequest.CommandID || closed.State != "terminal" || closed.OwnerVersion != owner.Snapshot.Version+1 || f.store.Durable() != before+3 {
			t.Fatalf("deferred caller did not own exactly the close: %+v %v", closed, err)
		}
		f.assertEnded(t, "interrupted", 0)
		before = f.store.Durable()
		if retry, err := f.service.ReconcileLoop(context.Background(), request); err != nil || !reflect.DeepEqual(retry, accepted) || f.store.Durable() != before {
			t.Fatalf("entry retry was rebound to close: %+v %v", retry, err)
		}
	})
	t.Run("reconcile_same_id_retry", func(t *testing.T) {
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

func testContinuationCancelDuringRecovery(t *testing.T) {
	t.Run("cancel_during_reconcile_release", func(t *testing.T) {
		f := continuationKilled(t, "paid_sent")
		view := f.openSession(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var held atomic.Bool
		f.service.deps.beforeLookup = func() {
			if held.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		done := make(chan continuationRecoveryResult, 1)
		go func() {
			receipt, err := f.service.ReconcileLoop(context.Background(), request)
			done <- continuationRecoveryResult{receipt, err}
		}()
		<-entered // ReconcilePaid's unlocked lookup, between the two owner gate holds.
		accepted, err := view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.signal.Origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: f.signal.Turn}})
		close(release)
		stop, ok := accepted.Data.(ContinuationReceipt)
		if err != nil || accepted.Outcome != "accepted" || !ok {
			t.Fatalf("Session stop could not enter the released recovery interval: %+v %v", accepted, err)
		}
		result := <-done
		if result.err != nil || result.receipt.CommandID != request.CommandID {
			t.Fatalf("accepted recovery lost its receipt across stop: %+v", result)
		}
		owner := f.owner(t)
		if owner.Snapshot.State != "terminal" || owner.Snapshot.StopReason != "cancelled" || owner.Snapshot.OperationID != stop.OperationID || owner.Snapshot.Generation != stop.Generation {
			t.Fatalf("recovery replaced the winning stop: %+v", owner.Snapshot)
		}
		stops, native, reconciles := 0, 0, 0
		for _, record := range f.records(t) {
			if record.Type == ledger.TypeRuntimeContinuationReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
				if v.Submission.Operation == "owner.stop" {
					stops++
				}
				if v.Submission.Operation == "owner.reconcile" && v.Submission.ExpectedState == "stopping" {
					reconciles++
				}
			}
			if record.Type == ledger.TypeRuntimeScopedReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, record.Payload)
				if v.Submission.Operation == "turn.cancel" {
					native++
					if v.CommandID != owner.Snapshot.CancelCommandID {
						t.Fatal("recovery reminted native cancel identity")
					}
				}
			}
		}
		if stops != 1 || native != 1 || reconciles != 1 {
			t.Fatalf("recovery repeated or skipped the stop chain: stop=%d native=%d reconcile=%d", stops, native, reconciles)
		}
	})
	t.Run("cancel_fences_deferred_reconcile_close", func(t *testing.T) {
		f := continuationKilled(t, "paid_sent")
		view := f.openSession(t)
		f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			if len(d) != 0 && d[len(d)-1].Type == ledger.TypeRuntimePaidReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, d[len(d)-1].Payload)
				if v.Submission.Operation == "paid.observe" {
					return nil
				}
			}
			return d
		}
		first := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		if receipt, err := f.service.ReconcileLoop(context.Background(), first); !errors.Is(err, ErrRecoveryPending) || receipt.CommandID != first.CommandID {
			t.Fatalf("could not retain a reconciling owner with a child: %+v %v", receipt, err)
		}
		f.service.deps.beforeSubmit = nil
		status := func() session.TurnView {
			result, err := view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.signal.Origin.ContextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: f.signal.Turn}})
			if err != nil {
				t.Fatal(err)
			}
			turn, ok := result.Data.(session.TurnView)
			if !ok || turn.State != "recovery_pending" {
				t.Fatalf("recovered turn was enqueued: %+v", result)
			}
			return turn
		}
		before := status()
		if len(before.Pending) != 1 || before.Pending[0].Kind != "paid_attempt" {
			t.Fatalf("saved unresolved child missing from status: %+v", before)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var held atomic.Bool
		f.service.deps.beforeLookup = func() {
			if held.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: f.owner(t).Snapshot.Version}
		done := make(chan continuationRecoveryResult, 1)
		go func() {
			receipt, err := f.service.ReconcileLoop(context.Background(), request)
			done <- continuationRecoveryResult{receipt, err}
		}()
		<-entered
		accepted, err := view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.signal.Origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: f.signal.Turn}})
		close(release)
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("stop did not cross the released child interval: %+v %v", accepted, err)
		}
		result := <-done
		if !errors.Is(result.err, ErrContinuationStale) || result.receipt != (ContinuationReceipt{}) {
			t.Fatalf("deferred caller consumed a changed preimage: %+v", result)
		}
		owner := f.owner(t)
		if owner.Snapshot.State != "stopping" || owner.Snapshot.StopReason != "cancelled" {
			t.Fatalf("deferred caller finished under a newer stop: %+v", owner.Snapshot)
		}
		for _, record := range f.records(t) {
			if record.OperationID == request.CommandID || record.Type == ledger.TypeRuntimeTurnResult {
				t.Fatalf("fenced caller wrote an ending or consumed its ID: %+v", record)
			}
		}
		after := status()
		if len(after.Pending) != 0 || after.OwnerVersion != owner.Snapshot.Version {
			t.Fatalf("status retained a positively ended child or advanced its owner: %+v", after)
		}
		next := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
		if _, err := f.service.ReconcileLoop(context.Background(), next); err != nil {
			t.Fatal(err)
		}
		f.assertEnded(t, "cancelled", 1)
	})
}

func (f *continuationRestart) openSession(t *testing.T) session.Session {
	t.Helper()
	ctx := context.Background()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	s, _, err := OpenPaid(ctx, f.store, f.signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()})
	if err != nil {
		t.Fatal(err)
	}
	f.service = s
	ws, _, err := workspace.Open(ctx, f.store, f.signal.Root, workspace.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	var setting LoopContextSettings
	for _, route := range s.p.paidRoutes {
		if route.Enabled {
			setting = LoopContextSettings{Binding: bindingOf(f.signal.Origin), RouteID: route.RouteID, ExpectedRouteVersion: route.Version, CredentialScopeID: route.CredentialScopeID, MaxOutputTokens: 4096, DisplayTitle: "General work", TimeoutMS: 600000, Policy: TurnPolicy{Schema: 1, MaxModelCalls: 4, MaxToolCalls: 8, MaxArgumentBytes: 64 << 10, MaxResultBytes: 64 << 10, MaxDiscoveryEntries: 8, MaxSearchEntries: 4096, MaxSearchMatches: 100, MaxLiveBytes: 64 << 10}}
			break
		}
	}
	through := f.store.Durable()
	if err := s.EnableTurnLoop(ctx, ws, LoopOptions{Contexts: []LoopContextSettings{setting}}); err != nil {
		t.Fatal(err)
	}
	view, err := s.OpenSession(ctx, setting.Binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.Close)
	if f.store.Durable() != through {
		t.Fatal("enabling a Session executed a recovered turn")
	}
	return view
}

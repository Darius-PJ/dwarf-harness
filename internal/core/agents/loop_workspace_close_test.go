package agents

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func testIntegratedWorkspaceClose(t *testing.T) {
	f := newContinuationCapture(t, true, nil)
	entered, release := make(chan uuid.UUID, 1), make(chan struct{})
	var freeOnce sync.Once
	free := func() { freeOnce.Do(func() { close(release) }) }
	defer free()
	var attempts atomic.Int32
	f.service.deps.beforeFinishCommit = func(turn uuid.UUID) error {
		attempts.Add(1)
		entered <- turn
		<-release
		// PM answer 4: S3's TestWriterCloseAndServiceClose proves this real
		// refusal after Close starts. Agents has no internal close barrier.
		return &workspace.UnavailableError{Reason: "workspace_unverified"}
	}
	f.enable(t)
	_, started := f.send(t)
	if id := <-entered; id != started.TurnID {
		t.Fatal("wrong writer reached the finish barrier")
	}
	runtime := f.service.loop
	runtime.mu.Lock()
	turn := runtime.turns[started.TurnID]
	runtime.mu.Unlock()
	if turn == nil {
		t.Fatal("held finish lost its running turn")
	}
	records := foreignRecords(t, f, started.TurnID)
	if len(records.created) != 1 || len(records.observed) != 0 || len(records.merged) != 0 {
		t.Fatal("writer did not stop before its real observation")
	}
	path := filepath.Join(f.root, "scratch", records.created[0].Dir, "safe", "owned-capture.txt")
	edit := readTestFile(t, path)
	if string(edit) != "temporary capture" {
		t.Fatal("native temporary write was not present")
	}
	parent := readTestFile(t, filepath.Join(f.parent.Dir, "safe", "seed.txt"))
	type paidKey struct {
		Scope  ledger.PaidScope
		Period string
	}
	type accounting struct {
		capacity map[ledger.ScopeKey][2]Exposure
		paid     map[paidKey][2]string
	}
	scopes := make(map[ledger.ScopeKey]bool)
	paidScopes := make(map[paidKey]bool)
	paidAdmissions := 0
	through := f.store.Durable()
	if err := f.service.scan(context.Background(), 0, through, func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypeRuntimeWorkAdmitted:
				value := scopedDecode(t, ledger.DecodeRuntimeWorkAdmitted, record.Payload)
				for _, scope := range value.Scopes {
					scopes[scope] = true
				}
			case ledger.TypeRuntimeLocalUsed:
				value := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, record.Payload)
				for _, scope := range value.Scopes {
					scopes[scope] = true
				}
			case ledger.TypeRuntimePaidReserved:
				value := scopedDecode(t, ledger.DecodeRuntimePaidReserved, record.Payload)
				if value.ParentTurnID == started.TurnID {
					paidAdmissions++
					for _, scope := range value.Scopes {
						paidScopes[paidKey{scope, value.AdmissionPeriod}] = true
					}
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if paidAdmissions != 2 {
		t.Fatal("finish barrier did not follow both real model calls")
	}
	preparations := make(map[ledger.ScopeKey]Exposure)
	f.service.mu.Lock()
	hold := turn.reservation.budget.hold
	for _, scope := range turn.reservation.scopes {
		scopes[scope] = true
		preparations[scope] = Exposure{hold[0], hold[1], hold[2], hold[3]}
	}
	f.service.mu.Unlock()
	snapshot := func(service *Service) accounting {
		out := accounting{capacity: make(map[ledger.ScopeKey][2]Exposure), paid: make(map[paidKey][2]string)}
		for scope := range scopes {
			view, err := service.CapacityStatus(context.Background(), CapacityLookup{Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			out.capacity[scope] = [2]Exposure{view.Held, view.Used}
		}
		for key := range paidScopes {
			view, err := service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: key.Scope, Period: key.Period})
			if err != nil {
				t.Fatal(err)
			}
			out.paid[key] = [2]string{view.HeldMicros, view.SettledMicros}
		}
		return out
	}
	fresh := func() accounting {
		service, _, err := Open(context.Background(), f.turnFixture.store, f.root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		return snapshot(service)
	}
	before, liveBefore := fresh(), snapshot(f.service)
	if !reflect.DeepEqual(before.paid, liveBefore.paid) {
		t.Fatal("live money differs from its durable projection")
	}
	for scope, live := range liveBefore.capacity {
		durable, preparation := before.capacity[scope], preparations[scope]
		want := Exposure{durable[0].TurnSlots + preparation.TurnSlots, durable[0].LocalSlots + preparation.LocalSlots, durable[0].BufferBytes + preparation.BufferBytes, durable[0].LocalUnits + preparation.LocalUnits}
		if live[0] != want || live[1] != durable[1] {
			t.Fatal("live exposure differs from native exposure plus this turn's preparation")
		}
	}
	closeStarted, workspaceClosed := make(chan struct{}), make(chan error, 1)
	var released, early atomic.Bool
	go func() {
		close(closeStarted)
		err := f.workspace.Close()
		if !released.Load() {
			early.Store(true)
		}
		workspaceClosed <- err
	}()
	<-closeStarted
	select {
	case err := <-workspaceClosed:
		t.Fatal("workspace Close returned with its writer open", err)
	default:
	}
	released.Store(true)
	free()
	<-turn.done
	if err := <-workspaceClosed; err != nil || early.Load() {
		t.Fatal("workspace Close did not join the refused writer", err)
	}
	runtime.mu.Lock()
	state, running, terminalError := turn.state, turn.running, turn.terminalError
	runtime.mu.Unlock()
	var unavailable *workspace.UnavailableError
	if state != "recovery_pending" || running || !errors.As(terminalError, &unavailable) || unavailable.Reason != "workspace_unverified" || attempts.Load() != 1 {
		t.Fatal("workspace refusal was retried or did not preserve pending recovery", state, terminalError, attempts.Load())
	}
	afterRefusal := fresh()
	if f.store.Durable() != through || !reflect.DeepEqual(before, afterRefusal) {
		t.Fatalf("refusal changed native holds, charges or money: tail=%d/%d before=%+v after=%+v", through, f.store.Durable(), before, afterRefusal)
	}
	if live := snapshot(f.service); !reflect.DeepEqual(live, afterRefusal) {
		t.Fatal("refusal did not release exactly its own transient preparation")
	}
	if !bytes.Equal(readTestFile(t, path), edit) || !bytes.Equal(readTestFile(t, filepath.Join(f.parent.Dir, "safe", "seed.txt")), parent) {
		t.Fatal("refused observation changed retained edits")
	}
	records = foreignRecords(t, f, started.TurnID)
	if len(records.observed) != 0 || len(records.prepared) != 0 || len(records.merged) != 0 || len(records.closed) != 0 {
		t.Fatal("late workspace work followed the refusal")
	}
	if err := f.service.Close(); !errors.As(err, &unavailable) || unavailable.Reason != "workspace_unverified" {
		t.Error("shutdown lost its original finish refusal", err)
	}
	if view, err := f.service.OpenSession(context.Background(), bindingOf(f.origin)); !errors.Is(err, ErrClosed) {
		if err == nil {
			view.Close()
		}
		t.Error("failed runtime shutdown skipped the Service.Close fence", err)
	}
	select {
	case <-runtime.done:
	default:
		t.Error("failed runtime shutdown did not join run")
	}
	f.view.Close()
	reopened, report, err := Open(context.Background(), f.turnFixture.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	f.service = reopened
	if len(report.Issues) != 0 || len(report.PendingLoops) != 1 || report.PendingLoops[0].TurnID != started.TurnID || f.store.Durable() != through || !reflect.DeepEqual(before, snapshot(reopened)) {
		t.Fatal("Open changed or lost the pending refusal", report)
	}
	owner, err := reopened.currentContinuation(context.Background(), f.origin, started.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: started.TurnID, ExpectedOwnerVersion: owner.Snapshot.Version}
	first, err := reopened.ReconcileLoop(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	after := f.store.Durable()
	reconciled := snapshot(reopened)
	if !reflect.DeepEqual(reconciled, fresh()) || !reflect.DeepEqual(reconciled.paid, before.paid) {
		t.Fatal("first reconciliation changed money or retained a transient hold")
	}
	repeated, err := reopened.ReconcileLoop(context.Background(), request)
	if err != nil || !reflect.DeepEqual(first, repeated) || f.store.Durable() != after {
		t.Fatal("bookkeeping retry performed more work", err)
	}
	if !reflect.DeepEqual(reconciled, snapshot(reopened)) || !reflect.DeepEqual(reconciled, fresh()) {
		t.Fatal("second reconciliation released a hold twice or acquired a new one")
	}
	records = foreignRecords(t, f, started.TurnID)
	if len(records.observed) != 0 || len(records.prepared) != 0 || len(records.merged) != 0 || len(records.closed) != 0 || attempts.Load() != 1 || !bytes.Equal(readTestFile(t, path), edit) || !bytes.Equal(readTestFile(t, filepath.Join(f.parent.Dir, "safe", "seed.txt")), parent) {
		t.Fatal("recovery observed, merged, retried or lost the retained edit")
	}
	var endings []ledger.RuntimeTurnResult
	if err := reopened.scan(context.Background(), through, after, func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			if record.Type == ledger.TypeRuntimeTurnResult && record.TurnID == started.TurnID {
				endings = append(endings, scopedDecode(t, ledger.DecodeRuntimeTurnResult, record.Payload))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(endings) != 1 || endings[0].Outcome != "interrupted" || endings[0].ModelCalls != 2 || endings[0].ToolDispatches != 1 {
		t.Fatalf("unexpected recovery ending; report before any product change: %+v", endings)
	}
	for scope, exposure := range reconciled.capacity {
		if exposure[1] != before.capacity[scope][1] || exposure[0].TurnSlots != 0 || exposure[0].LocalSlots != 0 {
			t.Fatal("recovery lost charges or retained a completed native hold")
		}
	}
}

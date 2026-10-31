package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func testLoopPhysicalRelease(t *testing.T, cancelBeforeSend bool) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var f *turnFixture
	var holder Binding
	var held atomic.Int64
	var claimed, lowered atomic.Bool
	var posts atomic.Int32
	f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Physical capacity became available."))
	}, func(deps *serviceDeps) {
		deps.beforeRead = func(binding Binding, scope string) error {
			if binding != holder || scope != "context" || !claimed.CompareAndSwap(false, true) {
				return nil
			}
			close(entered)
			<-release
			return ErrDenied // Real read entrance abandons its reservation, without a receipt.
		}
		deps.beforePublish = func(_ Binding, slot string) error {
			if slot != "paid_input" || !lowered.CompareAndSwap(false, true) {
				return nil
			}
			scope := ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}
			f.service.mu.Lock()
			version := f.service.p.book.accounts[scope].version
			physical := f.service.p.book.preparing[scope][2]
			f.service.mu.Unlock()
			capacity := paidPhysicalCapacity()
			capacity.MaxOperationBytes = 4 << 20
			capacity.BufferBytes = physical + (96 << 20) - held.Load()
			_, err := f.service.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(version), Scope: scope, Limits: capacity})
			return err
		}
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	holder = scopedWorkContext(t, f.service, bindingOf(f.origin), 62000, 62001)
	grant := uuid.NewV7()
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grant, Grantee: holder, Operations: []string{"private.read", "private.write"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(holder), Scope: "context", Slots: []string{"memory"}}}, DataClass: "private", Recipients: []Binding{holder}, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: holder, Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := f.service.BindAgent(context.Background(), holder)
	if err != nil {
		t.Fatal(err)
	}
	written, err := agent.WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: grant, Scope: "context", ExpectedVersion: view.Revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("Synthetic reserved body.")}}})
	if err != nil {
		t.Fatal(err)
	}
	command := uuid.NewV7()
	readDone := make(chan error, 1)
	go func() {
		_, err := agent.ReadPrivate(context.Background(), ScopedPrivateRead{CommandID: command, GrantID: grant, Scope: "context", Slot: "memory", Revision: uint64(written.Version), MaxBytes: min(f.service.limits.MaxReadBytes, 4<<20)})
		readDone <- err
	}()
	select {
	case <-entered:
	case err := <-readDone:
		t.Fatalf("holder was not admitted: %v", err)
	}
	f.service.mu.Lock()
	held.Store(f.service.p.book.preparing[ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}][2])
	f.service.mu.Unlock()
	f.enable(t)
	_, start := f.send(t)
	waitLoopState(t, f.view, f.origin.ContextID, start.TurnID, "waiting_resource")
	if posts.Load() != 0 {
		t.Fatal("model dispatched despite held physical capacity")
	}
	if cancelBeforeSend {
		_, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: start.TurnID}})
		if err != nil {
			t.Fatal(err)
		}
		result, _ := f.finished(t, start.TurnID)
		if result.Outcome != "cancelled" || result.ModelCalls != 0 || posts.Load() != 0 {
			t.Fatal("reserved cancellation sent or consumed a model call")
		}
		admissions := loopPaidAdmissions(t, f.service)
		if len(admissions) != 1 {
			t.Fatal("resource retry prepared a second attempt")
		}
		view, err := f.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: admissions[0].AttemptID})
		if err != nil || view.Intent != nil || view.Result == nil || view.Result.Kind != "released_unsent" || view.HeldMicros != "0" || view.SettledMicros != "0" {
			t.Fatal("reserved cancellation failed to release its never-sent hold")
		}
	}
	close(release)
	if err := <-readDone; !errors.Is(err, ErrDenied) {
		t.Fatal("holder did not abandon its native read")
	}
	if !cancelBeforeSend {
		result, _ := f.finished(t, start.TurnID)
		if result.Outcome != "finished" || result.ModelCalls != 1 || posts.Load() != 1 {
			t.Fatal("non-durable reservation release did not wake exactly the waiting model")
		}
	}
	f.service.mu.Lock()
	match, err := f.service.findCommand(context.Background(), command, 0, f.service.p.through)
	f.service.mu.Unlock()
	if err != nil || match.found {
		t.Fatal("the wakeup required a fabricated holder receipt")
	}
}

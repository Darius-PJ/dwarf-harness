package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func eligibilityIdle(t testing.TB, f *turnFixture, turn uuid.UUID) session.TurnView {
	t.Helper()
	for {
		r := f.service.loop
		r.mu.Lock()
		live := r.turns[turn]
		ready := live != nil && !live.running && live.eligibilityHeld
		changed := r.changed
		r.mu.Unlock()
		if live == nil {
			t.Fatalf("held turn ended instead of waiting: paid intents=%d tool effects=%d", eligibilityCount(t, f.service, 0, ledger.TypeRuntimePaidDispatchIntent), eligibilityCount(t, f.service, 0, ledger.TypeRuntimeToolResult))
		}
		if ready {
			result, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: turn}})
			if err != nil {
				t.Fatal(err)
			}
			view := result.Data.(session.TurnView)
			if view.State != "eligibility_held" || view.Eligibility == nil {
				t.Fatalf("held view erased durable owner: %+v", view)
			}
			return view
		}
		select {
		case <-changed:
		case <-t.Context().Done():
			t.Fatal("held worker failed to quiesce")
		}
	}
}

func TestEligibilityRace(t *testing.T) {
	t.Parallel()
	t.Run("hold_wins_unlocked_cold_lookup", func(t *testing.T) {
		t.Parallel()
		entered, resume := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-resume:
			default:
				close(resume)
			}
		}()
		var armed atomic.Bool
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{beforeLookup: func() {
			if armed.CompareAndSwap(true, false) {
				close(entered)
				<-resume
			}
		}}, scopedCapacity())
		start := TurnAdmission{CommandID: uuid.NewV7(), TurnID: uuid.NewV7(), GrantID: grant.GrantID}
		armed.Store(true)
		done := make(chan error, 1)
		go func() { _, err := agent.StartTurn(context.Background(), start); done <- err }()
		<-entered
		held := holdEligibility(t, f.service, binding.AgentID)
		through := f.store.Durable()
		close(resume)
		requireEligibilityHeld(t, <-done)
		if f.store.Durable() != through {
			t.Fatal("cold lookup published stale-prefix admission")
		}
		releaseEligibility(t, f.service, binding.AgentID, held)
		if _, err := agent.StartTurn(context.Background(), start); err != nil {
			t.Fatal("refused command became durable", err)
		}
	})
	t.Run("native_preparation_hold_has_no_fake_tool_result", func(t *testing.T) {
		t.Parallel()
		entered, resume := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-resume:
			default:
				close(resume)
			}
		}()
		var claimed atomic.Bool
		var posts atomic.Int32
		var arguments string
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if posts.Add(1) == 1 {
				_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
			} else {
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
			}
		}, func(deps *serviceDeps) {
			deps.afterScopedClaim = func(sub ledger.ScopedSubmission) {
				if sub.Operation == "private.write" && len(sub.Target.Slots) == 1 && sub.Target.Slots[0] == "memory" && claimed.CompareAndSwap(false, true) {
					close(entered)
					<-resume
				}
			}
		})
		before, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"once after release"}`, before.Revision)
		f.enable(t)
		_, sent := f.send(t)
		<-entered
		held := holdEligibility(t, f.service, f.origin.AgentID)
		through := f.store.Durable()
		close(resume)
		view := eligibilityIdle(t, f, sent.TurnID)
		if view.SavedPhase != "tool_pending" || view.WaitReason != "none" {
			t.Fatalf("hold invented a wait or owner phase: %+v", view)
		}
		if posts.Load() != 1 || eligibilityCount(t, f.service, 0, ledger.TypeRuntimeToolResult) != 0 || f.store.Durable() != through {
			t.Fatal("held native preparation appended a fake result or other work")
		}
		snapshot, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
		if err != nil || snapshot.Revision != before.Revision {
			t.Fatal("held native preparation mutated memory", err)
		}
		for range 2 {
			eligibilityIdle(t, f, sent.TurnID)
		}
		if f.store.Durable() != through {
			t.Fatal("repeated held inspection advanced owner")
		}
		releaseEligibility(t, f.service, f.origin.AgentID, held)
		result, _ := f.finished(t, sent.TurnID)
		if result.Outcome != "finished" || posts.Load() != 2 || result.ToolDispatches != 1 || eligibilityCount(t, f.service, through, ledger.TypeRuntimeToolResult) != 1 {
			t.Fatal("released native effect did not proceed exactly once")
		}
		snapshot, err = f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		body, err := f.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{Binding: bindingOf(f.origin), Scope: "context"}, Slot: "memory", Revision: snapshot.Revision, MaxBytes: 1024})
		if err != nil || string(body.Bytes) != "once after release" {
			t.Fatal("released result did not reflect native effect", err)
		}
	})
	t.Run("result_during_hold", func(t *testing.T) {
		t.Parallel()
		entered, response := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-response:
			default:
				close(response)
			}
		}()
		var posts atomic.Int32
		var arguments string
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if posts.Add(1) == 1 {
				close(entered)
				<-response
				_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
			} else {
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
			}
		}, nil)
		before, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"after settlement and release"}`, before.Revision)
		f.enable(t)
		_, sent := f.send(t)
		<-entered
		held := holdEligibility(t, f.service, f.origin.AgentID)
		close(response)
		eligibilityIdle(t, f, sent.TurnID)
		if posts.Load() != 1 || eligibilityCount(t, f.service, 0, ledger.TypeRuntimePaidResult) != 1 || eligibilityCount(t, f.service, 0, ledger.TypeRuntimeToolStarted) != 0 || eligibilityCount(t, f.service, 0, ledger.TypeRuntimeToolResult) != 0 {
			t.Fatal("pre-hold result either failed settlement or authorized a next dispatch")
		}
		through := f.store.Durable()
		releaseEligibility(t, f.service, f.origin.AgentID, held)
		result, _ := f.finished(t, sent.TurnID)
		if result.Outcome != "finished" || posts.Load() != 2 || eligibilityCount(t, f.service, through, ledger.TypeRuntimeToolStarted) != 1 || eligibilityCount(t, f.service, through, ledger.TypeRuntimeToolResult) != 1 {
			t.Fatal("release did not resume one saved next quantum")
		}
	})
	for _, stop := range []bool{false, true} {
		name := "hold_after_selection_release"
		if stop {
			name = "hold_after_selection_cancel"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			prepared, proceed := make(chan uuid.UUID, 1), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(proceed) })
			var once sync.Once
			var posts atomic.Int32
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				model, _ := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "finished after release"))
			}, func(deps *serviceDeps) {
				deps.afterLoopPrepare = func(turn uuid.UUID) {
					once.Do(func() {
						prepared <- turn
						<-proceed
					})
				}
			})
			t.Cleanup(unblock)
			f.service.paid.limits.MaxActivePaid = 1
			f.enable(t)
			through := f.store.Durable()
			_, sent := f.send(t)
			if turn := <-prepared; turn != sent.TurnID {
				t.Fatal("prepared a different turn")
			}
			f.service.mu.Lock()
			claimed := len(f.service.loop.paidTokens)
			f.service.mu.Unlock()
			if claimed != 1 {
				t.Fatalf("preparation seam missed the paid claim: tokens=%d", claimed)
			}
			held := holdEligibility(t, f.service, f.origin.AgentID)
			runtime := f.service.loop
			runtime.mu.Lock()
			changed := runtime.changed
			runtime.mu.Unlock()
			unblock()
			<-changed
			view := eligibilityIdle(t, f, sent.TurnID)
			f.service.mu.Lock()
			tokens := len(runtime.paidTokens)
			f.service.mu.Unlock()
			if tokens != 0 {
				t.Errorf("held selected turn retained %d paid exchange token(s)", tokens)
			}
			if posts.Load() != 0 || eligibilityPaidCount(t, f.service, through) != 0 || view.WaitReason != "none" {
				t.Fatalf("held selected turn dispatched or parked: posts=%d view=%+v", posts.Load(), view)
			}
			// A red token-leak run cancels instead of waiting forever for a
			// release to reacquire its own sole slot. No test clears the token.
			if stop || tokens != 0 {
				if _, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: sent.TurnID}}); err != nil {
					t.Fatal(err)
				}
				result, _ := f.finished(t, sent.TurnID)
				f.service.mu.Lock()
				remaining := len(runtime.paidTokens)
				f.service.mu.Unlock()
				if result.Outcome != "cancelled" || posts.Load() != 0 || eligibilityPaidCount(t, f.service, through) != 0 || remaining != 0 {
					t.Errorf("held selected cancel: outcome=%q posts=%d paid tokens=%d", result.Outcome, posts.Load(), remaining)
				}
				return
			}
			releaseEligibility(t, f.service, f.origin.AgentID, held)
			result, _ := f.finished(t, sent.TurnID)
			if result.Outcome != "finished" || posts.Load() != 1 || eligibilityCount(t, f.service, through, ledger.TypeRuntimePaidDispatchIntent) != 1 || eligibilityCount(t, f.service, through, ledger.TypeRuntimePaidResult) != 1 {
				t.Fatalf("released selection did not execute once: outcome=%q posts=%d", result.Outcome, posts.Load())
			}
		})
	}
}

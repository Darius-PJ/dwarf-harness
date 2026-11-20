package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func eligibilityCount(t testing.TB, s *Service, after uint64, typ string) int {
	t.Helper()
	count := 0
	if err := s.scan(context.Background(), after, s.store.Durable(), func(c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type == typ {
				count++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestEligibilityDispatchBoundaries(t *testing.T) {
	t.Parallel()
	t.Run("native_paid_held", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		var posts atomic.Int32
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "bounded answer"))
		})
		f := newLivePaidFixture(t, paidServerDeps(server, nil))
		request := f.prepare(t)
		agent := scopedBound(t, f.service, bindingOf(f.origin))
		held := holdEligibility(t, f.service, f.origin.AgentID)
		through := f.store.Durable()
		_, _, err := agent.AdmitPaid(ctx, request)
		requireEligibilityHeld(t, err)
		if f.store.Durable() != through || posts.Load() != 0 {
			t.Fatal("held paid admission created exposure or POST")
		}
		releaseEligibility(t, f.service, f.origin.AgentID, held)
		original, _ := f.admit(t, request)
		held = holdEligibility(t, f.service, f.origin.AgentID)
		through = f.store.Durable()
		budget := f.budget(t, "")
		retry, _, err := agent.AdmitPaid(ctx, request)
		if err != nil || !reflect.DeepEqual(original.RuntimePaidReceipt, retry.RuntimePaidReceipt) || original.Sequence != retry.Sequence || original.EffectSequence != retry.EffectSequence || retry.ThroughSequence != through || f.store.Durable() != through {
			t.Fatal("admitted same-ID retry was not receipt-only under hold", err)
		}
		dispatch := PaidDispatch{CommandID: uuid.NewV7(), AttemptID: request.AttemptID, ExpectedVersion: 1}
		_, err = f.service.DispatchPaid(ctx, dispatch)
		after := f.budget(t, "")
		if f.store.Durable() != through || posts.Load() != 0 || after.HeldMicros != budget.HeldMicros || after.SettledMicros != budget.SettledMicros || eligibilityCount(t, f.service, through, ledger.TypeRuntimePaidDispatchIntent) != 0 {
			t.Fatalf("ownerless held paid dispatch sent work: posts=%d intents=%d held=%s/%s", posts.Load(), eligibilityCount(t, f.service, through, ledger.TypeRuntimePaidDispatchIntent), after.HeldMicros, budget.HeldMicros)
		}
		requireEligibilityHeld(t, err)
		releaseEligibility(t, f.service, f.origin.AgentID, held)
		out, err := f.service.DispatchPaid(ctx, dispatch)
		if err != nil || out.ObservedPaidCall == nil || posts.Load() != 1 {
			t.Fatal("explicit released dispatch did not execute once", err)
		}
		if _, err := f.service.RecordPaidObservation(ctx, PaidObservation{CommandID: uuid.NewV7(), AttemptID: request.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("native_local_held", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, agent, start, admit := scopedLocalFixture(t, DefaultLimits())
		if _, err := agent.StartTurn(ctx, start); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.AdmitLocal(ctx, admit); err != nil {
			t.Fatal(err)
		}
		held := holdEligibility(t, f.service, agent.ids.agent)
		through := f.store.Durable()
		before, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "grant", ID: admit.GrantID}})
		if err != nil {
			t.Fatal(err)
		}
		next := admit
		next.CommandID, next.WorkID = uuid.NewV7(), uuid.NewV7()
		_, err = agent.AdmitLocal(ctx, next)
		requireEligibilityHeld(t, err)
		dispatch := LocalDispatch{CommandID: uuid.NewV7(), WorkID: admit.WorkID, ExpectedVersion: 1, ArgumentsSHA256: admit.ArgumentsSHA256}
		_, err = f.service.BeginLocal(ctx, dispatch)
		requireEligibilityHeld(t, err)
		after, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "grant", ID: admit.GrantID}})
		if err != nil || !reflect.DeepEqual(before, after) || f.store.Durable() != through || eligibilityCount(t, f.service, through, ledger.TypeRuntimeWorkDispatched) != 0 {
			t.Fatal("ownerless held local dispatch consumed a unit", err)
		}
		releaseEligibility(t, f.service, agent.ids.agent, held)
		if _, err := f.service.BeginLocal(ctx, dispatch); err != nil {
			t.Fatal("refused local command was consumed", err)
		}
		if eligibilityCount(t, f.service, through, ledger.TypeRuntimeWorkDispatched) != 1 {
			t.Fatal("released local dispatch repeated")
		}
		holdEligibility(t, f.service, agent.ids.agent)
		if _, err := f.service.EndLocal(ctx, LocalEnding{CommandID: uuid.NewV7(), WorkID: admit.WorkID, ExpectedVersion: 2, Outcome: "failed"}); err != nil {
			t.Fatal("hold fenced local ending", err)
		}
		if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: uuid.NewV7(), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
			t.Fatal("hold fenced native ending", err)
		}
	})
	t.Run("atomic_effect_without_owner", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		held := holdEligibility(t, f.service, binding.AgentID)
		through := f.store.Durable()
		write := ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("effect")}}}
		_, err := agent.WritePrivate(ctx, write)
		requireEligibilityHeld(t, err)
		if _, err := agent.Snapshot(ctx, ScopedSnapshotLookup{GrantID: grant.GrantID, Scope: "context"}); err != nil {
			t.Fatal("hold fenced read", err)
		}
		if f.store.Durable() != through {
			t.Fatal("unpromised atomic effect bypassed hold")
		}
		releaseEligibility(t, f.service, binding.AgentID, held)
		if _, err := agent.WritePrivate(ctx, write); err != nil {
			t.Fatal("explicit released write failed", err)
		}
	})
	t.Run("session_send_is_not_retained", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
		}, nil)
		f.enable(t)
		held := holdEligibility(t, f.service, f.origin.AgentID)
		through := f.store.Durable()
		command := turnMessage(f.origin.ContextID, "not retained")
		result, err := f.view.Do(context.Background(), command)
		requireEligibilityHeld(t, err)
		if result.Outcome != "held" || result.TurnID != (uuid.UUID{}) || result.ReceiptSequence != 0 || f.store.Durable() != through || posts.Load() != 0 {
			t.Fatal("held Session consumed or retained request")
		}
		releaseEligibility(t, f.service, f.origin.AgentID, held)
		if posts.Load() != 0 || eligibilityCount(t, f.service, through, ledger.TypeRuntimeTurnBound) != 0 {
			t.Fatal("release replayed refused send")
		}
		if _, err := f.view.Do(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		turn := command.Arguments.(session.MessageSend).TurnID
		resultTurn, _ := f.finished(t, turn)
		if resultTurn.Outcome != "finished" || posts.Load() != 1 {
			t.Fatal("explicit released send did not execute once")
		}
	})
	t.Run("finishing_while_held", func(t *testing.T) {
		t.Parallel()
		entered := make(chan struct{})
		var posts atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
		}, nil)
		f.enable(t)
		command, _ := f.send(t)
		<-entered
		turn := command.Arguments.(session.MessageSend).TurnID
		holdEligibility(t, f.service, f.origin.AgentID)
		if _, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}}); err != nil {
			t.Fatal(err)
		}
		result, _ := f.finished(t, turn)
		if result.Outcome != "cancelled" || posts.Load() != 1 {
			t.Fatal("held cancellation dispatched or failed to finish")
		}
		view, err := f.service.EligibilityStatus(context.Background(), f.origin.AgentID)
		if err != nil || view.State != "ordinary_work_ineligible" {
			t.Fatal("finishing released the gate", err)
		}
	})
	t.Run("held_recovery_pending_shutdown", func(t *testing.T) {
		t.Parallel()
		var service *Service
		var held EligibilityReceipt
		var posts, refused atomic.Int32
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			t.Error("held turn dispatched before shutdown")
			http.Error(w, "unexpected dispatch", http.StatusInternalServerError)
		}, func(deps *serviceDeps) {
			deps.afterLoopCommit = func(receipt ledger.RuntimeLoopReceipt) {
				if receipt.Submission.Operation == "loop.bind" {
					// The durable binding exists, but owner.initialize and the
					// first scheduler admission have not run yet.
					held = holdEligibility(t, service, receipt.Submission.Origin.AgentID)
				}
			}
			deps.beforeSubmit = func(drafts []ledger.Draft) []ledger.Draft {
				if len(drafts) == 3 && drafts[2].Type == ledger.TypeRuntimeContinuationReceipt {
					receipt := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, drafts[2].Payload)
					if receipt.Submission.Operation == "owner.stop" && receipt.Submission.Arguments.StopReason == "shutdown" {
						refused.Add(1)
						return nil
					}
				}
				return drafts
			}
		})
		service = f.service
		f.enable(t)
		_, sent := f.send(t)
		eligibilityIdle(t, f, sent.TurnID)
		runtime := service.loop
		runtime.mu.Lock()
		turn := runtime.turns[sent.TurnID]
		unspent := turn != nil && turn.paid == nil && turn.eligibilityHeld
		runtime.mu.Unlock()
		if !unspent {
			t.Fatal("fixture did not hold the real turn before its first paid call")
		}
		before := f.store.Durable()
		closed := make(chan error, 1)
		defer func() {
			reopened, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			f.service = reopened
			if len(report.PendingLoops) != 1 || report.PendingLoops[0].TurnID != sent.TurnID {
				t.Fatal("failed shutdown did not leave exactly one recoverable turn")
			}
		}()
		go func() { closed <- service.Close() }()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case err := <-closed:
			if !errors.Is(err, ErrInvalid) {
				t.Fatal("shutdown did not return its refused stop", err)
			}
		case <-timer.C:
			t.Error("held recovery_pending shutdown did not return within the fixed bound")
			// Restore eligibility through the public owner entrance so the
			// broken scheduler can discard its failed turn and cleanup joins.
			releaseEligibility(t, service, f.origin.AgentID, held)
			<-closed
			return
		}
		for name, done := range map[string]<-chan struct{}{"runtime": runtime.done, "turn": turn.done} {
			select {
			case <-done:
			default:
				t.Fatalf("held failed shutdown did not join %s", name)
			}
		}
		if refused.Load() != 1 || posts.Load() != 0 || f.store.Durable() != before || turn.state != "recovery_pending" || !errors.Is(turn.terminalError, ErrInvalid) {
			t.Fatal("held failed shutdown dispatched, appended, or lost its recovery error")
		}
	})
}

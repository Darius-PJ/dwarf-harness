package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func TestTurnLoopCancelResultRace(t *testing.T) {
	t.Run("accepted_cancel_to_native_end", func(t *testing.T) {
		t.Parallel()
		gates := map[string]chan struct{}{"held": make(chan struct{})}
		entered := make(chan string, 1)
		f := newTurnFixture(t, "anthropic", finalScript(t, []string{"held"}, gates, entered), nil)
		f.enable(t)
		command := turnMessage(f.origin.ContextID, "held request")
		if _, err := f.view.Do(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		turn := command.Arguments.(session.MessageSend).TurnID
		<-entered
		r := f.service.loop
		r.mu.Lock()
		live := r.turns[turn]
		r.mu.Unlock()
		if live == nil {
			t.Fatal("turn vanished before cancellation")
		}
		t.Run("public_core_actor_refused", func(t *testing.T) {
			before := f.store.Durable()
			forged := ledger.ScopedSubmission{Schema: 2, Actor: "core", Origin: f.origin, Operation: "turn.cancel", GrantID: f.privateGrant, Arguments: ledger.WorkEndArguments{WorkID: turn, Outcome: "cancelled"}}
			_, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: forged})
			if !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
				t.Fatalf("public Session accepted a Core envelope: %v", err)
			}
		})
		cancel := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}}
		accepted, err := f.view.Do(context.Background(), cancel)
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("cancel stop was not acknowledged: %+v %v", accepted, err)
		}
		<-live.done
		if live.terminalError != nil {
			t.Fatalf("accepted cancel could not reach native ending: %v", live.terminalError)
		}
		result, _ := f.finished(t, turn)
		if result.Outcome != "cancelled" || result.FinalMessageObject != nil {
			t.Fatalf("wrong durable cancel result: %+v", result)
		}
		var stop ledger.RuntimeContinuationChanged
		var native ledger.RuntimeScopedReceipt
		stops, endings, closes := 0, 0, 0
		if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
			for _, record := range c.Records {
				if record.TurnID != turn {
					continue
				}
				switch record.Type {
				case ledger.TypeRuntimeContinuationReceipt:
					v, err := ledger.DecodeRuntimeContinuationReceipt(record.Payload)
					if err != nil {
						return err
					}
					if v.Submission.Operation == "owner.stop" {
						stops++
						stop = continuationChange(v.Submission)
					}
					if v.Submission.Operation == "owner.close" {
						closes++
					}
				case ledger.TypeRuntimeScopedReceipt:
					v, err := ledger.DecodeRuntimeScopedReceipt(record.Payload)
					if err != nil {
						return err
					}
					if v.Submission.Operation == "turn.cancel" {
						endings++
						native = v
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if stops != 1 || endings != 1 || closes != 1 || native.CommandID != stop.CancelCommandID || native.CommandID == cancel.CommandID || native.Submission.Actor != "core" || native.Submission.GrantID != f.privateGrant {
			t.Fatalf("stop/native ownership was not preserved once: stops=%d native=%d close=%d stop=%+v native=%+v", stops, endings, closes, stop, native)
		}
		before := f.store.Durable()
		if retry, err := f.view.Do(context.Background(), cancel); err != nil || !reflect.DeepEqual(retry, accepted) || f.store.Durable() != before {
			t.Fatalf("closed-turn stop retry changed its receipt: %+v %v", retry, err)
		}
	})
	testContinuationCancelDuringRecovery(t)
	testContinuationStopCapture(t)
	testContinuationStopClaim(t)
	testContinuationCallbackFences(t)
}

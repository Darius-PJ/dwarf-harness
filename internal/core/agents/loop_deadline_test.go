package agents

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

type loopCancelReply struct {
	command session.Command
	result  session.CommandResult
	err     error
}

func testLoopDeadlinePrecedence(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"deadline", "authority_lost", "cancelled", "shutdown"} {
		t.Run(kind+"_before_exhaustion", func(t *testing.T) {
			t.Parallel()
			var clock atomic.Int64
			var posts atomic.Int32
			var f *turnFixture
			var host *Service
			cancelled := make(chan loopCancelReply, 1)
			closed := make(chan error, 1)
			f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				_, _ = turnRequest(t, r)
				posts.Add(1)
				if kind == "authority_lost" {
					f.revoke(t, f.privateGrant)
				}
				clock.Add(int64(11 * time.Minute))
				switch kind {
				case "cancelled":
					host.loop.mu.Lock()
					var turn uuid.UUID
					for id := range host.loop.turns {
						turn = id
					}
					host.loop.mu.Unlock()
					go func() {
						command := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}}
						result, err := f.view.Do(context.Background(), command)
						cancelled <- loopCancelReply{command, result, err}
					}()
				case "shutdown":
					// The owning host's Close must drain this running exchange
					// to a durable ending before it returns.
					go func() { closed <- host.Close() }()
				default:
					// A real owner commit wakes the host after the guarded clock
					// advances; no polling, sleep, or test-only scheduler call.
					scope := ledger.ScopeKey{Kind: "colony"}
					host.mu.Lock()
					version := host.p.book.accounts[scope].version
					host.mu.Unlock()
					capacity := paidPhysicalCapacity()
					capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
					if _, err := host.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(version), Scope: scope, Limits: capacity}); err != nil {
						t.Error("clock wake commit failed")
					}
				}
				// Deliberately no response: the single host deadline, cancellation
				// or shutdown must terminate the real exchange, not just a future
				// model call.
				<-r.Context().Done()
			}, func(deps *serviceDeps) {
				deps.reviewClock = func() (int64, error) { return time.Now().UnixNano() + clock.Load(), nil }
			})
			host = f.service
			f.setting.Policy.MaxModelCalls = 1
			f.enable(t)
			_, start := f.send(t)
			if kind == "shutdown" {
				testLoopShutdownEnding(t, f, start.TurnID, closed, &posts)
				return
			}
			result, _ := f.finished(t, start.TurnID)
			outcome := "failed"
			if kind == "cancelled" {
				outcome = "cancelled"
			}
			if result.Outcome != outcome || result.Reason != kind || result.ModelCalls != 1 || posts.Load() != 1 {
				t.Fatalf("stop precedence outcome=%s reason=%s calls=%d posts=%d", result.Outcome, result.Reason, result.ModelCalls, posts.Load())
			}
			if kind != "cancelled" {
				return
			}
			reply := <-cancelled
			if reply.err != nil || reply.result.Outcome != "applied" {
				t.Fatal("cancellation did not acknowledge its recorded native ending")
			}
			// The live acknowledgement and a historical retry of the same
			// command must both name the native cancellation's actual records.
			host.mu.Lock()
			effect, effectFound, effectErr := host.sourceRecord(context.Background(), int64(reply.result.EffectSequence), host.p.through)
			receipt, receiptFound, receiptErr := host.sourceRecord(context.Background(), int64(reply.result.ReceiptSequence), host.p.through)
			host.mu.Unlock()
			if effectErr != nil || receiptErr != nil || !effectFound || !receiptFound || effect.Type != ledger.TypeRuntimeWorkEnded || receipt.Type != ledger.TypeRuntimeScopedReceipt {
				t.Fatal("live cancellation acknowledgement did not name its native effect and receipt")
			}
			ended, err := ledger.DecodeRuntimeWorkEnded(effect.Payload)
			if err != nil || ended.WorkID != start.TurnID || ended.Outcome != "cancelled" || effect.OperationID != reply.command.CommandID {
				t.Fatal("cancellation effect sequence selected another record")
			}
			retry, err := f.view.Do(context.Background(), reply.command)
			if err != nil || retry.Outcome != "applied" || retry.EffectSequence != reply.result.EffectSequence || retry.ReceiptSequence != reply.result.ReceiptSequence {
				t.Fatal("historical cancellation retry disagreed with its live acknowledgement")
			}
		})
	}
}

// testLoopShutdownEnding observes the turn only through a reopened service:
// Close has returned, so no live view, runtime or exchange can remain.
func testLoopShutdownEnding(t *testing.T, f *turnFixture, turn uuid.UUID, closed <-chan error, posts *atomic.Int32) {
	t.Helper()
	if err := <-closed; err != nil {
		t.Fatal("host shutdown failed")
	}
	reopened, report, err := openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	f.service = reopened
	// Only the loop is drained: the sent attempt's unknown ending keeps its
	// hold, so the money stays pending rather than released or settled.
	if len(report.PendingLoops) != 0 || len(report.PendingPaid) != 1 {
		t.Fatal("host shutdown left the running turn recovery_pending or dropped the sent attempt's hold")
	}
	var ending *ledger.RuntimeTurnResult
	var paid []string
	reopened.mu.Lock()
	err = reopened.scan(context.Background(), 0, reopened.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.TurnID != turn {
				continue
			}
			switch record.Type {
			case ledger.TypeRuntimeTurnResult:
				value, err := ledger.DecodeRuntimeTurnResult(record.Payload)
				if err != nil {
					return err
				}
				ending = &value
			case ledger.TypeRuntimePaidResult:
				value, err := ledger.DecodeRuntimePaidResult(record.Payload)
				if err != nil {
					return err
				}
				paid = append(paid, value.Kind)
			}
		}
		return nil
	})
	var native ledger.Record
	var nativeFound bool
	if err == nil && ending != nil {
		native, nativeFound, err = reopened.sourceRecord(context.Background(), ending.EndingSequence, reopened.p.through)
	}
	reopened.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if ending == nil || ending.Outcome != "interrupted" || ending.Reason != "shutdown" || ending.ModelCalls != 1 || ending.FinalMessageObject != nil || posts.Load() != 1 {
		t.Fatal("shutdown did not outrank the expired deadline and exhausted model counter with an interrupted ending")
	}
	if !nativeFound || native.Type != ledger.TypeRuntimeWorkEnded {
		t.Fatal("interrupted loop ending did not name its native turn ending")
	}
	if value, err := ledger.DecodeRuntimeWorkEnded(native.Payload); err != nil || value.WorkID != turn || value.Outcome != "interrupted" {
		t.Fatal("shutdown released or cancelled the native turn instead of interrupting it")
	}
	// The request was sent, so only an unknown ending that keeps the hold is
	// honest; shutdown never settles or releases a sent attempt.
	if len(paid) != 1 || paid[0] != "unknown" {
		t.Fatal("shutdown fabricated evidence for the sent paid attempt")
	}
}

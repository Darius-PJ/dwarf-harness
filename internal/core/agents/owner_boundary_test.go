package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func continuationReadyFixture(t *testing.T) (*serviceFixture, continuationState) {
	t.Helper()
	commits, at := continuationFrozen(t)
	f, _ := scopedGoldenFixture(t, "16", at["owner.advance"])
	s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil || len(report.Issues) != 0 {
		t.Fatal("invalid ready prefix", report, err)
	}
	f.service = s
	_, _, _, _, _, _, next, err := decodeFamily(draftsOf(commits[at["owner.advance"]]), f.store.Durable()+1)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.currentContinuation(context.Background(), next.receipt.Submission.Origin, next.change.TurnID)
	if err != nil || owner.Snapshot.State != "ready" {
		t.Fatalf("ready fixture: %+v %v", owner, err)
	}
	return f, owner
}

func continuationModelOpening(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
	a.State, a.OperationID, a.Generation = "model_pending", command, owner.Snapshot.Generation+1
	a.Pending = []ledger.ContinuationPending{{Kind: "paid_attempt", ID: uuid.NewV7(), CommandID: uuid.NewV7(), ExpectedVersion: 1}}
	return nil
}

func testContinuationCallbackFences(t *testing.T) {
	t.Run("callback_identity_fenced", func(t *testing.T) {
		t.Parallel()
		f, owner := continuationReadyFixture(t)
		for _, field := range []string{"agent", "session", "context", "operation", "generation", "phase", "version", "result"} {
			t.Run(field, func(t *testing.T) {
				token := owner.token(uuid.UUID{})
				switch field {
				case "agent":
					token.origin.AgentID = uuid.NewV7()
				case "session":
					token.origin.SessionID = uuid.NewV7()
				case "context":
					token.origin.ContextID = uuid.NewV7()
				case "operation":
					token.operation = uuid.NewV7()
				case "generation":
					token.generation++
				case "phase":
					token.phase = "model_pending"
				case "version":
					token.version++
				case "result":
					token.result = uuid.NewV7()
				}
				before := f.store.Durable()
				_, err := f.service.updateContinuation(context.Background(), owner.Origin, owner.Snapshot.TurnID, "owner.advance", &token, continuationModelOpening)
				if !errors.Is(err, ErrContinuationStale) || f.store.Durable() != before {
					t.Fatalf("stale callback acquired a new operation (%s): %v", field, err)
				}
			})
		}
	})
	t.Run("callback_stop_during_cold_read", func(t *testing.T) {
		t.Parallel()
		f, owner := continuationReadyFixture(t)
		token := owner.token(uuid.UUID{})
		damageHistoryCache(f.service, "disabled")
		arrived, release := stopColdHistory(f.service, needCommand)
		defer release()
		done := make(chan error, 1)
		go func() {
			_, err := f.service.updateContinuation(context.Background(), owner.Origin, owner.Snapshot.TurnID, "owner.advance", &token, continuationModelOpening)
			done <- err
		}()
		select {
		case <-arrived:
		case err := <-done:
			t.Fatalf("callback did not cross a cold unlock: %v", err)
		}
		stop, outcome, err := f.service.stopContinuation(context.Background(), owner.Origin, owner.Snapshot.TurnID, uuid.NewV7(), "cancelled", "owner")
		if err != nil || outcome != "accepted" {
			t.Fatalf("cold read held the stop gate or mutex: %+v %s %v", stop, outcome, err)
		}
		through := f.store.Durable()
		release()
		if err := <-done; !errors.Is(err, ErrContinuationStale) || f.store.Durable() != through {
			t.Fatalf("callback reused an owner across the unlocked stop: %v", err)
		}
		current, err := f.service.currentContinuation(context.Background(), owner.Origin, owner.Snapshot.TurnID)
		if err != nil || current.Snapshot.State != "stopping" || current.Snapshot.OperationID != stop.OperationID || current.Snapshot.Generation != stop.Generation {
			t.Fatalf("cold callback replaced the winning stop: %+v %v", current, err)
		}
	})
}

func TestContinuationProducerBounds(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"pending_count", "snapshot_bytes"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f, owner := continuationReadyFixture(t)
			id := uuid.NewV7()
			a := continuationArguments(owner.Snapshot)
			if err := continuationModelOpening(&a, owner, id); err != nil {
				t.Fatal(err)
			}
			if kind == "pending_count" {
				a.Pending = make([]ledger.ContinuationPending, 129)
				for i := range a.Pending {
					a.Pending[i] = ledger.ContinuationPending{Kind: "paid_attempt", ID: privateID(80000 + i), CommandID: privateID(81000 + i), ExpectedVersion: 1}
				}
			} else {
				a.NoticeCursors = make([]ledger.ContinuationNoticeCursor, 600)
				for i := range a.NoticeCursors {
					a.NoticeCursors[i] = ledger.ContinuationNoticeCursor{WorkspaceID: privateID(82000 + i), ScratchID: privateID(83000 + i)}
				}
			}
			before := f.store.Durable()
			_, err := f.service.commitContinuation(context.Background(), id, continuationSubmission(owner, "owner.advance", a), nil)
			if !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
				t.Fatalf("oversized owner changed the journal or lost its bounded refusal: %v", err)
			}
			current, err := f.service.currentContinuation(context.Background(), owner.Origin, owner.Snapshot.TurnID)
			if err != nil || !reflect.DeepEqual(current, owner) {
				t.Fatalf("oversized owner changed the current continuation: %+v %v", current, err)
			}
		})
	}
}

func TestContinuationUnknownWaitHeld(t *testing.T) {
	t.Parallel()
	commits, _ := continuationFrozen(t)
	for index, commit := range commits {
		_, _, _, _, _, _, group, err := decodeFamily(draftsOf(commit), commit.First)
		if err != nil {
			t.Fatal(err)
		}
		if group == nil || group.change.State != "waiting_resource" {
			continue
		}
		f, _ := scopedGoldenFixture(t, "16", index)
		sub := group.receipt.Submission
		sub.Arguments.WaitReason = "future_wait"
		drafts, err := continuationDrafts(group.command.CommandID, sub, f.store.Durable()+1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		through := f.store.Durable()
		s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if err != nil || len(report.Issues) != 0 {
			t.Fatalf("unknown wait was rejected rather than retained: %+v %v", report, err)
		}
		f.service = s
		found := false
		for _, pending := range report.PendingLoops {
			if pending.TurnID == sub.TurnID && pending.WaitReason == "future_wait" && pending.SavedPhase == "waiting_resource" {
				found = true
			}
		}
		reads := 0
		s.deps.beforeRead = func(Binding, string) error { reads++; return nil }
		receipt, err := s.ReconcileLoop(context.Background(), LoopReconciliation{CommandID: uuid.NewV7(), TurnID: sub.TurnID, ExpectedOwnerVersion: group.change.Version})
		if !found || !errors.Is(err, ErrRecoveryPending) || !errors.Is(err, ErrUnavailable) || receipt != (ContinuationReceipt{}) || reads != 0 || f.store.Durable() != through {
			t.Fatalf("unknown wait entered executable recovery: found=%v receipt=%+v reads=%d err=%v", found, receipt, reads, err)
		}
		return
	}
	t.Fatal("frozen wait prefix missing")
}

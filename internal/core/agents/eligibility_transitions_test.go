package agents

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func holdEligibility(t testing.TB, s *Service, agent uuid.UUID) EligibilityReceipt {
	t.Helper()
	view, err := s.EligibilityStatus(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	op := view.OperationID
	if view.State == "eligible" {
		op = uuid.UUID{}
	}
	r, err := s.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: uuid.NewV7(), AgentID: agent, ExpectedVersion: view.Version, ExpectedOperationID: op, Reason: "owner_hold"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func releaseEligibility(t testing.TB, s *Service, agent uuid.UUID, held EligibilityReceipt) EligibilityReceipt {
	t.Helper()
	r, err := s.ReleaseEligibility(context.Background(), EligibilityRelease{CommandID: uuid.NewV7(), AgentID: agent, ExpectedVersion: held.Version, ExpectedOperationID: held.OperationID})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func requireEligibilityHeld(t testing.TB, err error) *EligibilityHeldError {
	t.Helper()
	var held *EligibilityHeldError
	var busy *BusyError
	if !errors.As(err, &held) || !errors.Is(err, session.ErrEligibilityHeld) || errors.As(err, &busy) || errors.Is(err, ErrDenied) {
		t.Fatalf("not a distinct held refusal: %v", err)
	}
	return held
}

func TestEligibilityTransitions(t *testing.T) {
	t.Parallel()
	t.Run("oversized_owner_request_is_bounded", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t, serviceDeps{})
		binding := registerPrivate(t, f.service, 10)
		through := f.store.Durable()
		command := uuid.NewV7()
		_, err := f.service.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: command, AgentID: binding.AgentID, Reason: strings.Repeat("x", ledger.MaxEligibilityPayloadBytes+1)})
		if !errors.Is(err, ErrTooLarge) || f.store.Durable() != through {
			t.Fatal("oversized owner request appended or lost bounded error", err)
		}
		if _, err := f.service.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: command, AgentID: binding.AgentID, Reason: "owner_hold"}); err != nil {
			t.Fatal("invalid owner request consumed command", err)
		}
	})
	t.Run("owner_cas_receipt_and_no_effect", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		initial, err := f.service.EligibilityStatus(ctx, binding.AgentID)
		if err != nil || initial.State != "eligible" || initial.Version != 0 || initial.OperationID != (uuid.UUID{}) {
			t.Fatalf("initial projection: %+v %v", initial, err)
		}
		request := EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "future_owner_reason"}
		held, err := f.service.HoldOrdinaryWork(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		through := f.store.Durable()
		view, err := f.service.EligibilityStatus(ctx, binding.AgentID)
		if err != nil || view.State != "ordinary_work_ineligible" || view.Reason != request.Reason || view.Version != 1 || view.OperationID != request.CommandID || view.ThroughSequence != through {
			t.Fatalf("hold projection: %+v %v", view, err)
		}
		start := TurnAdmission{CommandID: uuid.NewV7(), TurnID: uuid.NewV7(), GrantID: grant.GrantID}
		_, err = agent.StartTurn(ctx, start)
		refusal := requireEligibilityHeld(t, err)
		if refusal.ThroughSequence != through || f.store.Durable() != through {
			t.Fatal("refusal consumed work or returned another prefix")
		}
		repeated, err := f.service.HoldOrdinaryWork(ctx, request)
		if err != nil || !reflect.DeepEqual(repeated, held) || f.store.Durable() != through {
			t.Fatal("same-ID hold was not receipt-only", err)
		}
		changed := request
		changed.Reason = "different"
		if _, err := f.service.HoldOrdinaryWork(ctx, changed); !errors.Is(err, ErrCommandMismatch) {
			t.Fatal("changed meaning reused command", err)
		}
		unchanged := request
		unchanged.CommandID, unchanged.ExpectedVersion, unchanged.ExpectedOperationID = uuid.NewV7(), 1, held.OperationID
		if _, err := f.service.HoldOrdinaryWork(ctx, unchanged); !errors.Is(err, ErrNoEffect) || f.store.Durable() != through {
			t.Fatal("no-effect hold appended", err)
		}
		stale := EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "hold"}
		if _, err := f.service.HoldOrdinaryWork(ctx, stale); !errors.Is(err, ErrRevision) {
			t.Fatal("stale version changed hold", err)
		}
		released := releaseEligibility(t, f.service, binding.AgentID, held)
		through = f.store.Durable()
		if _, err := f.service.ReleaseEligibility(ctx, EligibilityRelease{CommandID: uuid.NewV7(), AgentID: binding.AgentID, ExpectedVersion: released.Version, ExpectedOperationID: released.OperationID}); !errors.Is(err, ErrNoEffect) || f.store.Durable() != through {
			t.Fatal("distinct repeated release appended", err)
		}
		if replay, err := f.service.HoldOrdinaryWork(ctx, request); err != nil || replay != held || f.store.Durable() != through {
			t.Fatal("old receipt lost after release", err)
		}
		if _, err := agent.StartTurn(ctx, start); err != nil {
			t.Fatal("explicit retry of refused identity failed", err)
		}
		if _, err := f.service.HoldOrdinaryWork(ctx, EligibilityHold{CommandID: start.CommandID, AgentID: binding.AgentID, ExpectedVersion: released.Version, Reason: "hold"}); !errors.Is(err, ErrCommandMismatch) {
			t.Fatal("cross-family command reused", err)
		}
		reopenScoped(t, f, DefaultLimits())
		if replay, err := f.service.HoldOrdinaryWork(ctx, request); err != nil || replay != held {
			t.Fatal("cold receipt lost after reopen", err)
		}
	})
	t.Run("release_old_operation_at_current_version", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, binding, agent, grant := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		old := holdEligibility(t, f.service, binding.AgentID)
		releaseEligibility(t, f.service, binding.AgentID, old)
		current := holdEligibility(t, f.service, binding.AgentID)
		through := f.store.Durable()
		_, releaseErr := f.service.ReleaseEligibility(ctx, EligibilityRelease{CommandID: uuid.NewV7(), AgentID: binding.AgentID, ExpectedVersion: current.Version, ExpectedOperationID: old.OperationID})
		_, startErr := agent.StartTurn(ctx, TurnAdmission{CommandID: uuid.NewV7(), TurnID: uuid.NewV7(), GrantID: grant.GrantID})
		if !errors.Is(releaseErr, ErrEligibilityStale) || !errors.Is(startErr, session.ErrEligibilityHeld) || f.store.Durable() != through {
			t.Fatalf("old operation released newer hold or admitted fresh work: release=%v start=%v", releaseErr, startErr)
		}
		requireEligibilityHeld(t, startErr)
	})
	t.Run("concurrent_holds_have_one_winner", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t, serviceDeps{})
		binding := registerPrivate(t, f.service, 10)
		before := f.store.Durable()
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				<-start
				_, err := f.service.HoldOrdinaryWork(context.Background(), EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "hold"})
				errs <- err
			})
		}
		close(start)
		wg.Wait()
		close(errs)
		wins, stale := 0, 0
		for err := range errs {
			if err == nil {
				wins++
			} else if errors.Is(err, ErrRevision) {
				stale++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || stale != 1 || f.store.Durable() != before+3 {
			t.Fatalf("concurrent hold counts: %d/%d", wins, stale)
		}
	})
	t.Run("hold_from_first_required_retains_declaration", func(t *testing.T) {
		t.Parallel()
		f, _ := eligibilityGolden(t, 78)
		var agent uuid.UUID
		for id, a := range f.service.p.agents {
			if a.eligibility.current().State == "first_action_required" {
				agent = id
			}
		}
		before, err := f.service.EligibilityStatus(context.Background(), agent)
		if err != nil || before.FirstAction == nil {
			t.Fatal("missing frozen declaration", err)
		}
		held := holdEligibility(t, f.service, agent)
		after, err := f.service.EligibilityStatus(context.Background(), agent)
		if err != nil || after.State != "ordinary_work_ineligible" || after.OperationID != before.OperationID || !reflect.DeepEqual(after.FirstAction, before.FirstAction) {
			t.Fatal("hold discarded first-action history", err)
		}
		releaseEligibility(t, f.service, agent, held)
		fresh := holdEligibility(t, f.service, agent)
		after, err = f.service.EligibilityStatus(context.Background(), agent)
		if err != nil || after.FirstAction != nil || after.OperationID == before.OperationID || fresh.OperationID != fresh.CommandID {
			t.Fatal("new hold retained an executable old declaration", err)
		}
	})
}

func eligibilityGolden(t testing.TB, through uint64) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	_, commits := scopedGoldenFixture(t, "90", 0)
	count := 0
	for _, c := range commits {
		if c.Last <= through {
			count++
		}
	}
	f, commits := scopedGoldenFixture(t, "90", count)
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return f, commits
}

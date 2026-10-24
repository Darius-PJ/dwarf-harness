package agents

import (
	"bytes"
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// A projection is disclosed only after refresh, with authority and its ledger
// high-water held stable until all returned metadata has been copied.
func (s *Service) scopedQuery(ctx context.Context, bound *submissionIDs) (func(), error) {
	if s == nil {
		return nil, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	done := func() { s.mu.Unlock(); s.active.Done() }
	err := s.usableLocked(ctx)
	if err == nil {
		err = s.refreshLocked(ctx, s.store.Durable())
	}
	if err == nil && bound != nil && !s.p.registeredOrigin(originOf(*bound)) {
		err = ErrNotBound
	}
	if err != nil {
		done()
		return nil, err
	}
	return done, nil
}

func (s *Service) workView(work scopedWorkState) (WorkView, error) {
	encoded, err := work.admission.Encode()
	if err != nil {
		return WorkView{}, err
	}
	admission, err := ledger.DecodeRuntimeWorkAdmitted(encoded)
	if err != nil {
		return WorkView{}, err
	}
	view := WorkView{Admission: admission, Origin: work.origin, Version: work.version, State: "admitted", Outcome: work.outcome, Sequence: work.sequence, Dispatched: work.dispatched, Ended: work.ended}
	if work.dispatched {
		view.State = "dispatched"
	}
	if work.ended {
		view.State = work.outcome
	}
	if !work.released {
		if work.admission.Kind == "turn" {
			view.Held.TurnSlots = 1
		} else {
			view.Held.LocalSlots, view.Held.BufferBytes = 1, *work.admission.MaxBytes
			if !work.dispatched {
				view.Held.LocalUnits = 1
			}
		}
		if work.sequence <= s.openedThrough {
			view.State = "recovery_pending"
		}
	}
	return view, nil
}

func (s *Service) pendingWorkViews() ([]WorkView, error) {
	ids := []uuid.UUID{}
	for id, work := range s.p.work {
		if !work.released {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	views := make([]WorkView, 0, len(ids))
	for _, id := range ids {
		view, err := s.workView(s.p.work[id])
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

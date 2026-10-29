package agents

import (
	"context"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// A miss in the bounded live slots is not absence. Only the validated ledger
// establishes historical identity, outcome and explicit replacement edges.
func (s *Service) loadReview(ctx context.Context, id uuid.UUID, through uint64) (reviewSlot, bool, error) {
	for _, slot := range s.p.reviews {
		if slot.id == id && through == s.p.through {
			return slot, true, nil
		}
	}
	if slot, found, hit := s.history.review(id, through); hit {
		return slot, found, nil
	}
	var slot reviewSlot
	found := false
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
		_, scoped, g, _, _, err := decodeFamily(draftsOf(c), c.First)
		if err != nil {
			return err
		}
		if g != nil {
			if q := g.request; q != nil {
				if q.RequestID == id {
					slot = reviewSlot{id: id, binding: q.Binding, kind: q.Kind, state: "pending", request: *q, sequence: c.First + 1}
					found = true
				}
				if found && q.ReplacesRequestID == id {
					slot.supersededBy = q.RequestID
					if slot.result == nil {
						slot.state = "superseded"
					}
				}
			}
			if v := g.result; v != nil && found && v.RequestID == id {
				slot.result, slot.resultSequence, slot.state = v, c.First+1, reviewOutcome(v.Verdict)
			}
		}
		if found && scoped != nil && scoped.task != nil && scoped.task.TaskID == slot.binding.TaskID && slot.result == nil && slot.supersededBy == (uuid.UUID{}) {
			slot.state = "stale"
		}
		return nil
	})
	if err == nil {
		s.history.putReview(id, through, slot, found)
	}
	return slot, found, err
}

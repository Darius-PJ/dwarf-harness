package ledgerstore

import (
	"fmt"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// maxPinned bounds the active (pin, body) pairs. It is Core policy, which
// the owner may change without a format change.
const maxPinned = 1 << 16

// pinSet is the projection of the active pins: each active pin ID with the
// bodies its ledger.body_pin record listed, and the number of active pins
// holding each body. The writer turn guards it.
type pinSet struct {
	active map[uuid.UUID][]ledger.Digest
	pinned map[ledger.Digest]int
	pairs  int // active (pin, body) pairs
}

func newPinSet() pinSet {
	return pinSet{active: map[uuid.UUID][]ledger.Digest{}, pinned: map[ledger.Digest]int{}}
}

// pinPlan is the effect of one commit's pin and unpin records on a pinSet.
type pinPlan struct {
	released []uuid.UUID                   // active pins the commit releases
	added    map[uuid.UUID][]ledger.Digest // pins the commit leaves active
	pairs    int                           // active pairs after the commit
}

// plan checks the F0 record rules of drafts, one commit, against ps and
// returns the commit's effect on it. Drafts are taken in order, so a commit
// may pin and then release a pin, or release one and pin its ID again. It
// refuses a ledger.bodies_collected draft, which only collection writes; a
// pin whose payload does not decode, that lists no body or whose ID is
// active; an unpin whose payload does not decode, that lists bodies or whose
// ID is not active; and a commit holding a pin that would leave more than
// limit active pairs. A commit of unpins alone is never refused for the
// bound, so that a projection over a lowered bound can shrink.
func (ps *pinSet) plan(drafts []ledger.Draft, limit int) (pinPlan, error) {
	p := pinPlan{pairs: ps.pairs}
	released := map[uuid.UUID]bool{}
	active := func(id uuid.UUID) bool {
		if _, ok := p.added[id]; ok {
			return true
		}
		_, ok := ps.active[id]
		return ok && !released[id]
	}
	pins := false
	for i, d := range drafts {
		switch d.Type {
		case ledger.TypeBodiesCollected:
			return pinPlan{}, fmt.Errorf("draft %d: %s is written only by the store's collection", i, d.Type)
		case ledger.TypeBodyPin:
			pin, err := ledger.DecodeBodyPin(d.Payload)
			switch {
			case err != nil:
				return pinPlan{}, fmt.Errorf("draft %d: %w", i, err)
			case len(d.BodyRefs) == 0:
				return pinPlan{}, fmt.Errorf("draft %d: %s %s lists no body", i, d.Type, pin.PinID)
			case active(pin.PinID):
				return pinPlan{}, fmt.Errorf("draft %d: pin %s is already active", i, pin.PinID)
			}
			if p.added == nil {
				p.added = map[uuid.UUID][]ledger.Digest{}
			}
			p.added[pin.PinID] = slices.Clone(d.BodyRefs)
			p.pairs += len(d.BodyRefs)
			pins = true
		case ledger.TypeBodyUnpin:
			unpin, err := ledger.DecodeBodyUnpin(d.Payload)
			switch {
			case err != nil:
				return pinPlan{}, fmt.Errorf("draft %d: %w", i, err)
			case len(d.BodyRefs) != 0:
				return pinPlan{}, fmt.Errorf("draft %d: %s %s lists bodies", i, d.Type, unpin.PinID)
			case !active(unpin.PinID):
				return pinPlan{}, fmt.Errorf("draft %d: pin %s is not active", i, unpin.PinID)
			}
			if refs, ok := p.added[unpin.PinID]; ok {
				delete(p.added, unpin.PinID)
				p.pairs -= len(refs)
			} else {
				released[unpin.PinID] = true
				p.released = append(p.released, unpin.PinID)
				p.pairs -= len(ps.active[unpin.PinID])
			}
		}
	}
	if pins && p.pairs > limit {
		return pinPlan{}, fmt.Errorf("the commit would leave %d active (pin, body) pairs, above the bound of %d", p.pairs, limit)
	}
	return p, nil
}

// apply makes p, planned against ps, ps's state.
func (ps *pinSet) apply(p pinPlan) {
	for _, id := range p.released {
		for _, d := range ps.active[id] {
			if ps.pinned[d]--; ps.pinned[d] == 0 {
				delete(ps.pinned, d)
			}
		}
		delete(ps.active, id)
	}
	for id, refs := range p.added {
		ps.active[id] = refs
		for _, d := range refs {
			ps.pinned[d]++
		}
	}
	ps.pairs = p.pairs
}

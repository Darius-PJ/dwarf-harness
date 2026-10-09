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
	gone     map[uuid.UUID]bool            // the pins in released
}

// plan checks the F0 record rules of drafts, one commit, against ps and
// returns the commit's effect on it. Drafts are taken in order, so a commit
// may pin and then release a pin, or release one and pin its ID again. It
// refuses a ledger.command draft, which only Submit writes; a
// ledger.bodies_collected draft, which only collection writes; a pin or an
// unpin that take refuses; and a commit holding a pin that would leave more
// than limit active pairs. A commit of unpins alone is never refused for the
// bound, so that a projection over a lowered bound can shrink.
func (ps *pinSet) plan(drafts []ledger.Draft, limit int) (pinPlan, error) {
	p := pinPlan{pairs: ps.pairs}
	pins := false
	for i, d := range drafts {
		switch d.Type {
		case ledger.TypeCommand:
			return pinPlan{}, fmt.Errorf("draft %d: %s is written only by Submit", i, d.Type)
		case ledger.TypeBodiesCollected:
			return pinPlan{}, fmt.Errorf("draft %d: %s is written only by the store's collection", i, d.Type)
		}
		pinned, err := p.take(ps, d)
		if err != nil {
			return pinPlan{}, fmt.Errorf("draft %d: %w", i, err)
		}
		pins = pins || pinned
	}
	if pins && p.pairs > limit {
		return pinPlan{}, fmt.Errorf("the commit would leave %d active (pin, body) pairs, above the bound of %d", p.pairs, limit)
	}
	return p, nil
}

// take adds the effect of d, the next record of the commit that p plans
// against ps, to p, and reports whether d was a pin. A record of any other
// type has no effect. It refuses a pin whose payload does not decode, that
// lists no body or whose ID is active, and an unpin whose payload does not
// decode, that lists bodies or whose ID is not active; a pin is active
// from the pin record that makes it so, in the same commit too. Append and
// the rebuild at Open apply these rules alike, and take applies no bound.
func (p *pinPlan) take(ps *pinSet, d ledger.Draft) (bool, error) {
	active := func(id uuid.UUID) bool {
		if _, ok := p.added[id]; ok {
			return true
		}
		_, ok := ps.active[id]
		return ok && !p.gone[id]
	}
	switch d.Type {
	case ledger.TypeBodyPin:
		pin, err := ledger.DecodeBodyPin(d.Payload)
		switch {
		case err != nil:
			return false, err
		case len(d.BodyRefs) == 0:
			return false, fmt.Errorf("%s %s lists no body", d.Type, pin.PinID)
		case active(pin.PinID):
			return false, fmt.Errorf("pin %s is already active", pin.PinID)
		}
		if p.added == nil {
			p.added = map[uuid.UUID][]ledger.Digest{}
		}
		p.added[pin.PinID] = slices.Clone(d.BodyRefs)
		p.pairs += len(d.BodyRefs)
		return true, nil
	case ledger.TypeBodyUnpin:
		unpin, err := ledger.DecodeBodyUnpin(d.Payload)
		switch {
		case err != nil:
			return false, err
		case len(d.BodyRefs) != 0:
			return false, fmt.Errorf("%s %s lists bodies", d.Type, unpin.PinID)
		case !active(unpin.PinID):
			return false, fmt.Errorf("pin %s is not active", unpin.PinID)
		}
		if refs, ok := p.added[unpin.PinID]; ok {
			delete(p.added, unpin.PinID)
			p.pairs -= len(refs)
			return false, nil
		}
		if p.gone == nil {
			p.gone = map[uuid.UUID]bool{}
		}
		p.gone[unpin.PinID] = true
		p.released = append(p.released, unpin.PinID)
		p.pairs -= len(ps.active[unpin.PinID])
	}
	return false, nil
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

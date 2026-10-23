package agents

import (
	"bytes"
	"cmp"
	"math"
	"slices"
	"uuid"
)

type budgetScope struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id,omitzero"`
}

// budgetScopes accepts identities already selected by authority, not additional
// grants to authorize. Shared ancestors in several chains occupy only one scope.
func budgetScopes(binding Binding, chains ...[]uuid.UUID) ([]budgetScope, error) {
	if !validID(binding.AgentID) || !validID(binding.ContextID) || len(chains) == 0 {
		return nil, ErrInvalid
	}
	scopes := []budgetScope{{Kind: "colony"}, {Kind: "agent", ID: binding.AgentID}, {Kind: "context", ID: binding.ContextID}}
	for _, chain := range chains {
		if len(chain) == 0 {
			return nil, ErrInvalid
		}
		for _, id := range chain {
			if !validID(id) {
				return nil, ErrInvalid
			}
			scopes = append(scopes, budgetScope{Kind: "grant", ID: id})
		}
	}
	slices.SortFunc(scopes, func(a, b budgetScope) int {
		order := func(kind string) int {
			switch kind {
			case "colony":
				return 0
			case "agent":
				return 1
			case "context":
				return 2
			default:
				return 3
			}
		}
		if n := cmp.Compare(order(a.Kind), order(b.Kind)); n != 0 {
			return n
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	return slices.Compact(scopes), nil
}

type budgetCounter struct{ held, consumed int64 }

// changeBudget returns the original counter on refusal. Releases and conversion
// of existing holds remain legal after a limit is lowered; only new holds need
// room. Consumed units never reset and a charge must consume an existing hold.
func changeBudget(current budgetCounter, hold, release, charge, limit int64) (budgetCounter, error) {
	if current.held < 0 || current.consumed < 0 || hold < 0 || release < 0 || charge < 0 || limit < 0 ||
		release > current.held || charge > current.held-release || charge > math.MaxInt64-current.consumed {
		return current, ErrInvalid
	}
	next := budgetCounter{held: current.held - release - charge, consumed: current.consumed + charge}
	if hold > math.MaxInt64-next.held {
		return current, ErrInvalid
	}
	next.held += hold
	if next.held > math.MaxInt64-next.consumed {
		return current, ErrInvalid
	}
	if hold > 0 && next.held+next.consumed > limit {
		return current, &BusyError{}
	}
	return next, nil
}

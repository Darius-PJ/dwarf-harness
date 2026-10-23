package agents

import (
	"errors"
	"math"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScopedBudgetScopeSet(t *testing.T) {
	t.Parallel()
	binding := Binding{privateID(1), privateID(2)}
	chains := [][]uuid.UUID{{privateID(12), privateID(10)}, {privateID(11), privateID(10)}}
	got, err := budgetScopes(binding, chains...)
	want := []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: binding.AgentID}, {Kind: "context", ID: binding.ContextID}, {Kind: "grant", ID: privateID(10)}, {Kind: "grant", ID: privateID(11)}, {Kind: "grant", ID: privateID(12)}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("scope set = %v, %v; want %v", got, err, want)
	}
	if chains[0][0] != privateID(12) {
		t.Fatal("scope sort mutated caller chain")
	}
	if _, err := budgetScopes(binding, []uuid.UUID{{}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid grant ID: %v", err)
	}
	if _, err := budgetScopes(binding); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing leaf: %v", err)
	}
}

func TestScopedCheckedBudget(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                         string
		before                       budgetCounter
		hold, release, charge, limit int64
		want                         budgetCounter
		refuse                       bool
	}{
		{"hold_last", budgetCounter{held: math.MaxInt64 - 1}, 1, 0, 0, math.MaxInt64, budgetCounter{held: math.MaxInt64}, false},
		{"hold_overflow", budgetCounter{held: math.MaxInt64}, 1, 0, 0, math.MaxInt64, budgetCounter{held: math.MaxInt64}, true},
		{"charge_last", budgetCounter{held: 1, consumed: math.MaxInt64 - 1}, 0, 0, 1, math.MaxInt64, budgetCounter{consumed: math.MaxInt64}, false},
		{"charge_overflow", budgetCounter{held: 1, consumed: math.MaxInt64}, 0, 0, 1, math.MaxInt64, budgetCounter{held: 1, consumed: math.MaxInt64}, true},
		{"negative_release", budgetCounter{held: 2}, 0, -1, 0, 10, budgetCounter{held: 2}, true},
		{"excess_release", budgetCounter{held: 2}, 0, 3, 0, 10, budgetCounter{held: 2}, true},
		{"double_release", budgetCounter{}, 0, 1, 0, 10, budgetCounter{}, true},
		{"unheld_charge", budgetCounter{held: 1}, 0, 0, 2, 10, budgetCounter{held: 1}, true},
		{"lowered_limit_release", budgetCounter{held: 3, consumed: 5}, 0, 1, 0, 1, budgetCounter{held: 2, consumed: 5}, false},
		{"lowered_limit_charge", budgetCounter{held: 3, consumed: 5}, 0, 0, 1, 1, budgetCounter{held: 2, consumed: 6}, false},
		{"capacity", budgetCounter{held: 2, consumed: 5}, 1, 0, 0, 7, budgetCounter{held: 2, consumed: 5}, true},
		{"unsent", budgetCounter{held: 1, consumed: 5}, 0, 1, 0, 7, budgetCounter{consumed: 5}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got, err := changeBudget(row.before, row.hold, row.release, row.charge, row.limit)
			if got != row.want || (err != nil) != row.refuse {
				t.Fatalf("budget = %+v, %v; want %+v, refuse=%v", got, err, row.want, row.refuse)
			}
			if row.refuse {
				var busy *BusyError
				if row.name == "capacity" {
					if !errors.As(err, &busy) {
						t.Fatalf("capacity refusal is not busy: %v", err)
					}
				} else if !errors.Is(err, ErrInvalid) {
					t.Fatalf("arithmetic refusal is not invalid: %v", err)
				}
			}
		})
	}
}

func TestScopedLiveLimits(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"tasks", "grants", "local_work"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			limits := DefaultLimits()
			switch field {
			case "tasks":
				limits.MaxLiveTasks = -1
			case "grants":
				limits.MaxLiveGrants = -1
			case "local_work":
				limits.MaxPendingLocalWork = -1
			}
			if !errors.Is(limits.validate(), ErrInvalid) {
				t.Fatal("negative admission guard accepted")
			}
		})
	}
	limits := DefaultLimits()
	limits.MaxLiveTasks, limits.MaxLiveGrants, limits.MaxPendingLocalWork = 0, 0, 0
	if err := limits.validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero live limits accepted: %v", err)
	}
	limits.MaxLiveTasks, limits.MaxLiveGrants, limits.MaxPendingLocalWork = 1, 1, 1
	if err := limits.validate(); err != nil {
		t.Fatalf("small positive live limits refused: %v", err)
	}
}

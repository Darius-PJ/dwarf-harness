package agents

import (
	"bytes"
	"math/big"
	"slices"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Only the selected period's aggregate is hot. Holds and fences span periods.
// Integers are immutable once installed; every plan owns its changed values.
type paidAccount struct {
	version       int64
	terms         ledger.SpendTerms
	period        string
	settled, held *big.Int
	fences        int
}

func paidInteger(n *big.Int) *big.Int {
	if n == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(n)
}
func paidPeriod(kind string, observed int64) string {
	t := time.Unix(0, observed).UTC()
	switch kind {
	case "lifetime":
		return "lifetime"
	case "utc_day":
		return t.Format("2006-01-02")
	case "utc_month":
		return t.Format("2006-01")
	}
	return ""
}

func validPaidQueryPeriod(kind, period string) bool {
	if kind == "lifetime" {
		return period == "lifetime"
	}
	layout := map[string]string{"utc_day": "2006-01-02", "utc_month": "2006-01"}[kind]
	if layout == "" {
		return false
	}
	t, err := time.Parse(layout, period)
	return err == nil && t.Format(layout) == period
}
func comparePaidScopes(a, b ledger.PaidScope) int {
	kinds := []string{"colony", "agent", "credential", "task", "grant"}
	if n := slices.Index(kinds, a.Kind) - slices.Index(kinds, b.Kind); n != 0 {
		return n
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}
func paidScopes(origin ledger.Origin, credential uuid.UUID, task *ledger.TaskBinding, chain []grantRules) []ledger.PaidScope {
	scopes := []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: origin.AgentID}, {Kind: "credential", ID: credential}}
	if task != nil {
		scopes = append(scopes, ledger.PaidScope{Kind: "task", ID: task.TaskID})
	}
	for _, grant := range chain {
		scopes = append(scopes, ledger.PaidScope{Kind: "grant", ID: grant.id})
	}
	slices.SortFunc(scopes, comparePaidScopes)
	return slices.Compact(scopes)
}
func paidSettled(a paidAccount, period string) *big.Int {
	if a.period != period {
		return new(big.Int)
	}
	return paidInteger(a.settled)
}
func paidOccupied(a paidAccount, period string) *big.Int {
	return new(big.Int).Add(paidSettled(a, period), paidInteger(a.held))
}
func paidWarning(a paidAccount, period string) bool {
	if a.terms.Mode != "finite" || a.terms.WarningPPM == nil {
		return false
	}
	threshold := new(big.Int).Mul(big.NewInt(*a.terms.CeilingMicros), big.NewInt(*a.terms.WarningPPM))
	threshold.Add(threshold, big.NewInt(999999)).Quo(threshold, big.NewInt(1000000))
	return paidOccupied(a, period).Cmp(threshold) >= 0
}
func (b *budgetBook) checkPaid(scopes []ledger.PaidScope, period string, extra int64) error {
	for _, scope := range scopes {
		a := b.paid[scope]
		if a.version == 0 && scope.Kind == "task" {
			continue
		}
		if a.version == 0 || !a.terms.Enabled {
			return ErrDenied
		}
		if a.fences != 0 {
			return &BusyError{Reason: "paid_violation"}
		}
		occupied := paidOccupied(a, period)
		occupied.Add(occupied, big.NewInt(extra))
		if a.terms.Mode == "finite" && occupied.Cmp(big.NewInt(*a.terms.CeilingMicros)) > 0 {
			return &BusyError{Reason: "budget_exposure"}
		}
	}
	return nil
}

// planPaidMoney changes every affected scope together, never a second wallet.
func (b *budgetBook) planPaidMoney(before, after paidAttempt, current string) (map[ledger.PaidScope]paidAccount, error) {
	next := make(map[ledger.PaidScope]paidAccount, len(after.admission.Scopes))
	for _, scope := range after.admission.Scopes {
		a := b.paid[scope]
		a.settled, a.held = paidSettled(a, current), paidInteger(a.held)
		a.period = current
		if before.held {
			a.held.Sub(a.held, big.NewInt(before.admission.Quote.MaximumMicros))
		}
		if after.held {
			a.held.Add(a.held, big.NewInt(after.admission.Quote.MaximumMicros))
		}
		if after.admission.AdmissionPeriod == current {
			a.settled.Sub(a.settled, big.NewInt(before.cost))
			a.settled.Add(a.settled, big.NewInt(after.cost))
		}
		if before.fenced {
			a.fences--
		}
		if after.fenced {
			a.fences++
		}
		if a.held.Sign() < 0 || a.settled.Sign() < 0 || a.fences < 0 {
			return nil, ErrInvalid
		}
		next[scope] = a
	}
	return next, nil
}

func paidTermsAttenuate(parent, child ledger.SpendTerms) bool {
	if child.PeriodKind != parent.PeriodKind || child.Enabled && !parent.Enabled {
		return false
	}
	if parent.Mode == "finite" && (child.Mode != "finite" || *child.CeilingMicros > *parent.CeilingMicros) {
		return false
	}
	return child.WarningPPM == nil || parent.WarningPPM != nil && *child.WarningPPM == *parent.WarningPPM
}

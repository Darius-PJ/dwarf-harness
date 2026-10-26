package agents

import (
	"math"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

const (
	turnExposure = iota
	localExposure
	bufferExposure
	unitExposure
)

type budgetAccount struct {
	version    int64
	configured bool
	limits     ledger.Capacity
	usage      [4]budgetCounter
}

// Durable usage and transient preparation share one admission book. Replay
// reconstructs only durable exposure; a preparation never grants a permit.
type budgetBook struct {
	accounts        map[ledger.ScopeKey]budgetAccount
	preparing       map[ledger.ScopeKey][4]int64
	originPreparing map[ledger.Origin]map[ledger.ScopeKey][4]int64
	paid            map[ledger.PaidScope]paidAccount
	paidPeriodKind  string
	paidPeriod      string
	paidObserved    int64
}

type budgetDelta struct{ hold, release, charge [4]int64 }

// A nil operationBytes is a byte-returning retry: its physical holds remain
// bounded, but it neither reserves a unit nor rechecks a new-operation ceiling.
type budgetPreparation struct {
	hold           [4]int64
	operationBytes *int64
}

func capacityAmounts(c ledger.Capacity) [4]int64 {
	return [4]int64{c.TurnSlots, c.LocalSlots, c.BufferBytes, c.LocalUnits}
}

func (b *budgetBook) plan(scopes []ledger.ScopeKey, delta budgetDelta, operationBytes int64, admission bool) (map[ledger.ScopeKey]budgetAccount, error) {
	next := make(map[ledger.ScopeKey]budgetAccount, len(scopes))
	for _, scope := range scopes {
		account, exists := b.accounts[scope]
		if (!exists || !account.configured) && scope.Kind != "context" {
			return nil, &BusyError{Reason: "not_enrolled"}
		}
		limits := capacityAmounts(account.limits)
		if !account.configured {
			limits = [4]int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
		} else if admission && operationBytes > account.limits.MaxOperationBytes {
			return nil, &BusyError{Reason: "operation_bytes"}
		}
		for i := range account.usage {
			value, err := changeBudget(account.usage[i], delta.hold[i], delta.release[i], delta.charge[i], limits[i])
			if err != nil {
				return nil, budgetScopeError(scope, err)
			}
			account.usage[i] = value
		}
		next[scope] = account
	}
	return next, nil
}

func (b *budgetBook) atomic(scopes []ledger.ScopeKey, size int64) (map[ledger.ScopeKey]budgetAccount, error) {
	if size < 0 {
		return nil, ErrInvalid
	}
	holds := [4]int64{0, 1, size, 1}
	next, err := b.plan(scopes, budgetDelta{hold: holds}, size, true)
	if err != nil {
		return nil, err
	}
	for scope, account := range next {
		for i := range account.usage {
			release, charge := holds[i], int64(0)
			if i == unitExposure {
				release, charge = 0, 1
			}
			account.usage[i], err = changeBudget(account.usage[i], 0, release, charge, math.MaxInt64)
			if err != nil {
				return nil, err
			}
		}
		next[scope] = account
	}
	return next, nil
}

func (b *budgetBook) reservePreparation(origin ledger.Origin, scopes []ledger.ScopeKey, preparation budgetPreparation) error {
	amounts := preparation.hold
	if err := b.checkPreparation(scopes, preparation, true); err != nil {
		return err
	}
	if b.originPreparing == nil {
		b.originPreparing = map[ledger.Origin]map[ledger.ScopeKey][4]int64{}
	}
	own := b.originPreparing[origin]
	if own == nil {
		own = map[ledger.ScopeKey][4]int64{}
		b.originPreparing[origin] = own
	}
	for _, scope := range scopes {
		prepared := b.preparing[scope]
		for i, amount := range amounts {
			prepared[i] += amount
		}
		b.preparing[scope] = prepared
		prepared = own[scope]
		for i, amount := range amounts {
			prepared[i] += amount
		}
		own[scope] = prepared
	}
	return nil
}

func (b *budgetBook) releasePreparation(origin ledger.Origin, scopes []ledger.ScopeKey, preparation budgetPreparation) {
	own := b.originPreparing[origin]
	for _, scope := range scopes {
		prepared := b.preparing[scope]
		for i, amount := range preparation.hold {
			prepared[i] -= amount
		}
		if prepared == ([4]int64{}) {
			delete(b.preparing, scope)
		} else {
			b.preparing[scope] = prepared
		}
		prepared = own[scope]
		for i, amount := range preparation.hold {
			prepared[i] -= amount
		}
		if prepared == ([4]int64{}) {
			delete(own, scope)
		} else {
			own[scope] = prepared
		}
	}
	if len(own) == 0 {
		delete(b.originPreparing, origin)
	}
}

func (b *budgetBook) checkPreparation(scopes []ledger.ScopeKey, preparation budgetPreparation, additional bool) error {
	for _, scope := range scopes {
		account := b.accounts[scope]
		if !account.configured && scope.Kind != "context" {
			return &BusyError{Reason: "not_enrolled"}
		}
		limits := capacityAmounts(account.limits)
		if !account.configured {
			limits = [4]int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
		} else if preparation.operationBytes != nil && *preparation.operationBytes > account.limits.MaxOperationBytes {
			return &BusyError{Reason: "operation_bytes"}
		}
		for i, needed := range preparation.hold {
			if needed < 0 {
				return ErrInvalid
			}
			if needed == 0 {
				continue
			}
			current, err := changeBudget(account.usage[i], b.preparing[scope][i], 0, 0, limits[i])
			if err != nil {
				return budgetScopeError(scope, err)
			}
			if additional {
				if _, err := changeBudget(current, needed, 0, 0, limits[i]); err != nil {
					return budgetScopeError(scope, err)
				}
			}
		}
	}
	return nil
}

func budgetScopeError(scope ledger.ScopeKey, err error) error {
	if _, busy := err.(*BusyError); busy {
		return &BusyError{Reason: scope.Kind + "_capacity"}
	}
	return err
}

func (b *budgetBook) install(next map[ledger.ScopeKey]budgetAccount) {
	for scope, account := range next {
		b.accounts[scope] = account
	}
}

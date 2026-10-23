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
}

type budgetDelta struct{ hold, release, charge [4]int64 }

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
				return nil, err
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

func (b *budgetBook) reservePreparation(origin ledger.Origin, scopes []ledger.ScopeKey, size int64) error {
	amounts := [4]int64{0, 1, size, 1}
	if _, err := b.plan(scopes, budgetDelta{hold: amounts}, size, true); err != nil {
		return err
	}
	for _, scope := range scopes {
		account := b.accounts[scope]
		limits := capacityAmounts(account.limits)
		if !account.configured {
			limits = [4]int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
		}
		for i, amount := range amounts {
			prepared := b.preparing[scope][i]
			current, err := changeBudget(account.usage[i], prepared, 0, 0, limits[i])
			if err != nil {
				return err
			}
			if _, err := changeBudget(current, amount, 0, 0, limits[i]); err != nil {
				return err
			}
		}
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

func (b *budgetBook) releasePreparation(origin ledger.Origin, scopes []ledger.ScopeKey, size int64) {
	own := b.originPreparing[origin]
	for _, scope := range scopes {
		prepared := b.preparing[scope]
		for i, amount := range [4]int64{0, 1, size, 1} {
			prepared[i] -= amount
		}
		if prepared == ([4]int64{}) {
			delete(b.preparing, scope)
		} else {
			b.preparing[scope] = prepared
		}
		prepared = own[scope]
		for i, amount := range [4]int64{0, 1, size, 1} {
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

func (b *budgetBook) checkPreparation(scopes []ledger.ScopeKey, size int64) error {
	for _, scope := range scopes {
		account := b.accounts[scope]
		if !account.configured && scope.Kind != "context" {
			return &BusyError{Reason: "not_enrolled"}
		}
		limits := capacityAmounts(account.limits)
		if !account.configured {
			limits = [4]int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
		} else if size > account.limits.MaxOperationBytes {
			return &BusyError{Reason: "operation_bytes"}
		}
		for i, prepared := range b.preparing[scope] {
			if _, err := changeBudget(account.usage[i], prepared, 0, 0, limits[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *budgetBook) install(next map[ledger.ScopeKey]budgetAccount) {
	for scope, account := range next {
		b.accounts[scope] = account
	}
}

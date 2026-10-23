package agents

import (
	"context"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// This reads a validated prefix, not a second authorization/replay engine. It
// reconstructs one cold account, or the originating share that may be disclosed
// to an Agent, without retaining a lifetime per-origin accounting index.
func (s *Service) budgetHistory(ctx context.Context, scope ledger.ScopeKey, origin *ledger.Origin) (budgetAccount, error) {
	var account budgetAccount
	work := map[uuid.UUID]scopedWorkState{}
	apply := func(delta budgetDelta) error {
		for i := range account.usage {
			next, err := changeBudget(account.usage[i], delta.hold[i], delta.release[i], delta.charge[i], math.MaxInt64)
			if err != nil {
				return err
			}
			account.usage[i] = next
		}
		return nil
	}
	err := s.scan(ctx, 0, s.p.through, func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			own := origin == nil || *origin == (ledger.Origin{AgentID: record.AgentID, SessionID: record.ExecutionSessionID, ContextID: record.ContextID})
			switch record.Type {
			case ledger.TypeRuntimeGrantIssued:
				grant, err := ledger.DecodeRuntimeGrantIssued(record.Payload)
				if err != nil {
					return err
				}
				if scope == (ledger.ScopeKey{Kind: "grant", ID: grant.GrantID}) {
					account.configured, account.version, account.limits = true, 1, grant.Limits
				}
			case ledger.TypeRuntimeCapacityConfigured:
				configuration, err := ledger.DecodeRuntimeCapacityConfigured(record.Payload)
				if err != nil {
					return err
				}
				if configuration.Scope == scope {
					account.configured, account.version, account.limits = true, configuration.Version, configuration.Limits
				}
			case ledger.TypeRuntimeLocalUsed:
				if !own {
					continue
				}
				usage, err := ledger.DecodeRuntimeLocalUsed(record.Payload)
				if err != nil {
					return err
				}
				if !slices.Contains(usage.Scopes, scope) {
					continue
				}
				if err := apply(budgetDelta{hold: [4]int64{0, 0, 0, usage.Units}}); err != nil {
					return err
				}
				if err := apply(budgetDelta{charge: [4]int64{0, 0, 0, usage.Units}}); err != nil {
					return err
				}
			case ledger.TypeRuntimeWorkAdmitted:
				if !own {
					continue
				}
				admission, err := ledger.DecodeRuntimeWorkAdmitted(record.Payload)
				if err != nil {
					return err
				}
				if !slices.Contains(admission.Scopes, scope) {
					continue
				}
				work[admission.WorkID] = scopedWorkState{admission: admission}
				holds := [4]int64{1, 0, 0, 0}
				if admission.Kind == "local" {
					holds = [4]int64{0, 1, *admission.MaxBytes, 1}
				}
				if err := apply(budgetDelta{hold: holds}); err != nil {
					return err
				}
			case ledger.TypeRuntimeWorkDispatched:
				if !own {
					continue
				}
				dispatch, err := ledger.DecodeRuntimeWorkDispatched(record.Payload)
				if err != nil {
					return err
				}
				item, found := work[dispatch.WorkID]
				if !found {
					continue
				}
				if err := apply(budgetDelta{charge: [4]int64{0, 0, 0, 1}}); err != nil {
					return err
				}
				item.dispatched = true
				work[dispatch.WorkID] = item
			case ledger.TypeRuntimeWorkEnded:
				if !own {
					continue
				}
				ending, err := ledger.DecodeRuntimeWorkEnded(record.Payload)
				if err != nil {
					return err
				}
				item, found := work[ending.WorkID]
				if !found || !releasesWork(item.admission.Kind, ending.Outcome) {
					continue
				}
				releases := [4]int64{1, 0, 0, 0}
				if item.admission.Kind == "local" {
					releases = [4]int64{0, 1, *item.admission.MaxBytes, 0}
					if !item.dispatched {
						releases[unitExposure] = 1
					}
				}
				if err := apply(budgetDelta{release: releases}); err != nil {
					return err
				}
				delete(work, ending.WorkID)
			}
		}
		return nil
	})
	return account, err
}

func capacityExposures(account budgetAccount, prepared [4]int64, unlimited bool) (held, used, available Exposure, err error) {
	limits := capacityAmounts(account.limits)
	if unlimited {
		limits = [4]int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
	}
	var h, u, a [4]int64
	for i, counter := range account.usage {
		if prepared[i] > math.MaxInt64-counter.held {
			return held, used, available, ErrInvalid
		}
		h[i], u[i] = counter.held+prepared[i], counter.consumed
		if u[i] <= limits[i] && h[i] <= limits[i]-u[i] {
			a[i] = limits[i] - u[i] - h[i]
		}
	}
	return Exposure{h[0], h[1], h[2], h[3]}, Exposure{u[0], u[1], u[2], u[3]}, Exposure{a[0], a[1], a[2], a[3]}, nil
}

// Capacity has no selected concrete byte size here. The band reports whether a
// minimal operation has room; it never promises admission of a later request.
func (s *Service) capacityBand(origin ledger.Origin, chain []grantRules) (string, error) {
	scopes, err := chainScopes(origin, chain)
	if err != nil {
		return "", err
	}
	turn := slices.Contains(chain[0].operations, "turn.start")
	local := false
	for _, operation := range chain[0].operations {
		if operation != "grant.revoke" && operation != "context.inspect" && operation != "task.inspect" && operation != "turn.inspect" && operation != "turn.cancel" && operation != "turn.start" {
			local = true
		}
	}
	if !turn && !local {
		return "admitted", nil
	}
	turnRoom, localRoom := turn, local
	for _, scope := range scopes {
		account := s.p.book.accounts[scope]
		if !account.configured && scope.Kind != "context" {
			return "busy", nil
		}
		_, _, available, err := capacityExposures(account, s.p.book.preparing[scope], !account.configured)
		if err != nil {
			return "", err
		}
		turnRoom = turnRoom && available.TurnSlots > 0
		localRoom = localRoom && available.LocalSlots > 0 && available.LocalUnits > 0
	}
	for _, work := range s.p.work {
		if work.admission.Kind == "turn" && !work.released && work.origin.ContextID == origin.ContextID {
			turnRoom = false
		}
	}
	if turnRoom || localRoom {
		return "admitted", nil
	}
	return "busy", nil
}

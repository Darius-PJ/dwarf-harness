package agents

import (
	"context"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) GrantStatus(ctx context.Context, request GrantLookup) (GrantView, error) {
	return s.grantStatus(ctx, nil, request)
}
func (a *Agent) GrantStatus(ctx context.Context, request GrantLookup) (GrantView, error) {
	if a == nil || a.service == nil {
		return GrantView{}, ErrNotBound
	}
	return a.service.grantStatus(ctx, &a.ids, request)
}
func (s *Service) grantStatus(ctx context.Context, bound *submissionIDs, request GrantLookup) (GrantView, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return GrantView{}, err
	}
	defer done()
	if !validID(request.GrantID) {
		return GrantView{}, ErrInvalid
	}
	grant, found, err := s.loadGrant(ctx, request.GrantID, s.p.through)
	if err != nil {
		return GrantView{}, err
	}
	if bound != nil && (!found || grant.definition.Grantee != ledger.Binding(bindingOf(originOf(*bound)))) {
		return GrantView{}, ErrDenied
	}
	if !found {
		return GrantView{}, ErrUnavailable
	}
	return s.grantView(ctx, grant)
}

func (s *Service) grantLocalCurrent(ctx context.Context, grant grantState) (bool, string, error) {
	if grant.revoked {
		return false, "revoked", nil
	}
	if grant.definition.Lifetime != "until_revoked" {
		return false, "lifetime_not_current", nil
	}
	if binding := grant.definition.TaskBinding; binding != nil {
		task, found, err := s.loadTask(ctx, binding.TaskID, s.p.through)
		if err != nil {
			return false, "", err
		}
		if !found || task.record.Assignment == nil || task.record.Assignment.AssignmentID != binding.AssignmentID || task.record.Assignment.Generation != binding.Generation {
			return false, "generation_not_current", nil
		}
	}
	return true, "", nil
}

func (s *Service) grantView(ctx context.Context, grant grantState) (GrantView, error) {
	encoded, err := grant.definition.Encode()
	if err != nil {
		return GrantView{}, err
	}
	copy, err := ledger.DecodeRuntimeGrantIssued(encoded)
	if err != nil {
		return GrantView{}, err
	}
	view := GrantView{Grant: copy, IssuerActor: grant.issuer, Issuer: grant.origin, Revoked: grant.revoked, ThroughSequence: s.p.through}
	chain := []grantState{grant}
	seen := map[uuid.UUID]bool{grant.definition.GrantID: true}
	for id := grant.definition.ParentGrantID; id != (uuid.UUID{}); {
		if seen[id] {
			return GrantView{}, ErrInvalid
		}
		seen[id] = true
		parent, found, err := s.loadGrant(ctx, id, s.p.through)
		if err != nil {
			return GrantView{}, err
		}
		if !found {
			return GrantView{}, ErrUnavailable
		}
		chain = append(chain, parent)
		id = parent.definition.ParentGrantID
	}
	view.Ancestors = make([]GrantAncestor, len(chain)-1)
	current := true
	effective := chain[len(chain)-1].definition.Limits
	for i := len(chain) - 1; i >= 0; i-- {
		local, reason, err := s.grantLocalCurrent(ctx, chain[i])
		if err != nil {
			return GrantView{}, err
		}
		current = current && local
		limit := chain[i].definition.Limits
		effective = ledger.Capacity{TurnSlots: min(effective.TurnSlots, limit.TurnSlots), LocalSlots: min(effective.LocalSlots, limit.LocalSlots), BufferBytes: min(effective.BufferBytes, limit.BufferBytes), LocalUnits: min(effective.LocalUnits, limit.LocalUnits), MaxOperationBytes: min(effective.MaxOperationBytes, limit.MaxOperationBytes)}
		if i == 0 {
			view.Current, view.EffectiveLimits = current, effective
			if !local {
				view.Reason = reason
			} else if !current {
				view.Reason = "ancestor_not_current"
			}
		} else {
			view.Ancestors[i-1] = GrantAncestor{GrantID: chain[i].definition.GrantID, Current: current, EffectiveLimits: effective}
		}
	}
	return view, nil
}

func (s *Service) CapacityStatus(ctx context.Context, request CapacityLookup) (CapacityView, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return CapacityView{}, err
	}
	defer done()
	scope := request.Scope
	switch scope.Kind {
	case "colony":
		if scope.ID != (uuid.UUID{}) {
			return CapacityView{}, ErrInvalid
		}
	case "agent":
		if _, found := s.p.agents[scope.ID]; !found {
			return CapacityView{}, ErrUnavailable
		}
	case "context":
		if _, found := s.p.contexts[scope.ID]; !found {
			return CapacityView{}, ErrUnavailable
		}
	case "grant":
		if _, found, err := s.loadGrant(ctx, scope.ID, s.p.through); err != nil {
			return CapacityView{}, err
		} else if !found {
			return CapacityView{}, ErrUnavailable
		}
	default:
		return CapacityView{}, ErrInvalid
	}
	account, found := s.p.book.accounts[scope]
	if !found {
		account, err = s.budgetHistory(ctx, scope, nil)
		if err != nil {
			return CapacityView{}, err
		}
	}
	held, used, available, err := capacityExposures(account, s.p.book.preparing[scope], !account.configured && scope.Kind == "context")
	if err != nil {
		return CapacityView{}, err
	}
	view := CapacityView{Scope: scope, Version: account.version, Configured: account.configured, Limits: account.limits, Held: held, Used: used, Band: "busy", ThroughSequence: s.p.through}
	if account.configured {
		view.Available = &available
	}
	if available.TurnSlots > 0 || available.LocalSlots > 0 && available.LocalUnits > 0 {
		view.Band = "admitted"
	}
	return view, nil
}

func (a *Agent) CapacityStatus(ctx context.Context, request GrantLookup) (CapacityView, error) {
	if a == nil || a.service == nil {
		return CapacityView{}, ErrNotBound
	}
	s := a.service
	done, err := s.scopedQuery(ctx, &a.ids)
	if err != nil {
		return CapacityView{}, err
	}
	defer done()
	if !validID(request.GrantID) {
		return CapacityView{}, ErrInvalid
	}
	grant, found, err := s.loadGrant(ctx, request.GrantID, s.p.through)
	if err != nil {
		return CapacityView{}, err
	}
	origin := originOf(a.ids)
	if !found || grant.definition.Grantee != ledger.Binding(bindingOf(origin)) {
		return CapacityView{}, ErrDenied
	}
	scope := ledger.ScopeKey{Kind: "grant", ID: request.GrantID}
	account, err := s.budgetHistory(ctx, scope, &origin)
	if err != nil {
		return CapacityView{}, err
	}
	held, used, _, err := capacityExposures(account, s.p.book.originPreparing[origin][scope], false)
	if err != nil {
		return CapacityView{}, err
	}
	view := CapacityView{Scope: scope, Version: account.version, Configured: account.configured, Limits: grant.definition.Limits, Held: held, Used: used, Band: "denied", ThroughSequence: s.p.through}
	chain, err := s.currentChain(ctx, origin, request.GrantID, s.p.through)
	if errors.Is(err, ErrDenied) {
		return view, nil
	}
	if err != nil {
		return CapacityView{}, err
	}
	view.Band, err = s.capacityBand(origin, chain)
	return view, err
}

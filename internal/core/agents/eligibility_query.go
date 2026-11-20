package agents

import (
	"context"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func eligibilityRefusalSupported(refusal *ledger.EligibilityRefusal) bool {
	if refusal == nil || refusal.Stage != "turn_admission" {
		return false
	}
	switch refusal.Code {
	case "denied", "unavailable", "invalid_argument", "too_large", "integrity":
		return true
	}
	return false
}

func eligibilityView(e eligibilityState, through uint64) session.EligibilityView {
	v := e.current()
	view := session.EligibilityView{State: v.State, Reason: v.Reason, Version: v.Version, OperationID: v.OperationID, ThroughSequence: through}
	if v.FirstAction == nil {
		return view
	}
	first := &session.EligibilityFirstAction{Declaration: *v.FirstAction, Disposition: "not_admitted", NativeSequence: e.nativeSequence, BoundSequence: e.boundSequence, NativeEndingSequence: e.nativeEndingSequence, FirstAdmittedSequence: e.firstAdmittedSequence}
	if e.firstAdmittedSequence != 0 {
		admitted := e.firstAdmitted
		if admitted.TaskBinding != nil {
			admitted.TaskBinding = new(*admitted.TaskBinding)
		}
		first.FirstAdmittedTurn = &admitted
	}
	switch {
	case e.nativeEndingSequence != 0:
		first.Disposition = "terminal"
	case e.boundSequence != 0:
		first.Disposition = "bound"
	case e.nativeSequence != 0:
		first.Disposition = "admitted_unbound"
	case e.outcome != "recorded":
		first.Disposition = "unsupported"
	case v.FirstAction.Refusal != nil:
		first.Disposition = "admission_refused"
		if !eligibilityRefusalSupported(v.FirstAction.Refusal) {
			first.Disposition = "unsupported"
		}
	}
	view.FirstAction = first
	return view
}

// EligibilityStatus is the trusted owner's full, detached projection.
func (s *Service) EligibilityStatus(ctx context.Context, agentID uuid.UUID) (session.EligibilityView, error) {
	return queryHistory(ctx, s, nil, func(*historyReadSet) (session.EligibilityView, error) {
		if !validID(agentID) {
			return session.EligibilityView{}, ErrInvalid
		}
		agent, found := s.p.agents[agentID]
		if !found {
			return session.EligibilityView{}, ErrUnavailable
		}
		return eligibilityView(agent.eligibility, s.p.through), nil
	})
}

// EligibilityStatus never accepts a target agent: a bound Agent can inspect only
// itself, with an inspect grant for its current context.
func (a *Agent) EligibilityStatus(ctx context.Context, grantID uuid.UUID) (session.EligibilityView, error) {
	if a == nil || a.service == nil {
		return session.EligibilityView{}, ErrNotBound
	}
	s := a.service
	return queryHistory(ctx, s, &a.ids, func(reads *historyReadSet) (session.EligibilityView, error) {
		if !validID(grantID) {
			return session.EligibilityView{}, ErrInvalid
		}
		origin := originOf(a.ids)
		target := eligibilityInspectTarget(origin)
		if _, err := s.authorize(ctx, reads, origin, grantID, "context.inspect", []ledger.Resource{target}, nil, 0, s.p.through); err != nil {
			return session.EligibilityView{}, err
		}
		view := eligibilityView(s.p.agents[a.ids.agent].eligibility, s.p.through)
		if view.FirstAction != nil && view.FirstAction.Declaration.PermittedTurn.ContextID != a.ids.context {
			view.FirstAction = nil
		}
		return view, nil
	})
}

func eligibilityInspectTarget(origin ledger.Origin) ledger.Resource {
	return ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{}}
}

// filterEligibility runs in a prepared read phase, including on a Since page
// assembled outside mu. Authority lost during a read cannot disclose old data.
func (s *Service) filterEligibility(ctx context.Context, reads *historyReadSet, origin ledger.Origin, view session.EligibilityView) (session.EligibilityView, error) {
	if view.FirstAction == nil {
		return view, nil
	}
	if view.FirstAction.Declaration.PermittedTurn.ContextID != origin.ContextID {
		view.FirstAction = nil
		return view, nil
	}
	_, err := s.loopGrantLocked(ctx, reads, origin, "context.inspect", []ledger.Resource{eligibilityInspectTarget(origin)}, 0)
	if errors.Is(err, ErrDenied) || errors.Is(err, ErrUnavailable) {
		view.FirstAction = nil
		return view, nil
	}
	return view, err
}

package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// A plan owns just the one replacement snapshot. History remains in the ledger.
type registrationPlan struct {
	resource    *ResourceRegistrationView
	domain      *WorkDomainRegistrationView
	declaration *DeclarationView
}

func registrationRevision(expected, current int64) error {
	if expected != current {
		return &RevisionError{Expected: uint64(expected), Current: uint64(current)}
	}
	return nil
}

func registrationAudienceWithin(audience, source []uuid.UUID) bool {
	for _, id := range audience {
		if !slices.Contains(source, id) {
			return false
		}
	}
	return true
}

func (s *Service) deriveRegistration(ctx context.Context, sub ledger.RegistrationSubmission, first uint64) (registrationPlan, error) {
	var plan registrationPlan
	if sub.Actor != "owner" {
		return plan, ErrDenied
	}
	if !s.p.registeredOrigin(sub.Origin) {
		return plan, ErrNotBound
	}
	var audience []uuid.UUID
	switch a := sub.Arguments.(type) {
	case ledger.ResourceRegistrationArguments:
		old := s.p.resources[a.ResourceID]
		if err := registrationRevision(a.ExpectedVersion, old.Record.Version); err != nil {
			return plan, err
		}
		if old.Record.Version != 0 && !sameScopedValue(old.Record.Resource, a.Resource) {
			return plan, ErrInvalid
		}
		for id, existing := range s.p.resources {
			if id != a.ResourceID && existing.Record.Resource.ResourceKey == a.Resource.ResourceKey {
				return plan, ErrInvalid
			}
		}
		audience = a.Audience
		plan.resource = &ResourceRegistrationView{
			Record: ledger.RuntimeResourceRegistered{ResourceID: a.ResourceID, ExpectedVersion: a.ExpectedVersion, Version: a.ExpectedVersion + 1, Resource: a.Resource, Audience: slices.Clone(a.Audience)},
			Origin: sub.Origin, EffectSequence: first + 1, ReceiptSequence: first + 2,
		}
	case ledger.WorkDomainRegistrationArguments:
		old := s.p.domains[a.WorkDomainID]
		if err := registrationRevision(a.ExpectedVersion, old.Record.Version); err != nil {
			return plan, err
		}
		audience = a.Audience
		plan.domain = &WorkDomainRegistrationView{
			Record: ledger.RuntimeWorkDomainRegistered{WorkDomainID: a.WorkDomainID, ExpectedVersion: a.ExpectedVersion, Version: a.ExpectedVersion + 1, Audience: slices.Clone(a.Audience)},
			Origin: sub.Origin, EffectSequence: first + 1, ReceiptSequence: first + 2,
		}
	case ledger.DeclarationArguments:
		old := s.p.declarations[a.DeclarationID]
		if err := registrationRevision(a.ExpectedVersion, old.Record.Version); err != nil {
			return plan, err
		}
		if old.Record.Version != 0 && (old.Record.Subject.Kind != a.Subject.Kind || old.Record.Subject.ID != a.Subject.ID) {
			return plan, ErrInvalid
		}
		var sourceAudience []uuid.UUID
		var version int64
		switch a.Subject.Kind {
		case "resource":
			source := s.p.resources[a.Subject.ID].Record
			sourceAudience, version = source.Audience, source.Version
		case "work_domain":
			source := s.p.domains[a.Subject.ID].Record
			sourceAudience, version = source.Audience, source.Version
		default:
			return plan, ErrInvalid
		}
		if version == 0 {
			return plan, ErrUnavailable
		}
		if err := registrationRevision(a.Subject.Version, version); err != nil {
			return plan, err
		}
		if !registrationAudienceWithin(a.Audience, sourceAudience) {
			return plan, ErrInvalid
		}
		for _, consumer := range a.Consumers {
			switch consumer.Kind {
			case "task":
				task, found, err := s.loadTask(ctx, consumer.TaskID, s.p.through)
				if err != nil {
					return plan, err
				}
				if !found {
					return plan, ErrUnavailable
				}
				if !registrationAudienceWithin(a.Audience, task.record.Audience) {
					return plan, ErrInvalid
				}
			case "context":
				if _, found := s.p.agents[consumer.Binding.AgentID]; !found || s.p.contexts[consumer.Binding.ContextID] != consumer.Binding.AgentID {
					return plan, ErrNotBound
				}
				if !slices.Contains(a.Audience, consumer.Binding.AgentID) {
					return plan, ErrInvalid
				}
			default:
				return plan, ErrInvalid
			}
		}
		audience = a.Audience
		plan.declaration = &DeclarationView{
			Record: ledger.RuntimeDeclarationChanged{DeclarationID: a.DeclarationID, ExpectedVersion: a.ExpectedVersion, Version: a.ExpectedVersion + 1, Subject: a.Subject, Audience: slices.Clone(a.Audience), Consumers: slices.Clone(a.Consumers)},
			Origin: sub.Origin, EffectSequence: first + 1, ReceiptSequence: first + 2,
		}
	default:
		return plan, ErrInvalid
	}
	for _, id := range audience {
		if _, found := s.p.agents[id]; !found {
			return registrationPlan{}, ErrNotBound
		}
	}
	return plan, nil
}

func (s *Service) planRegistration(ctx context.Context, g *registrationGroup, first uint64) (registrationPlan, error) {
	plan, err := s.deriveRegistration(ctx, g.receipt.Submission, first)
	if err != nil {
		return registrationPlan{}, err
	}
	if plan.resource != nil && !sameScopedValue(plan.resource.Record, g.resource) ||
		plan.domain != nil && !sameScopedValue(plan.domain.Record, g.domain) ||
		plan.declaration != nil && !sameScopedValue(plan.declaration.Record, g.declaration) {
		return registrationPlan{}, inconsistent(first, "registration effect differs from request and state")
	}
	return plan, nil
}

func (p *projection) installRegistration(plan registrationPlan) {
	if v := plan.resource; v != nil {
		p.resources[v.Record.ResourceID] = *v
	}
	if v := plan.domain; v != nil {
		p.domains[v.Record.WorkDomainID] = *v
	}
	if v := plan.declaration; v != nil {
		p.declarations[v.Record.DeclarationID] = *v
	}
}

func (s *Service) registrationProducerGuard(sub ledger.RegistrationSubmission) error {
	registration := false
	switch a := sub.Arguments.(type) {
	case ledger.ResourceRegistrationArguments:
		registration = a.ExpectedVersion == 0
	case ledger.WorkDomainRegistrationArguments:
		registration = a.ExpectedVersion == 0
	case ledger.DeclarationArguments:
		if a.ExpectedVersion == 0 && len(s.p.declarations) >= s.limits.MaxDeclarations {
			return &BusyError{Reason: "declarations"}
		}
	}
	if registration && len(s.p.resources)+len(s.p.domains) >= s.limits.MaxRegistrations {
		return &BusyError{Reason: "registrations"}
	}
	return nil
}

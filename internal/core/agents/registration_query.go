package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func copyResourceRegistration(v ResourceRegistrationView, through uint64) ResourceRegistrationView {
	v.Record.Audience = slices.Clone(v.Record.Audience)
	v.Record.Resource.Slots = slices.Clone(v.Record.Resource.Slots)
	v.ThroughSequence = through
	return v
}

func copyWorkDomainRegistration(v WorkDomainRegistrationView, through uint64) WorkDomainRegistrationView {
	v.Record.Audience = slices.Clone(v.Record.Audience)
	v.ThroughSequence = through
	return v
}

func copyDeclaration(v DeclarationView, through uint64) DeclarationView {
	v.Record.Audience = slices.Clone(v.Record.Audience)
	v.Record.Consumers = slices.Clone(v.Record.Consumers)
	v.ThroughSequence = through
	return v
}

// selectRegistration only selects complete snapshots in an already validated
// prefix. It never reduces historical records against today's projection.
func (s *Service) selectRegistration(ctx context.Context, kind string, id uuid.UUID, address string, version int64, through uint64) (registrationPlan, error) {
	var selected registrationPlan
	if through == ledger.ToEnd {
		through = s.p.through
	}
	if through > s.p.through {
		return selected, ledger.ErrCursor
	}
	if through == s.p.through {
		switch kind {
		case "resource":
			for key, v := range s.p.resources {
				if (id == (uuid.UUID{}) && v.Record.Resource.ResourceKey == address || key == id) && (version == 0 || version == v.Record.Version) {
					v = copyResourceRegistration(v, through)
					selected.resource = &v
				}
			}
		case "work_domain":
			if v, found := s.p.domains[id]; found && (version == 0 || version == v.Record.Version) {
				v = copyWorkDomainRegistration(v, through)
				selected.domain = &v
			}
		case "declaration":
			if v, found := s.p.declarations[id]; found {
				v = copyDeclaration(v, through)
				selected.declaration = &v
			}
		}
		if version == 0 || selected.resource != nil || selected.domain != nil {
			return selected, nil
		}
	}
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
		_, _, _, _, g, _, err := decodeFamily(draftsOf(c), c.First)
		if err != nil || g == nil {
			return err
		}
		origin := g.receipt.Submission.Origin
		switch kind {
		case "resource":
			if p := g.resource; p != nil && (id == (uuid.UUID{}) && p.Resource.ResourceKey == address || p.ResourceID == id) && (version == 0 || p.Version == version) {
				v := copyResourceRegistration(ResourceRegistrationView{Record: *p, Origin: origin, EffectSequence: c.First + 1, ReceiptSequence: c.Last}, through)
				selected.resource = &v
			}
		case "work_domain":
			if p := g.domain; p != nil && p.WorkDomainID == id && (version == 0 || p.Version == version) {
				v := copyWorkDomainRegistration(WorkDomainRegistrationView{Record: *p, Origin: origin, EffectSequence: c.First + 1, ReceiptSequence: c.Last}, through)
				selected.domain = &v
			}
		case "declaration":
			if p := g.declaration; p != nil && p.DeclarationID == id {
				v := copyDeclaration(DeclarationView{Record: *p, Origin: origin, EffectSequence: c.First + 1, ReceiptSequence: c.Last}, through)
				selected.declaration = &v
			}
		}
		return nil
	})
	return selected, err
}

// ResourceStatus is an owner-only metadata query by exact external grant address.
func (s *Service) ResourceStatus(ctx context.Context, lookup ResourceRegistrationLookup) (ResourceRegistrationView, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return ResourceRegistrationView{}, err
	}
	defer done()
	if lookup.Resource.Kind != "external" || !validResource(lookup.Resource) {
		return ResourceRegistrationView{}, ErrInvalid
	}
	plan, err := s.selectRegistration(ctx, "resource", uuid.UUID{}, lookup.Resource.ResourceKey, 0, lookup.ThroughSequence)
	if err != nil {
		return ResourceRegistrationView{}, err
	}
	if plan.resource == nil {
		return ResourceRegistrationView{}, ErrUnavailable
	}
	return *plan.resource, nil
}

func (s *Service) WorkDomainStatus(ctx context.Context, lookup RegistrationLookup) (WorkDomainRegistrationView, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return WorkDomainRegistrationView{}, err
	}
	defer done()
	if !validID(lookup.ID) {
		return WorkDomainRegistrationView{}, ErrInvalid
	}
	plan, err := s.selectRegistration(ctx, "work_domain", lookup.ID, "", 0, lookup.ThroughSequence)
	if err != nil {
		return WorkDomainRegistrationView{}, err
	}
	if plan.domain == nil {
		return WorkDomainRegistrationView{}, ErrUnavailable
	}
	return *plan.domain, nil
}

func (s *Service) DeclarationStatus(ctx context.Context, lookup RegistrationLookup) (DeclarationView, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return DeclarationView{}, err
	}
	defer done()
	if !validID(lookup.ID) {
		return DeclarationView{}, ErrInvalid
	}
	plan, err := s.selectRegistration(ctx, "declaration", lookup.ID, "", 0, lookup.ThroughSequence)
	if err != nil {
		return DeclarationView{}, err
	}
	if plan.declaration == nil {
		return DeclarationView{}, ErrUnavailable
	}
	return *plan.declaration, nil
}

func (s *Service) RegistrationVersion(ctx context.Context, lookup RegistrationVersionLookup) (RegistrationEvidence, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return RegistrationEvidence{}, err
	}
	defer done()
	ref := lookup.Ref
	if !validID(ref.ID) || ref.Version <= 0 || ref.Kind != "resource" && ref.Kind != "work_domain" {
		return RegistrationEvidence{}, ErrInvalid
	}
	plan, err := s.selectRegistration(ctx, ref.Kind, ref.ID, "", ref.Version, lookup.ThroughSequence)
	if err != nil {
		return RegistrationEvidence{}, err
	}
	if plan.resource == nil && plan.domain == nil {
		return RegistrationEvidence{}, ErrUnavailable
	}
	return RegistrationEvidence{Resource: plan.resource, WorkDomain: plan.domain}, nil
}

func (s *Service) RegistrationCommandStatus(ctx context.Context, lookup RegistrationCommandLookup) (RegistrationReceipt, error) {
	done, err := s.scopedQuery(ctx, nil)
	if err != nil {
		return RegistrationReceipt{}, err
	}
	defer done()
	if !validID(lookup.CommandID) || lookup.SubmissionSHA256 == (ledger.Digest{}) {
		return RegistrationReceipt{}, ErrInvalid
	}
	match, err := s.findCommand(ctx, lookup.CommandID, 0, s.p.through)
	if err != nil {
		return RegistrationReceipt{}, err
	}
	if !match.found {
		return RegistrationReceipt{}, ErrUnavailable
	}
	if match.registration == nil || match.command.SubmissionSHA256 != lookup.SubmissionSHA256 {
		return RegistrationReceipt{}, ErrCommandMismatch
	}
	return registrationReceipt(match.registration), nil
}

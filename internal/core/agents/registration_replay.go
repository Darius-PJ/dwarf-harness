package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type registrationGroup struct {
	first       uint64
	command     ledger.Command
	receipt     ledger.RuntimeRegistrationReceipt
	resource    *ledger.RuntimeResourceRegistered
	domain      *ledger.RuntimeWorkDomainRegistered
	declaration *ledger.RuntimeDeclarationChanged
}

func isRegistrationType(typ string) bool {
	switch typ {
	case ledger.TypeRuntimeResourceRegistered, ledger.TypeRuntimeWorkDomainRegistered, ledger.TypeRuntimeDeclarationChanged, ledger.TypeRuntimeRegistrationReceipt:
		return true
	default:
		return false
	}
}

func decodeRegistrationGroup(d []ledger.Draft, first uint64) (*registrationGroup, error) {
	bad := func(reason string) (*registrationGroup, error) { return nil, inconsistent(first, reason) }
	if len(d) != 3 || d[0].Type != ledger.TypeCommand || d[2].Type != ledger.TypeRuntimeRegistrationReceipt {
		return bad("registration group boundaries")
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad("registration command")
	}
	receipt, err := ledger.DecodeRuntimeRegistrationReceipt(d[2].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 || receipt.Outcome != "applied" {
		return bad("registration receipt binding or outcome")
	}
	sub := receipt.Submission
	for _, draft := range d {
		if draft.TypeVersion != 1 || draft.Actor != ledger.ActorOwner || draft.AgentID != sub.Origin.AgentID || draft.ExecutionSessionID != sub.Origin.SessionID || draft.ContextID != sub.Origin.ContextID || draft.OperationID != command.CommandID || draft.TurnID != (uuid.UUID{}) || draft.MeasurementPath != "" || len(draft.BodyRefs) != 0 {
			return bad("registration envelope")
		}
	}
	g := &registrationGroup{first: first, command: command, receipt: receipt}
	switch sub.Operation {
	case "resource.set":
		if d[1].Type != ledger.TypeRuntimeResourceRegistered {
			return bad("registration effect order")
		}
		p, e := ledger.DecodeRuntimeResourceRegistered(d[1].Payload)
		g.resource, err = &p, e
	case "work_domain.set":
		if d[1].Type != ledger.TypeRuntimeWorkDomainRegistered {
			return bad("registration effect order")
		}
		p, e := ledger.DecodeRuntimeWorkDomainRegistered(d[1].Payload)
		g.domain, err = &p, e
	case "declaration.set":
		if d[1].Type != ledger.TypeRuntimeDeclarationChanged {
			return bad("registration effect order")
		}
		p, e := ledger.DecodeRuntimeDeclarationChanged(d[1].Payload)
		g.declaration, err = &p, e
	default:
		return bad("registration operation")
	}
	if err != nil {
		return bad("registration effect payload")
	}
	return g, nil
}

func (s *Service) preflightRegistration(ctx context.Context, input registrationInput, drafts []ledger.Draft) error {
	private, scoped, review, paid, g, err := decodeFamily(drafts, s.p.through+1)
	if err != nil {
		return err
	}
	if private != nil || scoped != nil || review != nil || paid != nil || g == nil || g.command.CommandID != input.id || g.command.SubmissionSHA256 != input.digest || !sameScopedValue(g.receipt.Submission, input.sub) {
		return inconsistent(s.p.through+1, "registration preflight request binding")
	}
	_, err = s.planRegistration(ctx, g, s.p.through+1)
	return err
}

func registrationReceipt(g *registrationGroup) RegistrationReceipt {
	r := g.receipt
	switch a := r.Submission.Arguments.(type) {
	case ledger.ResourceRegistrationArguments:
		a.Audience = slices.Clone(a.Audience)
		a.Resource.Slots = slices.Clone(a.Resource.Slots)
		r.Submission.Arguments = a
	case ledger.WorkDomainRegistrationArguments:
		a.Audience = slices.Clone(a.Audience)
		r.Submission.Arguments = a
	case ledger.DeclarationArguments:
		a.Audience = slices.Clone(a.Audience)
		a.Consumers = slices.Clone(a.Consumers)
		r.Submission.Arguments = a
	}
	return RegistrationReceipt{RuntimeRegistrationReceipt: r, EffectSequence: g.first + 1, Sequence: g.first + 2}
}

func matchRegistrationReceipt(match commandMatch, input registrationInput) (RegistrationReceipt, error) {
	if match.registration == nil {
		return RegistrationReceipt{}, ErrCommandMismatch
	}
	sub := match.registration.receipt.Submission
	if sub.Actor != input.sub.Actor || sub.Origin != input.sub.Origin {
		return RegistrationReceipt{}, ErrDenied
	}
	if sub.Operation != input.sub.Operation || match.command.SubmissionSHA256 != input.digest {
		return RegistrationReceipt{}, ErrCommandMismatch
	}
	return registrationReceipt(match.registration), nil
}

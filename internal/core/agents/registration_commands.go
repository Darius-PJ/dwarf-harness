package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type registrationInput struct {
	id     uuid.UUID
	sub    ledger.RegistrationSubmission
	digest ledger.Digest
}

// These are trusted owner/host entrances, not authentication of Binding text.
func (s *Service) SetResourceRegistration(ctx context.Context, origin Binding, request ResourceRegistration) (RegistrationReceipt, error) {
	return s.executeRegistration(ctx, scopedCaller{actor: "owner", binding: origin}, "resource.set", request)
}

func (s *Service) SetWorkDomainRegistration(ctx context.Context, origin Binding, request WorkDomainRegistration) (RegistrationReceipt, error) {
	return s.executeRegistration(ctx, scopedCaller{actor: "owner", binding: origin}, "work_domain.set", request)
}

func (s *Service) SetDeclaration(ctx context.Context, origin Binding, request DeclarationChange) (RegistrationReceipt, error) {
	return s.executeRegistration(ctx, scopedCaller{actor: "owner", binding: origin}, "declaration.set", request)
}

// The raw compact size check distinguishes service size refusal from malformed
// values; the frozen encoder then validates all known fields and canonical sets.
func encodeRegistrationValue(value interface {
	Encode() (json.RawMessage, error)
}) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	if len(encoded) > ledger.MaxRegistrationPayloadBytes {
		return nil, ErrTooLarge
	}
	encoded, err = value.Encode()
	if err != nil {
		return nil, ErrInvalid
	}
	return encoded, nil
}

func makeRegistrationInput(ids submissionIDs, operation string, request any) (registrationInput, error) {
	input := registrationInput{sub: ledger.RegistrationSubmission{Schema: 5, Actor: "owner", Origin: originOf(ids), Operation: operation}}
	switch r := request.(type) {
	case ResourceRegistration:
		if operation != "resource.set" || r.ExpectedVersion >= math.MaxInt64 || r.Resource.Kind != "external" || !validResource(r.Resource) {
			return input, ErrInvalid
		}
		if !boundedArrays(len(r.Audience)) {
			return input, ErrTooLarge
		}
		input.id = r.CommandID
		input.sub.Arguments = ledger.ResourceRegistrationArguments{ResourceID: r.ResourceID, ExpectedVersion: int64(r.ExpectedVersion), Resource: r.Resource, Audience: normalizeIDs(r.Audience)}
	case WorkDomainRegistration:
		if operation != "work_domain.set" || r.ExpectedVersion >= math.MaxInt64 {
			return input, ErrInvalid
		}
		if !boundedArrays(len(r.Audience)) {
			return input, ErrTooLarge
		}
		input.id = r.CommandID
		input.sub.Arguments = ledger.WorkDomainRegistrationArguments{WorkDomainID: r.WorkDomainID, ExpectedVersion: int64(r.ExpectedVersion), Audience: normalizeIDs(r.Audience)}
	case DeclarationChange:
		if operation != "declaration.set" || r.ExpectedVersion >= math.MaxInt64 {
			return input, ErrInvalid
		}
		if !boundedArrays(len(r.Audience), len(r.Consumers)) {
			return input, ErrTooLarge
		}
		consumers := append([]ledger.DeclarationConsumer{}, r.Consumers...)
		for _, consumer := range consumers {
			if _, err := consumer.MarshalJSON(); err != nil {
				return input, ErrInvalid
			}
		}
		slices.SortFunc(consumers, func(a, b ledger.DeclarationConsumer) int {
			left, _ := a.MarshalJSON()
			right, _ := b.MarshalJSON()
			return bytes.Compare(left, right)
		})
		input.id = r.CommandID
		input.sub.Arguments = ledger.DeclarationArguments{DeclarationID: r.DeclarationID, ExpectedVersion: int64(r.ExpectedVersion), Subject: r.Subject, Audience: normalizeIDs(r.Audience), Consumers: consumers}
	default:
		return input, ErrInvalid
	}
	if !validID(input.id) {
		return input, ErrInvalid
	}
	encoded, err := encodeRegistrationValue(input.sub)
	if err != nil {
		return input, err
	}
	input.digest = ledger.Digest(sha256.Sum256(encoded))
	input.sub, err = ledger.DecodeRegistrationSubmission(encoded)
	return input, err
}

func (s *Service) refreshRegistrationLocked(ctx context.Context, sub ledger.RegistrationSubmission) error {
	if err := s.usableLocked(ctx); err != nil {
		return err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return err
	}
	if sub.Actor != "owner" {
		return ErrDenied
	}
	if !s.p.registeredOrigin(sub.Origin) {
		return ErrNotBound
	}
	return nil
}

// Returns with mu held. Scan the captured old prefix without mu, then refresh
// and scan the tail under mu to close a concurrent same-command publication.
func (s *Service) lookupRegistration(ctx context.Context, input registrationInput) (commandMatch, error) {
	s.mu.Lock()
	if err := s.refreshRegistrationLocked(ctx, input.sub); err != nil {
		return commandMatch{}, err
	}
	through := s.p.through
	old := bytes.Compare(input.id[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var match commandMatch
	var err error
	if old {
		match, err = s.findCommand(ctx, input.id, 0, through)
	}
	s.mu.Lock()
	if err != nil {
		return match, err
	}
	if err := s.refreshRegistrationLocked(ctx, input.sub); err != nil {
		return match, err
	}
	if !match.found && through < s.p.through {
		match, err = s.findCommand(ctx, input.id, through, s.p.through)
	}
	return match, err
}

func registrationDrafts(input registrationInput, plan registrationPlan) ([]ledger.Draft, error) {
	var effect interface {
		Encode() (json.RawMessage, error)
	}
	var typ string
	var version int64
	switch {
	case plan.resource != nil:
		effect, typ, version = plan.resource.Record, ledger.TypeRuntimeResourceRegistered, plan.resource.Record.Version
	case plan.domain != nil:
		effect, typ, version = plan.domain.Record, ledger.TypeRuntimeWorkDomainRegistered, plan.domain.Record.Version
	case plan.declaration != nil:
		effect, typ, version = plan.declaration.Record, ledger.TypeRuntimeDeclarationChanged, plan.declaration.Record.Version
	default:
		return nil, ErrInvalid
	}
	command, err := (ledger.Command{CommandID: input.id, SubmissionSHA256: input.digest}).Encode()
	if err != nil {
		return nil, err
	}
	payload, err := encodeRegistrationValue(effect)
	if err != nil {
		return nil, err
	}
	receipt, err := encodeRegistrationValue(ledger.RuntimeRegistrationReceipt{CommandID: input.id, SubmissionSHA256: input.digest, Submission: input.sub, Outcome: "applied", Version: version})
	if err != nil {
		return nil, err
	}
	drafts := []ledger.Draft{{Type: ledger.TypeCommand, Payload: command}, {Type: typ, Payload: payload}, {Type: ledger.TypeRuntimeRegistrationReceipt, Payload: receipt}}
	for i := range drafts {
		drafts[i].TypeVersion = 1
		drafts[i].Actor = ledger.ActorOwner
		drafts[i].AgentID = input.sub.Origin.AgentID
		drafts[i].ExecutionSessionID = input.sub.Origin.SessionID
		drafts[i].ContextID = input.sub.Origin.ContextID
		drafts[i].OperationID = input.id
	}
	return drafts, nil
}

func (s *Service) executeRegistration(ctx context.Context, caller scopedCaller, operation string, request any) (RegistrationReceipt, error) {
	if caller.actor != "owner" {
		return RegistrationReceipt{}, ErrDenied
	}
	if s == nil {
		return RegistrationReceipt{}, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return RegistrationReceipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, caller.binding, false)
	if err != nil {
		return RegistrationReceipt{}, err
	}
	input, err := makeRegistrationInput(ids, operation, request)
	if err != nil {
		return RegistrationReceipt{}, err
	}
	match, err := s.lookupRegistration(ctx, input)
	if err != nil {
		s.mu.Unlock()
		return RegistrationReceipt{}, err
	}
	if match.found {
		receipt, err := matchRegistrationReceipt(match, input)
		s.mu.Unlock()
		return receipt, err
	}
	plan, err := s.deriveRegistration(ctx, input.sub, s.p.through+1)
	if err == nil {
		err = s.registrationProducerGuard(input.sub)
	}
	var drafts []ledger.Draft
	if err == nil {
		drafts, err = registrationDrafts(input, plan)
	}
	if err == nil && s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	if err == nil {
		err = s.preflightRegistration(ctx, input, drafts)
	}
	if err == nil {
		err = s.usableLocked(ctx)
	}
	if err != nil {
		s.mu.Unlock()
		return RegistrationReceipt{}, err
	}
	committed, err := s.store.Submit(ctx, drafts[0], drafts[1:])
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		s.mu.Unlock()
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			return RegistrationReceipt{}, ErrCommandMismatch
		}
		return RegistrationReceipt{}, err
	}
	if s.deps.afterScopedSubmit != nil {
		s.deps.afterScopedSubmit()
	}
	// Caller cancellation cannot undo a committed transition or skip its readback.
	err = s.refreshLocked(context.Background(), max(s.p.through, committed.Last))
	var actual commandMatch
	if err == nil {
		actual, err = s.findCommand(context.Background(), input.id, committed.First-1, committed.Last)
	}
	var receipt RegistrationReceipt
	if err == nil && !actual.found {
		err = ErrInconsistent
	}
	if err == nil {
		receipt, err = matchRegistrationReceipt(actual, input)
	}
	if err != nil {
		s.poisoned = true
		s.mu.Unlock()
		return RegistrationReceipt{}, err
	}
	s.mu.Unlock()
	if s.deps.afterRegistrationCommit != nil {
		if err := s.deps.afterRegistrationCommit(receipt); err != nil {
			return RegistrationReceipt{}, err
		}
	}
	return receipt, nil
}

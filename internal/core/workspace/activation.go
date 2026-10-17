package workspace

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// ActivationSchedule selects immediate eligibility or a strict RFC3339 instant.
// NotBefore is supplied only with Mode not_before.
type ActivationSchedule struct{ Mode, NotBefore string }

// ActivationRequest authorizes one exact target. Actor states owner authority;
// C3 authenticates the caller. The service copies target slices and artifact bytes.
type ActivationRequest struct {
	RequestID, OwnerApprovalID uuid.UUID
	Actor                      ledger.Actor
	Target                     ActivationTarget
	ExpectedActiveRecordHash   ledger.Digest
	Schedule                   ActivationSchedule
	Artifacts                  map[ledger.Digest][]byte
}

// ActivationReplacement atomically ends the named revision and admits Request.
type ActivationReplacement struct {
	Request                 ActivationRequest
	ExpectedPendingID       uuid.UUID
	ExpectedPendingRevision uint64
}

// ActivationCancellation ends only the named pending request at its revision.
type ActivationCancellation struct {
	Actor                   ledger.Actor
	ExpectedPendingID       uuid.UUID
	ExpectedPendingRevision uint64
}

// ActivePolicy is a detached full manifest with its actual committed identity.
type ActivePolicy struct {
	ActivationID, OwnerApprovalID                           uuid.UUID
	Epoch, EffectiveSequence                                uint64
	RecordHash, RequestRecordHash, ExpectedActiveRecordHash ledger.Digest
	NotBeforeUTCNS, EffectiveUTCNS                          int64
	Target                                                  ActivationTarget
}

// ActivationState separates the request's lifetime from its durable hold.
// State is pending, replaced, cancelled, applied or a later version's ending token.
// Applied is present only for an applied request; Reason describes live blockage.
type ActivationState struct {
	RequestID, OwnerApprovalID     uuid.UUID
	RequestRecordHash              ledger.Digest
	Revision                       uint64
	State, Hold, Reason            string
	ReplacementRequestID           uuid.UUID
	AdmissionUTCNS, NotBeforeUTCNS int64
	Applied                        *ActivePolicy
}

// ActivationControlResult reports cancelled, too_late or already_terminal.
type ActivationControlResult struct {
	Outcome string
	State   ActivationState
}

type activationState struct {
	request ledger.PolicyActivationRequested
	refs    []ledger.Digest
	state   ActivationState
}

func cloneActivationTarget(target ActivationTarget) ActivationTarget {
	target.WorkspacePolicies = slices.Clone(target.WorkspacePolicies)
	return target
}

func cloneActivePolicy(active ActivePolicy) ActivePolicy {
	active.Target = cloneActivationTarget(active.Target)
	return active
}

func (s *Service) activationViewLocked(r activationState) ActivationState {
	state := r.state
	if state.Applied != nil {
		active := cloneActivePolicy(*state.Applied)
		state.Applied = &active
	}
	if s.activationRecoveryPendingLocked(r) {
		state.Reason = "activation_recovery_pending"
	}
	return state
}

func activationRequestTarget(p ledger.PolicyActivationRequested) ActivationTarget {
	return ActivationTarget{FieldLawSHA256: p.FieldLawSHA256, ValidationPolicyHash: p.ValidationPolicyHash, WorkspacePolicies: p.WorkspacePolicies}
}

func (s *Service) priorActivationTargetLocked() *ActivationTarget {
	if s.activePolicy.RecordHash == (ledger.Digest{}) {
		return nil
	}
	return &s.activePolicy.Target
}

func (s *Service) checkActivationHashLocked(expected ledger.Digest) error {
	current := s.activePolicy.RecordHash
	if expected == current {
		return nil
	}
	if expected != (ledger.Digest{}) && current != (ledger.Digest{}) && !s.activationHashes[expected] {
		return fmt.Errorf("%w: unknown active record hash %s", ErrInvalid, expected)
	}
	if expected != (ledger.Digest{}) && current == (ledger.Digest{}) {
		return fmt.Errorf("%w: expected active hash %s, current genesis", ErrRevision, expected)
	}
	return fmt.Errorf("%w: expected active hash %s, current %s", ErrRevision, expected, current)
}

func (s *Service) activationCASLocked(id uuid.UUID, revision uint64) error {
	pending := s.activations[s.pendingActivation]
	if id != s.pendingActivation || revision != pending.state.Revision {
		return fmt.Errorf("%w: expected activation %s revision %d, current %s revision %d", ErrRevision, id, revision, s.pendingActivation, pending.state.Revision)
	}
	return nil
}

func (s *Service) activationAdmissionLocked(r ActivationRequest, notBefore int64, replaces uuid.UUID, revision uint64, reserved bool) (ActivationState, bool, error) {
	if err := s.usableLocked(); err != nil {
		return ActivationState{}, false, err
	}
	if old, ok := s.activations[r.RequestID]; ok {
		p := old.request
		if p.OwnerApprovalID != r.OwnerApprovalID || p.ExpectedActiveRecordHash != r.ExpectedActiveRecordHash || p.Schedule != r.Schedule.Mode ||
			(p.Schedule == ledger.ScheduleNotBefore && p.NotBeforeUTCNS != notBefore) || p.ReplacesRequestID != replaces || uint64(p.ReplacesRevision) != revision ||
			p.FieldLawSHA256 != r.Target.FieldLawSHA256 || p.ValidationPolicyHash != r.Target.ValidationPolicyHash || !slices.Equal(p.WorkspacePolicies, r.Target.WorkspacePolicies) {
			return ActivationState{}, true, ErrActivationMismatch
		}
		return s.activationViewLocked(old), true, nil
	}
	if !reserved && s.activationInFlight[r.RequestID] {
		return ActivationState{}, false, &BusyError{Reason: "activation_in_flight"}
	}
	if replaces == (uuid.UUID{}) {
		if s.pendingActivation != (uuid.UUID{}) {
			return ActivationState{}, false, &BusyError{Reason: "activation_pending"}
		}
	} else if err := s.activationCASLocked(replaces, revision); err != nil {
		return ActivationState{}, false, err
	}
	if err := s.checkActivationHashLocked(r.ExpectedActiveRecordHash); err != nil {
		return ActivationState{}, false, err
	}
	if s.activationIdentities[r.RequestID] || s.activationIdentities[r.OwnerApprovalID] {
		return ActivationState{}, false, fmt.Errorf("%w: activation identity reused", ErrInvalid)
	}
	for _, policy := range r.Target.WorkspacePolicies {
		if _, exists := s.workspaces[policy.WorkspaceID]; !exists {
			return ActivationState{}, false, fmt.Errorf("%w: unregistered policy workspace", ErrInvalid)
		}
	}
	for _, id := range activationAffectedWorkspaces(s.priorActivationTargetLocked(), r.Target) {
		if _, exists := s.workspaces[id]; !exists {
			return ActivationState{}, false, fmt.Errorf("%w: unregistered affected workspace", ErrInvalid)
		}
	}
	return ActivationState{}, false, nil
}

// RequestActivation retains the exact approved bytes and admits one pending
// activation. Same-ID/same-binding calls return its state without doing work.
func (s *Service) RequestActivation(ctx context.Context, r ActivationRequest) (ActivationState, error) {
	if err := s.start(); err != nil {
		return ActivationState{}, err
	}
	defer s.ops.Done()
	return s.requestActivation(ctx, r, uuid.UUID{}, 0)
}

// ReplaceActivation uses one commit for the old ending, new request and pin.
func (s *Service) ReplaceActivation(ctx context.Context, r ActivationReplacement) (ActivationState, error) {
	if err := s.start(); err != nil {
		return ActivationState{}, err
	}
	defer s.ops.Done()
	if r.Request.Actor != ledger.ActorOwner {
		return ActivationState{}, ErrNotBound
	}
	if !validID(r.ExpectedPendingID) || r.ExpectedPendingRevision == 0 || r.ExpectedPendingRevision > math.MaxInt64 {
		return ActivationState{}, ErrInvalid
	}
	return s.requestActivation(ctx, r.Request, r.ExpectedPendingID, r.ExpectedPendingRevision)
}

func (s *Service) requestActivation(ctx context.Context, r ActivationRequest, replaces uuid.UUID, revision uint64) (ActivationState, error) {
	if r.Actor != ledger.ActorOwner {
		return ActivationState{}, ErrNotBound
	}
	if !validID(r.RequestID) || !validID(r.OwnerApprovalID) || r.RequestID == r.OwnerApprovalID || r.RequestID == replaces {
		return ActivationState{}, ErrInvalid
	}
	r.Target = cloneActivationTarget(r.Target)
	if err := validateActivationTarget(r.Target); err != nil {
		return ActivationState{}, err
	}
	var notBefore int64
	switch r.Schedule.Mode {
	case ledger.ScheduleImmediate:
		if r.Schedule.NotBefore != "" {
			return ActivationState{}, ErrInvalid
		}
	case ledger.ScheduleNotBefore:
		var err error
		notBefore, err = parseActivationTimestamp(r.Schedule.NotBefore)
		if err != nil {
			return ActivationState{}, err
		}
	default:
		return ActivationState{}, ErrInvalid
	}
	s.mu.Lock()
	previous, replay, err := s.activationAdmissionLocked(r, notBefore, replaces, revision, false)
	if err != nil || replay {
		s.mu.Unlock()
		return previous, err
	}
	s.activationInFlight[r.RequestID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.activationInFlight, r.RequestID); s.mu.Unlock() }()
	artifacts := make(map[ledger.Digest][]byte, len(r.Artifacts))
	for digest, body := range r.Artifacts {
		artifacts[digest] = bytes.Clone(body)
	}
	refs, err := validateActivationArtifacts(r.Target, artifacts)
	if err != nil {
		return ActivationState{}, err
	}
	for _, body := range artifacts {
		if len(body) > ledgerstore.MaxBodyBytes {
			return ActivationState{}, fmt.Errorf("%w: activation artifact exceeds body limit", ErrInvalid)
		}
	}
	for _, digest := range refs {
		published, _, err := s.store.PublishBody(ctx, bytes.NewReader(artifacts[digest]))
		if err != nil {
			return ActivationState{}, err
		}
		if published != digest {
			return ActivationState{}, fmt.Errorf("%w: published activation digest differs", ErrInvalid)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	admission, trusted := s.sampleActivationTimeLocked()
	if !trusted {
		return ActivationState{}, &UnavailableError{Reason: "clock_untrusted"}
	}
	previous, replay, err = s.activationAdmissionLocked(r, notBefore, replaces, revision, true)
	if err != nil || replay {
		return previous, err
	}
	if r.Schedule.Mode == ledger.ScheduleImmediate {
		notBefore = admission
	} else if notBefore <= admission {
		return ActivationState{}, fmt.Errorf("%w: not_before is not after admission", ErrInvalid)
	}
	p := ledger.PolicyActivationRequested{RequestID: r.RequestID, OwnerApprovalID: r.OwnerApprovalID, FieldLawSHA256: r.Target.FieldLawSHA256, ValidationPolicyHash: r.Target.ValidationPolicyHash, WorkspacePolicies: r.Target.WorkspacePolicies, AffectedWorkspaceIDs: activationAffectedWorkspaces(s.priorActivationTargetLocked(), r.Target), ExpectedActiveRecordHash: r.ExpectedActiveRecordHash, Schedule: r.Schedule.Mode, AdmissionUTCNS: admission, NotBeforeUTCNS: notBefore, ReplacesRequestID: replaces, ReplacesRevision: int64(revision), PinID: s.deps.newID()}
	payload, err := p.Encode()
	if err != nil {
		return ActivationState{}, err
	}
	pin, err := (ledger.BodyPin{PinID: p.PinID}).Encode()
	if err != nil {
		return ActivationState{}, err
	}
	var drafts []ledger.Draft
	if replaces != (uuid.UUID{}) {
		ended, err := s.activationEndingLocked(replaces, revision, ledger.ActivationReplaced, r.RequestID)
		if err != nil {
			return ActivationState{}, err
		}
		drafts = append(drafts, ended)
	}
	drafts = append(drafts, ledger.Draft{Type: ledger.TypePolicyActivationRequested, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: payload}, ledger.Draft{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: pin})
	if err := s.appendLocked(ctx, drafts); err != nil {
		return ActivationState{}, err
	}
	s.wakeActivationLocked()
	return s.activationViewLocked(s.activations[r.RequestID]), nil
}

func (s *Service) activationEndingLocked(id uuid.UUID, revision uint64, outcome string, replacement uuid.UUID) (ledger.Draft, error) {
	p := ledger.PolicyActivationEnded{RequestID: id, ActivationRequestRecordHash: s.activations[id].state.RequestRecordHash, ExpectedPendingRevision: int64(revision), Outcome: outcome, ReplacementRequestID: replacement}
	payload, err := p.Encode()
	return ledger.Draft{Type: ledger.TypePolicyActivationEnded, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: payload}, err
}

// CancelActivation ends the pending approval, never a promotion or a foreign fence.
func (s *Service) CancelActivation(ctx context.Context, c ActivationCancellation) (ActivationControlResult, error) {
	if err := s.start(); err != nil {
		return ActivationControlResult{}, err
	}
	defer s.ops.Done()
	if c.Actor != ledger.ActorOwner {
		return ActivationControlResult{}, ErrNotBound
	}
	if !validID(c.ExpectedPendingID) || c.ExpectedPendingRevision == 0 || c.ExpectedPendingRevision > math.MaxInt64 {
		return ActivationControlResult{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return ActivationControlResult{}, err
	}
	r, exists := s.activations[c.ExpectedPendingID]
	if !exists {
		if s.activationInFlight[c.ExpectedPendingID] {
			return ActivationControlResult{}, &BusyError{Reason: "activation_in_flight"}
		}
		return ActivationControlResult{}, ErrUnknownActivation
	}
	result := ActivationControlResult{State: s.activationViewLocked(r)}
	if r.state.State != "pending" {
		switch r.state.State {
		case "applied":
			result.Outcome = "too_late"
		case "cancelled":
			result.Outcome = "cancelled"
		default:
			result.Outcome = "already_terminal"
		}
		return result, nil
	}
	if err := s.activationCASLocked(c.ExpectedPendingID, c.ExpectedPendingRevision); err != nil {
		return result, err
	}
	draft, err := s.activationEndingLocked(c.ExpectedPendingID, c.ExpectedPendingRevision, ledger.ActivationCancelled, uuid.UUID{})
	if err != nil {
		return result, err
	}
	if err := s.appendLocked(ctx, []ledger.Draft{draft}); err != nil {
		return result, err
	}
	s.wakeActivationLocked()
	return ActivationControlResult{Outcome: "cancelled", State: s.activationViewLocked(s.activations[c.ExpectedPendingID])}, nil
}

// ActivationStatus is an inert owner-only query, including before recovery resume.
func (s *Service) ActivationStatus(ctx context.Context, actor ledger.Actor, requestID uuid.UUID) (ActivationState, error) {
	if err := s.start(); err != nil {
		return ActivationState{}, err
	}
	defer s.ops.Done()
	if actor != ledger.ActorOwner {
		return ActivationState{}, ErrNotBound
	}
	if !validID(requestID) {
		return ActivationState{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return ActivationState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, exists := s.activations[requestID]
	if !exists {
		return ActivationState{}, ErrUnknownActivation
	}
	return s.activationViewLocked(r), nil
}

// ActivePolicy returns no_active_policy before the first durable activation.
func (s *Service) ActivePolicy(ctx context.Context, actor ledger.Actor) (ActivePolicy, error) {
	if err := s.start(); err != nil {
		return ActivePolicy{}, err
	}
	defer s.ops.Done()
	if actor != ledger.ActorOwner {
		return ActivePolicy{}, ErrNotBound
	}
	if err := ctx.Err(); err != nil {
		return ActivePolicy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activePolicy.RecordHash == (ledger.Digest{}) {
		return ActivePolicy{}, &UnavailableError{Reason: "no_active_policy"}
	}
	return cloneActivePolicy(s.activePolicy), nil
}

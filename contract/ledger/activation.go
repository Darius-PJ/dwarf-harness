package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// The F2-09 record types, each at version 1: ticket 09's part of F2,
// proposed for the owner's freeze (D113). The package documentation
// specifies their payloads and service-owned record rules.
const (
	TypePolicyActivationRequested   = "workspace.policy_activation_requested"
	TypePolicyActivationHoldChanged = "workspace.policy_activation_hold_changed"
	TypePolicyActivationEnded       = "workspace.policy_activation_ended"
	TypePolicyActivated             = "workspace.policy_activated"
)

// F2-09's closed schedule tokens, proposed for the owner's freeze (D113).
const (
	ScheduleImmediate = "immediate"
	ScheduleNotBefore = "not_before"
)

// F2-09's closed hold tokens, proposed for the owner's freeze (D113).
const (
	HoldDue            = "due"
	HoldClockUntrusted = "clock_untrusted"
	HoldNone           = "none"
)

// F2-09's version-1 outcomes under an open outcome grammar, proposed for
// the owner's freeze (D113). Readers also accept other well-formed tokens.
const (
	ActivationCancelled = "cancelled"
	ActivationReplaced  = "replaced"
)

// WorkspacePolicy binds one workspace's check and environment artifacts.
// A workspace_policies array is strictly ascending by WorkspaceID bytes.
type WorkspacePolicy struct {
	WorkspaceID           uuid.UUID `json:"workspace_id"`
	CheckPolicyHash       Digest    `json:"check_policy_hash"`
	EnvironmentPolicyHash Digest    `json:"environment_policy_hash"`
}

// PolicyActivationRequested is the workspace.policy_activation_requested
// v1 payload: one exact owner approval and its activation schedule.
type PolicyActivationRequested struct {
	RequestID                uuid.UUID         `json:"request_id"`
	OwnerApprovalID          uuid.UUID         `json:"owner_approval_id"`
	FieldLawSHA256           Digest            `json:"field_law_sha256"`
	ValidationPolicyHash     Digest            `json:"validation_policy_hash"`
	WorkspacePolicies        []WorkspacePolicy `json:"workspace_policies"`
	AffectedWorkspaceIDs     []uuid.UUID       `json:"affected_workspace_ids"`
	ExpectedActiveRecordHash Digest            `json:"expected_active_record_hash,omitzero"`
	Schedule                 string            `json:"schedule"`
	AdmissionUTCNS           int64             `json:"admission_utc_ns"`
	NotBeforeUTCNS           int64             `json:"not_before_utc_ns"`
	ReplacesRequestID        uuid.UUID         `json:"replaces_request_id,omitzero"`
	ReplacesRevision         int64             `json:"replaces_revision,omitzero"`
	PinID                    uuid.UUID         `json:"pin_id"`
}

// PolicyActivationHoldChanged is the workspace.policy_activation_hold_changed
// v1 payload. ObservedUTCNS is present exactly for due and none; zero is a
// valid observation. For clock_untrusted it must be zero and is omitted.
type PolicyActivationHoldChanged struct {
	RequestID                   uuid.UUID `json:"request_id"`
	ActivationRequestRecordHash Digest    `json:"activation_request_record_hash"`
	Hold                        string    `json:"hold"`
	ObservedUTCNS               int64     `json:"observed_utc_ns"`
}

// PolicyActivationEnded is the workspace.policy_activation_ended v1 payload.
// An unknown well-formed outcome is terminal, not applied and not invalid.
type PolicyActivationEnded struct {
	RequestID                   uuid.UUID `json:"request_id"`
	ActivationRequestRecordHash Digest    `json:"activation_request_record_hash"`
	ExpectedPendingRevision     int64     `json:"expected_pending_revision"`
	Outcome                     string    `json:"outcome"`
	ReplacementRequestID        uuid.UUID `json:"replacement_request_id,omitzero"`
}

// PolicyActivated is the workspace.policy_activated v1 payload. Its effective
// sequence and record hash are the committed envelope's identity, not keys.
type PolicyActivated struct {
	ActivationID                uuid.UUID         `json:"activation_id"`
	Epoch                       int64             `json:"epoch"`
	FieldLawSHA256              Digest            `json:"field_law_sha256"`
	ValidationPolicyHash        Digest            `json:"validation_policy_hash"`
	WorkspacePolicies           []WorkspacePolicy `json:"workspace_policies"`
	OwnerApprovalID             uuid.UUID         `json:"owner_approval_id"`
	ActivationRequestRecordHash Digest            `json:"activation_request_record_hash"`
	ExpectedActiveRecordHash    Digest            `json:"expected_active_record_hash,omitzero"`
	NotBeforeUTCNS              int64             `json:"not_before_utc_ns"`
	EffectiveUTCNS              int64             `json:"effective_utc_ns"`
}

// Encode returns p as a workspace.policy_activation_requested v1 payload.
// Its errors wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PolicyActivationRequested) Encode() (json.RawMessage, error) {
	if p.WorkspacePolicies == nil {
		p.WorkspacePolicies = []WorkspacePolicy{}
	}
	if p.AffectedWorkspaceIDs == nil {
		p.AffectedWorkspaceIDs = []uuid.UUID{}
	}
	return encodePayload(TypePolicyActivationRequested, p, p.check())
}

// DecodePolicyActivationRequested decodes a workspace.policy_activation_requested
// v1 payload. Its errors wrap ErrInvalid.
func DecodePolicyActivationRequested(payload json.RawMessage) (PolicyActivationRequested, error) {
	var p PolicyActivationRequested
	if err := decodePayload(TypePolicyActivationRequested, payload, func(m *members) error {
		p = PolicyActivationRequested{
			RequestID: m.id("request_id"), OwnerApprovalID: m.id("owner_approval_id"),
			FieldLawSHA256: m.digest("field_law_sha256"), ValidationPolicyHash: m.digest("validation_policy_hash"),
			Schedule: m.text("schedule"), PinID: m.id("pin_id"),
		}
		p.AffectedWorkspaceIDs, _ = m.sortedIDs("affected_workspace_ids")
		p.ExpectedActiveRecordHash, _ = m.optionalDigest("expected_active_record_hash")
		p.AdmissionUTCNS, _ = m.signed("admission_utc_ns", true)
		p.NotBeforeUTCNS, _ = m.signed("not_before_utc_ns", true)
		p.ReplacesRequestID, _ = m.optionalID("replaces_request_id")
		p.ReplacesRevision, _ = m.optionalNat("replaces_revision")
		raw, ok := m.get("workspace_policies", true)
		if !ok {
			return nil
		}
		var elems []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
			return errors.New("workspace_policies is not an array")
		}
		p.WorkspacePolicies = make([]WorkspacePolicy, 0, len(elems))
		for i, el := range elems {
			b := members{}
			if el[0] != '{' || json.Unmarshal(el, &b.m) != nil {
				return fmt.Errorf("workspace_policies element %d is not an object", i)
			}
			policy := WorkspacePolicy{WorkspaceID: b.id("workspace_id"), CheckPolicyHash: b.digest("check_policy_hash"), EnvironmentPolicyHash: b.digest("environment_policy_hash")}
			if b.err != nil {
				return fmt.Errorf("workspace_policies element %d %w", i, b.err)
			}
			p.WorkspacePolicies = append(p.WorkspacePolicies, policy)
		}
		return p.check()
	}); err != nil {
		return PolicyActivationRequested{}, err
	}
	return p, nil
}

func (p PolicyActivationRequested) check() error {
	switch {
	case !isV7(p.RequestID) || !isV7(p.OwnerApprovalID) || !isV7(p.PinID):
		return errors.New("required ID is not a UUIDv7")
	case p.RequestID == p.OwnerApprovalID || p.RequestID == p.PinID || p.OwnerApprovalID == p.PinID:
		return errors.New("request_id, owner_approval_id and pin_id must differ")
	case p.FieldLawSHA256 == (Digest{}) || p.ValidationPolicyHash == (Digest{}):
		return errors.New("global artifact digest is zero")
	case p.Schedule != ScheduleImmediate && p.Schedule != ScheduleNotBefore:
		return errors.New("schedule is not immediate or not_before")
	case p.Schedule == ScheduleImmediate && p.NotBeforeUTCNS != p.AdmissionUTCNS:
		return errors.New("immediate schedule differs from admission")
	case p.Schedule == ScheduleNotBefore && p.NotBeforeUTCNS <= p.AdmissionUTCNS:
		return errors.New("not_before schedule is not after admission")
	case (p.ReplacesRequestID == (uuid.UUID{})) != (p.ReplacesRevision == 0):
		return errors.New("replacement ID and revision must both be present or absent")
	case p.ReplacesRequestID != (uuid.UUID{}) && (!isV7(p.ReplacesRequestID) || p.ReplacesRequestID == p.RequestID):
		return errors.New("replaces_request_id is not a different UUIDv7")
	case p.ReplacesRevision < 0:
		return errors.New("replaces_revision is negative")
	}
	for i, id := range p.AffectedWorkspaceIDs {
		if !isV7(id) || i > 0 && bytes.Compare(p.AffectedWorkspaceIDs[i-1][:], id[:]) >= 0 {
			return fmt.Errorf("affected_workspace_ids element %d is not a strictly ascending UUIDv7", i)
		}
	}
	return checkWorkspacePolicies(p.WorkspacePolicies)
}

func checkWorkspacePolicies(policies []WorkspacePolicy) error {
	for i, p := range policies {
		switch {
		case !isV7(p.WorkspaceID):
			return fmt.Errorf("workspace_policies element %d workspace_id is not a UUIDv7", i)
		case p.CheckPolicyHash == (Digest{}) || p.EnvironmentPolicyHash == (Digest{}):
			return fmt.Errorf("workspace_policies element %d artifact digest is zero", i)
		case i > 0 && bytes.Compare(policies[i-1].WorkspaceID[:], p.WorkspaceID[:]) >= 0:
			return fmt.Errorf("workspace_policies element %d workspace_id is not above the one before it", i)
		}
	}
	return nil
}

// Encode returns p as a workspace.policy_activation_hold_changed v1 payload.
// Its errors wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PolicyActivationHoldChanged) Encode() (json.RawMessage, error) {
	wire := struct {
		RequestID                   uuid.UUID `json:"request_id"`
		ActivationRequestRecordHash Digest    `json:"activation_request_record_hash"`
		Hold                        string    `json:"hold"`
		ObservedUTCNS               *int64    `json:"observed_utc_ns,omitempty"`
	}{RequestID: p.RequestID, ActivationRequestRecordHash: p.ActivationRequestRecordHash, Hold: p.Hold}
	if p.Hold != HoldClockUntrusted {
		wire.ObservedUTCNS = &p.ObservedUTCNS
	}
	return encodePayload(TypePolicyActivationHoldChanged, wire, p.check(wire.ObservedUTCNS != nil))
}

// DecodePolicyActivationHoldChanged decodes a workspace.policy_activation_hold_changed
// v1 payload. Its errors wrap ErrInvalid.
func DecodePolicyActivationHoldChanged(payload json.RawMessage) (PolicyActivationHoldChanged, error) {
	var p PolicyActivationHoldChanged
	if err := decodePayload(TypePolicyActivationHoldChanged, payload, func(m *members) error {
		p = PolicyActivationHoldChanged{RequestID: m.id("request_id"), ActivationRequestRecordHash: m.digest("activation_request_record_hash"), Hold: m.text("hold")}
		var observed bool
		p.ObservedUTCNS, observed = m.signed("observed_utc_ns", false)
		return p.check(observed)
	}); err != nil {
		return PolicyActivationHoldChanged{}, err
	}
	return p, nil
}

func (p PolicyActivationHoldChanged) check(observed bool) error {
	switch {
	case !isV7(p.RequestID):
		return errors.New("request_id is not a UUIDv7")
	case p.ActivationRequestRecordHash == (Digest{}):
		return errors.New("activation_request_record_hash is zero")
	case p.Hold != HoldDue && p.Hold != HoldClockUntrusted && p.Hold != HoldNone:
		return errors.New("hold is not due, clock_untrusted or none")
	case p.Hold == HoldClockUntrusted && (observed || p.ObservedUTCNS != 0):
		return errors.New("clock_untrusted supplies an observation")
	case p.Hold != HoldClockUntrusted && !observed:
		return errors.New("due or none requires an observation")
	}
	return nil
}

// Encode returns p as a workspace.policy_activation_ended v1 payload.
// Its errors wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PolicyActivationEnded) Encode() (json.RawMessage, error) {
	return encodePayload(TypePolicyActivationEnded, p, p.check())
}

// DecodePolicyActivationEnded decodes a workspace.policy_activation_ended
// v1 payload. Its errors wrap ErrInvalid.
func DecodePolicyActivationEnded(payload json.RawMessage) (PolicyActivationEnded, error) {
	var p PolicyActivationEnded
	if err := decodePayload(TypePolicyActivationEnded, payload, func(m *members) error {
		p = PolicyActivationEnded{RequestID: m.id("request_id"), ActivationRequestRecordHash: m.digest("activation_request_record_hash"), ExpectedPendingRevision: m.nat("expected_pending_revision", true), Outcome: m.text("outcome")}
		p.ReplacementRequestID, _ = m.optionalID("replacement_request_id")
		return p.check()
	}); err != nil {
		return PolicyActivationEnded{}, err
	}
	return p, nil
}

func (p PolicyActivationEnded) check() error {
	switch {
	case !isV7(p.RequestID):
		return errors.New("request_id is not a UUIDv7")
	case p.ActivationRequestRecordHash == (Digest{}):
		return errors.New("activation_request_record_hash is zero")
	case p.ExpectedPendingRevision < 1:
		return errors.New("expected_pending_revision is below 1")
	case p.ReplacementRequestID != (uuid.UUID{}) && !isV7(p.ReplacementRequestID):
		return errors.New("replacement_request_id is not a UUIDv7")
	}
	if n, ok := dottedTokens(p.Outcome); !ok || n != 1 || len(p.Outcome) > 64 {
		return errors.New("outcome is not a token of at most 64 bytes")
	}
	switch p.Outcome {
	case ActivationReplaced:
		if p.ReplacementRequestID == (uuid.UUID{}) || p.ReplacementRequestID == p.RequestID {
			return errors.New("replaced requires a different replacement_request_id")
		}
	case ActivationCancelled:
		if p.ReplacementRequestID != (uuid.UUID{}) {
			return errors.New("cancelled supplies replacement_request_id")
		}
	}
	return nil
}

// Encode returns p as a workspace.policy_activated v1 payload.
// Its errors wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PolicyActivated) Encode() (json.RawMessage, error) {
	if p.WorkspacePolicies == nil {
		p.WorkspacePolicies = []WorkspacePolicy{}
	}
	return encodePayload(TypePolicyActivated, p, p.check())
}

// DecodePolicyActivated decodes a workspace.policy_activated v1 payload.
// Its errors wrap ErrInvalid.
func DecodePolicyActivated(payload json.RawMessage) (PolicyActivated, error) {
	var p PolicyActivated
	if err := decodePayload(TypePolicyActivated, payload, func(m *members) error {
		p = PolicyActivated{
			ActivationID: m.id("activation_id"), Epoch: m.nat("epoch", true),
			FieldLawSHA256: m.digest("field_law_sha256"), ValidationPolicyHash: m.digest("validation_policy_hash"),
			OwnerApprovalID: m.id("owner_approval_id"), ActivationRequestRecordHash: m.digest("activation_request_record_hash"),
		}
		p.ExpectedActiveRecordHash, _ = m.optionalDigest("expected_active_record_hash")
		p.NotBeforeUTCNS, _ = m.signed("not_before_utc_ns", true)
		p.EffectiveUTCNS, _ = m.signed("effective_utc_ns", true)
		raw, ok := m.get("workspace_policies", true)
		if !ok {
			return nil
		}
		var elems []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
			return errors.New("workspace_policies is not an array")
		}
		p.WorkspacePolicies = make([]WorkspacePolicy, 0, len(elems))
		for i, el := range elems {
			b := members{}
			if el[0] != '{' || json.Unmarshal(el, &b.m) != nil {
				return fmt.Errorf("workspace_policies element %d is not an object", i)
			}
			policy := WorkspacePolicy{WorkspaceID: b.id("workspace_id"), CheckPolicyHash: b.digest("check_policy_hash"), EnvironmentPolicyHash: b.digest("environment_policy_hash")}
			if b.err != nil {
				return fmt.Errorf("workspace_policies element %d %w", i, b.err)
			}
			p.WorkspacePolicies = append(p.WorkspacePolicies, policy)
		}
		return p.check()
	}); err != nil {
		return PolicyActivated{}, err
	}
	return p, nil
}

func (p PolicyActivated) check() error {
	switch {
	case !isV7(p.ActivationID) || !isV7(p.OwnerApprovalID):
		return errors.New("required ID is not a UUIDv7")
	case p.ActivationID == p.OwnerApprovalID:
		return errors.New("activation_id equals owner_approval_id")
	case p.Epoch < 1:
		return errors.New("epoch is below 1")
	case (p.Epoch == 1) != (p.ExpectedActiveRecordHash == (Digest{})):
		return errors.New("expected_active_record_hash must be absent exactly at epoch 1")
	case p.FieldLawSHA256 == (Digest{}) || p.ValidationPolicyHash == (Digest{}) || p.ActivationRequestRecordHash == (Digest{}):
		return errors.New("required digest is zero")
	case p.EffectiveUTCNS < p.NotBeforeUTCNS:
		return errors.New("effective_utc_ns is below not_before_utc_ns")
	}
	return checkWorkspacePolicies(p.WorkspacePolicies)
}

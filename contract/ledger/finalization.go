package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// The F2-08 record types, each at version 1: ticket 08's part of F2,
// proposed for the owner's freeze (D113). The package documentation
// specifies their payloads and service-owned record rules.
const (
	TypeFinalizationIntended = "workspace.finalization_intended"
	TypePromotionAccepted    = "workspace.promotion_accepted"
	TypePromotionNoChange    = "workspace.promotion_no_change"
)

// FinalizationIntended is the payload of a workspace.finalization_intended
// v1 record: the request, generation and exact canonical update bindings.
// Zero PredecessorWitnessSeq is absent; otherwise it is at least 1.
type FinalizationIntended struct {
	IntentID              uuid.UUID `json:"intent_id"`
	RequestID             uuid.UUID `json:"request_id"`
	GenerationID          uuid.UUID `json:"generation_id"`
	PredecessorWitnessSeq int64     `json:"predecessor_witness_seq,omitzero"`
	PredecessorCommit     string    `json:"predecessor_commit"`
	PredecessorTree       string    `json:"predecessor_tree"`
	TargetCommit          string    `json:"target_commit"`
	TargetTree            string    `json:"target_tree"`
	FinalizationRules     string    `json:"finalization_rules"`
}

// PromotionAccepted is the payload of a workspace.promotion_accepted v1
// record: a successful ending and the workspace's next accepted witness.
// ChangedPaths must be non-empty, distinct and byte-sorted. Zero
// PredecessorWitnessSeq is absent; otherwise it is at least 1.
type PromotionAccepted struct {
	IntentID              uuid.UUID `json:"intent_id"`
	RequestID             uuid.UUID `json:"request_id"`
	GenerationID          uuid.UUID `json:"generation_id"`
	PredecessorWitnessSeq int64     `json:"predecessor_witness_seq,omitzero"`
	PredecessorCommit     string    `json:"predecessor_commit"`
	PredecessorTree       string    `json:"predecessor_tree"`
	AcceptedCommit        string    `json:"accepted_commit"`
	AcceptedTree          string    `json:"accepted_tree"`
	ChangedPaths          []string  `json:"changed_paths"`
}

// PromotionNoChange is the payload of a workspace.promotion_no_change v1
// record: a successful ending without an accepted effect. Zero
// PredecessorWitnessSeq is absent; otherwise it is at least 1.
type PromotionNoChange struct {
	RequestID             uuid.UUID `json:"request_id"`
	GenerationID          uuid.UUID `json:"generation_id"`
	PredecessorWitnessSeq int64     `json:"predecessor_witness_seq,omitzero"`
	PredecessorCommit     string    `json:"predecessor_commit"`
	PredecessorTree       string    `json:"predecessor_tree"`
}

// Encode returns p as a workspace.finalization_intended v1 payload. Its
// errors wrap ErrInvalid. It checks payload rules, not service record rules.
func (p FinalizationIntended) Encode() (json.RawMessage, error) {
	return encodePayload(TypeFinalizationIntended, p, p.check())
}

// DecodeFinalizationIntended decodes a workspace.finalization_intended v1
// payload. Its errors wrap ErrInvalid.
func DecodeFinalizationIntended(payload json.RawMessage) (FinalizationIntended, error) {
	var p FinalizationIntended
	if err := decodePayload(TypeFinalizationIntended, payload, func(m *members) error {
		p = FinalizationIntended{
			IntentID: m.id("intent_id"), RequestID: m.id("request_id"), GenerationID: m.id("generation_id"),
			PredecessorCommit: m.text("predecessor_commit"), PredecessorTree: m.text("predecessor_tree"),
			TargetCommit: m.text("target_commit"), TargetTree: m.text("target_tree"), FinalizationRules: m.text("finalization_rules"),
		}
		p.PredecessorWitnessSeq, _ = m.optionalNat("predecessor_witness_seq")
		return p.check()
	}); err != nil {
		return FinalizationIntended{}, err
	}
	return p, nil
}

func (p FinalizationIntended) check() error {
	if !isV7(p.IntentID) || !isV7(p.RequestID) || !isV7(p.GenerationID) {
		return errors.New("required ID is not a UUIDv7")
	}
	for _, id := range []string{p.PredecessorCommit, p.PredecessorTree, p.TargetCommit, p.TargetTree} {
		if !validGitID(id) || len(id) != len(p.PredecessorCommit) {
			return errors.New("tree and commit IDs must be 40 or 64 lowercase hex digits of one length")
		}
	}
	switch {
	case p.PredecessorWitnessSeq < 0:
		return errors.New("predecessor_witness_seq is negative")
	case p.PredecessorCommit == p.TargetCommit:
		return errors.New("target_commit equals predecessor_commit")
	case p.PredecessorTree == p.TargetTree:
		return errors.New("target_tree equals predecessor_tree")
	case !validIntegrationRules(p.FinalizationRules):
		return errors.New("finalization_rules does not match the versioned rules grammar")
	}
	return nil
}

// Encode returns p as a workspace.promotion_accepted v1 payload. Its errors
// wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PromotionAccepted) Encode() (json.RawMessage, error) {
	return encodePayload(TypePromotionAccepted, p, p.check())
}

// DecodePromotionAccepted decodes a workspace.promotion_accepted v1 payload.
// Its errors wrap ErrInvalid.
func DecodePromotionAccepted(payload json.RawMessage) (PromotionAccepted, error) {
	var p PromotionAccepted
	if err := decodePayload(TypePromotionAccepted, payload, func(m *members) error {
		p = PromotionAccepted{
			IntentID: m.id("intent_id"), RequestID: m.id("request_id"), GenerationID: m.id("generation_id"),
			PredecessorCommit: m.text("predecessor_commit"), PredecessorTree: m.text("predecessor_tree"),
			AcceptedCommit: m.text("accepted_commit"), AcceptedTree: m.text("accepted_tree"),
		}
		p.PredecessorWitnessSeq, _ = m.optionalNat("predecessor_witness_seq")
		p.ChangedPaths, _ = m.paths("changed_paths", true)
		return p.check()
	}); err != nil {
		return PromotionAccepted{}, err
	}
	return p, nil
}

func (p PromotionAccepted) check() error {
	if !isV7(p.IntentID) || !isV7(p.RequestID) || !isV7(p.GenerationID) {
		return errors.New("required ID is not a UUIDv7")
	}
	for _, id := range []string{p.PredecessorCommit, p.PredecessorTree, p.AcceptedCommit, p.AcceptedTree} {
		if !validGitID(id) || len(id) != len(p.PredecessorCommit) {
			return errors.New("tree and commit IDs must be 40 or 64 lowercase hex digits of one length")
		}
	}
	switch {
	case p.PredecessorWitnessSeq < 0:
		return errors.New("predecessor_witness_seq is negative")
	case p.PredecessorCommit == p.AcceptedCommit:
		return errors.New("accepted_commit equals predecessor_commit")
	case p.PredecessorTree == p.AcceptedTree:
		return errors.New("accepted_tree equals predecessor_tree")
	case len(p.ChangedPaths) == 0:
		return errors.New("changed_paths is empty")
	}
	if err := checkRepoPaths(p.ChangedPaths); err != nil {
		return fmt.Errorf("changed_paths: %w", err)
	}
	return nil
}

// Encode returns p as a workspace.promotion_no_change v1 payload. Its errors
// wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PromotionNoChange) Encode() (json.RawMessage, error) {
	return encodePayload(TypePromotionNoChange, p, p.check())
}

// DecodePromotionNoChange decodes a workspace.promotion_no_change v1 payload.
// Its errors wrap ErrInvalid.
func DecodePromotionNoChange(payload json.RawMessage) (PromotionNoChange, error) {
	var p PromotionNoChange
	if err := decodePayload(TypePromotionNoChange, payload, func(m *members) error {
		p = PromotionNoChange{
			RequestID: m.id("request_id"), GenerationID: m.id("generation_id"),
			PredecessorCommit: m.text("predecessor_commit"), PredecessorTree: m.text("predecessor_tree"),
		}
		p.PredecessorWitnessSeq, _ = m.optionalNat("predecessor_witness_seq")
		return p.check()
	}); err != nil {
		return PromotionNoChange{}, err
	}
	return p, nil
}

func (p PromotionNoChange) check() error {
	switch {
	case !isV7(p.RequestID) || !isV7(p.GenerationID):
		return errors.New("required ID is not a UUIDv7")
	case p.PredecessorWitnessSeq < 0:
		return errors.New("predecessor_witness_seq is negative")
	case !validGitID(p.PredecessorCommit) || !validGitID(p.PredecessorTree) || len(p.PredecessorCommit) != len(p.PredecessorTree):
		return errors.New("tree and commit IDs must be 40 or 64 lowercase hex digits of one length")
	}
	return nil
}

package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"uuid"
)

// The F2-06 record types, each at version 1: ticket 06's part of F2,
// frozen (D118). The package documentation
// specifies their payloads and service-owned record rules.
const (
	TypePromotionAdmitted = "workspace.promotion_admitted"
	TypeGenerationDerived = "workspace.generation_derived"
	TypePromotionFailed   = "workspace.promotion_failed"
)

// Generation outcomes are closed in version 1.
const (
	GenerationClean    = "clean"
	GenerationConflict = "conflict"
)

// These promotion failure outcomes have payload rules in version 1.
// Other well-formed outcome tokens remain readable and re-encodable.
const (
	FailedConflict         = "conflict"
	FailedDependencyFailed = "dependency_failed"
)

// These F2-08 outcomes are ticket 08's part of F2, proposed for the owner's
// freeze (D113), under F2-06's open outcome grammar. No encoding rule changes.
const (
	FailedCancelled   = "cancelled"
	FailedInterrupted = "interrupted"
)

// PromotionAdmitted is the payload of a workspace.promotion_admitted v1
// record: a checkpoint's receipt and immutable submission. Zero optional
// fields are absent; every non-zero ID must be a UUIDv7.
type PromotionAdmitted struct {
	RequestID            uuid.UUID   `json:"request_id"`
	WorkspaceID          uuid.UUID   `json:"workspace_id"`
	ScratchID            uuid.UUID   `json:"scratch_id"`
	ContextID            uuid.UUID   `json:"context_id"`
	LineageID            uuid.UUID   `json:"lineage_id"`
	Checkpoint           int64       `json:"checkpoint"` // at least 1
	ParentRequestID      uuid.UUID   `json:"parent_request_id,omitzero"`
	RestartFrom          uuid.UUID   `json:"restart_from,omitzero"`
	ScratchBase          string      `json:"scratch_base"`    // 40 or 64 lowercase hex digits
	BaselineTree         string      `json:"baseline_tree"`   // 40 or 64 lowercase hex digits
	DeltaBaseTree        string      `json:"delta_base_tree"` // 40 or 64 lowercase hex digits
	CaptureID            uuid.UUID   `json:"capture_id"`
	SealedTree           string      `json:"sealed_tree"` // 40 or 64 lowercase hex digits
	ManifestSHA256       Digest      `json:"manifest_sha256"`
	InputsRevision       int64       `json:"inputs_revision"` // at least 1
	CaptureInputsSHA256  Digest      `json:"capture_inputs_sha256"`
	PinID                uuid.UUID   `json:"pin_id"`
	ScratchObservationID uuid.UUID   `json:"scratch_observation_id,omitzero"`
	MergeBackIDs         []uuid.UUID `json:"merge_back_ids,omitempty"`
}

// GenerationDerived is the payload of a workspace.generation_derived v1
// record: one integration over a train predecessor. Zero optional fields
// are absent. Clean generations carry derived IDs; conflicts carry a report
// digest and its pin ID instead.
type GenerationDerived struct {
	GenerationID            uuid.UUID `json:"generation_id"`
	RequestID               uuid.UUID `json:"request_id"`
	Supersedes              uuid.UUID `json:"supersedes,omitzero"`
	PredecessorRequestID    uuid.UUID `json:"predecessor_request_id,omitzero"`
	PredecessorGenerationID uuid.UUID `json:"predecessor_generation_id,omitzero"`
	PredecessorCommit       string    `json:"predecessor_commit"` // 40 or 64 lowercase hex digits
	PredecessorTree         string    `json:"predecessor_tree"`   // 40 or 64 lowercase hex digits
	InputsRevision          int64     `json:"inputs_revision"`    // at least 1
	IntegrationRules        string    `json:"integration_rules"`  // [a-z][a-z0-9-]*/[1-9][0-9]*, at most 64 bytes
	Outcome                 string    `json:"outcome"`            // clean or conflict
	DerivedCommit           string    `json:"derived_commit,omitzero"`
	DerivedTree             string    `json:"derived_tree,omitzero"`
	ConflictSHA256          Digest    `json:"conflict_sha256,omitzero"`
	ReportPinID             uuid.UUID `json:"report_pin_id,omitzero"`
	PredecessorWitnessSeq   int64     `json:"predecessor_witness_seq,omitzero"`
}

// PromotionFailed is the payload of a workspace.promotion_failed v1
// record. Outcome is open: encoding and decoding preserve unknown
// well-formed tokens. Core writes only outcomes its reading version defines.
// Every outcome fails the lineage; zero optional IDs are absent.
type PromotionFailed struct {
	RequestID       uuid.UUID `json:"request_id"`
	Outcome         string    `json:"outcome"` // [a-z][a-z0-9_]*, at most 64 bytes
	GenerationID    uuid.UUID `json:"generation_id,omitzero"`
	FailedRequestID uuid.UUID `json:"failed_request_id,omitzero"`
}

// Encode returns p as a workspace.promotion_admitted v1 payload. Its errors
// wrap ErrInvalid. It checks payload rules, not service record rules.
func (p PromotionAdmitted) Encode() (json.RawMessage, error) {
	return encodePayload(TypePromotionAdmitted, p, p.check())
}

// DecodePromotionAdmitted decodes a workspace.promotion_admitted v1
// payload. Its errors wrap ErrInvalid.
func DecodePromotionAdmitted(payload json.RawMessage) (PromotionAdmitted, error) {
	var p PromotionAdmitted
	if err := decodePayload(TypePromotionAdmitted, payload, func(m *members) error {
		p = PromotionAdmitted{
			RequestID: m.id("request_id"), WorkspaceID: m.id("workspace_id"), ScratchID: m.id("scratch_id"),
			ContextID: m.id("context_id"), LineageID: m.id("lineage_id"), Checkpoint: m.nat("checkpoint", true),
			ScratchBase: m.text("scratch_base"), BaselineTree: m.text("baseline_tree"), DeltaBaseTree: m.text("delta_base_tree"),
			CaptureID: m.id("capture_id"), SealedTree: m.text("sealed_tree"), ManifestSHA256: m.digest("manifest_sha256"),
			InputsRevision: m.nat("inputs_revision", true), CaptureInputsSHA256: m.digest("capture_inputs_sha256"), PinID: m.id("pin_id"),
		}
		p.ParentRequestID, _ = m.optionalID("parent_request_id")
		p.RestartFrom, _ = m.optionalID("restart_from")
		p.ScratchObservationID, _ = m.optionalID("scratch_observation_id")
		p.MergeBackIDs, _ = m.optionalIDs("merge_back_ids")
		return p.check()
	}); err != nil {
		return PromotionAdmitted{}, err
	}
	return p, nil
}

func (p PromotionAdmitted) check() error {
	for _, id := range []uuid.UUID{p.RequestID, p.WorkspaceID, p.ScratchID, p.ContextID, p.LineageID, p.CaptureID, p.PinID} {
		if !isV7(id) {
			return errors.New("required ID is not a UUIDv7")
		}
	}
	for _, id := range []uuid.UUID{p.ParentRequestID, p.RestartFrom, p.ScratchObservationID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("optional ID is not a UUIDv7")
		}
	}
	seen := make(map[uuid.UUID]bool, len(p.MergeBackIDs))
	for _, id := range p.MergeBackIDs {
		if !isV7(id) || seen[id] {
			return errors.New("merge_back_ids contains a non-v7 or repeated ID")
		}
		seen[id] = true
	}
	switch {
	case p.Checkpoint < 1:
		return errors.New("checkpoint is below 1")
	case (p.Checkpoint == 1) != (p.ParentRequestID == (uuid.UUID{})):
		return errors.New("checkpoint is 1 exactly when parent_request_id is absent")
	case p.RestartFrom != (uuid.UUID{}) && p.ParentRequestID != (uuid.UUID{}):
		return errors.New("restart_from and parent_request_id are both present")
	case !validGitID(p.ScratchBase) || !validGitID(p.BaselineTree) || !validGitID(p.DeltaBaseTree) || !validGitID(p.SealedTree):
		return errors.New("tree or commit ID is not 40 or 64 lowercase hex digits")
	case p.ManifestSHA256 == (Digest{}) || p.CaptureInputsSHA256 == (Digest{}):
		return errors.New("required digest is zero")
	case p.InputsRevision < 1:
		return errors.New("inputs_revision is below 1")
	}
	return nil
}

// Encode returns p as a workspace.generation_derived v1 payload. Its errors
// wrap ErrInvalid. It checks payload rules, not service record rules.
func (p GenerationDerived) Encode() (json.RawMessage, error) {
	return encodePayload(TypeGenerationDerived, p, p.check())
}

// DecodeGenerationDerived decodes a workspace.generation_derived v1
// payload. Its errors wrap ErrInvalid.
func DecodeGenerationDerived(payload json.RawMessage) (GenerationDerived, error) {
	var p GenerationDerived
	if err := decodePayload(TypeGenerationDerived, payload, func(m *members) error {
		p = GenerationDerived{
			GenerationID: m.id("generation_id"), RequestID: m.id("request_id"),
			PredecessorCommit: m.text("predecessor_commit"), PredecessorTree: m.text("predecessor_tree"),
			InputsRevision: m.nat("inputs_revision", true), IntegrationRules: m.text("integration_rules"), Outcome: m.text("outcome"),
		}
		p.Supersedes, _ = m.optionalID("supersedes")
		p.PredecessorRequestID, _ = m.optionalID("predecessor_request_id")
		p.PredecessorGenerationID, _ = m.optionalID("predecessor_generation_id")
		p.DerivedCommit, _ = m.optionalText("derived_commit")
		p.DerivedTree, _ = m.optionalText("derived_tree")
		p.ConflictSHA256, _ = m.optionalDigest("conflict_sha256")
		p.ReportPinID, _ = m.optionalID("report_pin_id")
		p.PredecessorWitnessSeq, _ = m.optionalNat("predecessor_witness_seq")
		return p.check()
	}); err != nil {
		return GenerationDerived{}, err
	}
	return p, nil
}

func (p GenerationDerived) check() error {
	if !isV7(p.GenerationID) || !isV7(p.RequestID) {
		return errors.New("required ID is not a UUIDv7")
	}
	for _, id := range []uuid.UUID{p.Supersedes, p.PredecessorRequestID, p.PredecessorGenerationID, p.ReportPinID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("optional ID is not a UUIDv7")
		}
	}
	switch {
	case (p.PredecessorRequestID == (uuid.UUID{})) != (p.PredecessorGenerationID == (uuid.UUID{})):
		return errors.New("predecessor request and generation must both be present or absent")
	case p.PredecessorWitnessSeq < 0:
		return errors.New("predecessor_witness_seq is negative")
	case p.PredecessorWitnessSeq != 0 && (p.PredecessorRequestID != (uuid.UUID{}) || p.PredecessorGenerationID != (uuid.UUID{})):
		return errors.New("predecessor witness and train predecessor IDs are both present")
	case !validGitID(p.PredecessorCommit) || !validGitID(p.PredecessorTree):
		return errors.New("predecessor Git ID is not 40 or 64 lowercase hex digits")
	case p.InputsRevision < 1:
		return errors.New("inputs_revision is below 1")
	case !validIntegrationRules(p.IntegrationRules):
		return errors.New("integration_rules does not match the versioned rules grammar")
	}
	switch p.Outcome {
	case GenerationClean:
		if !validGitID(p.DerivedCommit) || !validGitID(p.DerivedTree) {
			return errors.New("clean generation requires valid derived Git IDs")
		}
		if p.ConflictSHA256 != (Digest{}) || p.ReportPinID != (uuid.UUID{}) {
			return errors.New("clean generation carries a conflict digest or report pin")
		}
	case GenerationConflict:
		if p.ConflictSHA256 == (Digest{}) || p.ReportPinID == (uuid.UUID{}) {
			return errors.New("conflict generation requires a digest and report pin")
		}
		if p.DerivedCommit != "" || p.DerivedTree != "" {
			return errors.New("conflict generation carries derived Git IDs")
		}
	default:
		return errors.New("outcome is not clean or conflict")
	}
	return nil
}

// Encode returns p as a workspace.promotion_failed v1 payload, accepting
// every well-formed outcome token. Its errors wrap ErrInvalid.
func (p PromotionFailed) Encode() (json.RawMessage, error) {
	return encodePayload(TypePromotionFailed, p, p.check())
}

// DecodePromotionFailed decodes a workspace.promotion_failed v1 payload,
// including unknown well-formed outcomes. Its errors wrap ErrInvalid.
func DecodePromotionFailed(payload json.RawMessage) (PromotionFailed, error) {
	var p PromotionFailed
	if err := decodePayload(TypePromotionFailed, payload, func(m *members) error {
		p = PromotionFailed{RequestID: m.id("request_id"), Outcome: m.text("outcome")}
		p.GenerationID, _ = m.optionalID("generation_id")
		p.FailedRequestID, _ = m.optionalID("failed_request_id")
		return p.check()
	}); err != nil {
		return PromotionFailed{}, err
	}
	return p, nil
}

func (p PromotionFailed) check() error {
	if !isV7(p.RequestID) {
		return errors.New("request_id is not a UUIDv7")
	}
	for _, id := range []uuid.UUID{p.GenerationID, p.FailedRequestID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("optional ID is not a UUIDv7")
		}
	}
	if n, ok := dottedTokens(p.Outcome); !ok || n != 1 || len(p.Outcome) > 64 {
		return errors.New("outcome is not a token of at most 64 bytes")
	}
	switch p.Outcome {
	case FailedConflict:
		if p.GenerationID == (uuid.UUID{}) || p.FailedRequestID != (uuid.UUID{}) {
			return errors.New("conflict requires generation_id and no failed_request_id")
		}
	case FailedDependencyFailed:
		if p.FailedRequestID == (uuid.UUID{}) || p.GenerationID != (uuid.UUID{}) {
			return errors.New("dependency_failed requires failed_request_id and no generation_id")
		}
	}
	return nil
}

func validIntegrationRules(s string) bool {
	if len(s) > 64 {
		return false
	}
	name, version, found := strings.Cut(s, "/")
	if !found || name == "" || name[0] < 'a' || name[0] > 'z' || version == "" || version[0] < '1' || version[0] > '9' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	for i := 1; i < len(version); i++ {
		if version[i] < '0' || version[i] > '9' {
			return false
		}
	}
	return true
}

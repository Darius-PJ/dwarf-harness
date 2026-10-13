package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
	"uuid"
)

// The F2-07 record types, each at version 1: ticket 07's part of F2,
// frozen (D119). The package documentation
// specifies their payloads and service-owned record rules.
const (
	TypeScratchChanged   = "workspace.scratch_changed"
	TypeScratchMergeBack = "workspace.scratch_merge_back"
)

// Merge-back outcomes are closed in version 1.
const (
	MergeBackPrepared    = "prepared"
	MergeBackWriteFailed = "write_failed"
	MergeBackFastForward = "fast_forward"
	MergeBackMerged      = "merged"
	MergeBackConflict    = "conflict"
)

// These merge-back failure tokens are defined in version 1. Other
// well-formed failure tokens remain readable and re-encodable.
const (
	MergeBackFailureGit          = "git"
	MergeBackFailureIndexLock    = "index_lock"
	MergeBackFailureRefMismatch  = "ref_mismatch"
	MergeBackFailureVerification = "verification"
	MergeBackFailureCancelled    = "cancelled"
	MergeBackFailureInterference = "interference"
)

// ScratchChanged is the payload of a workspace.scratch_changed v1 record:
// a host observation authored by ContextID. Nil required path slices encode
// as empty arrays. Zero ParentScratchID is absent.
type ScratchChanged struct {
	ObservationID       uuid.UUID `json:"observation_id"`
	WorkspaceID         uuid.UUID `json:"workspace_id"`
	ScratchID           uuid.UUID `json:"scratch_id"`
	ContextID           uuid.UUID `json:"context_id"`
	ParentScratchID     uuid.UUID `json:"parent_scratch_id,omitzero"`
	PreviousCommit      string    `json:"previous_commit"`
	PreviousTree        string    `json:"previous_tree"`
	ScratchCommit       string    `json:"scratch_commit"`
	ScratchTree         string    `json:"scratch_tree"`
	WrittenPaths        []string  `json:"written_paths"`
	ChangedPaths        []string  `json:"changed_paths"`
	InputsRevision      int64     `json:"inputs_revision"`
	CaptureInputsSHA256 Digest    `json:"capture_inputs_sha256"`
	ScratchRules        string    `json:"scratch_rules"`
	ManifestSHA256      Digest    `json:"manifest_sha256"`
	PinID               uuid.UUID `json:"pin_id"`
}

// ScratchMergeBack is the payload of a workspace.scratch_merge_back v1
// record: a private merge-back plan or result. Nil WrittenPaths encodes as
// an empty array. ChangedPaths is presence-aware: nil is absent, whereas a
// non-nil empty slice encodes as []. Other zero optional fields are absent.
// Failure is an open token; unknown well-formed failures are preserved.
type ScratchMergeBack struct {
	MergeBackID         uuid.UUID `json:"merge_back_id"`
	WorkspaceID         uuid.UUID `json:"workspace_id"`
	ScratchID           uuid.UUID `json:"scratch_id"`
	ParentScratchID     uuid.UUID `json:"parent_scratch_id"`
	ContextID           uuid.UUID `json:"context_id"`
	AuthoringContextID  uuid.UUID `json:"authoring_context_id"`
	SourceObservationID uuid.UUID `json:"source_observation_id"`
	ForkCommit          string    `json:"fork_commit"`
	ForkTree            string    `json:"fork_tree"`
	ParentCommit        string    `json:"parent_commit"`
	ParentTree          string    `json:"parent_tree"`
	SourceCommit        string    `json:"source_commit"`
	SourceTree          string    `json:"source_tree"`
	InputsRevision      int64     `json:"inputs_revision"`
	CaptureInputsSHA256 Digest    `json:"capture_inputs_sha256"`
	IntegrationRules    string    `json:"integration_rules"`
	Outcome             string    `json:"outcome"`
	TargetCommit        string    `json:"target_commit,omitzero"`
	TargetTree          string    `json:"target_tree,omitzero"`
	WrittenPaths        []string  `json:"written_paths"`
	ChangedPaths        []string  `json:"changed_paths,omitzero"`
	ManifestSHA256      Digest    `json:"manifest_sha256,omitzero"`
	PinID               uuid.UUID `json:"pin_id,omitzero"`
	ConflictSHA256      Digest    `json:"conflict_sha256,omitzero"`
	ReportPinID         uuid.UUID `json:"report_pin_id,omitzero"`
	Failure             string    `json:"failure,omitzero"`
}

// Encode returns p as a workspace.scratch_changed v1 payload. Its errors
// wrap ErrInvalid. It checks payload rules, not service record rules.
func (p ScratchChanged) Encode() (json.RawMessage, error) {
	if p.WrittenPaths == nil {
		p.WrittenPaths = []string{}
	}
	if p.ChangedPaths == nil {
		p.ChangedPaths = []string{}
	}
	return encodePayload(TypeScratchChanged, p, p.check())
}

// DecodeScratchChanged decodes a workspace.scratch_changed v1 payload.
// Its errors wrap ErrInvalid.
func DecodeScratchChanged(payload json.RawMessage) (ScratchChanged, error) {
	var p ScratchChanged
	if err := decodePayload(TypeScratchChanged, payload, func(m *members) error {
		p = ScratchChanged{
			ObservationID: m.id("observation_id"), WorkspaceID: m.id("workspace_id"), ScratchID: m.id("scratch_id"), ContextID: m.id("context_id"),
			PreviousCommit: m.text("previous_commit"), PreviousTree: m.text("previous_tree"), ScratchCommit: m.text("scratch_commit"), ScratchTree: m.text("scratch_tree"),
			InputsRevision: m.nat("inputs_revision", true), CaptureInputsSHA256: m.digest("capture_inputs_sha256"),
			ScratchRules: m.text("scratch_rules"), ManifestSHA256: m.digest("manifest_sha256"), PinID: m.id("pin_id"),
		}
		p.ParentScratchID, _ = m.optionalID("parent_scratch_id")
		p.WrittenPaths, _ = m.paths("written_paths", true)
		p.ChangedPaths, _ = m.paths("changed_paths", true)
		return p.check()
	}); err != nil {
		return ScratchChanged{}, err
	}
	return p, nil
}

func (p ScratchChanged) check() error {
	for _, id := range []uuid.UUID{p.ObservationID, p.WorkspaceID, p.ScratchID, p.ContextID, p.PinID} {
		if !isV7(id) {
			return errors.New("required ID is not a UUIDv7")
		}
	}
	switch {
	case p.ParentScratchID != (uuid.UUID{}) && !isV7(p.ParentScratchID):
		return errors.New("parent_scratch_id is not a UUIDv7")
	case !validGitID(p.PreviousCommit) || !validGitID(p.PreviousTree) || !validGitID(p.ScratchCommit) || !validGitID(p.ScratchTree):
		return errors.New("tree or commit ID is not 40 or 64 lowercase hex digits")
	case p.ScratchCommit == p.PreviousCommit:
		return errors.New("scratch_commit equals previous_commit")
	case (len(p.WrittenPaths) == 0) != (p.PreviousTree == p.ScratchTree):
		return errors.New("written_paths is empty exactly when previous_tree equals scratch_tree")
	case p.InputsRevision < 1:
		return errors.New("inputs_revision is below 1")
	case p.CaptureInputsSHA256 == (Digest{}) || p.ManifestSHA256 == (Digest{}):
		return errors.New("required digest is zero")
	case !validIntegrationRules(p.ScratchRules):
		return errors.New("scratch_rules does not match the versioned rules grammar")
	}
	if err := checkRepoPaths(p.WrittenPaths); err != nil {
		return fmt.Errorf("written_paths: %w", err)
	}
	if err := checkRepoPaths(p.ChangedPaths); err != nil {
		return fmt.Errorf("changed_paths: %w", err)
	}
	return nil
}

// Encode returns p as a workspace.scratch_merge_back v1 payload. Its errors
// wrap ErrInvalid. It checks payload rules, not service record rules.
func (p ScratchMergeBack) Encode() (json.RawMessage, error) {
	if p.WrittenPaths == nil {
		p.WrittenPaths = []string{}
	}
	return encodePayload(TypeScratchMergeBack, p, p.check())
}

// DecodeScratchMergeBack decodes a workspace.scratch_merge_back v1 payload,
// including unknown well-formed failure tokens. Its errors wrap ErrInvalid.
func DecodeScratchMergeBack(payload json.RawMessage) (ScratchMergeBack, error) {
	var p ScratchMergeBack
	if err := decodePayload(TypeScratchMergeBack, payload, func(m *members) error {
		p = ScratchMergeBack{
			MergeBackID: m.id("merge_back_id"), WorkspaceID: m.id("workspace_id"), ScratchID: m.id("scratch_id"), ParentScratchID: m.id("parent_scratch_id"),
			ContextID: m.id("context_id"), AuthoringContextID: m.id("authoring_context_id"), SourceObservationID: m.id("source_observation_id"),
			ForkCommit: m.text("fork_commit"), ForkTree: m.text("fork_tree"), ParentCommit: m.text("parent_commit"), ParentTree: m.text("parent_tree"),
			SourceCommit: m.text("source_commit"), SourceTree: m.text("source_tree"), InputsRevision: m.nat("inputs_revision", true),
			CaptureInputsSHA256: m.digest("capture_inputs_sha256"), IntegrationRules: m.text("integration_rules"), Outcome: m.text("outcome"),
		}
		p.TargetCommit, _ = m.optionalText("target_commit")
		p.TargetTree, _ = m.optionalText("target_tree")
		p.WrittenPaths, _ = m.paths("written_paths", true)
		p.ChangedPaths, _ = m.paths("changed_paths", false)
		p.ManifestSHA256, _ = m.optionalDigest("manifest_sha256")
		p.PinID, _ = m.optionalID("pin_id")
		p.ConflictSHA256, _ = m.optionalDigest("conflict_sha256")
		p.ReportPinID, _ = m.optionalID("report_pin_id")
		p.Failure, _ = m.optionalText("failure")
		return p.check()
	}); err != nil {
		return ScratchMergeBack{}, err
	}
	return p, nil
}

func (p ScratchMergeBack) check() error {
	for _, id := range []uuid.UUID{p.MergeBackID, p.WorkspaceID, p.ScratchID, p.ParentScratchID, p.ContextID, p.AuthoringContextID, p.SourceObservationID} {
		if !isV7(id) {
			return errors.New("required ID is not a UUIDv7")
		}
	}
	for _, id := range []uuid.UUID{p.PinID, p.ReportPinID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("optional ID is not a UUIDv7")
		}
	}
	for _, id := range []string{p.ForkCommit, p.ForkTree, p.ParentCommit, p.ParentTree, p.SourceCommit, p.SourceTree} {
		if !validGitID(id) {
			return errors.New("tree or commit ID is not 40 or 64 lowercase hex digits")
		}
	}
	switch {
	case p.ContextID == p.AuthoringContextID:
		return errors.New("context_id equals authoring_context_id")
	case p.InputsRevision < 1:
		return errors.New("inputs_revision is below 1")
	case p.CaptureInputsSHA256 == (Digest{}):
		return errors.New("capture_inputs_sha256 is zero")
	case !validIntegrationRules(p.IntegrationRules):
		return errors.New("integration_rules does not match the versioned rules grammar")
	case (len(p.WrittenPaths) == 0) != (p.ForkTree == p.SourceTree):
		return errors.New("written_paths is empty exactly when fork_tree equals source_tree")
	}
	switch p.Outcome {
	case MergeBackPrepared, MergeBackWriteFailed, MergeBackFastForward, MergeBackMerged, MergeBackConflict:
	default:
		return errors.New("unknown merge-back outcome")
	}
	if p.Outcome == MergeBackConflict {
		if p.TargetCommit != "" || p.TargetTree != "" {
			return errors.New("conflict carries target Git IDs")
		}
	} else if !validGitID(p.TargetCommit) || !validGitID(p.TargetTree) {
		return errors.New("non-conflict requires valid target Git IDs")
	}
	if (p.ChangedPaths != nil) != (p.Outcome == MergeBackFastForward || p.Outcome == MergeBackMerged) {
		return errors.New("changed_paths is present exactly for applied outcomes")
	}
	if (p.ManifestSHA256 != (Digest{})) != (p.Outcome == MergeBackPrepared) || (p.PinID != (uuid.UUID{})) != (p.Outcome == MergeBackPrepared) {
		return errors.New("manifest_sha256 and pin_id are present exactly for prepared")
	}
	if (p.ConflictSHA256 != (Digest{})) != (p.Outcome == MergeBackConflict) || (p.ReportPinID != (uuid.UUID{})) != (p.Outcome == MergeBackConflict) {
		return errors.New("conflict_sha256 and report_pin_id are present exactly for conflict")
	}
	if (p.Failure != "") != (p.Outcome == MergeBackWriteFailed) {
		return errors.New("failure is present exactly for write_failed")
	}
	if p.Failure != "" {
		if n, ok := dottedTokens(p.Failure); !ok || n != 1 || len(p.Failure) > 64 {
			return errors.New("failure is not a token of at most 64 bytes")
		}
	}
	switch p.Outcome {
	case MergeBackFastForward:
		if p.ParentCommit != p.ForkCommit || p.TargetCommit != p.SourceCommit || p.TargetTree != p.SourceTree {
			return errors.New("fast_forward requires the fork parent and source target")
		}
	case MergeBackMerged:
		if p.ParentCommit == p.ForkCommit || p.TargetCommit == p.SourceCommit {
			return errors.New("merged requires a moved parent and a new target commit")
		}
	case MergeBackPrepared, MergeBackWriteFailed:
		if (p.TargetCommit == p.SourceCommit) != (p.ParentCommit == p.ForkCommit) {
			return errors.New("target_commit equals source_commit exactly when parent_commit equals fork_commit")
		}
	}
	if err := checkRepoPaths(p.WrittenPaths); err != nil {
		return fmt.Errorf("written_paths: %w", err)
	}
	if err := checkRepoPaths(p.ChangedPaths); err != nil {
		return fmt.Errorf("changed_paths: %w", err)
	}
	return nil
}

func validRepoPath(s string) bool {
	if s == "" || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\\") {
		return false
	}
	for component := range strings.SplitSeq(s, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func checkRepoPaths(paths []string) error {
	for i, path := range paths {
		if !validRepoPath(path) {
			return fmt.Errorf("element %d is not a repository-relative path", i)
		}
		if i > 0 && paths[i-1] >= path {
			return fmt.Errorf("element %d is not above the one before it", i)
		}
	}
	return nil
}

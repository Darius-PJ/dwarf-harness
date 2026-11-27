package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"uuid"
)

// F4-17's eleven version-1 record types are proposed for freeze (D125).
const (
	TypeValidationCheckDefined      = "validation.check_defined"
	TypeValidationRequested         = "validation.requested"
	TypeValidationRequestChanged    = "validation.request_changed"
	TypeValidationAttemptQueued     = "validation.attempt_queued"
	TypeValidationAttemptStarted    = "validation.attempt_started"
	TypeValidationAttemptFinished   = "validation.attempt_finished"
	TypeValidationEvidence          = "validation.evidence"
	TypeValidationRequestEnded      = "validation.request_ended"
	TypeValidationVisibilityChanged = "validation.visibility_changed"
	TypeValidationHookRefused       = "validation.hook_refused"
	TypeValidationReceipt           = "validation.receipt"
)

// ValidationOrigin uses the envelope's execution-session spelling. It names
// provenance and a result recipient, not a grant of current read authority.
type ValidationOrigin struct {
	AgentID            uuid.UUID `json:"agent_id"`
	ExecutionSessionID uuid.UUID `json:"execution_session_id"`
	ContextID          uuid.UUID `json:"context_id"`
	TurnID             uuid.UUID `json:"turn_id,omitzero"`
}

type ValidationTrigger struct {
	Kind             string `json:"kind"`
	SourceSequence   int64  `json:"source_sequence,omitempty"`
	SourceRecordHash Digest `json:"source_record_hash,omitzero"`
	HookID           string `json:"hook_id,omitempty"`
}

// ValidationSubject describes the immutable F2 source, never a working path.
type ValidationSubject struct {
	Kind                    string    `json:"kind"`
	WorkspaceID             uuid.UUID `json:"workspace_id"`
	ObjectFormat            string    `json:"object_format"`
	SourceSequence          int64     `json:"source_sequence"`
	SourceRecordHash        Digest    `json:"source_record_hash"`
	Commit                  string    `json:"commit"`
	Tree                    string    `json:"tree"`
	CaptureInputsRevision   int64     `json:"capture_inputs_revision"`
	CaptureInputsSHA256     Digest    `json:"capture_inputs_sha256"`
	SourceManifestSHA256    Digest    `json:"source_manifest_sha256,omitzero"`
	RequestID               uuid.UUID `json:"request_id,omitzero"`
	CaptureID               uuid.UUID `json:"capture_id,omitzero"`
	SubmissionTree          string    `json:"submission_tree,omitempty"`
	SubmissionBase          string    `json:"submission_base,omitempty"`
	GenerationID            uuid.UUID `json:"generation_id,omitzero"`
	GenerationSequence      int64     `json:"generation_sequence,omitempty"`
	GenerationHash          Digest    `json:"generation_hash,omitzero"`
	PredecessorRequestID    uuid.UUID `json:"predecessor_request_id,omitzero"`
	PredecessorGenerationID uuid.UUID `json:"predecessor_generation_id,omitzero"`
	PredecessorWitnessSeq   int64     `json:"predecessor_witness_seq,omitempty"`
	PredecessorCommit       string    `json:"predecessor_commit,omitempty"`
	PredecessorTree         string    `json:"predecessor_tree,omitempty"`
	IntegrationRules        string    `json:"integration_rules,omitempty"`
}

type ValidationAuthorization struct {
	Principal               string `json:"principal"`
	SourcePermissionsSHA256 Digest `json:"source_permissions_sha256"`
}

type ValidationRequiredCheck struct {
	CheckID           string    `json:"check_id"`
	DefinitionID      uuid.UUID `json:"definition_id"`
	CheckSHA256       Digest    `json:"check_sha256"`
	EnvironmentSHA256 Digest    `json:"environment_sha256"`
	InventorySHA256   Digest    `json:"inventory_sha256"`
}

type ValidationReproductionPlan struct {
	Subject      ValidationSubject `json:"subject"`
	DefinitionID uuid.UUID         `json:"definition_id"`
	Executions   int64             `json:"executions"`
}

type ValidationLineage struct {
	OriginKind             string    `json:"origin_kind"`
	AuthorContextID        uuid.UUID `json:"author_context_id,omitzero"`
	CloneRootContextID     uuid.UUID `json:"clone_root_context_id,omitzero"`
	DerivedFromCheckSHA256 Digest    `json:"derived_from_check_sha256,omitzero"`
	ModelFamily            string    `json:"model_family,omitempty"`
}

type ValidationClosedAttempt struct {
	AttemptID    uuid.UUID `json:"attempt_id"`
	ProcessRunID uuid.UUID `json:"process_run_id,omitzero"`
	Proof        string    `json:"proof"`
}

type ValidationCanaryResult struct {
	State          string `json:"state"`
	TemplateSHA256 Digest `json:"template_sha256"`
	CanarySHA256   Digest `json:"canary_sha256,omitzero"`
	NonceSHA256    Digest `json:"nonce_sha256,omitzero"`
	ObservedPID    int64  `json:"observed_pid,omitempty"`
	MarkerSHA256   Digest `json:"marker_sha256,omitzero"`
}

type ValidationRequiredResult struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
}

type ValidationArtifact struct {
	Kind     string `json:"kind"`
	SHA256   Digest `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Complete bool   `json:"complete"`
}

type ValidationAdjacent struct {
	PromotionRequestID uuid.UUID   `json:"promotion_request_id"`
	AcceptanceSequence int64       `json:"acceptance_sequence"`
	AcceptanceHash     Digest      `json:"acceptance_hash"`
	BeforeCommit       string      `json:"before_commit"`
	BeforeTree         string      `json:"before_tree"`
	AfterCommit        string      `json:"after_commit"`
	AfterTree          string      `json:"after_tree"`
	BeforeEvidenceIDs  []uuid.UUID `json:"before_evidence_ids"`
	AfterEvidenceIDs   []uuid.UUID `json:"after_evidence_ids"`
	Complete           bool        `json:"complete"`
}

type ValidationCheckDefined struct {
	DefinitionID          uuid.UUID         `json:"definition_id"`
	WorkspaceID           uuid.UUID         `json:"workspace_id"`
	CheckID               string            `json:"check_id"`
	CheckSHA256           Digest            `json:"check_sha256"`
	CheckPolicyHash       Digest            `json:"check_policy_hash"`
	EnvironmentPolicyHash Digest            `json:"environment_policy_hash"`
	EnvironmentSHA256     Digest            `json:"environment_sha256"`
	InventorySHA256       Digest            `json:"inventory_sha256"`
	DefiningFiles         []ValidationFile  `json:"defining_files"`
	Lineage               ValidationLineage `json:"lineage"`
	PinID                 uuid.UUID         `json:"pin_id"`
}

type ValidationRequested struct {
	ValidationID          uuid.UUID                    `json:"validation_id"`
	Origin                ValidationOrigin             `json:"origin"`
	Trigger               ValidationTrigger            `json:"trigger"`
	Subject               ValidationSubject            `json:"subject"`
	Authorization         ValidationAuthorization      `json:"authorization"`
	ActivationRecordHash  Digest                       `json:"activation_record_hash"`
	Epoch                 int64                        `json:"epoch"`
	FieldLawSHA256        Digest                       `json:"field_law_sha256"`
	ValidationPolicyHash  Digest                       `json:"validation_policy_hash"`
	CheckPolicyHash       Digest                       `json:"check_policy_hash"`
	EnvironmentPolicyHash Digest                       `json:"environment_policy_hash"`
	RequiredChecks        []ValidationRequiredCheck    `json:"required_checks"`
	CapacityProfileSHA256 Digest                       `json:"capacity_profile_sha256"`
	IncidentID            uuid.UUID                    `json:"incident_id,omitzero"`
	IncidentVersion       int64                        `json:"incident_version,omitempty"`
	ReproductionPlan      []ValidationReproductionPlan `json:"reproduction_plan,omitempty"`
}

type ValidationRequestChanged struct {
	ValidationID    uuid.UUID                 `json:"validation_id"`
	ExpectedVersion int64                     `json:"expected_version"`
	State           string                    `json:"state"`
	Reason          string                    `json:"reason"`
	ClosedAttempts  []ValidationClosedAttempt `json:"closed_attempts"`
}

type ValidationAttemptQueued struct {
	ValidationID           uuid.UUID         `json:"validation_id"`
	AttemptID              uuid.UUID         `json:"attempt_id"`
	RunID                  uuid.UUID         `json:"run_id"`
	Ordinal                int64             `json:"ordinal"`
	Subject                ValidationSubject `json:"subject"`
	DefinitionID           uuid.UUID         `json:"definition_id"`
	CheckSHA256            Digest            `json:"check_sha256"`
	EnvironmentSHA256      Digest            `json:"environment_sha256"`
	InventorySHA256        Digest            `json:"inventory_sha256"`
	RunnerSHA256           Digest            `json:"runner_sha256"`
	AdapterSHA256          Digest            `json:"adapter_sha256"`
	BindingSHA256          Digest            `json:"binding_sha256"`
	IncidentID             uuid.UUID         `json:"incident_id,omitzero"`
	IncidentExecutionIndex int64             `json:"incident_execution_index,omitempty"`
}

type ValidationAttemptStarted struct {
	ValidationID               uuid.UUID `json:"validation_id"`
	AttemptID                  uuid.UUID `json:"attempt_id"`
	RunID                      uuid.UUID `json:"run_id"`
	MaterializedManifestSHA256 Digest    `json:"materialized_manifest_sha256"`
	OverlaySHA256              Digest    `json:"overlay_sha256"`
	CanarySHA256               Digest    `json:"canary_sha256"`
	NonceSHA256                Digest    `json:"nonce_sha256"`
	ConcreteEnvironmentSHA256  Digest    `json:"concrete_environment_sha256"`
}

type ValidationAttemptFinished struct {
	ValidationID    uuid.UUID                  `json:"validation_id"`
	AttemptID       uuid.UUID                  `json:"attempt_id"`
	RunID           uuid.UUID                  `json:"run_id"`
	Outcome         string                     `json:"outcome"`
	Reason          string                     `json:"reason"`
	Started         bool                       `json:"started"`
	ExitKnown       bool                       `json:"exit_known"`
	ExitCode        *int64                     `json:"exit_code,omitempty"`
	RootExited      bool                       `json:"root_exited"`
	TreeClosed      bool                       `json:"tree_closed"`
	IOJoined        bool                       `json:"io_joined"`
	ClosureProof    string                     `json:"closure_proof"`
	Canary          ValidationCanaryResult     `json:"canary"`
	RequiredResults []ValidationRequiredResult `json:"required_results"`
	Artifacts       []ValidationArtifact       `json:"artifacts"`
	InputIntegrity  string                     `json:"input_integrity"`
	RunnerSuspect   bool                       `json:"runner_suspect"`
	RootPID         int64                      `json:"root_pid,omitempty"`
	ProcessRunID    uuid.UUID                  `json:"process_run_id,omitzero"`
}

type ValidationRequestEnded struct {
	ValidationID        uuid.UUID   `json:"validation_id"`
	ExpectedVersion     int64       `json:"expected_version"`
	Outcome             string      `json:"outcome"`
	Reason              string      `json:"reason"`
	EvidenceIDs         []uuid.UUID `json:"evidence_ids"`
	NotLaunchedCheckIDs []string    `json:"not_launched_check_ids"`
}

type ValidationVisibilityChanged struct {
	ValidationID                  uuid.UUID   `json:"validation_id"`
	AttemptIDs                    []uuid.UUID `json:"attempt_ids"`
	ExpectedVersion               int64       `json:"expected_version"`
	State                         string      `json:"state"`
	AuthorizationSHA256           Digest      `json:"authorization_sha256"`
	PredecessorAcceptanceSequence int64       `json:"predecessor_acceptance_sequence,omitempty"`
	PredecessorAcceptanceHash     Digest      `json:"predecessor_acceptance_hash,omitzero"`
}

type ValidationHookRefused struct {
	HookID           string    `json:"hook_id"`
	WorkspaceID      uuid.UUID `json:"workspace_id"`
	SourceSequence   int64     `json:"source_sequence"`
	SourceRecordHash Digest    `json:"source_record_hash"`
	Reason           string    `json:"reason"`
}

func validationDigests(ds ...Digest) bool {
	for _, d := range ds {
		if d == (Digest{}) {
			return false
		}
	}
	return true
}

func validationIDs(ids ...uuid.UUID) bool {
	for _, id := range ids {
		if !isV7(id) {
			return false
		}
	}
	return true
}

func validationVersion(version int64) bool { return version > 0 && version < math.MaxInt64 }

func validationOrigin(o ValidationOrigin) bool {
	return validationIDs(o.AgentID, o.ExecutionSessionID, o.ContextID) && (o.TurnID == (uuid.UUID{}) || isV7(o.TurnID))
}

func validationTrigger(t ValidationTrigger) bool {
	if !slices.Contains([]string{"explicit", "hook", "promotion", "reproduction"}, t.Kind) {
		return false
	}
	if t.Kind == "explicit" {
		return t.SourceSequence == 0 && t.SourceRecordHash == (Digest{}) && t.HookID == ""
	}
	return t.SourceSequence > 0 && t.SourceRecordHash != (Digest{}) && ((t.Kind == "hook" && scopedToken(t.HookID)) || (t.Kind != "hook" && t.HookID == ""))
}

func (p ValidationAttemptStarted) check() error {
	if !validationIDs(p.ValidationID, p.AttemptID, p.RunID) || !validationDigests(p.MaterializedManifestSHA256, p.OverlaySHA256, p.CanarySHA256, p.NonceSHA256, p.ConcreteEnvironmentSHA256) {
		return errors.New("invalid validation launch identities")
	}
	return nil
}

func (p ValidationRequestChanged) check() error {
	if !isV7(p.ValidationID) || !validationVersion(p.ExpectedVersion) || !slices.Contains([]string{"running", "stopping"}, p.State) || !scopedToken(p.Reason) || p.ClosedAttempts == nil {
		return errors.New("invalid validation request transition")
	}
	for i, a := range p.ClosedAttempts {
		if !isV7(a.AttemptID) || a.ProcessRunID != (uuid.UUID{}) && !isV7(a.ProcessRunID) || !slices.Contains([]string{"retained_job", "name_absent"}, a.Proof) || i > 0 && p.ClosedAttempts[i-1].AttemptID.String() >= a.AttemptID.String() {
			return errors.New("invalid deferred closure")
		}
	}
	return nil
}

func (p ValidationRequestEnded) check() error {
	if !isV7(p.ValidationID) || !validationVersion(p.ExpectedVersion) || !slices.Contains([]string{"finished", "cancelled", "interrupted", "unavailable"}, p.Outcome) || !scopedToken(p.Reason) || !scopedIDs(p.EvidenceIDs, true) || p.NotLaunchedCheckIDs == nil {
		return errors.New("invalid validation ending")
	}
	seen := map[string]bool{}
	for _, id := range p.NotLaunchedCheckIDs {
		if !scopedToken(id) || seen[id] {
			return errors.New("invalid unlaunched checks")
		}
		seen[id] = true
	}
	return nil
}

func (p ValidationVisibilityChanged) check() error {
	if !isV7(p.ValidationID) || !scopedIDs(p.AttemptIDs, false) || !validationVersion(p.ExpectedVersion) || !slices.Contains([]string{"sealed", "released", "audit_only_obsolete"}, p.State) || p.AuthorizationSHA256 == (Digest{}) || p.PredecessorAcceptanceSequence < 0 || (p.PredecessorAcceptanceSequence == 0) != (p.PredecessorAcceptanceHash == (Digest{})) {
		return errors.New("invalid validation visibility")
	}
	return nil
}

func (p ValidationHookRefused) check() error {
	if !scopedToken(p.HookID) || !isV7(p.WorkspaceID) || p.SourceSequence <= 0 || p.SourceRecordHash == (Digest{}) || !scopedToken(p.Reason) {
		return errors.New("invalid hook refusal")
	}
	return nil
}

func readValidationOrigin(m *members) ValidationOrigin {
	p := ValidationOrigin{AgentID: m.id("agent_id"), ExecutionSessionID: m.id("execution_session_id"), ContextID: m.id("context_id")}
	p.TurnID, _ = m.optionalID("turn_id")
	return p
}

func readValidationTrigger(m *members) ValidationTrigger {
	p := ValidationTrigger{Kind: m.text("kind")}
	p.SourceSequence, _ = m.optionalNat("source_sequence")
	p.SourceRecordHash, _ = m.optionalDigest("source_record_hash")
	p.HookID, _ = m.optionalText("hook_id")
	return p
}

func readValidationSubject(m *members) ValidationSubject {
	p := ValidationSubject{Kind: m.text("kind"), WorkspaceID: m.id("workspace_id"), ObjectFormat: m.text("object_format"), SourceSequence: m.nat("source_sequence", true), SourceRecordHash: m.digest("source_record_hash"), Commit: m.text("commit"), Tree: m.text("tree"), CaptureInputsRevision: m.nat("capture_inputs_revision", true), CaptureInputsSHA256: m.digest("capture_inputs_sha256")}
	p.SourceManifestSHA256, _ = m.optionalDigest("source_manifest_sha256")
	p.RequestID, _ = m.optionalID("request_id")
	p.CaptureID, _ = m.optionalID("capture_id")
	p.SubmissionTree, _ = m.optionalText("submission_tree")
	p.SubmissionBase, _ = m.optionalText("submission_base")
	p.GenerationID, _ = m.optionalID("generation_id")
	p.GenerationSequence, _ = m.optionalNat("generation_sequence")
	p.GenerationHash, _ = m.optionalDigest("generation_hash")
	p.PredecessorRequestID, _ = m.optionalID("predecessor_request_id")
	p.PredecessorGenerationID, _ = m.optionalID("predecessor_generation_id")
	p.PredecessorWitnessSeq, _ = m.optionalNat("predecessor_witness_seq")
	p.PredecessorCommit, _ = m.optionalText("predecessor_commit")
	p.PredecessorTree, _ = m.optionalText("predecessor_tree")
	p.IntegrationRules, _ = m.optionalText("integration_rules")
	allowed := []string{"kind", "workspace_id", "object_format", "source_sequence", "source_record_hash", "commit", "tree", "capture_inputs_revision", "capture_inputs_sha256"}
	if p.Kind == "observation" || p.Kind == "generation" {
		allowed = append(allowed, "source_manifest_sha256")
	}
	if p.Kind == "generation" {
		allowed = append(allowed, "request_id", "capture_id", "submission_tree", "submission_base", "generation_id", "generation_sequence", "generation_hash", "predecessor_request_id", "predecessor_generation_id", "predecessor_witness_seq", "predecessor_commit", "predecessor_tree", "integration_rules")
	}
	validationClosed(m, allowed...)
	return p
}

func readValidationRequestChanged(m *members) ValidationRequestChanged {
	return ValidationRequestChanged{m.id("validation_id"), m.nat("expected_version", true), m.text("state"), m.text("reason"), readScopedObjects(m, "closed_attempts", func(b *members) ValidationClosedAttempt {
		p := ValidationClosedAttempt{AttemptID: b.id("attempt_id"), Proof: b.text("proof")}
		p.ProcessRunID, _ = b.optionalID("process_run_id")
		return p
	})}
}

func readValidationAttemptStarted(m *members) ValidationAttemptStarted {
	return ValidationAttemptStarted{m.id("validation_id"), m.id("attempt_id"), m.id("run_id"), m.digest("materialized_manifest_sha256"), m.digest("overlay_sha256"), m.digest("canary_sha256"), m.digest("nonce_sha256"), m.digest("concrete_environment_sha256")}
}

func readValidationRequestEnded(m *members) ValidationRequestEnded {
	p := ValidationRequestEnded{ValidationID: m.id("validation_id"), ExpectedVersion: m.nat("expected_version", true), Outcome: m.text("outcome"), Reason: m.text("reason"), NotLaunchedCheckIDs: readScopedStrings(m, "not_launched_check_ids")}
	p.EvidenceIDs, _ = m.sortedIDs("evidence_ids")
	return p
}

func readValidationVisibilityChanged(m *members) ValidationVisibilityChanged {
	p := ValidationVisibilityChanged{ValidationID: m.id("validation_id"), ExpectedVersion: m.nat("expected_version", true), State: m.text("state"), AuthorizationSHA256: m.digest("authorization_sha256")}
	p.AttemptIDs, _ = m.sortedIDs("attempt_ids")
	p.PredecessorAcceptanceSequence, _ = m.optionalNat("predecessor_acceptance_sequence")
	p.PredecessorAcceptanceHash, _ = m.optionalDigest("predecessor_acceptance_hash")
	return p
}

func readValidationHookRefused(m *members) ValidationHookRefused {
	return ValidationHookRefused{m.text("hook_id"), m.id("workspace_id"), m.nat("source_sequence", true), m.digest("source_record_hash"), m.text("reason")}
}

func (p ValidationRequestChanged) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationRequestChanged, p, p.check())
}
func (p ValidationAttemptStarted) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationAttemptStarted, p, p.check())
}
func (p ValidationRequestEnded) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationRequestEnded, p, p.check())
}
func (p ValidationVisibilityChanged) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationVisibilityChanged, p, p.check())
}
func (p ValidationHookRefused) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationHookRefused, p, p.check())
}

func DecodeValidationRequestChanged(js json.RawMessage) (ValidationRequestChanged, error) {
	var p ValidationRequestChanged
	err := decodeValidationPayload(TypeValidationRequestChanged, js, func(m *members) error { p = readValidationRequestChanged(m); return p.check() })
	if err != nil {
		return ValidationRequestChanged{}, err
	}
	return p, nil
}
func DecodeValidationAttemptStarted(js json.RawMessage) (ValidationAttemptStarted, error) {
	var p ValidationAttemptStarted
	err := decodeValidationPayload(TypeValidationAttemptStarted, js, func(m *members) error { p = readValidationAttemptStarted(m); return p.check() })
	if err != nil {
		return ValidationAttemptStarted{}, err
	}
	return p, nil
}
func DecodeValidationRequestEnded(js json.RawMessage) (ValidationRequestEnded, error) {
	var p ValidationRequestEnded
	err := decodeValidationPayload(TypeValidationRequestEnded, js, func(m *members) error { p = readValidationRequestEnded(m); return p.check() })
	if err != nil {
		return ValidationRequestEnded{}, err
	}
	return p, nil
}
func DecodeValidationVisibilityChanged(js json.RawMessage) (ValidationVisibilityChanged, error) {
	var p ValidationVisibilityChanged
	err := decodeValidationPayload(TypeValidationVisibilityChanged, js, func(m *members) error { p = readValidationVisibilityChanged(m); return p.check() })
	if err != nil {
		return ValidationVisibilityChanged{}, err
	}
	return p, nil
}
func DecodeValidationHookRefused(js json.RawMessage) (ValidationHookRefused, error) {
	var p ValidationHookRefused
	err := decodeValidationPayload(TypeValidationHookRefused, js, func(m *members) error { p = readValidationHookRefused(m); return p.check() })
	if err != nil {
		return ValidationHookRefused{}, err
	}
	return p, nil
}

func readValidationLineage(m *members) ValidationLineage {
	p := ValidationLineage{OriginKind: m.text("origin_kind")}
	p.AuthorContextID, _ = m.optionalID("author_context_id")
	p.CloneRootContextID, _ = m.optionalID("clone_root_context_id")
	p.DerivedFromCheckSHA256, _ = m.optionalDigest("derived_from_check_sha256")
	p.ModelFamily, _ = m.optionalText("model_family")
	return p
}

func readValidationCheckDefined(m *members) ValidationCheckDefined {
	return ValidationCheckDefined{
		DefinitionID: m.id("definition_id"), WorkspaceID: m.id("workspace_id"), CheckID: m.text("check_id"),
		CheckSHA256: m.digest("check_sha256"), CheckPolicyHash: m.digest("check_policy_hash"),
		EnvironmentPolicyHash: m.digest("environment_policy_hash"), EnvironmentSHA256: m.digest("environment_sha256"), InventorySHA256: m.digest("inventory_sha256"),
		DefiningFiles: readScopedObjects(m, "defining_files", func(b *members) ValidationFile {
			return ValidationFile{b.text("path"), b.text("mode"), b.nat("bytes", true), b.digest("sha256")}
		}),
		Lineage: graphObject(m, "lineage", readValidationLineage), PinID: m.id("pin_id"),
	}
}

func readValidationRequested(m *members) ValidationRequested {
	p := ValidationRequested{
		ValidationID: m.id("validation_id"), Origin: graphObject(m, "origin", readValidationOrigin),
		Trigger: graphObject(m, "trigger", readValidationTrigger), Subject: graphObject(m, "subject", readValidationSubject),
		Authorization: graphObject(m, "authorization", func(b *members) ValidationAuthorization {
			return ValidationAuthorization{b.text("principal"), b.digest("source_permissions_sha256")}
		}),
		ActivationRecordHash: m.digest("activation_record_hash"), Epoch: m.nat("epoch", true),
		FieldLawSHA256: m.digest("field_law_sha256"), ValidationPolicyHash: m.digest("validation_policy_hash"),
		CheckPolicyHash: m.digest("check_policy_hash"), EnvironmentPolicyHash: m.digest("environment_policy_hash"),
		RequiredChecks: readScopedObjects(m, "required_checks", func(b *members) ValidationRequiredCheck {
			return ValidationRequiredCheck{b.text("check_id"), b.id("definition_id"), b.digest("check_sha256"), b.digest("environment_sha256"), b.digest("inventory_sha256")}
		}),
		CapacityProfileSHA256: m.digest("capacity_profile_sha256"),
	}
	p.IncidentID, _ = m.optionalID("incident_id")
	p.IncidentVersion, _ = m.optionalNat("incident_version")
	if _, ok := m.get("reproduction_plan", false); ok {
		p.ReproductionPlan = readScopedObjects(m, "reproduction_plan", func(b *members) ValidationReproductionPlan {
			return ValidationReproductionPlan{graphObject(b, "subject", readValidationSubject), b.id("definition_id"), b.nat("executions", true)}
		})
	}
	return p
}

func readValidationAttemptQueued(m *members) ValidationAttemptQueued {
	p := ValidationAttemptQueued{
		ValidationID: m.id("validation_id"), AttemptID: m.id("attempt_id"), RunID: m.id("run_id"), Ordinal: m.nat("ordinal", true),
		Subject: graphObject(m, "subject", readValidationSubject), DefinitionID: m.id("definition_id"),
		CheckSHA256: m.digest("check_sha256"), EnvironmentSHA256: m.digest("environment_sha256"), InventorySHA256: m.digest("inventory_sha256"),
		RunnerSHA256: m.digest("runner_sha256"), AdapterSHA256: m.digest("adapter_sha256"), BindingSHA256: m.digest("binding_sha256"),
	}
	p.IncidentID, _ = m.optionalID("incident_id")
	p.IncidentExecutionIndex, _ = m.optionalNat("incident_execution_index")
	return p
}

func readValidationAttemptFinished(m *members) ValidationAttemptFinished {
	p := ValidationAttemptFinished{
		ValidationID: m.id("validation_id"), AttemptID: m.id("attempt_id"), RunID: m.id("run_id"),
		Outcome: m.text("outcome"), Reason: m.text("reason"), Started: m.boolean("started"), ExitKnown: m.boolean("exit_known"),
		RootExited: m.boolean("root_exited"), TreeClosed: m.boolean("tree_closed"), IOJoined: m.boolean("io_joined"), ClosureProof: m.text("closure_proof"),
		Canary: graphObject(m, "canary", func(b *members) ValidationCanaryResult {
			p := ValidationCanaryResult{State: b.text("state"), TemplateSHA256: b.digest("template_sha256")}
			p.CanarySHA256, _ = b.optionalDigest("canary_sha256")
			p.NonceSHA256, _ = b.optionalDigest("nonce_sha256")
			p.ObservedPID, _ = b.optionalNat("observed_pid")
			p.MarkerSHA256, _ = b.optionalDigest("marker_sha256")
			return p
		}),
		RequiredResults: readScopedObjects(m, "required_results", func(b *members) ValidationRequiredResult {
			return ValidationRequiredResult{b.text("package"), b.text("name"), b.text("outcome")}
		}),
		Artifacts: readScopedObjects(m, "artifacts", func(b *members) ValidationArtifact {
			return ValidationArtifact{b.text("kind"), b.digest("sha256"), b.nat("bytes", true), b.boolean("complete")}
		}),
		InputIntegrity: m.text("input_integrity"), RunnerSuspect: m.boolean("runner_suspect"),
	}
	if n, ok := m.signed("exit_code", false); ok {
		p.ExitCode = &n
	}
	p.RootPID, _ = m.optionalNat("root_pid")
	p.ProcessRunID, _ = m.optionalID("process_run_id")
	return p
}

func (p ValidationCheckDefined) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationCheckDefined, p, p.check())
}
func (p ValidationRequested) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationRequested, p, p.check())
}
func (p ValidationAttemptQueued) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationAttemptQueued, p, p.check())
}
func (p ValidationAttemptFinished) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationAttemptFinished, p, p.check())
}

func DecodeValidationCheckDefined(js json.RawMessage) (ValidationCheckDefined, error) {
	var p ValidationCheckDefined
	err := decodeValidationPayload(TypeValidationCheckDefined, js, func(m *members) error { p = readValidationCheckDefined(m); return p.check() })
	if err != nil {
		return ValidationCheckDefined{}, err
	}
	return p, nil
}
func DecodeValidationRequested(js json.RawMessage) (ValidationRequested, error) {
	var p ValidationRequested
	err := decodeValidationPayload(TypeValidationRequested, js, func(m *members) error { p = readValidationRequested(m); return p.check() })
	if err != nil {
		return ValidationRequested{}, err
	}
	return p, nil
}
func DecodeValidationAttemptQueued(js json.RawMessage) (ValidationAttemptQueued, error) {
	var p ValidationAttemptQueued
	err := decodeValidationPayload(TypeValidationAttemptQueued, js, func(m *members) error { p = readValidationAttemptQueued(m); return p.check() })
	if err != nil {
		return ValidationAttemptQueued{}, err
	}
	return p, nil
}
func DecodeValidationAttemptFinished(js json.RawMessage) (ValidationAttemptFinished, error) {
	var p ValidationAttemptFinished
	err := decodeValidationPayload(TypeValidationAttemptFinished, js, func(m *members) error { p = readValidationAttemptFinished(m); return p.check() })
	if err != nil {
		return ValidationAttemptFinished{}, err
	}
	return p, nil
}

// ValidationBinding is the typed binding_sha256 preimage, not a cache key.
type ValidationBinding struct {
	Schema                int64             `json:"schema"`
	ColonyID              uuid.UUID         `json:"colony_id"`
	Origin                ValidationOrigin  `json:"origin"`
	Trigger               ValidationTrigger `json:"trigger"`
	Subject               ValidationSubject `json:"subject"`
	ActivationRecordHash  Digest            `json:"activation_record_hash"`
	Epoch                 int64             `json:"epoch"`
	FieldLawSHA256        Digest            `json:"field_law_sha256"`
	ValidationPolicyHash  Digest            `json:"validation_policy_hash"`
	CheckPolicyHash       Digest            `json:"check_policy_hash"`
	EnvironmentPolicyHash Digest            `json:"environment_policy_hash"`
	CheckSHA256           Digest            `json:"check_sha256"`
	EnvironmentSHA256     Digest            `json:"environment_sha256"`
	InventorySHA256       Digest            `json:"inventory_sha256"`
	RunnerSHA256          Digest            `json:"runner_sha256"`
	AdapterSHA256         Digest            `json:"adapter_sha256"`
}

// ValidationComparison deliberately excludes the triggering command, origin,
// activation revision and concrete run state. Equality permits comparison,
// never reuse of an execution or disclosure of its output.
type ValidationComparison struct {
	Schema               int64             `json:"schema"`
	Subject              ValidationSubject `json:"subject"`
	CheckSHA256          Digest            `json:"check_sha256"`
	EnvironmentSHA256    Digest            `json:"environment_sha256"`
	InventorySHA256      Digest            `json:"inventory_sha256"`
	FieldLawSHA256       Digest            `json:"field_law_sha256"`
	ValidationPolicyHash Digest            `json:"validation_policy_hash"`
	RunnerSHA256         Digest            `json:"runner_sha256"`
	AdapterSHA256        Digest            `json:"adapter_sha256"`
}

func (p ValidationCheckDefined) check() error {
	if !validationIDs(p.DefinitionID, p.WorkspaceID, p.PinID) || !scopedToken(p.CheckID) ||
		!validationDigests(p.CheckSHA256, p.CheckPolicyHash, p.EnvironmentPolicyHash, p.EnvironmentSHA256, p.InventorySHA256) ||
		!validationFiles(p.DefiningFiles) {
		return errors.New("invalid validation definition")
	}
	return p.Lineage.check()
}

func (p ValidationRequested) check() error {
	if !isV7(p.ValidationID) || !validationOrigin(p.Origin) || !validationTrigger(p.Trigger) || p.Epoch <= 0 ||
		!validationDigests(p.ActivationRecordHash, p.FieldLawSHA256, p.ValidationPolicyHash, p.CheckPolicyHash, p.EnvironmentPolicyHash, p.CapacityProfileSHA256, p.Authorization.SourcePermissionsSHA256) ||
		!slices.Contains([]string{"owner", "hook"}, p.Authorization.Principal) || (p.Authorization.Principal == "hook") != (p.Trigger.Kind == "hook") ||
		len(p.RequiredChecks) == 0 {
		return errors.New("invalid validation request")
	}
	switch p.Trigger.Kind {
	case "hook":
		if p.Origin.TurnID == (uuid.UUID{}) || p.Subject.Kind != "observation" ||
			p.Subject.SourceSequence != p.Trigger.SourceSequence || p.Subject.SourceRecordHash != p.Trigger.SourceRecordHash {
			return errors.New("hook request must name its turn and source observation")
		}
	case "explicit", "reproduction":
		if p.Origin.TurnID != (uuid.UUID{}) {
			return errors.New("owner request must not name a turn")
		}
	}
	authorization, err := p.AuthorizationDocument().Encode()
	if err != nil {
		return err
	}
	if p.Authorization.SourcePermissionsSHA256 != Digest(sha256.Sum256(authorization)) {
		return errors.New("authorization digest differs from the request's canonical document")
	}
	if err := p.Subject.check(); err != nil {
		return err
	}
	seen := map[string]bool{}
	definitions := map[uuid.UUID]bool{}
	for _, c := range p.RequiredChecks {
		if !scopedToken(c.CheckID) || seen[c.CheckID] || !isV7(c.DefinitionID) || definitions[c.DefinitionID] ||
			!validationDigests(c.CheckSHA256, c.EnvironmentSHA256, c.InventorySHA256) {
			return errors.New("invalid required check binding")
		}
		seen[c.CheckID], definitions[c.DefinitionID] = true, true
	}
	if p.IncidentID == (uuid.UUID{}) {
		if p.IncidentVersion != 0 || p.ReproductionPlan != nil || p.Trigger.Kind == "reproduction" {
			return errors.New("reproduction members without incident")
		}
		return nil
	}
	if !isV7(p.IncidentID) || !validationVersion(p.IncidentVersion) || p.Trigger.Kind != "reproduction" ||
		len(p.RequiredChecks) != 1 || len(p.ReproductionPlan) < 1 || len(p.ReproductionPlan) > 2 {
		return errors.New("incomplete incident binding")
	}
	var executions int64
	for _, plan := range p.ReproductionPlan {
		if !definitions[plan.DefinitionID] || plan.Executions <= 0 || plan.Executions > math.MaxInt64-executions ||
			plan.Subject.WorkspaceID != p.Subject.WorkspaceID {
			return errors.New("invalid reproduction plan")
		}
		if err := plan.Subject.check(); err != nil {
			return err
		}
		executions += plan.Executions
	}
	return nil
}

func (p ValidationAttemptQueued) check() error {
	if !validationIDs(p.ValidationID, p.AttemptID, p.RunID, p.DefinitionID) || p.Ordinal <= 0 ||
		!validationDigests(p.CheckSHA256, p.EnvironmentSHA256, p.InventorySHA256, p.RunnerSHA256, p.AdapterSHA256, p.BindingSHA256) {
		return errors.New("invalid queued validation attempt")
	}
	if p.IncidentID == (uuid.UUID{}) {
		if p.IncidentExecutionIndex != 0 {
			return errors.New("incident index without incident")
		}
	} else if !isV7(p.IncidentID) || p.IncidentExecutionIndex < 2 {
		return errors.New("incident execution index must be at least two")
	}
	return p.Subject.check()
}

func (p ValidationBinding) Encode() (json.RawMessage, error) {
	var err error
	if p.Schema != 1 {
		return nil, ErrUnsupportedValidation
	}
	if !isV7(p.ColonyID) || !validationOrigin(p.Origin) || !validationTrigger(p.Trigger) || p.Epoch <= 0 ||
		!validationDigests(p.ActivationRecordHash, p.FieldLawSHA256, p.ValidationPolicyHash, p.CheckPolicyHash, p.EnvironmentPolicyHash, p.CheckSHA256, p.EnvironmentSHA256, p.InventorySHA256, p.RunnerSHA256, p.AdapterSHA256) {
		err = errors.New("invalid validation binding preimage")
	} else {
		err = p.Subject.check()
	}
	return encodeValidationPayload("binding preimage", p, err)
}

func (p ValidationComparison) Encode() (json.RawMessage, error) {
	var err error
	if p.Schema != 1 {
		return nil, ErrUnsupportedValidation
	}
	if !validationDigests(p.CheckSHA256, p.EnvironmentSHA256, p.InventorySHA256, p.FieldLawSHA256, p.ValidationPolicyHash, p.RunnerSHA256, p.AdapterSHA256) {
		err = errors.New("invalid validation comparison preimage")
	} else {
		err = p.Subject.check()
	}
	return encodeValidationPayload("comparison preimage", p, err)
}

func (p ValidationAttemptFinished) check() error {
	if !validationIDs(p.ValidationID, p.AttemptID, p.RunID) ||
		!slices.Contains([]string{"passed", "failed", "skipped", "timeout", "incomplete", "infra_failure", "no_status"}, p.Outcome) ||
		!scopedToken(p.Reason) || p.ExitKnown != (p.ExitCode != nil) || p.RootPID < 0 ||
		p.ProcessRunID != (uuid.UUID{}) && !isV7(p.ProcessRunID) ||
		!slices.Contains([]string{"live", "retained_job", "name_absent", "unconfirmed", "not_started"}, p.ClosureProof) ||
		!slices.Contains([]string{"verified", "changed", "missing", "unavailable"}, p.InputIntegrity) ||
		p.RequiredResults == nil || p.Artifacts == nil {
		return errors.New("invalid validation finish")
	}
	if err := p.Canary.check(p.Started); err != nil {
		return err
	}
	if !p.Started {
		if (p.Outcome != "infra_failure" && p.Outcome != "incomplete") || p.Canary.State != "not_run" ||
			p.ClosureProof != "not_started" || p.ExitKnown || p.RootExited || p.TreeClosed || p.IOJoined ||
			p.ExitCode != nil || p.RootPID != 0 || p.ProcessRunID != (uuid.UUID{}) || len(p.RequiredResults) != 0 {
			return errors.New("unlaunched validation has execution evidence")
		}
	} else if len(p.RequiredResults) == 0 {
		return errors.New("started validation lacks required results")
	}
	if slices.Contains([]string{"passed", "failed", "skipped"}, p.Outcome) && p.Canary.State != "expected_failed" ||
		p.Outcome == "infra_failure" && p.Canary.State != "not_run" ||
		slices.Contains([]string{"passed", "missing", "ambiguous", "unbound"}, p.Canary.State) && !p.RunnerSuspect {
		return errors.New("canary state contradicts outcome or runner suspicion")
	}
	previous := ""
	for _, r := range p.RequiredResults {
		key := r.Package + "\x00" + r.Name
		if key <= previous {
			return errors.New("required result order")
		}
		if err := r.check(); err != nil {
			return err
		}
		previous = key
	}
	for i, a := range p.Artifacts {
		if !slices.Contains([]string{"stdout", "stderr", "events", "manifest", "overlay", "integrity"}, a.Kind) ||
			a.SHA256 == (Digest{}) || a.Bytes < 0 || i > 0 && p.Artifacts[i-1].Kind >= a.Kind {
			return errors.New("invalid validation artifact")
		}
	}
	if p.Outcome == "passed" {
		if !p.Started || !p.ExitKnown || !p.RootExited || !p.TreeClosed || !p.IOJoined ||
			p.ClosureProof != "live" || p.Canary.State != "expected_failed" ||
			p.InputIntegrity != "verified" || p.RunnerSuspect || len(p.RequiredResults) == 0 {
			return errors.New("passing validation lacks full proof")
		}
		for _, r := range p.RequiredResults {
			if r.Outcome != "passed" {
				return errors.New("passing validation has a nonpassing required node")
			}
		}
		for _, a := range p.Artifacts {
			if !a.Complete {
				return errors.New("passing validation has an incomplete artifact")
			}
		}
	}
	return nil
}

func (p ValidationLineage) check() error {
	if p.OriginKind != "owner_policy" ||
		p.AuthorContextID != (uuid.UUID{}) && !isV7(p.AuthorContextID) ||
		p.CloneRootContextID != (uuid.UUID{}) && !isV7(p.CloneRootContextID) ||
		p.ModelFamily != "" && !validationText(p.ModelFamily) {
		return errors.New("invalid validation lineage")
	}
	return nil
}

func (p ValidationSubject) check() error {
	oid := func(s string) bool {
		return validGitID(s) && (p.ObjectFormat == "sha1" && len(s) == 40 || p.ObjectFormat == "sha256" && len(s) == 64)
	}
	if !isV7(p.WorkspaceID) || p.SourceSequence <= 0 || p.CaptureInputsRevision <= 0 ||
		!validationDigests(p.SourceRecordHash, p.CaptureInputsSHA256) || !oid(p.Commit) || !oid(p.Tree) {
		return errors.New("invalid validation subject source")
	}
	if p.Kind != "generation" {
		if p.RequestID != (uuid.UUID{}) || p.CaptureID != (uuid.UUID{}) || p.SubmissionTree != "" || p.SubmissionBase != "" ||
			p.GenerationID != (uuid.UUID{}) || p.GenerationSequence != 0 || p.GenerationHash != (Digest{}) ||
			p.PredecessorRequestID != (uuid.UUID{}) || p.PredecessorGenerationID != (uuid.UUID{}) || p.PredecessorWitnessSeq != 0 ||
			p.PredecessorCommit != "" || p.PredecessorTree != "" || p.IntegrationRules != "" {
			return errors.New("nongeneration subject has generation members")
		}
		switch p.Kind {
		case "canonical":
			if p.SourceManifestSHA256 != (Digest{}) {
				return errors.New("canonical subject has a source manifest")
			}
		case "observation":
			if p.SourceManifestSHA256 == (Digest{}) {
				return errors.New("observation subject lacks a source manifest")
			}
		default:
			return errors.New("unknown validation subject kind")
		}
		return nil
	}
	if !validationIDs(p.RequestID, p.CaptureID, p.GenerationID) || p.GenerationSequence <= 0 ||
		!validationDigests(p.SourceManifestSHA256, p.GenerationHash) || !oid(p.SubmissionTree) || !oid(p.SubmissionBase) ||
		!oid(p.PredecessorCommit) || !oid(p.PredecessorTree) || !validIntegrationRules(p.IntegrationRules) ||
		(p.PredecessorRequestID == (uuid.UUID{})) != (p.PredecessorGenerationID == (uuid.UUID{})) || p.PredecessorWitnessSeq < 0 {
		return errors.New("invalid generation subject")
	}
	if p.PredecessorRequestID != (uuid.UUID{}) && (!validationIDs(p.PredecessorRequestID, p.PredecessorGenerationID) || p.PredecessorWitnessSeq != 0) {
		return errors.New("generation predecessor identities conflict")
	}
	return nil
}

func (p ValidationCanaryResult) check(started bool) error {
	if !slices.Contains([]string{"expected_failed", "passed", "missing", "ambiguous", "unbound", "not_run"}, p.State) ||
		p.TemplateSHA256 == (Digest{}) || (p.CanarySHA256 == (Digest{})) != (p.NonceSHA256 == (Digest{})) ||
		started && p.CanarySHA256 == (Digest{}) || p.ObservedPID < 0 ||
		(p.ObservedPID == 0) != (p.MarkerSHA256 == (Digest{})) {
		return errors.New("invalid validation canary result")
	}
	switch p.State {
	case "expected_failed", "unbound":
		if p.ObservedPID == 0 {
			return errors.New("canary result lacks marker provenance")
		}
	case "missing", "ambiguous", "not_run":
		if p.ObservedPID != 0 {
			return errors.New("canary state forbids marker provenance")
		}
	}
	return nil
}

func (p ValidationRequiredResult) check() error {
	if !validationText(p.Package) || !validationText(p.Name) ||
		!slices.Contains([]string{"passed", "failed", "skipped", "missing", "unfinished"}, p.Outcome) {
		return errors.New("invalid required-node result")
	}
	return nil
}

// ValidationRational preserves the law's unreduced uint64 pair exactly.
type ValidationRational struct {
	Numerator   uint64 `json:"numerator"`
	Denominator uint64 `json:"denominator"`
}

func (p ValidationRational) valid() bool { return p.Numerator > 0 && p.Numerator < p.Denominator }

func readValidationRational(m *members) ValidationRational {
	read := func(key string) uint64 {
		raw, ok := m.m[key]
		if !ok || len(raw) == 0 || raw[0] < '1' || raw[0] > '9' || strings.IndexFunc(string(raw), func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			m.err = errors.New("rational requires a positive plain uint64")
			return 0
		}
		n, err := strconv.ParseUint(string(raw), 10, 64)
		if err != nil {
			m.err = err
		}
		return n
	}
	return ValidationRational{read("numerator"), read("denominator")}
}

type ValidationEvidence struct {
	EvidenceID        uuid.UUID           `json:"evidence_id"`
	ValidationID      uuid.UUID           `json:"validation_id"`
	Subject           ValidationSubject   `json:"subject"`
	DefinitionID      uuid.UUID           `json:"definition_id"`
	CheckSHA256       Digest              `json:"check_sha256"`
	EnvironmentSHA256 Digest              `json:"environment_sha256"`
	ComparisonSHA256  Digest              `json:"comparison_sha256"`
	BindingSHA256     Digest              `json:"binding_sha256"`
	AttemptIDs        []uuid.UUID         `json:"attempt_ids"`
	Summary           string              `json:"summary"`
	Canonical         bool                `json:"canonical"`
	FailureGrade      string              `json:"failure_grade"`
	IncidentID        uuid.UUID           `json:"incident_id,omitzero"`
	FailureCount      int64               `json:"failure_count"`
	PassCount         int64               `json:"pass_count"`
	RequiredCount     int64               `json:"required_count"`
	Prior             ValidationRational  `json:"prior"`
	Confidence        ValidationRational  `json:"confidence"`
	Adjacent          *ValidationAdjacent `json:"adjacent,omitempty"`
}

func (p ValidationEvidence) check() error {
	if !validationIDs(p.EvidenceID, p.ValidationID, p.DefinitionID) ||
		!validationDigests(p.CheckSHA256, p.EnvironmentSHA256, p.ComparisonSHA256, p.BindingSHA256) ||
		!validationOrderedIDs(p.AttemptIDs, false) || !slices.Contains([]string{"passed", "failed", "flaky", "inconclusive"}, p.Summary) ||
		!slices.Contains([]string{"none", "F1", "F2"}, p.FailureGrade) || p.FailureCount < 0 || p.PassCount < 0 || p.RequiredCount <= 0 ||
		p.FailureCount > int64(len(p.AttemptIDs)) || p.PassCount > int64(len(p.AttemptIDs))-p.FailureCount ||
		!p.Prior.valid() || !p.Confidence.valid() || p.IncidentID != (uuid.UUID{}) && !isV7(p.IncidentID) {
		return errors.New("invalid validation evidence")
	}
	if err := p.Subject.check(); err != nil {
		return err
	}
	if p.Canonical && p.Subject.Kind != "canonical" ||
		p.FailureGrade != "none" && (!p.Canonical || p.IncidentID == (uuid.UUID{}) || p.FailureCount == 0) ||
		p.FailureGrade == "F2" && (p.FailureCount < p.RequiredCount || p.PassCount != 0 || p.Summary != "failed") {
		return errors.New("failure grade lacks canonical failed evidence")
	}
	if p.Summary == "passed" && (p.PassCount == 0 || p.FailureCount != 0) ||
		p.Summary == "failed" && (p.FailureCount == 0 || p.PassCount != 0) ||
		p.Summary == "flaky" && (p.PassCount == 0 || p.FailureCount == 0) {
		return errors.New("summary contradicts evidence counts")
	}
	if p.Adjacent != nil {
		return p.Adjacent.check()
	}
	return nil
}

func validationOrderedIDs(ids []uuid.UUID, empty bool) bool {
	if ids == nil || !empty && len(ids) == 0 {
		return false
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if !isV7(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (p ValidationAdjacent) check() error {
	if !isV7(p.PromotionRequestID) || p.AcceptanceSequence <= 0 || p.AcceptanceHash == (Digest{}) ||
		!validGitID(p.BeforeCommit) || !validGitID(p.BeforeTree) || !validGitID(p.AfterCommit) || !validGitID(p.AfterTree) ||
		len(p.BeforeCommit) != len(p.AfterCommit) || len(p.BeforeCommit) != len(p.BeforeTree) || len(p.BeforeTree) != len(p.AfterTree) ||
		!scopedIDs(p.BeforeEvidenceIDs, true) || !scopedIDs(p.AfterEvidenceIDs, true) ||
		p.Complete && (len(p.BeforeEvidenceIDs) == 0 || len(p.AfterEvidenceIDs) == 0) {
		return errors.New("invalid adjacent validation evidence")
	}
	return nil
}

func readValidationAdjacent(m *members) ValidationAdjacent {
	p := ValidationAdjacent{PromotionRequestID: m.id("promotion_request_id"), AcceptanceSequence: m.nat("acceptance_sequence", true), AcceptanceHash: m.digest("acceptance_hash"), BeforeCommit: m.text("before_commit"), BeforeTree: m.text("before_tree"), AfterCommit: m.text("after_commit"), AfterTree: m.text("after_tree"), Complete: m.boolean("complete")}
	p.BeforeEvidenceIDs, _ = m.sortedIDs("before_evidence_ids")
	p.AfterEvidenceIDs, _ = m.sortedIDs("after_evidence_ids")
	return p
}

func readValidationEvidence(m *members) ValidationEvidence {
	p := ValidationEvidence{EvidenceID: m.id("evidence_id"), ValidationID: m.id("validation_id"), Subject: graphObject(m, "subject", readValidationSubject), DefinitionID: m.id("definition_id"), CheckSHA256: m.digest("check_sha256"), EnvironmentSHA256: m.digest("environment_sha256"), ComparisonSHA256: m.digest("comparison_sha256"), BindingSHA256: m.digest("binding_sha256"), Summary: m.text("summary"), Canonical: m.boolean("canonical"), FailureGrade: m.text("failure_grade"), FailureCount: m.nat("failure_count", true), PassCount: m.nat("pass_count", true), RequiredCount: m.nat("required_count", true), Prior: graphObject(m, "prior", readValidationRational), Confidence: graphObject(m, "confidence", readValidationRational)}
	p.AttemptIDs = []uuid.UUID{}
	for _, text := range readScopedStrings(m, "attempt_ids") {
		id, ok := parseID(text)
		if !ok {
			m.err = errors.New("invalid attempt ID")
			break
		}
		p.AttemptIDs = append(p.AttemptIDs, id)
	}
	p.IncidentID, _ = m.optionalID("incident_id")
	p.Adjacent = optionalObject(m, "adjacent", readValidationAdjacent)
	return p
}

func (p ValidationEvidence) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationEvidence, p, p.check())
}

func DecodeValidationEvidence(js json.RawMessage) (ValidationEvidence, error) {
	var p ValidationEvidence
	err := decodeValidationPayload(TypeValidationEvidence, js, func(m *members) error { p = readValidationEvidence(m); return p.check() })
	if err != nil {
		return ValidationEvidence{}, err
	}
	return p, nil
}

// ValidationBodyRefs returns the sorted distinct direct body-reference set.
// For a definition, supply both loaded specifications to include its three
// transitive bodies. Replay supplies neither and obtains the payload-visible
// subset; it checks that the record covers it and its permanent pin agrees.
// Other records require nil specifications. Preimage and record hashes are
// never included. An empty result is nil, for an absent envelope member.
func ValidationBodyRefs(p any, check *CheckSpec, environment *EnvironmentSpec) ([]Digest, error) {
	var refs []Digest
	var err error
	switch p := p.(type) {
	case ValidationCheckDefined:
		err = p.check()
		refs = []Digest{p.CheckPolicyHash, p.EnvironmentPolicyHash}
		for _, f := range p.DefiningFiles {
			refs = append(refs, f.SHA256)
		}
		if (check == nil) != (environment == nil) {
			return nil, errors.New("definition requires both specifications or neither")
		}
		if check != nil {
			if check.check() != nil || environment.check() != nil || check.CheckID != p.CheckID || check.EnvironmentID != environment.EnvironmentID {
				return nil, errors.New("definition specifications do not match")
			}
			refs = append(refs, check.Canary.TemplateSHA256, environment.ToolchainManifestSHA256, environment.DependencyManifestSHA256)
		}
	case ValidationRequested:
		err = p.check()
		refs = []Digest{p.Authorization.SourcePermissionsSHA256}
	case ValidationAttemptStarted:
		err = p.check()
		refs = []Digest{p.MaterializedManifestSHA256, p.OverlaySHA256}
	case ValidationAttemptFinished:
		err = p.check()
		for _, a := range p.Artifacts {
			refs = append(refs, a.SHA256)
		}
	case ValidationVisibilityChanged:
		err = p.check()
		refs = []Digest{p.AuthorizationSHA256}
	case ValidationRequestChanged:
		err = p.check()
	case ValidationAttemptQueued:
		err = p.check()
	case ValidationEvidence:
		err = p.check()
	case ValidationRequestEnded:
		err = p.check()
	case ValidationHookRefused:
		err = p.check()
	case ValidationReceipt:
		err = p.check()
	default:
		return nil, errors.New("not a validation payload")
	}
	if _, definition := p.(ValidationCheckDefined); !definition && (check != nil || environment != nil) {
		return nil, errors.New("transitive specification supplied for a nondefinition")
	}
	if err != nil {
		return nil, err
	}
	slices.SortFunc(refs, func(a, b Digest) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(refs), nil
}

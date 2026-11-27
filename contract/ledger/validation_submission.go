package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"uuid"
)

// ValidationSubmission is the canonical schema-10 preimage, frozen (D189).
// Arguments is the matching concrete value, never arbitrary JSON.
// Origin is the established command origin; requested.origin additionally
// preserves the optional turn and the envelope's execution-session spelling.
type ValidationSubmission struct {
	Schema          int64  `json:"schema"`
	Actor           string `json:"actor"`
	Origin          Origin `json:"origin"`
	Operation       string `json:"operation"`
	ExpectedVersion int64  `json:"expected_version"`
	Arguments       any    `json:"arguments"`
}

type ValidationDefineArguments ValidationCheckDefined
type ValidationLaunchArguments ValidationAttemptStarted
type ValidationRefuseArguments ValidationHookRefused

type ValidationRequestArguments struct {
	Requested ValidationRequested       `json:"requested"`
	Attempts  []ValidationAttemptQueued `json:"attempts"`
	PinID     uuid.UUID                 `json:"pin_id"`
}

type ValidationFinishArguments struct {
	AttemptFinished ValidationAttemptFinished `json:"attempt_finished"`
	Evidence        ValidationEvidence        `json:"evidence"`
	FailurePinID    uuid.UUID                 `json:"failure_pin_id,omitzero"`
}

type ValidationChangeArguments struct {
	ValidationID uuid.UUID `json:"validation_id"`
	Reason       string    `json:"reason"`
}

type ValidationEndArguments struct {
	ValidationID        uuid.UUID   `json:"validation_id"`
	Outcome             string      `json:"outcome"`
	Reason              string      `json:"reason"`
	EvidenceIDs         []uuid.UUID `json:"evidence_ids"`
	NotLaunchedCheckIDs []string    `json:"not_launched_check_ids"`
}

type ValidationVisibilityArguments struct {
	ValidationID                  uuid.UUID   `json:"validation_id"`
	AttemptIDs                    []uuid.UUID `json:"attempt_ids"`
	State                         string      `json:"state"`
	AuthorizationSHA256           Digest      `json:"authorization_sha256"`
	PredecessorAcceptanceSequence int64       `json:"predecessor_acceptance_sequence,omitempty"`
	PredecessorAcceptanceHash     Digest      `json:"predecessor_acceptance_hash,omitzero"`
}

type ValidationReconcileChanged struct {
	Reason         string                    `json:"reason"`
	ClosedAttempts []ValidationClosedAttempt `json:"closed_attempts"`
}

type ValidationReconcileEnded struct {
	Outcome             string      `json:"outcome"`
	Reason              string      `json:"reason"`
	EvidenceIDs         []uuid.UUID `json:"evidence_ids"`
	NotLaunchedCheckIDs []string    `json:"not_launched_check_ids"`
}

type ValidationReconcileArguments struct {
	ValidationID    uuid.UUID                   `json:"validation_id"`
	AttemptFinished *ValidationAttemptFinished  `json:"attempt_finished,omitempty"`
	Changed         *ValidationReconcileChanged `json:"changed,omitempty"`
	Ended           *ValidationReconcileEnded   `json:"ended,omitempty"`
}

func (p ValidationSubmission) check() error {
	if p.Schema != 10 || !validOrigin(p.Origin) || (p.Actor != "owner" && p.Actor != "core") || p.ExpectedVersion < 0 || p.ExpectedVersion == math.MaxInt64 {
		return errors.New("invalid validation submission header")
	}
	fresh := p.Operation == "validation.define" || p.Operation == "validation.request" || p.Operation == "validation.reproduce" || p.Operation == "validation.refuse"
	if fresh != (p.ExpectedVersion == 0) {
		return errors.New("invalid validation expected_version")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case ValidationDefineArguments:
		valid = p.Operation == "validation.define" && p.Actor == "core" && ValidationCheckDefined(a).check() == nil
	case ValidationRequestArguments:
		valid = (p.Operation == "validation.request" || p.Operation == "validation.reproduce") && a.check(p) == nil
	case ValidationLaunchArguments:
		valid = p.Operation == "validation.launch" && p.Actor == "core" && ValidationAttemptStarted(a).check() == nil
	case ValidationFinishArguments:
		valid = p.Operation == "validation.finish" && p.Actor == "core" && a.check() == nil
	case ValidationChangeArguments:
		valid = (p.Operation == "validation.advance" && p.Actor == "core" || p.Operation == "validation.cancel") && isV7(a.ValidationID) && scopedToken(a.Reason)
	case ValidationEndArguments:
		valid = p.Operation == "validation.end" && p.Actor == "core" && (ValidationRequestEnded{a.ValidationID, p.ExpectedVersion, a.Outcome, a.Reason, a.EvidenceIDs, a.NotLaunchedCheckIDs}).check() == nil
	case ValidationVisibilityArguments:
		valid = p.Operation == "validation.visibility" && p.Actor == "core" && (ValidationVisibilityChanged{a.ValidationID, a.AttemptIDs, p.ExpectedVersion, a.State, a.AuthorizationSHA256, a.PredecessorAcceptanceSequence, a.PredecessorAcceptanceHash}).check() == nil
	case ValidationReconcileArguments:
		valid = p.Operation == "validation.reconcile" && p.Actor == "core" && a.check(p.ExpectedVersion) == nil
	case ValidationRefuseArguments:
		valid = p.Operation == "validation.refuse" && p.Actor == "core" && ValidationHookRefused(a).check() == nil
	}
	if !valid {
		return errors.New("validation operation, principal or argument mismatch")
	}
	return nil
}

func (a ValidationRequestArguments) check(s ValidationSubmission) error {
	r := a.Requested
	if err := r.check(); err != nil {
		return err
	}
	if !isV7(a.PinID) || len(a.Attempts) == 0 || r.Origin.AgentID != s.Origin.AgentID || r.Origin.ExecutionSessionID != s.Origin.SessionID || r.Origin.ContextID != s.Origin.ContextID {
		return errors.New("invalid validation request identity")
	}
	reproduce := s.Operation == "validation.reproduce"
	if reproduce {
		if s.Actor != "owner" || r.Trigger.Kind != "reproduction" {
			return errors.New("invalid reproduction principal or trigger")
		}
	} else if !(s.Actor == "owner" && r.Trigger.Kind == "explicit" || s.Actor == "core" && (r.Trigger.Kind == "hook" || r.Trigger.Kind == "promotion")) || r.IncidentID != (uuid.UUID{}) || len(a.Attempts) != len(r.RequiredChecks) {
		return errors.New("invalid ordinary request trigger or complete required set")
	}
	seenAttempts, seenRuns := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	planIndex, planCount := 0, int64(0)
	for i, q := range a.Attempts {
		if err := q.check(); err != nil {
			return err
		}
		if q.ValidationID != r.ValidationID || q.Ordinal != int64(i)+1 || seenAttempts[q.AttemptID] || seenRuns[q.RunID] {
			return errors.New("invalid queued attempt identity or ordinal")
		}
		seenAttempts[q.AttemptID], seenRuns[q.RunID] = true, true
		check := r.RequiredChecks[0]
		if !reproduce {
			check = r.RequiredChecks[i]
		}
		if q.DefinitionID != check.DefinitionID || q.CheckSHA256 != check.CheckSHA256 || q.EnvironmentSHA256 != check.EnvironmentSHA256 || q.InventorySHA256 != check.InventorySHA256 {
			return errors.New("queued check differs from required check")
		}
		if !reproduce {
			if q.Subject != r.Subject || q.IncidentID != (uuid.UUID{}) {
				return errors.New("ordinary attempt changes subject or incident")
			}
		} else {
			if planIndex >= len(r.ReproductionPlan) {
				return errors.New("more attempts than reproduction plan")
			}
			plan := r.ReproductionPlan[planIndex]
			if q.Subject != plan.Subject || q.DefinitionID != plan.DefinitionID || q.IncidentID != r.IncidentID || i > 0 && (a.Attempts[i-1].IncidentExecutionIndex == math.MaxInt64 || q.IncidentExecutionIndex != a.Attempts[i-1].IncidentExecutionIndex+1) {
				return errors.New("attempt differs from reproduction plan or incident reservation")
			}
			planCount++
			if planCount == plan.Executions {
				planIndex++
				planCount = 0
			}
		}
	}
	if reproduce && (planIndex != len(r.ReproductionPlan) || planCount != 0) {
		return errors.New("reproduction plan is not fully reserved")
	}
	return nil
}

func (a ValidationFinishArguments) check() error {
	if err := a.AttemptFinished.check(); err != nil {
		return err
	}
	if err := a.Evidence.check(); err != nil {
		return err
	}
	if a.AttemptFinished.ValidationID != a.Evidence.ValidationID || a.Evidence.AttemptIDs[len(a.Evidence.AttemptIDs)-1] != a.AttemptFinished.AttemptID ||
		(a.FailurePinID != (uuid.UUID{})) != (a.Evidence.Canonical && a.AttemptFinished.Outcome == "failed") || a.FailurePinID != (uuid.UUID{}) && !isV7(a.FailurePinID) {
		return errors.New("finish evidence or failure pin does not match")
	}
	return nil
}

func (a ValidationReconcileArguments) check(version int64) error {
	if !isV7(a.ValidationID) || a.AttemptFinished == nil && a.Changed == nil && a.Ended == nil {
		return errors.New("empty validation reconciliation")
	}
	if a.AttemptFinished != nil {
		p := *a.AttemptFinished
		if p.check() != nil || p.ValidationID != a.ValidationID || p.Outcome != "incomplete" || p.Reason != "interrupted" {
			return errors.New("reconciliation must retain an interrupted attempt")
		}
	}
	if a.Changed != nil {
		p := ValidationRequestChanged{a.ValidationID, version, "stopping", a.Changed.Reason, a.Changed.ClosedAttempts}
		if err := p.check(); err != nil {
			return err
		}
		version++
	}
	if a.Ended != nil {
		p := ValidationRequestEnded{a.ValidationID, version, a.Ended.Outcome, a.Ended.Reason, a.Ended.EvidenceIDs, a.Ended.NotLaunchedCheckIDs}
		if err := p.check(); err != nil {
			return err
		}
	}
	return nil
}

func (p ValidationSubmission) Encode() (json.RawMessage, error) {
	return encodeValidationPayload("schema-10 submission", p, p.check())
}

func DecodeValidationSubmission(js json.RawMessage) (ValidationSubmission, error) {
	var p ValidationSubmission
	err := decodeValidationPayload("schema-10 submission", js, func(m *members) error { p = readValidationSubmission(m); return p.check() })
	if err != nil {
		return ValidationSubmission{}, err
	}
	return p, nil
}

func readValidationSubmission(m *members) ValidationSubmission {
	p := ValidationSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation"), ExpectedVersion: m.nat("expected_version", true)}
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		var allowed []string
		var a any
		switch p.Operation {
		case "validation.define":
			allowed = []string{"definition_id", "workspace_id", "check_id", "check_sha256", "check_policy_hash", "environment_policy_hash", "environment_sha256", "inventory_sha256", "defining_files", "lineage", "pin_id"}
			a = ValidationDefineArguments(readValidationCheckDefined(b))
		case "validation.request", "validation.reproduce":
			allowed = []string{"requested", "attempts", "pin_id"}
			a = ValidationRequestArguments{graphObject(b, "requested", readValidationRequestedArgument), readScopedObjects(b, "attempts", readValidationQueuedArgument), b.id("pin_id")}
		case "validation.launch":
			allowed = []string{"validation_id", "attempt_id", "run_id", "materialized_manifest_sha256", "overlay_sha256", "canary_sha256", "nonce_sha256", "concrete_environment_sha256"}
			a = ValidationLaunchArguments(readValidationAttemptStarted(b))
		case "validation.finish":
			allowed = []string{"attempt_finished", "evidence", "failure_pin_id"}
			arg := ValidationFinishArguments{AttemptFinished: graphObject(b, "attempt_finished", readValidationFinishedArgument), Evidence: graphObject(b, "evidence", readValidationEvidenceArgument)}
			arg.FailurePinID, _ = b.optionalID("failure_pin_id")
			a = arg
		case "validation.advance", "validation.cancel":
			allowed = []string{"validation_id", "reason"}
			a = ValidationChangeArguments{b.id("validation_id"), b.text("reason")}
		case "validation.end":
			allowed = []string{"validation_id", "outcome", "reason", "evidence_ids", "not_launched_check_ids"}
			a = readValidationEndArguments(b)
		case "validation.visibility":
			allowed = []string{"validation_id", "attempt_ids", "state", "authorization_sha256", "predecessor_acceptance_sequence", "predecessor_acceptance_hash"}
			arg := ValidationVisibilityArguments{ValidationID: b.id("validation_id"), State: b.text("state"), AuthorizationSHA256: b.digest("authorization_sha256")}
			arg.AttemptIDs, _ = b.sortedIDs("attempt_ids")
			arg.PredecessorAcceptanceSequence, _ = b.optionalNat("predecessor_acceptance_sequence")
			arg.PredecessorAcceptanceHash, _ = b.optionalDigest("predecessor_acceptance_hash")
			a = arg
		case "validation.reconcile":
			allowed = []string{"validation_id", "attempt_finished", "changed", "ended"}
			a = ValidationReconcileArguments{ValidationID: b.id("validation_id"), AttemptFinished: optionalObject(b, "attempt_finished", readValidationFinishedArgument), Changed: optionalObject(b, "changed", func(c *members) ValidationReconcileChanged {
				validationClosed(c, "reason", "closed_attempts")
				return ValidationReconcileChanged{c.text("reason"), readScopedObjects(c, "closed_attempts", readValidationClosedAttempt)}
			}), Ended: optionalObject(b, "ended", func(c *members) ValidationReconcileEnded {
				validationClosed(c, "outcome", "reason", "evidence_ids", "not_launched_check_ids")
				arg := ValidationReconcileEnded{Outcome: c.text("outcome"), Reason: c.text("reason"), NotLaunchedCheckIDs: readScopedStrings(c, "not_launched_check_ids")}
				arg.EvidenceIDs, _ = c.sortedIDs("evidence_ids")
				return arg
			})}
		case "validation.refuse":
			allowed = []string{"hook_id", "workspace_id", "source_sequence", "source_record_hash", "reason"}
			a = ValidationRefuseArguments(readValidationHookRefused(b))
		default:
			b.err = errors.New("unknown validation operation")
		}
		validationClosed(b, allowed...)
		return a
	})
	return p
}

func readValidationClosedAttempt(m *members) ValidationClosedAttempt {
	p := ValidationClosedAttempt{AttemptID: m.id("attempt_id"), Proof: m.text("proof")}
	p.ProcessRunID, _ = m.optionalID("process_run_id")
	return p
}

func readValidationEndArguments(m *members) ValidationEndArguments {
	p := ValidationEndArguments{ValidationID: m.id("validation_id"), Outcome: m.text("outcome"), Reason: m.text("reason"), NotLaunchedCheckIDs: readScopedStrings(m, "not_launched_check_ids")}
	p.EvidenceIDs, _ = m.sortedIDs("evidence_ids")
	return p
}

// ValidationReceipt carries the canonical submission itself, not just its hash.
type ValidationReceipt struct {
	CommandID        uuid.UUID            `json:"command_id"`
	SubmissionSHA256 Digest               `json:"submission_sha256"`
	Submission       ValidationSubmission `json:"submission"`
	ValidationID     uuid.UUID            `json:"validation_id,omitzero"`
	DefinitionID     uuid.UUID            `json:"definition_id,omitzero"`
	Operation        string               `json:"operation"`
	Version          int64                `json:"version"`
	EffectSequence   int64                `json:"effect_sequence"`
}

func validationSubmissionTarget(p ValidationSubmission) (validation, definition uuid.UUID, version int64) {
	version = p.ExpectedVersion
	switch a := p.Arguments.(type) {
	case ValidationDefineArguments:
		definition = a.DefinitionID
	case ValidationRequestArguments:
		validation, version = a.Requested.ValidationID, 1
	case ValidationLaunchArguments:
		validation = a.ValidationID
	case ValidationFinishArguments:
		validation = a.AttemptFinished.ValidationID
	case ValidationChangeArguments:
		validation, version = a.ValidationID, version+1
	case ValidationEndArguments:
		validation, version = a.ValidationID, version+1
	case ValidationVisibilityArguments:
		validation, version = a.ValidationID, version+1
	case ValidationReconcileArguments:
		validation = a.ValidationID
		if a.Changed != nil {
			version++
		}
		if a.Ended != nil {
			version++
		}
	}
	return
}

func (p ValidationReceipt) check() error {
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	v, d, version := validationSubmissionTarget(p.Submission)
	if !isV7(p.CommandID) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) || p.Operation != p.Submission.Operation || p.ValidationID != v || p.DefinitionID != d || p.Version != version || p.EffectSequence <= 0 {
		return errors.New("validation receipt differs from its submission")
	}
	return nil
}

func (p ValidationReceipt) Encode() (json.RawMessage, error) {
	return encodeValidationPayload(TypeValidationReceipt, p, p.check())
}

func DecodeValidationReceipt(js json.RawMessage) (ValidationReceipt, error) {
	var p ValidationReceipt
	err := decodeValidationPayload(TypeValidationReceipt, js, func(m *members) error {
		p = ValidationReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Submission: graphObject(m, "submission", readValidationSubmission), Operation: m.text("operation"), Version: m.nat("version", true), EffectSequence: m.nat("effect_sequence", true)}
		p.ValidationID, _ = m.optionalID("validation_id")
		p.DefinitionID, _ = m.optionalID("definition_id")
		return p.check()
	})
	if err != nil {
		return ValidationReceipt{}, err
	}
	return p, nil
}

func readValidationRequestedArgument(m *members) ValidationRequested {
	validationClosed(m, "validation_id", "origin", "trigger", "subject", "authorization", "activation_record_hash", "epoch", "field_law_sha256", "validation_policy_hash", "check_policy_hash", "environment_policy_hash", "required_checks", "capacity_profile_sha256", "incident_id", "incident_version", "reproduction_plan")
	return readValidationRequested(m)
}

func readValidationQueuedArgument(m *members) ValidationAttemptQueued {
	validationClosed(m, "validation_id", "attempt_id", "run_id", "ordinal", "subject", "definition_id", "check_sha256", "environment_sha256", "inventory_sha256", "runner_sha256", "adapter_sha256", "binding_sha256", "incident_id", "incident_execution_index")
	return readValidationAttemptQueued(m)
}

func readValidationFinishedArgument(m *members) ValidationAttemptFinished {
	validationClosed(m, "validation_id", "attempt_id", "run_id", "outcome", "reason", "started", "exit_known", "exit_code", "root_exited", "tree_closed", "io_joined", "closure_proof", "canary", "required_results", "artifacts", "input_integrity", "runner_suspect", "root_pid", "process_run_id")
	return readValidationAttemptFinished(m)
}

func readValidationEvidenceArgument(m *members) ValidationEvidence {
	validationClosed(m, "evidence_id", "validation_id", "subject", "definition_id", "check_sha256", "environment_sha256", "comparison_sha256", "binding_sha256", "attempt_ids", "summary", "canonical", "failure_grade", "incident_id", "failure_count", "pass_count", "required_count", "prior", "confidence", "adjacent")
	return readValidationEvidence(m)
}

package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"uuid"
)

// F3-11's version-1 record types are frozen (D140).
const (
	TypeRuntimeGrantIssued        = "runtime.grant_issued"
	TypeRuntimeGrantRevoked       = "runtime.grant_revoked"
	TypeRuntimeTaskChanged        = "runtime.task_changed"
	TypeRuntimeCapacityConfigured = "runtime.capacity_configured"
	TypeRuntimeWorkAdmitted       = "runtime.work_admitted"
	TypeRuntimeWorkDispatched     = "runtime.work_dispatched"
	TypeRuntimeWorkEnded          = "runtime.work_ended"
	TypeRuntimeLocalUsed          = "runtime.local_used"
	TypePrivateScopedRevision     = "private.scoped_revision"
	TypeRuntimeScopedReceipt      = "runtime.scoped_receipt"
)

// RuntimeGrantIssued records an immutable grant and the authority CAS.
type RuntimeGrantIssued struct {
	GrantID                  uuid.UUID    `json:"grant_id"`
	ExpectedAuthorityVersion int64        `json:"expected_authority_version"`
	AuthorityVersion         int64        `json:"authority_version"`
	Grantee                  Binding      `json:"grantee"`
	ParentGrantID            uuid.UUID    `json:"parent_grant_id,omitzero"`
	Operations               []string     `json:"operations"`
	Resources                []Resource   `json:"resources"`
	DataClass                string       `json:"data_class"`
	Recipients               []Binding    `json:"recipients"`
	Limits                   Capacity     `json:"limits"`
	DelegationDepth          int64        `json:"delegation_depth"`
	Lifetime                 string       `json:"lifetime"`
	TaskBinding              *TaskBinding `json:"task_binding,omitempty"`
}

func (p RuntimeGrantIssued) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeGrantIssued, p, p.check())
}
func DecodeRuntimeGrantIssued(js json.RawMessage) (RuntimeGrantIssued, error) {
	var p RuntimeGrantIssued
	err := decodeScopedPayload(TypeRuntimeGrantIssued, js, func(m *members) error {
		p = readGrantIssued(m)
		p.ExpectedAuthorityVersion = m.nat("expected_authority_version", true)
		p.AuthorityVersion = m.nat("authority_version", true)
		return p.check()
	})
	if err != nil {
		return RuntimeGrantIssued{}, err
	}
	return p, nil
}
func readGrantIssued(m *members) RuntimeGrantIssued {
	p := RuntimeGrantIssued{GrantID: m.id("grant_id"), Grantee: graphObject(m, "grantee", readBinding), Operations: readScopedStrings(m, "operations"), Resources: readScopedObjects(m, "resources", readResource), DataClass: m.text("data_class"), Recipients: readScopedObjects(m, "recipients", readBinding), Limits: graphObject(m, "limits", readCapacity), DelegationDepth: m.nat("delegation_depth", true), Lifetime: m.text("lifetime"), TaskBinding: optionalObject(m, "task_binding", readTaskBinding)}
	p.ParentGrantID, _ = m.optionalID("parent_grant_id")
	return p
}
func (p RuntimeGrantIssued) check() error {
	if !nextScopedVersion(p.ExpectedAuthorityVersion, p.AuthorityVersion) || !isV7(p.GrantID) || !validBinding(p.Grantee) || p.ParentGrantID != (uuid.UUID{}) && !isV7(p.ParentGrantID) || !validTaskBinding(p.TaskBinding) || !validCapacity(p.Limits) || p.DelegationDepth < 0 || !scopedToken(p.Lifetime) {
		return errors.New("invalid grant identity, version or limits")
	}
	if len(p.Operations) == 0 || !strictScopedStrings(p.Operations) || len(p.Resources) == 0 || len(p.Recipients) == 0 || !slices.Contains([]string{"private", "task", "workspace", "artifact", "external"}, p.DataClass) {
		return errors.New("invalid grant sets or class")
	}
	external := 0
	for _, op := range p.Operations {
		if op == "grant.issue" || op == "grant.revoke" {
			continue
		}
		if !scopedClassOperation(p.DataClass, op) {
			return errors.New("operation does not belong to class")
		}
		if p.DataClass == "external" {
			external++
		}
	}
	if external > 1 {
		return errors.New("external grant combines effect operations")
	}
	var previous []byte
	for _, r := range p.Resources {
		class := r.Kind
		if class == "context" {
			class = "private"
		}
		encoded, err := r.MarshalJSON()
		if err != nil || class != p.DataClass || previous != nil && bytes.Compare(previous, encoded) >= 0 {
			return errors.New("invalid or unsorted grant resources")
		}
		previous = encoded
		for _, op := range p.Operations {
			if r.Kind == "task" && r.TaskID == (uuid.UUID{}) && op != "task.create" && op != "grant.issue" && op != "grant.revoke" {
				return errors.New("unbound task region only permits creation and delegation")
			}
			if r.Kind == "context" && len(r.Slots) == 0 && op != "context.inspect" && op != "turn.start" && op != "turn.inspect" && op != "turn.cancel" && op != "grant.issue" && op != "grant.revoke" {
				return errors.New("private read/write requires slots")
			}
		}
	}
	for i, b := range p.Recipients {
		if !validBinding(b) {
			return errors.New("invalid recipient")
		}
		if i > 0 {
			a := p.Recipients[i-1]
			c := bytes.Compare(a.AgentID[:], b.AgentID[:])
			if c > 0 || c == 0 && bytes.Compare(a.ContextID[:], b.ContextID[:]) >= 0 {
				return errors.New("recipients not strictly sorted")
			}
		}
	}
	return nil
}
func scopedClassOperation(class, op string) bool {
	switch class {
	case "private":
		return slices.Contains([]string{"context.inspect", "private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, op)
	case "task":
		return slices.Contains([]string{"task.inspect", "task.create", "task.claim", "task.assign", "task.start", "task.wait", "task.resume", "task.submit", "task.complete", "task.fail", "task.cancel", "task.reassign"}, op)
	case "workspace":
		return slices.Contains([]string{"file.read", "file.search", "file.write", "file.edit"}, op)
	case "artifact":
		return op == "artifact.read"
	case "external":
		return slices.Contains([]string{"shell.execute", "process.spawn", "network.connect", "connector.call", "model.call"}, op)
	}
	return false
}

// RuntimeGrantRevoked advances authority without releasing descendant holds.
type RuntimeGrantRevoked struct {
	GrantID                  uuid.UUID `json:"grant_id"`
	ExpectedAuthorityVersion int64     `json:"expected_authority_version"`
	AuthorityVersion         int64     `json:"authority_version"`
}

func (p RuntimeGrantRevoked) check() error {
	if !isV7(p.GrantID) || !nextScopedVersion(p.ExpectedAuthorityVersion, p.AuthorityVersion) {
		return errors.New("invalid revocation identity or version")
	}
	return nil
}
func (p RuntimeGrantRevoked) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeGrantRevoked, p, p.check())
}
func DecodeRuntimeGrantRevoked(js json.RawMessage) (RuntimeGrantRevoked, error) {
	var p RuntimeGrantRevoked
	err := decodeScopedPayload(TypeRuntimeGrantRevoked, js, func(m *members) error {
		p = RuntimeGrantRevoked{m.id("grant_id"), m.nat("expected_authority_version", true), m.nat("authority_version", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeGrantRevoked{}, err
	}
	return p, nil
}

// RuntimeTaskChanged is the complete current task snapshot, not a patch.
type RuntimeTaskChanged struct {
	TaskID           uuid.UUID   `json:"task_id"`
	Action           string      `json:"action"`
	ExpectedVersion  int64       `json:"expected_version"`
	Version          int64       `json:"version"`
	CreatorActor     string      `json:"creator_actor"`
	Creator          Origin      `json:"creator"`
	Description      ObjectRef   `json:"description"`
	AcceptanceSHA256 Digest      `json:"acceptance_sha256"`
	Dependencies     []uuid.UUID `json:"dependencies"`
	Audience         []uuid.UUID `json:"audience"`
	Assignment       *Assignment `json:"assignment,omitempty"`
	State            string      `json:"state"`
	WaitReason       string      `json:"wait_reason,omitempty"`
	WaitArtifact     *SubjectRef `json:"wait_artifact,omitempty"`
	Submission       *SubjectRef `json:"submission,omitempty"`
	FailureCause     string      `json:"failure_cause,omitempty"`
	FailureEvidence  *SubjectRef `json:"failure_evidence,omitempty"`
}

func (p RuntimeTaskChanged) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeTaskChanged, p, p.check())
}
func DecodeRuntimeTaskChanged(js json.RawMessage) (RuntimeTaskChanged, error) {
	var p RuntimeTaskChanged
	err := decodeScopedPayload(TypeRuntimeTaskChanged, js, func(m *members) error {
		p = RuntimeTaskChanged{TaskID: m.id("task_id"), Action: m.text("action"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), CreatorActor: m.text("creator_actor"), Creator: graphObject(m, "creator", readOrigin), Description: graphObject(m, "description", readObject), AcceptanceSHA256: m.digest("acceptance_sha256"), Assignment: optionalObject(m, "assignment", readAssignment), State: m.text("state"), WaitArtifact: subjectAt(m, "wait_artifact"), Submission: subjectAt(m, "submission"), FailureEvidence: subjectAt(m, "failure_evidence")}
		p.Dependencies, _ = m.sortedIDs("dependencies")
		p.Audience, _ = m.sortedIDs("audience")
		p.WaitReason, _ = m.optionalText("wait_reason")
		p.FailureCause, _ = m.optionalText("failure_cause")
		return p.check()
	})
	if err != nil {
		return RuntimeTaskChanged{}, err
	}
	return p, nil
}
func (p RuntimeTaskChanged) check() error {
	if !isV7(p.TaskID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || (p.CreatorActor != "owner" && p.CreatorActor != "agent") || !validOrigin(p.Creator) || !validObject(p.Description) || p.AcceptanceSHA256 == (Digest{}) || !scopedIDs(p.Dependencies, true) || !scopedIDs(p.Audience, false) || slices.Contains(p.Dependencies, p.TaskID) || !validAssignment(p.Assignment) {
		return errors.New("invalid task identity, definition, assignment or version")
	}
	if !validSubject(p.WaitArtifact) || !validSubject(p.Submission) || !validSubject(p.FailureEvidence) {
		return errors.New("invalid task subject")
	}
	if p.WaitReason != "" && (p.State != "waiting" || !scopedWaitReason(p.WaitReason)) || p.WaitArtifact != nil && p.WaitReason == "" {
		return errors.New("invalid waiting fields")
	}
	submitted := p.State == "submitted" || p.State == "completed"
	if submitted != (p.Submission != nil) || (p.State == "failed") != (p.FailureCause != "") || (p.State == "failed") != (p.FailureEvidence != nil) || p.FailureCause != "" && !scopedToken(p.FailureCause) {
		return errors.New("invalid submission or failure fields")
	}
	state := "waiting"
	switch p.Action {
	case "create":
		if p.ExpectedVersion != 0 || p.Assignment != nil && p.Assignment.Generation != 1 {
			return errors.New("invalid task creation")
		}
	case "claim", "assign":
		if p.Assignment == nil || p.Assignment.Generation != 1 {
			return errors.New("first assignment requires generation one")
		}
	case "reassign":
		if p.Assignment == nil || p.Assignment.Generation < 2 {
			return errors.New("reassignment requires later generation")
		}
	case "resume":
	case "wait":
		if p.WaitReason == "" {
			return errors.New("wait requires reason")
		}
	case "start":
		state = "active"
	case "submit":
		state = "submitted"
	case "complete":
		state = "completed"
	case "fail":
		state = "failed"
	case "cancel":
		state = "cancelled"
	default:
		return errors.New("unknown task action")
	}
	if p.State != state || p.Action != "create" && p.ExpectedVersion == 0 || p.Action != "wait" && p.WaitReason != "" || p.Action != "create" && p.Action != "cancel" && p.Assignment == nil {
		return errors.New("task action/state mismatch")
	}
	return nil
}
func scopedWaitReason(s string) bool {
	return slices.Contains([]string{"dependency", "input", "review", "explicit_hold"}, s)
}

// RuntimeCapacityConfigured replaces future ceilings without resetting usage.
type RuntimeCapacityConfigured struct {
	Scope           ScopeKey `json:"scope"`
	ExpectedVersion int64    `json:"expected_version"`
	Version         int64    `json:"version"`
	Limits          Capacity `json:"limits"`
}

func (p RuntimeCapacityConfigured) check() error {
	if !validScope(p.Scope) || p.Scope.Kind == "grant" || !nextScopedVersion(p.ExpectedVersion, p.Version) || !validCapacity(p.Limits) {
		return errors.New("invalid capacity configuration")
	}
	return nil
}
func (p RuntimeCapacityConfigured) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeCapacityConfigured, p, p.check())
}
func DecodeRuntimeCapacityConfigured(js json.RawMessage) (RuntimeCapacityConfigured, error) {
	var p RuntimeCapacityConfigured
	err := decodeScopedPayload(TypeRuntimeCapacityConfigured, js, func(m *members) error {
		p = RuntimeCapacityConfigured{graphObject(m, "scope", readScope), m.nat("expected_version", true), m.nat("version", true), graphObject(m, "limits", readCapacity)}
		return p.check()
	})
	if err != nil {
		return RuntimeCapacityConfigured{}, err
	}
	return p, nil
}

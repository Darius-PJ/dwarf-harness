package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"uuid"
)

// ScopedExpected binds entity and optional assignment/authority expectations.
// Pointers preserve a present authority_version of zero.
type ScopedExpected struct {
	Version          int64     `json:"version"`
	AssignmentID     uuid.UUID `json:"assignment_id,omitzero"`
	Generation       *int64    `json:"generation,omitempty"`
	AuthorityVersion *int64    `json:"authority_version,omitempty"`
}

// ScopedSubmission is schema 2's canonical producer encoding, frozen
// (D140). Arguments must be the corresponding concrete argument type
// below, never a map, pointer or arbitrary JSON. Command IDs and derived facts
// are deliberately absent. Encode returns compact JSON; errors wrap ErrInvalid.
type ScopedSubmission struct {
	Schema    int64          `json:"schema"`
	Actor     string         `json:"actor"`
	Origin    Origin         `json:"origin"`
	Operation string         `json:"operation"`
	Target    Resource       `json:"target"`
	GrantID   uuid.UUID      `json:"grant_id,omitzero"`
	Expected  ScopedExpected `json:"expected"`
	Arguments any            `json:"arguments"`
}

// GrantIssueArguments has the grant's caller-supplied fields in canonical order.
type GrantIssueArguments struct {
	GrantID         uuid.UUID    `json:"grant_id"`
	Grantee         Binding      `json:"grantee"`
	ParentGrantID   uuid.UUID    `json:"parent_grant_id,omitzero"`
	Operations      []string     `json:"operations"`
	Resources       []Resource   `json:"resources"`
	DataClass       string       `json:"data_class"`
	Recipients      []Binding    `json:"recipients"`
	Limits          Capacity     `json:"limits"`
	DelegationDepth int64        `json:"delegation_depth"`
	Lifetime        string       `json:"lifetime"`
	TaskBinding     *TaskBinding `json:"task_binding,omitempty"`
}
type GrantRevokeArguments struct {
	GrantID uuid.UUID `json:"grant_id"`
}
type CapacityArguments struct {
	Scope  ScopeKey `json:"scope"`
	Limits Capacity `json:"limits"`
}
type AssignmentInput struct {
	AssignmentID uuid.UUID `json:"assignment_id"`
	Assignee     Binding   `json:"assignee"`
}
type TaskCreateArguments struct {
	TaskID           uuid.UUID        `json:"task_id"`
	Description      ObjectRef        `json:"description"`
	AcceptanceSHA256 Digest           `json:"acceptance_sha256"`
	Dependencies     []uuid.UUID      `json:"dependencies"`
	Audience         []uuid.UUID      `json:"audience"`
	Assignment       *AssignmentInput `json:"assignment,omitempty"`
}
type TaskMutationArguments struct {
	TaskID          uuid.UUID        `json:"task_id"`
	Action          string           `json:"action"`
	NewAssignment   *AssignmentInput `json:"new_assignment,omitempty"`
	WaitReason      string           `json:"wait_reason,omitempty"`
	WaitArtifact    *SubjectRef      `json:"wait_artifact,omitempty"`
	Submission      *SubjectRef      `json:"submission,omitempty"`
	FailureCause    string           `json:"failure_cause,omitempty"`
	FailureEvidence *SubjectRef      `json:"failure_evidence,omitempty"`
}
type TurnStartArguments struct {
	TurnID      uuid.UUID    `json:"turn_id"`
	TaskBinding *TaskBinding `json:"task_binding,omitempty"`
}
type LocalAdmissionArguments struct {
	WorkID          uuid.UUID    `json:"work_id"`
	ParentTurnID    uuid.UUID    `json:"parent_turn_id"`
	LocalOperation  string       `json:"local_operation"`
	ArgumentsSHA256 Digest       `json:"arguments_sha256"`
	ToolSchema      int64        `json:"tool_schema"`
	MaxBytes        int64        `json:"max_bytes"`
	TaskBinding     *TaskBinding `json:"task_binding,omitempty"`
}
type WorkDispatchArguments struct {
	WorkID          uuid.UUID `json:"work_id"`
	ArgumentsSHA256 Digest    `json:"arguments_sha256"`
}
type WorkEndArguments struct {
	WorkID  uuid.UUID `json:"work_id"`
	Outcome string    `json:"outcome"`
}
type PrivateWriteArguments struct {
	Changes []PrivateChange `json:"changes"`
}

func (a PrivateWriteArguments) MarshalJSON() ([]byte, error) {
	for _, c := range a.Changes {
		if c.PreviousSHA256 != (Digest{}) {
			return nil, errors.New("submission forbids transcript predecessor")
		}
	}
	return json.Marshal(struct {
		Changes []privateChangeWire `json:"changes"`
	}{scopedChanges(a.Changes)})
}

type PrivateReadArguments struct {
	Read PrivateReadRef `json:"read"`
}
type ArtifactReadArguments struct {
	Artifact ArtifactRef `json:"artifact"`
	MaxBytes int64       `json:"max_bytes"`
}

func (p ScopedSubmission) Encode() (json.RawMessage, error) {
	return encodeScopedPayload("schema-2 submission", p, p.check())
}
func DecodeScopedSubmission(js json.RawMessage) (ScopedSubmission, error) {
	var p ScopedSubmission
	err := decodeScopedPayload("schema-2 submission", js, func(m *members) error { p = readScopedSubmission(m); return p.check() })
	if err != nil {
		return ScopedSubmission{}, err
	}
	return p, nil
}
func scopedWorkOperation(op string) bool {
	return op == "turn.start" || op == "turn.cancel" || op == "turn.end" || op == "local.admit" || op == "local.begin" || op == "local.end"
}
func (p ScopedSubmission) check() error {
	if p.Schema != 2 || (p.Actor != "owner" && p.Actor != "agent" && p.Actor != "core") || !validOrigin(p.Origin) || p.Target.check() != nil || p.GrantID != (uuid.UUID{}) && !isV7(p.GrantID) || (p.Actor == "agent" || scopedWorkOperation(p.Operation)) && !isV7(p.GrantID) {
		return errors.New("invalid submission envelope")
	}
	e := p.Expected
	assigned := e.AssignmentID != (uuid.UUID{})
	authority := p.Operation == "grant.issue" || p.Operation == "grant.revoke"
	if e.Version < 0 || e.Version == math.MaxInt64 || assigned != (e.Generation != nil) || assigned && (!isV7(e.AssignmentID) || *e.Generation < 1 || !strings.HasPrefix(p.Operation, "task.") || p.Operation == "task.create") || authority != (e.AuthorityVersion != nil) || e.AuthorityVersion != nil && (*e.AuthorityVersion < 0 || *e.AuthorityVersion == math.MaxInt64) {
		return errors.New("invalid submission expectation")
	}
	switch p.Operation {
	case "grant.issue", "task.create", "turn.start", "local.admit", "private.read", "artifact.read":
		if e.Version != 0 {
			return errors.New("operation requires zero expectation")
		}
	case "grant.revoke", "local.begin":
		if e.Version != 1 {
			return errors.New("operation requires expectation one")
		}
	case "capacity.configure":
	default:
		if e.Version < 1 {
			return errors.New("operation requires positive expectation")
		}
	}
	valid := false
	switch a := p.Arguments.(type) {
	case GrantIssueArguments:
		g := RuntimeGrantIssued{GrantID: a.GrantID, AuthorityVersion: 1, Grantee: a.Grantee, ParentGrantID: a.ParentGrantID, Operations: a.Operations, Resources: a.Resources, DataClass: a.DataClass, Recipients: a.Recipients, Limits: a.Limits, DelegationDepth: a.DelegationDepth, Lifetime: a.Lifetime, TaskBinding: a.TaskBinding}
		valid = p.Operation == "grant.issue" && g.check() == nil
	case GrantRevokeArguments:
		valid = p.Operation == "grant.revoke" && isV7(a.GrantID)
	case CapacityArguments:
		valid = p.Operation == "capacity.configure" && validScope(a.Scope) && a.Scope.Kind != "grant" && validCapacity(a.Limits)
	case TaskCreateArguments:
		valid = p.Operation == "task.create" && isV7(a.TaskID) && validObject(a.Description) && a.AcceptanceSHA256 != (Digest{}) && scopedIDs(a.Dependencies, true) && scopedIDs(a.Audience, false) && validAssignmentInput(a.Assignment)
		for _, id := range a.Dependencies {
			valid = valid && id != a.TaskID
		}
	case TaskMutationArguments:
		valid = p.Operation == "task."+a.Action && validTaskMutation(a)
	case TurnStartArguments:
		valid = p.Operation == "turn.start" && isV7(a.TurnID) && validTaskBinding(a.TaskBinding)
	case LocalAdmissionArguments:
		valid = p.Operation == "local.admit" && isV7(a.WorkID) && isV7(a.ParentTurnID) && scopedClassOperation("workspace", a.LocalOperation) && a.ArgumentsSHA256 != (Digest{}) && a.ToolSchema == 1 && a.MaxBytes > 0 && validTaskBinding(a.TaskBinding)
	case WorkDispatchArguments:
		valid = p.Operation == "local.begin" && isV7(a.WorkID) && a.ArgumentsSHA256 != (Digest{})
	case WorkEndArguments:
		valid = (p.Operation == "local.end" || p.Operation == "turn.end" || p.Operation == "turn.cancel") && isV7(a.WorkID) && scopedToken(a.Outcome)
	case PrivateWriteArguments:
		valid = p.Operation == "private.write" && p.Target.Kind == "context" && (PrivateScopedRevision{p.Target.Scope, e.Version, e.Version + 1, a.Changes}).check() == nil
		for _, c := range a.Changes {
			valid = valid && c.PreviousSHA256 == (Digest{})
		}
	case PrivateReadArguments:
		valid = p.Operation == "private.read" && validPrivateRead(a.Read)
	case ArtifactReadArguments:
		valid = p.Operation == "artifact.read" && validArtifact(a.Artifact) && a.MaxBytes > 0 && a.Artifact.Bytes <= a.MaxBytes
	}
	if !valid {
		return errors.New("operation/argument mismatch or invalid arguments")
	}
	return nil
}
func validAssignmentInput(a *AssignmentInput) bool {
	return a == nil || isV7(a.AssignmentID) && validBinding(a.Assignee)
}
func validTaskMutation(a TaskMutationArguments) bool {
	if !isV7(a.TaskID) || !validAssignmentInput(a.NewAssignment) || !validSubject(a.WaitArtifact) || !validSubject(a.Submission) || !validSubject(a.FailureEvidence) {
		return false
	}
	assign := a.Action == "assign" || a.Action == "claim" || a.Action == "reassign"
	if assign != (a.NewAssignment != nil) || (a.Action == "wait") != (a.WaitReason != "") || a.WaitReason != "" && !scopedWaitReason(a.WaitReason) || a.WaitArtifact != nil && a.Action != "wait" || (a.Action == "submit" || a.Action == "complete") != (a.Submission != nil) || (a.Action == "fail") != (a.FailureCause != "") || (a.Action == "fail") != (a.FailureEvidence != nil) || a.FailureCause != "" && !scopedToken(a.FailureCause) {
		return false
	}
	return a.Action != "create" && a.Action != "inspect" && scopedClassOperation("task", "task."+a.Action)
}
func readAssignmentInput(m *members) AssignmentInput {
	return AssignmentInput{m.id("assignment_id"), graphObject(m, "assignee", readBinding)}
}
func readScopedExpected(m *members) ScopedExpected {
	e := ScopedExpected{Version: m.nat("version", true), Generation: optionalScopedNat(m, "generation"), AuthorityVersion: optionalScopedNat(m, "authority_version")}
	e.AssignmentID, _ = m.optionalID("assignment_id")
	return e
}
func readScopedSubmission(m *members) ScopedSubmission {
	p := ScopedSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation"), Target: graphObject(m, "target", readResource), Expected: graphObject(m, "expected", readScopedExpected)}
	p.GrantID, _ = m.optionalID("grant_id")
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		switch p.Operation {
		case "grant.issue":
			g := readGrantIssued(b)
			return GrantIssueArguments{g.GrantID, g.Grantee, g.ParentGrantID, g.Operations, g.Resources, g.DataClass, g.Recipients, g.Limits, g.DelegationDepth, g.Lifetime, g.TaskBinding}
		case "grant.revoke":
			return GrantRevokeArguments{b.id("grant_id")}
		case "capacity.configure":
			return CapacityArguments{graphObject(b, "scope", readScope), graphObject(b, "limits", readCapacity)}
		case "task.create":
			a := TaskCreateArguments{TaskID: b.id("task_id"), Description: graphObject(b, "description", readObject), AcceptanceSHA256: b.digest("acceptance_sha256"), Assignment: optionalObject(b, "assignment", readAssignmentInput)}
			a.Dependencies, _ = b.sortedIDs("dependencies")
			a.Audience, _ = b.sortedIDs("audience")
			return a
		case "task.claim", "task.assign", "task.start", "task.wait", "task.resume", "task.submit", "task.complete", "task.fail", "task.cancel", "task.reassign":
			a := TaskMutationArguments{TaskID: b.id("task_id"), Action: b.text("action"), NewAssignment: optionalObject(b, "new_assignment", readAssignmentInput), WaitArtifact: subjectAt(b, "wait_artifact"), Submission: subjectAt(b, "submission"), FailureEvidence: subjectAt(b, "failure_evidence")}
			a.WaitReason, _ = b.optionalText("wait_reason")
			a.FailureCause, _ = b.optionalText("failure_cause")
			return a
		case "turn.start":
			return TurnStartArguments{b.id("turn_id"), optionalObject(b, "task_binding", readTaskBinding)}
		case "local.admit":
			return LocalAdmissionArguments{b.id("work_id"), b.id("parent_turn_id"), b.text("local_operation"), b.digest("arguments_sha256"), b.nat("tool_schema", true), b.nat("max_bytes", true), optionalObject(b, "task_binding", readTaskBinding)}
		case "local.begin":
			return WorkDispatchArguments{b.id("work_id"), b.digest("arguments_sha256")}
		case "local.end", "turn.end", "turn.cancel":
			return WorkEndArguments{b.id("work_id"), b.text("outcome")}
		case "private.write":
			return PrivateWriteArguments{readScopedChanges(b, true)}
		case "private.read":
			return PrivateReadArguments{graphObject(b, "read", readPrivateRead)}
		case "artifact.read":
			return ArtifactReadArguments{graphObject(b, "artifact", readArtifact), b.nat("max_bytes", true)}
		}
		b.err = errors.New("unknown submission operation")
		return nil
	})
	return p
}

// RuntimeScopedReceipt binds a command to its canonical schema-2 submission.
// Replay still verifies its complete group, meaning and resulting version.
type RuntimeScopedReceipt struct {
	CommandID        uuid.UUID        `json:"command_id"`
	SubmissionSHA256 Digest           `json:"submission_sha256"`
	Submission       ScopedSubmission `json:"submission"`
	Outcome          string           `json:"outcome"`
	Version          int64            `json:"version"`
	WorkID           uuid.UUID        `json:"work_id,omitzero"`
}

func (p RuntimeScopedReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) || p.Version < 0 || scopedWorkOperation(p.Submission.Operation) != (p.WorkID != (uuid.UUID{})) || p.WorkID != (uuid.UUID{}) && !isV7(p.WorkID) {
		return errors.New("invalid receipt identity or result")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("receipt submission digest mismatch")
	}
	return nil
}
func (p RuntimeScopedReceipt) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeScopedReceipt, p, p.check())
}
func DecodeRuntimeScopedReceipt(js json.RawMessage) (RuntimeScopedReceipt, error) {
	var p RuntimeScopedReceipt
	err := decodeScopedPayload(TypeRuntimeScopedReceipt, js, func(m *members) error {
		p = RuntimeScopedReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Submission: graphObject(m, "submission", readScopedSubmission), Outcome: m.text("outcome"), Version: m.nat("version", true)}
		p.WorkID, _ = m.optionalID("work_id")
		return p.check()
	})
	if err != nil {
		return RuntimeScopedReceipt{}, err
	}
	return p, nil
}

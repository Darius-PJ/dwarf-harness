package agents

import (
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// The envelope has no command ID, receipt sequence, scope set, handle or derived
// transcript predecessor. Arguments are separate so the contract review can
// refine their order without changing schema 2's envelope.
type scopedEnvelope struct {
	actor     string
	origin    submissionIDs
	operation string
	target    grantResource
	grant     uuid.UUID
	expected  scopedExpected
}

type scopedExpected struct {
	Version          uint64    `json:"version"`
	AssignmentID     uuid.UUID `json:"assignment_id,omitzero"`
	Generation       *int64    `json:"generation,omitempty"`
	AuthorityVersion *uint64   `json:"authority_version,omitempty"`
}

type scopedOrigin struct {
	AgentID   uuid.UUID `json:"agent_id"`
	SessionID uuid.UUID `json:"session_id"`
	ContextID uuid.UUID `json:"context_id"`
}

type scopedCanonical struct {
	Schema    int            `json:"schema"`
	Actor     string         `json:"actor"`
	Origin    scopedOrigin   `json:"origin"`
	Operation string         `json:"operation"`
	Target    grantResource  `json:"target"`
	GrantID   uuid.UUID      `json:"grant_id,omitzero"`
	Expected  scopedExpected `json:"expected"`
	Arguments any            `json:"arguments"`
}

type grantIssueArguments struct {
	GrantID         uuid.UUID        `json:"grant_id"`
	Grantee         scopedBinding    `json:"grantee"`
	ParentGrantID   uuid.UUID        `json:"parent_grant_id,omitzero"`
	Operations      []string         `json:"operations"`
	Resources       []grantResource  `json:"resources"`
	DataClass       string           `json:"data_class"`
	Recipients      []scopedBinding  `json:"recipients"`
	Limits          capacityAmounts  `json:"limits"`
	DelegationDepth int64            `json:"delegation_depth"`
	Lifetime        string           `json:"lifetime"`
	TaskBinding     *taskRestriction `json:"task_binding,omitempty"`
}

type grantRevokeArguments struct {
	GrantID uuid.UUID `json:"grant_id"`
}

type capacityArguments struct {
	Scope  budgetScope     `json:"scope"`
	Limits capacityAmounts `json:"limits"`
}

type objectIdentity struct {
	SHA256 ledger.Digest `json:"sha256"`
	Bytes  int64         `json:"bytes"`
}

type taskCreateArguments struct {
	TaskID           uuid.UUID            `json:"task_id"`
	Description      objectIdentity       `json:"description"`
	AcceptanceSHA256 ledger.Digest        `json:"acceptance_sha256"`
	Dependencies     []uuid.UUID          `json:"dependencies"`
	Audience         []uuid.UUID          `json:"audience"`
	Assignment       *taskAssignmentInput `json:"assignment,omitempty"`
}

type taskMutationArguments struct {
	TaskID          uuid.UUID            `json:"task_id"`
	Action          string               `json:"action"`
	NewAssignment   *taskAssignmentInput `json:"new_assignment,omitempty"`
	WaitReason      string               `json:"wait_reason,omitempty"`
	WaitArtifact    taskSubject          `json:"wait_artifact,omitzero"`
	Submission      taskSubject          `json:"submission,omitzero"`
	FailureCause    string               `json:"failure_cause,omitempty"`
	FailureEvidence taskSubject          `json:"failure_evidence,omitzero"`
}

type turnStartArguments struct {
	TurnID      uuid.UUID        `json:"turn_id"`
	TaskBinding *taskRestriction `json:"task_binding,omitempty"`
}

type localAdmissionArguments struct {
	WorkID          uuid.UUID        `json:"work_id"`
	ParentTurnID    uuid.UUID        `json:"parent_turn_id"`
	LocalOperation  string           `json:"local_operation"`
	ArgumentsSHA256 ledger.Digest    `json:"arguments_sha256"`
	ToolSchema      uint64           `json:"tool_schema"`
	MaxBytes        int64            `json:"max_bytes"`
	TaskBinding     *taskRestriction `json:"task_binding,omitempty"`
}

type workDispatchArguments struct {
	WorkID          uuid.UUID     `json:"work_id"`
	ArgumentsSHA256 ledger.Digest `json:"arguments_sha256"`
}

type workEndArguments struct {
	WorkID  uuid.UUID `json:"work_id"`
	Outcome string    `json:"outcome"`
}

type privateReadSelection struct {
	Scope    string `json:"scope"`
	Slot     string `json:"slot"`
	Revision uint64 `json:"revision"`
	Cursor   uint64 `json:"cursor"`
	MaxBytes int64  `json:"max_bytes"`
}

type privateReadArguments struct {
	Read privateReadSelection `json:"read"`
}

type artifactReadArguments struct {
	Artifact artifactIdentity `json:"artifact"`
	MaxBytes int64            `json:"max_bytes"`
}

// encodeScopedSubmission encodes already normalized operation arguments. The
// operation's validation and live authority remain separate admission rules.
// No generic maps or raw JSON can sneak unordered or derived members into it.
func encodeScopedSubmission(e scopedEnvelope, arguments any) ([]byte, ledger.Digest, error) {
	if !validID(e.origin.agent) || !validID(e.origin.session) || !validID(e.origin.context) ||
		(e.actor != "owner" && e.actor != "agent" && e.actor != "core") || !validResource(e.target) ||
		(e.grant != (uuid.UUID{}) && !validID(e.grant)) || e.actor == "agent" && e.grant == (uuid.UUID{}) ||
		!validScopedExpected(e.operation, e.expected) || !scopedArgumentType(e.operation, arguments) {
		return nil, ledger.Digest{}, ErrInvalid
	}
	data, err := json.Marshal(scopedCanonical{
		Schema: 2, Actor: e.actor, Origin: scopedOrigin{e.origin.agent, e.origin.session, e.origin.context},
		Operation: e.operation, Target: e.target, GrantID: e.grant, Expected: e.expected, Arguments: arguments,
	})
	if err != nil {
		return nil, ledger.Digest{}, err
	}
	return data, ledger.Digest(sha256.Sum256(data)), nil
}

func validScopedExpected(operation string, e scopedExpected) bool {
	if e.Version > math.MaxInt64 || e.AuthorityVersion != nil && *e.AuthorityVersion > math.MaxInt64 {
		return false
	}
	assigned := e.AssignmentID != (uuid.UUID{})
	if assigned != (e.Generation != nil) || assigned && (!validID(e.AssignmentID) || *e.Generation <= 0) {
		return false
	}
	authority := operation == "grant.issue" || operation == "grant.revoke"
	if authority != (e.AuthorityVersion != nil) {
		return false
	}
	if assigned && !(strings.HasPrefix(operation, "task.") && operation != "task.create") {
		return false
	}
	switch operation {
	case "grant.issue", "task.create", "turn.start", "local.admit", "private.read", "artifact.read":
		return e.Version == 0
	case "grant.revoke", "local.begin":
		return e.Version == 1
	case "capacity.configure":
		return e.Version < math.MaxInt64
	default:
		return e.Version > 0 && e.Version < math.MaxInt64
	}
}

func scopedArgumentType(operation string, arguments any) bool {
	switch a := arguments.(type) {
	case grantIssueArguments:
		return operation == "grant.issue"
	case grantRevokeArguments:
		return operation == "grant.revoke"
	case capacityArguments:
		return operation == "capacity.configure"
	case taskCreateArguments:
		return operation == "task.create"
	case taskMutationArguments:
		return classOperation("task", operation) && operation != "task.create" && operation != "task.inspect" && operation == "task."+a.Action
	case turnStartArguments:
		return operation == "turn.start"
	case localAdmissionArguments:
		return operation == "local.admit"
	case workDispatchArguments:
		return operation == "local.begin"
	case workEndArguments:
		return operation == "local.end" || operation == "turn.end" || operation == "turn.cancel"
	case writeArguments:
		return operation == "private.write"
	case privateReadArguments:
		return operation == "private.read"
	case artifactReadArguments:
		return operation == "artifact.read"
	default:
		return false
	}
}

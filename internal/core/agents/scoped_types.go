package agents

import (
	"errors"
	"fmt"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var (
	ErrDenied         = errors.New("agents: denied")
	ErrAssignment     = errors.New("agents: assignment mismatch")
	ErrAlreadyRevoked = errors.New("agents: already_revoked")
	ErrNoEffect       = errors.New("agents: no_effect")
)

// Agent fixes a trusted host's origin, not an authenticated identity. Its fields
// cannot be replaced by an operation. Authority is checked anew on every call.
type Agent struct {
	service *Service
	ids     submissionIDs
}

type GrantSpec struct {
	GrantID         uuid.UUID
	Grantee         Binding
	ParentGrantID   uuid.UUID
	Operations      []string
	Resources       []ledger.Resource
	DataClass       string
	Recipients      []Binding
	Limits          ledger.Capacity
	DelegationDepth int64
	Lifetime        string
	TaskBinding     *ledger.TaskBinding
}

type GrantIssue struct {
	CommandID, GrantID       uuid.UUID
	ExpectedAuthorityVersion uint64
	Spec                     GrantSpec
}

type GrantRevocation struct {
	CommandID, GrantID, RevokeGrantID         uuid.UUID
	ExpectedVersion, ExpectedAuthorityVersion uint64
}

type CapacitySpec struct {
	CommandID       uuid.UUID
	ExpectedVersion uint64
	Scope           ledger.ScopeKey
	Limits          ledger.Capacity
}

type TaskCreation struct {
	CommandID, GrantID, TaskID uuid.UUID
	ExpectedVersion            uint64
	Description                []byte
	AcceptanceSHA256           ledger.Digest
	Dependencies, Audience     []uuid.UUID
	Assignment                 *ledger.AssignmentInput
}

type TaskAssignment struct {
	CommandID, GrantID, TaskID uuid.UUID
	ExpectedVersion            uint64
	ExpectedAssignmentID       uuid.UUID
	ExpectedGeneration         int64
	Assignment                 ledger.AssignmentInput
}

// TaskMutation permits only start/wait/resume/submit/complete/fail/cancel.
// Creation and assignment have their own typed entry points.
type TaskMutation struct {
	CommandID, GrantID, TaskID uuid.UUID
	ExpectedVersion            uint64
	ExpectedAssignmentID       uuid.UUID
	ExpectedGeneration         int64
	Action, WaitReason         string
	WaitArtifact, Submission   *ledger.SubjectRef
	FailureCause               string
	FailureEvidence            *ledger.SubjectRef
}

type TurnAdmission struct {
	CommandID, GrantID, TurnID uuid.UUID
	ExpectedVersion            uint64
	TaskBinding                *ledger.TaskBinding
}

type TurnCancellation struct {
	CommandID, GrantID, TurnID uuid.UUID
	ExpectedVersion            uint64
}

// LocalAdmission binds a file effect but neither dispatches nor executes it.
type LocalAdmission struct {
	CommandID, GrantID, WorkID, ParentTurnID uuid.UUID
	ExpectedVersion                          uint64
	LocalOperation                           string
	Target                                   ledger.Resource
	ArgumentsSHA256                          ledger.Digest
	ToolSchema, MaxBytes                     int64
	TaskBinding                              *ledger.TaskBinding
}

// Host controls select immutable admitted work, never a caller-selected origin.
type LocalDispatch struct {
	CommandID, WorkID uuid.UUID
	ExpectedVersion   uint64
	ArgumentsSHA256   ledger.Digest
}

type LocalEnding struct {
	CommandID, WorkID uuid.UUID
	ExpectedVersion   uint64
	Outcome           string
}

type TurnEnding struct {
	CommandID, TurnID uuid.UUID
	ExpectedVersion   uint64
	Outcome           string
}

type EndingRequest struct {
	CommandID, WorkID uuid.UUID
	ExpectedVersion   uint64
}

type ScopedPrivateWrite struct {
	CommandID, GrantID uuid.UUID
	Scope              string
	ExpectedVersion    uint64
	Changes            []Change
}

type ScopedPrivateRead struct {
	CommandID, GrantID uuid.UUID
	Scope, Slot        string
	Revision, Cursor   uint64
	MaxBytes           int64
}

type ScopedPrivatePage struct {
	Receipt ScopedReceipt
	Page    PrivatePage
}

type ArtifactRead struct {
	CommandID, GrantID uuid.UUID
	Artifact           ledger.ArtifactRef
	MaxBytes           int64
}

type ArtifactPage struct {
	Receipt ScopedReceipt
	Bytes   []byte
}

type ArtifactLookup struct {
	Artifact ledger.ArtifactRef
	MaxBytes int64
}

type ScopedSnapshotLookup struct {
	GrantID uuid.UUID
	Scope   string
}

type ScopedScratchLookup struct {
	GrantID, WorkspaceID, ExpectedScratchID uuid.UUID
}

type WorkLookup struct{ WorkID, GrantID uuid.UUID }

type TaskLookup struct{ TaskID, GrantID uuid.UUID }
type TaskList struct {
	GrantID, AfterTaskID uuid.UUID
	Limit                int
}
type GrantLookup struct{ GrantID uuid.UUID }
type CapacityLookup struct{ Scope ledger.ScopeKey }
type ScopedCommandLookup struct {
	CommandID, GrantID uuid.UUID
	SubmissionSHA256   ledger.Digest
}

// ScopedReceipt records a committed decision, never proof of an external effect.
type ScopedReceipt struct {
	CommandID        uuid.UUID
	SubmissionSHA256 ledger.Digest
	Actor            string
	Origin           ledger.Origin
	Operation        string
	Target           ledger.Resource
	GrantID          uuid.UUID
	Outcome          string
	Version          int64
	WorkID           uuid.UUID
	Sequence         uint64
}

type TaskView struct {
	Task ledger.RuntimeTaskChanged
	// DescriptionRef keeps the original publication source across later states.
	DescriptionRef    ledger.ArtifactRef
	Sequence          uint64
	Readiness, Reason string
	ThroughSequence   uint64
}
type TaskPage struct {
	Tasks           []TaskView
	AfterTaskID     uuid.UUID
	ThroughSequence uint64
}

type GrantAncestor struct {
	GrantID         uuid.UUID
	Current         bool
	EffectiveLimits ledger.Capacity
}

type GrantView struct {
	Grant            ledger.RuntimeGrantIssued
	IssuerActor      string
	Issuer           ledger.Origin
	Revoked, Current bool
	Reason           string
	Ancestors        []GrantAncestor
	EffectiveLimits  ledger.Capacity
	ThroughSequence  uint64
}

type Exposure struct{ TurnSlots, LocalSlots, BufferBytes, LocalUnits int64 }

type CapacityView struct {
	Scope      ledger.ScopeKey
	Version    int64
	Configured bool
	Limits     ledger.Capacity
	// Agent views filter Held and Used to their own originating work.
	Held, Used Exposure
	// Available is owner-only; nil means withheld, not zero remaining capacity.
	Available       *Exposure
	Band            string
	ThroughSequence uint64
}

// WorkView describes admission/accounting only. recovery_pending means Open
// preserved a hold; it neither resumed work nor inferred its external outcome.
type WorkView struct {
	Admission         ledger.RuntimeWorkAdmitted
	Origin            ledger.Origin
	Version           int64
	State, Outcome    string
	Held              Exposure
	Dispatched, Ended bool
	// Sequence identifies the original admission record.
	Sequence uint64
}

type AssignmentError struct {
	ExpectedID, CurrentID                 uuid.UUID
	ExpectedGeneration, CurrentGeneration int64
}

func (e *AssignmentError) Error() string {
	return fmt.Sprintf("agents: expected assignment %s/%d, current %s/%d", e.ExpectedID, e.ExpectedGeneration, e.CurrentID, e.CurrentGeneration)
}
func (*AssignmentError) Unwrap() error { return ErrAssignment }

package agents

import (
	"errors"
	"fmt"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var (
	ErrClosed          = errors.New("agents: service closed")
	ErrNotBound        = errors.New("agents: identity not bound")
	ErrRevision        = errors.New("agents: revision mismatch")
	ErrCommandMismatch = errors.New("agents: command binding or digest mismatch")
	ErrUnavailable     = errors.New("agents: unavailable")
	ErrPoisoned        = errors.New("agents: service requires reopening")
	ErrInconsistent    = errors.New("agents: inconsistent committed records")
	ErrMaterialization = errors.New("agents: committed revision not materialized")
)

// Limits bound producer admission, preparation and reads, not historical state.
type Limits struct {
	MaxObjectBytes          int64
	MaxOperationBytes       int64
	MaxConcurrentWrites     int
	MaxReadBytes            int64
	MaxReadChunks           int
	MaxLiveTasks            int
	MaxLiveGrants           int
	MaxPendingLocalWork     int
	MaxDelegationDepth      int64
	MaxGrantOperations      int
	MaxGrantResources       int
	MaxGrantRecipients      int
	MaxTaskAudience         int
	MaxTaskDependencies     int
	MaxBudgetScopes         int
	MaxTaskDescriptionBytes int64
	MaxAgentTurnSlots       int64
	MaxScopedBytes          int64
	MaxSelectedObjects      int
}

func DefaultLimits() Limits {
	return Limits{
		MaxObjectBytes: maxObjectBytes, MaxOperationBytes: maxOperationBytes,
		MaxConcurrentWrites: 16, MaxReadBytes: maxOperationBytes, MaxReadChunks: 64,
		MaxLiveTasks: 4096, MaxLiveGrants: 8192, MaxPendingLocalWork: 4096,
		MaxDelegationDepth: 8, MaxGrantOperations: 32, MaxGrantResources: 32,
		MaxGrantRecipients: 64, MaxTaskAudience: 64, MaxTaskDependencies: 32,
		MaxBudgetScopes: 12, MaxTaskDescriptionBytes: 1 << 20,
		MaxAgentTurnSlots: 3, MaxScopedBytes: 4 << 20, MaxSelectedObjects: 64,
	}
}
func (l Limits) validate() error {
	if l.MaxObjectBytes <= 0 || l.MaxObjectBytes > maxObjectBytes || l.MaxOperationBytes < l.MaxObjectBytes || l.MaxOperationBytes > maxOperationBytes || l.MaxConcurrentWrites <= 0 || l.MaxReadBytes <= 0 || l.MaxReadBytes > maxOperationBytes || l.MaxReadChunks <= 0 || l.MaxReadChunks > 64 {
		return ErrInvalid
	}
	if l.MaxLiveTasks <= 0 || l.MaxLiveGrants <= 0 || l.MaxPendingLocalWork <= 0 ||
		l.MaxDelegationDepth < 0 || l.MaxGrantOperations <= 0 || l.MaxGrantResources <= 0 ||
		l.MaxGrantRecipients <= 0 || l.MaxTaskAudience <= 0 || l.MaxTaskDependencies < 0 ||
		l.MaxBudgetScopes <= 0 || l.MaxTaskDescriptionBytes <= 0 || l.MaxAgentTurnSlots <= 0 ||
		l.MaxScopedBytes <= 0 || l.MaxSelectedObjects <= 0 {
		return ErrInvalid
	}
	return nil
}

// Binding names an owner-addressed agent/context. Core derives its session.
type Binding struct{ AgentID, ContextID uuid.UUID }
type AgentRegistration struct {
	CommandID, AgentID, SessionID, ContextID uuid.UUID
	Directory                                string
	AgentMarkdown                            []byte
	ExpectedVersion                          uint64
}
type ContextCreation struct {
	Binding
	CommandID       uuid.UUID
	Kind            string
	ExpectedVersion uint64
}
type Change struct {
	Slot, Mode string
	Bytes      []byte
}
type PrivateWrite struct {
	Binding
	CommandID       uuid.UUID
	Scope           string
	ExpectedVersion uint64
	Changes         []Change
}
type ReadBinding struct {
	Binding
	Scope string
}
type PrivateRead struct {
	ReadBinding
	Slot     string
	Revision uint64
	Cursor   uint64 // sequence of the last transcript occurrence; zero starts a page
	MaxBytes int64
}
type Chunk struct {
	Revision, Sequence uint64
	Bytes              []byte
}
type PrivatePage struct {
	Revision   uint64
	Present    bool
	Bytes      []byte
	Chunks     []Chunk
	NextCursor uint64 // zero means that no further chunk exists at this revision
}
type CommandLookup struct {
	Binding
	CommandID        uuid.UUID
	SubmissionSHA256 ledger.Digest
}

// Receipt is the immutable applied receipt. Availability is queried separately.
type Receipt struct {
	Binding
	SessionID, CommandID                               uuid.UUID
	SubmissionSHA256                                   ledger.Digest
	Operation                                          string
	Target                                             ledger.PrivateTarget
	ExpectedVersion, Revision, AgentRevision, Sequence uint64
}
type Head struct {
	SHA256             ledger.Digest
	Bytes              int64
	Revision, Sequence uint64
}
type Snapshot struct {
	ReadBinding
	SessionID                 uuid.UUID
	Revision, ThroughSequence uint64
	Heads                     map[string]Head
	Materializations          map[string]string
	Issues                    []IntegrityIssue
}
type IntegrityIssue struct {
	ReadBinding
	Slot                           string
	Revision                       uint64
	ExpectedSHA256, ObservedSHA256 ledger.Digest
	Reason                         string
}

func (e IntegrityIssue) Error() string {
	return fmt.Sprintf("agents: integrity %s/%s revision %d: %s (expected %s, observed %s)", e.Scope, e.Slot, e.Revision, e.Reason, e.ExpectedSHA256, e.ObservedSHA256)
}
func (e IntegrityIssue) Unwrap() error { return ErrIntegrity }

type Reconciliation struct {
	Issues, Completed []IntegrityIssue
	PendingWork       []WorkView
}
type RevisionError struct{ Expected, Current uint64 }

func (e *RevisionError) Error() string {
	return fmt.Sprintf("agents: expected revision %d, current %d", e.Expected, e.Current)
}
func (e *RevisionError) Unwrap() error { return ErrRevision }

type BusyError struct{ Reason string }

func (e *BusyError) Error() string {
	if e.Reason == "" {
		return "agents: private_capacity"
	}
	return "agents: " + e.Reason
}

type MaterializationError struct {
	ReadBinding
	Revision uint64
	Cause    error
}

func (e *MaterializationError) Error() string {
	return fmt.Sprintf("agents: committed %s revision %d requires materialization", e.Scope, e.Revision)
}
func (e *MaterializationError) Unwrap() []error { return []error{ErrMaterialization, e.Cause} }

type InconsistencyError struct {
	Sequence uint64
	Reason   string
}

func (e *InconsistencyError) Error() string {
	return fmt.Sprintf("agents: inconsistent record at sequence %d: %s", e.Sequence, e.Reason)
}
func (e *InconsistencyError) Unwrap() error { return ErrInconsistent }

type ScratchLookup struct {
	Binding
	WorkspaceID, ExpectedScratchID uuid.UUID
}
type ScratchBinding struct {
	ScratchID, WorkspaceID, ContextID, ParentScratchID uuid.UUID
	State                                              string // absent, present, closed or mismatch
	Kind                                               string // dedicated or temporary when identified
}

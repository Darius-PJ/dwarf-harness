package launcher

import (
	"encoding/json"
	"uuid"
)

const (
	DraftVersion  = 1
	MaxFrameBytes = 1_048_576
	MaxTextBytes  = 65_536
	MaxJSONDepth  = 32
	MaxViews      = 32
	MaxResources  = 128
	MaxPageRows   = 128
)

type Code string

const (
	OK                 Code = "ok"
	NotReady           Code = "not_ready"
	Unauthorized       Code = "unauthorized"
	Incompatible       Code = "incompatible"
	WrongColony        Code = "wrong_colony"
	InvalidBinding     Code = "invalid_binding"
	InvalidCommand     Code = "invalid_command"
	CommandMismatch    Code = "command_mismatch"
	StaleRevision      Code = "stale_revision"
	StalePage          Code = "stale_page"
	Busy               Code = "busy"
	Held               Code = "held"
	Unavailable        Code = "unavailable"
	RecoveryPending    Code = "recovery_pending"
	InProgress         Code = "in_progress"
	TooLarge           Code = "too_large"
	ProtocolError      Code = "protocol_error"
	Closed             Code = "closed"
	ClosureUnconfirmed Code = "closure_unconfirmed"
)

type Principal struct {
	Kind             string  `json:"kind"`
	UserSID          string  `json:"user_sid,omitempty"`
	AuthenticationID string  `json:"authentication_id,omitempty"`
	UID              *uint32 `json:"uid,omitempty"`
}
type Hello struct {
	Kind         string    `json:"kind"`
	Versions     []int64   `json:"versions"`
	Profile      string    `json:"profile"`
	ColonyID     uuid.UUID `json:"colony_id"`
	ConnectionID uuid.UUID `json:"connection_id"`
}
type Welcome struct {
	Kind              string    `json:"kind"`
	Version           int64     `json:"version"`
	Profile           string    `json:"profile"`
	ConnectionID      uuid.UUID `json:"connection_id"`
	HostID            uuid.UUID `json:"host_id"`
	ColonyID          uuid.UUID `json:"colony_id"`
	Principal         Principal `json:"principal"`
	State             string    `json:"state"`
	LifecycleRevision int64     `json:"lifecycle_revision"`
	MaxFrameBytes     int64     `json:"max_frame_bytes"`
}
type Refused struct {
	Kind string `json:"kind"`
	Code Code   `json:"code"`
}
type Envelope struct {
	Version      int64     `json:"version"`
	ConnectionID uuid.UUID `json:"connection_id"`
	Seq          int64     `json:"seq"`
	Kind         string    `json:"kind"`
}
type Binding struct {
	ColonyID  uuid.UUID `json:"colony_id"`
	AgentID   uuid.UUID `json:"agent_id"`
	SessionID uuid.UUID `json:"session_id"`
	ContextID uuid.UUID `json:"context_id"`
}
type Expected struct {
	Kind     string    `json:"kind"`
	ID       uuid.UUID `json:"id"`
	Revision int64     `json:"revision"`
}
type Request struct {
	Envelope
	RPCID     int64     `json:"rpc_id"`
	Operation string    `json:"operation"`
	Binding   *Binding  `json:"binding,omitempty"`
	Expected  *Expected `json:"expected,omitempty"`
	CommandID uuid.UUID `json:"command_id,omitzero"`
	Arguments any       `json:"arguments"`
}
type Response struct {
	Envelope
	RPCID  int64 `json:"rpc_id"`
	Code   Code  `json:"code"`
	Part   int64 `json:"part"`
	Last   bool  `json:"last"`
	Result any   `json:"result,omitempty"`
}
type HostState struct {
	Envelope
	HostID            uuid.UUID `json:"host_id"`
	State             string    `json:"state"`
	LifecycleRevision int64     `json:"lifecycle_revision"`
	Code              Code      `json:"code,omitempty"`
}
type TextDelta struct {
	Envelope
	SubscriptionID uuid.UUID `json:"subscription_id"`
	TurnID         uuid.UUID `json:"turn_id"`
	Index          int64     `json:"index"`
	Text           string    `json:"text"`
}
type TextEnd struct {
	Envelope
	SubscriptionID uuid.UUID `json:"subscription_id"`
	Code           Code      `json:"code"`
}

type HostStatus struct{}
type HostWatch struct {
	AfterRevision int64 `json:"after_revision"`
}
type HostShutdown struct{}
type SessionList struct {
	AfterAgentID   uuid.UUID `json:"after_agent_id,omitzero"`
	AfterContextID uuid.UUID `json:"after_context_id,omitzero"`
	Limit          int64     `json:"limit"`
	ThroughSeq     *int64    `json:"through_seq,omitempty"`
}
type MessageSend struct {
	TurnID    uuid.UUID     `json:"turn_id"`
	Text      string        `json:"text"`
	Artifacts []ArtifactRef `json:"artifacts,omitempty"`
}
type TurnCancel struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type PromotionRequest struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Head        uuid.UUID `json:"head"`
	RestartFrom uuid.UUID `json:"restart_from,omitzero"`
}
type TurnStatus struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type ContextStatus struct{}
type SessionEvents struct {
	AfterSeq int64 `json:"after_seq"`
	Limit    int64 `json:"limit"`
}
type SessionWait struct {
	Token uuid.UUID `json:"token"`
}
type TextAttach struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type TextClose struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
}
type TextFinal struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type ServiceStatus struct {
	AgentID uuid.UUID `json:"agent_id,omitzero"`
}
type ServiceOutages struct {
	OutageID        uuid.UUID            `json:"outage_id,omitzero"`
	After           *ServiceOutageCursor `json:"after,omitempty"`
	Limit           int64                `json:"limit,omitempty"`
	ThroughSequence int64                `json:"through_sequence,omitempty"`
}

type EmptyResult struct{}
type HostStatusResult struct {
	HostID            uuid.UUID       `json:"host_id"`
	ColonyID          uuid.UUID       `json:"colony_id"`
	State             string          `json:"state"`
	LifecycleRevision int64           `json:"lifecycle_revision"`
	Code              Code            `json:"code,omitempty"`
	Startup           *StartupSummary `json:"startup,omitempty"`
}
type StartupSummary struct {
	IntegrityIssues  int64               `json:"integrity_issues"`
	PendingPaid      int64               `json:"pending_paid"`
	UnresumableTurns int64               `json:"unresumable_turns"`
	Conditions       []string            `json:"conditions"`
	Credentials      []CredentialVersion `json:"credentials"`
}
type CredentialVersion struct {
	ScopeID       uuid.UUID `json:"scope_id"`
	Provider      string    `json:"provider"`
	Version       uuid.UUID `json:"version"`
	OtherVersions int64     `json:"other_versions"`
}
type SessionRow struct {
	AgentID         uuid.UUID `json:"agent_id"`
	SessionID       uuid.UUID `json:"session_id"`
	ContextID       uuid.UUID `json:"context_id"`
	Title           string    `json:"title"`
	ContextRevision int64     `json:"context_revision"`
}
type SessionListResult struct {
	Rows          []SessionRow `json:"rows"`
	ThroughSeq    int64        `json:"through_seq"`
	NextAgentID   uuid.UUID    `json:"next_agent_id,omitzero"`
	NextContextID uuid.UUID    `json:"next_context_id,omitzero"`
}
type MutationResult struct {
	RequestID             uuid.UUID `json:"request_id"`
	BindingSequence       int64     `json:"binding_sequence"`
	State                 string    `json:"state"`
	Outcome               string    `json:"outcome,omitempty"`
	NativeReceiptSequence *int64    `json:"native_receipt_sequence,omitempty"`
	NativeEffectSequence  *int64    `json:"native_effect_sequence,omitempty"`
	TurnID                uuid.UUID `json:"turn_id,omitzero"`
	Code                  Code      `json:"code,omitempty"`
}

// HeldResult is a pre-bind refusal: there is no invented binding sequence.
type HeldResult struct {
	Eligibility EligibilityView `json:"eligibility"`
}

// Digest is lowercase hexadecimal SHA-256, never a secret value.
type Digest string
type ObjectRef struct {
	SHA256 Digest `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type ArtifactRef struct {
	SourceSequence int64  `json:"source_sequence"`
	SHA256         Digest `json:"sha256"`
	Bytes          int64  `json:"bytes"`
}

// RecordPayload carries only an explicitly allowed, already-authorized record
// payload. The host and client must also invoke its frozen ledger decoder.
type RecordPayload struct {
	RecordType    string          `json:"record_type"`
	RecordVersion int64           `json:"record_version"`
	Payload       json.RawMessage `json:"payload"`
}
type Pending struct {
	Kind            string    `json:"kind"`
	ID              uuid.UUID `json:"id"`
	CommandID       uuid.UUID `json:"command_id,omitzero"`
	ExpectedVersion int64     `json:"expected_version"`
	SourceSequence  *int64    `json:"source_sequence,omitempty"`
}
type TurnView struct {
	TurnID         uuid.UUID        `json:"turn_id"`
	State          string           `json:"state"`
	Reason         string           `json:"reason"`
	ModelCalls     int64            `json:"model_calls"`
	ToolDispatches int64            `json:"tool_dispatches"`
	BoundSequence  int64            `json:"bound_sequence"`
	OwnerVersion   int64            `json:"owner_version"`
	OperationID    uuid.UUID        `json:"operation_id,omitzero"`
	Generation     int64            `json:"generation"`
	SavedPhase     string           `json:"saved_phase,omitempty"`
	ResumeState    string           `json:"resume_state,omitempty"`
	WaitReason     string           `json:"wait_reason,omitempty"`
	Pending        []Pending        `json:"pending"`
	DeadlineUTCNS  int64            `json:"deadline_utc_ns"`
	Result         *RecordPayload   `json:"result,omitempty"`
	Eligibility    *EligibilityView `json:"eligibility,omitempty"`
}
type ContextView struct {
	ContextID   uuid.UUID        `json:"context_id"`
	Revision    int64            `json:"revision"`
	Turns       []TurnView       `json:"turns"`
	ThroughSeq  int64            `json:"through_seq"`
	Eligibility *EligibilityView `json:"eligibility,omitempty"`
	Service     *ServiceView     `json:"service,omitempty"`
}
type TaskBinding struct {
	TaskID       uuid.UUID `json:"task_id"`
	AssignmentID uuid.UUID `json:"assignment_id"`
	Generation   int64     `json:"generation"`
}
type EligibilityTurn struct {
	ContextID          uuid.UUID    `json:"context_id"`
	TurnID             uuid.UUID    `json:"turn_id"`
	StartCommandID     uuid.UUID    `json:"start_command_id"`
	AdmissionCommandID uuid.UUID    `json:"admission_command_id"`
	InputSHA256        Digest       `json:"input_sha256"`
	TaskBinding        *TaskBinding `json:"task_binding,omitempty"`
}
type EligibilityRefusal struct {
	TurnID         uuid.UUID `json:"turn_id"`
	StartCommandID uuid.UUID `json:"start_command_id"`
	Stage          string    `json:"stage"`
	Code           string    `json:"code"`
}
type EligibilityDeclaration struct {
	ActionID      uuid.UUID           `json:"action_id"`
	InitialTurn   EligibilityTurn     `json:"initial_turn"`
	PermittedTurn EligibilityTurn     `json:"permitted_turn"`
	Refusal       *EligibilityRefusal `json:"refusal,omitempty"`
}
type EligibilityFirstAction struct {
	Declaration           EligibilityDeclaration `json:"declaration"`
	Disposition           string                 `json:"disposition"`
	FirstAdmittedTurn     *EligibilityTurn       `json:"first_admitted_turn,omitempty"`
	FirstAdmittedSequence int64                  `json:"first_admitted_sequence,omitempty"`
	NativeSequence        int64                  `json:"native_sequence,omitempty"`
	BoundSequence         int64                  `json:"bound_sequence,omitempty"`
	NativeEndingSequence  int64                  `json:"native_ending_sequence,omitempty"`
}
type EligibilityView struct {
	State           string                  `json:"state"`
	Reason          string                  `json:"reason"`
	Version         int64                   `json:"version"`
	OperationID     uuid.UUID               `json:"operation_id,omitzero"`
	ThroughSequence int64                   `json:"through_sequence"`
	FirstAction     *EligibilityFirstAction `json:"first_action,omitempty"`
}
type Event struct {
	Sequence    int64     `json:"sequence"`
	Type        string    `json:"type"`
	AgentID     uuid.UUID `json:"agent_id,omitzero"`
	SessionID   uuid.UUID `json:"session_id,omitzero"`
	ContextID   uuid.UUID `json:"context_id,omitzero"`
	TurnID      uuid.UUID `json:"turn_id,omitzero"`
	OperationID uuid.UUID `json:"operation_id,omitzero"`
	Data        any       `json:"data"`
}
type EventPage struct {
	Events     []Event   `json:"events"`
	ThroughSeq int64     `json:"through_seq"`
	Token      uuid.UUID `json:"token"`
}

// TextSnapshotPart is result part zero of text.attach. Continuations and all
// text.final parts use TextPart; Response.Part supplies their ordinal.
type TextSnapshotPart struct {
	Text               string     `json:"text"`
	DeltaHighWater     int64      `json:"delta_high_water"`
	State              string     `json:"state"`
	FinalMessageObject *ObjectRef `json:"final_message_object,omitempty"`
	SubscriptionID     uuid.UUID  `json:"subscription_id,omitzero"`
}
type TextPart struct {
	Text string `json:"text"`
}

type ServiceCause struct {
	Family            string    `json:"family"`
	Code              string    `json:"code"`
	ScopeKind         string    `json:"scope_kind"`
	Provider          string    `json:"provider,omitempty"`
	CredentialScopeID uuid.UUID `json:"credential_scope_id,omitzero"`
	CredentialVersion uuid.UUID `json:"credential_version,omitzero"`
	Model             string    `json:"model,omitempty"`
	Region            *string   `json:"region,omitempty"`
	AuthoritySHA256   Digest    `json:"authority_sha256,omitempty"`
	AgentID           uuid.UUID `json:"agent_id,omitzero"`
	ContextID         uuid.UUID `json:"context_id,omitzero"`
	PrivateScope      string    `json:"private_scope,omitempty"`
	Revision          *int64    `json:"revision,omitempty"`
}
type ServiceCauseView struct {
	CauseID       uuid.UUID      `json:"cause_id"`
	OutageID      uuid.UUID      `json:"outage_id,omitzero"`
	Cause         ServiceCause   `json:"cause"`
	CauseVersion  int64          `json:"cause_version"`
	MemberVersion int64          `json:"member_version"`
	Generation    int64          `json:"generation"`
	Phase         string         `json:"phase"`
	Condition     string         `json:"condition"`
	FirstSource   *ServiceSource `json:"first_source,omitempty"`
	DueUTCNS      *int64         `json:"due_utc_ns,omitempty"`
}
type ServiceRecovery struct {
	UncoveredObservations int64  `json:"uncovered_observations"`
	RetainedSources       int64  `json:"retained_sources"`
	LastErrorClass        string `json:"last_error_class"`
}
type ServiceView struct {
	AgentID         uuid.UUID          `json:"agent_id"`
	State           string             `json:"state"`
	Active          []ServiceCauseView `json:"active"`
	Pending         []ServiceCauseView `json:"pending"`
	Unsupported     []ServiceCauseView `json:"unsupported"`
	ThroughSequence int64              `json:"through_sequence"`
	SourceThrough   int64              `json:"source_through"`
	Recovery        ServiceRecovery    `json:"recovery"`
}
type ServiceOutageRow struct {
	OutageID      uuid.UUID    `json:"outage_id"`
	CauseID       uuid.UUID    `json:"cause_id"`
	AgentID       uuid.UUID    `json:"agent_id"`
	Cause         ServiceCause `json:"cause"`
	Generation    int64        `json:"generation"`
	Active        bool         `json:"active"`
	Unsupported   bool         `json:"unsupported"`
	BeginNoticeID uuid.UUID    `json:"begin_notice_id,omitzero"`
	EndNoticeID   uuid.UUID    `json:"end_notice_id,omitzero"`
	BeginSequence int64        `json:"begin_sequence"`
	EndSequence   int64        `json:"end_sequence"`
	Members       []uuid.UUID  `json:"members"`
	ActiveMembers []uuid.UUID  `json:"active_members"`
}
type ServiceOutageCursor struct {
	OutageID        uuid.UUID `json:"outage_id"`
	AgentID         uuid.UUID `json:"agent_id"`
	ThroughSequence int64     `json:"through_sequence"`
	CallerID        uuid.UUID `json:"caller_id"`
	FilterAgentID   uuid.UUID `json:"filter_agent_id,omitzero"`
	FilterCauseID   uuid.UUID `json:"filter_cause_id,omitzero"`
	FilterOutageID  uuid.UUID `json:"filter_outage_id,omitzero"`
}
type ServiceOutagePage struct {
	Rows            []ServiceOutageRow   `json:"rows"`
	ThroughSequence int64                `json:"through_sequence"`
	Next            *ServiceOutageCursor `json:"next,omitempty"`
}

type Origin struct {
	AgentID   uuid.UUID `json:"agent_id"`
	SessionID uuid.UUID `json:"session_id"`
	ContextID uuid.UUID `json:"context_id"`
}
type ServiceSource struct {
	Kind             string             `json:"kind"`
	ObservationID    uuid.UUID          `json:"observation_id"`
	ObservedThrough  int64              `json:"observed_through"`
	SourceSequence   *int64             `json:"source_sequence,omitempty"`
	CommandID        uuid.UUID          `json:"command_id,omitzero"`
	SubmissionSHA256 Digest             `json:"submission_sha256,omitempty"`
	Origin           *Origin            `json:"origin,omitempty"`
	TurnID           uuid.UUID          `json:"turn_id,omitzero"`
	AttemptID        uuid.UUID          `json:"attempt_id,omitzero"`
	Object           *ObjectRef         `json:"object,omitempty"`
	ObservedUTCNS    int64              `json:"observed_utc_ns"`
	ClockHeld        bool               `json:"clock_held"`
	Reason           string             `json:"reason"`
	Provenance       string             `json:"provenance,omitempty"`
	ResetUTCNS       *int64             `json:"reset_utc_ns,omitempty"`
	Money            *ServiceMoney      `json:"money,omitempty"`
	Completion       *ServiceCompletion `json:"completion,omitempty"`
	WorksetSHA256    Digest             `json:"workset_sha256,omitempty"`
	FailedWorkCount  *int64             `json:"failed_work_count,omitempty"`
	ExecutionRunID   uuid.UUID          `json:"execution_run_id,omitzero"`
	ExecutionProof   string             `json:"execution_proof,omitempty"`
}
type PaidScope struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id,omitzero"`
}
type ServiceMoney struct {
	Scope             PaidScope `json:"scope"`
	Period            string    `json:"period"`
	SpendSequence     int64     `json:"spend_sequence"`
	Mode              string    `json:"mode"`
	OccupiedMicros    string    `json:"occupied_micros"`
	CeilingMicros     *string   `json:"ceiling_micros,omitempty"`
	Coverage          string    `json:"coverage"`
	WorksetSHA256     Digest    `json:"workset_sha256,omitempty"`
	EligibleWorkCount *int64    `json:"eligible_work_count,omitempty"`
	FailedWorkCount   *int64    `json:"failed_work_count,omitempty"`
}
type ServiceCompletionPermit struct {
	CauseID          uuid.UUID `json:"cause_id"`
	CauseVersion     int64     `json:"cause_version"`
	MemberGeneration int64     `json:"member_generation"`
	Kind             string    `json:"kind"`
	ProofSequence    *int64    `json:"proof_sequence,omitempty"`
	FailedWorkCount  *int64    `json:"failed_work_count,omitempty"`
}
type ServiceCompletion struct {
	Provider             string                   `json:"provider"`
	Terminal             string                   `json:"terminal"`
	NativeResultSequence *int64                   `json:"native_result_sequence,omitempty"`
	FinancialKind        string                   `json:"financial_kind,omitempty"`
	Permit               *ServiceCompletionPermit `json:"permit,omitempty"`
}
type ServiceChanged struct {
	AgentID         uuid.UUID     `json:"agent_id"`
	CauseID         uuid.UUID     `json:"cause_id"`
	ExpectedVersion int64         `json:"expected_version"`
	Version         int64         `json:"version"`
	Generation      int64         `json:"generation"`
	Phase           string        `json:"phase"`
	FirstSource     ServiceSource `json:"first_source"`
	DueUTCNS        *int64        `json:"due_utc_ns,omitempty"`
	OutageID        uuid.UUID     `json:"outage_id,omitzero"`
	Source          ServiceSource `json:"source"`
}
type OutageNotice struct {
	OutageID             uuid.UUID `json:"outage_id"`
	CauseID              uuid.UUID `json:"cause_id"`
	Edge                 string    `json:"edge"`
	NoticeID             uuid.UUID `json:"notice_id"`
	MemberSourceSequence int64     `json:"member_source_sequence"`
}

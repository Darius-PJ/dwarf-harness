package session

import (
	"context"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var (
	ErrReattachRequired = errors.New("session: reattach required")
	ErrFinished         = errors.New("session: finished")
	ErrClosed           = errors.New("session: closed")
)

// Session is a trusted-host owner view bound to one existing agent/session.
// Closing a view neither cancels its turns nor shuts down Core.
type Session interface {
	Do(context.Context, Command) (CommandResult, error)
	Since(context.Context, uint64, int) (EventPage, error)
	LiveText(context.Context, uuid.UUID) (TextSnapshot, *TextSubscription, error)
	Close()
}

// Command has one concrete Arguments value from the closed set below. Core
// validates Schema, UUIDv7 identities and the operation/value pairing.
type Command struct {
	Schema    int       `json:"schema"`
	CommandID uuid.UUID `json:"command_id"`
	ContextID uuid.UUID `json:"context_id"`
	Operation string    `json:"operation"`
	Arguments any       `json:"arguments"`
}

type MessageSend struct {
	TurnID    uuid.UUID            `json:"turn_id"`
	Text      string               `json:"text"`
	Artifacts []ledger.ArtifactRef `json:"artifacts"`
}
type TurnCancel struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type TurnStatus struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type ContextStatus struct{}
type PromotionRequest struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Head        uuid.UUID `json:"head"`
	RestartFrom uuid.UUID `json:"restart_from,omitzero"`
}

type CommandResult struct {
	CommandID       uuid.UUID `json:"command_id"`
	Outcome         string    `json:"outcome"`
	ReceiptSequence uint64    `json:"receipt_sequence,omitempty"`
	EffectSequence  uint64    `json:"effect_sequence,omitempty"`
	TurnID          uuid.UUID `json:"turn_id,omitzero"`
	Data            any       `json:"data,omitempty"`
}

type TurnView struct {
	TurnID         uuid.UUID                 `json:"turn_id"`
	State          string                    `json:"state"`
	Reason         string                    `json:"reason,omitempty"`
	ModelCalls     int64                     `json:"model_calls"`
	ToolDispatches int64                     `json:"tool_dispatches"`
	BoundSequence  uint64                    `json:"bound_sequence"`
	Result         *ledger.RuntimeTurnResult `json:"result,omitempty"`
}

type ContextView struct {
	ContextID       uuid.UUID  `json:"context_id"`
	Revision        uint64     `json:"revision"`
	Turns           []TurnView `json:"turns"`
	ThroughSequence uint64     `json:"through_sequence"`
}

type Event struct {
	Sequence           uint64    `json:"sequence"`
	Type               string    `json:"type"`
	AgentID            uuid.UUID `json:"agent_id"`
	ExecutionSessionID uuid.UUID `json:"execution_session_id"`
	ContextID          uuid.UUID `json:"context_id"`
	TurnID             uuid.UUID `json:"turn_id"`
	OperationID        uuid.UUID `json:"operation_id"`
	Data               any       `json:"data"`
}
type EventPage struct {
	Events          []Event         `json:"events"`
	ThroughSequence uint64          `json:"through_sequence"`
	Wake            <-chan struct{} `json:"-"`
}

type TextSnapshot struct {
	Text               string            `json:"text"`
	DeltaHighWater     uint64            `json:"delta_high_water"`
	State              string            `json:"state"`
	FinalMessageObject *ledger.ObjectRef `json:"final_message_object,omitempty"`
}
type TextDelta struct {
	Index uint64 `json:"index"`
	Text  string `json:"text"`
}

// A subscription never drops a chunk silently: Err distinguishes normal
// completion, a closed view, a capture refusal and an overflow requiring attach.
type TextSubscription struct {
	Deltas <-chan TextDelta
	close  func()
	err    func() error
}

func NewTextSubscription(deltas <-chan TextDelta, close func(), terminalError func() error) *TextSubscription {
	return &TextSubscription{Deltas: deltas, close: close, err: terminalError}
}
func (s *TextSubscription) Close() {
	if s != nil && s.close != nil {
		s.close()
	}
}
func (s *TextSubscription) Err() error {
	if s == nil || s.err == nil {
		return ErrClosed
	}
	return s.err()
}

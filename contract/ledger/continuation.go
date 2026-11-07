package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"uuid"
)

// F3-16's two version-1 types are frozen (D169).
const (
	TypeRuntimeContinuationChanged = "runtime.continuation_changed"
	TypeRuntimeContinuationReceipt = "runtime.continuation_receipt"

	// MaxContinuationPayloadBytes bounds each complete input and compact output,
	// including a receipt's embedded submission and ignored input extensions.
	MaxContinuationPayloadBytes = 64 << 10
)

func encodeContinuationPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxContinuationPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxContinuationPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeContinuationPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxContinuationPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxContinuationPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

// ContinuationPending identifies a child, not permission to dispatch it.
// CommandID is absent for a child with no native command. SourceSequence is
// absent until its exact source commits; recovery never dispatches that entry.
type ContinuationPending struct {
	Kind            string    `json:"kind"`
	ID              uuid.UUID `json:"id"`
	CommandID       uuid.UUID `json:"command_id,omitzero"`
	ExpectedVersion int64     `json:"expected_version"`
	SourceSequence  int64     `json:"source_sequence,omitempty"`
}

// ContinuationNoticeCursor is presentation progress, never authority or deletion.
type ContinuationNoticeCursor struct {
	WorkspaceID   uuid.UUID `json:"workspace_id"`
	ScratchID     uuid.UUID `json:"scratch_id"`
	AfterSequence int64     `json:"after_sequence"`
}

// RuntimeContinuationChanged snapshots the continuation owner, separately from
// provider artifacts. OperationID names the transition that opened the logical
// operation; the envelope names this transition's own command instead.
type RuntimeContinuationChanged struct {
	TurnID               uuid.UUID                  `json:"turn_id"`
	BoundSequence        int64                      `json:"bound_sequence"`
	ExpectedVersion      int64                      `json:"expected_version"`
	Version              int64                      `json:"version"`
	ExpectedState        string                     `json:"expected_state"`
	State                string                     `json:"state"`
	ResumeState          string                     `json:"resume_state,omitempty"`
	OperationID          uuid.UUID                  `json:"operation_id"`
	Generation           int64                      `json:"generation"`
	WaitReason           string                     `json:"wait_reason"`
	ModelCalls           int64                      `json:"model_calls"`
	ToolDispatches       int64                      `json:"tool_dispatches"`
	DeadlineUTCNS        int64                      `json:"deadline_utc_ns"`
	Pending              []ContinuationPending      `json:"pending"`
	StopReason           string                     `json:"stop_reason,omitempty"`
	CancelCommandID      uuid.UUID                  `json:"cancel_command_id,omitzero"`
	FinalMessageObject   *ObjectRef                 `json:"final_message_object,omitempty"`
	NativeEndingSequence int64                      `json:"native_ending_sequence,omitempty"`
	NoticeCursors        []ContinuationNoticeCursor `json:"notice_cursors"`
}

func (p RuntimeContinuationChanged) arguments() ContinuationArguments {
	return ContinuationArguments{p.BoundSequence, p.State, p.ResumeState, p.OperationID, p.Generation, p.WaitReason, p.ModelCalls, p.ToolDispatches, p.DeadlineUTCNS, p.Pending, p.StopReason, p.CancelCommandID, p.FinalMessageObject, p.NativeEndingSequence, p.NoticeCursors}
}

func continuationExpectation(version int64, state string) bool {
	return version >= 0 && version < math.MaxInt64 && scopedToken(state) && (version == 0) == (state == "absent") && state != "terminal"
}

func (p RuntimeContinuationChanged) check() error {
	if !isV7(p.TurnID) || !continuationExpectation(p.ExpectedVersion, p.ExpectedState) || p.Version != p.ExpectedVersion+1 {
		return errors.New("invalid continuation identity or version")
	}
	if p.ExpectedVersion == 0 && (p.Generation != 1 || knownContinuationState(p.State) && p.State != "ready" && p.State != "reconciling" || p.StopReason != "") {
		return errors.New("invalid initial continuation")
	}
	return p.arguments().check()
}

func (p RuntimeContinuationChanged) Encode() (json.RawMessage, error) {
	return encodeContinuationPayload(TypeRuntimeContinuationChanged, p, p.check())
}

func DecodeRuntimeContinuationChanged(js json.RawMessage) (RuntimeContinuationChanged, error) {
	var p RuntimeContinuationChanged
	err := decodeContinuationPayload(TypeRuntimeContinuationChanged, js, func(m *members) error {
		turn, expected, version, state := m.id("turn_id"), m.nat("expected_version", true), m.nat("version", true), m.text("expected_state")
		a := readContinuationArguments(m)
		p = RuntimeContinuationChanged{turn, a.BoundSequence, expected, version, state, a.State, a.ResumeState, a.OperationID, a.Generation, a.WaitReason, a.ModelCalls, a.ToolDispatches, a.DeadlineUTCNS, a.Pending, a.StopReason, a.CancelCommandID, a.FinalMessageObject, a.NativeEndingSequence, a.NoticeCursors}
		return p.check()
	})
	if err != nil {
		return RuntimeContinuationChanged{}, err
	}
	return p, nil
}

func compareContinuationPending(a, b ContinuationPending) int {
	if c := strings.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	if c := bytes.Compare(a.ID[:], b.ID[:]); c != 0 {
		return c
	}
	return bytes.Compare(a.CommandID[:], b.CommandID[:])
}

func compareContinuationCursor(a, b ContinuationNoticeCursor) int {
	if c := bytes.Compare(a.WorkspaceID[:], b.WorkspaceID[:]); c != 0 {
		return c
	}
	return bytes.Compare(a.ScratchID[:], b.ScratchID[:])
}

// RuntimeContinuationReceipt binds a command to the schema-7 preimage and the
// actual change record. Writers emit recorded; unknown outcomes remain opaque.
type RuntimeContinuationReceipt struct {
	CommandID        uuid.UUID              `json:"command_id"`
	SubmissionSHA256 Digest                 `json:"submission_sha256"`
	Submission       ContinuationSubmission `json:"submission"`
	Outcome          string                 `json:"outcome"`
	EffectSequence   int64                  `json:"effect_sequence"`
}

func (p RuntimeContinuationReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) || p.EffectSequence <= 0 {
		return errors.New("invalid continuation receipt")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("continuation receipt submission digest mismatch")
	}
	return nil
}

func (p RuntimeContinuationReceipt) Encode() (json.RawMessage, error) {
	return encodeContinuationPayload(TypeRuntimeContinuationReceipt, p, p.check())
}

func DecodeRuntimeContinuationReceipt(js json.RawMessage) (RuntimeContinuationReceipt, error) {
	var p RuntimeContinuationReceipt
	err := decodeContinuationPayload(TypeRuntimeContinuationReceipt, js, func(m *members) error {
		p = RuntimeContinuationReceipt{m.id("command_id"), m.digest("submission_sha256"), graphObject(m, "submission", readContinuationSubmission), m.text("outcome"), m.nat("effect_sequence", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeContinuationReceipt{}, err
	}
	return p, nil
}

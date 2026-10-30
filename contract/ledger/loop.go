package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// F3-15's six version-1 types are frozen (D165).
const (
	TypePrivateTurnRevision = "private.turn_revision"
	TypeRuntimeTurnBound    = "runtime.turn_bound"
	TypeRuntimeToolStarted  = "runtime.tool_started"
	TypeRuntimeToolResult   = "runtime.tool_result"
	TypeRuntimeTurnResult   = "runtime.turn_result"
	TypeRuntimeLoopReceipt  = "runtime.loop_receipt"

	// MaxLoopPayloadBytes bounds complete input and compact output, including
	// ignored extensions and the entire receipt with its embedded submission.
	MaxLoopPayloadBytes = 64 << 10
)

func encodeLoopPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxLoopPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxLoopPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeLoopPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxLoopPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxLoopPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

// PrivateTurnRevision appends one manifest chunk on the shared context head.
// It never initializes a scope or revises another slot.
type PrivateTurnRevision struct {
	Scope           string          `json:"scope"`
	ExpectedVersion int64           `json:"expected_version"`
	Revision        int64           `json:"revision"`
	Changes         []PrivateChange `json:"changes"`
}

func (p PrivateTurnRevision) check() error {
	if p.Scope != PrivateScopeContext || p.ExpectedVersion < 1 || len(p.Changes) != 1 || p.Changes[0].Slot != PrivateSlotTranscript {
		return errors.New("turn revision requires one existing context transcript append")
	}
	return PrivateRevision(p).check()
}

func (p PrivateTurnRevision) Encode() (json.RawMessage, error) {
	wire := struct {
		Scope           string              `json:"scope"`
		ExpectedVersion int64               `json:"expected_version"`
		Revision        int64               `json:"revision"`
		Changes         []privateChangeWire `json:"changes"`
	}{p.Scope, p.ExpectedVersion, p.Revision, scopedChanges(p.Changes)}
	return encodeLoopPayload(TypePrivateTurnRevision, wire, p.check())
}

func DecodePrivateTurnRevision(js json.RawMessage) (PrivateTurnRevision, error) {
	var p PrivateTurnRevision
	err := decodeLoopPayload(TypePrivateTurnRevision, js, func(m *members) error {
		p = PrivateTurnRevision{m.text("scope"), m.nat("expected_version", true), m.nat("revision", true), readScopedChanges(m, false)}
		return p.check()
	})
	if err != nil {
		return PrivateTurnRevision{}, err
	}
	return p, nil
}

// RuntimeTurnBound selects immutable context-owned input, policy and render.
// AdmissionSequence identifies the existing F3-11 turn admission effect.
type RuntimeTurnBound struct {
	TurnID            uuid.UUID `json:"turn_id"`
	AdmissionSequence int64     `json:"admission_sequence"`
	InputObject       ObjectRef `json:"input_object"`
	PolicyObject      ObjectRef `json:"policy_object"`
	RenderObject      ObjectRef `json:"render_object"`
}

func (p RuntimeTurnBound) check() error {
	if !isV7(p.TurnID) || p.AdmissionSequence <= 0 || !validObject(p.InputObject) || !validObject(p.PolicyObject) || !validObject(p.RenderObject) {
		return errors.New("invalid turn binding")
	}
	return nil
}

func (p RuntimeTurnBound) Encode() (json.RawMessage, error) {
	return encodeLoopPayload(TypeRuntimeTurnBound, p, p.check())
}

func DecodeRuntimeTurnBound(js json.RawMessage) (RuntimeTurnBound, error) {
	var p RuntimeTurnBound
	err := decodeLoopPayload(TypeRuntimeTurnBound, js, func(m *members) error {
		p = RuntimeTurnBound{m.id("turn_id"), m.nat("admission_sequence", true), graphObject(m, "input_object", readObject), graphObject(m, "policy_object", readObject), graphObject(m, "render_object", readObject)}
		return p.check()
	})
	if err != nil {
		return RuntimeTurnBound{}, err
	}
	return p, nil
}

// RuntimeToolStarted journals an accepted invocation, not an execution permit.
// GrantID records passive disclosure authority only; native effects instead
// bind their command ID and later verify its actual receipt.
type RuntimeToolStarted struct {
	ToolID          uuid.UUID `json:"tool_id"`
	ParentTurnID    uuid.UUID `json:"parent_turn_id"`
	AttemptID       uuid.UUID `json:"attempt_id"`
	ProviderCallID  string    `json:"provider_call_id"`
	ToolName        string    `json:"tool_name"`
	ToolSchema      int64     `json:"tool_schema"`
	RequestObject   ObjectRef `json:"request_object"`
	RequestSHA256   Digest    `json:"request_sha256"`
	Operation       string    `json:"operation"`
	Target          *Resource `json:"target,omitempty"`
	GrantID         uuid.UUID `json:"grant_id,omitzero"`
	EffectCommandID uuid.UUID `json:"effect_command_id,omitzero"`
}

func loopToolName(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func loopCallID(s string) bool {
	if len(s) < 1 || len(s) > 256 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func (p RuntimeToolStarted) check() error {
	if !isV7(p.ToolID) || !isV7(p.ParentTurnID) || !isV7(p.AttemptID) || !loopCallID(p.ProviderCallID) || !loopToolName(p.ToolName) || p.ToolSchema <= 0 || !validObject(p.RequestObject) || p.RequestSHA256 != p.RequestObject.SHA256 || !validPath(p.Operation) || len(p.Operation) > 64 {
		return errors.New("invalid tool invocation")
	}
	if p.Target != nil && p.Target.check() != nil {
		return errors.New("invalid tool target")
	}
	if (p.Operation == "tools.discover" || p.Operation == "task.list") && p.Target != nil {
		return errors.New("catalogue query forbids target")
	}
	invalid := p.Operation == "invalid_argument" || p.Operation == "unrecognized"
	if invalid {
		if p.Target != nil || p.GrantID != (uuid.UUID{}) || p.EffectCommandID != (uuid.UUID{}) {
			return errors.New("invalid invocation forbids target and authority references")
		}
	} else if p.EffectCommandID == (uuid.UUID{}) {
		if !isV7(p.GrantID) {
			return errors.New("passive invocation requires grant_id")
		}
	} else if !isV7(p.EffectCommandID) || p.GrantID != (uuid.UUID{}) {
		return errors.New("native invocation requires effect command and no grant_id")
	}
	return nil
}

func (p RuntimeToolStarted) Encode() (json.RawMessage, error) {
	return encodeLoopPayload(TypeRuntimeToolStarted, p, p.check())
}

func readLoopToolStarted(m *members) RuntimeToolStarted {
	p := RuntimeToolStarted{ToolID: m.id("tool_id"), ParentTurnID: m.id("parent_turn_id"), AttemptID: m.id("attempt_id"), ProviderCallID: m.text("provider_call_id"), ToolName: m.text("tool_name"), ToolSchema: m.nat("tool_schema", true), RequestObject: graphObject(m, "request_object", readObject), RequestSHA256: m.digest("request_sha256"), Operation: m.text("operation"), Target: optionalObject(m, "target", readResource)}
	p.GrantID, _ = m.optionalID("grant_id")
	p.EffectCommandID, _ = m.optionalID("effect_command_id")
	return p
}

func DecodeRuntimeToolStarted(js json.RawMessage) (RuntimeToolStarted, error) {
	var p RuntimeToolStarted
	err := decodeLoopPayload(TypeRuntimeToolStarted, js, func(m *members) error { p = readLoopToolStarted(m); return p.check() })
	if err != nil {
		return RuntimeToolStarted{}, err
	}
	return p, nil
}

// RuntimeToolResult ends the journal child without releasing any accounting.
// EffectReceiptSequence, when present, identifies an actual native receipt.
type RuntimeToolResult struct {
	ToolID                uuid.UUID `json:"tool_id"`
	StartedSequence       int64     `json:"started_sequence"`
	Outcome               string    `json:"outcome"`
	ResultObject          ObjectRef `json:"result_object"`
	EffectReceiptSequence int64     `json:"effect_receipt_sequence,omitempty"`
}

func (p RuntimeToolResult) check() error {
	if !isV7(p.ToolID) || p.StartedSequence <= 0 || !scopedToken(p.Outcome) || !validObject(p.ResultObject) || p.EffectReceiptSequence < 0 {
		return errors.New("invalid tool result")
	}
	return nil
}

func (p RuntimeToolResult) Encode() (json.RawMessage, error) {
	return encodeLoopPayload(TypeRuntimeToolResult, p, p.check())
}

func readLoopToolResult(m *members) RuntimeToolResult {
	p := RuntimeToolResult{ToolID: m.id("tool_id"), StartedSequence: m.nat("started_sequence", true), Outcome: m.text("outcome"), ResultObject: graphObject(m, "result_object", readObject)}
	if n := optionalScopedNat(m, "effect_receipt_sequence"); n != nil {
		p.EffectReceiptSequence = *n
		if *n == 0 {
			m.err = errors.New("present effect_receipt_sequence must be positive")
		}
	}
	return p
}

func DecodeRuntimeToolResult(js json.RawMessage) (RuntimeToolResult, error) {
	var p RuntimeToolResult
	err := decodeLoopPayload(TypeRuntimeToolResult, js, func(m *members) error { p = readLoopToolResult(m); return p.check() })
	if err != nil {
		return RuntimeToolResult{}, err
	}
	return p, nil
}

// RuntimeTurnResult records loop closure independently of F3-11 slot release.
// Only a finished turn selects the validated natural final message.
type RuntimeTurnResult struct {
	TurnID             uuid.UUID  `json:"turn_id"`
	BoundSequence      int64      `json:"bound_sequence"`
	Outcome            string     `json:"outcome"`
	Reason             string     `json:"reason"`
	ModelCalls         int64      `json:"model_calls"`
	ToolDispatches     int64      `json:"tool_dispatches"`
	FinalMessageObject *ObjectRef `json:"final_message_object,omitempty"`
	EndingSequence     int64      `json:"ending_sequence"`
}

func validLoopFinal(outcome string, final *ObjectRef) bool {
	if final != nil && !validObject(*final) {
		return false
	}
	switch outcome {
	case "finished":
		return final != nil
	case "cancelled", "failed", "interrupted":
		return final == nil
	default:
		return true
	}
}

func (p RuntimeTurnResult) check() error {
	if !isV7(p.TurnID) || p.BoundSequence <= 0 || !scopedToken(p.Outcome) || !scopedToken(p.Reason) || p.ModelCalls < 0 || p.ToolDispatches < 0 || !validLoopFinal(p.Outcome, p.FinalMessageObject) || p.EndingSequence <= 0 {
		return errors.New("invalid turn result")
	}
	return nil
}

func (p RuntimeTurnResult) Encode() (json.RawMessage, error) {
	return encodeLoopPayload(TypeRuntimeTurnResult, p, p.check())
}

func DecodeRuntimeTurnResult(js json.RawMessage) (RuntimeTurnResult, error) {
	var p RuntimeTurnResult
	err := decodeLoopPayload(TypeRuntimeTurnResult, js, func(m *members) error {
		p = RuntimeTurnResult{m.id("turn_id"), m.nat("bound_sequence", true), m.text("outcome"), m.text("reason"), m.nat("model_calls", true), m.nat("tool_dispatches", true), optionalObject(m, "final_message_object", readObject), m.nat("ending_sequence", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeTurnResult{}, err
	}
	return p, nil
}

// RuntimeLoopReceipt binds a command to the canonical schema-6 submission.
// EffectSequence is the actual matching runtime effect, not a predicted cursor.
type RuntimeLoopReceipt struct {
	CommandID        uuid.UUID      `json:"command_id"`
	SubmissionSHA256 Digest         `json:"submission_sha256"`
	Submission       LoopSubmission `json:"submission"`
	Outcome          string         `json:"outcome"`
	EffectSequence   int64          `json:"effect_sequence"`
}

func (p RuntimeLoopReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) || p.EffectSequence <= 0 {
		return errors.New("invalid loop receipt")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("loop receipt submission digest mismatch")
	}
	return nil
}

func (p RuntimeLoopReceipt) Encode() (json.RawMessage, error) {
	return encodeLoopPayload(TypeRuntimeLoopReceipt, p, p.check())
}

func DecodeRuntimeLoopReceipt(js json.RawMessage) (RuntimeLoopReceipt, error) {
	var p RuntimeLoopReceipt
	err := decodeLoopPayload(TypeRuntimeLoopReceipt, js, func(m *members) error {
		p = RuntimeLoopReceipt{m.id("command_id"), m.digest("submission_sha256"), graphObject(m, "submission", readLoopSubmission), m.text("outcome"), m.nat("effect_sequence", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeLoopReceipt{}, err
	}
	return p, nil
}

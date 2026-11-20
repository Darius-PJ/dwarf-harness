package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// F3-90's two version-1 types are frozen (D174).
const (
	TypeRuntimeEligibilityChanged = "runtime.eligibility_changed"
	TypeRuntimeEligibilityReceipt = "runtime.eligibility_receipt"

	// MaxEligibilityPayloadBytes bounds each complete input and compact output,
	// including ignored extensions and a receipt's embedded submission.
	MaxEligibilityPayloadBytes = 64 << 10
)

func encodeEligibilityPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxEligibilityPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxEligibilityPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeEligibilityPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxEligibilityPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxEligibilityPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

// EligibilityTurn identifies the exact owner input and native admission of a
// declared first action. The digest is not a grant or a reservation.
type EligibilityTurn struct {
	ContextID          uuid.UUID    `json:"context_id"`
	TurnID             uuid.UUID    `json:"turn_id"`
	StartCommandID     uuid.UUID    `json:"start_command_id"`
	AdmissionCommandID uuid.UUID    `json:"admission_command_id"`
	InputSHA256        Digest       `json:"input_sha256"`
	TaskBinding        *TaskBinding `json:"task_binding,omitempty"`
}

func (p EligibilityTurn) valid() bool {
	return isV7(p.ContextID) && isV7(p.TurnID) && isV7(p.StartCommandID) && isV7(p.AdmissionCommandID) && p.StartCommandID != p.AdmissionCommandID && p.InputSHA256 != (Digest{}) && validTaskBinding(p.TaskBinding)
}

// EligibilityRefusal is a definite refusal before native turn admission.
// Stage is closed to turn_admission; Code is an open token, not an error string.
type EligibilityRefusal struct {
	TurnID         uuid.UUID `json:"turn_id"`
	StartCommandID uuid.UUID `json:"start_command_id"`
	Stage          string    `json:"stage"`
	Code           string    `json:"code"`
}

func (p EligibilityRefusal) valid() bool {
	return isV7(p.TurnID) && isV7(p.StartCommandID) && p.Stage == "turn_admission" && scopedToken(p.Code)
}

// EligibilityFirstAction retains the initial declaration across explicit next
// turns. A declaration retained by hold/release is history, not permission.
type EligibilityFirstAction struct {
	ActionID      uuid.UUID           `json:"action_id"`
	InitialTurn   EligibilityTurn     `json:"initial_turn"`
	PermittedTurn EligibilityTurn     `json:"permitted_turn"`
	Refusal       *EligibilityRefusal `json:"refusal,omitempty"`
}

func (p EligibilityFirstAction) valid() bool {
	if !isV7(p.ActionID) || !p.InitialTurn.valid() || !p.PermittedTurn.valid() {
		return false
	}
	a, b := p.InitialTurn, p.PermittedTurn
	if a.ContextID != b.ContextID || (a.TaskBinding == nil) != (b.TaskBinding == nil) || a.TaskBinding != nil && *a.TaskBinding != *b.TaskBinding {
		return false
	}
	if a.TurnID == b.TurnID {
		if a.StartCommandID != b.StartCommandID || a.AdmissionCommandID != b.AdmissionCommandID || a.InputSHA256 != b.InputSHA256 {
			return false
		}
	} else if a.StartCommandID == b.StartCommandID || a.StartCommandID == b.AdmissionCommandID || a.AdmissionCommandID == b.StartCommandID || a.AdmissionCommandID == b.AdmissionCommandID || a.InputSHA256 == b.InputSHA256 {
		return false
	}
	return p.Refusal == nil || p.Refusal.valid() && p.Refusal.TurnID == b.TurnID && p.Refusal.StartCommandID == b.StartCommandID
}

// RuntimeEligibilityChanged is a full agent snapshot. Payload OperationID names
// the held operation; envelope operation_id names this transition's command.
type RuntimeEligibilityChanged struct {
	ExpectedVersion int64                   `json:"expected_version"`
	Version         int64                   `json:"version"`
	State           string                  `json:"state"`
	Reason          string                  `json:"reason"`
	OperationID     uuid.UUID               `json:"operation_id"`
	FirstAction     *EligibilityFirstAction `json:"first_action,omitempty"`
}

func (p RuntimeEligibilityChanged) check() error {
	if !nextScopedVersion(p.ExpectedVersion, p.Version) || !isV7(p.OperationID) || !scopedToken(p.Reason) {
		return errors.New("invalid eligibility version, operation or reason")
	}
	switch p.State {
	case "eligible":
		if p.Reason != "none" || p.ExpectedVersion == 0 {
			return errors.New("invalid eligible snapshot")
		}
	case "ordinary_work_ineligible":
		if p.Reason == "none" || p.ExpectedVersion == 0 && p.FirstAction != nil {
			return errors.New("invalid ordinary hold")
		}
	case "first_action_required":
		if p.Reason == "none" || p.FirstAction == nil {
			return errors.New("first action requires reason and declaration")
		}
	default:
		return errors.New("unknown eligibility state")
	}
	if p.FirstAction != nil && !p.FirstAction.valid() {
		return errors.New("invalid first-action declaration")
	}
	return nil
}

func (p RuntimeEligibilityChanged) Encode() (json.RawMessage, error) {
	return encodeEligibilityPayload(TypeRuntimeEligibilityChanged, p, p.check())
}

func DecodeRuntimeEligibilityChanged(js json.RawMessage) (RuntimeEligibilityChanged, error) {
	var p RuntimeEligibilityChanged
	err := decodeEligibilityPayload(TypeRuntimeEligibilityChanged, js, func(m *members) error {
		p = RuntimeEligibilityChanged{m.nat("expected_version", true), m.nat("version", true), m.text("state"), m.text("reason"), m.id("operation_id"), optionalObject(m, "first_action", readEligibilityFirstAction)}
		return p.check()
	})
	if err != nil {
		return RuntimeEligibilityChanged{}, err
	}
	return p, nil
}

func readEligibilityTurn(m *members) EligibilityTurn {
	return EligibilityTurn{m.id("context_id"), m.id("turn_id"), m.id("start_command_id"), m.id("admission_command_id"), m.digest("input_sha256"), optionalObject(m, "task_binding", readTaskBinding)}
}

func readEligibilityRefusal(m *members) EligibilityRefusal {
	return EligibilityRefusal{m.id("turn_id"), m.id("start_command_id"), m.text("stage"), m.text("code")}
}

func readEligibilityFirstAction(m *members) EligibilityFirstAction {
	return EligibilityFirstAction{m.id("action_id"), graphObject(m, "initial_turn", readEligibilityTurn), graphObject(m, "permitted_turn", readEligibilityTurn), optionalObject(m, "refusal", readEligibilityRefusal)}
}

// RuntimeEligibilityReceipt binds a command to schema 8 and the actual change.
// Writers emit recorded; unfamiliar outcomes remain opaque.
type RuntimeEligibilityReceipt struct {
	CommandID        uuid.UUID             `json:"command_id"`
	SubmissionSHA256 Digest                `json:"submission_sha256"`
	Submission       EligibilitySubmission `json:"submission"`
	Outcome          string                `json:"outcome"`
	EffectSequence   int64                 `json:"effect_sequence"`
}

func (p RuntimeEligibilityReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) || p.EffectSequence <= 0 {
		return errors.New("invalid eligibility receipt")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("eligibility receipt submission digest mismatch")
	}
	return nil
}

func (p RuntimeEligibilityReceipt) Encode() (json.RawMessage, error) {
	return encodeEligibilityPayload(TypeRuntimeEligibilityReceipt, p, p.check())
}

func DecodeRuntimeEligibilityReceipt(js json.RawMessage) (RuntimeEligibilityReceipt, error) {
	var p RuntimeEligibilityReceipt
	err := decodeEligibilityPayload(TypeRuntimeEligibilityReceipt, js, func(m *members) error {
		p = RuntimeEligibilityReceipt{m.id("command_id"), m.digest("submission_sha256"), graphObject(m, "submission", readEligibilitySubmission), m.text("outcome"), m.nat("effect_sequence", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeEligibilityReceipt{}, err
	}
	return p, nil
}

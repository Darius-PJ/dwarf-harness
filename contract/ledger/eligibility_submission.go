package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"uuid"
)

// EligibilitySubmission is the canonical schema-8 preimage, frozen (D174).
// Arguments is the matching concrete value below, never arbitrary JSON.
// ExpectedOperationID is absent for a new hold and required for an existing one.
type EligibilitySubmission struct {
	Schema              int64     `json:"schema"`
	Actor               string    `json:"actor"`
	Origin              Origin    `json:"origin"`
	Operation           string    `json:"operation"`
	ExpectedVersion     int64     `json:"expected_version"`
	ExpectedOperationID uuid.UUID `json:"expected_operation_id,omitzero"`
	Arguments           any       `json:"arguments"`
}

// EligibilityHoldArguments opens an ordinary hold or suspends a first action.
type EligibilityHoldArguments struct {
	Reason string `json:"reason"`
}

// EligibilityRequireFirstArguments declares an exact first action without work.
type EligibilityRequireFirstArguments struct {
	Reason   string          `json:"reason"`
	ActionID uuid.UUID       `json:"action_id"`
	Turn     EligibilityTurn `json:"turn"`
}

// EligibilityFirstNextArguments explicitly advances the same first action.
type EligibilityFirstNextArguments struct {
	ActionID uuid.UUID       `json:"action_id"`
	Turn     EligibilityTurn `json:"turn"`
}

// EligibilityReleaseArguments carries no approval, verdict or reason.
type EligibilityReleaseArguments struct{}

// EligibilityFirstRefusedArguments records a definite pre-admission refusal.
type EligibilityFirstRefusedArguments struct {
	ActionID       uuid.UUID `json:"action_id"`
	TurnID         uuid.UUID `json:"turn_id"`
	StartCommandID uuid.UUID `json:"start_command_id"`
	Stage          string    `json:"stage"`
	Code           string    `json:"code"`
}

func (p EligibilitySubmission) check() error {
	if p.Schema != 8 || !validOrigin(p.Origin) || p.ExpectedVersion < 0 || p.ExpectedVersion == math.MaxInt64 {
		return errors.New("invalid eligibility schema, origin or expectation")
	}
	existing := p.ExpectedOperationID != (uuid.UUID{})
	if existing && (!isV7(p.ExpectedOperationID) || p.ExpectedVersion == 0) {
		return errors.New("invalid eligibility operation expectation")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case EligibilityHoldArguments:
		valid = p.Operation == "eligibility.hold" && p.Actor == "owner" && scopedToken(a.Reason) && a.Reason != "none"
	case EligibilityRequireFirstArguments:
		valid = p.Operation == "eligibility.require_first" && p.Actor == "owner" && scopedToken(a.Reason) && a.Reason != "none" && isV7(a.ActionID) && a.Turn.valid()
	case EligibilityFirstNextArguments:
		valid = p.Operation == "eligibility.first_next" && p.Actor == "owner" && existing && isV7(a.ActionID) && a.Turn.valid()
	case EligibilityReleaseArguments:
		valid = p.Operation == "eligibility.release" && p.Actor == "owner" && existing
	case EligibilityFirstRefusedArguments:
		valid = p.Operation == "eligibility.first_refused" && p.Actor == "core" && existing && isV7(a.ActionID) && (EligibilityRefusal{a.TurnID, a.StartCommandID, a.Stage, a.Code}).valid()
	}
	if !valid {
		return errors.New("eligibility operation, principal, expectation or argument mismatch")
	}
	return nil
}

func (p EligibilitySubmission) Encode() (json.RawMessage, error) {
	return encodeEligibilityPayload("schema-8 submission", p, p.check())
}

func DecodeEligibilitySubmission(js json.RawMessage) (EligibilitySubmission, error) {
	var p EligibilitySubmission
	err := decodeEligibilityPayload("schema-8 submission", js, func(m *members) error { p = readEligibilitySubmission(m); return p.check() })
	if err != nil {
		return EligibilitySubmission{}, err
	}
	return p, nil
}

func readEligibilitySubmission(m *members) EligibilitySubmission {
	p := EligibilitySubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation"), ExpectedVersion: m.nat("expected_version", true)}
	p.ExpectedOperationID, _ = m.optionalID("expected_operation_id")
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		var allowed []string
		var a any
		switch p.Operation {
		case "eligibility.hold":
			allowed = []string{"reason"}
			a = EligibilityHoldArguments{b.text("reason")}
		case "eligibility.require_first":
			allowed = []string{"reason", "action_id", "turn"}
			a = EligibilityRequireFirstArguments{b.text("reason"), b.id("action_id"), graphObject(b, "turn", readEligibilityTurn)}
		case "eligibility.first_next":
			allowed = []string{"action_id", "turn"}
			a = EligibilityFirstNextArguments{b.id("action_id"), graphObject(b, "turn", readEligibilityTurn)}
		case "eligibility.release":
			a = EligibilityReleaseArguments{}
		case "eligibility.first_refused":
			allowed = []string{"action_id", "turn_id", "start_command_id", "stage", "code"}
			a = EligibilityFirstRefusedArguments{b.id("action_id"), b.id("turn_id"), b.id("start_command_id"), b.text("stage"), b.text("code")}
		default:
			b.err = errors.New("unknown eligibility operation")
		}
		for _, key := range []string{"reason", "action_id", "turn", "turn_id", "start_command_id", "stage", "code"} {
			if !slices.Contains(allowed, key) {
				forbidScoped(b, key)
			}
		}
		return a
	})
	return p
}

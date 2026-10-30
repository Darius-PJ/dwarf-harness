package ledger

import (
	"encoding/json"
	"errors"
	"slices"
	"uuid"
)

// LoopSubmission is the canonical schema-6 encoding, proposed for freeze (D125).
// Arguments is the matching concrete value below, never arbitrary JSON.
// ExpectedVersion describes binding/result identity, not the private-head CAS.
type LoopSubmission struct {
	Schema          int64     `json:"schema"`
	Actor           string    `json:"actor"`
	Origin          Origin    `json:"origin"`
	TurnID          uuid.UUID `json:"turn_id"`
	Operation       string    `json:"operation"`
	ExpectedVersion int64     `json:"expected_version"`
	Arguments       any       `json:"arguments"`
}

// LoopBindArguments names the separately committed F3-11 admission command.
type LoopBindArguments struct {
	TurnID             uuid.UUID `json:"turn_id"`
	AdmissionCommandID uuid.UUID `json:"admission_command_id"`
	InputObject        ObjectRef `json:"input_object"`
	PolicyObject       ObjectRef `json:"policy_object"`
	RenderObject       ObjectRef `json:"render_object"`
}

// LoopToolBeginArguments has exactly the runtime.tool_started member order.
type LoopToolBeginArguments RuntimeToolStarted

// LoopToolResultArguments has exactly the runtime.tool_result member order.
type LoopToolResultArguments RuntimeToolResult

// LoopEndArguments names the host's actual ending command. The independently
// derived transcript revision is not a caller-supplied submission member.
type LoopEndArguments struct {
	TurnID             uuid.UUID  `json:"turn_id"`
	BoundSequence      int64      `json:"bound_sequence"`
	Outcome            string     `json:"outcome"`
	Reason             string     `json:"reason"`
	ModelCalls         int64      `json:"model_calls"`
	ToolDispatches     int64      `json:"tool_dispatches"`
	FinalMessageObject *ObjectRef `json:"final_message_object,omitempty"`
	EndingCommandID    uuid.UUID  `json:"ending_command_id"`
}

func (p LoopSubmission) check() error {
	if p.Schema != 6 || !validOrigin(p.Origin) || !isV7(p.TurnID) {
		return errors.New("invalid loop submission schema or binding")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case LoopBindArguments:
		valid = p.Operation == "loop.bind" && p.Actor == "owner" && p.ExpectedVersion == 0 && a.TurnID == p.TurnID && isV7(a.AdmissionCommandID) && validObject(a.InputObject) && validObject(a.PolicyObject) && validObject(a.RenderObject)
	case LoopToolBeginArguments:
		valid = p.Operation == "tool.begin" && p.Actor == "agent" && p.ExpectedVersion == 0 && a.ParentTurnID == p.TurnID && RuntimeToolStarted(a).check() == nil
	case LoopToolResultArguments:
		valid = p.Operation == "tool.result" && p.Actor == "core" && p.ExpectedVersion == 1 && RuntimeToolResult(a).check() == nil
	case LoopEndArguments:
		valid = p.Operation == "loop.end" && p.Actor == "core" && p.ExpectedVersion == 1 && a.TurnID == p.TurnID && a.BoundSequence > 0 && scopedToken(a.Outcome) && scopedToken(a.Reason) && a.ModelCalls >= 0 && a.ToolDispatches >= 0 && validLoopFinal(a.Outcome, a.FinalMessageObject) && isV7(a.EndingCommandID)
	}
	if !valid {
		return errors.New("loop operation, principal, expectation or argument mismatch")
	}
	return nil
}

func (p LoopSubmission) Encode() (json.RawMessage, error) {
	return encodeLoopPayload("schema-6 submission", p, p.check())
}

func DecodeLoopSubmission(js json.RawMessage) (LoopSubmission, error) {
	var p LoopSubmission
	err := decodeLoopPayload("schema-6 submission", js, func(m *members) error { p = readLoopSubmission(m); return p.check() })
	if err != nil {
		return LoopSubmission{}, err
	}
	return p, nil
}

func readLoopSubmission(m *members) LoopSubmission {
	p := LoopSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), TurnID: m.id("turn_id"), Operation: m.text("operation"), ExpectedVersion: m.nat("expected_version", true)}
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		var allowed []string
		var a any
		switch p.Operation {
		case "loop.bind":
			allowed = []string{"turn_id", "admission_command_id", "input_object", "policy_object", "render_object"}
			a = LoopBindArguments{b.id("turn_id"), b.id("admission_command_id"), graphObject(b, "input_object", readObject), graphObject(b, "policy_object", readObject), graphObject(b, "render_object", readObject)}
		case "tool.begin":
			allowed = []string{"tool_id", "parent_turn_id", "attempt_id", "provider_call_id", "tool_name", "tool_schema", "request_object", "request_sha256", "operation", "target", "grant_id", "effect_command_id"}
			a = LoopToolBeginArguments(readLoopToolStarted(b))
		case "tool.result":
			allowed = []string{"tool_id", "started_sequence", "outcome", "result_object", "effect_receipt_sequence"}
			a = LoopToolResultArguments(readLoopToolResult(b))
		case "loop.end":
			allowed = []string{"turn_id", "bound_sequence", "outcome", "reason", "model_calls", "tool_dispatches", "final_message_object", "ending_command_id"}
			a = LoopEndArguments{b.id("turn_id"), b.nat("bound_sequence", true), b.text("outcome"), b.text("reason"), b.nat("model_calls", true), b.nat("tool_dispatches", true), optionalObject(b, "final_message_object", readObject), b.id("ending_command_id")}
		default:
			b.err = errors.New("unknown loop operation")
		}
		for _, key := range []string{"turn_id", "admission_command_id", "input_object", "policy_object", "render_object", "tool_id", "parent_turn_id", "attempt_id", "provider_call_id", "tool_name", "tool_schema", "request_object", "request_sha256", "operation", "target", "grant_id", "effect_command_id", "started_sequence", "outcome", "result_object", "effect_receipt_sequence", "bound_sequence", "reason", "model_calls", "tool_dispatches", "final_message_object", "ending_command_id", "admission_sequence", "ending_sequence", "changes", "revision"} {
			if !slices.Contains(allowed, key) {
				forbidScoped(b, key)
			}
		}
		return a
	})
	return p
}

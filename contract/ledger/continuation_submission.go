package ledger

import (
	"encoding/json"
	"errors"
	"uuid"
)

// ContinuationSubmission is the canonical schema-7 preimage, proposed for freeze
// (D125). All five closed operations use the same concrete snapshot arguments.
type ContinuationSubmission struct {
	Schema          int64                 `json:"schema"`
	Actor           string                `json:"actor"`
	Origin          Origin                `json:"origin"`
	TurnID          uuid.UUID             `json:"turn_id"`
	Operation       string                `json:"operation"`
	ExpectedVersion int64                 `json:"expected_version"`
	ExpectedState   string                `json:"expected_state"`
	Arguments       ContinuationArguments `json:"arguments"`
}

// ContinuationArguments contains the snapshot without the turn and CAS fields.
// Pending and NoticeCursors must be non-nil and strictly sorted by their keys.
type ContinuationArguments struct {
	BoundSequence        int64                      `json:"bound_sequence"`
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

func knownContinuationState(s string) bool {
	switch s {
	case "ready", "model_pending", "tool_pending", "waiting_resource", "finishing", "stopping", "reconciling", "terminal":
		return true
	}
	return false
}

func (a ContinuationArguments) check() error {
	if a.BoundSequence <= 0 || !scopedToken(a.State) || a.State == "absent" || !isV7(a.OperationID) || a.Generation <= 0 || !scopedToken(a.WaitReason) || a.ModelCalls < 0 || a.ToolDispatches < 0 || a.DeadlineUTCNS < 0 || a.NativeEndingSequence < 0 || a.Pending == nil || a.NoticeCursors == nil {
		return errors.New("invalid continuation snapshot")
	}
	if a.ResumeState != "" && !scopedToken(a.ResumeState) || a.StopReason != "" && (!scopedToken(a.StopReason) || a.StopReason == "natural_stop") {
		return errors.New("invalid continuation optional token")
	}
	if a.State == "waiting_resource" {
		if a.ResumeState == "" || a.WaitReason == "none" {
			return errors.New("resource wait requires resume state and reason")
		}
		if knownContinuationState(a.ResumeState) && a.ResumeState != "model_pending" && a.ResumeState != "tool_pending" && a.ResumeState != "finishing" && a.ResumeState != "reconciling" || a.ResumeState == "absent" {
			return errors.New("invalid resource resume state")
		}
	} else if knownContinuationState(a.State) && a.ResumeState != "" {
		return errors.New("resume state outside resource wait")
	}
	if a.State == "stopping" && a.StopReason == "" || a.State == "terminal" && a.NativeEndingSequence == 0 {
		return errors.New("missing stop reason or terminal ending")
	}
	if a.CancelCommandID != (uuid.UUID{}) && !isV7(a.CancelCommandID) || (a.StopReason == "cancelled") != (a.CancelCommandID != (uuid.UUID{})) {
		return errors.New("cancel command required exactly for cancelled")
	}
	if a.StopReason != "" && (a.State == "ready" || a.State == "model_pending" || a.State == "tool_pending") {
		return errors.New("stopped continuation cannot execute")
	}
	if a.FinalMessageObject != nil {
		if !validObject(*a.FinalMessageObject) || a.StopReason != "" || a.State == "ready" || a.State == "model_pending" || a.State == "tool_pending" || a.State == "stopping" {
			return errors.New("invalid natural final selection")
		}
	}
	for i, p := range a.Pending {
		if !scopedToken(p.Kind) || !isV7(p.ID) || p.CommandID != (uuid.UUID{}) && !isV7(p.CommandID) || p.ExpectedVersion < 0 || p.SourceSequence < 0 || i > 0 && compareContinuationPending(a.Pending[i-1], p) >= 0 {
			return errors.New("invalid, duplicate or unsorted pending child")
		}
		switch p.Kind {
		case "paid_attempt", "local_work", "scratch_observation", "merge_back":
			if p.CommandID == (uuid.UUID{}) {
				return errors.New("native child requires command_id")
			}
		}
	}
	for i, c := range a.NoticeCursors {
		if !isV7(c.WorkspaceID) || !isV7(c.ScratchID) || c.AfterSequence < 0 || i > 0 && compareContinuationCursor(a.NoticeCursors[i-1], c) >= 0 {
			return errors.New("invalid, duplicate or unsorted notice cursor")
		}
	}
	return nil
}

func (p ContinuationSubmission) check() error {
	if p.Schema != 7 || !validOrigin(p.Origin) || !isV7(p.TurnID) || !continuationExpectation(p.ExpectedVersion, p.ExpectedState) || p.Actor != "core" && (p.Actor != "owner" || p.Operation != "owner.stop") {
		return errors.New("invalid continuation schema, principal or expectation")
	}
	a := p.Arguments
	switch p.Operation {
	case "owner.initialize":
		if p.ExpectedVersion != 0 || a.Generation != 1 || knownContinuationState(a.State) && a.State != "ready" && a.State != "reconciling" || a.StopReason != "" {
			return errors.New("invalid owner initialization")
		}
	case "owner.advance", "owner.reconcile", "owner.stop", "owner.close":
		if p.ExpectedVersion == 0 {
			return errors.New("continuation operation requires existing owner")
		}
		if p.Operation == "owner.stop" && (knownContinuationState(a.State) && a.State != "stopping" || a.StopReason == "" || a.NativeEndingSequence != 0) || p.Operation == "owner.close" && knownContinuationState(a.State) && a.State != "terminal" {
			return errors.New("invalid stop or close snapshot")
		}
	default:
		return errors.New("unknown continuation operation")
	}
	return a.check()
}

func (p ContinuationSubmission) Encode() (json.RawMessage, error) {
	return encodeContinuationPayload("schema-7 submission", p, p.check())
}

func DecodeContinuationSubmission(js json.RawMessage) (ContinuationSubmission, error) {
	var p ContinuationSubmission
	err := decodeContinuationPayload("schema-7 submission", js, func(m *members) error { p = readContinuationSubmission(m); return p.check() })
	if err != nil {
		return ContinuationSubmission{}, err
	}
	return p, nil
}

func readContinuationSubmission(m *members) ContinuationSubmission {
	p := ContinuationSubmission{m.nat("schema", true), m.text("actor"), graphObject(m, "origin", readOrigin), m.id("turn_id"), m.text("operation"), m.nat("expected_version", true), m.text("expected_state"), graphObject(m, "arguments", func(b *members) ContinuationArguments {
		forbidScoped(b, "turn_id", "expected_version", "version", "expected_state")
		return readContinuationArguments(b)
	})}
	return p
}

func continuationOptionalSequence(m *members, key string) int64 {
	if n := optionalScopedNat(m, key); n != nil {
		if *n == 0 {
			m.err = errors.New("present continuation source sequence must be positive")
		}
		return *n
	}
	return 0
}

func readContinuationArguments(m *members) ContinuationArguments {
	a := ContinuationArguments{BoundSequence: m.nat("bound_sequence", true), State: m.text("state"), OperationID: m.id("operation_id"), Generation: m.nat("generation", true), WaitReason: m.text("wait_reason"), ModelCalls: m.nat("model_calls", true), ToolDispatches: m.nat("tool_dispatches", true), DeadlineUTCNS: m.nat("deadline_utc_ns", true)}
	a.ResumeState, _ = m.optionalText("resume_state")
	a.StopReason, _ = m.optionalText("stop_reason")
	a.CancelCommandID, _ = m.optionalID("cancel_command_id")
	a.FinalMessageObject = optionalObject(m, "final_message_object", readObject)
	a.NativeEndingSequence = continuationOptionalSequence(m, "native_ending_sequence")
	a.Pending = readScopedObjects(m, "pending", func(b *members) ContinuationPending {
		p := ContinuationPending{Kind: b.text("kind"), ID: b.id("id"), ExpectedVersion: b.nat("expected_version", true)}
		p.CommandID, _ = b.optionalID("command_id")
		p.SourceSequence = continuationOptionalSequence(b, "source_sequence")
		return p
	})
	a.NoticeCursors = readScopedObjects(m, "notice_cursors", func(b *members) ContinuationNoticeCursor {
		return ContinuationNoticeCursor{b.id("workspace_id"), b.id("scratch_id"), b.nat("after_sequence", true)}
	})
	return a
}

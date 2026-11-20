package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"uuid"
)

// ServiceSubmission is the canonical schema-9 preimage, frozen (D175).
// Expectations are pointers because inapplicable members are omitted,
// while an applicable zero names absence. Actor is always host-fixed core.
type ServiceSubmission struct {
	Schema                int64     `json:"schema"`
	Actor                 string    `json:"actor"`
	Operation             string    `json:"operation"`
	CauseID               uuid.UUID `json:"cause_id,omitzero"`
	AgentID               uuid.UUID `json:"agent_id,omitzero"`
	ExpectedCauseVersion  *int64    `json:"expected_cause_version,omitempty"`
	ExpectedMemberVersion *int64    `json:"expected_member_version,omitempty"`
	ExpectedGeneration    *int64    `json:"expected_generation,omitempty"`
	Arguments             any       `json:"arguments"`
}

// ServiceRecordArguments selects an observation without changing service state.
type ServiceRecordArguments struct {
	ObservationID           uuid.UUID         `json:"observation_id"`
	CauseKey                Digest            `json:"cause_key,omitzero"`
	Cause                   *ServiceCauseSpec `json:"cause,omitempty"`
	Source                  ServiceSource     `json:"source"`
	CompletionPermitsObject *ObjectRef        `json:"completion_permits_object,omitempty"`
}

// ServiceStateArguments carries one cause's state transition inputs. Its fields
// are admitted by operation; a caller cannot choose the resulting phase or edge.
type ServiceStateArguments struct {
	CauseKey    Digest           `json:"cause_key"`
	Cause       ServiceCauseSpec `json:"cause"`
	Source      ServiceSource    `json:"source"`
	FirstSource *ServiceSource   `json:"first_source,omitempty"`
	DueUTCNS    *int64           `json:"due_utc_ns,omitempty"`
	OutageID    uuid.UUID        `json:"outage_id,omitzero"`
	NoticeID    uuid.UUID        `json:"notice_id,omitzero"`
}

func (p ServiceSubmission) check() error {
	if p.Schema != 9 || p.Actor != "core" || p.CauseID != (uuid.UUID{}) && !isV7(p.CauseID) || p.AgentID != (uuid.UUID{}) && !isV7(p.AgentID) {
		return errors.New("invalid service schema, actor or identity")
	}
	for _, n := range []*int64{p.ExpectedCauseVersion, p.ExpectedMemberVersion, p.ExpectedGeneration} {
		if n != nil && *n < 0 {
			return errors.New("negative service expectation")
		}
	}
	if p.Operation == "service.record" {
		a, ok := p.Arguments.(ServiceRecordArguments)
		if !ok || p.ExpectedCauseVersion != nil || p.ExpectedMemberVersion != nil || p.ExpectedGeneration != nil {
			return errors.New("service record arguments/expectations")
		}
		return (RuntimeServiceObserved{a.ObservationID, p.AgentID, p.CauseID, a.CauseKey, a.Cause, a.Source, a.CompletionPermitsObject}).check()
	}
	if !slices.Contains([]string{"service.observe", "service.expire", "service.clear", "service.complete", "service.withdraw"}, p.Operation) || !isV7(p.CauseID) || p.ExpectedCauseVersion == nil {
		return errors.New("unknown service operation or missing cause expectation")
	}
	if p.Operation == "service.clear" {
		if p.AgentID != (uuid.UUID{}) || p.ExpectedMemberVersion != nil || p.ExpectedGeneration != nil || *p.ExpectedCauseVersion == 0 || *p.ExpectedCauseVersion == math.MaxInt64 {
			return errors.New("service clear expectation presence")
		}
	} else if !isV7(p.AgentID) || p.ExpectedMemberVersion == nil || p.ExpectedGeneration == nil || *p.ExpectedMemberVersion == math.MaxInt64 || (*p.ExpectedMemberVersion == 0) != (*p.ExpectedGeneration == 0) || p.Operation != "service.observe" && (*p.ExpectedMemberVersion == 0 || *p.ExpectedCauseVersion == 0) {
		return errors.New("service member expectation presence")
	}
	a, ok := p.Arguments.(ServiceStateArguments)
	if !ok {
		return errors.New("service state arguments are not concrete")
	}
	key, err := a.Cause.Key()
	if err != nil {
		return err
	}
	if key != a.CauseKey {
		return errors.New("service state cause key mismatch")
	}
	if err := a.Source.check(true); err != nil {
		return err
	}
	if a.Source.Origin != nil && p.AgentID != (uuid.UUID{}) && a.Source.Origin.AgentID != p.AgentID {
		return errors.New("foreign service submission source")
	}
	if a.Source.Completion != nil {
		permit := a.Source.Completion.Permit
		if permit.CauseID != p.CauseID || permit.CauseVersion != *p.ExpectedCauseVersion || p.ExpectedGeneration != nil && permit.MemberGeneration != *p.ExpectedGeneration {
			return errors.New("service completion expectation mismatch")
		}
	}
	for _, id := range []uuid.UUID{a.OutageID, a.NoticeID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("invalid service incident identity")
		}
	}
	if a.NoticeID != (uuid.UUID{}) && a.OutageID == (uuid.UUID{}) {
		return errors.New("notice without service incident")
	}
	if p.Operation == "service.observe" {
		if a.FirstSource == nil || !serviceDue(*a.FirstSource, a.DueUTCNS) || a.DueUTCNS != nil && a.OutageID != (uuid.UUID{}) || a.DueUTCNS == nil && !isV7(a.OutageID) {
			return errors.New("service observe generation shape")
		}
		if err := a.FirstSource.check(false); err != nil {
			return err
		}
		if slices.Contains([]string{"cause_clear", "successful_call", "grace_expiry", "demand_withdrawn"}, a.FirstSource.Kind) {
			return errors.New("first source is not a refusal")
		}
		if a.FirstSource.Origin != nil && a.FirstSource.Origin.AgentID != p.AgentID || a.FirstSource.ObservationID != a.Source.ObservationID {
			return errors.New("service observe first source binding")
		}
	} else if a.FirstSource != nil || a.DueUTCNS != nil {
		return errors.New("inapplicable service first source or due")
	}
	switch p.Operation {
	case "service.clear", "service.withdraw":
		if a.OutageID != (uuid.UUID{}) || a.NoticeID != (uuid.UUID{}) {
			return errors.New("clear/withdraw cannot name an incident edge")
		}
	case "service.expire", "service.complete":
		if !isV7(a.OutageID) {
			return errors.New("service loss/restoration lacks incident")
		}
	}
	// Unknown source kinds remain unsupported. A known kind must belong to
	// this operation, rather than being reinterpreted as another transition.
	if slices.Contains([]string{"paid_call", "money_refusal", "integrity_refusal", "process_refusal", "cause_clear", "successful_call", "grace_expiry", "demand_withdrawn"}, a.Source.Kind) {
		valid := false
		switch p.Operation {
		case "service.observe":
			valid = serviceFailureKind(a.Source.Kind)
		case "service.expire":
			valid = a.Source.Kind == "grace_expiry"
		case "service.clear":
			valid = a.Source.Kind == "cause_clear" || a.Source.Kind == "successful_call"
		case "service.complete":
			valid = a.Source.Kind == "successful_call"
		case "service.withdraw":
			valid = a.Source.Kind == "demand_withdrawn"
		}
		if !valid {
			return errors.New("service operation/source mismatch")
		}
	}
	return nil
}

func (p ServiceSubmission) Encode() (json.RawMessage, error) {
	return encodeServicePayload("schema-9 submission", p, p.check())
}
func DecodeServiceSubmission(js json.RawMessage) (ServiceSubmission, error) {
	var p ServiceSubmission
	err := decodeServicePayload("schema-9 submission", js, func(m *members) error { p = readServiceSubmission(m); return p.check() })
	if err != nil {
		return ServiceSubmission{}, err
	}
	return p, nil
}
func readServiceSubmission(m *members) ServiceSubmission {
	p := ServiceSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Operation: m.text("operation"), ExpectedCauseVersion: optionalScopedNat(m, "expected_cause_version"), ExpectedMemberVersion: optionalScopedNat(m, "expected_member_version"), ExpectedGeneration: optionalScopedNat(m, "expected_generation")}
	p.CauseID, _ = m.optionalID("cause_id")
	p.AgentID, _ = m.optionalID("agent_id")
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		if p.Operation == "service.record" {
			forbidScoped(b, "first_source", "due_utc_ns", "outage_id", "notice_id")
			a := ServiceRecordArguments{ObservationID: b.id("observation_id"), Cause: optionalObject(b, "cause", readServiceCause), Source: graphObject(b, "source", readServiceSource), CompletionPermitsObject: optionalObject(b, "completion_permits_object", readObject)}
			a.CauseKey, _ = b.optionalDigest("cause_key")
			return a
		}
		forbidScoped(b, "observation_id", "completion_permits_object")
		a := ServiceStateArguments{CauseKey: b.digest("cause_key"), Cause: graphObject(b, "cause", readServiceCause), Source: graphObject(b, "source", readServiceSource), FirstSource: optionalObject(b, "first_source", readServiceSource), DueUTCNS: optionalScopedNat(b, "due_utc_ns")}
		a.OutageID, _ = b.optionalID("outage_id")
		a.NoticeID, _ = b.optionalID("notice_id")
		return a
	})
	return p
}

// ServiceEffect names the actual effect envelope; order is commit order.
type ServiceEffect struct {
	Type     string `json:"type"`
	Sequence int64  `json:"sequence"`
}

// RuntimeServiceReceipt binds a command to schema 9 and complete source coverage.
// Writers emit recorded. An unfamiliar outcome is retained as unsupported.
type RuntimeServiceReceipt struct {
	CommandID        uuid.UUID         `json:"command_id"`
	SubmissionSHA256 Digest            `json:"submission_sha256"`
	Submission       ServiceSubmission `json:"submission"`
	Outcome          string            `json:"outcome"`
	Effects          []ServiceEffect   `json:"effects"`
	SourceThrough    int64             `json:"source_through"`
}

func (p RuntimeServiceReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) || p.SourceThrough < 0 || len(p.Effects) == 0 {
		return errors.New("invalid service receipt")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("service receipt submission digest mismatch")
	}
	lastRank := -1
	member := false
	for i, effect := range p.Effects {
		if effect.Sequence <= 0 || i > 0 && (p.Effects[i-1].Sequence == math.MaxInt64 || effect.Sequence != p.Effects[i-1].Sequence+1) {
			return errors.New("service effect sequence order")
		}
		if p.Submission.Operation == "service.record" {
			if len(p.Effects) != 1 || effect.Type != TypeRuntimeServiceObserved || p.Submission.Arguments.(ServiceRecordArguments).ObservationID != p.CommandID {
				return errors.New("service observation receipt shape")
			}
			continue
		}
		rank := slices.Index([]string{TypeRuntimeServiceCauseChanged, TypeRuntimeServiceAgentChanged, TypeRuntimeServiceOutageNotice}, effect.Type)
		if rank < 0 || rank <= lastRank {
			return errors.New("service state effect order")
		}
		lastRank = rank
		member = member || effect.Type == TypeRuntimeServiceAgentChanged
		if effect.Type == TypeRuntimeServiceOutageNotice && !member {
			return errors.New("service notice without member")
		}
	}
	if p.Submission.Operation == "service.clear" && (len(p.Effects) != 1 || p.Effects[0].Type != TypeRuntimeServiceCauseChanged) || p.Submission.Operation != "service.clear" && p.Submission.Operation != "service.record" && !member {
		return errors.New("service operation/effect mismatch")
	}
	if p.SourceThrough >= p.Effects[0].Sequence-1 {
		return errors.New("service coverage beyond preceding prefix")
	}
	return nil
}

func (p RuntimeServiceReceipt) Encode() (json.RawMessage, error) {
	return encodeServicePayload(TypeRuntimeServiceReceipt, p, p.check())
}
func DecodeRuntimeServiceReceipt(js json.RawMessage) (RuntimeServiceReceipt, error) {
	var p RuntimeServiceReceipt
	err := decodeServicePayload(TypeRuntimeServiceReceipt, js, func(m *members) error {
		p = RuntimeServiceReceipt{m.id("command_id"), m.digest("submission_sha256"), graphObject(m, "submission", readServiceSubmission), m.text("outcome"), readScopedObjects(m, "effects", func(b *members) ServiceEffect { return ServiceEffect{b.text("type"), b.nat("sequence", true)} }), m.nat("source_through", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeServiceReceipt{}, err
	}
	return p, nil
}

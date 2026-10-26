package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"uuid"
)

// PaidSubmission is schema 4's canonical producer encoding, frozen
// (D148). Arguments is the matching concrete value, never arbitrary JSON.
type PaidSubmission struct {
	Schema    int64     `json:"schema"`
	Actor     string    `json:"actor"`
	Origin    Origin    `json:"origin"`
	Operation string    `json:"operation"`
	GrantID   uuid.UUID `json:"grant_id,omitzero"`
	Arguments any       `json:"arguments"`
}

type PaidRouteArguments struct {
	RouteID           uuid.UUID `json:"route_id"`
	ExpectedVersion   int64     `json:"expected_version"`
	Enabled           bool      `json:"enabled"`
	CredentialScopeID uuid.UUID `json:"credential_scope_id"`
	ProfileSHA256     Digest    `json:"profile_sha256"`
}

type PaidCredentialBindArguments struct {
	ScopeID               uuid.UUID `json:"scope_id"`
	ExpectedVersion       int64     `json:"expected_version"`
	Provider              string    `json:"provider"`
	CredentialVersion     uuid.UUID `json:"credential_version"`
	SourceKind            string    `json:"source_kind"`
	SourceLocator         string    `json:"source_locator"`
	RevokePreviousVersion uuid.UUID `json:"revoke_previous_version,omitzero"`
}

type PaidCredentialRevokeArguments struct {
	ScopeID           uuid.UUID `json:"scope_id"`
	ExpectedVersion   int64     `json:"expected_version"`
	Provider          string    `json:"provider"`
	CredentialVersion uuid.UUID `json:"credential_version"`
}

type PaidSpendArguments struct {
	Scope           PaidScope  `json:"scope"`
	ExpectedVersion int64      `json:"expected_version"`
	Terms           SpendTerms `json:"terms"`
	ParentGrantID   uuid.UUID  `json:"parent_grant_id,omitzero"`
}

type PaidAdmitArguments struct {
	AttemptID     uuid.UUID        `json:"attempt_id"`
	GrantID       uuid.UUID        `json:"grant_id"`
	ParentTurnID  uuid.UUID        `json:"parent_turn_id"`
	TaskBinding   *TaskBinding     `json:"task_binding,omitempty"`
	RouteID       uuid.UUID        `json:"route_id"`
	RouteVersion  int64            `json:"route_version"`
	ProfileSHA256 Digest           `json:"profile_sha256"`
	Request       SealedDescriptor `json:"request"`
	Quote         CostBound        `json:"quote"`
	InputObject   ObjectRef        `json:"input_object"`
}

// PaidAttemptArguments serves dispatch and provably-unsent release only.
type PaidAttemptArguments struct {
	AttemptID       uuid.UUID `json:"attempt_id"`
	ExpectedVersion int64     `json:"expected_version"`
}

type PaidObserveArguments struct {
	AttemptID       uuid.UUID   `json:"attempt_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Kind            string      `json:"kind"`
	EvidenceSHA256  Digest      `json:"evidence_sha256"`
	EvidenceObject  ObjectRef   `json:"evidence_object"`
	MessageObjects  []ObjectRef `json:"message_objects,omitempty"`
}

type PaidCorrectArguments struct {
	AttemptID              uuid.UUID   `json:"attempt_id"`
	ExpectedVersion        int64       `json:"expected_version"`
	PreviousResultSequence int64       `json:"previous_result_sequence"`
	PreviousEvidenceSHA256 Digest      `json:"previous_evidence_sha256"`
	EvidenceSHA256         Digest      `json:"evidence_sha256"`
	EvidenceObject         ObjectRef   `json:"evidence_object"`
	MessageObjects         []ObjectRef `json:"message_objects,omitempty"`
}

type PaidOverrunArguments struct {
	AttemptID            uuid.UUID `json:"attempt_id"`
	ExpectedVersion      int64     `json:"expected_version"`
	ResultSequence       int64     `json:"result_sequence"`
	EvidenceSHA256       Digest    `json:"evidence_sha256"`
	RetiredProfileSHA256 Digest    `json:"retired_profile_sha256"`
}

func paidAttemptOperation(op string) bool {
	return slices.Contains([]string{"paid.admit", "paid.dispatch", "paid.observe", "paid.release", "paid.correct", "paid.overrun.acknowledge"}, op)
}
func paidExpectation(v int64) bool { return v >= 0 && v < math.MaxInt64 }
func paidObservation(id uuid.UUID, expected int64, digest Digest, object ObjectRef, messages []ObjectRef) bool {
	return isV7(id) && expected >= 2 && paidExpectation(expected) && validPaidObject(object) && digest == object.SHA256 && validPaidMessages(messages)
}
func (p PaidSubmission) check() error {
	if p.Schema != 4 || !validOrigin(p.Origin) {
		return errors.New("invalid paid submission schema or origin")
	}
	actor, grant := "owner", false
	switch p.Operation {
	case "paid.admit", "paid.spend.delegate":
		actor, grant = "agent", true
	case "paid.dispatch", "paid.observe", "paid.release", "paid.correct":
		actor, grant = "core", true
	}
	if p.Actor != actor || grant && !isV7(p.GrantID) || !grant && p.GrantID != (uuid.UUID{}) {
		return errors.New("invalid paid submission principal")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case PaidRouteArguments:
		valid = p.Operation == "paid.route.configure" && isV7(a.RouteID) && paidExpectation(a.ExpectedVersion) && isV7(a.CredentialScopeID) && a.ProfileSHA256 != (Digest{})
	case PaidCredentialBindArguments:
		valid = p.Operation == "paid.credential.bind" && (RuntimeCredentialChanged{ScopeID: a.ScopeID, Provider: a.Provider, ExpectedVersion: a.ExpectedVersion, Version: a.ExpectedVersion + 1, Action: "bind", CredentialVersion: a.CredentialVersion, SourceKind: a.SourceKind, SourceLocator: a.SourceLocator, RevokePreviousVersion: a.RevokePreviousVersion}).check() == nil
	case PaidCredentialRevokeArguments:
		valid = p.Operation == "paid.credential.revoke" && (RuntimeCredentialChanged{ScopeID: a.ScopeID, Provider: a.Provider, ExpectedVersion: a.ExpectedVersion, Version: a.ExpectedVersion + 1, Action: "revoke", CredentialVersion: a.CredentialVersion}).check() == nil
	case PaidSpendArguments:
		valid = (p.Operation == "paid.spend.configure" || p.Operation == "paid.spend.delegate") && (RuntimeSpendConfigured{Scope: a.Scope, ExpectedVersion: a.ExpectedVersion, Version: a.ExpectedVersion + 1, Terms: a.Terms, ParentGrantID: a.ParentGrantID}).check() == nil
		if p.Operation == "paid.spend.delegate" {
			valid = valid && a.Scope.Kind == "grant" && isV7(a.ParentGrantID)
		}
	case PaidAdmitArguments:
		valid = p.Operation == "paid.admit" && isV7(a.AttemptID) && a.GrantID == p.GrantID && isV7(a.ParentTurnID) && validTaskBinding(a.TaskBinding) && isV7(a.RouteID) && a.RouteVersion > 0 && a.ProfileSHA256 != (Digest{}) && a.Request.check() == nil && a.Quote.check() == nil && validPaidObject(a.InputObject) && a.InputObject.SHA256 == a.Request.BodySHA256 && a.InputObject.Bytes == a.Request.BodyBytes
	case PaidAttemptArguments:
		valid = (p.Operation == "paid.dispatch" || p.Operation == "paid.release") && isV7(a.AttemptID) && a.ExpectedVersion == 1
	case PaidObserveArguments:
		valid = p.Operation == "paid.observe" && paidObservation(a.AttemptID, a.ExpectedVersion, a.EvidenceSHA256, a.EvidenceObject, a.MessageObjects) && (a.Kind == "unknown" || a.Kind == "usage" || a.Kind == "no_charge") && (a.Kind == "usage" || a.MessageObjects == nil)
	case PaidCorrectArguments:
		valid = p.Operation == "paid.correct" && paidObservation(a.AttemptID, a.ExpectedVersion, a.EvidenceSHA256, a.EvidenceObject, a.MessageObjects) && a.PreviousResultSequence > 0 && a.PreviousEvidenceSHA256 != (Digest{})
	case PaidOverrunArguments:
		valid = p.Operation == "paid.overrun.acknowledge" && (RuntimePaidOverrunAcknowledged{a.AttemptID, a.ExpectedVersion, a.ExpectedVersion + 1, a.ResultSequence, a.EvidenceSHA256, a.RetiredProfileSHA256}).check() == nil
	}
	if !valid {
		return errors.New("paid operation/argument mismatch or invalid arguments")
	}
	return nil
}

func (p PaidSubmission) Encode() (json.RawMessage, error) {
	return encodePaidPayload("schema-4 submission", p, p.check())
}
func DecodePaidSubmission(js json.RawMessage) (PaidSubmission, error) {
	var p PaidSubmission
	err := decodePaidPayload("schema-4 submission", js, func(m *members) error { p = readPaidSubmission(m); _, err := p.Encode(); return err })
	if err != nil {
		return PaidSubmission{}, err
	}
	return p, nil
}

func readPaidMessageObjects(m *members) []ObjectRef {
	if _, ok := m.get("message_objects", false); !ok {
		return nil
	}
	return readScopedObjects(m, "message_objects", readObject)
}
func readPaidSubmission(m *members) PaidSubmission {
	p := PaidSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation")}
	p.GrantID, _ = m.optionalID("grant_id")
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		switch p.Operation {
		case "paid.route.configure":
			return PaidRouteArguments{b.id("route_id"), b.nat("expected_version", true), b.boolean("enabled"), b.id("credential_scope_id"), b.digest("profile_sha256")}
		case "paid.credential.bind":
			a := PaidCredentialBindArguments{ScopeID: b.id("scope_id"), ExpectedVersion: b.nat("expected_version", true), Provider: b.text("provider"), CredentialVersion: b.id("credential_version"), SourceKind: b.text("source_kind"), SourceLocator: b.text("source_locator")}
			a.RevokePreviousVersion, _ = b.optionalID("revoke_previous_version")
			return a
		case "paid.credential.revoke":
			forbidScoped(b, "source_kind", "source_locator", "revoke_previous_version")
			return PaidCredentialRevokeArguments{b.id("scope_id"), b.nat("expected_version", true), b.text("provider"), b.id("credential_version")}
		case "paid.spend.configure", "paid.spend.delegate":
			a := PaidSpendArguments{Scope: graphObject(b, "scope", readPaidScope), ExpectedVersion: b.nat("expected_version", true), Terms: graphObject(b, "terms", readSpendTerms)}
			a.ParentGrantID, _ = b.optionalID("parent_grant_id")
			return a
		case "paid.admit":
			return PaidAdmitArguments{b.id("attempt_id"), b.id("grant_id"), b.id("parent_turn_id"), optionalObject(b, "task_binding", readTaskBinding), b.id("route_id"), b.nat("route_version", true), b.digest("profile_sha256"), graphObject(b, "request", readSealedDescriptor), graphObject(b, "quote", readCostBound), graphObject(b, "input_object", readObject)}
		case "paid.dispatch", "paid.release":
			forbidScoped(b, "kind", "evidence_sha256", "evidence_object", "message_objects", "previous_result_sequence", "previous_evidence_sha256")
			return PaidAttemptArguments{b.id("attempt_id"), b.nat("expected_version", true)}
		case "paid.observe":
			forbidScoped(b, "previous_result_sequence", "previous_evidence_sha256")
			return PaidObserveArguments{b.id("attempt_id"), b.nat("expected_version", true), b.text("kind"), b.digest("evidence_sha256"), graphObject(b, "evidence_object", readObject), readPaidMessageObjects(b)}
		case "paid.correct":
			forbidScoped(b, "kind")
			return PaidCorrectArguments{b.id("attempt_id"), b.nat("expected_version", true), b.nat("previous_result_sequence", true), b.digest("previous_evidence_sha256"), b.digest("evidence_sha256"), graphObject(b, "evidence_object", readObject), readPaidMessageObjects(b)}
		case "paid.overrun.acknowledge":
			return PaidOverrunArguments{b.id("attempt_id"), b.nat("expected_version", true), b.nat("result_sequence", true), b.digest("evidence_sha256"), b.digest("retired_profile_sha256")}
		}
		b.err = errors.New("unknown paid operation")
		return nil
	})
	return p
}

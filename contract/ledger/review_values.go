package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"uuid"
)

// MaxReviewPayloadBytes is F3-12's complete payload cap, proposed for freeze (D125).
const MaxReviewPayloadBytes = 64 << 10

// PrivateRevisionRef identifies one changed private object or transcript chunk,
// not a mutable head or all transcript bytes. It is proposed for freeze (D125).
// The service verifies the exact source revision, change and current read rights.
type PrivateRevisionRef struct {
	Binding        Binding `json:"binding"`
	Scope          string  `json:"scope"`
	Slot           string  `json:"slot"`
	Revision       int64   `json:"revision"`
	SourceSequence int64   `json:"source_sequence"`
	SHA256         Digest  `json:"sha256"`
	Bytes          int64   `json:"bytes"`
}

func validPrivateRevisionRef(r PrivateRevisionRef) bool {
	return validBinding(r.Binding) && privateSlot(r.Scope, r.Slot) && r.Revision > 0 && r.SourceSequence > 0 && r.SHA256 != (Digest{}) && r.Bytes >= 0
}

func readPrivateRevisionRef(m *members) PrivateRevisionRef {
	return PrivateRevisionRef{graphObject(m, "binding", readBinding), m.text("scope"), m.text("slot"), m.nat("revision", true), m.nat("source_sequence", true), m.digest("sha256"), m.nat("bytes", true)}
}

// ReviewPrincipal names the exact reviewer, not an inherited owner privilege.
type ReviewPrincipal struct {
	Actor   string    `json:"actor"`
	Origin  Origin    `json:"origin"`
	GrantID uuid.UUID `json:"grant_id,omitzero"`
}

// ReviewBinding binds a decision to one assignment, subject and evidence set.
// CheckSHA256 and ConfigSHA256 identify review inputs, not executed checks.
type ReviewBinding struct {
	TaskID                uuid.UUID       `json:"task_id"`
	TaskVersion           int64           `json:"task_version"`
	AssignmentID          uuid.UUID       `json:"assignment_id"`
	Generation            int64           `json:"generation"`
	Assignee              Binding         `json:"assignee"`
	Reviewer              ReviewPrincipal `json:"reviewer"`
	Subject               SubjectRef      `json:"subject"`
	AcceptanceSHA256      Digest          `json:"acceptance_sha256"`
	CheckSHA256           Digest          `json:"check_sha256"`
	ConfigSHA256          Digest          `json:"config_sha256"`
	RequiredEvidence      []SubjectRef    `json:"required_evidence"`
	AuthorizationRevision int64           `json:"authorization_revision"`
	DeadlineUTCNS         int64           `json:"deadline_utc_ns"`
}

// ReviewUsage charges the existing local-unit book, never a second wallet.
type ReviewUsage struct {
	AuthorityVersion int64      `json:"authority_version"`
	GrantID          uuid.UUID  `json:"grant_id"`
	Scopes           []ScopeKey `json:"scopes"`
	Units            int64      `json:"units"`
	Bytes            int64      `json:"bytes"`
}

func validReviewPrincipal(actor string, origin Origin, grant uuid.UUID) bool {
	return validOrigin(origin) && (actor == "owner" && grant == (uuid.UUID{}) || actor == "agent" && isV7(grant))
}

func (b ReviewBinding) check() error {
	if !isV7(b.TaskID) || b.TaskVersion < 1 || b.TaskVersion == math.MaxInt64 || !isV7(b.AssignmentID) || b.Generation < 1 || !validBinding(b.Assignee) || !validReviewPrincipal(b.Reviewer.Actor, b.Reviewer.Origin, b.Reviewer.GrantID) || !validSubject(&b.Subject) || b.AcceptanceSHA256 == (Digest{}) || b.CheckSHA256 == (Digest{}) || b.ConfigSHA256 == (Digest{}) || b.AuthorizationRevision < 0 || b.DeadlineUTCNS < 1 {
		return errors.New("invalid review binding")
	}
	if len(b.RequiredEvidence) == 0 {
		return errors.New("empty required evidence")
	}
	var previous []byte
	for _, subject := range b.RequiredEvidence {
		wire, err := subject.MarshalJSON()
		if err != nil || previous != nil && bytes.Compare(previous, wire) >= 0 {
			return errors.New("invalid or unordered required evidence")
		}
		previous = wire
	}
	return nil
}

func validReviewFailure(failure bool, cause string, evidence *SubjectRef, binding ReviewBinding) bool {
	if !failure {
		return cause == "" && evidence == nil
	}
	if !scopedToken(cause) || evidence == nil {
		return false
	}
	wire, err := evidence.MarshalJSON()
	if err != nil {
		return false
	}
	for _, required := range binding.RequiredEvidence {
		candidate, err := required.MarshalJSON()
		if err == nil && bytes.Equal(wire, candidate) {
			return true
		}
	}
	return false
}

func readReviewPrincipal(m *members) ReviewPrincipal {
	p := ReviewPrincipal{Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin)}
	p.GrantID, _ = m.optionalID("grant_id")
	return p
}

func requiredReviewSubject(m *members, key string) SubjectRef {
	if _, ok := m.get(key, true); !ok {
		return SubjectRef{}
	}
	s := subjectAt(m, key)
	if s == nil {
		return SubjectRef{}
	}
	return *s
}

func readReviewBinding(m *members) ReviewBinding {
	return ReviewBinding{
		TaskID: m.id("task_id"), TaskVersion: m.nat("task_version", true),
		AssignmentID: m.id("assignment_id"), Generation: m.nat("generation", true),
		Assignee: graphObject(m, "assignee", readBinding), Reviewer: graphObject(m, "reviewer", readReviewPrincipal),
		Subject: requiredReviewSubject(m, "subject"), AcceptanceSHA256: m.digest("acceptance_sha256"),
		CheckSHA256: m.digest("check_sha256"), ConfigSHA256: m.digest("config_sha256"),
		RequiredEvidence:      graphArray(m, "required_evidence", func(b *members) SubjectRef { return requiredReviewSubject(b, "element") }),
		AuthorizationRevision: m.nat("authorization_revision", true), DeadlineUTCNS: m.nat("deadline_utc_ns", true),
	}
}

func readReviewUsage(m *members) ReviewUsage {
	return ReviewUsage{m.nat("authority_version", true), m.id("grant_id"), readScopedObjects(m, "scopes", readScope), m.nat("units", true), m.nat("bytes", true)}
}

func encodeReviewPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxReviewPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxReviewPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeReviewPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxReviewPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxReviewPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

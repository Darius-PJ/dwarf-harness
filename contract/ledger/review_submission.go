package ledger

import (
	"encoding/json"
	"errors"
	"uuid"
)

// ReviewSubmission is schema 3's canonical producer encoding, proposed for freeze
// (D125). Arguments is the matching concrete value below, not arbitrary JSON.
// Host observations, command IDs, derived versions and usage are not inputs.
type ReviewSubmission struct {
	Schema    int64     `json:"schema"`
	Actor     string    `json:"actor"`
	Origin    Origin    `json:"origin"`
	Operation string    `json:"operation"`
	GrantID   uuid.UUID `json:"grant_id,omitzero"`
	Arguments any       `json:"arguments"`
}

// ReviewRequestArguments serves review.request and failure.request. Failure-only
// members are absent for review.request and required for failure.request.
type ReviewRequestArguments struct {
	RequestID         uuid.UUID     `json:"request_id"`
	Binding           ReviewBinding `json:"binding"`
	ReplacesRequestID uuid.UUID     `json:"replaces_request_id,omitzero"`
	FailureCause      string        `json:"failure_cause,omitempty"`
	FailingEvidence   *SubjectRef   `json:"failing_evidence,omitempty"`
}

// ReviewResolveArguments records an approve or reject decision.
type ReviewResolveArguments struct {
	RequestID       uuid.UUID     `json:"request_id"`
	RequestSequence int64         `json:"request_sequence"`
	Binding         ReviewBinding `json:"binding"`
	Verdict         string        `json:"verdict"`
}

// FailureResolveArguments records a fail decision with its exact evidence.
type FailureResolveArguments struct {
	RequestID       uuid.UUID     `json:"request_id"`
	RequestSequence int64         `json:"request_sequence"`
	Binding         ReviewBinding `json:"binding"`
	Cause           string        `json:"cause"`
	FailingEvidence SubjectRef    `json:"failing_evidence"`
}

func (p ReviewSubmission) check() error {
	if p.Schema != 3 || !validReviewPrincipal(p.Actor, p.Origin, p.GrantID) {
		return errors.New("invalid review submission principal or schema")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case ReviewRequestArguments:
		valid = (p.Operation == "review.request" || p.Operation == "failure.request") && isV7(a.RequestID) && a.Binding.check() == nil && (a.ReplacesRequestID == (uuid.UUID{}) || isV7(a.ReplacesRequestID) && a.ReplacesRequestID != a.RequestID) && validReviewFailure(p.Operation == "failure.request", a.FailureCause, a.FailingEvidence, a.Binding)
	case ReviewResolveArguments:
		valid = p.Operation == "review.resolve" && isV7(a.RequestID) && a.RequestSequence > 0 && a.Binding.check() == nil && (a.Verdict == "approve" || a.Verdict == "reject")
	case FailureResolveArguments:
		valid = p.Operation == "failure.resolve" && isV7(a.RequestID) && a.RequestSequence > 0 && a.Binding.check() == nil && validReviewFailure(true, a.Cause, &a.FailingEvidence, a.Binding)
	}
	if !valid {
		return errors.New("review operation/argument mismatch or invalid arguments")
	}
	return nil
}

func (p ReviewSubmission) Encode() (json.RawMessage, error) {
	return encodeReviewPayload("schema-3 submission", p, p.check())
}

func DecodeReviewSubmission(js json.RawMessage) (ReviewSubmission, error) {
	var p ReviewSubmission
	err := decodeReviewPayload("schema-3 submission", js, func(m *members) error {
		p = readReviewSubmission(m)
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return ReviewSubmission{}, err
	}
	return p, nil
}

func readReviewSubmission(m *members) ReviewSubmission {
	p := ReviewSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation")}
	p.GrantID, _ = m.optionalID("grant_id")
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		switch p.Operation {
		case "review.request", "failure.request":
			a := ReviewRequestArguments{RequestID: b.id("request_id"), Binding: graphObject(b, "binding", readReviewBinding), FailingEvidence: subjectAt(b, "failing_evidence")}
			a.ReplacesRequestID, _ = b.optionalID("replaces_request_id")
			a.FailureCause, _ = b.optionalText("failure_cause")
			forbidScoped(b, "request_sequence", "verdict", "cause")
			if p.Operation != "failure.request" {
				forbidScoped(b, "failure_cause", "failing_evidence")
			}
			return a
		case "review.resolve":
			forbidScoped(b, "replaces_request_id", "failure_cause", "cause", "failing_evidence")
			return ReviewResolveArguments{b.id("request_id"), b.nat("request_sequence", true), graphObject(b, "binding", readReviewBinding), b.text("verdict")}
		case "failure.resolve":
			forbidScoped(b, "replaces_request_id", "failure_cause", "verdict")
			return FailureResolveArguments{b.id("request_id"), b.nat("request_sequence", true), graphObject(b, "binding", readReviewBinding), b.text("cause"), requiredReviewSubject(b, "failing_evidence")}
		}
		b.err = errors.New("unknown review operation")
		return nil
	})
	return p
}

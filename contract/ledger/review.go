package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"uuid"
)

// F3-12's version-1 record types are proposed for freeze (D125).
const (
	TypeRuntimeReviewRequested = "runtime.review_requested"
	TypeRuntimeReviewResult    = "runtime.review_result"
	TypeRuntimeReviewReceipt   = "runtime.review_receipt"
)

// RuntimeReviewRequested establishes or explicitly replaces a task's review slot.
type RuntimeReviewRequested struct {
	RequestID         uuid.UUID     `json:"request_id"`
	Kind              string        `json:"kind"`
	Version           int64         `json:"version"`
	RequesterActor    string        `json:"requester_actor"`
	Requester         Origin        `json:"requester"`
	RequesterGrantID  uuid.UUID     `json:"requester_grant_id,omitzero"`
	Binding           ReviewBinding `json:"binding"`
	ReplacesRequestID uuid.UUID     `json:"replaces_request_id,omitzero"`
	FailureCause      string        `json:"failure_cause,omitempty"`
	FailingEvidence   *SubjectRef   `json:"failing_evidence,omitempty"`
	ObservedUTCNS     int64         `json:"observed_utc_ns"`
}

func (p RuntimeReviewRequested) check() error {
	if !isV7(p.RequestID) || (p.Kind != "review" && p.Kind != "failure") || p.Version != 1 || !validReviewPrincipal(p.RequesterActor, p.Requester, p.RequesterGrantID) || p.Binding.check() != nil || p.ReplacesRequestID != (uuid.UUID{}) && (!isV7(p.ReplacesRequestID) || p.ReplacesRequestID == p.RequestID) || !validReviewFailure(p.Kind == "failure", p.FailureCause, p.FailingEvidence, p.Binding) || p.ObservedUTCNS < 1 {
		return errors.New("invalid review request")
	}
	return nil
}

func (p RuntimeReviewRequested) Encode() (json.RawMessage, error) {
	return encodeReviewPayload(TypeRuntimeReviewRequested, p, p.check())
}

func DecodeRuntimeReviewRequested(js json.RawMessage) (RuntimeReviewRequested, error) {
	var p RuntimeReviewRequested
	err := decodeReviewPayload(TypeRuntimeReviewRequested, js, func(m *members) error {
		p = RuntimeReviewRequested{RequestID: m.id("request_id"), Kind: m.text("kind"), Version: m.nat("version", true), RequesterActor: m.text("requester_actor"), Requester: graphObject(m, "requester", readOrigin), Binding: graphObject(m, "binding", readReviewBinding), ObservedUTCNS: m.nat("observed_utc_ns", true)}
		p.RequesterGrantID, _ = m.optionalID("requester_grant_id")
		p.ReplacesRequestID, _ = m.optionalID("replaces_request_id")
		p.FailureCause, _ = m.optionalText("failure_cause")
		p.FailingEvidence = subjectAt(m, "failing_evidence")
		if p.Kind != "failure" {
			forbidScoped(m, "failure_cause", "failing_evidence")
		}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeReviewRequested{}, err
	}
	return p, nil
}

// RuntimeReviewResult records a judgment; approve and fail also transition the task.
// Cross-record identity, authority and clock checks belong to the service.
type RuntimeReviewResult struct {
	RequestID       uuid.UUID     `json:"request_id"`
	RequestSequence int64         `json:"request_sequence"`
	ExpectedVersion int64         `json:"expected_version"`
	Version         int64         `json:"version"`
	Binding         ReviewBinding `json:"binding"`
	Verdict         string        `json:"verdict"`
	Cause           string        `json:"cause,omitempty"`
	FailingEvidence *SubjectRef   `json:"failing_evidence,omitempty"`
	TaskVersion     int64         `json:"task_version"`
	ObservedUTCNS   int64         `json:"observed_utc_ns"`
}

func (p RuntimeReviewResult) check() error {
	if !isV7(p.RequestID) || p.RequestSequence < 1 || p.ExpectedVersion != 1 || p.Version != 2 || p.Binding.check() != nil || (p.Verdict != "approve" && p.Verdict != "reject" && p.Verdict != "fail") || !validReviewFailure(p.Verdict == "fail", p.Cause, p.FailingEvidence, p.Binding) || p.ObservedUTCNS < 1 {
		return errors.New("invalid review result")
	}
	version := p.Binding.TaskVersion
	if p.Verdict != "reject" {
		version++
	}
	if p.TaskVersion != version {
		return errors.New("review task version mismatch")
	}
	return nil
}

func (p RuntimeReviewResult) Encode() (json.RawMessage, error) {
	return encodeReviewPayload(TypeRuntimeReviewResult, p, p.check())
}

func DecodeRuntimeReviewResult(js json.RawMessage) (RuntimeReviewResult, error) {
	var p RuntimeReviewResult
	err := decodeReviewPayload(TypeRuntimeReviewResult, js, func(m *members) error {
		p = RuntimeReviewResult{RequestID: m.id("request_id"), RequestSequence: m.nat("request_sequence", true), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Binding: graphObject(m, "binding", readReviewBinding), Verdict: m.text("verdict"), FailingEvidence: subjectAt(m, "failing_evidence"), TaskVersion: m.nat("task_version", true), ObservedUTCNS: m.nat("observed_utc_ns", true)}
		p.Cause, _ = m.optionalText("cause")
		if p.Verdict != "fail" {
			forbidScoped(m, "cause", "failing_evidence")
		}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeReviewResult{}, err
	}
	return p, nil
}

// RuntimeReviewReceipt binds the immutable result to its canonical submission.
// An unfamiliar outcome survives decoding but confers no applied-state meaning.
type RuntimeReviewReceipt struct {
	CommandID        uuid.UUID        `json:"command_id"`
	SubmissionSHA256 Digest           `json:"submission_sha256"`
	Submission       ReviewSubmission `json:"submission"`
	Outcome          string           `json:"outcome"`
	Version          int64            `json:"version"`
	RequestID        uuid.UUID        `json:"request_id"`
	TaskVersion      int64            `json:"task_version"`
	Usage            *ReviewUsage     `json:"usage,omitempty"`
}

func (p RuntimeReviewReceipt) check() error {
	if !isV7(p.CommandID) || !isV7(p.RequestID) || !scopedToken(p.Outcome) || (p.Version != 1 && p.Version != 2) || p.TaskVersion < 1 || (p.Submission.Actor == "agent") != (p.Usage != nil) {
		return errors.New("invalid review receipt")
	}
	if u := p.Usage; u != nil && (u.AuthorityVersion < 0 || !isV7(u.GrantID) || u.GrantID != p.Submission.GrantID || !validScopes(u.Scopes) || u.Units != 1 || u.Bytes != 0) {
		return errors.New("invalid review usage")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("receipt submission digest mismatch")
	}
	return nil
}

func (p RuntimeReviewReceipt) Encode() (json.RawMessage, error) {
	return encodeReviewPayload(TypeRuntimeReviewReceipt, p, p.check())
}

func DecodeRuntimeReviewReceipt(js json.RawMessage) (RuntimeReviewReceipt, error) {
	var p RuntimeReviewReceipt
	err := decodeReviewPayload(TypeRuntimeReviewReceipt, js, func(m *members) error {
		p = RuntimeReviewReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Submission: graphObject(m, "submission", readReviewSubmission), Outcome: m.text("outcome"), Version: m.nat("version", true), RequestID: m.id("request_id"), TaskVersion: m.nat("task_version", true), Usage: optionalObject(m, "usage", readReviewUsage)}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeReviewReceipt{}, err
	}
	return p, nil
}

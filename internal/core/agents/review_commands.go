package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ReviewRequest asks for a judgment, not execution of a check.
type ReviewRequest struct {
	CommandID, GrantID, RequestID uuid.UUID
	Binding                       ledger.ReviewBinding
	ReplacesRequestID             uuid.UUID
}

type FailureRequest struct {
	CommandID, GrantID, RequestID uuid.UUID
	Binding                       ledger.ReviewBinding
	ReplacesRequestID             uuid.UUID
	Cause                         string
	FailingEvidence               ledger.SubjectRef
}

type ReviewResult struct {
	CommandID, GrantID, RequestID uuid.UUID
	RequestSequence               int64
	Binding                       ledger.ReviewBinding
	Verdict                       string
}

type FailureResult struct {
	CommandID, GrantID, RequestID uuid.UUID
	RequestSequence               int64
	Binding                       ledger.ReviewBinding
	Cause                         string
	FailingEvidence               ledger.SubjectRef
}

func (s *Service) RequestReview(ctx context.Context, origin Binding, request ReviewRequest) (ReviewReceipt, error) {
	return s.executeReview(ctx, scopedCaller{actor: "owner", binding: origin}, "review.request", request)
}
func (a *Agent) RequestReview(ctx context.Context, request ReviewRequest) (ReviewReceipt, error) {
	return a.reviewCommand(ctx, "review.request", request)
}
func (s *Service) RequestFailure(ctx context.Context, origin Binding, request FailureRequest) (ReviewReceipt, error) {
	return s.executeReview(ctx, scopedCaller{actor: "owner", binding: origin}, "failure.request", request)
}
func (a *Agent) RequestFailure(ctx context.Context, request FailureRequest) (ReviewReceipt, error) {
	return a.reviewCommand(ctx, "failure.request", request)
}
func (s *Service) ResolveReview(ctx context.Context, origin Binding, result ReviewResult) (ReviewReceipt, error) {
	return s.executeReview(ctx, scopedCaller{actor: "owner", binding: origin}, "review.resolve", result)
}
func (a *Agent) ResolveReview(ctx context.Context, result ReviewResult) (ReviewReceipt, error) {
	return a.reviewCommand(ctx, "review.resolve", result)
}
func (s *Service) ResolveFailure(ctx context.Context, origin Binding, result FailureResult) (ReviewReceipt, error) {
	return s.executeReview(ctx, scopedCaller{actor: "owner", binding: origin}, "failure.resolve", result)
}
func (a *Agent) ResolveFailure(ctx context.Context, result FailureResult) (ReviewReceipt, error) {
	return a.reviewCommand(ctx, "failure.resolve", result)
}
func (a *Agent) reviewCommand(ctx context.Context, operation string, request any) (ReviewReceipt, error) {
	if a == nil || a.service == nil {
		return ReviewReceipt{}, ErrNotBound
	}
	r, err := a.service.executeReview(ctx, scopedCaller{actor: "agent", binding: Binding{a.ids.agent, a.ids.context}, fixed: a.ids}, operation, request)
	return r, reviewDenied(err)
}

type reviewInput struct {
	id        uuid.UUID
	sub       ledger.ReviewSubmission
	digest    ledger.Digest
	binding   ledger.ReviewBinding
	requestID uuid.UUID
	kind      string
	result    bool
}

func reviewArguments(request any) (uuid.UUID, uuid.UUID, ledger.ReviewBinding, any) {
	switch r := request.(type) {
	case ReviewRequest:
		return r.CommandID, r.GrantID, r.Binding, ledger.ReviewRequestArguments{RequestID: r.RequestID, Binding: r.Binding, ReplacesRequestID: r.ReplacesRequestID}
	case FailureRequest:
		return r.CommandID, r.GrantID, r.Binding, ledger.ReviewRequestArguments{RequestID: r.RequestID, Binding: r.Binding, ReplacesRequestID: r.ReplacesRequestID, FailureCause: r.Cause, FailingEvidence: &r.FailingEvidence}
	case ReviewResult:
		return r.CommandID, r.GrantID, r.Binding, ledger.ReviewResolveArguments{RequestID: r.RequestID, RequestSequence: r.RequestSequence, Binding: r.Binding, Verdict: r.Verdict}
	case FailureResult:
		return r.CommandID, r.GrantID, r.Binding, ledger.FailureResolveArguments{RequestID: r.RequestID, RequestSequence: r.RequestSequence, Binding: r.Binding, Cause: r.Cause, FailingEvidence: r.FailingEvidence}
	}
	return uuid.UUID{}, uuid.UUID{}, ledger.ReviewBinding{}, nil
}

func makeReviewInput(actor string, ids submissionIDs, operation string, request any) (reviewInput, error) {
	id, grant, b, args := reviewArguments(request)
	if !validID(id) || len(b.RequiredEvidence) > ledger.MaxReviewPayloadBytes {
		return reviewInput{}, ErrInvalid
	}
	// Encode once per member, sort a copy, then let the frozen codec reject
	// duplicate references. Never mutate or deduplicate the caller's set.
	type member struct {
		ref ledger.SubjectRef
		raw []byte
	}
	members := make([]member, len(b.RequiredEvidence))
	for i, ref := range b.RequiredEvidence {
		raw, err := ref.MarshalJSON()
		if err != nil {
			return reviewInput{}, ErrInvalid
		}
		members[i] = member{ref, raw}
	}
	slices.SortFunc(members, func(a, b member) int { return bytes.Compare(a.raw, b.raw) })
	b.RequiredEvidence = make([]ledger.SubjectRef, len(members))
	for i, m := range members {
		b.RequiredEvidence[i] = m.ref
	}
	switch a := args.(type) {
	case ledger.ReviewRequestArguments:
		a.Binding = b
		args = a
	case ledger.ReviewResolveArguments:
		a.Binding = b
		args = a
	case ledger.FailureResolveArguments:
		a.Binding = b
		args = a
	}
	sub := ledger.ReviewSubmission{Schema: 3, Actor: actor, Origin: originOf(ids), Operation: operation, GrantID: grant, Arguments: args}
	raw, err := sub.Encode()
	if err != nil {
		return reviewInput{}, ErrInvalid
	}
	// Own every nested slice and pointer, using the same canonical form replay
	// compares; caller mutation cannot change a prepared command.
	sub, err = ledger.DecodeReviewSubmission(raw)
	if err != nil {
		return reviewInput{}, ErrInvalid
	}
	input := reviewInput{id: id, sub: sub, digest: ledger.Digest(sha256.Sum256(raw)), kind: "review"}
	if operation == "failure.request" || operation == "failure.resolve" {
		input.kind = "failure"
	}
	switch a := sub.Arguments.(type) {
	case ledger.ReviewRequestArguments:
		input.binding, input.requestID = a.Binding, a.RequestID
	case ledger.ReviewResolveArguments:
		input.binding, input.requestID, input.result = a.Binding, a.RequestID, true
	case ledger.FailureResolveArguments:
		input.binding, input.requestID, input.result = a.Binding, a.RequestID, true
	}
	return input, nil
}

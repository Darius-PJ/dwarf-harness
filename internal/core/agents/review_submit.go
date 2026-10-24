package agents

import (
	"context"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// reviewRecords is the request or result this command writes, observed at
// sample. A result's task version is the one its approve or fail produces.
func reviewRecords(input reviewInput, sample int64) (*ledger.RuntimeReviewRequested, *ledger.RuntimeReviewResult) {
	sub, b := input.sub, input.binding
	var result ledger.RuntimeReviewResult
	switch args := sub.Arguments.(type) {
	case ledger.ReviewRequestArguments:
		return &ledger.RuntimeReviewRequested{RequestID: args.RequestID, Kind: input.kind, Version: 1, RequesterActor: sub.Actor, Requester: sub.Origin, RequesterGrantID: sub.GrantID, Binding: b, ReplacesRequestID: args.ReplacesRequestID, FailureCause: args.FailureCause, FailingEvidence: args.FailingEvidence, ObservedUTCNS: sample}, nil
	case ledger.ReviewResolveArguments:
		result = ledger.RuntimeReviewResult{RequestID: args.RequestID, RequestSequence: args.RequestSequence, ExpectedVersion: 1, Version: 2, Binding: b, Verdict: args.Verdict, TaskVersion: b.TaskVersion, ObservedUTCNS: sample}
	case ledger.FailureResolveArguments:
		result = ledger.RuntimeReviewResult{RequestID: args.RequestID, RequestSequence: args.RequestSequence, ExpectedVersion: 1, Version: 2, Binding: b, Verdict: "fail", Cause: args.Cause, FailingEvidence: &args.FailingEvidence, TaskVersion: b.TaskVersion, ObservedUTCNS: sample}
	}
	if result.Verdict != "reject" {
		result.TaskVersion++
	}
	return nil, &result
}

// reviewUsage is an agent command's one charged unit across its grant chain's
// scopes; an owner command charges nothing.
func (s *Service) reviewUsage(ctx context.Context, sub ledger.ReviewSubmission) (*ledger.ReviewUsage, error) {
	if sub.Actor != "agent" {
		return nil, nil
	}
	chain, err := s.currentChain(ctx, sub.Origin, sub.GrantID, s.p.through)
	if err != nil {
		return nil, err
	}
	scopes, err := chainScopes(sub.Origin, chain)
	if err != nil {
		return nil, err
	}
	return &ledger.ReviewUsage{AuthorityVersion: s.p.authority, GrantID: sub.GrantID, Scopes: scopes, Units: 1}, nil
}

func (s *Service) reviewDrafts(ctx context.Context, input reviewInput, sample int64) ([]ledger.Draft, error) {
	sub, b := input.sub, input.binding
	drafts := make([]ledger.Draft, 0, 4)
	appendPayload := func(typ string, actor ledger.Actor, payload []byte) {
		drafts = append(drafts, ledger.Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, OperationID: input.id, Payload: payload})
	}
	command, err := (ledger.Command{CommandID: input.id, SubmissionSHA256: input.digest}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeCommand, ledger.Actor(sub.Actor), command)
	receipt := ledger.RuntimeReviewReceipt{CommandID: input.id, SubmissionSHA256: input.digest, Submission: sub, Outcome: "requested", Version: 1, RequestID: input.requestID, TaskVersion: b.TaskVersion}
	request, result := reviewRecords(input, sample)
	if request != nil {
		payload, err := request.Encode()
		if err != nil {
			return nil, ErrInvalid
		}
		appendPayload(ledger.TypeRuntimeReviewRequested, ledger.Actor(sub.Actor), payload)
	} else {
		payload, err := result.Encode()
		if err != nil {
			return nil, ErrInvalid
		}
		appendPayload(ledger.TypeRuntimeReviewResult, ledger.Actor(sub.Actor), payload)
		if result.Verdict != "reject" {
			payload, err := (ledger.BodyUnpin{PinID: b.TaskID}).Encode()
			if err != nil {
				return nil, err
			}
			appendPayload(ledger.TypeBodyUnpin, ledger.ActorCore, payload)
		}
		receipt.Outcome, receipt.Version, receipt.TaskVersion = reviewOutcome(result.Verdict), 2, result.TaskVersion
	}
	if receipt.Usage, err = s.reviewUsage(ctx, sub); err != nil {
		return nil, err
	}
	payload, err := receipt.Encode()
	if err != nil {
		return nil, ErrInvalid
	}
	appendPayload(ledger.TypeRuntimeReviewReceipt, ledger.Actor(sub.Actor), payload)
	return drafts, nil
}

package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type ReviewLookup struct {
	RequestID uuid.UUID
	GrantID   uuid.UUID
}

type ReviewCommandLookup struct {
	CommandID        uuid.UUID
	GrantID          uuid.UUID
	SubmissionSHA256 ledger.Digest
}

// ReviewView is an owned snapshot, not an authorization or an expiry reading.
type ReviewView struct {
	Request               ledger.RuntimeReviewRequested
	Result                *ledger.RuntimeReviewResult
	RequestSequence       uint64
	ResultSequence        uint64
	State                 string
	Current               bool
	SupersededByRequestID uuid.UUID
	ThroughSequence       uint64
}

// ReviewReceipt reports an immutable historical decision, never current success.
type ReviewReceipt struct {
	ledger.RuntimeReviewReceipt
	EffectSequence  uint64
	Sequence        uint64
	ThroughSequence uint64
}

func (s *Service) ReviewStatus(ctx context.Context, request ReviewLookup) (ReviewView, error) {
	return s.reviewStatus(ctx, nil, request)
}
func (a *Agent) ReviewStatus(ctx context.Context, request ReviewLookup) (ReviewView, error) {
	if a == nil || a.service == nil {
		return ReviewView{}, ErrNotBound
	}
	return a.service.reviewStatus(ctx, &a.ids, request)
}
func (s *Service) reviewStatus(ctx context.Context, bound *submissionIDs, request ReviewLookup) (ReviewView, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return ReviewView{}, err
	}
	defer done()
	if !validID(request.RequestID) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return ReviewView{}, ErrInvalid
	}
	if bound != nil {
		if _, err := s.currentChain(ctx, originOf(*bound), request.GrantID, s.p.through); err != nil {
			return ReviewView{}, reviewDenied(err)
		}
	}
	slot, found, err := s.loadReview(ctx, request.RequestID, s.p.through)
	if err != nil {
		return ReviewView{}, err
	}
	if !found {
		return ReviewView{}, ErrDenied
	}
	if bound != nil {
		origin := originOf(*bound)
		if slot.request.RequesterActor != "agent" || slot.request.Requester != origin {
			if slot.binding.Reviewer.Actor != "agent" || slot.binding.Reviewer.Origin != origin {
				return ReviewView{}, ErrDenied
			}
		}
		if err := s.reviewQueryRights(ctx, origin, request.GrantID, "task.inspect", slot.binding); err != nil {
			return ReviewView{}, reviewDenied(err)
		}
	}
	raw, err := slot.request.Encode()
	if err != nil {
		return ReviewView{}, err
	}
	copy, err := ledger.DecodeRuntimeReviewRequested(raw)
	if err != nil {
		return ReviewView{}, err
	}
	view := ReviewView{Request: copy, RequestSequence: slot.sequence, ResultSequence: slot.resultSequence, State: slot.state, Current: s.p.reviews[slot.binding.TaskID].id == slot.id, SupersededByRequestID: slot.supersededBy, ThroughSequence: s.p.through}
	if slot.result != nil {
		raw, err := slot.result.Encode()
		if err != nil {
			return ReviewView{}, err
		}
		result, err := ledger.DecodeRuntimeReviewResult(raw)
		if err != nil {
			return ReviewView{}, err
		}
		view.Result = &result
	}
	return view, nil
}

func (s *Service) reviewQueryRights(ctx context.Context, origin ledger.Origin, grant uuid.UUID, operation string, b ledger.ReviewBinding) error {
	if _, err := s.authorize(ctx, origin, grant, operation, []ledger.Resource{{Kind: "task", TaskID: b.TaskID}}, nil, 0, s.p.through); err != nil {
		return err
	}
	task, found, err := s.loadTask(ctx, b.TaskID, s.p.through)
	if err != nil {
		return err
	}
	if !found || !slices.Contains(task.record.Audience, origin.AgentID) {
		return ErrDenied
	}
	sources, err := s.reviewWitnesses(ctx, b, s.p.through)
	if err != nil {
		return err
	}
	return s.reviewReadRights(ctx, ledger.ReviewPrincipal{Actor: "agent", Origin: origin, GrantID: grant}, b, s.p.through, sources)
}

func (s *Service) ReviewCommandStatus(ctx context.Context, request ReviewCommandLookup) (ReviewReceipt, error) {
	return s.reviewCommandStatus(ctx, nil, request)
}
func (a *Agent) ReviewCommandStatus(ctx context.Context, request ReviewCommandLookup) (ReviewReceipt, error) {
	if a == nil || a.service == nil {
		return ReviewReceipt{}, ErrNotBound
	}
	return a.service.reviewCommandStatus(ctx, &a.ids, request)
}
func (s *Service) reviewCommandStatus(ctx context.Context, bound *submissionIDs, request ReviewCommandLookup) (ReviewReceipt, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return ReviewReceipt{}, err
	}
	defer done()
	if !validID(request.CommandID) || request.SubmissionSHA256 == (ledger.Digest{}) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return ReviewReceipt{}, ErrInvalid
	}
	if bound != nil {
		if _, err := s.currentChain(ctx, originOf(*bound), request.GrantID, s.p.through); err != nil {
			return ReviewReceipt{}, reviewDenied(err)
		}
	}
	match, err := s.findCommand(ctx, request.CommandID, 0, s.p.through)
	if err != nil {
		return ReviewReceipt{}, err
	}
	if match.review == nil {
		if bound != nil {
			return ReviewReceipt{}, ErrDenied
		}
		return ReviewReceipt{}, ErrUnavailable
	}
	g := match.review
	if bound != nil {
		sub := g.receipt.Submission
		if sub.Actor != "agent" || sub.Origin != originOf(*bound) {
			return ReviewReceipt{}, ErrDenied
		}
		b := ledger.ReviewBinding{}
		kind := "review"
		if g.request != nil {
			b, kind = g.request.Binding, g.request.Kind
		} else {
			b = g.result.Binding
			if g.result.Verdict == "fail" {
				kind = "failure"
			}
		}
		if err := s.reviewQueryRights(ctx, sub.Origin, request.GrantID, reviewOperation(kind), b); err != nil {
			return ReviewReceipt{}, reviewDenied(err)
		}
	}
	if match.command.SubmissionSHA256 != request.SubmissionSHA256 {
		return ReviewReceipt{}, ErrCommandMismatch
	}
	raw, err := g.receipt.Encode()
	if err != nil {
		return ReviewReceipt{}, err
	}
	copy, err := ledger.DecodeRuntimeReviewReceipt(raw)
	if err != nil {
		return ReviewReceipt{}, err
	}
	return ReviewReceipt{RuntimeReviewReceipt: copy, EffectSequence: g.first + 1, Sequence: g.first + uint64(g.receiptIndex), ThroughSequence: s.p.through}, nil
}

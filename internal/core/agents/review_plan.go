package agents

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ErrReviewStale refuses a new decision for a known, no-longer-pending request.
var ErrReviewStale = errors.New("agents: review stale")

type reviewPlan struct {
	slot      reviewSlot
	task      *scopedTaskState
	budget    map[ledger.ScopeKey]budgetAccount
	floor     int64
	requestID uuid.UUID
}

func (s *Service) planReview(ctx context.Context, g *reviewGroup, first uint64) (reviewPlan, error) {
	plan := reviewPlan{}
	through := first - 1
	var slot reviewSlot
	var sample int64
	if g.request != nil {
		q := *g.request
		if reviewRequestNeedsScan(q.RequestID, s.p.maxRequest) {
			_, found, err := s.loadReview(ctx, q.RequestID, through)
			if err != nil {
				return plan, err
			}
			if found {
				return plan, ErrInvalid
			}
		}
		previous := s.p.reviews[q.Binding.TaskID]
		if q.ReplacesRequestID != previous.id {
			return plan, ErrReviewStale
		}
		if previous.id != (uuid.UUID{}) {
			if _, err := transitionReviewSlot(previous, previous.id, "supersede", q.RequestID); err != nil {
				return plan, err
			}
		}
		if q.Binding.AuthorizationRevision != s.p.authority {
			return plan, ErrInvalid
		}
		slot = reviewSlot{id: q.RequestID, binding: q.Binding, kind: q.Kind, state: "pending", request: q, sequence: first + 1}
		sample = q.ObservedUTCNS
		plan.requestID = q.RequestID
	} else {
		v := *g.result
		slot = s.p.reviews[v.Binding.TaskID]
		if slot.id != v.RequestID {
			_, found, err := s.loadReview(ctx, v.RequestID, through)
			if err != nil {
				return plan, err
			}
			if !found {
				return plan, ErrDenied
			}
			return plan, ErrReviewStale
		}
		if slot.state != "pending" {
			return plan, ErrReviewStale
		}
		if int64(slot.sequence) != v.RequestSequence || matchReviewBinding(slot.binding, v.Binding) != nil || slot.request.FailureCause != v.Cause || !equalSubject(subjectValue(slot.request.FailingEvidence), subjectValue(v.FailingEvidence)) {
			return plan, ErrInvalid
		}
		sample = v.ObservedUTCNS
	}
	floor, err := reviewObservation(sample, nil, s.p.reviewFloor, slot.binding.DeadlineUTCNS, g.result != nil)
	if err != nil {
		return plan, err
	}
	task, found, err := s.loadTask(ctx, slot.binding.TaskID, through)
	if err != nil {
		return plan, err
	}
	if !found {
		return plan, ErrDenied
	}
	b, p := slot.binding, task.record
	if p.Version != b.TaskVersion {
		return plan, &RevisionError{Expected: uint64(b.TaskVersion), Current: uint64(p.Version)}
	}
	if p.Assignment == nil || p.Assignment.AssignmentID != b.AssignmentID || p.Assignment.Generation != b.Generation || p.Assignment.Assignee != b.Assignee {
		return plan, ErrReviewStale
	}
	if terminalTask(p.State) || p.AcceptanceSHA256 != b.AcceptanceSHA256 || slot.kind == "review" && p.State != "submitted" || p.State == "submitted" && !equalSubject(subjectValue(p.Submission), b.Subject) {
		return plan, ErrInvalid
	}
	requester := ledger.ReviewPrincipal{Actor: slot.request.RequesterActor, Origin: slot.request.Requester, GrantID: slot.request.RequesterGrantID}
	requestChain, err := s.reviewParticipant(ctx, requester, b, slot.kind, true, p, through)
	if err != nil {
		return plan, err
	}
	reviewerChain, err := s.reviewParticipant(ctx, b.Reviewer, b, slot.kind, false, p, through)
	if err != nil {
		return plan, err
	}
	sources, err := s.reviewWitnesses(ctx, b, through)
	if err != nil {
		return plan, err
	}
	for _, principal := range []ledger.ReviewPrincipal{requester, b.Reviewer} {
		if err := s.reviewReadRights(ctx, principal, b, through, sources); err != nil {
			return plan, err
		}
	}
	if err := s.reviewSources(b, sources); err != nil {
		return plan, err
	}
	if g.result != nil {
		next, err := transitionReviewSlot(slot, slot.id, g.result.Verdict, uuid.UUID{})
		if err != nil {
			return plan, err
		}
		next.result, next.resultSequence = g.result, first+1
		slot = next
		if g.result.Verdict != "reject" {
			action := taskAction{task: p.TaskID, action: "complete", submission: b.Subject}
			if g.result.Verdict == "fail" {
				action = taskAction{task: p.TaskID, action: "fail", failureCause: g.result.Cause, failureEvidence: *g.result.FailingEvidence}
			}
			facts := taskFacts{actor: b.Reviewer.Actor, origin: bindingOf(b.Reviewer.Origin), authorized: true, subjectAuthorized: true, reviewedFailure: g.result.Verdict == "fail"}
			current := taskRules(p)
			if err := guardReviewTask(slot, current, action.action); err != nil {
				return plan, err
			}
			nextTask, err := transitionTask(current, facts, action, taskExpectation{version: uint64(b.TaskVersion), assignment: b.AssignmentID, generation: b.Generation})
			if err != nil {
				return plan, err
			}
			if int64(nextTask.version) != g.result.TaskVersion {
				return plan, ErrInvalid
			}
			changed := taskAfterReview(task, *g.result, first+1)
			plan.task = &changed
		}
	}
	if u := g.receipt.Usage; u != nil {
		chain := requestChain
		if g.result != nil {
			chain = reviewerChain
		}
		sub := g.receipt.Submission
		scopes, err := chainScopes(sub.Origin, chain)
		if err != nil {
			return plan, err
		}
		if u.AuthorityVersion != s.p.authority || u.GrantID != sub.GrantID || !slices.Equal(u.Scopes, scopes) || u.Units != 1 || u.Bytes != 0 {
			return plan, ErrInvalid
		}
		plan.budget, err = s.p.book.atomic(scopes, 0)
		if err != nil {
			return reviewPlan{}, err
		}
	}
	plan.slot, plan.floor = slot, floor
	return plan, nil
}

func (p *projection) installReview(plan reviewPlan) {
	p.reviewFloor = plan.floor
	if bytes.Compare(plan.requestID[:], p.maxRequest[:]) > 0 {
		p.maxRequest = plan.requestID
	}
	id := plan.slot.binding.TaskID
	if plan.task != nil {
		p.tasks[id] = *plan.task
	}
	if plan.slot.state == "pending" || plan.slot.state == "rejected" {
		p.reviews[id] = plan.slot
	} else {
		delete(p.reviews, id)
	}
	p.book.install(plan.budget)
	p.pruneScoped()
}

// The reducer has already proved the kernel transition. History uses this same
// snapshot construction, preserving the original description source identity.
func taskAfterReview(task scopedTaskState, result ledger.RuntimeReviewResult, sequence uint64) scopedTaskState {
	p := task.record
	p.ExpectedVersion, p.Version = p.Version, result.TaskVersion
	if result.Verdict == "approve" {
		p.Action, p.State = "complete", "completed"
	} else {
		p.Action, p.State = "fail", "failed"
		p.WaitReason, p.WaitArtifact, p.Submission = "", nil, nil
		p.FailureCause, p.FailureEvidence = result.Cause, result.FailingEvidence
	}
	task.record, task.sequence = p, sequence
	return task
}

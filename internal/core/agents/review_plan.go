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

// reviewHead holds what the reducer's first half proved about one request or
// result: its slot, task and both participants' current chains.
type reviewHead struct {
	slot                        reviewSlot
	task                        scopedTaskState
	requester                   ledger.ReviewPrincipal
	requestChain, reviewerChain []grantRules
	sample                      int64
}

// reviewHeadAt is brief §3.1 step 4 up to subject rights: the exact request
// and its slot, then the task's version, assignment and stored submission, then
// both participants' current authority. planReview runs it first; a producer
// also runs it alone, before any witness, clock or capacity decision.
func (s *Service) reviewHeadAt(ctx context.Context, request *ledger.RuntimeReviewRequested, result *ledger.RuntimeReviewResult, first uint64) (reviewHead, error) {
	h := reviewHead{}
	through := first - 1
	if request != nil {
		q := *request
		if reviewRequestNeedsScan(q.RequestID, s.p.maxRequest) {
			_, found, err := s.loadReview(ctx, q.RequestID, through)
			if err != nil {
				return h, err
			}
			if found {
				return h, ErrInvalid
			}
		}
		previous := s.p.reviews[q.Binding.TaskID]
		if q.ReplacesRequestID != previous.id {
			return h, ErrReviewStale
		}
		if previous.id != (uuid.UUID{}) {
			if _, err := transitionReviewSlot(previous, previous.id, "supersede", q.RequestID); err != nil {
				return h, err
			}
		}
		if q.Binding.AuthorizationRevision != s.p.authority {
			return h, ErrInvalid
		}
		h.slot = reviewSlot{id: q.RequestID, binding: q.Binding, kind: q.Kind, state: "pending", request: q, sequence: first + 1}
		h.sample = q.ObservedUTCNS
	} else {
		v := *result
		h.slot = s.p.reviews[v.Binding.TaskID]
		if h.slot.id != v.RequestID {
			_, found, err := s.loadReview(ctx, v.RequestID, through)
			if err != nil {
				return h, err
			}
			if !found {
				return h, ErrDenied
			}
			return h, ErrReviewStale
		}
		if h.slot.state != "pending" {
			return h, ErrReviewStale
		}
		if int64(h.slot.sequence) != v.RequestSequence || matchReviewBinding(h.slot.binding, v.Binding) != nil || h.slot.request.FailureCause != v.Cause || !equalSubject(subjectValue(h.slot.request.FailingEvidence), subjectValue(v.FailingEvidence)) {
			return h, ErrInvalid
		}
		h.sample = v.ObservedUTCNS
	}
	task, found, err := s.loadTask(ctx, h.slot.binding.TaskID, through)
	if err != nil {
		return h, err
	}
	if !found {
		return h, ErrDenied
	}
	b, p := h.slot.binding, task.record
	if p.Version != b.TaskVersion {
		return h, &RevisionError{Expected: uint64(b.TaskVersion), Current: uint64(p.Version)}
	}
	if p.Assignment == nil || p.Assignment.AssignmentID != b.AssignmentID || p.Assignment.Generation != b.Generation || p.Assignment.Assignee != b.Assignee {
		return h, ErrReviewStale
	}
	if terminalTask(p.State) || p.AcceptanceSHA256 != b.AcceptanceSHA256 || h.slot.kind == "review" && p.State != "submitted" || p.State == "submitted" && !equalSubject(subjectValue(p.Submission), b.Subject) {
		return h, ErrInvalid
	}
	h.task = task
	h.requester = ledger.ReviewPrincipal{Actor: h.slot.request.RequesterActor, Origin: h.slot.request.Requester, GrantID: h.slot.request.RequesterGrantID}
	if h.requestChain, err = s.reviewParticipant(ctx, h.requester, b, h.slot.kind, true, p, through); err != nil {
		return h, err
	}
	h.reviewerChain, err = s.reviewParticipant(ctx, b.Reviewer, b, h.slot.kind, false, p, through)
	return h, err
}

// planReview is the one F3-12 rule set, in brief §3.1 step 4's order: the head,
// then both participants' subject and evidence rights and the source
// witnesses, then trusted time, then the transition and its charge.
func (s *Service) planReview(ctx context.Context, g *reviewGroup, first uint64) (reviewPlan, error) {
	plan := reviewPlan{}
	through := first - 1
	h, err := s.reviewHeadAt(ctx, g.request, g.result, first)
	if err != nil {
		return plan, err
	}
	if g.request != nil {
		plan.requestID = g.request.RequestID
	}
	slot, task, requester, b, p := h.slot, h.task, h.requester, h.slot.binding, h.task.record
	sources := g.sources
	if sources == nil {
		sources, err = s.reviewWitnesses(ctx, b, through)
		if err != nil {
			return plan, err
		}
	}
	for _, principal := range []ledger.ReviewPrincipal{requester, b.Reviewer} {
		if err := s.reviewReadRights(ctx, principal, b, through, sources); err != nil {
			return plan, err
		}
	}
	if err := s.reviewSources(b, sources); err != nil {
		return plan, err
	}
	floor, err := reviewObservation(h.sample, nil, s.p.reviewFloor, b.DeadlineUTCNS, g.result != nil)
	if err != nil {
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
		chain := h.requestChain
		if g.result != nil {
			chain = h.reviewerChain
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

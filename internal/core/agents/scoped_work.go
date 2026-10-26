package agents

import (
	"context"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) planWork(ctx context.Context, g *scopedGroup, first uint64) (scopedPlan, error) {
	plan := scopedPlan{authority: s.p.authority}
	sub, through := g.receipt.Submission, first-1
	if g.admission != nil {
		p := *g.admission
		if _, found, err := s.loadWork(ctx, p.WorkID, through); err != nil {
			return plan, err
		} else if found {
			return plan, ErrInvalid
		}
		if p.GrantID != sub.GrantID || p.AuthorityVersion != s.p.authority || !sameScopedValue(p.Target, sub.Target) || g.receipt.WorkID != p.WorkID || g.receipt.Version != 1 {
			return plan, ErrInvalid
		}
		size := int64(0)
		if p.MaxBytes != nil {
			size = *p.MaxBytes
		}
		chain, err := s.authorize(ctx, sub.Origin, sub.GrantID, p.Operation, []ledger.Resource{p.Target}, nil, size, through)
		if err != nil {
			return plan, err
		}
		scopes, err := chainScopes(sub.Origin, chain)
		if err != nil || !sameScopedValue(scopes, p.Scopes) {
			return plan, ErrInvalid
		}
		if chain[0].task != (ledger.TaskBinding{}) && (p.TaskBinding == nil || *p.TaskBinding != chain[0].task) {
			return plan, ErrDenied
		}
		task, err := s.activeWorkTask(ctx, sub.Origin, p.TaskBinding, through)
		if err != nil {
			return plan, err
		}
		if task != nil {
			plan.dependencies = append(plan.dependencies, *task)
		}
		delta := budgetDelta{}
		if p.Kind == "turn" {
			if sub.Operation != "turn.start" || g.turn != p.WorkID || !sameScopedValue(sub.Arguments, ledger.TurnStartArguments{TurnID: p.WorkID, TaskBinding: p.TaskBinding}) {
				return plan, ErrInvalid
			}
			for _, work := range s.p.work {
				if work.admission.Kind == "turn" && !work.released && work.origin.ContextID == sub.Origin.ContextID {
					return plan, &BusyError{Reason: "context_active"}
				}
			}
			delta.hold[turnExposure] = 1
		} else {
			parent, found, err := s.loadWork(ctx, p.ParentTurnID, through)
			if err != nil {
				return plan, err
			}
			if !found || parent.admission.Kind != "turn" || parent.ended || parent.released || parent.origin != sub.Origin || g.turn != parent.admission.WorkID || !sameScopedValue(parent.admission.TaskBinding, p.TaskBinding) {
				return plan, ErrDenied
			}
			if sub.Operation != "local.admit" || !sameScopedValue(sub.Arguments, ledger.LocalAdmissionArguments{WorkID: p.WorkID, ParentTurnID: p.ParentTurnID, LocalOperation: p.Operation, ArgumentsSHA256: p.ArgumentsSHA256, ToolSchema: *p.ToolSchema, MaxBytes: *p.MaxBytes, TaskBinding: p.TaskBinding}) {
				return plan, ErrInvalid
			}
			if err := s.workspaceReady(ctx, p.Target, through); err != nil {
				return plan, err
			}
			delta.hold = [4]int64{0, 1, *p.MaxBytes, 1}
		}
		plan.budget, err = s.p.book.plan(scopes, delta, size, true)
		if err != nil {
			return plan, err
		}
		plan.work = &scopedWorkState{admission: p, origin: sub.Origin, turn: g.turn, version: 1, sequence: first + uint64(g.effectIndex)}
		return plan, nil
	}
	id := g.receipt.WorkID
	work, found, err := s.loadWork(ctx, id, through)
	if err != nil {
		return plan, err
	}
	if !found {
		return plan, ErrUnavailable
	}
	agentCancel := sub.Actor == "agent" && sub.Operation == "turn.cancel"
	if work.origin != sub.Origin || work.turn != g.turn || !agentCancel && work.admission.GrantID != sub.GrantID || !sameScopedValue(work.admission.Target, sub.Target) {
		return plan, ErrDenied
	}
	if sub.Expected.Version != work.version {
		return plan, &RevisionError{Expected: uint64(sub.Expected.Version), Current: uint64(work.version)}
	}
	if work.released {
		return plan, ErrNoEffect
	}
	delta := budgetDelta{}
	if g.dispatch != nil {
		p := *g.dispatch
		if work.admission.Kind != "local" || work.version != 1 || work.dispatched || work.ended || p.WorkID != id || p.ExpectedVersion != work.version || p.AuthorityVersion != s.p.authority || p.GrantID != work.admission.GrantID || p.ArgumentsSHA256 != work.admission.ArgumentsSHA256 || !sameScopedValue(sub.Arguments, ledger.WorkDispatchArguments{WorkID: id, ArgumentsSHA256: p.ArgumentsSHA256}) {
			return plan, ErrInvalid
		}
		if _, err := s.authorize(ctx, work.origin, work.admission.GrantID, work.admission.Operation, []ledger.Resource{work.admission.Target}, nil, *work.admission.MaxBytes, through); err != nil {
			return plan, err
		}
		task, err := s.activeWorkTask(ctx, work.origin, work.admission.TaskBinding, through)
		if err != nil {
			return plan, err
		}
		if task != nil {
			plan.dependencies = append(plan.dependencies, *task)
		}
		if err := s.workspaceReady(ctx, work.admission.Target, through); err != nil {
			return plan, err
		}
		parent, found, err := s.loadWork(ctx, work.admission.ParentTurnID, through)
		if err != nil {
			return plan, err
		}
		if !found || parent.ended || parent.released {
			return plan, ErrDenied
		}
		work.dispatched, work.version = true, p.Version
		delta.charge[unitExposure] = 1
	} else {
		p := *g.ending
		if p.WorkID != id || p.ExpectedVersion != work.version || !sameScopedValue(sub.Arguments, ledger.WorkEndArguments{WorkID: id, Outcome: p.Outcome}) {
			return plan, ErrInvalid
		}
		if work.ended && p.Outcome == work.outcome {
			return plan, ErrNoEffect
		}
		if work.admission.Kind == "turn" {
			if sub.Operation != "turn.end" && sub.Operation != "turn.cancel" {
				return plan, ErrInvalid
			}
			if sub.Operation == "turn.cancel" {
				if p.Outcome != "cancelled" {
					return plan, ErrInvalid
				}
				if sub.Actor == "agent" {
					if work.version != 1 {
						return plan, ErrDenied
					}
					if _, err := s.authorize(ctx, work.origin, sub.GrantID, "turn.cancel", []ledger.Resource{sub.Target}, nil, 0, through); err != nil {
						return plan, err
					}
				}
			}
			if releasesWork("turn", p.Outcome) {
				if s.p.paidChildren[id] != 0 {
					return plan, &BusyError{Reason: "child_unended"}
				}
				for _, child := range s.p.work {
					if child.admission.ParentTurnID == id && !child.ended {
						return plan, &BusyError{Reason: "child_unended"}
					}
				}
				delta.release[turnExposure] = 1
			}
		} else {
			if sub.Operation != "local.end" {
				return plan, ErrInvalid
			}
			if p.Outcome == "released_unsent" && work.dispatched || (p.Outcome == "succeeded" || p.Outcome == "failed" || p.Outcome == "interrupted") && !work.dispatched {
				return plan, ErrInvalid
			}
			if releasesWork("local", p.Outcome) {
				delta.release[localExposure], delta.release[bufferExposure] = 1, *work.admission.MaxBytes
				if !work.dispatched {
					delta.release[unitExposure] = 1
				}
			}
		}
		work.ended, work.outcome, work.version = true, p.Outcome, p.Version
		work.released = releasesWork(work.admission.Kind, p.Outcome)
	}
	if g.receipt.Version != work.version {
		return plan, ErrInvalid
	}
	plan.budget, err = s.p.book.plan(work.admission.Scopes, delta, 0, false)
	if err != nil {
		return plan, err
	}
	plan.work = &work
	return plan, nil
}

// A broad context grant cannot erase the assignment carried by a task context.
// The durable admission and every dispatch must retain that exact active pair.
func (s *Service) activeWorkTask(ctx context.Context, origin ledger.Origin, binding *ledger.TaskBinding, through uint64) (*scopedTaskState, error) {
	task, bound, err := s.contextTask(ctx, origin, through)
	if err != nil {
		return nil, err
	}
	if !bound {
		if binding != nil {
			return nil, ErrDenied
		}
		return nil, nil
	}
	assignment := task.record.Assignment
	if binding == nil || binding.TaskID != task.record.TaskID || task.record.State != "active" || assignment == nil || binding.AssignmentID != assignment.AssignmentID || binding.Generation != assignment.Generation || assignment.Assignee != ledger.Binding(bindingOf(origin)) {
		return nil, ErrDenied
	}
	return &task, nil
}

func (s *Service) workspaceReady(ctx context.Context, target ledger.Resource, through uint64) error {
	if target.Kind != "workspace" {
		return ErrUnavailable
	}
	registered, err := s.registeredResource(ctx, target, through)
	if err != nil {
		return err
	}
	if !registered {
		return ErrUnavailable
	}
	for _, scratch := range s.p.scratches {
		if scratch.WorkspaceID == target.WorkspaceID && scratch.ContextID == target.Binding.ContextID && scratch.ParentScratchID == (uuid.UUID{}) && !scratch.closed {
			return nil
		}
	}
	return ErrUnavailable
}

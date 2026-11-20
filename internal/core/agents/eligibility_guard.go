package agents

import (
	"context"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// Replay validates the recorded identity, never private input bytes. S2 has no
// live first-action producer; S3 will verify its input before using an exception.
type eligibilityMode uint8

const (
	eligibilityLive eligibilityMode = iota
	eligibilityReplay
)

type eligibilityWork struct {
	turn, command, start          uuid.UUID
	task                          *ledger.TaskBinding
	admission, binding, taskStart bool
}

// guardEligibility is deliberately beside, not inside, the continuation guard:
// a turn need not have a loop owner to be ordinary work.
func (s *Service) guardEligibility(origin ledger.Origin, work eligibilityWork, mode eligibilityMode, through uint64) error {
	agent, found := s.p.agents[origin.AgentID]
	if !found || agent.session != origin.SessionID || s.p.contexts[origin.ContextID] != origin.AgentID {
		return ErrNotBound
	}
	e := agent.eligibility
	v := e.current()
	if v.Version == 0 || v.State == "eligible" && e.outcome == "recorded" {
		return nil
	}
	if mode == eligibilityReplay && v.State == "first_action_required" && e.outcome == "recorded" && v.FirstAction != nil {
		turn := v.FirstAction.PermittedTurn
		if turn.ContextID == origin.ContextID {
			switch {
			case work.taskStart:
				if turn.TaskBinding != nil && sameScopedValue(work.task, turn.TaskBinding) {
					return nil
				}
			case work.admission:
				if work.turn == turn.TurnID && work.command == turn.AdmissionCommandID && sameScopedValue(work.task, turn.TaskBinding) && e.nativeSequence == 0 {
					return nil
				}
			case work.binding:
				if work.turn == turn.TurnID && work.command == turn.AdmissionCommandID && work.start == turn.StartCommandID && e.nativeSequence != 0 && e.boundSequence == 0 {
					return nil
				}
			default:
				bound, ok := s.p.loopTurns[work.turn]
				if work.turn == turn.TurnID && e.boundSequence != 0 && ok && bound.origin == origin && bound.sequence == e.boundSequence {
					return nil
				}
			}
		}
	}
	view := eligibilityView(e, through)
	// No inspect authority was evaluated by this pure decision. Disclosure
	// entrances may attach the declaration only in a prepared authority phase.
	view.FirstAction = nil
	return &EligibilityHeldError{AgentID: origin.AgentID, EligibilityView: view}
}

// An ordinary hold does not fence loop.bind itself: an already admitted native
// turn must remain recoverable. first_action_required binds only its exact tuple.
func (s *Service) guardEligibilityBind(origin ledger.Origin, turn, admission, start uuid.UUID, mode eligibilityMode, through uint64) error {
	if s.p.agents[origin.AgentID].eligibility.current().State == "ordinary_work_ineligible" {
		return nil
	}
	return s.guardEligibility(origin, eligibilityWork{turn: turn, command: admission, start: start, binding: true}, mode, through)
}

func eligibilityEffect(operation string) bool {
	return operation == "private.write"
}

func (s *Service) guardEligibilityEffect(ctx context.Context, reads *historyReadSet, origin ledger.Origin, command uuid.UUID, mode eligibilityMode, through uint64) error {
	turn, err := s.promisedContinuation(ctx, reads, command, through)
	if err != nil {
		return err
	}
	return s.guardEligibility(origin, eligibilityWork{turn: turn}, mode, through)
}

func (s *Service) installEligibilityFlagsLocked() {
	if r := s.loop; r != nil {
		r.mu.Lock()
		changed := false
		for _, t := range r.turns {
			held := s.guardEligibility(t.origin, eligibilityWork{turn: t.id}, eligibilityLive, s.p.through) != nil
			changed = changed || t.eligibilityHeld != held
			t.eligibilityHeld = held
		}
		if changed {
			r.changedLocked()
		}
		r.mu.Unlock()
	}
}

// Completed effects and paid intents are reconciliation, not permission for a
// new dispatch. Only the idle scheduler or the turn's own worker reads these.
func eligibilityDispatch(t *loopTurn) bool {
	if t.reason != "" || t.answer != nil && len(t.tools) == 0 {
		return false
	}
	if len(t.tools) != 0 {
		inv := &t.tools[0]
		return !inv.completed && inv.executed == nil && !(inv.started != nil && (inv.correction != nil || inv.started.start.Operation == "unrecognized"))
	}
	return t.paid == nil || !t.paid.counted
}

// Recheck after scheduler selection and before any owner advance or exchange.
// Flags are only a scheduling hint; the prepared native planners fence commits.
func (r *loopRuntime) eligibilityBoundary(t *loopTurn) error {
	s := r.service
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareHistoryLocked(context.Background(), &historyReadSet{}, nil, true, func(*historyReadSet) error {
		err := s.guardEligibility(t.origin, eligibilityWork{turn: t.id}, eligibilityLive, s.p.through)
		r.mu.Lock()
		t.eligibilityHeld = errors.Is(err, session.ErrEligibilityHeld)
		dispatch := eligibilityDispatch(t)
		r.mu.Unlock()
		if !dispatch && errors.Is(err, session.ErrEligibilityHeld) {
			return nil
		}
		return err
	})
}

func (s *Service) sessionEligibility(ctx context.Context, reads *historyReadSet, ids submissionIDs, command uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		match, err := s.findCommand(ctx, reads, command, 0, s.p.through)
		if err != nil || match.found {
			// r.start retains the original request/receipt comparison, including
			// same-ID mismatches. A later hold cannot replace that result.
			return err
		}
		origin := originOf(ids)
		if _, err := s.loopGrantLocked(ctx, reads, origin, "turn.start", []ledger.Resource{eligibilityInspectTarget(origin)}, 0); err != nil {
			return err
		}
		err = s.guardEligibility(origin, eligibilityWork{admission: true}, eligibilityLive, s.p.through)
		var held *EligibilityHeldError
		if errors.As(err, &held) {
			view, filterErr := s.filterEligibility(ctx, reads, origin, eligibilityView(s.p.agents[ids.agent].eligibility, s.p.through))
			if filterErr != nil {
				return filterErr
			}
			return &EligibilityHeldError{AgentID: ids.agent, EligibilityView: view}
		}
		return err
	})
}

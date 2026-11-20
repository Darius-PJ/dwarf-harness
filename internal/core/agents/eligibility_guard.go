package agents

import (
	"context"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

type eligibilityWork struct {
	turn, command                 uuid.UUID
	task                          *ledger.TaskBinding
	admission, binding, taskStart bool
}

// guardEligibility is deliberately beside, not inside, the continuation guard:
// a turn need not have a loop owner to be ordinary work.
func (s *Service) guardEligibility(origin ledger.Origin, work eligibilityWork, through uint64) error {
	agent, found := s.p.agents[origin.AgentID]
	if !found || agent.session != origin.SessionID || s.p.contexts[origin.ContextID] != origin.AgentID {
		return ErrNotBound
	}
	e := agent.eligibility
	v := e.current()
	if v.Version == 0 || v.State == "eligible" && e.outcome == "recorded" {
		return nil
	}
	if v.State == "first_action_required" && e.outcome == "recorded" && v.FirstAction != nil {
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
				if work.turn == turn.TurnID && work.command == turn.AdmissionCommandID {
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
func (s *Service) guardEligibilityBind(origin ledger.Origin, turn, admission uuid.UUID, through uint64) error {
	if s.p.agents[origin.AgentID].eligibility.current().State == "ordinary_work_ineligible" {
		return nil
	}
	return s.guardEligibility(origin, eligibilityWork{turn: turn, command: admission, binding: true}, through)
}

func eligibilityEffect(operation string) bool {
	return operation == "private.write"
}

func (s *Service) guardEligibilityEffect(ctx context.Context, reads *historyReadSet, origin ledger.Origin, command uuid.UUID, through uint64) error {
	turn, err := s.promisedContinuation(ctx, reads, command, through)
	if err != nil {
		return err
	}
	return s.guardEligibility(origin, eligibilityWork{turn: turn}, through)
}

func (s *Service) installEligibilityFlagsLocked() {
	if r := s.loop; r != nil {
		r.mu.Lock()
		changed := false
		for _, t := range r.turns {
			held := s.guardEligibility(t.origin, eligibilityWork{turn: t.id}, s.p.through) != nil
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
		err := s.guardEligibility(t.origin, eligibilityWork{turn: t.id}, s.p.through)
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

// classifyFirstAction is live-only input verification. The shared native and
// replay planners enforce the same ledger tuple without reading private bytes.
func (s *Service) classifyFirstAction(origin ledger.Origin, command uuid.UUID, input session.MessageSend, task *ledger.TaskBinding) (*ledger.RuntimeEligibilityChanged, error) {
	e := s.p.agents[origin.AgentID].eligibility
	v := e.current()
	if v.State == "first_action_required" && e.outcome == "recorded" && v.FirstAction != nil {
		turn := v.FirstAction.PermittedTurn
		if turn.ContextID == origin.ContextID && turn.TurnID == input.TurnID && turn.StartCommandID == command && sameScopedValue(turn.TaskBinding, task) {
			digest, err := s.eligibilityInputDigest(origin, command, input, task)
			if err != nil {
				return nil, err
			}
			if digest == turn.InputSHA256 {
				return &v, nil
			}
		}
	}
	return nil, s.guardEligibility(origin, eligibilityWork{admission: true}, s.p.through)
}

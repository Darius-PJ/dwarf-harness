package agents

import (
	"context"
	"crypto/sha256"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func isEligibilityType(typ string) bool {
	return typ == ledger.TypeRuntimeEligibilityChanged || typ == ledger.TypeRuntimeEligibilityReceipt
}

func decodeEligibilityGroup(d []ledger.Draft, first uint64) (*eligibilityGroup, error) {
	bad := func() (*eligibilityGroup, error) { return nil, inconsistent(first, "eligibility group") }
	if len(d) != 3 || d[0].Type != ledger.TypeCommand || d[1].Type != ledger.TypeRuntimeEligibilityChanged || d[2].Type != ledger.TypeRuntimeEligibilityReceipt {
		return bad()
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad()
	}
	change, err := ledger.DecodeRuntimeEligibilityChanged(d[1].Payload)
	if err != nil {
		return bad()
	}
	receipt, err := ledger.DecodeRuntimeEligibilityReceipt(d[2].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 || receipt.EffectSequence != int64(first+1) {
		return bad()
	}
	sub := receipt.Submission
	raw, err := sub.Encode()
	if err != nil || ledger.Digest(sha256.Sum256(raw)) != command.SubmissionSHA256 {
		return bad()
	}
	for i, r := range d {
		actor := ledger.Actor(sub.Actor)
		if i == 1 {
			actor = ledger.ActorCore
		}
		if r.TypeVersion != 1 || r.Actor != actor || r.AgentID != sub.Origin.AgentID || r.ExecutionSessionID != sub.Origin.SessionID || r.ContextID != sub.Origin.ContextID || r.TurnID != (uuid.UUID{}) || r.OperationID != command.CommandID || r.MeasurementPath != "" || len(r.BodyRefs) != 0 {
			return bad()
		}
	}
	return &eligibilityGroup{ids: submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}, first: first, command: command, change: change, receipt: receipt}, nil
}

func (s *Service) planEligibility(ctx context.Context, reads *historyReadSet, g *eligibilityGroup, first uint64) (eligibilityState, error) {
	next, err := s.deriveEligibility(ctx, reads, g.command.CommandID, g.receipt.Submission, first-1)
	if err != nil {
		return eligibilityState{}, err
	}
	if !sameScopedValue(next.snapshot, g.change) {
		return eligibilityState{}, ErrInvalid
	}
	next.sequence, next.outcome = first+1, g.receipt.Outcome
	return next, nil
}

// deriveEligibility is pure: even a cold historical read discards the whole
// proposed edge and retries it at the newly accepted prefix.
func (s *Service) deriveEligibility(ctx context.Context, reads *historyReadSet, command uuid.UUID, sub ledger.EligibilitySubmission, through uint64) (eligibilityState, error) {
	zero := eligibilityState{}
	if _, err := sub.Encode(); err != nil || !validID(command) {
		return zero, ErrInvalid
	}
	agent, ok := s.p.agents[sub.Origin.AgentID]
	if !ok || agent.session != sub.Origin.SessionID || agent.general != sub.Origin.ContextID {
		return zero, ErrNotBound
	}
	next := agent.eligibility
	old := next.current()
	if sub.ExpectedVersion != old.Version {
		return zero, &RevisionError{Expected: uint64(sub.ExpectedVersion), Current: uint64(old.Version)}
	}
	if old.Version != 0 && next.outcome != "recorded" {
		return zero, ErrUnavailable
	}
	if old.State != "eligible" {
		if sub.ExpectedOperationID != old.OperationID {
			return zero, ErrEligibilityStale
		}
	} else if sub.Operation != "eligibility.release" && sub.ExpectedOperationID != (uuid.UUID{}) {
		return zero, ErrEligibilityStale
	}
	change := old
	change.ExpectedVersion, change.Version = old.Version, old.Version+1
	switch a := sub.Arguments.(type) {
	case ledger.EligibilityHoldArguments:
		switch old.State {
		case "eligible":
			change.OperationID, change.FirstAction = command, nil
			next = eligibilityState{}
		case "first_action_required":
		case "ordinary_work_ineligible":
			if old.Reason == a.Reason {
				return zero, ErrNoEffect
			}
			return zero, ErrEligibilityStale
		default:
			return zero, ErrEligibilityStale
		}
		change.State, change.Reason = "ordinary_work_ineligible", a.Reason
	case ledger.EligibilityReleaseArguments:
		if sub.ExpectedOperationID != old.OperationID {
			return zero, ErrEligibilityStale
		}
		if old.State == "eligible" {
			return zero, ErrNoEffect
		}
		change.State, change.Reason = "eligible", "none"
	case ledger.EligibilityRequireFirstArguments:
		if old.State != "eligible" && (old.State != "ordinary_work_ineligible" || old.FirstAction != nil) {
			return zero, ErrEligibilityStale
		}
		if a.Turn.StartCommandID == command || a.Turn.AdmissionCommandID == command {
			return zero, ErrEligibilityStale
		}
		if err := s.checkEligibilityDeclaration(ctx, reads, sub.Origin, a.Turn, nil, through); err != nil {
			return zero, err
		}
		if old.State == "eligible" {
			change.OperationID = command
		}
		next = eligibilityState{}
		change.State, change.Reason = "first_action_required", a.Reason
		change.FirstAction = &ledger.EligibilityFirstAction{ActionID: a.ActionID, InitialTurn: a.Turn, PermittedTurn: a.Turn}
	case ledger.EligibilityFirstNextArguments:
		if old.State != "first_action_required" || old.FirstAction == nil || old.FirstAction.ActionID != a.ActionID {
			return zero, ErrEligibilityStale
		}
		ended := next.nativeEndingSequence != 0 && (next.boundSequence == 0 || next.loopEndingSequence != 0 && next.ownerTerminalSequence != 0)
		if !ended && !(next.nativeSequence == 0 && eligibilityRefusalSupported(old.FirstAction.Refusal)) {
			return zero, ErrEligibilityStale
		}
		prior := old.FirstAction.PermittedTurn
		if a.Turn.ContextID != prior.ContextID || !sameScopedValue(a.Turn.TaskBinding, prior.TaskBinding) {
			return zero, ErrEligibilityStale
		}
		if a.Turn.StartCommandID == command || a.Turn.AdmissionCommandID == command {
			return zero, ErrEligibilityStale
		}
		if err := s.checkEligibilityDeclaration(ctx, reads, sub.Origin, a.Turn, old.FirstAction, through); err != nil {
			return zero, err
		}
		change.FirstAction.PermittedTurn, change.FirstAction.Refusal = a.Turn, nil
		next.nativeSequence, next.boundSequence = 0, 0
		next.nativeEndingSequence, next.loopEndingSequence, next.ownerTerminalSequence = 0, 0, 0
	case ledger.EligibilityFirstRefusedArguments:
		if old.State != "first_action_required" || old.FirstAction == nil || old.FirstAction.ActionID != a.ActionID || old.FirstAction.PermittedTurn.TurnID != a.TurnID || old.FirstAction.PermittedTurn.StartCommandID != a.StartCommandID || next.nativeSequence != 0 || next.boundSequence != 0 {
			return zero, ErrEligibilityStale
		}
		refusal := &ledger.EligibilityRefusal{TurnID: a.TurnID, StartCommandID: a.StartCommandID, Stage: a.Stage, Code: a.Code}
		if sameScopedValue(old.FirstAction.Refusal, refusal) {
			return zero, ErrNoEffect
		}
		change.FirstAction.Refusal = refusal
	default:
		return zero, ErrInvalid
	}
	if _, err := change.Encode(); err != nil {
		return zero, ErrInvalid
	}
	next.snapshot = change
	return next, nil
}

func (s *Service) checkEligibilityDeclaration(ctx context.Context, reads *historyReadSet, origin ledger.Origin, turn ledger.EligibilityTurn, prior *ledger.EligibilityFirstAction, through uint64) error {
	if s.p.contexts[turn.ContextID] != origin.AgentID {
		return ErrNotBound
	}
	agent := s.p.agents[origin.AgentID]
	if turn.ContextID == agent.general {
		if turn.TaskBinding != nil {
			return ErrInvalid
		}
	} else {
		if turn.TaskBinding == nil {
			return ErrInvalid
		}
		task, found, err := s.loadTask(ctx, reads, turn.TaskBinding.TaskID, through)
		if err != nil {
			return err
		}
		if !found || task.record.Assignment == nil || terminalTask(task.record.State) {
			return ErrEligibilityStale
		}
		a := task.record.Assignment
		if a.Assignee.AgentID != origin.AgentID || a.Assignee.ContextID != turn.ContextID || a.AssignmentID != turn.TaskBinding.AssignmentID || a.Generation != turn.TaskBinding.Generation {
			return ErrEligibilityStale
		}
	}
	if _, found, err := s.loadWork(ctx, reads, turn.TurnID, through); err != nil {
		return err
	} else if found {
		return ErrEligibilityStale
	}
	for _, id := range []uuid.UUID{turn.StartCommandID, turn.AdmissionCommandID} {
		if used, err := s.commandUsed(ctx, reads, id, through); err != nil {
			return err
		} else if used {
			return ErrEligibilityStale
		}
	}
	if prior != nil {
		// Retain no lifetime declaration map: first_next is cold administrative
		// work and uses the complete, checked occurrence directory.
		used, err := foldDirectory(ctx, s, reads, directoryKey{kind: directoryEligibility, id: origin.AgentID}, 0, through, directoryReadSelection{Purpose: "eligibility_declaration"}, func(used *bool, c ledger.Commit) error {
			for _, r := range c.Records {
				if r.Type != ledger.TypeRuntimeEligibilityChanged || r.AgentID != origin.AgentID {
					continue
				}
				v, err := ledger.DecodeRuntimeEligibilityChanged(r.Payload)
				if err != nil {
					return err
				}
				if v.FirstAction == nil || v.FirstAction.ActionID != prior.ActionID {
					continue
				}
				p := v.FirstAction.PermittedTurn
				if p.TurnID == turn.TurnID || p.StartCommandID == turn.StartCommandID || p.StartCommandID == turn.AdmissionCommandID || p.AdmissionCommandID == turn.StartCommandID || p.AdmissionCommandID == turn.AdmissionCommandID || p.InputSHA256 == turn.InputSHA256 {
					*used = true
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if used {
			return ErrEligibilityStale
		}
	}
	return nil
}

// installEligibilitySources runs in the accepted commit's sole installer. No
// ending, settlement or receipt can change the gate; these are source positions
// for the declaration's view and its next-turn edge only.
func (s *Service) installEligibilitySources(match commandMatch) {
	var origin ledger.Origin
	switch {
	case match.scoped != nil:
		origin = match.scoped.receipt.Submission.Origin
	case match.loop != nil:
		origin = match.loop.receipt.Submission.Origin
	case match.continuation != nil:
		origin = match.continuation.receipt.Submission.Origin
	default:
		return
	}
	agent := s.p.agents[origin.AgentID]
	e := agent.eligibility
	first := e.snapshot.FirstAction
	if first == nil {
		return
	}
	turn := first.PermittedTurn
	if origin.SessionID != agent.session || origin.ContextID != turn.ContextID {
		return
	}
	if g := match.scoped; g != nil {
		if g.admission != nil && g.admission.Kind == "turn" && g.admission.WorkID == turn.TurnID && g.command.CommandID == turn.AdmissionCommandID {
			e.nativeSequence = g.first + uint64(g.effectIndex)
			if e.firstAdmittedSequence == 0 {
				e.firstAdmitted, e.firstAdmittedSequence = turn, e.nativeSequence
			}
		}
		if g.ending != nil && g.ending.WorkID == turn.TurnID && releasesWork("turn", g.ending.Outcome) {
			e.nativeEndingSequence = g.first + uint64(g.effectIndex)
		}
	}
	if g := match.loop; g != nil && g.receipt.Submission.TurnID == turn.TurnID {
		if g.bound != nil {
			e.boundSequence = g.first + 1
		}
		if g.ending != nil {
			e.loopEndingSequence = uint64(g.receipt.EffectSequence)
		}
	}
	if g := match.continuation; g != nil && g.change.TurnID == turn.TurnID && g.change.State == "terminal" && g.receipt.Outcome == "recorded" {
		e.ownerTerminalSequence = g.first + 1
	}
	agent.eligibility = e
	s.p.agents[origin.AgentID] = agent
}

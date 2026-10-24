package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func subjectValue(subject *ledger.SubjectRef) ledger.SubjectRef {
	if subject == nil {
		return ledger.SubjectRef{}
	}
	return *subject
}
func subjectPointer(subject ledger.SubjectRef) *ledger.SubjectRef {
	if emptySubject(subject) {
		return nil
	}
	return &subject
}

func taskRules(p ledger.RuntimeTaskChanged) taskState {
	state := taskState{id: p.TaskID, version: uint64(p.Version), state: p.State, audience: p.Audience, dependencies: p.Dependencies, waitReason: p.WaitReason, waitSubject: subjectValue(p.WaitArtifact), submission: subjectValue(p.Submission), failureCause: p.FailureCause, failureEvidence: subjectValue(p.FailureEvidence)}
	if p.Assignment != nil {
		state.assignment = taskAssignment{id: p.Assignment.AssignmentID, generation: p.Assignment.Generation, binding: Binding(p.Assignment.Assignee)}
	}
	return state
}

func (s *Service) ownGrantIDs(origin ledger.Origin) []uuid.UUID {
	ids := []uuid.UUID{}
	for id, grant := range s.p.grants {
		if grant.definition.Grantee == ledger.Binding(bindingOf(origin)) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return ids
}

func (s *Service) anyPermission(ctx context.Context, origin ledger.Origin, operation string, target ledger.Resource, through uint64) (bool, error) {
	for _, id := range s.ownGrantIDs(origin) {
		_, err := s.authorize(ctx, origin, id, operation, []ledger.Resource{target}, nil, 0, through)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, ErrDenied) {
			return false, err
		}
	}
	return false, nil
}

func (s *Service) executionAuthorized(ctx context.Context, task ledger.RuntimeTaskChanged, actor string, through uint64) (bool, error) {
	if task.Assignment == nil {
		return false, nil
	}
	binding := task.Assignment.Assignee
	origin := ledger.Origin{AgentID: binding.AgentID, SessionID: s.p.agents[binding.AgentID].session, ContextID: binding.ContextID}
	if actor != "owner" {
		allowed, err := s.anyPermission(ctx, origin, "task.start", ledger.Resource{Kind: "task", TaskID: task.TaskID}, through)
		if err != nil || !allowed {
			return false, err
		}
	}
	return s.anyPermission(ctx, origin, "turn.start", ledger.Resource{Kind: "context", Binding: binding, Scope: "context", Slots: []string{}}, through)
}

func (s *Service) planTask(ctx context.Context, g *scopedGroup, first uint64) (scopedPlan, []grantRules, error) {
	plan, chain, err := s.deriveTask(ctx, g.receipt.Submission, first)
	if err != nil {
		return scopedPlan{}, nil, err
	}
	if !sameScopedValue(*g.task, plan.task.record) || g.receipt.Version != plan.task.record.Version {
		return scopedPlan{}, nil, ErrInvalid
	}
	return plan, chain, nil
}

func (s *Service) deriveTask(ctx context.Context, sub ledger.ScopedSubmission, first uint64) (scopedPlan, []grantRules, error) {
	plan := scopedPlan{authority: s.p.authority}
	through := first - 1
	var action taskAction
	var creation *ledger.TaskCreateArguments
	switch args := sub.Arguments.(type) {
	case ledger.TaskCreateArguments:
		if args.Description.Bytes > ledgerstore.MaxBodyBytes {
			return plan, nil, ErrTooLarge
		}
		creation = &args
		action = taskAction{action: "create", task: args.TaskID, audience: args.Audience, dependencies: args.Dependencies}
		if args.Assignment != nil {
			action.assignment = *args.Assignment
		}
	case ledger.TaskMutationArguments:
		action = taskAction{action: args.Action, task: args.TaskID, waitReason: args.WaitReason, waitSubject: subjectValue(args.WaitArtifact), submission: subjectValue(args.Submission), failureCause: args.FailureCause, failureEvidence: subjectValue(args.FailureEvidence)}
		if args.NewAssignment != nil {
			action.assignment = *args.NewAssignment
		}
	default:
		return plan, nil, ErrInvalid
	}
	if sub.Target.TaskID != action.task || sub.Operation != "task."+action.action {
		return plan, nil, ErrInvalid
	}
	var chain []grantRules
	var err error
	if sub.Actor == "agent" {
		size := int64(0)
		if creation != nil {
			size = creation.Description.Bytes
		}
		chain, err = s.authorize(ctx, sub.Origin, sub.GrantID, sub.Operation, []ledger.Resource{sub.Target}, nil, size, through)
		if err != nil {
			return plan, nil, err
		}
	} else if sub.GrantID != (uuid.UUID{}) {
		return plan, nil, ErrInvalid
	}
	previous, found, err := s.loadTask(ctx, action.task, through)
	if err != nil {
		return plan, nil, err
	}
	if found && creation != nil && sub.Actor == "agent" {
		return plan, nil, ErrDenied
	}
	current := taskState{}
	if found {
		current = taskRules(previous.record)
	}
	facts := taskFacts{actor: sub.Actor, origin: bindingOf(sub.Origin), authorized: true, subjectAuthorized: true}
	if len(chain) != 0 {
		facts.recipients = chain[0].recipients
	}
	dependencies := current.dependencies
	if creation != nil {
		dependencies = creation.Dependencies
	}
	for _, id := range dependencies {
		dependency, exists, err := s.loadTask(ctx, id, through)
		if err != nil {
			return plan, nil, err
		}
		if exists {
			facts.dependencies = append(facts.dependencies, taskDependency{id: id, state: dependency.record.State})
			plan.dependencies = append(plan.dependencies, dependency)
		}
	}
	if action.action == "start" || action.action == "resume" {
		facts.executionAuthorized, err = s.executionAuthorized(ctx, previous.record, sub.Actor, through)
		if err != nil {
			return plan, nil, err
		}
	}
	if action.assignment != (ledger.AssignmentInput{}) {
		binding := action.assignment.Assignee
		facts.registeredContext = s.p.contexts[binding.ContextID] == binding.AgentID
		if facts.registeredContext && s.p.agents[binding.AgentID].general != binding.ContextID {
			facts.contextKind = "work"
		}
		facts.newAssignmentUnused, facts.freshContext, err = s.assignmentUnused(ctx, action.assignment, through)
		if err != nil {
			return plan, nil, err
		}
	}
	for _, subject := range []*ledger.SubjectRef{subjectPointer(action.waitSubject), subjectPointer(action.submission), subjectPointer(action.failureEvidence)} {
		if err := s.subjectAuthorized(ctx, sub.Actor, sub.Origin, subject, through); err != nil {
			return plan, nil, err
		}
	}
	expected := taskExpectation{version: uint64(sub.Expected.Version), assignment: sub.Expected.AssignmentID}
	if sub.Expected.Generation != nil {
		expected.generation = *sub.Expected.Generation
	}
	if err := guardReviewTask(s.p.reviews[action.task], current, action.action); err != nil {
		return plan, nil, err
	}
	next, err := transitionTask(current, facts, action, expected)
	if err != nil {
		var refusal *taskRefusal
		if errors.As(err, &refusal) {
			switch refusal.reason {
			case "version":
				return plan, nil, &RevisionError{Expected: expected.version, Current: current.version}
			case "assignment":
				return plan, nil, &AssignmentError{ExpectedID: expected.assignment, CurrentID: current.assignment.id, ExpectedGeneration: expected.generation, CurrentGeneration: current.assignment.generation}
			case "authority", "origin", "audience":
				return plan, nil, ErrDenied
			case "subject":
				return plan, nil, fmt.Errorf("%w: submission mismatch", ErrInvalid)
			}
		}
		return plan, nil, err
	}
	record := previous.record
	if creation != nil {
		record = ledger.RuntimeTaskChanged{TaskID: creation.TaskID, CreatorActor: sub.Actor, Creator: sub.Origin, Description: creation.Description, AcceptanceSHA256: creation.AcceptanceSHA256, Dependencies: slices.Clone(creation.Dependencies), Audience: slices.Clone(creation.Audience)}
	}
	if action.assignment != (ledger.AssignmentInput{}) {
		record.Assignment = &ledger.Assignment{AssignmentID: next.assignment.id, Generation: next.assignment.generation, Assignee: ledger.Binding(next.assignment.binding), AssignerActor: sub.Actor, Assigner: sub.Origin}
	}
	record.Action, record.ExpectedVersion, record.Version, record.State = action.action, int64(current.version), int64(next.version), next.state
	record.WaitReason, record.WaitArtifact, record.Submission = next.waitReason, subjectPointer(next.waitSubject), subjectPointer(next.submission)
	record.FailureCause, record.FailureEvidence = next.failureCause, subjectPointer(next.failureEvidence)
	index := uint64(1)
	if creation != nil {
		index = 2
	}
	source := previous.descriptionSequence
	if creation != nil {
		source = first + index
	}
	plan.task = &scopedTaskState{record: record, sequence: first + index, descriptionSequence: source}
	return plan, chain, nil
}

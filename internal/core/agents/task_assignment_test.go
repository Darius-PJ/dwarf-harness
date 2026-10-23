package agents

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"uuid"
)

func TestScopedTaskAssignmentInvariants(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*taskState, *taskFacts, *taskAction, *taskExpectation)
		code   string
	}{
		{"reused_context", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.freshContext = false }, "context"},
		{"unregistered_context", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.registeredContext = false }, "context"},
		{"general_context", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.contextKind = "general" }, "context"},
		{"old_context", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			a.assignment.Assignee.ContextID = s.assignment.binding.ContextID
		}, "context"},
		{"reused_assignment", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.newAssignmentUnused = false }, "assignment"},
		{"same_assignment", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			a.assignment.AssignmentID = s.assignment.id
		}, "assignment"},
		{"generation_overflow", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.assignment.generation = math.MaxInt64
			e.generation = math.MaxInt64
		}, "assignment"},
		{"outside_audience", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.audience = []uuid.UUID{privateID(4)}
		}, "audience"},
		{"outside_recipients", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.recipients = nil }, "audience"},
		{"claim_other_agent", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.assignment = taskAssignment{}
			*e = taskExpectation{version: s.version}
			a.action = "claim"
			f.origin.AgentID = privateID(4)
		}, "origin"},
		{"claim_already_assigned", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { a.action = "claim" }, "edge"},
		{"first_assignment_expectation", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.assignment = taskAssignment{}
			a.action = "assign"
		}, "assignment"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			s, f := taskRuleFixture()
			a := taskAction{action: "reassign", task: s.id, assignment: taskAssignmentInput{AssignmentID: privateID(52), Assignee: scopedBinding{privateID(1), privateID(3)}}}
			e := taskExpectation{s.version, s.assignment.id, s.assignment.generation}
			row.change(&s, &f, &a, &e)
			next, err := transitionTask(s, f, a, e)
			var refusal *taskRefusal
			if !errors.As(err, &refusal) || refusal.reason != row.code || !reflect.DeepEqual(next, s) {
				t.Fatalf("next=%+v err=%v, want %s", next, err, row.code)
			}
		})
	}
}

func TestScopedTaskCreationInvariants(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*taskFacts, *taskAction)
		code   string
	}{
		{"valid_existing_dependency", func(f *taskFacts, a *taskAction) {}, ""},
		{"self_dependency", func(f *taskFacts, a *taskAction) { a.dependencies = []uuid.UUID{a.task} }, "definition"},
		{"duplicate_dependency", func(f *taskFacts, a *taskAction) { a.dependencies = []uuid.UUID{privateID(60), privateID(60)} }, "definition"},
		{"missing_dependency", func(f *taskFacts, a *taskAction) { f.dependencies = nil }, "dependency"},
		{"empty_audience", func(f *taskFacts, a *taskAction) { a.audience = nil }, "definition"},
		{"duplicate_audience", func(f *taskFacts, a *taskAction) { a.audience = []uuid.UUID{privateID(1), privateID(1)} }, "definition"},
		{"audience_outside_grant", func(f *taskFacts, a *taskAction) { a.audience = []uuid.UUID{privateID(99)} }, "audience"},
		{"owner_separate_authority", func(f *taskFacts, a *taskAction) { f.actor = "owner"; f.recipients = nil }, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			_, facts := taskRuleFixture()
			facts.dependencies = []taskDependency{{id: privateID(60), state: "waiting"}}
			action := taskAction{action: "create", task: privateID(50), audience: []uuid.UUID{privateID(1)}, dependencies: []uuid.UUID{privateID(60)}}
			row.change(&facts, &action)
			next, err := transitionTask(taskState{}, facts, action, taskExpectation{})
			if row.code == "" {
				if err != nil || next.state != "waiting" || next.version != 1 || !reflect.DeepEqual(next.dependencies, action.dependencies) {
					t.Fatalf("create=%+v, %v", next, err)
				}
				action.audience[0] = privateID(100)
				if next.audience[0] == privateID(100) {
					t.Fatal("task audience aliases caller input")
				}
			} else {
				var refusal *taskRefusal
				if !errors.As(err, &refusal) || refusal.reason != row.code || !reflect.DeepEqual(next, taskState{}) {
					t.Fatalf("create=%+v, %v; want %s", next, err, row.code)
				}
			}
		})
	}
}

func TestScopedTaskWaitReasonsAndDependencyResume(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"dependency", "input", "review", "explicit_hold"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			s, f := taskRuleFixture()
			s.state = "active"
			e := taskExpectation{s.version, s.assignment.id, s.assignment.generation}
			next, err := transitionTask(s, f, taskAction{action: "wait", task: s.id, waitReason: reason}, e)
			if err != nil || next.state != "waiting" || next.waitReason != reason {
				t.Fatalf("wait=%+v, %v", next, err)
			}
			next.dependencies = []uuid.UUID{privateID(60)}
			e.version = next.version
			if _, err := transitionTask(next, f, taskAction{action: "resume", task: s.id}, e); err == nil {
				t.Fatal("resume bypassed dependency")
			}
			f.dependencies = []taskDependency{{id: privateID(60), state: "completed"}}
			resumed, err := transitionTask(next, f, taskAction{action: "resume", task: s.id}, e)
			if err != nil || resumed.waitReason != "" || resumed.state != "waiting" {
				t.Fatalf("resume=%+v, %v", resumed, err)
			}
		})
	}
}

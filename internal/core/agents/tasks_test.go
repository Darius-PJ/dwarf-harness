package agents

import (
	"crypto/sha256"
	"errors"
	"math"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func taskRuleFixture() (taskState, taskFacts) {
	return taskState{
		id: privateID(50), version: 3, state: "waiting", audience: []uuid.UUID{privateID(1), privateID(4)},
		assignment: taskAssignment{id: privateID(51), generation: 1, binding: Binding{privateID(1), privateID(2)}},
	}, taskFacts{
		actor: "agent", origin: Binding{privateID(1), privateID(2)}, authorized: true, executionAuthorized: true,
		freshContext: true, contextKind: "work", registeredContext: true, newAssignmentUnused: true, subjectAuthorized: true,
		recipients: []Binding{{privateID(1), privateID(3)}, {privateID(4), privateID(5)}},
	}
}

func taskArtifact() ledger.SubjectRef {
	return ledger.SubjectRef{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 4, SHA256: ledger.Digest(sha256.Sum256([]byte("artifact"))), Bytes: 8}}
}

func TestScopedTaskAllowedEdges(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		action, from, to string
		assigned         bool
	}{
		{"create", "", "waiting", false}, {"create", "", "waiting", true},
		{"claim", "waiting", "waiting", false}, {"assign", "waiting", "waiting", false},
		{"start", "waiting", "active", true}, {"wait", "active", "waiting", true},
		{"resume", "waiting", "waiting", true}, {"submit", "active", "submitted", true},
		{"complete", "submitted", "completed", true},
		{"fail", "waiting", "failed", true}, {"fail", "active", "failed", true}, {"fail", "submitted", "failed", true},
		{"cancel", "waiting", "cancelled", false}, {"cancel", "waiting", "cancelled", true}, {"cancel", "active", "cancelled", true}, {"cancel", "submitted", "cancelled", true},
		{"reassign", "waiting", "waiting", true}, {"reassign", "active", "waiting", true}, {"reassign", "submitted", "waiting", true},
	} {
		t.Run(row.action+"_"+row.from+"_"+map[bool]string{true: "assigned", false: "unassigned"}[row.assigned], func(t *testing.T) {
			t.Parallel()
			state, facts := taskRuleFixture()
			state.state = row.from
			if !row.assigned {
				state.assignment = taskAssignment{}
			}
			if row.from == "submitted" {
				state.submission = taskArtifact()
			}
			if row.action == "resume" {
				state.waitReason, state.waitSubject = "input", taskArtifact()
			}
			if row.action == "reassign" && row.from == "waiting" {
				state.waitReason, state.waitSubject = "review", taskArtifact()
			}
			request := taskAction{action: row.action, task: state.id}
			switch row.action {
			case "create":
				state = taskState{}
				request.task, request.audience = privateID(50), []uuid.UUID{privateID(1), privateID(4)}
				if row.assigned {
					request.assignment = ledger.AssignmentInput{AssignmentID: privateID(52), Assignee: ledger.Binding{AgentID: privateID(1), ContextID: privateID(3)}}
				}
			case "claim", "assign", "reassign":
				request.assignment = ledger.AssignmentInput{AssignmentID: privateID(52), Assignee: ledger.Binding{AgentID: privateID(1), ContextID: privateID(3)}}
			case "wait":
				request.waitReason, request.waitSubject = "input", taskArtifact()
			case "submit", "complete":
				request.submission = taskArtifact()
			case "fail":
				request.failureCause, request.failureEvidence = "tool_failed", taskArtifact()
			}
			expected := taskExpectation{version: state.version, assignment: state.assignment.id, generation: state.assignment.generation}
			before := state
			next, err := transitionTask(state, facts, request, expected)
			if err != nil || next.state != row.to || next.version != state.version+1 {
				t.Fatalf("transition = %+v, %v", next, err)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("transition mutated its input")
			}
			if row.action == "reassign" {
				if next.assignment.generation != 2 || next.assignment.id != privateID(52) || next.waitReason != "" || !emptySubject(next.submission) || !emptySubject(next.waitSubject) || next.failureCause != "" {
					t.Fatalf("reassignment retained old work: %+v", next)
				}
			} else if request.assignment.AssignmentID != (uuid.UUID{}) && next.assignment.generation != 1 {
				t.Fatal("first assignment generation is not one")
			}
			if row.action == "resume" && (next.waitReason != "" || !emptySubject(next.waitSubject)) {
				t.Fatal("resume did not clear wait")
			}
			if row.action == "complete" && !equalSubject(next.submission, taskArtifact()) {
				t.Fatal("completion changed exact subject")
			}
		})
	}
}

func TestScopedTaskRefusals(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*taskState, *taskFacts, *taskAction, *taskExpectation)
		code   string
	}{
		{"stale_version", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { e.version-- }, "version"},
		{"stale_assignment", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { e.assignment = privateID(99) }, "assignment"},
		{"stale_generation", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { e.generation++ }, "assignment"},
		{"denied", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.authorized = false }, "authority"},
		{"wrong_context", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			f.origin.ContextID = privateID(99)
		}, "origin"},
		{"unassigned", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.assignment = taskAssignment{}
			e.assignment = uuid.UUID{}
			e.generation = 0
		}, "edge"},
		{"wrong_source", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { s.state = "active" }, "edge"},
		{"completed_terminal", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { s.state = "completed" }, "terminal"},
		{"failed_terminal", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { s.state = "failed" }, "terminal"},
		{"cancelled_terminal", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { s.state = "cancelled" }, "terminal"},
		{"dependency", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.dependencies = []uuid.UUID{privateID(60)}
			f.dependencies = []taskDependency{{id: privateID(60), state: "active"}}
		}, "dependency"},
		{"missing_dependency", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.dependencies = []uuid.UUID{privateID(60)}
		}, "dependency"},
		{"wait_blocks_start", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { s.waitReason = "input" }, "wait"},
		{"execution_grant", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { f.executionAuthorized = false }, "authority"},
		{"resume_no_wait", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { a.action = "resume" }, "wait"},
		{"invalid_reason", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.state = "active"
			a.action = "wait"
			a.waitReason = "later"
		}, "arguments"},
		{"missing_submission", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.state = "active"
			a.action = "submit"
		}, "subject"},
		{"unknown_subject", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.state = "active"
			a.action = "submit"
			a.submission = ledger.SubjectRef{Kind: "promotion"}
		}, "subject"},
		{"zero_subject_digest", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.state = "active"
			a.action, a.submission = "submit", taskArtifact()
			a.submission.Artifact.SHA256 = ledger.Digest{}
		}, "subject"},
		{"unauthorized_subject", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.state = "active"
			a.action = "submit"
			a.submission = taskArtifact()
			f.subjectAuthorized = false
		}, "subject"},
		{"wrong_completion_subject", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.state = "submitted"
			s.submission = taskArtifact()
			a.action = "complete"
			a.submission = taskArtifact()
			a.submission.Artifact.Bytes++
		}, "subject"},
		{"missing_failure_evidence", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			a.action = "fail"
			a.failureCause = "failed"
		}, "subject"},
		{"invalid_failure_cause", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			a.action = "fail"
			a.failureEvidence = taskArtifact()
			a.failureCause = "Not valid"
		}, "arguments"},
		{"irrelevant_field", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) { a.failureCause = "ignored" }, "arguments"},
		{"version_overflow", func(s *taskState, f *taskFacts, a *taskAction, e *taskExpectation) {
			s.version = math.MaxInt64
			e.version = s.version
		}, "version"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			state, facts := taskRuleFixture()
			action := taskAction{action: "start", task: state.id}
			expected := taskExpectation{state.version, state.assignment.id, state.assignment.generation}
			row.change(&state, &facts, &action, &expected)
			next, err := transitionTask(state, facts, action, expected)
			var refusal *taskRefusal
			if !errors.As(err, &refusal) || refusal.reason != row.code || !reflect.DeepEqual(next, state) {
				t.Fatalf("refusal = %+v, %v; want %s and unchanged state", next, err, row.code)
			}
		})
	}
}

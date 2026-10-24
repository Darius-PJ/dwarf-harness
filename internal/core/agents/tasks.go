package agents

import (
	"bytes"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type taskAssignment struct {
	id         uuid.UUID
	generation int64
	binding    Binding
}

type taskState struct {
	id                      uuid.UUID
	version                 uint64
	state                   string
	audience, dependencies  []uuid.UUID
	assignment              taskAssignment
	waitReason              string
	waitSubject, submission ledger.SubjectRef
	failureCause            string
	failureEvidence         ledger.SubjectRef
}

type taskDependency struct {
	id    uuid.UUID
	state string
}

// Facts are observations the caller gathers under its state lock. They are not
// an authority cache: the eventual producer must refresh them before commit.
type taskFacts struct {
	actor                                  string
	origin                                 Binding
	authorized, executionAuthorized        bool
	dependencies                           []taskDependency
	recipients                             []Binding
	registeredContext, freshContext        bool
	contextKind                            string
	newAssignmentUnused, subjectAuthorized bool
	// Set only after proving an exact live failure request and both current
	// participant authorities. It relaxes the fail origin, not authorization.
	reviewedFailure bool
}

type taskExpectation struct {
	version    uint64
	assignment uuid.UUID
	generation int64
}

type taskAction struct {
	action                  string
	task                    uuid.UUID
	audience, dependencies  []uuid.UUID
	assignment              ledger.AssignmentInput
	waitReason              string
	waitSubject, submission ledger.SubjectRef
	failureCause            string
	failureEvidence         ledger.SubjectRef
}

type taskRefusal struct{ reason string }

func (e *taskRefusal) Error() string { return "agents: task " + e.reason }
func (e *taskRefusal) Unwrap() error { return ErrInvalid }

// transitionTask changes no input and returns the original state on refusal.
// It declares task state only; it starts no turn, performs no review and releases
// no work reservation. Future review guards must precede this same completion.
func transitionTask(current taskState, facts taskFacts, action taskAction, expected taskExpectation) (taskState, error) {
	refuse := func(reason string) (taskState, error) { return current, &taskRefusal{reason} }
	if expected.version != current.version || current.version >= math.MaxInt64 {
		return refuse("version")
	}
	if expected.assignment != current.assignment.id || expected.generation != current.assignment.generation {
		return refuse("assignment")
	}
	if !facts.authorized || (facts.actor != "owner" && facts.actor != "agent") {
		return refuse("authority")
	}
	if !validID(action.task) || current.version != 0 && action.task != current.id {
		return refuse("identity")
	}
	if !validTaskArguments(action) {
		return refuse("arguments")
	}
	if slices.Contains([]string{"completed", "failed", "cancelled"}, current.state) {
		return refuse("terminal")
	}
	next := current
	assigned := current.assignment.id != (uuid.UUID{})
	if action.action == "create" {
		if current.version != 0 || current.state != "" || assigned {
			return refuse("edge")
		}
		if len(action.audience) == 0 || !strictIDs(action.audience) || !strictIDs(action.dependencies) || slices.Contains(action.dependencies, action.task) {
			return refuse("definition")
		}
		for _, id := range action.dependencies {
			if !slices.ContainsFunc(facts.dependencies, func(d taskDependency) bool { return d.id == id }) {
				return refuse("dependency")
			}
		}
		if facts.actor == "agent" && !audienceContained(action.audience, facts.recipients) {
			return refuse("audience")
		}
		next.id, next.state = action.task, "waiting"
		next.audience, next.dependencies = slices.Clone(action.audience), slices.Clone(action.dependencies)
	} else {
		if current.version == 0 {
			return refuse("edge")
		}
		if facts.actor == "agent" && slices.Contains([]string{"start", "wait", "resume", "submit", "fail"}, action.action) && assigned && facts.origin != current.assignment.binding &&
			!(action.action == "fail" && facts.reviewedFailure) {
			return refuse("origin")
		}
		switch action.action {
		case "claim", "assign":
			if current.state != "waiting" || assigned {
				return refuse("edge")
			}
		case "start", "resume":
			if current.state != "waiting" || !assigned {
				return refuse("edge")
			}
			if action.action == "start" && current.waitReason != "" || action.action == "resume" && current.waitReason == "" {
				return refuse("wait")
			}
			if !facts.executionAuthorized {
				return refuse("authority")
			}
			for _, id := range current.dependencies {
				if !slices.ContainsFunc(facts.dependencies, func(d taskDependency) bool { return d.id == id && d.state == "completed" }) {
					return refuse("dependency")
				}
			}
			next.waitReason, next.waitSubject = "", ledger.SubjectRef{}
			if action.action == "start" {
				next.state = "active"
			}
		case "wait":
			if current.state != "active" || !assigned {
				return refuse("edge")
			}
			next.state, next.waitReason, next.waitSubject = "waiting", action.waitReason, action.waitSubject
		case "submit":
			if current.state != "active" || !assigned {
				return refuse("edge")
			}
			next.state, next.submission = "submitted", action.submission
		case "complete":
			if current.state != "submitted" || !assigned {
				return refuse("edge")
			}
			if !equalSubject(current.submission, action.submission) {
				return refuse("subject")
			}
			next.state = "completed"
		case "fail":
			if !assigned || !slices.Contains([]string{"waiting", "active", "submitted"}, current.state) {
				return refuse("edge")
			}
			next.state, next.failureCause, next.failureEvidence = "failed", action.failureCause, action.failureEvidence
			next.waitReason, next.waitSubject, next.submission = "", ledger.SubjectRef{}, ledger.SubjectRef{}
		case "cancel":
			if !slices.Contains([]string{"waiting", "active", "submitted"}, current.state) {
				return refuse("edge")
			}
			next.state, next.waitReason, next.waitSubject, next.submission = "cancelled", "", ledger.SubjectRef{}, ledger.SubjectRef{}
		case "reassign":
			if !assigned || !slices.Contains([]string{"waiting", "active", "submitted"}, current.state) {
				return refuse("edge")
			}
			next.state, next.waitReason, next.waitSubject, next.submission, next.failureCause, next.failureEvidence = "waiting", "", ledger.SubjectRef{}, ledger.SubjectRef{}, "", ledger.SubjectRef{}
		default:
			return refuse("edge")
		}
	}
	if action.assignment != (ledger.AssignmentInput{}) {
		binding := Binding{action.assignment.Assignee.AgentID, action.assignment.Assignee.ContextID}
		if !validID(action.assignment.AssignmentID) || !validID(binding.AgentID) || !validID(binding.ContextID) || !facts.newAssignmentUnused || action.assignment.AssignmentID == current.assignment.id {
			return refuse("assignment")
		}
		if !facts.registeredContext || !facts.freshContext || facts.contextKind != "work" || assigned && binding.ContextID == current.assignment.binding.ContextID {
			return refuse("context")
		}
		if !slices.Contains(next.audience, binding.AgentID) || facts.actor == "agent" && !slices.Contains(facts.recipients, binding) {
			return refuse("audience")
		}
		if action.action == "claim" && (facts.actor != "agent" || facts.origin.AgentID != binding.AgentID) {
			return refuse("origin")
		}
		if current.assignment.generation == math.MaxInt64 {
			return refuse("assignment")
		}
		next.assignment = taskAssignment{id: action.assignment.AssignmentID, generation: current.assignment.generation + 1, binding: binding}
	}
	for _, subject := range []ledger.SubjectRef{action.waitSubject, action.submission, action.failureEvidence} {
		if !emptySubject(subject) && (!validTaskSubject(subject) || !facts.subjectAuthorized) {
			return refuse("subject")
		}
	}
	if (action.action == "submit" || action.action == "complete") && emptySubject(action.submission) || action.action == "fail" && emptySubject(action.failureEvidence) {
		return refuse("subject")
	}
	next.version++
	return next, nil
}

func audienceContained(audience []uuid.UUID, recipients []Binding) bool {
	for _, agent := range audience {
		if !slices.ContainsFunc(recipients, func(b Binding) bool { return b.AgentID == agent }) {
			return false
		}
	}
	return true
}

func validTaskArguments(a taskAction) bool {
	assigning := a.action == "create" || a.action == "claim" || a.action == "assign" || a.action == "reassign"
	if !assigning && a.assignment != (ledger.AssignmentInput{}) || assigning && a.action != "create" && a.assignment == (ledger.AssignmentInput{}) {
		return false
	}
	if a.action != "create" && (len(a.audience) != 0 || len(a.dependencies) != 0) {
		return false
	}
	if a.action == "wait" {
		if !slices.Contains([]string{"dependency", "input", "review", "explicit_hold"}, a.waitReason) {
			return false
		}
	} else if a.waitReason != "" || !emptySubject(a.waitSubject) {
		return false
	}
	if a.action != "submit" && a.action != "complete" && !emptySubject(a.submission) {
		return false
	}
	if a.action == "fail" {
		if !validScopedToken(a.failureCause) {
			return false
		}
	} else if a.failureCause != "" || !emptySubject(a.failureEvidence) {
		return false
	}
	return true
}

func validTaskSubject(subject ledger.SubjectRef) bool {
	return subject.Kind == "artifact" && subject.Members == nil && validResource(ledger.Resource{Kind: "artifact", Artifact: subject.Artifact})
}

func equalSubject(a, b ledger.SubjectRef) bool {
	return a.Kind == b.Kind && a.Artifact == b.Artifact && bytes.Equal(a.Members, b.Members)
}

func emptySubject(subject ledger.SubjectRef) bool {
	return equalSubject(subject, ledger.SubjectRef{})
}

func validScopedToken(token string) bool {
	if len(token) == 0 || len(token) > 64 || token[0] < 'a' || token[0] > 'z' {
		return false
	}
	for _, b := range []byte(token) {
		if b != '_' && (b < 'a' || b > 'z') && (b < '0' || b > '9') {
			return false
		}
	}
	return true
}

func strictIDs(ids []uuid.UUID) bool {
	for i, id := range ids {
		if !validID(id) || i > 0 && bytes.Compare(ids[i-1][:], id[:]) >= 0 {
			return false
		}
	}
	return true
}

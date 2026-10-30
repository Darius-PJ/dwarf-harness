package agents

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// taskFactsRig composes the existing real-store fixtures; serial IDs are only
// commands, contexts and grants, leaving small explicit task IDs for ordering.
type taskFactsRig struct {
	*serviceFixture
	owner, other Binding
	serial       int
}

func newTaskFactsRig(t *testing.T, deps serviceDeps) *taskFactsRig {
	t.Helper()
	f := &taskFactsRig{serviceFixture: newServiceFixture(t, deps), serial: 2000}
	f.owner = registerPrivate(t, f.service, 100)
	f.other = registerPrivate(t, f.service, 200)
	scopedEnrol(t, f.service, f.owner, scopedCapacity(), 300, f.owner.AgentID, f.other.AgentID)
	return f
}

func (f *taskFactsRig) next() int { f.serial++; return f.serial }

func (f *taskFactsRig) task(t *testing.T, id int, assignee Binding, dependencies []uuid.UUID, audience ...uuid.UUID) TaskView {
	t.Helper()
	q := scopedNewTask(f.next(), id, uuid.UUID{}, audience...)
	q.Dependencies = dependencies
	if assignee != (Binding{}) {
		work := scopedWorkContext(t, f.service, assignee, f.next(), f.next())
		q.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(f.next()), Assignee: ledger.Binding(work)}
	}
	if _, err := f.service.CreateTask(context.Background(), f.owner, q); err != nil {
		t.Fatal(err)
	}
	return f.status(t, q.TaskID)
}

func (f *taskFactsRig) status(t *testing.T, id uuid.UUID) TaskView {
	t.Helper()
	view, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: id})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func (f *taskFactsRig) grant(t *testing.T, grantee Binding, operations []string, ids ...uuid.UUID) GrantSpec {
	t.Helper()
	resources := []ledger.Resource{}
	for _, id := range ids {
		resources = append(resources, ledger.Resource{Kind: "task", TaskID: id})
	}
	g := GrantSpec{GrantID: privateID(f.next()), Grantee: grantee, Operations: operations, Resources: resources, DataClass: "task", Recipients: []Binding{grantee}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	issueScopedRoot(t, f.service, f.owner, f.next(), g)
	return g
}

func (f *taskFactsRig) execution(t *testing.T, view TaskView) GrantSpec {
	t.Helper()
	work := Binding(view.Task.Assignment.Assignee)
	f.grant(t, work, []string{"task.start"}, view.Task.TaskID)
	turn := privateScopedGrant(work, f.next(), scopedCapacity(), "turn.start")
	issueScopedRoot(t, f.service, f.owner, f.next(), turn)
	return turn
}

func (f *taskFactsRig) transition(t *testing.T, view TaskView, action string) TaskView {
	t.Helper()
	if action == "start" {
		work := Binding(view.Task.Assignment.Assignee)
		turn := privateScopedGrant(work, f.next(), scopedCapacity(), "turn.start")
		issueScopedRoot(t, f.service, f.owner, f.next(), turn)
	}
	q := TaskMutation{CommandID: privateID(f.next()), TaskID: view.Task.TaskID, ExpectedVersion: uint64(view.Task.Version), Action: action}
	if a := view.Task.Assignment; a != nil {
		q.ExpectedAssignmentID, q.ExpectedGeneration = a.AssignmentID, a.Generation
	}
	switch action {
	case "wait":
		q.WaitReason = "explicit_hold"
	case "submit", "complete":
		q.Submission = &ledger.SubjectRef{Kind: "artifact", Artifact: view.DescriptionRef}
	case "fail":
		q.FailureCause, q.FailureEvidence = "executor_unavailable", &ledger.SubjectRef{Kind: "artifact", Artifact: view.DescriptionRef}
	}
	if _, err := f.service.TransitionTask(context.Background(), f.owner, q); err != nil {
		t.Fatalf("%s task %s: %v", action, view.Task.TaskID, err)
	}
	return f.status(t, view.Task.TaskID)
}

func taskFactFromView(view TaskView, readiness, reason string) TaskFact {
	return TaskFact{TaskID: view.Task.TaskID, Version: view.Task.Version, State: view.Task.State, Assignment: *view.Task.Assignment, DescriptionRef: view.DescriptionRef, Sequence: view.Sequence, Readiness: readiness, Reason: reason}
}

func assertTaskFacts(t *testing.T, page TaskFactsPage, target uuid.UUID, head uint64, ready, blocked, active []TaskFact) {
	t.Helper()
	want := TaskFactsPage{AgentID: target, ThroughSequence: head, WaitingReady: append([]TaskFact{}, ready...), WaitingBlocked: append([]TaskFact{}, blocked...), Active: append([]TaskFact{}, active...), Counts: TaskFactCounts{len(ready), len(blocked), len(active)}}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("task facts\ngot  %+v\nwant %+v", page, want)
	}
}

func TestTaskFactsBucketsAndAssignments(t *testing.T) {
	t.Parallel()
	// Catches creator/audience ownership, lost claims/delegation, false active
	// state from occupied slots, aliased assignment values and hot terminal rows.
	t.Run("membership_provenance_and_turn_slot", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTaskFactsRig(t, serviceDeps{})
		claim := f.task(t, 400, Binding{}, nil, f.owner.AgentID, f.other.AgentID)
		work := scopedWorkContext(t, f.service, f.owner, f.next(), f.next())
		claimGrant := f.grant(t, work, []string{"task.claim"}, claim.Task.TaskID)
		claimID := privateID(f.next())
		claimer := scopedBound(t, f.service, work)
		receipt, err := claimer.ClaimTask(ctx, TaskAssignment{CommandID: privateID(f.next()), GrantID: claimGrant.GrantID, TaskID: claim.Task.TaskID, ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: claimID, Assignee: ledger.Binding(work)}})
		if err != nil {
			t.Fatal(err)
		}
		claim = f.status(t, claim.Task.TaskID)
		if *claim.Task.Assignment != (ledger.Assignment{AssignmentID: claimID, Generation: 1, Assignee: ledger.Binding(work), AssignerActor: "agent", Assigner: receipt.Origin}) || claim.Task.Version != 2 {
			t.Fatalf("claim provenance: %+v", claim.Task)
		}
		f.execution(t, claim)
		delegated := f.task(t, 410, f.other, nil, f.owner.AgentID, f.other.AgentID)
		f.execution(t, delegated)
		if delegated.Task.Assignment.AssignerActor != "owner" || delegated.Task.Assignment.Assigner != (ledger.Origin{AgentID: f.owner.AgentID, SessionID: privateID(101), ContextID: f.owner.ContextID}) {
			t.Fatalf("delegation provenance: %+v", delegated.Task.Assignment)
		}
		agentDelegated := f.task(t, 415, Binding{}, nil, f.owner.AgentID, f.other.AgentID)
		recipient := scopedWorkContext(t, f.service, f.other, f.next(), f.next())
		assign := GrantSpec{GrantID: privateID(f.next()), Grantee: f.owner, Operations: []string{"task.assign"}, Resources: []ledger.Resource{{Kind: "task", TaskID: agentDelegated.Task.TaskID}}, DataClass: "task", Recipients: []Binding{recipient}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
		issueScopedRoot(t, f.service, f.owner, f.next(), assign)
		assignmentID := privateID(f.next())
		assigned, err := scopedBound(t, f.service, f.owner).AssignTask(ctx, TaskAssignment{CommandID: privateID(f.next()), GrantID: assign.GrantID, TaskID: agentDelegated.Task.TaskID, ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: assignmentID, Assignee: ledger.Binding(recipient)}})
		if err != nil {
			t.Fatal(err)
		}
		agentDelegated = f.status(t, agentDelegated.Task.TaskID)
		if *agentDelegated.Task.Assignment != (ledger.Assignment{AssignmentID: assignmentID, Generation: 1, Assignee: ledger.Binding(recipient), AssignerActor: "agent", Assigner: assigned.Origin}) || agentDelegated.Task.Version != 2 {
			t.Fatalf("other-agent delegation provenance: %+v", agentDelegated.Task)
		}
		f.execution(t, agentDelegated)
		dependency := f.task(t, 420, f.owner, nil, f.owner.AgentID)
		blocked := f.task(t, 430, f.owner, []uuid.UUID{dependency.Task.TaskID}, f.owner.AgentID)
		f.execution(t, blocked)
		dependencyPage, err := f.service.ListTaskFacts(ctx, TaskFactsQuery{AgentID: f.owner.AgentID, Limit: 20})
		if err != nil || !slices.Contains(dependencyPage.WaitingBlocked, taskFactFromView(blocked, "waiting_blocked", "dependency")) {
			t.Fatalf("visible incomplete dependency: %+v %v", dependencyPage, err)
		}
		held := f.task(t, 440, f.owner, nil, f.owner.AgentID)
		held = f.transition(t, f.transition(t, held, "start"), "wait")
		active := f.transition(t, f.task(t, 450, f.owner, nil, f.owner.AgentID), "start")
		f.task(t, 460, Binding{}, nil, f.owner.AgentID)
		submitted := f.transition(t, f.transition(t, f.task(t, 470, f.owner, nil, f.owner.AgentID), "start"), "submit")
		f.transition(t, f.task(t, 480, f.owner, nil, f.owner.AgentID), "cancel")
		f.transition(t, f.task(t, 490, f.owner, nil, f.owner.AgentID), "fail")
		dependency = f.transition(t, f.transition(t, f.transition(t, dependency, "start"), "submit"), "complete")
		if dependency.Task.State != "completed" || submitted.Task.State != "submitted" {
			t.Fatal("fixture state")
		}
		query := func(target uuid.UUID) TaskFactsPage {
			t.Helper()
			page, err := f.service.ListTaskFacts(ctx, TaskFactsQuery{AgentID: target, Limit: f.service.limits.MaxLiveTasks})
			if err != nil {
				t.Fatal(err)
			}
			return page
		}
		assertTaskFacts(t, query(f.owner.AgentID), f.owner.AgentID, f.store.Durable(), []TaskFact{taskFactFromView(claim, "ready", ""), taskFactFromView(blocked, "ready", "")}, []TaskFact{taskFactFromView(held, "waiting_blocked", "explicit_hold")}, []TaskFact{taskFactFromView(active, "active", "")})
		assertTaskFacts(t, query(f.other.AgentID), f.other.AgentID, f.store.Durable(), []TaskFact{taskFactFromView(delegated, "ready", ""), taskFactFromView(agentDelegated, "ready", "")}, nil, nil)
		capacity := scopedCapacity()
		capacity.TurnSlots = 1
		if _, err := f.service.ConfigureCapacity(ctx, f.owner, CapacitySpec{CommandID: privateID(f.next()), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "agent", ID: f.owner.AgentID}, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
		// Admission is durable even though the waiting task was never started.
		turn := privateScopedGrant(f.owner, f.next(), scopedCapacity(), "turn.start")
		issueScopedRoot(t, f.service, f.owner, f.next(), turn)
		turnID := privateID(f.next())
		if _, err := scopedBound(t, f.service, f.owner).StartTurn(ctx, TurnAdmission{CommandID: privateID(f.next()), GrantID: turn.GrantID, TurnID: turnID}); err != nil {
			t.Fatal(err)
		}
		queued := query(f.owner.AgentID)
		assertTaskFacts(t, queued, f.owner.AgentID, f.store.Durable(), []TaskFact{taskFactFromView(claim, "ready_queued", "turn_capacity"), taskFactFromView(blocked, "ready_queued", "turn_capacity")}, []TaskFact{taskFactFromView(held, "waiting_blocked", "explicit_hold")}, []TaskFact{taskFactFromView(active, "active", "")})
		queued.WaitingReady[0].Assignment.Assignee.AgentID = f.other.AgentID
		queued.WaitingReady[0].DescriptionRef.SourceSequence = 1
		if got := query(f.owner.AgentID); got.WaitingReady[0] != taskFactFromView(claim, "ready_queued", "turn_capacity") {
			t.Fatalf("caller mutated projection: %+v", got)
		}
		if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(f.next()), TurnID: turnID, ExpectedVersion: 1, Outcome: "released"}); err != nil {
			t.Fatal(err)
		}
		claim = f.transition(t, claim, "start")
		assertTaskFacts(t, query(f.owner.AgentID), f.owner.AgentID, f.store.Durable(), []TaskFact{taskFactFromView(blocked, "ready", "")}, []TaskFact{taskFactFromView(held, "waiting_blocked", "explicit_hold")}, []TaskFact{taskFactFromView(claim, "active", ""), taskFactFromView(active, "active", "")})
	})
	t.Run("durable_local_slot", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		base, agent, turn, local := scopedLocalFixture(t, DefaultLimits())
		f := &taskFactsRig{serviceFixture: base, owner: Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}, serial: 10000}
		if _, err := agent.StartTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.AdmitLocal(ctx, local); err != nil {
			t.Fatal(err)
		}
		waiting := f.task(t, 8000, f.owner, nil, f.owner.AgentID)
		f.execution(t, waiting)
		scope := ledger.ScopeKey{Kind: "agent", ID: f.owner.AgentID}
		book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		capacity := scopedCapacity()
		capacity.LocalSlots = 1
		if _, err := f.service.ConfigureCapacity(ctx, f.owner, CapacitySpec{CommandID: privateID(f.next()), ExpectedVersion: uint64(book.Version), Scope: scope, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
		page, err := f.service.ListTaskFacts(ctx, TaskFactsQuery{AgentID: f.owner.AgentID, Limit: 20})
		if err != nil || !slices.Contains(page.WaitingReady, taskFactFromView(waiting, "ready_queued", "local_capacity")) {
			t.Fatalf("durable local occupancy: %+v %v", page, err)
		}
	})
}

func TestTaskFactsCommittedReadiness(t *testing.T) {
	t.Parallel()
	// Catches preparation changing same-head readiness, prose/cache authority,
	// and queries that sample a clock, append, charge or retain capacity.
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var armed atomic.Bool
	var sibling Binding
	f := newTaskFactsRig(t, serviceDeps{
		beforePublish: func(b Binding, scope string) error {
			if armed.Load() && b == sibling && scope == "context" {
				close(entered)
				<-release
			}
			return nil
		},
		reviewClock: func() (int64, error) { t.Error("task facts sampled review clock"); return 0, ErrInvalid },
	})
	waiting := f.task(t, 400, f.owner, nil, f.owner.AgentID)
	f.execution(t, waiting)
	inspect := f.grant(t, f.owner, []string{"task.inspect"}, waiting.Task.TaskID)
	sibling = scopedWorkContext(t, f.service, f.owner, f.next(), f.next())
	writeGrant := privateScopedGrant(sibling, f.next(), scopedCapacity(), "private.write")
	issueScopedRoot(t, f.service, f.owner, f.next(), writeGrant)
	capacity := scopedCapacity()
	capacity.LocalSlots = 1
	if _, err := f.service.ConfigureCapacity(ctx, f.owner, CapacitySpec{CommandID: privateID(f.next()), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "agent", ID: f.owner.AgentID}, Limits: capacity}); err != nil {
		t.Fatal(err)
	}
	reader := scopedBound(t, f.service, f.owner)
	q := TaskFactsQuery{AgentID: f.owner.AgentID, GrantID: inspect.GrantID, Limit: 1}
	query := func() TaskFactsPage {
		t.Helper()
		page, err := reader.ListTaskFacts(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	before := query()
	assertTaskFacts(t, before, f.owner.AgentID, f.store.Durable(), []TaskFact{taskFactFromView(waiting, "ready", "")}, nil, nil)
	writer := scopedBound(t, f.service, sibling)
	writeCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	request := ScopedPrivateWrite{CommandID: privateID(f.next()), GrantID: writeGrant.GrantID, Scope: "context", ExpectedVersion: 1, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("not committed")}}}
	armed.Store(true)
	go func() { _, err := writer.WritePrivate(writeCtx, request); result <- err }()
	joined := false
	defer func() {
		cancel()
		once.Do(func() { close(release) })
		if !joined {
			<-result
		}
	}()
	<-entered
	// The old query deliberately still sees preparing, proving this fixture
	// occupies a scope on the waiting assignee's chain.
	old, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: waiting.Task.TaskID})
	if err != nil || old.Readiness != "ready_queued" || old.Reason != "local_capacity" {
		t.Fatalf("preparation fixture: %+v %v", old, err)
	}
	if got := query(); !reflect.DeepEqual(got, before) || f.store.Durable() != before.ThroughSequence {
		t.Fatalf("uncommitted preparation changed facts: %+v", got)
	}
	cancel()
	once.Do(func() { close(release) })
	writeErr := <-result
	joined = true
	if !errors.Is(writeErr, context.Canceled) {
		t.Fatalf("cancel before Submit: %v", writeErr)
	}
	armed.Store(false)
	if got := query(); !reflect.DeepEqual(got, before) || f.store.Durable() != before.ThroughSequence {
		t.Fatalf("cancelled preparation changed facts: %+v", got)
	}
	prose := []byte(`{"tasks":{"` + waiting.Task.TaskID.String() + `":{"state":"completed","readiness":"active"}}}`)
	for i, slot := range []string{"memory", "handoff"} {
		if _, err := f.service.WritePrivate(ctx, PrivateWrite{Binding: sibling, CommandID: privateID(f.next()), Scope: "context", ExpectedVersion: uint64(i + 1), Changes: []Change{{Slot: slot, Mode: "replace", Bytes: prose}}}); err != nil {
			t.Fatal(err)
		}
		got := query()
		want := before
		want.ThroughSequence = f.store.Durable()
		if want.ThroughSequence <= before.ThroughSequence || !reflect.DeepEqual(got, want) {
			t.Fatalf("committed prose changed facts: %+v", got)
		}
	}
	// A published task-description artifact is real prose, but an unassigned
	// carrier is not work for this target and cannot decide the existing task.
	artifact := scopedNewTask(f.next(), 500, uuid.UUID{}, f.owner.AgentID)
	artifact.Description = prose
	if _, err := f.service.CreateTask(ctx, f.owner, artifact); err != nil {
		t.Fatal(err)
	}
	before.ThroughSequence = f.store.Durable()
	if got := query(); !reflect.DeepEqual(got, before) {
		t.Fatalf("artifact prose changed facts: %+v", got)
	}
	writeTestFile(t, filepath.Join(f.root, "agents", "Scout-100", "contexts", sibling.ContextID.String(), "core", "state.json"), prose)
	writeTestFile(t, filepath.Join(f.root, "agents", "Scout-100", "core", "state.json"), prose)
	scope := CapacityLookup{Scope: ledger.ScopeKey{Kind: "agent", ID: f.owner.AgentID}}
	bookBefore, err := f.service.CapacityStatus(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := query(); !reflect.DeepEqual(got, before) {
			t.Fatalf("forged cache or inspection changed facts: %+v", got)
		}
	}
	bookAfter, err := f.service.CapacityStatus(ctx, scope)
	if err != nil || !reflect.DeepEqual(bookAfter, bookBefore) || f.store.Durable() != before.ThroughSequence {
		t.Fatalf("inspection charged or appended: %+v %+v %v", bookBefore, bookAfter, err)
	}
	reopenScoped(t, f.serviceFixture, DefaultLimits())
	reader = scopedBound(t, f.service, f.owner)
	if got := query(); !reflect.DeepEqual(got, before) {
		t.Fatalf("reopen trusted cosmetic cache: %+v", got)
	}
}

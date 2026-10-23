package agents

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScopedTaskTransitionsAndProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	origin := registerPrivate(t, f.service, 100)
	outsider := registerPrivate(t, f.service, 200)
	dependencyWork := scopedWorkContext(t, f.service, origin, 110, 111)
	work := scopedWorkContext(t, f.service, origin, 120, 121)
	failureWork := scopedWorkContext(t, f.service, origin, 130, 131)
	capacity := scopedCapacity()
	capacity.TurnSlots = 1
	scopedEnrol(t, f.service, origin, capacity, 300, origin.AgentID, outsider.AgentID)
	for _, row := range []struct {
		request TaskCreation
		want    error
	}{
		{scopedNewTask(390, 391, uuid.UUID{}, privateID(999)), ErrNotBound},
		{func() TaskCreation {
			r := scopedNewTask(390, 391, uuid.UUID{}, origin.AgentID)
			r.Dependencies = []uuid.UUID{privateID(999)}
			return r
		}(), ErrInvalid},
	} {
		before := f.store.Durable()
		if _, err := f.service.CreateTask(ctx, origin, row.request); !errors.Is(err, row.want) || f.store.Durable() != before {
			t.Fatalf("unregistered audience or missing dependency: %v", err)
		}
	}
	description := []byte("Shared task description\n")
	for _, definition := range []TaskCreation{scopedNewTask(401, 400, uuid.UUID{}, origin.AgentID), scopedNewTask(406, 405, uuid.UUID{}, outsider.AgentID)} {
		definition.Description = description
		if _, err := f.service.CreateTask(ctx, origin, definition); err != nil {
			t.Fatal(err)
		}
	}
	taskRequest := scopedNewTask(411, 410, uuid.UUID{}, origin.AgentID)
	taskRequest.Description = description
	taskRequest.Dependencies = []uuid.UUID{privateID(400)}
	if _, err := f.service.CreateTask(ctx, origin, taskRequest); err != nil {
		t.Fatal(err)
	}
	failure := scopedNewTask(421, 420, uuid.UUID{}, origin.AgentID)
	failure.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(422), Assignee: ledger.Binding(failureWork)}
	if _, err := f.service.CreateTask(ctx, origin, failure); err != nil {
		t.Fatal(err)
	}
	empty := scopedNewTask(431, 430, uuid.UUID{}, origin.AgentID)
	empty.Description = nil
	if _, err := f.service.CreateTask(ctx, origin, empty); err != nil {
		t.Fatalf("empty published description: %v", err)
	}
	if _, err := f.service.TransitionTask(ctx, origin, TaskMutation{CommandID: privateID(432), TaskID: empty.TaskID, ExpectedVersion: 1, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []struct {
		task, command, id int
		work              Binding
	}{{400, 601, 602, dependencyWork}, {410, 611, 612, work}} {
		if _, err := f.service.AssignTask(ctx, origin, TaskAssignment{CommandID: privateID(assignment.command), TaskID: privateID(assignment.task), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(assignment.id), Assignee: ledger.Binding(assignment.work)}}); err != nil {
			t.Fatal(err)
		}
	}
	inspector := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(400)}, {Kind: "task", TaskID: privateID(405)}, {Kind: "task", TaskID: privateID(410)}, {Kind: "task", TaskID: privateID(420)}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	scopedRoot(t, f.service, origin, 0, 501, inspector)
	onlyTask := inspector
	onlyTask.GrantID, onlyTask.Resources = privateID(510), []ledger.Resource{{Kind: "task", TaskID: privateID(410)}}
	scopedRoot(t, f.service, origin, 1, 511, onlyTask)
	binding := &ledger.TaskBinding{TaskID: privateID(410), AssignmentID: privateID(612), Generation: 1}
	execution := GrantSpec{GrantID: privateID(520), Grantee: work, Operations: []string{"task.inspect", "task.resume", "task.start", "task.submit", "task.wait"}, Resources: onlyTask.Resources, DataClass: "task", Recipients: []Binding{work}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: binding}
	scopedRoot(t, f.service, origin, 2, 521, execution)
	dependencyBinding := &ledger.TaskBinding{TaskID: privateID(400), AssignmentID: privateID(602), Generation: 1}
	dependencyTurn := GrantSpec{GrantID: privateID(530), Grantee: dependencyWork, Operations: []string{"turn.start"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(dependencyWork), Scope: "context", Slots: []string{}}}, DataClass: "private", Recipients: []Binding{dependencyWork}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: dependencyBinding}
	scopedRoot(t, f.service, origin, 3, 531, dependencyTurn)
	turn := dependencyTurn
	turn.GrantID, turn.Grantee, turn.Resources, turn.Recipients, turn.TaskBinding = privateID(540), work, []ledger.Resource{{Kind: "context", Binding: ledger.Binding(work), Scope: "context", Slots: []string{}}}, []Binding{work}, binding
	scopedRoot(t, f.service, origin, 4, 541, turn)
	failGrant := GrantSpec{GrantID: privateID(550), Grantee: failureWork, Operations: []string{"task.fail", "task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: failure.TaskID}}, DataClass: "task", Recipients: []Binding{failureWork}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: &ledger.TaskBinding{TaskID: failure.TaskID, AssignmentID: privateID(422), Generation: 1}}
	scopedRoot(t, f.service, origin, 5, 551, failGrant)
	reader, worker := scopedBound(t, f.service, origin), scopedBound(t, f.service, work)
	cursor := uuid.UUID{}
	for i, id := range []int{400, 410, 420} {
		page, err := reader.ListTasks(ctx, TaskList{GrantID: inspector.GrantID, Limit: 1, AfterTaskID: cursor})
		if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Task.TaskID != privateID(id) {
			t.Fatalf("authorized page %d: %+v %v", i, page, err)
		}
		want := privateID(id)
		if i == 2 {
			want = uuid.UUID{}
		}
		if page.AfterTaskID != want {
			t.Fatalf("hidden/terminal ID in cursor: %s want %s", page.AfterTaskID, want)
		}
		cursor = page.AfterTaskID
	}
	for _, id := range []uuid.UUID{privateID(405), privateID(999)} {
		if _, err := reader.TaskStatus(ctx, TaskLookup{TaskID: id, GrantID: inspector.GrantID}); err != ErrDenied {
			t.Fatalf("hidden/absent task oracle: %v", err)
		}
	}
	view, err := reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: inspector.GrantID})
	if err != nil || view.Readiness != "waiting_blocked" || view.Reason != "dependency" {
		t.Fatalf("dependency-blocked task marked ready: %+v %v", view, err)
	}
	view.Task.Audience[0] = outsider.AgentID
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: inspector.GrantID})
	if err != nil || !slices.Contains(view.Task.Audience, origin.AgentID) {
		t.Fatalf("caller changed task audience through its view: %+v %v", view, err)
	}
	start := TaskMutation{CommandID: privateID(700), GrantID: execution.GrantID, TaskID: privateID(410), ExpectedVersion: 2, ExpectedAssignmentID: privateID(612), ExpectedGeneration: 1, Action: "start"}
	before := f.store.Durable()
	if _, err := worker.TransitionTask(ctx, start); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("start bypassed incomplete dependency: %v", err)
	}
	if _, err := f.service.RevokeGrant(ctx, origin, GrantRevocation{CommandID: privateID(701), RevokeGrantID: inspector.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 6}); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "waiting_blocked" || view.Reason != "dependency_unavailable" {
		t.Fatalf("revoked dependency leaked readiness/state: %+v %v", view, err)
	}
	dependency, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(400)})
	if err != nil {
		t.Fatal(err)
	}
	depStep := TaskMutation{CommandID: privateID(710), TaskID: privateID(400), ExpectedVersion: 2, ExpectedAssignmentID: privateID(602), ExpectedGeneration: 1, Action: "start"}
	if _, err := f.service.TransitionTask(ctx, origin, depStep); err != nil {
		t.Fatal(err)
	}
	turnOrigin := ledger.Origin{AgentID: origin.AgentID, SessionID: privateID(101), ContextID: dependencyWork.ContextID}
	admission := ledger.RuntimeWorkAdmitted{WorkID: privateID(720), Kind: "turn", Version: 1, AuthorityVersion: 7, GrantID: dependencyTurn.GrantID, Scopes: []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: origin.AgentID}, {Kind: "context", ID: dependencyWork.ContextID}, {Kind: "grant", ID: dependencyTurn.GrantID}}, Operation: "turn.start", Target: dependencyTurn.Resources[0], TaskBinding: dependencyBinding}
	sub := ledger.ScopedSubmission{Schema: 2, Actor: "agent", Origin: turnOrigin, Operation: "turn.start", Target: admission.Target, GrantID: admission.GrantID, Expected: ledger.ScopedExpected{Version: 0}, Arguments: ledger.TurnStartArguments{TurnID: admission.WorkID, TaskBinding: dependencyBinding}}
	appendScopedFixture(t, f, scopedTurnFixture(t, privateID(721), admission.WorkID, sub, ledger.TypeRuntimeWorkAdmitted, testPayload(t, admission), 1, "admitted"))
	depStep.CommandID, depStep.ExpectedVersion, depStep.Action, depStep.Submission = privateID(730), 3, "submit", &ledger.SubjectRef{Kind: "artifact", Artifact: dependency.DescriptionRef}
	if _, err := f.service.TransitionTask(ctx, origin, depStep); err != nil {
		t.Fatal(err)
	}
	depStep.CommandID, depStep.ExpectedVersion, depStep.Action = privateID(731), 4, "complete"
	if _, err := f.service.TransitionTask(ctx, origin, depStep); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Reason != "dependency_unavailable" {
		t.Fatalf("invisible completed dependency disclosed: %+v %v", view, err)
	}
	depInspect := onlyTask
	depInspect.GrantID, depInspect.Resources = privateID(560), []ledger.Resource{{Kind: "task", TaskID: privateID(400)}}
	scopedRoot(t, f.service, origin, 7, 561, depInspect)
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "ready_queued" {
		t.Fatalf("occupied turn slot became denial or ready: %+v %v", view, err)
	}
	capacity.TurnSlots = 0
	if _, err := f.service.ConfigureCapacity(ctx, origin, CapacitySpec{CommandID: privateID(740), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "agent", ID: origin.AgentID}, Limits: capacity}); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "waiting_blocked" || view.Reason != "capacity_disabled" {
		t.Fatalf("disabled admission configuration: %+v %v", view, err)
	}
	capacity.TurnSlots = 1
	if _, err := f.service.ConfigureCapacity(ctx, origin, CapacitySpec{CommandID: privateID(741), ExpectedVersion: 2, Scope: ledger.ScopeKey{Kind: "agent", ID: origin.AgentID}, Limits: capacity}); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "ready_queued" {
		t.Fatalf("reconfiguration reset occupied turn: %+v %v", view, err)
	}
	sub.Actor, sub.Operation, sub.Expected.Version, sub.Arguments = "core", "turn.end", 1, ledger.WorkEndArguments{WorkID: admission.WorkID, Outcome: "released"}
	ending := ledger.RuntimeWorkEnded{WorkID: admission.WorkID, ExpectedVersion: 1, Version: 2, Outcome: "released"}
	appendScopedFixture(t, f, scopedTurnFixture(t, privateID(750), admission.WorkID, sub, ledger.TypeRuntimeWorkEnded, testPayload(t, ending), 2, "recorded"))
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "ready" {
		t.Fatalf("ready work required context enrollment or lost release: %+v %v", view, err)
	}
	if _, err := worker.TransitionTask(ctx, start); err != nil {
		t.Fatal(err)
	}
	wait := start
	wait.CommandID, wait.ExpectedVersion, wait.Action, wait.WaitReason = privateID(760), 3, "wait", "explicit_hold"
	hidden, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(405)})
	if err != nil {
		t.Fatal(err)
	}
	unpublished := hidden.DescriptionRef
	unpublished.SourceSequence += 1 << 20
	for _, subject := range []ledger.ArtifactRef{hidden.DescriptionRef, unpublished} {
		denied := wait
		denied.WaitArtifact = &ledger.SubjectRef{Kind: "artifact", Artifact: subject}
		before = f.store.Durable()
		if _, err := worker.TransitionTask(ctx, denied); err != ErrDenied || f.store.Durable() != before {
			t.Fatalf("existing/absent unreadable subject disclosed or authorized by hash alone: %v", err)
		}
	}
	wait.WaitArtifact = &ledger.SubjectRef{Kind: "artifact", Artifact: view.DescriptionRef}
	if _, err := worker.TransitionTask(ctx, wait); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "waiting_blocked" || view.Reason != "explicit_hold" {
		t.Fatalf("explicit wait lost: %+v %v", view, err)
	}
	resume := start
	resume.CommandID, resume.ExpectedVersion, resume.Action = privateID(770), 4, "resume"
	if _, err := worker.TransitionTask(ctx, resume); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Task.State != "waiting" || view.Readiness != "ready" {
		t.Fatalf("resume implicitly started work: %+v %v", view, err)
	}
	start.CommandID, start.ExpectedVersion = privateID(780), 5
	if _, err := worker.TransitionTask(ctx, start); err != nil {
		t.Fatal(err)
	}
	ownerWait := TaskMutation{CommandID: privateID(781), TaskID: start.TaskID, ExpectedVersion: 6, ExpectedAssignmentID: start.ExpectedAssignmentID, ExpectedGeneration: 1, Action: "wait", WaitReason: "input", WaitArtifact: &ledger.SubjectRef{Kind: "artifact", Artifact: hidden.DescriptionRef}}
	if _, err := f.service.TransitionTask(ctx, origin, ownerWait); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Task.WaitArtifact == nil || view.Task.WaitArtifact.Artifact != hidden.DescriptionRef {
		t.Fatalf("authorized task fact redacted by artifact authority: %+v %v", view, err)
	}
	resume.CommandID, resume.ExpectedVersion = privateID(782), 7
	if _, err := worker.TransitionTask(ctx, resume); err != nil {
		t.Fatal(err)
	}
	start.CommandID, start.ExpectedVersion = privateID(783), 8
	if _, err := worker.TransitionTask(ctx, start); err != nil {
		t.Fatal(err)
	}
	submit := start
	submit.CommandID, submit.ExpectedVersion, submit.Action, submit.Submission = privateID(790), 9, "submit", &ledger.SubjectRef{Kind: "artifact", Artifact: view.DescriptionRef}
	unreadable := submit
	unreadable.Submission = ownerWait.WaitArtifact
	before = f.store.Durable()
	if _, err := worker.TransitionTask(ctx, unreadable); err != ErrDenied || f.store.Durable() != before {
		t.Fatalf("task-fact disclosure granted artifact authority: %v", err)
	}
	if _, err := worker.TransitionTask(ctx, submit); err != nil {
		t.Fatal(err)
	}
	view, err = reader.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: onlyTask.GrantID})
	if err != nil || view.Readiness != "submitted" || view.Task.Submission == nil || view.Task.Submission.Artifact != submit.Submission.Artifact {
		t.Fatalf("submission identity or stockpile state: %+v %v", view, err)
	}
	complete := submit
	complete.CommandID, complete.ExpectedVersion, complete.Action = privateID(800), 10, "complete"
	before = f.store.Durable()
	if _, err := worker.TransitionTask(ctx, complete); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatalf("task.submit implied task.complete: %v", err)
	}
	completion := onlyTask
	completion.GrantID, completion.Operations = privateID(570), []string{"task.complete"}
	scopedRoot(t, f.service, origin, 8, 571, completion)
	complete.GrantID = completion.GrantID
	wrong := complete
	wrong.Submission = &ledger.SubjectRef{Kind: "artifact", Artifact: dependency.DescriptionRef}
	before = f.store.Durable()
	if _, err := reader.TransitionTask(ctx, wrong); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("completion ignored exact stored submission: %v", err)
	}
	if _, err := reader.TransitionTask(ctx, complete); err != nil {
		t.Fatal(err)
	}
	failed, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: failure.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	failureAgent := scopedBound(t, f.service, failureWork)
	if _, err := failureAgent.TransitionTask(ctx, TaskMutation{CommandID: privateID(810), GrantID: failGrant.GrantID, TaskID: failure.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(422), ExpectedGeneration: 1, Action: "fail", FailureCause: "executor_unavailable", FailureEvidence: &ledger.SubjectRef{Kind: "artifact", Artifact: failed.DescriptionRef}}); err != nil {
		t.Fatal(err)
	}
	page, err := reader.ListTasks(ctx, TaskList{GrantID: onlyTask.GrantID, Limit: 1})
	if err != nil || len(page.Tasks) != 0 || page.AfterTaskID != (uuid.UUID{}) {
		t.Fatalf("terminal task stayed in live projection: %+v %v", page, err)
	}
	before = f.store.Durable()
	complete.CommandID, complete.ExpectedVersion = privateID(820), 11
	if _, err := reader.TransitionTask(ctx, complete); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("terminal task reopened: %v", err)
	}
	report := reopenScoped(t, f, DefaultLimits())
	if len(report.PendingWork) != 0 {
		t.Fatalf("task declarations created or resumed work: %+v", report.PendingWork)
	}
	for id, state := range map[uuid.UUID]string{privateID(400): "completed", privateID(410): "completed", failure.TaskID: "failed", empty.TaskID: "cancelled"} {
		view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: id})
		if err != nil || view.Task.State != state {
			t.Fatalf("terminal replay %s: %+v %v", state, view, err)
		}
	}
}

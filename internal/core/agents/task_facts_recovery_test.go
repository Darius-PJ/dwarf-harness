package agents

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Each entrance traverses the same expected facts with small pages, including
// when retained history exceeds a subsequently lowered producer limit.
func taskFactsHistory(t *testing.T, f *taskFactsRig, viewer Binding, inspect, target uuid.UUID, facts ...TaskFact) [2][]TaskFactsPage {
	t.Helper()
	facts = slices.Clone(facts)
	slices.SortFunc(facts, func(a, b TaskFact) int { return bytes.Compare(a.TaskID[:], b.TaskID[:]) })
	counts := TaskFactCounts{}
	for _, fact := range facts {
		switch fact.Readiness {
		case "ready", "ready_queued":
			counts.WaitingReady++
		case "waiting_blocked":
			counts.WaitingBlocked++
		case "active":
			counts.Active++
		default:
			t.Fatalf("ineligible expected fact: %+v", fact)
		}
	}
	reader := scopedBound(t, f.service, viewer)
	var pages [2][]TaskFactsPage
	for entrance, query := range []func(context.Context, TaskFactsQuery) (TaskFactsPage, error){f.service.ListTaskFacts, reader.ListTaskFacts} {
		q := TaskFactsQuery{AgentID: target, Limit: 1}
		coordinate := TaskFactsCursor{ColonyID: f.store.Colony(), Actor: "owner", AgentID: target, ThroughSequence: f.store.Durable()}
		if entrance == 1 {
			q.GrantID = inspect
			coordinate.Actor, coordinate.Origin, coordinate.GrantID = "agent", originOf(reader.ids), inspect
		}
		for i := range max(1, len(facts)) {
			page, err := query(context.Background(), q)
			if err != nil {
				t.Fatalf("entrance %d page %d: %v", entrance, i, err)
			}
			want := TaskFactsPage{AgentID: target, ThroughSequence: coordinate.ThroughSequence, Counts: counts, WaitingReady: []TaskFact{}, WaitingBlocked: []TaskFact{}, Active: []TaskFact{}}
			if len(facts) != 0 {
				fact := facts[i]
				switch fact.Readiness {
				case "ready", "ready_queued":
					want.WaitingReady = append(want.WaitingReady, fact)
				case "waiting_blocked":
					want.WaitingBlocked = append(want.WaitingBlocked, fact)
				case "active":
					want.Active = append(want.Active, fact)
				}
				if i+1 < len(facts) {
					next := coordinate
					next.AfterTaskID = fact.TaskID
					want.Next = &next
				}
			}
			if !reflect.DeepEqual(page, want) {
				t.Fatalf("entrance %d page %d\ngot  %+v\nwant %+v", entrance, i, page, want)
			}
			pages[entrance] = append(pages[entrance], page)
			q.Cursor = page.Next
		}
	}
	return pages
}

func taskFactsVersion(t *testing.T, f *taskFactsRig, id uuid.UUID, version int64, state string, sequence uint64) TaskView {
	t.Helper()
	view := f.status(t, id)
	if view.Task.Version != version || view.Task.State != state || view.Sequence != sequence {
		t.Fatalf("task effect: got version=%d state=%s sequence=%d, want %d %s %d", view.Task.Version, view.Task.State, view.Sequence, version, state, sequence)
	}
	return view
}

func TestTaskFactsTransitionRecovery(t *testing.T) {
	t.Parallel()
	// Catches missed or double-applied ordered effects, old-assignment membership,
	// revocation rewriting active state, and replay truncation at producer limits.
	for _, terminal := range []string{"complete", "fail", "cancel"} {
		t.Run(terminal, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newTaskFactsRig(t, serviceDeps{})
			view := f.task(t, 400, Binding{}, nil, f.owner.AgentID)
			if view.Task.Version != 1 || view.Task.State != "waiting" || view.Task.Assignment != nil {
				t.Fatalf("unassigned creation: %+v", view)
			}
			inspect := f.grant(t, f.owner, []string{"task.inspect"}, view.Task.TaskID)
			check := func(facts ...TaskFact) [2][]TaskFactsPage {
				return taskFactsHistory(t, f, f.owner, inspect.GrantID, f.owner.AgentID, facts...)
			}
			check()
			work := scopedWorkContext(t, f.service, f.owner, f.next(), f.next())
			execution := f.grant(t, work, []string{"task.claim", "task.inspect", "task.resume", "task.start", "task.submit", "task.wait"}, view.Task.TaskID)
			turn := privateScopedGrant(work, f.next(), scopedCapacity(), "turn.start")
			issueScopedRoot(t, f.service, f.owner, f.next(), turn)
			worker := scopedBound(t, f.service, work)
			assignment := TaskAssignment{CommandID: privateID(f.next()), TaskID: view.Task.TaskID, ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(f.next()), Assignee: ledger.Binding(work)}}
			assign := func(q TaskAssignment) (ScopedReceipt, error) { return f.service.AssignTask(ctx, f.owner, q) }
			actor := "owner"
			if terminal == "complete" {
				assignment.GrantID, actor = execution.GrantID, "agent"
				assign = func(q TaskAssignment) (ScopedReceipt, error) { return worker.ClaimTask(ctx, q) }
			}
			before := f.store.Durable()
			receipt, err := assign(assignment)
			if err != nil {
				t.Fatal(err)
			}
			view = taskFactsVersion(t, f, view.Task.TaskID, 2, "waiting", before+2)
			if *view.Task.Assignment != (ledger.Assignment{AssignmentID: assignment.Assignment.AssignmentID, Generation: 1, Assignee: ledger.Binding(work), AssignerActor: actor, Assigner: receipt.Origin}) {
				t.Fatalf("assignment identity/provenance: %+v", view.Task.Assignment)
			}
			fact := taskFactFromView(view, "ready", "")
			pages := check(fact)
			if retry, err := assign(assignment); err != nil || !reflect.DeepEqual(retry, receipt) || !reflect.DeepEqual(check(fact), pages) {
				t.Fatalf("matched assignment repeated: %+v %v", retry, err)
			}
			staleAssignment := assignment
			staleAssignment.CommandID = privateID(f.next())
			if _, err := assign(staleAssignment); !errors.Is(err, ErrRevision) || !reflect.DeepEqual(check(fact), pages) {
				t.Fatalf("old-version assignment changed facts: %v", err)
			}
			for _, action := range []string{"start", "wait", "resume", "start", "submit", terminal} {
				q := TaskMutation{CommandID: privateID(f.next()), GrantID: execution.GrantID, TaskID: fact.TaskID, ExpectedVersion: uint64(fact.Version), ExpectedAssignmentID: fact.Assignment.AssignmentID, ExpectedGeneration: fact.Assignment.Generation, Action: action}
				state, readiness, reason := "waiting", "ready", ""
				switch action {
				case "start":
					state, readiness = "active", "active"
				case "wait":
					q.WaitReason, q.WaitArtifact = "explicit_hold", &ledger.SubjectRef{Kind: "artifact", Artifact: fact.DescriptionRef}
					readiness, reason = "waiting_blocked", "explicit_hold"
				case "submit", "complete":
					q.Submission = &ledger.SubjectRef{Kind: "artifact", Artifact: fact.DescriptionRef}
					state = "submitted"
					if action == "complete" {
						state = "completed"
					}
				case "fail":
					q.FailureCause, q.FailureEvidence = "executor_unavailable", &ledger.SubjectRef{Kind: "artifact", Artifact: fact.DescriptionRef}
					state = "failed"
				case "cancel":
					state = "cancelled"
				}
				apply := func(q TaskMutation) (ScopedReceipt, error) { return worker.TransitionTask(ctx, q) }
				if action == terminal {
					q.GrantID = uuid.UUID{}
					apply = func(q TaskMutation) (ScopedReceipt, error) { return f.service.TransitionTask(ctx, f.owner, q) }
				}
				before = f.store.Durable()
				receipt, err = apply(q)
				if err != nil {
					t.Fatalf("%s: %v", action, err)
				}
				taskFactsVersion(t, f, fact.TaskID, fact.Version+1, state, before+2)
				fact.Version, fact.State, fact.Sequence, fact.Readiness, fact.Reason = fact.Version+1, state, before+2, readiness, reason
				var eligible []TaskFact
				if state == "waiting" || state == "active" {
					eligible = []TaskFact{fact}
				}
				pages = check(eligible...)
				if retry, err := apply(q); err != nil || !reflect.DeepEqual(retry, receipt) || !reflect.DeepEqual(check(eligible...), pages) {
					t.Fatalf("%s retry changed facts: %+v %v", action, retry, err)
				}
				q.CommandID = privateID(f.next())
				if _, err := apply(q); !errors.Is(err, ErrRevision) || !reflect.DeepEqual(check(eligible...), pages) {
					t.Fatalf("%s old-version mutation changed facts: %v", action, err)
				}
				taskFactsVersion(t, f, fact.TaskID, fact.Version, state, fact.Sequence)
			}
			reopenScoped(t, f.serviceFixture, DefaultLimits())
			check()
			taskFactsVersion(t, f, fact.TaskID, fact.Version, fact.State, fact.Sequence)
		})
	}

	t.Run("reassignment", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTaskFactsRig(t, serviceDeps{})
		view := f.task(t, 400, f.owner, nil, f.owner.AgentID, f.other.AgentID)
		inspect := f.grant(t, f.owner, []string{"task.inspect"}, view.Task.TaskID)
		f.execution(t, view)
		work := Binding(view.Task.Assignment.Assignee)
		oldGrant := GrantSpec{GrantID: privateID(f.next()), Grantee: work, Operations: []string{"task.start"}, Resources: []ledger.Resource{{Kind: "task", TaskID: view.Task.TaskID}}, DataClass: "task", Recipients: []Binding{work}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: &ledger.TaskBinding{TaskID: view.Task.TaskID, AssignmentID: view.Task.Assignment.AssignmentID, Generation: 1}}
		issueScopedRoot(t, f.service, f.owner, f.next(), oldGrant)
		oldWorker := scopedBound(t, f.service, work)
		check := func(view TaskView, readiness, reason string) [2][2][]TaskFactsPage {
			t.Helper()
			var pages [2][2][]TaskFactsPage
			for i, target := range []uuid.UUID{f.owner.AgentID, f.other.AgentID} {
				var facts []TaskFact
				if target == view.Task.Assignment.Assignee.AgentID && (view.Task.State == "waiting" || view.Task.State == "active") {
					facts = []TaskFact{taskFactFromView(view, readiness, reason)}
				}
				pages[i] = taskFactsHistory(t, f, f.owner, inspect.GrantID, target, facts...)
			}
			return pages
		}
		check(view, "ready", "")
		view = f.transition(t, view, "start")
		check(view, "active", "")
		view = f.transition(t, view, "wait")
		check(view, "waiting_blocked", "explicit_hold")
		for generation := int64(2); generation <= 3; generation++ {
			previous := view
			fresh := scopedWorkContext(t, f.service, f.other, f.next(), f.next())
			q := TaskAssignment{CommandID: privateID(f.next()), TaskID: view.Task.TaskID, ExpectedVersion: uint64(view.Task.Version), ExpectedAssignmentID: view.Task.Assignment.AssignmentID, ExpectedGeneration: view.Task.Assignment.Generation, Assignment: ledger.AssignmentInput{AssignmentID: privateID(f.next()), Assignee: ledger.Binding(fresh)}}
			before := f.store.Durable()
			receipt, err := f.service.ReassignTask(ctx, f.owner, q)
			if err != nil {
				t.Fatal(err)
			}
			view = taskFactsVersion(t, f, q.TaskID, previous.Task.Version+1, "waiting", before+2)
			wantAssignment := ledger.Assignment{AssignmentID: q.Assignment.AssignmentID, Generation: generation, Assignee: ledger.Binding(fresh), AssignerActor: "owner", Assigner: receipt.Origin}
			if *view.Task.Assignment != wantAssignment || view.Task.WaitReason != "" || view.Task.WaitArtifact != nil || view.Task.Submission != nil {
				t.Fatalf("reassignment copied old identity or transient state: %+v", view.Task)
			}
			pages := check(view, "waiting_blocked", "task.start_denied")
			if retry, err := f.service.ReassignTask(ctx, f.owner, q); err != nil || !reflect.DeepEqual(retry, receipt) || !reflect.DeepEqual(check(view, "waiting_blocked", "task.start_denied"), pages) {
				t.Fatalf("reassignment applied twice: %+v %v", retry, err)
			}
			stale := TaskMutation{CommandID: privateID(f.next()), TaskID: q.TaskID, ExpectedVersion: q.ExpectedVersion, ExpectedAssignmentID: q.ExpectedAssignmentID, ExpectedGeneration: q.ExpectedGeneration, Action: "cancel"}
			if _, err := f.service.TransitionTask(ctx, f.owner, stale); !errors.Is(err, ErrRevision) {
				t.Fatalf("old version: %v", err)
			}
			stale.ExpectedVersion = uint64(view.Task.Version)
			if _, err := f.service.TransitionTask(ctx, f.owner, stale); !errors.Is(err, ErrAssignment) {
				t.Fatalf("old assignment: %v", err)
			}
			stale.CommandID, stale.GrantID, stale.Action = privateID(f.next()), oldGrant.GrantID, "start"
			if _, err := oldWorker.TransitionTask(ctx, stale); !errors.Is(err, ErrDenied) {
				t.Fatalf("old task-bound execution: %v", err)
			}
			if !reflect.DeepEqual(check(view, "waiting_blocked", "task.start_denied"), pages) {
				t.Fatal("stale mutations changed reassigned facts")
			}
			f.execution(t, view)
			check(view, "ready", "")
			if generation == 2 {
				view = f.transition(t, view, "start")
				check(view, "active", "")
				view = f.transition(t, view, "submit")
				check(view, "submitted", "")
			}
		}
		before := check(view, "ready", "")
		reopenScoped(t, f.serviceFixture, DefaultLimits())
		if !reflect.DeepEqual(check(view, "ready", ""), before) {
			t.Fatal("reopen lost the latest assignment generation")
		}
	})

	for _, operation := range []string{"task.start", "turn.start"} {
		t.Run("revoke_"+operation+"_and_reopen", func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newTaskFactsRig(t, serviceDeps{})
			waiting := f.task(t, 400, f.owner, nil, f.owner.AgentID)
			active := f.task(t, 410, f.owner, nil, f.owner.AgentID)
			ready := f.task(t, 420, f.owner, nil, f.owner.AgentID)
			f.execution(t, ready)
			var revoked []uuid.UUID
			for _, view := range []TaskView{waiting, active} {
				work := Binding(view.Task.Assignment.Assignee)
				start := f.grant(t, work, []string{"task.start"}, view.Task.TaskID)
				turn := privateScopedGrant(work, f.next(), scopedCapacity(), "turn.start")
				issueScopedRoot(t, f.service, f.owner, f.next(), turn)
				id := start.GrantID
				if operation == "turn.start" {
					id = turn.GrantID
				}
				revoked = append(revoked, id)
			}
			inspect := f.grant(t, f.owner, []string{"task.inspect"}, waiting.Task.TaskID, active.Task.TaskID, ready.Task.TaskID)
			check := func(reason string) [2][]TaskFactsPage {
				readiness := "ready"
				if reason != "" {
					readiness = "waiting_blocked"
				}
				return taskFactsHistory(t, f, f.owner, inspect.GrantID, f.owner.AgentID, taskFactFromView(waiting, readiness, reason), taskFactFromView(active, active.Readiness, active.Reason), taskFactFromView(ready, "ready", ""))
			}
			active = f.status(t, active.Task.TaskID)
			check("")
			before := f.store.Durable()
			if _, err := f.service.TransitionTask(ctx, f.owner, TaskMutation{CommandID: privateID(f.next()), TaskID: active.Task.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: active.Task.Assignment.AssignmentID, ExpectedGeneration: 1, Action: "start"}); err != nil {
				t.Fatal(err)
			}
			active = taskFactsVersion(t, f, active.Task.TaskID, 2, "active", before+2)
			check("")
			for _, id := range revoked {
				if _, err := f.service.RevokeGrant(ctx, f.owner, GrantRevocation{CommandID: privateID(f.next()), RevokeGrantID: id, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)}); err != nil {
					t.Fatal(err)
				}
				check(operation + "_denied")
			}
			pages := check(operation + "_denied")
			for _, limit := range []int{f.service.limits.MaxLiveTasks, 1} {
				limits := DefaultLimits()
				limits.MaxLiveTasks = limit
				report := reopenScoped(t, f.serviceFixture, limits)
				if len(report.PendingWork) != 0 || !reflect.DeepEqual(check(operation+"_denied"), pages) {
					t.Fatal("reopen resumed work or truncated historical facts")
				}
				reader := scopedBound(t, f.service, f.owner)
				for entrance, query := range []func(context.Context, TaskFactsQuery) (TaskFactsPage, error){f.service.ListTaskFacts, reader.ListTaskFacts} {
					q := TaskFactsQuery{AgentID: f.owner.AgentID, Limit: 1, Cursor: pages[entrance][0].Next}
					if entrance == 1 {
						q.GrantID = inspect.GrantID
					}
					page, err := query(ctx, q)
					if err != nil || !reflect.DeepEqual(page, pages[entrance][1]) {
						t.Fatalf("same-head cursor with limit %d: %+v %v", limit, page, err)
					}
					if limit < DefaultLimits().MaxLiveTasks {
						q.Limit = DefaultLimits().MaxLiveTasks
						page, err = query(ctx, q)
						assertTaskFactsError(t, page, err, ErrTooLarge)
					}
				}
			}
		})
	}
}

func TestTaskFactsReviewTransitions(t *testing.T) {
	t.Parallel()
	// Catches an F3-11-only reader, request/reject treated as task effects,
	// failure satisfying dependencies, stale callbacks changing a replacement,
	// and lost acknowledgements applying a terminal review result twice.
	for _, scenario := range []string{"approve", "fail-active", "fail-waiting", "old-generation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			prefix := 17
			switch scenario {
			case "fail-active":
				prefix = 16
			case "fail-waiting":
				prefix = 15
			}
			base, request := liveReviewAt(t, prefix)
			f := &taskFactsRig{serviceFixture: base, owner: reviewOwner(), serial: 2000}
			parent := f.status(t, request.Binding.TaskID)
			if scenario == "fail-waiting" {
				f.execution(t, parent)
			}
			dependent := f.task(t, 400, f.owner, []uuid.UUID{parent.Task.TaskID}, f.owner.AgentID)
			f.execution(t, dependent)
			inspect := f.grant(t, f.owner, []string{"task.complete", "task.fail", "task.inspect"}, dependent.Task.TaskID, parent.Task.TaskID, reviewGoldenID(0x201), reviewGoldenID(0x202))
			reviewerBinding := bindingOf(request.Binding.Reviewer.Origin)
			otherContext := scopedWorkContext(t, f.service, reviewerBinding, f.next(), f.next())
			wrongReviewer := scopedBound(t, f.service, otherContext)
			request.Binding.TaskVersion = parent.Task.Version
			request.Binding.AuthorizationRevision = f.service.p.authority
			background := []TaskFact{
				taskFactFromView(f.status(t, reviewGoldenID(0x201)), "waiting_blocked", "task.start_denied"),
				taskFactFromView(f.status(t, reviewGoldenID(0x202)), "waiting_blocked", "task.start_denied"),
			}
			dependentFact := taskFactFromView(dependent, "waiting_blocked", "dependency")
			var parentFact *TaskFact
			if scenario == "fail-active" {
				fact := taskFactFromView(parent, "active", "")
				parentFact = &fact
			} else if scenario == "fail-waiting" {
				fact := taskFactFromView(parent, "ready", "")
				parentFact = &fact
			} else if parent.Task.State != "submitted" {
				t.Fatalf("review fixture is not submitted: %+v", parent.Task)
			}
			check := func() [2][]TaskFactsPage {
				t.Helper()
				facts := append(slices.Clone(background), dependentFact)
				if parentFact != nil {
					facts = append(facts, *parentFact)
				}
				return taskFactsHistory(t, f, f.owner, inspect.GrantID, f.owner.AgentID, facts...)
			}
			unchangedTask := func() {
				t.Helper()
				view := taskFactsVersion(t, f, parent.Task.TaskID, parent.Task.Version, parent.Task.State, parent.Sequence)
				if !reflect.DeepEqual(view.Task, parent.Task) {
					t.Fatalf("non-transition review changed task: %+v", view.Task)
				}
				check()
			}
			refuse := func(action func() error, want error) {
				t.Helper()
				before := check()
				if err := action(); !errors.Is(err, want) {
					t.Fatalf("review refusal: %v, want %v", err, want)
				}
				if !reflect.DeepEqual(check(), before) {
					t.Fatal("refused callback/direct mutation changed task facts")
				}
				unchangedTask()
			}
			guard := func() {
				t.Helper()
				for _, action := range []string{"complete", "fail"} {
					q := reviewTaskMutation(t, f.service, action)
					q.CommandID = privateID(f.next())
					refuse(func() error { _, err := f.service.TransitionTask(ctx, f.owner, q); return err }, ErrReviewRequired)
					q.GrantID = inspect.GrantID
					refuse(func() error {
						_, err := scopedBound(t, f.service, f.owner).TransitionTask(ctx, q)
						return err
					}, ErrReviewRequired)
				}
			}
			check()
			if scenario == "fail-active" || scenario == "fail-waiting" {
				binding := request.Binding
				binding.Subject = ledger.SubjectRef{Kind: "artifact", Artifact: parent.DescriptionRef}
				binding.RequiredEvidence = []ledger.SubjectRef{binding.Subject}
				failure := FailureRequest{CommandID: privateID(f.next()), RequestID: privateID(f.next()), Binding: binding, Cause: "semantic_failure", FailingEvidence: binding.Subject}
				receipt, err := f.service.RequestFailure(ctx, f.owner, failure)
				if err != nil {
					t.Fatal(err)
				}
				unchangedTask() // A pending failure is not a failed or blocked task.
				pages := check()
				if retry, err := f.service.RequestFailure(ctx, f.owner, failure); err != nil || !reflect.DeepEqual(retry, receipt) || !reflect.DeepEqual(check(), pages) {
					t.Fatalf("failure request retry changed facts: %+v %v", retry, err)
				}
				reviewer := scopedBound(t, f.service, reviewerBinding)
				result := FailureResult{CommandID: privateID(f.next()), GrantID: binding.Reviewer.GrantID, RequestID: failure.RequestID, RequestSequence: int64(receipt.EffectSequence), Binding: binding, Cause: failure.Cause, FailingEvidence: failure.FailingEvidence}
				refuse(func() error { _, err := wrongReviewer.ResolveFailure(ctx, result); return err }, ErrDenied)
				lost := errors.New("lost failure result acknowledgement")
				var committed ReviewReceipt
				f.service.deps.afterReviewCommit = func(r ReviewReceipt) error { committed = r; return lost }
				before := f.store.Durable()
				if _, err := reviewer.ResolveFailure(ctx, result); !errors.Is(err, lost) {
					t.Fatalf("failure acknowledgement hook: %v", err)
				}
				parent = taskFactsVersion(t, f, parent.Task.TaskID, parent.Task.Version+1, "failed", before+2)
				parentFact = nil
				pages = check() // Failure removes only the parent; its dependent stays blocked.
				reopenScoped(t, f.serviceFixture, DefaultLimits())
				f.service.deps.reviewClock = func() (int64, error) { t.Error("matched failure retry sampled a clock"); return 0, ErrReviewClock }
				reviewer = scopedBound(t, f.service, reviewerBinding)
				if retry, err := reviewer.ResolveFailure(ctx, result); err != nil || !reflect.DeepEqual(retry, committed) || !reflect.DeepEqual(check(), pages) {
					t.Fatalf("reopened failure repeated its task effect: %+v %v", retry, err)
				}
				unchangedTask()
				if consumed, unpins := reviewCrashLog(t, f.store, result.CommandID, parent.Task.TaskID); !consumed || unpins != 1 {
					t.Fatalf("failure result consumed=%t unpins=%d", consumed, unpins)
				}
				return
			}

			_, reviewer, result := requestLiveReview(t, base, request)
			unchangedTask()
			guard()
			if scenario == "old-generation" {
				fresh := scopedWorkContext(t, f.service, f.owner, f.next(), f.next())
				q := TaskAssignment{CommandID: privateID(f.next()), TaskID: parent.Task.TaskID, ExpectedVersion: uint64(parent.Task.Version), ExpectedAssignmentID: parent.Task.Assignment.AssignmentID, ExpectedGeneration: parent.Task.Assignment.Generation, Assignment: ledger.AssignmentInput{AssignmentID: privateID(f.next()), Assignee: ledger.Binding(fresh)}}
				before := f.store.Durable()
				receipt, err := f.service.ReassignTask(ctx, f.owner, q)
				if err != nil {
					t.Fatal(err)
				}
				parent = taskFactsVersion(t, f, parent.Task.TaskID, parent.Task.Version+1, "waiting", before+2)
				if *parent.Task.Assignment != (ledger.Assignment{AssignmentID: q.Assignment.AssignmentID, Generation: q.ExpectedGeneration + 1, Assignee: ledger.Binding(fresh), AssignerActor: "owner", Assigner: receipt.Origin}) {
					t.Fatalf("review-slot reassignment: %+v", parent.Task.Assignment)
				}
				fact := taskFactFromView(parent, "waiting_blocked", "task.start_denied")
				parentFact = &fact
				refuse(func() error { _, err := reviewer.ResolveReview(ctx, result); return err }, ErrReviewStale)
				pages := check()
				reopenScoped(t, f.serviceFixture, DefaultLimits())
				f.service.deps.reviewClock = func() (int64, error) { return request.Binding.DeadlineUTCNS - 1, nil }
				reviewer = scopedBound(t, f.service, reviewerBinding)
				refuse(func() error { _, err := reviewer.ResolveReview(ctx, result); return err }, ErrReviewStale)
				if !reflect.DeepEqual(check(), pages) {
					t.Fatal("reopen revived the old-generation review")
				}
				return
			}

			old := result
			request.CommandID, request.ReplacesRequestID, request.RequestID = privateID(f.next()), request.RequestID, privateID(f.next())
			replacement, err := f.service.RequestReview(ctx, f.owner, request)
			if err != nil {
				t.Fatal(err)
			}
			unchangedTask()
			refuse(func() error { _, err := reviewer.ResolveReview(ctx, old); return err }, ErrReviewStale)
			result.CommandID, result.RequestID, result.RequestSequence = privateID(f.next()), request.RequestID, int64(replacement.EffectSequence)
			refuse(func() error { _, err := wrongReviewer.ResolveReview(ctx, result); return err }, ErrDenied)
			result.Verdict = "reject"
			rejected, err := reviewer.ResolveReview(ctx, result)
			if err != nil {
				t.Fatal(err)
			}
			unchangedTask()
			pages := check()
			if retry, err := reviewer.ResolveReview(ctx, result); err != nil || !reflect.DeepEqual(retry, rejected) || !reflect.DeepEqual(check(), pages) {
				t.Fatalf("rejection retry changed facts: %+v %v", retry, err)
			}
			guard()
			request.CommandID, request.ReplacesRequestID, request.RequestID = privateID(f.next()), request.RequestID, privateID(f.next())
			replacement, err = f.service.RequestReview(ctx, f.owner, request)
			if err != nil {
				t.Fatal(err)
			}
			unchangedTask()
			result.CommandID, result.RequestID, result.RequestSequence, result.Verdict = privateID(f.next()), request.RequestID, int64(replacement.EffectSequence), "approve"
			lost := errors.New("lost approval acknowledgement")
			var committed ReviewReceipt
			f.service.deps.afterReviewCommit = func(r ReviewReceipt) error { committed = r; return lost }
			before := f.store.Durable()
			if _, err := reviewer.ResolveReview(ctx, result); !errors.Is(err, lost) {
				t.Fatalf("approval acknowledgement hook: %v", err)
			}
			parent = taskFactsVersion(t, f, parent.Task.TaskID, parent.Task.Version+1, "completed", before+2)
			dependentFact.Readiness, dependentFact.Reason = "ready", ""
			pages = check() // Completion unblocks the dependent without re-versioning it.
			reopenScoped(t, f.serviceFixture, DefaultLimits())
			f.service.deps.reviewClock = func() (int64, error) { t.Error("matched approval retry sampled a clock"); return 0, ErrReviewClock }
			reviewer = scopedBound(t, f.service, reviewerBinding)
			if retry, err := reviewer.ResolveReview(ctx, result); err != nil || !reflect.DeepEqual(retry, committed) || !reflect.DeepEqual(check(), pages) {
				t.Fatalf("reopened approval repeated its task effect: %+v %v", retry, err)
			}
			unchangedTask()
			if consumed, unpins := reviewCrashLog(t, f.store, result.CommandID, parent.Task.TaskID); !consumed || unpins != 1 {
				t.Fatalf("approval consumed=%t unpins=%d", consumed, unpins)
			}
		})
	}
}

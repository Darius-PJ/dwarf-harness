package agents

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScopedTaskClaimAndReassignCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	first := registerPrivate(t, f.service, 100)
	second := registerPrivate(t, f.service, 200)
	firstWork := scopedWorkContext(t, f.service, first, 110, 111)
	fresh := scopedWorkContext(t, f.service, first, 120, 121)
	secondWork := scopedWorkContext(t, f.service, second, 210, 211)
	scopedEnrol(t, f.service, first, scopedCapacity(), 300, first.AgentID, second.AgentID)
	scopedCreate(t, f.service, first, 400, 401, first.AgentID, second.AgentID)
	root := GrantSpec{GrantID: privateID(500), Grantee: first, Operations: []string{"task.cancel", "task.claim", "task.inspect", "task.reassign"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(400)}}, DataClass: "task", Recipients: []Binding{firstWork, fresh, secondWork}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	scopedRoot(t, f.service, first, 0, 501, root)
	other := root
	other.GrantID, other.Grantee = privateID(510), second
	scopedRoot(t, f.service, first, 1, 511, other)
	a, b := scopedBound(t, f.service, first), scopedBound(t, f.service, second)
	before := f.store.Durable()
	if _, err := a.ClaimTask(ctx, TaskAssignment{CommandID: privateID(580), GrantID: root.GrantID, TaskID: privateID(400), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(581), Assignee: ledger.Binding(secondWork)}}); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatalf("claim assigned another agent: %v", err)
	}
	arrived, release := make(chan struct{}, 2), make(chan struct{})
	var lookups atomic.Int32
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	f.service.deps.beforeLookup = func() {
		if lookups.Add(1) <= 2 {
			arrived <- struct{}{}
			<-release
		}
	}
	type result struct {
		request TaskAssignment
		receipt ScopedReceipt
		err     error
	}
	results := make(chan result, 2)
	requests := []TaskAssignment{
		{CommandID: privateID(600), GrantID: root.GrantID, TaskID: privateID(400), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(601), Assignee: ledger.Binding(firstWork)}},
		{CommandID: privateID(610), GrantID: other.GrantID, TaskID: privateID(400), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(611), Assignee: ledger.Binding(secondWork)}},
	}
	for i, agent := range []*Agent{a, b} {
		go func() {
			receipt, err := agent.ClaimTask(ctx, requests[i])
			results <- result{requests[i], receipt, err}
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case result := <-results:
			t.Fatalf("claim bypassed lookup barrier: %v", result.err)
		}
	}
	releaseOnce.Do(func() { close(release) })
	var winner result
	wins, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			wins++
			winner = result
		} else if errors.Is(result.err, ErrRevision) {
			conflicts++
		} else {
			t.Fatalf("claim race error: %v", result.err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("two claims won: %d wins, %d conflicts", wins, conflicts)
	}
	view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(400)})
	if err != nil || view.Task.Version != 2 || view.Task.Assignment == nil || view.Task.Assignment.Generation != 1 || view.Task.Assignment.AssignmentID != winner.request.Assignment.AssignmentID || view.Task.Assignment.Assignee != winner.request.Assignment.Assignee {
		t.Fatalf("winning claim: %+v %v", view, err)
	}
	originalDescription := view.DescriptionRef
	capacity, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || capacity.Used.LocalUnits != 1 {
		t.Fatalf("losing CAS charged: %+v %v", capacity, err)
	}
	oldBinding := Binding(view.Task.Assignment.Assignee)
	old := scopedBound(t, f.service, oldBinding)
	tied := GrantSpec{GrantID: privateID(520), Grantee: oldBinding, Operations: []string{"task.cancel", "task.inspect"}, Resources: root.Resources, DataClass: "task", Recipients: []Binding{oldBinding}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: &ledger.TaskBinding{TaskID: privateID(400), AssignmentID: view.Task.Assignment.AssignmentID, Generation: 1}}
	scopedRoot(t, f.service, first, 2, 521, tied)
	broad := tied
	broad.GrantID, broad.TaskBinding = privateID(530), nil
	scopedRoot(t, f.service, first, 3, 531, broad)
	change := TaskAssignment{CommandID: privateID(700), GrantID: root.GrantID, TaskID: privateID(400), ExpectedVersion: 2, ExpectedAssignmentID: view.Task.Assignment.AssignmentID, ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(701), Assignee: ledger.Binding(fresh)}}
	wrong := change
	wrong.ExpectedGeneration = 2
	before = f.store.Durable()
	var assignment *AssignmentError
	if _, err := a.ReassignTask(ctx, wrong); !errors.As(err, &assignment) || f.store.Durable() != before {
		t.Fatalf("stale generation accepted: %v", err)
	}
	reused := change
	reused.Assignment.Assignee = ledger.Binding(oldBinding)
	if _, err := a.ReassignTask(ctx, reused); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
		t.Fatalf("reassignment reused old context: %v", err)
	}
	if _, err := a.ReassignTask(ctx, change); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(400)})
	if err != nil || view.Task.Version != 3 || view.Task.Assignment.Generation != 2 || view.Task.Assignment.Assignee != ledger.Binding(fresh) || view.DescriptionRef != originalDescription {
		t.Fatalf("reassignment changed definition/source or generation: %+v %v", view, err)
	}
	for _, grant := range []uuid.UUID{tied.GrantID, broad.GrantID} {
		before := f.store.Durable()
		if _, err := old.TaskStatus(ctx, TaskLookup{TaskID: privateID(400), GrantID: grant}); !errors.Is(err, ErrDenied) {
			t.Fatalf("old context disclosed reassigned task: %v", err)
		}
		if _, err := old.TransitionTask(ctx, TaskMutation{CommandID: privateID(750), GrantID: grant, TaskID: privateID(400), ExpectedVersion: 3, ExpectedAssignmentID: privateID(701), ExpectedGeneration: 2, Action: "cancel"}); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
			t.Fatalf("old broad/tied context mutated task: %v", err)
		}
	}
	grant, err := old.GrantStatus(ctx, GrantLookup{GrantID: tied.GrantID})
	if err != nil || grant.Current || grant.Reason != "generation_not_current" {
		t.Fatalf("own stale-generation status: %+v %v", grant, err)
	}
	if _, err := a.TransitionTask(ctx, TaskMutation{CommandID: privateID(760), GrantID: root.GrantID, TaskID: privateID(400), ExpectedVersion: 3, ExpectedAssignmentID: privateID(701), ExpectedGeneration: 2, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	reopenScoped(t, f, DefaultLimits())
	view, err = f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(400)})
	if err != nil || view.Task.State != "cancelled" || view.Task.Version != 4 || view.DescriptionRef != originalDescription {
		t.Fatalf("cold terminal task: %+v %v", view, err)
	}
	scopedCreate(t, f.service, first, 800, 801, first.AgentID, second.AgentID)
	unused := scopedWorkContext(t, f.service, second, 220, 221)
	for _, retired := range []struct {
		assignment ledger.AssignmentInput
		want       error
	}{
		{ledger.AssignmentInput{AssignmentID: privateID(900), Assignee: ledger.Binding(oldBinding)}, ErrInvalid},
		{ledger.AssignmentInput{AssignmentID: winner.request.Assignment.AssignmentID, Assignee: ledger.Binding(unused)}, ErrAssignment},
	} {
		before := f.store.Durable()
		if _, err := f.service.AssignTask(ctx, first, TaskAssignment{CommandID: privateID(901), TaskID: privateID(800), ExpectedVersion: 1, Assignment: retired.assignment}); !errors.Is(err, retired.want) || f.store.Durable() != before {
			t.Fatalf("cold history treated used assignment/context as absent: %v", err)
		}
	}
}

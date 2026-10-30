package agents

import (
	"context"
	"testing"
)

func TestTaskFactsQueryRace(t *testing.T) {
	t.Parallel()
	// The race detector is the primary oracle for copying rows after releasing
	// the query lock. The page must also reject publishing the head before the
	// task state, or collecting counts and rows under different heads.
	ctx := context.Background()
	f := newTaskFactsRig(t, serviceDeps{})
	waiting := f.task(t, 400, f.owner, nil, f.owner.AgentID)
	f.execution(t, waiting)
	inspect := f.grant(t, f.owner, []string{"task.inspect"}, waiting.Task.TaskID)
	reader := scopedBound(t, f.service, f.owner)
	q := TaskFactsQuery{AgentID: f.owner.AgentID, GrantID: inspect.GrantID, Limit: 1}
	beforeHead := f.store.Durable()
	beforeFact := taskFactFromView(waiting, "ready", "")
	before, err := reader.ListTaskFacts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	assertTaskFacts(t, before, q.AgentID, beforeHead, []TaskFact{beforeFact}, nil, nil)
	mutation := TaskMutation{
		CommandID: privateID(f.next()), TaskID: beforeFact.TaskID,
		ExpectedVersion: uint64(beforeFact.Version), Action: "start",
		ExpectedAssignmentID: beforeFact.Assignment.AssignmentID,
		ExpectedGeneration:   beforeFact.Assignment.Generation,
	}

	gate := make(chan struct{})
	ready := make(chan struct{}, 2)
	mutated := make(chan error, 1)
	type queryResult struct {
		page TaskFactsPage
		err  error
	}
	queried := make(chan queryResult, 1)
	go func() {
		ready <- struct{}{}
		<-gate
		_, err := f.service.TransitionTask(ctx, f.owner, mutation)
		mutated <- err
	}()
	go func() {
		ready <- struct{}{}
		<-gate
		page, err := reader.ListTaskFacts(ctx, q)
		queried <- queryResult{page, err}
	}()
	<-ready
	<-ready
	close(gate)
	mutationErr, result := <-mutated, <-queried
	if mutationErr != nil || result.err != nil {
		t.Fatalf("concurrent mutation: %v; query: %v", mutationErr, result.err)
	}

	afterHead := f.store.Durable()
	if afterHead <= beforeHead {
		t.Fatalf("start did not advance the committed head: %d -> %d", beforeHead, afterHead)
	}
	active := taskFactsVersion(t, f, beforeFact.TaskID, beforeFact.Version+1, "active", beforeHead+2)
	afterFact := taskFactFromView(active, "active", "")
	switch result.page.ThroughSequence {
	case beforeHead:
		assertTaskFacts(t, result.page, q.AgentID, beforeHead, []TaskFact{beforeFact}, nil, nil)
	case afterHead:
		assertTaskFacts(t, result.page, q.AgentID, afterHead, nil, nil, []TaskFact{afterFact})
	default:
		t.Fatalf("query exposed an intermediate head: %+v", result.page)
	}
	taskFactsHistory(t, f, f.owner, inspect.GrantID, q.AgentID, afterFact)
}

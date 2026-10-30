package agents

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type taskFactsVisibility struct {
	*taskFactsRig
	reader                             *Agent
	query                              TaskFactsQuery
	root                               GrantSpec
	ready, blocked, active, dependency TaskView
}

func taskFactsVisibilityFixture(t *testing.T) *taskFactsVisibility {
	t.Helper()
	ctx := context.Background()
	f := newTaskFactsRig(t, serviceDeps{})
	v := &taskFactsVisibility{taskFactsRig: f}
	v.dependency = f.task(t, 390, f.other, nil, f.other.AgentID)
	v.ready = f.task(t, 400, f.other, nil, f.owner.AgentID, f.other.AgentID)
	f.execution(t, v.ready)
	outsideRegion := f.task(t, 410, f.other, nil, f.owner.AgentID, f.other.AgentID)
	f.execution(t, outsideRegion)
	v.blocked = f.task(t, 420, f.other, []uuid.UUID{v.dependency.Task.TaskID}, f.owner.AgentID, f.other.AgentID)
	outsideAudience := f.task(t, 430, f.other, nil, f.other.AgentID)
	f.execution(t, outsideAudience)
	v.active = f.transition(t, f.task(t, 440, f.other, nil, f.owner.AgentID, f.other.AgentID), "start")
	f.transition(t, f.task(t, 450, f.other, nil, f.owner.AgentID, f.other.AgentID), "start")
	f.transition(t, f.transition(t, f.task(t, 460, f.other, nil, f.other.AgentID), "start"), "wait")
	f.transition(t, f.transition(t, f.task(t, 470, f.other, nil, f.owner.AgentID, f.other.AgentID), "start"), "submit")
	f.task(t, 480, Binding{}, nil, f.owner.AgentID, f.other.AgentID)
	f.transition(t, f.task(t, 490, f.other, nil, f.owner.AgentID, f.other.AgentID), "cancel")
	resources := []ledger.Resource{}
	for _, id := range []int{400, 420, 430, 440, 460, 470, 480, 490} {
		resources = append(resources, ledger.Resource{Kind: "task", TaskID: privateID(id)})
	}
	v.root = GrantSpec{GrantID: privateID(f.next()), Grantee: f.other, Operations: []string{"grant.issue", "task.inspect"}, Resources: resources, DataClass: "task", Recipients: []Binding{f.owner}, Limits: scopedCapacity(), DelegationDepth: 1, Lifetime: "until_revoked"}
	issueScopedRoot(t, f.service, f.owner, f.next(), v.root)
	child := v.root
	child.GrantID, child.Grantee, child.ParentGrantID = privateID(f.next()), f.owner, v.root.GrantID
	child.Operations, child.DelegationDepth = []string{"task.inspect"}, 0
	issuer := scopedBound(t, f.service, f.other)
	if _, err := issuer.IssueGrant(ctx, GrantIssue{CommandID: privateID(f.next()), GrantID: v.root.GrantID, ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: child}); err != nil {
		t.Fatal(err)
	}
	// A second current region does not widen the selected leaf, even though
	// it is valid for dependency visibility elsewhere in the shared evaluator.
	f.grant(t, f.owner, []string{"task.inspect"}, privateID(410), privateID(450))
	v.reader = scopedBound(t, f.service, f.owner)
	v.query = TaskFactsQuery{AgentID: f.other.AgentID, GrantID: child.GrantID, Limit: 1}
	return v
}

func (v *taskFactsVisibility) revoke(t *testing.T) {
	t.Helper()
	if _, err := v.service.RevokeGrant(context.Background(), v.owner, GrantRevocation{CommandID: privateID(v.next()), RevokeGrantID: v.root.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(v.service.p.authority)}); err != nil {
		t.Fatal(err)
	}
}

func assertTaskFactsError(t *testing.T, page TaskFactsPage, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || !reflect.DeepEqual(page, TaskFactsPage{}) {
		t.Fatalf("refusal leaked a page: %+v, error %v want %v", page, err, want)
	}
}

func taskFactsRows(page TaskFactsPage) []TaskFact {
	rows := append([]TaskFact{}, page.WaitingReady...)
	rows = append(rows, page.WaitingBlocked...)
	return append(rows, page.Active...)
}

func TestTaskFactsAuthorization(t *testing.T) {
	t.Parallel()
	// Catches selected-region unions, audience bypass, counts/look-ahead before
	// filters, inherited grants, and disclosure of hidden dependency completion.
	ctx := context.Background()
	v := taskFactsVisibilityFixture(t)
	q := v.query
	counts := TaskFactCounts{WaitingReady: 1, WaitingBlocked: 1, Active: 1}
	for i, want := range []TaskFact{taskFactFromView(v.ready, "ready", ""), taskFactFromView(v.blocked, "waiting_blocked", "dependency_unavailable"), taskFactFromView(v.active, "active", "")} {
		page, err := v.reader.ListTaskFacts(ctx, q)
		if err != nil || page.Counts != counts || !slices.Equal(taskFactsRows(page), []TaskFact{want}) {
			t.Fatalf("selected-region/audience filtering page %d: %+v %v", i, page, err)
		}
		if i < 2 {
			if page.Next == nil || page.Next.AfterTaskID != want.TaskID {
				t.Fatalf("hidden look-ahead/cursor: %+v", page)
			}
		} else if page.Next != nil {
			t.Fatalf("forbidden trailing tasks created Next: %+v", page)
		}
		q.Cursor = page.Next
	}
	// Owner is a distinct entrance, not a caller-asserted cursor field.
	ownerQuery := TaskFactsQuery{AgentID: v.other.AgentID, Limit: 20}
	ownerPage, err := v.service.ListTaskFacts(ctx, ownerQuery)
	if err != nil || ownerPage.Counts != (TaskFactCounts{WaitingReady: 3, WaitingBlocked: 3, Active: 2}) {
		t.Fatalf("owner visibility: %+v %v", ownerPage, err)
	}
	ownerQuery.Limit = 1
	ownerPage, err = v.service.ListTaskFacts(ctx, ownerQuery)
	if err != nil || ownerPage.Next == nil {
		t.Fatalf("owner cursor: %+v %v", ownerPage, err)
	}
	q = v.query
	q.Cursor = ownerPage.Next
	page, err := v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrInvalid)
	otherContext := scopedWorkContext(t, v.service, v.owner, v.next(), v.next())
	for _, binding := range []Binding{otherContext, v.other} {
		page, err := scopedBound(t, v.service, binding).ListTaskFacts(ctx, v.query)
		assertTaskFactsError(t, page, err, ErrDenied)
	}
	v.dependency = v.transition(t, v.transition(t, v.transition(t, v.dependency, "start"), "submit"), "complete")
	q = v.query
	q.Limit = 20
	page, err = v.reader.ListTaskFacts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	assertTaskFacts(t, page, v.other.AgentID, v.store.Durable(), []TaskFact{taskFactFromView(v.ready, "ready", "")}, []TaskFact{taskFactFromView(v.blocked, "waiting_blocked", "dependency_unavailable")}, []TaskFact{taskFactFromView(v.active, "active", "")})
	q.Limit = 1
	page, err = v.reader.ListTaskFacts(ctx, q)
	if err != nil || page.Next == nil {
		t.Fatalf("cursor before revocation: %+v %v", page, err)
	}
	q.Cursor = page.Next
	v.revoke(t)
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrDenied)
	q.Cursor = &TaskFactsCursor{} // precheck still wins over malformed coordinates.
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrDenied)
	q.Limit = 0
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrInvalid)
	q.Limit = v.service.limits.MaxLiveTasks + 1
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrTooLarge)
}

func TestTaskFactsPagination(t *testing.T) {
	t.Parallel()
	// Catches per-bucket limits, unordered/skipped IDs, page-local totals,
	// forged-scope authority, mixed heads, and stale-before-denied precedence.
	ctx := context.Background()
	v := taskFactsVisibilityFixture(t)
	q := v.query
	counts := TaskFactCounts{WaitingReady: 1, WaitingBlocked: 1, Active: 1}
	wantIDs := []uuid.UUID{v.ready.Task.TaskID, v.blocked.Task.TaskID, v.active.Task.TaskID}
	var cursor *TaskFactsCursor
	for i, id := range wantIDs {
		page, err := v.reader.ListTaskFacts(ctx, q)
		rows := taskFactsRows(page)
		if err != nil || len(rows) != 1 || rows[0].TaskID != id || page.Counts != counts || page.ThroughSequence != v.store.Durable() {
			t.Fatalf("one combined ordered page %d: %+v %v", i, page, err)
		}
		if i < len(wantIDs)-1 {
			want := TaskFactsCursor{ColonyID: v.store.Colony(), Actor: "agent", Origin: originOf(v.reader.ids), AgentID: q.AgentID, GrantID: q.GrantID, ThroughSequence: page.ThroughSequence, AfterTaskID: id}
			if page.Next == nil || *page.Next != want {
				t.Fatalf("cursor not last returned authorized ID: %+v", page)
			}
		} else if page.Next != nil {
			t.Fatalf("hidden/noneligible trailing ID: %+v", page.Next)
		}
		if i == 0 {
			copy := *page.Next
			cursor = &copy
		}
		q.Cursor = page.Next
	}
	q = v.query
	q.Limit = v.service.limits.MaxLiveTasks
	page, err := v.reader.ListTaskFacts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	assertTaskFacts(t, page, q.AgentID, v.store.Durable(), []TaskFact{taskFactFromView(v.ready, "ready", "")}, []TaskFact{taskFactFromView(v.blocked, "waiting_blocked", "dependency_unavailable")}, []TaskFact{taskFactFromView(v.active, "active", "")})
	q.Cursor = cursor // Limit may change between pages.
	page, err = v.reader.ListTaskFacts(ctx, q)
	if err != nil || page.Counts != counts || page.Next != nil || !slices.Equal(taskFactsRows(page), []TaskFact{taskFactFromView(v.blocked, "waiting_blocked", "dependency_unavailable"), taskFactFromView(v.active, "active", "")}) {
		t.Fatalf("changed limit continuation: %+v %v", page, err)
	}
	for _, limit := range []int{0, -1, v.service.limits.MaxLiveTasks + 1} {
		q = v.query
		q.Limit = limit
		want := ErrInvalid
		if limit > 0 {
			want = ErrTooLarge
		}
		page, err := v.reader.ListTaskFacts(ctx, q)
		assertTaskFactsError(t, page, err, want)
	}
	for _, target := range []uuid.UUID{v.owner.AgentID, privateID(999)} {
		q = v.query
		q.AgentID = target
		page, err := v.reader.ListTaskFacts(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		assertTaskFacts(t, page, target, v.store.Durable(), nil, nil, nil)
	}
	for _, mutate := range []func(*TaskFactsCursor){
		func(c *TaskFactsCursor) { c.ColonyID = privateID(999) },
		func(c *TaskFactsCursor) { c.Actor = "owner" },
		func(c *TaskFactsCursor) { c.Origin = ledger.Origin{} },
		func(c *TaskFactsCursor) { c.Origin.ContextID = v.other.ContextID },
		func(c *TaskFactsCursor) { c.Origin.SessionID = privateID(999) },
		func(c *TaskFactsCursor) { c.Origin.AgentID = v.other.AgentID },
		func(c *TaskFactsCursor) { c.AgentID = v.owner.AgentID },
		func(c *TaskFactsCursor) { c.GrantID = v.root.GrantID },
		func(c *TaskFactsCursor) { c.AfterTaskID = uuid.UUID{} },
		func(c *TaskFactsCursor) { c.AfterTaskID[6] = 0x40 },
		func(c *TaskFactsCursor) { c.ThroughSequence = 0 },
	} {
		copy := *cursor
		mutate(&copy)
		q = v.query
		q.Cursor = &copy
		page, err := v.reader.ListTaskFacts(ctx, q)
		assertTaskFactsError(t, page, err, ErrInvalid)
	}
	q = v.query
	q.AgentID = uuid.UUID{}
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrInvalid)
	q = v.query
	q.AgentID[6] = 0x40
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrInvalid)
	page, err = v.service.ListTaskFacts(ctx, v.query)
	assertTaskFactsError(t, page, err, ErrInvalid)
	for _, unbound := range []*Agent{nil, {}} {
		page, err := unbound.ListTaskFacts(ctx, v.query)
		assertTaskFactsError(t, page, err, ErrNotBound)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	page, err = v.reader.ListTaskFacts(cancelled, TaskFactsQuery{})
	assertTaskFactsError(t, page, err, context.Canceled)
	// Coordinates need not name an existing or visible task. They confer no
	// authority and Next must come from the next row, not the forged value.
	forged := *cursor
	forged.AfterTaskID = privateID(415)
	q = v.query
	q.Cursor = &forged
	page, err = v.reader.ListTaskFacts(ctx, q)
	if err != nil || len(page.WaitingBlocked) != 1 || page.WaitingBlocked[0].TaskID != v.blocked.Task.TaskID || page.Next == nil || page.Next.AfterTaskID != v.blocked.Task.TaskID {
		t.Fatalf("scalar coordinate handling: %+v %v", page, err)
	}
	reopenScoped(t, v.serviceFixture, DefaultLimits())
	v.reader = scopedBound(t, v.service, v.owner)
	q = v.query
	q.Cursor = cursor
	page, err = v.reader.ListTaskFacts(ctx, q)
	if err != nil || page.Counts != counts || !slices.Equal(taskFactsRows(page), []TaskFact{taskFactFromView(v.blocked, "waiting_blocked", "dependency_unavailable")}) {
		t.Fatalf("same-head cursor after reopen: %+v %v", page, err)
	}
	updates := []func(){
		func() { v.ready = v.transition(t, v.ready, "start") },
		func() {
			capacity := scopedCapacity()
			capacity.LocalUnits++
			if _, err := v.service.ConfigureCapacity(ctx, v.owner, CapacitySpec{CommandID: privateID(v.next()), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: capacity}); err != nil {
				t.Fatal(err)
			}
		},
		func() { scopedWorkContext(t, v.service, v.owner, v.next(), v.next()) },
	}
	for i, update := range updates {
		q = v.query
		first, err := v.reader.ListTaskFacts(ctx, q)
		if err != nil || first.Next == nil {
			t.Fatalf("before update: %+v %v", first, err)
		}
		update()
		q.Cursor = first.Next
		page, err := v.reader.ListTaskFacts(ctx, q)
		assertTaskFactsError(t, page, err, ErrTaskFactsStale)
		q.Cursor = nil
		q.Limit = v.service.limits.MaxLiveTasks
		page, err = v.reader.ListTaskFacts(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		assertTaskFacts(t, page, q.AgentID, v.store.Durable(), nil, []TaskFact{taskFactFromView(v.blocked, "waiting_blocked", "dependency_unavailable")}, []TaskFact{taskFactFromView(v.ready, "active", ""), taskFactFromView(v.active, "active", "")})
		if page.ThroughSequence <= first.ThroughSequence {
			t.Fatalf("update %d not committed", i)
		}
	}
	q = v.query
	page, err = v.reader.ListTaskFacts(ctx, q)
	if err != nil || page.Next == nil {
		t.Fatalf("before revoke: %+v %v", page, err)
	}
	q.Cursor = page.Next
	v.revoke(t)
	page, err = v.reader.ListTaskFacts(ctx, q)
	assertTaskFactsError(t, page, err, ErrDenied)
	if err := v.service.Close(); err != nil {
		t.Fatal(err)
	}
	page, err = v.reader.ListTaskFacts(ctx, TaskFactsQuery{})
	assertTaskFactsError(t, page, err, ErrClosed)
}

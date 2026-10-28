package agents

import (
	"context"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Faults affect acceleration only; every assertion below observes the service,
// its durable frontier or its exposure rather than the representation of a hint.
func damageHistoryProofs(s *Service, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch mode {
	case "missing":
		s.p.identities = identityMaxima{}
	case "damaged":
		s.p.identities.task = uuid.UUID{}
	case "stale":
		s.p.identities.through--
	}
}

func damageContextAssociations(s *Service, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range s.p.scopes {
		switch mode {
		case "missing":
			state.association = contextAssociation{}
		case "damaged":
			state.association.task = uuid.UUID{}
		case "stale":
			state.association.accepted = s.p.through + 1
		}
	}
}

func TestHistoryIndexContextBinding(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"intact", "missing", "damaged", "stale"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			owner := registerPrivate(t, f.service, 100)
			old := scopedWorkContext(t, f.service, owner, 110, 111)
			next := scopedWorkContext(t, f.service, owner, 120, 121)
			unused := scopedWorkContext(t, f.service, owner, 130, 131)
			scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
			grant := privateScopedGrant(old, 500, scopedCapacity(), "private.write", "turn.start")
			issueScopedRoot(t, f.service, owner, 501, grant)
			freshGrant := privateScopedGrant(unused, 510, scopedCapacity(), "turn.start")
			issueScopedRoot(t, f.service, owner, 511, freshGrant)
			creation := scopedNewTask(401, 400, uuid.UUID{}, owner.AgentID)
			creation.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(600), Assignee: ledger.Binding(old)}
			if _, err := f.service.CreateTask(ctx, owner, creation); err != nil {
				t.Fatal(err)
			}
			// Both revision families replace scope state without unbinding it.
			if _, err := f.service.WritePrivate(ctx, memoryWrite(old, 402, 1, "retained context")); err != nil {
				t.Fatal(err)
			}
			if _, err := scopedBound(t, f.service, old).WritePrivate(ctx, ScopedPrivateWrite{CommandID: privateID(405), GrantID: grant.GrantID, Scope: "context", ExpectedVersion: 2, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("scoped context")}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ReassignTask(ctx, owner, TaskAssignment{CommandID: privateID(403), TaskID: creation.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(600), ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(601), Assignee: ledger.Binding(next)}}); err != nil {
				t.Fatal(err)
			}
			scopedCreate(t, f.service, owner, 410, 411, owner.AgentID)
			for phase := range 3 {
				if phase == 1 {
					if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: privateID(404), TaskID: creation.TaskID, ExpectedVersion: 2, ExpectedAssignmentID: privateID(601), ExpectedGeneration: 2, Action: "cancel"}); err != nil {
						t.Fatal(err)
					}
				}
				if phase == 2 {
					reopenScoped(t, f, DefaultLimits())
				}
				damageContextAssociations(f.service, mode)
				agent := scopedBound(t, f.service, old)
				assertReviewRefusal(t, f, func() error {
					_, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(700), TurnID: privateID(701), GrantID: grant.GrantID})
					return err
				}, ErrDenied)
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.AssignTask(ctx, owner, TaskAssignment{CommandID: privateID(702), TaskID: privateID(410), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(703), Assignee: ledger.Binding(old)}})
					return err
				}, ErrInvalid)
				view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: creation.TaskID})
				state, version := "waiting", int64(2)
				if phase != 0 {
					state, version = "cancelled", 3
				}
				if err != nil || view.Task.State != state || view.Task.Version != version || view.Task.Assignment == nil || view.Task.Assignment.Assignee != ledger.Binding(next) || view.Task.Assignment.Generation != 2 {
					t.Fatalf("historical binding changed current task: %+v %v", view, err)
				}
			}
			// An actually never-assigned context still admits unbound work after
			// the same faults, and may subsequently receive an assignment.
			fresh := scopedBound(t, f.service, unused)
			start := TurnAdmission{CommandID: privateID(710), TurnID: privateID(711), GrantID: freshGrant.GrantID}
			if _, err := fresh.StartTurn(ctx, start); err != nil {
				t.Fatalf("never-assigned context refused: %v", err)
			}
			if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(712), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "released"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.AssignTask(ctx, owner, TaskAssignment{CommandID: privateID(713), TaskID: privateID(410), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(550), Assignee: ledger.Binding(unused)}}); err != nil {
				t.Fatalf("fresh out-of-order assignment refused: %v", err)
			}
		})
	}
}

func TestHistoryIndexIdentityReuse(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"intact", "missing", "damaged", "stale"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			owner := registerPrivate(t, f.service, 100)
			scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
			turnGrant := privateScopedGrant(owner, 800, scopedCapacity(), "turn.start")
			issueScopedRoot(t, f.service, owner, 801, turnGrant)
			// The same bytes are valid in all four independent namespaces. The
			// second generation is bytewise lower, not a UUID clock restriction.
			for i, id := range []int{900, 400} {
				if i == 1 {
					damageHistoryProofs(f.service, mode)
				}
				work := scopedWorkContext(t, f.service, owner, 110+i*10, 111+i*10)
				task := scopedNewTask(1000+i*20, id, uuid.UUID{}, owner.AgentID)
				task.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(id), Assignee: ledger.Binding(work)}
				if _, err := f.service.CreateTask(ctx, owner, task); err != nil {
					t.Fatalf("fresh task/assignment %d: %v", id, err)
				}
				if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: privateID(1001 + i*20), TaskID: task.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(id), ExpectedGeneration: 1, Action: "cancel"}); err != nil {
					t.Fatal(err)
				}
				grant := privateScopedGrant(owner, id, scopedCapacity(), "context.inspect")
				issued := issueScopedRoot(t, f.service, owner, 1002+i*20, grant)
				if _, err := f.service.RevokeGrant(ctx, owner, GrantRevocation{CommandID: privateID(1003 + i*20), RevokeGrantID: grant.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(issued.Version)}); err != nil {
					t.Fatal(err)
				}
				agent := scopedBound(t, f.service, owner)
				if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(1004 + i*20), TurnID: privateID(id), GrantID: turnGrant.GrantID}); err != nil {
					t.Fatalf("fresh work %d: %v", id, err)
				}
				if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(1005 + i*20), TurnID: privateID(id), ExpectedVersion: 1, Outcome: "released"}); err != nil {
					t.Fatal(err)
				}
			}
			unused := scopedWorkContext(t, f.service, owner, 140, 141)
			scopedCreate(t, f.service, owner, 450, 451, owner.AgentID)
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
					damageHistoryProofs(f.service, mode)
				}
				// A suffix after loss of proof must not become a complete-history
				// maximum: the higher, pruned identities still exist.
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.CreateTask(ctx, owner, scopedNewTask(1100, 900, uuid.UUID{}, owner.AgentID))
					return err
				}, ErrRevision)
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: privateID(1101), ExpectedAuthorityVersion: 5, Spec: privateScopedGrant(owner, 900, scopedCapacity(), "context.inspect")})
					return err
				}, ErrInvalid)
				agent := scopedBound(t, f.service, owner)
				assertReviewRefusal(t, f, func() error {
					_, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(1102), TurnID: privateID(900), GrantID: turnGrant.GrantID})
					return err
				}, ErrInvalid)
				assertReviewRefusal(t, f, func() error {
					_, err := f.service.AssignTask(ctx, owner, TaskAssignment{CommandID: privateID(1103), TaskID: privateID(450), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(900), Assignee: ledger.Binding(unused)}})
					return err
				}, ErrAssignment)
				view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(450)})
				if err != nil || view.Task.Version != 1 || view.Task.Assignment != nil {
					t.Fatalf("reused assignment changed task: %+v %v", view, err)
				}
				work, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: privateID(900)})
				if err != nil || !work.Ended || work.Held != (Exposure{}) {
					t.Fatalf("reused work revived exposure: %+v %v", work, err)
				}
			}
		})
	}
}

func TestHistoryIndexFamilyTransitions(t *testing.T) {
	t.Parallel()
	for _, verdict := range []string{"approve", "fail"} {
		t.Run(verdict, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, q := liveReviewFixture(t)
			q.Binding.Reviewer = reviewOwnerPrincipal()
			old := Binding(q.Binding.Assignee)
			grant := privateScopedGrant(old, 1500, scopedCapacity(), "turn.start")
			q.Binding.AuthorizationRevision = issueScopedRoot(t, f.service, reviewOwner(), 1501, grant).Version
			var receipt ReviewReceipt
			var retry func() (ReviewReceipt, error)
			state := "completed"
			if verdict == "approve" {
				_, agent, result := requestLiveReview(t, f, q)
				retry = func() (ReviewReceipt, error) { return resolveLiveReview(f, agent, result) }
			} else {
				state = "failed"
				request := FailureRequest{CommandID: privateID(1510), RequestID: privateID(1511), Binding: q.Binding, Cause: "semantic_failure", FailingEvidence: q.Binding.Subject}
				r, err := f.service.RequestFailure(ctx, reviewOwner(), request)
				if err != nil {
					t.Fatal(err)
				}
				result := FailureResult{CommandID: privateID(1512), RequestID: request.RequestID, RequestSequence: int64(r.EffectSequence), Binding: q.Binding, Cause: request.Cause, FailingEvidence: request.FailingEvidence}
				retry = func() (ReviewReceipt, error) { return f.service.ResolveFailure(ctx, reviewOwner(), result) }
			}
			var err error
			receipt, err = retry()
			if err != nil {
				t.Fatal(err)
			}
			scopedCreate(t, f.service, reviewOwner(), 1520, 1521, old.AgentID)
			for phase := range 2 {
				if phase == 1 {
					reopenScoped(t, f, DefaultLimits())
				}
				for _, mode := range []string{"intact", "damaged", "missing"} {
					damageContextAssociations(f.service, mode)
					view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
					if err != nil || view.Task.State != state || view.Task.Version != q.Binding.TaskVersion+1 || view.Task.Assignment == nil || view.Task.Assignment.Assignee != q.Binding.Assignee {
						t.Fatalf("review terminal state lost: %+v %v", view, err)
					}
					agent := scopedBound(t, f.service, old)
					assertReviewRefusal(t, f, func() error {
						_, err := agent.StartTurn(ctx, TurnAdmission{CommandID: privateID(1522), TurnID: privateID(1523), GrantID: grant.GrantID})
						return err
					}, ErrDenied)
					assertReviewRefusal(t, f, func() error {
						_, err := f.service.AssignTask(ctx, reviewOwner(), TaskAssignment{CommandID: privateID(1524), TaskID: privateID(1520), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(1525), Assignee: ledger.Binding(old)}})
						return err
					}, ErrInvalid)
					before := f.store.Durable()
					again, err := retry()
					if err != nil || !reflect.DeepEqual(again, receipt) || f.store.Durable() != before {
						t.Fatalf("terminal receipt changed or appended: %+v %v", again, err)
					}
				}
			}
		})
	}
}

func TestHistoryIndexHistoricalPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	owner := registerPrivate(t, f.service, 100)
	old := scopedWorkContext(t, f.service, owner, 110, 111)
	next := scopedWorkContext(t, f.service, owner, 120, 121)
	scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID)
	unassigned := f.store.Durable()
	creation := scopedNewTask(401, 400, uuid.UUID{}, owner.AgentID)
	creation.Assignment = &ledger.AssignmentInput{AssignmentID: privateID(600), Assignee: ledger.Binding(old)}
	if _, err := f.service.CreateTask(ctx, owner, creation); err != nil {
		t.Fatal(err)
	}
	assigned := f.store.Durable()
	if _, err := f.service.ReassignTask(ctx, owner, TaskAssignment{CommandID: privateID(402), TaskID: creation.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: privateID(600), ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(601), Assignee: ledger.Binding(next)}}); err != nil {
		t.Fatal(err)
	}
	grant := privateScopedGrant(owner, 500, scopedCapacity(), "turn.start")
	issueScopedRoot(t, f.service, owner, 501, grant)
	if _, err := scopedBound(t, f.service, owner).StartTurn(ctx, TurnAdmission{CommandID: privateID(700), TurnID: privateID(701), GrantID: grant.GrantID}); err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	origin := ledger.Origin{AgentID: owner.AgentID, SessionID: privateID(101), ContextID: old.ContextID}
	if _, found, err := f.service.contextTask(ctx, origin, unassigned); err != nil || found {
		t.Fatalf("later association borrowed by earlier prefix: found=%v %v", found, err)
	}
	task, found, err := f.service.contextTask(ctx, origin, assigned)
	if err != nil || !found || task.record.Version != 1 || task.record.Assignment == nil || task.record.Assignment.Assignee != ledger.Binding(old) {
		t.Fatalf("later assignment borrowed by earlier task: %+v %v", task.record, err)
	}
	if _, found, err := f.service.loadTask(ctx, creation.TaskID, unassigned); err != nil || found {
		t.Fatalf("future task existed before creation: found=%v %v", found, err)
	}
	if _, found, err := f.service.loadGrant(ctx, grant.GrantID, assigned); err != nil || found {
		t.Fatalf("future grant existed before issue: found=%v %v", found, err)
	}
	if _, found, err := f.service.loadWork(ctx, privateID(701), assigned); err != nil || found {
		t.Fatalf("future admission existed before work: found=%v %v", found, err)
	}
	idUnused, contextUnused, err := f.service.assignmentUnused(ctx, *creation.Assignment, unassigned)
	if err != nil || !idUnused || !contextUnused {
		t.Fatalf("future assignment consumed earlier identity: id=%v context=%v %v", idUnused, contextUnused, err)
	}
}

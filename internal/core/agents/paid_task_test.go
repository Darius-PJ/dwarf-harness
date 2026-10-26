package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func paidTaskAuthorityRows(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"wrong-generation", "wrong-assignment", "reassignment-before-admission"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			ctx := context.Background()
			owner := bindingOf(f.origin)
			work := scopedWorkContext(t, f.service, owner, 3000, 3001)
			task := scopedNewTask(3002, 3003, ledger.CredentialRef{}.Version, owner.AgentID)
			task.Assignment = &ledger.AssignmentInput{AssignmentID: f.id(), Assignee: ledger.Binding(work)}
			if _, err := f.service.CreateTask(ctx, owner, task); err != nil {
				t.Fatal(err)
			}
			binding := &ledger.TaskBinding{TaskID: task.TaskID, AssignmentID: task.Assignment.AssignmentID, Generation: 1}
			model := GrantSpec{GrantID: f.id(), Grantee: work, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{work}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked", TaskBinding: binding}
			if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: model}); err != nil {
				t.Fatal(err)
			}
			f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: model.GrantID}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"})
			turn := model
			turn.GrantID = f.id()
			turn.Operations = []string{"turn.start"}
			turn.Resources = []ledger.Resource{{Kind: "context", Binding: ledger.Binding(work), Scope: "context", Slots: []string{}}}
			turn.DataClass = "private"
			if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: turn}); err != nil {
				t.Fatal(err)
			}
			a := scopedBound(t, f.service, work)
			parent := f.id()
			if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: f.id(), TaskID: task.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: binding.AssignmentID, ExpectedGeneration: 1, Action: "start"}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.StartTurn(ctx, TurnAdmission{CommandID: f.id(), TurnID: parent, GrantID: turn.GrantID, TaskBinding: binding}); err != nil {
				t.Fatal(err)
			}
			input := f.input()
			input.GrantID = model.GrantID
			input.ParentTurnID = parent
			input.TaskBinding = binding
			var err error
			before := f.store.Durable()
			if name == "reassignment-before-admission" {
				p, e := f.service.PrepareAnthropic(ctx, work, input)
				if e != nil {
					t.Fatal(e)
				}
				defer p.Close()
				fresh := scopedWorkContext(t, f.service, owner, 3010, 3011)
				if _, e = f.service.ReassignTask(ctx, owner, TaskAssignment{CommandID: f.id(), TaskID: task.TaskID, ExpectedVersion: 2, ExpectedAssignmentID: binding.AssignmentID, ExpectedGeneration: 1, Assignment: ledger.AssignmentInput{AssignmentID: f.id(), Assignee: ledger.Binding(fresh)}}); e != nil {
					t.Fatal(e)
				}
				before = f.store.Durable()
				_, _, err = a.AdmitPaid(ctx, PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: parent, TaskBinding: binding, Prepared: p})
			} else {
				wrong := *binding
				input.TaskBinding = &wrong
				if name == "wrong-generation" {
					wrong.Generation++
				} else {
					wrong.AssignmentID = f.id()
				}
				_, err = f.service.PrepareAnthropic(ctx, work, input)
			}
			if !errors.Is(err, ErrDenied) || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
				t.Fatalf("stale task admitted: %v", err)
			}
		})
	}
}

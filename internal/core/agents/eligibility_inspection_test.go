package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// Publish an owner-authorized frozen record, not a live S3 entrance. Real Open
// and the reducer, not a hand-installed projection, select the declaration.
func eligibilityDeclaration(t testing.TB, s *Service, agent uuid.UUID, turn ledger.EligibilityTurn) uint64 {
	t.Helper()
	s.mu.Lock()
	a := s.p.agents[agent]
	through := s.p.through
	s.mu.Unlock()
	command, action := uuid.NewV7(), uuid.NewV7()
	sub := ledger.EligibilitySubmission{Schema: 8, Actor: "owner", Origin: ledger.Origin{AgentID: agent, SessionID: a.session, ContextID: a.general}, Operation: "eligibility.require_first", Arguments: ledger.EligibilityRequireFirstArguments{Reason: "first_action", ActionID: action, Turn: turn}}
	change := ledger.RuntimeEligibilityChanged{Version: 1, State: "first_action_required", Reason: "first_action", OperationID: command, FirstAction: &ledger.EligibilityFirstAction{ActionID: action, InitialTurn: turn, PermittedTurn: turn}}
	drafts, err := eligibilityDrafts(command, sub, change, through+1)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := s.store.SubmitAt(context.Background(), through, drafts[0], drafts[1:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EligibilityStatus(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	return committed.First + 1
}

func TestEligibilityInspectionAndReceivedWork(t *testing.T) {
	t.Parallel()
	t.Run("declaration_redacted", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) { t.Error("inspection dispatched a provider request") }, nil)
		work := f.workSetting(t, 71000, "Declared work")
		f.enable(t, work)
		turn := ledger.EligibilityTurn{ContextID: work.Binding.ContextID, TurnID: uuid.NewV7(), StartCommandID: uuid.NewV7(), AdmissionCommandID: uuid.NewV7(), InputSHA256: sha256.Sum256([]byte("synthetic declared input")), TaskBinding: work.TaskBinding}
		sequence := eligibilityDeclaration(t, f.service, f.origin.AgentID, turn)
		through := f.store.Durable()
		owner, err := f.service.EligibilityStatus(ctx, f.origin.AgentID)
		if err != nil || owner.FirstAction == nil || !reflect.DeepEqual(owner.FirstAction.Declaration.PermittedTurn, turn) {
			t.Fatal("owner lost declaration", err)
		}
		general := scopedBound(t, f.service, bindingOf(f.origin))
		redacted, err := general.EligibilityStatus(ctx, f.privateGrant)
		if err != nil || redacted.FirstAction != nil || redacted.State != "first_action_required" {
			t.Fatal("general context received peer declaration", err)
		}
		workOrigin := f.origin
		workOrigin.ContextID = work.Binding.ContextID
		grant, err := f.service.loopGrant(ctx, workOrigin, "context.inspect", []ledger.Resource{eligibilityInspectTarget(workOrigin)}, 0)
		if err != nil {
			t.Fatal(err)
		}
		worker := scopedBound(t, f.service, work.Binding)
		visible, err := worker.EligibilityStatus(ctx, grant)
		if err != nil || visible.FirstAction == nil {
			t.Fatal("declared context lost authorized metadata", err)
		}
		visible.FirstAction.Declaration.PermittedTurn.TaskBinding.Generation++
		visible.FirstAction.Declaration.InitialTurn.InputSHA256[0]++
		copied, err := f.service.EligibilityStatus(ctx, f.origin.AgentID)
		if err != nil || copied.FirstAction.Declaration.PermittedTurn.TaskBinding.Generation != work.TaskBinding.Generation || copied.FirstAction.Declaration.InitialTurn.InputSHA256 != turn.InputSHA256 {
			t.Fatal("caller mutated eligibility authority through a view", err)
		}
		workView, err := f.service.OpenSession(ctx, work.Binding)
		if err != nil {
			t.Fatal(err)
		}
		defer workView.Close()
		checkPage := func(view session.Session, visible bool) {
			t.Helper()
			page, err := view.Since(ctx, sequence-2, 128)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, event := range page.Events {
				if event.Type != "eligibility.changed" {
					continue
				}
				count++
				value, ok := event.Data.(session.EligibilityView)
				if !ok || (value.FirstAction != nil) != visible || event.Sequence != sequence {
					t.Fatal("Since disclosed peer/revoked declaration or wrong sequence")
				}
			}
			if count != 1 {
				t.Fatalf("eligibility event count: %d", count)
			}
		}
		checkPage(f.view, false)
		checkPage(workView, true)
		status, err := f.view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "context.status", Arguments: session.ContextStatus{}})
		if err != nil {
			t.Fatal(err)
		}
		if view := status.Data.(session.ContextView); view.Eligibility == nil || view.Eligibility.FirstAction != nil {
			t.Fatal("context status disclosed peer declaration")
		}
		if f.store.Durable() != through {
			t.Fatal("inspection appended work")
		}
		if _, err := f.service.RevokeGrant(ctx, bindingOf(f.origin), GrantRevocation{CommandID: uuid.NewV7(), RevokeGrantID: grant, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)}); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.EligibilityStatus(ctx, grant); !errors.Is(err, ErrDenied) {
			t.Fatal("revoked inspect disclosed declaration", err)
		}
		checkPage(workView, false)
	})
	t.Run("received_task_facts_and_assignee_start", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		owner := registerPrivate(t, f.service, 100)
		recipient := registerPrivate(t, f.service, 200)
		work := scopedWorkContext(t, f.service, recipient, 210, 211)
		scopedEnrol(t, f.service, owner, scopedCapacity(), 300, owner.AgentID, recipient.AgentID)
		task, assignment := uuid.NewV7(), uuid.NewV7()
		request := TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("received task"), AcceptanceSHA256: sha256.Sum256([]byte("acceptance")), Audience: []uuid.UUID{recipient.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(work)}}
		held := holdEligibility(t, f.service, recipient.AgentID)
		if _, err := f.service.CreateTask(ctx, owner, request); err != nil {
			t.Fatal("hold refused received task fact", err)
		}
		binding := &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
		for _, spec := range []GrantSpec{
			{GrantID: uuid.NewV7(), Grantee: work, Operations: []string{"task.inspect", "task.start", "task.wait", "task.resume"}, Resources: []ledger.Resource{{Kind: "task", TaskID: task}}, DataClass: "task", Recipients: []Binding{work}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: binding},
			{GrantID: uuid.NewV7(), Grantee: work, Operations: []string{"turn.start"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(work), Scope: "context", Slots: []string{}}}, DataClass: "private", Recipients: []Binding{work}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: binding},
		} {
			if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec}); err != nil {
				t.Fatal(err)
			}
		}
		view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: task})
		if err != nil || view.Readiness != "waiting_blocked" || view.Reason != "eligibility_held" || view.Task.State != "waiting" {
			t.Fatalf("held task readiness: %+v %v", view, err)
		}
		start := TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "start"}
		through := f.store.Durable()
		_, err = f.service.TransitionTask(ctx, owner, start)
		requireEligibilityHeld(t, err)
		if f.store.Durable() != through {
			t.Fatal("owner bypassed assignee hold")
		}
		releaseEligibility(t, f.service, recipient.AgentID, held)
		if _, err := f.service.TransitionTask(ctx, owner, start); err != nil {
			t.Fatal(err)
		}
		holdEligibility(t, f.service, recipient.AgentID)
		view, err = f.service.TaskStatus(ctx, TaskLookup{TaskID: task})
		if err != nil || view.Readiness != "active" || view.Task.State != "active" {
			t.Fatal("hold rewrote active task fact", err)
		}
		wait := start
		wait.CommandID, wait.ExpectedVersion, wait.Action, wait.WaitReason = uuid.NewV7(), 2, "wait", "explicit_hold"
		if _, err := f.service.TransitionTask(ctx, owner, wait); err != nil {
			t.Fatal("hold fenced non-dispatch task wait", err)
		}
		view, err = f.service.TaskStatus(ctx, TaskLookup{TaskID: task})
		if err != nil || view.Reason != "explicit_hold" {
			t.Fatal("eligibility overrode earlier wait reason", err)
		}
	})
}

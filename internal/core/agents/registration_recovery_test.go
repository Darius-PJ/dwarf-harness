package agents

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestRegistrationReviewHistoryRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, request := liveReviewFixture(t)
	owner := reviewOwner()
	task, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.Binding.TaskID})
	if err != nil || task.Task.Assignment == nil || task.Task.State != "submitted" {
		t.Fatalf("real review task prerequisite: %+v %v", task, err)
	}
	oldContext := task.Task.Assignment.Assignee
	resource := registrationRequest(owner, 13910, 13911, "review-evidence")
	resource.Audience = task.Task.Audience
	resourceReceipt, err := f.service.SetResourceRegistration(ctx, owner, resource)
	if err != nil {
		t.Fatal(err)
	}
	declaration := DeclarationChange{
		CommandID: privateID(13912), DeclarationID: privateID(13913),
		Subject:   ledger.RegistrationRef{Kind: "resource", ID: resource.ResourceID, Version: 1},
		Audience:  []uuid.UUID{owner.AgentID},
		Consumers: []ledger.DeclarationConsumer{{Kind: "context", Binding: oldContext}, {Kind: "task", TaskID: task.Task.TaskID}},
	}
	declarationReceipt, err := f.service.SetDeclaration(ctx, owner, declaration)
	if err != nil {
		t.Fatal(err)
	}
	originalResource, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: resource.Resource, ThroughSequence: resourceReceipt.Sequence})
	if err != nil {
		t.Fatal(err)
	}
	originalDeclaration, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: declaration.DeclarationID, ThroughSequence: declarationReceipt.Sequence})
	if err != nil {
		t.Fatal(err)
	}
	replacement := scopedWorkContext(t, f.service, owner, 13914, 13915)
	reassigned, err := f.service.ReassignTask(ctx, owner, TaskAssignment{
		CommandID: privateID(13916), TaskID: task.Task.TaskID, ExpectedVersion: uint64(task.Task.Version),
		ExpectedAssignmentID: task.Task.Assignment.AssignmentID, ExpectedGeneration: task.Task.Assignment.Generation,
		Assignment: ledger.AssignmentInput{AssignmentID: privateID(13917), Assignee: ledger.Binding(replacement)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Owner start still needs the new assignee's real execution grant. No turn
	// is started; these are task declarations and a real review decision.
	grant := privateScopedGrant(replacement, 13918, scopedCapacity(), "turn.start")
	issued := issueScopedRoot(t, f.service, owner, 13919, grant)
	mutation := TaskMutation{CommandID: privateID(13920), TaskID: task.Task.TaskID, ExpectedVersion: uint64(reassigned.Version), ExpectedAssignmentID: privateID(13917), ExpectedGeneration: task.Task.Assignment.Generation + 1, Action: "start"}
	started, err := f.service.TransitionTask(ctx, owner, mutation)
	if err != nil {
		t.Fatal(err)
	}
	mutation.CommandID, mutation.ExpectedVersion, mutation.Action, mutation.Submission = privateID(13921), uint64(started.Version), "submit", task.Task.Submission
	submitted, err := f.service.TransitionTask(ctx, owner, mutation)
	if err != nil {
		t.Fatal(err)
	}
	request.Binding.TaskVersion = submitted.Version
	request.Binding.AssignmentID, request.Binding.Generation, request.Binding.Assignee = mutation.ExpectedAssignmentID, mutation.ExpectedGeneration, ledger.Binding(replacement)
	request.Binding.AuthorizationRevision = issued.Version
	request.Binding.Reviewer = reviewOwnerPrincipal()
	_, reviewer, result := requestLiveReview(t, f, request)
	resolved, err := resolveLiveReview(f, reviewer, result)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: task.Task.TaskID})
	if err != nil || terminal.Task.State != "completed" || terminal.Task.Version != submitted.Version+1 || terminal.Task.Assignment.Assignee != ledger.Binding(replacement) {
		t.Fatalf("review-derived terminal/reassigned task: %+v %v", terminal, err)
	}
	revision := resource
	revision.CommandID, revision.ExpectedVersion, revision.Audience = privateID(13922), 1, []uuid.UUID{owner.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, owner, revision); err != nil {
		t.Fatal(err)
	}
	currentResource, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: resource.Resource, ThroughSequence: ledger.ToEnd})
	if err != nil {
		t.Fatal(err)
	}

	check := func() {
		t.Helper()
		through := f.store.Durable()
		recoveredTask, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: task.Task.TaskID})
		if err != nil || !reflect.DeepEqual(recoveredTask.Task, terminal.Task) {
			t.Fatalf("replay lost review-derived terminal task history: %+v %v", recoveredTask, err)
		}
		old, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: resource.Resource, ThroughSequence: originalResource.ThroughSequence})
		if err != nil || !reflect.DeepEqual(old, originalResource) {
			t.Fatalf("review traffic rewrote historical source: %+v %v", old, err)
		}
		current, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: resource.Resource, ThroughSequence: ledger.ToEnd})
		want := currentResource
		want.ThroughSequence = through
		if err != nil || !reflect.DeepEqual(current, want) {
			t.Fatalf("unrelated traffic changed current source: %+v %v", current, err)
		}
		oldDeclaration, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: declaration.DeclarationID, ThroughSequence: originalDeclaration.ThroughSequence})
		if err != nil || !reflect.DeepEqual(oldDeclaration, originalDeclaration) {
			t.Fatalf("review traffic rewrote declaration history: %+v %v", oldDeclaration, err)
		}
		currentDeclaration, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: declaration.DeclarationID, ThroughSequence: ledger.ToEnd})
		wantDeclaration := originalDeclaration
		wantDeclaration.ThroughSequence = through
		if err != nil || !reflect.DeepEqual(currentDeclaration, wantDeclaration) {
			t.Fatalf("review completion followed assignment or rewrote source pin: %+v %v", currentDeclaration, err)
		}
		pinned, err := f.service.RegistrationVersion(ctx, RegistrationVersionLookup{Ref: declaration.Subject, ThroughSequence: ledger.ToEnd})
		wantSource := originalResource
		wantSource.ThroughSequence = through
		if err != nil || pinned.Resource == nil || !reflect.DeepEqual(*pinned.Resource, wantSource) {
			t.Fatalf("exact source pin borrowed revision two: %+v %v", pinned, err)
		}
		for _, command := range []uuid.UUID{reassigned.CommandID, request.CommandID, result.CommandID} {
			collision := resource
			collision.CommandID = command
			if _, err := f.service.SetResourceRegistration(ctx, owner, collision); !errors.Is(err, ErrCommandMismatch) {
				t.Fatalf("scoped/review command reused by registration: %s %v", command, err)
			}
		}
		collision := request
		collision.CommandID = resource.CommandID
		if _, err := f.service.RequestReview(ctx, owner, collision); !errors.Is(err, ErrCommandMismatch) {
			t.Fatalf("registration command reused by review: %v", err)
		}
		for _, receipt := range []RegistrationReceipt{resourceReceipt, declarationReceipt} {
			actual := registrationStoredReceipt(t, f.store, receipt.CommandID)
			again, err := retryRegistration(ctx, f.service, receipt.RuntimeRegistrationReceipt)
			if err != nil || !reflect.DeepEqual(again, actual) || !reflect.DeepEqual(again, receipt) {
				t.Fatalf("review traffic lost original registration receipt: %+v %v", again, err)
			}
		}
		review, err := f.service.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: result.CommandID, SubmissionSHA256: resolved.SubmissionSHA256})
		if err != nil || !reflect.DeepEqual(review, resolved) || f.store.Durable() != through {
			t.Fatalf("cross-family retry/collision changed durable review: %+v %v", review, err)
		}
	}
	check()
	segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	before := readTestFile(t, segment)
	reopenScoped(t, f, DefaultLimits())
	check()
	if !bytes.Equal(before, readTestFile(t, segment)) {
		t.Fatal("cross-family reconstruction or matching retries appended")
	}
	// A new declaration after restart must find the terminal task via history,
	// including its review result, even though it is no longer a live task.
	terminalDeclaration := declaration
	terminalDeclaration.CommandID, terminalDeclaration.DeclarationID, terminalDeclaration.Subject.Version = privateID(13923), privateID(13924), 2
	if _, err := f.service.SetDeclaration(ctx, owner, terminalDeclaration); err != nil {
		t.Fatalf("review-derived terminal task was treated as absent: %v", err)
	}
	view, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: terminalDeclaration.DeclarationID, ThroughSequence: ledger.ToEnd})
	if err != nil || !reflect.DeepEqual(view.Record.Consumers, declaration.Consumers) || view.Record.Subject != terminalDeclaration.Subject {
		t.Fatalf("terminal declaration lost stable task/exact old context: %+v %v", view, err)
	}
}

func TestRegistrationPaidHistoryRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, commits := openPaidFixture(t, -1)
	root, err := f.service.GrantStatus(ctx, GrantLookup{GrantID: paidGoldenID(0x410)})
	if err != nil || !root.Current {
		t.Fatalf("existing F3-13 external grant: %+v %v", root, err)
	}
	owner := Binding(root.Grant.Grantee)
	outsider := registerPrivate(t, f.service, 13930)
	address := root.Grant.Resources[0]
	if !reflect.DeepEqual(address, ledger.Resource{Kind: "external", ResourceKey: "paid-route/" + paidGoldenID(0x400).String()}) {
		t.Fatalf("fixture did not supply the actual F3-13 address: %+v", address)
	}
	delegate := func(command, id int) ScopedReceipt {
		t.Helper()
		spec := GrantSpec{GrantID: privateID(id), ParentGrantID: root.Grant.GrantID, Grantee: owner, Operations: []string{"model.call"}, Resources: []ledger.Resource{address}, DataClass: "external", Recipients: []Binding{owner}, Limits: root.Grant.Limits, Lifetime: "until_revoked"}
		request := GrantIssue{CommandID: privateID(command), GrantID: root.Grant.GrantID, ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec}
		receipt, err := scopedBound(t, f.service, owner).IssueGrant(ctx, request)
		if err != nil {
			t.Fatalf("existing grant permission depends on registration: %v", err)
		}
		return receipt
	}
	// Real delegation, not just grant metadata, works before registration.
	delegated := delegate(13940, 13941)
	paidBefore, err := f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: paidGoldenID(0x501)})
	if err != nil {
		t.Fatal(err)
	}
	budgetBefore, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}, Period: "2026-10-24"})
	if err != nil {
		t.Fatal(err)
	}
	resource := registrationRequest(owner, 13942, 13943, address.ResourceKey)
	resource.Audience = []uuid.UUID{owner.AgentID, outsider.AgentID}
	first, err := f.service.SetResourceRegistration(ctx, owner, resource)
	if err != nil {
		t.Fatalf("opaque F3-13 address required provider setup: %v", err)
	}
	old, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: address, ThroughSequence: first.Sequence})
	if err != nil {
		t.Fatal(err)
	}
	declaration := DeclarationChange{CommandID: privateID(13944), DeclarationID: privateID(13945), Subject: ledger.RegistrationRef{Kind: "resource", ID: resource.ResourceID, Version: 1}, Audience: []uuid.UUID{owner.AgentID}, Consumers: []ledger.DeclarationConsumer{{Kind: "context", Binding: ledger.Binding(owner)}}}
	declReceipt, err := f.service.SetDeclaration(ctx, owner, declaration)
	if err != nil {
		t.Fatal(err)
	}
	declBefore, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: declaration.DeclarationID, ThroughSequence: declReceipt.Sequence})
	if err != nil {
		t.Fatal(err)
	}
	revision := resource
	revision.CommandID, revision.ExpectedVersion, revision.Audience = privateID(13946), 1, []uuid.UUID{outsider.AgentID}
	if _, err := f.service.SetResourceRegistration(ctx, owner, revision); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: address, ThroughSequence: ledger.ToEnd})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.service.Snapshot(ctx, ReadBinding{Binding: owner, Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	private := writePrivate(t, f.service, owner, 13947, snapshot.Revision, Change{Slot: "memory", Mode: "replace", Bytes: []byte("unrelated private history\n")})
	paid := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, commits[len(commits)-1].Records[2].Payload)
	check := func() {
		t.Helper()
		through := f.store.Durable()
		grant, err := f.service.GrantStatus(ctx, GrantLookup{GrantID: root.Grant.GrantID})
		wantGrant := root
		wantGrant.ThroughSequence = through
		if err != nil || !reflect.DeepEqual(grant, wantGrant) {
			t.Fatalf("registration changed independent external authority: %+v %v", grant, err)
		}
		if _, err := scopedBound(t, f.service, outsider).GrantStatus(ctx, GrantLookup{GrantID: root.Grant.GrantID}); !errors.Is(err, ErrDenied) {
			t.Fatalf("registration audience disclosed another principal's grant: %v", err)
		}
		attempt, err := f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: paidBefore.Admission.AttemptID})
		wantPaid := paidBefore
		wantPaid.ThroughSequence = through
		if err != nil || !reflect.DeepEqual(attempt, wantPaid) {
			t.Fatalf("registration changed paid history or initiated work: %+v %v", attempt, err)
		}
		budget, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}, Period: "2026-10-24"})
		wantBudget := budgetBefore
		wantBudget.ThroughSequence = through
		if err != nil || !reflect.DeepEqual(budget, wantBudget) {
			t.Fatalf("registration charged or changed paid accounting: %+v %v", budget, err)
		}
		historical, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: address, ThroughSequence: old.ThroughSequence})
		if err != nil || !reflect.DeepEqual(historical, old) {
			t.Fatalf("private/paid history rewrote old resource: %+v %v", historical, err)
		}
		latest, err := f.service.ResourceStatus(ctx, ResourceRegistrationLookup{Resource: address, ThroughSequence: ledger.ToEnd})
		wantCurrent := current
		wantCurrent.ThroughSequence = through
		if err != nil || !reflect.DeepEqual(latest, wantCurrent) {
			t.Fatalf("unrelated private history changed resource: %+v %v", latest, err)
		}
		for _, cursor := range []uint64{declBefore.ThroughSequence, ledger.ToEnd} {
			view, err := f.service.DeclarationStatus(ctx, RegistrationLookup{ID: declaration.DeclarationID, ThroughSequence: cursor})
			want := declBefore
			if cursor == ledger.ToEnd {
				want.ThroughSequence = through
			}
			if err != nil || !reflect.DeepEqual(view, want) {
				t.Fatalf("unrelated history changed declaration/pin: %+v %v", view, err)
			}
		}
		pinned, err := f.service.RegistrationVersion(ctx, RegistrationVersionLookup{Ref: declaration.Subject, ThroughSequence: ledger.ToEnd})
		wantSource := old
		wantSource.ThroughSequence = through
		if err != nil || pinned.Resource == nil || !reflect.DeepEqual(*pinned.Resource, wantSource) {
			t.Fatalf("pin borrowed narrowed current scope: %+v %v", pinned, err)
		}
		for _, command := range []uuid.UUID{paid.CommandID, delegated.CommandID, private.CommandID} {
			collision := resource
			collision.CommandID = command
			if _, err := f.service.SetResourceRegistration(ctx, owner, collision); !errors.Is(err, ErrCommandMismatch) {
				t.Fatalf("paid/scoped/private command reused by registration: %s %v", command, err)
			}
		}
		if _, err := f.service.ConfigureCapacity(ctx, owner, CapacitySpec{CommandID: first.CommandID, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: scopedCapacity()}); !errors.Is(err, ErrCommandMismatch) {
			t.Fatalf("registration command reused by scoped family: %v", err)
		}
		for _, receipt := range []RegistrationReceipt{first, declReceipt} {
			actual := registrationStoredReceipt(t, f.store, receipt.CommandID)
			again, err := retryRegistration(ctx, f.service, receipt.RuntimeRegistrationReceipt)
			if err != nil || !reflect.DeepEqual(again, receipt) || !reflect.DeepEqual(again, actual) {
				t.Fatalf("cross-family traffic lost original receipt: %+v %v", again, err)
			}
		}
		if f.store.Durable() != through {
			t.Fatal("passive queries, refusals or matching retries appended")
		}
	}
	check()
	segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	before := readTestFile(t, segment)
	reopenScoped(t, f, DefaultLimits())
	check()
	if !bytes.Equal(before, readTestFile(t, segment)) {
		t.Fatal("registration/paid reconstruction appended or rewrote history")
	}
	// Removing the grantee from evidence scope did not revoke its authority.
	delegate(13948, 13949)
	check()
}

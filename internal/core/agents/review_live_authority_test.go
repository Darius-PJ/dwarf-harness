package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func issueReviewRoot(t *testing.T, f *serviceFixture, id int, grantee Binding, recipients []Binding, operations []string, task uuid.UUID) uuid.UUID {
	t.Helper()
	spec := GrantSpec{GrantID: privateID(id), Grantee: grantee, Operations: operations, Resources: []ledger.Resource{{Kind: "task", TaskID: task}}, DataClass: "task", Recipients: recipients, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	scopedRoot(t, f.service, reviewOwner(), uint64(f.service.p.authority), id+1, spec)
	return spec.GrantID
}

func reviewAncestorChain(t *testing.T, f *serviceFixture, grantee Binding, recipients []Binding, task uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	owner := reviewOwner()
	root := GrantSpec{GrantID: privateID(1350), Grantee: owner, Operations: []string{"grant.issue", "task.complete", "task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: task}}, DataClass: "task", Recipients: append([]Binding{owner}, recipients...), Limits: scopedCapacity(), Lifetime: "until_revoked", DelegationDepth: 1}
	scopedRoot(t, f.service, owner, uint64(f.service.p.authority), 1351, root)
	leaf := root
	leaf.GrantID, leaf.ParentGrantID, leaf.Grantee, leaf.DelegationDepth = privateID(1352), root.GrantID, grantee, 0
	leaf.Operations, leaf.Recipients = []string{"task.complete", "task.inspect"}, recipients
	_, err := scopedBound(t, f.service, owner).IssueGrant(context.Background(), GrantIssue{CommandID: privateID(1353), GrantID: root.GrantID, ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: leaf})
	if err != nil {
		t.Fatal(err)
	}
	return leaf.GrantID, root.GrantID
}

func TestReviewAuthorityAndDisclosure(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"unrelated-issue", "requester-leaf", "reviewer-leaf", "requester-ancestor", "reviewer-ancestor"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			q.GrantID = reviewGoldenID(0x300)
			revoked := q.GrantID
			if change == "reviewer-leaf" {
				revoked = q.Binding.Reviewer.GrantID
			}
			if change == "requester-ancestor" || change == "reviewer-ancestor" {
				grantee := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)}
				recipients := []Binding{grantee, bindingOf(q.Binding.Reviewer.Origin)}
				if change == "reviewer-ancestor" {
					grantee = bindingOf(q.Binding.Reviewer.Origin)
					recipients = []Binding{grantee}
				}
				leaf, root := reviewAncestorChain(t, f, grantee, recipients, q.Binding.TaskID)
				revoked = root
				if change == "requester-ancestor" {
					q.GrantID = leaf
				} else {
					q.Binding.Reviewer.GrantID = leaf
				}
				q.Binding.AuthorizationRevision = f.service.p.authority
			}
			_, a, r := requestLiveReview(t, f, q)
			if change == "unrelated-issue" {
				issueReviewRoot(t, f, 1360, reviewOwner(), []Binding{reviewOwner()}, []string{"task.inspect"}, reviewGoldenID(0x201))
				if _, err := resolveLiveReview(f, a, r); err != nil {
					t.Fatalf("unrelated authority revision staled callback: %v", err)
				}
			} else {
				_, err := f.service.RevokeGrant(context.Background(), reviewOwner(), GrantRevocation{CommandID: privateID(1362), RevokeGrantID: revoked, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
				if err != nil {
					t.Fatal(err)
				}
				assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, a, r); return err }, ErrDenied)
			}
		})
	}
	t.Run("exact-reviewer-origin-and-principal", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		_, a, r := requestLiveReview(t, f, q)
		ctx := context.Background()
		otherContext := scopedWorkContext(t, f.service, bindingOf(q.Binding.Reviewer.Origin), 1375, 1376)
		wrongContext := scopedBound(t, f.service, otherContext)
		assertReviewRefusal(t, f, func() error { _, err := wrongContext.ResolveReview(ctx, r); return err }, ErrDenied)
		for _, field := range []string{"context", "session", "actor"} {
			wrong := r
			switch field {
			case "context":
				wrong.Binding.Reviewer.Origin.ContextID = otherContext.ContextID
			case "session":
				wrong.Binding.Reviewer.Origin.SessionID = privateID(1370)
			case "actor":
				wrong.Binding.Reviewer.Actor, wrong.Binding.Reviewer.GrantID = "owner", uuid.UUID{}
			}
			assertReviewRefusal(t, f, func() error { _, err := a.ResolveReview(ctx, wrong); return err }, ErrDenied)
		}
		assertReviewRefusal(t, f, func() error { _, err := f.service.ResolveReview(ctx, reviewOwner(), r); return err }, ErrInvalid)
	})
	t.Run("authorized-owner-versus-claimed-owner", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		ctx := context.Background()
		requesterBinding := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)}
		requester := scopedBound(t, f.service, requesterBinding)
		q.GrantID, q.Binding.Reviewer = reviewGoldenID(0x300), reviewOwnerPrincipal()
		assertReviewRefusal(t, f, func() error { _, err := requester.RequestReview(ctx, q); return err }, ErrDenied)
		q.GrantID = issueReviewRoot(t, f, 1380, requesterBinding, []Binding{requesterBinding, reviewOwner()}, []string{"task.complete", "task.inspect"}, q.Binding.TaskID)
		q.Binding.AuthorizationRevision = f.service.p.authority
		_, _, r := requestLiveReview(t, f, q)
		claimed := r
		claimed.GrantID = q.GrantID
		assertReviewRefusal(t, f, func() error { _, err := requester.ResolveReview(ctx, claimed); return err }, ErrDenied)
		if _, err := f.service.ResolveReview(ctx, reviewOwner(), r); err != nil {
			t.Fatalf("explicit owner reviewer refused: %v", err)
		}
	})
	t.Run("decision-authority-not-inspect-or-a-role", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		binding := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)}
		q.GrantID = issueReviewRoot(t, f, 1390, binding, []Binding{binding, bindingOf(q.Binding.Reviewer.Origin)}, []string{"task.inspect"}, q.Binding.TaskID)
		q.Binding.AuthorizationRevision = f.service.p.authority
		a := scopedBound(t, f.service, binding)
		assertReviewRefusal(t, f, func() error { _, err := a.RequestReview(context.Background(), q); return err }, ErrDenied)
	})
	t.Run("hidden-and-missing-receipts", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		receipt, _, _ := requestLiveReview(t, f, q)
		outsider := scopedBound(t, f.service, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd5)})
		for _, id := range []uuid.UUID{q.CommandID, privateID(1399)} {
			view, err := outsider.ReviewCommandStatus(context.Background(), ReviewCommandLookup{CommandID: id, GrantID: reviewGoldenID(0x301), SubmissionSHA256: receipt.SubmissionSHA256})
			if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(view, ReviewReceipt{}) {
				t.Fatalf("receipt existence leaked: %+v %v", view, err)
			}
		}
	})
}

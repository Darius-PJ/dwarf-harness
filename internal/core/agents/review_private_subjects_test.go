package agents

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reviewPrivateWrite(t *testing.T, f *serviceFixture, binding Binding, scope, slot, mode string, data []byte, command int) ledger.SubjectRef {
	t.Helper()
	ctx := context.Background()
	snapshot, err := f.service.Snapshot(ctx, ReadBinding{Binding: binding, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	r, err := f.service.WritePrivate(ctx, PrivateWrite{Binding: binding, CommandID: privateID(command), Scope: scope, ExpectedVersion: snapshot.Revision, Changes: []Change{{Slot: slot, Mode: mode, Bytes: data}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := ledger.PrivateRevisionRef{Binding: ledger.Binding(binding), Scope: scope, Slot: slot, Revision: int64(r.Revision), SHA256: ledger.Digest(sha256.Sum256(data)), Bytes: int64(len(data))}
	if err := f.service.scan(ctx, before, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypePrivateRevision {
				ref.SourceSequence = int64(record.Seq)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ledger.SubjectRef{Kind: "private_revision", PrivateRevision: &ref}
}

func privateFailureRequest(t *testing.T, f *serviceFixture, q ReviewRequest, subject ledger.SubjectRef) FailureRequest {
	t.Helper()
	v, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: reviewGoldenID(0x201)})
	if err != nil {
		t.Fatal(err)
	}
	b := q.Binding
	b.TaskID, b.TaskVersion, b.AssignmentID, b.Generation, b.Assignee = v.Task.TaskID, v.Task.Version, v.Task.Assignment.AssignmentID, v.Task.Assignment.Generation, v.Task.Assignment.Assignee
	b.AcceptanceSHA256, b.Reviewer, b.AuthorizationRevision = v.Task.AcceptanceSHA256, reviewOwnerPrincipal(), f.service.p.authority
	b.Subject, b.RequiredEvidence = subject, []ledger.SubjectRef{subject}
	return FailureRequest{CommandID: privateID(1450), RequestID: privateID(1451), Binding: b, Cause: "semantic_failure", FailingEvidence: subject}
}

func TestReviewPrivateSubjects(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{"other-context", "other-scope", "wrong-sequence", "wrong-revision", "unchanged-later-slot", "clear", "hash", "size"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			binding := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd4)}
			slot := "memory"
			if shape == "clear" {
				slot = "continuation"
			}
			subject := reviewPrivateWrite(t, f, binding, "context", slot, "replace", []byte("exact memory"), 1400)
			ref := *subject.PrivateRevision
			switch shape {
			case "other-context":
				ref.Binding.ContextID = reviewGoldenID(0xd3)
			case "other-scope":
				ref.Scope = "agent"
			case "wrong-sequence":
				ref.SourceSequence++
			case "wrong-revision":
				ref.Revision++
			case "unchanged-later-slot":
				later := reviewPrivateWrite(t, f, binding, "context", "handoff", "replace", []byte("other slot"), 1401)
				ref.Revision, ref.SourceSequence = later.PrivateRevision.Revision, later.PrivateRevision.SourceSequence
			case "clear":
				later := reviewPrivateWrite(t, f, binding, "context", slot, "clear", nil, 1401)
				ref.Revision, ref.SourceSequence = later.PrivateRevision.Revision, later.PrivateRevision.SourceSequence
			case "hash":
				ref.SHA256[0] ^= 1
			case "size":
				ref.Bytes++
			}
			subject.PrivateRevision = &ref
			r := privateFailureRequest(t, f, q, subject)
			assertReviewRefusal(t, f, func() error { _, err := f.service.RequestFailure(context.Background(), reviewOwner(), r); return err }, ErrInvalid)
		})
	}
	t.Run("equal-chunks-remain-distinct", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		binding := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd4)}
		first := reviewPrivateWrite(t, f, binding, "context", "transcript", "append", []byte("same chunk"), 1410)
		second := reviewPrivateWrite(t, f, binding, "context", "transcript", "append", []byte("same chunk"), 1411)
		r := privateFailureRequest(t, f, q, first)
		receipt, err := f.service.RequestFailure(context.Background(), reviewOwner(), r)
		if err != nil {
			t.Fatal(err)
		}
		result := FailureResult{CommandID: privateID(1412), RequestID: r.RequestID, RequestSequence: int64(receipt.EffectSequence), Binding: r.Binding, Cause: r.Cause, FailingEvidence: r.FailingEvidence}
		wrong := result
		wrong.Binding.Subject, wrong.Binding.RequiredEvidence, wrong.FailingEvidence = second, []ledger.SubjectRef{second}, second
		assertReviewRefusal(t, f, func() error {
			_, err := f.service.ResolveFailure(context.Background(), reviewOwner(), wrong)
			return err
		}, ErrInvalid)
		if _, err := f.service.ResolveFailure(context.Background(), reviewOwner(), result); err != nil {
			t.Fatal(err)
		}
		view, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: r.Binding.TaskID})
		if err != nil || !equalSubject(subjectValue(view.Task.FailureEvidence), first) || equalSubject(subjectValue(view.Task.FailureEvidence), second) {
			t.Fatalf("same bytes selected different occurrence: %+v %v", view, err)
		}
	})
	for _, scope := range []string{"context", "agent"} {
		t.Run(scope+"-read-chain", func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			ctx := context.Background()
			binding := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd4)}
			subject := reviewPrivateWrite(t, f, binding, scope, "memory", "replace", []byte("private subject"), 1420)
			reviewer := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)}
			grant := issueReviewRoot(t, f, 1421, reviewer, []Binding{reviewer}, []string{"task.fail", "task.inspect"}, reviewGoldenID(0x201))
			read := GrantSpec{GrantID: privateID(1423), Grantee: reviewer, Operations: []string{"private.read"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(reviewer), Scope: scope, Slots: []string{"memory"}}}, DataClass: "private", Recipients: []Binding{reviewer}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
			scopedRoot(t, f.service, reviewOwner(), uint64(f.service.p.authority), 1424, read)
			r := privateFailureRequest(t, f, q, subject)
			r.Binding.Reviewer = ledger.ReviewPrincipal{Actor: "agent", Origin: ledger.Origin{AgentID: reviewer.AgentID, SessionID: reviewGoldenID(0xb1), ContextID: reviewer.ContextID}, GrantID: grant}
			if scope == "context" {
				assertReviewRefusal(t, f, func() error { _, err := f.service.RequestFailure(ctx, reviewOwner(), r); return err }, ErrDenied)
				return
			}
			receipt, err := f.service.RequestFailure(ctx, reviewOwner(), r)
			if err != nil {
				t.Fatalf("same-agent memory reference refused: %v", err)
			}
			_, err = f.service.RevokeGrant(ctx, reviewOwner(), GrantRevocation{CommandID: privateID(1425), RevokeGrantID: read.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
			if err != nil {
				t.Fatal(err)
			}
			a := scopedBound(t, f.service, reviewer)
			result := FailureResult{CommandID: privateID(1426), GrantID: grant, RequestID: r.RequestID, RequestSequence: int64(receipt.EffectSequence), Binding: r.Binding, Cause: r.Cause, FailingEvidence: r.FailingEvidence}
			assertReviewRefusal(t, f, func() error { _, err := a.ResolveFailure(ctx, result); return err }, ErrDenied)
		})
	}
	t.Run("artifact-audience-route", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		q.GrantID = reviewGoldenID(0x300)
		_, a, r := requestLiveReview(t, f, q)
		if _, err := resolveLiveReview(f, a, r); err != nil {
			t.Fatalf("task.inspect audience lost artifact route: %v", err)
		}
	})
}

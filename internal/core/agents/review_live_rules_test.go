package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reviewOwnerPrincipal() ledger.ReviewPrincipal {
	return ledger.ReviewPrincipal{Actor: "owner", Origin: ledger.Origin{AgentID: reviewGoldenID(0xa1), SessionID: reviewGoldenID(0xb1), ContextID: reviewGoldenID(0xd1)}}
}

func requestLiveReview(t *testing.T, f *serviceFixture, q ReviewRequest) (ReviewReceipt, *Agent, ReviewResult) {
	t.Helper()
	ctx := context.Background()
	var receipt ReviewReceipt
	var err error
	if q.GrantID == (uuid.UUID{}) {
		receipt, err = f.service.RequestReview(ctx, reviewOwner(), q)
	} else {
		requester := scopedBound(t, f.service, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)})
		receipt, err = requester.RequestReview(ctx, q)
	}
	if err != nil {
		t.Fatal(err)
	}
	var reviewer *Agent
	if q.Binding.Reviewer.Actor == "agent" {
		reviewer = scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
	}
	return receipt, reviewer, ReviewResult{CommandID: privateID(1150), GrantID: q.Binding.Reviewer.GrantID, RequestID: q.RequestID, RequestSequence: int64(receipt.EffectSequence), Binding: q.Binding, Verdict: "approve"}
}

func resolveLiveReview(f *serviceFixture, reviewer *Agent, r ReviewResult) (ReviewReceipt, error) {
	if reviewer == nil {
		return f.service.ResolveReview(context.Background(), reviewOwner(), r)
	}
	return reviewer.ResolveReview(context.Background(), r)
}

func assertReviewRefusal(t *testing.T, f *serviceFixture, action func() error, want error) {
	t.Helper()
	before := f.store.Durable()
	book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil {
		t.Fatal(err)
	}
	err = action()
	if !errors.Is(err, want) {
		t.Fatalf("refusal = %v, want %v", err, want)
	}
	after, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || f.store.Durable() != before || !reflect.DeepEqual(book, after) {
		t.Fatalf("refusal changed frontier/book: %+v %+v %v", book, after, err)
	}
}

func testReviewExactBinding(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*ReviewResult)
	}{
		{"subject-source", func(r *ReviewResult) { r.Binding.Subject.Artifact.SourceSequence++ }},
		{"subject-size", func(r *ReviewResult) { r.Binding.Subject.Artifact.Bytes++ }},
		{"acceptance", func(r *ReviewResult) { r.Binding.AcceptanceSHA256[0] ^= 1 }},
		{"check", func(r *ReviewResult) { r.Binding.CheckSHA256[0] ^= 1 }},
		{"config", func(r *ReviewResult) { r.Binding.ConfigSHA256[0] ^= 1 }},
		{"evidence", func(r *ReviewResult) {
			r.Binding.RequiredEvidence = []ledger.SubjectRef{{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 1, SHA256: ledger.Digest{1}, Bytes: 1}}}
		}},
		{"task-version", func(r *ReviewResult) { r.Binding.TaskVersion++ }},
		{"assignment", func(r *ReviewResult) { r.Binding.AssignmentID = privateID(1151) }},
		{"generation", func(r *ReviewResult) { r.Binding.Generation++ }},
		{"assignee", func(r *ReviewResult) { r.Binding.Assignee.ContextID = reviewGoldenID(0xd1) }},
		{"deadline", func(r *ReviewResult) { r.Binding.DeadlineUTCNS++ }},
		{"authorization-revision", func(r *ReviewResult) { r.Binding.AuthorizationRevision++ }},
		{"request-sequence", func(r *ReviewResult) { r.RequestSequence++ }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			q.Binding.Reviewer = reviewOwnerPrincipal()
			_, a, r := requestLiveReview(t, f, q)
			row.change(&r)
			assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, a, r); return err }, ErrInvalid)
			v, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: q.Binding.TaskID})
			if err != nil || v.Task.State != "submitted" || v.Task.Version != q.Binding.TaskVersion {
				t.Fatalf("mismatched result changed task: %+v %v", v, err)
			}
		})
	}
}

func testReviewFailureTransitions(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"waiting", "active", "submitted"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			prefix := 17
			if state == "active" {
				prefix = 16
			}
			f, q := liveReviewAt(t, prefix)
			ctx := context.Background()
			id := reviewGoldenID(0x201)
			if state != "waiting" {
				id = q.Binding.TaskID
			}
			v, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: id})
			if err != nil {
				t.Fatal(err)
			}
			b := q.Binding
			b.TaskID, b.TaskVersion, b.AssignmentID, b.Generation, b.Assignee = id, v.Task.Version, v.Task.Assignment.AssignmentID, v.Task.Assignment.Generation, v.Task.Assignment.Assignee
			b.AcceptanceSHA256, b.Reviewer = v.Task.AcceptanceSHA256, reviewOwnerPrincipal()
			b.Subject = ledger.SubjectRef{Kind: "artifact", Artifact: v.DescriptionRef}
			b.RequiredEvidence = []ledger.SubjectRef{b.Subject}
			request := FailureRequest{CommandID: privateID(1161), RequestID: privateID(1162), Binding: b, Cause: "semantic_failure", FailingEvidence: b.Subject}
			receipt, err := f.service.RequestFailure(ctx, reviewOwner(), request)
			if err != nil {
				t.Fatal(err)
			}
			result := FailureResult{CommandID: privateID(1163), RequestID: request.RequestID, RequestSequence: int64(receipt.EffectSequence), Binding: b, Cause: request.Cause, FailingEvidence: request.FailingEvidence}
			wrong := result
			wrong.Cause = "different_failure"
			assertReviewRefusal(t, f, func() error { _, err := f.service.ResolveFailure(ctx, reviewOwner(), wrong); return err }, ErrInvalid)
			resolved, err := f.service.ResolveFailure(ctx, reviewOwner(), result)
			if err != nil {
				t.Fatal(err)
			}
			v, err = f.service.TaskStatus(ctx, TaskLookup{TaskID: id})
			if err != nil || v.Task.State != "failed" || v.Task.Version != b.TaskVersion+1 || v.Task.FailureCause != request.Cause || !equalSubject(subjectValue(v.Task.FailureEvidence), request.FailingEvidence) || v.Task.Submission != nil || v.Task.WaitArtifact != nil {
				t.Fatalf("failure lost exact state: %+v %v", v, err)
			}
			unpins := 0
			if err := f.service.scan(ctx, receipt.Sequence, resolved.Sequence, func(c ledger.Commit) error {
				for _, record := range c.Records {
					if record.Type == ledger.TypeBodyUnpin {
						u := scopedDecode(t, ledger.DecodeBodyUnpin, record.Payload)
						if u.PinID != id {
							t.Fatal("wrong description unpinned")
						}
						unpins++
					}
				}
				return nil
			}); err != nil || unpins != 1 {
				t.Fatalf("failure unpins=%d: %v", unpins, err)
			}
		})
	}
}

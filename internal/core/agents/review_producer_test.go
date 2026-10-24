package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func liveReviewFixture(t *testing.T) (*serviceFixture, ReviewRequest) {
	t.Helper()
	return liveReviewAt(t, 17)
}

func liveReviewAt(t *testing.T, prefix int) (*serviceFixture, ReviewRequest) {
	t.Helper()
	f, commits := openReviewFixture(t, prefix)
	q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, commits[17].Records[1].Payload)
	sample := f.service.p.reviewFloor + 315360000000000000
	f.service.deps.reviewClock = func() (int64, error) { return sample, nil }
	q.Binding.DeadlineUTCNS = sample + 1000000000
	return f, ReviewRequest{CommandID: privateID(1100), RequestID: privateID(1101), Binding: q.Binding}
}

func TestReviewExactBindingAndTransitions(t *testing.T) {
	t.Parallel()
	t.Run("exact-binding", testReviewExactBinding)
	t.Run("failure-states", testReviewFailureTransitions)
	f, request := liveReviewFixture(t)
	ctx := context.Background()
	q, err := f.service.RequestReview(ctx, reviewOwner(), request)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.service.BindAgent(ctx, bindingOf(request.Binding.Reviewer.Origin))
	if err != nil {
		t.Fatal(err)
	}
	result := ReviewResult{CommandID: privateID(1102), GrantID: request.Binding.Reviewer.GrantID, RequestID: request.RequestID, RequestSequence: int64(q.EffectSequence), Binding: request.Binding, Verdict: "reject"}
	if _, err := a.ResolveReview(ctx, result); err != nil {
		t.Fatal(err)
	}
	v, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: request.Binding.TaskID})
	if err != nil || v.Task.State != "submitted" || v.Task.Version != request.Binding.TaskVersion {
		t.Fatalf("rejection changed task: %+v %v", v, err)
	}
	if _, err := f.service.TransitionTask(ctx, reviewOwner(), reviewTaskMutation(t, f.service, "complete")); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("rejection removed guard: %v", err)
	}
	request.CommandID, request.ReplacesRequestID, request.RequestID = privateID(1103), request.RequestID, privateID(1104)
	q, err = f.service.RequestReview(ctx, reviewOwner(), request)
	if err != nil {
		t.Fatal(err)
	}
	result.CommandID, result.RequestID, result.RequestSequence, result.Verdict = privateID(1105), request.RequestID, int64(q.EffectSequence), "approve"
	if _, err := a.ResolveReview(ctx, result); err != nil {
		t.Fatal(err)
	}
	v, err = f.service.TaskStatus(ctx, TaskLookup{TaskID: request.Binding.TaskID})
	if err != nil || v.Task.State != "completed" || v.Task.Version != request.Binding.TaskVersion+1 || !equalSubject(subjectValue(v.Task.Submission), request.Binding.Subject) {
		t.Fatalf("approval lost exact submission: %+v %v", v, err)
	}
}

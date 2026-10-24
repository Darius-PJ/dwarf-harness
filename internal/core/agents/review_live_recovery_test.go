package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testReviewProducerRecovery(t *testing.T) {
	t.Parallel()
	t.Run("failure-request-lost-ack-and-result-reopen", func(t *testing.T) {
		t.Parallel()
		f, commits := openReviewFixture(t, 17)
		q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, commits[23].Records[1].Payload)
		sample := f.service.p.reviewFloor + 315360000000000000
		f.service.deps.reviewClock = func() (int64, error) { return sample, nil }
		q.Binding.DeadlineUTCNS = sample + 1000000000
		request := FailureRequest{CommandID: privateID(1470), GrantID: q.RequesterGrantID, RequestID: privateID(1471), Binding: q.Binding, Cause: q.FailureCause, FailingEvidence: *q.FailingEvidence}
		requester := scopedBound(t, f.service, bindingOf(q.Requester))
		lost := errors.New("lost request acknowledgement")
		var committed ReviewReceipt
		f.service.deps.afterReviewCommit = func(r ReviewReceipt) error { committed = r; return lost }
		if _, err := requester.RequestFailure(context.Background(), request); !errors.Is(err, lost) {
			t.Fatalf("ack fault: %v", err)
		}
		before := f.store.Durable()
		f.service.deps.reviewClock = func() (int64, error) { t.Error("retry sampled"); return 0, ErrReviewClock }
		receipt, err := requester.RequestFailure(context.Background(), request)
		if err != nil || !reflect.DeepEqual(receipt, committed) || before != f.store.Durable() {
			t.Fatalf("request retry repeated effect: %+v %v", receipt, err)
		}
		f.service.deps.afterReviewCommit = nil
		f.service.deps.reviewClock = func() (int64, error) { return sample, nil }
		reviewer := scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
		result := FailureResult{CommandID: privateID(1472), GrantID: q.Binding.Reviewer.GrantID, RequestID: request.RequestID, RequestSequence: int64(receipt.EffectSequence), Binding: request.Binding, Cause: request.Cause, FailingEvidence: request.FailingEvidence}
		resolved, err := reviewer.ResolveFailure(context.Background(), result)
		if err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, DefaultLimits())
		reviewer = scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
		retry, err := reviewer.ResolveFailure(context.Background(), result)
		if err != nil || !reflect.DeepEqual(retry, resolved) {
			t.Fatalf("reopened failure receipt changed: %+v %v", retry, err)
		}
		book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 2 || book.Held != (Exposure{}) {
			t.Fatalf("recovered failure double charged: %+v %v", book, err)
		}
		view, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: q.Binding.TaskID})
		if err != nil || view.Task.State != "failed" || view.Task.Version != q.Binding.TaskVersion+1 {
			t.Fatalf("recovery lost failure: %+v %v", view, err)
		}
	})
	t.Run("cancellation-after-submit-keeps-fact", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		_, a, result := requestLiveReview(t, f, q)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.service.deps.afterScopedSubmit = cancel
		receipt, err := a.ResolveReview(ctx, result)
		if err != nil || receipt.Outcome != "approved" {
			t.Fatalf("cancel abandoned committed decision: %+v %v", receipt, err)
		}
		view, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: q.Binding.TaskID})
		if err != nil || view.Task.State != "completed" {
			t.Fatalf("committed projection missing: %+v %v", view, err)
		}
	})
	t.Run("historical-requests-after-reopen", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, q := liveReviewFixture(t)
		q.GrantID = reviewGoldenID(0x300)
		first, reviewer, result := requestLiveReview(t, f, q)
		replaced := q.RequestID
		requesterBinding := Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)}
		q.CommandID, q.RequestID, q.ReplacesRequestID = privateID(1480), privateID(1481), replaced
		second, err := scopedBound(t, f.service, requesterBinding).RequestReview(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		result.CommandID, result.RequestID, result.RequestSequence = privateID(1482), q.RequestID, int64(second.EffectSequence)
		if _, err := resolveLiveReview(f, reviewer, result); err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, DefaultLimits())
		f.service.deps.reviewClock = func() (int64, error) { return q.Binding.DeadlineUTCNS - 1, nil }
		reviewer = scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
		requester := scopedBound(t, f.service, requesterBinding)
		// Only the cold history knows these requests now. The named reviewer
		// learns that its replaced request is stale; the requester, holding
		// the same decision authority but named by neither, cannot tell a
		// replaced, a terminal and a never-used request ID apart.
		stale := result
		stale.CommandID, stale.RequestID, stale.RequestSequence = privateID(1483), replaced, int64(first.EffectSequence)
		assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, reviewer, stale); return err }, ErrReviewStale)
		claimed := stale
		claimed.GrantID, claimed.Binding.Reviewer = q.GrantID, ledger.ReviewPrincipal{Actor: "agent", Origin: first.Submission.Origin, GrantID: q.GrantID}
		for i, id := range []uuid.UUID{replaced, q.RequestID, privateID(1499)} {
			claimed.CommandID, claimed.RequestID = privateID(1484+i), id
			assertReviewRefusal(t, f, func() error { _, err := requester.ResolveReview(ctx, claimed); return err }, ErrDenied)
		}
	})
}

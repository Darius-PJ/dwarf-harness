package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestReviewDuplicateAndReplacement(t *testing.T) {
	t.Parallel()
	t.Run("agent-duplicate-terminal-and-reopen", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		q.GrantID = reviewGoldenID(0x300)
		requestReceipt, reviewer, result := requestLiveReview(t, f, q)
		requester := scopedBound(t, f.service, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)})
		committed, err := resolveLiveReview(f, reviewer, result)
		if err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		f.service.deps.reviewClock = func() (int64, error) { t.Error("duplicate sampled clock"); return q.Binding.DeadlineUTCNS + 1, nil }
		for i := range 2 {
			retry, err := resolveLiveReview(f, reviewer, result)
			if err != nil || !reflect.DeepEqual(retry, committed) {
				t.Fatalf("terminal duplicate changed result: %+v %v", retry, err)
			}
			r, err := requester.RequestReview(context.Background(), q)
			if err != nil || !reflect.DeepEqual(r, requestReceipt) {
				t.Fatalf("request duplicate hit terminal CAS: %+v %v", r, err)
			}
			if i == 0 {
				reopenScoped(t, f, DefaultLimits())
				f.service.deps.reviewClock = func() (int64, error) { t.Error("reopened duplicate sampled clock"); return 0, ErrReviewClock }
				reviewer = scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
				requester = scopedBound(t, f.service, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)})
			}
		}
		if f.store.Durable() != before {
			t.Fatal("duplicate appended")
		}
		book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 2 || book.Held != (Exposure{}) {
			t.Fatalf("duplicate charged: %+v %v", book, err)
		}
		changed := result
		changed.Binding.ConfigSHA256[0] ^= 1
		assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, reviewer, changed); return err }, ErrCommandMismatch)
		result.CommandID = privateID(1200)
		f.service.deps.reviewClock = func() (int64, error) { return q.Binding.DeadlineUTCNS - 1, nil }
		assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, reviewer, result); return err }, ErrReviewStale)
	})
	t.Run("replacement-fences-history-and-id-reuse", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		q.Binding.Reviewer = reviewOwnerPrincipal()
		_, _, oldResult := requestLiveReview(t, f, q)
		oldID := q.RequestID
		q.CommandID, q.RequestID = privateID(1210), privateID(1211)
		assertReviewRefusal(t, f, func() error { _, err := f.service.RequestReview(context.Background(), reviewOwner(), q); return err }, ErrReviewStale)
		q.ReplacesRequestID = oldID
		_, _, current := requestLiveReview(t, f, q)
		assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, nil, oldResult); return err }, ErrReviewStale)
		reused := q
		reused.CommandID, reused.RequestID, reused.ReplacesRequestID = privateID(1215), oldID, q.RequestID
		assertReviewRefusal(t, f, func() error {
			_, err := f.service.RequestReview(context.Background(), reviewOwner(), reused)
			return err
		}, ErrInvalid)
		current.CommandID = privateID(1212)
		if _, err := resolveLiveReview(f, nil, current); err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, DefaultLimits())
		f.service.deps.reviewClock = func() (int64, error) { return q.Binding.DeadlineUTCNS - 1, nil }
		for i, id := range []uuid.UUID{oldID, q.RequestID} {
			q.CommandID, q.RequestID, q.ReplacesRequestID = privateID(1213+i), id, uuid.UUID{}
			assertReviewRefusal(t, f, func() error { _, err := f.service.RequestReview(context.Background(), reviewOwner(), q); return err }, ErrInvalid)
		}
		outsider := scopedBound(t, f.service, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd5)})
		for _, id := range []uuid.UUID{oldID, privateID(1299)} {
			view, err := outsider.ReviewStatus(context.Background(), ReviewLookup{RequestID: id, GrantID: reviewGoldenID(0x301)})
			if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(view, ReviewView{}) {
				t.Fatalf("historical request disclosure: %+v %v", view, err)
			}
		}
	})
	t.Run("normalized-evidence-before-lowered-limit", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		q.Binding.Reviewer = reviewOwnerPrincipal()
		other, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: reviewGoldenID(0x201)})
		if err != nil {
			t.Fatal(err)
		}
		q.Binding.RequiredEvidence = []ledger.SubjectRef{{Kind: "artifact", Artifact: other.DescriptionRef}, q.Binding.Subject}
		receipt, err := f.service.RequestReview(context.Background(), reviewOwner(), q)
		if err != nil {
			t.Fatal(err)
		}
		q.Binding.RequiredEvidence[0], q.Binding.RequiredEvidence[1] = q.Binding.RequiredEvidence[1], q.Binding.RequiredEvidence[0]
		f.service.limits.MaxReviewEvidence = 1
		f.service.deps.reviewClock = func() (int64, error) { t.Error("matched request sampled"); return 0, ErrReviewClock }
		retry, err := f.service.RequestReview(context.Background(), reviewOwner(), q)
		if err != nil || !reflect.DeepEqual(retry, receipt) {
			t.Fatalf("set order or lower ceiling broke dedup: %+v %v", retry, err)
		}
		q.CommandID, q.ReplacesRequestID, q.RequestID = privateID(1220), q.RequestID, privateID(1221)
		f.service.deps.reviewClock = func() (int64, error) { return q.Binding.DeadlineUTCNS - 1, nil }
		assertReviewRefusal(t, f, func() error { _, err := f.service.RequestReview(context.Background(), reviewOwner(), q); return err }, ErrTooLarge)
	})
}

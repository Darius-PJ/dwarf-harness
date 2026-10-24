package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestReviewDeadlineAndClock(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		result bool
		offset int64
		want   error
	}{
		{"request-at-deadline", false, 0, ErrReviewExpired},
		{"result-at-deadline", true, 0, nil},
		{"result-past-deadline", true, 1, ErrReviewExpired},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			q.Binding.Reviewer = reviewOwnerPrincipal()
			var result ReviewResult
			if row.result {
				_, _, result = requestLiveReview(t, f, q)
			}
			f.service.deps.reviewClock = func() (int64, error) { return q.Binding.DeadlineUTCNS + row.offset, nil }
			before := f.store.Durable()
			var err error
			if row.result {
				_, err = resolveLiveReview(f, nil, result)
			} else {
				_, err = f.service.RequestReview(context.Background(), reviewOwner(), q)
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("deadline rule: %v want %v", err, row.want)
			}
			if row.want != nil && f.store.Durable() != before {
				t.Fatal("late operation committed")
			}
			if row.want == nil {
				v, err := f.service.ReviewStatus(context.Background(), ReviewLookup{RequestID: q.RequestID})
				if err != nil || v.State != "approved" || v.Result.ObservedUTCNS != q.Binding.DeadlineUTCNS {
					t.Fatalf("exact-deadline result: %+v %v", v, err)
				}
			}
		})
	}
	for _, resultPath := range []bool{false, true} {
		name := "request"
		if resultPath {
			name = "result"
		}
		t.Run(name+"-final-guard", func(t *testing.T) {
			t.Parallel()
			for _, fault := range []string{"expiry", "rollback", "error", "zero"} {
				t.Run(fault, func(t *testing.T) {
					t.Parallel()
					f, q := liveReviewFixture(t)
					q.Binding.Reviewer = reviewOwnerPrincipal()
					var result ReviewResult
					if resultPath {
						_, _, result = requestLiveReview(t, f, q)
					}
					entry := q.Binding.DeadlineUTCNS - 100
					calls := 0
					f.service.deps.reviewClock = func() (int64, error) {
						calls++
						if calls == 1 {
							return entry, nil
						}
						switch fault {
						case "expiry":
							return q.Binding.DeadlineUTCNS + 1, nil
						case "rollback":
							return entry - 1, nil
						case "error":
							return entry, errors.New("injected clock failure")
						default:
							return 0, nil
						}
					}
					want := ErrReviewClock
					if fault == "expiry" {
						want = ErrReviewExpired
					}
					assertReviewRefusal(t, f, func() error {
						if resultPath {
							_, err := resolveLiveReview(f, nil, result)
							return err
						}
						_, err := f.service.RequestReview(context.Background(), reviewOwner(), q)
						return err
					}, want)
				})
			}
		})
	}
	// Brief §3.1 step 4 judges the request, slot and task before trusted time,
	// time before the producer bounds, and the bounds before any witness is
	// read. Each row has two faults and must report the earlier one.
	for _, row := range []struct {
		name string
		want error
	}{
		{"superseded-callback-late", ErrReviewStale},
		{"superseded-callback-clock-failure", ErrReviewStale},
		{"unreplaced-request-at-deadline", ErrReviewStale},
		{"evidence-bound-at-deadline", ErrReviewExpired},
		{"missing-witness-at-deadline", ErrReviewExpired},
		{"missing-witness-over-bound", ErrTooLarge},
		{"missing-witness", ErrDenied},
	} {
		t.Run("precedence/"+row.name, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			q.Binding.Reviewer = reviewOwnerPrincipal()
			ctx := context.Background()
			late := func() (int64, error) { return q.Binding.DeadlineUTCNS, nil }
			other, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: reviewGoldenID(0x201)})
			if err != nil {
				t.Fatal(err)
			}
			missing := other.DescriptionRef
			missing.SourceSequence = int64(f.store.Durable()) + 100
			var action func() error
			switch row.name {
			case "superseded-callback-late", "superseded-callback-clock-failure", "unreplaced-request-at-deadline":
				_, _, old := requestLiveReview(t, f, q)
				replacement := q
				replacement.CommandID, replacement.RequestID, replacement.ReplacesRequestID = privateID(1290), privateID(1291), q.RequestID
				if _, err := f.service.RequestReview(ctx, reviewOwner(), replacement); err != nil {
					t.Fatal(err)
				}
				action = func() error { _, err := resolveLiveReview(f, nil, old); return err }
				if row.name == "superseded-callback-late" {
					late = func() (int64, error) { return q.Binding.DeadlineUTCNS + 1, nil }
				} else if row.name == "superseded-callback-clock-failure" {
					late = func() (int64, error) { return 0, errors.New("injected clock failure") }
				} else {
					q.CommandID, q.RequestID = privateID(1292), privateID(1293)
					action = func() error { _, err := f.service.RequestReview(ctx, reviewOwner(), q); return err }
				}
			default:
				evidence := map[string][]ledger.SubjectRef{
					"evidence-bound-at-deadline":  {{Kind: "artifact", Artifact: other.DescriptionRef}, q.Binding.Subject},
					"missing-witness-at-deadline": {{Kind: "artifact", Artifact: missing}},
					"missing-witness-over-bound":  {{Kind: "artifact", Artifact: missing}, q.Binding.Subject},
					"missing-witness":             {{Kind: "artifact", Artifact: missing}},
				}
				q.Binding.RequiredEvidence = evidence[row.name]
				f.service.limits.MaxReviewEvidence = 1
				if row.name == "missing-witness-over-bound" || row.name == "missing-witness" {
					late = func() (int64, error) { return q.Binding.DeadlineUTCNS - 1, nil }
				}
				action = func() error { _, err := f.service.RequestReview(ctx, reviewOwner(), q); return err }
			}
			f.service.deps.reviewClock = late
			assertReviewRefusal(t, f, action, row.want)
		})
	}
	t.Run("durable-floor-survives-reopen", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		q.Binding.Reviewer = reviewOwnerPrincipal()
		_, _, result := requestLiveReview(t, f, q)
		floor := f.service.p.reviewFloor
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		f.service, _, err = openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) { return floor - 1, nil }})
		if err != nil {
			t.Fatal(err)
		}
		assertReviewRefusal(t, f, func() error { _, err := resolveLiveReview(f, nil, result); return err }, ErrReviewClock)
	})
	t.Run("lost-ack-duplicate-never-samples", func(t *testing.T) {
		t.Parallel()
		f, q := liveReviewFixture(t)
		_, a, result := requestLiveReview(t, f, q)
		lost := errors.New("injected acknowledgement loss")
		var committed ReviewReceipt
		f.service.deps.afterReviewCommit = func(r ReviewReceipt) error { committed = r; return lost }
		if _, err := resolveLiveReview(f, a, result); !errors.Is(err, lost) {
			t.Fatalf("lost acknowledgement: %v", err)
		}
		before := f.store.Durable()
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		f.service, _, err = openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) {
			t.Error("Open/query/duplicate sampled clock")
			return 0, errors.New("later clock unavailable")
		}})
		if err != nil {
			t.Fatal(err)
		}
		a = scopedBound(t, f.service, bindingOf(q.Binding.Reviewer.Origin))
		if _, err := a.ReviewStatus(context.Background(), ReviewLookup{RequestID: q.RequestID, GrantID: result.GrantID}); err != nil {
			t.Fatal(err)
		}
		retry, err := resolveLiveReview(f, a, result)
		if err != nil || !reflect.DeepEqual(retry, committed) || f.store.Durable() != before {
			t.Fatalf("lost-ack retry changed history: %+v %+v %v", committed, retry, err)
		}
		book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
			t.Fatalf("lost-ack duplicate charged twice: %+v %v", book, err)
		}
	})
}

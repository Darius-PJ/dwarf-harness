package agents

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestReviewResultRaces(t *testing.T) {
	t.Parallel()
	for _, duplicate := range []bool{true, false} {
		name := "approve-versus-reject"
		if duplicate {
			name = "duplicate-callbacks"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			_, a, r := requestLiveReview(t, f, q)
			var lookups atomic.Int32
			arrived, release := make(chan struct{}, 2), make(chan struct{})
			f.service.deps.beforeLookup = func() {
				if lookups.Add(1) <= 2 {
					arrived <- struct{}{}
					<-release
				}
			}
			type outcome struct {
				receipt ReviewReceipt
				err     error
			}
			out := make(chan outcome, 2)
			for i := range 2 {
				input := r
				if !duplicate && i == 1 {
					input.CommandID, input.Verdict = privateID(1250), "reject"
				}
				go func() { receipt, err := resolveLiveReview(f, a, input); out <- outcome{receipt, err} }()
			}
			<-arrived
			<-arrived
			close(release)
			x, y := <-out, <-out
			if duplicate {
				if x.err != nil || y.err != nil || !reflect.DeepEqual(x.receipt, y.receipt) {
					t.Fatalf("duplicate winners differ: %+v %+v", x, y)
				}
			} else {
				if x.err != nil {
					x, y = y, x
				}
				if x.err != nil || !errors.Is(y.err, ErrReviewStale) {
					t.Fatalf("distinct callbacks both decided: %+v %+v", x, y)
				}
			}
			book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != 1 || book.Held != (Exposure{}) {
				t.Fatalf("race double charge/leaked preparation: %+v %v", book, err)
			}
			view, err := f.service.ReviewStatus(context.Background(), ReviewLookup{RequestID: q.RequestID})
			if err != nil || view.Result == nil || view.ResultSequence != x.receipt.EffectSequence {
				t.Fatalf("race installed wrong result: %+v %v", view, err)
			}
			task, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: q.Binding.TaskID})
			want := "completed"
			if view.State == "rejected" {
				want = "submitted"
			}
			if err != nil || task.Task.State != want || task.Task.Version != view.Result.TaskVersion {
				t.Fatalf("race task differs from winning judgment: %+v %v", task, err)
			}
		})
	}
	for _, winner := range []string{"cancel", "reassign", "revoke", "replacement"} {
		t.Run("callback-versus-"+winner, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			_, a, r := requestLiveReview(t, f, q)
			ctx := context.Background()
			var calls atomic.Int32
			prepared, release := make(chan struct{}), make(chan struct{})
			f.service.deps.beforeLookup = func() {
				if calls.Add(1) == 2 {
					close(prepared)
					<-release
				}
			}
			done := make(chan error, 1)
			go func() { _, err := resolveLiveReview(f, a, r); done <- err }()
			<-prepared
			var err error
			switch winner {
			case "cancel":
				_, err = f.service.TransitionTask(ctx, reviewOwner(), reviewTaskMutation(t, f.service, "cancel"))
			case "reassign":
				fresh := scopedWorkContext(t, f.service, reviewOwner(), 1260, 1261)
				_, err = f.service.ReassignTask(ctx, reviewOwner(), TaskAssignment{CommandID: privateID(1262), TaskID: q.Binding.TaskID, ExpectedVersion: uint64(q.Binding.TaskVersion), ExpectedAssignmentID: q.Binding.AssignmentID, ExpectedGeneration: q.Binding.Generation, Assignment: ledger.AssignmentInput{AssignmentID: privateID(1263), Assignee: ledger.Binding(fresh)}})
			case "revoke":
				_, err = f.service.RevokeGrant(ctx, reviewOwner(), GrantRevocation{CommandID: privateID(1264), RevokeGrantID: r.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
			case "replacement":
				q.CommandID, q.ReplacesRequestID, q.RequestID = privateID(1265), q.RequestID, privateID(1266)
				_, err = f.service.RequestReview(ctx, reviewOwner(), q)
			}
			before := f.store.Durable()
			close(release)
			callbackErr := <-done
			if err != nil {
				t.Fatal(err)
			}
			want := ErrReviewStale
			if winner == "revoke" {
				want = ErrDenied
			}
			if !errors.Is(callbackErr, want) || f.store.Durable() != before {
				t.Fatalf("callback escaped %s: %v", winner, callbackErr)
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != 0 || book.Held != (Exposure{}) {
				t.Fatalf("losing callback charged: %+v %v", book, err)
			}
		})
	}
}

package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reviewOwner() Binding { return Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd1)} }
func reviewTaskMutation(t *testing.T, s *Service, action string) TaskMutation {
	t.Helper()
	v, err := s.TaskStatus(context.Background(), TaskLookup{TaskID: reviewGoldenID(0x200)})
	if err != nil {
		t.Fatal(err)
	}
	r := TaskMutation{CommandID: privateID(970), TaskID: v.Task.TaskID, ExpectedVersion: uint64(v.Task.Version), ExpectedAssignmentID: v.Task.Assignment.AssignmentID, ExpectedGeneration: v.Task.Assignment.Generation, Action: action}
	if action == "complete" {
		r.Submission = v.Task.Submission
	}
	if action == "fail" {
		r.FailureCause = "semantic_failure"
		r.FailureEvidence = v.Task.Submission
	}
	return r
}

func TestReviewDirectGuard(t *testing.T) {
	t.Parallel()
	for _, prefix := range []int{18, 19} {
		for _, actor := range []string{"owner", "agent"} {
			for _, action := range []string{"complete", "fail"} {
				t.Run(string(rune('a'+prefix-18))+"/"+actor+"/"+action, func(t *testing.T) {
					t.Parallel()
					f, _ := openReviewFixture(t, prefix)
					ctx := context.Background()
					r := reviewTaskMutation(t, f.service, action)
					var agent *Agent
					if actor == "agent" {
						grant := reviewGoldenID(0x300)
						if action == "fail" {
							old := f.service.p.grants[grant].definition
							grant = privateID(971)
							_, err := f.service.IssueGrant(ctx, reviewOwner(), GrantIssue{CommandID: privateID(972), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grant, Grantee: Binding(old.Grantee), Operations: []string{"task.fail", "task.inspect"}, Resources: old.Resources, DataClass: "task", Recipients: []Binding{Binding(old.Grantee)}, Limits: old.Limits, Lifetime: "until_revoked"}})
							if err != nil {
								t.Fatal(err)
							}
						}
						r.GrantID = grant
						var err error
						agent, err = f.service.BindAgent(ctx, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)})
						if err != nil {
							t.Fatal(err)
						}
					}
					before := f.store.Durable()
					for _, stale := range []bool{false, true} {
						if stale {
							r.ExpectedVersion--
						}
						var err error
						if agent != nil {
							_, err = agent.TransitionTask(ctx, r)
						} else {
							_, err = f.service.TransitionTask(ctx, reviewOwner(), r)
						}
						if !errors.Is(err, ErrReviewRequired) {
							t.Fatalf("guard or precedence bypass (stale=%t): %v", stale, err)
						}
					}
					if f.store.Durable() != before {
						t.Fatal("direct refusal committed")
					}
				})
			}
		}
	}
	for _, action := range []string{"cancel", "reassign"} {
		t.Run(action+"-invalidates", func(t *testing.T) {
			t.Parallel()
			f, _ := openReviewFixture(t, 18)
			ctx := context.Background()
			r := reviewTaskMutation(t, f.service, action)
			if action == "cancel" {
				if _, err := f.service.TransitionTask(ctx, reviewOwner(), r); err != nil {
					t.Fatal(err)
				}
			} else {
				fresh := Binding{reviewGoldenID(0xa1), privateID(974)}
				if _, err := f.service.CreateContext(ctx, ContextCreation{Binding: fresh, CommandID: privateID(975), Kind: "work"}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.service.ReassignTask(ctx, reviewOwner(), TaskAssignment{CommandID: r.CommandID, TaskID: r.TaskID, ExpectedVersion: r.ExpectedVersion, ExpectedAssignmentID: r.ExpectedAssignmentID, ExpectedGeneration: r.ExpectedGeneration, Assignment: ledger.AssignmentInput{AssignmentID: privateID(976), Assignee: ledger.Binding(fresh)}}); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x400)})
			if err != nil || view.Current || view.State != "stale" || len(f.service.p.reviews) != 0 {
				t.Fatalf("escape kept obligation: %+v %v", view, err)
			}
		})
	}
	t.Run("Open-refuses-direct-complete", func(t *testing.T) {
		t.Parallel()
		f, _ := openReviewFixture(t, 18)
		ctx := context.Background()
		r := reviewTaskMutation(t, f.service, "complete")
		current, _, err := f.service.loadTask(ctx, r.TaskID, f.service.p.through)
		if err != nil {
			t.Fatal(err)
		}
		p := current.record
		p.Action, p.State, p.ExpectedVersion, p.Version = "complete", "completed", p.Version, p.Version+1
		generation := r.ExpectedGeneration
		sub := ledger.ScopedSubmission{Schema: 2, Actor: "owner", Origin: ledger.Origin{AgentID: reviewGoldenID(0xa1), SessionID: reviewGoldenID(0xb1), ContextID: reviewGoldenID(0xd1)}, Operation: "task.complete", Target: ledger.Resource{Kind: "task", TaskID: r.TaskID}, Expected: ledger.ScopedExpected{Version: int64(r.ExpectedVersion), AssignmentID: r.ExpectedAssignmentID, Generation: &generation}, Arguments: ledger.TaskMutationArguments{TaskID: r.TaskID, Action: "complete", Submission: r.Submission}}
		drafts := []ledger.Draft{{Type: ledger.TypeCommand, Payload: testPayload(t, ledger.Command{CommandID: r.CommandID, SubmissionSHA256: ledger.Digest{1}})}, {Type: ledger.TypeRuntimeTaskChanged, Payload: testPayload(t, p)}, {Type: ledger.TypeBodyUnpin, Actor: ledger.ActorCore, Payload: testPayload(t, ledger.BodyUnpin{PinID: r.TaskID})}, {Type: ledger.TypeRuntimeScopedReceipt}}
		for i := range drafts {
			drafts[i].TypeVersion = 1
			drafts[i].AgentID = sub.Origin.AgentID
			drafts[i].ExecutionSessionID = sub.Origin.SessionID
			drafts[i].ContextID = sub.Origin.ContextID
			drafts[i].OperationID = r.CommandID
			if i != 2 {
				drafts[i].Actor = ledger.ActorOwner
			}
		}
		scopedRewriteReceipt(t, drafts, ledger.RuntimeScopedReceipt{CommandID: r.CommandID, Submission: sub, Outcome: "applied", Version: p.Version})
		if _, err := f.store.Submit(ctx, drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		f.service = nil
		f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
		if !errors.Is(err, ErrInconsistent) || f.service != nil || f.store.Durable() != before {
			t.Fatalf("Open allowed direct bypass: %v", err)
		}
	})
	t.Run("direct-private-failure-still-refuses", func(t *testing.T) {
		t.Parallel()
		f, commits := openReviewFixture(t, 17)
		r := reviewTaskMutation(t, f.service, "fail")
		q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, commits[21].Records[1].Payload)
		r.FailureEvidence = q.FailingEvidence
		before := f.store.Durable()
		if _, err := f.service.TransitionTask(context.Background(), reviewOwner(), r); err == nil {
			t.Fatal("direct fail adopted reviewed private evidence")
		}
		if f.store.Durable() != before {
			t.Fatal("direct private failure committed")
		}
	})
}

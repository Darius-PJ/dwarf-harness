package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func reviewGoldenID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1200-0000-7000-8000-%012x", n))
}
func openReviewFixture(t testing.TB, prefix int) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	f, commits := scopedGoldenFixture(t, "12", prefix)
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return f, commits
}

// A future writer may publish a newer type; this service still consumes v1.
func reviewFutureCatalogue(t *testing.T, f *serviceFixture, commits []ledger.Commit, drafts []ledger.Draft) {
	t.Helper()
	seen := map[ledger.TypeVersion]bool{}
	for _, c := range commits {
		for _, r := range c.Records {
			seen[ledger.TypeVersion{Type: r.Type, Version: r.TypeVersion}] = true
		}
	}
	for _, d := range drafts {
		seen[ledger.TypeVersion{Type: d.Type, Version: d.TypeVersion}] = true
	}
	versions := []ledger.TypeVersion{}
	for version := range seen {
		versions = append(versions, version)
	}
	catalogue, err := ledger.NewCatalogue(versions...)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, _, err = ledgerstore.Open(f.root, catalogue)
	if err != nil {
		t.Fatal(err)
	}
}
func reviewPreflight(t *testing.T, f *serviceFixture, d []ledger.Draft) error {
	t.Helper()
	ctx := context.Background()
	baseline, _, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Close()
	_, _, g, err := decodeFamily(d, f.service.p.through+1)
	if err == nil && g == nil {
		err = ErrInvalid
	}
	if err == nil {
		_, err = f.service.planReview(ctx, g, f.service.p.through+1)
	}
	if !reflect.DeepEqual(baseline.p, f.service.p) {
		t.Fatal("preflight installed rejected or uncommitted state")
	}
	return err
}
func rewriteReviewReceipt(t *testing.T, d []ledger.Draft, r ledger.RuntimeReviewReceipt) {
	t.Helper()
	r.SubmissionSHA256 = ledger.Digest(sha256.Sum256(testPayload(t, r.Submission)))
	d[0].Payload = testPayload(t, ledger.Command{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
	d[len(d)-1].Payload = testPayload(t, r)
	for i := range d {
		d[i].OperationID = r.CommandID
	}
}

// Rebind the canonical submission after changing a typed effect. This prevents
// a semantic row from passing merely because its old digest no longer matches.
func rewriteReviewEffect(t *testing.T, d []ledger.Draft) {
	t.Helper()
	r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, d[len(d)-1].Payload)
	if d[1].Type == ledger.TypeRuntimeReviewRequested {
		q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, d[1].Payload)
		r.RequestID, r.TaskVersion = q.RequestID, q.Binding.TaskVersion
		r.Submission.Arguments = ledger.ReviewRequestArguments{RequestID: q.RequestID, Binding: q.Binding, ReplacesRequestID: q.ReplacesRequestID, FailureCause: q.FailureCause, FailingEvidence: q.FailingEvidence}
	} else {
		v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
		r.RequestID, r.TaskVersion = v.RequestID, v.TaskVersion
		if v.Verdict == "fail" {
			r.Submission.Arguments = ledger.FailureResolveArguments{RequestID: v.RequestID, RequestSequence: v.RequestSequence, Binding: v.Binding, Cause: v.Cause, FailingEvidence: *v.FailingEvidence}
		} else {
			r.Submission.Arguments = ledger.ReviewResolveArguments{RequestID: v.RequestID, RequestSequence: v.RequestSequence, Binding: v.Binding, Verdict: v.Verdict}
		}
	}
	rewriteReviewReceipt(t, d, r)
}

func TestReviewFrozenGoldenReplay(t *testing.T) {
	t.Parallel()
	for _, chapter := range []string{"10", "11", "12"} {
		t.Run(chapter, func(t *testing.T) {
			t.Parallel()
			f, commits := scopedGoldenFixture(t, chapter, -1)
			before := f.store.Durable()
			var err error
			var report Reconciliation
			f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil || len(report.Issues) != 0 || f.store.Durable() != before {
				t.Fatalf("frozen replay changed history: %+v %v", report, err)
			}
			ctx := context.Background()
			if chapter == "10" {
				work := Binding{AgentID: commits[0].Records[0].AgentID, ContextID: commits[1].Records[0].ContextID}
				page, err := f.service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: work, Scope: "context"}, Slot: "memory", Revision: 6, MaxBytes: 1024})
				if err != nil || string(page.Bytes) != "local lesson\n" {
					t.Fatalf("legacy private family lost: %+v %v", page, err)
				}
				return
			}
			if chapter == "11" {
				for _, row := range []struct {
					id      int
					state   string
					version int64
				}{{0x200, "completed", 8}, {0x201, "cancelled", 4}} {
					view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: scopedGoldenID(row.id)})
					if err != nil || view.Task.State != row.state || view.Task.Version != row.version {
						t.Fatalf("legacy task family lost: %+v %v", view, err)
					}
				}
				book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
				if err != nil || book.Used.LocalUnits != 8 || book.Held != (Exposure{}) {
					t.Fatalf("legacy book lost: %+v %v", book, err)
				}
				return
			}
			for _, row := range []struct {
				id      int
				state   string
				version int64
			}{{0x200, "completed", 4}, {0x201, "failed", 2}, {0x202, "failed", 2}} {
				view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: reviewGoldenID(row.id)})
				if err != nil || view.Task.State != row.state || view.Task.Version != row.version {
					t.Fatalf("terminal result lost task: %+v %v", view, err)
				}
				if row.id == 0x201 && (view.Task.FailureEvidence == nil || view.Task.FailureEvidence.Kind != "private_revision") {
					t.Fatal("reviewed private failure was lost")
				}
			}
			for _, row := range []struct {
				id          int
				state       string
				replacement int
			}{{0x400, "rejected", 0x401}, {0x401, "approved", 0}, {0x402, "failed", 0}, {0x403, "failed", 0}} {
				view, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(row.id)})
				if err != nil || view.State != row.state || view.Current || view.Result == nil || view.ThroughSequence != before {
					t.Fatalf("review outcome lost: %+v %v", view, err)
				}
				expected := uuid.UUID{}
				if row.replacement != 0 {
					expected = reviewGoldenID(row.replacement)
				}
				if view.SupersededByRequestID != expected {
					t.Fatal("replacement edge was invented or lost")
				}
			}
			for _, commit := range commits[17:] {
				r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, commit.Records[len(commit.Records)-1].Payload)
				receipt, err := f.service.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
				if err != nil || !reflect.DeepEqual(receipt.RuntimeReviewReceipt, r) || receipt.Sequence != commit.Last || receipt.EffectSequence != commit.First+1 {
					t.Fatalf("immutable receipt lost: %+v %v", receipt, err)
				}
				if _, err := f.service.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("scoped API adopted review receipt: %v", err)
				}
				if _, err := f.service.CommandStatus(ctx, CommandLookup{Binding: bindingOf(r.Submission.Origin), CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("private API adopted review receipt: %v", err)
				}
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != 4 || book.Held != (Exposure{}) || len(f.service.p.reviews) != 0 {
				t.Fatalf("review book or eviction: %+v %v", book, err)
			}
			for _, id := range []int{0xa1, 0xa2} {
				book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "agent", ID: reviewGoldenID(id)}})
				if err != nil || book.Used.LocalUnits != 2 || book.Held != (Exposure{}) {
					t.Fatalf("agent review charge lost: %+v %v", book, err)
				}
			}
		})
	}
}

func TestReviewLedgerRecovery(t *testing.T) {
	t.Parallel()
	t.Run("producer-recovery", testReviewProducerRecovery)
	for _, row := range []struct {
		name        string
		prefix, id  int
		state, task string
		units       int64
		current     bool
	}{{"pending", 18, 0x400, "pending", "submitted", 1, true}, {"rejected", 19, 0x400, "rejected", "submitted", 2, true}, {"approved", 21, 0x401, "approved", "completed", 2, false}, {"failed", 23, 0x402, "failed", "failed", 2, false}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, commits := openReviewFixture(t, row.prefix)
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			f.service = nil
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			f.store = nil
			root := testRoot(t)
			if err := os.CopyFS(root, os.DirFS(f.root)); err != nil {
				t.Fatal(err)
			}
			store, _, err := ledgerstore.Open(root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			before := store.Durable()
			service, _, err := Open(context.Background(), store, root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			view, err := service.ReviewStatus(context.Background(), ReviewLookup{RequestID: reviewGoldenID(row.id)})
			if err != nil || view.State != row.state || view.Current != row.current {
				t.Fatalf("recovery lost obligation: %+v %v", view, err)
			}
			task, err := service.TaskStatus(context.Background(), TaskLookup{TaskID: view.Request.Binding.TaskID})
			if err != nil || task.Task.State != row.task {
				t.Fatalf("recovery task: %+v %v", task, err)
			}
			book, err := service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used.LocalUnits != row.units {
				t.Fatalf("recovery charge: %+v %v", book, err)
			}
			last := commits[row.prefix-1]
			r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, last.Records[len(last.Records)-1].Payload)
			receipt, err := service.ReviewCommandStatus(context.Background(), ReviewCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
			if err != nil || !reflect.DeepEqual(receipt.RuntimeReviewReceipt, r) {
				t.Fatalf("recovery receipt: %+v %v", receipt, err)
			}
			if row.current {
				_, err = service.TransitionTask(context.Background(), Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd1)}, TaskMutation{CommandID: privateID(960), TaskID: task.Task.TaskID, ExpectedVersion: uint64(task.Task.Version), ExpectedAssignmentID: task.Task.Assignment.AssignmentID, ExpectedGeneration: task.Task.Assignment.Generation, Action: "complete", Submission: task.Task.Submission})
				if !errors.Is(err, ErrReviewRequired) {
					t.Fatalf("recovery cleared guard: %v", err)
				}
			}
			if store.Durable() != before {
				t.Fatal("recovery or passive view appended")
			}
		})
	}
}

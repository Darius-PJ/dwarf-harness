package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type reviewCrashInput struct {
	Reviewer             Binding
	RequestID, CommandID uuid.UUID
	Sample               int64
}

type reviewCrashSignal struct {
	Boundary                 string
	Before                   uint64
	Receipt                  []byte // the acknowledged receipt record, when one was formed
	EffectSequence, Sequence uint64
}

// Injected process exits and a deliberately truncated final record are not
// power-loss or isolation proofs. Each row copies one closed real-store prefix
// holding one pending request named to an agent reviewer, whose result charges
// one unit and, when it approves, transitions the task and unpins its body.
func TestReviewInjectedCrashRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prepared, q := liveReviewFixture(t)
	_, _, result := requestLiveReview(t, prepared, q)
	sample, err := prepared.service.deps.reviewClock()
	if err != nil {
		t.Fatal(err)
	}
	input := reviewCrashInput{Reviewer: bindingOf(q.Binding.Reviewer.Origin), RequestID: q.RequestID, CommandID: result.CommandID, Sample: sample}
	baseTask, err := prepared.service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	baseBook, err := prepared.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil {
		t.Fatal(err)
	}
	baseThrough := prepared.store.Durable()
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(input); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(prepared.root, "review-crash-input.gob"), encoded.Bytes())
	if err := prepared.service.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.service = nil
	if err := prepared.store.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.store = nil

	for _, boundary := range []string{"before_submit", "torn_result", "approval_before_projection", "rejection_lost_ack"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			root := testRoot(t)
			if err := os.CopyFS(root, os.DirFS(prepared.root)); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestReviewCrashChild$")
			cmd.Env = append(os.Environ(), "DWARF_REVIEW_CRASH_ROOT="+root, "DWARF_REVIEW_CRASH_BOUNDARY="+boundary)
			output, err := cmd.CombinedOutput() // Positively wait before opening any colony file.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != privateCrashExit {
				t.Fatalf("injected child did not exit at %s: %v\n%s", boundary, err, output)
			}
			var signal reviewCrashSignal
			if err := gob.NewDecoder(bytes.NewReader(output)).Decode(&signal); err != nil || signal.Boundary != boundary || signal.Before != baseThrough {
				t.Fatalf("wrong injected boundary signal: %+v %v\n%s", signal, err, output)
			}
			store, recovery, err := ledgerstore.Open(root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if (recovery.TruncatedBytes > 0) != (boundary == "torn_result") {
				t.Fatalf("wrong injected torn-tail recovery: %+v", recovery)
			}
			// The records of the result group that reached the log, its body
			// unpins and its one unit charge to the reviewer's scopes.
			var committed uint64
			unpinned, charged := 0, int64(0)
			switch boundary {
			case "approval_before_projection":
				committed, unpinned, charged = 4, 1, 1
			case "rejection_lost_ack":
				committed, charged = 3, 1
			}
			through := baseThrough + committed
			if store.Durable() != through {
				t.Fatalf("partial or duplicated recovered group: %d want %d", store.Durable(), through)
			}
			if consumed, unpins := reviewCrashLog(t, store, input.CommandID, q.Binding.TaskID); consumed != (committed != 0) || unpins != unpinned {
				t.Fatalf("recovered command consumed %v with %d unpins", consumed, unpins)
			}
			segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
			recoveredLog := readTestFile(t, segment)
			service, report, err := openService(ctx, store, root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) {
				t.Error("Open, a query or a matched retry sampled the review clock")
				return 0, errors.New("review clock unavailable after restart")
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if len(report.Issues) != 0 || len(report.Completed) != 0 || len(report.PendingWork) != 0 || store.Durable() != through || !bytes.Equal(recoveredLog, readTestFile(t, segment)) {
				t.Fatalf("Open appended, completed or dispatched something: %+v", report)
			}
			task, err := service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID})
			if err != nil {
				t.Fatal(err)
			}
			view, err := service.ReviewStatus(ctx, ReviewLookup{RequestID: q.RequestID})
			if err != nil {
				t.Fatal(err)
			}
			book, err := service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Held != (Exposure{}) || book.Used.LocalUnits != baseBook.Used.LocalUnits+charged {
				t.Fatalf("wrong recovered reservation or charge: %+v; base %+v; %v", book, baseBook, err)
			}
			reviewer := scopedBound(t, service, input.Reviewer)
			guarded := func() {
				t.Helper()
				if _, err := service.TransitionTask(ctx, reviewOwner(), reviewTaskMutation(t, service, "complete")); !errors.Is(err, ErrReviewRequired) || store.Durable() != through {
					t.Fatalf("recovery lifted the review guard: %v", err)
				}
			}
			switch boundary {
			case "before_submit", "torn_result":
				if !reflect.DeepEqual(task.Task, baseTask.Task) || view.State != "pending" || !view.Current || view.Result != nil {
					t.Fatalf("uncommitted result applied: %+v %+v", task, view)
				}
				guarded()
				if receipt, err := service.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: input.CommandID, SubmissionSHA256: ledger.Digest{1}}); err != ErrUnavailable || !reflect.DeepEqual(receipt, ReviewReceipt{}) {
					t.Fatalf("uncommitted receipt became visible: %+v %v", receipt, err)
				}
				// A different valid intent under the same command ID proves the
				// ID was not consumed; it then becomes exactly one decision.
				service.deps.reviewClock = func() (int64, error) { return input.Sample, nil }
				retry := result
				retry.Verdict = "reject"
				receipt, err := reviewer.ResolveReview(ctx, retry)
				if err != nil || store.Durable() != through+3 || receipt.Sequence != through+3 {
					t.Fatalf("uncommitted command ID was consumed: %+v %v", receipt, err)
				}
				if again, err := reviewer.ResolveReview(ctx, retry); err != nil || !reflect.DeepEqual(again, receipt) || store.Durable() != through+3 {
					t.Fatalf("retry did not become exactly one decision: %+v %v", again, err)
				}
			case "approval_before_projection":
				if task.Task.State != "completed" || task.Task.Version != baseTask.Task.Version+1 || view.State != "approved" || view.Current || view.Result == nil || view.ResultSequence != baseThrough+2 {
					t.Fatalf("committed approval not recovered once: %+v %+v", task, view)
				}
				receipt, err := service.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: input.CommandID, SubmissionSHA256: reviewCrashDigest(t, store, through)})
				if err != nil || receipt.Sequence != through || receipt.EffectSequence != baseThrough+2 || receipt.Outcome != "approved" {
					t.Fatalf("committed receipt not recovered: %+v %v", receipt, err)
				}
				retry, err := reviewer.ResolveReview(ctx, result)
				if err != nil || !reflect.DeepEqual(retry, receipt) {
					t.Fatalf("lost acknowledgement retry differs from the committed receipt: %+v %+v %v", retry, receipt, err)
				}
			case "rejection_lost_ack":
				if !reflect.DeepEqual(task.Task, baseTask.Task) || view.State != "rejected" || !view.Current || view.Result == nil || view.ResultSequence != baseThrough+2 {
					t.Fatalf("committed rejection not recovered as a guard: %+v %+v", task, view)
				}
				guarded()
				if len(signal.Receipt) == 0 {
					t.Fatal("missing committed rejection signal")
				}
				original := ReviewReceipt{RuntimeReviewReceipt: scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, signal.Receipt), EffectSequence: signal.EffectSequence, Sequence: signal.Sequence}
				if status, err := service.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: input.CommandID, SubmissionSHA256: original.SubmissionSHA256}); err != nil || !reflect.DeepEqual(status, original) {
					t.Fatalf("passive receipt differs from the original: %+v %+v %v", status, original, err)
				}
				retry := result
				retry.Verdict = "reject"
				if again, err := reviewer.ResolveReview(ctx, retry); err != nil || !reflect.DeepEqual(again, original) {
					t.Fatalf("lost acknowledgement retry differs from the original: %+v %+v %v", again, original, err)
				}
			}
			if committed != 0 {
				after, err := service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
				if err != nil || store.Durable() != through || !reflect.DeepEqual(after, book) {
					t.Fatalf("lost acknowledgement retry applied again: %+v %+v %v", after, book, err)
				}
				if again, err := service.TaskStatus(ctx, TaskLookup{TaskID: q.Binding.TaskID}); err != nil || !reflect.DeepEqual(again.Task, task.Task) {
					t.Fatalf("lost acknowledgement retry transitioned again: %+v %v", again, err)
				}
				if _, unpins := reviewCrashLog(t, store, input.CommandID, q.Binding.TaskID); unpins != unpinned {
					t.Fatalf("lost acknowledgement retry unpinned again: %d", unpins)
				}
			}
		})
	}
}

// reviewCrashLog reads the durable log: whether the command ID is consumed and
// how many body unpins name the task.
func reviewCrashLog(t *testing.T, store *ledgerstore.Store, command, task uuid.UUID) (bool, int) {
	t.Helper()
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: store.Durable(), Consume: ledger.Consumption{ledger.TypeCommand: {1}, ledger.TypeBodyUnpin: {1}}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	consumed, unpins := false, 0
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return consumed, unpins
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypeCommand:
				consumed = consumed || scopedDecode(t, ledger.DecodeCommand, record.Payload).CommandID == command
			case ledger.TypeBodyUnpin:
				if scopedDecode(t, ledger.DecodeBodyUnpin, record.Payload).PinID == task {
					unpins++
				}
			}
		}
	}
}

// reviewCrashDigest reads the submission digest of the receipt at sequence.
func reviewCrashDigest(t *testing.T, store *ledgerstore.Store, sequence uint64) ledger.Digest {
	t.Helper()
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: sequence, Consume: ledger.Consumption{ledger.TypeRuntimeReviewReceipt: {1}}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var digest ledger.Digest
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return digest
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range commit.Records {
			if record.Seq == sequence {
				digest = scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, record.Payload).SubmissionSHA256
			}
		}
	}
}

func TestReviewCrashChild(t *testing.T) {
	root := os.Getenv("DWARF_REVIEW_CRASH_ROOT")
	if root == "" {
		return
	} // Only the parent supplies the subprocess protocol.
	boundary := os.Getenv("DWARF_REVIEW_CRASH_BOUNDARY")
	var input reviewCrashInput
	if err := gob.NewDecoder(bytes.NewReader(readTestFile(t, filepath.Join(root, "review-crash-input.gob")))).Decode(&input); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	before, prefix := store.Durable(), readTestFile(t, segment)
	stop := func(receipt *ReviewReceipt) {
		signal := reviewCrashSignal{Boundary: boundary, Before: before}
		if receipt != nil {
			raw, err := receipt.RuntimeReviewReceipt.Encode()
			if err != nil {
				t.Fatal(err)
			}
			signal.Receipt, signal.EffectSequence, signal.Sequence = raw, receipt.EffectSequence, receipt.Sequence
		}
		if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
			t.Fatal(err)
		}
		os.Exit(privateCrashExit)
	}
	lookups := 0
	deps := serviceDeps{
		reviewClock: func() (int64, error) { return input.Sample, nil },
		// The second lookup is pass 2's, after pass 1 reserved the unit and
		// before Submit.
		beforeLookup: func() {
			if lookups++; lookups == 2 && boundary == "before_submit" {
				stop(nil)
			}
		},
		// Committed, before the projection and the acknowledgement.
		afterScopedSubmit: func() {
			if boundary == "approval_before_projection" {
				stop(nil)
			}
		},
		// Committed and projected, before the acknowledgement.
		afterReviewCommit: func(receipt ReviewReceipt) error {
			if boundary == "torn_result" {
				full := readTestFile(t, segment)
				start := bytes.LastIndexByte(full[:len(full)-1], '\n') + 1
				cut := start + (len(full)-start)/2
				if start <= len(prefix) || cut <= start || cut >= len(full) || !bytes.Equal(full[:len(prefix)], prefix) {
					t.Fatal("injected cut is not inside the new group's final record")
				}
				file, err := os.OpenFile(segment, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(int64(cut)); err != nil {
					t.Fatal(err)
				}
				if err := file.Sync(); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			stop(&receipt)
			return nil
		},
	}
	ctx := context.Background()
	service, _, err := openService(ctx, store, root, DefaultLimits(), deps)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.ReviewStatus(ctx, ReviewLookup{RequestID: input.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	result := ReviewResult{CommandID: input.CommandID, GrantID: view.Request.Binding.Reviewer.GrantID, RequestID: input.RequestID, RequestSequence: int64(view.RequestSequence), Binding: view.Request.Binding, Verdict: "approve"}
	if boundary == "rejection_lost_ack" {
		result.Verdict = "reject"
	}
	reviewer := scopedBound(t, service, input.Reviewer)
	_, err = reviewer.ResolveReview(ctx, result)
	t.Fatalf("injected boundary %q was not reached: %v", boundary, err)
}

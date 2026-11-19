package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func integratedParentSetting(t *testing.T, f *continuationCaptureFixture) LoopContextSettings {
	t.Helper()
	parent := f.contextSetting(t, Binding{AgentID: f.origin.AgentID, ContextID: f.parent.ContextID})
	task, assignment := uuid.NewV7(), uuid.NewV7()
	_, err := f.service.CreateTask(context.Background(), bindingOf(f.origin), TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("synthetic parent work"), AcceptanceSHA256: sha256.Sum256([]byte("synthetic acceptance")), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{parent.Binding.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(parent.Binding)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TransitionTask(context.Background(), bindingOf(f.origin), TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "start"}); err != nil {
		t.Fatal(err)
	}
	parent.DisplayTitle, parent.TaskBinding = "Parent task", &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
	return parent
}

func TestTurnLoopTemporaryMergeBack(t *testing.T) {
	t.Run("crash_after_prepared", testIntegratedMergeCrash)
	t.Run("workspace_close_capture_refusal", func(t *testing.T) {
		t.Parallel()
		testIntegratedWorkspaceClose(t)
	})
	for _, name := range []string{"fast_forward", "clean_three_way", "conflict", "cancel_while_parent_held"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testIntegratedMergeOutcome(t, name, false)
		})
	}
	t.Run("capacity", func(t *testing.T) {
		t.Parallel()
		// This fixture's BytesInFlight equals MaxBytes: one real capture.
		f := newContinuationCapture(t, true, nil)
		parent := integratedParentSetting(t, f)
		parentHeld, parentRelease := make(chan struct{}), make(chan struct{})
		parentObserved, observationRelease := make(chan struct{}), make(chan struct{})
		var releaseParentOnce, releaseObservationOnce sync.Once
		freeParent := func() { releaseParentOnce.Do(func() { close(parentRelease) }) }
		freeObservation := func() { releaseObservationOnce.Do(func() { close(observationRelease) }) }
		defer freeParent()
		defer freeObservation()
		var parentCalls, authorCalls atomic.Int32
		f.respond = func(w http.ResponseWriter, r *http.Request) {
			model, raw := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if bytes.Contains(raw, []byte("parent-held")) {
				if parentCalls.Add(1) == 1 {
					_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", finishBusyWrite(f.registered.ID, "parent-change")), ""))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				close(parentHeld)
				select {
				case <-parentRelease:
				case <-r.Context().Done():
					return
				}
			} else if authorCalls.Add(1) == 1 {
				_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", foreignWriteArguments(f, parent.Binding.ContextID, "safe/owned-capture.txt", "temporary capture")), ""))
				return
			}
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Merged sibling work."))
		}
		// Park after the parent's real F2 acknowledgement, while its writer
		// is still open. The wrapper holds no agents lock.
		f.store.after = func(drafts []ledger.Draft) {
			for _, draft := range drafts {
				if draft.Type != ledger.TypeScratchChanged {
					continue
				}
				v := scopedDecode(t, ledger.DecodeScratchChanged, draft.Payload)
				if v.ContextID == parent.Binding.ContextID {
					close(parentObserved)
					<-observationRelease
				}
			}
		}
		var mergeCapture atomic.Bool
		f.service.deps.beforeSubmit = func(drafts []ledger.Draft) []ledger.Draft {
			for _, draft := range drafts {
				if draft.Type == ledger.TypeRuntimeContinuationChanged {
					v := scopedDecode(t, ledger.DecodeRuntimeContinuationChanged, draft.Payload)
					if draft.ContextID == f.origin.ContextID && v.WaitReason == "workspace_merge_back" {
						mergeCapture.Store(true)
					}
				}
			}
			return drafts
		}
		freeCapture := f.holdCapture(t)
		defer freeCapture()
		f.enable(t, parent)
		// With B's model held, only A can release workspace capacity after
		// its merge park. The source capture releases its bytes before the
		// parent wait; the callback stays atomic-only like loop.released.
		sourceCaptured := make(chan struct{})
		var sourceCapturedOnce sync.Once
		f.workspace.SetReleaseNotifier(func() {
			f.service.loop.released()
			if mergeCapture.Load() {
				sourceCapturedOnce.Do(func() { close(sourceCaptured) })
			}
		})
		parentView, err := f.service.OpenSession(context.Background(), parent.Binding)
		if err != nil {
			t.Fatal(err)
		}
		defer parentView.Close()
		parentStart, err := parentView.Do(context.Background(), turnMessage(parent.Binding.ContextID, "parent-held"))
		if err != nil {
			t.Fatal(err)
		}
		<-parentHeld
		_, childStart := f.send(t)
		continuationWait(t, f.turnFixture, childStart.TurnID, "workspace_capture")
		if f.probe.count() != 0 || f.probe.attempts() == 0 {
			t.Fatal("capture park did not precede the real observation")
		}
		freeCapture()
		<-sourceCaptured
		before := foreignRecords(t, f, childStart.TurnID)
		if len(before.created) != 1 || len(before.observed) != 1 || len(before.prepared) != 0 || len(before.merged) != 0 {
			t.Fatalf("temporary escaped its held parent: %+v", before)
		}
		freeParent()
		<-parentObserved
		// A's capture and B's capture each reached CommitObserved once. A
		// still owns its source exclusion, but its capture bytes are free.
		if f.probe.count() != 2 {
			t.Fatalf("source reservation prevented the parent's single observation: passes=%d", f.probe.count())
		}
		cancel := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: childStart.TurnID}}
		accepted, err := f.view.Do(context.Background(), cancel)
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("merge wait prevented cancellation acceptance: %+v %v", accepted, err)
		}
		drained := make(chan error, 1)
		go func() { drained <- f.service.Close() }()
		freeObservation()
		if err := <-drained; err != nil {
			t.Fatal(err)
		}
		if f.probe.count() != 2 {
			t.Fatalf("shutdown repeated an observation: %d", f.probe.count())
		}
		records := foreignRecords(t, f, childStart.TurnID)
		if len(records.created) != 1 || len(records.observed) != 1 || len(records.prepared) != 1 || len(records.merged) != 1 || len(records.closed) != 1 || records.merged[0].Outcome != ledger.MergeBackMerged || records.closed[0].ScratchID != records.created[0].ScratchID {
			t.Fatalf("cancel/drain lost or duplicated the temporary: %+v", records)
		}
		for path, want := range map[string]string{"safe/owned-capture.txt": "temporary capture", "safe/parent-change.txt": "synthetic finish write parent-change", "safe/seed.txt": "parent seed\n"} {
			if got := string(readTestFile(t, filepath.Join(f.parent.Dir, filepath.FromSlash(path)))); got != want {
				t.Fatalf("merged file %s differs: %q", path, got)
			}
		}
		parentScratch, err := f.workspace.Scratch(context.Background(), parent.Binding.ContextID, f.registered.ID)
		if err != nil || parentScratch.TipTree != records.merged[0].TargetTree {
			t.Fatalf("parent did not acknowledge the merged tree: %+v %v", parentScratch, err)
		}
		ends, closes, observed := map[uuid.UUID]int{}, map[uuid.UUID]int{}, map[uuid.UUID]int{}
		waits := map[string]int{}
		if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				if record.Type == ledger.TypeScratchChanged {
					v := scopedDecode(t, ledger.DecodeScratchChanged, record.Payload)
					if v.PreviousCommit == f.parent.TipCommit && v.ContextID == parent.Binding.ContextID {
						observed[v.ContextID]++
					}
				}
				if record.TurnID != parentStart.TurnID && record.TurnID != childStart.TurnID {
					continue
				}
				switch record.Type {
				case ledger.TypeRuntimeTurnResult:
					v := scopedDecode(t, ledger.DecodeRuntimeTurnResult, record.Payload)
					ends[record.TurnID]++
					if record.TurnID == childStart.TurnID && v.Outcome != "cancelled" {
						t.Fatalf("shutdown replaced the accepted cancellation: %+v", v)
					}
				case ledger.TypeRuntimeContinuationChanged:
					v := scopedDecode(t, ledger.DecodeRuntimeContinuationChanged, record.Payload)
					if v.State == "terminal" {
						closes[record.TurnID]++
					}
					if v.State == "waiting_resource" && v.WaitReason == "workspace_capture" && record.TurnID == parentStart.TurnID {
						t.Fatal("child's source reservation blocked the parent's observation")
					}
					if record.TurnID == childStart.TurnID && v.State == "waiting_resource" {
						waits[v.WaitReason]++
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if ends[parentStart.TurnID] != 1 || ends[childStart.TurnID] != 1 || closes[parentStart.TurnID] != 1 || closes[childStart.TurnID] != 1 || observed[parent.Binding.ContextID] != 1 || waits["workspace_capture"] != 1 || waits["workspace_merge_back"] != 1 {
			t.Fatalf("drain lost durable progress: ends=%v closes=%v observations=%v waits=%v", ends, closes, observed, waits)
		}
		if err := f.workspace.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if len(report.Issues) != 0 || len(report.PendingLoops) != 0 {
			t.Fatalf("drain left interrupted bookkeeping: %+v", report)
		}
	})
}

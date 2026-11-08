package agents

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func testContinuationCaptureTail(t *testing.T) {
	t.Run("capture_resume_tail_refused", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, true, nil)
		free := f.holdCapture(t)
		barrier := &tailBarrier{effect: ledger.TypeRuntimeContinuationChanged, fires: 3}
		barrier.prepare(t, f.serviceFixture, f.workspace, f.registered.ID)
		f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			if len(d) == 3 && d[2].Type == ledger.TypeRuntimeContinuationReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, d[2].Payload)
				if v.Submission.ExpectedState == "waiting_resource" && v.Submission.Arguments.State == "finishing" {
					return barrier.submit(d)
				}
			}
			return d
		}
		f.enable(t)
		_, started := f.send(t)
		continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
		calls := f.probe.count()
		barrier.arm(f.store.Durable())
		free()
		result, _ := f.finished(t, started.TurnID)
		if result.Outcome != "finished" {
			t.Fatalf("owed resume stranded the capture: %+v", result)
		}
		if f.probe.count() != calls+1 {
			t.Fatalf("tail refusal called CommitObserved again: before=%d after=%d", calls, f.probe.count())
		}
		barrier.mu.Lock()
		moved := len(barrier.foreign)
		barrier.mu.Unlock()
		if moved != 3 {
			t.Fatalf("real conditional tail moved %d times, want 3", moved)
		}
		owners, observations, endings := continuationCaptureRecords(t, f, started.TurnID)
		var captureResume, mergePark uint64
		lastWait := ""
		for _, owner := range owners {
			if owner.Submission.Arguments.State == "waiting_resource" {
				lastWait = owner.Submission.Arguments.WaitReason
			}
			if owner.Submission.ExpectedState == "waiting_resource" && owner.Submission.Arguments.State == "finishing" && lastWait == "workspace_capture" {
				if captureResume != 0 {
					t.Fatal("capture resumed twice")
				}
				captureResume = uint64(owner.EffectSequence)
			}
			if owner.Submission.Arguments.State == "waiting_resource" && owner.Submission.Arguments.WaitReason == "workspace_merge_back" {
				if mergePark != 0 {
					t.Fatal("merge-back parked twice")
				}
				mergePark = uint64(owner.EffectSequence)
			}
		}
		if len(observations) != 1 || len(endings) != 1 || captureResume <= observations[0] || mergePark <= captureResume || endings[0] <= mergePark {
			t.Fatalf("next claim preceded owed capture resume: observations=%v resume=%d mergePark=%d endings=%v", observations, captureResume, mergePark, endings)
		}
	})
}

func continuationCaptureCrashChild(t *testing.T, boundary string, barrier func(*continuationCaptureFixture, uuid.UUID, workspace.Scratch)) {
	t.Helper()
	var f *continuationCaptureFixture
	var turn uuid.UUID
	var scratch workspace.Scratch
	f = newContinuationCapture(t, false, func(store *continuationCaptureStore) {
		store.before = func(d []ledger.Draft) {
			if boundary != "capture_before_append" {
				return
			}
			for _, draft := range d {
				if draft.Type == ledger.TypeScratchChanged {
					v := scopedDecode(t, ledger.DecodeScratchChanged, draft.Payload)
					if v.ContextID == f.origin.ContextID {
						barrier(f, turn, scratch)
					}
				}
			}
		}
	})
	f.probe.before = func() {
		// The hook is on this finish goroutine, before its workspace call.
		f.service.loop.mu.Lock()
		live := f.service.loop.turns[turn]
		f.service.loop.mu.Unlock()
		for _, writer := range live.writers {
			scratch = writer.Scratch()
		}
	}
	f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
		if boundary == "capture_after_observation" && len(d) == 3 && d[2].Type == ledger.TypeRuntimeContinuationReceipt {
			v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, d[2].Payload)
			if v.Submission.ExpectedState == "waiting_resource" && v.Submission.Arguments.State == "finishing" {
				barrier(f, turn, scratch)
			}
		}
		return d
	}
	free := f.holdCapture(t)
	f.enable(t)
	command := turnMessage(f.origin.ContextID, "capture kill boundary")
	turn = command.Arguments.(session.MessageSend).TurnID
	if _, err := f.view.Do(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	continuationWait(t, f.turnFixture, turn, "workspace_capture")
	if boundary == "capture_stop_parked" {
		stop := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: turn}}
		if _, err := f.view.Do(context.Background(), stop); err != nil {
			t.Fatal(err)
		}
		parked := continuationWait(t, f.turnFixture, turn, "workspace_capture")
		if parked.OperationID != stop.CommandID {
			t.Fatal("crash did not reach the new park under the stop")
		}
		barrier(f, turn, scratch)
	}
	free()
	f.finished(t, turn)
	t.Fatal("capture crash boundary was not reached")
}

func testContinuationCaptureRestart(t *testing.T) {
	for _, row := range []struct {
		name, boundary, outcome string
		observations            int
	}{
		{"restart_capture_before_append", "capture_before_append", "interrupted", 0},
		{"restart_capture_after_observation_before_resume", "capture_after_observation", "interrupted", 1},
		{"restart_capture_parked_under_stop", "capture_stop_parked", "cancelled", 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := continuationKilled(t, row.boundary)
			owner := f.owner(t)
			if owner.Snapshot.State != "waiting_resource" || owner.Snapshot.ResumeState != "finishing" || owner.Snapshot.WaitReason != "workspace_capture" || len(f.report.PendingLoops) != 1 || f.report.PendingLoops[0].SavedPhase != "waiting_resource" || f.report.PendingLoops[0].WaitReason != "workspace_capture" {
				t.Fatalf("restart relabelled the saved capture wait: owner=%+v report=%+v", owner.Snapshot, f.report)
			}
			wantCalls := 1
			if row.boundary == "capture_stop_parked" {
				wantCalls = 0
			}
			if f.signal.Scratch.ID == (uuid.UUID{}) || f.signal.Scratch.ParentScratchID != (uuid.UUID{}) || f.signal.CaptureCalls != wantCalls {
				t.Fatalf("kill fixture did not reach the intended dedicated capture boundary: scratch=%+v calls=%d want=%d", f.signal.Scratch, f.signal.CaptureCalls, wantCalls)
			}
			calls := 0
			f.service.deps.beforeFinishCommit = func(uuid.UUID) error { calls++; return errors.New("recovery attempted a capture") }
			ws, _, err := workspace.Open(context.Background(), f.store, f.signal.Root, workspace.Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
			receipt, err := f.service.ReconcileLoop(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			through := f.store.Durable()
			if retry, err := f.service.ReconcileLoop(context.Background(), request); err != nil || !reflect.DeepEqual(retry, receipt) || f.store.Durable() != through {
				t.Fatalf("capture recovery retry appended: %+v %v", retry, err)
			}
			observations, merges, endings, native, stops := 0, 0, 0, 0, 0
			tip := f.signal.Scratch.TipCommit
			if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
				for _, record := range c.Records {
					if record.Type == ledger.TypeScratchChanged {
						v := scopedDecode(t, ledger.DecodeScratchChanged, record.Payload)
						if v.ScratchID == f.signal.Scratch.ID {
							observations++
							tip = v.ScratchCommit
						}
					}
					if record.Type == ledger.TypeScratchMergeBack {
						v := scopedDecode(t, ledger.DecodeScratchMergeBack, record.Payload)
						if v.ScratchID == f.signal.Scratch.ID {
							merges++
						}
					}
					if record.TurnID != f.signal.Turn {
						continue
					}
					if record.Type == ledger.TypeRuntimeTurnResult {
						v := scopedDecode(t, ledger.DecodeRuntimeTurnResult, record.Payload)
						endings++
						if v.Outcome != row.outcome {
							t.Fatalf("recovery ignored persisted stop: %+v", v)
						}
					}
					if record.Type == ledger.TypeRuntimeScopedReceipt {
						v := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, record.Payload)
						if v.Submission.Operation == "turn.end" || v.Submission.Operation == "turn.cancel" {
							native++
							if row.outcome == "cancelled" && v.CommandID != owner.Snapshot.CancelCommandID {
								t.Fatal("recovery minted a new native cancel identity")
							}
						}
					}
					if record.Type == ledger.TypeRuntimeContinuationReceipt {
						v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
						if v.Submission.Operation == "owner.stop" {
							stops++
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			wantStops := 0
			if row.outcome == "cancelled" {
				wantStops = 1
			}
			if observations != row.observations || merges != 0 || calls != 0 || endings != 1 || native != 1 || stops != wantStops || f.owner(t).Snapshot.State != "terminal" {
				t.Fatalf("capture recovery repeated work: observations=%d merges=%d calls=%d endings=%d native=%d stops=%d", observations, merges, calls, endings, native, stops)
			}
			cmd := exec.CommandContext(context.Background(), "git", "-C", f.signal.Scratch.Dir, "rev-parse", "HEAD")
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != tip {
				t.Fatalf("recovery changed the acknowledged scratch tip: got=%s want=%s err=%v", out, tip, err)
			}
		})
	}
}

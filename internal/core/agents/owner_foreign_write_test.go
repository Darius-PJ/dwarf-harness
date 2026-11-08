package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type continuationForeignRecords struct {
	created              []ledger.ScratchCreated
	observed             []ledger.ScratchChanged
	prepared             []ledger.ScratchMergeBack
	preparationSequences []uint64
	merged               []ledger.ScratchMergeBack
	closed               []ledger.ScratchClosed
	mergeSequences       []uint64
	work                 []ledger.RuntimeWorkAdmitted
}

func foreignRecords(t testing.TB, f *continuationCaptureFixture, turn uuid.UUID) continuationForeignRecords {
	t.Helper()
	var out continuationForeignRecords
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			switch record.Type {
			case ledger.TypeScratchCreated:
				v := scopedDecode(t, ledger.DecodeScratchCreated, record.Payload)
				if v.ContextID == f.origin.ContextID && v.ParentScratchID != (uuid.UUID{}) {
					out.created = append(out.created, v)
				}
			case ledger.TypeScratchChanged:
				v := scopedDecode(t, ledger.DecodeScratchChanged, record.Payload)
				if v.ContextID == f.origin.ContextID && v.ParentScratchID != (uuid.UUID{}) {
					out.observed = append(out.observed, v)
				}
			case ledger.TypeScratchMergeBack:
				v := scopedDecode(t, ledger.DecodeScratchMergeBack, record.Payload)
				if v.AuthoringContextID == f.origin.ContextID {
					if v.Outcome == ledger.MergeBackPrepared {
						out.prepared = append(out.prepared, v)
						out.preparationSequences = append(out.preparationSequences, record.Seq)
					} else {
						out.merged = append(out.merged, v)
						out.mergeSequences = append(out.mergeSequences, record.Seq)
					}
				}
			case ledger.TypeScratchClosed:
				v := scopedDecode(t, ledger.DecodeScratchClosed, record.Payload)
				if v.ContextID == f.origin.ContextID && v.Reason == "temporary_merged_back" {
					out.closed = append(out.closed, v)
				}
			case ledger.TypeRuntimeWorkAdmitted:
				v := scopedDecode(t, ledger.DecodeRuntimeWorkAdmitted, record.Payload)
				if v.ParentTurnID == turn && v.Kind == "local" {
					out.work = append(out.work, v)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func grantForeignWorkspace(t *testing.T, f *continuationCaptureFixture, target Binding, operations ...string) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	capacity := paidPhysicalCapacity()
	capacity.MaxOperationBytes = 4 << 20
	_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: id, Grantee: bindingOf(f.origin), Operations: operations, Resources: []ledger.Resource{{Kind: "workspace", Binding: ledger.Binding(target), WorkspaceID: f.registered.ID}}, DataClass: "workspace", Recipients: []Binding{bindingOf(f.origin)}, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func foreignWriteArguments(f *continuationCaptureFixture, contextID uuid.UUID, path, text string) string {
	return fmt.Sprintf(`{"schema":1,"workspace_id":%q,"task_context_id":%q,"path":%q,"expected_sha256":"absent","text":%q}`, f.registered.ID, contextID, path, text)
}

func foreignProgram(t *testing.T, f *continuationCaptureFixture, calls []struct{ ID, Name, Arguments string }) {
	t.Helper()
	var posts atomic.Int32
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Foreign task complete."))
		}
	}
}

func TestTurnLoopForeignTaskWrite(t *testing.T) {
	t.Parallel()
	f := newContinuationCapture(t, true, nil)
	var turn uuid.UUID
	t.Run("write_creates_temporary_and_merges_back", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		free := func() { once.Do(func() { close(release) }) }
		defer free()
		f.probe.before = func() { close(entered); <-release }
		f.enable(t)
		_, started := f.send(t)
		turn = started.TurnID
		<-entered
		before := foreignRecords(t, f, turn)
		if len(before.created) != 1 || before.created[0].ParentScratchID != f.parent.ID || before.created[0].ContextID != f.origin.ContextID || len(before.observed) != 0 || len(before.merged) != 0 {
			t.Fatalf("foreign write did not fork an unobserved temporary: %+v", before)
		}
		for _, scratch := range []workspace.Scratch{f.scratch, f.parent} {
			if _, err := os.Stat(filepath.Join(scratch.Dir, "safe", "owned-capture.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("foreign effect touched a dedicated scratch before finish: %s %v", scratch.ID, err)
			}
		}
		free()
		result, _ := f.finished(t, turn)
		if result.Outcome != "finished" {
			t.Fatalf("temporary did not finish: %+v", result)
		}
		after := foreignRecords(t, f, turn)
		if len(after.created) != 1 || len(after.observed) != 1 || len(after.merged) != 1 || len(after.closed) != 1 || after.merged[0].Outcome != ledger.MergeBackFastForward || after.merged[0].ForkCommit != after.merged[0].ParentCommit || after.closed[0].ScratchID != after.created[0].ScratchID {
			t.Fatalf("temporary did not apply exactly once by fast-forward: %+v", after)
		}
		assertContinuationMerge(t, f, turn, false)
		parent, err := f.workspace.Scratch(context.Background(), f.parent.ContextID, f.registered.ID)
		if err != nil {
			t.Fatal(err)
		}
		observation := after.observed[0]
		if parent.TipCommit != after.merged[0].TargetCommit || parent.TipTree != observation.ScratchTree || len(observation.ChangedPaths) != 1 || observation.ChangedPaths[0] != "safe/owned-capture.txt" {
			t.Fatalf("parent tip did not acknowledge the captured foreign file: parent=%+v observation=%+v", parent, observation)
		}
		if got := readTestFile(t, filepath.Join(f.parent.Dir, "safe", "owned-capture.txt")); string(got) != "temporary capture" {
			t.Fatalf("parent did not receive the temporary: %q", got)
		}
		if _, err := os.Stat(filepath.Join(f.scratch.Dir, "safe", "owned-capture.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("author's dedicated scratch received the foreign effect")
		}
	})
	t.Run("reopen_replays", func(t *testing.T) {
		if turn == (uuid.UUID{}) {
			t.Fatal("foreign write did not start")
		}
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		parent, err := f.workspace.Scratch(context.Background(), f.parent.ContextID, f.registered.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.workspace.Close(); err != nil {
			t.Fatal(err)
		}
		through := f.store.Durable()
		segment := readTestFile(t, filepath.Join(f.store.LogDir(), ledger.SegmentName(1)))
		reopened, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		ws, _, err := workspace.Open(context.Background(), f.store, f.root, workspace.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		again, err := ws.Scratch(context.Background(), f.parent.ContextID, f.registered.ID)
		if err != nil || len(report.Issues) != 0 || len(report.PendingLoops) != 0 || again.TipCommit != parent.TipCommit || again.TipTree != parent.TipTree || f.store.Durable() != through || !bytes.Equal(segment, readTestFile(t, filepath.Join(f.store.LogDir(), ledger.SegmentName(1)))) {
			t.Fatalf("replay changed sibling-write authority or its acknowledged tip: report=%+v scratch=%+v err=%v", report, again, err)
		}
	})
	t.Run("foreign_read_denied", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, true, nil)
		target := Binding{AgentID: f.origin.AgentID, ContextID: f.parent.ContextID}
		grantForeignWorkspace(t, f, target, "file.write", "file.edit", "file.read", "file.search")
		base := fmt.Sprintf(`"workspace_id":%q,"task_context_id":%q`, f.registered.ID, target.ContextID)
		read := func(path string) string { return fmt.Sprintf(`{"schema":1,%s,"path":%q,"max_bytes":1024}`, base, path) }
		search := fmt.Sprintf(`{"schema":1,%s,"path":"safe","mode":"text","query":"capture","max_bytes":4096}`, base)
		calls := []struct{ ID, Name, Arguments string }{
			{"read-before", "file_read_v1", read("safe/seed.txt")},
			{"search-before", "file_search_v1", search},
			{"write", "file_write_v1", foreignWriteArguments(f, target.ContextID, "safe/owned-capture.txt", "temporary capture")},
			{"edit", "file_edit_v1", fmt.Sprintf(`{"schema":1,%s,"path":"safe/owned-capture.txt","expected_sha256":%q,"old_text":"temporary","new_text":"edited"}`, base, ledger.Digest(sha256.Sum256([]byte("temporary capture"))))},
			{"read-after", "file_read_v1", read("safe/owned-capture.txt")},
			{"search-after", "file_search_v1", search},
		}
		foreignProgram(t, f, calls)
		f.enable(t)
		_, started := f.send(t)
		result, events := f.finished(t, started.TurnID)
		results := f.toolResults(t, events)
		if result.Outcome != "finished" || len(results) != len(calls) {
			t.Fatalf("foreign access sequence did not finish: %+v %+v", result, results)
		}
		for i, result := range results {
			if i == 2 || i == 3 {
				if result.Status != "ok" {
					t.Fatalf("authorized sibling mutation refused: %+v", result)
				}
				continue
			}
			if result.Error == nil || result.Error.Code != "denied" || result.Data != nil {
				t.Fatalf("sibling read disclosed task or temporary bytes: %+v", result)
			}
		}
		records := foreignRecords(t, f, started.TurnID)
		if len(records.work) != 2 || records.work[0].Operation != "file.write" || records.work[1].Operation != "file.edit" {
			t.Fatalf("denied sibling reads admitted native work: %+v", records.work)
		}
		if got := readTestFile(t, filepath.Join(f.parent.Dir, "safe", "owned-capture.txt")); string(got) != "edited capture" {
			t.Fatalf("sibling edit was not applied: %q", got)
		}
	})
	for _, mode := range []string{"other_agent_denied", "no_dedicated_scratch_denied"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newContinuationCapture(t, false, nil)
			var target Binding
			if mode == "other_agent_denied" {
				target = registerPrivate(t, f.service, 79000)
				f.finishBusyFiles(t, f.registered, target)
			} else {
				target = scopedWorkContext(t, f.service, bindingOf(f.origin), 79000, 79001)
			}
			grant := grantForeignWorkspace(t, f, target, "file.write")
			if mode == "other_agent_denied" {
				// The native entrance must deny too, even though fileAuthority
				// provides an additional later cross-agent guard.
				agent := scopedBound(t, f.service, bindingOf(f.origin))
				turn := uuid.NewV7()
				if _, err := agent.StartTurn(context.Background(), TurnAdmission{CommandID: uuid.NewV7(), TurnID: turn, GrantID: f.privateGrant}); err != nil {
					t.Fatal(err)
				}
				before := f.store.Durable()
				_, err := agent.AdmitLocal(context.Background(), LocalAdmission{CommandID: uuid.NewV7(), WorkID: uuid.NewV7(), ParentTurnID: turn, GrantID: grant, LocalOperation: "file.write", Target: ledger.Resource{Kind: "workspace", WorkspaceID: f.registered.ID, Binding: ledger.Binding(target), PathPrefix: "safe/escape.txt"}, ArgumentsSHA256: sha256.Sum256([]byte("foreign write")), ToolSchema: 1, MaxBytes: 64})
				if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
					t.Fatalf("cross-agent workspace authority admitted native work: %v", err)
				}
				if _, err := f.service.EndTurn(context.Background(), TurnEnding{CommandID: uuid.NewV7(), TurnID: turn, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
					t.Fatal(err)
				}
			}
			foreignProgram(t, f, turnCalls("file_write_v1", foreignWriteArguments(f, target.ContextID, "safe/escape.txt", "must remain absent")))
			f.enable(t)
			_, started := f.send(t)
			_, events := f.finished(t, started.TurnID)
			results := f.toolResults(t, events)
			records := foreignRecords(t, f, started.TurnID)
			if len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "denied" || results[0].Data != nil || len(records.work) != 0 || len(records.created) != 0 {
				t.Fatalf("unready or foreign target acquired work instead of a fixed denial: tools=%+v records=%+v", results, records)
			}
		})
	}
}

func assertContinuationMerge(t *testing.T, f *continuationCaptureFixture, turn uuid.UUID, moved bool) {
	t.Helper()
	records := foreignRecords(t, f, turn)
	owners, observations, endings := continuationCaptureRecords(t, f, turn)
	var park, resume uint64
	lastWait := ""
	for _, owner := range owners {
		if owner.Submission.Arguments.State == "waiting_resource" {
			lastWait = owner.Submission.Arguments.WaitReason
			if lastWait == "workspace_merge_back" {
				if park != 0 {
					t.Fatal("merge-back parked twice")
				}
				park = uint64(owner.EffectSequence)
			}
		}
		if owner.Submission.ExpectedState == "waiting_resource" && owner.Submission.Arguments.State == "finishing" && lastWait == "workspace_merge_back" {
			if resume != 0 {
				t.Fatal("merge-back resumed twice")
			}
			resume = uint64(owner.EffectSequence)
		}
	}
	outcome := ledger.MergeBackFastForward
	if moved {
		outcome = ledger.MergeBackMerged
	}
	if len(records.prepared) != 1 || len(records.merged) != 1 || records.merged[0].Outcome != outcome || records.prepared[0].MergeBackID != records.merged[0].MergeBackID || len(observations) != 1 || len(endings) != 1 || park == 0 || park >= records.preparationSequences[0] || records.preparationSequences[0] >= records.mergeSequences[0] || resume <= records.mergeSequences[0] || resume >= endings[0] {
		t.Fatalf("merge-back violated park/application/resume/end order: records=%+v observe=%v park=%d resume=%d end=%v", records, observations, park, resume, endings)
	}
	if (records.merged[0].ForkCommit != records.merged[0].ParentCommit) != moved {
		t.Fatalf("wrong parent base for merge: %+v", records.merged[0])
	}
}

func testContinuationMergeWaits(t *testing.T) {
	t.Run("merge_back_order_idle_parent", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, true, nil)
		f.enable(t)
		_, started := f.send(t)
		result, _ := f.finished(t, started.TurnID)
		if result.Outcome != "finished" {
			t.Fatalf("idle-parent merge failed: %+v", result)
		}
		assertContinuationMerge(t, f, started.TurnID, false)
	})
	t.Run("merge_back_resume_tail_refused", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, true, nil)
		barrier := &tailBarrier{effect: ledger.TypeRuntimeContinuationChanged, fires: 3}
		barrier.prepare(t, f.serviceFixture, f.workspace, f.registered.ID)
		f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			if len(d) == 3 && d[2].Type == ledger.TypeRuntimeContinuationReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, d[2].Payload)
				if v.Submission.ExpectedState == "waiting_resource" && v.Submission.Arguments.State == "finishing" && f.service.p.owners[v.Submission.TurnID].Snapshot.WaitReason == "workspace_merge_back" {
					return barrier.submit(d)
				}
			}
			return d
		}
		f.enable(t)
		barrier.arm(f.store.Durable())
		_, started := f.send(t)
		result, _ := f.finished(t, started.TurnID)
		if result.Outcome != "finished" {
			t.Fatalf("tail refusal repeated or stranded merge-back: %+v", result)
		}
		barrier.mu.Lock()
		moved := len(barrier.foreign)
		barrier.mu.Unlock()
		if moved != 3 {
			t.Fatalf("merge resume did not meet three real tail moves: %d", moved)
		}
		assertContinuationMerge(t, f, started.TurnID, false)
	})
	t.Run("merge_back_park_before_wait", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, true, nil)
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
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		free := func() { once.Do(func() { close(release) }) }
		defer free()
		var parentCalls, authorCalls atomic.Int32
		f.respond = func(w http.ResponseWriter, r *http.Request) {
			model, raw := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			if bytes.Contains(raw, []byte("parent-held")) {
				if parentCalls.Add(1) == 1 {
					_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", finishBusyWrite(f.registered.ID, "parent-change")), ""))
					return
				}
				close(entered)
				<-release
			} else if authorCalls.Add(1) == 1 {
				_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", foreignWriteArguments(f, parent.Binding.ContextID, "safe/owned-capture.txt", "temporary capture")), ""))
				return
			}
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Merged sibling work."))
		}
		f.enable(t, parent)
		parentView, err := f.service.OpenSession(context.Background(), parent.Binding)
		if err != nil {
			t.Fatal(err)
		}
		defer parentView.Close()
		parentStart, err := parentView.Do(context.Background(), turnMessage(parent.Binding.ContextID, "parent-held"))
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		_, started := f.send(t)
		var cursor uint64
		for {
			page, err := f.view.Since(context.Background(), cursor, 128)
			if err != nil {
				t.Fatal(err)
			}
			status := finishBusyStatus(t, f.view, f.origin.ContextID, started.TurnID)
			if status.Result != nil {
				t.Fatalf("merge escaped the live parent: %+v", status)
			}
			if status.SavedPhase == "waiting_resource" && status.WaitReason == "workspace_merge_back" {
				break
			}
			cursor = page.ThroughSequence
			<-page.Wake
		}
		if records := foreignRecords(t, f, started.TurnID); len(records.prepared) != 0 || len(records.merged) != 0 {
			t.Fatal("merge-back prepared or applied before its parent's writer closed")
		}
		free()
		parentResult, _ := f.finishedFor(t, parentView, parentStart.TurnID)
		result, _ := f.finished(t, started.TurnID)
		if parentResult.Outcome != "finished" || result.Outcome != "finished" {
			t.Fatalf("concurrent parent/temporary did not finish: %+v %+v", parentResult, result)
		}
		assertContinuationMerge(t, f, started.TurnID, true)
	})
}

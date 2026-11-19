package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type passiveFixture struct {
	*continuationCaptureFixture
	parentSetting LoopContextSettings
	inspection    uuid.UUID
	requests      chan string
	posts         atomic.Int32
}

func newPassiveFixture(t *testing.T) *passiveFixture {
	t.Helper()
	f := newContinuationCapture(t, true, nil)
	p := &passiveFixture{continuationCaptureFixture: f, parentSetting: integratedParentSetting(t, f), requests: make(chan string, 16)}
	// Keep notice inspection distinct from turn.start/private.read so its
	// revocation tests the notice barrier, not an earlier generic barrier.
	var old ledger.RuntimeGrantIssued
	for _, grant := range f.service.p.grants {
		if grant.definition.Grantee == ledger.Binding(p.parentSetting.Binding) && slices.Contains(grant.definition.Operations, "context.inspect") {
			old = grant.definition
		}
	}
	f.revoke(t, old.GrantID)
	for _, operations := range [][]string{{"private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, {"context.inspect"}} {
		id := uuid.NewV7()
		if len(operations) == 1 {
			p.inspection = id
		}
		_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: id, Grantee: p.parentSetting.Binding, Operations: operations, Resources: old.Resources, DataClass: "private", Recipients: []Binding{p.parentSetting.Binding}, Limits: old.Limits, Lifetime: "until_revoked"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	var authorCalls atomic.Int32
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		p.posts.Add(1)
		var request struct {
			Messages []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
		}
		var text strings.Builder
		for _, message := range request.Messages {
			for _, block := range message.Content {
				text.WriteString(block.Text)
			}
		}
		p.requests <- text.String()
		w.Header().Set("Content-Type", "text/event-stream")
		if !bytes.Contains(raw, []byte("passive-natural")) && authorCalls.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", foreignWriteArguments(f, f.parent.ContextID, "safe/owned-capture.txt", "temporary capture")), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Natural task answer."))
		}
	}
	return p
}

func (f *passiveFixture) merge(t *testing.T) workspace.ScratchNotice {
	t.Helper()
	_, started := f.send(t)
	result, _ := f.finished(t, started.TurnID)
	if result.Outcome != "finished" || result.ModelCalls != 2 || result.ToolDispatches != 1 {
		t.Fatalf("real notice-producing turn failed: %+v", result)
	}
	<-f.requests
	<-f.requests
	notices, err := f.workspace.ScratchNotices(context.Background(), f.parent.ContextID, f.parent.ID, 0)
	if err != nil || len(notices) != 1 || notices[0].Outcome != ledger.MergeBackFastForward {
		t.Fatalf("real merge did not produce exactly one notice: %+v %v", notices, err)
	}
	return notices[0]
}

func passiveInput(t testing.TB, f *turnFixture, origin ledger.Origin, turn uuid.UUID) ([]byte, loopInputObject) {
	t.Helper()
	var ref ledger.ObjectRef
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeTurnBound && record.TurnID == turn {
				bound := scopedDecode(t, ledger.DecodeRuntimeTurnBound, record.Payload)
				ref = bound.InputObject
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ref.Bytes == 0 {
		t.Fatal("turn has no committed input")
	}
	raw, err := f.service.readLoopObject(context.Background(), origin, ref)
	if err != nil {
		t.Fatal(err)
	}
	input, err := f.service.readLoopInput(context.Background(), origin, ref)
	if err != nil {
		t.Fatal("producer input failed verification", err)
	}
	return raw, input
}

func (f *passiveFixture) natural(t *testing.T, binding Binding) (uuid.UUID, ledger.RuntimeTurnResult, loopInputObject) {
	t.Helper()
	view, err := f.service.OpenSession(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	start, err := view.Do(context.Background(), turnMessage(binding.ContextID, "passive-natural request"))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := f.finishedFor(t, view, start.TurnID)
	_, input := passiveInput(t, f.turnFixture, ledger.Origin{AgentID: binding.AgentID, SessionID: f.origin.SessionID, ContextID: binding.ContextID}, start.TurnID)
	return start.TurnID, result, input
}

func assertPassiveSelection(t *testing.T, input loopInputObject, notice workspace.ScratchNotice, workspaceID uuid.UUID, request string) {
	t.Helper()
	want := loopNoticeResult(notice)
	cursor := ledger.ContinuationNoticeCursor{WorkspaceID: workspaceID, ScratchID: notice.ParentScratchID, AfterSequence: int64(notice.Sequence)}
	if input.Schema != 2 || !sameScopedValue(input.TaskResults, []loopTaskResult{want}) || !slices.Equal(input.NoticeCursors, []ledger.ContinuationNoticeCursor{cursor}) {
		t.Fatalf("natural input did not retain the exact immutable selection: %+v", input)
	}
	block := "\nTask scratch results:\n- " + string(modelJSON(want)) + "\n"
	if !strings.Contains(request, block+"\nOwner input:\n") || strings.Count(request, "Task scratch results:") != 1 {
		t.Fatal("natural request omitted, repeated or misplaced its selected results")
	}
}

func (f *passiveFixture) reopen(t *testing.T, settings ...LoopContextSettings) {
	t.Helper()
	reopenIntegratedFixture(t, f.turnFixture, append([]LoopContextSettings{f.parentSetting}, settings...)...)
}

func assertPassiveCursor(t *testing.T, f *passiveFixture, turn uuid.UUID, cursors []ledger.ContinuationNoticeCursor) {
	t.Helper()
	var intent uint64
	var first *ledger.RuntimeContinuationReceipt
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.TurnID != turn {
				continue
			}
			if record.Type == ledger.TypeRuntimePaidDispatchIntent && intent == 0 {
				intent = record.Seq
			}
			if record.Type == ledger.TypeRuntimeContinuationReceipt {
				v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
				if intent == 0 && len(v.Submission.Arguments.NoticeCursors) != 0 {
					t.Fatal("pre-intent owner consumed a notice")
				}
				if intent != 0 && first == nil && v.Submission.Operation == "owner.advance" {
					first = &v
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if intent == 0 || first == nil || !slices.Equal(first.Submission.Arguments.NoticeCursors, cursors) {
		t.Fatalf("first post-intent advance lost its selected cursors: intent=%d first=%+v", intent, first)
	}
}

func TestTurnLoopPassiveResults(t *testing.T) {
	t.Run("crash_before_cursor", testPassiveCursorCrash)
	t.Run("conflict_reference", func(t *testing.T) {
		t.Parallel()
		testIntegratedMergeOutcome(t, "conflict", true)
	})
	t.Run("notice_after_merge_and_reopen", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		taskBefore, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: f.parentSetting.TaskBinding.TaskID})
		if err != nil {
			t.Fatal(err)
		}
		notice := f.merge(t)
		if f.posts.Load() != 2 {
			t.Fatal("merge triggered a completion-only POST")
		}
		status := func() {
			t.Helper()
			notices, err := f.workspace.ScratchNotices(context.Background(), f.parent.ContextID, f.parent.ID, 0)
			if err != nil || !sameScopedValue(notices, []workspace.ScratchNotice{notice}) {
				t.Fatal("task scratch result status changed or was consumed", err)
			}
			task, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: f.parentSetting.TaskBinding.TaskID})
			if err != nil || !sameScopedValue(task.Task, taskBefore.Task) || task.Sequence != taskBefore.Sequence || task.Readiness != taskBefore.Readiness || task.Reason != taskBefore.Reason {
				t.Fatal("passive completion changed native task status", err)
			}
		}
		status()
		view, err := f.service.OpenSession(context.Background(), f.parentSetting.Binding)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.parent.ContextID, Operation: "context.status", Arguments: session.ContextStatus{}})
		view.Close()
		if err != nil || len(snapshot.Data.(session.ContextView).Turns) != 0 || f.posts.Load() != 2 {
			t.Fatal("passive result scheduled a task turn", err)
		}
		f.reopen(t)
		status()
		if f.posts.Load() != 2 {
			t.Fatal("reopen dispatched a task turn")
		}
		_, result, input := f.natural(t, f.parentSetting.Binding)
		if result.Outcome != "finished" || f.posts.Load() != 3 {
			t.Fatalf("natural turn did not finish once: %+v", result)
		}
		assertPassiveSelection(t, input, notice, f.registered.ID, <-f.requests)
		status()
		f.reopen(t)
		status()
		if f.posts.Load() != 3 {
			t.Fatal("result status or second reopen dispatched work")
		}
	})
	t.Run("cursor_after_first_intent", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		notice := f.merge(t)
		turn, result, input := f.natural(t, f.parentSetting.Binding)
		if result.Outcome != "finished" {
			t.Fatal(result)
		}
		assertPassiveSelection(t, input, notice, f.registered.ID, <-f.requests)
		assertPassiveCursor(t, f, turn, input.NoticeCursors)
	})
	t.Run("cursor_after_eviction", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		f.merge(t)
		turn, _, first := f.natural(t, f.parentSetting.Binding)
		<-f.requests
		if len(first.TaskResults) != 1 {
			t.Fatal("first natural turn did not select the notice")
		}
		f.service.mu.Lock()
		_, retained := f.service.p.owners[turn]
		f.service.mu.Unlock()
		if retained {
			t.Fatal("completed owner's hot metadata was not evicted")
		}
		for restart := range 2 {
			if restart == 1 {
				f.reopen(t)
			}
			_, result, next := f.natural(t, f.parentSetting.Binding)
			if result.Outcome != "finished" || next.Schema != 2 || len(next.TaskResults) != 0 || len(next.NoticeCursors) != 0 || strings.Contains(<-f.requests, "Task scratch results:") {
				t.Fatal("eviction or reopen lost presentation progress")
			}
		}
	})
	t.Run("failed_start_consumes_nothing", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		notice := f.merge(t)
		var modelGrant uuid.UUID
		for id, grant := range f.service.p.grants {
			if grant.definition.Grantee == ledger.Binding(f.parentSetting.Binding) && slices.Contains(grant.definition.Operations, "model.call") {
				modelGrant = id
			}
		}
		f.service.deps.afterLoopCommit = func(receipt ledger.RuntimeLoopReceipt) {
			if receipt.Submission.Operation == "loop.bind" && receipt.Submission.Origin.ContextID == f.parent.ContextID {
				f.revoke(t, modelGrant)
			}
		}
		turn, result, input := f.natural(t, f.parentSetting.Binding)
		if len(input.TaskResults) != 1 || result.ModelCalls != 0 || result.Reason != "authority_lost" || f.posts.Load() != 2 {
			t.Fatalf("fixture did not fail between selection and first intent: %+v", result)
		}
		owner, err := f.service.currentContinuation(context.Background(), input.Origin, turn)
		if err != nil || len(owner.Snapshot.NoticeCursors) != 0 {
			t.Fatal("failed pre-intent turn consumed a notice", err)
		}
		f.service.deps.afterLoopCommit = nil
		f.contextSetting(t, f.parentSetting.Binding)
		_, result, input = f.natural(t, f.parentSetting.Binding)
		if result.Outcome != "finished" {
			t.Fatal(result)
		}
		assertPassiveSelection(t, input, notice, f.registered.ID, <-f.requests)
	})
	t.Run("schema1_history", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		command, start := f.send(t)
		result, _ := f.finished(t, start.TurnID)
		<-f.requests
		<-f.requests
		raw, input := passiveInput(t, f.turnFixture, f.origin, start.TurnID)
		legacy := modelJSON(struct {
			Schema    int                  `json:"schema"`
			Kind      string               `json:"kind"`
			Origin    ledger.Origin        `json:"origin"`
			TurnID    uuid.UUID            `json:"turn_id"`
			Text      string               `json:"text"`
			Artifacts []ledger.ArtifactRef `json:"artifacts"`
			OtherWork []string             `json:"other_work"`
		}{1, "owner_input", f.origin, start.TurnID, command.Arguments.(session.MessageSend).Text, []ledger.ArtifactRef{}, []string{}})
		if result.Outcome != "finished" || input.Schema != 1 || !bytes.Equal(raw, legacy) {
			t.Fatal("schema-1 producer bytes changed")
		}
		f.reopen(t)
		_, result, next := f.natural(t, bindingOf(f.origin))
		request := <-f.requests
		if result.Outcome != "finished" || next.Schema != 1 || !strings.Contains(request, "\nPrior owner input:\n"+input.Text) || !strings.Contains(request, "\nPrior assistant answer:\nNatural task answer.") || strings.Contains(request, "Task scratch results:") {
			t.Fatal("schema-1 history was upgraded or rendered differently")
		}
	})
	t.Run("sibling_gets_none", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		other := f.workSetting(t, 82300, "Other task")
		f.finishBusyFiles(t, f.registered, other.Binding)
		f.enable(t, f.parentSetting, other)
		notice := f.merge(t)
		for _, binding := range []Binding{bindingOf(f.origin), other.Binding} {
			_, result, input := f.natural(t, binding)
			request := <-f.requests
			if result.Outcome != "finished" || len(input.TaskResults) != 0 || len(input.NoticeCursors) != 0 || strings.Contains(request, "Task scratch results:") || strings.Contains(request, notice.MergeBackID.String()) {
				t.Fatal("sibling received another task's result")
			}
			// An author legitimately retains its own prior tool's path in its
			// transcript. Neither sibling receives new notice metadata there,
			// and the new input contains no result identity, digest or path.
			for _, key := range []string{"written_paths", "changed_paths", "conflict_sha256", "notice_cursors"} {
				if strings.Contains(request, key) {
					t.Fatal("sibling request received result metadata", key)
				}
			}
			for _, value := range append([]string{notice.MergeBackID.String(), notice.ParentScratchID.String()}, notice.WrittenPaths...) {
				if bytes.Contains(modelJSON(input), []byte(value)) {
					t.Fatal("sibling input disclosed result identity or path")
				}
			}
		}
	})
	for _, stage := range []string{"revoked_before_render", "revoked_before_send"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			f := newPassiveFixture(t)
			f.enable(t, f.parentSetting)
			f.merge(t)
			if stage == "revoked_before_render" {
				f.revoke(t, f.inspection)
			} else {
				f.service.deps.afterLoopCommit = func(receipt ledger.RuntimeLoopReceipt) {
					if receipt.Submission.Operation == "loop.bind" && receipt.Submission.Origin.ContextID == f.parent.ContextID {
						f.revoke(t, f.inspection)
					}
				}
			}
			_, result, input := f.natural(t, f.parentSetting.Binding)
			if stage == "revoked_before_render" {
				if len(input.TaskResults) != 0 || len(input.NoticeCursors) != 0 || strings.Contains(<-f.requests, "Task scratch results:") {
					t.Fatal("revoked render selected a notice")
				}
			} else if len(input.TaskResults) != 1 || result.Reason != "authority_lost" || result.ModelCalls != 0 || f.posts.Load() != 2 {
				t.Fatalf("notice authority recheck failed: %+v", result)
			}
		})
	}
	t.Run("no_selection_bytes", func(t *testing.T) {
		t.Parallel()
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		_, result, input := f.natural(t, f.parentSetting.Binding)
		request := <-f.requests
		_, baselineResult, baselineInput := f.natural(t, bindingOf(f.origin))
		baseline := <-f.requests
		if baselineResult.Outcome != "finished" || baselineInput.Schema != 1 || result.Outcome != "finished" || input.Schema != 2 || input.TaskResults == nil || input.NoticeCursors == nil || len(input.TaskResults) != 0 || len(input.NoticeCursors) != 0 || request != baseline {
			t.Fatalf("empty selection changed the request: %q %+v", request, input)
		}
	})
}

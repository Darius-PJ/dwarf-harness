package agents

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
)

func testTerminalIntegratedMergeCancel(t *testing.T) {
	t.Parallel()
	f := newContinuationCapture(t, true, nil)
	parent := integratedParentSetting(t, f)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	defer free()
	var parentPosts, sourcePosts atomic.Int32
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if bytes.Contains(raw, []byte("terminal-parent")) {
			if parentPosts.Add(1) == 1 {
				_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", finishBusyWrite(f.registered.ID, "terminal-parent")), ""))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		} else if sourcePosts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", foreignWriteArguments(f, f.parent.ContextID, "safe/terminal-source.txt", "synthetic terminal source")), ""))
			return
		}
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Synthetic terminal answer."))
	}
	f.enable(t, parent)
	parentView, err := f.service.OpenSession(context.Background(), parent.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer parentView.Close()
	parentStart, err := parentView.Do(context.Background(), turnMessage(parent.Binding.ContextID, "terminal-parent"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	h := runTerminal(t, []terminal.Agent{{AgentID: f.origin.AgentID, Session: f.terminalView(t, bindingOf(f.origin)), Contexts: []terminal.Context{{ContextID: f.origin.ContextID, Title: "General work"}}}}, false)
	position := h.wait(t, 0, "ready")
	h.send(t, "terminal temporary source")
	position = h.wait(t, position, "event turn.started", terminalTail(f.origin.ContextID))
	turn := turnFrom(t, h.line(position))
	label := terminalLabel(f.origin.AgentID, f.origin.ContextID, turn)
	position = h.wait(t, position, label, "event tool.finished")
	// Observe the durable owner, not Session status: status is forbidden here
	// until the terminal has printed its final (durable) line.
	var cursor uint64
	for {
		page, err := f.view.Since(context.Background(), cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		owner, err := f.service.currentContinuation(context.Background(), f.origin, turn)
		if err != nil {
			t.Fatal(err)
		}
		if owner.Snapshot.State == "waiting_resource" && owner.Snapshot.WaitReason == "workspace_merge_back" {
			break
		}
		if owner.Snapshot.State == "terminal" {
			t.Fatal("source ended without reaching the integrated merge wait")
		}
		cursor = page.ThroughSequence
		<-page.Wake
	}
	h.send(t, "/cancel "+turn.String())
	accepted := h.wait(t, position, label, "command turn.cancel accepted")
	h.mu.Lock()
	premature := false
	for _, line := range h.lines {
		if strings.HasPrefix(line, label+" ") && strings.Contains(line, "final (durable)") {
			premature = true
		}
	}
	h.mu.Unlock()
	if premature {
		t.Fatal("terminal announced a final result while the parent writer was held")
	}
	free()
	parentResult, _ := f.finishedFor(t, parentView, parentStart.TurnID)
	if parentResult.Outcome != "finished" {
		t.Fatal(parentResult)
	}
	final := h.wait(t, accepted, label, "final (durable) none: the turn ended cancelled")
	h.send(t, "/status "+turn.String())
	h.wait(t, final, label, "status state=cancelled")
	if err := h.in.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
	acceptances, finals := 0, 0
	for _, line := range h.all() {
		if !strings.HasPrefix(line, label+" ") {
			continue
		}
		if strings.Contains(line, "command turn.cancel accepted") {
			acceptances++
		}
		if strings.Contains(line, "final (durable)") {
			finals++
		}
	}
	records := foreignRecords(t, f, turn)
	if acceptances != 1 || finals != 1 || len(records.observed) != 1 || len(records.merged) != 1 || records.merged[0].Outcome != ledger.MergeBackMerged || len(records.closed) != 1 || sourcePosts.Load() != 2 || parentPosts.Load() != 2 {
		t.Fatal("terminal cancellation repeated or lost the integrated merge")
	}
	notices, err := f.workspace.ScratchNotices(context.Background(), f.parent.ContextID, f.parent.ID, 0)
	if err != nil || len(notices) != 1 || notices[0].MergeBackID != records.merged[0].MergeBackID || notices[0].AuthoringContextID != f.origin.ContextID {
		t.Fatal("accepted stop lost its merge notice", err)
	}
	owner, err := f.service.currentContinuation(context.Background(), f.origin, turn)
	if err != nil || owner.Snapshot.State != "terminal" || owner.Snapshot.CancelCommandID == (uuid.UUID{}) {
		t.Fatal("durable display preceded terminal cancellation", err)
	}
}

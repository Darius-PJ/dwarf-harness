package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestTurnLoopShutdownStopRefused(t *testing.T) {
	t.Parallel()
	f := newContinuationCapture(t, true, nil)
	free := f.holdCapture(t)
	var posts, refused atomic.Int32
	arguments := fmt.Sprintf(`{"schema":1,"workspace_id":%q,"task_context_id":%q,"path":"safe/owned-capture.txt","expected_sha256":"absent","text":"temporary capture"}`, f.registered.ID, f.parent.ContextID)
	f.respond = func(w http.ResponseWriter, request *http.Request) {
		model, _ := turnRequest(t, request)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Capture once."))
		}
	}
	f.service.deps.beforeSubmit = func(drafts []ledger.Draft) []ledger.Draft {
		if len(drafts) == 3 && drafts[2].Type == ledger.TypeRuntimeContinuationReceipt {
			receipt := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, drafts[2].Payload)
			if receipt.Submission.Operation == "owner.stop" && receipt.Submission.Arguments.StopReason == "shutdown" {
				refused.Add(1)
				return nil
			}
		}
		return drafts
	}
	f.enable(t)
	_, started := f.send(t)
	continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
	runtime := f.service.loop
	runtime.mu.Lock()
	turn := runtime.turns[started.TurnID]
	if turn == nil {
		runtime.mu.Unlock()
		t.Fatal("capture park lost its live turn")
	}
	// The durable wait is visible before its worker completion. Join that
	// completion through the existing scheduler change barrier as well.
	for turn.running || turn.state != "waiting_resource" || !runtime.queued[turn.id] || turn.waitGeneration < runtime.generation.Load() {
		changed := runtime.changed
		runtime.mu.Unlock()
		<-changed
		runtime.mu.Lock()
	}
	runtime.mu.Unlock()
	if posts.Load() != 2 || f.probe.count() != 0 {
		t.Fatalf("capture park has posts=%d observations=%d", posts.Load(), f.probe.count())
	}
	records := foreignRecords(t, f, started.TurnID)
	if len(records.created) != 1 || len(records.observed) != 0 || len(records.merged) != 0 {
		t.Fatal("refusal fixture did not retain its unobserved temporary")
	}
	path := filepath.Join(f.root, "scratch", records.created[0].Dir, "safe", "owned-capture.txt")
	if string(readTestFile(t, path)) != "temporary capture" {
		t.Fatal("refusal fixture lost its native file edit")
	}
	snapshot, text, err := f.view.LiveText(context.Background(), started.TurnID)
	if err != nil || snapshot.State != "provisional" {
		t.Fatal("parked turn has no live stream", err)
	}
	defer text.Close()
	before := f.store.Durable()
	attempts := f.probe.attempts()
	if err := f.service.Close(); !errors.Is(err, ErrInvalid) {
		t.Fatal("shutdown did not report its refused stop", err)
	}
	if refused.Load() == 0 {
		t.Fatal("shutdown bypassed the stop refusal")
	}
	if view, err := f.service.OpenSession(context.Background(), bindingOf(f.origin)); !errors.Is(err, ErrClosed) {
		if err == nil {
			view.Close()
		}
		t.Fatal("refused stop skipped the service close fence", err)
	}
	for name, done := range map[string]<-chan struct{}{"runtime": runtime.done, "turn": turn.done} {
		select {
		case <-done:
		default:
			t.Fatalf("shutdown did not join %s", name)
		}
	}
	if !errors.Is(turn.terminalError, ErrInvalid) || turn.state != "recovery_pending" || len(turn.reservation.scopes) != 0 || turn.reservation.process {
		t.Fatal("failed turn lost its error or retained a preparation", turn.terminalError)
	}
	select {
	case _, open := <-text.Deltas:
		if open {
			t.Fatal("refused stop emitted more live text")
		}
	default:
		t.Fatal("refused stop left its live stream open")
	}
	if err := f.workspace.Close(); err != nil {
		t.Fatal("failed turn retained its workspace writer", err)
	}
	if f.store.Durable() != before || posts.Load() != 2 || f.probe.attempts() != attempts || string(readTestFile(t, path)) != "temporary capture" {
		t.Fatal("refused shutdown appended work, called the provider, retried capture or lost the edit")
	}
	free()
	f.view.Close()
	reopened, report, err := Open(context.Background(), f.turnFixture.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	f.service = reopened
	if len(report.Issues) != 0 || len(report.PendingLoops) != 1 || report.PendingLoops[0].TurnID != started.TurnID || f.store.Durable() != before {
		t.Fatal("Open did not preserve exactly the pending failed turn", report)
	}
	owner, err := reopened.currentContinuation(context.Background(), f.origin, started.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: started.TurnID, ExpectedOwnerVersion: owner.Snapshot.Version}
	first, err := reopened.ReconcileLoop(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	after := f.store.Durable()
	repeated, err := reopened.ReconcileLoop(context.Background(), request)
	if err != nil || !reflect.DeepEqual(first, repeated) || f.store.Durable() != after {
		t.Fatal("reconciliation retry performed additional work", err)
	}
	var endings []ledger.RuntimeTurnResult
	if err := reopened.scan(context.Background(), before, after, func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			if strings.HasPrefix(record.Type, "workspace.") || record.Type == ledger.TypeRuntimePaidDispatchIntent {
				t.Fatal("recovery performed workspace work or sent another call")
			}
			if record.Type == ledger.TypeRuntimeTurnResult && record.TurnID == started.TurnID {
				endings = append(endings, scopedDecode(t, ledger.DecodeRuntimeTurnResult, record.Payload))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(endings) != 1 || endings[0].Outcome != "interrupted" || endings[0].ModelCalls != 2 || endings[0].ToolDispatches != 1 || posts.Load() != 2 || f.probe.attempts() != attempts || string(readTestFile(t, path)) != "temporary capture" {
		t.Fatalf("recovery did not produce exactly one inert interrupted ending: %+v", endings)
	}
}

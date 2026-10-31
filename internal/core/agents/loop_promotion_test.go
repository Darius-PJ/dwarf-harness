package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func TestTurnLoopPromotionUnavailable(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	var arguments string
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Candidate remains in the private scratch."))
		}
	}, nil)
	scratch, _ := f.files(t, "")
	arguments = fmt.Sprintf(`{"schema":1,"workspace_id":%q,"path":"candidate.txt","expected_sha256":"absent","text":"Synthetic promotion candidate."}`, scratch.WorkspaceID)
	f.enable(t)
	_, start := f.send(t)
	result, events := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || result.ToolDispatches != 1 || posts.Load() != 2 {
		t.Fatal("candidate turn did not finish")
	}
	tools := f.toolResults(t, events)
	if len(tools) != 1 || tools[0].Status != "ok" {
		t.Fatal("candidate was not written through the native executor")
	}
	before, err := f.workspace.Scratch(context.Background(), f.origin.ContextID, scratch.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	through := f.store.Durable()
	command := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "promotion.request", Arguments: session.PromotionRequest{WorkspaceID: scratch.WorkspaceID}}
	for range 2 {
		out, err := f.view.Do(context.Background(), command)
		var unavailable *workspace.UnavailableError
		if !errors.As(err, &unavailable) || unavailable.Reason != "no_validator" || out.Outcome != "refused" {
			t.Fatal("owner promotion did not preserve the native unavailable result")
		}
	}
	after, err := f.workspace.Scratch(context.Background(), f.origin.ContextID, scratch.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if f.store.Durable() != through || after.TipCommit != before.TipCommit || after.ObservationID != before.ObservationID || posts.Load() != 2 {
		t.Fatal("unavailable promotion queued work, changed a ref or dispatched a model")
	}
}

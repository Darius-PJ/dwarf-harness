package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func testLoopNormalizationIdentity(t *testing.T) {
	t.Parallel()
	var calls []struct{ ID, Name, Arguments string }
	var posts atomic.Int32
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, calls, ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Equivalent reads completed."))
		}
	}, nil)
	scratch, _ := f.files(t, "")
	if err := os.WriteFile(filepath.Join(scratch.Dir, "value.txt"), []byte("Equivalent local data."), 0600); err != nil {
		t.Fatal(err)
	}
	calls = []struct{ ID, Name, Arguments string }{
		{"identity-one", "file_read_v1", fmt.Sprintf(`{"schema":1,"workspace_id":%q,"path":"value.txt","max_bytes":64}`, scratch.WorkspaceID)},
		{"identity-two", "file_read_v1", fmt.Sprintf(` { "max_bytes":"64", "file_path":"value.txt", "task_context_id":%q, "workspace_id":%q, "schema":1 } `, f.origin.ContextID, scratch.WorkspaceID)},
	}
	f.enable(t)
	command, start := f.send(t)
	result, events := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || result.ModelCalls != 2 || result.ToolDispatches != 2 || posts.Load() != 2 {
		t.Fatal("equivalent tool calls did not complete one matched batch")
	}
	var starts []ledger.RuntimeToolStarted
	for _, event := range events {
		if value, ok := event.Data.(ledger.RuntimeToolStarted); ok {
			starts = append(starts, value)
		}
	}
	if len(starts) != 2 || starts[0].RequestSHA256 != starts[1].RequestSHA256 || starts[0].ToolID == starts[1].ToolID || starts[0].EffectCommandID == starts[1].EffectCommandID {
		t.Fatal("normalization changed equivalent meaning or conflated separate call identities")
	}
	for _, start := range starts {
		f.service.mu.Lock()
		match, err := f.service.findCommand(context.Background(), start.EffectCommandID, 0, f.service.p.through)
		f.service.mu.Unlock()
		if err != nil || match.scoped == nil || match.scoped.admission == nil || match.scoped.admission.ArgumentsSHA256 != start.RequestSHA256 {
			t.Fatal("native local admission did not bind normalized bytes")
		}
	}
	for _, value := range f.toolResults(t, events) {
		if value.Status != "ok" {
			t.Fatal("equivalent invocation did not return actual file bytes")
		}
	}
	through := f.store.Durable()
	replay, err := f.view.Do(context.Background(), command)
	if err != nil || replay.ReceiptSequence != start.ReceiptSequence || replay.EffectSequence != start.EffectSequence || f.store.Durable() != through {
		t.Fatal("same command and meaning did not return its historical loop receipt")
	}
	changed := command
	args := changed.Arguments.(session.MessageSend)
	args.Text += " Different meaning."
	changed.Arguments = args
	if _, err := f.view.Do(context.Background(), changed); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != through {
		t.Fatal("one command ID changed meaning across loop submissions")
	}
	if _, err := f.service.EndTurn(context.Background(), TurnEnding{CommandID: command.CommandID, TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "released"}); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != through {
		t.Fatal("native receipt lookup ignored the sixth command family")
	}
}

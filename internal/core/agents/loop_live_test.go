package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func loopNoPersistedValue(t *testing.T, root, value string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "lock" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(value)) {
			t.Error("protected synthetic bytes persisted")
		}
		return nil
	})
	if err != nil {
		t.Fatal("persisted-byte scan failed")
	}
}
func TestTurnLoopLiveSecretCapture(t *testing.T) {
	t.Parallel()
	t.Run("split_decoded_callbacks", func(t *testing.T) {
		t.Parallel()
		syntheticValue := os.Getenv(paidTestEnvironment)
		attached, resume := make(chan struct{}), make(chan struct{})
		var posts atomic.Int64
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			posts.Add(1)
			select {
			case <-attached:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"message-one\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", model)
			for _, part := range []string{"provisional introduction ", syntheticValue[:len(syntheticValue)/2]} {
				fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", part)
			}
			w.(http.Flusher).Flush()
			select {
			case <-resume:
			case <-r.Context().Done():
				return
			}
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_details\":null},\"usage\":{\"input_tokens\":100,\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}\n\n", syntheticValue[len(syntheticValue)/2:])
		}, nil)
		f.enable(t)
		_, start := f.send(t)
		snapshot, subscription, err := f.view.LiveText(context.Background(), start.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Close()
		close(attached)
		first, ok := <-subscription.Deltas
		if !ok {
			t.Fatal("provisional stream closed before first text")
		}
		seen := snapshot.Text + first.Text
		close(resume)
		for delta := range subscription.Deltas {
			seen += delta.Text
		}
		if strings.Contains(seen, syntheticValue) {
			t.Fatal("split literal passed the incremental presentation gate")
		}
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "failed" || result.FinalMessageObject != nil || posts.Load() != 1 {
			t.Fatal("credential echo became a natural answer or replacement call")
		}
		charged := false
		for _, event := range events {
			if paid, ok := event.Data.(ledger.RuntimePaidResult); ok {
				charged = paid.Kind == "usage" && len(paid.MessageObjects) == 0
			}
		}
		if !charged {
			t.Fatal("presentation refusal discarded complete billing evidence")
		}
		loopNoPersistedValue(t, f.root, syntheticValue)
	})
	t.Run("escaped_declared_tool_argument", func(t *testing.T) {
		t.Parallel()
		syntheticValue := os.Getenv(paidTestEnvironment)
		escaped := fmt.Sprintf(`\u%04x`, syntheticValue[0]) + syntheticValue[1:]
		var posts atomic.Int64
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			arguments := `{"schema":1,"action":"write","scope":"context","slot":"memory","revision":1,"text":"` + escaped + `"}`
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
		}, nil)
		f.enable(t)
		_, start := f.send(t)
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "failed" || result.ToolDispatches != 0 || posts.Load() != 1 {
			t.Fatal("escaped credential argument was executed or continued")
		}
		for _, event := range events {
			if event.Type == "tool.started" {
				t.Fatal("credential-bearing request reached tool admission")
			}
		}
		loopNoPersistedValue(t, f.root, syntheticValue)
	})
	t.Run("revoked_pin_drains_metadata_only", func(t *testing.T) {
		t.Parallel()
		var f *turnFixture
		var posts atomic.Int64
		var revoked atomic.Bool
		f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_status_v1", `{"schema":1,"subject":"context"}`), ""))
		}, func(deps *serviceDeps) {
			deps.afterLoopCommit = func(receipt ledger.RuntimeLoopReceipt) {
				if receipt.Submission.Operation != "tool.begin" || !revoked.CompareAndSwap(false, true) {
					return
				}
				_, err := f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: f.credential.Provider, CredentialVersion: f.credential.CredentialVersion})
				if err != nil {
					t.Error("synthetic credential revocation failed")
				}
			}
		})
		f.enable(t)
		_, start := f.send(t)
		result, events := f.finished(t, start.TurnID)
		if !revoked.Load() || result.Outcome != "failed" || result.Reason != "authority_lost" || result.FinalMessageObject != nil || posts.Load() != 1 {
			t.Fatal("revoked pin did not produce a durable failed turn")
		}
		results := f.toolResults(t, events)
		if len(results) != 1 || results[0].Data != nil || results[0].Error == nil || results[0].Error.Code != "unavailable" {
			t.Fatal("post-revocation tool publication carried content")
		}
		for _, event := range events {
			if value, ok := event.Data.(ledger.RuntimeToolResult); ok {
				raw, err := f.service.readLoopObject(context.Background(), f.origin, value.ResultObject)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 {
					t.Fatal("post-revocation result was not a closed fixed refusal")
				}
			}
		}
	})
	t.Run("revoked_pin_refuses_content_publication", func(t *testing.T) {
		t.Parallel()
		// The owner input object is content-bearing and the next publication
		// after the native admission; no runtime boundary check runs between
		// them, so only the pinned publication itself can refuse it.
		const ownerText = "Owner input withheld after synthetic revocation."
		var f *turnFixture
		var posts atomic.Int64
		var armed, revoked atomic.Bool
		f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			t.Error("revoked credential reached a provider exchange")
		}, func(deps *serviceDeps) {
			deps.afterScopedCommit = func(receipt ScopedReceipt) error {
				if !armed.Load() || receipt.Operation != "turn.start" || !revoked.CompareAndSwap(false, true) {
					return nil
				}
				_, err := f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: f.credential.Provider, CredentialVersion: f.credential.CredentialVersion})
				if err != nil {
					t.Error("synthetic credential revocation failed")
				}
				return nil
			}
		})
		f.enable(t)
		armed.Store(true)
		command := turnMessage(f.origin.ContextID, ownerText)
		turn := command.Arguments.(session.MessageSend).TurnID
		if _, err := f.view.Do(context.Background(), command); !revoked.Load() || !errors.Is(err, ErrUnavailable) {
			t.Fatal("content publication after revocation did not refuse the start")
		}
		f.service.mu.Lock()
		work, found, err := f.service.loadWork(context.Background(), turn, f.service.p.through)
		bound := false
		if err == nil {
			err = f.service.scan(context.Background(), 0, f.service.p.through, func(c ledger.Commit) error {
				for _, record := range c.Records {
					bound = bound || record.TurnID == turn && record.Type == ledger.TypeRuntimeTurnBound
				}
				return nil
			})
		}
		f.service.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !found || !work.ended || work.outcome != "released" || bound || posts.Load() != 0 {
			t.Fatal("refused start left a bound, admitted or dispatched turn")
		}
		loopNoPersistedValue(t, f.root, ownerText)
	})
}

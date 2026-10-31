package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// commentaryThenCall streams provisional text, then one tool call, so a turn's
// live text contains commentary its durable answer must not.
func commentaryThenCall(model, text, name, arguments string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"message-one\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n", model)
	fmt.Fprintf(&out, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n", text)
	fmt.Fprintf(&out, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call-one\",\"name\":%q,\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n", name, arguments)
	out.WriteString("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_details\":null},\"usage\":{\"input_tokens\":100,\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	return out.String()
}

// finalScript answers the first POST of each marked turn with commentary and a
// correctable call, and its continuation with the marker's final answer once
// the marker's gate opens; a non-nil entered first receives the marker, so a
// test knows the continuation POST is in flight. Later turns render earlier
// owner input, so markers match in the given order: a later turn's comes first.
func finalScript(t *testing.T, order []string, gates map[string]chan struct{}, entered chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for _, marker := range order {
			gate := gates[marker]
			if !bytes.Contains(raw, []byte(marker+" request")) {
				continue
			}
			if !bytes.Contains(raw, []byte("tool_result")) {
				_, _ = io.WriteString(w, commentaryThenCall(model, marker+" commentary. ", "file_read_v1", `{}`))
				return
			}
			// Headers are flushed; only the body waits, well inside the
			// dispatch timeout.
			if entered != nil {
				select {
				case entered <- marker:
				case <-r.Context().Done():
					return
				}
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, turnAnthropic(model, nil, marker+" final answer."))
			return
		}
		t.Error("unmarked request reached the provider")
	}
}

func liveUntilEnd(t *testing.T, view session.Session, turn uuid.UUID, release chan struct{}) string {
	t.Helper()
	snapshot, sub, err := view.LiveText(context.Background(), turn)
	if err != nil || sub == nil {
		t.Fatal("live attachment unavailable", err)
	}
	defer sub.Close()
	close(release)
	text := snapshot.Text
	for delta := range sub.Deltas {
		text += delta.Text
	}
	if !errors.Is(sub.Err(), session.ErrFinished) {
		t.Fatal("live text ended without the recorded ending", sub.Err())
	}
	return text
}

func TestTurnLoopFinalMessage(t *testing.T) {
	t.Parallel()
	t.Run("attribution", func(t *testing.T) {
		t.Parallel()
		gates := map[string]chan struct{}{"general": make(chan struct{}), "work": make(chan struct{})}
		f := newTurnFixture(t, "anthropic", finalScript(t, []string{"general", "work"}, gates, nil), nil)
		work := f.workSetting(t, 71000, "Final work")
		f.enable(t, work)
		ctx := context.Background()
		turns := map[string]uuid.UUID{}
		for marker, contextID := range map[string]uuid.UUID{"general": f.origin.ContextID, "work": work.Binding.ContextID} {
			command := turnMessage(contextID, marker+" request")
			if _, err := f.view.Do(ctx, command); err != nil {
				t.Fatal(err)
			}
			turns[marker] = command.Arguments.(session.MessageSend).TurnID
		}
		for marker, turn := range turns {
			live := liveUntilEnd(t, f.view, turn, gates[marker])
			if live != marker+" commentary. "+marker+" final answer." {
				t.Fatalf("provisional text was not the whole streamed turn: %q", live)
			}
			text, err := f.view.FinalText(ctx, turn)
			if err != nil {
				t.Fatal(err)
			}
			if text != marker+" final answer." {
				t.Fatalf("durable answer was not this turn's own last answer: %q", text)
			}
		}
	})
	t.Run("foreign_turn", func(t *testing.T) {
		t.Parallel()
		gates := map[string]chan struct{}{"own": make(chan struct{}), "peer": make(chan struct{})}
		close(gates["own"])
		close(gates["peer"])
		f := newTurnFixture(t, "anthropic", finalScript(t, []string{"own", "peer"}, gates, nil), nil)
		peerBinding := registerPrivate(t, f.service, 72000)
		f.enable(t, f.contextSetting(t, peerBinding))
		peer, err := f.service.OpenSession(context.Background(), peerBinding)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(peer.Close)
		ctx := context.Background()
		command := turnMessage(peerBinding.ContextID, "peer request")
		if _, err := peer.Do(ctx, command); err != nil {
			t.Fatal(err)
		}
		peerTurn := command.Arguments.(session.MessageSend).TurnID
		if result, _ := f.finishedFor(t, peer, peerTurn); result.Outcome != "finished" {
			t.Fatal("peer turn did not finish")
		}
		own := turnMessage(f.origin.ContextID, "own request")
		if _, err := f.view.Do(ctx, own); err != nil {
			t.Fatal(err)
		}
		f.finished(t, own.Arguments.(session.MessageSend).TurnID)
		if text, err := peer.FinalText(ctx, peerTurn); err != nil || text != "peer final answer." {
			t.Fatal("the running agent could not read its own durable answer", err)
		}
		if text, err := f.view.FinalText(ctx, peerTurn); !errors.Is(err, ErrDenied) || text != "" {
			t.Fatal("another agent's Session read a foreign turn's durable answer")
		}
		if _, err := f.view.FinalText(ctx, uuid.NewV7()); !errors.Is(err, ErrDenied) {
			t.Fatal("an unknown turn was refused differently from a foreign one")
		}
	})
	t.Run("unended_cancelled_and_revoked", func(t *testing.T) {
		t.Parallel()
		gates := map[string]chan struct{}{"held": make(chan struct{}), "kept": make(chan struct{})}
		close(gates["kept"])
		entered := make(chan string, 2)
		f := newTurnFixture(t, "anthropic", finalScript(t, []string{"kept", "held"}, gates, entered), nil)
		f.enable(t)
		ctx := context.Background()
		held := turnMessage(f.origin.ContextID, "held request")
		if _, err := f.view.Do(ctx, held); err != nil {
			t.Fatal(err)
		}
		heldTurn := held.Arguments.(session.MessageSend).TurnID
		if <-entered != "held" {
			t.Fatal("unexpected continuation")
		}
		if _, err := f.view.FinalText(ctx, heldTurn); !errors.Is(err, ErrDenied) {
			t.Fatal("an unended turn disclosed a final answer")
		}
		if _, err := f.view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: heldTurn}}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.view.FinalText(ctx, heldTurn); !errors.Is(err, ErrDenied) {
			t.Fatal("a cancelled turn produced a durable answer")
		}
		kept := turnMessage(f.origin.ContextID, "kept request")
		if _, err := f.view.Do(ctx, kept); err != nil {
			t.Fatal(err)
		}
		keptTurn := kept.Arguments.(session.MessageSend).TurnID
		if result, _ := f.finished(t, keptTurn); result.Outcome != "finished" {
			t.Fatal("kept turn did not finish")
		}
		if text, err := f.view.FinalText(ctx, keptTurn); err != nil || text != "kept final answer." {
			t.Fatal("finished answer unavailable before revocation", err)
		}
		f.revoke(t, f.privateGrant)
		if text, err := f.view.FinalText(ctx, keptTurn); !errors.Is(err, ErrDenied) || text != "" {
			t.Fatal("a revoked source still disclosed its durable answer")
		}
	})
}

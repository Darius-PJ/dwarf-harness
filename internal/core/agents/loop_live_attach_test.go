package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func loopStreamStart(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"live-message\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", model)
}
func loopStreamDelta(w http.ResponseWriter, text string) {
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text)
	w.(http.Flusher).Flush()
}
func loopStreamEnd(w http.ResponseWriter) {
	_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_details\":null},\"usage\":{\"input_tokens\":100,\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
}

func TestTurnLoopLiveAttachAndAttribution(t *testing.T) {
	t.Parallel()
	begin, ack, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		select {
		case <-begin:
		case <-r.Context().Done():
			return
		}
		loopStreamStart(w, model)
		for range 70 {
			loopStreamDelta(w, "x")
			select {
			case <-ack:
			case <-r.Context().Done():
				return
			}
		}
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		loopStreamEnd(w)
	}, nil)
	f.enable(t)
	_, start := f.send(t)
	// Channel barriers order every step; a stalled producer or subscription
	// surfaces as the package test deadline, not a per-test timer.
	ctx := context.Background()
	snapshot, fast, err := f.view.LiveText(ctx, start.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	if snapshot.Text != "" || snapshot.DeltaHighWater != 0 || snapshot.State != "provisional" {
		t.Fatal("initial live attachment was not empty and provisional")
	}
	_, slow, err := f.view.LiveText(ctx, start.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	close(begin)
	var attached session.TextSnapshot
	var middle *session.TextSubscription
	for i := uint64(1); i <= 70; i++ {
		delta, ok := <-fast.Deltas
		if !ok || delta.Index != i || delta.Text != "x" {
			t.Fatal("live sequence omitted, repeated or changed a delta")
		}
		if i == 35 {
			attached, middle, err = f.view.LiveText(ctx, start.TurnID)
			if err != nil {
				t.Fatal(err)
			}
			defer middle.Close()
			if attached.Text != strings.Repeat("x", 35) || attached.DeltaHighWater != 35 {
				t.Fatal("snapshot and subscription were not atomic at the text high-water")
			}
		}
		ack <- struct{}{}
	}
	if !errors.Is(slow.Err(), session.ErrReattachRequired) {
		t.Fatal("overflow did not close only the slow subscriber with reattach_required")
	}
	for i := uint64(36); i <= 70; i++ {
		if delta := <-middle.Deltas; delta.Index != i || delta.Text != "x" {
			t.Fatal("midstream attachment duplicated its snapshot or lost its suffix")
		}
	}
	fresh, replacement, err := f.view.LiveText(ctx, start.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if fresh.DeltaHighWater != 70 || fresh.Text != strings.Repeat("x", 70) {
		t.Fatal("reattachment did not recover the complete bounded provisional text")
	}
	f.view.Close()
	if !errors.Is(replacement.Err(), session.ErrClosed) {
		t.Fatal("closing a Session retained its live subscription")
	}
	f.view, err = f.service.OpenSession(ctx, bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.view.Close)
	_, ending, err := f.view.LiveText(ctx, start.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	defer ending.Close()
	close(finish)
	result, events := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || result.FinalMessageObject == nil || result.ModelCalls != 1 {
		t.Fatal("view closure stopped or replaced the paid turn")
	}
	for _, event := range events {
		if event.AgentID != f.origin.AgentID || event.ExecutionSessionID != f.origin.SessionID || event.ContextID != f.origin.ContextID || event.TurnID != start.TurnID {
			t.Fatal("durable event was relabeled by a view attachment")
		}
	}
	if _, ok := <-ending.Deltas; ok {
		t.Fatal("ending emitted duplicated provisional text")
	}
	late, subscription, err := f.view.LiveText(ctx, start.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if late.Text != "" || late.FinalMessageObject == nil || *late.FinalMessageObject != *result.FinalMessageObject {
		t.Fatal("late attach treated dropped provisional text as the durable answer")
	}
	if subscription != nil {
		defer subscription.Close()
		if !errors.Is(subscription.Err(), session.ErrFinished) {
			t.Fatal("late attachment remained provisional")
		}
	}
}

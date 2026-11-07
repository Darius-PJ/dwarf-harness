package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
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

func TestAgentTailCheckpointPages(t *testing.T) {
	t.Parallel()
	t.Run("pages", testAgentTailPages)
	t.Run("concurrent_refresh", testAgentTailConcurrentRefresh)
	t.Run("close_stopped_refresh", testAgentTailCloseRefresh)
	t.Run("acknowledgement_overlap", testAgentTailAcknowledgementOverlap)
}

func testAgentTailPages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var posts atomic.Int64
	var foreign func()
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			foreign()
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_read_v1", `{}`), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "checkpoint page answer"))
		}
	}, nil)
	scratch, _ := f.files(t, "")
	dormant := registerPrivate(t, f.service, 810000)
	later := registerPrivate(t, f.service, 810010)
	rotatedContext := registerPrivate(t, f.service, 810020)
	// Use the real workspace producer, not a service projection edit.
	workspaceID := scratch.WorkspaceID
	foreign = func() {
		if _, err := f.workspace.Scratch(ctx, dormant.ContextID, workspaceID); err != nil {
			t.Error(err)
		}
	}
	f.enable(t)
	before := f.store.Durable()
	_, start := f.send(t)
	result, expected := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || result.ToolDispatches != 1 || posts.Load() != 2 {
		t.Fatalf("recorded turn did not finish exactly once: %+v posts=%d", result, posts.Load())
	}
	largest := 0
	for _, event := range expected {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		largest = max(largest, len(raw))
	}
	t.Logf("largest serialized event: %d bytes; byte predicate unchanged", largest)
	types := make([]string, len(expected))
	for i, event := range expected {
		types[i] = event.Type
	}
	wantTypes := []string{"turn.started", "usage", "tool.started", "tool.finished", "tool.output", "usage", "turn.finished"}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Fatalf("turn events: %v", types)
	}
	second, err := f.service.OpenSession(ctx, bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	through := f.store.Durable()
	var partial, resultCursor, resultEnd uint64
	if err := f.service.scan(ctx, before, through, func(c ledger.Commit) error {
		if partial == 0 && c.First < c.Last {
			partial = c.First
		}
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeToolResult {
				resultCursor, resultEnd = c.First-1, c.Last
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.view.Since(ctx, partial, 128); !errors.Is(err, ledger.ErrCursor) {
		t.Fatalf("old partial cursor: %v", err)
	}
	if _, err := f.view.Since(ctx, partial, 129); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit precedence changed: %v", err)
	}
	overflow, err := f.view.Since(ctx, resultCursor, 1)
	if !errors.Is(err, ErrTooLarge) || len(overflow.Events) != 0 || overflow.ThroughSequence != resultCursor {
		t.Fatalf("split a two-event tool result: %+v %v", overflow, err)
	}
	exact, err := f.view.Since(ctx, resultCursor, 2)
	if err != nil || len(exact.Events) != 2 || exact.Events[0].Type != "tool.finished" || exact.Events[1].Type != "tool.output" || exact.ThroughSequence < resultEnd {
		t.Fatalf("exact event boundary: %+v %v", exact, err)
	}
	for _, mode := range []string{"warm", "discarded", "damaged", "disabled"} {
		if mode != "warm" {
			damageHistoryCache(f.service, mode)
		}
		cursor := before
		var got []session.Event
		for cursor < through {
			page, err := f.view.Since(ctx, cursor, 2)
			if err != nil || page.ThroughSequence <= cursor || page.ThroughSequence > through {
				t.Fatalf("%s page failed to advance at a complete commit: %+v %v", mode, page, err)
			}
			retry, err := second.Since(ctx, cursor, 2)
			if err != nil || !reflect.DeepEqual(retry.Events, page.Events) || retry.ThroughSequence != page.ThroughSequence {
				t.Fatalf("%s same-cursor second session changed its page: %+v %v", mode, retry, err)
			}
			got = append(got, page.Events...)
			cursor = page.ThroughSequence
		}
		if !reflect.DeepEqual(got, expected) || f.store.Durable() != through {
			t.Fatalf("%s pages duplicated, omitted or reordered events", mode)
		}
	}
	empty, err := f.view.Since(ctx, through, 128)
	if err != nil || len(empty.Events) != 0 || empty.ThroughSequence != through {
		t.Fatalf("equal cursor: %+v %v", empty, err)
	}
	if _, err := f.workspace.Scratch(ctx, later.ContextID, workspaceID); err != nil {
		t.Fatal(err)
	}
	zero, err := f.view.Since(ctx, through, 128)
	if err != nil || len(zero.Events) != 0 || zero.ThroughSequence != f.store.Durable() || zero.ThroughSequence <= through {
		t.Fatalf("foreign-only page: %+v %v", zero, err)
	}
	<-empty.Wake
	// An empty newest segment is precisely the writer's state after rotation.
	// Preserve every framed byte; Open seeds a token in the previous segment.
	f.view.Close()
	second.Close()
	deps := f.service.deps
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	rotatedAt := f.store.Durable()
	writeTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(rotatedAt+1)), nil)
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.service, _, err = openPaidService(ctx, f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	f.workspace, _, err = workspace.Open(ctx, f.store, f.root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.service.Close()
		_ = f.workspace.Close()
	})
	f.enable(t)
	if _, err := f.workspace.Scratch(ctx, rotatedContext.ContextID, workspaceID); err != nil {
		t.Fatal(err)
	}
	rotated, err := f.view.Since(ctx, rotatedAt, 128)
	if err != nil || len(rotated.Events) != 0 || rotated.ThroughSequence != f.store.Durable() || rotated.ThroughSequence <= rotatedAt {
		t.Fatalf("token across rotation changed its page: %+v %v", rotated, err)
	}
	all, err := f.view.Since(ctx, before, 128)
	if err != nil || !reflect.DeepEqual(all.Events, expected) || all.ThroughSequence != rotated.ThroughSequence {
		t.Fatalf("multi-segment pages differ from full replay: %+v %v", all, err)
	}
}

func testAgentTailConcurrentRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stopped, resume := make(chan struct{}), make(chan struct{})
	waiting := make(chan struct{}, 3)
	var armed atomic.Bool
	f := newServiceFixture(t, serviceDeps{
		beforeRefreshInstall: func(ledger.Commit) {
			if armed.CompareAndSwap(true, false) {
				close(stopped)
				<-resume
			}
		},
		beforeRefreshWait: func() { waiting <- struct{}{} },
	})
	binding := registerPrivate(t, f.service, 820000)
	ws, registered := privateWorkspace(t, f)
	dormant := registerPrivate(t, f.service, 820020)
	writer, _, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	before := f.store.Durable()
	if _, err := writer.WritePrivate(ctx, memoryWrite(binding, 820010, 1, "first")); err != nil {
		t.Fatal(err)
	}
	target := f.store.Durable()
	refresh := func(ctx context.Context, through uint64) <-chan error {
		done := make(chan error, 1)
		go func() {
			f.service.mu.Lock()
			err := f.service.refreshLocked(ctx, through)
			f.service.mu.Unlock()
			done <- err
		}()
		return done
	}
	armed.Store(true)
	first := refresh(ctx, target)
	<-stopped
	// Reading outside the service lock must leave the old committed frontier
	// available; none of the group has yet been installed.
	f.service.mu.Lock()
	if f.service.p.through != before {
		t.Error("stopped read installed a partial group")
	}
	f.service.mu.Unlock()
	joined := refresh(ctx, target)
	<-waiting
	cancelCtx, cancel := context.WithCancel(ctx)
	cancelled := refresh(cancelCtx, target)
	<-waiting
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting cancellation: %v", err)
	}
	foreign, err := ws.Scratch(ctx, dormant.ContextID, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreignEnd := f.store.Durable()
	if _, err := writer.WritePrivate(ctx, memoryWrite(binding, 820011, 2, "later")); err != nil {
		t.Fatal(err)
	}
	laterTarget := f.store.Durable()
	later := refresh(ctx, laterTarget)
	<-waiting
	var drafts []ledger.Draft
	if err := writer.scan(ctx, foreignEnd, laterTarget, func(c ledger.Commit) error {
		drafts = scopedGoldenDrafts(c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Public pre-Submit refreshes wait for the flight. Enter the locked layer
	// defensively: duplicate-retry acknowledgement can catch up foreign groups.
	// The duplicate is the writer's actual private group, byte for byte.
	f.service.mu.Lock()
	duplicate, err := f.store.Submit(ctx, drafts[0], drafts[1:])
	if err == nil {
		err = f.service.acknowledgeLocked(max(f.service.p.through, duplicate.Last))
	}
	f.service.mu.Unlock()
	if err != nil || !duplicate.Duplicate || duplicate.Last != laterTarget {
		close(resume)
		for _, done := range []<-chan error{first, joined, later} {
			<-done
		}
		t.Fatalf("duplicate readback did not install the real tail: %+v %v", duplicate, err)
	}
	close(resume)
	for _, done := range []<-chan error{first, joined, later} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if f.service.poisoned || f.service.p.through != laterTarget || f.store.Durable() != laterTarget || !(target < foreignEnd && foreignEnd < laterTarget) {
		t.Fatal("concurrent refresh changed the committed frontier")
	}
	view, err := f.service.Snapshot(ctx, ReadBinding{Binding: binding, Scope: "context"})
	if err != nil || view.Revision != 3 {
		t.Fatalf("larger waiter lost the later revision: %+v %v", view, err)
	}
	expected, err := writer.Snapshot(ctx, ReadBinding{Binding: binding, Scope: "context"})
	if err != nil || view.SessionID != expected.SessionID || view.ThroughSequence != expected.ThroughSequence || !reflect.DeepEqual(view.Heads, expected.Heads) || !reflect.DeepEqual(view.Issues, expected.Issues) {
		t.Fatalf("concurrent refresh differs from writer's committed heads: %+v %+v %v", view, expected, err)
	}
	if got := readPrivate(t, f.service, binding, 2, "memory"); string(got.Bytes) != "first" {
		t.Fatalf("concurrent refresh lost the preceding revision: %q", got.Bytes)
	}
	sameScratch, err := ws.Scratch(ctx, dormant.ContextID, registered.ID)
	if err != nil || !reflect.DeepEqual(sameScratch, foreign) || f.store.Durable() != laterTarget {
		t.Fatalf("foreign scratch was repeated or changed: %+v %v", sameScratch, err)
	}
	if got := readPrivate(t, f.service, binding, 3, "memory"); string(got.Bytes) != "later" {
		t.Fatalf("concurrent refresh bytes: %q", got.Bytes)
	}
}

func testAgentTailCloseRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stopped, resume, waiting := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	f := newServiceFixture(t, serviceDeps{
		beforeRefreshInstall: func(ledger.Commit) {
			if armed.CompareAndSwap(true, false) {
				close(stopped)
				<-resume
			}
		},
		beforeRefreshWait: func() { close(waiting) },
	})
	binding := registerPrivate(t, f.service, 830000)
	writer, _, err := Open(ctx, f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	before := f.store.Durable()
	if _, err := writer.WritePrivate(ctx, memoryWrite(binding, 830010, 1, "not installed after close")); err != nil {
		t.Fatal(err)
	}
	target := f.store.Durable()
	refresh := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			f.service.mu.Lock()
			err := f.service.refreshLocked(ctx, target)
			f.service.mu.Unlock()
			done <- err
		}()
		return done
	}
	armed.Store(true)
	performer := refresh()
	<-stopped
	waiter := refresh()
	<-waiting
	closed := make(chan error, 1)
	go func() { closed <- f.service.Close() }()
	<-f.service.closeSignal
	if err := <-waiter; !errors.Is(err, ErrClosed) {
		t.Fatalf("close did not release waiter: %v", err)
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned before its stopped read drained: %v", err)
	default:
	}
	close(resume)
	if err := <-performer; !errors.Is(err, ErrClosed) {
		t.Fatalf("performer crossed the close fence: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if f.service.p.through != before || f.store.Durable() != target {
		t.Fatal("close installed or removed the stopped group")
	}
}

package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testLoopLateNativeEffect(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	var arguments string
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "The stale write was refused."))
		}
	}, nil)
	revision := f.seedMemory(t, "Current native memory.")
	arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"Never committed."}`, revision-1)
	f.enable(t)
	_, start := f.send(t)
	result, events := f.finished(t, start.TurnID)
	if result.Outcome != "finished" || posts.Load() != 2 {
		t.Fatal("definite pre-commit refusal did not complete")
	}
	var effect uuid.UUID
	refused := false
	for _, event := range events {
		switch value := event.Data.(type) {
		case ledger.RuntimeToolStarted:
			effect = value.EffectCommandID
		case ledger.RuntimeToolResult:
			refused = value.Outcome == "conflict" && value.EffectReceiptSequence == 0
		}
	}
	if !validID(effect) || !refused {
		t.Fatal("fixture did not end a pre-minted native command without its receipt")
	}
	var captured []ledger.Draft
	f.service.mu.Lock()
	f.service.deps.beforeSubmit = func(drafts []ledger.Draft) []ledger.Draft {
		captured = slices.Clone(drafts)
		for i := range captured {
			captured[i].Payload = bytes.Clone(captured[i].Payload)
		}
		return nil
	}
	scope := ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}
	version := f.service.p.book.accounts[scope].version
	f.service.mu.Unlock()
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	before := f.store.Durable()
	_, err := f.service.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: effect, ExpectedVersion: uint64(version), Scope: scope, Limits: capacity})
	f.service.mu.Lock()
	f.service.deps.beforeSubmit = nil
	f.service.mu.Unlock()
	if err == nil || len(captured) == 0 || f.store.Durable() != before {
		t.Fatal("could not capture a valid, uncommitted native group")
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Submit(context.Background(), captured[0], captured[1:]); err != nil {
		t.Fatal(err)
	}
	through := f.store.Durable()
	log := readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))
	s, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if s != nil {
		_ = s.Close()
	}
	if !errors.Is(err, ErrInconsistent) || s != nil || f.store.Durable() != through || !bytes.Equal(log, readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))) {
		t.Fatal("Open accepted or repaired a native commit after the tool proved it never committed")
	}
}

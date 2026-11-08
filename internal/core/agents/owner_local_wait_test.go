package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

type continuationLocalFixture struct {
	*turnFixture
	reader  *Agent
	read    ScopedPrivateRead
	free    func()
	restore func()
	posts   *atomic.Int32
}

func newContinuationLocal(t *testing.T, wait string, answerOnly bool) *continuationLocalFixture {
	t.Helper()
	var f *turnFixture
	var holder Binding
	var lowered, holding atomic.Bool
	var posts atomic.Int32
	var arguments string
	var holderBytes int64
	entered, release := make(chan struct{}), make(chan struct{})
	configure := func(local, buffer int64) {
		t.Helper()
		scope := ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}
		f.service.mu.Lock()
		version := f.service.p.book.accounts[scope].version
		f.service.mu.Unlock()
		capacity := paidPhysicalCapacity()
		capacity.LocalSlots, capacity.BufferBytes, capacity.MaxOperationBytes = local, buffer, 4<<20
		if _, err := f.service.ConfigureCapacity(context.Background(), bindingOf(f.origin), CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(version), Scope: scope, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
	}
	restore := func() { configure(1, 512<<20) }
	f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 && !answerOnly {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("core_memory_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Atomic tool completed."))
		}
	}, func(deps *serviceDeps) {
		deps.beforeRead = func(binding Binding, scope string) error {
			if binding == holder && scope == "context" && holding.CompareAndSwap(false, true) {
				close(entered)
				<-release
				return ErrDenied
			}
			return nil
		}
		deps.afterLoopCommit = func(receipt ledger.RuntimeLoopReceipt) {
			if receipt.Submission.Operation != "tool.begin" || !lowered.CompareAndSwap(false, true) {
				return
			}
			if wait == "local_slot" {
				configure(1, 512<<20)
				return
			}
			f.service.mu.Lock()
			f.service.loop.mu.Lock()
			live := f.service.loop.turns[receipt.Submission.TurnID]
			buffer := live.reservation.budget.hold[bufferExposure]
			f.service.loop.mu.Unlock()
			f.service.mu.Unlock()
			configure(2, buffer+holderBytes)
		}
		deps.afterScopedCommit = func(receipt ScopedReceipt) error {
			if lowered.Load() && receipt.Origin == f.origin && (receipt.Operation == "private.read" || receipt.Operation == "private.write") {
				restore()
			}
			return nil
		}
	})
	revision := f.seedMemory(t, "Original target memory.")
	if wait == "local_slot" {
		arguments = fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"Written exactly once."}`, revision)
	} else {
		arguments = fmt.Sprintf(`{"schema":1,"action":"read","scope":"context","slot":"memory","revision":%d,"max_bytes":65536}`, revision)
	}
	holder = scopedWorkContext(t, f.service, bindingOf(f.origin), 76000, 76001)
	grant := uuid.NewV7()
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	if _, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: grant, Grantee: holder, Operations: []string{"private.read", "private.write"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(holder), Scope: "context", Slots: []string{"memory"}}}, DataClass: "private", Recipients: []Binding{holder}, Limits: capacity, Lifetime: "until_revoked"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: holder, Scope: "context"})
	if err != nil {
		t.Fatal(err)
	}
	reader := scopedBound(t, f.service, holder)
	written, err := reader.WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: grant, Scope: "context", ExpectedVersion: snapshot.Revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: bytes.Repeat([]byte("h"), 128<<10)}}})
	if err != nil {
		t.Fatal(err)
	}
	read := ScopedPrivateRead{CommandID: uuid.NewV7(), GrantID: grant, Scope: "context", Slot: "memory", Revision: uint64(written.Version), MaxBytes: 4 << 20}
	done := make(chan error, 1)
	go func() { _, err := reader.ReadPrivate(context.Background(), read); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("real holder did not claim: %v", err)
	}
	f.service.mu.Lock()
	holderBytes = f.service.p.book.preparing[ledger.ScopeKey{Kind: "agent", ID: f.origin.AgentID}][bufferExposure]
	f.service.mu.Unlock()
	var once sync.Once
	free := func() {
		once.Do(func() {
			close(release)
			if err := <-done; !errors.Is(err, ErrDenied) {
				t.Errorf("holder did not release without a receipt: %v", err)
			}
		})
	}
	t.Cleanup(free)
	return &continuationLocalFixture{turnFixture: f, reader: reader, read: read, free: free, restore: restore, posts: &posts}
}

func (f *continuationLocalFixture) assertSlotReleased(t testing.TB) {
	t.Helper()
	request := f.read
	request.CommandID = uuid.NewV7()
	if _, err := f.reader.ReadPrivate(context.Background(), request); err != nil {
		t.Fatalf("sibling could not admit at the exact one-slot capacity: %v", err)
	}
}

func testContinuationLocalWaits(t *testing.T) {
	for _, row := range []struct{ name, wait string }{{"local_slot_resume_before_effect", "local_slot"}, {"private_read_gate_held_resume", "local_buffer"}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newContinuationLocal(t, row.wait, false)
			f.enable(t)
			_, start := f.send(t)
			waiting := continuationWait(t, f.turnFixture, start.TurnID, row.wait)
			if waiting.ResumeState != "tool_pending" {
				t.Fatalf("wrong saved local resume phase: %+v", waiting)
			}
			before := f.store.Durable()
			probe := scopedBound(t, f.service, bindingOf(f.origin))
			snapshot, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = probe.ReadPrivate(context.Background(), ScopedPrivateRead{CommandID: uuid.NewV7(), GrantID: f.privateGrant, Scope: "context", Slot: "memory", Revision: snapshot.Revision, MaxBytes: 64 << 10})
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Reason != "agent_capacity" || busy.waitKind != row.wait || f.store.Durable() != before {
				t.Fatalf("capacity reason or classification changed: %v", err)
			}
			f.free()
			result, events := f.finished(t, start.TurnID)
			if result.Outcome != "finished" || result.ToolDispatches != 1 {
				t.Fatalf("atomic tool failed or counted twice: %+v", result)
			}
			var resume uint64
			if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
				for _, record := range c.Records {
					if record.TurnID == start.TurnID && record.Type == ledger.TypeRuntimeContinuationReceipt {
						v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
						if v.Submission.ExpectedState == "waiting_resource" && v.Submission.Arguments.State == "tool_pending" {
							if resume != 0 {
								t.Fatal("local resume repeated")
							}
							resume = uint64(v.EffectSequence)
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			results := 0
			for _, event := range events {
				if tool, ok := event.Data.(ledger.RuntimeToolResult); ok {
					results++
					if tool.Outcome != "ok" || resume == 0 || tool.EffectReceiptSequence <= int64(resume) {
						t.Fatalf("atomic effect preceded the successful resume: resume=%d tool=%+v", resume, tool)
					}
				}
			}
			if results != 1 {
				t.Fatalf("tool emitted %d results", results)
			}
			f.assertSlotReleased(t)
		})
	}
}

func testContinuationStopClaim(t *testing.T) {
	t.Run("stop_between_claim_and_resume", func(t *testing.T) {
		t.Parallel()
		f := newContinuationLocal(t, "local_slot", false)
		claimed, release := make(chan struct{}), make(chan struct{})
		var held atomic.Bool
		f.service.deps.afterScopedClaim = func(sub ledger.ScopedSubmission) {
			if sub.Origin == f.origin && sub.Operation == "private.write" && held.CompareAndSwap(false, true) {
				close(claimed)
				<-release
			}
		}
		var once sync.Once
		t.Cleanup(func() { once.Do(func() { close(release) }) })
		f.enable(t)
		_, start := f.send(t)
		continuationWait(t, f.turnFixture, start.TurnID, "local_slot")
		f.free()
		<-claimed
		accepted, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: start.TurnID}})
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("stop did not win before resume: %+v %v", accepted, err)
		}
		f.restore()
		once.Do(func() { close(release) })
		result, events := f.finished(t, start.TurnID)
		if result.Outcome != "cancelled" || result.ToolDispatches != 1 {
			t.Fatalf("stopped claim did not finish once: %+v", result)
		}
		var effect uuid.UUID
		results := 0
		for _, event := range events {
			if tool, ok := event.Data.(ledger.RuntimeToolStarted); ok {
				effect = tool.EffectCommandID
			}
			if tool, ok := event.Data.(ledger.RuntimeToolResult); ok {
				results++
				if tool.EffectReceiptSequence != 0 {
					t.Fatal("atomic effect landed after the persisted stop")
				}
			}
		}
		match, err := f.service.findCommand(context.Background(), nil, effect, 0, f.store.Durable())
		if err != nil || match.found || !validID(effect) || results != 1 {
			t.Fatalf("stopped claim submitted its fixed native command: found=%v results=%d err=%v", match.found, results, err)
		}
		f.assertSlotReleased(t)
	})
}

func testContinuationPaidReadWait(t *testing.T) {
	t.Run("paid_result_read_slot_resume_before_io", func(t *testing.T) {
		t.Parallel()
		f := newContinuationLocal(t, "local_slot", true)
		before := f.service.deps.beforeRead
		var reads atomic.Int32
		f.service.deps.beforeRead = func(binding Binding, scope string) error {
			if binding == bindingOf(f.origin) && scope == "paid_object" {
				n := reads.Add(1)
				f.service.mu.Lock()
				if n == 1 {
					// This read and the real sibling reader already own their
					// claims. After this read releases, the sibling fills the
					// process capacity before the next paid evidence read.
					f.service.limits.MaxConcurrentWrites = f.service.preparing - 1
				} else {
					for _, owner := range f.service.p.owners {
						if owner.Origin == f.origin && (owner.Snapshot.State != "model_pending" || owner.Snapshot.WaitReason != "none") {
							t.Errorf("paid evidence I/O before its claimed resume: %+v", owner.Snapshot)
						}
					}
				}
				f.service.mu.Unlock()
			}
			return before(binding, scope)
		}
		f.enable(t)
		_, start := f.send(t)
		finishBusyState(t, f.view, f.origin.ContextID, start.TurnID, "waiting_resource", "")
		status := finishBusyStatus(t, f.view, f.origin.ContextID, start.TurnID)
		if status.ResumeState != "model_pending" || status.WaitReason != "local_slot" {
			t.Errorf("paid result read did not publish its slot wait: %+v", status)
		}
		if f.posts.Load() != 1 || reads.Load() != 1 {
			t.Fatalf("wait did not follow the first paid result read: posts=%d reads=%d", f.posts.Load(), reads.Load())
		}
		through := f.store.Durable()
		runtime := f.service.loop
		for range 2 {
			generation := runtime.generation.Load() + 1
			runtime.notify()
			for {
				runtime.mu.Lock()
				live, changed := runtime.turns[start.TurnID], runtime.changed
				parked := live != nil && !live.running && live.state == "waiting_resource" && live.waitGeneration >= generation
				runtime.mu.Unlock()
				if f.store.Durable() != through {
					t.Fatal("still-busy paid read resumed or re-parked without its claim")
				}
				if parked {
					break
				}
				<-changed
			}
		}
		f.free()
		result, _ := f.finished(t, start.TurnID)
		if result.Outcome != "finished" || result.ModelCalls != 1 || f.posts.Load() != 1 {
			t.Fatalf("paid read retry repeated or lost its effect: %+v posts=%d", result, f.posts.Load())
		}
		var intents, results, resumes int
		if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
			for _, record := range c.Records {
				if record.TurnID != start.TurnID {
					continue
				}
				switch record.Type {
				case ledger.TypeRuntimePaidDispatchIntent:
					intents++
				case ledger.TypeRuntimePaidResult:
					results++
				case ledger.TypeRuntimeContinuationReceipt:
					receipt := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
					if receipt.Submission.ExpectedState == "waiting_resource" && receipt.Submission.Arguments.State == "model_pending" {
						resumes++
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if intents != 1 || results != 1 || resumes != 1 {
			t.Fatalf("paid result wait duplicated or omitted durable work: intent=%d result=%d resume=%d", intents, results, resumes)
		}
	})
}

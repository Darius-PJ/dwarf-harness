package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type continuationCaptureStore struct {
	*ledgerstore.Store
	before, after func([]ledger.Draft)
}

func (s *continuationCaptureStore) Append(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
	if s.before != nil {
		s.before(drafts)
	}
	committed, err := s.Store.Append(ctx, drafts)
	if err == nil && s.after != nil {
		s.after(drafts)
	}
	return committed, err
}

type continuationCaptureProbe struct {
	mu            sync.Mutex
	holds, passes int
	held          bool
	changed       chan struct{}
	before        func()
}

func (p *continuationCaptureProbe) called(uuid.UUID) error {
	p.mu.Lock()
	before := p.before
	p.mu.Unlock()
	if before != nil {
		before()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { close(p.changed); p.changed = make(chan struct{}) }()
	if p.held {
		p.holds++
		return &workspace.BusyError{Reason: "capture_budget"}
	}
	p.passes++
	return nil
}
func (p *continuationCaptureProbe) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.passes }
func (p *continuationCaptureProbe) attempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.holds + p.passes
}
func (p *continuationCaptureProbe) after(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.holds+p.passes <= n {
		changed := p.changed
		p.mu.Unlock()
		<-changed
		p.mu.Lock()
	}
}

type continuationCaptureFixture struct {
	*turnFixture
	registered workspace.Workspace
	scratch    workspace.Scratch
	parent     workspace.Scratch
	respond    func(http.ResponseWriter, *http.Request)
	store      *continuationCaptureStore
	probe      *continuationCaptureProbe
}

func newContinuationCapture(t *testing.T, temporary bool, configure func(*continuationCaptureStore)) *continuationCaptureFixture {
	t.Helper()
	probe := &continuationCaptureProbe{changed: make(chan struct{})}
	var arguments string
	var posts atomic.Int32
	var capture *continuationCaptureFixture
	var parentScratch workspace.Scratch
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		if capture != nil && capture.respond != nil {
			capture.respond(w, r)
			return
		}
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if posts.Add(1) == 1 {
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls("file_write_v1", arguments), ""))
		} else {
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "Capture once."))
		}
	}, func(deps *serviceDeps) { deps.beforeFinishCommit = probe.called })
	registered := f.finishBusyWorkspace(t)
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	store := &continuationCaptureStore{Store: f.store}
	ws, _, err := workspace.Open(context.Background(), store, f.root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = ws
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if err := f.service.Close(); err != nil {
			t.Error(err)
		}
	})
	scratch := f.finishBusyFiles(t, registered, bindingOf(f.origin))
	arguments = finishBusyWrite(registered.ID, "owned-capture")
	if temporary {
		parent := scopedWorkContext(t, f.service, bindingOf(f.origin), 78000, 78001)
		taskScratch := f.finishBusyFiles(t, registered, parent)
		writer, err := f.workspace.BeginWrite(context.Background(), workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: parent.ContextID, AgentID: parent.AgentID, TaskAgentID: parent.AgentID, TaskScratchID: taskScratch.ID})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(taskScratch.Dir, "safe", "seed.txt"), []byte("parent seed\n"))
		_, err = writer.CommitObserved(context.Background())
		writer.Close()
		if err != nil {
			t.Fatal(err)
		}
		parentScratch, err = f.workspace.Scratch(context.Background(), parent.ContextID, registered.ID)
		if err != nil {
			t.Fatal(err)
		}
		capacity := paidPhysicalCapacity()
		capacity.MaxOperationBytes = 4 << 20
		if _, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: uuid.NewV7(), Grantee: bindingOf(f.origin), Operations: []string{"file.write", "file.edit"}, Resources: []ledger.Resource{{Kind: "workspace", Binding: ledger.Binding(parent), WorkspaceID: registered.ID}}, DataClass: "workspace", Recipients: []Binding{bindingOf(f.origin)}, Limits: capacity, Lifetime: "until_revoked"}}); err != nil {
			t.Fatal(err)
		}
		arguments = fmt.Sprintf(`{"schema":1,"workspace_id":%q,"task_context_id":%q,"path":"safe/owned-capture.txt","expected_sha256":"absent","text":"temporary capture"}`, registered.ID, parent.ContextID)
	}
	if configure != nil {
		configure(store)
	}
	capture = &continuationCaptureFixture{turnFixture: f, registered: registered, scratch: scratch, parent: parentScratch, store: store, probe: probe}
	return capture
}

// The existing finish seam returns the workspace's typed busy answer before
// CommitObserved. Passing the seam always performs the real observation.
func (f *continuationCaptureFixture) holdCapture(t *testing.T) func() {
	t.Helper()
	f.probe.mu.Lock()
	f.probe.held = true
	f.probe.mu.Unlock()
	var once sync.Once
	free := func() {
		once.Do(func() {
			f.probe.mu.Lock()
			f.probe.held = false
			f.probe.mu.Unlock()
			// Release as the workspace notifier does: quantum() counts releases.
			f.service.loop.released()
		})
	}
	t.Cleanup(free)
	return free
}

func continuationWait(t *testing.T, f *turnFixture, turn uuid.UUID, reason string) session.TurnView {
	t.Helper()
	var cursor uint64
	for {
		page, err := f.view.Since(context.Background(), cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		var parked bool
		var phase string
		var saved ledger.RuntimeContinuationChanged
		f.service.mu.Lock()
		if runtime := f.service.loop; runtime != nil {
			runtime.mu.Lock()
			if current := runtime.turns[turn]; current != nil {
				// A gated park stays queued (the completion enqueues it, next()
				// re-enqueues it under runtime.mu, prepareQuantum's park enqueues
				// it). Only the dequeue → prepareQuantum → running window drops
				// it, where a stop's own finishing step may already be durable.
				parked = !current.running && current.state == "waiting_resource" && runtime.queued[turn]
				phase = "model_pending"
				if current.reason != "" || current.answer != nil && len(current.tools) == 0 {
					phase = "finishing"
				} else if len(current.tools) != 0 {
					phase = "tool_pending"
				}
				saved = f.service.p.owners[turn].Snapshot
			}
			runtime.mu.Unlock()
		}
		f.service.mu.Unlock()
		// A stop may replace the old park before the scheduler picks it up.
		if parked && saved.State != "stopping" && (saved.State != "waiting_resource" || saved.ResumeState != phase || saved.WaitReason != reason) {
			t.Fatalf("RAM park lacks matching durable wait: want=%s/%s saved=%+v", phase, reason, saved)
		}
		status := finishBusyStatus(t, f.view, f.origin.ContextID, turn)
		if status.Result != nil {
			_, events := f.finished(t, turn)
			for _, tool := range f.toolResults(t, events) {
				if tool.Error != nil {
					t.Logf("tool failure: %+v", *tool.Error)
				}
			}
			t.Fatalf("turn ended before %s wait: %+v tools=%+v", reason, status, f.toolResults(t, events))
		}
		if status.State == "waiting_resource" && status.SavedPhase == "waiting_resource" && status.WaitReason == reason {
			return status
		}
		cursor = page.ThroughSequence
		<-page.Wake
	}
}

func continuationCaptureRecords(t testing.TB, f *continuationCaptureFixture, turn uuid.UUID) ([]ledger.RuntimeContinuationReceipt, []uint64, []uint64) {
	t.Helper()
	var owners []ledger.RuntimeContinuationReceipt
	var observations, endings []uint64
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeScratchChanged {
				v := scopedDecode(t, ledger.DecodeScratchChanged, record.Payload)
				if v.ContextID == f.origin.ContextID {
					observations = append(observations, record.Seq)
				}
			}
			if record.TurnID != turn {
				continue
			}
			if record.Type == ledger.TypeRuntimeContinuationReceipt {
				owners = append(owners, scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload))
			}
			if record.Type == ledger.TypeRuntimeTurnResult {
				endings = append(endings, record.Seq)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return owners, observations, endings
}

func TestTurnLoopPersistedWaits(t *testing.T) {
	testContinuationLocalWaits(t)
	testContinuationCaptureTail(t)
	testContinuationMergeWaits(t)
	testContinuationPaidReadWait(t)
	t.Run("capture_busy_park_before_park", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, false, nil)
		free := f.holdCapture(t)
		f.enable(t)
		_, started := f.send(t)
		waiting := continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
		owners, observations, endings := continuationCaptureRecords(t, f, started.TurnID)
		if waiting.ResumeState != "finishing" || waiting.OwnerVersion == 0 || len(observations) != 0 || len(endings) != 0 || owners[len(owners)-1].Submission.Arguments.State != "waiting_resource" {
			t.Fatalf("RAM parked before its durable capture wait: %+v", waiting)
		}
		free()
		result, _ := f.finished(t, started.TurnID)
		if result.Outcome != "finished" {
			t.Fatalf("capture did not complete: %+v", result)
		}
		owners, observations, endings = continuationCaptureRecords(t, f, started.TurnID)
		resumes := 0
		for _, owner := range owners {
			if owner.Submission.ExpectedState == "waiting_resource" && owner.Submission.Arguments.State == "finishing" {
				resumes++
				if len(observations) != 1 || uint64(owner.EffectSequence) <= observations[0] {
					t.Fatal("capture resumed before its successful observation")
				}
			}
		}
		if len(observations) != 1 || len(endings) != 1 || resumes != 1 {
			t.Fatalf("capture repeated or skipped its owed resume: observe=%v end=%v resume=%d", observations, endings, resumes)
		}
	})
	t.Run("capture_first_call_writes_nothing", func(t *testing.T) {
		t.Parallel()
		entered, release := make(chan struct{}), make(chan struct{})
		var target uuid.UUID
		var once sync.Once
		f := newContinuationCapture(t, false, func(store *continuationCaptureStore) {
			store.before = func(d []ledger.Draft) {
				for _, draft := range d {
					if draft.Type == ledger.TypeScratchChanged {
						v := scopedDecode(t, ledger.DecodeScratchChanged, draft.Payload)
						if v.ScratchID == target {
							once.Do(func() { close(entered); <-release })
						}
					}
				}
			}
		})
		target = f.scratch.ID
		var releaseOnce sync.Once
		t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
		f.enable(t)
		_, started := f.send(t)
		<-entered
		owner, err := f.service.currentContinuation(context.Background(), f.origin, started.TurnID)
		if err != nil || owner.Snapshot.State != "finishing" || owner.Snapshot.WaitReason != "none" || f.probe.count() != 1 {
			t.Fatalf("first immediate capture invented a wait: %+v %v", owner.Snapshot, err)
		}
		releaseOnce.Do(func() { close(release) })
		f.finished(t, started.TurnID)
		owners, observations, endings := continuationCaptureRecords(t, f, started.TurnID)
		for _, owner := range owners {
			if owner.Submission.Arguments.State == "waiting_resource" || owner.Submission.ExpectedState == "waiting_resource" && owner.Submission.Arguments.State == "finishing" {
				t.Fatal("successful first capture wrote a park/resume")
			}
		}
		if len(observations) != 1 || len(endings) != 1 || f.probe.count() != 1 {
			t.Fatal("successful first capture was repeated")
		}
	})
	t.Run("capture_still_busy_rerun_writes_nothing", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, false, nil)
		free := f.holdCapture(t)
		other := scopedWorkContext(t, f.service, bindingOf(f.origin), 75000, 75001)
		scratch, err := f.workspace.Scratch(context.Background(), other.ContextID, f.registered.ID)
		if err != nil {
			t.Fatal(err)
		}
		writer, err := f.workspace.BeginWrite(context.Background(), workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: other.ContextID, AgentID: other.AgentID, TaskAgentID: other.AgentID, TaskScratchID: scratch.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		f.enable(t)
		_, started := f.send(t)
		waiting := continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
		before := f.store.Durable()
		for range 2 {
			calls := f.probe.attempts()
			release, err := writer.Lease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			release() // a real non-ledger workspace availability notification
			f.probe.after(calls)
			again := continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
			if again.OwnerVersion != waiting.OwnerVersion || again.OperationID != waiting.OperationID || f.store.Durable() != before {
				t.Fatalf("still-busy wake wrote owner progress: before=%+v after=%+v", waiting, again)
			}
		}
		free()
		f.finished(t, started.TurnID)
	})
}

func testContinuationStopCapture(t *testing.T) {
	t.Run("stop_during_capture_wait", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, false, nil)
		free := f.holdCapture(t)
		f.enable(t)
		_, started := f.send(t)
		continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
		capture, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		t.Cleanup(func() { once.Do(func() { close(release) }) })
		f.probe.mu.Lock()
		f.probe.before = func() {
			owner, err := f.service.currentContinuation(context.Background(), f.origin, started.TurnID)
			if err != nil {
				t.Error(err)
				return
			}
			if owner.Snapshot.StopReason != "" {
				close(capture)
				<-release
			}
		}
		f.probe.mu.Unlock()
		command := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: started.TurnID}}
		accepted, err := f.view.Do(context.Background(), command)
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("busy capture delayed or refused stop acceptance: %+v %v", accepted, err)
		}
		<-capture
		_, observations, endings := continuationCaptureRecords(t, f, started.TurnID)
		if len(observations) != 0 || len(endings) != 0 {
			t.Fatal("accepted stop claimed the blocked observation had completed")
		}
		free()
		once.Do(func() { close(release) })
		result, _ := f.finished(t, started.TurnID)
		if result.Outcome != "cancelled" || result.ToolDispatches != 1 {
			t.Fatalf("stop lost its result or tool count: %+v", result)
		}
		owners, observations, endings := continuationCaptureRecords(t, f, started.TurnID)
		stops := 0
		for _, owner := range owners {
			if owner.Submission.Operation == "owner.stop" {
				stops++
			}
			if owner.Submission.ExpectedState == "waiting_resource" && uint64(owner.EffectSequence) > accepted.EffectSequence && owner.Submission.Arguments.State == "finishing" {
				t.Fatal("capture resumed after the stop replaced its wait")
			}
		}
		if stops != 1 || len(observations) != 1 || len(endings) != 1 {
			t.Fatalf("stopped capture repeated work: stops=%d observations=%v endings=%v", stops, observations, endings)
		}
	})
	t.Run("stop_then_capture_busy_again", func(t *testing.T) {
		t.Parallel()
		f := newContinuationCapture(t, false, nil)
		free := f.holdCapture(t)
		f.enable(t)
		_, started := f.send(t)
		waiting := continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
		cancel := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: started.TurnID}}
		accepted, err := f.view.Do(context.Background(), cancel)
		if err != nil || accepted.Outcome != "accepted" {
			t.Fatalf("stop was not accepted: %+v %v", accepted, err)
		}
		parked := continuationWait(t, f.turnFixture, started.TurnID, "workspace_capture")
		if parked.OperationID != cancel.CommandID || parked.Generation != waiting.Generation+1 || parked.Reason != "cancelled" {
			t.Fatalf("post-stop wait lost its durable stop operation: %+v", parked)
		}
		owners, _, _ := continuationCaptureRecords(t, f, started.TurnID)
		last := owners[len(owners)-1]
		if last.Submission.Arguments.State != "waiting_resource" || last.Submission.ExpectedState != "finishing" || last.Submission.Arguments.OperationID != cancel.CommandID {
			t.Fatal("post-stop capture waited without its own park")
		}
		before := f.store.Durable()
		cancel.CommandID = uuid.NewV7()
		repeated, err := f.view.Do(context.Background(), cancel)
		if err != nil || repeated.Outcome != "accepted" || f.store.Durable() != before {
			t.Fatalf("a later stop rewrote the first durable stop: %+v %v", repeated, err)
		}
		free()
		result, _ := f.finished(t, started.TurnID)
		if result.Outcome != "cancelled" {
			t.Fatalf("post-stop capture ignored cancellation: %+v", result)
		}
		owners, observations, endings := continuationCaptureRecords(t, f, started.TurnID)
		if len(observations) != 1 || len(endings) != 1 {
			t.Fatalf("post-stop capture repeated an effect: observations=%v endings=%v", observations, endings)
		}
		stops, resumes := 0, 0
		for _, owner := range owners {
			if owner.Submission.Operation == "owner.stop" {
				stops++
			}
			if owner.Submission.ExpectedState == "waiting_resource" && owner.Submission.Arguments.State == "finishing" && uint64(owner.EffectSequence) > accepted.EffectSequence {
				resumes++
				if owner.Submission.Arguments.OperationID != parked.OperationID || owner.Submission.Arguments.Generation != parked.Generation || uint64(owner.EffectSequence) <= observations[0] {
					t.Fatal("post-stop resume used the replaced park or preceded capture")
				}
			}
		}
		if stops != 1 || resumes != 1 {
			t.Fatalf("post-stop capture repeated a stop or resume: stops=%d resumes=%d", stops, resumes)
		}
	})
}

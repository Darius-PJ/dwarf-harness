package agents

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// The pre-append hook is outside the Store. It parks the first real owner
// submission after a durable paid intent without fabricating a ledger state.
// The parent test kills this process while the submission is still uncommitted.

type passiveCrashStore struct {
	*ledgerstore.Store
	before func([]ledger.Draft)
}

func (s *passiveCrashStore) SubmitAt(ctx context.Context, through uint64, command ledger.Draft, effects []ledger.Draft) (ledgerstore.Receipt, error) {
	s.before(append([]ledger.Draft{command}, effects...))
	return s.Store.SubmitAt(ctx, through, command, effects)
}

func continuationIntegratedCrashChild(t *testing.T, boundary string, barrier func(*turnFixture, uuid.UUID, workspace.Scratch)) {
	t.Helper()
	if boundary == "passive_before_cursor" {
		f := newPassiveFixture(t)
		f.enable(t, f.parentSetting)
		f.merge(t)
		local, paid := *f.turnFixture, *f.paidFixture
		paid.origin.ContextID = f.parent.ContextID
		local.paidFixture = &paid
		view, err := f.service.OpenSession(context.Background(), f.parentSetting.Binding)
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		command := turnMessage(f.parent.ContextID, "passive-natural crash")
		turn := command.Arguments.(session.MessageSend).TurnID
		f.service.store = &passiveCrashStore{Store: f.turnFixture.store, before: func(drafts []ledger.Draft) {
			for _, draft := range drafts {
				if draft.Type != ledger.TypeRuntimeContinuationReceipt {
					continue
				}
				receipt := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, draft.Payload)
				if receipt.Submission.TurnID == turn && receipt.Submission.Operation == "owner.advance" && receipt.Submission.Arguments.ModelCalls > 0 {
					if len(receipt.Submission.Arguments.NoticeCursors) != 1 {
						t.Fatal("first post-intent submission did not owe the selected cursor")
					}
					barrier(&local, turn, f.parent)
				}
			}
		}}
		if _, err := view.Do(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		f.finishedFor(t, view, turn)
		t.Fatal("passive pre-cursor kill barrier was not reached")
		return
	}
	f := newContinuationCapture(t, true, nil)
	parent := integratedParentSetting(t, f)
	var turn uuid.UUID
	var source workspace.Scratch
	f.probe.before = func() {
		f.service.loop.mu.Lock()
		live := f.service.loop.turns[turn]
		f.service.loop.mu.Unlock()
		for _, writer := range live.writers {
			source = writer.Scratch()
		}
	}
	f.store.after = func(drafts []ledger.Draft) {
		for _, draft := range drafts {
			if draft.Type != ledger.TypeScratchMergeBack {
				continue
			}
			plan := scopedDecode(t, ledger.DecodeScratchMergeBack, draft.Payload)
			if plan.Outcome == ledger.MergeBackPrepared {
				barrier(f.turnFixture, turn, source)
			}
		}
	}
	f.enable(t, parent)
	command := turnMessage(f.origin.ContextID, "merge preparation kill")
	turn = command.Arguments.(session.MessageSend).TurnID
	if _, err := f.view.Do(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	f.finished(t, turn)
	t.Fatal("prepared merge kill barrier was not reached")
}

func integratedReconcileTwice(t *testing.T, f *continuationRestart) {
	t.Helper()
	owner := f.owner(t)
	request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
	first, err := f.service.ReconcileLoop(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	through := f.store.Durable()
	repeated, err := f.service.ReconcileLoop(context.Background(), request)
	if err != nil || !reflect.DeepEqual(first, repeated) || through != f.store.Durable() {
		t.Fatal("reconciliation retry did work", err)
	}
	f.assertEnded(t, "interrupted", 0)
}

func testIntegratedMergeCrash(t *testing.T) {
	f := continuationKilled(t, "merge_after_prepared")
	var prepared ledger.ScratchMergeBack
	var observed ledger.ScratchChanged
	inspect := func() (observations, preparations, applications int) {
		err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
			for _, record := range c.Records {
				if record.Type == ledger.TypeScratchChanged {
					v := scopedDecode(t, ledger.DecodeScratchChanged, record.Payload)
					if v.ScratchID == f.signal.Scratch.ID {
						observed = v
						observations++
					}
				}
				if record.Type == ledger.TypeScratchMergeBack {
					v := scopedDecode(t, ledger.DecodeScratchMergeBack, record.Payload)
					if v.ScratchID != f.signal.Scratch.ID {
						continue
					}
					if v.Outcome == ledger.MergeBackPrepared {
						prepared = v
						preparations++
					} else {
						applications++
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if o, p, a := inspect(); o != 1 || p != 1 || a != 0 || f.signal.Scratch.ParentScratchID == (uuid.UUID{}) {
		t.Fatalf("wrong real-kill boundary: observations=%d prepared=%d applied=%d", o, p, a)
	}
	var ws *workspace.Service
	for range 2 {
		before := f.store.Durable()
		opened, _, err := workspace.Open(context.Background(), f.store, f.signal.Root, workspace.Config{})
		if err != nil {
			t.Fatal(err)
		}
		ws = opened
		parent, err := ws.Scratch(context.Background(), prepared.ContextID, prepared.WorkspaceID)
		if err != nil || parent.TipCommit != prepared.ParentCommit || f.store.Durable() != before {
			t.Fatal("workspace Open applied the pending plan", err)
		}
		actual, err := exec.CommandContext(t.Context(), "git", "-C", parent.Dir, "rev-parse", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(actual)) != prepared.ParentCommit {
			t.Fatal("workspace Open moved the parent ref", err)
		}
		assertIntegratedPendingFence(t, ws, f.signal, prepared)
		if err := ws.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	f.service.deps.beforeFinishCommit = func(uuid.UUID) error { calls.Add(1); return errors.New("restart must not observe a scratch") }
	integratedReconcileTwice(t, f)
	if o, p, a := inspect(); o != 1 || p != 1 || a != 0 || calls.Load() != 0 {
		t.Fatalf("restart performed workspace work: %d/%d/%d captures=%d", o, p, a, calls.Load())
	}
	ws, _, err := workspace.Open(context.Background(), f.store, f.signal.Root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	parent, err := ws.Scratch(context.Background(), prepared.ContextID, prepared.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := exec.CommandContext(t.Context(), "git", "-C", parent.Dir, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(actual)) != prepared.ParentCommit {
		t.Fatal("bookkeeping moved the parent ref", err)
	}
	assertIntegratedPendingFence(t, ws, f.signal, prepared)
	applied, err := ws.RetryMergeBack(context.Background(), prepared.MergeBackID)
	if err != nil || applied.ID != prepared.MergeBackID || applied.TargetCommit != prepared.TargetCommit || applied.TargetTree != prepared.TargetTree || applied.Outcome != ledger.MergeBackFastForward {
		t.Fatalf("explicit retry changed the recorded plan: %+v %v", applied, err)
	}
	through := f.store.Durable()
	repeated, err := ws.RetryMergeBack(context.Background(), prepared.MergeBackID)
	if err != nil || repeated != applied || through != f.store.Durable() {
		t.Fatal("applied retry was not idempotent", err)
	}
	if o, p, a := inspect(); o != 1 || p != 1 || a != 1 {
		t.Fatalf("explicit retry repeated observation/application: %d/%d/%d", o, p, a)
	}
	if observed.ScratchCommit != prepared.SourceCommit || !bytes.Equal(readTestFile(t, filepath.Join(parent.Dir, "safe", "owned-capture.txt")), []byte("temporary capture")) {
		t.Fatal("explicit retry did not land the acknowledged source")
	}
	assertIntegratedTree(t, parent.Dir, applied.TargetTree, map[string]string{"safe/seed.txt": "parent seed\n", "safe/owned-capture.txt": "temporary capture"})
}

func assertIntegratedPendingFence(t *testing.T, ws *workspace.Service, signal continuationCrashSignal, prepared ledger.ScratchMergeBack) {
	t.Helper()
	for _, contextID := range []uuid.UUID{prepared.ContextID, signal.Origin.ContextID} {
		binding := workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: contextID, AgentID: signal.Origin.AgentID, TaskAgentID: signal.Origin.AgentID, TaskScratchID: prepared.ParentScratchID}
		if contextID == signal.Origin.ContextID {
			binding.ResumeScratchID = prepared.ScratchID
		}
		writer, err := ws.BeginWrite(context.Background(), binding)
		if writer != nil {
			writer.Close()
		}
		if !errors.Is(err, workspace.ErrMergeBackPending) {
			t.Fatal("pending plan admitted a parent or source writer", err)
		}
	}
	if _, err := ws.MergeBack(context.Background(), prepared.ScratchID); !errors.Is(err, workspace.ErrMergeBackPending) {
		t.Fatal("pending plan admitted a new merge", err)
	}
}

func testPassiveCursorCrash(t *testing.T) {
	f := continuationKilled(t, "passive_before_cursor")
	owner := f.owner(t)
	if len(owner.Snapshot.NoticeCursors) != 0 {
		t.Fatal("an uncommitted cursor survived the kill")
	}
	intents := 0
	for _, record := range f.records(t) {
		if record.Type == ledger.TypeRuntimePaidDispatchIntent {
			intents++
		}
	}
	if intents != 1 {
		t.Fatalf("kill did not follow the first real intent: %d", intents)
	}
	integratedReconcileTwice(t, f)
	if len(f.owner(t).Snapshot.NoticeCursors) != 0 {
		t.Fatal("restart bookkeeping consumed the notice")
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 1)
	var posts atomic.Int32
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		posts.Add(1)
		requests <- string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "After real kill."))
	})
	s, report, err := openPaidService(context.Background(), f.store, f.signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, paidServerDeps(server, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.service = s
	if len(report.Issues) != 0 || len(report.PendingLoops) != 0 || posts.Load() != 0 {
		t.Fatal("reopen executed a call or lost bookkeeping")
	}
	ws, _, err := workspace.Open(context.Background(), f.store, f.signal.Root, workspace.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	defer s.Close()
	var setting LoopContextSettings
	for _, candidate := range f.signal.Settings {
		if candidate.Binding.ContextID == f.signal.Origin.ContextID {
			setting = candidate
		}
	}
	var credential ledger.RuntimeCredentialChanged
	for _, candidate := range s.p.paidCredentials {
		if candidate.ScopeID == setting.CredentialScopeID && candidate.Version > credential.Version {
			credential = candidate
		}
	}
	_, err = s.BindCredential(context.Background(), bindingOf(f.signal.Origin), CredentialBinding{CommandID: uuid.NewV7(), ScopeID: credential.ScopeID, ExpectedVersion: credential.Version, Provider: "anthropic", CredentialVersion: uuid.NewV7(), SourceKind: "environment", SourceLocator: paidTestEnvironment, RevokePreviousVersion: credential.CredentialVersion})
	if err != nil {
		t.Fatal(err)
	}
	route := s.p.paidRoutes[setting.RouteID]
	profile := providerProfile(route.Profile)
	profile.Authority = s.deps.paidTestAuthority
	// A new loopback port is a different capability profile, even though its
	// provider and synthetic tariffs have not changed.
	profile.CapabilityVersion += "-restart"
	rawProfile, err := profile.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ConfigurePaidRoute(context.Background(), setting.Binding, PaidRouteConfiguration{CommandID: uuid.NewV7(), RouteID: route.RouteID, ExpectedVersion: route.Version, Enabled: true, CredentialScopeID: route.CredentialScopeID, Profile: rawProfile})
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.signal.Settings {
		if f.signal.Settings[i].RouteID == route.RouteID {
			f.signal.Settings[i].ExpectedRouteVersion = s.p.paidRoutes[route.RouteID].Version
		}
	}
	if err := s.EnableTurnLoop(context.Background(), ws, LoopOptions{Contexts: f.signal.Settings}); err != nil {
		t.Fatal(err)
	}
	view, err := s.OpenSession(context.Background(), setting.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	fixture := &turnFixture{paidFixture: &paidFixture{serviceFixture: &serviceFixture{root: f.signal.Root, store: f.store, service: s}, origin: f.signal.Origin}, workspace: ws, view: view, setting: setting}
	_, start := fixture.send(t)
	result, _ := fixture.finished(t, start.TurnID)
	_, input := passiveInput(t, fixture, f.signal.Origin, start.TurnID)
	notices, err := ws.ScratchNotices(context.Background(), f.signal.Origin.ContextID, f.signal.Scratch.ID, 0)
	if err != nil || len(notices) != 1 || len(input.TaskResults) != 1 || !sameScopedValue(input.TaskResults[0], loopNoticeResult(notices[0])) || result.Outcome != "finished" || posts.Load() != 1 || !strings.Contains(<-requests, notices[0].MergeBackID.String()) {
		t.Fatalf("next natural turn lost the uncommitted notice: %+v %v", result, err)
	}
	cursor, err := s.currentContinuation(context.Background(), f.signal.Origin, start.TurnID)
	if err != nil || !reflect.DeepEqual(cursor.Snapshot.NoticeCursors, input.NoticeCursors) {
		t.Fatal("new intent did not commit its exact cursor", err)
	}
}

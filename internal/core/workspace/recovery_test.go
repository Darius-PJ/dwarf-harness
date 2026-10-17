package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func recoveryRequests(t *testing.T, f *serviceFixture) (Request, Request) {
	t.Helper()
	scratch := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(scratch.Dir, "delivered.txt"), []byte("sealed\n"))
	r := rootRequest(scratch)
	mustAdmit(t, f, r)
	foreign := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(foreign.Dir, "queued.txt"), []byte("queued\n"))
	queued := rootRequest(foreign)
	mustAdmit(t, f, queued)
	mustIntegrate(t, f)
	return r, queued
}

func holdFinalizationIntent(t *testing.T, f *serviceFixture, r Request, step string) ledger.GenerationDerived {
	t.Helper()
	g := currentGeneration(t, f, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.deps.beforeFinalizeStep = func(at string) {
		if at == step {
			cancel()
		}
	}
	state, err := f.svc.finalize(ctx, r.RequestID, g.GenerationID)
	f.svc.deps.beforeFinalizeStep = nil
	if err == nil || state.State != RequestFinalizing {
		t.Fatalf("hold at %s: %+v %v", step, state, err)
	}
	return g
}

func reopenRecovery(t *testing.T, f *serviceFixture) (*writerQuiescence, Reconciliation) {
	t.Helper()
	prior := f.svc
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.svc, report, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
	if err != nil {
		t.Fatal(err)
	}
	return &writerQuiescence{WorkspaceID: f.w.ID, DrainedService: prior}, report
}

func assertRecoveryOnce(t *testing.T, f *serviceFixture, r Request, proof *writerQuiescence, want string) {
	t.Helper()
	updates := countMainUpdates(f.svc.git)
	_, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, proof)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.RequestPromotion(context.Background(), r)
	if err != nil || state.State != want {
		t.Fatalf("reconciled state: %+v %v, want %s", state, err, want)
	}
	before := f.store.Durable()
	got, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, proof)
	if err != nil || got != (promotionRecovery{}) || f.store.Durable() != before || *updates != 0 {
		t.Fatalf("reconciliation repeated effect: %+v %v updates=%d", got, err, *updates)
	}
}

func assertRecoveryGate(t *testing.T, f *serviceFixture, r Request, reason string) {
	t.Helper()
	before := f.store.Durable()
	next := r
	next.Head, next.RequestID = r.RequestID, uuid.NewV7()
	assertUnavailable(t, promotionError(context.Background(), f.svc, next), "promotion_recovery_pending")
	assertUnavailable(t, f.svc.integrate(context.Background(), f.w.ID), reason)
	_, err := f.svc.finalize(context.Background(), r.RequestID, currentGeneration(t, f, r).GenerationID)
	assertUnavailable(t, err, reason)
	_, err = f.svc.noChange(context.Background(), r.RequestID)
	assertUnavailable(t, err, reason)
	if f.store.Durable() != before {
		t.Fatal("recovery gate wrote")
	}
}

func TestRecoveryInterruptsQueuedWithoutRestart(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	r, other := recoveryRequests(t, f)
	child := r
	child.Head, child.RequestID = r.RequestID, uuid.NewV7()
	mustAdmit(t, f, child)
	main := fixtureMain(t, f.gitFixture)
	before := f.store.Durable()
	_, report := reopenRecovery(t, f)
	if !slices.Contains(report.Pending, "promotion_recovery_pending "+f.w.ID.String()) || f.store.Durable() != before {
		t.Fatalf("record-free Open: %+v", report)
	}
	assertRecoveryGate(t, f, r, "promotion_recovery_pending")
	// The log alone proves pre-intent nonpromotion, even with Git unavailable.
	f.svc.git.unavailable = errors.New("Git offline during bookkeeping")
	result, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, nil)
	f.svc.git.unavailable = nil
	if err != nil || result.Interrupted != 2 || result.Accepted != 0 {
		t.Fatalf("queued recovery: %+v %v", result, err)
	}
	for _, row := range []struct {
		r    Request
		want string
	}{{r, RequestInterrupted}, {child, RequestDependencyFailed}, {other, RequestInterrupted}} {
		state, err := f.svc.RequestPromotion(context.Background(), row.r)
		if err != nil || state.State != row.want {
			t.Fatalf("queued result: %+v %v", state, err)
		}
	}
	assertCanonicalTip(t, f, main)
	before = f.store.Durable()
	result, err = f.svc.reconcilePromotions(context.Background(), f.w.ID, nil)
	if err != nil || result != (promotionRecovery{}) || f.store.Durable() != before {
		t.Fatalf("second phase: %+v %v", result, err)
	}
	fresh := child
	fresh.RequestID, fresh.Head, fresh.RestartFrom = uuid.NewV7(), uuid.UUID{}, child.RequestID
	assertUnavailable(t, promotionError(context.Background(), f.svc, fresh), "no_validator")
	mustAdmit(t, f, fresh)
	// Calling the phase again cannot end work admitted after that restart.
	if _, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, nil); err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.RequestPromotion(context.Background(), fresh)
	if err != nil || state.State != RequestPending {
		t.Fatalf("phase ended fresh work: %+v %v", state, err)
	}
}

func TestUnfencedWriterBlocksRecovery(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, tip := range []string{"old", "target", "third", "absent", "missing_commit", "missing_tree", "durability"} {
		t.Run(tip, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			r, queued := recoveryRequests(t, f)
			step := "durability"
			if tip == "old" {
				step = "cas"
			}
			g := holdFinalizationIntent(t, f, r, step)
			proof, _ := reopenRecovery(t, f)
			if tip == "third" {
				advanceMain(t, f, nil)
			}
			if tip == "absent" {
				gitCommand(t, f.env, f.root, nil, "update-ref", "-d", "refs/heads/main")
			}
			if tip == "missing_commit" || tip == "missing_tree" {
				object := g.DerivedCommit
				if tip == "missing_tree" {
					object = g.DerivedTree
				}
				if err := os.Remove(filepath.Join(f.p.CommonDir, "objects", object[:2], object[2:])); err != nil {
					t.Fatal(err)
				}
			}
			updates := countMainUpdates(f.svc.git)
			// A new store lock and a completed prior Close do not become an
			// implicit proof. A nil/missing Wait result is not a PID check.
			for _, absent := range []*writerQuiescence{nil, {WorkspaceID: f.w.ID, PriorCore: &waitedWriter{}}, {WorkspaceID: f.w.ID, PriorCore: &waitedWriter{State: &os.ProcessState{}}}, {WorkspaceID: uuid.NewV7(), DrainedService: proof.DrainedService}, {WorkspaceID: f.w.ID, DrainedService: f.svc}} {
				result, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, absent)
				assertUnavailable(t, err, "writer_unfenced")
				if result.IntentID == (uuid.UUID{}) {
					t.Fatal("unfenced intent disappeared")
				}
			}
			expired, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := f.svc.reconcilePromotions(expired, f.w.ID, nil)
			assertUnavailable(t, err, "writer_unfenced")
			state, err := f.svc.RequestPromotion(context.Background(), queued)
			if err != nil || state.State != RequestInterrupted {
				t.Fatalf("blocked intent kept unrelated slot: %+v %v", state, err)
			}
			f.svc.pool.profile.CheckpointsPerPair = 1
			scratch := f.svc.scratches[f.svc.requests[r.RequestID].admission.ScratchID]
			token, err := f.svc.pool.reserve(scratch.Dir, admissionReservation)
			if token != nil {
				token.release()
				t.Fatal("blocked intent freed its slot")
			}
			assertBusy(t, err, "checkpoint_slots")
			if tip == "durability" {
				pack := filepath.Join(f.p.CommonDir, "objects", "pack")
				if err := os.Remove(pack); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, pack, []byte("not a directory"))
				before := f.store.Durable()
				if _, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, proof); err == nil || f.store.Durable() != before {
					t.Fatal("accepted before recovery durability")
				}
				if err := os.Remove(pack); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(pack, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if tip == "third" || tip == "absent" || tip == "missing_commit" || tip == "missing_tree" {
				before := f.store.Durable()
				if _, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, proof); err == nil || f.store.Durable() != before || *updates != 0 {
					t.Fatal("inferred ambiguous effect")
				}
				return
			}
			// Mutable scratch and current main cannot supply changed_paths:
			// at target, a main-to-target diff would be empty.
			writeTestFile(t, filepath.Join(scratch.Dir, "later-private.txt"), []byte("private\n"))
			want := RequestAccepted
			if tip == "old" {
				want = RequestInterrupted
			}
			assertRecoveryOnce(t, f, r, proof, want)
			if want == RequestAccepted {
				for _, commit := range serviceCommits(t, f.store) {
					for _, record := range commit.Records {
						if record.Type != ledger.TypePromotionAccepted {
							continue
						}
						p, err := ledger.DecodePromotionAccepted(record.Payload)
						if err != nil || p.AcceptedTree != g.DerivedTree || !slices.Equal(p.ChangedPaths, []string{"delivered.txt"}) {
							t.Fatalf("recovery paths: %+v %v", p, err)
						}
					}
				}
			}
		})
	}
}

func TestFinalizationLostAcknowledgment(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, gate := range []string{ledger.TypeFinalizationIntended, ledger.TypePromotionAccepted} {
		t.Run(gate, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			r, queued := recoveryRequests(t, f)
			g := currentGeneration(t, f, r)
			updates := countMainUpdates(f.svc.git)
			f.svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
				if drafts[0].Type == gate {
					f.svc.store = &uncertainStore{Store: f.store, persist: true}
				}
			}
			_, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID)
			if !errors.Is(err, appender.ErrUncertain) || !errors.Is(err, ErrPoisoned) {
				t.Fatalf("response-loss seam: %v", err)
			}
			before := f.store.Durable()
			_, err = f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner})
			if !errors.Is(err, ErrPoisoned) {
				t.Fatal(err)
			}
			if gate == ledger.TypeFinalizationIntended && *updates != 0 {
				t.Fatal("CAS followed uncertain intent acknowledgement")
			}
			if f.store.Durable() != before {
				t.Fatal("poisoned service wrote")
			}
			proof, _ := reopenRecovery(t, f)
			want := RequestInterrupted
			if gate == ledger.TypePromotionAccepted {
				want = RequestAccepted
			}
			assertRecoveryOnce(t, f, r, proof, want)
			state, err := f.svc.RequestPromotion(context.Background(), queued)
			if err != nil || state.State != RequestInterrupted {
				t.Fatalf("queued after lost ack: %+v %v", state, err)
			}
			t.Log("persist=true response loss: real committed/flushed bytes retained; not a native append failure")
		})
	}
}

// A real owned process is held by its stdin, so no sleep or PID-absence poll
// stands in for a completed Wait. The helper is also used by Windows recovery.
func TestHelperRecoveryWriter(t *testing.T) {
	if os.Getenv("DWARF_RECOVERY_WRITER") != "1" {
		t.Skip("owned writer helper only")
	}
	var one [1]byte
	_, _ = os.Stdin.Read(one[:])
}

func ownedRecoveryWriter(t *testing.T) (*exec.Cmd, func() *os.ProcessState) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRecoveryWriter$")
	cmd.Env = append(os.Environ(), "DWARF_RECOVERY_WRITER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	wait := func() *os.ProcessState {
		if !waited {
			_ = stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Error(err)
			}
			waited = true
		}
		return cmd.ProcessState
	}
	t.Cleanup(func() { wait() })
	return cmd, wait
}

func TestUnfencedWriterRequiresEveryWait(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	r, _ := recoveryRequests(t, f)
	holdFinalizationIntent(t, f, r, "cas")
	reopenRecovery(t, f)
	core, waitCore := ownedRecoveryWriter(t)
	git, waitGit := ownedRecoveryWriter(t)
	proof := &writerQuiescence{WorkspaceID: f.w.ID, PriorCore: &waitedWriter{State: core.ProcessState}, PriorGit: []waitedWriter{{State: git.ProcessState}}}
	_, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, proof)
	assertUnavailable(t, err, "writer_unfenced")
	proof.PriorCore.State = waitCore()
	_, err = f.svc.reconcilePromotions(context.Background(), f.w.ID, proof)
	assertUnavailable(t, err, "writer_unfenced")
	proof.PriorGit[0].State = waitGit()
	assertRecoveryOnce(t, f, r, proof, RequestInterrupted)
}

type finalizationCrashPoint struct {
	Request, Queued, Child Request
	Generation             ledger.GenerationDerived
	GitPID                 int
}

// The helper opens an empty-request prefix, then admits its own requests. It
// never bypasses Open's recovery gate to resume a previous execution.
func TestHelperFinalizationCrash(t *testing.T) {
	colony := os.Getenv("DWARF_FINALIZATION_COLONY")
	if colony == "" {
		t.Skip("finalization crash helper only")
	}
	store, _, err := ledgerstore.Open(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, _, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, err := uuid.Parse(os.Getenv("DWARF_FINALIZATION_WORKSPACE"))
	if err != nil {
		t.Fatal(err)
	}
	w := svc.workspaces[id]
	f := &serviceFixture{gitFixture: gitFixture{root: w.CommonDir, colony: colony, env: os.Environ(), git: svc.git}, store: store, svc: svc, w: w.Workspace, p: w.inputs}
	r, queued := recoveryRequests(t, f)
	child := r
	child.Head, child.RequestID = r.RequestID, uuid.NewV7()
	mustAdmit(t, f, child)
	point := finalizationCrashPoint{Request: r, Queued: queued, Child: child, Generation: currentGeneration(t, f, r)}
	stop := func() {
		if err := json.NewEncoder(os.Stdout).Encode(point); err != nil {
			t.Fatal(err)
		}
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		t.Fatal("parent did not terminate crash helper")
	}
	step := os.Getenv("DWARF_FINALIZATION_STEP")
	if strings.HasPrefix(step, "cancel_") {
		if step == "cancel_before" {
			svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
				if drafts[0].Type == ledger.TypePromotionFailed {
					stop()
				}
			}
		}
		if _, err := svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner}); err != nil {
			t.Fatal(err)
		}
		stop()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if step == "ambiguous_git_exit" || step == "owned_git" {
		svc.git.afterStart = func(cmd *exec.Cmd) {
			if slices.Contains(cmd.Args, "update-ref") && slices.Contains(cmd.Args, "refs/heads/main") {
				if step == "owned_git" {
					point.GitPID = cmd.Process.Pid
					stop()
				}
				cmd.Cancel = func() error { return nil }
				cancel()
			}
		}
	}
	svc.deps.beforeFinalizeStep = func(at string) {
		target := step
		switch step {
		case "torn_intent":
			target = "cas"
		case "torn_acceptance":
			target = "release"
		case "ambiguous_git_exit":
			target = "durability"
		}
		if at == target {
			stop()
		}
	}
	if _, err := svc.finalize(ctx, r.RequestID, point.Generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	t.Fatal("finalizer passed the requested crash boundary")
}

func crashFinalization(t *testing.T, f *serviceFixture, step string, observeGit func(int)) (finalizationCrashPoint, *writerQuiescence) {
	t.Helper()
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperFinalizationCrash$")
	cmd.Env = append(slices.Clone(f.env), "DWARF_FINALIZATION_COLONY="+f.colony, "DWARF_FINALIZATION_WORKSPACE="+f.w.ID.String(), "DWARF_FINALIZATION_STEP="+step)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatalf("crash handshake: %v %s", err, stderr.String())
	}
	var point finalizationCrashPoint
	if err := json.Unmarshal(line, &point); err != nil {
		t.Fatalf("crash handshake %q: %v %s", line, err, stderr.String())
	}
	if observeGit != nil {
		observeGit(point.GitPID)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("crash helper exited successfully")
	}
	waited = true
	if cmd.ProcessState == nil {
		t.Fatal("crash helper has no Wait result")
	}
	return point, &writerQuiescence{WorkspaceID: f.w.ID, PriorCore: &waitedWriter{State: cmd.ProcessState}}
}

// Truncate a closed real-store commit, not invented bytes. Recovery must discard
// the complete prefix line of a multi-record terminal commit as well as its tail.
func tearFinalCommit(t *testing.T, colony string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(colony, "log"))
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			path = filepath.Join(colony, "log", entry.Name())
		}
	}
	if path == "" {
		t.Fatal("no log segment")
	}
	data := readTestFile(t, path)
	// Removing the final newline tears the last record; for the two/four-record
	// endings earlier complete lines remain and must roll back with that commit.
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("fixture lacks complete final commit")
	}
	if err := os.Truncate(path, int64(len(data)-1)); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizationCrashMatrix(t *testing.T) {
	t.Parallel()
	// Each boundary owns its repository, colony and child handshake; only the
	// unregistered Git history is shared by these parallel rows.
	history := newGitFixture(t, "sha1")
	for _, step := range []string{"intent", "torn_intent", "cas", "ambiguous_git_exit", "durability", "acceptance", "torn_acceptance", "release", "cancel_before", "cancel_torn", "cancel_after"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			point, proof := crashFinalization(t, f, step, nil)
			if strings.HasPrefix(step, "torn_") || step == "cancel_torn" {
				tearFinalCommit(t, f.colony)
			}
			var err error
			var recovery ledgerstore.Recovery
			f.store, recovery, err = ledgerstore.Open(f.colony, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			if (strings.HasPrefix(step, "torn_") || step == "cancel_torn") && recovery.TruncatedBytes == 0 {
				t.Fatal("torn final commit was not discarded")
			}
			if strings.HasPrefix(step, "torn_") || step == "cancel_torn" {
				t.Logf("real torn-tail recovery: %+v", recovery)
			}
			f.svc, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
			if err != nil {
				t.Fatal(err)
			}
			want := RequestInterrupted
			switch step {
			case "ambiguous_git_exit", "durability", "acceptance", "torn_acceptance", "release":
				want = RequestAccepted
			case "cancel_after":
				want = RequestCancelled
			}
			main := point.Generation.PredecessorCommit
			if want == RequestAccepted {
				main = point.Generation.DerivedCommit
			}
			assertCanonicalTip(t, f, main)
			assertRecoveryOnce(t, f, point.Request, proof, want)
			for _, row := range []struct {
				request Request
				want    string
			}{{point.Queued, RequestInterrupted}, {point.Child, RequestDependencyFailed}} {
				if row.request.RequestID == point.Child.RequestID && want == RequestAccepted {
					row.want = RequestInterrupted
				}
				state, err := f.svc.RequestPromotion(context.Background(), row.request)
				if err != nil || state.State != row.want {
					t.Fatalf("crash suffix: %+v %v, want %s", state, err, row.want)
				}
			}
			fresh := point.Queued
			fresh.RequestID, fresh.RestartFrom = uuid.NewV7(), point.Queued.RequestID
			assertUnavailable(t, promotionError(context.Background(), f.svc, fresh), "no_validator")
			t.Logf("boundary=%s; actual main=%s; terminal=%s; positive helper Wait PID=%d", step, main, want, proof.PriorCore.State.Pid())
		})
	}
}

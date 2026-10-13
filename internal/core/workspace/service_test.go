package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

const workspaceChildColony = "DWARF_WORKSPACE_CHILD_COLONY"

func TestHelperScratchCrash(t *testing.T) {
	colony := os.Getenv(workspaceChildColony)
	if colony == "" {
		t.Skip("only runs as TestOpenReconciles child")
	}
	store, _, err := ledgerstore.Open(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, _, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{beforeAppend: func() {
		fmt.Println("scratch-ready")
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	workspaceID, err := uuid.Parse(os.Getenv("DWARF_WORKSPACE_CHILD_ID"))
	if err != nil {
		t.Fatal(err)
	}
	if text := os.Getenv("DWARF_WORKSPACE_CHILD_CONTEXT"); text != "" {
		c, err := uuid.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		_, err = admission(service, rootRequest(Scratch{ContextID: c, WorkspaceID: workspaceID}))
		t.Fatalf("parent failed to kill admission before append: %v", err)
	}
	if _, err := service.Scratch(context.Background(), uuid.NewV7(), workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Fatal("parent failed to kill creation before append")
}

func TestOpenReconciles(t *testing.T) {
	t.Run("crashed_creation", func(t *testing.T) {
		f := newServiceFixture(t)
		if err := f.svc.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperScratchCrash$")
		cmd.Env = append(slices.Clone(f.env), workspaceChildColony+"="+f.colony, "DWARF_WORKSPACE_CHILD_ID="+f.w.ID.String())
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
		t.Cleanup(func() { _ = stdin.Close() })
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stopped := false
		t.Cleanup(func() {
			if !stopped {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || line != "scratch-ready\n" {
			t.Fatalf("child readiness: %q %v %s", line, err, stderr.String())
		}
		entries, err := os.ReadDir(filepath.Join(f.colony, "scratch"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("child scratch: %#v %v", entries, err)
		}
		name := entries[0].Name()
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("killed child exited successfully")
		}
		stopped = true
		f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		report := f.reopen(t, serviceConfig())
		if report.OrphansRemoved != 1 || len(report.Pending) != 0 {
			t.Fatalf("orphan report: %#v", report)
		}
		if _, err := os.Stat(filepath.Join(f.colony, "scratch", name)); !os.IsNotExist(err) {
			t.Fatalf("orphan remains: %v", err)
		}
		branch, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, "refs/heads/dwarf-harness/"+name, 40)
		if err != nil || branch != "" {
			t.Fatalf("orphan branch retained: %s %v", branch, err)
		}
		refs, err := f.svc.anchorRefs(context.Background(), f.svc.workspaces[f.w.ID])
		if err != nil || len(refs) != 0 {
			t.Fatalf("orphan anchors: %#v %v", refs, err)
		}
		if f.store.Durable() != 3 {
			t.Fatal("crashed creation left a scratch record")
		}
	})
	t.Run("closed_leftover", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		f.svc.deps.removeAll = func(string) error { return os.ErrPermission }
		if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); !errors.Is(err, ErrRemovalPending) {
			t.Fatalf("failed removal: %v", err)
		}
		report := f.reopen(t, serviceConfig())
		if report.RemovalsCompleted != 1 || len(report.Pending) != 0 {
			t.Fatalf("leftover: %#v", report)
		}
		if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
			t.Fatalf("leftover not removed: %v", err)
		}
	})
	t.Run("closed_refs_without_directory", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
			t.Fatal(err)
		}
		for _, ref := range []string{s.Branch, s.worktree().AnchorRef} {
			if err := f.git.createRef(context.Background(), f.colony, f.p.CommonDir, ref, s.BaseCommit); err != nil {
				t.Fatal(err)
			}
			report := f.reopen(t, serviceConfig())
			if report.RemovalsCompleted != 0 || len(report.Pending) != 0 {
				t.Fatalf("ref-only cleanup: %#v", report)
			}
			if oid, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || oid != "" {
				t.Fatalf("closed ref remains: %s = %q, %v", ref, oid, err)
			}
		}
	})
	t.Run("anchor_restored_tmp_emptied", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		ref := s.worktree().AnchorRef
		gitCommand(t, f.env, f.root, nil, "update-ref", "-d", ref)
		tmp := filepath.Join(f.colony, "workspaces", shortID(f.w.ID), "tmp", "orphan.idx")
		writeTestFile(t, tmp, []byte("incomplete index"))
		report := f.reopen(t, serviceConfig())
		if len(report.Pending) != 0 || len(report.Missing) != 0 {
			t.Fatalf("repair: %#v", report)
		}
		anchor, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40)
		if err != nil || anchor != s.BaseCommit {
			t.Fatalf("anchor: %q %v", anchor, err)
		}
		if _, err := os.Stat(tmp); !os.IsNotExist(err) {
			t.Fatalf("tmp remains: %v", err)
		}
	})
	t.Run("missing_directory", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		if err := os.RemoveAll(s.Dir); err != nil {
			t.Fatal(err)
		}
		report := f.reopen(t, serviceConfig())
		if !slices.Contains(report.Missing, s.ID) {
			t.Fatalf("missing scratch unreported: %#v", report)
		}
		assertUnavailable(t, promotionError(context.Background(), f.svc, rootRequest(s)), "scratch_missing")
		if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOpenGitCallsIndependentOfCompletedScratches(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	openCalls := func() int {
		t.Helper()
		if err := f.svc.Close(); err != nil {
			t.Fatal(err)
		}
		calls := 0
		// Open obtains the controlled environment once per Git invocation;
		// reuse that seam instead of replacing any real Git operation.
		deps := serviceDeps{environ: func() []string {
			calls++
			return f.env
		}}
		var report Reconciliation
		var err error
		f.svc, report, err = openService(context.Background(), f.store, f.colony, serviceConfig(), deps)
		if err != nil {
			t.Fatal(err)
		}
		if report.RemovalsCompleted != 0 || len(report.Pending) != 0 {
			t.Fatalf("completed cleanup was retried: %#v", report)
		}
		return calls
	}
	before := openCalls()
	for range 3 {
		s := mustScratch(t, f, uuid.NewV7())
		if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(s.Dir); !os.IsNotExist(err) {
			t.Fatalf("closed scratch remains: %v", err)
		}
	}
	if after := openCalls(); after != before {
		t.Fatalf("Open Git calls grew with completed scratches: before=%d after=%d", before, after)
	}
}

type uncertainStore struct {
	*ledgerstore.Store
	once    sync.Once
	persist bool
}

func (s *uncertainStore) Append(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
	inject := false
	s.once.Do(func() { inject = true })
	if inject {
		if s.persist {
			if _, err := s.Store.Append(ctx, drafts); err != nil {
				return appender.Committed{}, err
			}
		}
		return appender.Committed{}, appender.ErrUncertain
	}
	return s.Store.Append(ctx, drafts)
}

func TestUncertainAppendPoisons(t *testing.T) {
	t.Parallel()
	for _, persist := range []bool{false, true} {
		t.Run(fmt.Sprintf("persist_%t", persist), func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t)
			f.svc.store = &uncertainStore{Store: f.store, persist: persist}
			c := uuid.NewV7()
			_, err := f.svc.Scratch(context.Background(), c, f.w.ID)
			if !errors.Is(err, appender.ErrUncertain) {
				t.Fatalf("initial failure: %v", err)
			}
			before := f.store.Durable()
			for _, call := range []func() error{
				func() error { _, err := f.svc.Register(context.Background(), f.p); return err },
				func() error { _, err := f.svc.UpdateInputs(context.Background(), f.w.ID, 1, f.p); return err },
				func() error { _, err := f.svc.Scratch(context.Background(), c, f.w.ID); return err },
				func() error { _, err := f.svc.BeginWrite(context.Background(), WriteBinding{}); return err },
				func() error { _, err := f.svc.CloseScratch(context.Background(), Closure{}); return err },
				func() error {
					return promotionError(context.Background(), f.svc, rootRequest(Scratch{ContextID: c, WorkspaceID: f.w.ID}))
				},
			} {
				if err := call(); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("service continued after uncertainty: %v", err)
				}
			}
			if f.store.Durable() != before {
				t.Fatal("poisoned service wrote")
			}
			f.reopen(t, serviceConfig())
			s := mustScratch(t, f, c)
			if _, err := os.Stat(s.Dir); err != nil {
				t.Fatalf("recovery lost acknowledged scratch: %v", err)
			}
			if f.store.Durable() != 4 {
				t.Fatalf("recovery created duplicate or missing scratch: %d", f.store.Durable())
			}
		})
	}
}

func TestCurrentInputsUnavailable(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "future_version"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t)
			s := mustScratch(t, f, uuid.NewV7())
			if kind == "missing" {
				digest := f.w.InputsSHA256.String()
				if err := os.Remove(filepath.Join(f.colony, "bodies", digest[:2], digest)); err != nil {
					t.Fatal(err)
				}
			} else {
				body, err := f.store.ReadBody(context.Background(), f.w.InputsSHA256)
				if err != nil {
					t.Fatal(err)
				}
				body = bytes.Replace(body, []byte(`"version":1`), []byte(`"version":2`), 1)
				d := publishTestBody(t, f, body)
				if _, err := f.store.Append(context.Background(), revisionDrafts(t, f, 2, d, []ledger.Digest{d})); err != nil {
					t.Fatal(err)
				}
			}
			report := f.reopen(t, serviceConfig())
			if len(report.Pending) != 1 || !strings.Contains(report.Pending[0], "inputs_unavailable") {
				t.Fatalf("unavailable input report: %#v", report)
			}
			assertUnavailable(t, promotionError(context.Background(), f.svc, rootRequest(s)), "inputs_unavailable")
			_, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
			assertUnavailable(t, err, "inputs_unavailable")
		})
	}
}

func TestServiceCloseWaitsForWriters(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	release, err := leaseTestWriter(context.Background(), f.svc, s)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	f.svc.deps.afterCloseStart = func() { close(started) }
	done := make(chan error, 1)
	go func() { done <- f.svc.Close() }()
	<-started
	// The write lease is itself an in-progress operation, not merely a call
	// that ended when BeginWrite returned.
	select {
	case err := <-done:
		t.Fatalf("Close passed a live writer: %v", err)
	default:
	}
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("writer completed\n"))
	release()
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Scratch(context.Background(), s.ContextID, f.w.ID); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed service remained usable: %v", err)
	}
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
}

func orphanWorktree(t *testing.T, f *serviceFixture) scratchWorktree {
	t.Helper()
	id := uuid.NewV7()
	w := f.svc.workspaces[f.w.ID]
	wt, err := f.git.createWorktree(context.Background(), f.colony, w.inputs, f.svc.inputsDir(w), filepath.Join(f.colony, "scratch", shortID(id)), id.String(), fixtureMain(t, f.gitFixture))
	if err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestOpenReconcilesUnprovenOrMovedOrphanBranch(t *testing.T) {
	t.Parallel()
	for _, missingAnchor := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_anchor_%t", missingAnchor), func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t)
			orphan := orphanWorktree(t, f)
			want := orphan.BaseCommit
			if missingAnchor {
				gitCommand(t, f.env, f.root, nil, "update-ref", "-d", orphan.AnchorRef)
			} else {
				gitCommand(t, f.env, orphan.Dir, nil, "commit", "--allow-empty", "-m", "preserve orphan history")
				want = strings.TrimSpace(string(gitCommand(t, f.env, orphan.Dir, nil, "rev-parse", "HEAD")))
			}
			report := f.reopen(t, serviceConfig())
			if report.OrphansRemoved != 1 || !strings.Contains(strings.Join(report.Pending, "\n"), orphan.Branch) {
				t.Fatalf("retained branch unreported: %#v", report)
			}
			got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, orphan.Branch, 40)
			if err != nil || got != want {
				t.Fatalf("orphan branch lost: %q %v", got, err)
			}
			got, err = f.git.readRef(context.Background(), f.colony, f.p.CommonDir, orphan.AnchorRef, 40)
			if err != nil || got != "" {
				t.Fatalf("orphan anchor retained: %q %v", got, err)
			}
			if _, err := os.Stat(orphan.Dir); !os.IsNotExist(err) {
				t.Fatalf("orphan directory remains: %v", err)
			}
		})
	}
}

func TestOpenSweepsUnownedRefs(t *testing.T) {
	t.Run("killed_admission", func(t *testing.T) {
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		writeTestFile(t, filepath.Join(s.Dir, "crashed.txt"), []byte("unacknowledged capture"))
		before := f.store.Durable()
		if err := f.svc.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperScratchCrash$")
		cmd.Env = append(slices.Clone(f.env), workspaceChildColony+"="+f.colony, "DWARF_WORKSPACE_CHILD_ID="+f.w.ID.String(), "DWARF_WORKSPACE_CHILD_CONTEXT="+s.ContextID.String())
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
		t.Cleanup(func() { _ = stdin.Close() })
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stopped := false
		t.Cleanup(func() {
			if !stopped {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || line != "scratch-ready\n" {
			t.Fatalf("admission child: %q %v %s", line, err, stderr.String())
		}
		refs := string(gitCommand(t, f.env, f.root, nil, "for-each-ref", "--format=%(refname)", "refs/dwarf-harness/captures/"))
		if refs == "" {
			t.Fatal("child did not seal before append")
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("killed child succeeded")
		}
		stopped = true
		f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		report := f.reopen(t, serviceConfig())
		if len(report.Pending) != 0 || f.store.Durable() != before {
			t.Fatalf("crash reconciliation: %+v", report)
		}
		refs = string(gitCommand(t, f.env, f.root, nil, "for-each-ref", "--format=%(refname)", "refs/dwarf-harness/captures/"))
		if refs != "" {
			t.Fatalf("unowned capture retained: %s", refs)
		}
	})
	t.Run("recorded_and_failed_release", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		r := rootRequest(s)
		writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("local\ntwo\n"))
		mustAdmit(t, f, r)
		a := admittedRecord(t, f, r.RequestID)
		captureRef := "refs/dwarf-harness/captures/" + a.CaptureID.String()
		f.reopen(t, serviceConfig())
		if got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, captureRef, 40); err != nil || got != a.SealedTree {
			t.Fatalf("admitted retention lost: %q %v", got, err)
		}
		advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
		mustIntegrate(t, f)
		if err := f.git.createRef(context.Background(), f.colony, f.p.CommonDir, captureRef, a.SealedTree); err != nil {
			t.Fatal(err)
		}
		stray := "refs/dwarf-harness/generations/" + uuid.NewV7().String()
		if err := f.git.createRef(context.Background(), f.colony, f.p.CommonDir, stray, s.BaseCommit); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 {
			t.Fatalf("release replay: %+v", report)
		}
		for _, ref := range []string{captureRef, stray} {
			if got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || got != "" {
				t.Fatalf("unowned ref %s remains: %q %v", ref, got, err)
			}
		}
		if f.store.Durable() != before {
			t.Fatal("sweep wrote records")
		}
	})
}

func TestOpenRestoresRetainedRefs(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "different", "pruned"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t)
			s := mustScratch(t, f, uuid.NewV7())
			r := rootRequest(s)
			writeTestFile(t, filepath.Join(s.Dir, "retained.txt"), []byte("retained submission only"))
			want := mustAdmit(t, f, r)
			mustIntegrate(t, f)
			a := admittedRecord(t, f, r.RequestID)
			g := generations(t, f, r.RequestID)[0]
			refs := map[string]string{"refs/dwarf-harness/captures/" + a.CaptureID.String(): a.SealedTree, "refs/dwarf-harness/generations/" + g.GenerationID.String(): g.DerivedCommit}
			for ref, oid := range refs {
				gitCommand(t, f.env, f.root, nil, "update-ref", "-d", "--end-of-options", ref, oid)
				if mode == "different" {
					if err := f.git.createRef(context.Background(), f.colony, f.p.CommonDir, ref, s.BaseCommit); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "pruned" {
				gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
			}
			before := f.store.Durable()
			report := f.reopen(t, serviceConfig())
			if mode == "missing" {
				if len(report.Pending) != 0 {
					t.Fatalf("restore: %+v", report)
				}
				for ref, oid := range refs {
					if got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || got != oid {
						t.Fatalf("ref not restored: %q %v", got, err)
					}
				}
			} else {
				if len(report.Pending) != 2 || !strings.Contains(strings.Join(report.Pending, "\n"), "capture_missing "+r.RequestID.String()) || !strings.Contains(strings.Join(report.Pending, "\n"), "generation_missing "+g.GenerationID.String()) {
					t.Fatalf("missing retained objects not reported: %+v", report)
				}
				if mode == "different" {
					for ref := range refs {
						if got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || got != s.BaseCommit {
							t.Fatalf("different ref overwritten: %q %v", got, err)
						}
					}
				}
			}
			got, err := f.svc.RequestPromotion(context.Background(), r)
			if err != nil || got != want || f.store.Durable() != before {
				t.Fatalf("replay changed request: %+v %v", got, err)
			}
		})
	}
}

func TestOpenRebuildsAdmissionState(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	for range 4 {
		mustAdmit(t, f, r)
		r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	}
	mustIntegrate(t, f)
	before := snapshotRequestEffects(t, f)
	// A lowered policy cannot invalidate a previously valid ledger.
	cfg := serviceConfig()
	cfg.Capacity.CheckpointsPerPair = 1
	cfg.Capacity.GenerationWindow = 1
	report := f.reopen(t, cfg)
	if len(report.Pending) != 0 {
		t.Fatalf("replay: %+v", report)
	}
	// Open empties tmp, but creates no Git objects and changes no retained ref.
	after := snapshotRequestEffects(t, f)
	if after.Durable != before.Durable || after.Capture.Objects != before.Capture.Objects || after.Capture.Refs != before.Capture.Refs {
		t.Fatal("Open executed work")
	}
	assertBusy(t, promotionError(context.Background(), f.svc, r), "checkpoint_slots")
	if err := promotionError(context.Background(), f.svc, rootRequest(s)); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("replayed head lost: %v", err)
	}
	before = snapshotRequestEffects(t, f)
	mustIntegrate(t, f)
	assertRequestEffects(t, f, before)
}

func TestRequestPoisonReplay(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"admission", "generation"} {
		for _, persist := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/persist_%t", phase, persist), func(t *testing.T) {
				t.Parallel()
				f := newServiceFixture(t)
				cfg := serviceConfig()
				cfg.Capacity.CheckpointsPerPair = 1
				f.reopen(t, cfg)
				s := mustScratch(t, f, uuid.NewV7())
				r := rootRequest(s)
				if phase == "generation" {
					mustAdmit(t, f, r)
				}
				f.svc.store = &uncertainStore{Store: f.store, persist: persist}
				var err error
				if phase == "admission" {
					_, err = admission(f.svc, r)
				} else {
					err = f.svc.integrate(context.Background(), f.w.ID)
				}
				if !errors.Is(err, ErrPoisoned) || !errors.Is(err, appender.ErrUncertain) {
					t.Fatalf("uncertain %s: %v", phase, err)
				}
				prefix := "refs/dwarf-harness/captures/"
				if phase == "generation" {
					prefix = "refs/dwarf-harness/generations/"
				}
				refs, err := f.svc.listRefs(context.Background(), f.svc.workspaces[f.w.ID], prefix)
				if err != nil || len(refs) != 1 {
					t.Fatalf("uncertainty lost retained ref: %v %v", refs, err)
				}
				before := snapshotRequestEffects(t, f)
				if _, err := f.svc.RequestPromotion(context.Background(), r); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned replay: %v", err)
				}
				if err := f.svc.integrate(context.Background(), f.w.ID); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned integration: %v", err)
				}
				assertRequestEffects(t, f, before)
				if report := f.reopen(t, cfg); len(report.Pending) != 0 {
					t.Fatalf("recovery: %+v", report)
				}
				after, err := f.svc.listRefs(context.Background(), f.svc.workspaces[f.w.ID], prefix)
				want := 0
				if persist {
					want = 1
				}
				if err != nil || len(after) != want {
					t.Fatalf("record-based recovery refs: %v %v", after, err)
				}
				if phase == "admission" && !persist {
					assertUnavailable(t, promotionError(context.Background(), f.svc, r), "no_validator")
				} else {
					if state, err := f.svc.RequestPromotion(context.Background(), r); err != nil || state.State != RequestPending {
						t.Fatalf("recovered identity: %+v %v", state, err)
					}
					next := rootRequest(s)
					next.Head = r.RequestID
					assertBusy(t, promotionError(context.Background(), f.svc, next), "checkpoint_slots")
				}
			})
		}
	}
}

func TestWriterCloseAndServiceClose(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	b := testWriteBinding(task)
	writer := mustWriter(t, f, b)
	for _, forged := range []*Writer{nil, {}, {service: f.svc}} {
		if _, err := forged.Lease(ctx); !errors.Is(err, ErrInvalid) {
			t.Fatalf("forged lease: %v", err)
		}
		if _, err := forged.CommitObserved(ctx); !errors.Is(err, ErrInvalid) {
			t.Fatalf("forged observation: %v", err)
		}
		if forged.Scratch() != (Scratch{}) {
			t.Fatal("forged handle exposed directory")
		}
		forged.Close()
	}
	copied := *writer
	if _, err := copied.Lease(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("copied token became authority: %v", err)
	}
	copied.Close()
	release, err := writer.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	writer.Close()
	if writer.Scratch() != (Scratch{}) {
		t.Fatal("closed handle exposes directory")
	}
	if _, err := writer.Lease(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed lease: %v", err)
	}
	if _, err := writer.CommitObserved(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed observation: %v", err)
	}
	if _, err := f.svc.BeginWrite(ctx, b); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed live ID revived: %v", err)
	}
	b.WriterID = uuid.NewV7()
	_, err = f.svc.BeginWrite(ctx, b)
	assertBusy(t, err, "context_busy")
	release()
	release()
	writer = mustWriter(t, f, b)
	started := make(chan struct{})
	f.svc.deps.afterCloseStart = func() { close(started) }
	done := make(chan error, 1)
	go func() { done <- f.svc.Close() }()
	<-started
	if _, err := f.svc.BeginWrite(ctx, testWriteBinding(task)); !errors.Is(err, ErrClosed) {
		t.Fatalf("shutdown accepted new routing: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown passed logical writer: %v", err)
	default:
	}
	release, err = writer.Lease(ctx)
	if err != nil {
		t.Fatalf("owned writer could not finish during shutdown: %v", err)
	}
	writeTestFile(t, filepath.Join(task.Dir, "finished.txt"), []byte("finished while closing\n"))
	release()
	observation := mustObserve(t, writer)
	if got := string(gitCommand(t, f.env, f.root, nil, "show", observation.Commit+":finished.txt")); got != "finished while closing\n" {
		t.Fatal("shutdown lost the writer's observation")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cancelled_append", "uncertain_absent", "uncertain_committed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := serviceFromHistory(t, f.gitFixture)
			task := mustScratch(t, fixture, uuid.NewV7())
			writer := mustWriter(t, fixture, testWriteBinding(task))
			writeTestFile(t, filepath.Join(task.Dir, "observed.txt"), []byte("survives uncertainty\n"))
			before := fixture.store.Durable()
			observeCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if mode == "cancelled_append" {
				fixture.svc.deps.beforeAppend = cancel
			} else {
				fixture.svc.store = &uncertainStore{Store: fixture.store, persist: mode == "uncertain_committed"}
			}
			result, err := writer.CommitObserved(observeCtx)
			if result != (Observation{}) {
				t.Fatal("unacknowledged append returned an observation")
			}
			if mode == "cancelled_append" {
				if !errors.Is(err, context.Canceled) || fixture.store.Durable() != before {
					t.Fatalf("definite refusal changed the log: %v", err)
				}
				release, err := writer.Lease(ctx)
				if err != nil {
					t.Fatalf("refused observation did not thaw: %v", err)
				}
				release()
			} else {
				if !errors.Is(err, ErrPoisoned) || !errors.Is(err, appender.ErrUncertain) {
					t.Fatalf("uncertain append did not poison: %v", err)
				}
				if writer.Scratch() != (Scratch{}) {
					t.Fatal("poisoned writer exposed directory")
				}
				if _, err := writer.Lease(ctx); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned lease: %v", err)
				}
				if _, err := writer.CommitObserved(ctx); !errors.Is(err, ErrPoisoned) {
					t.Fatalf("poisoned observation: %v", err)
				}
			}
			writer.Close()
			writer.Close()
			refs, err := fixture.svc.listRefs(ctx, fixture.svc.workspaces[fixture.w.ID], "refs/dwarf-harness/scratch-commits/")
			wantRefs := 1
			if mode == "cancelled_append" {
				wantRefs = 0
			}
			if err != nil || len(refs) != wantRefs {
				t.Fatalf("pre-recovery retention: %v %v", refs, err)
			}
			branch, err := fixture.git.readRef(ctx, fixture.colony, fixture.p.CommonDir, task.Branch, 40)
			if err != nil || branch != task.BaseCommit {
				t.Fatalf("unacknowledged branch moved: %s %v", branch, err)
			}
			report := fixture.reopen(t, serviceConfig())
			if len(report.Pending) != 0 {
				t.Fatal(report.Pending)
			}
			projected := fixture.svc.scratches[task.ID]
			refs, err = fixture.svc.listRefs(ctx, fixture.svc.workspaces[fixture.w.ID], "refs/dwarf-harness/scratch-commits/")
			if mode == "uncertain_committed" {
				if fixture.store.Durable() != before+2 || projected.ObservationID == (uuid.UUID{}) || len(refs) != 1 || err != nil {
					t.Fatalf("recovery lost committed observation: %+v %v %v", projected, refs, err)
				}
			} else if fixture.store.Durable() != before || projected.ObservationID != (uuid.UUID{}) || len(refs) != 0 || err != nil {
				t.Fatalf("recovery invented an observation: %+v %v %v", projected, refs, err)
			}
			branch, err = fixture.git.readRef(ctx, fixture.colony, fixture.p.CommonDir, task.Branch, 40)
			if err != nil || branch != projected.TipCommit {
				t.Fatalf("recovery failed tip projection: %s %v", branch, err)
			}
		})
	}
}

func TestWriterObservationWaitsAndThaws(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	task := mustScratch(t, f, uuid.NewV7())
	writer := mustWriter(t, f, testWriteBinding(task))
	release, err := writer.Lease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frozen := make(chan struct{})
	f.svc.deps.afterWriterFreeze = func() { close(frozen) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	before := f.store.Durable()
	go func() { _, err := writer.CommitObserved(ctx); done <- err }()
	<-frozen
	cancelled, cancelLease := context.WithCancel(context.Background())
	cancelLease()
	if _, err := writer.Lease(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("lease escaped observing writer: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || f.store.Durable() != before {
		t.Fatalf("cancelled observation acknowledged work: %v", err)
	}
	release()
	f.svc.deps.afterWriterFreeze = nil
	lease, err := writer.Lease(context.Background())
	if err != nil {
		t.Fatalf("failed observation did not thaw: %v", err)
	}
	writeTestFile(t, filepath.Join(task.Dir, "later.txt"), []byte("later\n"))
	lease()
	if _, err := writer.CommitObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Lease(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("successful observation allowed another edit: %v", err)
	}
}

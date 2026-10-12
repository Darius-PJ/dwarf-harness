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
		defer cancel()
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
		defer stdin.Close()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stopped := false
		defer func() {
			if !stopped {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}()
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
	t.Run("capture_owner_retains_ref", func(t *testing.T) {
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		writeTestFile(t, filepath.Join(s.Dir, "captured.txt"), []byte("sealed content"))
		capture, err := f.capture(s, sealCapture)
		if err != nil {
			t.Fatal(err)
		}
		defer gitCommand(t, f.env, f.root, nil, "update-ref", "-d", capture.RetentionRef, capture.Tree)
		report := f.reopen(t, serviceConfig())
		if len(report.Pending) != 0 {
			t.Fatalf("capture reopen: %#v", report)
		}
		if tree, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, capture.RetentionRef, 40); err != nil || tree != capture.Tree {
			t.Fatalf("Open released the owner's capture: tree=%q want=%q err=%v", tree, capture.Tree, err)
		}
	})
	t.Run("anchor_restored_tmp_emptied", func(t *testing.T) {
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
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		if err := os.RemoveAll(s.Dir); err != nil {
			t.Fatal(err)
		}
		report := f.reopen(t, serviceConfig())
		if !slices.Contains(report.Missing, s.ID) {
			t.Fatalf("missing scratch unreported: %#v", report)
		}
		assertUnavailable(t, f.svc.RequestPromotion(context.Background(), s.ContextID, f.w.ID), "scratch_missing")
		if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOpenGitCallsIndependentOfCompletedScratches(t *testing.T) {
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
	for _, persist := range []bool{false, true} {
		t.Run(fmt.Sprintf("persist_%t", persist), func(t *testing.T) {
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
				func() error { _, err := f.svc.BeginWrite(context.Background(), uuid.NewV7()); return err },
				func() error { _, err := f.svc.CloseScratch(context.Background(), Closure{}); return err },
				func() error { return f.svc.RequestPromotion(context.Background(), c, f.w.ID) },
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
	for _, kind := range []string{"missing", "future_version"} {
		t.Run(kind, func(t *testing.T) {
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
			assertUnavailable(t, f.svc.RequestPromotion(context.Background(), s.ContextID, f.w.ID), "inputs_unavailable")
			_, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
			assertUnavailable(t, err, "inputs_unavailable")
		})
	}
}

func TestServiceCloseWaitsForWriters(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	release, err := f.svc.BeginWrite(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.svc.Close() }()
	// The write lease is itself an in-progress operation, not merely a call
	// that ended when BeginWrite returned.
	select {
	case err := <-done:
		t.Fatalf("Close passed a live writer: %v", err)
	case <-time.After(20 * time.Millisecond):
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
	wt, err := f.git.createWorktree(context.Background(), f.colony, w.inputs, f.svc.inputsDir(w), filepath.Join(f.colony, "scratch", shortID(id)), id.String())
	if err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestOpenReconcilesUnprovenOrMovedOrphanBranch(t *testing.T) {
	for _, missingAnchor := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_anchor_%t", missingAnchor), func(t *testing.T) {
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

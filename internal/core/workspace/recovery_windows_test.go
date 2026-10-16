package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func lockNextLedgerAppend(t *testing.T, dir string) func() {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			path = filepath.Join(dir, entry.Name())
		}
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	offset := uint64(info.Size())
	overlap := syscall.Overlapped{Offset: uint32(offset), OffsetHigh: uint32(offset >> 32)}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	lock := kernel.NewProc("LockFileEx")
	unlock := kernel.NewProc("UnlockFileEx")
	ok, _, callErr := lock.Call(file.Fd(), 3, 0, 1<<30, 0, uintptr(unsafe.Pointer(&overlap)))
	if ok == 0 {
		file.Close()
		t.Fatal(callErr)
	}
	t.Logf("native LockFileEx append range offset=%d length=%d (not byte zero)", offset, 1<<30)
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		ok, _, err := unlock.Call(file.Fd(), 0, 1<<30, 0, uintptr(unsafe.Pointer(&overlap)))
		if ok == 0 {
			t.Error(err)
		}
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return release
}

func TestWindowsFinalizationUncertainAppend(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, gate := range []string{ledger.TypeFinalizationIntended, ledger.TypePromotionAccepted} {
		t.Run(gate, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			r, _ := recoveryRequests(t, f)
			g := currentGeneration(t, f, r)
			updates := countMainUpdates(f.svc.git)
			var release func()
			f.svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
				if drafts[0].Type == gate {
					release = lockNextLedgerAppend(t, f.store.LogDir())
				}
			}
			_, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID)
			if release == nil {
				t.Fatal("append gate not reached")
			}
			release() // release the real cause before Open, including its readers
			if !errors.Is(err, appender.ErrUncertain) || !errors.Is(err, ErrPoisoned) {
				t.Fatalf("native append did not poison: %v", err)
			}
			before := f.store.Durable()
			if err := f.svc.integrate(context.Background(), f.w.ID); !errors.Is(err, ErrPoisoned) {
				t.Fatal(err)
			}
			if gate == ledger.TypeFinalizationIntended && *updates != 0 {
				t.Fatal("uncertain intent allowed CAS")
			}
			if f.store.Durable() != before {
				t.Fatal("poisoned service wrote")
			}
			proof, _ := reopenRecovery(t, f)
			if f.store.Durable() != before {
				t.Fatal("native absent commit appeared after reopen")
			}
			want := RequestInterrupted
			if gate == ledger.TypePromotionAccepted {
				want = RequestAccepted
			}
			assertRecoveryOnce(t, f, r, proof, want)
			t.Log("native failed append/absent commit, distinct from persist=true response loss")
		})
	}
}

// Pinned Windows Git refuses GIT_DIR longer than 220 characters, including the
// extended-length form. No long-path row is reachable; durable tests SyncDir.
func TestWindowsFinalizationSharingRecovery(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, directory := range []bool{false, true} {
		name := "held_ref"
		if directory {
			name = "held_directory"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			r, _ := recoveryRequests(t, f)
			holdFinalizationIntent(t, f, r, "durability")
			proof, _ := reopenRecovery(t, f)
			path := filepath.Join(f.p.CommonDir, "refs", "heads")
			if !directory {
				path = filepath.Join(path, "main")
			}
			release := holdCanonicalPath(t, path, directory)
			if _, err := f.svc.reconcilePromotions(context.Background(), f.w.ID, proof); err == nil {
				t.Error("sharing violation declared durable acceptance")
			}
			release()
			state, err := f.svc.RequestPromotion(context.Background(), r)
			if err != nil || state.State != RequestFinalizing {
				t.Fatalf("sharing failure lost intent: %+v %v", state, err)
			}
			assertRecoveryOnce(t, f, r, proof, RequestAccepted)
		})
	}
}

func TestWindowsOwnedGitExitBeforeRecovery(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	r, _ := recoveryRequests(t, f)
	g := holdFinalizationIntent(t, f, r, "cas")
	reopenRecovery(t, f)
	_, waitCore := ownedRecoveryWriter(t)
	coreState := waitCore()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, "git", "update-ref", "--stdin")
	cmd.Dir, cmd.Env = f.root, f.env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = stdin.Close()
			_ = cmd.Wait()
		}
	})
	if _, err := io.WriteString(stdin, "start\nupdate refs/heads/main "+g.DerivedCommit+" "+g.PredecessorCommit+"\nprepare\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	for _, want := range []string{"start: ok\n", "prepare: ok\n"} {
		if got, err := reader.ReadString('\n'); err != nil || got != want {
			t.Fatalf("owned Git handshake: %q %v", got, err)
		}
	}
	proof := &writerQuiescence{WorkspaceID: f.w.ID, PriorCore: &waitedWriter{State: coreState}, PriorGit: []waitedWriter{{State: cmd.ProcessState}}}
	_, err = f.svc.reconcilePromotions(context.Background(), f.w.ID, proof)
	assertUnavailable(t, err, "writer_unfenced")
	// The dead Core and the newly owned colony lock did not fence this real Git
	// transaction. Only its abort and completed Wait permit old-tip inference.
	if _, err := io.WriteString(stdin, "abort\n"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("owned Git Wait: %v %s", err, stderr.String())
	}
	waited = true
	proof.PriorGit[0].State = cmd.ProcessState
	assertRecoveryOnce(t, f, r, proof, RequestInterrupted)
}

func TestWindowsOwnedGitCrashBeforeRecovery(t *testing.T) {
	// One real helper crash, with the canonical Git handle opened while its
	// parent's exec.Cmd still owns a handle, even if Git has already exited.
	f := newServiceFixture(t)
	var git *os.Process
	point, proof := crashFinalization(t, f, "owned_git", func(pid int) {
		if pid <= 0 {
			t.Fatal("missing owned canonical Git PID")
		}
		var err error
		git, err = os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { _ = git.Release() })
	var err error
	f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.svc, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
	if err != nil {
		t.Fatal(err)
	}
	proof.PriorGit = []waitedWriter{{}}
	_, err = f.svc.reconcilePromotions(context.Background(), f.w.ID, proof)
	assertUnavailable(t, err, "writer_unfenced")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type waited struct {
		state *os.ProcessState
		err   error
	}
	done := make(chan waited, 1)
	go func() { state, err := git.Wait(); done <- waited{state, err} }()
	var result waited
	select {
	case result = <-done:
	case <-ctx.Done():
		_ = git.Kill()
		<-done
		t.Fatal("owned canonical Git did not exit before the context deadline")
	}
	if result.err != nil || result.state == nil || result.state.Pid() != point.GitPID {
		t.Fatalf("owned canonical Git Wait: %+v %v", result.state, result.err)
	}
	proof.PriorGit[0].State = result.state
	main := fixtureMain(t, f.gitFixture)
	want := RequestInterrupted
	switch main {
	case point.Generation.PredecessorCommit:
	case point.Generation.DerivedCommit:
		want = RequestAccepted
	default:
		t.Fatalf("owned Git left unexpected main %s", main)
	}
	assertRecoveryOnce(t, f, point.Request, proof, want)
}

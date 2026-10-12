package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScratchOnePerPair(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	c1 := uuid.NewV7()
	first := mustScratch(t, f, c1)
	second := mustScratch(t, f, c1)
	if first != second {
		t.Fatalf("same pair changed scratch: %#v %#v", first, second)
	}
	other := mustScratch(t, f, uuid.NewV7())
	if first.Dir == other.Dir || first.Branch == other.Branch {
		t.Fatal("different contexts shared a scratch")
	}
	before := f.store.Durable()
	c3 := uuid.NewV7()
	start := make(chan struct{})
	results := make(chan Scratch, 16)
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			s, err := f.svc.Scratch(ctx, c3, f.w.ID)
			results <- s
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var want Scratch
	for got := range results {
		if want.ID == (uuid.UUID{}) {
			want = got
		}
		if got != want {
			t.Fatalf("concurrent first uses differed: %#v %#v", want, got)
		}
	}
	if f.store.Durable() != before+1 {
		t.Fatal("concurrent first uses wrote more than one record")
	}
	entries, err := f.git.listWorktrees(ctx, f.colony, f.p.CommonDir, oidLength(f.p.ObjectFormat))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Branch, "refs/heads/dwarf-harness/") {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("private worktrees = %d, want 3", count)
	}
}

func TestScratchNamesNeverReused(t *testing.T) {
	f := newServiceFixture(t)
	old := mustScratch(t, f, uuid.NewV7())
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(old)); err != nil {
		t.Fatal(err)
	}
	fresh := uuid.NewV7()
	calls := 0
	f.svc.deps.newID = func() uuid.UUID {
		calls++
		if calls == 1 {
			return old.ID
		}
		return fresh
	}
	got := mustScratch(t, f, old.ContextID)
	if got.ID != fresh || got.Dir == old.Dir || got.Branch == old.Branch || calls != 2 {
		t.Fatalf("closed name reused: %#v (mint calls %d)", got, calls)
	}
	f.svc.deps.newID = func() uuid.UUID { return old.ID }
	before := f.store.Durable()
	if _, err := f.svc.Scratch(context.Background(), uuid.NewV7(), f.w.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("exhausted names: %v", err)
	}
	if f.store.Durable() != before {
		t.Fatal("name exhaustion wrote a record")
	}
}

func TestCloseScratchRefusesUnrepresentedEdits(t *testing.T) {
	for _, kind := range []string{"edit", "new", "deletion", "unsupported_filter"} {
		t.Run(kind, func(t *testing.T) {
			f := newServiceFixture(t)
			s := mustScratch(t, f, uuid.NewV7())
			path := filepath.Join(s.Dir, "base.txt")
			switch kind {
			case "edit":
				writeTestFile(t, path, []byte("edited\n"))
			case "new":
				path = filepath.Join(s.Dir, "new.txt")
				writeTestFile(t, path, []byte("new\n"))
			case "deletion":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "unsupported_filter":
				writeTestFile(t, filepath.Join(s.Dir, ".gitattributes"), []byte("base.txt filter=missing\n"))
			}
			before := f.store.Durable()
			_, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
			if kind == "unsupported_filter" {
				assertRefusal(t, err, kind)
			} else if !errors.Is(err, ErrUnrepresentedEdits) {
				t.Fatalf("closure discarded %s: %v", kind, err)
			}
			if f.store.Durable() != before {
				t.Fatal("refused closure wrote a record")
			}
			if _, err := os.Stat(s.Dir); err != nil {
				t.Fatalf("refusal removed scratch: %v", err)
			}
			if kind == "edit" && string(readTestFile(t, path)) != "edited\n" {
				t.Fatal("refusal changed edited bytes")
			}
			if kind == "new" && string(readTestFile(t, path)) != "new\n" {
				t.Fatal("refusal changed new bytes")
			}
			if kind == "deletion" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("refusal reversed deletion: %v", err)
				}
			}
		})
	}
	for _, moved := range []bool{false, true} {
		name := "branch_at_base"
		if moved {
			name = "moved_branch_equal_tree"
		}
		t.Run(name, func(t *testing.T) {
			f := newServiceFixture(t)
			s := mustScratch(t, f, uuid.NewV7())
			writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("edited\n"))
			writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("one\ntwo\n"))
			var commit string
			if moved {
				gitCommand(t, f.env, s.Dir, nil, "commit", "--allow-empty", "-m", "same represented tree")
				commit = strings.TrimSpace(string(gitCommand(t, f.env, s.Dir, nil, "rev-parse", "HEAD")))
			}
			if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
				t.Fatalf("closed scratch remains: %v", err)
			}
			branch, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, s.Branch, 40)
			if err != nil || branch != commit {
				t.Fatalf("branch = %q, want %q: %v", branch, commit, err)
			}
			anchor, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, "refs/dwarf-harness/anchors/"+s.ID.String(), 40)
			if err != nil || anchor != "" {
				t.Fatalf("anchor retained: %q %v", anchor, err)
			}
		})
	}
	t.Run("ignored_entries_unit", func(t *testing.T) {
		f := newServiceFixture(t)
		p := f.p
		p.LocalExcludes = HostFile{Present: true, Bytes: []byte("build/\n*.tmp\n")}
		if _, err := f.svc.UpdateInputs(context.Background(), f.w.ID, 1, p); err != nil {
			t.Fatal(err)
		}
		s := mustScratch(t, f, uuid.NewV7())
		writeTestFile(t, filepath.Join(s.Dir, "build", "one"), []byte("ignored"))
		writeTestFile(t, filepath.Join(s.Dir, "build", "two"), []byte("ignored"))
		writeTestFile(t, filepath.Join(s.Dir, "one.tmp"), []byte("ignored"))
		result, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
		if err != nil || result.IgnoredEntries != 2 {
			t.Fatalf("ignored listing count = %d, want 2: %v", result.IgnoredEntries, err)
		}
		if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
			t.Fatalf("ignored-only scratch remains: %v", err)
		}
	})
}

func TestCloseScratchAuthority(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	before := f.store.Durable()
	for _, actor := range []ledger.Actor{ledger.ActorAgent, ledger.ActorCore} {
		_, err := f.svc.CloseScratch(context.Background(), Closure{ScratchID: s.ID, Actor: actor, ContextID: uuid.NewV7(), Reason: "abandoned"})
		if !errors.Is(err, ErrNotBound) || f.store.Durable() != before {
			t.Fatalf("unbound closure: %v", err)
		}
	}
	if _, err := f.svc.CloseScratch(context.Background(), Closure{ScratchID: s.ID, Actor: ledger.ActorAgent, ContextID: s.ContextID, Reason: "future_reason"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("producer wrote unknown reason: %v", err)
	}
	if _, err := f.svc.CloseScratch(context.Background(), Closure{ScratchID: s.ID, Actor: ledger.ActorAgent, ContextID: s.ContextID, Reason: "abandoned"}); err != nil {
		t.Fatal(err)
	}
	s = mustScratch(t, f, uuid.NewV7())
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
		t.Fatal(err)
	}
}

func TestCloseScratchBusy(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	release, err := f.svc.BeginWrite(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	before := f.store.Durable()
	effects := snapshotCaptureSideEffects(t, f.gitFixture, f.p)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = f.svc.CloseScratch(ctx, ownerClosure(s))
	assertBusy(t, err, "scratch_busy")
	if f.store.Durable() != before {
		t.Fatal("busy closure wrote a record")
	}
	assertCaptureSideEffects(t, f.gitFixture, f.p, effects)
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("writer continues\n"))
}

func TestScratchWaitsOnlyForItsPair(t *testing.T) {
	f := newServiceFixture(t)
	c := uuid.NewV7()
	entered, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f.svc.deps.beforeAppend = func() {
		if calls.Add(1) == 1 {
			close(entered)
			<-resume
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.Scratch(context.Background(), c, f.w.ID)
		done <- err
	}()
	<-entered
	var release sync.Once
	defer release.Do(func() { close(resume) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := f.svc.Scratch(ctx, c, f.w.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same pair did not wait within context: %v", err)
	}
	otherCtx, otherCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer otherCancel()
	if _, err := f.svc.Scratch(otherCtx, uuid.NewV7(), f.w.ID); err != nil {
		t.Fatalf("unrelated pair waited for first append: %v", err)
	}
	release.Do(func() { close(resume) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAnchorRemovalPreservesMovedRef(t *testing.T) {
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	ref := s.worktree().AnchorRef
	f.svc.git.beforeAnchorDelete = func() {
		gitCommand(t, f.env, f.root, nil, "update-ref", ref, s.AnchorTree)
	}
	_, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
	if !errors.Is(err, ErrRemovalPending) {
		t.Fatalf("anchor race was not pending: %v", err)
	}
	got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40)
	if err != nil || got != s.AnchorTree {
		t.Fatalf("racing anchor was deleted: %q %v", got, err)
	}
	f.svc.git.beforeAnchorDelete = nil
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
		t.Fatal(err)
	}
	got, err = f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40)
	if err != nil || got != "" {
		t.Fatalf("anchor retry failed: %q %v", got, err)
	}
}

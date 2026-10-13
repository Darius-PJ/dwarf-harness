package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"uuid"
)

// Deny deletion/replacement, but permit the host to observe the retained bytes.
func holdMaterializeFile(t *testing.T, path string) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	release := func() {
		if !closed {
			if err := syscall.CloseHandle(handle); err != nil {
				t.Error(err)
			}
			closed = true
		}
	}
	t.Cleanup(release)
	return release
}

func TestMaterializeLockedModifiedFile(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	f := h.fixture(t, false)
	guard := materializeGuard(t, f)
	ageMaterializeFiles(t, f.s.Worktree.Dir)
	release := holdMaterializeFile(t, filepath.Join(f.s.Worktree.Dir, "modified.txt"))
	index := readTestFile(t, filepath.Join(materializeAdmin(t, f), "index"))
	id := uuid.NewV7()
	err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, id, guard)
	var gitErr *GitError
	if !errors.As(err, &gitErr) || gitErr.Exit == 0 {
		t.Fatalf("locked modification reported success: %v", err)
	}
	if string(readTestFile(t, filepath.Join(f.s.Worktree.Dir, "modified.txt"))) != "old\n" || string(readTestFile(t, filepath.Join(f.s.Worktree.Dir, "added.txt"))) != "added\n" {
		t.Fatal("partial mutation did not retain old/new state")
	}
	if _, err := os.Stat(filepath.Join(f.s.Worktree.Dir, "deleted.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected partial deletion: %v", err)
	}
	branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, len(h.old))
	if err != nil || branch != h.old || !bytes.Equal(index, readTestFile(t, filepath.Join(materializeAdmin(t, f), "index"))) {
		t.Fatalf("partial checkout installed index/ref: %s %v", branch, err)
	}
	release()
	actual := observedMaterializeTree(t, f, h, guard)
	if actual == h.oldTree || actual == h.targetTree {
		t.Fatal("fixture did not exercise intermediate O")
	}
	if err := f.engine.materialize(context.Background(), f.s, actual, h.targetTree, h.old, h.target, id, guard); err != nil {
		t.Fatal(err)
	}
	assertMaterialized(t, f, h, false)
}

func TestMaterializeLockedDeletedFile(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	f := h.fixture(t, false)
	guard := materializeGuard(t, f)
	ageMaterializeFiles(t, f.s.Worktree.Dir)
	release := holdMaterializeFile(t, filepath.Join(f.s.Worktree.Dir, "deleted.txt"))
	index := readTestFile(t, filepath.Join(materializeAdmin(t, f), "index"))
	id := uuid.NewV7()
	err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, id, guard)
	assertRefusal(t, err, "materialization_mismatch")
	if string(readTestFile(t, filepath.Join(f.s.Worktree.Dir, "deleted.txt"))) != "delete me\n" || string(readTestFile(t, filepath.Join(f.s.Worktree.Dir, "modified.txt"))) != "new\n" {
		t.Fatal("leftover was not preserved beside installed files")
	}
	branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, len(h.old))
	if err != nil || branch != h.old || !bytes.Equal(index, readTestFile(t, filepath.Join(materializeAdmin(t, f), "index"))) {
		t.Fatalf("exit-zero leftover installed index/ref: %s %v", branch, err)
	}
	release()
	actual := observedMaterializeTree(t, f, h, guard)
	if actual == h.oldTree || actual == h.targetTree {
		t.Fatal("fixture did not exercise leftover O")
	}
	if err := f.engine.materialize(context.Background(), f.s, actual, h.targetTree, h.old, h.target, id, guard); err != nil {
		t.Fatal(err)
	}
	assertMaterialized(t, f, h, false)
}

func TestMaterializePinnedCRLF(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	f := h.fixture(t, true)
	gitCommand(t, f.f.env, f.f.root, nil, "config", "core.autocrlf", "false")
	gitCommand(t, f.f.env, f.f.root, nil, "config", "core.eol", "lf")
	if err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, uuid.NewV7(), materializeGuard(t, f)); err != nil {
		t.Fatal(err)
	}
	assertMaterialized(t, f, h, true)
	for path, text := range h.files {
		if got := string(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "blob", h.targetTree+":"+path)); got != text {
			t.Fatalf("canonical blob %s: %q", path, got)
		}
	}
}

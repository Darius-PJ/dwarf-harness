package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureScratch(t *testing.T) (gitFixture, Proposal, scratchWorktree) {
	t.Helper()
	f := newGitFixture(t, "sha1")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.Normalization.AutoCRLF = "true"
	s, err := f.git.createWorktree(context.Background(), f.colony, p, filepath.Join(f.colony, "workspaces", "inputs", "1"), filepath.Join(f.colony, "scratch", "01234567"), "scratch-test")
	if err != nil {
		t.Fatal(err)
	}
	return f, p, s
}

func TestCreateNeverChecksOutMain(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			f := newGitFixture(t, format)
			p, err := f.git.inspect(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			p.Normalization.AutoCRLF = "true"
			s, err := f.git.createWorktree(context.Background(), f.colony, p, filepath.Join(f.colony, "workspaces", "inputs", "1"), filepath.Join(f.colony, "scratch", "01234567"), "scratch-test")
			if err != nil {
				t.Fatal(err)
			}
			if s.Branch != "refs/heads/dwarf-harness/01234567" {
				t.Fatalf("wrong branch: %s", s.Branch)
			}
			main := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "--verify", "--end-of-options", "refs/heads/main")))
			if s.BaseCommit != main {
				t.Fatal("wrong base")
			}
			entries, err := f.git.listWorktrees(context.Background(), f.colony, p.CommonDir, oidLength(format))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				if entry.Branch == "refs/heads/main" {
					t.Fatal("checked out main")
				}
				if entry.Branch == s.Branch {
					found = true
				}
			}
			if !found {
				t.Fatal("scratch not registered")
			}
			if got := readTestFile(t, filepath.Join(s.Dir, "base.txt")); !bytes.Equal(got, []byte("one\r\ntwo\r\n")) {
				t.Fatalf("wrong checkout bytes: %q", got)
			}
			status := gitCommand(t, f.env, s.Dir, nil, "-c", "core.autocrlf=true", "status", "--porcelain=v1", "-z")
			if len(status) != 0 {
				t.Fatalf("dirty checkout: %q", status)
			}
			if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreationRechecksMain(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	gitCommand(t, f.env, f.root, nil, "checkout", "main")
	_, err = f.git.createWorktree(context.Background(), f.colony, p, filepath.Join(f.colony, "workspaces", "inputs", "1"), filepath.Join(f.colony, "scratch", "01234567"), "scratch-test")
	if !errors.Is(err, ErrCanonicalCheckedOut) {
		t.Fatalf("got %v", err)
	}
}

func TestRemoveCompletesWhatGitLeaves(t *testing.T) {
	t.Parallel()
	f, p, s := fixtureScratch(t)
	nested := filepath.Join(s.Dir, "ignored", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, f.env, nested, nil, "init")
	writeTestFile(t, filepath.Join(nested, "precious"), []byte("scratch-local"))
	// No Git removal is invoked: Core owns deletion, including ignored nested repos.
	failure := errors.New("disk temporarily unavailable")
	err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, func(string) error { return failure })
	if !errors.Is(err, ErrRemovalPending) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(s.Dir); err != nil {
		t.Fatal("pending directory disappeared")
	}
	if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
		t.Fatal(err)
	}
	assertScratchRemoved(t, f, p, s)
	if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
		t.Fatal("retry not idempotent:", err)
	}
}

func assertScratchRemoved(t *testing.T, f gitFixture, p Proposal, s scratchWorktree) {
	t.Helper()
	if _, err := os.Lstat(s.Dir); !os.IsNotExist(err) {
		t.Fatalf("scratch remains: %v", err)
	}
	entries, err := f.git.listWorktrees(context.Background(), f.colony, p.CommonDir, oidLength(p.ObjectFormat))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Clean(e.Path) == s.Dir {
			t.Fatal("registration remains")
		}
	}
	for _, ref := range []string{s.Branch, s.AnchorRef} {
		oid, err := f.git.readRef(context.Background(), f.colony, p.CommonDir, ref, 40)
		if err != nil || oid != "" {
			t.Fatalf("ref remains: %s %s %v", ref, oid, err)
		}
	}
}

func TestRemovalPreservesMovedBranch(t *testing.T) {
	t.Parallel()
	f, p, s := fixtureScratch(t)
	tree := strings.TrimSpace(string(gitCommand(t, f.env, s.Dir, nil, "write-tree")))
	commit := strings.TrimSpace(string(gitCommand(t, f.env, s.Dir, []byte("agent commit\n"), "commit-tree", tree, "-p", s.BaseCommit)))
	gitCommand(t, f.env, s.Dir, nil, "update-ref", s.Branch, commit, s.BaseCommit)
	if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
		t.Fatal(err)
	}
	got, err := f.git.readRef(context.Background(), f.colony, p.CommonDir, s.Branch, 40)
	if err != nil || got != commit {
		t.Fatalf("lost agent history: %s %v", got, err)
	}
}

func TestRefsAreCreateOnly(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	a := strings.TrimSpace(string(gitCommand(t, f.env, f.root, []byte("first\n"), "hash-object", "-w", "--stdin", "--no-filters")))
	b := strings.TrimSpace(string(gitCommand(t, f.env, f.root, []byte("second\n"), "hash-object", "-w", "--stdin", "--no-filters")))
	for _, ref := range []string{"refs/dwarf-harness/colony", "refs/dwarf-harness/anchors/test", "refs/dwarf-harness/captures/test"} {
		if err := f.git.createRef(context.Background(), f.colony, p.CommonDir, ref, a); err != nil {
			t.Fatal(err)
		}
		if err := f.git.createRef(context.Background(), f.colony, p.CommonDir, ref, b); !errors.Is(err, ErrGit) {
			t.Fatalf("second create: %v", err)
		}
		got, err := f.git.readRef(context.Background(), f.colony, p.CommonDir, ref, 40)
		if err != nil || got != a {
			t.Fatalf("overwrote ref: %s %v", got, err)
		}
	}
}

func TestMarkerIsRawAndResumable(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha256")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	gitCommand(t, f.env, f.root, nil, "config", "core.autocrlf", "true")
	for range 2 {
		if err := f.git.ensureMarker(context.Background(), f.colony, p, "colony-id"); err != nil {
			t.Fatal(err)
		}
	}
	b, err := f.git.readBlob(context.Background(), f.colony, p.CommonDir, "refs/dwarf-harness/colony")
	if err != nil || string(b) != "dwarf-harness colony colony-id\n" {
		t.Fatalf("marker %q, %v", b, err)
	}
	if err := f.git.ensureMarker(context.Background(), f.colony, p, "other-colony"); !errors.Is(err, ErrOtherColony) {
		t.Fatalf("got %v", err)
	}
}

func TestRemoveDoesNotFollowSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows junction and directory symlink cases are in TestRemoveDoesNotFollowJunction")
	}
	f, p, s := fixtureScratch(t)
	target := t.TempDir()
	writeTestFile(t, filepath.Join(target, "keep"), []byte("owner bytes"))
	if err := os.Symlink(target, filepath.Join(s.Dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(target, "keep")); string(got) != "owner bytes" {
		t.Fatalf("target changed: %q", got)
	}
}

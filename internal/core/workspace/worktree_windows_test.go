package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func makeJunction(t *testing.T, path, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeIrregular == 0 {
		t.Fatalf("junction type: %v, %v", info, err)
	}
}

func TestRemoveDoesNotFollowJunction(t *testing.T) {
	t.Run("os.Remove unlinks junction", func(t *testing.T) {
		target := t.TempDir()
		writeTestFile(t, filepath.Join(target, "keep"), []byte("owner bytes"))
		link := filepath.Join(t.TempDir(), "junction")
		makeJunction(t, link, target)
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if got := readTestFile(t, filepath.Join(target, "keep")); string(got) != "owner bytes" {
			t.Fatalf("target changed: %q", got)
		}
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("link remains: %v", err)
		}
	})
	for _, kind := range []string{"junction", "directory symlink"} {
		for _, retry := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: " retry", false: " remove"}[retry], func(t *testing.T) {
				f, p, s := fixtureScratch(t)
				target := t.TempDir()
				writeTestFile(t, filepath.Join(target, "keep"), []byte("owner bytes"))
				link := filepath.Join(s.Dir, "link")
				if kind == "junction" {
					makeJunction(t, link, target)
				} else if err := os.Symlink(target, link); err != nil {
					if errors.Is(err, syscall.Errno(1314)) {
						t.Skip("directory symlink creation lacks SeCreateSymbolicLinkPrivilege")
					}
					t.Fatal(err)
				}
				if retry {
					err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, func(string) error { return syscall.EACCES })
					if !errors.Is(err, ErrRemovalPending) {
						t.Fatalf("got %v", err)
					}
					if kind == "junction" {
						makeJunction(t, link, target)
					} else if err := os.Symlink(target, link); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
					t.Fatal(err)
				}
				if got := readTestFile(t, filepath.Join(target, "keep")); string(got) != "owner bytes" {
					t.Fatalf("target changed: %q", got)
				}
				assertScratchRemoved(t, f, p, s)
			})
		}
	}
}

func TestRemovalBlockedByOpenHandle(t *testing.T) {
	f, p, s := fixtureScratch(t)
	path, err := syscall.UTF16PtrFromString(filepath.Join(s.Dir, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = syscall.CloseHandle(handle)
		}
	}()
	err = f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil)
	if !errors.Is(err, ErrRemovalPending) {
		t.Fatalf("held handle result: %v", err)
	}
	if _, err := os.Stat(s.Dir); err != nil {
		t.Fatalf("pending directory missing: %v", err)
	}
	entries, err := f.git.listWorktrees(context.Background(), f.colony, p.CommonDir, 40)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if filepath.Clean(entry.Path) == s.Dir {
			found = true
		}
	}
	if !found {
		t.Fatal("pending removal dropped worktree registration")
	}
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := f.git.removeWorktree(context.Background(), f.colony, p.CommonDir, s, nil); err != nil {
		t.Fatal(err)
	}
	assertScratchRemoved(t, f, p, s)
}

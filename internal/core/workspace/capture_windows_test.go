package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCaptureRefusesJunction(t *testing.T) {
	t.Parallel()
	for _, tracked := range []bool{false, true} {
		t.Run(map[bool]string{false: "untracked", true: "tracked directory"}[tracked], func(t *testing.T) {
			t.Parallel()
			f := newCaptureFixture(t)
			target := t.TempDir()
			writeTestFile(t, filepath.Join(target, "inside.txt"), []byte("outside bytes must not be read\n"))
			if tracked {
				tree, _ := plumbingBaseline(t, f.f, []stageEntry{{Path: "base.txt", Mode: "100644"}, {Path: "sub/inside.txt", Mode: "100644"}})
				f.s.Worktree.AnchorTree = tree
			}
			link := filepath.Join(f.s.Worktree.Dir, "sub")
			makeJunction(t, link, target)
			t.Cleanup(func() {
				if err := os.Remove(link); err != nil {
					t.Error(err)
				}
			})
			reads := 0
			f.engine.afterRawRead = func(string) { reads++ }
			_, err := f.capture(context.Background(), sealCapture)
			assertRefusal(t, err, "special_file")
			if reads != 0 {
				t.Fatalf("read %d files before rejecting junction", reads)
			}
			if got := string(readTestFile(t, filepath.Join(target, "inside.txt"))); got != "outside bytes must not be read\n" {
				t.Fatal("junction target changed")
			}
		})
	}
}

func TestCaptureRefusesSymlink(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	writeTestFile(t, target, []byte("outside"))
	link := filepath.Join(f.s.Worktree.Dir, "link")
	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, syscall.Errno(1314)) {
			t.Skip("symlink creation lacks SeCreateSymbolicLinkPrivilege")
		}
		t.Fatal(err)
	}
	reads := 0
	f.engine.afterRawRead = func(string) { reads++ }
	_, err := f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "unsupported_kind")
	if reads != 0 {
		t.Fatalf("read %d files before rejecting symlink", reads)
	}
}

func TestCaptureCaseOnlyRename(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	f.p.Normalization.IgnoreCase = "true"
	if err := os.Rename(filepath.Join(f.s.Worktree.Dir, "base.txt"), filepath.Join(f.s.Worktree.Dir, "BASE.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "name_mismatch")
}

func TestCaptureLongPath(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	f.p.Normalization.LongPaths = "false"
	path := filepath.Join(f.s.Worktree.Dir, strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100), "long.txt")
	writeTestFile(t, path, []byte("Git cannot list this path\n"))
	_, err := f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "unobservable_path")
}

func TestCaptureUnreadable(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	path, err := syscall.UTF16PtrFromString(filepath.Join(f.s.Worktree.Dir, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(handle) })
	_, err = f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "unreadable")
}

func TestCaptureWindowsModes(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	f.p.Normalization.FileMode = "false"
	tree, _ := plumbingBaseline(t, f.f, []stageEntry{{Path: "base.txt", Mode: "100755"}})
	f.s.Worktree.AnchorTree = tree
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("new\n"))
	rows := manifestRows(t, mustCapture(t, f, sealCapture))
	modes := map[string]string{}
	for _, row := range rows {
		modes[row.Path] = row.Mode
	}
	if modes["base.txt"] != "100755" || modes["new.txt"] != "100644" {
		t.Fatalf("Windows modes: %v", modes)
	}
}

//go:build linux || darwin

package workspace

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestCaptureRefusesSpecialFiles(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"fifo"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newCaptureFixture(t)
			path := filepath.Join(f.s.Worktree.Dir, "special")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			reads := 0
			f.engine.afterRawRead = func(string) { reads++ }
			_, err := f.capture(context.Background(), sealCapture)
			assertRefusal(t, err, "special_file")
			if reads != 0 {
				t.Fatalf("read %d files before rejecting %s", reads, kind)
			}
		})
	}
}

func TestCaptureRefusesSocket(t *testing.T) {
	f := newCaptureFixture(t)
	// sockaddr_un has a short limit, especially under macOS's
	// long temporary root. The socket still lives in the scratch.
	t.Chdir(f.s.Worktree.Dir)
	socket, err := net.Listen("unix", "special")
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	reads := 0
	f.engine.afterRawRead = func(string) { reads++ }
	_, err = f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "special_file")
	if reads != 0 {
		t.Fatalf("read %d files before rejecting socket", reads)
	}
}

func TestCaptureRefusesSymlink(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	writeTestFile(t, target, []byte("outside"))
	if err := os.Symlink(target, filepath.Join(f.s.Worktree.Dir, "link")); err != nil {
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

func TestCaptureExecutableMode(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	f.p.Normalization.FileMode = "true"
	path := filepath.Join(f.s.Worktree.Dir, "run.sh")
	writeTestFile(t, path, []byte("#!/bin/sh\nexit 0\n"))
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	rows := manifestRows(t, mustCapture(t, f, sealCapture))
	for _, row := range rows {
		if row.Path == "run.sh" {
			if row.Mode != "100755" {
				t.Fatalf("executable mode lost: %s", row.Mode)
			}
			return
		}
	}
	t.Fatal("executable missing from capture")
}

func TestCaptureUnreadable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod 000 read denial")
	}
	f := newCaptureFixture(t)
	path := filepath.Join(f.s.Worktree.Dir, "base.txt")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Error(err)
		}
	})
	_, err := f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "unreadable")
}

func requireCaseSensitiveFilesystem(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "lower"), []byte("case probe"))
	_, err := os.Stat(filepath.Join(dir, "LOWER"))
	if err == nil {
		t.Skip("filesystem is case-insensitive")
	}
	if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestCaptureCaseOnlyRenameIsDeleteAndAdd(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("Linux case-sensitive rename row")
	}
	requireCaseSensitiveFilesystem(t)
	f := newCaptureFixture(t)
	f.p.Normalization.IgnoreCase = "false"
	if err := os.Rename(filepath.Join(f.s.Worktree.Dir, "base.txt"), filepath.Join(f.s.Worktree.Dir, "BASE.txt")); err != nil {
		t.Fatal(err)
	}
	want, _ := referenceTree(t, f)
	got := mustCapture(t, f, sealCapture)
	if got.Tree != want {
		t.Fatalf("case rename tree %s want %s", got.Tree, want)
	}
	rows := manifestRows(t, got)
	if len(rows) != 1 || rows[0].Path != "BASE.txt" {
		t.Fatalf("case rename paths: %#v", rows)
	}
}

func TestUnreadableDirectoryPathIsRelative(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod 000 read denial")
	}
	root := t.TempDir()
	path := filepath.Join(root, "nested", "unreadable")
	writeTestFile(t, filepath.Join(path, "file"), []byte("unreadable"))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o700); err != nil {
			t.Error(err)
		}
	})
	_, err := walkCapture(context.Background(), root, nil)
	refusal := assertRefusal(t, err, "unreadable")
	if refusal.Path != "nested/unreadable" {
		t.Fatalf("host path disclosed: %q", refusal.Path)
	}
}

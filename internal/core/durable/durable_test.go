package durable_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := durable.LockFile(path)
	if err != nil {
		t.Fatalf("LockFile of a new file = %v", err)
	}
	second, err := durable.LockFile(path)
	if !errors.Is(err, durable.ErrLocked) {
		if second != nil {
			second.Close()
		}
		first.Close()
		t.Fatalf("second LockFile while held = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	third, err := durable.LockFile(path)
	if err != nil {
		t.Fatalf("LockFile after Close = %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

// TestSyncDirAndRenameBeyondMaxPath runs SyncDir and Rename, by absolute and
// relative paths, in a directory whose path exceeds the 260-character
// Windows limit, and checks that their errors are returned. The working
// directory stays short, because Windows may refuse a longer one, and the
// relative paths resolve beyond the limit. Where Windows long paths are
// enabled the calls would succeed without the \\?\ form too; there the test
// checks that the form is built correctly.
func TestSyncDirAndRenameBeyondMaxPath(t *testing.T) {
	root := t.TempDir()
	dir := root
	for len(dir) <= 300 {
		dir = filepath.Join(dir, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tmp, target := filepath.Join(dir, "colony.json.tmp"), filepath.Join(dir, "colony.json")
	// rel returns path relative to root.
	rel := func(path string) string {
		t.Helper()
		r, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// assertTarget checks that target holds want and tmp is gone.
	assertTarget := func(want string) {
		t.Helper()
		if got, err := os.ReadFile(target); err != nil || string(got) != want {
			t.Fatalf("target = %q, %v; want %q", got, err, want)
		}
		if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("source after Rename: %v, want it gone", err)
		}
	}

	write(target, "old")
	write(tmp, "new")
	if err := durable.Rename(tmp, target); err != nil {
		t.Fatalf("Rename onto an existing file = %v", err)
	}
	assertTarget("new")
	if err := durable.SyncDir(dir); err != nil {
		t.Fatalf("SyncDir = %v", err)
	}

	t.Chdir(root)
	write(tmp, "newer")
	if err := durable.Rename(rel(tmp), rel(target)); err != nil {
		t.Fatalf("Rename by relative paths = %v", err)
	}
	assertTarget("newer")
	if err := durable.SyncDir(rel(dir)); err != nil {
		t.Fatalf("SyncDir by relative path = %v", err)
	}

	if err := durable.SyncDir(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SyncDir of a missing directory = %v, want an error matching fs.ErrNotExist", err)
	}
	if err := durable.Rename(tmp, target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename of a missing file = %v, want an error matching fs.ErrNotExist", err)
	}
	assertTarget("newer")
}

package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func holdCanonicalPath(t *testing.T, path string, directory bool) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(`\\?\` + filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	flags := uint32(syscall.FILE_ATTRIBUTE_NORMAL)
	if directory {
		flags = syscall.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	release := func() {
		if !closed {
			closed = true
			if err := syscall.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(release)
	return release
}

func TestWindowsPackedRefDurability(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	for _, row := range []string{"flush", "held-packed-ref"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f, w := finalizationFixture(t, h, true)
			packed := readTestFile(t, filepath.Join(w.CommonDir, "packed-refs"))
			before := canonicalFiles(t, w.CommonDir)
			updates := countCanonicalUpdates(f.svc.git)
			if row == "held-packed-ref" {
				release := holdCanonicalPath(t, filepath.Join(w.CommonDir, "packed-refs"), false)
				state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
				release()
				if err == nil || state == mainUpdateMoved || !reflect.DeepEqual(before, canonicalFiles(t, w.CommonDir)) {
					t.Fatalf("held packed refs did not refuse safely: %v %v", state, err)
				}
				t.Logf("held packed-refs real error: %v", err)
				return
			}
			state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
			if err != nil || state != mainUpdateMoved {
				t.Fatalf("packed update: %v %v", state, err)
			}
			if err := f.svc.hardenCanonicalStorage(context.Background(), w, h.target, h.tree); err != nil {
				t.Fatal(err)
			}
			if *updates != 1 || !bytes.Equal(packed, readTestFile(t, filepath.Join(w.CommonDir, "packed-refs"))) || string(readTestFile(t, filepath.Join(w.CommonDir, "refs", "heads", "main"))) != h.target+"\n" {
				t.Fatal("packed main did not become the exact durable loose target")
			}
		})
	}
}

// The pinned Windows Git rejects GIT_DIR paths longer than 220 characters,
// including extended-length paths: no working common directory can exceed
// MAX_PATH here. SyncDir's long-path behavior is covered in durable's tests;
// this test exercises the reachable canonical sharing and reparse failures.
func TestWindowsFinalizationSharing(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	for _, row := range []string{"held-ref", "held-directory", "junction"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f, w := finalizationFixture(t, h, false)
			updates := countCanonicalUpdates(f.svc.git)
			state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
			if err != nil || state != mainUpdateMoved {
				t.Fatalf("move: %v %v", state, err)
			}
			before := canonicalFiles(t, w.CommonDir)
			release := func() {}
			switch row {
			case "held-ref":
				release = holdCanonicalPath(t, filepath.Join(w.CommonDir, "refs", "heads", "main"), false)
			case "held-directory":
				release = holdCanonicalPath(t, filepath.Join(w.CommonDir, "refs", "heads"), true)
			case "junction":
				path := filepath.Join(w.CommonDir, "objects", "pack")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				makeJunction(t, path, t.TempDir())
			}
			err = f.svc.hardenCanonicalStorage(context.Background(), w, h.target, h.tree)
			release()
			if err == nil {
				t.Fatal("unsafe directory or sharing denial treated as durable")
			} else {
				t.Logf("real durability error: %v", err)
			}
			if *updates != 1 || !reflect.DeepEqual(before, canonicalFiles(t, w.CommonDir)) {
				t.Fatal("durability retried or rolled back the canonical effect")
			}
			assertCanonicalTip(t, f, h.target)
		})
	}
}

package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"uuid"
)

func TestCloseRemovalPending(t *testing.T) {
	t.Parallel()
	for _, reopen := range []bool{false, true} {
		name := "retry"
		if reopen {
			name = "open"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t)
			s := mustScratch(t, f, uuid.NewV7())
			path, err := syscall.UTF16PtrFromString(filepath.Join(s.Dir, "base.txt"))
			if err != nil {
				t.Fatal(err)
			}
			// Reads remain allowed so observation proves representability; only
			// deletion is denied. A share-zero handle would refuse the capture.
			handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					_ = syscall.CloseHandle(handle)
				}
			})
			before := f.store.Durable()
			_, err = f.svc.CloseScratch(context.Background(), ownerClosure(s))
			if !errors.Is(err, ErrRemovalPending) {
				t.Fatalf("held handle: %v", err)
			}
			if f.store.Durable() != before+1 {
				t.Fatal("closure was not durable before failed removal")
			}
			if _, err := f.svc.BeginWrite(context.Background(), s.ID); !errors.Is(err, ErrClosed) {
				t.Fatalf("closed scratch accepts writes: %v", err)
			}
			if err := syscall.CloseHandle(handle); err != nil {
				t.Fatal(err)
			}
			closed = true
			if reopen {
				report := f.reopen(t, serviceConfig())
				if report.RemovalsCompleted != 1 || len(report.Pending) != 0 {
					t.Fatalf("reconciliation: %#v", report)
				}
			} else if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
				t.Fatal(err)
			}
			if f.store.Durable() != before+1 {
				t.Fatal("removal retry wrote another closure")
			}
			if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
				t.Fatalf("directory remains: %v", err)
			}
		})
	}
}

func TestCloseScratchRefusesJunction(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	target := t.TempDir()
	writeTestFile(t, filepath.Join(target, "keep"), []byte("owner bytes"))
	makeJunction(t, filepath.Join(s.Dir, "junction"), target)
	before := f.store.Durable()
	_, err := f.svc.CloseScratch(context.Background(), ownerClosure(s))
	assertRefusal(t, err, "special_file")
	if f.store.Durable() != before || string(readTestFile(t, filepath.Join(target, "keep"))) != "owner bytes" {
		t.Fatal("refused junction closure changed state")
	}
}

func TestOpenReconcilesPendingOrphan(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	orphan := orphanWorktree(t, f)
	path, err := syscall.UTF16PtrFromString(filepath.Join(orphan.Dir, "base.txt"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = syscall.CloseHandle(handle)
		}
	})
	report := f.reopen(t, serviceConfig())
	if report.OrphansRemoved != 0 || len(report.Pending) == 0 {
		t.Fatalf("pending orphan not reported: %#v", report)
	}
	for _, ref := range []string{orphan.Branch, orphan.AnchorRef} {
		got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40)
		if err != nil || got != orphan.BaseCommit {
			t.Fatalf("pending orphan lost %s: %q %v", ref, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(orphan.Dir, ".git")); err != nil {
		t.Fatalf("pending orphan lost workspace identity: %v", err)
	}
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	closed = true
	report = f.reopen(t, serviceConfig())
	if report.OrphansRemoved != 1 || len(report.Pending) != 0 {
		t.Fatalf("retry: %#v", report)
	}
	if _, err := os.Stat(orphan.Dir); !os.IsNotExist(err) {
		t.Fatalf("orphan remains: %v", err)
	}
	for _, ref := range []string{orphan.Branch, orphan.AnchorRef} {
		got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40)
		if err != nil || got != "" {
			t.Fatalf("retry retained %s: %q %v", ref, got, err)
		}
	}
}

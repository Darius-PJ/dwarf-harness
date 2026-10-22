package agents

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func holdPrivateMarkdown(t *testing.T, path string) func() {
	t.Helper()
	// A plain native handle with no FILE_SHARE_DELETE blocks replacement even
	// on hosts where the test process has SeBackupPrivilege enabled.
	name, err := syscall.UTF16PtrFromString(`\\?\` + path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			if err := syscall.CloseHandle(h); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(release)
	return release
}

func lockPrivateAppend(t *testing.T, path string) func() {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	offset := uint64(info.Size())
	overlap := syscall.Overlapped{Offset: uint32(offset), OffsetHigh: uint32(offset >> 32)}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	lock, unlock := kernel.NewProc("LockFileEx"), kernel.NewProc("UnlockFileEx")
	ok, _, callErr := lock.Call(file.Fd(), 3, 0, 1<<30, 0, uintptr(unsafe.Pointer(&overlap)))
	if ok == 0 {
		_ = file.Close()
		t.Fatal(callErr)
	}
	t.Logf("native LockFileEx next append offset=%d length=%d", offset, 1<<30)
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

func assertNoPrivateTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".private-") {
			t.Fatalf("failed call left temporary file %s", entry.Name())
		}
	}
}

func TestPrivateWindowsSharingRecovery(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	path, err := f.service.scopePath(submissionIDs{b.AgentID, privateID(11), b.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	markdown, _ := path.markdownPath("memory")
	release := holdPrivateMarkdown(t, markdown)
	before := f.store.Durable()
	req := memoryWrite(b, 20, 1, "native sharing\x00\r\n")
	_, err = f.service.WritePrivate(context.Background(), req)
	var material *MaterializationError
	if !errors.As(err, &material) || material.Revision != 2 || f.store.Durable() != before+3 {
		t.Fatalf("sharing violation did not withhold committed acknowledgement: %v", err)
	}
	// MoveFileEx can report ACCESS_DENIED rather than SHARING_VIOLATION
	// when replacing a target held without FILE_SHARE_DELETE.
	var native syscall.Errno
	if !errors.As(material.Cause, &native) || native != syscall.Errno(32) && native != syscall.Errno(5) {
		t.Fatalf("not a native sharing refusal: %v", material.Cause)
	}
	t.Logf("native held-target replacement error=%d: %v", native, material.Cause)
	snap, err := f.service.Snapshot(context.Background(), ReadBinding{b, "context"})
	if err != nil || snap.Revision != 2 || snap.Materializations["memory"] != "stale" || len(readTestFile(t, markdown)) != 0 {
		t.Fatalf("native replacement failure lost stale state: %+v %v", snap, err)
	}
	assertNoPrivateTemps(t, path.directory())
	receipt, err := f.service.WritePrivate(context.Background(), req)
	if err != nil || receipt.Revision != 2 || f.store.Durable() != before+3 {
		t.Fatalf("immutable receipt unavailable while stale: %+v %v", receipt, err)
	}
	release()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	log := readTestFile(t, segment)
	var report Reconciliation
	f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil || len(report.Issues) != 0 || len(report.Completed) != 1 {
		t.Fatalf("released sharing violation did not complete: %+v %v", report, err)
	}
	if report.Completed[0].Revision != 2 || report.Completed[0].Slot != "memory" || report.Completed[0].Binding != b || !bytes.Equal(readTestFile(t, markdown), req.Changes[0].Bytes) || !bytes.Equal(readTestFile(t, segment), log) {
		t.Fatal("recovery changed history or failed to complete the bound revision")
	}
	replay, err := f.service.WritePrivate(context.Background(), req)
	if err != nil || !reflect.DeepEqual(replay, receipt) || f.store.Durable() != before+3 {
		t.Fatalf("native sharing recovery changed receipt: %+v %v", replay, err)
	}
	t.Log("native held-handle refusal; committed stale revision completed without append after handle release")
}

func TestPrivateWindowsLockedSegment(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	before := f.store.Durable()
	segment := filepath.Join(f.store.LogDir(), ledger.SegmentName(1))
	prefix := readTestFile(t, segment)
	var release func()
	f.service.deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
		release = lockPrivateAppend(t, segment)
		return d
	}
	req := memoryWrite(b, 20, 1, "native locked append")
	_, err := f.service.WritePrivate(context.Background(), req)
	if release == nil {
		t.Fatal("store append boundary not reached")
	}
	if !errors.Is(err, appender.ErrUncertain) || !errors.Is(err, syscall.Errno(33)) {
		t.Fatalf("no observed real store-write failure: %v", err)
	}
	release()
	if f.store.Durable() != before || !bytes.Equal(readTestFile(t, segment), prefix) {
		t.Fatal("failed locked append changed acknowledged history")
	}
	if _, err := f.service.WritePrivate(context.Background(), req); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("service reused uncertain store incarnation: %v", err)
	}
	if _, err := f.service.Snapshot(context.Background(), ReadBinding{b, "context"}); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("poisoned service answered a read: %v", err)
	}
	cmd := ledger.Command{CommandID: privateID(99), SubmissionSHA256: crashDigest(t, req)}
	if _, err := f.store.Submit(context.Background(), ledger.Draft{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, cmd)}, nil); !errors.Is(err, appender.ErrPoisoned) {
		t.Fatalf("store reused failed writer: %v", err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: b, CommandID: req.CommandID, SubmissionSHA256: crashDigest(t, req)}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed append recovered a receipt: %v", err)
	}
	if f.store.Durable() != before || len(readPrivate(t, f.service, b, 1, "memory").Bytes) != 0 {
		t.Fatal("failed append selected uncommitted bytes")
	}
	first, err := f.service.WritePrivate(context.Background(), req)
	if err != nil || first.Revision != 2 {
		t.Fatalf("same-ID retry after reopening failed: %+v %v", first, err)
	}
	second, err := f.service.WritePrivate(context.Background(), req)
	if err != nil || !reflect.DeepEqual(first, second) || f.store.Durable() != before+3 || !bytes.Equal(readPrivate(t, f.service, b, 2, "memory").Bytes, req.Changes[0].Bytes) {
		t.Fatalf("retry failed exact-once behavior: %+v %v", second, err)
	}
	t.Log("native ERROR_LOCK_VIOLATION; uncertain append poisoned service and store, no committed effect after reopen")
}

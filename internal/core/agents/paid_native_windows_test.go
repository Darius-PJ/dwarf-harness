//go:build windows && amd64

package agents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"

	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
)

type paidNativeCredential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             uint64
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	TargetAlias, UserName   *uint16
}

func TestNativeCredentialSource(t *testing.T) {
	// Owns a session-only native resource. Never enumerates or edits other targets.
	f := newLivePaidFixture(t)
	ctx := context.Background()
	dll := syscall.NewLazyDLL("advapi32.dll")
	write := dll.NewProc("CredWriteW")
	remove := dll.NewProc("CredDeleteW")
	if write.Find() != nil || remove.Find() != nil {
		t.Fatal("native credential procedures unavailable")
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	entropy[6] = entropy[6]&15 | 64
	entropy[8] = entropy[8]&63 | 128
	h := hex.EncodeToString(entropy[:])
	target := "dwarf-harness-test/" + h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	t.Log("credential target: " + target)
	name, err := syscall.UTF16FromString(target)
	if err != nil {
		t.Fatal(err)
	}
	present := false
	deleteTarget := func() {
		if !present {
			return
		}
		var pin runtime.Pinner
		pin.Pin(&name[0])
		defer pin.Unpin()
		r, _, _ := remove.Call(uintptr(unsafe.Pointer(&name[0])), 1, 0)
		runtime.KeepAlive(name)
		if r == 0 {
			t.Fatal("synthetic credential deletion failed")
		}
		present = false
	}
	t.Cleanup(deleteTarget)
	put := func(blob []byte) {
		c := paidNativeCredential{Type: 1, TargetName: &name[0], BlobSize: uint32(len(blob)), Blob: &blob[0], Persist: 1}
		var pin runtime.Pinner
		pin.Pin(&name[0])
		pin.Pin(&blob[0])
		pin.Pin(&c)
		r, _, _ := write.Call(uintptr(unsafe.Pointer(&c)), 0)
		runtime.KeepAlive(c)
		pin.Unpin()
		if r == 0 {
			t.Fatal("synthetic session credential write failed")
		}
		present = true
	}
	r := CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.service.p.paidCredentialScopes[f.credential.ScopeID].version, Provider: "anthropic", CredentialVersion: f.id(), SourceKind: "windows_credential_manager", SourceLocator: target, RevokePreviousVersion: f.credential.CredentialVersion}
	before := f.store.Durable()
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), r); !errors.Is(err, credentials.ErrMissing) || f.store.Durable() != before {
		t.Fatal("missing target admitted")
	}
	for _, blob := range [][]byte{{0x00}, {0x00, 0xd8}} {
		put(blob)
		if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), r); !errors.Is(err, credentials.ErrInvalid) || f.store.Durable() != before {
			t.Fatal("invalid UTF-16 target admitted")
		}
	}
	const value = "synthetic-native-producer-value"
	units := utf16.Encode([]rune(value))
	blob := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(blob[i*2:], u)
	}
	put(blob)
	clear(blob)
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), r); err != nil {
		t.Fatal("native binding refused")
	}
	if err = f.service.paid.secrets[r.CredentialVersion].WithBytes(func(b []byte) error {
		if !bytes.Equal(b, []byte(value)) {
			t.Error("native source did not round trip")
		}
		return nil
	}); err != nil {
		t.Fatal("native source inaccessible")
	}
	deleteTarget()
	if err = f.service.Close(); err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.service, report, err = openPaidService(ctx, f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.UnboundCredentials) != 1 || report.UnboundCredentials[0].SourceLocator != target {
		t.Fatal("replay consulted deleted native source")
	}
	r.CommandID = f.id()
	r.ExpectedVersion++
	r.RevokePreviousVersion = r.CredentialVersion
	r.CredentialVersion = f.id()
	if _, err = f.service.BindCredential(ctx, bindingOf(f.origin), r); !errors.Is(err, credentials.ErrMissing) {
		t.Fatal("deleted target admitted")
	}
}

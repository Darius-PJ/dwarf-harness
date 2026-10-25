//go:build windows && amd64

package credentials

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeCredentialSource(t *testing.T) {
	t.Parallel()
	write := advapi.NewProc("CredWriteW")
	deleteProc := advapi.NewProc("CredDeleteW")
	for _, proc := range []*windows.LazyProc{write, deleteProc} {
		if e := proc.Find(); e != nil {
			t.Fatal("native credential test procedure unavailable")
		}
	}
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		t.Fatal("UUID entropy unavailable")
	}
	id[6] = id[6]&15 | 64
	id[8] = id[8]&63 | 128
	h := hex.EncodeToString(id[:])
	target := "dwarf-harness-test/" + h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	t.Log("credential target: " + target)
	if secret, e := Native(target).Bind(); !errors.Is(e, ErrMissing) {
		if secret != nil {
			secret.Release()
		}
		t.Fatal("unique target was not absent")
	}
	name, e := windows.UTF16FromString(target)
	if e != nil {
		t.Fatal("target conversion failed")
	}
	wrote := false
	t.Cleanup(func() {
		if !wrote {
			return
		}
		var pin runtime.Pinner
		pin.Pin(&name[0])
		defer pin.Unpin()
		r, _, _ := deleteProc.Call(uintptr(unsafe.Pointer(&name[0])), 1, 0)
		runtime.KeepAlive(name)
		if r == 0 {
			t.Error("exact synthetic target deletion failed")
		}
		if secret, e := Native(target).Bind(); !errors.Is(e, ErrMissing) {
			if secret != nil {
				secret.Release()
			}
			t.Error("synthetic target remains after delete")
		}
	})
	blob := utf16Blob("synthetic-native-value")
	defer clear(blob)
	cred := credentialW{Type: 1, TargetName: &name[0], BlobSize: uint32(len(blob)), Blob: &blob[0], Persist: 1}
	var pin runtime.Pinner
	pin.Pin(&name[0])
	pin.Pin(&blob[0])
	pin.Pin(&cred)
	r, _, _ := write.Call(uintptr(unsafe.Pointer(&cred)), 0)
	runtime.KeepAlive(cred)
	pin.Unpin()
	if r == 0 {
		t.Fatal("synthetic credential write failed")
	}
	wrote = true
	secret, e := Native(target).Bind()
	if e != nil {
		t.Fatal(e)
	}
	defer secret.Release()
	if e = secret.WithBytes(func(b []byte) error {
		if !bytes.Equal(b, []byte("synthetic-native-value")) {
			t.Error("native credential did not round-trip")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestCredentialWLayoutMatchesNativeABI(t *testing.T) {
	t.Parallel()
	var c credentialW
	got := []uintptr{unsafe.Sizeof(c), unsafe.Offsetof(c.Flags), unsafe.Offsetof(c.Type), unsafe.Offsetof(c.TargetName), unsafe.Offsetof(c.Comment), unsafe.Offsetof(c.LastWritten), unsafe.Offsetof(c.BlobSize), unsafe.Offsetof(c.Blob), unsafe.Offsetof(c.Persist), unsafe.Offsetof(c.AttributeCount), unsafe.Offsetof(c.Attributes), unsafe.Offsetof(c.TargetAlias), unsafe.Offsetof(c.UserName)}
	want := []uintptr{80, 0, 4, 8, 16, 24, 32, 40, 48, 52, 56, 64, 72}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("native ABI member %d offset/size mismatch", i)
		}
	}
}

func TestNativeRecordRejectsWrongTypeAndUnboundedPointer(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"wrong_type", "oversize", "nil_blob"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			blob := utf16Blob("synthetic-record-value")
			defer clear(blob)
			record := credentialW{Type: 1, BlobSize: uint32(len(blob)), Blob: &blob[0]}
			switch name {
			case "wrong_type":
				record.Type = 2
			case "oversize":
				record.BlobSize = 2561
			case "nil_blob":
				record.Blob = nil
			}
			secret, err := copyNativeCredential(&record)
			if err == nil {
				if secret != nil {
					secret.Release()
				}
				t.Fatal("invalid native record dereferenced or accepted")
			}
		})
	}
}

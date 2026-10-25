//go:build windows && amd64

package credentials

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi   = windows.NewLazySystemDLL("advapi32.dll")
	credRead = advapi.NewProc("CredReadW")
	credFree = advapi.NewProc("CredFree")
)

// credentialW is the probed Windows/amd64 CREDENTIALW layout (80 bytes).
type credentialW struct {
	Flags          uint32
	Type           uint32
	TargetName     *uint16
	Comment        *uint16
	LastWritten    windows.Filetime
	BlobSize       uint32
	Blob           *byte
	Persist        uint32
	AttributeCount uint32
	Attributes     unsafe.Pointer
	TargetAlias    *uint16
	UserName       *uint16
}

func nativeFailure(err error) error {
	switch err {
	case windows.ERROR_NOT_FOUND:
		return ErrMissing
	case windows.ERROR_NO_SUCH_LOGON_SESSION:
		return ErrUnavailable
	case windows.ERROR_INVALID_FLAGS:
		return ErrInvalid
	default:
		return ErrNative
	}
}

func (s nativeSource) Bind() (*Secret, error) {
	if len(s.target) == 0 || len(s.target) > 32767 {
		return nil, ErrInvalid
	}
	target, err := windows.UTF16FromString(s.target)
	if err != nil || len(target) > 32768 {
		return nil, ErrInvalid
	}
	if err = credRead.Find(); err != nil {
		return nil, ErrUnavailable
	}
	if err = credFree.Find(); err != nil {
		return nil, ErrUnavailable
	}
	var out *credentialW
	var pin runtime.Pinner
	pin.Pin(&target[0])
	pin.Pin(&out)
	defer pin.Unpin()
	r, _, callErr := credRead.Call(uintptr(unsafe.Pointer(&target[0])), 1, 0, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(target)
	if r == 0 {
		return nil, nativeFailure(callErr)
	}
	if out == nil {
		return nil, ErrNative
	}
	// The OS allocation is not Go memory: Pinner is unnecessary for out itself.
	// Free the allocation once, never an interior Blob pointer.
	defer func() { credFree.Call(uintptr(unsafe.Pointer(out))); runtime.KeepAlive(out) }()
	return copyNativeCredential(out)
}

func copyNativeCredential(out *credentialW) (*Secret, error) {
	if out.Type != 1 || out.BlobSize == 0 || out.BlobSize > 2560 || out.Blob == nil {
		return nil, ErrInvalid
	}
	decoded, err := decodeBlob(unsafe.Slice(out.Blob, int(out.BlobSize)))
	if err != nil {
		return nil, err
	}
	defer clear(decoded)
	return bindBytes(decoded, 1280)
}

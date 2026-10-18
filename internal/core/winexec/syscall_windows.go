package winexec

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	userenv           = windows.NewLazySystemDLL("userenv.dll")
	createProfileProc = userenv.NewProc("CreateAppContainerProfile")
	deleteProfileProc = userenv.NewProc("DeleteAppContainerProfile")
	profileFolderProc = userenv.NewProc("GetAppContainerFolderPath")
	inJobProc         = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
)

type securityCapabilities struct {
	SID             *windows.SID
	Capabilities    *windows.SIDAndAttributes
	Count, Reserved uint32
}
type jobBasicAccounting struct {
	User, Kernel, PeriodUser, PeriodKernel int64
	Faults, Total, Active, Terminated      uint32
}
type jobBasicAndIO struct {
	Basic jobBasicAccounting
	IO    windows.IO_COUNTERS
}
type jobCPURate struct{ Flags, Rate uint32 }

func nativeError(stage string, err error) error {
	if err == nil {
		return nil
	}
	var code syscall.Errno
	if errors.As(err, &code) {
		return &NativeError{Stage: stage, Code: uint32(code), Err: err}
	}
	return fmt.Errorf("winexec: %s: %w", stage, err)
}
func callHRESULT(p *windows.LazyProc, args ...uintptr) error {
	if err := p.Find(); err != nil {
		return errors.Join(ErrUnsupported, nativeError(p.Name, err))
	}
	r, _, _ := p.Call(args...)
	if int32(r) < 0 {
		return &NativeError{Stage: p.Name, Code: uint32(r), HRESULT: true, Err: syscall.Errno(uint32(r))}
	}
	return nil
}
func nativeAvailable() error {
	for _, p := range []*windows.LazyProc{createProfileProc, deleteProfileProc, profileFolderProc, inJobProc} {
		if err := p.Find(); err != nil {
			return errors.Join(ErrUnsupported, nativeError(p.Name, err))
		}
	}
	return nil
}
func closeOwnedHandle(stage string, handle windows.Handle) error {
	if err := windows.CloseHandle(handle); err != nil {
		return errors.Join(ErrQuarantined, nativeError(stage, err))
	}
	return nil
}
func createProfile(name string) (*windows.SID, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	sid := new(*windows.SID)
	var pin runtime.Pinner
	pin.Pin(sid)
	defer pin.Unpin()
	err = callHRESULT(createProfileProc, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(p)), 0, 0, uintptr(unsafe.Pointer(sid)))
	runtime.KeepAlive(p)
	runtime.KeepAlive(sid)
	if err != nil {
		return nil, err
	}
	if *sid == nil {
		return nil, fmt.Errorf("%w: missing created SID", ErrQuarantined)
	}
	return *sid, nil
}
func deleteProfile(name string) error {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	err = callHRESULT(deleteProfileProc, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
	return err
}
func profileFolder(sid *windows.SID) (string, error) {
	p, err := windows.UTF16PtrFromString(sid.String())
	if err != nil {
		return "", err
	}
	result := new(*uint16)
	var pin runtime.Pinner
	pin.Pin(result)
	defer pin.Unpin()
	err = callHRESULT(profileFolderProc, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(result)))
	runtime.KeepAlive(p)
	runtime.KeepAlive(result)
	if *result != nil {
		defer windows.CoTaskMemFree(unsafe.Pointer(*result))
	}
	if err != nil {
		return "", err
	}
	if *result == nil {
		return "", ErrUnsupported
	}
	return windows.UTF16PtrToString(*result), nil
}
func processInJob(process, job windows.Handle) error {
	if err := inJobProc.Find(); err != nil {
		return errors.Join(ErrUnsupported, nativeError("IsProcessInJob", err))
	}
	var member int32
	r, _, err := inJobProc.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&member)))
	if r == 0 {
		return nativeError("IsProcessInJob", err)
	}
	if member == 0 {
		return fmt.Errorf("%w: process is not in the retained Job", ErrUnsupported)
	}
	return nil
}
func tokenInformation(token windows.Token, class uint32) ([]byte, error) {
	var n uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &n)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		if err == nil {
			return nil, ErrUnsupported
		}
		return nil, nativeError("GetTokenInformation.size", err)
	}
	if n == 0 || n > 65536 {
		return nil, ErrUnsupported
	}
	b := make([]byte, n)
	if err := windows.GetTokenInformation(token, class, &b[0], n, &n); err != nil {
		return nil, nativeError("GetTokenInformation", err)
	}
	if n > uint32(len(b)) {
		return nil, ErrUnsupported
	}
	return b[:n], nil
}

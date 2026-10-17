package winexec

import (
	"errors"
	"fmt"
)

var (
	ErrInvalid        = errors.New("winexec: invalid request")
	ErrInvalidRoot    = errors.New("winexec: invalid dedicated root")
	ErrUnsupported    = errors.New("winexec: unsupported native enforcement")
	ErrBusy           = errors.New("winexec: root is busy")
	ErrClosed         = errors.New("winexec: executor is closed")
	ErrQuarantined    = errors.New("winexec: resources quarantined")
	ErrCleanupPending = errors.New("winexec: closed resources require cleanup")
	ErrOutputLimit    = errors.New("winexec: combined output limit exceeded")
	ErrProcessExit    = errors.New("winexec: process exited unsuccessfully")
)

// NativeError preserves the operation and original native result. HRESULT
// distinguishes HRESULT-returning procedures from Win32 last-error APIs.
type NativeError struct {
	Stage   string
	Code    uint32
	HRESULT bool
	Err     error
}

func (e *NativeError) Error() string {
	return fmt.Sprintf("winexec: %s (code=0x%08x, HRESULT=%t): %v", e.Stage, e.Code, e.HRESULT, e.Err)
}
func (e *NativeError) Unwrap() error { return e.Err }

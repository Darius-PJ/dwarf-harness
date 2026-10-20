package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// openCaptureFile matches Git's ordinary file access. os.Open uses backup
// semantics on Windows, which can bypass sharing and ACL checks when the
// process has SeBackupPrivilege enabled.
func openCaptureFile(path string) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	switch {
	case strings.HasPrefix(abs, `\\?\`), strings.HasPrefix(abs, `\\.\`):
	case strings.HasPrefix(abs, `\\`):
		abs = `\\?\UNC\` + abs[2:]
	default:
		abs = `\\?\` + abs
	}
	name, err := syscall.UTF16PtrFromString(abs)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

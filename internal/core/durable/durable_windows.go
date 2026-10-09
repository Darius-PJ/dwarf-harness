package durable

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// LockFile opens path, creating it if missing, and locks it exclusively
// without waiting. It returns an error matching ErrLocked when another
// handle, in this process or another, holds the lock.
func LockFile(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	err = control(f, func(h windows.Handle) error {
		return windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	})
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			err = fmt.Errorf("%w: %s", ErrLocked, path)
		} else {
			err = &os.PathError{Op: "lock", Path: path, Err: err}
		}
		return nil, errors.Join(err, f.Close())
	}
	return &Lock{f: f}, nil
}

// Close unlocks and closes the file.
func (l *Lock) Close() error {
	err := control(l.f, func(h windows.Handle) error {
		return windows.UnlockFileEx(h, 0, 1, 0, &windows.Overlapped{})
	})
	if err != nil {
		err = &os.PathError{Op: "unlock", Path: l.f.Name(), Err: err}
	}
	return errors.Join(err, l.f.Close())
}

// control calls fn with the file's handle.
func control(f *os.File, fn func(windows.Handle) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var fnErr error
	if err := rc.Control(func(fd uintptr) { fnErr = fn(windows.Handle(fd)) }); err != nil {
		return err
	}
	return fnErr
}

// SyncDir flushes the entries of directory dir to stable storage.
func SyncDir(dir string) error {
	p, err := extendedPath(dir)
	if err != nil {
		return &os.PathError{Op: "syncdir", Path: dir, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return &os.PathError{Op: "syncdir", Path: dir, Err: err}
	}
	if err := windows.FlushFileBuffers(h); err != nil {
		return errors.Join(&os.PathError{Op: "syncdir", Path: dir, Err: err}, windows.CloseHandle(h))
	}
	if err := windows.CloseHandle(h); err != nil {
		return &os.PathError{Op: "syncdir", Path: dir, Err: err}
	}
	return nil
}

// Rename atomically replaces newpath with oldpath and then flushes
// newpath's directory.
func Rename(oldpath, newpath string) error {
	from, err := extendedPath(oldpath)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	to, err := extendedPath(newpath)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	return SyncDir(filepath.Dir(newpath))
}

// extendedPath returns path made absolute in the extended-length form, which
// Windows does not limit to MAX_PATH characters and does not normalize
// further.
func extendedPath(path string) (*uint16, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(abs, `\\?\`), strings.HasPrefix(abs, `\\.\`):
	case strings.HasPrefix(abs, `\\`):
		abs = `\\?\UNC\` + abs[2:]
	default:
		abs = `\\?\` + abs
	}
	return windows.UTF16PtrFromString(abs)
}

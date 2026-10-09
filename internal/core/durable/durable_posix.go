//go:build linux || darwin

package durable

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// LockFile opens path, creating it with mode 0o600 if missing, and locks it
// exclusively without waiting. It returns an error matching ErrLocked when
// another open file description, in this process or another, holds the lock.
func LockFile(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
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
	err := flock(l.f, unix.LOCK_UN)
	if err != nil {
		err = &os.PathError{Op: "unlock", Path: l.f.Name(), Err: err}
	}
	return errors.Join(err, l.f.Close())
}

// flock applies how to the file, retrying when a signal interrupts the call.
func flock(f *os.File, how int) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var flockErr error
	err = rc.Control(func(fd uintptr) {
		for {
			if flockErr = unix.Flock(int(fd), how); flockErr != unix.EINTR {
				return
			}
		}
	})
	if err != nil {
		return err
	}
	return flockErr
}

// SyncDir flushes the entries of directory dir to stable storage.
func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// Rename atomically replaces newpath with oldpath and then flushes
// newpath's directory.
func Rename(oldpath, newpath string) error {
	if err := os.Rename(oldpath, newpath); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(newpath))
}

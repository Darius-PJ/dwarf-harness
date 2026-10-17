// Package durable is Core's narrow adapter to the operating system's file
// locking and directory durability (B3). Other recorded Core OS adapters may
// also import golang.org/x/sys; this package owns only durability and locking.
//
// LockFile takes an exclusive, nonblocking lock that the operating system
// releases when its holder closes it or dies. SyncDir flushes a directory's
// entries, so that a file created or renamed in it survives a crash once the
// call returns. Rename replaces its target atomically and then flushes the
// target's directory. File data is flushed with os.File.Sync, which is the
// platform flush on every supported system, so this package has no file
// flush of its own.
//
// Windows uses LockFileEx on the file's first byte, FlushFileBuffers on a
// directory handle opened with FILE_FLAG_BACKUP_SEMANTICS, and MoveFileEx
// with MOVEFILE_REPLACE_EXISTING and MOVEFILE_WRITE_THROUGH. Linux and macOS
// use flock, fsync of the directory (F_FULLFSYNC on macOS) and rename(2).
//
// On Windows, SyncDir and Rename make their paths absolute and pass them in
// the extended-length \\?\ form, so neither is limited to MAX_PATH. LockFile
// opens its file through package os, which handles long paths itself.
//
// Every error is returned; nothing falls back silently. None of this is a
// power-loss guarantee: it relies on the file system and the device honouring
// their flushes, and only Windows on NTFS has been exercised (B3).
package durable

import (
	"errors"
	"os"
)

// ErrLocked reports that another holder has the lock.
var ErrLocked = errors.New("durable: file is locked by another holder")

// Lock is an exclusive lock on a file, held from LockFile until Close or
// the holder's death.
type Lock struct {
	f *os.File
}

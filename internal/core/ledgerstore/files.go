package ledgerstore

import (
	"io"
	"os"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

// fileSystem is the store's access to segment files, body files and directory
// flushes. osFiles is the only production implementation; internal tests wrap
// it to inject faults.
type fileSystem interface {
	// createSegment creates the segment file at path, which must not exist,
	// for writing.
	createSegment(path string) (segmentFile, error)
	// openSegment opens the existing segment file at path for writing.
	openSegment(path string) (segmentFile, error)
	// syncDir flushes the entries of directory dir.
	syncDir(dir string) error
	// createTemp creates a new temporary file in dir for a body publication.
	createTemp(dir string) (bodyFile, error)
	// rename publishes the file at from under the name to and flushes the
	// directory of to.
	rename(from, to string) error
	// remove removes the file at path.
	remove(path string) error
	// openBody opens the body file at path for reading.
	openBody(path string) (io.ReadCloser, error)
}

// segmentFile is an open segment.
type segmentFile interface {
	WriteAt(b []byte, off int64) (int, error)
	Sync() error
	Truncate(size int64) error
	Close() error
}

// bodyFile is the temporary file of a body publication.
type bodyFile interface {
	Write(b []byte) (int, error)
	Sync() error
	Close() error
	Name() string
}

// osFiles is the real file system: package os for files and package durable
// for directory flushes and renames. os.File.Sync is the platform's file
// flush.
type osFiles struct{}

func (osFiles) createSegment(path string) (segmentFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (osFiles) openSegment(path string) (segmentFile, error) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (osFiles) syncDir(dir string) error { return durable.SyncDir(dir) }

func (osFiles) createTemp(dir string) (bodyFile, error) {
	f, err := os.CreateTemp(dir, "body-*")
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (osFiles) rename(from, to string) error { return durable.Rename(from, to) }

func (osFiles) remove(path string) error { return os.Remove(path) }

func (osFiles) openBody(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

package ledgerstore

import (
	"os"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

// fileSystem is the writer's access to segment files and directory flushes.
// osFiles is the only production implementation; internal tests wrap it to
// inject faults.
type fileSystem interface {
	// createSegment creates the segment file at path, which must not exist,
	// for writing.
	createSegment(path string) (segmentFile, error)
	// openSegment opens the existing segment file at path for writing.
	openSegment(path string) (segmentFile, error)
	// syncDir flushes the entries of directory dir.
	syncDir(dir string) error
}

// segmentFile is an open segment.
type segmentFile interface {
	WriteAt(b []byte, off int64) (int, error)
	Sync() error
	Truncate(size int64) error
	Close() error
}

// osFiles is the real file system: package os for files and package durable
// for directory flushes. os.File.Sync is the platform's file flush.
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

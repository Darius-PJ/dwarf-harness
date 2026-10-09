package ledger

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalid marks a refusal by EncodeCommit, or an invalid argument to
	// Check or OpenReader. Nothing is written.
	ErrInvalid = errors.New("ledger: invalid record")
	// ErrTooLarge marks an EncodeCommit refusal for a line over
	// MaxRecordBytes or a commit over MaxCommitBytes.
	ErrTooLarge = errors.New("ledger: record or commit over cap")
	// ErrCorrupt marks a log that fails closed; see CorruptError.
	ErrCorrupt = errors.New("ledger: corrupt log")
	// ErrUnsupportedFormat marks a record whose format is not FormatVersion;
	// see UnsupportedFormatError.
	ErrUnsupportedFormat = errors.New("ledger: unsupported format version")
	// ErrUnsupportedVersion marks a consumed type at an undeclared version;
	// see UnsupportedVersionError.
	ErrUnsupportedVersion = errors.New("ledger: unsupported type version")
	// ErrCursor marks a reader bound that is not the end of a commit.
	ErrCursor = errors.New("ledger: cursor or bound not at a commit end")
)

// CorruptError locates a fail-closed finding. Segment is the file name in
// the log directory, or "" for the directory itself, and Offset is the byte
// offset in Segment where the finding begins. errors.Is(err, ErrCorrupt)
// holds.
type CorruptError struct {
	Segment string
	Offset  int64
	Reason  string
}

func (e *CorruptError) Error() string {
	if e.Segment == "" {
		return fmt.Sprintf("%v: log directory: %s", ErrCorrupt, e.Reason)
	}
	return fmt.Sprintf("%v: %s at offset %d: %s", ErrCorrupt, e.Segment, e.Offset, e.Reason)
}

func (e *CorruptError) Unwrap() error { return ErrCorrupt }

// UnsupportedFormatError locates a correctly hashed record whose format is
// not FormatVersion. errors.Is(err, ErrUnsupportedFormat) holds.
type UnsupportedFormatError struct {
	Segment string
	Offset  int64
	Format  int
}

func (e *UnsupportedFormatError) Error() string {
	return fmt.Sprintf("%v: %s at offset %d: format %d, this reader supports %d",
		ErrUnsupportedFormat, e.Segment, e.Offset, e.Format, FormatVersion)
}

func (e *UnsupportedFormatError) Unwrap() error { return ErrUnsupportedFormat }

// UnsupportedVersionError reports a record of a consumed type at a version
// the reader did not declare. errors.Is(err, ErrUnsupportedVersion) holds.
type UnsupportedVersionError struct {
	Seq       uint64
	Type      string
	Version   int
	Supported []int
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("%v: seq %d: type %s version %d, consumed versions %v",
		ErrUnsupportedVersion, e.Seq, e.Type, e.Version, e.Supported)
}

func (e *UnsupportedVersionError) Unwrap() error { return ErrUnsupportedVersion }

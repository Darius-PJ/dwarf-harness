package ledger

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"uuid"
)

// ToEnd as ReadOptions.Through reads every complete commit on disk. It is for
// offline reading: while a writer is live it may expose a commit the writer
// has not yet acknowledged.
const ToEnd = ^uint64(0)

// Consumption maps each consumed type to the type versions the reader
// decodes.
type Consumption map[string][]int

// ReadOptions bound and filter a Reader.
type ReadOptions struct {
	Colony  uuid.UUID   // required; every record must carry it
	After   uint64      // last sequence already consumed; 0 = from the start
	Through uint64      // last sequence to read: the writer's durable mark, or ToEnd
	Consume Consumption // the consumed types; records of other types are skipped
}

// Commit is one complete commit as a Reader returns it.
type Commit struct {
	ID      uuid.UUID
	First   uint64   // seq of the commit's first record
	Last    uint64   // seq of its last record
	Records []Record // its consumed records only, in seq order
}

// Reader returns the complete commits of a log in order, verifying every
// record it reads. It is read-only and not safe for concurrent use.
type Reader struct {
	opts   ReadOptions
	s      *scanner
	cursor uint64
	err    error // sticky
}

// OpenReader opens the log in logDir for reading after opts.After. It
// verifies the link into the segment holding opts.After+1 from the last line
// of the segment before it.
func OpenReader(logDir string, opts ReadOptions) (*Reader, error) {
	if !isV7(opts.Colony) {
		return nil, fmt.Errorf("%w: colony ID %s is not a UUIDv7", ErrInvalid, opts.Colony)
	}
	if opts.Through != ToEnd && opts.After > opts.Through {
		return nil, fmt.Errorf("%w: After %d is beyond Through %d", ErrCursor, opts.After, opts.Through)
	}
	segs, err := listSegments(logDir)
	if err != nil {
		return nil, err
	}
	k := 0 // the segment holding After+1, if the log reaches it
	for i, seg := range segs {
		if seg.first-1 <= opts.After {
			k = i
		}
	}
	start := Genesis()
	if k > 0 {
		if start, err = lastRecord(logDir, segs[k-1], opts.Colony); err != nil {
			return nil, err
		}
		// Checked here too because a reader already at Through never opens
		// segs[k].
		if segs[k].first != start.Seq+1 {
			return nil, &CorruptError{Segment: segs[k].name, Reason: fmt.Sprintf("segment name does not continue the sequence after seq %d", start.Seq)}
		}
	}
	keep := func(e *Envelope) bool {
		_, ok := opts.Consume[e.Type]
		return ok
	}
	return &Reader{
		opts:   opts,
		s:      newScanner(logDir, segs[k:], opts.Colony, start, opts.Through == ToEnd, keep),
		cursor: opts.After,
	}, nil
}

// lastRecord verifies the last line of a segment before the one a Reader
// starts in and returns the chain position after it.
func lastRecord(dir string, seg segment, colony uuid.UUID) (Chain, error) {
	f, err := os.Open(filepath.Join(dir, seg.name))
	if err != nil {
		return Chain{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Chain{}, err
	}
	size := fi.Size()
	if size == 0 {
		return Chain{}, &CorruptError{Segment: seg.name, Reason: "empty segment before the final one"}
	}
	// The window holds the last line and the LF before it, if the line is
	// within the cap.
	buf := make([]byte, min(size, MaxRecordBytes+1))
	base := size - int64(len(buf))
	if _, err := f.ReadAt(buf, base); err != nil {
		return Chain{}, err
	}
	at := bytes.LastIndexByte(buf[:len(buf)-1], '\n') + 1
	off := base + int64(at)
	line := buf[at:]
	switch {
	case buf[len(buf)-1] != '\n':
		return Chain{}, &CorruptError{Segment: seg.name, Offset: off, Reason: "segment ends inside a line"}
	case len(line) > MaxRecordBytes:
		return Chain{}, &CorruptError{Segment: seg.name, Offset: off, Reason: errLong.Error()}
	}
	js, ok := frameLine(line)
	if !ok {
		return Chain{}, &CorruptError{Segment: seg.name, Offset: off, Reason: "line framing or hash is invalid"}
	}
	var compact bytes.Buffer
	e, _, err := decodeEnvelope(js, &compact)
	switch {
	case err != nil:
		return Chain{}, lineError(seg.name, off, err)
	case e.ColonyID != colony:
		return Chain{}, &CorruptError{Segment: seg.name, Offset: off, Reason: "colony_id is not this colony's"}
	case e.CommitIndex != e.CommitCount-1:
		return Chain{}, &CorruptError{Segment: seg.name, Offset: off, Reason: "segment ends inside a commit"}
	}
	return Chain{Seq: e.Seq, Hash: string(line[:2*sha256.Size]), EventTimeNS: e.EventTimeNS}, nil
}

// Next returns the next commit with a consumed record. Commits with none are
// skipped, and Cursor still advances past them. It returns io.EOF after
// Through, or with ToEnd after the last complete commit; a torn final commit
// is not returned. It returns an *UnsupportedVersionError for a consumed type
// at an undeclared version, ErrCursor when After or Through falls inside a
// commit or After lies beyond the log, and ErrCorrupt when the log fails
// closed or ends before a numeric Through. Errors are final.
func (r *Reader) Next() (Commit, error) {
	if r.err != nil {
		return Commit{}, r.err
	}
	c, err := r.next()
	if err != nil {
		r.err = err
		r.s.close()
	}
	return c, err
}

func (r *Reader) next() (Commit, error) {
	for {
		if r.opts.Through != ToEnd && r.s.v.done.Seq >= r.opts.Through {
			return Commit{}, io.EOF
		}
		c, err := r.s.next()
		if err == io.EOF {
			end := r.s.v.done.Seq
			switch {
			case r.opts.Through != ToEnd:
				seg := ""
				if len(r.s.segs) > 0 {
					seg = r.s.segs[len(r.s.segs)-1].name
				}
				return Commit{}, &CorruptError{Segment: seg, Offset: r.s.end, Reason: fmt.Sprintf("log ends at seq %d before Through %d", end, r.opts.Through)}
			case end < r.opts.After:
				return Commit{}, fmt.Errorf("%w: After %d is beyond the log's last commit at seq %d", ErrCursor, r.opts.After, end)
			}
			return Commit{}, io.EOF
		}
		if err != nil {
			return Commit{}, err
		}
		switch {
		case c.Last <= r.opts.After:
			continue
		case c.First <= r.opts.After:
			return Commit{}, fmt.Errorf("%w: After %d is inside commit %d-%d", ErrCursor, r.opts.After, c.First, c.Last)
		case r.opts.Through != ToEnd && c.Last > r.opts.Through:
			return Commit{}, fmt.Errorf("%w: Through %d is inside commit %d-%d", ErrCursor, r.opts.Through, c.First, c.Last)
		}
		for _, rec := range c.Records {
			if vs := r.opts.Consume[rec.Type]; !slices.Contains(vs, rec.TypeVersion) {
				return Commit{}, &UnsupportedVersionError{Seq: rec.Seq, Type: rec.Type, Version: rec.TypeVersion, Supported: slices.Clone(vs)}
			}
		}
		r.cursor = c.Last
		if len(c.Records) > 0 {
			return c, nil
		}
	}
}

// Cursor returns the last sequence of the last commit fully processed,
// whether skipped or returned; before the first, it is After. A consumer may
// persist it as its next After.
func (r *Reader) Cursor() uint64 { return r.cursor }

// Close releases the open segment file. Next then fails with an error
// wrapping os.ErrClosed, unless it had already failed or reached the end.
func (r *Reader) Close() error {
	r.s.close()
	if r.err == nil {
		r.err = fmt.Errorf("ledger: reader closed: %w", os.ErrClosed)
	}
	return nil
}

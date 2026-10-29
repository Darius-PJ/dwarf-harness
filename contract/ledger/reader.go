package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
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

	// AllTypes also returns the records of types absent from Consume, for
	// their envelope fields. Their versions are not checked, and their
	// payloads are not the consumer's to decode: a consumer decodes only the
	// payloads of the type versions it consumes. Types in Consume are still
	// version-checked.
	AllTypes bool
}

// Commit is one complete commit as a Reader returns it.
type Commit struct {
	ID      uuid.UUID
	First   uint64   // seq of the commit's first record
	Last    uint64   // seq of its last record
	Records []Record // its consumed records, or all with AllTypes, in seq order
}

// ReadCheckpoint is an opaque, process-local proof of a Reader's completed
// position. Only Reader.Checkpoint creates a usable value. A zero or damaged
// value is a hint miss, not an error. It has no persistent encoding.
type ReadCheckpoint struct {
	version      uint64
	directory    [32]byte
	colony       [16]byte
	segmentFirst uint64
	offset       int64
	baseSeq      uint64
	baseHash     [32]byte
	baseTime     int64
	afterSeq     uint64
	afterHash    [32]byte
	afterTime    int64
	prefix       [32]byte
	checksum     [32]byte
}

// sum detects accidental damage, not hostile same-process reconstruction.
func (p *ReadCheckpoint) sum() [32]byte {
	var b [200]byte
	binary.LittleEndian.PutUint64(b[0:8], p.version)
	copy(b[8:40], p.directory[:])
	copy(b[40:56], p.colony[:])
	binary.LittleEndian.PutUint64(b[56:64], p.segmentFirst)
	binary.LittleEndian.PutUint64(b[64:72], uint64(p.offset))
	binary.LittleEndian.PutUint64(b[72:80], p.baseSeq)
	copy(b[80:112], p.baseHash[:])
	binary.LittleEndian.PutUint64(b[112:120], uint64(p.baseTime))
	binary.LittleEndian.PutUint64(b[120:128], p.afterSeq)
	copy(b[128:160], p.afterHash[:])
	binary.LittleEndian.PutUint64(b[160:168], uint64(p.afterTime))
	copy(b[168:200], p.prefix[:])
	return sha256.Sum256(b[:])
}

func (p *ReadCheckpoint) baseChain() Chain {
	return Chain{Seq: p.baseSeq, Hash: hex.EncodeToString(p.baseHash[:]), EventTimeNS: p.baseTime}
}

func (p *ReadCheckpoint) afterChain() Chain {
	return Chain{Seq: p.afterSeq, Hash: hex.EncodeToString(p.afterHash[:]), EventTimeNS: p.afterTime}
}

func (p *ReadCheckpoint) matches(directory [32]byte, opts ReadOptions, seg segment, base Chain) bool {
	if p == nil || p.version != 1 || p.checksum != p.sum() {
		return false
	}
	if opts.Through == ToEnd || opts.After >= opts.Through ||
		p.directory != directory || p.colony != [16]byte(opts.Colony) ||
		p.afterSeq != opts.After || p.segmentFirst != seg.first ||
		p.segmentFirst == 0 || p.baseSeq != p.segmentFirst-1 ||
		p.offset <= 0 || p.afterSeq < p.segmentFirst {
		return false
	}
	if p.baseChain() != base {
		return false
	}
	return base.valid() && p.afterChain().valid() && p.afterTime >= p.baseTime
}

// Reader returns the complete commits of a log in order, verifying every
// record it reads. It is read-only and not safe for concurrent use.
type Reader struct {
	opts      ReadOptions
	s         *scanner
	cursor    uint64
	err       error    // sticky
	directory [32]byte // optional path identity; s.prefix != nil enables capture
}

// OpenReader opens the log in logDir for reading after opts.After. It
// verifies the link into the segment holding opts.After+1 from the last line
// of the segment before it.
func OpenReader(logDir string, opts ReadOptions) (*Reader, error) {
	return openReader(logDir, opts, nil, false)
}

// OpenReaderWithCheckpoint opens a Reader with checked file closure and
// optional checkpoint reuse. A missing, invalid or changed hint uses ordinary
// scanning, still with checked closure. A usable hint avoids decoding its
// prefix again only after freshly hashing every prefix byte and checking the
// preceding segment's chain. Prefix hashing and suffix reading use one file.
// I/O, seek and actual close failures are errors, not hint misses.
func OpenReaderWithCheckpoint(logDir string, opts ReadOptions, checkpoint *ReadCheckpoint) (*Reader, error) {
	return openReader(logDir, opts, checkpoint, true)
}

func openReader(logDir string, opts ReadOptions, checkpoint *ReadCheckpoint, checkedClose bool) (*Reader, error) {
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
		if start, err = lastRecord(logDir, segs[k-1], opts.Colony, checkedClose); err != nil {
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
		return ok || opts.AllTypes
	}
	r := &Reader{
		opts:   opts,
		s:      newScanner(logDir, segs[k:], opts.Colony, start, opts.Through == ToEnd, keep),
		cursor: opts.After,
	}
	r.s.checkedClose = checkedClose
	if checkedClose && opts.Through != ToEnd && opts.After < opts.Through {
		if dir, err := filepath.Abs(logDir); err == nil {
			r.directory = sha256.Sum256([]byte(filepath.Clean(dir)))
			r.s.prefix = sha256.New()
			if len(segs) > 0 && checkpoint.matches(r.directory, opts, segs[k], start) {
				restored, err := r.s.restore(checkpoint)
				if err != nil {
					return nil, err
				}
				if !restored {
					// The candidate was closed successfully. Re-list and
					// recheck the anchor before the ordinary cold scan.
					return openReader(logDir, opts, nil, true)
				}
			}
		}
	}
	return r, nil
}

// lastRecord verifies the last line of a segment before the one a Reader
// starts in and returns the chain position after it.
func lastRecord(dir string, seg segment, colony uuid.UUID, checkedClose bool) (last Chain, err error) {
	f, err := os.Open(filepath.Join(dir, seg.name))
	if err != nil {
		return Chain{}, err
	}
	defer func() {
		closeErr := f.Close()
		if checkedClose {
			err = readCloseError(err, closeErr)
		}
	}()
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

// Next returns the next commit with a consumed record, or with AllTypes the
// next commit. Commits with none are skipped, and Cursor still advances past
// them. It returns io.EOF after Through, or with ToEnd after the last
// complete commit; a torn final commit is not returned. It returns an
// *UnsupportedVersionError for a consumed type at an undeclared version,
// ErrCursor when After or Through falls inside a commit or After lies beyond
// the log, and ErrCorrupt when the log fails closed or ends before a numeric
// Through. Errors are final.
func (r *Reader) Next() (Commit, error) {
	if r.err != nil {
		return Commit{}, r.err
	}
	c, err := r.next()
	if err != nil {
		err = readCloseError(err, r.s.close())
		r.err = err
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
			if vs, ok := r.opts.Consume[rec.Type]; ok && !slices.Contains(vs, rec.TypeVersion) {
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
// For OpenReaderWithCheckpoint it returns the first actual close failure,
// including one from automatic closure; repeated calls retain that failure.
func (r *Reader) Close() error {
	closeErr := r.s.close()
	if r.err == nil {
		r.err = fmt.Errorf("ledger: reader closed: %w", os.ErrClosed)
	}
	if r.s.checkedClose {
		r.err = readCloseError(r.err, closeErr)
		return closeErr
	}
	return nil
}

// Checkpoint returns a fresh copy of the proof of this Reader's completed
// numeric Through, or nil. It requires terminal EOF and successful actual
// closure. ToEnd, equal bounds, early Close and any read or close failure
// cannot issue a proof. OpenReader never issues one.
func (r *Reader) Checkpoint() *ReadCheckpoint {
	s := r.s
	if !s.checkedClose || s.prefix == nil || r.err != io.EOF || s.closeErr != nil || s.f != nil ||
		r.opts.Through == ToEnd || r.opts.After >= r.opts.Through ||
		r.cursor != r.opts.Through || s.v.done.Seq != r.cursor ||
		s.v.next != 0 || s.v.cur != s.v.done || !s.v.done.valid() || !s.base.valid() ||
		s.end <= 0 || s.end != s.lr.off || s.i >= len(s.segs) {
		return nil
	}
	seg := s.segs[s.i]
	if seg.first == 0 || s.base.Seq != seg.first-1 || s.v.done.Seq < seg.first {
		return nil
	}
	p := &ReadCheckpoint{
		version: 1, directory: r.directory, colony: [16]byte(r.opts.Colony),
		segmentFirst: seg.first, offset: s.end,
		baseSeq: s.base.Seq, baseTime: s.base.EventTimeNS,
		afterSeq: s.v.done.Seq, afterTime: s.v.done.EventTimeNS,
	}
	if _, err := hex.Decode(p.baseHash[:], []byte(s.base.Hash)); err != nil {
		return nil
	}
	if _, err := hex.Decode(p.afterHash[:], []byte(s.v.done.Hash)); err != nil {
		return nil
	}
	s.prefix.Sum(p.prefix[:0])
	p.checksum = p.sum()
	return p
}

// readCloseError never hides a close failure behind an EOF success branch.
func readCloseError(err, closeErr error) error {
	if closeErr == nil || errors.Is(err, closeErr) {
		return err
	}
	if err == nil || err == io.EOF {
		return closeErr
	}
	return errors.Join(err, closeErr)
}

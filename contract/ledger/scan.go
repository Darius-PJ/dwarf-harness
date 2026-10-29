package ledger

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"uuid"
)

// CheckResult reports where a verified log ends.
type CheckResult struct {
	Last      Chain  // end of the last complete commit (Genesis() if none)
	Segment   string // final segment file name; "" if the log has no segments
	Size      int64  // bytes of complete commits in Segment
	TornBytes int64  // bytes after Size that form a torn final commit
}

// Check verifies the whole log in logDir, read-only, for colony and reports
// where it ends. It never writes, truncates or locks. It fails closed with a
// *CorruptError, or an *UnsupportedFormatError, on anything but complete,
// correctly hashed and chained commits in order followed by at most one torn
// final commit (see the package documentation).
func Check(logDir string, colony uuid.UUID) (CheckResult, error) {
	if !isV7(colony) {
		return CheckResult{}, fmt.Errorf("%w: colony ID %s is not a UUIDv7", ErrInvalid, colony)
	}
	segs, err := listSegments(logDir)
	if err != nil {
		return CheckResult{}, err
	}
	s := newScanner(logDir, segs, colony, Genesis(), true, nil)
	defer s.close()
	for {
		if _, err := s.next(); err == io.EOF {
			break
		} else if err != nil {
			return CheckResult{}, err
		}
	}
	res := CheckResult{Last: s.v.done}
	if len(segs) > 0 {
		res.Segment = segs[len(segs)-1].name
		res.Size = s.end
		res.TornBytes = s.torn
	}
	return res, nil
}

// scanner reads segments in order and yields complete, verified commits.
type scanner struct {
	dir     string
	segs    []segment
	i       int      // index of the open segment, or of the next one to open
	f       *os.File // the open segment, or nil
	lr      lineReader
	v       verifier
	tail    bool                 // apply the torn-tail rule at the end of the final segment
	keep    func(*Envelope) bool // records to return; nil keeps none
	compact bytes.Buffer
	eof     bool

	end  int64 // offset after the last complete commit in the open or final segment
	torn int64 // bytes of the torn final commit
	cur  Commit

	checkedClose bool
	closeErr     error     // first actual close failure, retained in opt-in mode
	prefix       hash.Hash // hashes logically consumed framed lines; nil in legacy mode
	base         Chain     // freshly verified chain before the current segment
}

func newScanner(dir string, segs []segment, colony uuid.UUID, start Chain, tail bool, keep func(*Envelope) bool) *scanner {
	return &scanner{
		dir:  dir,
		segs: segs,
		v:    verifier{colony: colony, done: start, cur: start},
		tail: tail,
		keep: keep,
	}
}

func (s *scanner) close() error {
	if s.f != nil {
		err := s.f.Close()
		s.f = nil
		if s.checkedClose && s.closeErr == nil {
			s.closeErr = err
		}
	}
	return s.closeErr
}

func (s *scanner) corrupt(off int64, reason string) error {
	return &CorruptError{Segment: s.segs[s.i].name, Offset: off, Reason: reason}
}

// next returns the next complete commit, with the records keep selects. It
// returns io.EOF after the last complete commit of the final segment, once
// any bytes after it pass the torn-tail rule (tail mode) or when there are
// none.
func (s *scanner) next() (Commit, error) {
	if s.eof {
		return Commit{}, io.EOF
	}
	c, err := s.scan()
	if err != nil {
		s.eof = true
		err = readCloseError(err, s.close())
	}
	return c, err
}

func (s *scanner) scan() (Commit, error) {
	for {
		if s.f == nil {
			if s.i == len(s.segs) {
				return Commit{}, io.EOF
			}
			if err := s.open(); err != nil {
				return Commit{}, err
			}
		}
		final := s.i == len(s.segs)-1
		torn := final && s.tail
		line, off, err := s.lr.next()
		switch {
		case err == io.EOF:
			switch {
			case s.v.next != 0 && torn:
				return Commit{}, s.tornTail()
			case s.v.next != 0:
				return Commit{}, s.corrupt(s.end, "segment ends inside a commit")
			case final:
				return Commit{}, io.EOF
			case off == 0:
				return Commit{}, s.corrupt(0, "empty segment before the final one")
			}
			if err := s.close(); err != nil {
				return Commit{}, err
			}
			s.i++
			continue
		case err == errPartial || err == errLong:
			if torn {
				return Commit{}, s.tornTail()
			}
			return Commit{}, s.corrupt(off, err.Error())
		case err != nil:
			return Commit{}, err
		}
		if s.prefix != nil {
			// Hash at logical consumption, not buffered read-ahead, before
			// the next line can reuse this storage.
			s.prefix.Write(line)
		}

		js, ok := frameLine(line)
		if !ok {
			if torn {
				return Commit{}, s.tornTail()
			}
			return Commit{}, s.corrupt(off, "line framing or hash is invalid")
		}
		// A correctly hashed line that does not decode fails closed even in
		// a torn tail, so it is reported here directly.
		e, at, err := decodeEnvelope(js, &s.compact)
		if err != nil {
			return Commit{}, lineError(s.segs[s.i].name, off, err)
		}
		hash := string(line[:2*sha256.Size])
		if s.v.next == 0 {
			s.cur = Commit{ID: e.CommitID, First: e.Seq}
		}
		done, reason := s.v.accept(&e, hash, len(line))
		if reason != "" {
			if torn {
				return Commit{}, s.tornTail()
			}
			return Commit{}, s.corrupt(off, reason)
		}
		if s.keep != nil && s.keep(&e) {
			rec := Record{Envelope: e, Hash: hash, JSON: bytes.Clone(js)}
			rec.Payload = rec.JSON[at : at+len(e.Payload) : at+len(e.Payload)]
			s.cur.Records = append(s.cur.Records, rec)
		}
		if done {
			s.end = s.lr.off
			s.cur.Last = e.Seq
			c := s.cur
			s.cur = Commit{}
			return c, nil
		}
	}
}

// open opens segment s.i, whose name must continue the sequence.
func (s *scanner) open() error {
	seg := s.segs[s.i]
	if seg.first != s.v.done.Seq+1 {
		return s.corrupt(0, fmt.Sprintf("segment name does not continue the sequence after seq %d", s.v.done.Seq))
	}
	f, err := os.Open(filepath.Join(s.dir, seg.name))
	if err != nil {
		return err
	}
	s.f = f
	s.lr.reset(f)
	s.end = 0
	if s.prefix != nil {
		s.prefix.Reset()
		s.base = s.v.done
	}
	return nil
}

// restore checks the prefix on the same descriptor used for suffix reading.
// A byte mismatch is a miss only after the candidate closes successfully.
func (s *scanner) restore(p *ReadCheckpoint) (bool, error) {
	if err := s.open(); err != nil {
		return false, err
	}
	n, err := io.CopyBuffer(s.prefix, io.LimitReader(s.f, p.offset), make([]byte, 64<<10))
	if err != nil {
		return false, readCloseError(err, s.close())
	}
	var prefix [32]byte
	s.prefix.Sum(prefix[:0])
	if n != p.offset || prefix != p.prefix {
		return false, s.close()
	}
	if _, err := s.f.Seek(p.offset, io.SeekStart); err != nil {
		return false, readCloseError(err, s.close())
	}
	s.lr.reset(s.f)
	s.lr.off, s.end = p.offset, p.offset
	after := p.afterChain()
	s.v = verifier{colony: s.v.colony, done: after, cur: after}
	s.cur = Commit{}
	// s.base stays the fresh predecessor, not the restored after-chain.
	return true, nil
}

// tornTail applies the torn-tail rule to the bytes from s.end to the end of
// the final segment. It returns io.EOF when they form a torn final commit.
func (s *scanner) tornTail() error {
	r, err := io.ReadAll(io.NewSectionReader(s.f, s.end, MaxCommitBytes+1))
	if err != nil {
		return err
	}
	if len(r) > MaxCommitBytes {
		return s.corrupt(s.end, "bytes after the last complete commit exceed MaxCommitBytes")
	}
	if err := checkTorn(r, s.segs[s.i].name, s.end, s.v.done, s.v.colony, &s.compact); err != nil {
		return err
	}
	s.torn = int64(len(r))
	return io.EOF
}

// checkTorn reports whether r, found at offset base of segment seg after
// the last complete commit last, can be the torn remains of the one commit
// in flight. Chunks that are not correctly hashed lines are permitted. Every
// correctly hashed line must decode and fit that commit; a line that does
// not fails closed.
func checkTorn(r []byte, seg string, base int64, last Chain, colony uuid.UUID, compact *bytes.Buffer) error {
	var (
		first     Envelope // the first member line found
		members   int
		prevIndex = -1
		prevHash  string
	)
	for pos := 0; ; {
		n := bytes.IndexByte(r[pos:], '\n')
		if n < 0 {
			return nil // a partial line ends r
		}
		chunk := r[pos : pos+n+1]
		start := pos
		pos += n + 1

		// A line's JSON holds no TAB once compact, so a correctly hashed line
		// in the chunk is the whole chunk or begins 64 bytes before its last
		// TAB, behind damaged bytes.
		at := -1
		if _, ok := frameLine(chunk); ok {
			at = 0
		} else if t := bytes.LastIndexByte(chunk, '\t'); t > 2*sha256.Size {
			if _, ok := frameLine(chunk[t-2*sha256.Size:]); ok {
				at = t - 2*sha256.Size
			}
		}
		if at < 0 {
			continue
		}
		line := chunk[at:]
		off := base + int64(start+at)
		fail := func(reason string) error {
			return &CorruptError{Segment: seg, Offset: off, Reason: reason + " in the bytes after the last complete commit"}
		}
		if len(line) > MaxRecordBytes {
			return fail("line over MaxRecordBytes")
		}
		e, _, err := decodeEnvelope(line[2*sha256.Size+1:len(line)-1], compact)
		if err != nil {
			return lineError(seg, off, err)
		}
		if members == 0 {
			first = e
		}
		switch {
		case e.ColonyID != colony:
			return fail("colony_id is not this colony's")
		case e.CommitID != first.CommitID || e.CommitCount != first.CommitCount:
			return fail("record of a second commit")
		case e.CommitIndex <= prevIndex:
			return fail("commit_index does not increase")
		case e.Seq != last.Seq+1+uint64(e.CommitIndex):
			return fail("seq does not match commit_index")
		case e.CommitIndex == 0 && off != base:
			return fail("first record of a commit not at the commit's start")
		case e.CommitIndex == 0 && e.PrevHash != last.Hash:
			return fail("prev_hash does not link")
		case e.CommitIndex == prevIndex+1 && members > 0 && e.PrevHash != prevHash:
			return fail("prev_hash does not link")
		case e.EventTimeNS != first.EventTimeNS || e.ClockHeld != first.ClockHeld:
			return fail("event time differs within a commit")
		case e.EventTimeNS < last.EventTimeNS:
			return fail("event_time_ns decreases")
		case e.ClockHeld && e.EventTimeNS != last.EventTimeNS:
			return fail("clock_held with a time other than the previous record's")
		}
		members++
		prevIndex = e.CommitIndex
		prevHash = string(line[:2*sha256.Size])
	}
}

// lineError locates the decoding error of a correctly hashed line.
func lineError(seg string, off int64, err error) error {
	var fe *formatError
	if errors.As(err, &fe) {
		return &UnsupportedFormatError{Segment: seg, Offset: off, Format: fe.format}
	}
	return &CorruptError{Segment: seg, Offset: off, Reason: err.Error()}
}

// verifier checks that records continue a chain in whole commits.
type verifier struct {
	colony uuid.UUID
	done   Chain // after the last complete commit
	cur    Chain // after the last accepted record

	// The open commit; next is 0 when none is open.
	id    uuid.UUID
	count int
	next  int
	held  bool
	size  int // bytes of the open commit's lines
}

// accept checks that e, with hash and a line of n bytes, continues the chain.
// It returns whether e completes its commit, or the rule e breaks.
func (v *verifier) accept(e *Envelope, hash string, n int) (bool, string) {
	switch {
	case e.ColonyID != v.colony:
		return false, "colony_id is not this colony's"
	case e.Seq != v.cur.Seq+1:
		return false, fmt.Sprintf("seq %d does not follow %d", e.Seq, v.cur.Seq)
	case e.PrevHash != v.cur.Hash:
		return false, "prev_hash does not link"
	}
	if v.next == 0 {
		switch {
		case e.CommitIndex != 0:
			return false, "commit starts at a commit_index other than 0"
		case e.EventTimeNS < v.cur.EventTimeNS:
			return false, "event_time_ns decreases"
		case e.ClockHeld && e.EventTimeNS != v.cur.EventTimeNS:
			return false, "clock_held with a time other than the previous record's"
		}
		v.id, v.count, v.held, v.size = e.CommitID, e.CommitCount, e.ClockHeld, 0
	} else {
		switch {
		case e.CommitIndex != v.next:
			return false, "commit_index does not follow"
		case e.CommitID != v.id || e.CommitCount != v.count:
			return false, "commit ends early"
		case e.EventTimeNS != v.cur.EventTimeNS || e.ClockHeld != v.held:
			return false, "event time differs within a commit"
		}
	}
	v.size += n
	if v.size > MaxCommitBytes {
		return false, "commit over MaxCommitBytes"
	}
	v.cur = Chain{Seq: e.Seq, Hash: hash, EventTimeNS: e.EventTimeNS}
	v.next++
	if v.next < v.count {
		return false, ""
	}
	v.next = 0
	v.done = v.cur
	return true, ""
}

var (
	errPartial = errors.New("partial line at the end of the file")
	errLong    = errors.New("line over MaxRecordBytes")
)

// lineReader reads the LF-terminated lines of a segment.
type lineReader struct {
	br  *bufio.Reader
	off int64 // offset of the next line
	buf []byte
}

func (lr *lineReader) reset(r io.Reader) {
	if lr.br == nil {
		lr.br = bufio.NewReaderSize(r, 64<<10)
	} else {
		lr.br.Reset(r)
	}
	lr.off = 0
}

// next returns the next line, LF included, and its offset. The line is valid
// until the next call. It returns io.EOF at the end of the file, errPartial
// for bytes after the last LF and errLong for a line over MaxRecordBytes.
func (lr *lineReader) next() (line []byte, off int64, err error) {
	off = lr.off
	lr.buf = lr.buf[:0]
	for {
		chunk, err := lr.br.ReadSlice('\n')
		if len(lr.buf)+len(chunk) > MaxRecordBytes {
			return nil, off, errLong
		}
		switch {
		case err == nil && len(lr.buf) == 0:
			line = chunk
		case err == nil:
			line = append(lr.buf, chunk...)
			lr.buf = line
		case err == bufio.ErrBufferFull:
			lr.buf = append(lr.buf, chunk...)
			continue
		case err == io.EOF && len(lr.buf)+len(chunk) == 0:
			return nil, off, io.EOF
		case err == io.EOF:
			return nil, off, errPartial
		default:
			return nil, off, err
		}
		lr.off += int64(len(line))
		return line, off, nil
	}
}

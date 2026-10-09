// Package ledgerstore is Core's store for one colony's ledger (D50): the
// colony directory and its colony.json, the writer lock, and the sequencer,
// writer and recovery behind appender.Appender.
//
// # Directory
//
// A colony directory holds colony.json, lock and log (D38). log is the
// segment directory of package ledger's format. Directories are created with
// mode 0o700 and files with 0o600; Windows ignores the modes, and the ACL the
// directory inherits applies. colony.json is exactly
// {"format":1,"colony_id":"<UUIDv7>"} and LF, published through a flushed
// temporary file and a durable rename, so it is either absent or complete.
// lock has no content. One Store holds the operating system's lock on it from
// Create or Open until Close or the holder's death; nothing ever steals it,
// whatever its age or holder (Core:829).
//
// # Writing
//
// Append takes the single writer turn, refuses any draft whose type version is
// not in the store's catalogue, stamps one event time per commit, encodes the
// commit with ledger.EncodeCommit, writes it with one WriteAt and flushes the
// segment. Only then do the sequence and Durable advance. A reader inside
// Core passes Durable as ledger.ReadOptions.Through, so it never sees a
// commit that is not yet acknowledged. When the clock reads earlier than the
// previous commit's time, the commit is stamped with the previous time and
// marked clock_held. Before a commit that would take a non-empty segment past
// the segment target (64 MiB), the writer starts a new segment: it creates the
// file exclusively and flushes the log directory before writing any commit
// bytes to it.
//
// A failed write or flush poisons the store. That Append returns
// appender.ErrUncertain, and every later one returns appender.ErrPoisoned
// until Recover truncates the segment to the end of the last acknowledged
// commit and flushes it. Recover removes an unacknowledged commit even when
// it is complete, because bytes behind a failed flush may never reach the
// disk. A failure to start a new segment poisons the store as well, but no
// commit bytes were written, so that Append returns appender.ErrPoisoned.
//
// # Opening
//
// Open takes the lock, verifies the whole log with ledger.Check, truncates a
// torn final commit and flushes the segment and both directories. It fails
// closed, changing no byte, on anything Check rejects. Open keeps a complete,
// valid commit that was on disk when a writer died, because it cannot know
// whether the commit was acknowledged; that is why appender.ErrUncertain means
// unknown rather than not written.
package ledgerstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

var (
	// ErrLocked reports that another Store, in this process or another, holds
	// the colony. An error matching it also matches durable.ErrLocked.
	ErrLocked = errors.New("ledgerstore: colony is held by another core")
	// ErrExists reports that Create found a colony in the directory.
	ErrExists = errors.New("ledgerstore: colony already exists")
	// ErrNotColony reports that Open found no colony in the directory.
	ErrNotColony = errors.New("ledgerstore: not a colony directory")
)

const (
	lockName = "lock"
	logName  = "log"
	// segmentTarget is the size past which the writer starts a new segment
	// before a commit. It is writer policy, not a format rule (P7).
	segmentTarget = 64 << 20
)

// Recovery reports what Open removed.
type Recovery struct {
	Segment        string // the final segment, if Open truncated it; "" otherwise
	TruncatedBytes int64  // the torn final-commit bytes Open removed
}

// deps are the store's seams. Production uses realDeps. Internal tests
// replace them for deterministic regressions of the writer's state machine;
// they prove no platform behaviour.
type deps struct {
	clock         func() time.Time
	newID         func() uuid.UUID // the colony ID at Create, and each commit ID
	files         fileSystem
	segmentTarget int64
}

func realDeps() deps {
	return deps{clock: time.Now, newID: uuid.NewV7, files: osFiles{}, segmentTarget: segmentTarget}
}

// Store is one colony's open ledger. It holds the colony lock until Close and
// is safe for concurrent use.
type Store struct {
	dir, logDir string
	colony      uuid.UUID
	cat         ledger.Catalogue
	deps        deps
	lock        *durable.Lock

	// turn is the writer turn, a 1-slot semaphore: a goroutine holds the turn
	// while its token is in the channel. It guards the fields below.
	turn chan struct{}

	closed   bool
	poison   error        // why the store is poisoned; nil if it is not
	rotation string       // when a new segment failed to start, its name
	chain    ledger.Chain // the end of the last acknowledged commit
	seg      segmentFile  // the current segment's file; nil if none or after a failure
	segName  string       // the current segment's name; "" if there is none
	size     int64        // the current segment's acknowledged bytes

	acked atomic.Uint64 // chain.Seq, readable without the turn
}

var _ appender.Appender = (*Store)(nil)

// Create makes dir, created if absent, a new and empty colony and returns its
// store, holding the colony lock. The store refuses every record type version
// not in cat; production callers pass ledger.Defined(). A crash at any point
// leaves either no colony.json, and so no colony, or a complete colony. Create
// fails with ErrExists if dir holds colony.json, and with an error matching
// ledger.ErrCorrupt if dir holds a log directory with entries but no
// colony.json.
func Create(dir string, cat ledger.Catalogue) (*Store, error) {
	return create(dir, cat, realDeps())
}

func create(dir string, cat ledger.Catalogue, d deps) (*Store, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := checkNoColony(dir); err != nil {
		return nil, err
	}
	switch _, err := os.Lstat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		parent := filepath.Dir(dir)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, err
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			return nil, err
		}
		if err := d.files.syncDir(parent); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}
	lock, err := lockColony(dir)
	if err != nil {
		return nil, err
	}
	s, err := createLocked(dir, cat, d, lock)
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return s, nil
}

// createLocked creates the colony in dir under the colony lock.
func createLocked(dir string, cat ledger.Catalogue, d deps, lock *durable.Lock) (*Store, error) {
	// A concurrent Create may have finished before this one took the lock.
	if err := checkNoColony(dir); err != nil {
		return nil, err
	}
	logDir := filepath.Join(dir, logName)
	switch err := os.Mkdir(logDir, 0o700); {
	case errors.Is(err, fs.ErrExist):
		entries, err := os.ReadDir(logDir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				return nil, &ledger.CorruptError{Segment: e.Name(), Reason: "log directory without colony.json is not empty"}
			}
		}
	case err != nil:
		return nil, err
	}
	if err := d.files.syncDir(dir); err != nil {
		return nil, err
	}
	id := d.newID()
	if err := writeColony(dir, id); err != nil {
		return nil, err
	}
	return newStore(dir, id, cat, d, lock), nil
}

// checkNoColony returns an error matching ErrExists if dir holds colony.json.
func checkNoColony(dir string) error {
	_, err := os.Lstat(filepath.Join(dir, colonyFile))
	switch {
	case err == nil:
		return fmt.Errorf("%w: %s", ErrExists, dir)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	}
	return err
}

// Open opens the colony in dir and returns its store, holding the colony
// lock, with what recovery removed. It verifies the whole log with
// ledger.Check and fails closed on any error Check returns, changing no byte
// of the log. It then truncates and flushes a torn final commit, and flushes
// the log and colony directories, so that entries left by a process that died
// before flushing them are durable before anything builds on them.
//
// Open never creates a colony or its log directory, and leaves nothing in a
// directory that is not a colony: a directory without colony.json, or no
// directory, is ErrNotColony, and a colony without its log directory fails
// closed. In a colony, Open creates the lock file if it is missing. Every
// error after the lock is taken releases it.
func Open(dir string, cat ledger.Catalogue) (*Store, Recovery, error) {
	return open(dir, cat, realDeps())
}

func open(dir string, cat ledger.Catalogue, d deps) (*Store, Recovery, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, Recovery{}, err
	}
	// colony.json is looked for before the lock is taken, so that the lock
	// file is never created outside a colony. readColony, under the lock,
	// stays the authoritative read.
	switch _, err := os.Lstat(filepath.Join(dir, colonyFile)); {
	case errors.Is(err, fs.ErrNotExist):
		return nil, Recovery{}, fmt.Errorf("%w: %w", ErrNotColony, err)
	case err != nil:
		return nil, Recovery{}, err
	}
	lock, err := lockColony(dir)
	if err != nil {
		return nil, Recovery{}, err
	}
	s, rec, err := openLocked(dir, cat, d, lock)
	if err != nil {
		return nil, Recovery{}, errors.Join(err, lock.Close())
	}
	return s, rec, nil
}

// openLocked opens and recovers the colony in dir under the colony lock.
func openLocked(dir string, cat ledger.Catalogue, d deps, lock *durable.Lock) (*Store, Recovery, error) {
	id, err := readColony(dir)
	if err != nil {
		return nil, Recovery{}, err
	}
	s := newStore(dir, id, cat, d, lock)
	res, err := ledger.Check(s.logDir, id)
	if err != nil {
		return nil, Recovery{}, err
	}
	var rec Recovery
	if res.Segment != "" {
		f, err := d.files.openSegment(filepath.Join(s.logDir, res.Segment))
		if err != nil {
			return nil, Recovery{}, err
		}
		s.seg, s.segName, s.size = f, res.Segment, res.Size
		if res.TornBytes > 0 {
			if err := truncate(f, res.Size); err != nil {
				return nil, Recovery{}, errors.Join(err, s.closeSegment())
			}
			rec = Recovery{Segment: res.Segment, TruncatedBytes: res.TornBytes}
		}
	}
	for _, dir := range []string{s.logDir, s.dir} {
		if err := d.files.syncDir(dir); err != nil {
			return nil, Recovery{}, errors.Join(err, s.closeSegment())
		}
	}
	s.chain = res.Last
	s.acked.Store(res.Last.Seq)
	return s, rec, nil
}

// lockColony takes the colony lock of dir.
func lockColony(dir string) (*durable.Lock, error) {
	lock, err := durable.LockFile(filepath.Join(dir, lockName))
	if errors.Is(err, durable.ErrLocked) {
		return nil, fmt.Errorf("%w: %s: %w", ErrLocked, dir, err)
	}
	if err != nil {
		return nil, err
	}
	return lock, nil
}

func newStore(dir string, id uuid.UUID, cat ledger.Catalogue, d deps, lock *durable.Lock) *Store {
	return &Store{
		dir:    dir,
		logDir: filepath.Join(dir, logName),
		colony: id,
		cat:    cat,
		deps:   d,
		lock:   lock,
		turn:   make(chan struct{}, 1),
		chain:  ledger.Genesis(),
	}
}

// Colony returns the colony's ID.
func (s *Store) Colony() uuid.UUID { return s.colony }

// LogDir returns the absolute path of the colony's log directory, for
// ledger.OpenReader and ledger.Check.
func (s *Store) LogDir() string { return s.logDir }

// Durable returns the sequence of the last acknowledged record, 0 if there is
// none. It is safe for concurrent use. A reader inside Core passes it as
// ledger.ReadOptions.Through.
func (s *Store) Durable() uint64 { return s.acked.Load() }

// Append commits drafts as one commit and returns its sequence range once the
// commit is completely written and flushed. A draft whose type version is not
// in the store's catalogue, or that ledger.EncodeCommit rejects, is refused
// with appender.ErrRefused. ctx bounds only the wait for the writer turn. See
// package appender for the other errors.
func (s *Store) Append(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
	// select chooses at random between a free turn and an ended ctx, so an
	// ended ctx is checked first and never takes the turn.
	if err := ctx.Err(); err != nil {
		return appender.Committed{}, err
	}
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return appender.Committed{}, ctx.Err()
	}
	defer s.release()
	switch {
	case s.closed:
		return appender.Committed{}, appender.ErrClosed
	case s.poison != nil:
		return appender.Committed{}, fmt.Errorf("%w: %w", appender.ErrPoisoned, s.poison)
	}
	for i, d := range drafts {
		if !s.cat.Contains(ledger.TypeVersion{Type: d.Type, Version: d.TypeVersion}) {
			return appender.Committed{}, fmt.Errorf("%w: draft %d: type %s version %d is not in the catalogue", appender.ErrRefused, i, d.Type, d.TypeVersion)
		}
	}
	h := ledger.CommitHeader{ColonyID: s.colony, CommitID: s.deps.newID(), EventTimeNS: s.deps.clock().UnixNano()}
	if h.EventTimeNS < s.chain.EventTimeNS {
		h.EventTimeNS, h.ClockHeld = s.chain.EventTimeNS, true
	}
	data, next, err := ledger.EncodeCommit(s.chain, h, drafts)
	if err != nil {
		return appender.Committed{}, fmt.Errorf("%w: %w", appender.ErrRefused, err)
	}
	if s.segName == "" || (s.size > 0 && s.size+int64(len(data)) > s.deps.segmentTarget) {
		if err := s.rotate(); err != nil {
			return appender.Committed{}, fmt.Errorf("%w: %w", appender.ErrPoisoned, err)
		}
	}
	if err := s.write(data); err != nil {
		s.poison = err
		return appender.Committed{}, fmt.Errorf("%w: %w", appender.ErrUncertain, err)
	}
	first := s.chain.Seq + 1
	s.size += int64(len(data))
	s.chain = next
	s.acked.Store(next.Seq)
	return appender.Committed{First: first, Last: next.Seq}, nil
}

// release gives up the writer turn.
func (s *Store) release() { <-s.turn }

// rotate makes a new segment, named after the next sequence, the current one.
// On failure it poisons the store, because the new file's existence is then
// uncertain.
func (s *Store) rotate() error {
	name := ledger.SegmentName(s.chain.Seq + 1)
	f, err := s.startSegment(name)
	if err != nil {
		err = fmt.Errorf("starting segment %s: %w", name, err)
		s.poison, s.rotation = err, name
		return err
	}
	s.seg, s.segName, s.size = f, name, 0
	return nil
}

// startSegment closes the current segment's file, creates segment name and
// flushes the log directory, so that the new file's entry is durable before
// any commit bytes go into it.
func (s *Store) startSegment(name string) (segmentFile, error) {
	if err := s.closeSegment(); err != nil {
		return nil, err
	}
	f, err := s.deps.files.createSegment(filepath.Join(s.logDir, name))
	if err != nil {
		return nil, err
	}
	if err := s.deps.files.syncDir(s.logDir); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// write writes data after the current segment's acknowledged bytes with one
// WriteAt and flushes the segment.
func (s *Store) write(data []byte) error {
	n, err := s.seg.WriteAt(data, s.size)
	switch {
	case err != nil:
		return fmt.Errorf("writing %s: %w", s.segName, err)
	case n < len(data):
		return fmt.Errorf("writing %s: %w after %d of %d bytes", s.segName, io.ErrShortWrite, n, len(data))
	}
	if err := s.seg.Sync(); err != nil {
		return fmt.Errorf("flushing %s: %w", s.segName, err)
	}
	return nil
}

// Recover reconciles a poisoned store and clears the poison. It truncates the
// current segment to the end of the last acknowledged commit, or empties a
// new segment whose start failed, flushes that file and then the log
// directory. An unacknowledged commit is absent afterwards, even if it was
// complete. Recover does nothing to a store that is not poisoned, returns
// appender.ErrClosed after Close, and may be retried after an error; the store
// stays poisoned until Recover succeeds. The colony lock stays held
// throughout.
func (s *Store) Recover() error {
	s.turn <- struct{}{}
	defer s.release()
	switch {
	case s.closed:
		return appender.ErrClosed
	case s.poison == nil:
		return nil
	}
	// The open file saw the failure. Its close error adds nothing: the
	// truncation and flush below, through a fresh file, settle the outcome.
	_ = s.closeSegment()
	f, name, size, err := s.reopenSegment()
	if err != nil {
		return err
	}
	if f != nil {
		if err := truncate(f, size); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	if err := s.deps.files.syncDir(s.logDir); err != nil {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
		return err
	}
	s.seg, s.segName, s.size = f, name, size
	s.poison, s.rotation = nil, ""
	return nil
}

// reopenSegment opens the segment that is current after recovery and returns
// it with its name and acknowledged size: the new segment a failed rotation
// created, which holds no commit, or else the current segment. It returns a
// nil file if there is neither.
func (s *Store) reopenSegment() (segmentFile, string, int64, error) {
	if s.rotation != "" {
		f, err := s.deps.files.openSegment(filepath.Join(s.logDir, s.rotation))
		if err == nil {
			return f, s.rotation, 0, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, "", 0, err
		}
	}
	if s.segName == "" {
		return nil, "", 0, nil
	}
	f, err := s.deps.files.openSegment(filepath.Join(s.logDir, s.segName))
	if err != nil {
		return nil, "", 0, err
	}
	return f, s.segName, s.size, nil
}

// Close waits for the writer turn, closes the current segment, releases the
// colony lock and marks the store closed. Append then returns
// appender.ErrClosed, and a second Close returns nil.
func (s *Store) Close() error {
	s.turn <- struct{}{}
	defer s.release()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.closeSegment(), s.lock.Close())
}

// closeSegment closes the current segment's file, if it is open.
func (s *Store) closeSegment() error {
	if s.seg == nil {
		return nil
	}
	err := s.seg.Close()
	s.seg = nil
	return err
}

// truncate cuts f to size and flushes it.
func truncate(f segmentFile, size int64) error {
	if err := f.Truncate(size); err != nil {
		return err
	}
	return f.Sync()
}

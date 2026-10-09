// Package ledgerstore is Core's store for one colony's ledger (D50): the
// colony directory and its colony.json, the writer lock, the sequencer,
// writer and recovery behind appender.Appender, and the body store with its
// pins and explicit collection.
//
// # Directory
//
// A colony directory holds colony.json, lock, log and bodies (D38). log is
// the segment directory of package ledger's format. Directories are created
// with mode 0o700 and files with 0o600; Windows ignores the modes, and the
// ACL the directory inherits applies. colony.json is exactly
// {"format":1,"colony_id":"<UUIDv7>"} and LF, published through a flushed
// temporary file and a durable rename, so it is either absent or complete.
// lock has no content. One Store holds the operating system's lock on it from
// Create or Open until Close or the holder's death; nothing ever steals it,
// whatever its age or holder (Core:829).
//
// bodies is the body store. Each body is the file bodies/<hh>/<digest>, named
// by its SHA-256 as 64 lowercase hex digits in the fan-out directory named by
// the first two; Create makes all 256 fan-out directories. bodies/.tmp holds
// publications in progress, and Open empties it. In bodies and in each
// fan-out directory names beginning with a dot are ignored, and any other
// entry the store did not put there fails closed with ErrBodyStore wherever
// the store lists the directory. A body file is never renamed or rewritten
// once published; only Collect deletes it.
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
// # Bodies
//
// PublishBody copies a body into a temporary file, hashing and counting it,
// refuses one over MaxBodyBytes, flushes the file and publishes it under its
// name with a durable rename. A body is acknowledged only once its bytes and
// its name are flushed, and only then may a record reference it: Append
// refuses, with appender.ErrRefused and ErrBodyUnpublished, a draft whose
// body_refs names a body the store does not hold. A body is immutable, so
// publishing it again writes nothing; in particular it does not refresh the
// body's age, because collection ages a referenced body by its newest
// reference, not by its publication. A body that collection takes after its
// producer published it but before the producer's Append lands is refused
// that way; the producer publishes it again and retries.
//
// On Windows, a handle that another program holds on the temporary file,
// such as a real-time virus scanner's or an indexer's, can fail the rename
// with a sharing violation. PublishBody returns that error and nothing is
// published; the store never retries, and the caller may.
//
// ReadBody reads a whole body and returns it only after verifying it against
// its digest, so no caller sees unverified bytes. An absent body is
// classified from the ledger.bodies_collected records: collected, with the
// record's sequence, or missing, which fails closed. PublishBody and ReadBody
// do not take the writer turn, so they keep working while the store is
// poisoned.
//
// Close waits for body reads and publications in progress, and for the log
// scan that begins a collection, to finish, after which the store holds no
// file in the colony directory. Each of them notices Close between chunks,
// segments or commits and stops with appender.ErrClosed, a publication
// removing its temporary file. A Read on a publication's source that never
// returns delays Close, so a host cancels its producers' contexts first.
//
// One sync.RWMutex orders the body store. Reads and reference checks share
// it; the final step of a publication, each collection batch and Close take
// it alone. On Windows a file that is open for reading cannot be removed or
// replaced, and on Linux and macOS a renamed file is visible before its
// directory is flushed, so neither a deletion nor a reference check meets a
// read or a publication in progress. The mutex prefers a waiting writer:
// while a large ReadBody runs, a waiting publication or collection batch
// holds back every later read and publication.
//
// # Pins and collection
//
// A ledger.body_pin record pins the bodies its body_refs lists until a
// ledger.body_unpin with the same pin ID releases them, and Collect never
// takes a pinned body. The store holds the active pins in memory, bounded at
// 65,536 (pin, body) pairs: Append refuses a commit holding a pin that
// would pass the bound, and always accepts unpins.
//
// Collect deletes bodies only when a caller orders it, under the explicit
// policy it is given; the store never collects on its own. Each referenced
// body it deletes is first listed in a durable ledger.bodies_collected record,
// so history keeps every body it ever referenced, available or collected.
// A policy's byte target counts as present bytes the sizes of every body in
// the store when Collect lists it, pinned and ineligible bodies included,
// less those it deletes.
//
// # Opening
//
// Open takes the lock, verifies the whole log with ledger.Check, truncates a
// torn final commit and flushes the segment and both directories. It fails
// closed, changing no byte, on anything Check rejects, and on a missing body
// store. Open keeps a complete, valid commit that was on disk when a writer
// died, because it cannot know whether the commit was acknowledged; that is
// why appender.ErrUncertain means unknown rather than not written.
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
	"sync"
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
	clock            func() time.Time
	newID            func() uuid.UUID // the colony ID at Create, and each commit ID
	files            fileSystem
	segmentTarget    int64
	maxBodyBytes     int64  // MaxBodyBytes
	maxPinned        int    // the bound on active (pin, body) pairs
	collectBatch     int    // the bodies one collection batch takes at most
	afterCollectScan func() // called after Collect's first read of the log; nil in production
	beforeClassify   func() // called by ReadBody for an absent body after it releases the body lock; nil in production
}

func realDeps() deps {
	return deps{
		clock: time.Now, newID: uuid.NewV7, files: osFiles{}, segmentTarget: segmentTarget,
		maxBodyBytes: MaxBodyBytes, maxPinned: maxPinned, collectBatch: collectBatch,
	}
}

// Store is one colony's open ledger. It holds the colony lock until Close and
// is safe for concurrent use.
type Store struct {
	dir, logDir, bodiesDir string
	colony                 uuid.UUID
	cat                    ledger.Catalogue
	deps                   deps
	lock                   *durable.Lock

	// collectMu serializes Collect calls. It is taken before the turn.
	collectMu sync.Mutex

	// turn is the writer turn, a 1-slot semaphore: a goroutine holds the turn
	// while its token is in the channel. It guards the fields below.
	turn chan struct{}

	poison   error        // why the store is poisoned; nil if it is not
	rotation string       // when a new segment failed to start, its name
	chain    ledger.Chain // the end of the last acknowledged commit
	seg      segmentFile  // the current segment's file; nil if none or after a failure
	segName  string       // the current segment's name; "" if there is none
	size     int64        // the current segment's acknowledged bytes
	pins     pinSet       // the active pins

	// bodiesMu orders body publication, reads, reference checks, collection
	// batches and Close. A goroutine that holds both it and the turn took
	// the turn first. The fields below change only under both the turn and
	// its write lock, so either one guards reading them.
	bodiesMu  sync.RWMutex
	closed    bool
	collected []uint64 // the commit ends of the ledger.bodies_collected records, ascending

	// inFlight counts the body reads, publications and collection scans
	// that touch the colony's files outside the writer turn; Close waits
	// for them. Each is added under the read lock while closed is false.
	inFlight sync.WaitGroup
	closing  atomic.Bool   // closed, readable without a lock
	acked    atomic.Uint64 // chain.Seq, readable without the turn
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
	if err := createBodies(filepath.Join(dir, bodiesName), d.files); err != nil {
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
	if err := openBodies(s.bodiesDir); err != nil {
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
		dir:       dir,
		logDir:    filepath.Join(dir, logName),
		bodiesDir: filepath.Join(dir, bodiesName),
		colony:    id,
		cat:       cat,
		deps:      d,
		lock:      lock,
		turn:      make(chan struct{}, 1),
		chain:     ledger.Genesis(),
		pins:      newPinSet(),
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
// commit is completely written and flushed. It refuses with
// appender.ErrRefused a draft whose type version is not in the store's
// catalogue, that breaks a rule of its F0 record type, that references a
// body the body store does not hold, or that ledger.EncodeCommit rejects.
// ctx bounds only the wait for the writer turn. See package appender for the
// other errors.
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
	plan, err := s.pins.plan(drafts, s.deps.maxPinned)
	if err != nil {
		return appender.Committed{}, fmt.Errorf("%w: %w", appender.ErrRefused, err)
	}
	if err := s.checkReferences(drafts); err != nil {
		return appender.Committed{}, err
	}
	c, err := s.commit(drafts)
	if err != nil {
		return appender.Committed{}, err
	}
	s.pins.apply(plan)
	return c, nil
}

// commit stamps drafts with one event time, encodes them as one commit,
// writes it and flushes it, under the writer turn. Only then do the chain
// and Durable advance. An encoding refusal returns appender.ErrRefused, a
// failure to start a segment appender.ErrPoisoned, and a failed write or
// flush poisons the store and returns appender.ErrUncertain.
func (s *Store) commit(drafts []ledger.Draft) (appender.Committed, error) {
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

// Close waits for the writer turn and marks the store closed. It then waits
// for body reads and publications in progress, and for the log scan that
// begins a collection, to finish, closes the current segment and releases the
// colony lock. After Close returns the store holds no file in the colony
// directory. Append, Collect, PublishBody and ReadBody then return
// appender.ErrClosed, and a second Close returns nil. A Read on a
// publication's source that never returns delays Close, so a host cancels
// its producers' contexts first.
func (s *Store) Close() error {
	s.turn <- struct{}{}
	defer s.release()
	s.bodiesMu.Lock()
	if s.closed {
		s.bodiesMu.Unlock()
		return nil
	}
	s.closed = true
	s.closing.Store(true)
	// A publication takes the write lock to clean up, so the lock is not
	// held while waiting.
	s.bodiesMu.Unlock()
	s.inFlight.Wait()
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

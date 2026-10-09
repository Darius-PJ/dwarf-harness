package ledgerstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

// MaxBodyBytes caps one body. It is provisional Core policy, not a format
// rule: no reader of the ledger reads bodies.
const MaxBodyBytes = 64 << 20

const (
	bodiesName = "bodies"
	tmpName    = ".tmp"
	// copyChunk is the unit in which PublishBody copies, hashes and counts.
	copyChunk = 1 << 20
)

var (
	// ErrBodyTooLarge reports a body over the cap; nothing was published.
	ErrBodyTooLarge = errors.New("ledgerstore: body over cap")
	// ErrBodyUnpublished reports a body reference to a body that is not in
	// the body store.
	ErrBodyUnpublished = errors.New("ledgerstore: body is not published")
	// ErrBodyCorrupt reports a stored body whose bytes do not match its
	// digest or exceed the cap.
	ErrBodyCorrupt = errors.New("ledgerstore: body does not match its digest")
	// ErrBodyMissing reports an absent body that no ledger.bodies_collected
	// record lists.
	ErrBodyMissing = errors.New("ledgerstore: body is missing and was never collected")
	// ErrBodyUnavailable reports a body that collection removed; see
	// BodyUnavailableError.
	ErrBodyUnavailable = errors.New("ledgerstore: body was collected")
	// ErrBodyStore reports an entry in the body store that the store did not
	// put there.
	ErrBodyStore = errors.New("ledgerstore: unexpected entry in the body store")
)

// BodyUnavailableError reports a body that a ledger.bodies_collected record
// lists. Seq is the newest such record. errors.Is(err, ErrBodyUnavailable)
// holds.
type BodyUnavailableError struct {
	Digest ledger.Digest
	Seq    uint64
}

func (e *BodyUnavailableError) Error() string {
	return fmt.Sprintf("%v: %s, by the record at seq %d", ErrBodyUnavailable, e.Digest, e.Seq)
}

func (e *BodyUnavailableError) Unwrap() error { return ErrBodyUnavailable }

// bodyPath returns the path of body d in the body store at dir.
func bodyPath(dir string, d ledger.Digest) string {
	name := d.String()
	return filepath.Join(dir, name[:2], name)
}

// isFanOut reports whether name is a fan-out directory name: two lowercase
// hex digits.
func isFanOut(name string) bool {
	return len(name) == 2 && strings.Trim(name, "0123456789abcdef") == ""
}

// bodyStoreError reports an unexpected entry at path.
func bodyStoreError(path, what string) error {
	return fmt.Errorf("%w: %s %s", ErrBodyStore, path, what)
}

// createBodies makes the body store at dir: dir, its .tmp directory and the
// 256 fan-out directories, reusing those an interrupted Create left empty,
// and flushes dir. It refuses an existing dir holding a body or any entry
// other than names beginning with a dot, a .tmp directory and empty fan-out
// directories.
func createBodies(dir string, files fileSystem) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case e.Name() == tmpName && !e.IsDir():
			return bodyStoreError(path, "is not a directory")
		case strings.HasPrefix(e.Name(), "."):
			continue
		case !isFanOut(e.Name()) || !e.IsDir():
			return bodyStoreError(path, "is not a fan-out directory")
		}
		inner, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, b := range inner {
			if !strings.HasPrefix(b.Name(), ".") {
				return bodyStoreError(filepath.Join(path, b.Name()), "is in a body store without colony.json")
			}
		}
	}
	names := []string{tmpName}
	for i := range 256 {
		names = append(names, fmt.Sprintf("%02x", i))
	}
	for _, name := range names {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return files.syncDir(dir)
}

// openBodies checks that the body store at dir exists, then empties its .tmp
// directory, creating it if missing: what is there was left by publications
// that a stopped process never finished, and nothing references it.
func openBodies(dir string) error {
	fi, err := os.Lstat(dir)
	switch {
	case err != nil:
		return fmt.Errorf("ledgerstore: body store: %w", err)
	case !fi.IsDir():
		return bodyStoreError(dir, "is not a directory")
	}
	tmp := filepath.Join(dir, tmpName)
	if err := os.Mkdir(tmp, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(tmp, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// bodyInfo is one body file a listing found.
type bodyInfo struct {
	size    int64
	mtimeNS int64
}

// listBodies returns every body in the body store at dir. Names beginning
// with a dot are ignored in dir and in each fan-out directory. Any other
// entry that is not a fan-out directory, or within one a regular file named
// by a digest whose first two digits name the directory, fails closed with
// ErrBodyStore.
func listBodies(dir string) (map[ledger.Digest]bodyInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	bodies := map[ledger.Digest]bodyInfo{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fan := filepath.Join(dir, e.Name())
		if !isFanOut(e.Name()) || !e.IsDir() {
			return nil, bodyStoreError(fan, "is not a fan-out directory")
		}
		inner, err := os.ReadDir(fan)
		if err != nil {
			return nil, err
		}
		for _, b := range inner {
			if strings.HasPrefix(b.Name(), ".") {
				continue
			}
			path := filepath.Join(fan, b.Name())
			d, err := ledger.ParseDigest(b.Name())
			switch {
			case err != nil:
				return nil, bodyStoreError(path, "is not named by a digest")
			case b.Name()[:2] != e.Name():
				return nil, bodyStoreError(path, "is in the wrong fan-out directory")
			case !b.Type().IsRegular():
				return nil, bodyStoreError(path, "is not a regular file")
			}
			fi, err := b.Info()
			if err != nil {
				return nil, err
			}
			bodies[d] = bodyInfo{size: fi.Size(), mtimeNS: fi.ModTime().UnixNano()}
		}
	}
	return bodies, nil
}

// PublishBody stores the bytes of r in the colony's body store under their
// SHA-256 and returns the digest and the size. It returns only after the
// bytes are flushed, the body is atomically published under its name and
// that name is flushed, so a record may reference the body from then on. A
// body already published is not written again. A body over MaxBodyBytes
// fails with ErrBodyTooLarge. ctx and Close are checked between chunks; a
// Close stops the copy with appender.ErrClosed. On any error the call removes
// its temporary file, and a body file its failed rename left, and returns any
// error from that removal with the cause. PublishBody does not take the
// writer turn, so it works while the store is poisoned; after Close it
// returns appender.ErrClosed.
func (s *Store) PublishBody(ctx context.Context, r io.Reader) (ledger.Digest, int64, error) {
	if err := ctx.Err(); err != nil {
		return ledger.Digest{}, 0, err
	}
	if err := s.begin(); err != nil {
		return ledger.Digest{}, 0, err
	}
	defer s.inFlight.Done()
	f, err := s.deps.files.createTemp(filepath.Join(s.bodiesDir, tmpName))
	if err != nil {
		return ledger.Digest{}, 0, err
	}
	d, n, err := s.copyBody(ctx, f, r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.publish(f.Name(), d)
	} else {
		err = errors.Join(err, s.removeTemp(f.Name()))
	}
	if err != nil {
		return ledger.Digest{}, 0, err
	}
	return d, n, nil
}

// begin registers an operation that touches the colony's files outside the
// writer turn, so that Close waits for it, and returns appender.ErrClosed
// after Close. The caller calls s.inFlight.Done when the operation ends.
// The count is taken under the read lock, so none is added after Close has
// marked the store closed under the write lock.
func (s *Store) begin() error {
	s.bodiesMu.RLock()
	defer s.bodiesMu.RUnlock()
	if s.closed {
		return appender.ErrClosed
	}
	s.inFlight.Add(1)
	return nil
}

// copyBody copies r to w in chunks, hashing and counting. It stops with
// ErrBodyTooLarge once the count passes the cap, and with
// appender.ErrClosed once Close has marked the store closed.
func (s *Store) copyBody(ctx context.Context, w io.Writer, r io.Reader) (ledger.Digest, int64, error) {
	h := sha256.New()
	buf := make([]byte, copyChunk)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return ledger.Digest{}, 0, err
		}
		if s.closing.Load() {
			return ledger.Digest{}, 0, appender.ErrClosed
		}
		k, rerr := io.ReadFull(r, buf)
		if k > 0 {
			if n += int64(k); n > s.deps.maxBodyBytes {
				return ledger.Digest{}, 0, fmt.Errorf("%w: more than %d bytes", ErrBodyTooLarge, s.deps.maxBodyBytes)
			}
			h.Write(buf[:k])
			if _, err := w.Write(buf[:k]); err != nil {
				return ledger.Digest{}, 0, err
			}
		}
		switch rerr {
		case nil:
		case io.EOF, io.ErrUnexpectedEOF:
			var d ledger.Digest
			h.Sum(d[:0])
			return d, n, nil
		default:
			return ledger.Digest{}, 0, rerr
		}
	}
}

// publish moves the flushed temporary file temp into place as body d under
// the write lock, or removes it if d is already published.
func (s *Store) publish(temp string, d ledger.Digest) error {
	s.bodiesMu.Lock()
	defer s.bodiesMu.Unlock()
	if s.closed {
		return errors.Join(appender.ErrClosed, s.removeTemp(temp))
	}
	target := bodyPath(s.bodiesDir, d)
	dir := filepath.Dir(target)
	// Create makes every fan-out directory; this repairs a missing one.
	switch _, err := os.Lstat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(dir, 0o700); err != nil {
			return errors.Join(err, s.removeTemp(temp))
		}
		if err := s.deps.files.syncDir(s.bodiesDir); err != nil {
			return errors.Join(err, s.removeTemp(temp))
		}
	case err != nil:
		return errors.Join(err, s.removeTemp(temp))
	}
	switch fi, err := os.Lstat(target); {
	case err == nil && fi.Mode().IsRegular():
		// A body is immutable, so publication deduplicates. The flush makes
		// durable an entry that an earlier, failed publication may have left.
		return errors.Join(s.removeTemp(temp), s.deps.files.syncDir(dir))
	case err == nil:
		return errors.Join(bodyStoreError(target, "is not a regular file"), s.removeTemp(temp))
	case !errors.Is(err, fs.ErrNotExist):
		return errors.Join(err, s.removeTemp(temp))
	}
	if err := s.deps.files.rename(temp, target); err != nil {
		// The target's entry may not be durable, so it is not left behind.
		if _, lerr := os.Lstat(target); lerr == nil {
			err = errors.Join(err, s.deps.files.remove(target))
		}
		return errors.Join(err, s.removeTemp(temp))
	}
	return nil
}

// removeTemp removes a publication's temporary file, if it is still there.
func (s *Store) removeTemp(path string) error {
	if err := s.deps.files.remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ReadBody returns the bytes of body d, verified: it reads the whole body
// and returns it only if its SHA-256 is d and its size is within the cap,
// and otherwise fails with ErrBodyCorrupt. Memory is at most one body. An
// absent body fails with a *BodyUnavailableError if a ledger.bodies_collected
// record lists it, and with ErrBodyMissing if none does; a collection that
// lands while the call runs reads as missing for that call. ReadBody does not
// take the writer turn, so it works while the store is poisoned; after Close
// it returns appender.ErrClosed. Authorizing the read is the caller's.
func (s *Store) ReadBody(ctx context.Context, d ledger.Digest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.bodiesMu.RLock()
	if s.closed {
		s.bodiesMu.RUnlock()
		return nil, appender.ErrClosed
	}
	s.inFlight.Add(1)
	defer s.inFlight.Done()
	// Collect appends to collected under the write lock, so this snapshot
	// is consistent; a collection after it reads as missing for this call.
	collected := s.collected[:len(s.collected):len(s.collected)]
	data, err := s.readBody(d)
	s.bodiesMu.RUnlock()
	if errors.Is(err, fs.ErrNotExist) {
		if s.deps.beforeClassify != nil {
			s.deps.beforeClassify()
		}
		return nil, s.classify(ctx, d, collected)
	}
	return data, err
}

// readBody reads and verifies body d under the read lock.
func (s *Store) readBody(d ledger.Digest) ([]byte, error) {
	path := bodyPath(s.bodiesDir, d)
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, err
	case !fi.Mode().IsRegular():
		return nil, bodyStoreError(path, "is not a regular file")
	case fi.Size() > s.deps.maxBodyBytes:
		return nil, fmt.Errorf("%w: %s holds %d bytes, over the cap", ErrBodyCorrupt, d, fi.Size())
	}
	f, err := s.deps.files.openBody(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// One byte more than the listed size shows a body that grew.
	buf := make([]byte, fi.Size()+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	data := buf[:n]
	if sha256.Sum256(data) != d || int64(n) != fi.Size() {
		return nil, fmt.Errorf("%w: %s", ErrBodyCorrupt, d)
	}
	return data, nil
}

// classify explains why body d is absent from the store: the newest
// ledger.bodies_collected record that lists it, or ErrBodyMissing. ends is a
// snapshot of the store's collection index, the ascending commit ends of
// those records. A commit end lies in the segment of its record, so classify
// reads newest segment first, with one reader per segment that ends any,
// bounded by the segment's start and the newest recorded end in it. ctx and
// Close are checked between segments.
func (s *Store) classify(ctx context.Context, d ledger.Digest, ends []uint64) error {
	if len(ends) == 0 {
		return fmt.Errorf("%w: %s", ErrBodyMissing, d)
	}
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return err
	}
	var firsts []uint64
	for _, e := range entries {
		if first, ok := ledger.ParseSegmentName(e.Name()); ok {
			firsts = append(firsts, first)
		}
	}
	end := len(ends)
	for i := len(firsts) - 1; i >= 0 && end > 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.closing.Load() {
			return appender.ErrClosed
		}
		// ends[start:end] are the commit ends in the segment from firsts[i].
		start, _ := slices.BinarySearch(ends[:end], firsts[i])
		if start == end {
			continue
		}
		seq, err := s.newestListing(firsts[i]-1, ends[end-1], d)
		if err != nil {
			return err
		}
		if seq != 0 {
			return &BodyUnavailableError{Digest: d, Seq: seq}
		}
		end = start
	}
	return fmt.Errorf("%w: %s", ErrBodyMissing, d)
}

// newestListing returns the newest ledger.bodies_collected record in
// (after, through] that lists d, or 0 if none does.
func (s *Store) newestListing(after, through uint64, d ledger.Digest) (uint64, error) {
	r, err := ledger.OpenReader(s.logDir, ledger.ReadOptions{
		Colony: s.colony, After: after, Through: through,
		Consume: ledger.Consumption{ledger.TypeBodiesCollected: {1}},
	})
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var newest uint64
	for {
		c, err := r.Next()
		if err == io.EOF {
			return newest, nil
		}
		if err != nil {
			return 0, err
		}
		for _, rec := range c.Records {
			p, err := ledger.DecodeBodiesCollected(rec.Payload)
			if err != nil {
				return 0, fmt.Errorf("ledgerstore: seq %d: %w", rec.Seq, err)
			}
			if slices.ContainsFunc(p.Bodies, func(b ledger.CollectedBody) bool { return b.SHA256 == d }) {
				newest = rec.Seq
			}
		}
	}
}

// checkReferences refuses drafts that reference a body the body store does
// not hold as a regular file. It holds the read lock, so it never sees a
// publication in progress; the caller's writer turn keeps collection from
// removing a body before the commit.
func (s *Store) checkReferences(drafts []ledger.Draft) error {
	s.bodiesMu.RLock()
	defer s.bodiesMu.RUnlock()
	seen := map[ledger.Digest]bool{}
	for i, d := range drafts {
		for _, ref := range d.BodyRefs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			switch fi, err := os.Lstat(bodyPath(s.bodiesDir, ref)); {
			case err == nil && fi.Mode().IsRegular():
			case err == nil:
				return fmt.Errorf("%w: draft %d: %w", appender.ErrRefused, i, bodyStoreError(bodyPath(s.bodiesDir, ref), "is not a regular file"))
			case errors.Is(err, fs.ErrNotExist):
				return fmt.Errorf("%w: draft %d: %w: %s", appender.ErrRefused, i, ErrBodyUnpublished, ref)
			default:
				return fmt.Errorf("%w: draft %d: body %s: %w", appender.ErrRefused, i, ref, err)
			}
		}
	}
	return nil
}

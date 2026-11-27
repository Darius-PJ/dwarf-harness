package ledgerstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ErrLockMissing distinguishes an incomplete colony from a held one. Inspection
// never recreates the lock file; normal startup owns recovery.
var ErrLockMissing = errors.New("ledgerstore: colony lock missing")

// Inspection holds the colony's ordinary exclusive lock over its verified
// prefix. It exposes neither a writer nor a repair operation. Close it after
// every borrowed agents/workspace inspection has closed.
type Inspection struct{ store *Store }

// InspectColony verifies identity, the ledger and F0 projections without opening
// a writable segment, truncating a tail, flushing directories or collecting
// bodies. Recovery describes bytes normal Open would truncate, not an effect.
func InspectColony(dir string, cat ledger.Catalogue) (*Inspection, Recovery, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, Recovery{}, err
	}
	for _, name := range []string{colonyFile, lockName} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			if name == lockName {
				return nil, Recovery{}, ErrLockMissing
			}
			return nil, Recovery{}, ErrNotColony
		}
		if err != nil {
			return nil, Recovery{}, err
		}
		if !info.Mode().IsRegular() {
			return nil, Recovery{}, ErrInconsistent
		}
	}
	lock, err := lockColony(dir)
	if err != nil {
		return nil, Recovery{}, err
	}
	fail := func(err error) (*Inspection, Recovery, error) { return nil, Recovery{}, errors.Join(err, lock.Close()) }
	id, err := readColony(dir)
	if err != nil {
		return fail(err)
	}
	s := newStore(dir, id, cat, realDeps(), lock)
	checked, err := ledger.Check(s.logDir, id)
	if err != nil {
		return fail(err)
	}
	// openBodies is deliberately not used: it sweeps unfinished publications.
	info, err := os.Lstat(s.bodiesDir)
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() {
		return fail(ErrInconsistent)
	}
	s.chain = checked.Last
	s.acked.Store(checked.Last.Seq)
	if err := s.rebuild(); err != nil {
		return fail(err)
	}
	report := Recovery{}
	if checked.TornBytes != 0 {
		report = Recovery{Segment: checked.Segment, TruncatedBytes: checked.TornBytes}
	}
	return &Inspection{store: s}, report, nil
}

func (i *Inspection) Colony() uuid.UUID { return i.store.Colony() }
func (i *Inspection) LogDir() string    { return i.store.LogDir() }
func (i *Inspection) Durable() uint64   { return i.store.Durable() }
func (i *Inspection) ReadBody(ctx context.Context, digest ledger.Digest) ([]byte, error) {
	return i.store.ReadBody(ctx, digest)
}
func (i *Inspection) Close() error { return i.store.Close() }

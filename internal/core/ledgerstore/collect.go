package ledgerstore

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

// collectBatch bounds the bodies one collection batch takes, and so how
// long a batch holds the writer turn. It is Core policy.
const collectBatch = 256

// CollectPolicy is an explicit collection order.
type CollectPolicy struct {
	Actor     ledger.Actor // ledger.ActorOwner or ledger.ActorCore: who ordered it; recorded
	BeforeNS  int64        // above 0: the age cutoff, in nanoseconds
	KeepBytes int64        // at least 0: 0 collects every eligible body; above 0 stops once present bytes are at most KeepBytes
}

// CollectReport reports what one Collect did.
type CollectReport struct {
	Recorded          int      // referenced bodies recorded unavailable and deleted
	RecordedBytes     int64    // their bytes
	Unreferenced      int      // bodies no collection record lists, deleted without a record
	UnreferencedBytes int64    // their bytes
	Leftovers         int      // recorded bodies whose deletion failed; a later Collect retries them
	LeftoversDeleted  int      // bodies an earlier collection record lists, deleted without a new record
	Records           []uint64 // the seqs of the ledger.bodies_collected records appended
	PresentBytes      int64    // the bytes of the bodies present at the end
}

// collected is what one Collect knows of a body present in the store.
type collected struct {
	bodyInfo
	refSeq  uint64 // the newest record listing it in body_refs; 0 if none
	refNS   int64  // that record's event time
	collSeq uint64 // the newest ledger.bodies_collected record listing it; 0 if none
	tried   bool   // this Collect tried to delete it
}

// candidate is an eligible body in collection order.
type candidate struct {
	d          ledger.Digest
	b          *collected
	key        int64 // the newest reference's event time, or the file's modification time
	referenced bool
}

// Collect deletes bodies under policy p and records the referenced ones it
// deletes in ledger.bodies_collected records. The store never collects on
// its own: Collect runs only when a caller orders it.
//
// A present body is eligible when no active pin holds it and either a record
// references it after the newest collection record that lists it, and that
// newest reference's event time is before p.BeforeNS, or no record does, and
// its file's modification time is before p.BeforeNS. The first are recorded;
// the second, a publication whose reference never landed or a leftover of a
// failed deletion, are deleted without a record. Eligible bodies are taken
// oldest first by that time, then by digest. With p.KeepBytes above 0,
// Collect stops once the present bytes, the sizes of every body the store
// held when Collect listed it, pinned and ineligible bodies included, less
// those deleted, are at most p.KeepBytes.
//
// Collect works in batches of at most 256 bodies. Each batch holds the
// writer turn and the body lock, re-reads the records appended since the
// last look, appends its record and flushes it, and only then deletes the
// files and flushes their directories. A failure to write the record poisons
// the store, returns appender.ErrUncertain and deletes nothing. A recorded
// body whose deletion fails stays readable and is counted in
// CollectReport.Leftovers; a later Collect deletes it without a new record.
// On an error, batches already completed stay complete.
func (s *Store) Collect(ctx context.Context, p CollectPolicy) (CollectReport, error) {
	var rep CollectReport
	switch {
	case p.Actor != ledger.ActorOwner && p.Actor != ledger.ActorCore:
		return rep, fmt.Errorf("%w: collection actor %q is not owner or core", appender.ErrRefused, p.Actor)
	case p.BeforeNS < 1:
		return rep, fmt.Errorf("%w: collection cutoff %d is not above 0", appender.ErrRefused, p.BeforeNS)
	case p.KeepBytes < 0:
		return rep, fmt.Errorf("%w: collection byte target %d is negative", appender.ErrRefused, p.KeepBytes)
	case !s.cat.Contains(ledger.TypeVersion{Type: ledger.TypeBodiesCollected, Version: 1}):
		return rep, fmt.Errorf("%w: %s version 1 is not in the catalogue", appender.ErrRefused, ledger.TypeBodiesCollected)
	}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	s.collectMu.Lock()
	defer s.collectMu.Unlock()

	scanned, present, err := s.collectScan()
	if err != nil {
		return rep, err
	}
	if s.deps.afterCollectScan != nil {
		s.deps.afterCollectScan()
	}

	// Phase B: batches until nothing eligible is left or the target is met.
	for more := true; more; {
		if more, err = s.collectBatch(ctx, p, present, &scanned, &rep); err != nil {
			break
		}
	}
	for _, b := range present {
		rep.PresentBytes += b.size
	}
	return rep, err
}

// collectScan is Collect's first phase, run without the writer turn or the
// body lock: it lists the store and reads the durable records once, and
// returns the last sequence read and what it learnt of each present body.
// Close waits for it, and stops it with appender.ErrClosed.
func (s *Store) collectScan() (uint64, map[ledger.Digest]*collected, error) {
	if err := s.begin(); err != nil {
		return 0, nil, err
	}
	defer s.inFlight.Done()
	scanned := s.Durable()
	listed, err := listBodies(s.bodiesDir)
	if err != nil {
		return 0, nil, err
	}
	present := make(map[ledger.Digest]*collected, len(listed))
	for d, info := range listed {
		present[d] = &collected{bodyInfo: info}
	}
	if err := s.scan(0, scanned, present); err != nil {
		return 0, nil, err
	}
	return scanned, present, nil
}

// collectBatch runs one batch under the writer turn and the body lock and
// reports whether more eligible bodies remain.
func (s *Store) collectBatch(ctx context.Context, p CollectPolicy, present map[ledger.Digest]*collected, scanned *uint64, rep *CollectReport) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer s.release()
	switch {
	case s.closed:
		return false, appender.ErrClosed
	case s.poison != nil:
		return false, fmt.Errorf("%w: %w", appender.ErrPoisoned, s.poison)
	}
	s.bodiesMu.Lock()
	defer s.bodiesMu.Unlock()

	// Records appended since the last look count, including references
	// made while phase A ran.
	if err := s.scan(*scanned, s.chain.Seq, present); err != nil {
		return false, err
	}
	*scanned = s.chain.Seq

	var cands []candidate
	var presentBytes int64
	for d, b := range present {
		presentBytes += b.size
		if b.tried || s.pins.pinned[d] > 0 {
			continue
		}
		c := candidate{d: d, b: b, key: b.mtimeNS}
		if b.refSeq > b.collSeq {
			c.key, c.referenced = b.refNS, true
		}
		if c.key < p.BeforeNS {
			cands = append(cands, c)
		}
	}
	slices.SortFunc(cands, func(x, y candidate) int {
		return cmp.Or(cmp.Compare(x.key, y.key), bytes.Compare(x.d[:], y.d[:]))
	})
	var take []candidate
	more := false
	for _, c := range cands {
		if p.KeepBytes > 0 && presentBytes <= p.KeepBytes {
			break
		}
		if len(take) == s.deps.collectBatch {
			more = true
			break
		}
		take = append(take, c)
		presentBytes -= c.b.size
	}
	if len(take) == 0 {
		return false, nil
	}

	var record ledger.BodiesCollected
	for _, c := range take {
		if c.referenced {
			record.Bodies = append(record.Bodies, ledger.CollectedBody{SHA256: c.d, Bytes: c.b.size})
		}
	}
	if len(record.Bodies) > 0 {
		slices.SortFunc(record.Bodies, func(x, y ledger.CollectedBody) int { return bytes.Compare(x.SHA256[:], y.SHA256[:]) })
		record.BeforeNS, record.KeepBytes = p.BeforeNS, p.KeepBytes
		payload, err := record.Encode()
		if err != nil {
			return false, fmt.Errorf("%w: %w", appender.ErrRefused, err)
		}
		c, err := s.commit([]ledger.Draft{{Type: ledger.TypeBodiesCollected, TypeVersion: 1, Actor: p.Actor, Payload: payload}})
		if err != nil {
			return false, err
		}
		// The record is the commit's first record; the index keeps the
		// commit's end, where a reader may stop.
		s.collected = append(s.collected, c.Last)
		rep.Records = append(rep.Records, c.First)
	}

	// The record is durable, so the deletions may follow.
	touched := map[string]bool{}
	for _, c := range take {
		c.b.tried = true
		path := bodyPath(s.bodiesDir, c.d)
		err := s.deps.files.remove(path)
		switch {
		case err != nil && c.referenced:
			rep.Leftovers++
			continue
		case err != nil:
			continue
		case c.referenced:
			rep.Recorded++
			rep.RecordedBytes += c.b.size
		case c.b.collSeq > 0:
			rep.LeftoversDeleted++
		default:
			rep.Unreferenced++
			rep.UnreferencedBytes += c.b.size
		}
		delete(present, c.d)
		touched[filepath.Dir(path)] = true
	}
	for _, dir := range slices.Sorted(maps.Keys(touched)) {
		if err := s.deps.files.syncDir(dir); err != nil {
			return false, err
		}
	}
	return more, nil
}

// scan reads the records in (after, through] and updates, for the bodies in
// present, the newest reference and the newest collection record. It stops
// with appender.ErrClosed between commits once Close has marked the store
// closed; under the writer turn that never happens.
func (s *Store) scan(after, through uint64, present map[ledger.Digest]*collected) error {
	if after == through {
		return nil
	}
	r, err := ledger.OpenReader(s.logDir, ledger.ReadOptions{
		Colony: s.colony, After: after, Through: through,
		Consume:  ledger.Consumption{ledger.TypeBodiesCollected: {1}},
		AllTypes: true,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	for {
		if s.closing.Load() {
			return appender.ErrClosed
		}
		c, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		for _, rec := range c.Records {
			for _, d := range rec.BodyRefs {
				if b := present[d]; b != nil {
					b.refSeq, b.refNS = rec.Seq, rec.EventTimeNS
				}
			}
			if rec.Type != ledger.TypeBodiesCollected {
				continue
			}
			p, err := ledger.DecodeBodiesCollected(rec.Payload)
			if err != nil {
				return fmt.Errorf("ledgerstore: seq %d: %w", rec.Seq, err)
			}
			for _, cb := range p.Bodies {
				if b := present[cb.SHA256]; b != nil {
					b.collSeq = rec.Seq
				}
			}
		}
	}
}

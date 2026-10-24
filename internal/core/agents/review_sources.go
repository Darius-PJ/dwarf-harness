package agents

import (
	"context"
	"errors"
	"os"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Source sequences are inside commits, so they cannot themselves be reader
// cursors. Seek the first witness's segment boundary and stop at the last
// witness's complete commit. Keep only the requested metadata, not the range.
func (s *Service) sourceRecords(ctx context.Context, sequences []int64, through uint64) (map[int64]ledger.Record, error) {
	wanted := map[int64]ledger.Record{}
	var low, high uint64
	for _, seq := range sequences {
		if seq <= 0 || uint64(seq) > through {
			return nil, ErrUnavailable
		}
		wanted[seq] = ledger.Record{}
		if low == 0 || uint64(seq) < low {
			low = uint64(seq)
		}
		high = max(high, uint64(seq))
	}
	if len(wanted) == 0 {
		return wanted, nil
	}
	entries, err := os.ReadDir(s.store.LogDir())
	if err != nil {
		return nil, err
	}
	var first uint64
	for _, entry := range entries {
		if n, ok := ledger.ParseSegmentName(entry.Name()); ok && n <= low && n > first {
			first = n
		}
	}
	if first == 0 {
		return nil, ErrUnavailable
	}
	done := errors.New("agents: source batch complete")
	err = s.scan(ctx, first-1, through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if _, ok := wanted[int64(record.Seq)]; ok {
				wanted[int64(record.Seq)] = record
			}
		}
		if c.Last >= high {
			return done
		}
		return nil
	})
	// scan joins the visitor sentinel with Close's error. Remove only our
	// sentinel, never a simultaneous reader-close failure.
	if joined, ok := err.(interface{ Unwrap() []error }); ok && errors.Is(err, done) {
		var remaining []error
		for _, cause := range joined.Unwrap() {
			if cause != done {
				remaining = append(remaining, cause)
			}
		}
		err = errors.Join(remaining...)
	}
	if err != nil {
		return nil, err
	}
	for _, record := range wanted {
		if record.Seq == 0 {
			return nil, ErrUnavailable
		}
	}
	return wanted, nil
}

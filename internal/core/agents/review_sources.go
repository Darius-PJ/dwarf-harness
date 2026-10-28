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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wanted := map[int64]ledger.Record{}
	bounds := map[int64][2]uint64{}
	var low, high uint64
	for _, seq := range sequences {
		if seq <= 0 || uint64(seq) > through {
			return nil, ErrUnavailable
		}
		if _, seen := wanted[seq]; seen {
			continue
		}
		record, hit := s.history.source(seq, through, s.store.Colony())
		wanted[seq] = record
		if hit {
			continue
		}
		if low == 0 || uint64(seq) < low {
			low = uint64(seq)
		}
		high = max(high, uint64(seq))
	}
	if low == 0 {
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
			if value, ok := wanted[int64(record.Seq)]; ok && value.Seq == 0 {
				wanted[int64(record.Seq)] = record
				bounds[int64(record.Seq)] = [2]uint64{c.First, c.Last}
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
	for seq, bound := range bounds {
		s.history.putSource(wanted[seq], bound[0], bound[1], through)
	}
	return wanted, nil
}

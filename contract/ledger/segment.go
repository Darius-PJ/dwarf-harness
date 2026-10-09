package ledger

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"strings"
)

const (
	segmentDigits = 20
	segmentSuffix = ".jsonl"
)

// SegmentName returns the file name, within the log directory, of the
// segment whose first record has sequence first.
func SegmentName(first uint64) string {
	return fmt.Sprintf("%0*d%s", segmentDigits, first, segmentSuffix)
}

// ParseSegmentName returns the first sequence that a segment file name
// names; it is the inverse of SegmentName. It returns false for a name that
// is not 20 decimal digits followed by ".jsonl", or whose digits exceed the
// range of uint64.
func ParseSegmentName(name string) (first uint64, ok bool) {
	digits, ok := strings.CutSuffix(name, segmentSuffix)
	if !ok || len(digits) != segmentDigits {
		return 0, false
	}
	return parseNat([]byte(digits), math.MaxUint64)
}

// segment is one listed segment file.
type segment struct {
	name  string
	first uint64
}

// listSegments returns the segments of the log directory in sequence order.
// Names beginning with a dot are ignored; any other entry that is not a
// regular file with a segment name fails closed, as does a first segment
// that does not start at seq 1.
func listSegments(dir string) ([]segment, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &CorruptError{Reason: "log directory is missing"}
	}
	if err != nil {
		return nil, err
	}
	var segs []segment
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		first, ok := ParseSegmentName(name)
		if !ok || !e.Type().IsRegular() {
			return nil, &CorruptError{Segment: name, Reason: "not a segment file"}
		}
		// os.ReadDir sorts by name, which for fixed-width names is
		// sequence order.
		segs = append(segs, segment{name: name, first: first})
	}
	if len(segs) > 0 && segs[0].first != 1 {
		return nil, &CorruptError{Segment: segs[0].name, Reason: "first segment does not start at seq 1"}
	}
	return segs, nil
}

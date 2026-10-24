package agents

import (
	"errors"
	"math"
)

var (
	// ErrReviewClock refuses a failed, nonpositive, overflowing or regressed
	// clock sample. It reveals no committed or incarnation clock floor.
	ErrReviewClock = errors.New("agents: invalid review clock")
	// ErrReviewExpired refuses an otherwise valid observation at or beyond a
	// request deadline, or strictly beyond a result deadline.
	ErrReviewExpired = errors.New("agents: review expired")
)

// reviewUnixNano checks normalized Unix seconds and nanoseconds before their
// conversion can wrap, including overflow that would wrap to a positive value.
// It samples no clock; the eventual clock adapter supplies these plain values.
func reviewUnixNano(seconds, nanos int64) (int64, error) {
	const billion = int64(1000000000)
	if seconds < 0 || nanos < 0 || nanos >= billion || seconds > (math.MaxInt64-nanos)/billion {
		return 0, ErrReviewClock
	}
	sample := seconds*billion + nanos
	if sample <= 0 {
		return 0, ErrReviewClock
	}
	return sample, nil
}

// reviewObservation consumes nothing. It returns the incarnation's new floor
// even when a valid sample is expired; an unsuccessful sample cannot raise it.
// The caller supplies the maximum of the committed and incarnation floors.
func reviewObservation(sample int64, sampleErr error, floor, deadline int64, result bool) (int64, error) {
	if sampleErr != nil || sample <= 0 || sample < floor {
		return floor, ErrReviewClock
	}
	if sample > deadline || !result && sample == deadline {
		return sample, ErrReviewExpired
	}
	return sample, nil
}

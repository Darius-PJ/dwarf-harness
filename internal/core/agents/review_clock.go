package agents

import "errors"

var (
	// ErrReviewClock refuses a failed, nonpositive, overflowing or regressed
	// clock sample. It reveals no committed or incarnation clock floor.
	ErrReviewClock = errors.New("agents: invalid review clock")
	// ErrReviewExpired refuses an otherwise valid observation at or beyond a
	// request deadline, or strictly beyond a result deadline.
	ErrReviewExpired = errors.New("agents: review expired")
)

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

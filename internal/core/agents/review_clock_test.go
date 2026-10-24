package agents

import (
	"errors"
	"math"
	"testing"
)

func TestReviewClockSeparatesDeadlineAndRegression(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                    string
		sample, floor, deadline int64
		sampleErr               error
		result                  bool
		wantFloor               int64
		want                    error
	}{
		{"request_before_deadline", 99, 90, 100, nil, false, 99, nil},
		{"request_at_deadline_refuses", 100, 90, 100, nil, false, 100, ErrReviewExpired},
		{"request_after_deadline_refuses", 101, 90, 100, nil, false, 101, ErrReviewExpired},
		{"result_before_deadline", 99, 90, 100, nil, true, 99, nil},
		{"result_at_deadline_is_timely", 100, 90, 100, nil, true, 100, nil},
		{"result_after_deadline_refuses", 101, 90, 100, nil, true, 101, ErrReviewExpired},
		{"request_below_floor", 89, 90, 100, nil, false, 90, ErrReviewClock},
		{"result_below_floor", 89, 90, 100, nil, true, 90, ErrReviewClock},
		{"request_at_floor", 90, 90, 100, nil, false, 90, nil},
		{"result_at_floor", 90, 90, 100, nil, true, 90, nil},
		{"regression_precedes_expiry", 101, 102, 100, nil, true, 102, ErrReviewClock},
		{"zero_is_not_a_sample", 0, 0, 100, nil, true, 0, ErrReviewClock},
		{"negative_is_not_a_sample", -1, 0, 100, nil, false, 0, ErrReviewClock},
		{"wrapped_negative_refuses", math.MinInt64, 90, 100, nil, true, 90, ErrReviewClock},
		{"sampling_error_cannot_raise_floor", 99, 90, 100, errors.New("clock failure"), true, 90, ErrReviewClock},
		{"sampling_error_precedes_expiry", 101, 90, 100, errors.New("clock failure"), false, 90, ErrReviewClock},
		{"maximum_result_has_no_arithmetic_overflow", math.MaxInt64, math.MaxInt64, math.MaxInt64, nil, true, math.MaxInt64, nil},
		{"maximum_request_remains_exclusive", math.MaxInt64, math.MaxInt64, math.MaxInt64, nil, false, math.MaxInt64, ErrReviewExpired},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			floor, err := reviewObservation(row.sample, row.sampleErr, row.floor, row.deadline, row.result)
			if floor != row.wantFloor || !errors.Is(err, row.want) {
				t.Fatalf("observation = %d, %v; want %d, %v", floor, err, row.wantFloor, row.want)
			}
		})
	}
}

func TestReviewClockExpiryCannotLowerIncarnationFloor(t *testing.T) {
	t.Parallel()
	floor, err := reviewObservation(101, nil, 90, 100, true)
	if !errors.Is(err, ErrReviewExpired) || floor != 101 {
		t.Fatalf("expired observation = %d, %v", floor, err)
	}
	floor, err = reviewObservation(100, nil, floor, 200, false)
	if !errors.Is(err, ErrReviewClock) || floor != 101 {
		t.Fatalf("replacement hid rollback: %d, %v", floor, err)
	}
	floor, err = reviewObservation(101, nil, floor, 200, false)
	if err != nil || floor != 101 {
		t.Fatalf("clock recovery at floor = %d, %v", floor, err)
	}
}

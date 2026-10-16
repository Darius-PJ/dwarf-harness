package workspace

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// time.Parse alone accepts comma fractions, excess precision and offsets with
// an hour of 24 or a minute of 60. Admission accepts none of those spellings.
var activationTimestampSyntax = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func parseActivationTimestamp(text string) (int64, error) {
	if !activationTimestampSyntax.MatchString(text) || strings.HasSuffix(text, "-00:00") {
		return 0, fmt.Errorf("%w: not_before must be RFC3339 with a known explicit offset and nanosecond precision", ErrInvalid)
	}
	instant, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return 0, fmt.Errorf("%w: not_before: %v", ErrInvalid, err)
	}
	ns, err := activationTimeNS(instant)
	if err != nil {
		return 0, fmt.Errorf("not_before: %w", err)
	}
	return ns, nil
}

// activationTimeNS checks both endpoints before multiplying seconds. The
// negative endpoint's floor second is itself outside the nanosecond range.
func activationTimeNS(instant time.Time) (int64, error) {
	const billion = int64(time.Second)
	const minSecond = math.MinInt64/billion - 1
	const minFraction = billion + math.MinInt64%billion
	const maxSecond = math.MaxInt64 / billion
	const maxFraction = math.MaxInt64 % billion
	seconds, fraction := instant.Unix(), int64(instant.Nanosecond())
	if seconds < minSecond || seconds > maxSecond ||
		(seconds == minSecond && fraction < minFraction) ||
		(seconds == maxSecond && fraction > maxFraction) {
		return 0, fmt.Errorf("%w: UTC time is outside signed 64-bit nanoseconds", ErrInvalid)
	}
	if seconds < 0 {
		return (seconds+1)*billion + (fraction - billion), nil
	}
	return seconds*billion + fraction, nil
}

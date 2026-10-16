package workspace

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestActivationTimestampBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		want       int64
	}{
		{"epoch", "1970-01-01T00:00:00Z", 0},
		{"before_epoch", "1969-12-31T23:59:59.999999999Z", -1},
		{"fraction", "1970-01-01T00:00:00.1Z", 100000000},
		{"nanosecond", "1970-01-01T00:00:00.000000001Z", 1},
		{"minimum", "1677-09-21T00:12:43.145224192Z", math.MinInt64},
		{"minimum_offset", "1677-09-20T23:12:43.145224192-01:00", math.MinInt64},
		{"maximum", "2262-04-11T23:47:16.854775807Z", math.MaxInt64},
		{"maximum_offset", "2262-04-12T00:47:16.854775807+01:00", math.MaxInt64},
		{"positive_offset", "1970-01-01T05:30:00.123456789+05:30", 123456789},
		{"negative_offset", "1969-12-31T20:30:00.123456789-03:30", 123456789},
		{"zero_offset", "1970-01-01T00:00:00.123456789+00:00", 123456789},
		{"utc_identity", "1970-01-01T00:00:00.123456789Z", 123456789},
		{"largest_positive_offset", "1970-01-01T23:59:00+23:59", 0},
		{"largest_negative_offset", "1969-12-31T00:01:00-23:59", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseActivationTimestamp(tc.text)
			if err != nil || got != tc.want {
				t.Fatalf("parse %q = %d, %v; want %d", tc.text, got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, text string }{
		{"below_minimum", "1677-09-21T00:12:43.145224191Z"},
		{"above_maximum", "2262-04-11T23:47:16.854775808Z"},
		{"earlier_second", "1677-09-21T00:12:42.999999999Z"},
		{"later_second", "2262-04-11T23:47:17Z"},
		{"early_year", "0000-01-01T00:00:00Z"},
		{"late_year", "9999-12-31T23:59:59Z"},
		{"unknown_offset", "2026-10-16T12:00:00-00:00"},
		{"missing_zone", "2026-10-16T12:00:00"},
		{"local_zone", "2026-10-16T12:00:00EST"},
		{"leap_second", "2016-12-31T23:59:60Z"},
		{"subnanosecond", "1970-01-01T00:00:00.1234567891Z"},
		{"extra_zero_precision", "1970-01-01T00:00:00.1234567890Z"},
		{"offset_hour", "1970-01-01T00:00:00+24:00"},
		{"negative_offset_hour", "1970-01-01T00:00:00-24:00"},
		{"offset_minute", "1970-01-01T00:00:00+00:60"},
		{"missing_seconds", "2026-10-16T12:00Z"},
		{"short_hour", "2026-10-16T1:00:00Z"},
		{"comma_fraction", "2026-10-16T12:00:00,5Z"},
		{"empty_fraction", "2026-10-16T12:00:00.Z"},
		{"lowercase_zone", "2026-10-16T12:00:00z"},
		{"space_separator", "2026-10-16 12:00:00Z"},
		{"trailing_space", "2026-10-16T12:00:00Z "},
		{"bad_calendar", "2026-02-29T12:00:00Z"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := parseActivationTimestamp(tc.text); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %q as %d: %v", tc.text, got, err)
			}
		})
	}
}

func TestActivationTimeRange(t *testing.T) {
	t.Parallel()
	for _, want := range []int64{math.MinInt64, -1000000000, -1, 0, 1, math.MaxInt64} {
		// A non-UTC location cannot change the absolute sample's identity.
		sample := time.Unix(0, want).In(time.FixedZone("host", -7*60*60))
		if got, err := activationTimeNS(sample); err != nil || got != want {
			t.Fatalf("sample %v = %d, %v; want %d", sample, got, err, want)
		}
	}
	for _, sample := range []time.Time{
		time.Unix(0, math.MinInt64).Add(-time.Nanosecond),
		time.Unix(0, math.MaxInt64).Add(time.Nanosecond),
		{},
	} {
		if got, err := activationTimeNS(sample); !errors.Is(err, ErrInvalid) {
			t.Fatalf("out-of-range sample %v became %d: %v", sample, got, err)
		}
	}
}

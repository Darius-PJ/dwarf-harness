package ledger

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// seqs returns the seq of every record of commits, in order.
func seqs(commits []Commit) []uint64 {
	var s []uint64
	for _, c := range commits {
		for _, r := range c.Records {
			s = append(s, r.Seq)
		}
	}
	return s
}

// lasts returns the Last of every commit, in order.
func lasts(commits []Commit) []uint64 {
	var s []uint64
	for _, c := range commits {
		s = append(s, c.Last)
	}
	return s
}

func TestReaderConsumption(t *testing.T) {
	tests := []struct {
		name       string
		consume    Consumption
		wantSeqs   []uint64
		wantLasts  []uint64 // Last of each returned commit
		wantCursor uint64
		wantErr    *UnsupportedVersionError
	}{
		{name: "nothing consumed", consume: nil, wantCursor: 6},
		{
			name:     "both sample versions",
			consume:  Consumption{"fixture.sample": {1, 2}},
			wantSeqs: []uint64{1, 2, 4, 6}, wantLasts: []uint64{1, 3, 4, 6}, wantCursor: 6,
		},
		{
			name:     "sample version 1 only",
			consume:  Consumption{"fixture.sample": {1}},
			wantSeqs: []uint64{1, 2}, wantLasts: []uint64{1, 3}, wantCursor: 3,
			wantErr: &UnsupportedVersionError{Seq: 4, Type: "fixture.sample", Version: 2, Supported: []int{1}},
		},
		{
			name:     "other",
			consume:  Consumption{"fixture.other": {1}},
			wantSeqs: []uint64{3, 5}, wantLasts: []uint64{3, 6}, wantCursor: 6,
		},
		{
			name:       "other at an undeclared version",
			consume:    Consumption{"fixture.other": {2}},
			wantCursor: 1,
			wantErr:    &UnsupportedVersionError{Seq: 3, Type: "fixture.other", Version: 1, Supported: []int{2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commits, cursor, err := readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: ToEnd, Consume: tt.consume})
			if tt.wantErr == nil && err != nil {
				t.Fatalf("read error = %v", err)
			}
			if tt.wantErr != nil {
				var ue *UnsupportedVersionError
				if !errors.As(err, &ue) || !errors.Is(err, ErrUnsupportedVersion) || !reflect.DeepEqual(ue, tt.wantErr) {
					t.Fatalf("read error = %#v, want %#v", err, tt.wantErr)
				}
			}
			if got := seqs(commits); !slices.Equal(got, tt.wantSeqs) {
				t.Errorf("record seqs = %v, want %v", got, tt.wantSeqs)
			}
			if got := lasts(commits); !slices.Equal(got, tt.wantLasts) {
				t.Errorf("commit Lasts = %v, want %v", got, tt.wantLasts)
			}
			if cursor != tt.wantCursor {
				t.Errorf("Cursor() = %d, want %d", cursor, tt.wantCursor)
			}
		})
	}
}

func TestReaderCursorAndBound(t *testing.T) {
	full, _, err := readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: ToEnd, Consume: allFixtures})
	if err != nil {
		t.Fatal(err)
	}
	for _, after := range []uint64{0, 1, 3, 4, 6} {
		got, cursor, err := readAll(goldenDir, ReadOptions{Colony: goldenColony, After: after, Through: ToEnd, Consume: allFixtures})
		if err != nil {
			t.Errorf("After %d: %v", after, err)
			continue
		}
		var want []Commit
		for _, c := range full {
			if c.First > after {
				want = append(want, c)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("After %d: commits %v, want the full read's suffix %v", after, lasts(got), lasts(want))
		}
		if cursor != 6 {
			t.Errorf("After %d: Cursor() = %d, want 6", after, cursor)
		}
	}
	for _, after := range []uint64{2, 5, 7} {
		_, cursor, err := readAll(goldenDir, ReadOptions{Colony: goldenColony, After: after, Through: ToEnd, Consume: allFixtures})
		if !errors.Is(err, ErrCursor) || cursor != after {
			t.Errorf("After %d: error %v and Cursor() %d, want ErrCursor and %d", after, err, cursor, after)
		}
	}

	got, cursor, err := readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: 3, Consume: allFixtures})
	if err != nil || !slices.Equal(lasts(got), []uint64{1, 3}) || cursor != 3 {
		t.Errorf("Through 3: commits %v, cursor %d, error %v; want [1 3], 3, nil", lasts(got), cursor, err)
	}
	got, cursor, err = readAll(goldenDir, ReadOptions{Colony: goldenColony, After: 4, Through: 4, Consume: allFixtures})
	if err != nil || len(got) != 0 || cursor != 4 {
		t.Errorf("After 4 Through 4: commits %v, cursor %d, error %v; want none, 4, nil", lasts(got), cursor, err)
	}
	got, cursor, err = readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: 2, Consume: allFixtures})
	if !errors.Is(err, ErrCursor) || !slices.Equal(lasts(got), []uint64{1}) || cursor != 1 {
		t.Errorf("Through 2: commits %v, cursor %d, error %v; want [1], 1, ErrCursor", lasts(got), cursor, err)
	}
	got, cursor, err = readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: 7, Consume: allFixtures})
	if !errors.Is(err, ErrCorrupt) || len(got) != 4 || cursor != 6 {
		t.Errorf("Through 7: commits %v, cursor %d, error %v; want all 4, 6, ErrCorrupt", lasts(got), cursor, err)
	}

	// After Close, Next fails with os.ErrClosed, unless the reader had
	// already failed or reached its end, whose result stays.
	for _, tc := range []struct {
		name    string
		through uint64
		reads   int // Next calls before Close
		want    error
	}{
		{"before reading", ToEnd, 0, os.ErrClosed},
		{"mid-read", ToEnd, 1, os.ErrClosed},
		{"at the end", 3, 3, io.EOF},
		{"after a failure", 2, 2, ErrCursor},
	} {
		r, err := OpenReader(goldenDir, ReadOptions{Colony: goldenColony, Through: tc.through, Consume: allFixtures})
		if err != nil {
			t.Fatal(err)
		}
		for range tc.reads {
			r.Next()
		}
		if err := r.Close(); err != nil {
			t.Errorf("%s: Close = %v", tc.name, err)
		}
		if _, err := r.Next(); !errors.Is(err, tc.want) {
			t.Errorf("%s: Next after Close = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestReadingHasNoEffects(t *testing.T) {
	g := mustGolden(t)
	dir := copyGolden(t)
	next, _, err := EncodeCommit(g.end(), CommitHeader{ColonyID: goldenColony, CommitID: fid("c5"), EventTimeNS: 1791000002000000000}, []Draft{
		{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{"n":7}`)},
		{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{"n":8}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	torn := next[:len(next)-10]
	appendFile(t, filepath.Join(dir, g.final()), torn)
	before := snapshot(t, dir)

	res, err := Check(dir, goldenColony)
	if err != nil || res.Last.Seq != 6 || res.TornBytes != int64(len(torn)) {
		t.Errorf("Check = %+v, %v; want Last.Seq 6 and TornBytes %d", res, err, len(torn))
	}
	commits, _, err := readAll(dir, ReadOptions{Colony: goldenColony, Through: ToEnd, Consume: allFixtures})
	if err != nil || !slices.Equal(lasts(commits), []uint64{1, 3, 4, 6}) {
		t.Errorf("ToEnd read = commits %v, error %v; want [1 3 4 6] and no error", lasts(commits), err)
	}
	assertUnchanged(t, dir, before)
}

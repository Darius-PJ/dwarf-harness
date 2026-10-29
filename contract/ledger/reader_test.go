package ledger

import (
	"bytes"
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

func TestReaderAllTypes(t *testing.T) {
	sample := Consumption{"fixture.sample": {1}}

	// With AllTypes, every record of every commit is returned, and the
	// unconsumed F0 types are not version-checked.
	commits, cursor, err := readAll(catalogueDir, ReadOptions{Colony: catalogueColony, Through: ToEnd, Consume: sample, AllTypes: true})
	if err != nil || cursor != 7 || !slices.Equal(seqs(commits), []uint64{1, 2, 3, 4, 5, 6, 7}) || !slices.Equal(lasts(commits), []uint64{1, 2, 4, 5, 6, 7}) {
		t.Fatalf("AllTypes read: records %v in commits %v, cursor %d, error %v; want 7 records in 6 commits", seqs(commits), lasts(commits), cursor, err)
	}
	if pin := commits[1].Records[0]; pin.Type != TypeBodyPin || !slices.Equal(pin.BodyRefs, []Digest{goldenB2}) {
		t.Errorf("seq 2 = %s with BodyRefs %v, want %s referencing B2", pin.Type, pin.BodyRefs, TypeBodyPin)
	}

	// Without it, the F0 records are skipped.
	commits, cursor, err = readAll(catalogueDir, ReadOptions{Colony: catalogueColony, Through: ToEnd, Consume: sample})
	if err != nil || cursor != 7 || !slices.Equal(seqs(commits), []uint64{1, 4}) || !slices.Equal(lasts(commits), []uint64{1, 4}) {
		t.Errorf("read without AllTypes: records %v in commits %v, cursor %d, error %v; want [1 4] in [1 4], 7", seqs(commits), lasts(commits), cursor, err)
	}

	// A consumed type is still version-checked: fixture.sample v2 at seq 4
	// stops the reader, after fixture.other at seq 3 was returned unchecked.
	commits, cursor, err = readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: ToEnd, Consume: sample, AllTypes: true})
	want := &UnsupportedVersionError{Seq: 4, Type: "fixture.sample", Version: 2, Supported: []int{1}}
	var ue *UnsupportedVersionError
	if !errors.As(err, &ue) || !reflect.DeepEqual(ue, want) {
		t.Errorf("AllTypes read of %s: error %v, want %v", goldenDir, err, want)
	}
	if !slices.Equal(seqs(commits), []uint64{1, 2, 3}) || cursor != 3 {
		t.Errorf("AllTypes read of %s: records %v, cursor %d; want [1 2 3], 3", goldenDir, seqs(commits), cursor)
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
	dir := copyLog(t, goldenDir)
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
	if p, err := assertCheckpointRead(t, dir, ReadOptions{Colony: goldenColony, Through: ToEnd, Consume: allFixtures}, nil); err != nil || p != nil {
		t.Errorf("opt-in torn-tail read: error %v, proof %v", err, p != nil)
	}
	assertUnchanged(t, dir, before)
}

// readWithCheckpoint drains and closes the opt-in reader before asking for
// its proof. EOF alone is success; a joined read/close error is not.
func readWithCheckpoint(dir string, opts ReadOptions, checkpoint *ReadCheckpoint) ([]Commit, uint64, *ReadCheckpoint, error) {
	r, err := OpenReaderWithCheckpoint(dir, opts, checkpoint)
	if err != nil {
		return nil, opts.After, nil, err
	}
	var commits []Commit
	for {
		c, nextErr := r.Next()
		if nextErr != nil {
			if nextErr != io.EOF {
				err = nextErr
			}
			break
		}
		commits = append(commits, c)
	}
	err = errors.Join(err, r.Close())
	return commits, r.Cursor(), r.Checkpoint(), err
}

func requireReaderCheckpoint(t testing.TB, dir string, after, through uint64, previous *ReadCheckpoint) *ReadCheckpoint {
	t.Helper()
	_, cursor, p, err := readWithCheckpoint(dir, ReadOptions{
		Colony: goldenColony, After: after, Through: through, AllTypes: true,
	}, previous)
	if err != nil || cursor != through || p == nil {
		t.Fatalf("checkpoint read (%d,%d]: cursor %d, proof %v, error %v", after, through, cursor, p != nil, err)
	}
	return p
}

// assertCheckpointRead compares complete records (including their owned raw
// bytes), commit boundaries, cursor and typed error fields, not error wording.
func assertCheckpointRead(t testing.TB, dir string, opts ReadOptions, checkpoint *ReadCheckpoint) (*ReadCheckpoint, error) {
	t.Helper()
	before := snapshot(t, dir)
	want, wantCursor, wantErr := readAll(dir, opts)
	got, cursor, next, err := readWithCheckpoint(dir, opts, checkpoint)
	assertUnchanged(t, dir, before)
	if !reflect.DeepEqual(got, want) || cursor != wantCursor {
		t.Fatalf("checkpoint read: commits %+v, cursor %d; ordinary: %+v, %d", got, cursor, want, wantCursor)
	}
	if (err == nil) != (wantErr == nil) {
		t.Fatalf("checkpoint error %v; ordinary %v", err, wantErr)
	}
	if wantErr == nil {
		return next, nil
	}
	if next != nil {
		t.Fatal("failed read issued a checkpoint")
	}
	var wc, gc *CorruptError
	var wf, gf *UnsupportedFormatError
	var wv, gv *UnsupportedVersionError
	switch {
	case errors.As(wantErr, &wc):
		if !errors.Is(err, ErrCorrupt) || !errors.As(err, &gc) ||
			gc.Segment != wc.Segment || gc.Offset != wc.Offset {
			t.Fatalf("checkpoint corruption %v; ordinary %v", err, wantErr)
		}
	case errors.As(wantErr, &wf):
		if !errors.Is(err, ErrUnsupportedFormat) || !errors.As(err, &gf) || *gf != *wf {
			t.Fatalf("checkpoint format error %v; ordinary %v", err, wantErr)
		}
	case errors.As(wantErr, &wv):
		if !errors.Is(err, ErrUnsupportedVersion) || !errors.As(err, &gv) || !reflect.DeepEqual(gv, wv) {
			t.Fatalf("checkpoint version error %v; ordinary %v", err, wantErr)
		}
	case errors.Is(wantErr, ErrCursor):
		if !errors.Is(err, ErrCursor) {
			t.Fatalf("checkpoint error %v; want ErrCursor", err)
		}
	case errors.Is(wantErr, ErrInvalid):
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("checkpoint error %v; want ErrInvalid", err)
		}
	default:
		t.Fatalf("unexpected ordinary read error: %v", wantErr)
	}
	return next, err
}

func TestReaderCheckpointContinuation(t *testing.T) {
	t.Parallel()
	g := mustGolden(t)
	dir := copyLog(t, goldenDir)
	p1 := requireReaderCheckpoint(t, dir, 0, 1, nil)
	p3 := requireReaderCheckpoint(t, dir, 1, 3, p1)
	p4 := requireReaderCheckpoint(t, dir, 3, 4, p3)
	p6 := requireReaderCheckpoint(t, dir, 4, 6, p4)

	// Two genuinely warm continuations in segment 2 must keep that segment's
	// original predecessor, and hash only through each logical boundary.
	chain := g.end()
	previous := p6
	for _, commit := range []string{"c5", "c6"} {
		data, end, err := EncodeCommit(chain, CommitHeader{
			ColonyID: goldenColony, CommitID: fid(commit), EventTimeNS: chain.EventTimeNS + 1,
		}, []Draft{sampleDraft(`{"continued":1}`), sampleDraft(`{"continued":2}`)})
		if err != nil {
			t.Fatal(err)
		}
		appendFile(t, filepath.Join(dir, g.final()), data)
		next, err := assertCheckpointRead(t, dir, ReadOptions{
			Colony: goldenColony, After: chain.Seq, Through: end.Seq, AllTypes: true,
		}, previous)
		if err != nil || next == nil {
			t.Fatalf("continued read: proof %v, error %v", next != nil, err)
		}
		chain, previous = end, next
	}

	for _, tc := range []struct {
		name string
		opts ReadOptions
		p    *ReadCheckpoint
		want error
		mint bool
	}{
		{"consumed version", ReadOptions{After: 3, Through: 4, Consume: Consumption{"fixture.sample": {1}}}, p3, ErrUnsupportedVersion, false},
		{"all types unconsumed version", ReadOptions{After: 3, Through: 4, AllTypes: true}, p3, nil, true},
		{"skipped prefix version", ReadOptions{After: 4, Through: 6, Consume: Consumption{"fixture.sample": {1}}}, p4, nil, true},
		{"nothing consumed", ReadOptions{After: 1, Through: 3}, p1, nil, true},
		{"inside lower commit", ReadOptions{After: 2, Through: 4, AllTypes: true}, p3, ErrCursor, false},
		{"inside upper commit", ReadOptions{After: 1, Through: 2, AllTypes: true}, p1, ErrCursor, false},
		{"equal bounds", ReadOptions{After: 3, Through: 3, AllTypes: true}, p3, nil, false},
		{"to end", ReadOptions{After: 3, Through: ToEnd, AllTypes: true}, p3, nil, false},
		{"reversed bounds", ReadOptions{After: 4, Through: 3, AllTypes: true}, p4, ErrCursor, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Colony = goldenColony
			next, err := assertCheckpointRead(t, dir, tc.opts, tc.p)
			if !errors.Is(err, tc.want) || (next != nil) != tc.mint {
				t.Fatalf("proof %v, error %v; want proof %v, error %v", next != nil, err, tc.mint, tc.want)
			}
		})
	}

	t.Run("multi-buffer prefix", func(t *testing.T) {
		t.Parallel()
		inputs := goldenInputs()
		payload := append([]byte(`{"text":"`), bytes.Repeat([]byte("x"), 70<<10)...)
		inputs[0].drafts[0].Payload = append(payload, []byte(`"}`)...)
		large, err := buildLog("", goldenColony, allFixtures, inputs)
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		large.reset(t, root, "", nil)
		p := requireReaderCheckpoint(t, root, 0, 1, nil)
		p = requireReaderCheckpoint(t, root, 1, 3, p)
		if _, err := assertCheckpointRead(t, root, ReadOptions{
			Colony: goldenColony, After: 3, Through: 4, AllTypes: true,
		}, p); err != nil {
			t.Fatal(err)
		}
	})

	// Mutating one returned copy must not alter later copies from its Reader.
	r, err := OpenReaderWithCheckpoint(dir, ReadOptions{Colony: goldenColony, Through: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("unconsumed drain: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	copy1, copy2 := r.Checkpoint(), r.Checkpoint()
	if copy1 == nil || copy2 == nil {
		t.Fatal("completed drain did not issue a proof")
	}
	*copy1 = ReadCheckpoint{}
	if *copy2 == (ReadCheckpoint{}) {
		t.Fatal("Checkpoint returned aliased mutable storage")
	}
	if _, err := assertCheckpointRead(t, dir, ReadOptions{
		Colony: goldenColony, After: 3, Through: 4, AllTypes: true,
	}, copy2); err != nil {
		t.Fatal(err)
	}
}

func TestReaderCheckpointChangedPrefix(t *testing.T) {
	for _, kind := range []string{"framing", "unsupported format"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			g := mustGolden(t)
			dir := copyLog(t, goldenDir)
			p := requireReaderCheckpoint(t, dir, 0, 3, nil)
			rec := g.record(1)
			path := filepath.Join(dir, rec.seg)
			data := readFile(t, path)
			if kind == "framing" {
				data[0] ^= 1
			} else {
				changed := rehash(t, rec.line, sub(`"format":1`, `"format":2`))
				data = append(changed, data[len(rec.line):]...)
			}
			// Neither the last prefix line (seq 3) nor suffix (seq 4) is
			// changed, so the suffix verifier cannot mask a skipped prefix.
			writeFile(t, path, data)
			_, err := assertCheckpointRead(t, dir, ReadOptions{
				Colony: goldenColony, After: 3, Through: 4, AllTypes: true,
			}, p)
			if kind == "framing" {
				ce := errorsAsCorrupt(t, err)
				if ce.Segment != rec.seg || ce.Offset != rec.off {
					t.Fatalf("corruption location: %+v", ce)
				}
			} else {
				var fe *UnsupportedFormatError
				if !errors.As(err, &fe) || fe.Segment != rec.seg || fe.Offset != rec.off || fe.Format != 2 {
					t.Fatalf("format error: %v", err)
				}
			}
		})
	}
}

func TestReaderCheckpointAnchorAndFallback(t *testing.T) {
	for _, kind := range []string{
		"changed predecessor", "rotation", "truncated prefix", "missing segment",
		"wrong root", "wrong colony", "wrong After", "zero", "nil", "damaged afterHash",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			g := mustGolden(t)
			dir := copyLog(t, goldenDir)
			p := requireReaderCheckpoint(t, dir, 0, 6, nil)
			data, end, err := EncodeCommit(g.end(), CommitHeader{
				ColonyID: goldenColony, CommitID: fid("c5"), EventTimeNS: g.end().EventTimeNS + 1,
			}, []Draft{sampleDraft(`{"n":7}`), sampleDraft(`{"n":8}`)})
			if err != nil {
				t.Fatal(err)
			}
			opts := ReadOptions{Colony: goldenColony, After: 6, Through: end.Seq, AllTypes: true}
			want := error(nil)
			if kind == "rotation" {
				writeFile(t, filepath.Join(dir, SegmentName(7)), data)
			} else {
				appendFile(t, filepath.Join(dir, g.final()), data)
			}
			switch kind {
			case "changed predecessor":
				rec := g.record(4)
				changed := rehash(t, rec.line, sub(`"n":4`, `"n":9`))
				prior := append(bytes.Clone(g.files[rec.seg][:rec.off]), changed...)
				writeFile(t, filepath.Join(dir, rec.seg), prior)
				want = ErrCorrupt
			case "truncated prefix":
				writeFile(t, filepath.Join(dir, g.final()), g.files[g.final()][:len(g.files[g.final()])-1])
				want = ErrCorrupt
			case "missing segment":
				if err := os.Remove(filepath.Join(dir, g.final())); err != nil {
					t.Fatal(err)
				}
				want = ErrCorrupt
			case "wrong root":
				dir = copyLog(t, dir)
			case "wrong colony":
				opts.Colony = fid("09")
				want = ErrCorrupt
			case "wrong After":
				opts.After = 5
				want = ErrCursor
			case "zero":
				p = &ReadCheckpoint{}
			case "nil":
				p = nil
			case "damaged afterHash":
				// Valid source, damaged ending hash, unchanged self-checksum:
				// accepting this hint would invent authoritative corruption.
				p.afterHash[0] ^= 1
			}
			next, err := assertCheckpointRead(t, dir, opts, p)
			if !errors.Is(err, want) || (next != nil) != (want == nil) {
				t.Fatalf("proof %v, error %v; want %v", next != nil, err, want)
			}
			if kind == "changed predecessor" {
				ce := errorsAsCorrupt(t, err)
				if ce.Segment != g.final() || ce.Offset != 0 {
					t.Fatalf("broken anchor location: %+v", ce)
				}
			}
		})
	}
}

func TestReaderCheckpointErrors(t *testing.T) {
	for _, kind := range []string{"framing", "malformed envelope", "unsupported format", "unsupported version", "short numeric end", "partial commit"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			g := mustGolden(t)
			dir := copyLog(t, goldenDir)
			p := requireReaderCheckpoint(t, dir, 0, 3, nil)
			opts := ReadOptions{Colony: goldenColony, After: 3, Through: 4, AllTypes: true}
			rec := g.record(4)
			line := bytes.Clone(rec.line)
			want := ErrCorrupt
			switch kind {
			case "framing":
				line[0] ^= 1
			case "malformed envelope":
				line = rehash(t, line, sub(`"seq":4`, `"seq":null`))
			case "unsupported format":
				line = rehash(t, line, sub(`"format":1`, `"format":2`))
				want = ErrUnsupportedFormat
			case "unsupported version":
				opts.Consume = Consumption{"fixture.sample": {1}}
				want = ErrUnsupportedVersion
			case "short numeric end":
				opts.After, opts.Through = 6, 7
				p = requireReaderCheckpoint(t, dir, 0, 6, nil)
			case "partial commit":
				opts.Through = 6
				final := g.files[g.final()]
				writeFile(t, filepath.Join(dir, g.final()), final[:len(final)-1])
			}
			writeFile(t, filepath.Join(dir, rec.seg), append(bytes.Clone(g.files[rec.seg][:rec.off]), line...))
			if _, err := assertCheckpointRead(t, dir, opts, p); !errors.Is(err, want) {
				t.Fatalf("error %v; want %v", err, want)
			}
		})
	}

	for _, kind := range []string{"early Close", "numeric EOF close failure", "explicit close failure", "read and close failure", "corrupt and close failure", "legacy close failure"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := copyLog(t, goldenDir)
			opts := ReadOptions{Colony: goldenColony, Through: 1, AllTypes: true}
			if kind == "read and close failure" {
				opts.Through = 7 // requires another physical read before completion
			}
			if kind == "corrupt and close failure" {
				g := mustGolden(t)
				rec := g.record(2)
				data := readFile(t, filepath.Join(dir, rec.seg))
				data[rec.off] ^= 1
				writeFile(t, filepath.Join(dir, rec.seg), data)
				opts.Through = 3
			}
			var r *Reader
			var err error
			if kind == "legacy close failure" {
				r, err = OpenReader(dir, opts)
			} else {
				r, err = OpenReaderWithCheckpoint(dir, opts, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if c, err := r.Next(); err != nil || c.First != 1 || c.Last != 1 {
				t.Fatalf("first commit %+v, %v", c, err)
			}
			if r.Checkpoint() != nil {
				t.Fatal("proof issued before terminal EOF and actual closure")
			}
			if kind == "early Close" {
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := r.Next(); !errors.Is(err, os.ErrClosed) || r.Checkpoint() != nil {
					t.Fatalf("early close: Next %v, proof %v", err, r.Checkpoint() != nil)
				}
				return
			}
			// Deliberately invalidate the real OS descriptor. No fake file
			// or close hook supplies the actual Close failure.
			if err := r.s.f.Close(); err != nil {
				t.Fatal(err)
			}
			var nextErr error
			if kind != "explicit close failure" {
				for nextErr == nil {
					_, nextErr = r.Next()
				}
			}
			closeErr := r.Close()
			if kind == "legacy close failure" {
				if nextErr != io.EOF || closeErr != nil || r.Close() != nil || r.Checkpoint() != nil {
					t.Fatalf("legacy closure: Next %v, Close %v", nextErr, closeErr)
				}
				return
			}
			if !errors.Is(closeErr, os.ErrClosed) || !errors.Is(r.Close(), os.ErrClosed) {
				t.Fatalf("actual close failure not retained: %v", closeErr)
			}
			if kind != "explicit close failure" {
				if !errors.Is(nextErr, closeErr) || errors.Is(nextErr, io.EOF) {
					t.Fatalf("terminal error %v lost actual Close error %v or reports EOF", nextErr, closeErr)
				}
				if _, err := r.Next(); err != nextErr {
					t.Fatalf("terminal error not sticky: %v, originally %v", err, nextErr)
				}
			}
			if kind == "read and close failure" {
				var pe *os.PathError
				if !errors.As(nextErr, &pe) || pe.Op != "read" {
					t.Fatalf("actual read error not retained: %v", nextErr)
				}
			}
			if kind == "corrupt and close failure" && !errors.Is(nextErr, ErrCorrupt) {
				t.Fatalf("corruption not retained with Close failure: %v", nextErr)
			}
			if r.Checkpoint() != nil {
				t.Fatal("close failure issued a checkpoint")
			}
		})
	}
}

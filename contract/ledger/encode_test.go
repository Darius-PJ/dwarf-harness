package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"uuid"
)

func TestEncodeCommitRefuses(t *testing.T) {
	header := CommitHeader{ColonyID: goldenColony, CommitID: fid("c1"), EventTimeNS: 100}
	encode := func(after Chain, h CommitHeader, drafts []Draft) ([]byte, error) {
		data, _, err := EncodeCommit(after, h, drafts)
		return data, err
	}

	t.Run("envelope rules", func(t *testing.T) {
		v4 := uuid.MustParse("6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f")
		after := Chain{Seq: 3, Hash: strings.Repeat("ab", 32), EventTimeNS: 100}
		ok := sampleDraft(`{"n":1}`)
		with := func(f func(*Draft)) []Draft {
			d := ok
			f(&d)
			return []Draft{d}
		}
		payload := func(p string) []Draft { return with(func(d *Draft) { d.Payload = json.RawMessage(p) }) }
		at := func(t int64, held bool) CommitHeader {
			return CommitHeader{ColonyID: goldenColony, CommitID: fid("c1"), EventTimeNS: t, ClockHeld: held}
		}

		ds := sortedDigests(goldenB1, goldenB2)
		lo, hi := ds[0], ds[1]
		refs := func(rs ...Digest) []Draft { return with(func(d *Draft) { d.BodyRefs = rs }) }

		tests := []struct {
			name   string
			after  Chain
			header CommitHeader
			drafts []Draft
		}{
			{"no drafts", after, header, nil},
			{"type with one token", after, header, with(func(d *Draft) { d.Type = "fixture" })},
			{"type with an uppercase letter", after, header, with(func(d *Draft) { d.Type = "fixture.Sample" })},
			{"type with an empty token", after, header, with(func(d *Draft) { d.Type = "fixture..sample" })},
			{"type token starting with a digit", after, header, with(func(d *Draft) { d.Type = "fixture.1sample" })},
			{"type version 0", after, header, with(func(d *Draft) { d.TypeVersion = 0 })},
			{"unknown actor", after, header, with(func(d *Draft) { d.Actor = "system" })},
			{"non-v7 optional ID", after, header, with(func(d *Draft) { d.TurnID = v4 })},
			{"non-v7 colony ID", after, CommitHeader{ColonyID: v4, CommitID: fid("c1"), EventTimeNS: 100}, []Draft{ok}},
			{"zero commit ID", after, CommitHeader{ColonyID: goldenColony, EventTimeNS: 100}, []Draft{ok}},
			{"bad measurement path", after, header, with(func(d *Draft) { d.MeasurementPath = "process exit" })},
			{"payload array", after, header, payload(`[]`)},
			{"payload string", after, header, payload(`"x"`)},
			{"payload invalid JSON", after, header, payload(`{"n":`)},
			{"payload absent", after, header, payload(``)},
			{"payload with data after the object", after, header, payload(`{} {}`)},
			{"payload invalid UTF-8", after, header, payload("{\"n\":\"\xff\"}")},
			{"payload duplicate key", after, header, payload(`{"n":1,"n":2}`)},
			{"payload duplicate nested key", after, header, payload(`{"a":{"b":1,"b":2}}`)},
			{"payload duplicate key in an array", after, header, payload(`{"a":[1,{"b":1,"b":1}]}`)},
			{"payload duplicate key by escape", after, header, payload(`{"n":1,"\u006e":2}`)},
			{"time below after", after, at(99, false), []Draft{ok}},
			{"time zero", Genesis(), at(0, false), []Draft{ok}},
			{"clock held at seq 1", Genesis(), at(100, true), []Draft{ok}},
			{"clock held at another time", after, at(101, true), []Draft{ok}},
			{"after not a log position", Chain{Seq: 3, EventTimeNS: 100}, header, []Draft{ok}},
			{"body_refs unsorted", after, header, refs(hi, lo)},
			{"body_refs repeated", after, header, refs(lo, lo)},
			{"body_refs zero", after, header, refs(Digest{})},
			{"body_refs zero before a valid digest", after, header, refs(Digest{}, lo)},
		}
		for _, tt := range tests {
			if data, err := encode(tt.after, tt.header, tt.drafts); !errors.Is(err, ErrInvalid) || data != nil {
				t.Errorf("%s: EncodeCommit = %d bytes, %v; want nothing and ErrInvalid", tt.name, len(data), err)
			}
		}

		// Repeated names in different objects, and a clock held at the
		// previous time, are valid.
		if _, err := encode(after, at(100, true), payload(`{"a":{"x":1},"b":{"x":1},"c":[{"x":1},{"x":2}],"x":0}`)); err != nil {
			t.Errorf("EncodeCommit of a valid commit = %v", err)
		}

		// nil and empty BodyRefs both mean absent: no body_refs key, which
		// readers would reject as [].
		none, err := encode(after, header, refs([]Digest{}...))
		if err != nil {
			t.Fatal(err)
		}
		nilRefs, err := encode(after, header, []Draft{ok})
		if err != nil || !bytes.Equal(none, nilRefs) || bytes.Contains(none, []byte(`body_refs`)) {
			t.Errorf("EncodeCommit with empty and nil BodyRefs = %q and %q, %v; want the same line without body_refs", none, nilRefs, err)
		}
	})

	// pad returns drafts whose lines are exactly the given lengths.
	pad := func(t *testing.T, lengths ...int) []Draft {
		t.Helper()
		drafts := make([]Draft, len(lengths))
		for i := range drafts {
			drafts[i] = sampleDraft(`{"p":""}`)
		}
		data, err := encode(Genesis(), header, drafts)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range slicesOfLines(data) {
			drafts[i].Payload = json.RawMessage(`{"p":"` + strings.Repeat("x", lengths[i]-len(line)) + `"}`)
		}
		return drafts
	}
	// accepted checks that drafts encode to want bytes, and that Check
	// accepts the result as a one-commit log.
	accepted := func(t *testing.T, drafts []Draft, want int) {
		t.Helper()
		data, err := encode(Genesis(), header, drafts)
		if err != nil || len(data) != want {
			t.Fatalf("EncodeCommit = %d bytes, %v; want %d bytes", len(data), err, want)
		}
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, SegmentName(1)), data)
		if res, err := Check(dir, goldenColony); err != nil || res.Last.Seq != uint64(len(drafts)) || res.TornBytes != 0 {
			t.Errorf("Check of the commit = %+v, %v", res, err)
		}
	}
	tooLarge := func(t *testing.T, drafts []Draft) {
		t.Helper()
		if data, err := encode(Genesis(), header, drafts); !errors.Is(err, ErrTooLarge) || errors.Is(err, ErrInvalid) || data != nil {
			t.Errorf("EncodeCommit = %d bytes, %v; want nothing and ErrTooLarge alone", len(data), err)
		}
	}

	t.Run("record cap", func(t *testing.T) {
		accepted(t, pad(t, MaxRecordBytes), MaxRecordBytes)
		tooLarge(t, pad(t, MaxRecordBytes+1))
	})
	t.Run("commit cap", func(t *testing.T) {
		// 17 lines, each under MaxRecordBytes, totalling the cap.
		lengths := make([]int, 17)
		for i := range lengths {
			lengths[i] = MaxCommitBytes / 17
		}
		lengths[16] += MaxCommitBytes % 17
		accepted(t, pad(t, lengths...), MaxCommitBytes)
		lengths[0]++
		tooLarge(t, pad(t, lengths...))
	})
}

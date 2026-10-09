package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"
)

// rehash applies f to the JSON of line and returns the line with the hash of
// the result.
func rehash(t testing.TB, line []byte, f func(js string) string) []byte {
	t.Helper()
	js := string(line[65 : len(line)-1])
	mut := f(js)
	if mut == js {
		t.Fatalf("the mutation changed nothing in %s", js)
	}
	sum := sha256.Sum256([]byte(mut))
	return []byte(hex.EncodeToString(sum[:]) + "\t" + mut + "\n")
}

// sub returns a mutation replacing the first old with new.
func sub(old, new string) func(string) string {
	return func(js string) string { return strings.Replace(js, old, new, 1) }
}

// rewrite applies f to record seq of the golden copy in dir, re-hashes it,
// and re-chains every later record, so that f's change is the only error.
func rewrite(t testing.TB, dir string, seq uint64, f func(js string) string) {
	t.Helper()
	g := mustGolden(t)
	var n uint64
	var oldHash, newHash string // the last record's hash before and after
	for _, name := range g.names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var out []byte
		for line := range bytes.Lines(data) {
			n++
			hash := string(line[:64])
			if oldHash != newHash {
				line = rehash(t, line, sub(`"prev_hash":"`+oldHash+`"`, `"prev_hash":"`+newHash+`"`))
			}
			if n == seq {
				line = rehash(t, line, f)
			}
			oldHash, newHash = hash, string(line[:64])
			out = append(out, line...)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// encodeRaw encodes e with no validation or cap.
func encodeRaw(t testing.TB, e Envelope) []byte {
	t.Helper()
	line, _, err := encodeLine(&e)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func sampleDraft(payload string) Draft {
	return Draft{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(payload)}
}

func TestCheckFailsClosed(t *testing.T) {
	g := mustGolden(t)
	seg1, seg5 := g.names[0], g.final()
	base := int64(len(g.files[seg5])) // E: the end of the golden log's final segment
	off := func(seq uint64) int64 { return g.record(seq).off }
	encode := func(after Chain, commit string, n int) [][]byte {
		drafts := make([]Draft, n)
		for i := range drafts {
			drafts[i] = sampleDraft(`{"n":7}`)
		}
		data, _, err := EncodeCommit(after, CommitHeader{ColonyID: goldenColony, CommitID: fid(commit), EventTimeNS: 1791000002000000000}, drafts)
		if err != nil {
			t.Fatal(err)
		}
		return slicesOfLines(data)
	}
	next := encode(g.end(), "c5", 3) // a 3-record commit continuing the golden log
	stray := encode(Chain{Seq: 7, Hash: strings.Repeat("ab", 32), EventTimeNS: 1791000002000000000}, "c6", 1)[0]
	// A correctly hashed, chained record continuing the golden log whose line
	// is over MaxRecordBytes.
	long := encodeRaw(t, Envelope{
		Format: 1, Seq: 7, CommitID: fid("c5"), CommitCount: 1, PrevHash: g.end().Hash,
		EventTimeNS: 1791000002000000000, ColonyID: goldenColony,
		Draft: sampleDraft(`{"p":"` + strings.Repeat("x", MaxRecordBytes) + `"}`),
	})
	// A complete commit of 17 records, each under MaxRecordBytes, together
	// over MaxCommitBytes, in a non-final segment, and a valid commit after
	// it in the final segment.
	var big [][]byte
	chain := g.end()
	for i := range 17 {
		line := encodeRaw(t, Envelope{
			Format: 1, Seq: chain.Seq + 1, CommitID: fid("c5"), CommitIndex: i, CommitCount: 17, PrevHash: chain.Hash,
			EventTimeNS: 1791000002000000000, ColonyID: goldenColony,
			Draft: sampleDraft(`{"p":"` + strings.Repeat("x", 1_000_000-460) + `"}`),
		})
		if len(line) > MaxRecordBytes {
			t.Fatalf("big record %d is %d bytes, over the cap", i, len(line))
		}
		big = append(big, line)
		chain = Chain{Seq: chain.Seq + 1, Hash: string(line[:64]), EventTimeNS: 1791000002000000000}
	}
	afterBig := encode(chain, "c6", 1)[0]
	var bigOff int64 // offset of the record that takes the commit over the cap
	for _, line := range big[:16] {
		bigOff += int64(len(line))
	}
	if bigOff > MaxCommitBytes || bigOff+int64(len(big[16])) <= MaxCommitBytes {
		t.Fatalf("big commit sizes do not straddle MaxCommitBytes: %d then %d", bigOff, bigOff+int64(len(big[16])))
	}

	tail := func(parts ...[]byte) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { appendFile(t, filepath.Join(dir, seg5), bytes.Join(parts, nil)) }
	}
	last := func(f func(string) string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { rewrite(t, dir, 6, f) }
	}
	// wrongPrev replaces record seq's prev_hash with another well-formed hash.
	wrongPrev := func(seq uint64) func(string) string {
		prev := string(g.record(seq - 1).line[:64])
		return sub(`"prev_hash":"`+prev+`"`, `"prev_hash":"`+strings.Repeat("1", 64)+`"`)
	}
	timeOf := func(seqs []uint64, old, new string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			for _, seq := range seqs {
				rewrite(t, dir, seq, sub(`"event_time_ns":`+old+`,`, `"event_time_ns":`+new+`,`))
			}
		}
	}

	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string)
		colony     uuid.UUID // zero: the golden colony
		readAfter  uint64    // nonzero: read with a Reader from this cursor instead of Check
		wantSeg    string
		wantOff    int64
		wantFormat int // nonzero: an *UnsupportedFormatError with this format
	}{
		{
			name: "flipped JSON byte in segment 1",
			setup: func(t *testing.T, dir string) {
				flip(t, filepath.Join(dir, seg1), off(2)+100, 0x01)
			},
			wantSeg: seg1, wantOff: off(2),
		},
		{
			name: "uppercase hash in segment 1",
			setup: func(t *testing.T, dir string) {
				path := filepath.Join(dir, seg1)
				data := readFile(t, path)
				r := g.record(2)
				upper := bytes.ToUpper(r.line[:64])
				if bytes.Equal(upper, r.line[:64]) {
					t.Fatal("seq 2's hash has no letter to uppercase")
				}
				copy(data[r.off:], upper)
				writeFile(t, path, data)
			},
			wantSeg: seg1, wantOff: off(2),
		},
		{
			name:    "wrong prev_hash on the last record",
			setup:   last(wrongPrev(6)),
			wantSeg: seg5, wantOff: off(6),
		},
		{
			name: "wrong prev_hash in segment 1, later records re-chained",
			setup: func(t *testing.T, dir string) {
				rewrite(t, dir, 3, wrongPrev(3))
			},
			wantSeg: seg1, wantOff: off(3),
		},
		{
			name:    "decreasing time in segment 1, later records re-chained",
			setup:   timeOf([]uint64{2, 3}, "1791000000000000500", "1790999999999999999"),
			wantSeg: seg1, wantOff: off(2),
		},
		{
			name:    "decreasing time in the final commit",
			setup:   timeOf([]uint64{5, 6}, "1791000001000000000", "1791000000000000400"),
			wantSeg: seg5, wantOff: off(5),
		},
		{
			name:    "format 2 on the last record",
			setup:   last(sub(`{"format":1,`, `{"format":2,`)),
			wantSeg: seg5, wantOff: off(6), wantFormat: 2,
		},
		{
			name:    "format not the first key of the last record",
			setup:   last(sub(`{"format":1,"seq":6,`, `{"seq":6,"format":1,`)),
			wantSeg: seg5, wantOff: off(6),
		},
		{
			name:    "duplicate key on the last record",
			setup:   last(sub(`"actor":"core"`, `"actor":"core","actor":"core"`)),
			wantSeg: seg5, wantOff: off(6),
		},
		{
			name:    "null on the last record",
			setup:   last(sub(`"actor":"core"`, `"actor":null`)),
			wantSeg: seg5, wantOff: off(6),
		},
		{
			name:    "clock_held false on the last record",
			setup:   last(sub(`,"colony_id"`, `,"clock_held":false,"colony_id"`)),
			wantSeg: seg5, wantOff: off(6),
		},
		{
			name:    "non-compact JSON on the last record",
			setup:   last(sub(`"seq":6`, `"seq": 6`)),
			wantSeg: seg5, wantOff: off(6),
		},
		{
			name: "segment 1 truncated mid-commit",
			setup: func(t *testing.T, dir string) {
				if err := os.Truncate(filepath.Join(dir, seg1), off(3)+20); err != nil {
					t.Fatal(err)
				}
			},
			wantSeg: seg1, wantOff: off(3),
		},
		{
			name: "gap: segment 5 renamed to 6",
			setup: func(t *testing.T, dir string) {
				if err := os.Rename(filepath.Join(dir, seg5), filepath.Join(dir, SegmentName(6))); err != nil {
					t.Fatal(err)
				}
			},
			wantSeg: SegmentName(6),
		},
		{
			name: "segment 1 removed",
			setup: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, seg1)); err != nil {
					t.Fatal(err)
				}
			},
			wantSeg: seg5,
		},
		{
			name:    "unknown entry in the log directory",
			setup:   func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, "notes.txt"), []byte("x")) },
			wantSeg: "notes.txt",
		},
		{
			name:    "another colony's log",
			setup:   func(*testing.T, string) {},
			colony:  fid("02"),
			wantSeg: seg1,
		},
		{
			name:    "more than MaxCommitBytes of garbage appended",
			setup:   tail(bytes.Repeat([]byte("x"), MaxCommitBytes+1)),
			wantSeg: seg5, wantOff: base,
		},
		{
			name:    "garbage, then a correctly hashed record with seq 8",
			setup:   tail([]byte("garbage\n"), stray),
			wantSeg: seg5, wantOff: base + int64(len("garbage\n")),
		},
		{
			name:    "garbage with no LF, then the next commit's first record",
			setup:   tail([]byte("garbage"), next[0]),
			wantSeg: seg5, wantOff: base + int64(len("garbage")),
		},
		{
			name:    "a torn commit's record repeated",
			setup:   tail(next[0], next[1], next[1]),
			wantSeg: seg5, wantOff: base + int64(len(next[0])+len(next[1])),
		},
		{
			name:    "a torn commit's records disagree on the time",
			setup:   tail(next[0], rehash(t, next[1], sub(`"event_time_ns":1791000002000000000`, `"event_time_ns":1791000002000000001`))),
			wantSeg: seg5, wantOff: base + int64(len(next[0])),
		},
		{
			name:    "a correctly hashed line over MaxRecordBytes",
			setup:   tail(long),
			wantSeg: seg5, wantOff: base,
		},
		{
			name: "a complete commit over MaxCommitBytes",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, SegmentName(7)), bytes.Join(big, nil))
				writeFile(t, filepath.Join(dir, SegmentName(24)), afterBig)
			},
			wantSeg: SegmentName(7), wantOff: bigOff,
		},
		{
			name: "a damaged last line of segment 1 when a Reader starts at After 4",
			setup: func(t *testing.T, dir string) {
				flip(t, filepath.Join(dir, seg1), off(4)+100, 0x01)
			},
			readAfter: 4,
			wantSeg:   seg1, wantOff: off(4),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyLog(t, goldenDir)
			tt.setup(t, dir)
			before := snapshot(t, dir)
			colony := goldenColony
			if tt.colony != (uuid.UUID{}) {
				colony = tt.colony
			}
			var err error
			if tt.readAfter != 0 {
				_, _, err = readAll(dir, ReadOptions{Colony: colony, After: tt.readAfter, Through: ToEnd, Consume: allFixtures})
			} else {
				_, err = Check(dir, colony)
			}
			assertUnchanged(t, dir, before)
			if tt.wantFormat != 0 {
				var fe *UnsupportedFormatError
				if !errors.As(err, &fe) || !errors.Is(err, ErrUnsupportedFormat) || errors.Is(err, ErrCorrupt) {
					t.Fatalf("error = %v, want an *UnsupportedFormatError", err)
				}
				if *fe != (UnsupportedFormatError{Segment: tt.wantSeg, Offset: tt.wantOff, Format: tt.wantFormat}) {
					t.Errorf("error = %+v, want segment %s offset %d format %d", *fe, tt.wantSeg, tt.wantOff, tt.wantFormat)
				}
				return
			}
			ce := errorsAsCorrupt(t, err)
			if ce.Segment != tt.wantSeg || ce.Offset != tt.wantOff {
				t.Errorf("error = %v, want it located at %s offset %d", err, tt.wantSeg, tt.wantOff)
			}
		})
	}

	t.Run("dot entries are ignored", func(t *testing.T) {
		dir := copyLog(t, goldenDir)
		writeFile(t, filepath.Join(dir, ".DS_Store"), []byte("x"))
		res, err := Check(dir, goldenColony)
		if err != nil || res.Last != g.end() || res.TornBytes != 0 {
			t.Errorf("Check = %+v, %v; want the golden end and no error", res, err)
		}
	})
	t.Run("an empty log directory is an empty log", func(t *testing.T) {
		res, err := Check(t.TempDir(), goldenColony)
		if err != nil || res != (CheckResult{Last: Genesis()}) {
			t.Errorf("Check = %+v, %v; want Genesis() with no segment", res, err)
		}
	})

	// body_refs on the catalogue log's last record, which has none, re-hashed
	// so that the reference set is the only error.
	c := mustCatalogue(t)
	tip := c.record(c.end().Seq)
	ds := sortedDigests(goldenB1, goldenB2)
	lo, hi := `"`+ds[0].String()+`"`, `"`+ds[1].String()+`"`
	upper := strings.ToUpper(lo)
	if upper == lo {
		t.Fatal("the digest has no letter to uppercase")
	}
	withRefs := func(t *testing.T, refs string) string {
		t.Helper()
		dir := copyLog(t, catalogueDir)
		path := filepath.Join(dir, tip.seg)
		data := readFile(t, path)[:tip.off]
		writeFile(t, path, append(data, rehash(t, tip.line, sub(`,"payload":`, `,"body_refs":`+refs+`,"payload":`))...))
		return dir
	}
	for _, tc := range []struct{ name, refs string }{
		{"empty", `[]`},
		{"null", `null`},
		{"a string", lo},
		{"a number element", `[1]`},
		{"unsorted", `[` + hi + `,` + lo + `]`},
		{"repeated", `[` + lo + `,` + lo + `]`},
		{"uppercase", `[` + upper + `]`},
		{"short", `[` + lo[:64] + `"]`},
		{"zero", `["` + ZeroHash + `"]`},
	} {
		t.Run("body_refs "+tc.name, func(t *testing.T) {
			dir := withRefs(t, tc.refs)
			before := snapshot(t, dir)
			_, err := Check(dir, catalogueColony)
			assertUnchanged(t, dir, before)
			if ce := errorsAsCorrupt(t, err); ce.Segment != tip.seg || ce.Offset != tip.off {
				t.Errorf("error = %v, want it located at %s offset %d", err, tip.seg, tip.off)
			}
		})
	}
	t.Run("body_refs valid", func(t *testing.T) {
		res, err := Check(withRefs(t, `[`+lo+`,`+hi+`]`), catalogueColony)
		if err != nil || res.Last.Seq != tip.seq || res.TornBytes != 0 {
			t.Errorf("Check = %+v, %v; want the whole log and no error", res, err)
		}
	})
}

// slicesOfLines splits data into its LF-terminated lines.
func slicesOfLines(data []byte) [][]byte {
	var lines [][]byte
	for line := range bytes.Lines(data) {
		lines = append(lines, line)
	}
	return lines
}

func readFile(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// flip XORs the byte at off of the file at path with mask.
func flip(t testing.TB, path string, off int64, mask byte) {
	t.Helper()
	data := readFile(t, path)
	data[off] ^= mask
	writeFile(t, path, data)
}

package ledger

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

// boundaries returns the commit boundaries of the golden segment seg: 0 and
// the end of each of its commits.
func (g *goldenLog) boundaries(seg string) []int64 {
	b := []int64{0}
	for _, c := range g.commits {
		if c.seg == seg {
			b = append(b, c.end)
		}
	}
	return b
}

// chainAt returns the chain position at the end of the golden commits that
// end at or before offset end of seg, counting every earlier segment.
func (g *goldenLog) chainAt(seg string, end int64) Chain {
	chain := Genesis()
	for _, c := range g.commits {
		if c.seg < seg || c.seg == seg && c.end <= end {
			chain = c.after
		}
	}
	return chain
}

// goldenLasts returns the Last of every golden commit up to seq last.
func (g *goldenLog) goldenLasts(last uint64) []uint64 {
	var s []uint64
	for _, c := range g.commits {
		if c.after.Seq <= last {
			s = append(s, c.after.Seq)
		}
	}
	return s
}

// reset writes g into dir with segment name's bytes replaced by data. One
// directory serves every execution of a fuzz target, because creating and
// removing one per execution dominates its cost on Windows.
func (g *goldenLog) reset(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	for _, n := range g.names {
		b := g.files[n]
		if n == name {
			b = data
		}
		writeFile(t, filepath.Join(dir, n), b)
	}
}

func FuzzCheckTruncation(f *testing.F) {
	// The cut segments: both of the golden log's, then the catalogue log's
	// one, each log in its own directory.
	type target struct {
		g    *goldenLog
		name string
		dir  string
	}
	var targets []target
	for _, g := range []*goldenLog{mustGolden(f), mustCatalogue(f), mustWorkspace(f), mustPromotion(f), mustScratchChange(f), mustFinalization(f), mustActivation(f), mustGraph(f)} {
		dir := f.TempDir()
		for _, name := range g.names {
			targets = append(targets, target{g: g, name: name, dir: dir})
		}
	}
	for i, tg := range targets {
		for _, b := range tg.g.boundaries(tg.name) {
			for _, d := range []int64{-1, 0, 1} {
				if cut := b + d; cut >= 0 {
					f.Add(uint8(i), uint32(cut))
				}
			}
		}
	}
	f.Fuzz(func(t *testing.T, segIndex uint8, cut uint32) {
		tg := targets[int(segIndex)%len(targets)]
		g, name, dir := tg.g, tg.name, tg.dir
		size := int64(len(g.files[name]))
		at := int64(cut) % (size + 1)
		g.reset(t, dir, name, g.files[name][:at])
		res, err := Check(dir, g.colony)
		commits, _, rerr := readAll(dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: g.consume})

		if name != g.final() {
			if at == size {
				if err != nil || res.Last != g.end() {
					t.Fatalf("untruncated: Check = %+v, %v", res, err)
				}
				return
			}
			if !errors.Is(err, ErrCorrupt) || !errors.Is(rerr, ErrCorrupt) {
				t.Fatalf("segment %s cut at %d of %d: Check error %v, read error %v; want ErrCorrupt", name, at, size, err, rerr)
			}
			return
		}
		var boundary int64
		for _, b := range g.boundaries(name) {
			if b <= at {
				boundary = b
			}
		}
		want := CheckResult{Last: g.chainAt(name, boundary), Segment: name, Size: boundary, TornBytes: at - boundary}
		if err != nil || res != want {
			t.Fatalf("%s: final segment cut at %d: Check = %+v, %v; want %+v", g.dir, at, res, err, want)
		}
		if got := lasts(commits); rerr != nil || !slices.Equal(got, g.goldenLasts(want.Last.Seq)) {
			t.Fatalf("%s: final segment cut at %d: read %v, %v; want %v", g.dir, at, got, rerr, g.goldenLasts(want.Last.Seq))
		}
	})
}

func FuzzCheckTornTail(f *testing.F) {
	g := mustGolden(f)
	// next is a complete two-record commit continuing the golden log.
	next, nextEnd, err := EncodeCommit(g.end(), CommitHeader{ColonyID: goldenColony, CommitID: fid("c5"), EventTimeNS: 1791000002000000000}, []Draft{
		sampleDraft(`{"n":7}`), sampleDraft(`{"n":8}`),
	})
	if err != nil {
		f.Fatal(err)
	}
	first := slicesOfLines(next)[0]
	// Seeds are strict prefixes of next, cut far enough from its end that a
	// mutation cannot complete it by chance.
	f.Add([]byte{})
	f.Add(make([]byte, 4096))
	f.Add([]byte("\n"))
	f.Add(next[:len(first)/2])
	f.Add(next[:len(first)+len(first)/2])
	dir := f.TempDir()
	final := g.files[g.final()]
	f.Fuzz(func(t *testing.T, tail []byte) {
		g.reset(t, dir, g.final(), append(slices.Clip(final), tail...))
		res, err := Check(dir, goldenColony)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Check error = %v, want ErrCorrupt or success", err)
			}
			return
		}
		size := int64(len(final))
		want := CheckResult{Last: g.end(), Segment: g.final(), Size: size, TornBytes: int64(len(tail))}
		// Only next itself can be a complete commit after the golden log, and
		// the oracle accepts it if a mutation completes a seed.
		if bytes.HasPrefix(tail, next) {
			want = CheckResult{Last: nextEnd, Segment: g.final(), Size: size + int64(len(next)), TornBytes: int64(len(tail) - len(next))}
		}
		if res != want {
			t.Fatalf("Check = %+v, want %+v", res, want)
		}
	})
}

func FuzzCheckCorruption(f *testing.F) {
	g := mustGolden(f)
	total := uint32(0)
	for _, name := range g.names {
		total += uint32(len(g.files[name]))
	}
	for _, r := range g.records {
		pos := uint32(r.off)
		if r.seg == g.final() {
			pos += uint32(len(g.files[g.names[0]]))
		}
		f.Add(pos, byte(0x01))                       // the record's first hash digit
		f.Add(pos+64, byte('\t'^'x'))                // its TAB
		f.Add(pos+uint32(len(r.line))-1, byte(0x0a)) // its LF
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, pos uint32, mask byte) {
		if mask == 0 {
			return
		}
		pos %= total
		// Locate pos in its segment and its record.
		name := g.names[0]
		off := int64(pos)
		if size := int64(len(g.files[name])); off >= size {
			name, off = g.final(), off-size
		}
		var damaged goldenRecord
		for _, r := range g.records {
			if r.seg == name && r.off <= off {
				damaged = r
			}
		}
		data := bytes.Clone(g.files[name])
		data[off] ^= mask
		g.reset(t, dir, name, data)
		res, err := Check(dir, goldenColony)
		if name != g.final() {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("seq %d damaged at %s offset %d: Check = %+v, %v; want ErrCorrupt", damaged.seq, name, off, res, err)
			}
			return
		}
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("seq %d damaged: Check error = %v, want ErrCorrupt or success", damaged.seq, err)
			}
			return
		}
		// Success is a torn tail starting at the damaged record's commit.
		var start int64
		for _, c := range g.commits {
			if c.seg == name && c.first <= damaged.seq {
				start = c.start
			}
		}
		size := int64(len(g.files[name]))
		want := CheckResult{Last: g.chainAt(name, start), Segment: name, Size: start, TornBytes: size - start}
		if res != want || res.Last.Seq >= damaged.seq {
			t.Fatalf("seq %d damaged: Check = %+v, want %+v", damaged.seq, res, want)
		}
	})
}

func FuzzDecodeF2_05(f *testing.F) {
	fixtures := f2Payloads()
	for i, tc := range fixtures {
		f.Add(uint8(i), []byte(mustEncode(tc.p)))
		f.Add(uint8(i), []byte(`{}`))
	}
	f.Add(uint8(3), []byte(mustEncode(ScratchClosed{ScratchID: fid("51"), ContextID: fid("d1"), Reason: "merged"})))
	temporary := fixtures[2].p.(ScratchCreated)
	temporary.ParentScratchID = fid("53")
	f.Add(uint8(2), []byte(mustEncode(temporary)))
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeF2_05(typ, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
			return
		}
		js, err := p.Encode()
		if err != nil {
			t.Fatalf("Encode decoded %+v: %v", p, err)
		}
		again, err := decodeF2_05(typ, js)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("Decode re-encoded %s = %+v, %v; want %+v", js, again, err, p)
		}
	})
}

// FuzzDecodeF2_06 exercises ticket 06's part of F2, frozen (D118),
// including additive unknown failure outcomes.
func FuzzDecodeF2_06(f *testing.F) {
	fixtures := f2_06Payloads()
	for i, tc := range fixtures {
		f.Add(uint8(i), []byte(mustEncode(tc.p)))
		f.Add(uint8(i), []byte(`{}`))
	}
	p := fixtures[0].p.(PromotionAdmitted)
	p.ScratchBase, p.BaselineTree = strings.Repeat("a", 64), strings.Repeat("b", 64)
	p.DeltaBaseTree, p.SealedTree = strings.Repeat("c", 64), strings.Repeat("d", 64)
	f.Add(uint8(0), []byte(mustEncode(p)))
	p.ScratchObservationID, p.MergeBackIDs = fid("5d"), []uuid.UUID{fid("96"), fid("95")}
	f.Add(uint8(0), []byte(mustEncode(p)))
	generation := fixtures[3].p.(GenerationDerived)
	generation.PredecessorWitnessSeq = 9
	f.Add(uint8(3), []byte(mustEncode(generation)))
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeF2_06(typ, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
			return
		}
		js, err := p.Encode()
		if err != nil {
			t.Fatalf("Encode decoded %+v: %v", p, err)
		}
		again, err := decodeF2_06(typ, js)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("Decode re-encoded %s = %+v, %v; want %+v", js, again, err, p)
		}
		if encoded, err := again.Encode(); err != nil || !bytes.Equal(encoded, js) {
			t.Fatalf("re-encoding = %s, %v; want %s", encoded, err, js)
		}
	})
}

// FuzzDecodeF2_07 exercises ticket 07's part of F2, frozen (D119),
// including empty paths and open failure tokens.
func FuzzDecodeF2_07(f *testing.F) {
	fixtures := f2_07Payloads()
	for i, tc := range fixtures {
		f.Add(uint8(i), []byte(mustEncode(tc.p)))
		f.Add(uint8(i), []byte(`{}`))
	}
	wide := fixtures[0].p.(ScratchChanged)
	wide.PreviousCommit, wide.PreviousTree = strings.Repeat("c0", 32), strings.Repeat("c1", 32)
	wide.ScratchCommit, wide.ScratchTree = strings.Repeat("c2", 32), strings.Repeat("c3", 32)
	f.Add(uint8(0), []byte(mustEncode(wide)))
	merged := fixtures[7].p.(ScratchMergeBack)
	merged.ChangedPaths = []string{}
	f.Add(uint8(7), []byte(mustEncode(merged)))
	failed := fixtures[4].p.(ScratchMergeBack)
	failed.Failure, failed.WrittenPaths = "future_failure", []string{"a\tb\nc"}
	f.Add(uint8(4), []byte(mustEncode(failed)))
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeF2_07(typ, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
			return
		}
		js, err := p.Encode()
		if err != nil {
			t.Fatalf("Encode decoded %+v: %v", p, err)
		}
		again, err := decodeF2_07(typ, js)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("Decode re-encoded %s = %+v, %v; want %+v", js, again, err, p)
		}
		if encoded, err := again.Encode(); err != nil || !bytes.Equal(encoded, js) {
			t.Fatalf("re-encoding = %s, %v; want %s", encoded, err, js)
		}
	})
}

// FuzzDecodeF2_08 exercises ticket 08's part of F2, frozen (D123),
// including the optional witness and non-empty path set.
func FuzzDecodeF2_08(f *testing.F) {
	fixtures := f2_08Payloads()
	for i, tc := range fixtures {
		f.Add(uint8(i), []byte(mustEncode(tc.p)))
		f.Add(uint8(i), []byte(`{}`))
	}
	wide := fixtures[0].p.(FinalizationIntended)
	wide.PredecessorCommit, wide.PredecessorTree = strings.Repeat("e6", 32), strings.Repeat("e7", 32)
	wide.TargetCommit, wide.TargetTree = strings.Repeat("e9", 32), strings.Repeat("e8", 32)
	f.Add(uint8(0), []byte(mustEncode(wide)))
	wide.PredecessorWitnessSeq = 1<<63 - 1
	f.Add(uint8(0), []byte(mustEncode(wide)))
	wide.ActivationRecordHash = goldenB1
	f.Add(uint8(0), []byte(mustEncode(wide)))
	accepted := fixtures[2].p.(PromotionAccepted)
	accepted.ChangedPaths = []string{"a\tb\nc"}
	f.Add(uint8(2), []byte(mustEncode(accepted)))
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeF2_08(typ, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
			return
		}
		js, err := p.Encode()
		if err != nil {
			t.Fatalf("Encode decoded %+v: %v", p, err)
		}
		again, err := decodeF2_08(typ, js)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("Decode re-encoded %s = %+v, %v; want %+v", js, again, err, p)
		}
		if encoded, err := again.Encode(); err != nil || !bytes.Equal(encoded, js) {
			t.Fatalf("re-encoding = %s, %v; want %s", encoded, err, js)
		}
	})
}

// FuzzDecodeF2_09 exercises ticket 09's part of F2, frozen (D124),
// including signed instants and additive unknown outcomes.
func FuzzDecodeF2_09(f *testing.F) {
	fixtures := f2_09Payloads()
	for i, tc := range fixtures {
		f.Add(uint8(i), []byte(mustEncode(tc.p)))
		f.Add(uint8(i), []byte(`{}`))
	}
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeF2_09(typ, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
			return
		}
		js, err := p.Encode()
		if err != nil {
			t.Fatalf("Encode decoded %+v: %v", p, err)
		}
		again, err := decodeF2_09(typ, js)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("Decode re-encoded %s = %+v, %v; want %+v", js, again, err, p)
		}
		if encoded, err := again.Encode(); err != nil || !bytes.Equal(encoded, js) {
			t.Fatalf("re-encoding = %s, %v; want %s", encoded, err, js)
		}
	})
}

// FuzzDecodeU3_61 exercises F-U3-61, ticket 61's part and U3's first producer
// family, proposed for the project manager's freeze under D125 after the
// build advisor's approval, including nested values and additive open tokens.
func FuzzDecodeU3_61(f *testing.F) {
	fixtures := u3_61Payloads()
	for i, tc := range fixtures {
		f.Add(uint8(i), []byte(mustEncode(tc.p)))
		f.Add(uint8(i), []byte(`{}`))
	}
	f.Fuzz(func(t *testing.T, kind uint8, data []byte) {
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeU3_61(typ, data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
			return
		}
		js, err := p.Encode()
		if err != nil {
			t.Fatalf("Encode decoded %+v: %v", p, err)
		}
		again, err := decodeU3_61(typ, js)
		if err != nil || !reflect.DeepEqual(again, p) {
			t.Fatalf("Decode re-encoded = %+v, %v; want %+v", again, err, p)
		}
		if encoded, err := again.Encode(); err != nil || !bytes.Equal(encoded, js) {
			t.Fatalf("re-encoding = %s, %v; want %s", encoded, err, js)
		}
	})
}

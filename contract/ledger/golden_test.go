package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"uuid"
)

var update = flag.Bool("update", false, "rewrite the golden F0 log in testdata")

// goldenDir holds the golden F0 log, a two-segment log directory.
const goldenDir = "testdata/f0/log"

// fid returns a fixed fixture UUIDv7 ending in suffix.
func fid(suffix string) uuid.UUID {
	return uuid.MustParse("0199c000-0000-7000-8000-0000000000" + suffix)
}

var goldenColony = fid("01")

// goldenCommit is one commit of the golden log's fixed inputs.
type goldenCommit struct {
	header     CommitHeader
	newSegment bool // the commit starts a new segment
	drafts     []Draft
}

func goldenInputs() []goldenCommit {
	header := func(commit string, t int64, held bool) CommitHeader {
		return CommitHeader{ColonyID: goldenColony, CommitID: fid(commit), EventTimeNS: t, ClockHeld: held}
	}
	return []goldenCommit{
		{header: header("c1", 1791000000000000000, false), drafts: []Draft{
			{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{"n":1}`)},
		}},
		{header: header("c2", 1791000000000000500, false), drafts: []Draft{
			{
				Type: "fixture.sample", TypeVersion: 1, Actor: ActorAgent,
				AgentID: fid("a1"), ExecutionSessionID: fid("b1"), ContextID: fid("d1"), TurnID: fid("e1"), OperationID: fid("f1"),
				MeasurementPath: "process_exit",
				Payload:         json.RawMessage(`{"n":2,"text":"dwarf ⚒ <&>"}`),
			},
			{Type: "fixture.other", TypeVersion: 1, Actor: ActorRunner, OperationID: fid("f1"), Payload: json.RawMessage(`{}`)},
		}},
		{header: header("c3", 1791000000000000500, true), drafts: []Draft{
			{Type: "fixture.sample", TypeVersion: 2, Actor: ActorOwner, Payload: json.RawMessage(`{"n":4,"note":"v2"}`)},
		}},
		{header: header("c4", 1791000001000000000, false), newSegment: true, drafts: []Draft{
			{Type: "fixture.other", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{}`)},
			{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, Payload: json.RawMessage(`{"n":6}`)},
		}},
	}
}

// goldenRecord locates one record of the golden log.
type goldenRecord struct {
	seq  uint64
	seg  string
	off  int64
	line []byte
}

// goldenBoundary is the end of one golden commit.
type goldenBoundary struct {
	seg   string
	start int64 // offset of the commit's first record in seg
	end   int64 // offset after its last record
	first uint64
	after Chain // the chain position after the commit
}

type goldenLog struct {
	names   []string          // segment names in order
	files   map[string][]byte // segment name -> bytes
	records []goldenRecord    // in seq order
	commits []goldenBoundary  // in order
}

func (g *goldenLog) record(seq uint64) goldenRecord { return g.records[seq-1] }

// end returns the chain position after the whole golden log.
func (g *goldenLog) end() Chain { return g.commits[len(g.commits)-1].after }

// final returns the name of the final golden segment.
func (g *goldenLog) final() string { return g.names[len(g.names)-1] }

// buildGolden encodes the golden inputs with EncodeCommit, the one encoder.
func buildGolden() (*goldenLog, error) {
	g := &goldenLog{files: map[string][]byte{}}
	chain := Genesis()
	var seg string
	for i, c := range goldenInputs() {
		if i == 0 || c.newSegment {
			seg = SegmentName(chain.Seq + 1)
			g.names = append(g.names, seg)
		}
		data, next, err := EncodeCommit(chain, c.header, c.drafts)
		if err != nil {
			return nil, err
		}
		start := int64(len(g.files[seg]))
		off := start
		for line := range bytes.Lines(data) {
			g.records = append(g.records, goldenRecord{seq: uint64(len(g.records) + 1), seg: seg, off: off, line: line})
			off += int64(len(line))
		}
		g.files[seg] = append(g.files[seg], data...)
		g.commits = append(g.commits, goldenBoundary{seg: seg, start: start, end: off, first: chain.Seq + 1, after: next})
		chain = next
	}
	return g, nil
}

var golden = sync.OnceValues(buildGolden)

func mustGolden(t testing.TB) *goldenLog {
	t.Helper()
	g, err := golden()
	if err != nil {
		t.Fatalf("encoding the golden inputs: %v", err)
	}
	return g
}

// copyGolden copies the golden testdata log into a fresh directory and
// returns the copy's path.
func copyGolden(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "log")
	if err := os.CopyFS(dir, os.DirFS(goldenDir)); err != nil {
		t.Fatal(err)
	}
	return dir
}

// snapshot returns every file under dir by relative path.
func snapshot(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertUnchanged(t testing.TB, dir string, before map[string][]byte) {
	t.Helper()
	if after := snapshot(t, dir); !maps.EqualFunc(before, after, bytes.Equal) {
		t.Errorf("files under %s changed", dir)
	}
}

// appendFile appends data to the file at path.
func appendFile(t testing.TB, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// readAll reads dir to the end with opts and returns the commits, the final
// cursor, and the error that ended the read (nil for io.EOF).
func readAll(dir string, opts ReadOptions) ([]Commit, uint64, error) {
	r, err := OpenReader(dir, opts)
	if err != nil {
		return nil, opts.After, err
	}
	defer r.Close()
	var commits []Commit
	for {
		c, err := r.Next()
		if err == io.EOF {
			return commits, r.Cursor(), nil
		}
		if err != nil {
			return commits, r.Cursor(), err
		}
		commits = append(commits, c)
	}
}

// allFixtures consumes every fixture type version of the golden log.
var allFixtures = Consumption{"fixture.sample": {1, 2}, "fixture.other": {1}}

func TestGoldenF0(t *testing.T) {
	g := mustGolden(t)
	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(goldenDir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, goldenDir)
	if got, want := slices.Sorted(maps.Keys(stored)), g.names; !slices.Equal(got, want) {
		t.Fatalf("golden files = %v, want %v", got, want)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s differs from the encoding of the golden inputs; run go test -run '^TestGoldenF0$' -update only for an intended format change", name)
		}
	}

	res, err := Check(goldenDir, goldenColony)
	if err != nil {
		t.Fatalf("Check(golden) = %v", err)
	}
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if res != want || res.Last.Seq != 6 {
		t.Errorf("Check(golden) = %+v, want %+v with Last.Seq 6", res, want)
	}
}

func TestGoldenDecodes(t *testing.T) {
	g := mustGolden(t)
	commits, cursor, err := readAll(goldenDir, ReadOptions{Colony: goldenColony, Through: ToEnd, Consume: allFixtures})
	if err != nil {
		t.Fatalf("reading the golden log: %v", err)
	}
	if cursor != 6 {
		t.Errorf("Cursor() = %d, want 6", cursor)
	}

	// The stored payloads, with json.Marshal's compaction and HTML escaping.
	payloads := []string{
		`{"n":1}`,
		`{"n":2,"text":"dwarf ⚒ \u003c\u0026\u003e"}`,
		`{}`,
		`{"n":4,"note":"v2"}`,
		`{}`,
		`{"n":6}`,
	}
	var want []Commit
	chain := Genesis()
	for _, in := range goldenInputs() {
		c := Commit{ID: in.header.CommitID, First: chain.Seq + 1, Last: chain.Seq + uint64(len(in.drafts))}
		for i, d := range in.drafts {
			seq := chain.Seq + 1
			rec := g.record(seq)
			js := rec.line[65 : len(rec.line)-1]
			d.Payload = json.RawMessage(payloads[seq-1])
			c.Records = append(c.Records, Record{
				Envelope: Envelope{
					Format:      1,
					Seq:         seq,
					CommitID:    in.header.CommitID,
					CommitIndex: i,
					CommitCount: len(in.drafts),
					PrevHash:    chain.Hash,
					EventTimeNS: in.header.EventTimeNS,
					ClockHeld:   in.header.ClockHeld,
					ColonyID:    goldenColony,
					Draft:       d,
				},
				Hash: string(rec.line[:64]),
				JSON: js,
			})
			chain = Chain{Seq: seq, Hash: string(rec.line[:64]), EventTimeNS: in.header.EventTimeNS}
		}
		want = append(want, c)
	}
	if !reflect.DeepEqual(commits, want) {
		t.Fatalf("golden commits:\n got %+v\nwant %+v", commits, want)
	}
}

// errorsAsCorrupt returns err's *CorruptError, failing the test if there is
// none.
func errorsAsCorrupt(t testing.TB, err error) *CorruptError {
	t.Helper()
	var ce *CorruptError
	if !errors.As(err, &ce) || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error = %v, want a *CorruptError matching ErrCorrupt", err)
	}
	return ce
}

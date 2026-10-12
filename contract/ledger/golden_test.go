package ledger

import (
	"bytes"
	"crypto/sha256"
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

var update = flag.Bool("update", false, "rewrite the golden F0 logs in testdata")

const (
	// goldenDir holds the golden F0 log, a two-segment log directory.
	goldenDir = "testdata/f0/log"
	// catalogueDir holds the golden catalogue log, a one-segment log
	// directory holding every F0 record type.
	catalogueDir = "testdata/f0/catalogue/log"
	// workspaceDir holds the F2-05 catalogue's one-segment log.
	workspaceDir = "testdata/f2/05/log"
)

// fid returns a fixed fixture UUIDv7 ending in suffix.
func fid(suffix string) uuid.UUID {
	return uuid.MustParse("0199c000-0000-7000-8000-0000000000" + suffix)
}

var (
	goldenColony    = fid("01")
	catalogueColony = fid("02")
	workspaceColony = fid("03")
)

// The bodies and the submission that the catalogue log references.
var (
	bodyOne          = []byte("golden body one\n")
	bodyTwo          = []byte("golden body two\n")
	bodyThree        = []byte("golden body three\n")
	goldenB1         = Digest(sha256.Sum256(bodyOne))
	goldenB2         = Digest(sha256.Sum256(bodyTwo))
	goldenB3         = Digest(sha256.Sum256(bodyThree))
	goldenSubmission = Digest(sha256.Sum256([]byte("golden submission\n")))
)

// sortedDigests returns ds in ascending order.
func sortedDigests(ds ...Digest) []Digest {
	return slices.SortedFunc(slices.Values(ds), func(a, b Digest) int { return bytes.Compare(a[:], b[:]) })
}

// mustEncode returns the payload p encodes. The golden inputs are fixed, so
// an error is a bug in the inputs or the encoder.
func mustEncode(p payload) json.RawMessage {
	js, err := p.Encode()
	if err != nil {
		panic(err)
	}
	return js
}

// goldenCommit is one commit of a golden log's fixed inputs.
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

// catalogueInputs covers every F0 type version, body references on fixture,
// pin and command-commit records, a command commit with an effect, both
// states of keep_bytes and a pin's release. Its two collection records are
// the ones Core's collection must produce at their policies.
func catalogueInputs() []goldenCommit {
	header := func(commit string, t int64) CommitHeader {
		return CommitHeader{ColonyID: catalogueColony, CommitID: fid(commit), EventTimeNS: t}
	}
	return []goldenCommit{
		{header: header("c5", 1791000010000000000), drafts: []Draft{
			{
				Type: "fixture.sample", TypeVersion: 1, Actor: ActorAgent, ContextID: fid("d1"),
				BodyRefs: sortedDigests(goldenB1, goldenB2, goldenB3),
				Payload:  json.RawMessage(`{"n":1}`),
			},
		}},
		{header: header("c6", 1791000011000000000), drafts: []Draft{
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{goldenB2}, Payload: mustEncode(BodyPin{PinID: fid("21")})},
		}},
		{header: header("c7", 1791000012000000000), drafts: []Draft{
			{
				Type: TypeCommand, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("d1"),
				Payload: mustEncode(Command{CommandID: fid("31"), SubmissionSHA256: goldenSubmission}),
			},
			{Type: "fixture.sample", TypeVersion: 1, Actor: ActorCore, BodyRefs: []Digest{goldenB3}, Payload: json.RawMessage(`{"n":4}`)},
		}},
		{header: header("c8", 1791000013000000000), drafts: []Draft{
			{Type: TypeBodiesCollected, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodiesCollected{
				BeforeNS: 1791000011500000000,
				Bodies:   []CollectedBody{{SHA256: goldenB1, Bytes: int64(len(bodyOne))}},
			})},
		}},
		{header: header("c9", 1791000014000000000), drafts: []Draft{
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodyUnpin{PinID: fid("21")})},
		}},
		{header: header("ca", 1791000015000000000), drafts: []Draft{
			{Type: TypeBodiesCollected, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodiesCollected{
				BeforeNS:  1791000014500000000,
				KeepBytes: 20,
				Bodies:    []CollectedBody{{SHA256: goldenB2, Bytes: int64(len(bodyTwo))}},
			})},
		}},
	}
}

// workspaceInputs follows the F2-05 registration, scratch and closure
// history, with both Git object formats and a pinned-input revision update.
func workspaceInputs() []goldenCommit {
	header := func(commit string, t int64) CommitHeader {
		return CommitHeader{ColonyID: workspaceColony, CommitID: fid(commit), EventTimeNS: t}
	}
	return []goldenCommit{
		{header: header("c1", 1791000020000000000), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("41"), CommonDir: "C:/fixture/repo-a/.git", ObjectFormat: "sha1"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB1, goldenSubmission), Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("41"), Revision: 1, CaptureInputsSHA256: goldenB1, PinID: fid("21")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB1, goldenSubmission), Payload: mustEncode(BodyPin{PinID: fid("21")})},
		}},
		{header: header("c2", 1791000021000000000), drafts: []Draft{
			{Type: TypeWorkspaceRegistered, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(WorkspaceRegistered{WorkspaceID: fid("42"), CommonDir: "/fixture/repo-b/.git", ObjectFormat: "sha256"})},
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{goldenB2}, Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("42"), Revision: 1, CaptureInputsSHA256: goldenB2, PinID: fid("22")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: []Digest{goldenB2}, Payload: mustEncode(BodyPin{PinID: fid("22")})},
		}},
		{header: header("c3", 1791000022000000000), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("d1"), Payload: mustEncode(ScratchCreated{ScratchID: fid("51"), WorkspaceID: fid("41"), ContextID: fid("d1"), Dir: "0a1b2c3d", Branch: "refs/heads/dwarf-harness/0a1b2c3d", BaseCommit: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", AnchorTree: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"})},
		}},
		{header: header("c4", 1791000023000000000), drafts: []Draft{
			{Type: TypeScratchCreated, TypeVersion: 1, Actor: ActorCore, ContextID: fid("d2"), Payload: mustEncode(ScratchCreated{ScratchID: fid("52"), WorkspaceID: fid("42"), ContextID: fid("d2"), Dir: "4e5f6a7b", Branch: "refs/heads/dwarf-harness/4e5f6a7b", BaseCommit: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", AnchorTree: "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"})},
		}},
		{header: header("c5", 1791000024000000000), drafts: []Draft{
			{Type: TypeCaptureInputsPinned, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB3, goldenSubmission), Payload: mustEncode(CaptureInputsPinned{WorkspaceID: fid("41"), Revision: 2, CaptureInputsSHA256: goldenB3, PinID: fid("23")})},
			{Type: TypeBodyPin, TypeVersion: 1, Actor: ActorOwner, BodyRefs: sortedDigests(goldenB3, goldenSubmission), Payload: mustEncode(BodyPin{PinID: fid("23")})},
			{Type: TypeBodyUnpin, TypeVersion: 1, Actor: ActorOwner, Payload: mustEncode(BodyUnpin{PinID: fid("21")})},
		}},
		{header: header("c6", 1791000025000000000), drafts: []Draft{
			{Type: TypeScratchClosed, TypeVersion: 1, Actor: ActorAgent, ContextID: fid("d1"), Payload: mustEncode(ScratchClosed{ScratchID: fid("51"), ContextID: fid("d1"), Reason: "abandoned"})},
		}},
	}
}

// goldenRecord locates one record of a golden log.
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
	dir     string            // the testdata directory holding the log
	colony  uuid.UUID         // the log's colony
	consume Consumption       // every type version of the log
	inputs  []goldenCommit    // the fixed inputs it encodes
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

// buildLog encodes inputs with EncodeCommit, the one encoder.
func buildLog(dir string, colony uuid.UUID, consume Consumption, inputs []goldenCommit) (*goldenLog, error) {
	g := &goldenLog{dir: dir, colony: colony, consume: consume, inputs: inputs, files: map[string][]byte{}}
	chain := Genesis()
	var seg string
	for i, c := range inputs {
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

// allFixtures consumes every fixture type version of the golden log.
var allFixtures = Consumption{"fixture.sample": {1, 2}, "fixture.other": {1}}

// catalogueTypes consumes every type version of the catalogue log.
var catalogueTypes = Consumption{
	"fixture.sample": {1}, TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1}, TypeBodiesCollected: {1},
}

var workspaceTypes = Consumption{
	TypeWorkspaceRegistered: {1}, TypeCaptureInputsPinned: {1},
	TypeScratchCreated: {1}, TypeScratchClosed: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1},
}

var (
	golden = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(goldenDir, goldenColony, allFixtures, goldenInputs())
	})
	catalogueLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(catalogueDir, catalogueColony, catalogueTypes, catalogueInputs())
	})
	workspaceLog = sync.OnceValues(func() (*goldenLog, error) {
		return buildLog(workspaceDir, workspaceColony, workspaceTypes, workspaceInputs())
	})
)

func mustGolden(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, golden)
}

func mustCatalogue(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, catalogueLog)
}

func mustWorkspace(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, workspaceLog)
}

func mustBuild(t testing.TB, build func() (*goldenLog, error)) *goldenLog {
	t.Helper()
	g, err := build()
	if err != nil {
		t.Fatalf("encoding the golden inputs: %v", err)
	}
	return g
}

// copyLog copies the testdata log in src into a fresh directory and returns
// the copy's path.
func copyLog(t testing.TB, src string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "log")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
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

func TestGoldenF0(t *testing.T) {
	for _, tc := range []struct {
		g       *goldenLog
		lastSeq uint64
	}{
		{mustGolden(t), 6},
		{mustCatalogue(t), 7},
	} {
		g := tc.g
		if *update {
			if err := os.MkdirAll(g.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, data := range g.files {
				if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		stored := snapshot(t, g.dir)
		if got, want := slices.Sorted(maps.Keys(stored)), g.names; !slices.Equal(got, want) {
			t.Fatalf("%s files = %v, want %v", g.dir, got, want)
		}
		for _, name := range g.names {
			if !bytes.Equal(stored[name], g.files[name]) {
				t.Errorf("%s/%s differs from the encoding of the golden inputs; run go test -run '^TestGoldenF0$' -update only for an intended format change", g.dir, name)
			}
		}

		res, err := Check(g.dir, g.colony)
		if err != nil {
			t.Fatalf("Check(%s) = %v", g.dir, err)
		}
		want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
		if res != want || res.Last.Seq != tc.lastSeq {
			t.Errorf("Check(%s) = %+v, want %+v with Last.Seq %d", g.dir, res, want, tc.lastSeq)
		}
	}
}

func TestGoldenF2_05(t *testing.T) {
	g := mustWorkspace(t)
	if *update {
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, g.dir)
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, g.names) {
		t.Fatalf("files = %v, want %v", got, g.names)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s/%s differs from the F2-05 encoding", g.dir, name)
		}
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: g.consume})
	if err != nil {
		t.Fatal(err)
	}
	inputs := workspaceInputs()
	if len(commits) != len(inputs) {
		t.Fatalf("read %d commits, want %d", len(commits), len(inputs))
	}
	for i, c := range commits {
		if len(c.Records) != len(inputs[i].drafts) {
			t.Fatalf("commit %d: read %d records, want %d", i, len(c.Records), len(inputs[i].drafts))
		}
		for j, r := range c.Records {
			var got payload
			var err error
			switch r.Type {
			case TypeBodyPin, TypeBodyUnpin:
				got, err = decodeF0(r.Type, r.Payload)
			default:
				got, err = decodeF2_05(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d: decoding stored %s payload: %v", r.Seq, r.Type, err)
			}
			// Compare the decoded value's canonical encoding to the fixture
			// value, without decoding the expectation through the same code.
			want := inputs[i].drafts[j].Payload
			if encoded, err := got.Encode(); err != nil || !bytes.Equal(encoded, want) {
				t.Errorf("seq %d: decoded %+v encodes as %s, %v; want %s", r.Seq, got, encoded, err, want)
			}
		}
	}
	res, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || res != want || res.Last.Seq != 12 {
		t.Errorf("Check = %+v, %v; want %+v with Last.Seq 12", res, err, want)
	}
}

func TestGoldenDecodes(t *testing.T) {
	for _, tc := range []struct {
		g *goldenLog
		// payloads are the stored payloads by seq, with json.Marshal's
		// compaction and HTML escaping; nil when they equal the inputs'.
		payloads []string
	}{
		{mustGolden(t), []string{
			`{"n":1}`,
			`{"n":2,"text":"dwarf ⚒ \u003c\u0026\u003e"}`,
			`{}`,
			`{"n":4,"note":"v2"}`,
			`{}`,
			`{"n":6}`,
		}},
		{mustCatalogue(t), nil},
		{mustWorkspace(t), nil},
	} {
		g := tc.g
		commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: g.consume})
		if err != nil {
			t.Fatalf("reading %s: %v", g.dir, err)
		}
		if want := g.end().Seq; cursor != want {
			t.Errorf("%s: Cursor() = %d, want %d", g.dir, cursor, want)
		}

		var want []Commit
		chain := Genesis()
		for _, in := range g.inputs {
			c := Commit{ID: in.header.CommitID, First: chain.Seq + 1, Last: chain.Seq + uint64(len(in.drafts))}
			for i, d := range in.drafts {
				seq := chain.Seq + 1
				rec := g.record(seq)
				js := rec.line[65 : len(rec.line)-1]
				if tc.payloads != nil {
					d.Payload = json.RawMessage(tc.payloads[seq-1])
				}
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
						ColonyID:    g.colony,
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
			t.Fatalf("%s commits:\n got %+v\nwant %+v", g.dir, commits, want)
		}
	}

	// Body references decode as listed, and an absent body_refs as nil.
	commits, _, err := readAll(catalogueDir, ReadOptions{Colony: catalogueColony, Through: ToEnd, Consume: catalogueTypes})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[uint64]int{1: 3, 2: 1, 4: 1}
	for _, c := range commits {
		for _, r := range c.Records {
			if n, ok := refs[r.Seq]; ok && len(r.BodyRefs) != n || !ok && r.BodyRefs != nil {
				t.Errorf("seq %d: BodyRefs %v, want %d digests (nil for none)", r.Seq, r.BodyRefs, n)
			}
		}
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

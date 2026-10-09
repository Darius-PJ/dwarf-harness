package ledgerstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

// allFixtures consumes every type version of testCatalogue.
var allFixtures = ledger.Consumption{"fixture.sample": {1, 2}, "fixture.other": {1}}

// testCatalogue holds package ledger's reserved fixture types.
func testCatalogue(t testing.TB) ledger.Catalogue {
	t.Helper()
	cat, err := ledger.NewCatalogue(
		ledger.TypeVersion{Type: "fixture.sample", Version: 1},
		ledger.TypeVersion{Type: "fixture.sample", Version: 2},
		ledger.TypeVersion{Type: "fixture.other", Version: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// testDeps returns the real dependencies with a clock that reads one second
// later at every call, and with files unless it is nil.
func testDeps(files fileSystem) deps {
	d := realDeps()
	var ticks atomic.Int64
	d.clock = func() time.Time { return time.Unix(1_800_000_000+ticks.Add(1), 0) }
	if files != nil {
		d.files = files
	}
	return d
}

// newColony creates a colony with d in a fresh directory. The store is
// closed when the test ends.
func newColony(t testing.TB, d deps) *Store {
	t.Helper()
	s, err := create(filepath.Join(t.TempDir(), "colony"), testCatalogue(t), d)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// reopen closes s and opens its colony again with d. The new store is
// closed when the test ends.
func reopen(t testing.TB, s *Store, d deps) (*Store, Recovery) {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	s2, rec, err := open(s.dir, s.cat, d)
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	return s2, rec
}

// sample returns a fixture.sample v1 draft whose payload carries tag.
func sample(tag string) ledger.Draft {
	return ledger.Draft{Type: "fixture.sample", TypeVersion: 1, Actor: ledger.ActorCore, Payload: json.RawMessage(`{"tag":"` + tag + `"}`)}
}

// padded returns a fixture.sample v1 draft with a payload string of n bytes.
func padded(n int) ledger.Draft {
	return ledger.Draft{Type: "fixture.sample", TypeVersion: 1, Actor: ledger.ActorCore, Payload: json.RawMessage(`{"pad":"` + strings.Repeat("x", n) + `"}`)}
}

func mustAppend(t testing.TB, s *Store, drafts ...ledger.Draft) appender.Committed {
	t.Helper()
	c, err := s.Append(context.Background(), drafts)
	if err != nil {
		t.Fatalf("Append = %v", err)
	}
	return c
}

// readCommits reads every commit of the log through seq through, with all
// its records; types outside allFixtures are returned unchecked.
func readCommits(t testing.TB, logDir string, colony uuid.UUID, through uint64) []ledger.Commit {
	t.Helper()
	r, err := ledger.OpenReader(logDir, ledger.ReadOptions{Colony: colony, Through: through, Consume: allFixtures, AllTypes: true})
	if err != nil {
		t.Fatalf("OpenReader = %v", err)
	}
	defer r.Close()
	var commits []ledger.Commit
	for {
		c, err := r.Next()
		if errors.Is(err, io.EOF) {
			return commits
		}
		if err != nil {
			t.Fatalf("reading after %d commits: %v", len(commits), err)
		}
		commits = append(commits, c)
	}
}

func ranges(commits []ledger.Commit) []appender.Committed {
	var rs []appender.Committed
	for _, c := range commits {
		rs = append(rs, appender.Committed{First: c.First, Last: c.Last})
	}
	return rs
}

// tags returns the tag of every record of commits, in order.
func tags(t testing.TB, commits []ledger.Commit) []string {
	t.Helper()
	var ts []string
	for _, c := range commits {
		for _, r := range c.Records {
			var p struct{ Tag string }
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				t.Fatalf("seq %d payload: %v", r.Seq, err)
			}
			ts = append(ts, p.Tag)
		}
	}
	return ts
}

// snapshot returns the content of every file under dir by relative path.
func snapshot(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertSnapshot(t testing.TB, dir string, want map[string][]byte) {
	t.Helper()
	if got := snapshot(t, dir); !maps.EqualFunc(got, want, bytes.Equal) {
		t.Errorf("files under %s changed", dir)
	}
}

// assertUnlocked checks that nothing holds the colony lock of dir.
func assertUnlocked(t testing.TB, dir string) {
	t.Helper()
	l, err := durable.LockFile(filepath.Join(dir, lockName))
	if err != nil {
		t.Fatalf("taking the colony lock after the failed call: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndOpenLayout(t *testing.T) {
	cat := testCatalogue(t)
	// The parent of dir does not exist either; Create makes both.
	dir := filepath.Join(t.TempDir(), "colonies", "a")
	s, err := Create(dir, cat)
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	colony := filepath.Join(dir, colonyFile)
	want := `{"format":1,"colony_id":"` + s.Colony().String() + `"}` + "\n"
	if got, err := os.ReadFile(colony); err != nil || string(got) != want {
		t.Errorf("colony.json = %q, %v; want %q", got, err, want)
	}
	if !isV7(s.Colony()) {
		t.Errorf("colony ID %s is not a UUIDv7", s.Colony())
	}
	if fi, err := os.Stat(filepath.Join(dir, lockName)); err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
		t.Errorf("lock: %v, %v; want an empty file", fi, err)
	}
	if entries, err := os.ReadDir(s.LogDir()); err != nil || len(entries) != 0 {
		t.Errorf("log directory: %v, %v; want it empty", entries, err)
	}
	if want := filepath.Join(dir, logName); s.LogDir() != want {
		t.Errorf("LogDir = %s, want %s", s.LogDir(), want)
	}

	if _, _, err := Open(dir, cat); !errors.Is(err, ErrLocked) || !errors.Is(err, durable.ErrLocked) {
		t.Errorf("Open while held = %v, want ErrLocked matching durable.ErrLocked", err)
	}
	if _, err := Create(dir, cat); !errors.Is(err, ErrExists) {
		t.Errorf("Create while held = %v, want ErrExists", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if _, err := Create(dir, cat); !errors.Is(err, ErrExists) {
		t.Errorf("Create again = %v, want ErrExists", err)
	}
	if got, _ := os.ReadFile(colony); string(got) != want {
		t.Errorf("colony.json after Create again = %q, want %q", got, want)
	}
	s2, rec, err := Open(dir, cat)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if s2.Colony() != s.Colony() || s2.Durable() != 0 || rec != (Recovery{}) {
		t.Errorf("Open = colony %s, Durable %d, %+v; want colony %s, 0, no recovery", s2.Colony(), s2.Durable(), rec, s.Colony())
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}

	t.Run("no colony.json", func(t *testing.T) {
		dir := t.TempDir()
		if _, _, err := Open(dir, cat); !errors.Is(err, ErrNotColony) {
			t.Errorf("Open = %v, want ErrNotColony", err)
		}
		missing := filepath.Join(dir, "missing")
		if _, _, err := Open(missing, cat); !errors.Is(err, ErrNotColony) {
			t.Errorf("Open of a missing directory = %v, want ErrNotColony", err)
		}
		// Open leaves nothing behind, not even a lock file.
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("directory after Open: %v, %v; want it empty", entries, err)
		}
	})

	t.Run("log without colony.json", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, logName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, logName, ledger.SegmentName(1)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Create(dir, cat); !errors.Is(err, ledger.ErrCorrupt) {
			t.Errorf("Create = %v, want an error matching ledger.ErrCorrupt", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, colonyFile)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Create wrote colony.json: %v", err)
		}
		assertUnlocked(t, dir)
	})

	// closedColony returns the directory of a new colony, closed.
	closedColony := func(t *testing.T) (string, uuid.UUID) {
		dir := filepath.Join(t.TempDir(), "colony")
		s, err := Create(dir, cat)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return dir, s.Colony()
	}

	t.Run("colony.json format 2", func(t *testing.T) {
		dir, id := closedColony(t)
		data := `{"format":2,"colony_id":"` + id.String() + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, colonyFile), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Open(dir, cat); err == nil {
			t.Fatal("Open of a format 2 colony.json succeeded")
		}
		assertUnlocked(t, dir)
	})

	t.Run("log directory missing", func(t *testing.T) {
		dir, _ := closedColony(t)
		if err := os.Remove(filepath.Join(dir, logName)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Open(dir, cat); !errors.Is(err, ledger.ErrCorrupt) {
			t.Errorf("Open = %v, want an error matching ledger.ErrCorrupt", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, logName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open recreated the log directory: %v", err)
		}
		assertUnlocked(t, dir)
	})
}

func TestWriterReproducesGolden(t *testing.T) {
	// The catalogue log holds the F0 types beside one fixture type.
	catalogueTypes, err := ledger.NewCatalogue(
		ledger.TypeVersion{Type: "fixture.sample", Version: 1},
		ledger.TypeVersion{Type: ledger.TypeCommand, Version: 1},
		ledger.TypeVersion{Type: ledger.TypeBodyPin, Version: 1},
		ledger.TypeVersion{Type: ledger.TypeBodyUnpin, Version: 1},
		ledger.TypeVersion{Type: ledger.TypeBodiesCollected, Version: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		goldenDir string
		colony    uuid.UUID
		cat       ledger.Catalogue
	}{
		{"f0", "../../../contract/ledger/testdata/f0/log", uuid.MustParse("0199c000-0000-7000-8000-000000000001"), testCatalogue(t)},
		{"catalogue", "../../../contract/ledger/testdata/f0/catalogue/log", uuid.MustParse("0199c000-0000-7000-8000-000000000002"), catalogueTypes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commits := readCommits(t, tc.goldenDir, tc.colony, ledger.ToEnd)
			golden := snapshot(t, tc.goldenDir)
			first, err := os.Stat(filepath.Join(tc.goldenDir, ledger.SegmentName(1)))
			if err != nil {
				t.Fatal(err)
			}

			ids := []uuid.UUID{tc.colony}
			var times []int64
			for i, c := range commits {
				ids = append(ids, c.ID)
				at := c.Records[0].EventTimeNS
				if c.Records[0].ClockHeld {
					// The clock reads before the previous commit, so the
					// writer must hold the time and mark it.
					at = commits[i-1].Records[0].EventTimeNS - 1
				}
				times = append(times, at)
			}
			d := realDeps()
			d.newID = func() uuid.UUID {
				id := ids[0]
				ids = ids[1:]
				return id
			}
			d.clock = func() time.Time {
				at := times[0]
				times = times[1:]
				return time.Unix(0, at)
			}
			d.segmentTarget = first.Size()
			s, err := create(filepath.Join(t.TempDir(), "colony"), tc.cat, d)
			if err != nil {
				t.Fatalf("create = %v", err)
			}
			t.Cleanup(func() { s.Close() })
			for _, c := range commits {
				var drafts []ledger.Draft
				for _, r := range c.Records {
					drafts = append(drafts, r.Draft)
				}
				if got := mustAppend(t, s, drafts...); got != (appender.Committed{First: c.First, Last: c.Last}) {
					t.Fatalf("Append = %+v, want %d-%d", got, c.First, c.Last)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			got := snapshot(t, s.LogDir())
			if !maps.EqualFunc(got, golden, bytes.Equal) {
				t.Errorf("writer log = %v, want the golden segments %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(golden)))
				for name, data := range golden {
					if !bytes.Equal(got[name], data) {
						t.Errorf("%s differs:\n got %q\nwant %q", name, got[name], data)
					}
				}
			}
		})
	}
}

func TestConcurrentAppendsAreGapless(t *testing.T) {
	cat := testCatalogue(t)
	s, err := Create(filepath.Join(t.TempDir(), "colony"), cat)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	const goroutines, appends = 8, 50
	size := func(g, i int) int { return 1 + (g+i)%3 }
	tag := func(g, i, k int) string { return fmt.Sprintf("%d/%d/%d", g, i, k) }
	acked := make([][]appender.Committed, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			for i := range appends {
				var drafts []ledger.Draft
				for k := range size(g, i) {
					drafts = append(drafts, sample(tag(g, i, k)))
				}
				c, err := s.Append(context.Background(), drafts)
				if err != nil {
					t.Errorf("goroutine %d append %d: %v", g, i, err)
					return
				}
				acked[g] = append(acked[g], c)
			}
		})
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	// want maps each acknowledged commit's first sequence to its range and
	// record tags.
	type commit struct {
		r    appender.Committed
		tags []string
	}
	want := map[uint64]commit{}
	var total uint64
	for g := range goroutines {
		for i, c := range acked[g] {
			var ts []string
			for k := range size(g, i) {
				ts = append(ts, tag(g, i, k))
			}
			if c.Last-c.First+1 != uint64(len(ts)) {
				t.Errorf("goroutine %d append %d acknowledged %+v for %d drafts", g, i, c, len(ts))
			}
			if _, dup := want[c.First]; dup {
				t.Errorf("sequence %d acknowledged twice", c.First)
			}
			want[c.First] = commit{c, ts}
			total += uint64(len(ts))
		}
	}
	for seq := uint64(1); seq <= total; {
		c, ok := want[seq]
		if !ok {
			t.Fatalf("no acknowledged commit starts at seq %d", seq)
		}
		seq = c.r.Last + 1
	}
	if s.Durable() != total {
		t.Errorf("Durable = %d, want %d", s.Durable(), total)
	}

	s2, _ := reopen(t, s, realDeps())
	commits := readCommits(t, s2.LogDir(), s2.Colony(), s2.Durable())
	if len(commits) != goroutines*appends {
		t.Fatalf("read %d commits, want %d", len(commits), goroutines*appends)
	}
	for _, c := range commits {
		w := want[c.First]
		if got := ranges([]ledger.Commit{c})[0]; got != w.r || !slices.Equal(tags(t, []ledger.Commit{c}), w.tags) {
			t.Errorf("commit %d-%d tags %v, want %+v", c.First, c.Last, tags(t, []ledger.Commit{c}), w)
		}
	}
}

func TestRefusedAppendConsumesNothing(t *testing.T) {
	s := newColony(t, testDeps(nil))
	mustAppend(t, s, sample("first"))
	invalid := sample("bad")
	invalid.Actor = "nobody"
	commitOverCap := slices.Repeat([]ledger.Draft{padded(1_000_000)}, 17)
	for _, tc := range []struct {
		name     string
		drafts   []ledger.Draft
		tooLarge bool
	}{
		{"record over MaxRecordBytes", []ledger.Draft{padded(ledger.MaxRecordBytes)}, true},
		{"commit over MaxCommitBytes", commitOverCap, true},
		{"no drafts", nil, false},
		{"invalid draft", []ledger.Draft{invalid}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshot(t, s.LogDir())
			_, err := s.Append(context.Background(), tc.drafts)
			if !errors.Is(err, appender.ErrRefused) || errors.Is(err, ledger.ErrTooLarge) != tc.tooLarge {
				t.Errorf("Append = %v, want ErrRefused (ledger.ErrTooLarge %v)", err, tc.tooLarge)
			}
			assertSnapshot(t, s.LogDir(), before)
			if s.Durable() != 1 {
				t.Errorf("Durable = %d, want 1", s.Durable())
			}
		})
	}
	if c := mustAppend(t, s, sample("next")); c != (appender.Committed{First: 2, Last: 2}) {
		t.Errorf("next Append = %+v, want 2-2", c)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if _, err := s.Append(context.Background(), []ledger.Draft{sample("closed")}); !errors.Is(err, appender.ErrClosed) {
		t.Errorf("Append after Close = %v, want ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}

	// The production catalogue defines no record type yet, so it refuses
	// the fixture types.
	prod, err := Create(filepath.Join(t.TempDir(), "colony"), ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { prod.Close() })
	if _, err := prod.Append(context.Background(), []ledger.Draft{sample("fixture")}); !errors.Is(err, appender.ErrRefused) {
		t.Errorf("Append of a fixture type with ledger.Defined() = %v, want ErrRefused", err)
	}
	if entries, err := os.ReadDir(prod.LogDir()); err != nil || len(entries) != 0 || prod.Durable() != 0 {
		t.Errorf("after the refusal: log %v, %v, Durable %d; want an empty log", entries, err, prod.Durable())
	}
}

func TestRotationKeepsCommitsWhole(t *testing.T) {
	const target = 1 << 10
	d := testDeps(nil)
	d.segmentTarget = target
	s := newColony(t, d)
	// Payload sizes per draft. The two commits over the target must each go
	// alone into a fresh segment.
	plan := [][]int{{10}, {300, 40}, {50}, {700}, {20, 20, 20}, {900, 900}, {100}, {400}, {400}, {5}, {600}, {1500}, {30}}
	alone := map[int]bool{5: true, 11: true}
	var acked []appender.Committed
	for _, sizes := range plan {
		var drafts []ledger.Draft
		for _, n := range sizes {
			drafts = append(drafts, padded(n))
		}
		acked = append(acked, mustAppend(t, s, drafts...))
	}
	commits := readCommits(t, s.LogDir(), s.Colony(), s.Durable())
	if got := ranges(commits); !slices.Equal(got, acked) {
		t.Fatalf("read commits %v, want %v", got, acked)
	}

	entries, err := os.ReadDir(s.LogDir())
	if err != nil {
		t.Fatal(err)
	}
	// segOf[i] is the index in entries of the segment holding commit i.
	segOf := make([]int, len(commits))
	sizes := make([]int64, len(commits))
	held := make([]int64, len(entries)) // bytes of the commits in each segment
	seg := -1
	for i, c := range commits {
		for _, r := range c.Records {
			sizes[i] += int64(len(r.Hash) + 1 + len(r.JSON) + 1)
		}
		if seg+1 < len(entries) && entries[seg+1].Name() == ledger.SegmentName(c.First) {
			seg++
		}
		if seg < 0 || (seg+1 < len(entries) && entries[seg+1].Name() <= ledger.SegmentName(c.Last)) {
			t.Fatalf("commit %d-%d is not within one segment of %v", c.First, c.Last, entries)
		}
		segOf[i] = seg
		held[seg] += sizes[i]
	}
	if seg != len(entries)-1 {
		t.Fatalf("segments %v hold no commit after %s", entries, entries[seg].Name())
	}
	for k, e := range entries {
		fi, err := e.Info()
		if err != nil || fi.Size() != held[k] {
			t.Errorf("%s holds %v bytes (%v), want its commits' %d", e.Name(), fi.Size(), err, held[k])
		}
	}
	// Rotation happens exactly when the commit would take a non-empty
	// segment past the target.
	var segSize int64
	for i := range commits {
		rotated := i > 0 && segOf[i] != segOf[i-1]
		if want := i > 0 && segSize > 0 && segSize+sizes[i] > target; rotated != want {
			t.Errorf("commit %d at segment size %d, commit size %d: rotated %v, want %v", i, segSize, sizes[i], rotated, want)
		}
		if rotated {
			segSize = 0
		}
		segSize += sizes[i]
	}
	for i := range alone {
		if (i > 0 && segOf[i-1] == segOf[i]) || (i+1 < len(commits) && segOf[i+1] == segOf[i]) {
			t.Errorf("commit %d of %d bytes shares its segment", i, sizes[i])
		}
	}
}

func TestEventTimeNeverDecreases(t *testing.T) {
	readings := []int64{100, 50, 150}
	d := testDeps(nil)
	d.clock = func() time.Time {
		at := readings[0]
		readings = readings[1:]
		return time.Unix(0, at)
	}
	s := newColony(t, d)
	for i := range 3 {
		mustAppend(t, s, sample(fmt.Sprint(i)))
	}
	d.clock = func() time.Time { return time.Unix(0, 40) }
	s2, _ := reopen(t, s, d)
	mustAppend(t, s2, sample("after open"))

	type stamp struct {
		at   int64
		held bool
	}
	var got []stamp
	for _, c := range readCommits(t, s2.LogDir(), s2.Colony(), s2.Durable()) {
		got = append(got, stamp{c.Records[0].EventTimeNS, c.Records[0].ClockHeld})
	}
	if want := []stamp{{100, false}, {100, true}, {150, false}, {150, true}}; !slices.Equal(got, want) {
		t.Errorf("event times %v, want %v", got, want)
	}
}

package ledgerstore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

func TestOpenTruncatesTornFinalCommit(t *testing.T) {
	d := testDeps(nil)
	s := newColony(t, d)
	mustAppend(t, s, sample("before"))
	path := filepath.Join(s.LogDir(), ledger.SegmentName(1))
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	boundary := fi.Size()
	mustAppend(t, s, sample("a"), sample("b"), sample("c"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for cut := boundary; cut < int64(len(full)); cut++ {
		if err := os.WriteFile(path, full[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		s2, rec, err := open(s.dir, s.cat, d)
		if err != nil {
			t.Fatalf("cut at %d: open = %v", cut, err)
		}
		want := Recovery{}
		if cut > boundary {
			want = Recovery{Segment: ledger.SegmentName(1), TruncatedBytes: cut - boundary}
		}
		if rec != want {
			t.Errorf("cut at %d: recovery %+v, want %+v", cut, rec, want)
		}
		if size := fileSize(t, path); size != boundary {
			t.Errorf("cut at %d: segment size %d after open, want %d", cut, size, boundary)
		}
		c, err := s2.Append(context.Background(), []ledger.Draft{sample("next")})
		if cerr := s2.Close(); err == nil {
			err = cerr
		}
		if err != nil || c != (appender.Committed{First: 2, Last: 2}) {
			t.Fatalf("cut at %d: Append after open = %+v, %v; want 2-2", cut, c, err)
		}
		res, err := ledger.Check(s2.LogDir(), s2.Colony())
		if err != nil || res.Last.Seq != 2 || res.TornBytes != 0 {
			t.Fatalf("cut at %d: Check after the next Append = %+v, %v; want seq 2 and no torn bytes", cut, res, err)
		}
	}

	t.Run("fully torn later segment", func(t *testing.T) {
		d := testDeps(nil)
		d.segmentTarget = 1 // every commit after the first starts a segment
		s := newColony(t, d)
		mustAppend(t, s, sample("one"))
		mustAppend(t, s, sample("a"), sample("b"))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		name := ledger.SegmentName(2)
		path := filepath.Join(s.LogDir(), name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		torn := int64(len(data) / 2)
		if err := os.Truncate(path, torn); err != nil {
			t.Fatal(err)
		}
		s2, rec, err := open(s.dir, s.cat, d)
		if err != nil {
			t.Fatalf("open = %v", err)
		}
		t.Cleanup(func() { s2.Close() })
		if want := (Recovery{Segment: name, TruncatedBytes: torn}); rec != want {
			t.Errorf("recovery %+v, want %+v", rec, want)
		}
		if size := fileSize(t, path); size != 0 {
			t.Errorf("%s holds %d bytes after open, want 0", name, size)
		}
		if c := mustAppend(t, s2, sample("c")); c != (appender.Committed{First: 2, Last: 2}) {
			t.Errorf("Append after open = %+v, want 2-2", c)
		}
		if got := snapshot(t, s2.LogDir()); len(got) != 2 || len(got[name]) == 0 {
			t.Errorf("segments %v, want the commit in the emptied %s", slices.Sorted(maps.Keys(got)), name)
		}
	})
}

func fileSize(t testing.TB, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func TestOpenFailsClosedOnCorruption(t *testing.T) {
	// colony returns a closed colony with one segment per commit.
	colony := func(t *testing.T) *Store {
		d := testDeps(nil)
		d.segmentTarget = 1
		s := newColony(t, d)
		for i := range 3 {
			mustAppend(t, s, sample(fmt.Sprint(i)))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, s *Store)
	}{
		{"flipped byte in a non-final commit", func(t *testing.T, s *Store) {
			path := filepath.Join(s.LogDir(), ledger.SegmentName(1))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data[100] ^= 1 // inside the first record's JSON
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"renamed segment", func(t *testing.T, s *Store) {
			if err := os.Rename(filepath.Join(s.LogDir(), ledger.SegmentName(3)), filepath.Join(s.LogDir(), ledger.SegmentName(4))); err != nil {
				t.Fatal(err)
			}
		}},
		{"log of another colony", func(t *testing.T, s *Store) {
			other := colony(t)
			for name, data := range snapshot(t, other.LogDir()) {
				if err := os.WriteFile(filepath.Join(s.LogDir(), name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := colony(t)
			tc.corrupt(t, s)
			before := snapshot(t, s.dir)
			if _, _, err := Open(s.dir, s.cat); !errors.Is(err, ledger.ErrCorrupt) {
				t.Errorf("Open = %v, want ErrCorrupt", err)
			}
			assertSnapshot(t, s.dir, before)
			assertUnlocked(t, s.dir)
		})
	}
}

// childDirEnv names, to the child writer, the colony directory to create.
const childDirEnv = "DWARF_LEDGERSTORE_CHILD_DIR"

// TestHelperChildWriter is the writer process of
// TestKilledWriterReleasesLockAndRecovers. It creates the colony named by
// childDirEnv with a 4 KiB segment target and appends until it is killed or
// its standard output fails, printing each acknowledged range as
// "ack <First> <Last>".
func TestHelperChildWriter(t *testing.T) {
	dir := os.Getenv(childDirEnv)
	if dir == "" {
		t.Skip("runs only as the child of TestKilledWriterReleasesLockAndRecovers")
	}
	d := realDeps()
	d.segmentTarget = 4 << 10
	s, err := create(dir, testCatalogue(t), d)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		var drafts []ledger.Draft
		for range 1 + i%3 {
			drafts = append(drafts, padded(200))
		}
		c, err := s.Append(context.Background(), drafts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Printf("ack %d %d\n", c.First, c.Last); err != nil {
			return
		}
	}
}

func TestKilledWriterReleasesLockAndRecovers(t *testing.T) {
	cat := testCatalogue(t)
	dir := filepath.Join(t.TempDir(), "colony")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChildWriter$")
	cmd.Env = append(os.Environ(), childDirEnv+"="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewReader(stdout)
	var acks []appender.Committed
	var trailing []string // complete lines that are not acknowledgements
	// read records the next complete line and reports whether there was
	// one; a final line without its LF was cut by the kill and is ignored.
	read := func() bool {
		line, err := lines.ReadString('\n')
		if err != nil {
			return false
		}
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "ack" {
			first, err1 := strconv.ParseUint(f[1], 10, 64)
			last, err2 := strconv.ParseUint(f[2], 10, 64)
			if err1 == nil && err2 == nil {
				acks = append(acks, appender.Committed{First: first, Last: last})
				return true
			}
		}
		trailing = append(trailing, line)
		return true
	}
	// stop kills the child, reads what it printed before dying and waits
	// for its exit, which releases its handles.
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cmd.Process.Kill()
		for read() {
		}
		cmd.Wait()
	}
	t.Cleanup(stop)

	const n = 40
	for len(acks) < n && len(trailing) == 0 && read() {
		if len(acks) == 1 {
			// The child holds the lock from before its first
			// acknowledgement.
			if _, _, err := Open(dir, cat); !errors.Is(err, ErrLocked) {
				t.Errorf("Open while the child writes = %v, want ErrLocked", err)
			}
		}
	}
	stop()
	if len(acks) < n || len(trailing) > 0 {
		t.Fatalf("child stopped after %d acknowledgements; other output %q; stderr:\n%s", len(acks), trailing, stderr.Bytes())
	}
	s, rec, err := Open(dir, cat)
	if err != nil {
		t.Fatalf("Open after the kill = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	t.Logf("%d acknowledgements; recovery %+v", len(acks), rec)
	last := acks[len(acks)-1].Last
	if s.Durable() < last {
		t.Fatalf("Durable after the kill = %d, below the acknowledged %d", s.Durable(), last)
	}
	// The reader verifies that the log is gapless and chained from seq 1.
	commits := readCommits(t, s.LogDir(), s.Colony(), s.Durable())
	got := ranges(commits)
	if len(got) < len(acks) || !slices.Equal(got[:len(acks)], acks) {
		t.Fatalf("commits after the kill %v, want them to begin with the acknowledged %v", got, acks)
	}
	c := mustAppend(t, s, sample("after"))
	if c.First != got[len(got)-1].Last+1 {
		t.Errorf("Append after the kill = %+v, want it to follow seq %d", c, got[len(got)-1].Last)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := ledger.Check(s.LogDir(), s.Colony())
	if err != nil || res.Last.Seq != c.Last || res.TornBytes != 0 {
		t.Errorf("Check after the next Append = %+v, %v; want seq %d and no torn bytes", res, err, c.Last)
	}
}

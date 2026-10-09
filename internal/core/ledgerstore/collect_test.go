package ledgerstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

// commitTime returns the event time of the commit holding seq.
func commitTime(t testing.TB, s *Store, seq uint64) int64 {
	t.Helper()
	for _, c := range readCommits(t, s.LogDir(), s.Colony(), s.Durable()) {
		if c.First <= seq && seq <= c.Last {
			return c.Records[0].EventTimeNS
		}
	}
	t.Fatalf("no commit holds seq %d", seq)
	return 0
}

// setMtime sets the modification time of body d to ns.
func setMtime(t testing.TB, s *Store, d ledger.Digest, ns int64) {
	t.Helper()
	at := time.Unix(0, ns)
	if err := os.Chtimes(pathOf(s, d), at, at); err != nil {
		t.Fatal(err)
	}
}

// collectionAt decodes the ledger.bodies_collected record at seq.
func collectionAt(t testing.TB, s *Store, seq uint64) (ledger.Actor, ledger.BodiesCollected) {
	t.Helper()
	for _, c := range readCommits(t, s.LogDir(), s.Colony(), s.Durable()) {
		for _, r := range c.Records {
			if r.Seq == seq && r.Type == ledger.TypeBodiesCollected {
				p, err := ledger.DecodeBodiesCollected(r.Payload)
				if err != nil {
					t.Fatal(err)
				}
				return r.Actor, p
			}
		}
	}
	t.Fatalf("no collection record at seq %d", seq)
	return "", ledger.BodiesCollected{}
}

func collect(t testing.TB, s *Store, p CollectPolicy) CollectReport {
	t.Helper()
	rep, err := s.Collect(context.Background(), p)
	if err != nil {
		t.Fatalf("Collect(%+v) = %+v, %v", p, rep, err)
	}
	return rep
}

func assertCollected(t testing.TB, s *Store, d ledger.Digest, seq uint64) {
	t.Helper()
	var ue *BodyUnavailableError
	if _, err := s.ReadBody(context.Background(), d); !errors.As(err, &ue) || *ue != (BodyUnavailableError{Digest: d, Seq: seq}) {
		t.Errorf("ReadBody(%s) = %v, want it collected by seq %d", d, err, seq)
	}
}

func assertReadable(t testing.TB, s *Store, d ledger.Digest, content string) {
	t.Helper()
	if data, err := s.ReadBody(context.Background(), d); err != nil || string(data) != content {
		t.Errorf("ReadBody(%s) = %q, %v; want %q", d, data, err, content)
	}
}

const hour = int64(time.Hour)

func TestCollectRespectsPinsAndHistory(t *testing.T) {
	s := newColony(t, testDeps(nil))
	content := []string{"b1: referenced before the cutoff", "b2: pinned", "b3: referenced after the cutoff", "b4: unreferenced and old", "b5: unreferenced and new"}
	var b []ledger.Digest
	for _, c := range content {
		b = append(b, publish(t, s, c))
	}
	pin := uuid.NewV7()
	mustAppend(t, s, ref("1", b[0]))
	mustAppend(t, s, ref("2", b[1]))
	mustAppend(t, s, pinDraft(t, pin, b[1]))
	cutoff := commitTime(t, s, 3) + int64(time.Second/2)
	mustAppend(t, s, ref("3", b[2]))
	setMtime(t, s, b[3], cutoff-hour)
	setMtime(t, s, b[4], cutoff+hour)
	logBefore := snapshot(t, s.LogDir())
	commitsBefore := readCommits(t, s.LogDir(), s.Colony(), s.Durable())
	bodiesBefore := snapshot(t, s.bodiesDir)

	// A policy that is not a valid order does nothing.
	for _, bad := range []CollectPolicy{
		{Actor: ledger.ActorAgent, BeforeNS: cutoff},
		{Actor: ledger.ActorOwner},
		{Actor: ledger.ActorOwner, BeforeNS: cutoff, KeepBytes: -1},
	} {
		if _, err := s.Collect(context.Background(), bad); !errors.Is(err, appender.ErrRefused) {
			t.Errorf("Collect(%+v) = %v, want ErrRefused", bad, err)
		}
	}
	assertSnapshot(t, s.LogDir(), logBefore)
	assertSnapshot(t, s.bodiesDir, bodiesBefore)

	policy := CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: cutoff}
	size := func(i int) int64 { return int64(len(content[i])) }
	rep := collect(t, s, policy)
	want := CollectReport{
		Recorded: 1, RecordedBytes: size(0), Unreferenced: 1, UnreferencedBytes: size(3),
		Records: []uint64{5}, PresentBytes: size(1) + size(2) + size(4),
	}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("Collect = %+v, want %+v", rep, want)
	}
	actor, record := collectionAt(t, s, 5)
	if wantRecord := (ledger.BodiesCollected{BeforeNS: cutoff, Bodies: []ledger.CollectedBody{{SHA256: b[0], Bytes: size(0)}}}); actor != ledger.ActorOwner || !reflect.DeepEqual(record, wantRecord) {
		t.Errorf("record = %s %+v, want owner %+v", actor, record, wantRecord)
	}
	assertAbsent(t, pathOf(s, b[0]))
	assertAbsent(t, pathOf(s, b[3]))
	for _, i := range []int{1, 2, 4} {
		assertReadable(t, s, b[i], content[i])
	}
	assertCollected(t, s, b[0], 5)

	// History is never rewritten: the log only grew.
	logAfter := snapshot(t, s.LogDir())
	for name, data := range logBefore {
		if !bytes.HasPrefix(logAfter[name], data) {
			t.Errorf("%s is not an extension of its bytes before the collection", name)
		}
	}
	if got := readCommits(t, s.LogDir(), s.Colony(), s.Durable()); !reflect.DeepEqual(got[:len(commitsBefore)], commitsBefore) {
		t.Errorf("the commits before the collection read differently after it")
	}

	// Once unpinned, b2 is taken.
	mustAppend(t, s, unpinDraft(t, pin))
	if rep := collect(t, s, policy); rep.Recorded != 1 || !slices.Equal(rep.Records, []uint64{7}) {
		t.Errorf("Collect after the unpin = %+v, want b2 recorded at seq 7", rep)
	}
	assertCollected(t, s, b[1], 7)
	assertReadable(t, s, b[2], content[2])
	assertReadable(t, s, b[4], content[4])
}

func TestCollectKeepBytesOldestFirst(t *testing.T) {
	// Six bodies of distinct sizes, 175 bytes in all. b3 is referenced
	// first, b0 and b4 together, then b1 and b2, all before the cutoff; b5
	// after it, so it is never eligible but counts as present.
	sizes := []int{10, 20, 30, 40, 50, 25}
	order := [][]int{{3}, {0, 4}, {1}, {2}}
	contents := make([]string, len(sizes))
	digests := make([]ledger.Digest, len(sizes))
	for i, n := range sizes {
		contents[i] = strings.Repeat(string(rune('a'+i)), n)
		digests[i] = digestOf(contents[i])
	}
	// b0 and b4 share their reference time, so the digest decides.
	tieFirst, tieSecond := 0, 4
	if bytes.Compare(digests[4][:], digests[0][:]) < 0 {
		tieFirst, tieSecond = 4, 0
	}

	// run collects with keep on a fresh colony and returns the report and
	// the record's bodies.
	run := func(t *testing.T, keep int64) (CollectReport, []ledger.CollectedBody) {
		s := newColony(t, testDeps(nil))
		for _, c := range contents {
			publish(t, s, c)
		}
		var last appender.Committed
		for _, idx := range order {
			var ds []ledger.Digest
			for _, i := range idx {
				ds = append(ds, digests[i])
			}
			last = mustAppend(t, s, ref("r", ds...))
		}
		cutoff := commitTime(t, s, last.Last) + 1
		mustAppend(t, s, ref("late", digests[5]))
		rep := collect(t, s, CollectPolicy{Actor: ledger.ActorCore, BeforeNS: cutoff, KeepBytes: keep})
		if len(rep.Records) != 1 {
			t.Fatalf("KeepBytes %d: Collect = %+v, want one record", keep, rep)
		}
		_, record := collectionAt(t, s, rep.Records[0])
		if record.KeepBytes != keep {
			t.Errorf("KeepBytes %d: the record states %d", keep, record.KeepBytes)
		}
		return rep, record.Bodies
	}
	for _, tc := range []struct {
		keep int64
		want []int // the collected bodies, oldest first
	}{
		{125, []int{3, tieFirst}},                // 175, 135, then at or under 125
		{70, []int{3, tieFirst, tieSecond, 1}},   // 175, 135, ..., 55
		{0, []int{3, tieFirst, tieSecond, 1, 2}}, // every eligible body
	} {
		var present int64 = 175
		var want []ledger.CollectedBody
		for _, i := range tc.want {
			present -= int64(sizes[i])
			want = append(want, ledger.CollectedBody{SHA256: digests[i], Bytes: int64(sizes[i])})
		}
		slices.SortFunc(want, func(x, y ledger.CollectedBody) int { return bytes.Compare(x.SHA256[:], y.SHA256[:]) })
		// Two identical colonies choose the same bodies.
		for i := range 2 {
			rep, bodies := run(t, tc.keep)
			if rep.Recorded != len(tc.want) || rep.PresentBytes != present || !reflect.DeepEqual(bodies, want) {
				t.Errorf("KeepBytes %d, run %d: Collect = %+v recording %+v; want %+v and %d bytes present", tc.keep, i, rep, bodies, want, present)
			}
		}
	}
}

func TestCollectSeesReferencesAddedDuringScan(t *testing.T) {
	d := testDeps(nil)
	var s *Store
	var b ledger.Digest
	d.afterCollectScan = func() { mustAppend(t, s, ref("during the scan", b)) }
	s = newColony(t, d)
	b = publish(t, s, "referenced again during the scan")
	mustAppend(t, s, ref("before", b))
	cutoff := commitTime(t, s, 1) + int64(time.Second/2)
	if rep := collect(t, s, CollectPolicy{Actor: ledger.ActorCore, BeforeNS: cutoff}); rep.Recorded != 0 || len(rep.Records) != 0 {
		t.Errorf("Collect = %+v, want nothing collected", rep)
	}
	assertReadable(t, s, b, "referenced again during the scan")
}

func TestCollectLeftoverRetried(t *testing.T) {
	ff := &faultFiles{}
	s := newColony(t, testDeps(ff))
	b1, b2 := publish(t, s, "b1: its deletion fails"), publish(t, s, "b2: recent")
	mustAppend(t, s, ref("1", b1))
	cutoff := commitTime(t, s, 1) + int64(time.Second/2)
	mustAppend(t, s, ref("2", b2))
	ff.onRemove = func(path string) error {
		if filepath.Base(path) == b1.String() {
			return errFault
		}
		return os.Remove(path)
	}
	policy := CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: cutoff}
	if rep := collect(t, s, policy); rep.Leftovers != 1 || rep.Recorded != 0 || !slices.Equal(rep.Records, []uint64{3}) {
		t.Errorf("Collect with a failed deletion = %+v, want one leftover recorded at seq 3", rep)
	}
	if _, record := collectionAt(t, s, 3); len(record.Bodies) != 1 || record.Bodies[0].SHA256 != b1 {
		t.Errorf("record = %+v, want b1", record)
	}
	assertReadable(t, s, b1, "b1: its deletion fails")
	ff.onRemove = nil

	// The leftover's file is old, so the next Collect deletes it, with no new
	// record, even though its cutoff selects nothing new.
	setMtime(t, s, b1, cutoff-hour)
	rep := collect(t, s, policy)
	if want := (CollectReport{LeftoversDeleted: 1, PresentBytes: int64(len("b2: recent"))}); !reflect.DeepEqual(rep, want) {
		t.Errorf("Collect after the failure = %+v, want %+v", rep, want)
	}
	assertAbsent(t, pathOf(s, b1))
	assertCollected(t, s, b1, 3)
}

func TestCollectKeepsFreshRepublication(t *testing.T) {
	s := newColony(t, testDeps(nil))
	content := "collected, then published again"
	b := publish(t, s, content)
	mustAppend(t, s, ref("1", b))
	cutoff := commitTime(t, s, 1) + int64(time.Second/2)
	policy := CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: cutoff}
	if rep := collect(t, s, policy); !slices.Equal(rep.Records, []uint64{2}) {
		t.Fatalf("first Collect = %+v, want a record at seq 2", rep)
	}

	// Published again with a fresh file, the body waits for its reference:
	// its old reference and collection do not make it eligible.
	publish(t, s, content)
	setMtime(t, s, b, cutoff+hour)
	if rep := collect(t, s, policy); !reflect.DeepEqual(rep, CollectReport{PresentBytes: int64(len(content))}) {
		t.Errorf("Collect after republication = %+v, want nothing taken", rep)
	}
	assertReadable(t, s, b, content)

	// A new reference makes it a referenced body again, recorded by age.
	mustAppend(t, s, ref("3", b))
	later := CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: commitTime(t, s, 3) + 1}
	if rep := collect(t, s, later); rep.Recorded != 1 || !slices.Equal(rep.Records, []uint64{4}) {
		t.Errorf("Collect after the new reference = %+v, want it recorded at seq 4", rep)
	}
	assertCollected(t, s, b, 4)
}

func TestCollectRecordFailureDeletesNothing(t *testing.T) {
	ff := &faultFiles{}
	s := newColony(t, testDeps(ff))
	contents := []string{"b1: referenced", "b2: referenced", "u: unreferenced"}
	var b []ledger.Digest
	for _, c := range contents {
		b = append(b, publish(t, s, c))
	}
	mustAppend(t, s, ref("1", b[0]))
	mustAppend(t, s, ref("2", b[1]))
	cutoff := commitTime(t, s, 2) + 1
	setMtime(t, s, b[2], cutoff-hour)
	logBefore := snapshot(t, s.LogDir())
	policy := CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: cutoff}

	ff.onSync = func(segmentFile) error { return errFault }
	if _, err := s.Collect(context.Background(), policy); !errors.Is(err, appender.ErrUncertain) {
		t.Fatalf("Collect with a failed flush = %v, want ErrUncertain", err)
	}
	ff.onSync = nil
	for i, d := range b {
		if _, err := os.Lstat(pathOf(s, d)); err != nil {
			t.Errorf("%s after the failed record: %v, want it present", contents[i], err)
		}
	}
	if _, err := s.Collect(context.Background(), policy); !errors.Is(err, appender.ErrPoisoned) {
		t.Errorf("Collect while poisoned = %v, want ErrPoisoned", err)
	}
	if err := s.Recover(); err != nil {
		t.Fatalf("Recover = %v", err)
	}
	assertSnapshot(t, s.LogDir(), logBefore)
	assertReadable(t, s, b[0], contents[0])

	rep := collect(t, s, policy)
	if rep.Recorded != 2 || rep.Unreferenced != 1 || !slices.Equal(rep.Records, []uint64{3}) {
		t.Errorf("Collect after Recover = %+v, want b1 and b2 recorded at seq 3 and u deleted", rep)
	}
}

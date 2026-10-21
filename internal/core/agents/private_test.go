package agents

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func createPrivate(t *testing.T, s *Service, b Binding, id int) Binding {
	t.Helper()
	b.ContextID = privateID(id)
	if _, err := s.CreateContext(context.Background(), ContextCreation{Binding: b, CommandID: privateID(id + 1), Kind: "work"}); err != nil {
		t.Fatal(err)
	}
	return b
}
func memoryWrite(b Binding, id int, revision uint64, text string) PrivateWrite {
	return PrivateWrite{Binding: b, CommandID: privateID(id), Scope: "context", ExpectedVersion: revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte(text)}}}
}

func TestPrivateContextsDoNotFollowSelection(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	a := registerPrivate(t, f.service, 10)
	b := createPrivate(t, f.service, a, 20)
	other := registerPrivate(t, f.service, 30)
	for i, binding := range []Binding{a, b} {
		text := []byte{byte('A' + i)}
		writePrivate(t, f.service, binding, 50+i, 1, Change{"memory", "replace", text}, Change{"handoff", "replace", append([]byte("handoff-"), text...)}, Change{"transcript", "append", text}, Change{"continuation", "replace", text})
	}
	for _, binding := range []Binding{a, b, a} {
		want := "A"
		if binding == b {
			want = "B"
		}
		for _, slot := range []string{"memory", "continuation"} {
			p := readPrivate(t, f.service, binding, 2, slot)
			if string(p.Bytes) != want {
				t.Fatalf("%s crossed binding: %q", slot, p.Bytes)
			}
			p.Bytes[0] = '!'
		}
		if p := readPrivate(t, f.service, binding, 2, "handoff"); string(p.Bytes) != "handoff-"+want {
			t.Fatalf("handoff crossed binding: %q", p.Bytes)
		}
		p := readPrivate(t, f.service, binding, 2, "transcript")
		if len(p.Chunks) != 1 || string(p.Chunks[0].Bytes) != want {
			t.Fatalf("transcript crossed binding: %+v", p)
		}
	}
	wrong := Binding{AgentID: other.AgentID, ContextID: a.ContextID}
	if _, err := f.service.ReadPrivate(context.Background(), PrivateRead{ReadBinding: ReadBinding{wrong, "context"}, Slot: "memory", Revision: 2, MaxBytes: 100}); !errors.Is(err, ErrNotBound) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err := f.service.CreateContext(context.Background(), ContextCreation{Binding: wrong, CommandID: privateID(60), Kind: "work"}); !errors.Is(err, ErrNotBound) {
		t.Fatalf("reused context: %v", err)
	}
	snap, err := f.service.Snapshot(context.Background(), ReadBinding{a, "context"})
	if err != nil {
		t.Fatal(err)
	}
	delete(snap.Heads, "memory")
	snap.Materializations["memory"] = "foreign"
	snap, err = f.service.Snapshot(context.Background(), ReadBinding{a, "context"})
	if err != nil || snap.Heads["memory"].Bytes != 1 || snap.Materializations["memory"] != "current" {
		t.Fatalf("mutable snapshot: %+v %v", snap, err)
	}
}
func TestPrivateTranscriptRepeatedChunkAndPaging(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t, serviceDeps{})
	b := registerPrivate(t, f.service, 10)
	for i, text := range []string{"same", "same", "last"} {
		writePrivate(t, f.service, b, 20+i, uint64(i+1), Change{"transcript", "append", []byte(text)})
	}
	req := PrivateRead{ReadBinding: ReadBinding{b, "context"}, Slot: "transcript", Revision: 4, MaxBytes: 4}
	var chunks []string
	var sequences []uint64
	for i := range 3 {
		p, err := f.service.ReadPrivate(context.Background(), req)
		if err != nil || len(p.Chunks) != 1 {
			t.Fatalf("page %d: %+v %v", i, p, err)
		}
		chunks = append(chunks, string(p.Chunks[0].Bytes))
		sequences = append(sequences, p.Chunks[0].Sequence)
		req.Cursor = p.NextCursor
		if (i == 2) != (req.Cursor == 0) {
			t.Fatalf("continuation %d: %d", i, req.Cursor)
		}
	}
	if !reflect.DeepEqual(chunks, []string{"same", "same", "last"}) || sequences[0] >= sequences[1] || sequences[1] >= sequences[2] {
		t.Fatalf("lost occurrences: %v %v", chunks, sequences)
	}
	req.Cursor = 0
	req.Revision = 2
	req.MaxBytes = 100
	p, err := f.service.ReadPrivate(context.Background(), req)
	if err != nil || len(p.Chunks) != 1 || p.NextCursor != 0 {
		t.Fatalf("newer transcript leaked: %+v %v", p, err)
	}
	req.MaxBytes = 3
	if _, err := f.service.ReadPrivate(context.Background(), req); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize chunk: %v", err)
	}
	req.MaxBytes = 100
	req.Cursor = sequences[0] + 1
	if _, err := f.service.ReadPrivate(context.Background(), req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-occurrence cursor: %v", err)
	}
	writePrivate(t, f.service, b, 30, 4, Change{"continuation", "replace", []byte("opaque")})
	writePrivate(t, f.service, b, 31, 5, Change{"continuation", "clear", nil})
	if p := readPrivate(t, f.service, b, 6, "continuation"); p.Present || p.Bytes != nil {
		t.Fatalf("cleared continuation: %+v", p)
	}
	if p := readPrivate(t, f.service, b, 5, "continuation"); !p.Present || string(p.Bytes) != "opaque" {
		t.Fatalf("lost historical continuation: %+v", p)
	}
}
func TestPrivateExpectedRevisionCAS(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"context", "agent"} {
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t, serviceDeps{})
			b := registerPrivate(t, f.service, 10)
			start := make(chan struct{})
			type outcome struct {
				r       Receipt
				err     error
				request PrivateWrite
			}
			done := make(chan outcome, 2)
			for i := range 2 {
				request := memoryWrite(b, 20+i, 1, string(rune('a'+i)))
				request.Scope = scope
				go func() {
					<-start
					r, err := f.service.WritePrivate(context.Background(), request)
					done <- outcome{r, err, request}
				}()
			}
			close(start)
			a, c := <-done, <-done
			if a.err != nil {
				a, c = c, a
			}
			var revision *RevisionError
			if a.err != nil || !errors.As(c.err, &revision) || revision.Expected != 1 || revision.Current != 2 {
				t.Fatalf("CAS winners: %+v %+v", a, c)
			}
			before := f.store.Durable()
			again, err := f.service.WritePrivate(context.Background(), a.request)
			if err != nil || again.Sequence != a.r.Sequence || f.store.Durable() != before {
				t.Fatalf("stale winner replay: %+v %v", again, err)
			}
			c.request.ExpectedVersion = 2
			if _, err := f.service.WritePrivate(context.Background(), c.request); err != nil {
				t.Fatalf("refusal consumed ID: %v", err)
			}
			a.request.Changes[0].Bytes = []byte("different")
			if _, err := f.service.WritePrivate(context.Background(), a.request); !errors.Is(err, ErrCommandMismatch) {
				t.Fatalf("CAS before mismatch: %v", err)
			}
		})
	}
}
func TestPrivateReceiptReplayAndMismatch(t *testing.T) {
	t.Parallel()
	t.Run("old_before_capacity", func(t *testing.T) {
		t.Parallel()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		var hold atomic.Bool
		limits := DefaultLimits()
		limits.MaxConcurrentWrites = 1
		f := newServiceFixtureWithLimits(t, serviceDeps{beforePublish: func(b Binding, scope string) error {
			if hold.Load() {
				close(entered)
				<-release
			}
			return nil
		}}, limits)
		b := registerPrivate(t, f.service, 10)
		other := createPrivate(t, f.service, b, 40)
		req := memoryWrite(b, 20, 1, "old")
		old, err := f.service.WritePrivate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		writePrivate(t, f.service, b, 30, 2, Change{"memory", "replace", []byte("new")})
		hold.Store(true)
		done := make(chan error, 1)
		go func() {
			_, err := f.service.WritePrivate(context.Background(), memoryWrite(other, 50, 1, "held"))
			done <- err
		}()
		<-entered
		before := f.store.Durable()
		r, err := f.service.WritePrivate(context.Background(), req)
		if err != nil || r.Sequence != old.Sequence || f.store.Durable() != before {
			t.Fatalf("capacity/CAS before replay: %+v %v", r, err)
		}
		req.ExpectedVersion = 2
		if _, err := f.service.WritePrivate(context.Background(), req); !errors.Is(err, ErrCommandMismatch) {
			t.Fatalf("mismatch lost to capacity: %v", err)
		}
		var busy *BusyError
		if _, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 60, 3, "later")); !errors.As(err, &busy) || f.store.Durable() != before {
			t.Fatalf("preparation capacity did not refuse without consuming an ID: %v", err)
		}
		hold.Store(false)
		once.Do(func() { close(release) })
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 60, 3, "later")); err != nil {
			t.Fatalf("capacity refusal consumed ID: %v", err)
		}
	})
	t.Run("submit_duplicate", func(t *testing.T) {
		t.Parallel()
		var race atomic.Bool
		var f *serviceFixture
		f = newServiceFixture(t, serviceDeps{beforeSubmit: func(d []ledger.Draft) []ledger.Draft {
			if race.CompareAndSwap(true, false) {
				if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
					t.Fatal(err)
				}
			}
			return d
		}})
		b := registerPrivate(t, f.service, 10)
		race.Store(true)
		r, err := f.service.WritePrivate(context.Background(), memoryWrite(b, 20, 1, "race"))
		if err != nil || r.Revision != 2 || r.Sequence != 9 || f.store.Durable() != 9 {
			t.Fatalf("Submit duplicate: %+v %v through %d", r, err, f.store.Durable())
		}
		got, err := f.service.CommandStatus(context.Background(), CommandLookup{Binding: b, CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
		if err != nil || !reflect.DeepEqual(r, got) {
			t.Fatalf("duplicate receipt: %+v %v", got, err)
		}
	})
}
func TestPrivateIndependentScopesProgress(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	projected, finish := make(chan struct{}), make(chan struct{})
	var once, finishOnce sync.Once
	defer once.Do(func() { close(release) })
	defer finishOnce.Do(func() { close(finish) })
	var hold atomic.Bool
	var a Binding
	f := newServiceFixture(t, serviceDeps{
		beforeMaterialize: func(b Binding, scope, slot string) error {
			if hold.Load() && b == a && scope == "context" && slot == "memory" {
				close(entered)
				<-release
			}
			return nil
		},
		beforeProjection: func(b Binding, scope string) {
			if hold.Load() && b == a && scope == "context" {
				close(projected)
				<-finish
			}
		},
	})
	a = registerPrivate(t, f.service, 10)
	b := createPrivate(t, f.service, a, 20)
	hold.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := f.service.WritePrivate(context.Background(), PrivateWrite{Binding: a, CommandID: privateID(30), Scope: "context", ExpectedVersion: 1, Changes: []Change{{"memory", "replace", []byte("new memory")}, {"handoff", "replace", []byte("new handoff")}}})
		done <- err
	}()
	<-entered
	writePrivate(t, f.service, b, 40, 1, Change{"memory", "replace", []byte("independent")})
	if p := readPrivate(t, f.service, b, 2, "memory"); string(p.Bytes) != "independent" {
		t.Fatal("B failed while A held")
	}
	snapshot := make(chan Snapshot, 1)
	go func() { p, _ := f.service.Snapshot(context.Background(), ReadBinding{a, "context"}); snapshot <- p }()
	select {
	case err := <-done:
		t.Fatalf("ack before materialization: %v", err)
	default:
	}
	select {
	case p := <-snapshot:
		t.Fatalf("partially applied revision: %+v", p)
	default:
	}
	once.Do(func() { close(release) })
	<-projected
	select {
	case err := <-done:
		t.Fatalf("ack before projection: %v", err)
	default:
	}
	finishOnce.Do(func() { close(finish) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p := <-snapshot
	if p.Revision != 2 || p.Materializations["memory"] != "current" || p.Materializations["handoff"] != "current" {
		t.Fatalf("mixed projection: %+v", p)
	}
	base := filepath.Join(f.root, "agents", "Scout-10", "contexts", a.ContextID.String())
	if string(readTestFile(t, filepath.Join(base, "Memory.md"))) != "new memory" || string(readTestFile(t, filepath.Join(base, "Handoff.md"))) != "new handoff" {
		t.Fatal("acknowledged incomplete materialization")
	}
}

package ledgerstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
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
)

// digestOf returns the SHA-256 of content.
func digestOf(content string) ledger.Digest { return sha256.Sum256([]byte(content)) }

// pathOf returns the path where s keeps body d: bodies/<first two hex
// digits>/<64 hex digits>.
func pathOf(s *Store, d ledger.Digest) string {
	return filepath.Join(s.dir, "bodies", d.String()[:2], d.String())
}

// publish publishes content in s and returns its digest.
func publish(t testing.TB, s *Store, content string) ledger.Digest {
	t.Helper()
	d, n, err := s.PublishBody(context.Background(), strings.NewReader(content))
	if err != nil || d != digestOf(content) || n != int64(len(content)) {
		t.Fatalf("PublishBody(%q) = %v, %d, %v", content, d, n, err)
	}
	return d
}

// ref returns a fixture.sample v1 draft referencing ds.
func ref(tag string, ds ...ledger.Digest) ledger.Draft {
	d := sample(tag)
	d.BodyRefs = slices.SortedFunc(slices.Values(ds), func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
	return d
}

func pinDraft(t testing.TB, id uuid.UUID, ds ...ledger.Digest) ledger.Draft {
	t.Helper()
	p, err := ledger.BodyPin{PinID: id}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	d := ref("", ds...)
	d.Type, d.Payload = ledger.TypeBodyPin, p
	return d
}

func unpinDraft(t testing.TB, id uuid.UUID) ledger.Draft {
	t.Helper()
	p, err := ledger.BodyUnpin{PinID: id}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Draft{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: p}
}

// assertTmpEmpty checks that no publication left a temporary file.
func assertTmpEmpty(t testing.TB, s *Store) {
	t.Helper()
	if entries, err := os.ReadDir(filepath.Join(s.dir, "bodies", ".tmp")); err != nil || len(entries) != 0 {
		t.Errorf("bodies/.tmp holds %v, %v; want it empty", entries, err)
	}
}

// assertAbsent checks that no file is at path.
func assertAbsent(t testing.TB, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: %v, want it absent", path, err)
	}
}

func TestPublishBodyRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := testDeps(nil)
	d.maxBodyBytes = 2*copyChunk + 5 // a body at the cap spans three chunks
	s := newColony(t, d)
	for i, size := range []int64{0, 1, d.maxBodyBytes} {
		content := bytes.Repeat([]byte{byte('a' + i)}, int(size))
		want := ledger.Digest(sha256.Sum256(content))
		got, n, err := s.PublishBody(ctx, bytes.NewReader(content))
		if err != nil || got != want || n != size {
			t.Fatalf("PublishBody of %d bytes = %v, %d, %v; want %v, %d", size, got, n, err, want, size)
		}
		if data, err := os.ReadFile(pathOf(s, want)); err != nil || !bytes.Equal(data, content) {
			t.Errorf("%d-byte body file: %d bytes, %v; want the content", size, len(data), err)
		}
		if data, err := s.ReadBody(ctx, got); err != nil || !bytes.Equal(data, content) {
			t.Errorf("ReadBody of the %d-byte body = %d bytes, %v; want the content", size, len(data), err)
		}
		if again, n, err := s.PublishBody(ctx, bytes.NewReader(content)); err != nil || again != want || n != size {
			t.Errorf("republishing the %d-byte body = %v, %d, %v", size, again, n, err)
		}
		if bodies, err := listBodies(s.bodiesDir); err != nil || len(bodies) != i+1 {
			t.Errorf("after publishing %d bodies twice, the store holds %d, %v", i+1, len(bodies), err)
		}
		assertTmpEmpty(t, s)
	}

	over := bytes.Repeat([]byte{'z'}, int(d.maxBodyBytes)+1)
	if _, _, err := s.PublishBody(ctx, bytes.NewReader(over)); !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("PublishBody of cap+1 bytes = %v, want ErrBodyTooLarge", err)
	}
	assertAbsent(t, pathOf(s, sha256.Sum256(over)))
	assertTmpEmpty(t, s)

	kept := publish(t, s, "kept")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishBody(ctx, strings.NewReader("after close")); !errors.Is(err, appender.ErrClosed) {
		t.Errorf("PublishBody after Close = %v, want ErrClosed", err)
	}
	if _, err := s.ReadBody(ctx, kept); !errors.Is(err, appender.ErrClosed) {
		t.Errorf("ReadBody after Close = %v, want ErrClosed", err)
	}
	assertTmpEmpty(t, s)
}

func TestPublishBodyFailsWithoutDurability(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		arm  func(*faultFiles)
	}{
		{"temporary file flush", func(ff *faultFiles) {
			ff.onTempSync = func(bodyFile) error { return errFault }
		}},
		{"rename", func(ff *faultFiles) {
			ff.onRename = func(string, string) error { return errFault }
		}},
		{"directory flush after the rename", func(ff *faultFiles) {
			ff.onRename = func(from, to string) error {
				if err := os.Rename(from, to); err != nil {
					return err
				}
				return errFault
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := &faultFiles{}
			s := newColony(t, testDeps(ff))
			content := "body failing at " + tc.name
			tc.arm(ff)
			if _, _, err := s.PublishBody(ctx, strings.NewReader(content)); !errors.Is(err, errFault) {
				t.Fatalf("PublishBody = %v, want the injected fault", err)
			}
			*ff = faultFiles{}
			assertAbsent(t, pathOf(s, digestOf(content)))
			assertTmpEmpty(t, s)
			if _, err := s.Append(ctx, []ledger.Draft{ref("r", digestOf(content))}); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrBodyUnpublished) {
				t.Errorf("Append referencing the failed body = %v, want ErrRefused and ErrBodyUnpublished", err)
			}
		})
	}
}

func TestReadBodyVerifies(t *testing.T) {
	ctx := context.Background()
	d := testDeps(nil)
	d.maxBodyBytes = 64
	s := newColony(t, d)
	for _, tc := range []struct {
		name   string
		damage func([]byte) []byte
	}{
		{"flipped byte", func(b []byte) []byte { b[3] ^= 1; return b }},
		{"truncation", func(b []byte) []byte { return b[:len(b)-1] }},
		{"other content", func([]byte) []byte { return []byte("other bytes") }},
		{"over the cap", func([]byte) []byte { return bytes.Repeat([]byte{'x'}, 65) }},
	} {
		dg := publish(t, s, "verified body, "+tc.name)
		path := pathOf(s, dg)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, tc.damage(data))
		if got, err := s.ReadBody(ctx, dg); !errors.Is(err, ErrBodyCorrupt) || got != nil {
			t.Errorf("%s: ReadBody = %q, %v; want nothing and ErrBodyCorrupt", tc.name, got, err)
		}
	}

	// A body that vanished without a collection record is missing, also when
	// collection records exist.
	collected := publish(t, s, "collected")
	mustAppend(t, s, ref("c", collected))
	if rep, err := s.Collect(ctx, CollectPolicy{Actor: ledger.ActorCore, BeforeNS: math.MaxInt64}); err != nil || len(rep.Records) != 1 {
		t.Fatalf("Collect = %+v, %v; want one record", rep, err)
	}
	vanished := publish(t, s, "vanished")
	if err := os.Remove(pathOf(s, vanished)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadBody(ctx, vanished); !errors.Is(err, ErrBodyMissing) || errors.Is(err, ErrBodyUnavailable) {
		t.Errorf("ReadBody of a vanished body = %v, want ErrBodyMissing", err)
	}
}

func writeFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAppendRefusesUnpublishedBodies(t *testing.T) {
	ctx := context.Background()
	s := newColony(t, testDeps(nil))
	mustAppend(t, s, sample("first"))
	content := "published late"
	drafts := []ledger.Draft{sample("plain"), ref("late", digestOf(content))}
	before := snapshot(t, s.LogDir())
	if _, err := s.Append(ctx, drafts); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrBodyUnpublished) {
		t.Errorf("Append referencing an unpublished body = %v, want ErrRefused and ErrBodyUnpublished", err)
	}
	assertSnapshot(t, s.LogDir(), before)
	if s.Durable() != 1 {
		t.Errorf("Durable after the refusal = %d, want 1", s.Durable())
	}
	publish(t, s, content)
	if c := mustAppend(t, s, drafts...); c != (appender.Committed{First: 2, Last: 3}) {
		t.Errorf("Append after publication = %+v, want 2-3", c)
	}

	// An entry under a body's name that is not a regular file is no body.
	odd := digestOf("a directory")
	if err := os.Mkdir(pathOf(s, odd), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, []ledger.Draft{ref("odd", odd)}); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrBodyStore) {
		t.Errorf("Append referencing a directory = %v, want ErrRefused and ErrBodyStore", err)
	}
	if s.Durable() != 3 {
		t.Errorf("Durable after the refusal = %d, want 3", s.Durable())
	}
}

func TestF0TypeRules(t *testing.T) {
	ctx := context.Background()
	d := testDeps(nil)
	d.maxPinned = 3
	s := newColony(t, d)
	b := []ledger.Digest{publish(t, s, "b0"), publish(t, s, "b1"), publish(t, s, "b2"), publish(t, s, "b3")}
	p1, p2, p3, p4 := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	collected, err := ledger.BodiesCollected{BeforeNS: 1, Bodies: []ledger.CollectedBody{{SHA256: b[0], Bytes: 2}}}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	badPin := pinDraft(t, p1, b[0])
	badPin.Payload = json.RawMessage(`{"pin_id":"not a UUID"}`)
	unpinWithRefs := unpinDraft(t, p1)
	unpinWithRefs.BodyRefs = b[:1]

	refused := func(name string, drafts ...ledger.Draft) {
		t.Helper()
		before, seq := snapshot(t, s.LogDir()), s.Durable()
		if _, err := s.Append(ctx, drafts); !errors.Is(err, appender.ErrRefused) {
			t.Errorf("%s: Append = %v, want ErrRefused", name, err)
		}
		assertSnapshot(t, s.LogDir(), before)
		if s.Durable() != seq {
			t.Errorf("%s: Durable %d after the refusal, want %d", name, s.Durable(), seq)
		}
	}
	accepted := func(name string, drafts ...ledger.Draft) {
		t.Helper()
		if _, err := s.Append(ctx, drafts); err != nil {
			t.Errorf("%s: Append = %v, want success", name, err)
		}
	}

	refused("a collection record", ledger.Draft{Type: ledger.TypeBodiesCollected, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: collected})
	refused("a pin without references", pinDraft(t, p1))
	refused("a pin whose payload does not decode", badPin)
	refused("an unpin of an unknown pin", unpinDraft(t, p1))
	refused("one pin twice in a commit", pinDraft(t, p1, b[0]), pinDraft(t, p1, b[1]))
	refused("pins beyond the bound", pinDraft(t, p1, b...))

	accepted("a pin", pinDraft(t, p1, b[0], b[1]))
	refused("a pin of an active pin", pinDraft(t, p1, b[2]))
	refused("an unpin with references", unpinWithRefs)
	refused("a pin past the bound", pinDraft(t, p2, b[2], b[3]))
	accepted("a pin up to the bound", pinDraft(t, p2, b[2]))
	refused("a pin at the bound", pinDraft(t, p3, b[3]))
	accepted("an unpin making room for a pin in one commit", unpinDraft(t, p1), pinDraft(t, p3, b[3]))
	accepted("a pin and its unpin in one commit", pinDraft(t, p4, b[0]), unpinDraft(t, p4))
	accepted("a pin ID released earlier pinned again", pinDraft(t, p1, b[0]))

	// A bound lowered below the active pairs refuses new pins and still lets
	// unpins shrink the projection.
	s.deps.maxPinned = 1
	refused("a pin over a lowered bound", pinDraft(t, p4, b[1]))
	accepted("an unpin over a lowered bound", unpinDraft(t, p1))
	accepted("an unpin down to the lowered bound", unpinDraft(t, p2))
	accepted("a pin within the lowered bound", unpinDraft(t, p3), pinDraft(t, p4, b[1]))
}

func TestOpenBodiesLayout(t *testing.T) {
	ctx := context.Background()
	cat := testCatalogue(t)
	fanOuts := func(t *testing.T, dir string) {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join(dir, "bodies"))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() {
				t.Errorf("bodies/%s is not a directory", e.Name())
			}
			names = append(names, e.Name())
		}
		want := []string{".tmp"}
		for i := range 256 {
			want = append(want, fmt.Sprintf("%02x", i))
		}
		if !slices.Equal(names, want) {
			t.Errorf("bodies holds %d entries %v..., want .tmp and 00 to ff", len(names), names[:min(len(names), 3)])
		}
	}

	t.Run("Create makes the layout", func(t *testing.T) {
		s := newColony(t, testDeps(nil))
		fanOuts(t, s.dir)
	})

	t.Run("Create completes an interrupted Create", func(t *testing.T) {
		dir := t.TempDir()
		for _, sub := range []string{"log", "bodies/.tmp", "bodies/00", "bodies/7f"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		s, err := Create(dir, cat)
		if err != nil {
			t.Fatalf("Create = %v", err)
		}
		t.Cleanup(func() { s.Close() })
		fanOuts(t, dir)
	})

	for _, tc := range []struct{ name, path string }{
		{"a body file", "bodies/ab/" + strings.Repeat("ab", 32)},
		{"an unexpected file", "bodies/notes.txt"},
		{"a file named like a fan-out directory", "bodies/0a"},
	} {
		t.Run("Create refuses "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, []byte("x"))
			if _, err := Create(dir, cat); !errors.Is(err, ErrBodyStore) {
				t.Errorf("Create = %v, want ErrBodyStore", err)
			}
			assertAbsent(t, filepath.Join(dir, colonyFile))
			assertUnlocked(t, dir)
		})
	}

	// closedColony returns a closed colony with one referenced body.
	closedColony := func(t *testing.T) (*Store, ledger.Digest) {
		s := newColony(t, testDeps(nil))
		b := publish(t, s, "the body")
		mustAppend(t, s, ref("r", b))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return s, b
	}

	t.Run("Open without bodies fails closed", func(t *testing.T) {
		s, _ := closedColony(t)
		if err := os.RemoveAll(s.bodiesDir); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, s.dir)
		if _, _, err := Open(s.dir, s.cat); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open = %v, want an error matching fs.ErrNotExist", err)
		}
		assertSnapshot(t, s.dir, before)
		assertAbsent(t, s.bodiesDir)
		assertUnlocked(t, s.dir)
	})

	t.Run("Open empties .tmp", func(t *testing.T) {
		s, b := closedColony(t)
		writeFile(t, filepath.Join(s.bodiesDir, ".tmp", "body-123"), []byte("left by a crash"))
		s2, _, err := open(s.dir, s.cat, testDeps(nil))
		if err != nil {
			t.Fatalf("Open = %v", err)
		}
		t.Cleanup(func() { s2.Close() })
		assertTmpEmpty(t, s2)
		if data, err := s2.ReadBody(ctx, b); err != nil || string(data) != "the body" {
			t.Errorf("ReadBody after Open = %q, %v", data, err)
		}
	})

	for _, tc := range []struct {
		name string
		make func(t *testing.T, s *Store)
	}{
		{"an unexpected file", func(t *testing.T, s *Store) {
			writeFile(t, filepath.Join(s.bodiesDir, "notes.txt"), []byte("x"))
		}},
		{"a body under the wrong fan-out directory", func(t *testing.T, s *Store) {
			d := digestOf("misplaced")
			wrong := "00"
			if d.String()[:2] == wrong {
				wrong = "01"
			}
			writeFile(t, filepath.Join(s.bodiesDir, wrong, d.String()), []byte("misplaced"))
		}},
		{"a directory under a body's name", func(t *testing.T, s *Store) {
			if err := os.Mkdir(pathOf(s, digestOf("a directory")), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file not named by a digest", func(t *testing.T, s *Store) {
			writeFile(t, filepath.Join(s.bodiesDir, "ab", "ab"+strings.Repeat("A", 62)), []byte("x"))
		}},
	} {
		t.Run("Collect fails closed on "+tc.name, func(t *testing.T) {
			s := newColony(t, testDeps(nil))
			b := publish(t, s, "eligible")
			mustAppend(t, s, ref("r", b))
			tc.make(t, s)
			bodies, log := snapshot(t, s.bodiesDir), snapshot(t, s.LogDir())
			if _, err := s.Collect(ctx, CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64}); !errors.Is(err, ErrBodyStore) {
				t.Errorf("Collect = %v, want ErrBodyStore", err)
			}
			assertSnapshot(t, s.bodiesDir, bodies)
			assertSnapshot(t, s.LogDir(), log)
		})
	}
}

func TestBodiesConcurrentUse(t *testing.T) {
	// Publication, reads, appends with references and collection run
	// together, and Close lands while they do. Every acknowledged reference
	// reads back verified or classified as collected.
	t.Run("publish, read, append, collect and close", func(t *testing.T) {
		ctx := context.Background()
		s := newColony(t, testDeps(nil))
		const workers = 4
		var acked atomic.Int64
		var wg sync.WaitGroup
		check := func(d ledger.Digest, content string) {
			data, err := s.ReadBody(ctx, d)
			var ue *BodyUnavailableError
			switch {
			case err == nil && string(data) != content:
				t.Errorf("ReadBody(%s) = %q, want %q", d, data, content)
			case err == nil, errors.Is(err, appender.ErrClosed):
			case errors.As(err, &ue) && ue.Digest == d:
			default:
				t.Errorf("ReadBody(%s) = %v, want the verified body or a collection", d, err)
			}
		}
		for w := range workers {
			wg.Go(func() {
				type body struct {
					d       ledger.Digest
					content string
				}
				var mine []body
				for i := 0; ; i++ {
					content := fmt.Sprintf("worker %d body %d", w, i)
					d, _, err := s.PublishBody(ctx, strings.NewReader(content))
					if errors.Is(err, appender.ErrClosed) {
						return
					}
					if err != nil {
						t.Errorf("PublishBody = %v", err)
						return
					}
					_, err = s.Append(ctx, []ledger.Draft{ref(content, d)})
					switch {
					case errors.Is(err, appender.ErrClosed):
						return
					case errors.Is(err, ErrBodyUnpublished):
						// Collection took the body before its reference
						// landed; the producer moves on.
						continue
					case err != nil:
						t.Errorf("Append = %v", err)
						return
					}
					acked.Add(1)
					mine = append(mine, body{d, content})
					for _, b := range []body{mine[0], mine[len(mine)/2], mine[len(mine)-1]} {
						check(b.d, b.content)
					}
				}
			})
		}
		wg.Go(func() {
			for {
				_, err := s.Collect(ctx, CollectPolicy{Actor: ledger.ActorCore, BeforeNS: math.MaxInt64})
				if errors.Is(err, appender.ErrClosed) {
					return
				}
				if err != nil {
					t.Errorf("Collect = %v", err)
					return
				}
			}
		})
		deadline := time.Now().Add(time.Minute)
		for acked.Load() < 200 && !t.Failed() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if n := acked.Load(); n < 200 {
			t.Errorf("%d references acknowledged within a minute, want 200", n)
		}
		if err := s.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
		wg.Wait()
	})

	t.Run("Close waits for an in-flight read", func(t *testing.T) {
		ctx := context.Background()
		ff := &faultFiles{}
		s := newColony(t, testDeps(ff))
		b := publish(t, s, "read during Close")
		entered, release := make(chan struct{}), make(chan struct{})
		ff.onOpenBody = func(path string) (io.ReadCloser, error) {
			close(entered)
			<-release
			return os.Open(path)
		}
		read := make(chan error, 1)
		go func() {
			data, err := s.ReadBody(ctx, b)
			if err == nil && string(data) != "read during Close" {
				err = fmt.Errorf("read %q", data)
			}
			read <- err
		}()
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		select {
		case err := <-closed:
			t.Fatalf("Close returned %v while a read was in flight", err)
		case <-time.After(50 * time.Millisecond):
		}
		close(release)
		if err := <-read; err != nil {
			t.Errorf("the in-flight ReadBody = %v, want the body", err)
		}
		if err := <-closed; err != nil {
			t.Errorf("Close = %v", err)
		}
		if _, err := s.ReadBody(ctx, b); !errors.Is(err, appender.ErrClosed) {
			t.Errorf("ReadBody after Close = %v, want ErrClosed", err)
		}
	})
}

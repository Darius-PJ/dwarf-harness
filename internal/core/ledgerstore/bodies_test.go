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

	// Submit holds the drafts it carries to the same rule, and a refusal
	// leaves the command unrecorded: it is new when the body is published.
	late := digestOf("published late, for Submit")
	id := commandID(clockMS(0), 1)
	command, effect := commandDraft(t, id, submissionOf("a")), []ledger.Draft{sample("plain"), ref("late", late)}
	before = snapshot(t, s.LogDir())
	if _, err := s.Submit(ctx, command, effect); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrBodyUnpublished) {
		t.Errorf("Submit referencing an unpublished body = %v, want ErrRefused and ErrBodyUnpublished", err)
	}
	if _, err := s.Submit(ctx, command, []ledger.Draft{ref("odd", odd)}); !errors.Is(err, appender.ErrRefused) || !errors.Is(err, ErrBodyStore) {
		t.Errorf("Submit referencing a directory = %v, want ErrRefused and ErrBodyStore", err)
	}
	assertSnapshot(t, s.LogDir(), before)
	if s.Durable() != 3 {
		t.Errorf("Durable after the refusals = %d, want 3", s.Durable())
	}
	publish(t, s, "published late, for Submit")
	if got, want := mustSubmit(t, s, command, effect...), (Receipt{Committed: appender.Committed{First: 4, Last: 6}}); got != want {
		t.Errorf("Submit after publication = %+v, want %+v", got, want)
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

	cmd := commandDraft(t, commandID(clockMS(0), 1), submissionOf("a"))
	cmdWithRefs := cmd
	cmdWithRefs.BodyRefs = b[:1]
	cmdV2 := cmd
	cmdV2.TypeVersion = 2
	cmdBad := cmd
	cmdBad.Payload = json.RawMessage(`{"command_id":"not a UUID","submission_sha256":"nor a digest"}`)
	cmdIncomplete := cmd
	cmdIncomplete.Payload = json.RawMessage(`{"command_id":"` + commandID(clockMS(0), 1).String() + `"}`)
	unknownType := ledger.Draft{Type: "fixture.unknown", TypeVersion: 1, Actor: ledger.ActorCore, Payload: json.RawMessage(`{}`)}
	submitRefused := func(name string, command ledger.Draft, drafts ...ledger.Draft) {
		t.Helper()
		before, seq := snapshot(t, s.LogDir()), s.Durable()
		if _, err := s.Submit(ctx, command, drafts); !errors.Is(err, appender.ErrRefused) {
			t.Errorf("%s: Submit = %v, want ErrRefused", name, err)
		}
		assertSnapshot(t, s.LogDir(), before)
		if s.Durable() != seq {
			t.Errorf("%s: Durable %d after the refusal, want %d", name, s.Durable(), seq)
		}
	}

	// Only Submit writes a command record, and Submit refuses what is not
	// one, or an effect that breaks a rule, whatever comes first.
	refused("a command record", cmd)
	refused("a command record among others", sample("a"), cmd)
	refused("a collection record", ledger.Draft{Type: ledger.TypeBodiesCollected, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: collected})
	submitRefused("a command with body references", cmdWithRefs)
	submitRefused("a record of another type as the command", sample("not a command"))
	submitRefused("a command of another version", cmdV2)
	submitRefused("a command whose payload does not decode", cmdBad)
	submitRefused("a command whose payload lacks the digest", cmdIncomplete)
	submitRefused("a command among the effect's drafts", cmd, cmd)
	submitRefused("a collection record among the effect's drafts", cmd, ledger.Draft{Type: ledger.TypeBodiesCollected, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: collected})
	submitRefused("an unpin of an unknown pin in the effect", cmd, unpinDraft(t, p1))
	submitRefused("a draft the catalogue lacks in the effect", cmd, unknownType)
	// None of the refusals recorded the command: it is new, and takes the
	// next sequence.
	if got, want := mustSubmit(t, s, cmd), (Receipt{Committed: appender.Committed{First: 1, Last: 1}}); got != want {
		t.Errorf("Submit of the command after the refusals = %+v, want %+v", got, want)
	}
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

	// A store whose catalogue lacks ledger.command writes none, as it
	// writes no type version outside its catalogue.
	t.Run("a catalogue without ledger.command", func(t *testing.T) {
		cat, err := ledger.NewCatalogue(ledger.TypeVersion{Type: "fixture.sample", Version: 1})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Create(filepath.Join(t.TempDir(), "colony"), cat)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if _, err := s.Submit(ctx, cmd, nil); !errors.Is(err, appender.ErrRefused) {
			t.Errorf("Submit = %v, want ErrRefused", err)
		}
		if entries, err := os.ReadDir(s.LogDir()); err != nil || len(entries) != 0 || s.Durable() != 0 {
			t.Errorf("after the refusal: log %v, %v, Durable %d; want an empty log", entries, err, s.Durable())
		}
	})
}

func TestOpenRebuildsPinsAndCollections(t *testing.T) {
	ctx := context.Background()
	policy := CollectPolicy{Actor: ledger.ActorCore, BeforeNS: math.MaxInt64}

	t.Run("a pinned body stays and a collected body classifies", func(t *testing.T) {
		d := testDeps(nil)
		s := newColony(t, d)
		held, gone := publish(t, s, "held by two pins"), publish(t, s, "collected before the restart")
		p1, p2 := uuid.NewV7(), uuid.NewV7()
		mustAppend(t, s, ref("both", held, gone))
		mustAppend(t, s, pinDraft(t, p1, held))
		mustAppend(t, s, pinDraft(t, p2, held))
		rep := collect(t, s, policy)
		if rep.Recorded != 1 || len(rep.Records) != 1 {
			t.Fatalf("Collect = %+v, want the unpinned body recorded", rep)
		}
		seq := rep.Records[0]

		s2, _ := reopen(t, s, d)
		assertCollected(t, s2, gone, seq)
		assertReadable(t, s2, held, "held by two pins")
		// Both pins are active again: the body stays until both are released.
		if rep := collect(t, s2, policy); rep.Recorded != 0 || len(rep.Records) != 0 {
			t.Errorf("Collect after Open = %+v, want the pinned body kept", rep)
		}
		mustAppend(t, s2, unpinDraft(t, p1))
		if rep := collect(t, s2, policy); rep.Recorded != 0 {
			t.Errorf("Collect with one pin released = %+v, want the body kept by the other", rep)
		}
		mustAppend(t, s2, unpinDraft(t, p2))
		rep = collect(t, s2, policy)
		if rep.Recorded != 1 || len(rep.Records) != 1 {
			t.Fatalf("Collect with both pins released = %+v, want the body recorded", rep)
		}
		assertCollected(t, s2, held, rep.Records[0])
		assertCollected(t, s2, gone, seq)
		if _, err := s2.Append(ctx, []ledger.Draft{unpinDraft(t, p1)}); !errors.Is(err, appender.ErrRefused) {
			t.Errorf("Append of the unpin of a released pin = %v, want ErrRefused", err)
		}
	})

	// A log that holds more active pairs than the bound is valid, because
	// the bound may be lowered later: it opens, refuses new pins, and lets
	// unpins shrink it.
	t.Run("a log over a lowered pin bound opens", func(t *testing.T) {
		d := testDeps(nil)
		d.maxPinned = 4
		s := newColony(t, d)
		b := []ledger.Digest{publish(t, s, "b0"), publish(t, s, "b1"), publish(t, s, "b2"), publish(t, s, "b3")}
		p1, p2, p3 := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
		mustAppend(t, s, pinDraft(t, p1, b[0], b[1]))
		mustAppend(t, s, pinDraft(t, p2, b[2], b[3]))
		d.maxPinned = 1
		s2, _ := reopen(t, s, d)
		refused := func(name string) {
			t.Helper()
			if _, err := s2.Append(ctx, []ledger.Draft{pinDraft(t, p3, b[0])}); !errors.Is(err, appender.ErrRefused) {
				t.Errorf("%s: Append of a pin = %v, want ErrRefused", name, err)
			}
		}
		refused("over the bound")
		mustAppend(t, s2, unpinDraft(t, p1))
		refused("still over the bound")
		mustAppend(t, s2, unpinDraft(t, p2))
		mustAppend(t, s2, pinDraft(t, p3, b[0]))
	})

	// The index of collection records holds the end of each commit, where a
	// reader may stop, also when the record is not alone in its commit.
	t.Run("the collection index keeps commit ends", func(t *testing.T) {
		s := newColony(t, testDeps(nil))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		x := digestOf("collected beside another record")
		p, err := ledger.BodiesCollected{BeforeNS: 1, Bodies: []ledger.CollectedBody{{SHA256: x, Bytes: 3}}}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		writeLog(t, s, []ledger.Draft{{Type: ledger.TypeBodiesCollected, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: p}, sample("beside the collection")})
		s2, _, err := open(s.dir, s.cat, testDeps(nil))
		if err != nil {
			t.Fatalf("Open = %v", err)
		}
		t.Cleanup(func() { s2.Close() })
		assertCollected(t, s2, x, 1)
	})
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
	// Publication, reads, appends and submissions with references and
	// collection run together, and Close lands while they do. Every
	// acknowledged reference reads back verified or classified as collected.
	t.Run("publish, read, append, collect and close", func(t *testing.T) {
		ctx := context.Background()
		// Commits are stamped ten minutes apart, so that a command ID dated at
		// its commit costs no read of the log, and the hot index holds eight
		// commands, so that retries of older ones meet the log beside Collect
		// and Close.
		var ticks atomic.Int64
		dd := testDeps(nil)
		dd.clock = func() time.Time { return time.Unix(1_800_000_000+600*ticks.Add(1), 0) }
		dd.hotCommands = 8
		s := newColony(t, dd)
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
		type submitted struct {
			command ledger.Draft
			effect  []ledger.Draft
			r       Receipt
		}
		for w := range workers {
			wg.Go(func() {
				type body struct {
					d       ledger.Digest
					content string
				}
				var mine []body
				var firstSubmit *submitted
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
					if i%2 == 0 {
						_, err = s.Append(ctx, []ledger.Draft{ref(content, d)})
					} else {
						command := commandDraft(t, commandID((1_800_000_000+600*ticks.Load())*1000, w<<20|i), submissionOf(content))
						sub := &submitted{command: command, effect: []ledger.Draft{ref(content, d)}}
						if sub.r, err = s.Submit(ctx, sub.command, sub.effect); err == nil {
							if firstSubmit == nil {
								firstSubmit = sub
							}
							// A retry resolves to the same commit, also once
							// collection has taken the body; the first one's
							// is long out of the hot index.
							for _, p := range []*submitted{sub, firstSubmit} {
								switch again, retryErr := s.Submit(ctx, p.command, p.effect); {
								case errors.Is(retryErr, appender.ErrClosed):
								case retryErr != nil:
									t.Errorf("retry of Submit = %v", retryErr)
								case !again.Duplicate || again.Committed != p.r.Committed:
									t.Errorf("retry of Submit = %+v, want a duplicate of %+v", again, p.r)
								}
							}
						}
					}
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

	t.Run("Close waits for an in-flight publication", func(t *testing.T) {
		ctx := context.Background()
		s := newColony(t, testDeps(nil))
		src := &gatedReader{entered: make(chan struct{}, 1), release: make(chan struct{}), stop: make(chan struct{})}
		// On a failure below, the source ends, so that the publication and
		// Close can finish.
		defer close(src.stop)
		published := make(chan error, 1)
		go func() {
			_, _, err := s.PublishBody(ctx, src)
			published <- err
		}()
		<-src.entered
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		// Close marks the store closed before it waits.
		for deadline := time.Now().Add(10 * time.Second); !s.closing.Load(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("Close did not mark the store closed")
			}
		}
		select {
		case err := <-closed:
			t.Fatalf("Close returned %v while a publication was in flight", err)
		case <-time.After(50 * time.Millisecond):
		}
		src.release <- struct{}{}
		select {
		case err := <-published:
			if !errors.Is(err, appender.ErrClosed) {
				t.Errorf("the in-flight PublishBody = %v, want ErrClosed", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("PublishBody went on reading after Close")
		}
		select {
		case err := <-closed:
			if err != nil {
				t.Errorf("Close = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Close did not return after the publication ended")
		}
		assertTmpEmpty(t, s)
		s2, _, err := open(s.dir, s.cat, testDeps(nil))
		if err != nil {
			t.Fatalf("Open after Close = %v", err)
		}
		if err := s2.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// gatedReader is a publication source whose every Read waits for a release,
// then fills the whole buffer; once stop is closed, Reads end the source.
type gatedReader struct {
	entered chan struct{} // receives, if empty, when a Read starts
	release chan struct{}
	stop    chan struct{}
}

func (g *gatedReader) Read(p []byte) (int, error) {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
		for i := range p {
			p[i] = 'x'
		}
		return len(p), nil
	case <-g.stop:
		return 0, io.EOF
	}
}

func TestReadBodyClassifiesFromSnapshot(t *testing.T) {
	ctx := context.Background()
	d := testDeps(nil)
	var s *Store
	content := "published and collected during the read"
	x := digestOf(content)
	var seq uint64
	d.beforeClassify = func() {
		publish(t, s, content)
		mustAppend(t, s, ref("x", x))
		rep := collect(t, s, CollectPolicy{Actor: ledger.ActorCore, BeforeNS: math.MaxInt64})
		if len(rep.Records) != 1 {
			t.Fatalf("Collect in the hook = %+v, want one record", rep)
		}
		seq = rep.Records[0]
	}
	s = newColony(t, d)
	// The collection lands after the read's snapshot, so for this read the
	// body is missing.
	if _, err := s.ReadBody(ctx, x); !errors.Is(err, ErrBodyMissing) || errors.Is(err, ErrBodyUnavailable) {
		t.Errorf("ReadBody during the collection = %v, want ErrBodyMissing", err)
	}
	s.deps.beforeClassify = nil
	var ue *BodyUnavailableError
	if _, err := s.ReadBody(ctx, x); !errors.As(err, &ue) || *ue != (BodyUnavailableError{Digest: x, Seq: seq}) {
		t.Errorf("ReadBody after the collection = %v, want it collected at seq %d", err, seq)
	}
}

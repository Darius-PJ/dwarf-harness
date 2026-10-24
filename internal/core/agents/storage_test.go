package agents

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func writeTestFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPrivateObjectPublication(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	s := testScope(t, root, testContext)
	data := []byte("private\r\n\x00exact bytes\n")
	digest, err := s.publishObject(data)
	if err != nil {
		t.Fatal(err)
	}
	want := ledger.Digest(sha256.Sum256(data))
	if digest != want {
		t.Fatalf("digest = %s, want %s", digest, want)
	}
	path := filepath.Join(s.objects(), want.String())
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, path); !bytes.Equal(got, data) {
		t.Fatalf("published bytes changed: %x", got)
	}
	if _, err := s.publishObject(data); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) {
		t.Fatalf("existing verified object was replaced: %v", err)
	}
	other := testScope(t, root, "")
	if _, err := other.publishObject(data); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, []byte("altered context"))
	if got := readTestFile(t, filepath.Join(other.objects(), want.String())); !bytes.Equal(got, data) {
		t.Fatal("agent and context objects alias")
	}
}

func TestPrivateObjectRefusesCorruption(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, old string }{{"digest", "tampered"}, {"size", "different length"}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			old := row.old
			s := testScope(t, testRoot(t), testContext)
			if err := s.ensureObjects(); err != nil {
				t.Fatal(err)
			}
			data := []byte("original")
			digest := ledger.Digest(sha256.Sum256(data))
			path := filepath.Join(s.objects(), digest.String())
			writeTestFile(t, path, []byte(old))
			_, err := s.publishObject(data)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("corrupt existing object accepted: %v", err)
			}
			if got := string(readTestFile(t, path)); got != old {
				t.Fatalf("corrupt object overwritten: %q", got)
			}
			if strings.Contains(err.Error(), old) || strings.Contains(err.Error(), string(data)) {
				t.Fatalf("private bytes in integrity error: %v", err)
			}
		})
	}
}

func TestPrivatePublicationRemovesOnlyOwnTemp(t *testing.T) {
	t.Parallel()
	s := testScope(t, testRoot(t), testContext)
	if err := s.ensureObjects(); err != nil {
		t.Fatal(err)
	}
	data := []byte("new bytes")
	digest := ledger.Digest(sha256.Sum256(data))
	writeTestFile(t, filepath.Join(s.objects(), digest.String()), []byte("bad bytes"))
	unrelated := filepath.Join(s.objects(), ".private-unrelated")
	writeTestFile(t, unrelated, []byte("other call"))
	if _, err := s.publishObject(data); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("publication unexpectedly succeeded: %v", err)
	}
	entries, err := os.ReadDir(s.objects())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != digest.String() && entry.Name() != filepath.Base(unrelated) {
			t.Fatalf("failed publication left temp %s", entry.Name())
		}
	}
	if got := string(readTestFile(t, unrelated)); got != "other call" {
		t.Fatalf("removed another call's temp: %q", got)
	}
}

func TestPrivateObjectBoundsAndEmpty(t *testing.T) {
	t.Parallel()
	s := testScope(t, testRoot(t), "")
	if _, err := s.publishObject(make([]byte, maxObjectBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize publication: %v", err)
	}
	if _, err := os.Lstat(s.directory()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversize input created storage: %v", err)
	}
	for _, data := range [][]byte{nil, bytes.Repeat([]byte{0xff}, maxObjectBytes)} {
		digest, err := s.publishObject(data)
		if err != nil {
			t.Fatal(err)
		}
		if got := readTestFile(t, filepath.Join(s.objects(), digest.String())); !bytes.Equal(got, data) {
			t.Fatal("boundary bytes changed")
		}
		if len(data) == 0 && digest.String() != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Fatalf("empty object's digest = %s", digest)
		}
	}
}

func TestPrivateObjectsRejectNonRegularEntries(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			s := testScope(t, testRoot(t), "")
			if err := s.ensureObjects(); err != nil {
				t.Fatal(err)
			}
			data := []byte("private")
			digest := ledger.Digest(sha256.Sum256(data))
			path := filepath.Join(s.objects(), digest.String())
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(testRoot(t), "target")
				writeTestFile(t, target, data)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.publishObject(data); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %s object: %v", kind, err)
			}
		})
	}
}

func TestMarkdownMaterializationAndClassification(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ context, slot, name string }{
		{"", "instructions", "Agent.md"}, {"", "memory", "Memory.md"},
		{testContext, "memory", "Memory.md"}, {testContext, "handoff", "Handoff.md"},
	} {
		t.Run(row.context+row.slot, func(t *testing.T) {
			t.Parallel()
			s := testScope(t, testRoot(t), row.context)
			old, current := []byte("older\r\n"), []byte("current\n\x00")
			head := ledger.Digest(sha256.Sum256(current))
			prior := []ledger.Digest{ledger.Digest(sha256.Sum256(old))}
			for _, step := range []struct {
				data  []byte
				state materializationState
				kind  observationKind
			}{
				{nil, materializationStale, observationMissing},
				{old, materializationStale, observationRegular},
				{[]byte("foreign secret"), materializationForeign, observationRegular},
				{current, materializationCurrent, observationRegular},
			} {
				if step.data != nil {
					if err := s.materialize(row.slot, step.data); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(readTestFile(t, filepath.Join(s.directory(), row.name)), step.data) {
						t.Fatal("materialization changed bytes")
					}
				}
				got, err := s.classifyMaterialization(row.slot, head, prior)
				if err != nil || got.state != step.state || got.kind != step.kind {
					t.Fatalf("classification = %+v, %v; want %v/%v", got, err, step.state, step.kind)
				}
				if step.data != nil && got.digest != ledger.Digest(sha256.Sum256(step.data)) {
					t.Fatal("wrong observed digest")
				}
			}
		})
	}
}

func TestEmptyMarkdownIsCurrent(t *testing.T) {
	t.Parallel()
	s := testScope(t, testRoot(t), testContext)
	if err := s.materialize("memory", nil); err != nil {
		t.Fatal(err)
	}
	head := ledger.Digest(sha256.Sum256(nil))
	got, err := s.classifyMaterialization("memory", head, nil)
	if err != nil || got.state != materializationCurrent || got.kind != observationRegular || got.digest != head {
		t.Fatalf("empty Markdown mistaken for missing: %+v, %v", got, err)
	}
}

func TestMarkdownRefusesWrongKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			s := testScope(t, testRoot(t), testContext)
			if err := s.ensureObjects(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.directory(), "Memory.md")
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(testRoot(t), "target")
				writeTestFile(t, target, []byte("private target"))
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.classifyMaterialization("memory", ledger.Digest(sha256.Sum256(nil)), nil)
			if !errors.Is(err, ErrInvalid) || got.state != materializationForeign || got.kind != observationWrongKind {
				t.Fatalf("non-regular Markdown = %+v, %v", got, err)
			}
			if err := s.materialize("memory", []byte("new")); !errors.Is(err, ErrInvalid) {
				t.Fatalf("replaced non-regular Markdown: %v", err)
			}
		})
	}
}

func TestPrivateStorageLongPath(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	for len(root) <= 260 {
		root = filepath.Join(root, strings.Repeat("long", 10))
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	s := testScope(t, root, testContext)
	data := []byte("long path bytes")
	digest, err := s.publishObject(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readTestFile(t, filepath.Join(s.objects(), digest.String())), data) {
		t.Fatal("long path object changed")
	}
	if err := s.materialize("handoff", data); err != nil {
		t.Fatal(err)
	}
	got, err := s.classifyMaterialization("handoff", digest, nil)
	if err != nil || got.state != materializationCurrent {
		t.Fatalf("long path classification = %+v, %v", got, err)
	}
}

func TestMarkdownClassificationBoundsAndPrecedence(t *testing.T) {
	t.Parallel()
	s := testScope(t, testRoot(t), testContext)
	data := []byte("committed")
	head := ledger.Digest(sha256.Sum256(data))
	if err := s.materialize("memory", data); err != nil {
		t.Fatal(err)
	}
	got, err := s.classifyMaterialization("memory", head, []ledger.Digest{head})
	if err != nil || got.state != materializationCurrent {
		t.Fatalf("earlier occurrence overruled the head: %+v, %v", got, err)
	}
	oversize := bytes.Repeat([]byte{0xff}, maxObjectBytes+1)
	if err := s.materialize("memory", oversize); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize materialization: %v", err)
	}
	path := filepath.Join(s.directory(), "Memory.md")
	if !bytes.Equal(readTestFile(t, path), data) {
		t.Fatal("refused materialization changed the current file")
	}
	writeTestFile(t, path, oversize)
	got, err = s.classifyMaterialization("memory", head, nil)
	if !errors.Is(err, ErrTooLarge) || got.state != materializationForeign || got.kind != observationRegular {
		t.Fatalf("oversize foreign file: %+v, %v", got, err)
	}
}

func TestMarkdownScopeBoundaries(t *testing.T) {
	t.Parallel()
	for _, context := range []string{"", testContext} {
		t.Run(context, func(t *testing.T) {
			t.Parallel()
			s := testScope(t, testRoot(t), context)
			slots := []string{"../Memory.md", "Memory.md", "transcript", "continuation"}
			if context == "" {
				slots = append(slots, "handoff")
			} else {
				slots = append(slots, "instructions")
			}
			for _, slot := range slots {
				if err := s.materialize(slot, []byte("private")); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted Markdown slot %q: %v", slot, err)
				}
			}
			if _, err := os.Lstat(s.directory()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid slot created storage: %v", err)
			}
		})
	}
}

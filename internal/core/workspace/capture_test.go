package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type captureFixture struct {
	f      gitFixture
	p      Proposal
	s      captureScratch
	engine *captureEngine
	pool   *reservationPool
}

func newCaptureFixture(t *testing.T) *captureFixture {
	t.Helper()
	f, p, s := fixtureScratch(t)
	if err := f.git.ensureMarker(context.Background(), f.colony, p, "capture-test-colony"); err != nil {
		t.Fatal(err)
	}
	profile := CaptureProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20}
	return &captureFixture{f: f, p: p, s: captureScratch{Worktree: s, Inputs: p, Revision: 1, ColonyID: "capture-test-colony", InputsDir: filepath.Join(f.colony, "workspaces", "inputs", "1"), TmpDir: filepath.Join(f.colony, "workspaces", "00112233", "tmp")}, engine: newCaptureEngine(f.git, f.colony), pool: testReservationPool(t, &profile)}
}

func (f *captureFixture) capture(ctx context.Context, mode captureMode) (captureResult, error) {
	f.s.Inputs = f.p
	r, err := f.pool.reserve(f.s.Worktree.Dir, true)
	if err != nil {
		return captureResult{}, err
	}
	defer r.release()
	return f.engine.capture(ctx, f.s, f.s.Worktree.AnchorTree, r, mode)
}

func mustCapture(t *testing.T, f *captureFixture, mode captureMode) captureResult {
	t.Helper()
	got, err := f.capture(context.Background(), mode)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertRefusal(t *testing.T, err error, reason string) *RefusalError {
	t.Helper()
	var refusal *RefusalError
	if !errors.Is(err, ErrCaptureRefused) || !errors.As(err, &refusal) || refusal.Reason != reason {
		t.Fatalf("want refusal %s, got %v", reason, err)
	}
	return refusal
}

func copyScratchFiles(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// referenceTree returns the independently staged tree and its repository.
func referenceTree(t *testing.T, f *captureFixture) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "reference")
	gitCommand(t, f.f.env, f.f.root, nil, "clone", "--no-checkout", f.f.root, dir)
	n := f.p.Normalization
	for _, kv := range [][2]string{{"core.autocrlf", n.AutoCRLF}, {"core.eol", n.EOL}, {"core.filemode", n.FileMode}, {"core.ignorecase", n.IgnoreCase}, {"core.longpaths", n.LongPaths}, {"core.precomposeunicode", n.PrecomposeUnicode}, {"core.symlinks", n.Symlinks}, {"core.safecrlf", "false"}} {
		gitCommand(t, f.f.env, dir, nil, "config", kv[0], kv[1])
	}
	writeTestFile(t, filepath.Join(dir, ".git", "info", "exclude"), f.p.LocalExcludes.Bytes)
	writeTestFile(t, filepath.Join(dir, ".git", "info", "attributes"), f.p.LocalAttributes.Bytes)
	excluded := filepath.Join(t.TempDir(), "exclude")
	writeTestFile(t, excluded, f.p.GlobalExcludes.Bytes)
	gitCommand(t, f.f.env, dir, nil, "config", "core.excludesFile", excluded)
	attributes := append([]byte(nil), f.p.SystemAttributes.Bytes...)
	if len(attributes) > 0 && attributes[len(attributes)-1] != '\n' {
		attributes = append(attributes, '\n')
	}
	attributes = append(attributes, f.p.GlobalAttributes.Bytes...)
	attrPath := filepath.Join(t.TempDir(), "attributes")
	writeTestFile(t, attrPath, attributes)
	gitCommand(t, f.f.env, dir, nil, "config", "core.attributesFile", attrPath)
	gitCommand(t, f.f.env, dir, nil, "read-tree", "--end-of-options", f.s.Worktree.AnchorTree)
	copyScratchFiles(t, f.s.Worktree.Dir, dir)
	gitCommand(t, f.f.env, dir, nil, "add", "-A", "--")
	return strings.TrimSpace(string(gitCommand(t, f.f.env, dir, nil, "write-tree"))), dir
}

func TestCaptureEligibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *captureFixture)
	}{
		{"tracked edit", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt"), []byte("edited\n"))
		}},
		{"tracked deletion", func(t *testing.T, f *captureFixture) {
			if err := os.Remove(filepath.Join(f.s.Worktree.Dir, "base.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"new file", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("new\n"))
		}},
		{"captured ignore edit", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitignore"), []byte("new.txt\n"))
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("private\n"))
		}},
		{"pinned local exclude", func(t *testing.T, f *captureFixture) {
			f.p.LocalExcludes = HostFile{Present: true, Bytes: []byte("new.txt\n")}
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("private\n"))
		}},
		{"pinned global exclude", func(t *testing.T, f *captureFixture) {
			f.p.GlobalExcludes = HostFile{Present: true, Bytes: []byte("new.txt\n")}
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("private\n"))
		}},
		{"live exclusion ignored", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.p.CommonDir, "info", "exclude"), []byte("new.txt\n"))
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("eligible\n"))
		}},
		{"tracked ignored file", func(t *testing.T, f *captureFixture) {
			f.p.LocalExcludes = HostFile{Present: true, Bytes: []byte("base.txt\n")}
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt"), []byte("tracked edit\n"))
		}},
		{"tracked file in ignored directory", func(t *testing.T, f *captureFixture) {
			tree, commit := plumbingBaseline(t, f.f, []stageEntry{{Path: "base.txt", Mode: "100644"}, {Path: "build/tracked.txt", Mode: "100644"}})
			gitCommand(t, f.f.env, f.f.root, nil, "update-ref", "refs/heads/main", commit)
			f.s.Worktree.AnchorTree = tree
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitignore"), []byte("build/\n"))
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "build", "tracked.txt"), []byte("tracked edit\n"))
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "build", "ignored.txt"), []byte("scratch-local bytes\n"))
		}},
		{"ignore negation", func(t *testing.T, f *captureFixture) {
			f.p.GlobalExcludes = HostFile{Present: true, Bytes: []byte("new.txt\n")}
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitignore"), []byte("!new.txt\n"))
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "new.txt"), []byte("eligible\n"))
		}},
		{"tracked file becomes directory", func(t *testing.T, f *captureFixture) {
			if err := os.Remove(filepath.Join(f.s.Worktree.Dir, "base.txt")); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt", "child"), []byte("new\n"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCaptureFixture(t)
			tc.edit(t, f)
			want, _ := referenceTree(t, f)
			got := mustCapture(t, f, sealCapture)
			if got.Tree != want {
				t.Fatalf("tree %s want reference %s", got.Tree, want)
			}
		})
	}
}

func manifestRows(t *testing.T, result captureResult) []manifestEntry {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(result.Manifest))
	var header struct {
		Format       string
		Version      int
		ObjectFormat string `json:"object_format"`
	}
	if err := decoder.Decode(&header); err != nil {
		t.Fatal(err)
	}
	if header.Format != "dwarf-harness/capture-manifest" || header.Version != 1 {
		t.Fatalf("invalid manifest header: %#v", header)
	}
	var rows []manifestEntry
	for decoder.More() {
		var row manifestEntry
		if err := decoder.Decode(&row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if sha256.Sum256(result.Manifest) != result.ManifestSHA256 {
		t.Fatal("manifest digest mismatch")
	}
	return rows
}

func TestCaptureCanonicalContent(t *testing.T) {
	f := newCaptureFixture(t)
	raw := map[string][]byte{"text.txt": []byte("a\r\nb\r\n"), "raw.bin": []byte("a\r\nb\r\n"), "binary.dat": {0, 'a', '\r', '\n'}}
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitattributes"), []byte("*.bin -text\n"))
	for path, b := range raw {
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, path), b)
	}
	result := mustCapture(t, f, sealCapture)
	for _, row := range manifestRows(t, result) {
		b, ok := raw[row.Path]
		if !ok {
			continue
		}
		sum := sha256.Sum256(b)
		if row.Size != int64(len(b)) || row.SHA256 != fmt.Sprintf("%x", sum) {
			t.Fatalf("raw identity lost: %#v", row)
		}
		blob, err := f.f.git.readBlob(context.Background(), f.f.colony, f.p.CommonDir, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := b
		if row.Path == "text.txt" {
			want = []byte("a\nb\n")
		}
		if !bytes.Equal(blob, want) {
			t.Fatalf("canonical blob %s: %q", row.Path, blob)
		}
	}
}

func TestCaptureDeterministic(t *testing.T) {
	f := newCaptureFixture(t)
	a := mustCapture(t, f, observeCapture)
	b := mustCapture(t, f, observeCapture)
	if a.Tree != b.Tree || !bytes.Equal(a.Manifest, b.Manifest) || a.ManifestSHA256 != b.ManifestSHA256 {
		t.Fatal("unchanged scratch changed capture identity")
	}
}

func TestCaptureRequiresReservation(t *testing.T) {
	f := newCaptureFixture(t)
	before := snapshotCaptureSideEffects(t, f.f, f.p)
	for _, r := range []*reservation{nil, {}, {pool: f.pool, scratch: f.s.Worktree.Dir, bytes: f.pool.profile.MaxBytes}} {
		_, err := f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, sealCapture)
		assertReservationInvalid(t, err)
	}
	assertCaptureSideEffects(t, f.f, f.p, before)
	r, err := f.pool.reserve(f.s.Worktree.Dir, true)
	if err != nil {
		t.Fatal(err)
	}
	r.release()
	_, err = f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, sealCapture)
	assertReservationInvalid(t, err)
}

func TestIgnoredEntriesUnit(t *testing.T) {
	f := newCaptureFixture(t)
	f.p.LocalExcludes = HostFile{Present: true, Bytes: []byte("build/\none.tmp\n")}
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "build", "a"), []byte("a"))
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "build", "b"), []byte("b"))
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "one.tmp"), []byte("c"))
	got := mustCapture(t, f, observeCapture)
	if got.IgnoredEntries != 2 {
		t.Fatalf("ignored listing count: %d", got.IgnoredEntries)
	}
}

func TestObserveRetainsNothing(t *testing.T) {
	f := newCaptureFixture(t)
	before := snapshotCaptureSideEffects(t, f.f, f.p)
	observed := mustCapture(t, f, observeCapture)
	after := snapshotCaptureSideEffects(t, f.f, f.p)
	if observed.RetentionRef != "" || before.Refs != after.Refs {
		t.Fatal("observe retained a tree")
	}
	sealed := mustCapture(t, f, sealCapture)
	ref := string(gitCommand(t, f.f.env, f.f.root, nil, "for-each-ref", "--format=%(refname)%00%(objectname)%00", "--end-of-options", "refs/dwarf-harness/captures/"))
	want := sealed.RetentionRef + "\x00" + sealed.Tree + "\x00\n"
	if sealed.RetentionRef != "refs/dwarf-harness/captures/"+sealed.ID.String() || ref != want {
		t.Fatalf("seal retention: %q want %q", ref, want)
	}
}

func snapshotScratch(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = readTestFile(t, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertRefusalHasNoEffects(t *testing.T, f *captureFixture, before map[string][]byte, refs string) {
	t.Helper()
	if !reflect.DeepEqual(before, snapshotScratch(t, f.s.Worktree.Dir)) {
		t.Fatal("capture changed scratch bytes")
	}
	after := snapshotCaptureSideEffects(t, f.f, f.p)
	if after.Refs != refs {
		t.Fatal("refusal retained objects")
	}
	entries, err := os.ReadDir(f.s.TmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("capture index leaked: %v", entries)
	}
}

// Builds even non-materializable trees without ever checking them out.
func plumbingBaseline(t *testing.T, f gitFixture, entries []stageEntry) (string, string) {
	t.Helper()
	env := append(append([]string(nil), f.env...), "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"))
	gitCommand(t, env, f.root, nil, "read-tree", "--empty")
	for _, entry := range entries {
		oid := entry.OID
		if oid == "" {
			oid = strings.TrimSpace(string(gitCommand(t, env, f.root, []byte("blob\n"), "hash-object", "-w", "--stdin", "--no-filters")))
		}
		gitCommand(t, env, f.root, nil, "update-index", "--add", "--cacheinfo", entry.Mode+","+oid+","+entry.Path)
	}
	tree := strings.TrimSpace(string(gitCommand(t, env, f.root, nil, "write-tree")))
	commit := strings.TrimSpace(string(gitCommand(t, env, f.root, []byte("plumbing baseline\n"), "commit-tree", tree)))
	return tree, commit
}

func TestCaseCollidingBaselineRefusedBeforeMaterialization(t *testing.T) {
	for _, paths := range [][]string{{"A.txt", "a.txt"}, {"Dir/x", "dir/y"}} {
		t.Run(strings.Join(paths, "+"), func(t *testing.T) {
			entries := []stageEntry{{Path: paths[0], Mode: "100644"}, {Path: paths[1], Mode: "100644"}}
			f := newGitFixture(t, "sha1")
			_, commit := plumbingBaseline(t, f, entries)
			gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", commit)
			p, err := f.git.inspect(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.colony, "scratch", "11223344")
			_, err = f.git.createWorktree(context.Background(), f.colony, p, filepath.Join(f.colony, "workspaces", "inputs", "1"), dir, "collision")
			refusal := assertRefusal(t, err, "case_collision")
			for _, path := range paths {
				if !strings.Contains(refusal.Error(), path) {
					t.Fatalf("collision omitted %q: %v", path, err)
				}
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatalf("colliding worktree materialized: %v", err)
			}
			captured := newCaptureFixture(t)
			tree, _ := plumbingBaseline(t, captured.f, entries)
			captured.s.Worktree.AnchorTree = tree
			reads := 0
			captured.engine.afterRawRead = func(string) { reads++ }
			before := snapshotScratch(t, captured.s.Worktree.Dir)
			refs := snapshotCaptureSideEffects(t, captured.f, captured.p).Refs
			_, err = captured.capture(context.Background(), sealCapture)
			refusal = assertRefusal(t, err, "case_collision")
			for _, path := range paths {
				if !strings.Contains(refusal.Error(), path) {
					t.Fatalf("collision omitted %q: %v", path, err)
				}
			}
			if reads != 0 {
				t.Fatalf("read %d files before rejecting baseline", reads)
			}
			assertRefusalHasNoEffects(t, captured, before, refs)
		})
	}
}

func TestCaptureIgnoresScratchIndexAndHead(t *testing.T) {
	f := newCaptureFixture(t)
	dir := f.s.Worktree.Dir
	writeTestFile(t, filepath.Join(dir, "base.txt"), []byte("staged\n"))
	gitCommand(t, f.f.env, dir, nil, "add", "base.txt")
	gitCommand(t, f.f.env, dir, nil, "commit", "-m", "scratch head")
	writeTestFile(t, filepath.Join(dir, "other.txt"), []byte("staged other\n"))
	gitCommand(t, f.f.env, dir, nil, "add", "other.txt")
	gitCommand(t, f.f.env, dir, nil, "update-index", "--assume-unchanged", "base.txt")
	gitCommand(t, f.f.env, dir, nil, "update-index", "--skip-worktree", "other.txt")
	writeTestFile(t, filepath.Join(dir, "base.txt"), []byte("working base\n"))
	writeTestFile(t, filepath.Join(dir, "other.txt"), []byte("working other\n"))
	want, _ := referenceTree(t, f)
	got := mustCapture(t, f, sealCapture)
	if got.Tree != want {
		t.Fatalf("scratch index or HEAD influenced capture: %s want %s", got.Tree, want)
	}
}

func TestCaptureReadsEveryFile(t *testing.T) {
	f := newCaptureFixture(t)
	path := filepath.Join(f.s.Worktree.Dir, "base.txt")
	first := mustCapture(t, f, observeCapture)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	b := readTestFile(t, path)
	b[0] = 'X'
	writeTestFile(t, path, b)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	want, _ := referenceTree(t, f)
	second := mustCapture(t, f, sealCapture)
	if first.Tree == second.Tree || second.Tree != want {
		t.Fatal("same-stat edit was not read")
	}
}

func TestCaptureRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		edit         func(*testing.T, *captureFixture)
		beforeReads  bool
	}{
		{"nested repository", "unsupported_kind", func(t *testing.T, f *captureFixture) {
			gitCommand(t, f.f.env, f.s.Worktree.Dir, nil, "init", filepath.Join(f.s.Worktree.Dir, "nested"))
		}, true},
		{"baseline gitlink", "unsupported_kind", func(t *testing.T, f *captureFixture) {
			tree, _ := plumbingBaseline(t, f.f, []stageEntry{{Path: "module", Mode: "160000", OID: f.s.Worktree.BaseCommit}})
			f.s.Worktree.AnchorTree = tree
		}, true},
		{"baseline symlink", "unsupported_kind", func(t *testing.T, f *captureFixture) {
			tree, _ := plumbingBaseline(t, f.f, []stageEntry{{Path: "link", Mode: "120000"}})
			f.s.Worktree.AnchorTree = tree
		}, true},
		{"filter", "unsupported_filter", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitattributes"), []byte("* filter=lfs\n"))
		}, false},
		{"empty filter", "unsupported_filter", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitattributes"), []byte("* filter=\n"))
		}, false},
		{"encoding", "unsupported_conversion", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitattributes"), []byte("* working-tree-encoding=UTF-16LE\n"))
		}, false},
		{"ident", "unsupported_conversion", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, ".gitattributes"), []byte("* ident\n"))
		}, false},
		{"mixed endings", "lossy_normalization", func(t *testing.T, f *captureFixture) {
			writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "mixed.txt"), []byte("a\r\nb\n"))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCaptureFixture(t)
			tc.edit(t, f)
			reads := 0
			f.engine.afterRawRead = func(string) { reads++ }
			before := snapshotScratch(t, f.s.Worktree.Dir)
			refs := snapshotCaptureSideEffects(t, f.f, f.p).Refs
			_, err := f.capture(context.Background(), sealCapture)
			assertRefusal(t, err, tc.reason)
			if tc.beforeReads && reads != 0 {
				t.Fatalf("unsafe raw reads before refusal: %d", reads)
			}
			assertRefusalHasNoEffects(t, f, before, refs)
		})
	}
}

func TestRepeatedCarriageReturnUsesReferenceGit(t *testing.T) {
	f := newCaptureFixture(t)
	raw := []byte("\r\r\n")
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "repeated.txt"), raw)
	reference, referenceDir := referenceTree(t, f)
	blob := gitCommand(t, f.f.env, referenceDir, nil, "show", ":repeated.txt")
	matches := bytes.Equal(raw, blob) || bytes.Equal(raw, referenceCRLF(blob))
	got, err := f.capture(context.Background(), sealCapture)
	if matches {
		if err != nil || got.Tree != reference {
			t.Fatalf("reference preserves bytes: tree=%s want=%s err=%v", got.Tree, reference, err)
		}
	} else {
		assertRefusal(t, err, "lossy_normalization")
	}
}

func TestCaptureCaps(t *testing.T) {
	t.Run("files", func(t *testing.T) {
		f := newCaptureFixture(t)
		f.pool = testReservationPool(t, &CaptureProfile{MaxFiles: 1, MaxBytes: 100, BytesInFlight: 100})
		mustCapture(t, f, observeCapture)
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "extra"), []byte("x"))
		reads := 0
		f.engine.afterRawRead = func(string) { reads++ }
		_, err := f.capture(context.Background(), sealCapture)
		assertRefusal(t, err, "file_cap")
		if reads != 0 {
			t.Fatalf("file cap read %d files", reads)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		f := newCaptureFixture(t)
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt"), []byte("ab"))
		f.pool = testReservationPool(t, &CaptureProfile{MaxFiles: 2, MaxBytes: 2, BytesInFlight: 2})
		mustCapture(t, f, observeCapture)
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt"), []byte("abc"))
		reads := 0
		f.engine.afterRawRead = func(string) { reads++ }
		_, err := f.capture(context.Background(), sealCapture)
		assertRefusal(t, err, "byte_cap")
		if reads != 0 {
			t.Fatalf("byte cap read %d files", reads)
		}
	})
	t.Run("growth", func(t *testing.T) {
		f := newCaptureFixture(t)
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "zzz"), []byte("x"))
		f.pool = testReservationPool(t, &CaptureProfile{MaxFiles: 2, MaxBytes: 20, BytesInFlight: 20})
		f.engine.afterRawRead = func(path string) {
			if path == "base.txt" {
				writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "zzz"), bytes.Repeat([]byte("x"), 30))
			}
		}
		_, err := f.capture(context.Background(), sealCapture)
		var refused *RefusalError
		if !errors.As(err, &refused) || (refused.Reason != "byte_cap" && refused.Reason != "scratch_changed") {
			t.Fatalf("growth was not refused: %v", err)
		}
	})
	t.Run("manifest", func(t *testing.T) {
		f := newCaptureFixture(t)
		f.engine.manifestCap = 32
		_, err := f.capture(context.Background(), sealCapture)
		assertRefusal(t, err, "manifest_cap")
	})
}

func TestCaptureDetectsChangeDuringCapture(t *testing.T) {
	for _, change := range []string{"size", "metadata only"} {
		t.Run(change, func(t *testing.T) {
			f := newCaptureFixture(t)
			f.engine.afterRawRead = func(path string) {
				if path != "base.txt" {
					return
				}
				full := filepath.Join(f.s.Worktree.Dir, path)
				if change == "size" {
					writeTestFile(t, full, []byte("a different size before Git reads\n"))
					return
				}
				info, err := os.Stat(full)
				if err != nil {
					t.Fatal(err)
				}
				changed := info.ModTime().Add(2 * time.Second)
				if err := os.Chtimes(full, changed, changed); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotCaptureSideEffects(t, f.f, f.p)
			_, err := f.capture(context.Background(), sealCapture)
			assertRefusal(t, err, "scratch_changed")
			if after := snapshotCaptureSideEffects(t, f.f, f.p); before.Refs != after.Refs {
				t.Fatal("changing scratch was retained")
			}
		})
	}
}

func TestSealIsImmutable(t *testing.T) {
	f := newCaptureFixture(t)
	dir := f.s.Worktree.Dir
	writeTestFile(t, filepath.Join(dir, "sealed-only.txt"), []byte("only the capture retains this blob\n"))
	sealed := mustCapture(t, f, sealCapture)
	listing := gitCommand(t, f.f.env, f.f.root, nil, "ls-tree", "-r", "-z", sealed.Tree)
	blobs := map[string][]byte{}
	for _, row := range manifestRows(t, sealed) {
		b, err := f.f.git.readBlob(context.Background(), f.f.colony, f.p.CommonDir, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		blobs[row.ID] = b
	}
	if err := os.Remove(filepath.Join(dir, "sealed-only.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "base.txt"), []byte("edited\n"))
	writeTestFile(t, filepath.Join(dir, "new.txt"), []byte("added\n"))
	writeTestFile(t, filepath.Join(dir, ".gitignore"), []byte("new.txt\n"))
	writeTestFile(t, filepath.Join(dir, ".gitattributes"), []byte("* -text\n"))
	gitCommand(t, f.f.env, dir, nil, "add", "-A")
	gitCommand(t, f.f.env, dir, nil, "rm", "--cached", "base.txt")
	gitCommand(t, f.f.env, f.f.root, nil, "config", "core.autocrlf", "false")
	writeTestFile(t, filepath.Join(f.p.CommonDir, "info", "exclude"), []byte("*\n"))
	global := filepath.Join(t.TempDir(), "global-exclude")
	writeTestFile(t, global, []byte("*\n"))
	gitCommand(t, f.f.env, f.f.root, nil, "config", "--global", "core.excludesFile", global)
	writeTestFile(t, global, []byte("**\n"))
	gitCommand(t, f.f.env, f.f.root, nil, "gc", "--prune=now")
	gitCommand(t, f.f.env, dir, nil, "gc", "--prune=now")
	after := gitCommand(t, f.f.env, f.f.root, nil, "ls-tree", "-r", "-z", sealed.Tree)
	if !bytes.Equal(listing, after) {
		t.Fatal("sealed tree changed")
	}
	var ids []string
	for id := range blobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b, err := f.f.git.readBlob(context.Background(), f.f.colony, f.p.CommonDir, id)
		if err != nil || !bytes.Equal(b, blobs[id]) {
			t.Fatalf("sealed blob lost after GC: %s %v", id, err)
		}
	}
	ref, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, sealed.RetentionRef, len(sealed.Tree))
	if err != nil || ref != sealed.Tree {
		t.Fatalf("retention changed: %s %v", ref, err)
	}
}

func TestCaptureMaximumByteProfile(t *testing.T) {
	f := newCaptureFixture(t)
	f.pool = testReservationPool(t, &CaptureProfile{MaxFiles: 100, MaxBytes: 1<<63 - 1, BytesInFlight: 1<<63 - 1})
	raw := readTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt"))
	got := mustCapture(t, f, observeCapture)
	if got.RawBytes != int64(len(raw)) {
		t.Fatalf("byte allowance overflowed: %d", got.RawBytes)
	}
}

func TestBlobBatchStreaming(t *testing.T) {
	f := newGitFixture(t, "sha1")
	bodies := [][]byte{{}, []byte("a\r\nb\n\x00binary\n")}
	var ids []string
	want := map[string]blobHashes{}
	for _, body := range bodies {
		id := strings.TrimSpace(string(gitCommand(t, f.env, f.root, body, "hash-object", "-w", "--stdin", "--no-filters")))
		ids = append(ids, id)
		want[id] = blobHashes{raw: sha256.Sum256(body), crlf: sha256.Sum256(referenceCRLF(body))}
	}
	batch := gitCommand(t, f.env, f.root, []byte(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch")
	for split := range len(batch) + 1 {
		h := newBlobBatchHasher(ids, 40)
		_, _ = h.Write(batch[:split])
		_, _ = h.Write(batch[split:])
		if err := h.finish(); err != nil || !reflect.DeepEqual(h.sums, want) {
			t.Fatalf("split %d: hashes=%v err=%v", split, h.sums, err)
		}
	}
	truncated := newBlobBatchHasher(ids, 40)
	_, _ = truncated.Write(batch[:len(batch)-1])
	if !errors.Is(truncated.finish(), ErrGit) {
		t.Fatal("truncated batch accepted")
	}
}

func TestCaptureUnavailable(t *testing.T) {
	for _, reason := range []string{"no_capacity_profile", "git_unavailable", "inputs_stale", "workspace_unverified", "scratch_missing"} {
		t.Run(reason, func(t *testing.T) {
			f := newCaptureFixture(t)
			switch reason {
			case "no_capacity_profile":
				f.pool = testReservationPool(t, nil)
			case "git_unavailable":
				t.Setenv("PATH", t.TempDir())
				f.engine.git = newGitRunner(func() []string { return f.f.env })
			case "inputs_stale":
				f.p.GitVersion = "2.42.0-pinned-other"
			case "workspace_unverified":
				f.s.ColonyID = "another-colony"
			case "scratch_missing":
				if err := os.RemoveAll(f.s.Worktree.Dir); err != nil {
					t.Fatal(err)
				}
			}
			result, err := f.capture(context.Background(), sealCapture)
			var unavailable *UnavailableError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) || unavailable.Reason != reason || result.Tree != "" {
				t.Fatalf("precondition %s: result=%#v err=%v", reason, result, err)
			}
			if reason == "inputs_stale" && (!strings.Contains(unavailable.Detail, f.p.GitVersion) || !strings.Contains(unavailable.Detail, f.f.git.version)) {
				t.Fatal("staleness omitted pinned or running Git")
			}
		})
	}
}

func TestReservationOnePerPair(t *testing.T) {
	f := newCaptureFixture(t)
	r, err := f.pool.reserve(f.s.Worktree.Dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.release()
	other := f.s
	other.Worktree.Dir = filepath.Join(t.TempDir(), "other")
	_, err = f.engine.capture(context.Background(), other, other.Worktree.AnchorTree, r, sealCapture)
	assertReservationInvalid(t, err)
	var attempted atomic.Bool
	f.engine.afterRawRead = func(string) {
		if !attempted.CompareAndSwap(false, true) {
			return
		}
		done := make(chan error, 1)
		go func() {
			_, err := f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, observeCapture)
			done <- err
		}()
		assertReservationInvalid(t, <-done)
	}
	if _, err := f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, sealCapture); err != nil {
		t.Fatalf("wrong-scratch rejection poisoned the real holder: %v", err)
	}
	before := snapshotCaptureSideEffects(t, f.f, f.p)
	_, err = f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, sealCapture)
	assertReservationInvalid(t, err)
	assertCaptureSideEffects(t, f.f, f.p, before)
}

func TestCaptureSHA256Repository(t *testing.T) {
	f := newGitFixture(t, "sha256")
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.Normalization.AutoCRLF = "true"
	inputs := filepath.Join(f.colony, "workspaces", "inputs", "1")
	dir := filepath.Join(f.colony, "scratch", "12345678")
	s, err := f.git.createWorktree(context.Background(), f.colony, p, inputs, dir, "sha256-capture")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.git.ensureMarker(context.Background(), f.colony, p, "sha256-colony"); err != nil {
		t.Fatal(err)
	}
	profile := ProvisionalCaptureProfile()
	fixture := &captureFixture{f: f, p: p, s: captureScratch{Worktree: s, Inputs: p, Revision: 1, InputsDir: inputs, TmpDir: filepath.Join(f.colony, "workspaces", "00112233", "tmp"), ColonyID: "sha256-colony"}, engine: newCaptureEngine(f.git, f.colony), pool: testReservationPool(t, &profile)}
	writeTestFile(t, filepath.Join(dir, "new.txt"), []byte("sha256 content\r\n"))
	want, _ := referenceTree(t, fixture)
	result := mustCapture(t, fixture, sealCapture)
	if result.Tree != want {
		t.Fatalf("SHA-256 candidate %s want %s", result.Tree, want)
	}
	for _, row := range manifestRows(t, result) {
		if !validOID(row.ID, 64) {
			t.Fatalf("SHA-256 blob truncated: %s", row.ID)
		}
	}
}

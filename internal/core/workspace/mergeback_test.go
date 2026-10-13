package workspace

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"
)

type materializeHistory struct {
	f                                gitFixture
	old, target, oldTree, targetTree string
	files                            map[string]string
}

func newMaterializeHistory(t *testing.T) materializeHistory {
	t.Helper()
	f := newGitFixture(t, "sha1")
	oldTree := integrationTree(t, f, map[string]string{
		".gitignore":  "deleted.txt\nignored/\n",
		"deleted.txt": "delete me\n", "modified.txt": "old\n", "keep.txt": "keep\n",
		"file-to-dir": "old file\n", "dir-to-file/tracked": "old child\n",
	})
	files := map[string]string{
		".gitignore": "deleted.txt\nignored/\n",
		"added.txt":  "added\n", "modified.txt": "new\n", "keep.txt": "keep\n",
		"file-to-dir/child": "new child\n", "dir-to-file": "new file\n",
	}
	targetTree := integrationTree(t, f, files)
	old := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-m", "parent", oldTree)))
	target := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-p", old, "-m", "target", targetTree)))
	gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", old)
	gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/target", target)
	return materializeHistory{f: f, old: old, target: target, oldTree: oldTree, targetTree: targetTree, files: files}
}

func (h materializeHistory) fixture(t *testing.T, crlf bool) *captureFixture {
	t.Helper()
	f := gitFixture{root: filepath.Join(t.TempDir(), "repo"), colony: t.TempDir(), env: h.f.env}
	gitCommand(t, f.env, h.f.root, nil, "clone", "--no-checkout", "-b", "main", h.f.root, f.root)
	gitCommand(t, f.env, f.root, nil, "checkout", "--detach")
	if err := prepareGitLayout(f.colony); err != nil {
		t.Fatal(err)
	}
	f.git = newGitRunner(func() []string { return f.env })
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.Normalization.AutoCRLF = "false"
	if crlf {
		p.Normalization.AutoCRLF = "true"
	}
	inputs := filepath.Join(f.colony, "workspaces", "inputs", "1")
	wt, err := f.git.createWorktree(context.Background(), f.colony, p, inputs, filepath.Join(f.colony, "scratch", "12345678"), "materialize-test", h.old)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.git.ensureMarker(context.Background(), f.colony, p, "materialize-colony"); err != nil {
		t.Fatal(err)
	}
	profile := CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 1, GenerationWindow: 1}
	return &captureFixture{f: f, p: p, s: captureScratch{Worktree: wt, Inputs: p, Revision: 1, ColonyID: "materialize-colony", InputsDir: inputs, TmpDir: filepath.Join(f.colony, "workspaces", "tmp")}, engine: newCaptureEngine(f.git, f.colony), pool: testReservationPool(t, &profile)}
}

func materializeAdmin(t *testing.T, f *captureFixture) string {
	t.Helper()
	return strings.TrimSpace(string(gitCommand(t, f.f.env, f.s.Worktree.Dir, nil, "rev-parse", "--absolute-git-dir")))
}

func materializeGuard(t *testing.T, f *captureFixture) *reservation {
	t.Helper()
	guard, err := f.pool.beginSoleWrite(context.Background(), f.s.Worktree.Dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(guard.release)
	return guard
}

func assertMaterialized(t *testing.T, f *captureFixture, h materializeHistory, crlf bool) {
	t.Helper()
	files := snapshotScratch(t, f.s.Worktree.Dir)
	delete(files, ".git")
	want := map[string][]byte{}
	for path, text := range h.files {
		if crlf {
			text = strings.ReplaceAll(text, "\n", "\r\n")
		}
		want[filepath.FromSlash(path)] = []byte(text)
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("materialized files: %q want %q", files, want)
	}
	// Copy the installed real index: reference Git must not refresh its stat data.
	index := filepath.Join(t.TempDir(), "index")
	writeTestFile(t, index, readTestFile(t, filepath.Join(materializeAdmin(t, f), "index")))
	env := append(append([]string(nil), f.f.env...), "GIT_INDEX_FILE="+index)
	got := strings.TrimSpace(string(gitCommand(t, env, f.s.Worktree.Dir, nil, "write-tree")))
	branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, len(h.old))
	if err != nil || got != h.targetTree || branch != h.target || fixtureMain(t, f.f) != h.old {
		t.Fatalf("index/ref/main: tree=%s branch=%s err=%v", got, branch, err)
	}
	if _, err := os.Lstat(filepath.Join(materializeAdmin(t, f), "index.lock")); !os.IsNotExist(err) {
		t.Fatalf("owned lock retained: %v", err)
	}
}

func TestMaterializeTwoTreeCheckout(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	for _, poisonedIndex := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "untrusted index"}[poisonedIndex], func(t *testing.T) {
			t.Parallel()
			f := h.fixture(t, false)
			if poisonedIndex {
				gitCommand(t, f.f.env, f.s.Worktree.Dir, nil, "read-tree", "--empty")
			}
			if err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, uuid.NewV7(), materializeGuard(t, f)); err != nil {
				t.Fatal(err)
			}
			assertMaterialized(t, f, h, false)
		})
	}
}

func TestMaterializeRefusesObstruction(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	for _, tc := range []struct {
		name, path string
		ignored    bool
	}{
		{"untracked addition", "added.txt", false},
		{"ignored addition", "added.txt", true},
		{"ignored directory replacement", "dir-to-file/precious", true},
		{"ignored empty directory", "dir-to-file/empty/", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := h.fixture(t, false)
			if tc.ignored {
				f.s.Inputs.LocalExcludes = HostFile{Present: true, Bytes: []byte(tc.path + "\n")}
			}
			path := filepath.Join(f.s.Worktree.Dir, filepath.FromSlash(tc.path))
			if strings.HasSuffix(tc.path, "/") {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTestFile(t, path, []byte("precious\n"))
			}
			before := snapshotScratch(t, f.s.Worktree.Dir)
			admin := materializeAdmin(t, f)
			index := readTestFile(t, filepath.Join(admin, "index"))
			guard := materializeGuard(t, f)
			id := uuid.NewV7()
			err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, id, guard)
			refusal := assertRefusal(t, err, "obstructed_path")
			if refusal.Path != strings.TrimSuffix(tc.path, "/") {
				t.Fatalf("obstruction path: %q", refusal.Path)
			}
			if !reflect.DeepEqual(before, snapshotScratch(t, f.s.Worktree.Dir)) || !bytes.Equal(index, readTestFile(t, filepath.Join(admin, "index"))) {
				t.Fatal("obstruction changed files or real index")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("obstruction disappeared: %v", err)
			}
			branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, len(h.old))
			if err != nil || branch != h.old {
				t.Fatalf("obstruction moved branch: %s %v", branch, err)
			}
			entries, err := os.ReadDir(f.s.TmpDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("obstruction retained a private index: %v %v", entries, err)
			}
		})
	}
}

func TestMaterializeVerifiesBeforeRef(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	f := h.fixture(t, false)
	guard := materializeGuard(t, f)
	index := readTestFile(t, filepath.Join(materializeAdmin(t, f), "index"))
	f.engine.afterMergeBackFiles = func() {
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "deleted.txt"), []byte("delete me\n"))
	}
	err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, uuid.NewV7(), guard)
	assertRefusal(t, err, "materialization_mismatch")
	branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, len(h.old))
	if err != nil || branch != h.old || !bytes.Equal(index, readTestFile(t, filepath.Join(materializeAdmin(t, f), "index"))) {
		t.Fatalf("verification failure installed index or moved ref: %s %v", branch, err)
	}
	if string(readTestFile(t, filepath.Join(f.s.Worktree.Dir, "deleted.txt"))) != "delete me\n" {
		t.Fatal("failed verification destroyed leftover")
	}
}

func TestIndexInstallOwnedLock(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	for _, foreign := range []bool{true, false} {
		t.Run(map[bool]string{true: "foreign", false: "owned"}[foreign], func(t *testing.T) {
			t.Parallel()
			f := h.fixture(t, false)
			admin := materializeAdmin(t, f)
			index := filepath.Join(f.s.TmpDir, "prepared.idx")
			if err := materializeInputs(f.f.colony, f.s.InputsDir, f.p); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(f.s.TmpDir, 0o700); err != nil {
				t.Fatal(err)
			}
			env := captureEnvironment(f.f.env, f.f.colony, f.s.InputsDir, f.s.Worktree.Dir, index, f.p.CommonDir)
			gitCommand(t, env, f.s.Worktree.Dir, nil, "read-tree", h.targetTree)
			before := readTestFile(t, filepath.Join(admin, "index"))
			private := readTestFile(t, index)
			lock := filepath.Join(admin, "index.lock")
			id := uuid.NewV7()
			if foreign {
				writeTestFile(t, lock, []byte("held by someone else\n"))
			}
			f.engine.afterMergeBackIndex = func() {
				if got := string(readTestFile(t, lock)); got != "dwarf-harness scratch-index "+id.String()+"\n" {
					t.Fatalf("wrong owned marker: %q", got)
				}
			}
			// Git reports forward-slash paths even on Windows.
			err := f.engine.installScratchIndex(context.Background(), f.s, filepath.ToSlash(admin), index, h.targetTree, id)
			if foreign {
				assertBusy(t, err, "scratch_busy")
				if !bytes.Equal(before, readTestFile(t, filepath.Join(admin, "index"))) || !bytes.Equal(private, readTestFile(t, index)) || string(readTestFile(t, lock)) != "held by someone else\n" {
					t.Fatal("foreign lock was bypassed or changed")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(lock); !os.IsNotExist(err) {
					t.Fatalf("owned lock retained: %v", err)
				}
				if _, err := os.Lstat(index); !os.IsNotExist(err) {
					t.Fatalf("private index was copied instead of renamed: %v", err)
				}
			}
			branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, len(h.old))
			if err != nil || branch != h.old {
				t.Fatalf("index installation moved branch: %s %v", branch, err)
			}
		})
	}
}

// Avoid Git's racy-stat fallback without sleeping or testing a duration.
func ageMaterializeFiles(t *testing.T, root string) {
	t.Helper()
	past := time.Unix(1_000_000_000, 0)
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() != ".git" {
			return os.Chtimes(path, past, past)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func observedMaterializeTree(t *testing.T, f *captureFixture, h materializeHistory, guard *reservation) string {
	t.Helper()
	names, err := f.engine.treeNames(context.Background(), f.s, h.oldTree)
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.engine.treeNames(context.Background(), f.s, h.targetTree)
	if err != nil {
		t.Fatal(err)
	}
	s := f.s
	s.TrustedNames = append(names, target...)
	token, err := f.pool.reserveCaptureUnder(guard)
	if err != nil {
		t.Fatal(err)
	}
	defer token.release()
	got, err := f.engine.capture(context.Background(), s, h.targetTree, token, observeCapture)
	if err != nil {
		t.Fatal(err)
	}
	return got.Tree
}

func TestMaterializeRetryPreservesThirdValue(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	f := h.fixture(t, false)
	guard := materializeGuard(t, f)
	// O is an already-observed partial result. A later edit is not permission
	// to reset it: two-tree checkout must refuse rather than overwrite it.
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "added.txt"), []byte("added\n"))
	actual := observedMaterializeTree(t, f, h, guard)
	var before map[string][]byte
	f.engine.beforeMergeBackFiles = func() {
		writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "modified.txt"), []byte("third value\n"))
		before = snapshotScratch(t, f.s.Worktree.Dir)
	}
	err := f.engine.materialize(context.Background(), f.s, actual, h.targetTree, h.old, h.target, uuid.NewV7(), guard)
	var gitErr *GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("dirty retry was not refused: %v", err)
	}
	if !reflect.DeepEqual(before, snapshotScratch(t, f.s.Worktree.Dir)) {
		t.Fatal("retry overwrote intervening work")
	}
}

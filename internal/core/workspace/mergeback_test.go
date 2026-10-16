package workspace

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
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
	gitCommand(t, f.env, h.f.root, nil, "clone", "--no-hardlinks", "--no-checkout", "-b", "main", h.f.root, f.root)
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

func observedTemporary(t *testing.T, f *serviceFixture, task Scratch, files map[string]string) (Scratch, Observation) {
	t.Helper()
	binding := testWriteBinding(task)
	binding.ContextID = uuid.NewV7()
	writer := mustWriter(t, f, binding)
	source := writer.Scratch()
	for path, content := range files {
		writeTestFile(t, filepath.Join(source.Dir, path), []byte(content))
	}
	return source, mustObserve(t, writer)
}

func TestMergeBackFastForwardAndExplicitBase(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, mode := range []string{"fast_forward", "disjoint", "same_tree_parent", "multiple_observations"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			ctx := context.Background()
			task := mustScratch(t, f, uuid.NewV7())
			writer := mustWriter(t, f, testWriteBinding(task))
			writeTestFile(t, filepath.Join(task.Dir, "fork.txt"), []byte("private fork\n"))
			fork := mustObserve(t, writer)
			source, observed := observedTemporary(t, f, task, map[string]string{"source.txt": "source\n"})
			if mode == "multiple_observations" {
				binding := testWriteBinding(task)
				binding.ContextID, binding.ResumeScratchID = source.ContextID, source.ID
				writer = mustWriter(t, f, binding)
				writeTestFile(t, filepath.Join(source.Dir, "second.txt"), []byte("second\n"))
				observed = mustObserve(t, writer)
			}
			parent := fork
			if mode != "fast_forward" {
				writer = mustWriter(t, f, testWriteBinding(task))
				if mode != "same_tree_parent" {
					writeTestFile(t, filepath.Join(task.Dir, "parent.txt"), []byte("parent\n"))
				}
				parent = mustObserve(t, writer)
			}
			main := fixtureMain(t, f.gitFixture)
			result, err := f.svc.MergeBack(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := observed.Tree
			if mode != "fast_forward" {
				out := gitCommand(t, f.env, f.root, nil, "merge-tree", "--write-tree", "--merge-base="+fork.Commit, parent.Commit, observed.Commit)
				expected = strings.TrimSpace(string(out))
				if result.Outcome != ledger.MergeBackMerged || result.TargetCommit == observed.Commit {
					t.Fatalf("moved parent treated as fast-forward: %+v", result)
				}
				parents := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-list", "--parents", "-n", "1", result.TargetCommit)))
				if parents != result.TargetCommit+" "+parent.Commit {
					t.Fatalf("wrong merge parent: %s", parents)
				}
			} else if result.Outcome != ledger.MergeBackFastForward || result.TargetCommit != observed.Commit {
				t.Fatalf("fast-forward invented a commit: %+v", result)
			}
			indexTree := strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "write-tree")))
			branch := strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "rev-parse", "HEAD")))
			if result.TargetTree != expected || indexTree != expected || branch != result.TargetCommit || fixtureMain(t, f.gitFixture) != main {
				t.Fatalf("merge not landed exactly: result=%+v index=%s branch=%s expected=%s", result, indexTree, branch, expected)
			}
			if string(readTestFile(t, filepath.Join(task.Dir, "fork.txt"))) != "private fork\n" || string(readTestFile(t, filepath.Join(task.Dir, "source.txt"))) != "source\n" {
				t.Fatal("merge lost private fork or source bytes")
			}
			if _, err := os.Lstat(source.Dir); !os.IsNotExist(err) {
				t.Fatalf("applied source not disposed: %v", err)
			}
			durable := f.store.Durable()
			repeated, err := f.svc.MergeBack(ctx, source.ID)
			if err != nil || repeated != result || f.store.Durable() != durable {
				t.Fatalf("applied merge is not idempotent: %+v %v", repeated, err)
			}
			if _, err := f.svc.CloseScratch(ctx, ownerClosure(task)); !errors.Is(err, ErrUnrepresentedEdits) {
				t.Fatalf("private merge represented canonical delivery: %v", err)
			}
		})
	}
}

func TestScratchAdminRefusesForeignWorktree(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	for _, mode := range []string{"foreign_head", "git_directory", "foreign_backpointer"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := h.fixture(t, false)
			admin := materializeAdmin(t, f)
			index := filepath.Join(f.s.TmpDir, "prepared.idx")
			if err := os.MkdirAll(f.s.TmpDir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, index, []byte("must not be installed\n"))
			before := readTestFile(t, filepath.Join(admin, "index"))
			switch mode {
			case "foreign_head":
				writeTestFile(t, filepath.Join(admin, "HEAD"), []byte("ref: refs/heads/main\n"))
			case "git_directory":
				if err := os.Remove(filepath.Join(f.s.Worktree.Dir, ".git")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(f.s.Worktree.Dir, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "foreign_backpointer":
				foreign := filepath.Join(t.TempDir(), ".git")
				writeTestFile(t, foreign, []byte("foreign\n"))
				writeTestFile(t, filepath.Join(admin, "gitdir"), []byte(filepath.ToSlash(foreign)+"\n"))
			}
			err := f.engine.installScratchIndex(context.Background(), f.s, admin, index, h.targetTree, uuid.NewV7())
			assertUnavailable(t, err, "workspace_unverified")
			if !bytes.Equal(before, readTestFile(t, filepath.Join(admin, "index"))) || string(readTestFile(t, index)) != "must not be installed\n" {
				t.Fatal("unverified admin received an index")
			}
			if _, err := os.Lstat(filepath.Join(admin, "index.lock")); !os.IsNotExist(err) {
				t.Fatalf("unverified admin received a lock: %v", err)
			}
			branch, err := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, 40)
			if err != nil || branch != h.old {
				t.Fatalf("unverified admin moved branch: %s %v", branch, err)
			}
		})
	}
}

func TestMaterializeTipMismatchUnavailable(t *testing.T) {
	t.Parallel()
	h := newMaterializeHistory(t)
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after_index"}[late], func(t *testing.T) {
			t.Parallel()
			f := h.fixture(t, false)
			third := strings.TrimSpace(string(gitCommand(t, f.f.env, f.f.root, nil, "commit-tree", "-m", "foreign", h.oldTree)))
			move := func() {
				gitCommand(t, f.f.env, f.f.root, nil, "update-ref", f.s.Worktree.Branch, third)
			}
			if late {
				f.engine.afterMergeBackIndex = move
			} else {
				move()
			}
			err := f.engine.materialize(context.Background(), f.s, h.oldTree, h.targetTree, h.old, h.target, uuid.NewV7(), materializeGuard(t, f))
			assertUnavailable(t, err, "scratch_tip_mismatch")
			branch, readErr := f.f.git.readRef(context.Background(), f.f.colony, f.p.CommonDir, f.s.Worktree.Branch, 40)
			if readErr != nil || branch != third {
				t.Fatalf("foreign branch overwritten: %s %v", branch, readErr)
			}
		})
	}
}

func TestMergeBackConflictsRemainWork(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, mode := range []string{"content", "advanced_fork", "case", "prefix", "literal_markers", "pin_rollback"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			task := mustScratch(t, f, uuid.NewV7())
			if mode == "advanced_fork" {
				writer := mustWriter(t, f, testWriteBinding(task))
				writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("private fork\n"))
				mustObserve(t, writer)
			}
			sourcePath, parentPath := "base.txt", "base.txt"
			switch mode {
			case "case":
				sourcePath, parentPath = "Case.txt", "case.txt"
			case "prefix":
				sourcePath, parentPath = "Folder/a", "folder/b"
			case "literal_markers":
				sourcePath, parentPath = "markers.txt", "parent.txt"
			}
			source, observation := observedTemporary(t, f, task, map[string]string{sourcePath: "<<<<<<< text\n=======\n>>>>>>> literal\n"})
			writer := mustWriter(t, f, testWriteBinding(task))
			parentBytes := []byte("parent\n")
			if mode == "advanced_fork" {
				parentBytes = []byte("one\ntwo\n")
			}
			writeTestFile(t, filepath.Join(task.Dir, parentPath), parentBytes)
			parent := mustObserve(t, writer)
			before := snapshotScratch(t, task.Dir)
			result, err := f.svc.MergeBack(context.Background(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "literal_markers" {
				if result.Outcome != ledger.MergeBackMerged {
					t.Fatalf("literal markers treated as conflict: %+v", result)
				}
				return
			}
			if result.Outcome != ledger.MergeBackConflict || result.ConflictSHA256 == (ledger.Digest{}) || !reflect.DeepEqual(before, snapshotScratch(t, task.Dir)) {
				t.Fatalf("conflict changed parent or lost report: %+v", result)
			}
			branch := strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "rev-parse", "HEAD")))
			sourceBranch := strings.TrimSpace(string(gitCommand(t, f.env, source.Dir, nil, "rev-parse", "HEAD")))
			if branch != parent.Commit || sourceBranch != observation.Commit {
				t.Fatal("conflict moved private tips")
			}
			report, err := f.store.ReadBody(context.Background(), result.ConflictSHA256)
			if err != nil || !bytes.Contains(report, []byte(sourcePath)) {
				t.Fatalf("conflict report unavailable: %q %v", report, err)
			}
			notices, err := f.svc.ScratchNotices(context.Background(), task.ContextID, task.ID, 0)
			if err != nil || len(notices) != 1 || notices[0].AuthoringContextID != source.ContextID || !reflect.DeepEqual(notices[0].WrittenPaths, []string{sourcePath}) {
				t.Fatalf("conflict notice lost author/paths: %+v %v", notices, err)
			}
			if mode == "pin_rollback" {
				ctx := context.Background()
				writer = mustWriter(t, f, testWriteBinding(task))
				writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("one\ntwo\n"))
				clean := mustObserve(t, writer)
				observationRecord := f.svc.observations[clean.ID].record
				conflict := mergeBackRecord(t, f, result.ID, ledger.MergeBackConflict)
				for _, outcome := range []string{ledger.MergeBackPrepared, ledger.MergeBackConflict} {
					p := conflict
					p.MergeBackID = uuid.NewV7()
					p.ParentCommit, p.ParentTree = clean.Commit, clean.Tree
					p.Outcome = outcome
					pin := p.ReportPinID
					refs := []ledger.Digest{p.ConflictSHA256}
					if outcome == ledger.MergeBackPrepared {
						p.ConflictSHA256, p.ReportPinID = ledger.Digest{}, uuid.UUID{}
						p.ManifestSHA256, p.PinID = observationRecord.ManifestSHA256, observationRecord.PinID
						p.TargetCommit, p.TargetTree = strings.Repeat("c", 40), observation.Tree
						pin = p.PinID
						refs = mergeBackRefs(p.ManifestSHA256, f.svc.workspaces[f.w.ID].revisions[1])
					}
					drafts, err := mergeBackDrafts(p, refs, uuid.UUID{})
					if err != nil {
						t.Fatal(err)
					}
					before, owner, reportOwner, sequence := f.store.Durable(), f.svc.manifestPins[pin], f.svc.reportPinScratch[pin], f.svc.pinSequences[pin]
					if err := f.svc.append(ctx, drafts); !errors.Is(err, appender.ErrRefused) || f.store.Durable() != before {
						t.Fatalf("real store did not refuse reused active %s pin: %v", outcome, err)
					}
					if f.svc.manifestPins[pin] != owner || f.svc.reportPinScratch[pin] != reportOwner || f.svc.pinSequences[pin] != sequence {
						t.Fatalf("refused %s append destroyed prior pin ownership or order", outcome)
					}
				}
				if _, err := f.svc.CloseScratch(ctx, ownerClosure(source)); err != nil {
					t.Fatal(err)
				}
				if _, err := f.svc.CloseScratch(ctx, ownerClosure(task)); err != nil {
					t.Fatal(err)
				}
				if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 {
					t.Fatalf("later closure lost pin obligations after refused append: %+v", report)
				}
				return
			}
			if mode == "content" {
				writer = mustWriter(t, f, testWriteBinding(task))
				writeTestFile(t, filepath.Join(task.Dir, parentPath), []byte("<<<<<<< text\n=======\n>>>>>>> literal\n"))
				mustObserve(t, writer)
				applied, err := f.svc.MergeBack(context.Background(), source.ID)
				if err != nil || applied.ID == result.ID || applied.Outcome != ledger.MergeBackMerged {
					t.Fatalf("explicit resolved attempt failed: %+v %v", applied, err)
				}
			}
		})
	}
}

func TestMergeBackPreservesUnrepresentedFiles(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, mode := range []string{"dirty_parent", "dirty_source", "untracked", "ignored_file", "ignored_directory"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			if strings.HasPrefix(mode, "ignored") {
				p := f.p
				p.LocalExcludes = HostFile{Present: true, Bytes: []byte("collision\n")}
				var err error
				f.w, err = f.svc.UpdateInputs(context.Background(), f.w.ID, f.w.Revision, p)
				if err != nil {
					t.Fatal(err)
				}
			}
			task := mustScratch(t, f, uuid.NewV7())
			// Source uses a nonignored name, then pins its tracked eligibility
			// before installing the parent's local ignored obstruction.
			source, _ := observedTemporary(t, f, task, map[string]string{"added.txt": "source\n"})
			path := "added.txt"
			if strings.HasPrefix(mode, "ignored") {
				path = "collision"
				b := testWriteBinding(task)
				b.ContextID, b.ResumeScratchID = source.ContextID, source.ID
				writer := mustWriter(t, f, b)
				// Source's .gitignore makes its addition eligible under pinned excludes.
				writeTestFile(t, filepath.Join(source.Dir, ".gitignore"), []byte("!collision\n"))
				writeTestFile(t, filepath.Join(source.Dir, path), []byte("source collision\n"))
				mustObserve(t, writer)
			}
			switch mode {
			case "dirty_parent":
				writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("unobserved parent\n"))
			case "dirty_source":
				writeTestFile(t, filepath.Join(source.Dir, path), []byte("unobserved source\n"))
			case "ignored_directory":
				writeTestFile(t, filepath.Join(task.Dir, path, "private"), []byte("private\n"))
			default:
				writeTestFile(t, filepath.Join(task.Dir, path), []byte("unrepresented\n"))
			}
			beforeParent, beforeSource := snapshotScratch(t, task.Dir), snapshotScratch(t, source.Dir)
			_, err := f.svc.MergeBack(context.Background(), source.ID)
			if strings.HasPrefix(mode, "ignored") {
				assertRefusal(t, err, "obstructed_path")
			} else if !errors.Is(err, ErrUncommittedEdits) {
				t.Fatalf("unobserved work accepted: %v", err)
			}
			if !reflect.DeepEqual(beforeParent, snapshotScratch(t, task.Dir)) || !reflect.DeepEqual(beforeSource, snapshotScratch(t, source.Dir)) {
				t.Fatal("refused merge changed scratch bytes")
			}
		})
	}
}

func TestMergeBackWaitsForProtectedWindow(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	task := mustScratch(t, f, uuid.NewV7())
	source, _ := observedTemporary(t, f, task, map[string]string{"source.txt": "source\n"})
	entered, release := make(chan struct{}), make(chan struct{})
	var gated atomic.Bool
	f.svc.engine.afterRawRead = func(string) {
		if gated.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}
	request := rootRequest(task)
	admitted := make(chan error, 1)
	go func() {
		_, err := admission(f.svc, request)
		admitted <- err
	}()
	<-entered
	// A cancelled writer-side wait cannot land inside this capture.
	ctx, cancel := context.WithCancel(context.Background())
	f.svc.deps.beforeMergeBackParent = cancel
	_, err := f.svc.MergeBack(ctx, source.ID)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("merge did not wait for protected capture: %v", err)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, "source.txt")); !os.IsNotExist(err) {
		t.Error("merge landed inside capture")
	}
	close(release)
	if err := <-admitted; err != nil {
		t.Fatal(err)
	}
	f.svc.deps.beforeMergeBackParent = nil
	f.svc.engine.afterRawRead = nil
	assertExcluded := func() {
		before := snapshotRequestEffects(t, f)
		next := rootRequest(task)
		next.Head = request.RequestID
		_, err := admission(f.svc, next)
		assertBusy(t, err, "scratch_busy")
		assertRequestEffects(t, f, before)
	}
	f.svc.deps.beforeAppend = func() {
		f.svc.deps.beforeAppend = nil
		assertExcluded()
	}
	f.svc.deps.afterMergeBackPrepared = assertExcluded
	result, err := f.svc.MergeBack(context.Background(), source.ID)
	if err != nil || result.Outcome != ledger.MergeBackFastForward {
		t.Fatalf("pending checkpoint blocked post-capture merge: %+v %v", result, err)
	}
	record := admittedRecord(t, f, request.RequestID)
	if record.SealedTree != task.AnchorTree || len(record.MergeBackIDs) != 0 {
		t.Fatal("later merge mutated the sealed checkpoint")
	}
}

func TestMergeBackWaitsForTaskTurn(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, mode := range []string{"task_work", "inputs_update"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			task := mustScratch(t, f, uuid.NewV7())
			source, _ := observedTemporary(t, f, task, map[string]string{"source.txt": "source\n"})
			writer := mustWriter(t, f, testWriteBinding(task))
			lease, err := writer.Lease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			lease()
			ctx, cancel := context.WithCancel(context.Background())
			f.svc.deps.beforeMergeBackWait = cancel
			before := f.store.Durable()
			_, err = f.svc.MergeBack(ctx, source.ID)
			if !errors.Is(err, context.Canceled) || f.store.Durable() != before {
				t.Fatalf("tool gap allowed merge or recorded cancellation before prepared: %v", err)
			}
			entered := make(chan struct{})
			var waiting atomic.Bool
			f.svc.deps.beforeMergeBackWait = func() {
				if waiting.CompareAndSwap(false, true) {
					close(entered)
				}
			}
			mergeCtx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
			done := make(chan struct{})
			var result MergeBackResult
			var mergeErr error
			go func() {
				result, mergeErr = f.svc.MergeBack(mergeCtx, source.ID)
				close(done)
			}()
			defer func() { stop(); <-done }()
			select {
			case <-entered:
			case <-done:
				t.Fatalf("merge did not wait for the task turn: %v", mergeErr)
			}
			var updated Workspace
			if mode == "inputs_update" {
				p := f.p
				p.LocalExcludes = HostFile{Present: true, Bytes: []byte("ignored-later\n")}
				updateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				updated, err = f.svc.UpdateInputs(updateCtx, f.w.ID, f.w.Revision, p)
				cancel()
				if err != nil {
					t.Fatalf("input update blocked behind the task writer: %v", err)
				}
			}
			writeTestFile(t, filepath.Join(task.Dir, "parent.txt"), []byte("parent\n"))
			mustObserve(t, writer)
			<-done
			if mergeErr != nil {
				t.Fatal(mergeErr)
			}
			if string(readTestFile(t, filepath.Join(task.Dir, "parent.txt"))) != "parent\n" || string(readTestFile(t, filepath.Join(task.Dir, "source.txt"))) != "source\n" {
				t.Fatal("merge lost task-turn work")
			}
			if mode == "inputs_update" {
				plan := mergeBackRecord(t, f, result.ID, ledger.MergeBackPrepared)
				if plan.InputsRevision != updated.Revision || plan.CaptureInputsSHA256 != updated.InputsSHA256 {
					t.Fatal("merge prepared with the inputs from before its wait")
				}
			}
		})
	}
}

// Done announces a real context-aware wait, not a retry through Err.
type mergeParentWaitContext struct {
	context.Context
	waiting chan struct{}
	entered atomic.Bool
}

func (c *mergeParentWaitContext) Done() <-chan struct{} {
	if c.entered.CompareAndSwap(false, true) {
		close(c.waiting)
	}
	return c.Context.Done()
}

func TestMergeBackWaitsForPendingChild(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	task := mustScratch(t, f, uuid.NewV7())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	held := make(chan *reservation, 1)
	f.svc.deps.beforeAppend = func() {
		f.svc.mu.Lock()
		var dir string
		for name := range f.svc.pendingDirs {
			dir = filepath.Join(f.colony, "scratch", name)
		}
		f.svc.mu.Unlock()
		guard, err := f.svc.pool.reserve(dir, closureReservation)
		if err != nil {
			t.Error(err)
			cancel()
		}
		held <- guard
	}
	b := testWriteBinding(task)
	b.ContextID = uuid.NewV7()
	created := make(chan struct{})
	var creationErr error
	go func() {
		writer, err := f.svc.BeginWrite(ctx, b)
		if writer != nil {
			writer.Close()
		}
		creationErr = err
		close(created)
	}()
	defer func() { cancel(); <-created }()
	var childGuard *reservation
	select {
	case childGuard = <-held:
		defer childGuard.release()
	case <-created:
		t.Fatalf("temporary creation did not reach append: %v", creationErr)
	}
	// Creation has dropped routing but cannot finish its pool wait.
	unlock, err := f.svc.lockTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	waitCtx := &mergeParentWaitContext{Context: ctx, waiting: make(chan struct{})}
	var waits atomic.Int32
	f.svc.deps.beforeMergeBackWait = func() {
		if waits.Add(1) > 1 {
			cancel()
		}
	}
	done := make(chan struct{})
	var mergeErr error
	go func() {
		_, guard, unlock, err := f.svc.mergeParent(waitCtx, task.ID, uuid.UUID{})
		if err == nil {
			unlock()
			guard.release()
		}
		mergeErr = err
		close(done)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-waitCtx.waiting:
		childGuard.release()
	case <-done:
		t.Fatalf("merge retried pending creation without waiting: %v", mergeErr)
	}
	<-done
	if mergeErr != nil || waits.Load() != 1 {
		t.Fatalf("pending-child wait did not resume once: waits=%d, err=%v", waits.Load(), mergeErr)
	}
	<-created
	if creationErr != nil {
		t.Fatalf("temporary creation failed after release: %v", creationErr)
	}
}

func TestMergeBackDurableNoticeAndProvenance(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	firstSource, firstObservation := observedTemporary(t, f, task, map[string]string{"shared.txt": "first author\n"})
	first, err := f.svc.MergeBack(ctx, firstSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := rootRequest(task)
	firstRequest := mustAdmit(t, f, root)
	secondSource, _ := observedTemporary(t, f, task, map[string]string{"shared.txt": "second author\n"})
	second, err := f.svc.MergeBack(ctx, secondSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	writer := mustWriter(t, f, testWriteBinding(task))
	writeTestFile(t, filepath.Join(task.Dir, "shared.txt"), []byte("task edit\n"))
	taskObservation := mustObserve(t, writer)
	next := rootRequest(task)
	next.Head = root.RequestID
	mustAdmit(t, f, next)
	a, b := admittedRecord(t, f, root.RequestID), admittedRecord(t, f, next.RequestID)
	if !reflect.DeepEqual(a.MergeBackIDs, []uuid.UUID{first.ID}) || !reflect.DeepEqual(b.MergeBackIDs, []uuid.UUID{second.ID}) || b.ScratchObservationID != taskObservation.ID {
		t.Fatalf("admission window/provenance incorrect: root=%+v successor=%+v", a, b)
	}
	if b.DeltaBaseTree != firstRequest.SealedTree || a.SealedTree != firstObservation.Tree {
		t.Fatal("merge mutated sealed parent or incremental delta base")
	}
	notices, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, 0)
	if err != nil || len(notices) != 2 || notices[0].AuthoringContextID != firstSource.ContextID || notices[1].AuthoringContextID != secondSource.ContextID {
		t.Fatalf("authorship lost: %+v %v", notices, err)
	}
	before := f.store.Durable()
	f.reopen(t, serviceConfig())
	replayed, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, 0)
	if err != nil || !reflect.DeepEqual(replayed, notices) || f.store.Durable() != before {
		t.Fatalf("notice replay changed history: %+v %v", replayed, err)
	}
	assertRecoveryOnce(t, f, root, nil, RequestInterrupted)
	proposed := rootRequest(task)
	proposed.RestartFrom = next.RequestID
	assertUnavailable(t, promotionError(ctx, f.svc, proposed), "no_validator")
}

func TestScratchNoticesScopedAndReplayable(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	other := mustScratch(t, f, uuid.NewV7())
	source, _ := observedTemporary(t, f, task, map[string]string{"base.txt": "conflicting source\n"})
	writer := mustWriter(t, f, testWriteBinding(task))
	writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("parent\n"))
	mustObserve(t, writer)
	result, err := f.svc.MergeBack(ctx, source.ID)
	if err != nil || result.Outcome != ledger.MergeBackConflict {
		t.Fatalf("conflict: %+v %v", result, err)
	}
	notices, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, 0)
	if err != nil || len(notices) != 1 {
		t.Fatalf("missing notice: %+v %v", notices, err)
	}
	notices[0].WrittenPaths[0] = "mutated"
	again, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, 0)
	if err != nil || !reflect.DeepEqual(again[0].WrittenPaths, []string{"base.txt"}) {
		t.Fatalf("notice slice leaked projection: %+v %v", again, err)
	}
	if got, err := f.svc.ScratchNotices(ctx, other.ContextID, other.ID, 0); err != nil || len(got) != 0 {
		t.Fatalf("another task received notice: %+v %v", got, err)
	}
	for _, binding := range [][2]uuid.UUID{{other.ContextID, task.ID}, {task.ContextID, source.ID}, {uuid.NewV7(), uuid.NewV7()}} {
		if _, err := f.svc.ScratchNotices(ctx, binding[0], binding[1], 0); !errors.Is(err, ErrNotBound) {
			t.Fatalf("foreign binding accepted: %v", err)
		}
	}
	if got, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, result.Sequence); err != nil || len(got) != 0 {
		t.Fatalf("cursor replayed old notice: %+v %v", got, err)
	}
	if _, err := f.svc.CloseScratch(ctx, Closure{ScratchID: source.ID, Actor: ledger.ActorAgent, ContextID: source.ContextID, Reason: "abandoned"}); err != nil {
		t.Fatal(err)
	}
	writer = mustWriter(t, f, testWriteBinding(task))
	writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("one\ntwo\n"))
	mustObserve(t, writer)
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(task)); err != nil {
		t.Fatal(err)
	}
	f.reopen(t, serviceConfig())
	closed, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, 0)
	if err != nil || !reflect.DeepEqual(closed, again) {
		t.Fatalf("closed task lost notice: %+v %v", closed, err)
	}
}

func mergeBackRecord(t *testing.T, f *serviceFixture, id uuid.UUID, outcome string) ledger.ScratchMergeBack {
	t.Helper()
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			if record.Type != ledger.TypeScratchMergeBack {
				continue
			}
			p, err := ledger.DecodeScratchMergeBack(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if p.MergeBackID == id && p.Outcome == outcome {
				return p
			}
		}
	}
	t.Fatalf("missing merge-back %s %s", id, outcome)
	return ledger.ScratchMergeBack{}
}

func TestMergeBackPreparedRecovery(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, phase := range []string{"prepared", "files", "index", "applied_boundary", "foreign_lock", "verification", "third_tip", "third_value", "historical_inputs", "after_applied", "after_applied_dirty", "after_applied_abandoned"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			ctx := context.Background()
			task := mustScratch(t, f, uuid.NewV7())
			source, observation := observedTemporary(t, f, task, map[string]string{"base.txt": "replacement\n", "added.txt": "added\n"})
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			admin, err := scratchAdmin(task.worktree(), f.p.CommonDir)
			if err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(admin, "index.lock")
			var third string
			switch phase {
			case "files":
				f.svc.engine.afterMergeBackFiles = cancel
			case "index":
				f.svc.engine.afterMergeBackIndex = cancel
			case "applied_boundary":
				f.svc.deps.beforeMergeBackApplied = cancel
			case "after_applied", "after_applied_dirty", "after_applied_abandoned":
				f.svc.deps.afterMergeBackApplied = cancel
			case "foreign_lock":
				f.svc.deps.afterMergeBackPrepared = func() { writeTestFile(t, lockPath, []byte("foreign\n")) }
			case "verification":
				f.svc.engine.afterMergeBackFiles = func() { writeTestFile(t, filepath.Join(task.Dir, "added.txt"), []byte("third\n")) }
			case "third_tip":
				third = strings.TrimSpace(string(gitCommand(t, f.env, f.root, []byte("third\n"), "commit-tree", task.AnchorTree, "-p", task.BaseCommit)))
				f.svc.engine.afterMergeBackIndex = func() { gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, third, task.BaseCommit) }
			default:
				f.svc.deps.afterMergeBackPrepared = cancel
			}
			result, err := f.svc.MergeBack(cancelCtx, source.ID)
			if strings.HasPrefix(phase, "after_applied") {
				assertAppliedClosureRecovery(t, f, task, source, result, err, phase)
				return
			}
			if err == nil || result.Outcome != ledger.MergeBackWriteFailed {
				t.Fatalf("failure lost prepared plan: %+v %v", result, err)
			}
			wantFailure := ledger.MergeBackFailureCancelled
			switch phase {
			case "foreign_lock":
				wantFailure = ledger.MergeBackFailureIndexLock
			case "verification":
				wantFailure = ledger.MergeBackFailureVerification
			case "third_tip":
				wantFailure = ledger.MergeBackFailureRefMismatch
				assertUnavailable(t, err, "merge_back_interference")
			}
			failure := mergeBackRecord(t, f, result.ID, ledger.MergeBackWriteFailed)
			plan := mergeBackRecord(t, f, result.ID, ledger.MergeBackPrepared)
			if failure.Failure != wantFailure || !sameMergeBackPlan(plan, failure) {
				t.Fatalf("failure changed plan/token: %+v", failure)
			}
			before := snapshotScratch(t, task.Dir)
			tip := strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "rev-parse", "HEAD")))
			sequence := f.store.Durable()
			report := f.reopen(t, serviceConfig())
			if !slices.Contains(report.Pending, "merge_back_pending "+result.ID.String()) || f.store.Durable() != sequence || !reflect.DeepEqual(before, snapshotScratch(t, task.Dir)) || strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "rev-parse", "HEAD"))) != tip {
				t.Fatalf("Open executed pending merge: %+v", report)
			}
			for _, scratch := range []Scratch{task, source} {
				if _, err := f.svc.CloseScratch(ctx, ownerClosure(scratch)); !errors.Is(err, ErrMergeBackPending) {
					t.Fatalf("pending closure: %v", err)
				}
			}
			if _, err := f.svc.BeginWrite(ctx, testWriteBinding(task)); !errors.Is(err, ErrMergeBackPending) {
				t.Fatalf("pending task accepted writer: %v", err)
			}
			if err := promotionError(ctx, f.svc, rootRequest(task)); !errors.Is(err, ErrMergeBackPending) {
				t.Fatalf("pending task accepted admission: %v", err)
			}
			switch phase {
			case "third_tip":
				_, err = f.svc.RetryMergeBack(ctx, result.ID)
				assertUnavailable(t, err, "merge_back_interference")
				got := strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "rev-parse", "HEAD")))
				if got != third {
					t.Fatal("retry overwrote foreign branch")
				}
				gitCommand(t, f.env, f.root, nil, "update-ref", task.Branch, task.BaseCommit, third)
			case "third_value":
				writeTestFile(t, filepath.Join(task.Dir, "added.txt"), []byte("third\n"))
				_, err = f.svc.RetryMergeBack(ctx, result.ID)
				assertUnavailable(t, err, "merge_back_interference")
				if string(readTestFile(t, filepath.Join(task.Dir, "added.txt"))) != "third\n" {
					t.Fatal("retry overwrote third bytes")
				}
				if err := os.Remove(filepath.Join(task.Dir, "added.txt")); err != nil {
					t.Fatal(err)
				}
			case "foreign_lock":
				_, err = f.svc.RetryMergeBack(ctx, result.ID)
				assertBusy(t, err, "scratch_busy")
				if string(readTestFile(t, lockPath)) != "foreign\n" {
					t.Fatal("retry removed foreign index lock")
				}
				if err := os.Remove(lockPath); err != nil {
					t.Fatal(err)
				}
			case "verification":
				writeTestFile(t, filepath.Join(task.Dir, "added.txt"), []byte("added\n"))
			case "historical_inputs":
				p := f.p
				p.LocalAttributes = HostFile{Present: true, Bytes: []byte("*.txt text eol=crlf\n")}
				if _, err := f.svc.UpdateInputs(ctx, f.w.ID, 1, p); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.Collect(ctx, ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: time.Now().Add(time.Hour).UnixNano()}); err != nil {
					t.Fatal(err)
				}
			}
			applied, err := f.svc.RetryMergeBack(ctx, result.ID)
			if err != nil || applied.ID != result.ID || applied.Outcome != ledger.MergeBackFastForward || applied.TargetCommit != observation.Commit {
				t.Fatalf("retry recomputed/lost plan: %+v %v", applied, err)
			}
			if string(readTestFile(t, filepath.Join(task.Dir, "added.txt"))) != "added\n" {
				t.Fatal("retry used current inputs instead of pinned preparation")
			}
			got := strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "write-tree")))
			if got != plan.TargetTree {
				t.Fatalf("retry installed wrong real index: %s", got)
			}
			events, err := f.svc.ScratchNotices(ctx, task.ContextID, task.ID, 0)
			if err != nil || len(events) < 2 || events[0].Outcome != ledger.MergeBackWriteFailed || events[len(events)-1].Outcome != ledger.MergeBackFastForward {
				t.Fatalf("retry notices: %+v %v", events, err)
			}
		})
	}
}

func TestMergeBackPreparedAcknowledgementBoundary(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	task := mustScratch(t, f, uuid.NewV7())
	source, _ := observedTemporary(t, f, task, map[string]string{"base.txt": "replacement\n"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before, sequence := snapshotScratch(t, task.Dir), f.store.Durable()
	admin, err := scratchAdmin(task.worktree(), f.p.CommonDir)
	if err != nil {
		t.Fatal(err)
	}
	index := readTestFile(t, filepath.Join(admin, "index"))
	f.svc.deps.beforeAppend = cancel
	_, err = f.svc.MergeBack(ctx, source.ID)
	if !errors.Is(err, context.Canceled) || f.store.Durable() != sequence {
		t.Fatalf("unacknowledged preparation recorded effects: %v", err)
	}
	if !reflect.DeepEqual(before, snapshotScratch(t, task.Dir)) || !bytes.Equal(index, readTestFile(t, filepath.Join(admin, "index"))) || strings.TrimSpace(string(gitCommand(t, f.env, task.Dir, nil, "rev-parse", "HEAD"))) != task.BaseCommit {
		t.Fatal("files/index/branch moved before preparation acknowledgement")
	}
	f.svc.deps.beforeAppend = nil
	if _, err := f.svc.MergeBack(context.Background(), source.ID); err != nil {
		t.Fatalf("known rejection left phantom pending merge: %v", err)
	}
}

func assertAppliedClosureRecovery(t *testing.T, f *serviceFixture, task, source Scratch, result MergeBackResult, interrupted error, phase string) {
	t.Helper()
	ctx := context.Background()
	if !errors.Is(interrupted, context.Canceled) || result.Outcome != ledger.MergeBackFastForward {
		t.Fatalf("applied/closure interruption: %+v %v", result, interrupted)
	}
	admin, err := scratchAdmin(task.worktree(), f.p.CommonDir)
	if err != nil {
		t.Fatal(err)
	}
	durable := f.store.Durable()
	parentFiles := snapshotScratch(t, task.Dir)
	parentIndex := readTestFile(t, filepath.Join(admin, "index"))
	objects := snapshotRequestEffects(t, f).Capture.Objects
	records := serviceCommits(t, f.store)
	report := f.reopen(t, serviceConfig())
	if f.store.Durable() != durable || !slices.Contains(report.Pending, "merge_back_closure_pending "+source.ID.String()) || f.svc.scratches[source.ID].closed {
		t.Fatalf("Open completed missing closure: %+v", report)
	}
	binding := testWriteBinding(task)
	binding.ContextID, binding.ResumeScratchID = source.ContextID, source.ID
	if _, err := f.svc.BeginWrite(ctx, binding); !errors.Is(err, ErrClosed) {
		t.Fatalf("applied source resumed: %v", err)
	}
	if phase == "after_applied_dirty" {
		writeTestFile(t, filepath.Join(source.Dir, "added.txt"), []byte("foreign\n"))
		if _, err := f.svc.RetryMergeBack(ctx, result.ID); !errors.Is(err, ErrUnrepresentedEdits) || f.store.Durable() != durable || f.svc.scratches[source.ID].closed {
			t.Fatalf("repeat discarded foreign source bytes: %v", err)
		}
		writeTestFile(t, filepath.Join(source.Dir, "added.txt"), []byte("added\n"))
		// A refused observation may publish its observed blob, not a commit/ref.
		objects = snapshotRequestEffects(t, f).Capture.Objects
	}
	if phase == "after_applied_abandoned" {
		if _, err := f.svc.CloseScratch(ctx, ownerClosure(source)); !errors.Is(err, ErrInvalid) || f.store.Durable() != durable {
			t.Fatalf("merged source recorded as abandoned: %v", err)
		}
	}
	var repeated MergeBackResult
	if phase == "after_applied_dirty" {
		repeated, err = f.svc.RetryMergeBack(ctx, result.ID)
	} else {
		repeated, err = f.svc.MergeBack(ctx, source.ID)
	}
	if err != nil || repeated != result || f.store.Durable() != durable+2 {
		t.Fatalf("repeat did more than missing closure: %+v %v", repeated, err)
	}
	after := serviceCommits(t, f.store)
	if len(after) != len(records)+1 || len(after[len(after)-1].Records) != 2 {
		t.Fatal("closure repeat changed commit history shape")
	}
	last := after[len(after)-1]
	closure, err := ledger.DecodeScratchClosed(last.Records[0].Payload)
	if err != nil || closure.ScratchID != source.ID || closure.Reason != ledger.ReasonTemporaryMergedBack || last.Records[0].Actor != ledger.ActorAgent || last.Records[0].ContextID != source.ContextID || last.Records[1].Type != ledger.TypeBodyUnpin {
		t.Fatalf("wrong recovery closure: %+v %v", closure, err)
	}
	if !reflect.DeepEqual(parentFiles, snapshotScratch(t, task.Dir)) || !bytes.Equal(parentIndex, readTestFile(t, filepath.Join(admin, "index"))) || snapshotRequestEffects(t, f).Capture.Objects != objects {
		t.Fatal("missing-closure bookkeeping touched parent or created objects")
	}
	ref := "refs/dwarf-harness/scratch-commits/" + result.ID.String()
	if got, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, ref, 40); err != nil || got != result.TargetCommit {
		t.Fatalf("closure changed target retention: %s %v", got, err)
	}
	if _, err := os.Stat(source.Dir); !os.IsNotExist(err) {
		t.Fatalf("missing-closure recovery retained directory: %v", err)
	}
	durable = f.store.Durable()
	if got, err := f.svc.MergeBack(ctx, source.ID); err != nil || got != result || f.store.Durable() != durable {
		t.Fatalf("closed repeat wrote records: %+v %v", got, err)
	}
	if got, err := f.svc.RetryMergeBack(ctx, result.ID); err != nil || got != result || f.store.Durable() != durable {
		t.Fatalf("closed retry wrote records: %+v %v", got, err)
	}
}

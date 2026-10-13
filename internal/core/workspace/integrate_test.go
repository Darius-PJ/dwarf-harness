package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"uuid"
)

// Plain Git builds both inputs and independent reference trees. A private index
// permits case-colliding reference trees even on case-insensitive filesystems.
func integrationTree(t *testing.T, f gitFixture, files map[string]string) string {
	t.Helper()
	env := append(append([]string(nil), f.env...), "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"))
	gitCommand(t, env, f.root, nil, "read-tree", "--empty")
	var paths []string
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var entries strings.Builder
	for _, path := range paths {
		oid := strings.TrimSpace(string(gitCommand(t, env, f.root, []byte(files[path]), "hash-object", "-w", "--stdin", "--no-filters")))
		fmt.Fprintf(&entries, "100644 %s\t%s\x00", oid, path)
	}
	gitCommand(t, env, f.root, []byte(entries.String()), "update-index", "-z", "--index-info")
	return strings.TrimSpace(string(gitCommand(t, env, f.root, nil, "write-tree")))
}

func mustMergeTrees(t *testing.T, f *captureFixture, base, predecessor, submission string) mergeResult {
	t.Helper()
	f.s.Inputs = f.p
	got, err := f.engine.mergeTrees(context.Background(), f.s, base, predecessor, submission)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMergeTreesUsesDeltaBase(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	base := integrationTree(t, f.f, map[string]string{"base.txt": "a\ntwo\n"})
	predecessor := integrationTree(t, f.f, map[string]string{"base.txt": "foreign\ntwo\n"})
	submission := integrationTree(t, f.f, map[string]string{"base.txt": "a\ntwo\n", "b.txt": "successor\n"})
	reference := newGitFixture(t, "sha1")
	want := integrationTree(t, reference, map[string]string{"base.txt": "foreign\ntwo\n", "b.txt": "successor\n"})
	got := mustMergeTrees(t, f, base, predecessor, submission)
	if got.Tree != want || got.Cause != "" || len(got.Paths) != 0 {
		t.Fatalf("successor resubmitted its parent's edit: %#v want %s", got, want)
	}
	wrong := mustMergeTrees(t, f, f.s.Worktree.AnchorTree, predecessor, submission)
	if wrong.Cause != "merge" {
		t.Fatalf("fixture does not distinguish anchor from delta base: %#v", wrong)
	}
}

func TestMergeTreesConflictByExitStatus(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	base := integrationTree(t, f.f, map[string]string{"a.txt": "base\n", "b.txt": "base\n"})
	predecessor := integrationTree(t, f.f, map[string]string{"a.txt": "ours\n", "b.txt": "ours\n"})
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"content", map[string]string{"a.txt": "theirs\n", "b.txt": "theirs\n"}},
		{"modify delete without markers", map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submission := integrationTree(t, f.f, tc.files)
			before := string(gitCommand(t, f.f.env, f.f.root, nil, "for-each-ref", "--format=%(refname) %(objectname)"))
			commitsBefore := bytes.Count(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "--batch-all-objects", "--batch-check=%(objecttype)"), []byte("commit\n"))
			got := mustMergeTrees(t, f, base, predecessor, submission)
			if got.Tree != "" || got.Cause != "merge" || !reflect.DeepEqual(got.Paths, []string{"a.txt", "b.txt"}) {
				t.Fatalf("conflict lost or retained a candidate: %#v", got)
			}
			after := string(gitCommand(t, f.f.env, f.f.root, nil, "for-each-ref", "--format=%(refname) %(objectname)"))
			if after != before {
				t.Fatal("conflict moved a ref")
			}
			commitsAfter := bytes.Count(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "--batch-all-objects", "--batch-check=%(objecttype)"), []byte("commit\n"))
			if commitsAfter != commitsBefore {
				t.Fatal("conflict created a commit")
			}
		})
	}
	literal := "<<<<<<< literal text\nnot a conflict\n"
	predecessor = integrationTree(t, f.f, map[string]string{"a.txt": "base\n", "b.txt": "base\n", "literal.txt": literal})
	submission := integrationTree(t, f.f, map[string]string{"a.txt": "base\n", "b.txt": "base\n", "other.txt": "new\n"})
	want := integrationTree(t, newGitFixture(t, "sha1"), map[string]string{"a.txt": "base\n", "b.txt": "base\n", "literal.txt": literal, "other.txt": "new\n"})
	if got := mustMergeTrees(t, f, base, predecessor, submission); got.Tree != want || got.Cause != "" {
		t.Fatalf("literal marker was treated as conflict: %#v", got)
	}
}

func TestIntegrationEnvironment(t *testing.T) {
	t.Parallel()
	t.Run("exact variables", func(t *testing.T) {
		t.Parallel()
		host := []string{"PATH=kept", "HOME=kept", "GIT_DIR=bogus", "GIT_WORK_TREE=bogus", "GIT_INDEX_FILE=bogus", "GIT_OBJECT_DIRECTORY=bogus", "GIT_ALTERNATE_OBJECT_DIRECTORIES=bogus", "GIT_COMMON_DIR=bogus", "GIT_NAMESPACE=bogus", "GIT_CEILING_DIRECTORIES=bogus", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1", "GIT_CONFIG_PARAMETERS='merge.default=evil'", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=merge.default", "GIT_CONFIG_VALUE_0=evil", "GIT_CONFIG_GLOBAL=host-config", "GIT_CONFIG_SYSTEM=host-system", "GIT_ATTR_NOSYSTEM=0", "GIT_TRACE=1", "GIT_AUTHOR_NAME=host", "GIT_COMMITTER_DATE=host"}
		if runtime.GOOS == "windows" {
			host = append(host, "git_attr_source=HEAD", "git_config_parameters=host")
		}
		got := integrationEnvironment(host, "colony", "inputs", "common")
		want := map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join("colony", "workspaces", "empty"), "GIT_NO_REPLACE_OBJECTS": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0", "GIT_DIR": filepath.Join("inputs", "git"), "GIT_OBJECT_DIRECTORY": filepath.Join("common", "objects"), "GIT_ATTR_NOSYSTEM": "1"}
		if !reflect.DeepEqual(gitVariables(got), want) || len(got) != len(want)+2 || got[0] != host[0] || got[1] != host[1] {
			t.Fatalf("integration environment leaked host settings: %q", got)
		}
	})
	for _, driver := range []string{"evil", "union", "binary"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			f := newCaptureFixture(t)
			marker := filepath.Join(t.TempDir(), "merge-driver-ran")
			gitCommand(t, f.f.env, f.f.root, nil, "config", "merge.evil.driver", "touch '"+filepath.ToSlash(marker)+"'; exit 0")
			attributes := "*.txt merge=" + driver + "\n"
			writeTestFile(t, filepath.Join(f.f.root, ".gitattributes"), []byte(attributes))
			base := integrationTree(t, f.f, map[string]string{".gitattributes": attributes, "a.txt": "1\n2\n3\n4\n5\n"})
			predecessor := integrationTree(t, f.f, map[string]string{".gitattributes": attributes, "a.txt": "ours\n2\n3\n4\n5\n"})
			last := "theirs\n2\n3\n4\n5\n"
			if driver == "binary" {
				last = "1\n2\n3\n4\ntheirs\n"
			}
			submission := integrationTree(t, f.f, map[string]string{".gitattributes": attributes, "a.txt": last})
			got := mustMergeTrees(t, f, base, predecessor, submission)
			if driver == "binary" {
				want := integrationTree(t, newGitFixture(t, "sha1"), map[string]string{".gitattributes": attributes, "a.txt": "ours\n2\n3\n4\ntheirs\n"})
				if got.Cause != "" || got.Tree != want {
					t.Fatalf("tree binary attribute influenced integration: %#v", got)
				}
			} else if got.Cause != "merge" || !reflect.DeepEqual(got.Paths, []string{"a.txt"}) {
				t.Fatalf("tree merge attribute influenced integration: %#v", got)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("merge driver ran: %v", err)
			}
		})
	}
	for _, source := range []string{"local", "global"} {
		t.Run("pinned "+source, func(t *testing.T) {
			t.Parallel()
			f := newCaptureFixture(t)
			attrs := HostFile{Present: true, Bytes: []byte("*.txt merge=binary\n")}
			if source == "local" {
				f.p.LocalAttributes = attrs
			} else {
				f.p.GlobalAttributes = attrs
			}
			// Dirty materialized bytes and hostile live config must be replaced.
			writeTestFile(t, filepath.Join(f.s.InputsDir, "git", "info", "attributes"), []byte("*.txt merge=union\n"))
			writeTestFile(t, filepath.Join(f.s.InputsDir, "attributes"), []byte("*.txt merge=union\n"))
			host := append(append([]string(nil), f.f.env...), "GIT_DIR=bogus", "GIT_WORK_TREE=bogus", "GIT_INDEX_FILE=bogus", "GIT_CONFIG_PARAMETERS='merge.default=union'", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=merge.default", "GIT_CONFIG_VALUE_0=union")
			f.engine.git.environ = func() []string { return host }
			base := integrationTree(t, f.f, map[string]string{"a.txt": "1\n2\n3\n4\n5\n"})
			predecessor := integrationTree(t, f.f, map[string]string{"a.txt": "ours\n2\n3\n4\n5\n"})
			submission := integrationTree(t, f.f, map[string]string{"a.txt": "1\n2\n3\n4\ntheirs\n"})
			got := mustMergeTrees(t, f, base, predecessor, submission)
			if got.Cause != "merge" || !reflect.DeepEqual(got.Paths, []string{"a.txt"}) {
				t.Fatalf("pinned binary rule ignored: %#v", got)
			}
		})
	}
}

func TestDerivedTreeCaseCollision(t *testing.T) {
	t.Parallel()
	for _, paths := range [][2]string{{"Docs/x.txt", "docs/y.txt"}, {"Name.txt", "name.txt"}} {
		t.Run(paths[0], func(t *testing.T) {
			t.Parallel()
			f := newCaptureFixture(t)
			base := integrationTree(t, f.f, map[string]string{})
			predecessor := integrationTree(t, f.f, map[string]string{paths[0]: "one\n"})
			submission := integrationTree(t, f.f, map[string]string{paths[1]: "two\n"})
			got := mustMergeTrees(t, f, base, predecessor, submission)
			if got.Tree != "" || got.Cause != "case_collision" || !reflect.DeepEqual(got.Paths, paths[:]) {
				t.Fatalf("unmaterializable merge accepted: %#v", got)
			}
		})
	}
}

func TestIntegrationCommit(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	tree := integrationTree(t, f.f, map[string]string{"derived.txt": "integrated\n"})
	parent := f.s.Worktree.BaseCommit
	request := uuid.MustParse("018bcfe5-687b-7000-8000-000000000001")
	generation := uuid.MustParse("018bcfe5-687b-7000-8000-000000000002")
	// 0x018bcfe5687b milliseconds = 1700000000123; truncate to seconds.
	wantObject := fmt.Sprintf("tree %s\nparent %s\nauthor DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\ncommitter DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\n\ndwarf-harness integration\n\nrequest %s\ngeneration %s\n", tree, parent, request, generation)
	want := strings.TrimSpace(string(gitCommand(t, f.f.env, f.f.root, []byte(wantObject), "hash-object", "-t", "commit", "--stdin", "--no-filters")))
	for _, hostile := range []bool{false, true} {
		if hostile {
			global := filepath.Join(t.TempDir(), "config")
			writeTestFile(t, global, []byte("[user]\n name = Host\n email = host@example.invalid\n[commit]\n gpgSign = true\n"))
			gitCommand(t, f.f.env, f.f.root, nil, "config", "user.name", "Repository Host")
			gitCommand(t, f.f.env, f.f.root, nil, "config", "user.email", "host@example.invalid")
			gitCommand(t, f.f.env, f.f.root, nil, "config", "commit.gpgSign", "true")
			host := append(append([]string(nil), f.f.env...), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_PARAMETERS='commit.gpgSign=true'", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z")
			f.engine.git.environ = func() []string { return host }
		}
		got, err := f.engine.integrationCommit(context.Background(), f.s, tree, parent, request, generation)
		if err != nil || got != want {
			t.Fatalf("integration commit hostile=%t: %s want %s: %v", hostile, got, want, err)
		}
		object := gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "commit", got)
		if string(object) != wantObject {
			t.Fatalf("commit provenance: %q", object)
		}
	}
	generation[15]++
	other, err := f.engine.integrationCommit(context.Background(), f.s, tree, parent, request, generation)
	if err != nil || other == want {
		t.Fatalf("generation identity lost: %s %v", other, err)
	}
}

func TestIntegrationLineEndingNeutral(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	f.p.Normalization.AutoCRLF = "true"
	baseText := "1\n2\n3\n4\n5\n"
	base := integrationTree(t, f.f, map[string]string{"base.txt": baseText})
	predecessor := integrationTree(t, f.f, map[string]string{"base.txt": "ours\n2\n3\n4\n5\n"})
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "base.txt"), []byte("1\r\n2\r\n3\r\n4\r\ntheirs\r\n"))
	sealed := mustCapture(t, f, sealCapture)
	got := mustMergeTrees(t, f, base, predecessor, sealed.Tree)
	wantText := "ours\n2\n3\n4\ntheirs\n"
	want := integrationTree(t, newGitFixture(t, "sha1"), map[string]string{"base.txt": wantText})
	if got.Cause != "" || got.Tree != want {
		t.Fatalf("canonical CRLF submission conflicted: %#v want %s", got, want)
	}
	blob := gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "blob", got.Tree+":base.txt")
	if string(blob) != wantText {
		t.Fatalf("integration converted canonical bytes: %q", blob)
	}
}

func TestConflictReport(t *testing.T) {
	t.Parallel()
	for _, cause := range []string{"merge", "case_collision"} {
		body, digest, err := conflictReport(cause, []string{"z", "line\nname", "a", "z"})
		want := fmt.Sprintf("{\"format\":\"dwarf-harness/conflict-report\",\"version\":1,\"cause\":%q,\"paths\":3}\n{\"path\":\"a\"}\n{\"path\":\"line\\nname\"}\n{\"path\":\"z\"}\n", cause)
		if err != nil || string(body) != want || digest != sha256.Sum256(body) {
			t.Fatalf("report: %q %x %v", body, digest, err)
		}
		again, sum, err := conflictReport(cause, []string{"a", "z", "line\nname"})
		if err != nil || !bytes.Equal(again, body) || sum != digest {
			t.Fatal("report depends on input ordering or duplicates")
		}
	}
	var paths []string
	for i := maxConflictPaths; i >= 0; i-- {
		paths = append(paths, fmt.Sprintf("%05d", i))
	}
	body, _, err := conflictReport("merge", paths)
	if err != nil {
		t.Fatal(err)
	}
	rows := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	var header struct {
		Paths int `json:"paths"`
	}
	if err := json.Unmarshal(rows[0], &header); err != nil || header.Paths != maxConflictPaths+1 || len(rows) != maxConflictPaths+1 {
		t.Fatalf("truncated report lost total: %s rows=%d err=%v", rows[0], len(rows), err)
	}
	if string(rows[1]) != `{"path":"00000"}` || string(rows[len(rows)-1]) != `{"path":"09999"}` {
		t.Fatal("bounded rows were not the sorted prefix")
	}
	paths = append(paths, string([]byte{0xff}))
	body, digest, err := conflictReport("merge", paths)
	if !errors.Is(err, ErrInvalid) || body != nil || digest != ([32]byte{}) {
		t.Fatalf("non-UTF-8 path accepted or partially returned: %q %x %v", body, digest, err)
	}
}

func TestGenerationRefsCompareAndSwap(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	ctx := context.Background()
	id := uuid.NewV7()
	commit := f.s.Worktree.BaseCommit
	tree := integrationTree(t, f.f, map[string]string{"new": "new"})
	other := strings.TrimSpace(string(gitCommand(t, f.f.env, f.f.root, nil, "commit-tree", "-p", commit, "-m", "other", "--end-of-options", tree)))
	ref := "refs/dwarf-harness/generations/" + id.String()
	if err := f.engine.git.createGenerationRef(ctx, f.f.colony, f.p, id, commit); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.git.createGenerationRef(ctx, f.f.colony, f.p, id, other); !errors.Is(err, ErrGit) {
		t.Fatalf("generation ref overwritten: %v", err)
	}
	if err := f.engine.git.deleteGenerationRef(ctx, f.f.colony, f.p, id, other); !errors.Is(err, ErrGit) {
		t.Fatalf("moved generation ref deleted: %v", err)
	}
	got, err := f.engine.git.readRef(ctx, f.f.colony, f.p.CommonDir, ref, len(commit))
	if err != nil || got != commit {
		t.Fatalf("failed CAS changed ref: %s %v", got, err)
	}
	if err := f.engine.git.deleteGenerationRef(ctx, f.f.colony, f.p, id, commit); err != nil {
		t.Fatal(err)
	}
	got, err = f.engine.git.readRef(ctx, f.f.colony, f.p.CommonDir, ref, len(commit))
	if err != nil || got != "" {
		t.Fatalf("generation retained after deletion: %s %v", got, err)
	}
	main, err := f.engine.git.readRef(ctx, f.f.colony, f.p.CommonDir, "refs/heads/main", len(commit))
	if err != nil || main != commit {
		t.Fatalf("generation helpers moved main: %s %v", main, err)
	}
}

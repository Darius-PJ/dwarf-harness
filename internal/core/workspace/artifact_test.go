package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func artifactBlob(t *testing.T, f *serviceFixture, data []byte) string {
	t.Helper()
	return strings.TrimSpace(string(gitCommand(t, f.env, f.root, data, "hash-object", "-w", "--stdin")))
}
func artifactTree(t *testing.T, f *serviceFixture, rows string) string {
	t.Helper()
	return strings.TrimSpace(string(gitCommand(t, f.env, f.root, []byte(rows), "mktree", "-z", "--missing")))
}

func TestReadArtifactsImmutableOrderModesAndCaps(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	blob := artifactBlob(t, f, []byte("abc"))
	link := artifactBlob(t, f, []byte("../../outside"))
	commit := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "HEAD")))
	subtree := artifactTree(t, f, "100755 blob "+blob+"\tx\x00")
	tree := artifactTree(t, f, "100644 blob "+blob+"\té\x00"+"160000 commit "+commit+"\tsubmodule\x00"+"120000 blob "+link+"\tlink\x00"+"040000 tree "+subtree+"\ta\x00"+"100644 blob "+blob+"\ta-z\x00"+"100644 blob "+blob+"\tA\t\x00")
	request := ArtifactRequest{WorkspaceID: f.w.ID, Tree: tree, MaxBytes: 8 << 20, WorkAllowance: 10000}
	// Host Git overrides and mutable files must not become object authority.
	f.env = append(f.env, "GIT_OBJECT_DIRECTORY=does-not-exist", "GIT_NO_LAZY_FETCH=0")
	writeTestFile(t, f.root+"/base.txt", []byte("mutable"))
	out, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range out.Entries {
		paths = append(paths, e.Path)
		if e.Path == "link" || e.Path == "submodule" {
			if e.Reason != ledger.ReasonUnsupportedSyntax || e.Bytes != nil || e.SHA256 != (ledger.Digest{}) {
				t.Fatalf("followed nonregular entry %+v", e)
			}
		} else {
			if string(e.Bytes) != "abc" || e.SHA256 != ledger.Digest(sha256.Sum256([]byte("abc"))) || e.Reason != "" {
				t.Fatalf("wrong immutable bytes %+v", e)
			}
		}
	}
	want := []string{"A\t", "a-z", "a/x", "link", "submodule", "é"}
	if !reflect.DeepEqual(paths, want) || !out.Complete || out.NextPath != "é" || out.WorkUsed <= uint64(len(paths)) {
		t.Fatalf("order/cursor/work %+v", out)
	}
	request.MaxBytes = 3
	page, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if page.Complete || page.Reason != ledger.ReasonTemporaryCap || len(page.Entries) != 1 || page.NextPath != "A\t" {
		t.Fatalf("byte boundary %+v", page)
	}
	request.AfterPath = page.NextPath
	request.MaxBytes = 8 << 20
	rest, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !rest.Complete || len(rest.Entries) != 5 || rest.Entries[0].Path != "a-z" {
		t.Fatalf("incorrect resumed entries %+v", rest)
	}
	request.AfterPath = ""
	request.WorkAllowance = 1
	exhausted, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if exhausted.Complete || exhausted.WorkUsed != 1 || exhausted.Reason != ledger.ReasonWorkExhausted || len(exhausted.Entries) != 0 {
		t.Fatalf("work boundary %+v", exhausted)
	}
	request.WorkAllowance = 10000
	request.WorkspaceID = uuid.NewV7()
	if out, err := f.svc.ReadArtifacts(context.Background(), request); !errors.Is(err, ErrUnknownWorkspace) || out.Complete {
		t.Fatalf("unregistered workspace %+v %v", out, err)
	}
}

func TestReadArtifactsMissingOversizeAndCancellation(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	missing := strings.Repeat("c", 40)
	tree := artifactTree(t, f, "100644 blob "+missing+"\tmissing.py\x00")
	request := ArtifactRequest{WorkspaceID: f.w.ID, Tree: tree, MaxBytes: 8 << 20, WorkAllowance: 10000}
	out, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Complete || len(out.Entries) != 1 || out.Entries[0].Reason != ledger.ReasonSourceUnavailable || out.Entries[0].Bytes != nil {
		t.Fatalf("missing object was success %+v", out)
	}
	large := artifactBlob(t, f, bytes.Repeat([]byte("x"), (1<<20)+1))
	request.Tree = artifactTree(t, f, "100644 blob "+large+"\tlarge.py\x00")
	out, err = f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Complete || out.Reason != "" || out.NextPath != "large.py" || len(out.Entries) != 1 || out.Entries[0].Reason != ledger.ReasonHelperLimit || out.Entries[0].Bytes != nil || out.Entries[0].SHA256 != (ledger.Digest{}) {
		t.Fatalf("oversized object did not advance as unknown %+v", out)
	}
	small := artifactBlob(t, f, []byte("after"))
	request.Tree = artifactTree(t, f, "100644 blob "+large+"\tlarge.py\x00"+"100644 blob "+small+"\tz.py\x00")
	request.MaxBytes = 4
	out, err = f.svc.ReadArtifacts(context.Background(), request)
	if err != nil || out.Complete || out.Reason != ledger.ReasonTemporaryCap || out.NextPath != "large.py" || len(out.Entries) != 1 || out.Entries[0].Reason != ledger.ReasonHelperLimit {
		t.Fatalf("oversized entry blocked the cursor %+v %v", out, err)
	}
	request.AfterPath, request.MaxBytes = out.NextPath, 5
	out, err = f.svc.ReadArtifacts(context.Background(), request)
	if err != nil || !out.Complete || out.NextPath != "z.py" || len(out.Entries) != 1 || string(out.Entries[0].Bytes) != "after" || out.Entries[0].SHA256 != ledger.Digest(sha256.Sum256([]byte("after"))) {
		t.Fatalf("entry after oversized object unreadable %+v %v", out, err)
	}
	request.AfterPath = ""
	request.Tree = missing
	if out, err := f.svc.ReadArtifacts(context.Background(), request); err == nil || out.Complete {
		t.Fatalf("missing tree completed %+v %v", out, err)
	}
	request.Tree = tree
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var child *exec.Cmd
	f.svc.git.afterStart = func(cmd *exec.Cmd) { child = cmd; cancel() }
	out, err = f.svc.ReadArtifacts(ctx, request)
	if !errors.Is(err, context.Canceled) || out.Complete || child == nil || child.ProcessState == nil {
		t.Fatalf("cancel did not join child %+v %v", out, err)
	}
	f.svc.git.afterStart = nil
	out, err = f.svc.ReadArtifacts(context.Background(), request)
	if err != nil || !out.Complete {
		t.Fatalf("replacement could not run after wait %+v %v", out, err)
	}
}

func TestReadArtifactsLargeTreePages(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	blob := artifactBlob(t, f, []byte("x"))
	var rows strings.Builder
	for i := range 6000 {
		fmt.Fprintf(&rows, "100644 blob %s\t%05d.txt\x00", blob, i)
	}
	request := ArtifactRequest{WorkspaceID: f.w.ID, Tree: artifactTree(t, f, rows.String()), MaxBytes: 8 << 20, WorkAllowance: 10000}
	count, pages := 0, 0
	for {
		out, err := f.svc.ReadArtifacts(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Entries) == 0 || out.NextPath <= request.AfterPath || out.WorkUsed == 0 || out.WorkUsed > request.WorkAllowance {
			t.Fatalf("page made no bounded progress: entries=%d cursor=%q work=%d reason=%q", len(out.Entries), out.NextPath, out.WorkUsed, out.Reason)
		}
		for _, entry := range out.Entries {
			if entry.Path != fmt.Sprintf("%05d.txt", count) || string(entry.Bytes) != "x" || entry.Reason != "" {
				t.Fatalf("entry %d duplicated, skipped or unreadable: %+v", count, entry)
			}
			count++
		}
		pages++
		if out.Complete {
			break
		}
		if out.Reason != ledger.ReasonWorkExhausted {
			t.Fatalf("unexpected page boundary %q", out.Reason)
		}
		request.AfterPath = out.NextPath
	}
	if count != 6000 || pages < 2 {
		t.Fatalf("tree did not complete across pages: entries=%d pages=%d", count, pages)
	}
}

func TestReadArtifactsLongPath(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	blob := artifactBlob(t, f, []byte("long"))
	path := strings.Repeat("p", 9000)
	tree := artifactTree(t, f, "100644 blob "+blob+"\t"+path+"\x00")
	out, err := f.svc.ReadArtifacts(context.Background(), ArtifactRequest{WorkspaceID: f.w.ID, Tree: tree, MaxBytes: 8 << 20, WorkAllowance: 10000})
	if err != nil || !out.Complete || out.NextPath != path || len(out.Entries) != 1 || out.Entries[0].Path != path || string(out.Entries[0].Bytes) != "long" {
		t.Fatalf("long frozen path unreadable: entries=%d complete=%v reason=%q err=%v", len(out.Entries), out.Complete, out.Reason, err)
	}
}

func TestReadArtifactsListOnlyAndPathFilter(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	blob := artifactBlob(t, f, []byte("immutable"))
	link := artifactBlob(t, f, []byte("directory"))
	child := artifactTree(t, f, "100755 blob "+blob+"\tchild.py\x00")
	names := []string{":leading", "[literal]", "a..b", "a:b", "at@{", "caret^", "question?", "star*", "starX", "tilde~"}
	var rows strings.Builder
	for _, name := range names {
		fmt.Fprintf(&rows, "100644 blob %s\t%s\x00", blob, name)
	}
	fmt.Fprintf(&rows, "040000 tree %s\t-\x00"+"040000 tree %s\tdirectory\x00"+"120000 blob %s\tlink\x00", child, child, link)
	request := ArtifactRequest{WorkspaceID: f.w.ID, Tree: artifactTree(t, f, rows.String()), MaxBytes: 1, WorkAllowance: 10000, ListOnly: true}
	starts := 0
	f.svc.git.afterStart = func(*exec.Cmd) { starts++ }
	listed, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	all := append(slices.Clone(names), "-/child.py", "directory/child.py", "link")
	slices.Sort(all)
	var got []string
	for _, entry := range listed.Entries {
		got = append(got, entry.Path)
		if entry.Bytes != nil || entry.SHA256 != (ledger.Digest{}) {
			t.Fatalf("list-only read materialized %q", entry.Path)
		}
	}
	if !slices.Equal(got, all) || !listed.Complete || starts != 1 {
		t.Fatalf("list-only paths=%v complete=%v children=%d", got, listed.Complete, starts)
	}
	// None of these names is a glob, revision operator, directory prefix or
	// filesystem lookup. In particular, star* must not admit starX.
	exact := append(slices.Clone(names[:8]), names[9], "-/child.py")
	slices.Sort(exact)
	request.Paths = append(slices.Clone(exact), "absent", "directory", "link", "link/child.py", "zz-missing")
	slices.Sort(request.Paths)
	request.WorkAllowance = 4
	starts = 0
	got = nil
	for page := 0; ; page++ {
		out, err := f.svc.ReadArtifacts(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if out.NextPath <= request.AfterPath || out.WorkUsed == 0 || out.WorkUsed > request.WorkAllowance {
			t.Fatalf("filtered page did not charge and advance: %+v", out)
		}
		for _, entry := range out.Entries {
			got = append(got, entry.Path)
			if entry.Bytes != nil || entry.SHA256 != (ledger.Digest{}) {
				t.Fatalf("filtered listing materialized %q", entry.Path)
			}
			if entry.Path == "link" && entry.Reason != ledger.ReasonUnsupportedSyntax {
				t.Fatalf("symlink was admitted: %+v", entry)
			}
		}
		if out.Complete {
			if starts != page+1 || out.NextPath != "zz-missing" {
				t.Fatalf("completion lost missing answers or launched a blob reader: %+v children=%d pages=%d", out, starts, page+1)
			}
			break
		}
		if page > len(request.Paths) || out.Reason != ledger.ReasonWorkExhausted {
			t.Fatalf("filtered page did not stop at its budget: %+v", out)
		}
		request.AfterPath = out.NextPath
	}
	want := append(slices.Clone(exact), "link")
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("filtered names=%v want %v", got, want)
	}
	request.AfterPath, request.ListOnly, request.MaxBytes, request.WorkAllowance = "", false, 8<<20, 10000
	starts = 0
	out, err := f.svc.ReadArtifacts(context.Background(), request)
	if err != nil || !out.Complete || out.NextPath != "zz-missing" || starts != 2 {
		t.Fatalf("filtered blob page=%+v children=%d err=%v", out, starts, err)
	}
	for _, entry := range out.Entries {
		if entry.Path == "link" {
			if entry.Reason != ledger.ReasonUnsupportedSyntax || entry.Bytes != nil {
				t.Fatalf("followed symlink: %+v", entry)
			}
		} else if string(entry.Bytes) != "immutable" || entry.SHA256 != ledger.Digest(sha256.Sum256([]byte("immutable"))) {
			t.Fatalf("wrong exact-path bytes: %+v", entry)
		}
	}
	request.AfterPath = out.NextPath
	starts = 0
	out, err = f.svc.ReadArtifacts(context.Background(), request)
	if err != nil || !out.Complete || len(out.Entries) != 0 || starts != 0 {
		t.Fatalf("answered prefix was reread: %+v children=%d err=%v", out, starts, err)
	}
	// A gitlink's target need not be stored locally. Its special batch-check
	// answer still proves path presence, including after deletion/recreation.
	present := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "HEAD")))
	absent := strings.Repeat("f", len(present))
	request.AfterPath, request.Paths = "", []string{"submodule"}
	for _, oid := range []string{present, "", absent, "", present} {
		treeRows := ""
		if oid != "" {
			treeRows = "160000 commit " + oid + "\tsubmodule\x00"
		}
		request.Tree = artifactTree(t, f, treeRows)
		for _, listOnly := range []bool{true, false} {
			request.ListOnly = listOnly
			out, err := f.svc.ReadArtifacts(context.Background(), request)
			if err != nil || !out.Complete || out.NextPath != "submodule" || out.WorkUsed == 0 {
				t.Fatalf("gitlink presence page=%+v err=%v", out, err)
			}
			if oid == "" {
				if len(out.Entries) != 0 {
					t.Fatalf("deleted gitlink survived: %+v", out)
				}
				continue
			}
			if len(out.Entries) != 1 {
				t.Fatalf("present gitlink disappeared: %+v", out)
			}
			entry := out.Entries[0]
			if entry.Mode != "160000" || entry.GitOID != oid || entry.Reason != ledger.ReasonUnsupportedSyntax || entry.Bytes != nil || entry.SHA256 != (ledger.Digest{}) {
				t.Fatalf("gitlink lost its unknown identity: %+v", entry)
			}
		}
	}
	for _, invalid := range [][]string{{"b", "a"}, {"a", "a"}, {"../a"}, {"a\\b"}, {"a\x00b"}, make([]string, 10001)} {
		request.AfterPath, request.Paths = "", invalid
		if _, err := f.svc.ReadArtifacts(context.Background(), request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid exact paths accepted: %q: %v", invalid[:1], err)
		}
	}
}

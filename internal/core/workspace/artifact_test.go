package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os/exec"
	"reflect"
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
	if !rest.Complete || len(rest.Entries) != 5 || rest.Entries[0].Path != "a-z" || rest.WorkUsed <= 5 {
		t.Fatalf("resume did not scan/charge prefix %+v", rest)
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
	if out.Complete || out.Reason != ledger.ReasonHelperLimit || len(out.Entries) != 0 {
		t.Fatalf("oversized object read %+v", out)
	}
	request.Tree = artifactTree(t, f, "100644 blob "+missing+"\t"+strings.Repeat("p", 9000)+"\x00")
	out, err = f.svc.ReadArtifacts(context.Background(), request)
	if err != nil || out.Complete || out.Reason != ledger.ReasonHelperLimit || out.WorkUsed < 33 {
		t.Fatalf("oversized tree entry was not charged unknown %+v %v", out, err)
	}
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

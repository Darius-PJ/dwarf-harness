package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestValidationExactSubjects(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	scratch := mustScratch(t, f, uuid.NewV7())
	writer := mustWriter(t, f, testWriteBinding(scratch))
	writeTestFile(t, filepath.Join(scratch.Dir, ".gitattributes"), []byte("crlf.txt text eol=crlf\n"))
	writeTestFile(t, filepath.Join(scratch.Dir, "crlf.txt"), []byte("acknowledged\r\n"))
	observation := mustObserve(t, writer)
	selector := ValidationSelector{Kind: "observation", WorkspaceID: f.w.ID, ContextID: scratch.ContextID, ObservationID: observation.ID}
	subject, err := f.svc.ResolveValidationSubject(ctx, selector)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(scratch.Dir, "crlf.txt"), []byte("later staged lie\r\n"))
	gitCommand(t, f.env, scratch.Dir, nil, "add", "--", "crlf.txt")
	writeTestFile(t, filepath.Join(scratch.Dir, "crlf.txt"), []byte("even later working bytes\r\n"))
	root := canonicalTempDir(t)
	exported, err := f.svc.ExportValidationSubject(ctx, subject, root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(exported.Directory(), "crlf.txt")); !bytes.Equal(got, []byte("acknowledged\r\n")) {
		t.Fatalf("exported mutable scratch or lost conversion: %q", got)
	}
	files := map[string]ledger.ValidationFile{}
	digest, err := ledger.LoadFileManifest(bytes.NewReader(exported.Manifest()), 100, func(file ledger.ValidationFile) error { files[file.Path] = file; return nil })
	if err != nil || digest != sha256.Sum256(exported.Manifest()) || files["crlf.txt"].SHA256 != sha256.Sum256([]byte("acknowledged\r\n")) {
		t.Fatalf("raw export proof: %v %+v", err, files)
	}
	if subject.Descriptor().Tree != observation.Tree || subject.Descriptor().Commit != observation.Commit {
		t.Fatal("export changed immutable identity")
	}
	if _, err := os.Lstat(filepath.Join(root, "export")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("history area survived export: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(exported.Directory(), ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository history exposed: %v", err)
	}
	for _, bad := range []ValidationSelector{
		{Kind: "observation", WorkspaceID: f.w.ID, ContextID: scratch.ContextID, ObservationID: uuid.NewV7()},
		{Kind: "observation", WorkspaceID: f.w.ID, ContextID: uuid.NewV7(), ObservationID: observation.ID},
	} {
		if _, err := f.svc.ResolveValidationSubject(ctx, bad); err == nil {
			t.Fatal("unknown or foreign source accepted")
		}
	}
	canonical, err := f.svc.ResolveValidationSubject(ctx, ValidationSelector{Kind: "canonical", WorkspaceID: f.w.ID})
	if err != nil {
		t.Fatal(err)
	}
	gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", observation.Commit)
	empty := canonicalTempDir(t)
	if _, err := f.svc.ExportValidationSubject(ctx, canonical, empty, 1<<20); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale source: %v", err)
	}
	entries, err := os.ReadDir(empty)
	if err != nil || len(entries) != 0 {
		t.Fatalf("stale selector materialized files: %v %v", entries, err)
	}
}

package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func inspectionFiles(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	files := map[string][32]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "lock" {
			return nil
		}
		if entry.IsDir() {
			files[path] = [32]byte{}
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[path] = sha256.Sum256(data)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestInspectWorkspaceState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t)
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(f.colony, "scratch", uuid.NewV7().String())
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "candidate"), []byte("retain for normal recovery"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.InspectColony(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before := inspectionFiles(t, f.colony)
	view, err := InspectState(ctx, store, f.colony)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := view.ListWorkspaces()
	if err != nil || len(workspaces) != 1 || workspaces[0] != f.w {
		t.Fatalf("workspace: %+v %v", workspaces, err)
	}
	proposal, err := view.RegisteredInputs(f.w.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CaptureInputsDigest(proposal)
	if err != nil {
		t.Fatal(err)
	}
	want, err := CaptureInputsDigest(f.p)
	if err != nil || got != want {
		t.Fatalf("retained inputs differ: %v", err)
	}
	adapter := inspectionStore{store}
	if _, err := adapter.Append(ctx, nil); !errors.Is(err, errInspectionReadOnly) {
		t.Fatalf("append: %v", err)
	}
	if _, _, err := adapter.PublishBody(ctx, strings.NewReader("must not persist")); !errors.Is(err, errInspectionReadOnly) {
		t.Fatalf("publish: %v", err)
	}
	if !reflect.DeepEqual(before, inspectionFiles(t, f.colony)) {
		t.Fatal("inspection changed colony state or cleaned orphan scratch")
	}
}

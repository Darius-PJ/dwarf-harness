package agents

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

func TestInspectPrivateState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	binding := registerPrivate(t, f.service, 10)
	ids, err := f.service.bindingLocked(binding, false)
	if err != nil {
		t.Fatal(err)
	}
	path, err := f.service.scopePath(ids, "agent")
	if err != nil {
		t.Fatal(err)
	}
	markdown, _ := path.markdownPath("instructions")
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, _, err := ledgerstore.InspectColony(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before := inspectionFiles(t, f.root)
	view, report, err := Inspect(ctx, store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := view.OwnerSetupSnapshot(ctx)
	if err != nil || len(snapshot.Contexts) != 1 || snapshot.Contexts[0].Binding != binding || len(report.Issues) != 0 || len(report.Completed) != 0 {
		t.Fatalf("inspect state: %+v %+v %v", snapshot, report, err)
	}
	adapter := inspectionStore{store}
	if _, err := adapter.Submit(ctx, ledger.Draft{}, nil); !errors.Is(err, errInspectionReadOnly) {
		t.Fatalf("submit: %v", err)
	}
	if _, err := adapter.SubmitAt(ctx, store.Durable(), ledger.Draft{}, nil); !errors.Is(err, errInspectionReadOnly) {
		t.Fatalf("submit at: %v", err)
	}
	if _, _, err := adapter.PublishBody(ctx, strings.NewReader("must not persist")); !errors.Is(err, errInspectionReadOnly) {
		t.Fatalf("publish: %v", err)
	}
	if err := view.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, inspectionFiles(t, f.root)) {
		t.Fatal("inspection changed colony bytes")
	}
	if err := os.Remove(markdown); err != nil {
		t.Fatal(err)
	}
	before = inspectionFiles(t, f.root)
	view, report, err = Inspect(ctx, store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if len(report.Completed) != 0 || len(report.Issues) != 1 || report.Issues[0].Reason != "stale_materialization" {
		t.Fatalf("missing materialization: %+v", report)
	}
	if !reflect.DeepEqual(before, inspectionFiles(t, f.root)) {
		t.Fatal("inspection repaired materialization")
	}
}

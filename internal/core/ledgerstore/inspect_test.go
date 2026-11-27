package ledgerstore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
)

func TestInspectColony(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "colony")
	doc, err := setup.Review(ctx, setup.Services{}, root, "colony create", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Apply(ctx, setup.Services{}, root, doc, doc.ReviewSHA256); err != nil {
		t.Fatal(err)
	}
	s, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	through := s.Durable()
	digest, _, err := s.PublishBody(ctx, bytes.NewBufferString("retained"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ledgerstore.InspectColony(root, ledger.Defined()); !errors.Is(err, ledgerstore.ErrLocked) {
		t.Fatalf("held inspection: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	pendingBody := filepath.Join(root, "bodies", ".tmp", "unfinished")
	if err := os.WriteFile(pendingBody, []byte("retain unfinished publication"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, report, err := ledgerstore.InspectColony(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	if report.TruncatedBytes != 0 || inspection.Colony() != doc.ColonyID || inspection.Durable() != through {
		t.Fatalf("inspection prefix: %+v", report)
	}
	state, err := setup.Load(ctx, inspection)
	if err != nil || !state.Managed || state.ThroughSequence != through {
		t.Fatalf("managed inspection: %+v %v", state, err)
	}
	_, err = setup.LoadConfiguration(ctx, inspection, nil)
	var failure *setup.Failure
	if !errors.As(err, &failure) || failure.Code != "setup_required" {
		t.Fatalf("incomplete managed configuration: %v", err)
	}
	body, err := inspection.ReadBody(ctx, digest)
	if err != nil || string(body) != "retained" {
		t.Fatalf("body: %q %v", body, err)
	}
	if _, _, err := ledgerstore.Open(root, ledger.Defined()); !errors.Is(err, ledgerstore.ErrLocked) {
		t.Fatalf("inspection lost lock: %v", err)
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	checked, err := ledger.Check(filepath.Join(root, "log"), doc.ColonyID)
	if err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(root, "log", checked.Segment)
	if raw, err := os.ReadFile(pendingBody); err != nil || string(raw) != "retain unfinished publication" {
		t.Fatalf("inspection swept pending body: %v", err)
	}
	before, err := os.ReadFile(segment)
	if err != nil {
		t.Fatal(err)
	}
	torn := append(append([]byte{}, before...), []byte(`{"torn":`)...)
	if err := os.WriteFile(segment, torn, 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, report, err = ledgerstore.InspectColony(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	if report.TruncatedBytes != int64(len(torn)-len(before)) || inspection.Durable() != through {
		t.Fatalf("torn report: %+v", report)
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(segment)
	if err != nil || !bytes.Equal(after, torn) {
		t.Fatalf("inspection repaired tail: %v", err)
	}
	lock := filepath.Join(root, "lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ledgerstore.InspectColony(root, ledger.Defined()); !errors.Is(err, ledgerstore.ErrLockMissing) {
		t.Fatalf("missing lock: %v", err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created lock: %v", err)
	}
}

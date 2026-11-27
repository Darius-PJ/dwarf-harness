package ledgerstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestSetupColonyCreation(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "colony")
	if _, err := CreateWithID(dir, ledger.Defined(), uuid.UUID{}); !errors.Is(err, ledger.ErrInvalid) {
		t.Fatalf("invalid identity: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid identity created directory: %v", err)
	}
	id := uuid.NewV7()
	s, err := CreateWithID(dir, ledger.Defined(), id)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		commandID := uuid.NewV7()
		payload, err := (ledger.Command{CommandID: commandID, SubmissionSHA256: ledger.Digest{1}}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Submit(context.Background(), ledger.Draft{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorOwner, OperationID: commandID, Payload: payload}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, _, err = Open(dir, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Colony() != id || s.Durable() != 2 {
		t.Fatal("reviewed identity did not survive reopen")
	}
	reader, err := ledger.OpenReader(s.LogDir(), ledger.ReadOptions{Colony: id, Through: s.Durable(), Consume: ledger.Consumption{ledger.TypeCommand: {1}}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	first, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.Records[0].CommitID == second.Records[0].CommitID || first.Records[0].CommitID == id || second.Records[0].CommitID == id {
		t.Fatal("reviewed colony identity was reused for commits")
	}
	if _, err := CreateWithID(dir, ledger.Defined(), uuid.NewV7()); !errors.Is(err, ErrExists) {
		t.Fatalf("replacement identity: %v", err)
	}
}

package agents

import (
	"context"
	"errors"
	"io"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

var errInspectionReadOnly = errors.New("agents: inspection is read-only")

type inspectionReader interface {
	ReadBody(context.Context, ledger.Digest) ([]byte, error)
	LogDir() string
	Colony() uuid.UUID
	Durable() uint64
}

// The adapter makes any accidental replay write fail before reaching storage.
// The inspection never returns it or the enclosed Service to callers.
type inspectionStore struct{ inspectionReader }

func (inspectionStore) Submit(context.Context, ledger.Draft, []ledger.Draft) (ledgerstore.Receipt, error) {
	return ledgerstore.Receipt{}, errInspectionReadOnly
}
func (inspectionStore) SubmitAt(context.Context, uint64, ledger.Draft, []ledger.Draft) (ledgerstore.Receipt, error) {
	return ledgerstore.Receipt{}, errInspectionReadOnly
}
func (inspectionStore) PublishBody(context.Context, io.Reader) (ledger.Digest, int64, error) {
	return ledger.Digest{}, 0, errInspectionReadOnly
}

// Inspection is an owner-only frozen view, with no mutation entrance. It borrows
// the caller's locked ledger inspection and never creates a paid runtime.
type Inspection struct{ service *Service }

// Inspect reuses native replay and verifies private objects/materializations
// without completing missing or stale files. It starts no scheduler or timer.
func Inspect(ctx context.Context, store inspectionReader, colonyDir string, limits Limits) (*Inspection, Reconciliation, error) {
	if store == nil {
		return nil, Reconciliation{}, ErrInvalid
	}
	s, report, err := openServiceMode(ctx, inspectionStore{store}, colonyDir, limits, serviceDeps{}, false)
	if err != nil {
		return nil, report, err
	}
	return &Inspection{service: s}, report, nil
}

func (i *Inspection) OwnerSetupSnapshot(ctx context.Context) (SetupSnapshot, error) {
	return i.service.OwnerSetupSnapshot(ctx)
}
func (i *Inspection) Snapshot(ctx context.Context, binding ReadBinding) (Snapshot, error) {
	return i.service.Snapshot(ctx, binding)
}
func (i *Inspection) Close() error { return i.service.Close() }

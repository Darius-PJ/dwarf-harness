package workspace

import (
	"context"
	"errors"
	"io"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

var errInspectionReadOnly = errors.New("workspace: inspection is read-only")

type inspectionReader interface {
	ReadBody(context.Context, ledger.Digest) ([]byte, error)
	LogDir() string
	Colony() uuid.UUID
	Durable() uint64
}

type inspectionStore struct{ inspectionReader }

func (inspectionStore) Append(context.Context, []ledger.Draft) (appender.Committed, error) {
	return appender.Committed{}, errInspectionReadOnly
}
func (inspectionStore) PublishBody(context.Context, io.Reader) (ledger.Digest, int64, error) {
	return ledger.Digest{}, 0, errInspectionReadOnly
}

// Inspection borrows a locked ledger prefix and exposes only copied owner
// metadata. No process owner, activation timer or filesystem layout is opened.
type Inspection struct{ service *Service }

// InspectState uses the native reducers and retained-input loader, but never
// reconciles scratch, finalizations, refs, process fences or materializations.
func InspectState(ctx context.Context, store inspectionReader, colonyDir string) (*Inspection, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	profile := ProvisionalCapacityProfile()
	s, _, err := openServiceMode(ctx, inspectionStore{store}, colonyDir, Config{Capacity: &profile}, serviceDeps{}, true)
	if err != nil {
		return nil, err
	}
	return &Inspection{service: s}, nil
}

func (i *Inspection) ListWorkspaces() ([]Workspace, error) { return i.service.ListWorkspaces() }
func (i *Inspection) ActivePolicy(ctx context.Context) (ActivePolicy, error) {
	return i.service.ActivePolicy(ctx, ledger.ActorOwner)
}

// Activations includes pending and completed requests, in identity order.
func (i *Inspection) Activations() []ActivationState {
	out := make([]ActivationState, 0, len(i.service.activations))
	for _, id := range sortedIDs(i.service.activations) {
		out = append(out, i.service.activationViewLocked(i.service.activations[id]))
	}
	return out
}

// RegisteredInputs returns detached retained input bytes. It does not inspect
// current Git configuration; the caller may use Inspect for that read-only step.
func (i *Inspection) RegisteredInputs(id uuid.UUID) (Proposal, error) {
	w, ok := i.service.workspaces[id]
	if !ok {
		return Proposal{}, ErrUnknownWorkspace
	}
	if w.inputsError != "" {
		return Proposal{}, &UnavailableError{Reason: w.inputsError}
	}
	p := w.inputs
	for _, file := range []*HostFile{&p.LocalExcludes, &p.GlobalExcludes, &p.LocalAttributes, &p.GlobalAttributes, &p.SystemAttributes} {
		file.Bytes = slices.Clone(file.Bytes)
	}
	return p, nil
}

// RecoveryPending names workspaces with an unresolved native train. Inspection
// does not infer closure or acceptance from the repository's current refs.
func (i *Inspection) RecoveryPending() []uuid.UUID {
	var out []uuid.UUID
	for _, id := range sortedIDs(i.service.recoveryPending) {
		if i.service.recoveryPending[id] {
			out = append(out, id)
		}
	}
	return out
}

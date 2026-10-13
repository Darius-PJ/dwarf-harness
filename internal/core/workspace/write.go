package workspace

import (
	"context"
	"fmt"
	"uuid"
)

func (e *captureEngine) observationCommit(ctx context.Context, s captureScratch, tree, parent string, scratchID, observationID uuid.UUID) (string, error) {
	if !validID(scratchID) {
		return "", fmt.Errorf("%w: invalid scratch identity", ErrInvalid)
	}
	message := fmt.Sprintf("dwarf-harness scratch observation\n\nscratch %s\nobservation %s", scratchID, observationID)
	return e.deterministicCommit(ctx, s, tree, parent, observationID, message)
}

func (e *captureEngine) mergeBackCommit(ctx context.Context, s captureScratch, tree, parent string, scratchID, mergeBackID uuid.UUID) (string, error) {
	if !validID(scratchID) {
		return "", fmt.Errorf("%w: invalid scratch identity", ErrInvalid)
	}
	message := fmt.Sprintf("dwarf-harness scratch merge-back\n\nscratch %s\nmerge-back %s", scratchID, mergeBackID)
	return e.deterministicCommit(ctx, s, tree, parent, mergeBackID, message)
}

func (g *gitRunner) createScratchCommitRef(ctx context.Context, colony string, p Proposal, id uuid.UUID, commit string) error {
	return g.createCommitRef(ctx, colony, p, "scratch-commits", id, commit)
}

func (g *gitRunner) deleteScratchCommitRef(ctx context.Context, colony string, p Proposal, id uuid.UUID, commit string) error {
	return g.deleteCommitRef(ctx, colony, p, "scratch-commits", id, commit)
}

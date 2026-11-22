package workspace

import (
	"bytes"
	"errors"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
)

// ProcessClosure is one positive Git process closure proof: live, retained_job
// or name_absent. It names a run, never a path, PID, handle or fence.
type ProcessClosure struct {
	RunID uuid.UUID
	Proof string
}

// ClassifyExecutionFailure reports only whether err carries the gitprocess
// unconfirmed class. runID is the first UnconfirmedError's RunID when it is a
// UUIDv7, else zero; a bare class names no run. Every other error, including
// exits, limits, closure, busy and Git unavailability, is not blocked.
func ClassifyExecutionFailure(err error) (blocked bool, runID uuid.UUID) {
	if !errors.Is(err, gitprocess.ErrUnconfirmed) {
		return false, uuid.UUID{}
	}
	var unconfirmed *gitprocess.UnconfirmedError
	if errors.As(err, &unconfirmed) && validID(unconfirmed.RunID) {
		return true, unconfirmed.RunID
	}
	return true, uuid.UUID{}
}

// ProcessProofSupported reports whether this Owner proves descendant tree
// closure. Where it does not, no closure proof can ever exist.
func (s *Service) ProcessProofSupported() bool {
	return s != nil && s.treeProof
}

// processClosures applies Open's success-path rule: Reconcile's closed runs by
// RunID bytes, then the first proven run Open itself completed. Any unconfirmed
// run during Open means the Owner is retained again and nothing is reported.
func (s *Service) processClosures(reconciled gitprocess.Report) []ProcessClosure {
	first, unconfirmed := s.git.proofRecord()
	if unconfirmed {
		return nil
	}
	var out []ProcessClosure
	for _, r := range reconciled.Closed {
		if validID(r.RunID) && validProcessProof(r.Proof) {
			out = append(out, ProcessClosure{RunID: r.RunID, Proof: r.Proof})
		}
	}
	slices.SortFunc(out, func(a, b ProcessClosure) int { return bytes.Compare(a.RunID[:], b.RunID[:]) })
	if validID(first.RunID) && validProcessProof(first.Proof) {
		out = append(out, ProcessClosure{RunID: first.RunID, Proof: first.Proof})
	}
	return out
}

func validProcessProof(proof string) bool {
	return proof == "live" || proof == "retained_job" || proof == "name_absent"
}

package workspace

import (
	"bytes"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// CaptureInputsDigest identifies exactly the approved capture inputs. Repository
// identity is separate: the same inputs can be approved for distinct repositories.
func CaptureInputsDigest(p Proposal) (ledger.Digest, error) {
	_, digest, err := encodeCaptureInputs(p, "capture-rules/1")
	return ledger.Digest(digest), err
}

// ListWorkspaces returns the owner's current registrations in UUID order.
// The caller authenticates the owner; registration confers no permission.
func (s *Service) ListWorkspaces() ([]Workspace, error) {
	if err := s.start(); err != nil {
		return nil, err
	}
	defer s.ops.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.poisoned {
		return nil, ErrPoisoned
	}
	out := make([]Workspace, 0, len(s.workspaces))
	for _, w := range s.workspaces {
		out = append(out, w.Workspace)
	}
	slices.SortFunc(out, func(a, b Workspace) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	return out, nil
}

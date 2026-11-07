package agents

import (
	"bytes"
	"context"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) ScopedCommandStatus(ctx context.Context, request ScopedCommandLookup) (ScopedReceipt, error) {
	return s.scopedCommandStatus(ctx, nil, request)
}
func (a *Agent) ScopedCommandStatus(ctx context.Context, request ScopedCommandLookup) (ScopedReceipt, error) {
	if a == nil || a.service == nil {
		return ScopedReceipt{}, ErrNotBound
	}
	return a.service.scopedCommandStatus(ctx, &a.ids, request)
}
func (s *Service) scopedCommandStatus(ctx context.Context, bound *submissionIDs, request ScopedCommandLookup) (ScopedReceipt, error) {
	reads := &historyReadSet{}
	done, err := s.scopedQuery(ctx, reads, bound)
	if err != nil {
		return ScopedReceipt{}, err
	}
	defer done()
	if !validID(request.CommandID) || request.SubmissionSHA256 == (ledger.Digest{}) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return ScopedReceipt{}, ErrInvalid
	}
	authority := func(reads *historyReadSet) error {
		if bound != nil {
			if !s.p.registeredOrigin(originOf(*bound)) {
				return ErrNotBound
			}
			_, err := s.currentChain(ctx, reads, originOf(*bound), request.GrantID, s.p.through)
			return err
		}
		return nil
	}
	if err := s.prepareHistoryLocked(ctx, reads, bound, true, authority); err != nil {
		return ScopedReceipt{}, err
	}
	through := s.p.through
	old := bytes.Compare(request.CommandID[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var match commandMatch
	if old {
		match, err = s.findCommand(ctx, nil, request.CommandID, 0, through)
	}
	s.mu.Lock()
	if err != nil {
		return ScopedReceipt{}, err
	}
	prior := match
	var receipt ScopedReceipt
	err = s.prepareHistoryLocked(ctx, reads, bound, true, func(reads *historyReadSet) error {
		match, receipt = prior, ScopedReceipt{}
		if err := authority(reads); err != nil {
			return err
		}
		if !match.found && through < s.p.through {
			var err error
			match, err = s.findCommand(ctx, reads, request.CommandID, through, s.p.through)
			if err != nil {
				return err
			}
		}
		if match.scoped == nil {
			if bound != nil {
				return ErrDenied
			}
			return ErrUnavailable
		}
		if bound != nil {
			sub := match.scoped.receipt.Submission
			if sub.Actor != "agent" || sub.Origin != originOf(*bound) {
				return ErrDenied
			}
			// Current authority permits disclosure, not a rewritten receipt.
			sub.GrantID = request.GrantID
			if err := s.scopedAuthority(ctx, reads, sub); err != nil {
				return err
			}
		}
		if match.command.SubmissionSHA256 != request.SubmissionSHA256 {
			return ErrCommandMismatch
		}
		receipt = scopedReceipt(match.scoped)
		return nil
	})
	return receipt, err
}

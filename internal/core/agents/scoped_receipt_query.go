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
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return ScopedReceipt{}, err
	}
	defer done()
	if !validID(request.CommandID) || request.SubmissionSHA256 == (ledger.Digest{}) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return ScopedReceipt{}, ErrInvalid
	}
	if bound != nil {
		if _, err := s.currentChain(ctx, originOf(*bound), request.GrantID, s.p.through); err != nil {
			return ScopedReceipt{}, err
		}
	}
	through := s.p.through
	old := bytes.Compare(request.CommandID[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var match commandMatch
	if old {
		match, err = s.findCommand(ctx, request.CommandID, 0, through)
	}
	s.mu.Lock()
	if err != nil {
		return ScopedReceipt{}, err
	}
	if err := s.usableLocked(ctx); err != nil {
		return ScopedReceipt{}, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return ScopedReceipt{}, err
	}
	if bound != nil {
		if _, err := s.currentChain(ctx, originOf(*bound), request.GrantID, s.p.through); err != nil {
			return ScopedReceipt{}, err
		}
	}
	if !match.found && through < s.p.through {
		match, err = s.findCommand(ctx, request.CommandID, through, s.p.through)
		if err != nil {
			return ScopedReceipt{}, err
		}
	}
	if match.scoped == nil {
		if bound != nil {
			return ScopedReceipt{}, ErrDenied
		}
		return ScopedReceipt{}, ErrUnavailable
	}
	if bound != nil {
		sub := match.scoped.receipt.Submission
		if sub.Actor != "agent" || sub.Origin != originOf(*bound) {
			return ScopedReceipt{}, ErrDenied
		}
		// The caller selects current authority; the returned historical receipt
		// retains the original grant and semantic digest, never a new permit.
		sub.GrantID = request.GrantID
		if err := s.scopedAuthority(ctx, sub); err != nil {
			return ScopedReceipt{}, err
		}
	}
	if match.command.SubmissionSHA256 != request.SubmissionSHA256 {
		return ScopedReceipt{}, ErrCommandMismatch
	}
	return scopedReceipt(match.scoped), nil
}

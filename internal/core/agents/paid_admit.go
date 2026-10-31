package agents

import (
	"context"
	"errors"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func (a *Agent) AdmitPaid(ctx context.Context, request PaidAdmission) (PaidReceipt, *BoundCall, error) {
	if a == nil || a.service == nil {
		return PaidReceipt{}, nil, ErrNotBound
	}
	s := a.service
	if err := s.enter(ctx); err != nil {
		return PaidReceipt{}, nil, err
	}
	defer s.active.Done()
	if !validID(request.CommandID) || request.Prepared == nil || request.Prepared.service != s || request.Prepared.state == nil {
		return PaidReceipt{}, nil, ErrInvalid
	}
	p := request.Prepared.state
	s.mu.Lock()
	if !paidAdmissionMatches(p, a.ids, request) {
		s.mu.Unlock()
		return PaidReceipt{}, nil, ErrDenied
	}
	sub := ledger.PaidSubmission{Schema: 4, Actor: "agent", Origin: p.origin, Operation: "paid.admit", GrantID: request.GrantID, Arguments: paidAdmitArguments(p.admission)}
	digest, err := paidDigest(sub)
	s.mu.Unlock()
	if err != nil {
		return PaidReceipt{}, nil, err
	}
	authority := func() error {
		if p.closed && !p.consumed {
			return ErrClosed
		}
		if !s.p.registeredOrigin(originOf(a.ids)) {
			return ErrDenied
		}
		_, err := s.livePaidAuthority(ctx, p)
		return err
	}
	match, err := s.lookupPaid(ctx, request.CommandID, authority)
	if err == nil && match.found {
		r, e := s.matchPaid(match, sub, digest)
		var bound *BoundCall
		if e == nil {
			bound = &BoundCall{service: s, admission: *match.paid.reserved}
			if !p.consumed {
				s.releasePaidPreparation(p)
			}
		}
		s.mu.Unlock()
		return r, bound, e
	}
	if err == nil && p.consumed {
		err = ErrNoEffect
	}
	if err == nil && p.inUse {
		err = &BusyError{Reason: "paid_preparing"}
	}
	if err == nil {
		err = s.admissionGuardsLocked(ctx, p)
	}
	if err != nil {
		s.mu.Unlock()
		return PaidReceipt{}, nil, err
	}
	p.inUse = true
	body := p.body
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		p.inUse = false
		if p.closeRequested {
			s.releasePaidPreparation(p)
		}
		s.mu.Unlock()
	}()

	// Publish immutable input bytes outside mu. A refusal can leave an
	// unreferenced content-addressed object, never a ledger hold or receipt.
	release, err := s.acquireScope(ctx, keyFor(a.ids, "context"))
	if err != nil {
		return PaidReceipt{}, nil, err
	}
	defer release()
	scope, err := s.scopePath(a.ids, "context")
	if err == nil && s.deps.beforePublish != nil {
		err = s.deps.beforePublish(bindingOf(p.origin), "paid_input")
	}
	if err == nil {
		var hash ledger.Digest
		hash, err = scope.publishObject(body)
		if err == nil && hash != p.admission.InputObject.SHA256 {
			err = ErrIntegrity
		}
	}
	if err == nil && s.deps.afterPublish != nil {
		s.deps.afterPublish(bindingOf(p.origin), "paid_input")
	}
	if err != nil {
		return PaidReceipt{}, nil, err
	}

	match, err = s.lookupPaid(ctx, request.CommandID, authority)
	defer s.mu.Unlock()
	// The admission carries its clock sample, which another writer's later
	// commit time would raise the review floor above. A refusal at a moved tail
	// re-enters at the refresh and samples again; nothing was written.
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			match, err = s.relookupPaidLocked(ctx, request.CommandID, authority)
		}
		if err == nil && match.found {
			r, e := s.matchPaid(match, sub, digest)
			if e != nil {
				return PaidReceipt{}, nil, e
			}
			if !p.consumed {
				s.releasePaidPreparation(p)
			}
			return r, &BoundCall{service: s, admission: *match.paid.reserved}, nil
		}
		if err == nil && p.closeRequested {
			err = ErrClosed
		}
		if err == nil {
			err = s.admissionGuardsLocked(ctx, p)
		}
		var drafts []ledger.Draft
		if err == nil {
			drafts, err = paidDrafts(request.CommandID, sub, p.admission, ledger.TypeRuntimePaidReserved, "reserved", 1, request.AttemptID, request.ParentTurnID, nil)
		}
		var receipt PaidReceipt
		if err == nil {
			receipt, err = s.submitPaidGroupLocked(ctx, drafts, true)
		}
		if errors.Is(err, ledgerstore.ErrTailMoved) {
			if attempt == tailAttempts {
				return PaidReceipt{}, nil, &BusyError{Reason: "ledger_tail"}
			}
			continue
		}
		if err != nil {
			return PaidReceipt{}, nil, err
		}
		p.consumed = true
		delete(s.paid.prepared, p)
		s.paid.calls[request.AttemptID] = p
		return receipt, &BoundCall{service: s, admission: p.admission}, nil
	}
}

func (s *Service) admissionGuardsLocked(ctx context.Context, p *preparedPaid) error {
	if p.closed {
		return ErrClosed
	}
	if p.consumed {
		return ErrNoEffect
	}
	if _, err := s.livePaidAuthority(ctx, p); err != nil {
		return err
	}
	sample, err := s.samplePaidLocked()
	if err != nil {
		return err
	}
	profile := providerProfile(s.p.paidRoutes[p.admission.RouteID].Profile)
	if err = profile.Current(sample); err != nil {
		return err
	}
	if err = s.paidDestination(profile); err != nil {
		return err
	}
	if len(p.admission.Scopes) > s.paid.limits.MaxPaidScopes {
		return ErrTooLarge
	}
	unresolved := 0
	for _, a := range s.p.paidAttempts {
		if a.held {
			unresolved++
		}
	}
	if unresolved >= s.paid.limits.MaxUnresolvedPaid {
		return &BusyError{Reason: "paid_unresolved"}
	}
	if err = s.p.book.checkPreparation(p.reservation.scopes, p.reservation.budget, false); err != nil {
		return err
	}
	p.admission.AuthorityVersion = s.p.authority
	p.admission.AdmissionUTCNS = sample
	p.admission.AdmissionPeriod = paidPeriod(s.p.book.paidPeriodKind, sample)
	_, _, err = s.paidCurrent(ctx, paidAttempt{admission: p.admission, origin: p.origin}, sample, s.p.through, p.admission.Quote.MaximumMicros)
	return err
}

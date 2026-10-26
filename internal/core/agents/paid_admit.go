package agents

import (
	"context"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
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
		if !s.p.registeredOrigin(originOf(a.ids)) || p.closed && !p.consumed {
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
			bound = &BoundCall{service: s, admission: p.admission}
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
	if err == nil && match.found {
		r, e := s.matchPaid(match, sub, digest)
		if e != nil {
			return PaidReceipt{}, nil, e
		}
		return r, &BoundCall{service: s, admission: p.admission}, nil
	}
	if err == nil && p.closeRequested {
		err = ErrClosed
	}
	if err == nil {
		err = s.admissionGuardsLocked(ctx, p)
	}
	if err != nil {
		return PaidReceipt{}, nil, err
	}
	drafts, err := paidDrafts(request.CommandID, sub, p.admission, ledger.TypeRuntimePaidReserved, "reserved", 1, request.AttemptID, request.ParentTurnID, nil)
	if err != nil {
		return PaidReceipt{}, nil, err
	}
	receipt, err := s.submitPaidLocked(ctx, drafts)
	if err != nil {
		return PaidReceipt{}, nil, err
	}
	p.consumed = true
	delete(s.paid.prepared, p)
	s.paid.calls[request.AttemptID] = p
	return receipt, &BoundCall{service: s, admission: p.admission}, nil
}

func (s *Service) admissionGuardsLocked(ctx context.Context, p *preparedPaid) error {
	if p.closed || p.consumed {
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
	if profile.Authority != "api.anthropic.com" && profile.Authority != s.deps.paidTestAuthority {
		return providers.ErrUnsupported
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

// ReconcilePaid is a factual host ending. It never checks a source or clock and
// cannot release an attempt for which dispatch intent has become durable.
func (s *Service) ReconcilePaid(ctx context.Context, request PaidReconciliation) (PaidReceipt, error) {
	if s == nil {
		return PaidReceipt{}, ErrNotBound
	}
	if !validID(request.CommandID) || !validID(request.AttemptID) || request.ExpectedVersion <= 0 {
		return PaidReceipt{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return PaidReceipt{}, err
	}
	defer s.active.Done()
	var a paidAttempt
	// Recovery remains available through plain Open, not only OpenPaid.
	match, err := s.lookupPaid(ctx, request.CommandID, func() error {
		var found bool
		var err error
		a, found, err = s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
		if err == nil && !found {
			err = ErrDenied
		}
		return err
	})
	defer s.mu.Unlock()
	if err != nil {
		return PaidReceipt{}, err
	}
	if a.intent != nil {
		return PaidReceipt{}, ErrPaidSent
	}
	sub := ledger.PaidSubmission{Schema: 4, Actor: "core", Origin: a.origin, Operation: "paid.release", GrantID: a.admission.GrantID, Arguments: ledger.PaidAttemptArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion}}
	digest, err := paidDigest(sub)
	if err != nil {
		return PaidReceipt{}, err
	}
	if match.found {
		return s.matchPaid(match, sub, digest)
	}
	if a.version != request.ExpectedVersion {
		return PaidReceipt{}, ErrRevision
	}
	if a.ended || a.version != 1 {
		return PaidReceipt{}, ErrNoEffect
	}
	effect := ledger.RuntimePaidResult{AttemptID: request.AttemptID, ExpectedVersion: 1, Version: 2, Kind: "released_unsent"}
	drafts, err := paidDrafts(request.CommandID, sub, effect, ledger.TypeRuntimePaidResult, "released_unsent", 2, request.AttemptID, a.admission.ParentTurnID, nil)
	if err != nil {
		return PaidReceipt{}, err
	}
	receipt, err := s.submitPaidLocked(ctx, drafts)
	if err == nil && s.paid != nil {
		if p := s.paid.calls[request.AttemptID]; p != nil {
			s.releasePaidPreparation(p)
		}
	}
	return receipt, err
}

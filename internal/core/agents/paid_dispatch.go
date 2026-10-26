package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// DispatchPaid grants a send only to this invocation's freshly committed intent.
// Receipt lookup, recovery and BoundCall never reconstruct that permission.
func (s *Service) DispatchPaid(ctx context.Context, request PaidDispatch) (PaidDispatchOutcome, error) {
	var out PaidDispatchOutcome
	if s == nil {
		return out, ErrNotBound
	}
	if !validID(request.CommandID) || !validID(request.AttemptID) || request.ExpectedVersion <= 0 {
		return out, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return out, err
	}
	defer s.active.Done()
	var a paidAttempt
	authority := func() error {
		var found bool
		var err error
		a, found, err = s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
		if err != nil {
			return err
		}
		if !found {
			return ErrDenied
		}
		_, _, err = s.paidAuthority(ctx, a, s.p.through)
		return err
	}
	match, err := s.lookupPaid(ctx, request.CommandID, authority)
	sub := ledger.PaidSubmission{Schema: 4, Actor: "core", Origin: a.origin, Operation: "paid.dispatch", GrantID: a.admission.GrantID, Arguments: ledger.PaidAttemptArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion}}
	if err == nil && match.found {
		digest, e := paidDigest(sub)
		if e == nil {
			out.IntentReceipt, e = s.matchPaid(match, sub, digest)
		}
		if e == nil && a.result != nil {
			view, viewErr := s.paidView(a)
			out.Result, e = &view, viewErr
		}
		s.mu.Unlock()
		return out, e
	}
	if err == nil && (a.intent != nil || a.ended) {
		err = ErrPaidSent
	}
	if err == nil && a.version != request.ExpectedVersion {
		err = ErrRevision
	}
	if err == nil && s.paid == nil {
		err = ErrUnavailable
	}
	var p *preparedPaid
	if err == nil {
		p = s.paid.calls[request.AttemptID]
		if p == nil || p.closed || a.sequence <= s.openedThrough {
			err = ErrUnavailable
		} else if p.inUse || len(s.paid.exchanges) >= s.paid.limits.MaxActivePaid {
			err = &BusyError{Reason: "paid_active"}
		}
	}
	if err == nil {
		_, err = s.dispatchGuardsLocked(ctx, a, p)
	}
	if err != nil {
		s.mu.Unlock()
		return out, err
	}
	secret := s.paid.secrets[a.admission.Request.Credential.Version]
	if secret == nil {
		s.mu.Unlock()
		return out, ErrUnavailable
	}
	// S1 bounds the entity at 16 MiB and output at 8 MiB. Reserve the entity,
	// retained usage snapshots/output and bounded JSON encoding copies before
	// intent; this is physical occupancy, never a second monetary hold.
	buffer := budgetPreparation{hold: [4]int64{0, 0, 96 << 20, 0}}
	if err := s.p.book.reservePreparation(a.origin, p.reservation.scopes, buffer); err != nil {
		s.mu.Unlock()
		return out, err
	}
	exchangeCtx, cancel := context.WithTimeout(ctx, s.paid.limits.DispatchTimeout)
	s.paid.exchanges[request.AttemptID] = cancel
	p.inUse = true
	s.mu.Unlock()
	sent := false
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.paid.exchanges, request.AttemptID)
		s.p.book.releasePreparation(a.origin, p.reservation.scopes, buffer)
		p.inUse = false
		if sent || p.closeRequested {
			s.releasePaidPreparation(p)
		}
		s.mu.Unlock()
	}()

	// Borrow before the final transition. Revocation removes authorization under
	// mu, then releases the secret outside it, avoiding a lock-order inversion.
	err = secret.WithBytes(func(key []byte) error {
		s.mu.Lock()
		if e := s.paidUsableLocked(exchangeCtx); e != nil {
			s.mu.Unlock()
			return e
		}
		current, found, e := s.loadPaid(exchangeCtx, request.AttemptID, s.p.through, ledger.Digest{})
		if e == nil && (!found || current.version != request.ExpectedVersion) {
			e = ErrRevision
		}
		var sample int64
		if e == nil {
			sample, e = s.dispatchGuardsLocked(exchangeCtx, current, p)
		}
		if e == nil && paidContainsSecret(p.body, key) {
			e = ErrInvalid
		}
		if e != nil {
			s.mu.Unlock()
			return e
		}
		effect := ledger.RuntimePaidDispatchIntent{AttemptID: request.AttemptID, ExpectedVersion: current.version, Version: current.version + 1, AuthorityVersion: s.p.authority, RequestSHA256: current.admission.Quote.RequestSHA256, QuoteSHA256: current.admission.Quote.QuoteSHA256, Credential: current.admission.Request.Credential, ObservedUTCNS: sample}
		drafts, e := paidDrafts(request.CommandID, sub, effect, ledger.TypeRuntimePaidDispatchIntent, "sent", effect.Version, request.AttemptID, current.admission.ParentTurnID, nil)
		before := s.p.through
		if e == nil {
			out.IntentReceipt, e = s.submitPaidLocked(exchangeCtx, drafts)
		}
		if e == nil {
			sent = out.IntentReceipt.EffectSequence > before
		}
		s.mu.Unlock()
		if e != nil || !sent {
			return e
		}

		// Neither the request nor response.Request crosses this boundary. Errors
		// are classified without formatting them (URL errors can carry headers).
		response, networkErr := s.paid.transport.Do(exchangeCtx, p.envelope, key)
		var evidence providers.ResponseEvidence
		if response != nil {
			evidence, e = providers.ReadAnthropicResponse(response, providerProfile(current.profile), p.compiled)
			_ = response.Body.Close()
			var network net.Error
			if e != nil && (errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || errors.As(e, &network)) {
				evidence.ErrorClass = providers.ClassifyTransportError(e)
			}
		}
		if networkErr != nil {
			evidence.ErrorClass = providers.ClassifyTransportError(networkErr)
		}
		if !evidence.Stream.Complete && exchangeCtx.Err() != nil {
			evidence.ErrorClass = providers.ClassifyTransportError(exchangeCtx.Err())
		}
		out.ObservedPaidCall, e = s.publishPaidObservation(context.Background(), current, evidence, key)
		return e
	})
	return out, err
}

func (s *Service) dispatchGuardsLocked(ctx context.Context, a paidAttempt, p *preparedPaid) (int64, error) {
	if a.intent != nil || a.ended || a.version != 1 {
		return 0, ErrPaidSent
	}
	if p.closed || p.closeRequested || !p.consumed || !sameScopedValue(p.admission, a.admission) {
		return 0, ErrInvalid
	}
	if _, err := s.livePaidAuthority(ctx, p); err != nil {
		return 0, err
	}
	if err := s.p.book.checkPreparation(p.reservation.scopes, p.reservation.budget, false); err != nil {
		return 0, err
	}
	profile := providerProfile(a.profile)
	if profile.Authority != "api.anthropic.com" && profile.Authority != s.deps.paidTestAuthority {
		return 0, providers.ErrUnsupported
	}
	if err := checkPaidQuote(a.admission, a.profile); err != nil {
		return 0, err
	}
	body := p.envelope.Body()
	if !bytes.Equal(body, p.body) || !sameScopedValue(p.envelope.Descriptor(), paidDescriptor(a.admission.Request)) || ledger.Digest(sha256.Sum256(body)) != a.admission.InputObject.SHA256 || int64(len(body)) != a.admission.InputObject.Bytes || ledger.Digest(providers.RequestSHA256(p.envelope.Descriptor(), body)) != a.admission.Quote.RequestSHA256 || ledger.Digest(p.envelope.SHA256()) != a.admission.Quote.RequestSHA256 {
		return 0, ErrIntegrity
	}
	sample, err := s.samplePaidLocked()
	if err == nil {
		err = profile.Current(sample)
	}
	if err == nil {
		// The existing hold is counted once; extra is deliberately zero.
		_, _, err = s.paidCurrent(ctx, a, sample, s.p.through, 0)
	}
	return sample, err
}

package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// ReconcilePaid releases only a provably unsent reservation. A sent attempt ends
// as unknown, retaining its entire hold; reconciliation never performs I/O to a
// provider, resolves a credential, or samples a clock.
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
	reads := &historyReadSet{}
	var a paidAttempt
	authority := func(reads *historyReadSet) error {
		var found bool
		var err error
		a, found, err = s.loadPaid(ctx, reads, request.AttemptID, s.p.through, ledger.Digest{})
		if err == nil && !found {
			err = ErrDenied
		}
		return err
	}
	match, err := s.lookupPaid(ctx, reads, nil, request.CommandID, authority)
	if err != nil {
		s.mu.Unlock()
		return PaidReceipt{}, err
	}
	recovery := providers.ResponseEvidence{
		ErrorClass: "unreachable",
		ErrorBody:  json.RawMessage(`{"error":{"type":"recovery_reconciled","message":"Durable intent recovered without a committed result; delivery and billing are unknown."}}`),
	}
	var drafts []ledger.Draft
	var group *paidGroup
	var receipt PaidReceipt
	var observe, release bool
	ids := submissionIDs{a.origin.AgentID, a.origin.SessionID, a.origin.ContextID}
	err = s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		drafts, group, receipt, observe, release = nil, nil, PaidReceipt{}, false, false
		if err := authority(reads); err != nil {
			return err
		}
		var err error
		match, err = s.findCommand(ctx, reads, request.CommandID, 0, s.p.through)
		if err != nil {
			return err
		}
		if match.found && a.intent != nil {
			raw, err := json.Marshal(paidObservation(a, recovery))
			if err != nil {
				return err
			}
			ref := ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256(raw)), Bytes: int64(len(raw))}
			observation := PaidObservation{CommandID: request.CommandID, AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion}
			sub := paidObservationSubmission(a, observation, ref, "unknown", nil, nil)
			digest, err := paidDigest(sub)
			if err == nil {
				receipt, err = s.matchPaid(match, sub, digest)
			}
			return err
		}
		if s.paid != nil && s.paid.exchanges[request.AttemptID] != nil {
			return &BusyError{Reason: "paid_active"}
		}
		if a.intent != nil {
			if a.version != request.ExpectedVersion || a.ended {
				return ErrRevision
			}
			observe = true
			return nil
		}
		sub := ledger.PaidSubmission{Schema: 4, Actor: "core", Origin: a.origin, Operation: "paid.release", GrantID: a.admission.GrantID, Arguments: ledger.PaidAttemptArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion}}
		digest, err := paidDigest(sub)
		if err != nil {
			return err
		}
		if match.found {
			receipt, err = s.matchPaid(match, sub, digest)
			return err
		}
		if a.version != request.ExpectedVersion {
			return ErrRevision
		}
		if a.ended || a.version != 1 {
			return ErrNoEffect
		}
		effect := ledger.RuntimePaidResult{AttemptID: request.AttemptID, ExpectedVersion: 1, Version: 2, Kind: "released_unsent"}
		drafts, err = paidDrafts(request.CommandID, sub, effect, ledger.TypeRuntimePaidResult, "released_unsent", 2, request.AttemptID, a.admission.ParentTurnID, nil)
		if err == nil {
			drafts, group, err = s.preflightPaidLocked(ctx, reads, drafts)
		}
		release = err == nil
		return err
	})
	if err == nil && release {
		receipt, err = s.submitPaidGroupLocked(ctx, drafts, group, false)
		if err == nil && s.paid != nil {
			if p := s.paid.calls[request.AttemptID]; p != nil {
				s.releasePaidPreparation(p)
			}
		}
	}
	s.mu.Unlock()
	if err != nil || !observe {
		return receipt, err
	}
	observation, err := s.publishPaidObservation(ctx, a, recovery, nil)
	if err != nil {
		return PaidReceipt{}, err
	}
	return s.RecordPaidObservation(ctx, PaidObservation{CommandID: request.CommandID, AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, Observed: observation})
}

func (s *Service) AcknowledgePaidOverrun(ctx context.Context, origin Binding, request PaidOverrunAcknowledgement) (PaidReceipt, error) {
	if s == nil {
		return PaidReceipt{}, ErrNotBound
	}
	if request.GrantID != (uuid.UUID{}) || !validID(request.CommandID) || !validID(request.AttemptID) {
		return PaidReceipt{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return PaidReceipt{}, err
	}
	defer s.active.Done()
	reads := &historyReadSet{}
	ids, err := s.initialIDs(ctx, reads, origin, false)
	if err != nil {
		return PaidReceipt{}, err
	}
	sub := ledger.PaidSubmission{Schema: 4, Actor: "owner", Origin: originOf(ids), Operation: "paid.overrun.acknowledge", Arguments: ledger.PaidOverrunArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, ResultSequence: request.ResultSequence, EvidenceSHA256: request.EvidenceSHA256, RetiredProfileSHA256: request.RetiredProfileSHA256}}
	digest, err := paidDigest(sub)
	if err != nil {
		return PaidReceipt{}, err
	}
	authority := func(*historyReadSet) error {
		if !s.p.registeredOrigin(originOf(ids)) {
			return ErrNotBound
		}
		return nil
	}
	match, err := s.lookupPaid(ctx, reads, &ids, request.CommandID, authority)
	defer s.mu.Unlock()
	if err != nil {
		return PaidReceipt{}, err
	}
	if match.found {
		return s.matchPaid(match, sub, digest)
	}
	var drafts []ledger.Draft
	var group *paidGroup
	err = s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		drafts, group = nil, nil
		if err := authority(reads); err != nil {
			return err
		}
		var err error
		match, err = s.findCommand(ctx, reads, request.CommandID, 0, s.p.through)
		if err != nil || match.found {
			return err
		}
		a, found, err := s.loadPaid(ctx, reads, request.AttemptID, s.p.through, ledger.Digest{})
		if err != nil {
			return err
		}
		if !found {
			return ErrDenied
		}
		if a.version != request.ExpectedVersion {
			return ErrRevision
		}
		effect := ledger.RuntimePaidOverrunAcknowledged{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, Version: request.ExpectedVersion + 1, ResultSequence: request.ResultSequence, EvidenceSHA256: request.EvidenceSHA256, RetiredProfileSHA256: request.RetiredProfileSHA256}
		drafts, err = paidDrafts(request.CommandID, sub, effect, ledger.TypeRuntimePaidOverrunAcknowledged, "acknowledged", effect.Version, request.AttemptID, uuid.UUID{}, nil)
		if err == nil {
			drafts, group, err = s.preflightPaidLocked(ctx, reads, drafts)
		}
		return err
	})
	if err != nil {
		return PaidReceipt{}, err
	}
	if match.found {
		return s.matchPaid(match, sub, digest)
	}
	return s.submitPaidGroupLocked(ctx, drafts, group, false)
}

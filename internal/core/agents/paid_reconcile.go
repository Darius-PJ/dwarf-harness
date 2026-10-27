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
	var a paidAttempt
	match, err := s.lookupPaid(ctx, request.CommandID, func() error {
		var found bool
		var err error
		a, found, err = s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
		if err == nil && !found {
			err = ErrDenied
		}
		return err
	})
	if err != nil {
		s.mu.Unlock()
		return PaidReceipt{}, err
	}
	recovery := providers.ResponseEvidence{
		ErrorClass: "unreachable",
		ErrorBody:  json.RawMessage(`{"error":{"type":"recovery_reconciled","message":"Durable intent recovered without a committed result; delivery and billing are unknown."}}`),
	}
	if match.found && a.intent != nil {
		raw, err := json.Marshal(paidObservation(a, recovery))
		ref := ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256(raw)), Bytes: int64(len(raw))}
		observation := PaidObservation{CommandID: request.CommandID, AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion}
		sub := paidObservationSubmission(a, observation, ref, "unknown", nil, nil)
		var receipt PaidReceipt
		if err == nil {
			digest, e := paidDigest(sub)
			err = e
			if err == nil {
				receipt, err = s.matchPaid(match, sub, digest)
			}
		}
		s.mu.Unlock()
		return receipt, err
	}
	if s.paid != nil && s.paid.exchanges[request.AttemptID] != nil {
		s.mu.Unlock()
		return PaidReceipt{}, &BusyError{Reason: "paid_active"}
	}
	if a.intent != nil {
		if a.version != request.ExpectedVersion || a.ended {
			s.mu.Unlock()
			return PaidReceipt{}, ErrRevision
		}
		s.mu.Unlock()
		observation, err := s.publishPaidObservation(ctx, a, recovery, nil)
		if err != nil {
			return PaidReceipt{}, err
		}
		return s.RecordPaidObservation(ctx, PaidObservation{CommandID: request.CommandID, AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, Observed: observation})
	}
	defer s.mu.Unlock()
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
	ids, err := s.initialIDs(ctx, origin, false)
	if err != nil {
		return PaidReceipt{}, err
	}
	sub := ledger.PaidSubmission{Schema: 4, Actor: "owner", Origin: originOf(ids), Operation: "paid.overrun.acknowledge", Arguments: ledger.PaidOverrunArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, ResultSequence: request.ResultSequence, EvidenceSHA256: request.EvidenceSHA256, RetiredProfileSHA256: request.RetiredProfileSHA256}}
	digest, err := paidDigest(sub)
	if err != nil {
		return PaidReceipt{}, err
	}
	match, err := s.lookupPaid(ctx, request.CommandID, func() error {
		if !s.p.registeredOrigin(originOf(ids)) {
			return ErrNotBound
		}
		return nil
	})
	defer s.mu.Unlock()
	if err != nil {
		return PaidReceipt{}, err
	}
	if match.found {
		return s.matchPaid(match, sub, digest)
	}
	a, found, err := s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
	if err != nil {
		return PaidReceipt{}, err
	}
	if !found {
		return PaidReceipt{}, ErrDenied
	}
	if a.version != request.ExpectedVersion {
		return PaidReceipt{}, ErrRevision
	}
	effect := ledger.RuntimePaidOverrunAcknowledged{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, Version: request.ExpectedVersion + 1, ResultSequence: request.ResultSequence, EvidenceSHA256: request.EvidenceSHA256, RetiredProfileSHA256: request.RetiredProfileSHA256}
	drafts, err := paidDrafts(request.CommandID, sub, effect, ledger.TypeRuntimePaidOverrunAcknowledged, "acknowledged", effect.Version, request.AttemptID, uuid.UUID{}, nil)
	if err != nil {
		return PaidReceipt{}, err
	}
	return s.submitPaidLocked(ctx, drafts)
}

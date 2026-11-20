package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// HoldOrdinaryWork is owner administration, not an Agent permission. It never
// cancels a turn, refunds exposure or retains a request to start later.
func (s *Service) HoldOrdinaryWork(ctx context.Context, request EligibilityHold) (EligibilityReceipt, error) {
	return s.commitEligibility(ctx, request.CommandID, request.AgentID, ledger.EligibilitySubmission{Schema: 8, Actor: "owner", Operation: "eligibility.hold", ExpectedVersion: request.ExpectedVersion, ExpectedOperationID: request.ExpectedOperationID, Arguments: ledger.EligibilityHoldArguments{Reason: request.Reason}})
}

// ReleaseEligibility authorizes only this gate's release, not any other grant,
// budget, lifecycle approval or admission.
func (s *Service) ReleaseEligibility(ctx context.Context, request EligibilityRelease) (EligibilityReceipt, error) {
	return s.commitEligibility(ctx, request.CommandID, request.AgentID, ledger.EligibilitySubmission{Schema: 8, Actor: "owner", Operation: "eligibility.release", ExpectedVersion: request.ExpectedVersion, ExpectedOperationID: request.ExpectedOperationID, Arguments: ledger.EligibilityReleaseArguments{}})
}

func encodeEligibility(payload paidEncoding) ([]byte, error) {
	raw, err := payload.Encode()
	if err == nil {
		return raw, nil
	}
	if raw, marshalErr := json.Marshal(payload); marshalErr == nil && len(raw) > ledger.MaxEligibilityPayloadBytes {
		return nil, ErrTooLarge
	}
	return nil, ErrInvalid
}

func eligibilityDrafts(id uuid.UUID, sub ledger.EligibilitySubmission, change ledger.RuntimeEligibilityChanged, first uint64) ([]ledger.Draft, error) {
	raw, err := encodeEligibility(sub)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, ErrInvalid
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	payloads := []paidEncoding{ledger.Command{CommandID: id, SubmissionSHA256: digest}, change, ledger.RuntimeEligibilityReceipt{CommandID: id, SubmissionSHA256: digest, Submission: sub, Outcome: "recorded", EffectSequence: int64(first + 1)}}
	types := []string{ledger.TypeCommand, ledger.TypeRuntimeEligibilityChanged, ledger.TypeRuntimeEligibilityReceipt}
	drafts := make([]ledger.Draft, len(payloads))
	for i, payload := range payloads {
		raw, err := encodeEligibility(payload)
		if err != nil {
			return nil, err
		}
		actor := ledger.Actor(sub.Actor)
		if i == 1 {
			actor = ledger.ActorCore
		}
		drafts[i] = ledger.Draft{Type: types[i], TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, OperationID: id, Payload: raw}
	}
	return drafts, nil
}

func (s *Service) commitEligibility(ctx context.Context, id, agentID uuid.UUID, sub ledger.EligibilitySubmission) (EligibilityReceipt, error) {
	if s == nil {
		return EligibilityReceipt{}, ErrNotBound
	}
	if !validID(id) || !validID(agentID) {
		return EligibilityReceipt{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return EligibilityReceipt{}, err
	}
	defer s.active.Done()
	reads := &historyReadSet{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for attempt := 1; ; attempt++ {
		var drafts []ledger.Draft
		var prior *eligibilityGroup
		var digest ledger.Digest
		err := s.prepareHistoryLocked(ctx, reads, nil, true, func(reads *historyReadSet) error {
			drafts, prior = nil, nil
			agent, ok := s.p.agents[agentID]
			if !ok {
				return ErrNotBound
			}
			sub.Origin = ledger.Origin{AgentID: agentID, SessionID: agent.session, ContextID: agent.general}
			raw, err := encodeEligibility(sub)
			if err != nil {
				return err
			}
			digest = ledger.Digest(sha256.Sum256(raw))
			match, err := s.findCommand(ctx, reads, id, 0, s.p.through)
			if err != nil {
				return err
			}
			if match.found {
				if match.eligibility == nil || match.command.SubmissionSHA256 != digest {
					return ErrCommandMismatch
				}
				prior = match.eligibility
				return nil
			}
			next, err := s.deriveEligibility(ctx, reads, id, sub, s.p.through)
			if err != nil {
				return err
			}
			drafts, err = eligibilityDrafts(id, sub, next.snapshot, s.p.through+1)
			if err != nil {
				return err
			}
			if s.deps.beforeSubmit != nil {
				drafts = s.deps.beforeSubmit(drafts)
			}
			_, _, _, _, _, _, _, group, err := decodeFamily(drafts, s.p.through+1)
			if err != nil {
				return err
			}
			if group == nil || group.command.CommandID != id || group.command.SubmissionSHA256 != digest || !sameScopedValue(group.receipt.Submission, sub) {
				return ErrInvalid
			}
			_, err = s.planEligibility(ctx, reads, group, s.p.through+1)
			return err
		})
		if err != nil {
			return EligibilityReceipt{}, err
		}
		if prior != nil {
			return prior.result(), nil
		}
		committed, err := s.store.SubmitAt(ctx, s.p.through, drafts[0], drafts[1:])
		if errors.Is(err, ledgerstore.ErrTailMoved) {
			if attempt == tailAttempts {
				return EligibilityReceipt{}, &BusyError{Reason: "ledger_tail"}
			}
			continue
		}
		if err != nil {
			if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
				s.poisoned = true
			}
			if errors.Is(err, ledgerstore.ErrCommandMismatch) {
				err = ErrCommandMismatch
			}
			return EligibilityReceipt{}, err
		}
		if s.deps.afterScopedSubmit != nil {
			s.deps.afterScopedSubmit()
		}
		if err := s.acknowledgeLocked(max(s.p.through, committed.Last)); err != nil {
			s.poisoned = true
			return EligibilityReceipt{}, err
		}
		match, err := s.findCommand(context.Background(), nil, id, committed.First-1, committed.Last)
		if err != nil {
			return EligibilityReceipt{}, err
		}
		if match.eligibility == nil || match.command.SubmissionSHA256 != digest {
			s.poisoned = true
			return EligibilityReceipt{}, ErrIntegrity
		}
		return match.eligibility.result(), nil
	}
}

package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
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

// RequireFirstAction selects one exact request under the ordinary admission rules.
func (s *Service) RequireFirstAction(ctx context.Context, request EligibilityRequireFirst) (EligibilityReceipt, error) {
	turn, err := s.eligibilityTurn(ctx, request.AgentID, request.FirstActionTurn)
	if err != nil {
		return EligibilityReceipt{}, err
	}
	return s.commitEligibility(ctx, request.CommandID, request.AgentID, ledger.EligibilitySubmission{Schema: 8, Actor: "owner", Operation: "eligibility.require_first", ExpectedVersion: request.ExpectedVersion, ExpectedOperationID: request.ExpectedOperationID, Arguments: ledger.EligibilityRequireFirstArguments{Reason: request.Reason, ActionID: request.ActionID, Turn: turn}})
}

// DeclareNextFirstAction never enqueues work or releases ordinary work.
func (s *Service) DeclareNextFirstAction(ctx context.Context, request EligibilityFirstNext) (EligibilityReceipt, error) {
	turn, err := s.eligibilityTurn(ctx, request.AgentID, request.FirstActionTurn)
	if err != nil {
		return EligibilityReceipt{}, err
	}
	return s.commitEligibility(ctx, request.CommandID, request.AgentID, ledger.EligibilitySubmission{Schema: 8, Actor: "owner", Operation: "eligibility.first_next", ExpectedVersion: request.ExpectedVersion, ExpectedOperationID: request.ExpectedOperationID, Arguments: ledger.EligibilityFirstNextArguments{ActionID: request.ActionID, Turn: turn}})
}

func (s *Service) eligibilityTurn(ctx context.Context, agentID uuid.UUID, request FirstActionTurn) (ledger.EligibilityTurn, error) {
	return queryHistory(ctx, s, nil, func(*historyReadSet) (ledger.EligibilityTurn, error) {
		if request.Input.TurnID != request.TurnID {
			return ledger.EligibilityTurn{}, ErrInvalid
		}
		agent, found := s.p.agents[agentID]
		if !found {
			return ledger.EligibilityTurn{}, ErrNotBound
		}
		origin := ledger.Origin{AgentID: agentID, SessionID: agent.session, ContextID: request.ContextID}
		digest, err := s.eligibilityInputDigest(origin, request.StartCommandID, request.Input, request.TaskBinding)
		return ledger.EligibilityTurn{ContextID: request.ContextID, TurnID: request.TurnID, StartCommandID: request.StartCommandID, AdmissionCommandID: request.AdmissionCommandID, InputSHA256: digest, TaskBinding: request.TaskBinding}, err
	})
}

// The preimage uses modelJSON's UTF-8 encoding, no HTML escaping, JSON escaping
// of control characters and U+2028/U+2029, and no trailing newline. Artifact
// elements use ledger.ArtifactRef's source_sequence, sha256, bytes order.
// Only nil artifacts normalize; text and artifact order are never rewritten.
func (s *Service) eligibilityInputDigest(origin ledger.Origin, command uuid.UUID, input session.MessageSend, task *ledger.TaskBinding) (ledger.Digest, error) {
	if !validID(command) || !validID(input.TurnID) || !utf8.ValidString(input.Text) || len(input.Text) > maxObjectBytes || len(input.Artifacts) > s.limits.MaxSelectedObjects {
		return ledger.Digest{}, ErrInvalid
	}
	artifacts := input.Artifacts
	if artifacts == nil {
		artifacts = []ledger.ArtifactRef{}
	}
	raw := modelJSON(struct {
		Schema      int                  `json:"schema"`
		Origin      ledger.Origin        `json:"origin"`
		CommandID   uuid.UUID            `json:"command_id"`
		TurnID      uuid.UUID            `json:"turn_id"`
		Text        string               `json:"text"`
		Artifacts   []ledger.ArtifactRef `json:"artifacts"`
		TaskBinding *ledger.TaskBinding  `json:"task_binding,omitempty"`
	}{1, origin, command, input.TurnID, input.Text, artifacts, task})
	return ledger.Digest(sha256.Sum256(raw)), nil
}

// The observation races release, next and native admission through the same CAS
// journal. Losing that race changes no fact; persistence uncertainty stays real.
func (s *Service) firstActionRefused(ctx context.Context, agent uuid.UUID, observed *ledger.RuntimeEligibilityChanged, cause error) error {
	if observed == nil || cause == nil {
		return cause
	}
	code := ""
	switch {
	case errors.Is(cause, ErrDenied):
		code = "denied"
	case errors.Is(cause, ErrUnavailable):
		code = "unavailable"
	case errors.Is(cause, ErrInvalid):
		code = "invalid_argument"
	case errors.Is(cause, ErrTooLarge):
		code = "too_large"
	case errors.Is(cause, ErrIntegrity):
		code = "integrity"
	default:
		return cause
	}
	first := observed.FirstAction
	_, err := s.commitEligibility(ctx, uuid.NewV7(), agent, ledger.EligibilitySubmission{Schema: 8, Actor: "core", Operation: "eligibility.first_refused", ExpectedVersion: observed.Version, ExpectedOperationID: observed.OperationID, Arguments: ledger.EligibilityFirstRefusedArguments{ActionID: first.ActionID, TurnID: first.PermittedTurn.TurnID, StartCommandID: first.PermittedTurn.StartCommandID, Stage: "turn_admission", Code: code}})
	var revision *RevisionError
	if err == nil || errors.Is(err, ErrEligibilityStale) || errors.Is(err, ErrNoEffect) || errors.As(err, &revision) {
		return cause
	}
	return err
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

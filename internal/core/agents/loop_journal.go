package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func loopDrafts(id uuid.UUID, sub ledger.LoopSubmission, first uint64, effects []paidEncoding, types []string) ([]ledger.Draft, error) {
	raw, err := sub.Encode()
	if err != nil || !validID(id) || len(effects) != len(types) {
		return nil, ErrInvalid
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	receipt := ledger.RuntimeLoopReceipt{CommandID: id, SubmissionSHA256: digest, Submission: sub, Outcome: "applied", EffectSequence: int64(first) + int64(len(effects))}
	payloads := append([]paidEncoding{ledger.Command{CommandID: id, SubmissionSHA256: digest}}, effects...)
	payloads = append(payloads, receipt)
	types = append(append([]string{ledger.TypeCommand}, types...), ledger.TypeRuntimeLoopReceipt)
	drafts := make([]ledger.Draft, len(payloads))
	for i, p := range payloads {
		raw, err := p.Encode()
		if err != nil {
			return nil, err
		}
		actor := ledger.Actor(sub.Actor)
		if i > 0 && i < len(payloads)-1 {
			actor = ledger.ActorCore
		}
		drafts[i] = ledger.Draft{Type: types[i], TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, TurnID: sub.TurnID, OperationID: id, Payload: raw}
	}
	return drafts, nil
}

func (s *Service) commitLoop(ctx context.Context, id uuid.UUID, sub ledger.LoopSubmission, revision *ledger.PrivateTurnRevision) (result *loopGroup, err error) {
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.active.Done()
	raw, err := sub.Encode()
	if err != nil || !validID(id) {
		return nil, ErrInvalid
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	s.mu.Lock()
	applied := false
	defer func() {
		s.mu.Unlock()
		if applied && err == nil && result != nil && s.deps.afterLoopCommit != nil {
			s.deps.afterLoopCommit(result.receipt)
		}
	}()
	// The group predicts its receipt's effect sequence from the tail, so it is
	// written only at the tail it was planned at. Another writer's commit in
	// between re-enters here: refresh, search only the new commits for id,
	// and plan again, up to tailAttempts, then busy. Nothing was written.
	var committed ledgerstore.Receipt
	var searched uint64
	for attempt := 1; ; attempt++ {
		if err := s.usableLocked(ctx); err != nil {
			return nil, err
		}
		if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
			return nil, err
		}
		if !s.p.registeredOrigin(sub.Origin) {
			return nil, ErrNotBound
		}
		if searched < s.p.through && bytes.Compare(id[:], s.p.maxCommand[:]) <= 0 {
			m, err := s.findCommand(ctx, id, searched, s.p.through)
			if err != nil {
				return nil, err
			}
			if m.found {
				if m.loop == nil || m.command.SubmissionSHA256 != digest {
					return nil, ErrCommandMismatch
				}
				if m.loop.receipt.Submission.Origin != sub.Origin {
					return nil, ErrDenied
				}
				return m.loop, nil
			}
		}
		searched = s.p.through
		drafts, e := s.planLoopDraftsLocked(ctx, id, digest, sub, revision)
		if e != nil {
			return nil, e
		}
		committed, err = s.store.SubmitAt(ctx, s.p.through, drafts[0], drafts[1:])
		if !errors.Is(err, ledgerstore.ErrTailMoved) {
			break
		}
		if attempt == tailAttempts {
			return nil, &BusyError{Reason: "ledger_tail"}
		}
	}
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			err = ErrCommandMismatch
		}
		return nil, err
	}
	if s.deps.afterScopedSubmit != nil {
		s.deps.afterScopedSubmit()
	}
	if err := s.refreshLocked(context.Background(), max(s.p.through, committed.Last)); err != nil {
		s.poisoned = true
		return nil, err
	}
	m, err := s.findCommand(context.Background(), id, committed.First-1, committed.Last)
	if err != nil {
		return nil, err
	}
	if m.loop == nil || m.command.SubmissionSHA256 != digest {
		s.poisoned = true
		return nil, ErrIntegrity
	}
	applied = true
	return m.loop, nil
}

// planLoopDraftsLocked builds the group of sub at the projection's tail and
// preflights it with Open's reducer.
func (s *Service) planLoopDraftsLocked(ctx context.Context, id uuid.UUID, digest ledger.Digest, sub ledger.LoopSubmission, revision *ledger.PrivateTurnRevision) ([]ledger.Draft, error) {
	var effects []paidEncoding
	var types []string
	switch a := sub.Arguments.(type) {
	case ledger.LoopBindArguments:
		native, err := s.loopNative(ctx, a.AdmissionCommandID, sub.Origin, s.p.through)
		if err != nil {
			return nil, err
		}
		effects = []paidEncoding{ledger.RuntimeTurnBound{TurnID: sub.TurnID, AdmissionSequence: int64(native.first) + int64(native.effectIndex), InputObject: a.InputObject, PolicyObject: a.PolicyObject, RenderObject: a.RenderObject}}
		types = []string{ledger.TypeRuntimeTurnBound}
	case ledger.LoopToolBeginArguments:
		effects, types = []paidEncoding{ledger.RuntimeToolStarted(a)}, []string{ledger.TypeRuntimeToolStarted}
	case ledger.LoopToolResultArguments:
		effects, types = []paidEncoding{ledger.RuntimeToolResult(a)}, []string{ledger.TypeRuntimeToolResult}
	case ledger.LoopEndArguments:
		if revision == nil {
			return nil, ErrInvalid
		}
		native, err := s.loopNative(ctx, a.EndingCommandID, sub.Origin, s.p.through)
		if err != nil {
			return nil, err
		}
		effects = []paidEncoding{*revision, ledger.RuntimeTurnResult{TurnID: sub.TurnID, BoundSequence: a.BoundSequence, Outcome: a.Outcome, Reason: a.Reason, ModelCalls: a.ModelCalls, ToolDispatches: a.ToolDispatches, FinalMessageObject: a.FinalMessageObject, EndingSequence: int64(native.first) + int64(native.effectIndex)}}
		types = []string{ledger.TypePrivateTurnRevision, ledger.TypeRuntimeTurnResult}
	default:
		return nil, ErrInvalid
	}
	drafts, err := loopDrafts(id, sub, s.p.through+1, effects, types)
	if err != nil {
		return nil, err
	}
	if s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	_, _, _, _, _, g, err := decodeFamily(drafts, s.p.through+1)
	if err == nil && (g == nil || g.command.CommandID != id || g.command.SubmissionSHA256 != digest || !sameScopedValue(g.receipt.Submission, sub)) {
		err = ErrInvalid
	}
	if err == nil {
		_, err = s.planLoop(ctx, g, s.p.through+1)
	}
	if err != nil {
		return nil, err
	}
	return drafts, nil
}

// The immutable owning-context object is published only inside the pinned
// credential borrow. Callers reserve retained bytes before building raw.
func (s *Service) publishLoopObject(ctx context.Context, origin ledger.Origin, credential uuid.UUID, raw []byte) (ledger.ObjectRef, error) {
	if len(raw) == 0 || len(raw) > maxObjectBytes {
		return ledger.ObjectRef{}, ErrTooLarge
	}
	s.mu.Lock()
	if s.paid == nil {
		s.mu.Unlock()
		return ledger.ObjectRef{}, ErrUnavailable
	}
	secret := s.paid.secrets[credential]
	s.mu.Unlock()
	if secret == nil {
		return ledger.ObjectRef{}, ErrUnavailable
	}
	var ref ledger.ObjectRef
	err := secret.WithBytes(func(key []byte) error {
		if len(key) != 0 && (bytes.Contains(raw, key) || paidContainsSecret(raw, key)) {
			return ErrInvalid
		}
		ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
		scope, err := s.scopePath(ids, "context")
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.deps.beforePublish != nil {
			if err := s.deps.beforePublish(bindingOf(origin), "loop_object"); err != nil {
				return err
			}
		}
		digest, err := scope.publishObject(raw)
		if err != nil {
			return err
		}
		ref = ledger.ObjectRef{SHA256: digest, Bytes: int64(len(raw))}
		if s.deps.afterPublish != nil {
			s.deps.afterPublish(bindingOf(origin), "loop_object")
		}
		return nil
	})
	return ref, err
}

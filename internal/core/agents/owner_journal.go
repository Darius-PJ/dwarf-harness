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

func continuationDrafts(id uuid.UUID, sub ledger.ContinuationSubmission, first uint64) ([]ledger.Draft, error) {
	raw, err := encodeContinuation(sub)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, ErrInvalid
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	payloads := []paidEncoding{ledger.Command{CommandID: id, SubmissionSHA256: digest}, continuationChange(sub), ledger.RuntimeContinuationReceipt{CommandID: id, SubmissionSHA256: digest, Submission: sub, Outcome: "recorded", EffectSequence: int64(first + 1)}}
	types := []string{ledger.TypeCommand, ledger.TypeRuntimeContinuationChanged, ledger.TypeRuntimeContinuationReceipt}
	drafts := make([]ledger.Draft, 3)
	for i, payload := range payloads {
		raw, err := encodeContinuation(payload)
		if err != nil {
			return nil, err
		}
		actor := ledger.Actor(sub.Actor)
		if i == 1 {
			actor = ledger.ActorCore
		}
		drafts[i] = ledger.Draft{Type: types[i], TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, TurnID: sub.TurnID, OperationID: id, Payload: raw}
	}
	return drafts, nil
}

// commitContinuation uses the same bounded conditional-submit/readback protocol
// as commitLoop. The caller holds a context gate for stop and native ending.
func (s *Service) commitContinuation(ctx context.Context, id uuid.UUID, sub ledger.ContinuationSubmission, allowance *scopedReservation) (result *continuationGroup, err error) {
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.active.Done()
	if len(sub.Arguments.Pending) > 128 || len(sub.Arguments.NoticeCursors) > ledger.MaxContinuationPayloadBytes/(2*36) {
		return nil, ErrTooLarge
	}
	if !continuationSupported(continuationChange(sub)) {
		return nil, ErrUnavailable
	}
	reservation, err := s.prepareContinuation(ctx, sub, allowance)
	if err != nil {
		return nil, err
	}
	if reservation != nil {
		defer func() { s.mu.Lock(); s.releaseScopedLocked(reservation); s.mu.Unlock() }()
	}
	raw, err := encodeContinuation(sub)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, ErrInvalid
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	ids := submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}
	reads := &historyReadSet{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for attempt := 1; ; attempt++ {
		var drafts []ledger.Draft
		var prior *continuationGroup
		err := s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
			drafts, prior = nil, nil
			if !s.p.registeredOrigin(sub.Origin) {
				return ErrNotBound
			}
			m, err := s.findCommand(ctx, reads, id, 0, s.p.through)
			if err != nil {
				return err
			}
			if m.found {
				if m.continuation == nil || m.command.SubmissionSHA256 != digest {
					return ErrCommandMismatch
				}
				if m.continuation.receipt.Submission.Origin != sub.Origin {
					return ErrDenied
				}
				prior = m.continuation
				return nil
			}
			drafts, err = continuationDrafts(id, sub, s.p.through+1)
			if err != nil {
				return err
			}
			if s.deps.beforeSubmit != nil {
				drafts = s.deps.beforeSubmit(drafts)
			}
			_, _, _, _, _, _, g, err := decodeFamily(drafts, s.p.through+1)
			if err != nil {
				return err
			}
			if g == nil || g.command.CommandID != id || g.command.SubmissionSHA256 != digest || !sameScopedValue(g.receipt.Submission, sub) {
				return ErrInvalid
			}
			_, err = s.planContinuation(ctx, reads, g, s.p.through+1)
			return err
		})
		if err != nil || prior != nil {
			return prior, err
		}
		committed, err := s.store.SubmitAt(ctx, s.p.through, drafts[0], drafts[1:])
		if errors.Is(err, ledgerstore.ErrTailMoved) {
			if attempt == tailAttempts {
				return nil, &BusyError{Reason: "ledger_tail"}
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
			return nil, err
		}
		if s.deps.afterScopedSubmit != nil {
			s.deps.afterScopedSubmit()
		}
		if err := s.acknowledgeLocked(max(s.p.through, committed.Last)); err != nil {
			s.poisoned = true
			return nil, err
		}
		m, err := s.findCommand(context.Background(), nil, id, committed.First-1, committed.Last)
		if err != nil {
			return nil, err
		}
		if m.continuation == nil || m.command.SubmissionSHA256 != digest {
			s.poisoned = true
			return nil, ErrIntegrity
		}
		return m.continuation, nil
	}
}

func (s *Service) currentContinuation(ctx context.Context, origin ledger.Origin, turn uuid.UUID) (continuationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
	var owner continuationState
	err := s.prepareHistoryLocked(ctx, &historyReadSet{}, &ids, true, func(reads *historyReadSet) error {
		var err error
		owner, err = s.loadContinuation(ctx, reads, turn, s.p.through)
		if err == nil && owner.Snapshot.Version != 0 && owner.Origin != origin {
			return ErrDenied
		}
		return err
	})
	return owner, err
}

func continuationSubmission(owner continuationState, operation string, arguments ledger.ContinuationArguments) ledger.ContinuationSubmission {
	return ledger.ContinuationSubmission{Schema: 7, Actor: "core", Origin: owner.Origin, TurnID: owner.Snapshot.TurnID, Operation: operation, ExpectedVersion: owner.Snapshot.Version, ExpectedState: owner.Snapshot.State, Arguments: arguments}
}

// stopContinuation is the single stop edge. Nothing observable in the live
// scheduler changes until the transition is durably acknowledged.
func (s *Service) stopContinuation(ctx context.Context, origin ledger.Origin, turn, command uuid.UUID, reason, actor string) (ContinuationReceipt, string, error) {
	var receipt ContinuationReceipt
	if !validID(command) || reason == "" || !continuationKnownStop(reason) || actor != "owner" && actor != "core" {
		return receipt, "", ErrInvalid
	}
	ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
	unlock, err := s.acquireScope(ctx, keyFor(ids, "context"))
	if err != nil {
		return receipt, "", err
	}
	defer unlock()
	var owner continuationState
	var prior *continuationGroup
	var ended bool
	s.mu.Lock()
	err = s.prepareHistoryLocked(ctx, &historyReadSet{}, &ids, true, func(reads *historyReadSet) error {
		owner, prior, ended = continuationState{}, nil, false
		match, err := s.findCommand(ctx, reads, command, 0, s.p.through)
		if err != nil {
			return err
		}
		if match.found {
			g := match.continuation
			if g == nil || g.receipt.Submission.Actor != actor || g.receipt.Submission.Origin != origin || g.change.TurnID != turn || g.receipt.Submission.Operation != "owner.stop" || g.change.StopReason != reason {
				return ErrCommandMismatch
			}
			if g.receipt.Outcome != "recorded" {
				return ErrUnavailable
			}
			prior = g
			return nil
		}
		owner, err = s.loadContinuation(ctx, reads, turn, s.p.through)
		if err != nil {
			return err
		}
		if owner.Snapshot.Version == 0 {
			return ErrUnavailable
		}
		if owner.Origin != origin {
			return ErrDenied
		}
		if !owner.supported() {
			return ErrUnavailable
		}
		work, found, err := s.loadWork(ctx, reads, turn, s.p.through)
		if err != nil {
			return err
		}
		if !found || work.origin != origin {
			return ErrIntegrity
		}
		ended = work.ended
		if ended || owner.Snapshot.StopReason != "" {
			records, err := s.sourceRecords(ctx, reads, []int64{int64(owner.Sequence)}, s.p.through)
			if err != nil {
				return err
			}
			match, err := s.findCommand(ctx, reads, records[int64(owner.Sequence)].OperationID, 0, s.p.through)
			if err != nil {
				return err
			}
			if match.continuation == nil {
				return ErrIntegrity
			}
			prior = match.continuation
		}
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return receipt, "", err
	}
	if prior != nil {
		outcome := "accepted"
		if ended {
			outcome = "too_late"
		}
		return prior.result(), outcome, nil
	}
	a := continuationArguments(owner.Snapshot)
	a.State, a.ResumeState, a.WaitReason = "stopping", "", "none"
	a.OperationID, a.Generation = command, a.Generation+1
	a.StopReason, a.FinalMessageObject = reason, nil
	a.ModelCalls, a.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
	if reason == "cancelled" {
		a.CancelCommandID = uuid.NewV7()
	}
	sub := continuationSubmission(owner, "owner.stop", a)
	sub.Actor = actor
	g, err := s.commitContinuation(ctx, command, sub, nil)
	if err != nil {
		return receipt, "", err
	}
	s.publishContinuation(g)
	return g.result(), "accepted", nil
}

func (s *Service) publishContinuation(g *continuationGroup) {
	s.mu.Lock()
	r := s.loop
	if r != nil {
		r.mu.Lock()
		if t := r.turns[g.change.TurnID]; t != nil && g.change.Version > t.owner.Version {
			t.owner = g.change
			if g.change.ExpectedState == "waiting_resource" && g.change.State == "tool_pending" {
				for i := range t.tools {
					inv := &t.tools[i]
					token := inv.token
					if token.version == g.change.ExpectedVersion && token.phase == "waiting_resource" && token.operation == g.change.OperationID && token.generation == g.change.Generation && token.result == inv.id {
						inv.token = (continuationState{Origin: t.origin, Snapshot: g.change}).token(inv.id)
					}
				}
			}
			if call := t.paid; call != nil && g.change.ExpectedState == "waiting_resource" && g.change.State == "model_pending" {
				token := call.token
				if token.version == g.change.ExpectedVersion && token.phase == "waiting_resource" && token.operation == g.change.OperationID && token.generation == g.change.Generation && token.result == call.id {
					call.token = (continuationState{Origin: t.origin, Snapshot: g.change}).token(call.id)
				}
			}
			if g.change.StopReason != "" {
				t.reason, t.cancelID = g.change.StopReason, g.change.CancelCommandID
				t.cancel()
			}
		}
		r.changedLocked()
		r.mu.Unlock()
	}
	s.mu.Unlock()
	if r != nil && g.receipt.Submission.Operation == "owner.stop" {
		r.notify()
	}
}

// updateContinuation uses only the journal's Service.mu CAS, never a scope
// gate. It is safe when the caller already owns an agent or context gate.
func (s *Service) updateContinuation(ctx context.Context, origin ledger.Origin, turn uuid.UUID, operation string, token *continuationToken, change func(*ledger.ContinuationArguments, continuationState, uuid.UUID) error) (continuationState, error) {
	ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
	var err error
	var owner continuationState
	if token != nil {
		owner, err = queryHistory(ctx, s, &ids, func(reads *historyReadSet) (continuationState, error) {
			return s.continuationCallback(ctx, reads, *token)
		})
	} else {
		owner, err = s.currentContinuation(ctx, origin, turn)
	}
	if err != nil {
		return owner, err
	}
	if owner.Snapshot.Version == 0 || owner.Snapshot.State == "terminal" || !owner.supported() {
		return owner, ErrContinuationStale
	}
	command := uuid.NewV7()
	a := continuationArguments(owner.Snapshot)
	a.ModelCalls, a.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
	if err := change(&a, owner, command); err != nil {
		return owner, err
	}
	sortContinuationPending(a.Pending)
	if sameScopedValue(a, continuationArguments(owner.Snapshot)) {
		return owner, nil
	}
	g, err := s.commitContinuation(ctx, command, continuationSubmission(owner, operation, a), nil)
	if err != nil {
		return owner, err
	}
	s.publishContinuation(g)
	return continuationState{Origin: origin, Snapshot: g.change, Sequence: g.first + 1, Outcome: g.receipt.Outcome, ModelCalls: g.change.ModelCalls, ToolDispatches: g.change.ToolDispatches}, nil
}

func (s *Service) initializeContinuation(ctx context.Context, bound *loopGroup, policy TurnPolicy, state string, allowance *scopedReservation) (*continuationGroup, error) {
	command := uuid.NewV7()
	return s.commitContinuation(ctx, command, ledger.ContinuationSubmission{Schema: 7, Actor: "core", Origin: bound.receipt.Submission.Origin, TurnID: bound.bound.TurnID, Operation: "owner.initialize", ExpectedState: "absent", Arguments: ledger.ContinuationArguments{
		BoundSequence: int64(bound.first + 1), State: state, OperationID: command, Generation: 1, WaitReason: "none", DeadlineUTCNS: policy.DeadlineUTCNS, Pending: []ledger.ContinuationPending{}, NoticeCursors: []ledger.ContinuationNoticeCursor{},
	}}, allowance)
}

func (r *loopRuntime) ownerPhase(t *loopTurn, phase string, pending []ledger.ContinuationPending) (continuationState, error) {
	return r.service.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", nil, func(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
		if a.StopReason != "" {
			if phase != "finishing" {
				return ErrContinuationStale
			}
			if a.State == "stopping" {
				a.State, a.ResumeState, a.WaitReason = phase, "", "none"
			}
			return nil
		}
		if a.State == "ready" {
			a.State, a.OperationID, a.Generation = phase, command, a.Generation+1
			a.Pending = pending
		} else if a.State != phase && (a.State != "waiting_resource" || a.ResumeState != phase) {
			return ErrContinuationStale
		}
		if phase == "finishing" && a.State == "finishing" {
			a.FinalMessageObject = t.answer
		}
		return nil
	})
}

func (r *loopRuntime) parkOwner(t *loopTurn, reason string) error {
	_, err := r.service.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", nil, func(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
		if a.State == "stopping" {
			return ErrContinuationStale
		}
		if a.State == "waiting_resource" {
			return nil
		}
		a.State, a.ResumeState, a.WaitReason = "waiting_resource", a.State, reason
		return nil
	})
	return err
}

func (r *loopRuntime) resumeOwner(t *loopTurn, reason string) (continuationState, error) {
	return r.service.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", nil, func(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
		if a.StopReason != "" && (a.State != "waiting_resource" || a.ResumeState != "finishing") {
			return ErrContinuationStale
		}
		if a.State != "waiting_resource" {
			return nil
		}
		if a.WaitReason != reason {
			return ErrContinuationStale
		}
		a.State, a.ResumeState, a.WaitReason = a.ResumeState, "", "none"
		return nil
	})
}

func (r *loopRuntime) prepareQuantum(t *loopTurn, reason string) (bool, bool, error) {
	r.mu.Lock()
	if t.reason != "" {
		reason = t.reason
	} else if r.stopping {
		reason = "shutdown"
	}
	r.mu.Unlock()
	if reason != "" {
		if err := r.noteStop(t, reason); err != nil {
			return false, false, err
		}
	}
	r.mu.Lock()
	model := t.reason == "" && len(t.tools) == 0 && t.answer == nil
	phase := "finishing"
	var pending []ledger.ContinuationPending
	if model {
		phase = "model_pending"
		if t.paid == nil {
			t.paid = &loopPaidCall{id: uuid.NewV7(), admitID: uuid.NewV7(), dispatchID: uuid.NewV7(), observeID: uuid.NewV7(), reconcileID: uuid.NewV7()}
		}
		pending = []ledger.ContinuationPending{{Kind: "paid_attempt", ID: t.paid.id, CommandID: t.paid.admitID, ExpectedVersion: 1}}
	} else if t.reason == "" && len(t.tools) != 0 {
		phase = "tool_pending"
		inv := &t.tools[0]
		if inv.id == (uuid.UUID{}) {
			inv.id = uuid.NewV7()
		}
		var command uuid.UUID
		if inv.correction == nil && loopNativeOperation(inv.normalized.Operation) {
			command = inv.effectID
		}
		pending = []ledger.ContinuationPending{{Kind: "tool", ID: inv.id, CommandID: command, ExpectedVersion: 1}}
	}
	r.mu.Unlock()
	owner, err := r.ownerPhase(t, phase, pending)
	if err != nil {
		return model, false, err
	}
	if model {
		if claimed, generation := r.claimPaid(t); !claimed {
			if err := r.parkOwner(t, "paid_exchange"); err != nil {
				return model, false, err
			}
			r.mu.Lock()
			t.state, t.waitGeneration, t.parkedReason = "waiting_resource", generation, t.reason
			r.enqueue(t)
			r.changedLocked()
			r.mu.Unlock()
			return model, true, nil
		}
		if owner.Snapshot.State == "waiting_resource" && owner.Snapshot.WaitReason == "paid_exchange" {
			owner, err = r.resumeOwner(t, "paid_exchange")
			if err != nil {
				r.service.mu.Lock()
				delete(r.paidTokens, t.paid.id)
				r.service.mu.Unlock()
				return model, false, err
			}
		}
	}
	return model, false, nil
}

func (r *loopRuntime) ownerFailure(t *loopTurn, err error) {
	r.mu.Lock()
	r.failure = errors.Join(r.failure, err)
	t.terminalError = err
	if !t.running {
		t.state = "recovery_pending"
	}
	r.changedLocked()
	r.mu.Unlock()
}

func (r *loopRuntime) recordPending(t *loopTurn, pending ledger.ContinuationPending) (continuationState, error) {
	return r.service.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", nil, func(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
		for i, prior := range a.Pending {
			if prior.Kind == pending.Kind && prior.ID == pending.ID {
				if prior.SourceSequence != 0 && prior != pending {
					return ErrContinuationStale
				}
				a.Pending[i] = pending
				return nil
			}
		}
		if a.State != "tool_pending" && (a.State != "waiting_resource" || a.ResumeState != "tool_pending") {
			return ErrContinuationStale
		}
		a.Pending = append(a.Pending, pending)
		return nil
	})
}

func (r *loopRuntime) completeOwner(t *loopTurn, token continuationToken, final *ledger.ObjectRef) (continuationState, error) {
	return r.service.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", &token, func(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
		if a.StopReason != "" {
			return ErrContinuationStale
		}
		a.State, a.ResumeState, a.WaitReason, a.Pending = "ready", "", "none", []ledger.ContinuationPending{}
		if final != nil {
			a.State, a.FinalMessageObject = "finishing", final
		}
		return nil
	})
}

// The frozen codec intentionally has one ErrInvalid. Producers distinguish
// an oversized, bounded snapshot from other invalid input before any Submit.
func encodeContinuation(payload paidEncoding) ([]byte, error) {
	raw, err := payload.Encode()
	if err == nil {
		return raw, nil
	}
	encoded, marshalErr := json.Marshal(payload)
	if marshalErr == nil && len(encoded) > ledger.MaxContinuationPayloadBytes {
		return nil, ErrTooLarge
	}
	return nil, ErrInvalid
}

// The twelve codec-sized workspaces cover the submission, both effect/receipt
// encodings, their decoded copies, comparisons, and the appender copy. A live
// turn already owns the larger manifest/encoding allowance from startTurn.
const continuationBufferBytes = 12 * ledger.MaxContinuationPayloadBytes

func (s *Service) prepareContinuation(ctx context.Context, sub ledger.ContinuationSubmission, allowance *scopedReservation) (*scopedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if allowance == nil && s.loop != nil {
		s.loop.mu.Lock()
		if turn := s.loop.turns[sub.TurnID]; turn != nil && turn.origin == sub.Origin {
			allowance = &turn.reservation
		}
		s.loop.mu.Unlock()
	}
	if allowance != nil {
		if allowance.origin != sub.Origin || len(allowance.scopes) == 0 || allowance.budget.hold[bufferExposure] < continuationBufferBytes {
			return nil, ErrTooLarge
		}
		return nil, nil
	}
	reservation := &scopedReservation{origin: sub.Origin}
	ids := submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}
	var scopes []ledger.ScopeKey
	err := s.prepareHistoryLocked(ctx, &historyReadSet{}, &ids, true, func(reads *historyReadSet) error {
		work, found, err := s.loadWork(ctx, reads, sub.TurnID, s.p.through)
		if err != nil {
			return err
		}
		if !found || work.origin != sub.Origin {
			return ErrNotBound
		}
		scopes = work.admission.Scopes
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.reserveScopedProcessLocked(reservation); err != nil {
		return nil, err
	}
	preparation := budgetPreparation{hold: [4]int64{0, 0, continuationBufferBytes, 0}}
	if err := s.p.book.reservePreparation(sub.Origin, scopes, preparation); err != nil {
		s.releaseScopedLocked(reservation)
		return nil, err
	}
	reservation.scopes, reservation.budget = scopes, preparation
	return reservation, nil
}

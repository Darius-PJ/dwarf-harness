package agents

import (
	"context"
	"errors"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// These are transient holds in the existing book, not a second admission state.
// All changes to a reservation happen under Service.mu.
type scopedReservation struct {
	origin  ledger.Origin
	scopes  []ledger.ScopeKey
	budget  budgetPreparation
	process bool
}

func (s *Service) reserveScopedProcessLocked(reservation *scopedReservation) error {
	if reservation.process {
		return nil
	}
	if s.preparing >= s.limits.MaxConcurrentWrites {
		return &BusyError{Reason: "preparing"}
	}
	s.preparing++
	reservation.process = true
	return nil
}

func (s *Service) reserveScopedBudgetLocked(reservation *scopedReservation, input scopedInput, usage *ledger.RuntimeLocalUsed, replay bool) error {
	if usage == nil {
		return nil
	}
	buffer := usage.Bytes
	if input.sub.Operation == "private.read" {
		var err error
		buffer, err = input.selection.bufferBytes()
		if err != nil {
			return err
		}
	} else if input.sub.Operation == "artifact.read" {
		buffer = artifactReadBufferBytes
	}
	preparation := budgetPreparation{hold: [4]int64{0, 1, buffer, 0}}
	if !replay {
		size := usage.Bytes
		preparation.hold[unitExposure], preparation.operationBytes = 1, &size
	}
	if err := s.p.book.reservePreparation(input.sub.Origin, usage.Scopes, preparation); err != nil {
		return err
	}
	reservation.scopes, reservation.budget = usage.Scopes, preparation
	return nil
}

func (s *Service) releaseScopedLocked(reservation *scopedReservation) {
	if len(reservation.scopes) != 0 {
		s.p.book.releasePreparation(reservation.origin, reservation.scopes, reservation.budget)
	}
	if reservation.process {
		s.preparing--
	}
	*reservation = scopedReservation{origin: reservation.origin}
}

func (s *Service) consumeScopedReservationLocked(reservation *scopedReservation) {
	if reservation.budget.hold[unitExposure] != 0 {
		unit := budgetPreparation{hold: [4]int64{0, 0, 0, reservation.budget.hold[unitExposure]}}
		s.p.book.releasePreparation(reservation.origin, reservation.scopes, unit)
		reservation.budget.hold[unitExposure] = 0
	}
	reservation.budget.operationBytes = nil
}

func (s *Service) checkScopedAdmissionPreparation(group *scopedGroup) error {
	if group.admission == nil {
		return nil
	}
	admission := group.admission
	holds, size := [4]int64{1, 0, 0, 0}, int64(0)
	if admission.Kind == "local" {
		size = *admission.MaxBytes
		holds = [4]int64{0, 1, size, 1}
	}
	return s.p.book.checkPreparation(admission.Scopes, budgetPreparation{hold: holds, operationBytes: &size}, true)
}

func (s *Service) scopedPrivateStateLocked(input scopedInput) error {
	switch input.sub.Operation {
	case "private.read", "private.write", "turn.start":
	default:
		return nil
	}
	sub := input.sub
	ids := submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}
	if err := s.scopeIssuesLocked(ids, sub.Target.Scope); err != nil {
		return err
	}
	if args, ok := sub.Arguments.(ledger.PrivateReadArguments); ok {
		state := s.p.scopes[keyFor(ids, args.Read.Scope)]
		if state == nil {
			return ErrNotBound
		}
		if uint64(args.Read.Revision) > state.revision {
			return &RevisionError{Expected: uint64(args.Read.Revision), Current: state.revision}
		}
	}
	return nil
}

func (s *Service) executeScoped(ctx context.Context, caller scopedCaller, operation string, request any) (ScopedReceipt, error) {
	result, err := s.executeScopedResult(ctx, caller, operation, request)
	return result.receipt, err
}

func (s *Service) executeScopedResult(ctx context.Context, caller scopedCaller, operation string, request any) (scopedResult, error) {
	if caller.actor == "owner" && ownerGrantSelected(request) {
		return scopedResult{}, ErrInvalid
	}
	if s == nil {
		return scopedResult{}, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return scopedResult{}, err
	}
	defer s.active.Done()
	var ids submissionIDs
	var err error
	if caller.actor != "core" {
		ids, err = s.initialIDs(ctx, caller.binding, false)
		if err != nil {
			return scopedResult{}, err
		}
	}
	if caller.actor == "agent" && caller.fixed != ids {
		return scopedResult{}, ErrNotBound
	}
	input, err := makeScopedInput(caller.actor, ids, operation, request)
	if err != nil {
		return scopedResult{}, err
	}
	s.mu.Lock()
	err = s.usableLocked(ctx)
	if err == nil {
		err = s.refreshLocked(ctx, s.store.Durable())
	}
	if err == nil {
		err = s.resolveScopedTarget(ctx, &input)
	}
	s.mu.Unlock()
	if err != nil {
		return scopedResult{}, err
	}
	canonical, digest, err := encodeScopedSubmission(input.sub)
	if err != nil {
		return scopedResult{}, err
	}
	input.sub, err = ledger.DecodeScopedSubmission(canonical)
	if err != nil {
		return scopedResult{}, err
	}
	input.digest = digest
	ids = submissionIDs{input.sub.Origin.AgentID, input.sub.Origin.SessionID, input.sub.Origin.ContextID}
	reservation := scopedReservation{origin: input.sub.Origin}
	defer func() { s.mu.Lock(); s.releaseScopedLocked(&reservation); s.mu.Unlock() }()
	privateRead, privateWrite := operation == "private.read", operation == "private.write"
	read := privateRead || operation == "artifact.read"
	needsIO := read || privateWrite || operation == "task.create" || operation == "turn.start"
	scopeHeld := false
	match, err := s.lookupScoped(ctx, input)
	if err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	if match.found {
		return s.replayScopedLocked(ctx, input, match, false)
	}
	if err := s.scopedProducerGuards(input); err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	if err := s.scopedPrivateStateLocked(input); err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	if privateRead {
		// The finite process slot bounds metadata selection too. No private
		// bytes are read until its exact objects have a book reservation.
		if err := s.reserveScopedProcessLocked(&reservation); err != nil {
			s.mu.Unlock()
			return scopedResult{}, err
		}
		through, maxSelected := s.p.through, min(s.limits.MaxSelectedObjects, ledger.MaxScopedPayloadBytes)
		s.mu.Unlock()
		unlock, err := s.acquireScope(ctx, keyFor(ids, input.sub.Target.Scope))
		if err != nil {
			return scopedResult{}, err
		}
		defer unlock()
		scopeHeld = true
		selection, selectionErr := s.selectPrivateRead(ctx, ids, privateReadInput(input.sub), through, maxSelected)
		input.selection = selection
		match, err = s.lookupScoped(ctx, input)
		if err != nil {
			s.mu.Unlock()
			return scopedResult{}, err
		}
		if match.found {
			s.releaseScopedLocked(&reservation)
			return s.replayScopedLocked(ctx, input, match, true)
		}
		if selectionErr != nil {
			s.mu.Unlock()
			return scopedResult{}, selectionErr
		}
		if err := s.scopedProducerGuards(input); err != nil {
			s.mu.Unlock()
			return scopedResult{}, err
		}
	}
	drafts, err := s.scopedDrafts(ctx, input)
	var group *scopedGroup
	if err == nil {
		group, err = s.preflightScoped(ctx, input, drafts)
	}
	if err == nil {
		err = s.checkScopedAdmissionPreparation(group)
	}
	if err == nil && needsIO {
		err = s.reserveScopedProcessLocked(&reservation)
	}
	if err == nil {
		err = s.reserveScopedBudgetLocked(&reservation, input, group.usage, false)
	}
	s.mu.Unlock()
	if err != nil {
		return scopedResult{}, err
	}
	if !scopeHeld && (privateWrite || operation == "turn.start") {
		if operation == "turn.start" {
			unlockAgent, err := s.acquireScope(ctx, keyFor(ids, "agent"))
			if err != nil {
				return scopedResult{}, err
			}
			defer unlockAgent()
		}
		unlock, err := s.acquireScope(ctx, keyFor(ids, input.sub.Target.Scope))
		if err != nil {
			return scopedResult{}, err
		}
		defer unlock()
		scopeHeld = true
		match, err = s.lookupScoped(ctx, input)
		if err != nil {
			s.mu.Unlock()
			return scopedResult{}, err
		}
		if match.found {
			s.releaseScopedLocked(&reservation)
			return s.replayScopedLocked(ctx, input, match, true)
		}
		err = s.scopedPrivateStateLocked(input)
		if err == nil {
			drafts, err = s.scopedDrafts(ctx, input)
		}
		if err == nil {
			group, err = s.preflightScoped(ctx, input, drafts)
		}
		if err == nil {
			err = s.checkScopedAdmissionPreparation(group)
		}
		if err == nil && len(reservation.scopes) != 0 {
			err = s.p.book.checkPreparation(reservation.scopes, reservation.budget, false)
		}
		s.mu.Unlock()
		if err != nil {
			return scopedResult{}, err
		}
	}
	result, ioErr := s.prepareScopedIO(ctx, &input)
	match, err = s.lookupScoped(ctx, input)
	if err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	if match.found {
		// A concurrent winner can have selected a different bounded page.
		// Discard ours before releasing it, then re-read the recorded selection.
		result, input.changes = scopedResult{}, nil
		s.releaseScopedLocked(&reservation)
		return s.replayScopedLocked(ctx, input, match, scopeHeld)
	}
	if ioErr != nil {
		s.mu.Unlock()
		return scopedResult{}, ioErr
	}
	err = s.scopedProducerGuards(input)
	if err == nil {
		err = s.scopedPrivateStateLocked(input)
	}
	if err == nil {
		drafts, err = s.scopedDrafts(ctx, input)
	}
	if err == nil && s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	if err == nil {
		group, err = s.preflightScoped(ctx, input, drafts)
	}
	if err == nil {
		err = s.checkScopedAdmissionPreparation(group)
	}
	if err == nil && len(reservation.scopes) != 0 {
		err = s.p.book.checkPreparation(reservation.scopes, reservation.budget, false)
	}
	if err == nil {
		err = s.usableLocked(ctx)
	}
	if err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	committed, err := s.store.Submit(ctx, drafts[0], drafts[1:])
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		s.mu.Unlock()
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			return scopedResult{}, ErrCommandMismatch
		}
		return scopedResult{}, err
	}
	if s.deps.afterScopedSubmit != nil {
		s.deps.afterScopedSubmit()
	}
	var actual commandMatch
	if !committed.Duplicate && committed.First == s.p.through+1 {
		checkpoint := s.history.ackCheckpoint(s.p.through)
		if checkpoint == nil {
			checkpoint = &ledger.ReadCheckpoint{}
		}
		var accepted ledger.Commit
		accepted, checkpoint, err = s.readCommittedRange(context.Background(), committed.First, committed.Last, checkpoint)
		if err == nil {
			actual, err = s.acceptCommitLocked(context.Background(), accepted, &input)
		}
		if err == nil {
			s.history.putAckCheckpoint(s.p.through, checkpoint)
		}
	} else {
		err = s.refreshLocked(context.Background(), max(s.p.through, committed.Last))
		if err == nil {
			actual, err = s.findCommand(context.Background(), input.id, committed.First-1, committed.Last)
		}
	}
	if err != nil || !actual.found {
		s.poisoned = true
		s.mu.Unlock()
		if err == nil {
			err = ErrInconsistent
		}
		return scopedResult{}, err
	}
	if committed.Duplicate {
		result, input.changes = scopedResult{}, nil
		s.releaseScopedLocked(&reservation)
		return s.replayScopedLocked(ctx, input, actual, scopeHeld)
	}
	result.receipt, err = matchScopedReceipt(actual, input)
	if err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	// The committed U owns the consumed unit now. Retain only physical holds
	// until materialization/disclosure finishes; never count that unit twice.
	s.consumeScopedReservationLocked(&reservation)
	if !read && !privateWrite {
		s.releaseScopedLocked(&reservation)
	}
	s.mu.Unlock()
	var completionErr error
	if s.deps.afterScopedCommit != nil {
		completionErr = s.deps.afterScopedCommit(result.receipt)
	}
	if privateWrite {
		if err := s.completeScope(context.Background(), ids, input.sub.Target.Scope, false, nil); err != nil {
			return scopedResult{}, &MaterializationError{ReadBinding: ReadBinding{Binding: bindingOf(input.sub.Origin), Scope: input.sub.Target.Scope}, Revision: uint64(result.receipt.Version), Cause: err}
		}
		input.changes = nil
	}
	if read || privateWrite {
		disclosureContext := ctx
		if privateWrite {
			// Cancellation no longer owns this durable private transition.
			disclosureContext = context.Background()
		}
		s.mu.Lock()
		err = s.refreshScopedLocked(disclosureContext, input.sub)
		if err == nil && read {
			err = s.scopedPrivateStateLocked(input)
		}
		s.mu.Unlock()
		if err != nil {
			return scopedResult{}, err
		}
	}
	if completionErr != nil {
		return scopedResult{}, completionErr
	}
	return result, nil
}

// replayScopedLocked is entered with mu held and always returns with it released.
// A byte-returning retry repeats verified I/O under physical reservations only.
func (s *Service) replayScopedLocked(ctx context.Context, input scopedInput, match commandMatch, scopeHeld bool) (scopedResult, error) {
	receipt, err := matchScopedReceipt(match, input)
	if err != nil {
		s.mu.Unlock()
		return scopedResult{}, err
	}
	operation := input.sub.Operation
	ids := submissionIDs{input.sub.Origin.AgentID, input.sub.Origin.SessionID, input.sub.Origin.ContextID}
	if operation != "private.read" && operation != "artifact.read" {
		s.mu.Unlock()
		if operation == "private.write" && !scopeHeld {
			if err := s.awaitPrivateReplay(ctx, ids, input.sub.Target.Scope, &input.sub); err != nil {
				return scopedResult{}, err
			}
		}
		return scopedResult{receipt: receipt}, nil
	}
	usage := match.scoped.usage
	if usage == nil {
		s.mu.Unlock()
		return scopedResult{}, ErrInconsistent
	}
	if operation == "private.read" {
		input.selection = selectionFromUsage(*usage)
	}
	reservation := scopedReservation{origin: input.sub.Origin}
	err = s.reserveScopedProcessLocked(&reservation)
	if err == nil {
		err = s.reserveScopedBudgetLocked(&reservation, input, usage, true)
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.releaseScopedLocked(&reservation); s.mu.Unlock() }()
	if err != nil {
		return scopedResult{}, err
	}
	if operation == "private.read" && !scopeHeld {
		unlock, err := s.acquireScope(ctx, keyFor(ids, input.sub.Target.Scope))
		if err != nil {
			return scopedResult{}, err
		}
		defer unlock()
	}
	s.mu.Lock()
	err = s.refreshScopedLocked(ctx, input.sub)
	if err == nil {
		err = s.scopedPrivateStateLocked(input)
	}
	if err == nil && operation == "artifact.read" {
		err = s.verifyArtifactSource(ctx, input.sub.Arguments.(ledger.ArtifactReadArguments).Artifact, s.p.through)
	}
	s.mu.Unlock()
	if err != nil {
		return scopedResult{}, err
	}
	result, readErr := s.prepareScopedIO(ctx, &input)
	s.mu.Lock()
	err = s.refreshScopedLocked(ctx, input.sub)
	if err == nil {
		err = s.scopedPrivateStateLocked(input)
	}
	s.mu.Unlock()
	if err != nil {
		return scopedResult{}, err
	}
	if readErr != nil {
		return scopedResult{}, readErr
	}
	result.receipt = receipt
	return result, nil
}

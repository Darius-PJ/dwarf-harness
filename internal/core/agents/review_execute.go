package agents

import (
	"bytes"
	"context"
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func (s *Service) executeReview(ctx context.Context, caller scopedCaller, operation string, request any) (ReviewReceipt, error) {
	_, grant, _, _ := reviewArguments(request)
	if caller.actor == "owner" && grant != (uuid.UUID{}) {
		return ReviewReceipt{}, ErrInvalid
	}
	if s == nil {
		return ReviewReceipt{}, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return ReviewReceipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, caller.binding, false)
	if err != nil {
		return ReviewReceipt{}, err
	}
	if caller.actor == "agent" && caller.fixed != ids {
		return ReviewReceipt{}, ErrNotBound
	}
	input, err := makeReviewInput(caller.actor, ids, operation, request)
	if err != nil {
		return ReviewReceipt{}, err
	}
	reservation := scopedReservation{origin: input.sub.Origin}
	defer func() { s.mu.Lock(); s.releaseScopedLocked(&reservation); s.mu.Unlock() }()
	// Steps 2 and 3: the caller's current authority, then the command. A
	// retry answers before any step-4 guard, limit or clock.
	l, err := s.lookupReview(ctx, input, 0, false)
	if err == nil && l.match.found {
		r, err := s.retryReviewLocked(ctx, input, l)
		s.mu.Unlock()
		return r, err
	}
	// Steps 4 and 5: the new command's guards, then its bounds and
	// reservation, all before any witness is read.
	if err == nil {
		_, err = s.reviewGuardsLocked(ctx, input, nil)
	}
	var usage *ledger.ReviewUsage
	if err == nil {
		usage, err = s.reviewUsage(ctx, input.sub)
	}
	if err == nil {
		err = s.reviewProducerGuards(input, usage)
	}
	if err == nil {
		err = s.reserveScopedProcessLocked(&reservation)
	}
	if err == nil && usage != nil {
		err = s.reserveScopedBudgetLocked(&reservation, scopedInput{sub: ledger.ScopedSubmission{Origin: input.sub.Origin}}, &ledger.RuntimeLocalUsed{Scopes: usage.Scopes, Units: 1}, false)
	}
	through := s.p.through
	s.mu.Unlock()
	if err != nil {
		return ReviewReceipt{}, reviewDenied(err)
	}

	// The witnesses are prepared without mu, beside a scan of the tail for a
	// concurrent winner. Every guard then repeats under mu at the refreshed
	// prefix, the clock is sampled a second time, and Open's reducer judges
	// the exact group this Submit writes.
	l, err = s.lookupReview(ctx, input, through, true)
	if err == nil && l.match.found {
		r, err := s.retryReviewLocked(ctx, input, l)
		s.mu.Unlock()
		return r, err
	}
	var sample int64
	if err == nil {
		sample, err = s.reviewGuardsLocked(ctx, input, l.sourceErr)
	}
	var drafts []ledger.Draft
	var group *reviewGroup
	var plan reviewPlan
	if err == nil {
		drafts, group, plan, err = s.prepareReviewLocked(ctx, input, l.sources, sample)
	}
	if err == nil {
		err = s.reviewProducerGuards(input, group.receipt.Usage)
	}
	if err == nil && len(reservation.scopes) != 0 {
		err = s.p.book.checkPreparation(reservation.scopes, reservation.budget, false)
	}
	if err == nil {
		err = s.usableLocked(ctx)
	}
	if err != nil {
		s.mu.Unlock()
		return ReviewReceipt{}, reviewDenied(err)
	}
	committed, err := s.store.Submit(ctx, drafts[0], drafts[1:])
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		s.mu.Unlock()
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			err = ErrCommandMismatch
		}
		return ReviewReceipt{}, err
	}
	if s.deps.afterScopedSubmit != nil {
		s.deps.afterScopedSubmit()
	}
	if !committed.Duplicate && committed.First == s.p.through+1 {
		// The exact group was preflighted at this prefix. Reuse its bounded
		// authoritative read without rereading immutable source witnesses.
		floor := plan.floor
		var accepted ledger.Commit
		accepted, err = s.readCommittedRange(context.Background(), committed.First, committed.Last)
		if err == nil {
			for _, r := range accepted.Records {
				floor = max(floor, r.EventTimeNS)
			}
		}
		if err == nil {
			s.p.installReview(plan)
			s.p.publishHistory(nil, nil, committed.Last)
			s.history.advance(s.p.through, committed.Last, draftsOf(accepted))
			s.history.warmSources(accepted)
			s.p.reviewFloor, s.p.through = floor, committed.Last
			if bytes.Compare(input.id[:], s.p.maxCommand[:]) > 0 {
				s.p.maxCommand = input.id
			}
		}
	} else {
		err = s.refreshLocked(context.Background(), max(s.p.through, committed.Last))
		if err == nil {
			var match commandMatch
			match, err = s.findCommand(context.Background(), input.id, committed.First-1, committed.Last)
			if err == nil && match.review == nil {
				err = ErrInconsistent
			}
			group = match.review
		}
	}
	if err != nil {
		s.poisoned = true
		s.mu.Unlock()
		return ReviewReceipt{}, err
	}
	receipt, err := matchReviewReceipt(commandMatch{found: true, command: group.command, review: group}, input)
	if err != nil {
		s.poisoned = true
		s.mu.Unlock()
		return ReviewReceipt{}, err
	}
	if !committed.Duplicate {
		s.consumeScopedReservationLocked(&reservation)
	}
	s.releaseScopedLocked(&reservation)
	s.mu.Unlock()
	if s.deps.afterReviewCommit != nil {
		if err := s.deps.afterReviewCommit(receipt); err != nil {
			return ReviewReceipt{}, err
		}
	}
	return receipt, nil
}

func (input reviewInput) principal() ledger.ReviewPrincipal {
	return ledger.ReviewPrincipal{Actor: input.sub.Actor, Origin: input.sub.Origin, GrantID: input.sub.GrantID}
}

type reviewLookup struct {
	match     commandMatch
	sources   map[int64]ledger.Record
	sourceErr error
}

// lookupReview proves the caller's current authority, then searches the
// command history after the given prefix without mu; with witnesses it also
// prepares the binding's metadata witnesses there. It returns with mu held,
// even on error, after repeating the caller check and searching the tail. A
// witness batch is immutable metadata, never a permit.
func (s *Service) lookupReview(ctx context.Context, input reviewInput, after uint64, witnesses bool) (reviewLookup, error) {
	var l reviewLookup
	s.mu.Lock()
	if err := s.refreshReviewCaller(ctx, input); err != nil {
		return l, err
	}
	through := s.p.through
	// An ID above the watermark is in no commit of this prefix.
	old := after < through && bytes.Compare(input.id[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var err error
	if old {
		l.match, err = s.findCommand(ctx, input.id, after, through)
	}
	if witnesses {
		l.sources, l.sourceErr = s.reviewWitnesses(ctx, input.binding, through)
	}
	s.mu.Lock()
	if e := s.refreshReviewCaller(ctx, input); e != nil {
		return l, e
	}
	if err == nil && !l.match.found && through < s.p.through {
		l.match, err = s.findCommand(ctx, input.id, through, s.p.through)
	}
	return l, err
}

// retryReviewLocked answers a matched command with its immutable receipt. The
// caller's current read access to every reference it names still applies, as
// for scoped retries; no step-4 guard, limit or clock is consulted.
func (s *Service) retryReviewLocked(ctx context.Context, input reviewInput, l reviewLookup) (ReviewReceipt, error) {
	if err := s.reviewReadRights(ctx, input.principal(), input.binding, s.p.through, l.sources); err != nil {
		return ReviewReceipt{}, reviewDenied(err)
	}
	return matchReviewReceipt(l.match, input)
}

// reviewGuardsLocked is step 4 for a new command, in planReview's order: the
// caller as the request's named reviewer, the reducer's head, a witness
// failure, then trusted time. The clock is read after dedup and judged last,
// but every successfully read sample raises this incarnation's floor before
// the head, even when a later guard refuses the call; a failed sample cannot.
func (s *Service) reviewGuardsLocked(ctx context.Context, input reviewInput, witnessErr error) (int64, error) {
	if input.result {
		slot, found, err := s.loadReview(ctx, input.requestID, s.p.through)
		if err != nil {
			return 0, err
		}
		if !found || slot.binding.Reviewer != input.principal() {
			return 0, ErrDenied
		}
	}
	sample, clockErr := s.deps.reviewClock()
	if clockErr == nil {
		s.reviewSample = max(s.reviewSample, sample)
	}
	request, result := reviewRecords(input, sample)
	if _, err := s.reviewHeadAt(ctx, request, result, s.p.through+1); err != nil {
		return 0, err
	}
	if witnessErr != nil {
		return 0, witnessErr
	}
	floor, err := reviewObservation(sample, clockErr, max(s.p.reviewFloor, s.reviewSample), input.binding.DeadlineUTCNS, input.result)
	s.reviewSample = floor
	return sample, err
}

func (s *Service) refreshReviewCaller(ctx context.Context, input reviewInput) error {
	if err := s.usableLocked(ctx); err != nil {
		return err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return err
	}
	principal := input.principal()
	if input.result && principal != input.binding.Reviewer {
		return ErrDenied
	}
	if input.sub.Actor == "agent" {
		if _, err := s.currentChain(ctx, input.sub.Origin, input.sub.GrantID, s.p.through); err != nil {
			return err
		}
	}
	task, found, err := s.loadTask(ctx, input.binding.TaskID, s.p.through)
	if err != nil {
		return err
	}
	if !found {
		return ErrDenied
	}
	_, err = s.reviewParticipant(ctx, principal, input.binding, input.kind, !input.result, task.record, s.p.through)
	return err
}

func matchReviewReceipt(match commandMatch, input reviewInput) (ReviewReceipt, error) {
	if match.review == nil {
		return ReviewReceipt{}, ErrCommandMismatch
	}
	g := match.review
	if g.receipt.Submission.Actor != input.sub.Actor || g.receipt.Submission.Origin != input.sub.Origin {
		return ReviewReceipt{}, ErrDenied
	}
	if match.command.SubmissionSHA256 != input.digest {
		return ReviewReceipt{}, ErrCommandMismatch
	}
	raw, err := g.receipt.Encode()
	if err != nil {
		return ReviewReceipt{}, err
	}
	copy, err := ledger.DecodeRuntimeReviewReceipt(raw)
	if err != nil {
		return ReviewReceipt{}, err
	}
	return ReviewReceipt{RuntimeReviewReceipt: copy, EffectSequence: g.first + 1, Sequence: g.first + uint64(g.receiptIndex)}, nil
}

func (s *Service) reviewProducerGuards(input reviewInput, usage *ledger.ReviewUsage) error {
	if len(input.binding.RequiredEvidence) > s.limits.MaxReviewEvidence || usage != nil && len(usage.Scopes) > s.limits.MaxBudgetScopes {
		return ErrTooLarge
	}
	return nil
}

func (s *Service) prepareReviewLocked(ctx context.Context, input reviewInput, sources map[int64]ledger.Record, sample int64) ([]ledger.Draft, *reviewGroup, reviewPlan, error) {
	drafts, err := s.reviewDrafts(ctx, input, sample)
	if err == nil && s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	var group *reviewGroup
	var plan reviewPlan
	if err == nil {
		var private *privateGroup
		var scoped *scopedGroup
		var paid *paidGroup
		var registration *registrationGroup
		private, scoped, group, paid, registration, err = decodeFamily(drafts, s.p.through+1)
		if err == nil && (private != nil || scoped != nil || paid != nil || registration != nil || group == nil || group.command.CommandID != input.id || group.command.SubmissionSHA256 != input.digest || group.receipt.Submission.Actor != input.sub.Actor || group.receipt.Submission.Origin != input.sub.Origin) {
			err = ErrInvalid
		}
	}
	if err == nil {
		group.sources = sources
		plan, err = s.planReview(ctx, group, s.p.through+1)
	}
	return drafts, group, plan, err
}

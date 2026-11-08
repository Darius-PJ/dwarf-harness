package agents

import (
	"context"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) checkContinuationPending(ctx context.Context, reads *historyReadSet, origin ledger.Origin, c ledger.RuntimeContinuationChanged, through uint64) error {
	for _, p := range c.Pending {
		if p.SourceSequence == 0 {
			continue
		}
		records, err := s.sourceRecords(ctx, reads, []int64{p.SourceSequence}, through)
		if err != nil {
			return err
		}
		r, ok := records[p.SourceSequence]
		if !ok || r.ContextID != origin.ContextID || r.TypeVersion != 1 {
			return ErrInvalid
		}
		if p.Kind != "scratch_observation" && p.Kind != "merge_back" && (r.AgentID != origin.AgentID || r.ExecutionSessionID != origin.SessionID || r.TurnID != c.TurnID) {
			return ErrInvalid
		}
		switch p.Kind {
		case "paid_attempt":
			v, err := ledger.DecodeRuntimePaidReserved(r.Payload)
			if r.Type != ledger.TypeRuntimePaidReserved || err != nil || v.AttemptID != p.ID || v.ParentTurnID != c.TurnID || r.OperationID != p.CommandID || p.ExpectedVersion != v.Version {
				return ErrInvalid
			}
		case "tool":
			v, err := ledger.DecodeRuntimeToolStarted(r.Payload)
			if r.Type != ledger.TypeRuntimeToolStarted || err != nil || v.ToolID != p.ID || v.ParentTurnID != c.TurnID || v.EffectCommandID != p.CommandID || p.ExpectedVersion != 1 {
				return ErrInvalid
			}
		case "local_work":
			v, err := ledger.DecodeRuntimeWorkAdmitted(r.Payload)
			if r.Type != ledger.TypeRuntimeWorkAdmitted || err != nil || v.WorkID != p.ID || v.Kind != "local" || v.ParentTurnID != c.TurnID || r.OperationID != p.CommandID || p.ExpectedVersion != v.Version {
				return ErrInvalid
			}
		case "scratch_observation":
			v, err := ledger.DecodeScratchChanged(r.Payload)
			if r.Type != ledger.TypeScratchChanged || err != nil || v.ObservationID != p.ID || v.ContextID != origin.ContextID || p.ID != p.CommandID {
				return ErrInvalid
			}
		case "merge_back":
			v, err := ledger.DecodeScratchMergeBack(r.Payload)
			if r.Type != ledger.TypeScratchMergeBack || err != nil || v.MergeBackID != p.ID || v.AuthoringContextID != origin.ContextID || p.ID != p.CommandID {
				return ErrInvalid
			}
		default:
			// Unknown pending kinds remain opaque and make their owner inert.
		}
	}
	return nil
}

// guardedContinuation is called from the native producer's whole pure phase,
// so a cold-read suspension reruns the guard against the refreshed owner.
// Ownerless earlier-family logs retain their original rules.
func (s *Service) guardedContinuation(ctx context.Context, reads *historyReadSet, origin ledger.Origin, turn uuid.UUID, kind string, id, command uuid.UUID, through uint64) error {
	o, err := s.loadContinuation(ctx, reads, turn, through)
	if err != nil {
		return err
	}
	if o.Snapshot.Version == 0 {
		return nil
	}
	c := o.Snapshot
	if o.Origin != origin || !o.supported() || c.StopReason != "" || c.State != "model_pending" && c.State != "tool_pending" {
		return ErrContinuationStale
	}
	if kind == "paid_attempt" && c.State != "model_pending" || kind != "paid_attempt" && c.State != "tool_pending" {
		return ErrContinuationStale
	}
	return s.matchContinuationPending(ctx, reads, o, kind, id, command, through)
}

func (s *Service) matchContinuationPending(ctx context.Context, reads *historyReadSet, o continuationState, kind string, id, command uuid.UUID, through uint64) error {
	c, turn := o.Snapshot, o.Snapshot.TurnID
	for _, p := range c.Pending {
		if p.Kind != kind || p.ID != id || p.CommandID != command {
			continue
		}
		if kind != "tool" && p.SourceSequence == 0 {
			return ErrContinuationStale
		}
		type operationIdentity struct {
			Operation  uuid.UUID
			Generation int64
		}
		first, err := foldDirectory(ctx, s, reads, directoryKey{kind: directoryContinuation, id: turn}, 0, through, directoryReadSelection{Purpose: "continuation_pending", PendingKind: kind, PendingID: id}, func(first *operationIdentity, commit ledger.Commit) error {
			if first.Generation != 0 {
				return nil
			}
			for _, record := range commit.Records {
				if record.Type != ledger.TypeRuntimeContinuationChanged || record.TurnID != turn {
					continue
				}
				snapshot, err := ledger.DecodeRuntimeContinuationChanged(record.Payload)
				if err != nil {
					return err
				}
				for _, pending := range snapshot.Pending {
					if pending.Kind == kind && pending.ID == id {
						*first = operationIdentity{snapshot.OperationID, snapshot.Generation}
						return nil
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if first.Operation != c.OperationID || first.Generation != c.Generation {
			return ErrContinuationStale
		}
		return nil
	}
	return ErrContinuationStale
}

func (s *Service) promisedContinuation(ctx context.Context, reads *historyReadSet, command uuid.UUID, through uint64) (uuid.UUID, error) {
	return foldDirectory(ctx, s, reads, directoryKey{kind: directoryPromisedCommand, id: command}, 0, through, directoryReadSelection{Purpose: "promised_native_command"}, func(turn *uuid.UUID, commit ledger.Commit) error {
		for _, r := range commit.Records {
			if r.Type != ledger.TypeRuntimeContinuationChanged {
				continue
			}
			c, err := ledger.DecodeRuntimeContinuationChanged(r.Payload)
			if err != nil {
				return err
			}
			for _, p := range c.Pending {
				if p.CommandID == command {
					if *turn != (uuid.UUID{}) && *turn != c.TurnID {
						return ErrIntegrity
					}
					*turn = c.TurnID
				}
			}
		}
		return nil
	})
}

func (s *Service) guardPromisedCommand(ctx context.Context, reads *historyReadSet, origin ledger.Origin, command uuid.UUID, through uint64) error {
	turn, err := s.promisedContinuation(ctx, reads, command, through)
	if err != nil || turn == (uuid.UUID{}) {
		return err
	}
	o, err := s.loadContinuation(ctx, reads, turn, through)
	if err != nil {
		return err
	}
	for _, p := range o.Snapshot.Pending {
		if p.Kind == "tool" && p.CommandID == command {
			if p.SourceSequence == 0 {
				return ErrContinuationStale
			}
			return s.guardedContinuation(ctx, reads, origin, turn, "tool", p.ID, command, through)
		}
	}
	return ErrContinuationStale
}

func (s *Service) continuationCallback(ctx context.Context, reads *historyReadSet, token continuationToken) (continuationState, error) {
	o, err := s.loadContinuation(ctx, reads, token.turn, s.p.through)
	if err != nil {
		return o, err
	}
	if o.Origin != token.origin || o.Snapshot.OperationID != token.operation || o.Snapshot.Generation != token.generation || o.Snapshot.Version != token.version || o.Snapshot.State != token.phase || !o.supported() || o.Snapshot.StopReason != "" {
		return o, ErrContinuationStale
	}
	if token.result != (uuid.UUID{}) {
		for _, p := range o.Snapshot.Pending {
			if p.ID == token.result {
				return o, nil
			}
		}
		return o, ErrContinuationStale
	}
	return o, nil
}

// Removing a promise is progress only after its native result. A cancelled
// pre-mint with no admission is the exception: it has never owned an effect.
func (s *Service) continuationProgress(ctx context.Context, reads *historyReadSet, old continuationState, next ledger.RuntimeContinuationChanged, through uint64) error {
	if next.State == "ready" && len(next.Pending) != 0 {
		return ErrInvalid
	}
	for _, prior := range old.Snapshot.Pending {
		retained := false
		for _, pending := range next.Pending {
			if pending.Kind != prior.Kind || pending.ID != prior.ID {
				continue
			}
			retained = true
			if prior.SourceSequence != 0 && pending != prior {
				return ErrInvalid
			}
			break
		}
		if retained {
			continue
		}
		abandoned := next.StopReason != "" || next.State == "reconciling"
		if prior.SourceSequence == 0 && !abandoned {
			return ErrInvalid
		}
		switch prior.Kind {
		case "paid_attempt":
			attempt, found, err := s.loadPaid(ctx, reads, prior.ID, through, ledger.Digest{})
			if err != nil {
				return err
			}
			if found && (!attempt.ended || attempt.origin != old.Origin || attempt.admission.ParentTurnID != old.Snapshot.TurnID) || !found && prior.SourceSequence != 0 {
				return ErrInvalid
			}
		case "local_work":
			work, found, err := s.loadWork(ctx, reads, prior.ID, through)
			if err != nil {
				return err
			}
			if found && (!work.ended || work.origin != old.Origin || work.admission.ParentTurnID != old.Snapshot.TurnID) || !found && prior.SourceSequence != 0 {
				return ErrInvalid
			}
		case "tool":
			if _, active := s.p.loopTools[prior.ID]; active {
				return ErrInvalid
			}
			if prior.SourceSequence == 0 {
				continue
			}
			found, err := foldDirectory(ctx, s, reads, directoryKey{kind: directoryToolResult, id: prior.ID}, 0, through, directoryReadSelection{Purpose: "continuation_tool_result"}, func(found *bool, commit ledger.Commit) error {
				for _, record := range commit.Records {
					if record.Type != ledger.TypeRuntimeToolResult || record.TurnID != old.Snapshot.TurnID {
						continue
					}
					result, err := ledger.DecodeRuntimeToolResult(record.Payload)
					if err != nil {
						return err
					}
					if result.ToolID == prior.ID && result.StartedSequence == prior.SourceSequence && record.AgentID == old.Origin.AgentID && record.ExecutionSessionID == old.Origin.SessionID && record.ContextID == old.Origin.ContextID {
						*found = true
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			if !found {
				return ErrInvalid
			}
		case "scratch_observation", "merge_back":
			if prior.SourceSequence == 0 {
				return ErrInvalid
			}
		default:
			return ErrUnavailable
		}
	}
	return nil
}

// localContinuationClaim permits only reservation planning at a persisted local
// wait. The normal native preflight remains strict and runs again after resume.
func (s *Service) localContinuationClaim(ctx context.Context, reads *historyReadSet, origin ledger.Origin, command uuid.UUID) (continuationState, uuid.UUID, error) {
	turn, err := s.promisedContinuation(ctx, reads, command, s.p.through)
	if err != nil || turn == (uuid.UUID{}) {
		return continuationState{}, uuid.UUID{}, err
	}
	o, err := s.loadContinuation(ctx, reads, turn, s.p.through)
	if err != nil {
		return o, uuid.UUID{}, err
	}
	c := o.Snapshot
	waiting := c.State == "waiting_resource" && c.ResumeState == "tool_pending" && (c.WaitReason == "local_slot" || c.WaitReason == "local_buffer")
	if o.Origin != origin || !o.supported() || c.StopReason != "" || c.State != "tool_pending" && !waiting {
		return o, uuid.UUID{}, ErrContinuationStale
	}
	for _, pending := range c.Pending {
		if pending.Kind != "tool" || pending.CommandID != command || pending.SourceSequence == 0 {
			continue
		}
		err := s.matchContinuationPending(ctx, reads, o, "tool", pending.ID, command, s.p.through)
		return o, pending.ID, err
	}
	return o, uuid.UUID{}, ErrContinuationStale
}

func (s *Service) resumeLocalClaim(ctx context.Context, origin ledger.Origin, command uuid.UUID) error {
	ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
	var tool uuid.UUID
	o, err := queryHistory(ctx, s, &ids, func(reads *historyReadSet) (continuationState, error) {
		var state continuationState
		var err error
		state, tool, err = s.localContinuationClaim(ctx, reads, origin, command)
		return state, err
	})
	if err != nil || o.Snapshot.Version == 0 || o.Snapshot.State != "waiting_resource" {
		return err
	}
	token := o.token(tool)
	_, err = s.updateContinuation(ctx, origin, o.Snapshot.TurnID, "owner.advance", &token, func(a *ledger.ContinuationArguments, _ continuationState, _ uuid.UUID) error {
		a.State, a.ResumeState, a.WaitReason = a.ResumeState, "", "none"
		return nil
	})
	return err
}

// A prepared exchange retains its whole physical claim across the owner's
// resume. The paid intent's normal preflight then repeats every native guard.
func (s *Service) resumePaidClaim(ctx context.Context, attempt paidAttempt) error {
	ids := submissionIDs{attempt.origin.AgentID, attempt.origin.SessionID, attempt.origin.ContextID}
	owner, err := queryHistory(ctx, s, &ids, func(reads *historyReadSet) (continuationState, error) {
		o, err := s.loadContinuation(ctx, reads, attempt.admission.ParentTurnID, s.p.through)
		if err != nil || o.Snapshot.Version == 0 {
			return o, err
		}
		c := o.Snapshot
		waiting := c.State == "waiting_resource" && c.ResumeState == "model_pending" && (c.WaitReason == "paid_prepare" || c.WaitReason == "paid_exchange")
		if o.Origin != attempt.origin || !o.supported() || c.StopReason != "" || c.State != "model_pending" && !waiting {
			return o, ErrContinuationStale
		}
		return o, s.matchContinuationPending(ctx, reads, o, "paid_attempt", attempt.admission.AttemptID, attempt.receipt.CommandID, s.p.through)
	})
	if err != nil || owner.Snapshot.Version == 0 || owner.Snapshot.State != "waiting_resource" {
		return err
	}
	token := owner.token(attempt.admission.AttemptID)
	_, err = s.updateContinuation(ctx, attempt.origin, owner.Snapshot.TurnID, "owner.advance", &token, func(a *ledger.ContinuationArguments, _ continuationState, _ uuid.UUID) error {
		a.State, a.ResumeState, a.WaitReason = a.ResumeState, "", "none"
		return nil
	})
	return err
}

// Result reads retain their whole local claim across the resume. A first-try
// read only validates its token; it emits no same-phase counter update.
func (s *Service) resumePaidRead(ctx context.Context, token continuationToken) (continuationState, error) {
	ids := submissionIDs{token.origin.AgentID, token.origin.SessionID, token.origin.ContextID}
	owner, err := queryHistory(ctx, s, &ids, func(reads *historyReadSet) (continuationState, error) {
		return s.continuationCallback(ctx, reads, token)
	})
	if err != nil || owner.Snapshot.State == "model_pending" {
		return owner, err
	}
	c := owner.Snapshot
	if c.State != "waiting_resource" || c.ResumeState != "model_pending" || c.WaitReason != "local_slot" && c.WaitReason != "local_buffer" {
		return owner, ErrContinuationStale
	}
	return s.updateContinuation(ctx, token.origin, token.turn, "owner.advance", &token, func(a *ledger.ContinuationArguments, _ continuationState, _ uuid.UUID) error {
		a.State, a.ResumeState, a.WaitReason = "model_pending", "", "none"
		return nil
	})
}

// A finishing read keeps its process reservation across this CAS. The caller
// already holds the ending gate; the resume never acquires it again.
func (s *Service) resumeFinishingClaim(ctx context.Context, expected continuationState) (continuationState, error) {
	return s.updateContinuation(ctx, expected.Origin, expected.Snapshot.TurnID, "owner.advance", nil, func(a *ledger.ContinuationArguments, current continuationState, _ uuid.UUID) error {
		if current.token(uuid.UUID{}) != expected.token(uuid.UUID{}) {
			return ErrContinuationStale
		}
		if a.State == "finishing" {
			return nil
		}
		if a.State != "waiting_resource" || a.ResumeState != "finishing" || a.WaitReason != "local_slot" && a.WaitReason != "local_buffer" {
			return ErrContinuationStale
		}
		a.State, a.ResumeState, a.WaitReason = "finishing", "", "none"
		return nil
	})
}

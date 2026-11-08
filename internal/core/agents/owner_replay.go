package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func isContinuationType(typ string) bool {
	return typ == ledger.TypeRuntimeContinuationChanged || typ == ledger.TypeRuntimeContinuationReceipt
}

func decodeContinuationGroup(d []ledger.Draft, first uint64) (*continuationGroup, error) {
	bad := func() (*continuationGroup, error) { return nil, inconsistent(first, "continuation group") }
	if len(d) != 3 || d[0].Type != ledger.TypeCommand || d[1].Type != ledger.TypeRuntimeContinuationChanged || d[2].Type != ledger.TypeRuntimeContinuationReceipt {
		return bad()
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad()
	}
	change, err := ledger.DecodeRuntimeContinuationChanged(d[1].Payload)
	if err != nil {
		return bad()
	}
	receipt, err := ledger.DecodeRuntimeContinuationReceipt(d[2].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 || receipt.EffectSequence != int64(first+1) {
		return bad()
	}
	sub := receipt.Submission
	raw, err := sub.Encode()
	if err != nil || ledger.Digest(sha256.Sum256(raw)) != command.SubmissionSHA256 || !sameScopedValue(change, continuationChange(sub)) {
		return bad()
	}
	for i, r := range d {
		actor := ledger.Actor(sub.Actor)
		if i == 1 {
			actor = ledger.ActorCore
		}
		if r.TypeVersion != 1 || r.Actor != actor || r.AgentID != sub.Origin.AgentID || r.ExecutionSessionID != sub.Origin.SessionID || r.ContextID != sub.Origin.ContextID || r.TurnID != sub.TurnID || r.OperationID != command.CommandID || r.MeasurementPath != "" || len(r.BodyRefs) != 0 {
			return bad()
		}
	}
	return &continuationGroup{submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}, first, command, change, receipt}, nil
}

type continuationHistory struct {
	Origin                     ledger.Origin
	Snapshot                   json.RawMessage
	Sequence                   uint64
	Outcome                    string
	ModelCalls, ToolDispatches int64
}

func (s *Service) loadContinuation(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (continuationState, error) {
	if o, ok := s.p.owners[id]; ok && through == s.p.through {
		return o, nil
	}
	v, err := foldDirectory(ctx, s, reads, directoryKey{kind: directoryContinuation, id: id}, 0, through, directoryReadSelection{Purpose: "continuation"}, func(v *continuationHistory, c ledger.Commit) error {
		for _, r := range c.Records {
			if r.TurnID != id {
				continue
			}
			switch r.Type {
			case ledger.TypeRuntimeContinuationChanged:
				p, err := ledger.DecodeRuntimeContinuationChanged(r.Payload)
				if err != nil {
					return err
				}
				v.Origin = ledger.Origin{AgentID: r.AgentID, SessionID: r.ExecutionSessionID, ContextID: r.ContextID}
				v.Snapshot, v.Sequence = slices.Clone(r.Payload), r.Seq
				v.ModelCalls, v.ToolDispatches = p.ModelCalls, p.ToolDispatches
			case ledger.TypeRuntimeContinuationReceipt:
				p, err := ledger.DecodeRuntimeContinuationReceipt(r.Payload)
				if err != nil {
					return err
				}
				v.Outcome = p.Outcome
			case ledger.TypeRuntimePaidDispatchIntent:
				if v.ModelCalls == math.MaxInt64 {
					return ErrIntegrity
				}
				v.ModelCalls++
			case ledger.TypeRuntimeToolResult:
				if v.ToolDispatches == math.MaxInt64 {
					return ErrIntegrity
				}
				v.ToolDispatches++
			}
		}
		return nil
	})
	o := continuationState{Origin: v.Origin, Sequence: v.Sequence, Outcome: v.Outcome, ModelCalls: v.ModelCalls, ToolDispatches: v.ToolDispatches}
	if err == nil && v.Snapshot != nil {
		o.Snapshot, err = ledger.DecodeRuntimeContinuationChanged(v.Snapshot)
	}
	return o, err
}

func (s *Service) continuationBinding(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (*loopGroup, error) {
	v, err := foldDirectory(ctx, s, reads, directoryKey{kind: directoryTurnBound, id: id}, 0, through, directoryReadSelection{Purpose: "continuation_binding"}, func(v *historyCommitValue, c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type == ledger.TypeRuntimeTurnBound && r.TurnID == id {
				*v = encodeHistoryCommit(c)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if v.First == 0 {
		return nil, ErrInvalid
	}
	c, err := decodeHistoryCommit(v)
	if err != nil {
		return nil, err
	}
	return decodeLoopGroup(draftsOf(c), c.First)
}

func (s *Service) planContinuation(ctx context.Context, reads *historyReadSet, g *continuationGroup, first uint64) (continuationState, error) {
	var zero continuationState
	sub, next, through := g.receipt.Submission, g.change, first-1
	if !s.p.registeredOrigin(sub.Origin) {
		return zero, ErrNotBound
	}
	old, err := s.loadContinuation(ctx, reads, sub.TurnID, through)
	if err != nil {
		return zero, err
	}
	prior := old.Snapshot
	if prior.Version != sub.ExpectedVersion || prior.Version != 0 && (old.Origin != sub.Origin || prior.State != sub.ExpectedState) {
		return zero, ErrContinuationStale
	}
	if prior.State == "terminal" {
		return zero, ErrContinuationStale
	}
	if prior.Version != 0 && !old.supported() {
		return zero, ErrUnavailable
	}
	if next.ModelCalls != old.ModelCalls || next.ToolDispatches != old.ToolDispatches {
		return zero, ErrInvalid
	}
	if prior.Version == 0 {
		bound, err := s.continuationBinding(ctx, reads, sub.TurnID, through)
		if err != nil {
			return zero, err
		}
		if sub.Operation != "owner.initialize" || bound.receipt.Submission.Origin != sub.Origin || next.BoundSequence != int64(bound.first+1) || next.OperationID != g.command.CommandID || next.Generation != 1 {
			return zero, ErrInvalid
		}
	} else {
		if next.BoundSequence != prior.BoundSequence || next.DeadlineUTCNS != prior.DeadlineUTCNS {
			return zero, ErrInvalid
		}
		if prior.NativeEndingSequence != 0 && next.NativeEndingSequence != prior.NativeEndingSequence {
			return zero, ErrInvalid
		}
		if err := continuationEdge(g, prior); err != nil {
			return zero, err
		}
		for _, cursor := range prior.NoticeCursors {
			found := false
			for _, n := range next.NoticeCursors {
				if n.WorkspaceID == cursor.WorkspaceID && n.ScratchID == cursor.ScratchID && n.AfterSequence >= cursor.AfterSequence {
					found = true
				}
			}
			if !found {
				return zero, ErrInvalid
			}
		}
	}
	work, found, err := s.loadWork(ctx, reads, sub.TurnID, through)
	if err != nil {
		return zero, err
	}
	if !found || work.origin != sub.Origin || work.admission.Kind != "turn" {
		return zero, ErrInvalid
	}
	if sub.Operation == "owner.stop" && work.ended {
		return zero, ErrContinuationStale
	}
	if next.NativeEndingSequence != 0 {
		if !work.ended {
			return zero, ErrInvalid
		}
		native, err := s.loopNative(ctx, reads, work.receipt.CommandID, sub.Origin, through)
		if err != nil {
			return zero, err
		}
		if next.NativeEndingSequence != int64(native.first)+int64(native.effectIndex) {
			return zero, ErrInvalid
		}
	}
	if next.State == "terminal" {
		ending, err := s.continuationEnding(ctx, reads, sub.TurnID, through)
		if err != nil {
			return zero, err
		}
		if ending == nil || ending.ending.EndingSequence != next.NativeEndingSequence || ending.ending.ModelCalls != next.ModelCalls || ending.ending.ToolDispatches != next.ToolDispatches || !sameScopedValue(ending.ending.FinalMessageObject, next.FinalMessageObject) || next.StopReason != "" && ending.ending.Reason != next.StopReason {
			return zero, ErrInvalid
		}
		if len(next.Pending) != 0 {
			return zero, ErrInvalid
		}
	}
	if err := s.checkContinuationPending(ctx, reads, sub.Origin, next, through); err != nil {
		return zero, err
	}
	if continuationSupported(next) && g.receipt.Outcome == "recorded" {
		if err := s.continuationProgress(ctx, reads, old, next, through); err != nil {
			return zero, err
		}
	}
	return continuationState{Origin: sub.Origin, Snapshot: next, Sequence: first + 1, Outcome: g.receipt.Outcome, ModelCalls: next.ModelCalls, ToolDispatches: next.ToolDispatches}, nil
}

func continuationEdge(g *continuationGroup, old ledger.RuntimeContinuationChanged) error {
	n, op := g.change, g.receipt.Submission.Operation
	newOperation := false
	if !continuationSupported(old) {
		return ErrUnavailable
	}
	if !continuationSupported(n) {
		return nil
	} // Retained unsupported history, never executable.
	if n.State != "waiting_resource" && n.State != "stopping" && n.State != "reconciling" && n.WaitReason != "none" {
		return ErrInvalid
	}
	switch op {
	case "owner.stop":
		if old.StopReason != "" || n.State != "stopping" || n.StopReason == "" || !slices.Equal(n.Pending, old.Pending) || n.FinalMessageObject != nil || n.CancelCommandID == g.command.CommandID {
			return ErrInvalid
		}
		newOperation = true
	case "owner.reconcile":
		if n.State != "reconciling" {
			return ErrInvalid
		}
		newOperation = old.State != "reconciling" && old.State != "stopping"
	case "owner.close":
		if old.State != "finishing" && old.State != "reconciling" || n.State != "terminal" {
			return ErrInvalid
		}
	case "owner.advance":
		switch {
		case old.State == "ready":
			if n.State != "model_pending" && n.State != "tool_pending" && n.State != "finishing" {
				return ErrInvalid
			}
			newOperation = true
		case n.State == "waiting_resource":
			if old.State == "waiting_resource" {
				if n.ResumeState != old.ResumeState || n.WaitReason != old.WaitReason {
					return ErrInvalid
				}
			} else if n.ResumeState != old.State || old.State != "model_pending" && old.State != "tool_pending" && old.State != "finishing" && old.State != "reconciling" {
				return ErrInvalid
			}
		case old.State == "waiting_resource":
			if n.State != old.ResumeState {
				return ErrInvalid
			}
		case n.State == old.State:
		case old.State == "stopping":
			if n.State != "finishing" && n.State != "reconciling" {
				return ErrInvalid
			}
		case old.State == "model_pending" || old.State == "tool_pending":
			if n.State != "ready" && n.State != "finishing" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	recoveryClose := op == "owner.close" && old.State == "reconciling" && old.StopReason == ""
	if op != "owner.stop" && (!recoveryClose && n.StopReason != old.StopReason || n.CancelCommandID != old.CancelCommandID) {
		return ErrInvalid
	}
	if old.StopReason != "" && n.FinalMessageObject != nil {
		return ErrInvalid
	}
	if newOperation {
		if old.Generation == math.MaxInt64 || n.Generation != old.Generation+1 || n.OperationID != g.command.CommandID {
			return ErrInvalid
		}
	} else if n.Generation != old.Generation || n.OperationID != old.OperationID {
		return ErrContinuationStale
	}
	if sameScopedValue(continuationArguments(old), g.receipt.Submission.Arguments) {
		return ErrInvalid
	}
	return nil
}

func (s *Service) continuationEnding(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (*loopGroup, error) {
	v, err := foldDirectory(ctx, s, reads, directoryKey{kind: directoryTurnResult, id: id}, 0, through, directoryReadSelection{Purpose: "continuation_ending"}, func(v *historyCommitValue, c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type == ledger.TypeRuntimeTurnResult && r.TurnID == id {
				*v = encodeHistoryCommit(c)
			}
		}
		return nil
	})
	if err != nil || v.First == 0 {
		return nil, err
	}
	c, err := decodeHistoryCommit(v)
	if err != nil {
		return nil, err
	}
	return decodeLoopGroup(draftsOf(c), c.First)
}

func (p *projection) installContinuation(o continuationState) {
	if o.Snapshot.State == "terminal" && o.supported() {
		delete(p.owners, o.Snapshot.TurnID)
	} else {
		p.owners[o.Snapshot.TurnID] = o
	}
}

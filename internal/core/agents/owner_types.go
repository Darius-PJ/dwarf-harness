package agents

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// ErrContinuationStale refuses a callback from a replaced owner operation.
var ErrContinuationStale = errors.New("agents: stale continuation")

// ErrRecoveryPending means bookkeeping could not prove every child ended.
// It grants neither permission to execute nor permission to release a hold.
var ErrRecoveryPending = errors.New("agents: continuation recovery pending")

// LoopReconciliation requests bookkeeping only, never resumed execution.
type LoopReconciliation struct {
	CommandID            uuid.UUID
	TurnID               uuid.UUID
	ExpectedOwnerVersion int64
}

// ContinuationReceipt identifies the accepted owner transition. Later child
// bookkeeping does not replace this receipt on an idempotent retry.
type ContinuationReceipt struct {
	CommandID        uuid.UUID     `json:"command_id"`
	SubmissionSHA256 ledger.Digest `json:"submission_sha256"`
	Sequence         uint64        `json:"sequence"`
	EffectSequence   uint64        `json:"effect_sequence"`
	State            string        `json:"state"`
	OwnerVersion     int64         `json:"owner_version"`
	OperationID      uuid.UUID     `json:"operation_id"`
	Generation       int64         `json:"generation"`
}

type continuationGroup struct {
	ids     submissionIDs
	first   uint64
	command ledger.Command
	change  ledger.RuntimeContinuationChanged
	receipt ledger.RuntimeContinuationReceipt
}

func (g *continuationGroup) result() ContinuationReceipt {
	return ContinuationReceipt{g.command.CommandID, g.command.SubmissionSHA256, g.first + 2, g.first + 1, g.change.State, g.change.Version, g.change.OperationID, g.change.Generation}
}

// Exported fields permit the existing operation-local cold-read encoding. Only
// unclosed owners remain in the live projection; history uses the directory.
type continuationState struct {
	Origin         ledger.Origin
	Snapshot       ledger.RuntimeContinuationChanged
	Sequence       uint64
	Outcome        string
	ModelCalls     int64
	ToolDispatches int64
}

type continuationToken struct {
	origin                  ledger.Origin
	turn, operation, result uuid.UUID
	generation, version     int64
	phase                   string
}

func (o continuationState) token(result uuid.UUID) continuationToken {
	return continuationToken{o.Origin, o.Snapshot.TurnID, o.Snapshot.OperationID, result, o.Snapshot.Generation, o.Snapshot.Version, o.Snapshot.State}
}

func (o continuationState) supported() bool {
	return o.Outcome == "recorded" && continuationSupported(o.Snapshot)
}

func cloneContinuation(a ledger.ContinuationArguments) ledger.ContinuationArguments {
	a.Pending = slices.Clone(a.Pending)
	a.NoticeCursors = slices.Clone(a.NoticeCursors)
	if a.FinalMessageObject != nil {
		a.FinalMessageObject = new(*a.FinalMessageObject)
	}
	return a
}

func continuationArguments(c ledger.RuntimeContinuationChanged) ledger.ContinuationArguments {
	return cloneContinuation(ledger.ContinuationArguments{BoundSequence: c.BoundSequence, State: c.State, ResumeState: c.ResumeState, OperationID: c.OperationID, Generation: c.Generation, WaitReason: c.WaitReason, ModelCalls: c.ModelCalls, ToolDispatches: c.ToolDispatches, DeadlineUTCNS: c.DeadlineUTCNS, Pending: c.Pending, StopReason: c.StopReason, CancelCommandID: c.CancelCommandID, FinalMessageObject: c.FinalMessageObject, NativeEndingSequence: c.NativeEndingSequence, NoticeCursors: c.NoticeCursors})
}

func continuationChange(sub ledger.ContinuationSubmission) ledger.RuntimeContinuationChanged {
	a := sub.Arguments
	return ledger.RuntimeContinuationChanged{TurnID: sub.TurnID, BoundSequence: a.BoundSequence, ExpectedVersion: sub.ExpectedVersion, Version: sub.ExpectedVersion + 1, ExpectedState: sub.ExpectedState, State: a.State, ResumeState: a.ResumeState, OperationID: a.OperationID, Generation: a.Generation, WaitReason: a.WaitReason, ModelCalls: a.ModelCalls, ToolDispatches: a.ToolDispatches, DeadlineUTCNS: a.DeadlineUTCNS, Pending: a.Pending, StopReason: a.StopReason, CancelCommandID: a.CancelCommandID, FinalMessageObject: a.FinalMessageObject, NativeEndingSequence: a.NativeEndingSequence, NoticeCursors: a.NoticeCursors}
}

func sortContinuationPending(p []ledger.ContinuationPending) {
	slices.SortFunc(p, func(a, b ledger.ContinuationPending) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := bytes.Compare(a.ID[:], b.ID[:]); c != 0 {
			return c
		}
		return bytes.Compare(a.CommandID[:], b.CommandID[:])
	})
}

func continuationKnownState(state string) bool {
	switch state {
	case "ready", "model_pending", "tool_pending", "waiting_resource", "finishing", "stopping", "reconciling", "terminal":
		return true
	}
	return false
}

func continuationKnownWait(reason string) bool {
	switch reason {
	case "none", "paid_exchange", "paid_prepare", "local_slot", "local_buffer", "workspace_capture", "workspace_merge_back", "child_quiescence", "effect_reconciliation":
		return true
	}
	return false
}

func continuationKnownStop(reason string) bool {
	switch reason {
	case "", "cancelled", "shutdown", "deadline", "loop_limit", "authority_lost", "integrity", "provider_error", "content_invalid", "request_too_large", "unresolved_result", "scratch_pending":
		return true
	}
	return false
}

func continuationSupported(c ledger.RuntimeContinuationChanged) bool {
	if !continuationKnownState(c.State) || !continuationKnownWait(c.WaitReason) || !continuationKnownStop(c.StopReason) || c.ResumeState != "" && !continuationKnownState(c.ResumeState) {
		return false
	}
	for _, p := range c.Pending {
		switch p.Kind {
		case "paid_attempt", "tool", "local_work", "scratch_observation", "merge_back":
		default:
			return false
		}
	}
	return true
}

func copyContinuationView(view *session.TurnView, owner continuationState) {
	c := owner.Snapshot
	view.OwnerVersion, view.OperationID, view.Generation = c.Version, c.OperationID, c.Generation
	view.SavedPhase, view.ResumeState, view.WaitReason = c.State, c.ResumeState, c.WaitReason
	view.Pending, view.DeadlineUTCNS = slices.Clone(c.Pending), c.DeadlineUTCNS
	view.ModelCalls, view.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
	if view.Result == nil {
		view.Reason = c.StopReason
	}
}

// Views fold child endings without rewriting the saved owner preimage.
func (p *projection) remainingContinuationPending(saved []ledger.ContinuationPending) []ledger.ContinuationPending {
	out := make([]ledger.ContinuationPending, 0, len(saved))
	for _, pending := range saved {
		remaining := true
		if pending.SourceSequence != 0 {
			switch pending.Kind {
			case "paid_attempt":
				child, found := p.paidAttempts[pending.ID]
				remaining = found && !child.ended
			case "local_work":
				child, found := p.work[pending.ID]
				remaining = found && !child.ended
			case "tool":
				_, remaining = p.loopTools[pending.ID]
			case "scratch_observation", "merge_back":
				remaining = false // the referenced F2 record is already applied
			}
		}
		if remaining {
			out = append(out, pending)
		}
	}
	return out
}

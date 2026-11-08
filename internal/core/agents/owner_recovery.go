package agents

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type continuationRecovery struct {
	owner   continuationState
	bound   *loopGroup
	work    scopedWorkState
	paid    []paidAttempt
	local   []scopedWorkState
	tools   []loopToolState
	pending []ledger.ContinuationPending
	ending  *loopGroup
}

// recoverySnapshot is a whole guarded phase: none of its copied children or
// negative lookups survive an unlocked history read without revalidation.
func (s *Service) recoverySnapshot(ctx context.Context, turn uuid.UUID) (continuationRecovery, error) {
	var out continuationRecovery
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
		out = continuationRecovery{}
		if s.loop != nil {
			s.loop.mu.Lock()
			active := s.loop.turns[turn] != nil
			s.loop.mu.Unlock()
			if active {
				return &BusyError{Reason: "turn_active"}
			}
		}
		var err error
		out.owner, err = s.loadContinuation(ctx, reads, turn, s.p.through)
		if err != nil {
			return err
		}
		out.bound, err = s.continuationBinding(ctx, reads, turn, s.p.through)
		if err != nil {
			return err
		}
		origin := out.bound.receipt.Submission.Origin
		var found bool
		out.work, found, err = s.loadWork(ctx, reads, turn, s.p.through)
		if err != nil {
			return err
		}
		if !found || out.work.origin != origin {
			return ErrIntegrity
		}
		out.ending, err = s.continuationEnding(ctx, reads, turn, s.p.through)
		if err != nil {
			return err
		}
		for _, a := range s.p.paidAttempts {
			if a.admission.ParentTurnID != turn || a.ended {
				continue
			}
			if a.origin != origin {
				return ErrIntegrity
			}
			out.paid = append(out.paid, a)
		}
		for _, work := range s.p.work {
			if work.admission.ParentTurnID != turn || work.ended {
				continue
			}
			if work.origin != origin {
				return ErrIntegrity
			}
			out.local = append(out.local, work)
		}
		for _, tool := range s.p.loopTools {
			if tool.start.ParentTurnID != turn {
				continue
			}
			if tool.origin != origin {
				return ErrIntegrity
			}
			out.tools = append(out.tools, tool)
		}
		sequences := make([]int64, 0, len(out.paid)+len(out.local))
		for _, a := range out.paid {
			sequences = append(sequences, int64(a.sequence))
		}
		for _, work := range out.local {
			sequences = append(sequences, int64(work.sequence))
		}
		slices.Sort(sequences)
		sequences = slices.Compact(sequences)
		var records map[int64]ledger.Record
		if len(sequences) != 0 {
			records, err = s.sourceRecords(ctx, reads, sequences, s.p.through)
			if err != nil {
				return err
			}
		}
		out.pending = []ledger.ContinuationPending{}
		for _, a := range out.paid {
			out.pending = append(out.pending, ledger.ContinuationPending{Kind: "paid_attempt", ID: a.admission.AttemptID, CommandID: records[int64(a.sequence)].OperationID, SourceSequence: int64(a.sequence), ExpectedVersion: 1})
		}
		for _, work := range out.local {
			out.pending = append(out.pending, ledger.ContinuationPending{Kind: "local_work", ID: work.admission.WorkID, CommandID: records[int64(work.sequence)].OperationID, SourceSequence: int64(work.sequence), ExpectedVersion: 1})
		}
		for _, tool := range out.tools {
			out.pending = append(out.pending, ledger.ContinuationPending{Kind: "tool", ID: tool.start.ToolID, CommandID: tool.start.EffectCommandID, SourceSequence: int64(tool.sequence), ExpectedVersion: 1})
		}
		sortContinuationPending(out.pending)
		if len(out.pending) > 128 {
			return ErrTooLarge
		}
		return nil
	})
	return out, err
}

func (s *Service) recoveryPolicy(ctx context.Context, bound *loopGroup) (TurnPolicy, ledger.Profile, error) {
	origin := bound.receipt.Submission.Origin
	raw, err := s.readLoopObject(ctx, origin, bound.bound.PolicyObject)
	if err != nil {
		return TurnPolicy{}, ledger.Profile{}, err
	}
	var policy TurnPolicy
	if decodeLoopJSON(raw, &policy) != nil || policy.DeadlineUTCNS <= 0 {
		return policy, ledger.Profile{}, ErrIntegrity
	}
	template := policy
	template.DeadlineUTCNS = 0
	if !template.validTemplate() {
		return policy, ledger.Profile{}, ErrIntegrity
	}
	raw, err = s.readLoopObject(ctx, origin, bound.bound.RenderObject)
	if err != nil {
		return policy, ledger.Profile{}, err
	}
	var render struct {
		Schema   int           `json:"schema"`
		Origin   ledger.Origin `json:"origin"`
		TurnID   uuid.UUID     `json:"turn_id"`
		Provider string        `json:"provider"`
		Model    string        `json:"model"`
	}
	if json.Unmarshal(raw, &render) != nil || render.Schema != 1 || render.Origin != origin || render.TurnID != bound.bound.TurnID || render.Provider != "anthropic" && render.Provider != "openai" || render.Model == "" {
		return policy, ledger.Profile{}, ErrIntegrity
	}
	return policy, ledger.Profile{Provider: render.Provider, ModelID: render.Model}, nil
}

func (s *Service) reconcileLoopChildren(ctx context.Context, state continuationRecovery, policy TurnPolicy) error {
	for _, attempt := range state.paid {
		if _, err := s.ReconcilePaid(ctx, PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: attempt.admission.AttemptID, ExpectedVersion: attempt.version}); err != nil {
			return err
		}
	}
	for _, work := range state.local {
		if _, err := s.ReconcileAdmissions(ctx, EndingRequest{CommandID: uuid.NewV7(), WorkID: work.admission.WorkID, ExpectedVersion: uint64(work.version)}); err != nil {
			return err
		}
	}
	// This temporary value reuses only the existing result journal. It has no
	// scheduler, context, live output, workspace, credentials or executor.
	runtime := &loopRuntime{service: s}
	turn := &loopTurn{origin: state.bound.receipt.Submission.Origin, id: state.bound.bound.TurnID, policy: policy, toolCalls: state.owner.ToolDispatches}
	for _, tool := range state.tools {
		inv := loopInvocation{id: tool.start.ToolID, started: &loopGroup{first: tool.sequence - 1, start: &tool.start}}
		if err := runtime.stopTool(turn, &inv); err != nil {
			return err
		}
	}
	return nil
}

func recoveryPending(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCommandMismatch) || errors.Is(err, ErrContinuationStale) {
		return err
	}
	return errors.Join(ErrRecoveryPending, err)
}

func (s *Service) loopReconcileRetry(ctx context.Context, request LoopReconciliation) (*continuationGroup, error) {
	return queryHistory(ctx, s, nil, func(reads *historyReadSet) (*continuationGroup, error) {
		m, err := s.findCommand(ctx, reads, request.CommandID, 0, s.p.through)
		if err != nil || !m.found {
			return nil, err
		}
		g := m.continuation
		if g == nil || g.receipt.Submission.Actor != "core" || g.receipt.Submission.TurnID != request.TurnID || g.receipt.Submission.ExpectedVersion != request.ExpectedOwnerVersion {
			return nil, ErrCommandMismatch
		}
		switch g.receipt.Submission.Operation {
		case "owner.initialize", "owner.reconcile", "owner.close":
		default:
			return nil, ErrCommandMismatch
		}
		if g.receipt.Outcome != "recorded" {
			return nil, ErrUnavailable
		}
		return g, nil
	})
}

// ReconcileLoop performs journal bookkeeping only. Open/EnableTurnLoop never
// call it, and no child executor or workspace operation is reachable here.
func (s *Service) ReconcileLoop(ctx context.Context, request LoopReconciliation) (ContinuationReceipt, error) {
	var receipt ContinuationReceipt
	if s == nil || !validID(request.CommandID) || !validID(request.TurnID) || request.ExpectedOwnerVersion < 0 {
		return receipt, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return receipt, err
	}
	defer s.active.Done()
	if prior, err := s.loopReconcileRetry(ctx, request); err != nil || prior != nil {
		if prior != nil {
			receipt = prior.result()
		}
		return receipt, err
	}
	bound, err := queryHistory(ctx, s, nil, func(reads *historyReadSet) (*loopGroup, error) {
		return s.continuationBinding(ctx, reads, request.TurnID, s.p.through)
	})
	if err != nil {
		return receipt, recoveryPending(err)
	}
	origin := bound.receipt.Submission.Origin
	ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
	unlock, err := s.acquireScope(ctx, keyFor(ids, "context"))
	if err != nil {
		return receipt, err
	}
	// Hold 1 is intentionally separate from hold 2: ReconcilePaid acquires this
	// same one-slot context gate while publishing its observation.
	state, policy, profile, accepted, err := s.enterLoopRecovery(ctx, request)
	unlock()
	if accepted != nil {
		receipt = accepted.result()
	}
	if err != nil {
		return receipt, recoveryPending(err)
	}
	if accepted != nil && state.bound == nil {
		return receipt, nil
	}
	if err := s.reconcileLoopChildren(ctx, state, policy); err != nil {
		return receipt, recoveryPending(err)
	}
	unlock, err = s.acquireScope(ctx, keyFor(ids, "context"))
	if err != nil {
		return receipt, err
	}
	defer unlock()
	if prior, err := s.loopReconcileRetry(ctx, request); err != nil || prior != nil && accepted == nil {
		if prior != nil {
			receipt = prior.result()
		}
		return receipt, err
	}
	current, err := s.recoverySnapshot(ctx, request.TurnID)
	if err != nil {
		return receipt, recoveryPending(err)
	}
	if current.owner.Snapshot.State == "terminal" {
		if accepted != nil {
			return receipt, nil
		}
		return receipt, ErrContinuationStale
	}
	if err := s.recheckLoopRecovery(ctx, state.owner, current.owner, accepted == nil); err != nil {
		return receipt, err
	}
	if len(current.paid) != 0 || len(current.local) != 0 || len(current.tools) != 0 {
		return receipt, recoveryPending(&BusyError{Reason: "child_unended"})
	}
	runtime := &loopRuntime{service: s}
	turn := &loopTurn{origin: origin, id: request.TurnID, binding: current.bound, policy: policy, profile: profile, endingID: uuid.NewV7(), endCommandID: uuid.NewV7(), modelCalls: current.owner.ModelCalls, toolCalls: current.owner.ToolDispatches}
	ending := current.ending
	if ending == nil {
		if current.work.ended && current.work.outcome == "released" {
			turn.answer, err = s.recoveryFinal(ctx, current.owner, profile)
			if err != nil {
				return receipt, recoveryPending(err)
			}
		}
		native, err := runtime.endNativeLocked(turn)
		if err != nil {
			return receipt, recoveryPending(err)
		}
		manifest, err := runtime.manifest(turn, native.outcome, native.reason, native.final, nil)
		if err != nil {
			return receipt, recoveryPending(err)
		}
		ref, err := s.publishLoopMetadata(ctx, origin, uuid.UUID{}, manifest)
		if err != nil {
			return receipt, recoveryPending(err)
		}
		s.mu.Lock()
		contextState := s.p.scopes[keyFor(ids, "context")]
		version, prior := contextState.revision, contextState.heads["transcript"].SHA256
		s.mu.Unlock()
		revision := ledger.PrivateTurnRevision{Scope: "context", ExpectedVersion: int64(version), Revision: int64(version + 1), Changes: []ledger.PrivateChange{{Slot: "transcript", Mode: "append", SHA256: ref.SHA256, Bytes: ref.Bytes, PreviousSHA256: prior}}}
		ending, err = s.commitLoop(ctx, turn.endCommandID, ledger.LoopSubmission{Schema: 6, Actor: "core", Origin: origin, TurnID: turn.id, Operation: "loop.end", ExpectedVersion: 1, Arguments: ledger.LoopEndArguments{TurnID: turn.id, BoundSequence: int64(current.bound.first + 1), Outcome: native.outcome, Reason: native.reason, ModelCalls: current.owner.ModelCalls, ToolDispatches: current.owner.ToolDispatches, FinalMessageObject: native.final, EndingCommandID: native.endingID}}, &revision)
		if err != nil {
			return receipt, recoveryPending(err)
		}
	}
	owner, err := s.currentContinuation(ctx, origin, request.TurnID)
	if err != nil {
		return receipt, recoveryPending(err)
	}
	if owner.Snapshot.State == "stopping" {
		a := continuationArguments(owner.Snapshot)
		a.State, a.ResumeState, a.WaitReason, a.Pending = "reconciling", "", "none", []ledger.ContinuationPending{}
		a.ModelCalls, a.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
		a.NativeEndingSequence = ending.ending.EndingSequence
		g, err := s.commitContinuation(ctx, uuid.NewV7(), continuationSubmission(owner, "owner.reconcile", a), nil)
		if err != nil {
			return receipt, recoveryPending(err)
		}
		owner = continuationState{Origin: origin, Snapshot: g.change, Sequence: g.first + 1, Outcome: g.receipt.Outcome, ModelCalls: g.change.ModelCalls, ToolDispatches: g.change.ToolDispatches}
	}
	command := uuid.NewV7()
	if accepted == nil {
		if owner.Snapshot.Version != request.ExpectedOwnerVersion {
			return receipt, ErrContinuationStale
		}
		command = request.CommandID
	}
	a := continuationArguments(owner.Snapshot)
	a.State, a.ResumeState, a.WaitReason, a.Pending = "terminal", "", "none", []ledger.ContinuationPending{}
	a.ModelCalls, a.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
	a.NativeEndingSequence, a.FinalMessageObject = ending.ending.EndingSequence, ending.ending.FinalMessageObject
	if ending.ending.Reason != "natural_stop" {
		a.StopReason = ending.ending.Reason
	}
	closed, err := s.commitContinuation(ctx, command, continuationSubmission(owner, "owner.close", a), nil)
	if err != nil {
		return receipt, recoveryPending(err)
	}
	if accepted == nil {
		receipt = closed.result()
	}
	return receipt, nil
}

func (s *Service) enterLoopRecovery(ctx context.Context, request LoopReconciliation) (continuationRecovery, TurnPolicy, ledger.Profile, *continuationGroup, error) {
	var state continuationRecovery
	var policy TurnPolicy
	var profile ledger.Profile
	if prior, err := s.loopReconcileRetry(ctx, request); err != nil || prior != nil {
		return state, policy, profile, prior, err
	}
	state, err := s.recoverySnapshot(ctx, request.TurnID)
	if err != nil {
		return state, policy, profile, nil, err
	}
	owner := state.owner
	if owner.Snapshot.Version != request.ExpectedOwnerVersion {
		return state, policy, profile, nil, ErrContinuationStale
	}
	if owner.Snapshot.State == "terminal" || owner.Snapshot.Version == 0 && state.ending != nil {
		return state, policy, profile, nil, ErrUnavailable
	}
	if owner.Snapshot.Version != 0 && !owner.supported() {
		return state, policy, profile, nil, ErrUnavailable
	}
	policy, profile, err = s.recoveryPolicy(ctx, state.bound)
	if err != nil {
		return state, policy, profile, nil, err
	}
	if owner.Snapshot.Version != 0 && owner.Snapshot.DeadlineUTCNS != policy.DeadlineUTCNS {
		return state, policy, profile, nil, ErrIntegrity
	}
	if owner.Snapshot.State == "reconciling" {
		return state, policy, profile, nil, nil
	}
	a := continuationArguments(owner.Snapshot)
	a.State, a.ResumeState, a.WaitReason, a.Pending = "reconciling", "", "none", state.pending
	a.ModelCalls, a.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
	if state.work.ended {
		a.NativeEndingSequence = int64(state.work.receipt.Sequence - 1)
	}
	operation := "owner.reconcile"
	if owner.Snapshot.Version == 0 {
		owner.Origin, owner.Snapshot.TurnID, owner.Snapshot.State = state.bound.receipt.Submission.Origin, request.TurnID, "absent"
		operation = "owner.initialize"
		a.BoundSequence, a.DeadlineUTCNS = int64(state.bound.first+1), policy.DeadlineUTCNS
		a.NoticeCursors = []ledger.ContinuationNoticeCursor{}
	}
	if owner.Snapshot.State != "stopping" {
		a.OperationID, a.Generation = request.CommandID, owner.Snapshot.Generation+1
	}
	g, err := s.commitContinuation(ctx, request.CommandID, continuationSubmission(owner, operation, a), nil)
	if err == nil {
		state.owner = continuationState{Origin: owner.Origin, Snapshot: g.change, Sequence: g.first + 1, Outcome: g.receipt.Outcome, ModelCalls: g.change.ModelCalls, ToolDispatches: g.change.ToolDispatches}
	}
	return state, policy, profile, g, err
}

func (s *Service) recheckLoopRecovery(ctx context.Context, entered, current continuationState, closeIsCaller bool) error {
	a, b := entered.Snapshot, current.Snapshot
	if !current.supported() || current.Origin != entered.Origin || b.State != "reconciling" && b.State != "stopping" {
		return ErrContinuationStale
	}
	if closeIsCaller && (b.Version != a.Version || b.State != a.State) {
		return ErrContinuationStale
	}
	if b.OperationID == a.OperationID && b.Generation == a.Generation {
		return nil
	}
	if closeIsCaller || b.StopReason == "" || b.Generation != a.Generation+1 {
		return ErrContinuationStale
	}
	stop, err := queryHistory(ctx, s, nil, func(reads *historyReadSet) (commandMatch, error) {
		return s.findCommand(ctx, reads, b.OperationID, 0, s.p.through)
	})
	if err != nil {
		return err
	}
	if stop.continuation == nil || stop.continuation.receipt.Submission.Operation != "owner.stop" || stop.continuation.change.TurnID != b.TurnID || stop.continuation.change.Generation != b.Generation || stop.continuation.change.StopReason != b.StopReason {
		return ErrContinuationStale
	}
	return nil
}

// A released native ending is not sufficient evidence of a final message.
// Missing or invalid selected content becomes unresolved_result, never a new
// model call, while a transient read refusal leaves bookkeeping pending.
func (s *Service) recoveryFinal(ctx context.Context, owner continuationState, profile ledger.Profile) (*ledger.ObjectRef, error) {
	ref := owner.Snapshot.FinalMessageObject
	if ref == nil {
		return nil, nil
	}
	raw, err := s.readLoopObject(ctx, owner.Origin, *ref)
	if errors.Is(err, ErrIntegrity) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var final struct {
		Schema      int               `json:"schema"`
		Kind        string            `json:"kind"`
		Origin      ledger.Origin     `json:"origin"`
		TurnID      uuid.UUID         `json:"turn_id"`
		Provider    string            `json:"provider"`
		Model       string            `json:"model"`
		TextObjects []json.RawMessage `json:"text_objects"`
	}
	if decodeLoopJSON(raw, &final) != nil || final.Schema != 1 || final.Kind != "final_message" || final.Origin != owner.Origin || final.TurnID != owner.Snapshot.TurnID || final.Provider != profile.Provider || final.Model != profile.ModelID {
		return nil, nil
	}
	for _, encoded := range final.TextObjects {
		object, err := loopObjectRef(encoded)
		if err != nil {
			return nil, nil
		}
		raw, err := s.readLoopObject(ctx, owner.Origin, object)
		if errors.Is(err, ErrIntegrity) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var output struct {
			Schema       int           `json:"schema"`
			Kind         string        `json:"kind"`
			AttemptID    uuid.UUID     `json:"attempt_id"`
			Origin       ledger.Origin `json:"origin"`
			ParentTurnID uuid.UUID     `json:"parent_turn_id"`
			BlockIndex   int           `json:"block_index"`
			ChunkIndex   int           `json:"chunk_index"`
			Block        paidTextBlock `json:"block"`
		}
		if decodeLoopJSON(raw, &output) != nil || output.Schema != 1 || output.Kind != "paid_output" || output.Origin != owner.Origin || output.ParentTurnID != owner.Snapshot.TurnID || output.Block.Kind != "text" {
			return nil, nil
		}
		valid, err := queryHistory(ctx, s, nil, func(reads *historyReadSet) (bool, error) {
			attempt, found, err := s.loadPaid(ctx, reads, output.AttemptID, s.p.through, ledger.Digest{})
			if err != nil {
				return false, err
			}
			return found && attempt.origin == owner.Origin && attempt.admission.ParentTurnID == owner.Snapshot.TurnID && attempt.profile.Provider == profile.Provider && attempt.profile.ModelID == profile.ModelID && attempt.result != nil && attempt.result.Kind == "usage" && slices.Contains(attempt.result.MessageObjects, object), nil
		})
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, nil
		}
	}
	return new(*ref), nil
}

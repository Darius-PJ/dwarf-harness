package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type loopManifest struct {
	Schema  int         `json:"schema"`
	Entries []loopEntry `json:"entries"`
}
type loopEntry struct {
	Schema    int           `json:"schema"`
	Kind      string        `json:"kind"`
	Origin    ledger.Origin `json:"origin"`
	TurnID    uuid.UUID     `json:"turn_id"`
	Ordinal   int           `json:"ordinal"`
	Provider  string        `json:"provider,omitempty"`
	Model     string        `json:"model,omitempty"`
	AttemptID uuid.UUID     `json:"attempt_id,omitzero"`
	ToolID    uuid.UUID     `json:"tool_id,omitzero"`
	Content   any           `json:"content"`
}
type loopInputRef struct {
	InputObject ledger.ObjectRef `json:"input_object"`
}
type loopResponseRefs struct {
	OutputObjects []ledger.ObjectRef `json:"output_objects"`
	ReplayObject  ledger.ObjectRef   `json:"replay_object"`
}
type loopRequestRef struct {
	RequestObject ledger.ObjectRef `json:"request_object"`
}
type loopResultRef struct {
	ResultObject ledger.ObjectRef `json:"result_object"`
}
type loopEndRef struct {
	Outcome            string            `json:"outcome"`
	Reason             string            `json:"reason"`
	FinalMessageObject *ledger.ObjectRef `json:"final_message_object,omitempty"`
}
type loopFinalMessage struct {
	Schema      int                `json:"schema"`
	Kind        string             `json:"kind"`
	Origin      ledger.Origin      `json:"origin"`
	TurnID      uuid.UUID          `json:"turn_id"`
	Provider    string             `json:"provider"`
	Model       string             `json:"model"`
	TextObjects []ledger.ObjectRef `json:"text_objects"`
}
type loopFixedError struct {
	Code string `json:"code"`
}
type loopRefusal struct {
	Schema int            `json:"schema"`
	ToolID uuid.UUID      `json:"tool_id"`
	Status string         `json:"status"`
	Error  loopFixedError `json:"error"`
}

// This is a closed producer-only exception for post-revocation draining. It
// cannot carry text, raw JSON, an error string, a file result or tool arguments.
func loopMetadata(value any) bool {
	switch v := value.(type) {
	case loopRefusal:
		return v.Schema == 1 && validID(v.ToolID) && v.Status == "error" && slices.Contains([]string{"denied", "unavailable", "unknown_effect", "too_large", "conflict", "invalid_argument", "busy"}, v.Error.Code)
	case loopManifest:
		if v.Schema != 1 {
			return false
		}
		for _, entry := range v.Entries {
			if entry.Schema != 1 || !validID(entry.TurnID) || entry.Ordinal < 0 {
				return false
			}
			switch entry.Content.(type) {
			case loopInputRef:
				if entry.Kind != "turn_input" {
					return false
				}
			case loopResponseRefs:
				if entry.Kind != "model_response" {
					return false
				}
			case loopRequestRef:
				if entry.Kind != "tool_request" {
					return false
				}
			case loopResultRef:
				if entry.Kind != "tool_result" {
					return false
				}
			case loopEndRef:
				if entry.Kind != "turn_end" {
					return false
				}
			default:
				return false
			}
		}
		return true
	case loopFinalMessage:
		return v.Schema == 1 && v.Kind == "final_message" && validID(v.TurnID)
	default:
		return false
	}
}
func (s *Service) publishLoopMetadata(ctx context.Context, origin ledger.Origin, credential uuid.UUID, value any) (ledger.ObjectRef, error) {
	if !loopMetadata(value) {
		return ledger.ObjectRef{}, ErrInvalid
	}
	raw := modelJSON(value)
	if len(raw) > maxObjectBytes {
		return ledger.ObjectRef{}, ErrTooLarge
	}
	s.mu.Lock()
	available := s.paid != nil && s.paid.secrets[credential] != nil
	s.mu.Unlock()
	if available {
		return s.publishLoopObject(ctx, origin, credential, raw)
	}
	scope, err := s.scopePath(submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}, "context")
	if err != nil {
		return ledger.ObjectRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return ledger.ObjectRef{}, err
	}
	digest, err := scope.publishObject(raw)
	return ledger.ObjectRef{SHA256: digest, Bytes: int64(len(raw))}, err
}

func (r *loopRuntime) manifest(t *loopTurn, outcome, reason string, final *ledger.ObjectRef, owner *continuationState) (loopManifest, error) {
	s := r.service
	manifest := loopManifest{Schema: 1, Entries: []loopEntry{}}
	appendEntry := func(kind string, attempt, tool uuid.UUID, content any) {
		entry := loopEntry{Schema: 1, Kind: kind, Origin: t.origin, TurnID: t.id, Ordinal: len(manifest.Entries), AttemptID: attempt, ToolID: tool, Content: content}
		if kind == "model_response" {
			entry.Provider, entry.Model = t.profile.Provider, t.profile.ModelID
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	s.mu.Lock()
	through := s.p.through
	s.mu.Unlock()
	err := s.scanDirectory(context.Background(), directoryKey{kind: directoryTurn, id: t.id}, t.binding.first-1, through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.TurnID != t.id {
				continue
			}
			if record.AgentID != t.origin.AgentID || record.ExecutionSessionID != t.origin.SessionID || record.ContextID != t.origin.ContextID {
				return ErrIntegrity
			}
			switch record.Type {
			case ledger.TypeRuntimeTurnBound:
				bound, err := ledger.DecodeRuntimeTurnBound(record.Payload)
				if err != nil {
					return err
				}
				if record.Seq != t.binding.first+1 || bound != *t.binding.bound {
					return ErrIntegrity
				}
				appendEntry("turn_input", uuid.UUID{}, uuid.UUID{}, loopInputRef{bound.InputObject})
			case ledger.TypeRuntimePaidResult:
				result, err := ledger.DecodeRuntimePaidResult(record.Payload)
				if err != nil {
					return err
				}
				if result.Kind != "usage" || result.EvidenceObject == nil {
					continue
				}
				raw, err := s.readLoopObjectForOwner(context.Background(), t.origin, *result.EvidenceObject, owner)
				if err != nil {
					return err
				}
				var observation paidObservationObject
				if json.Unmarshal(raw, &observation) != nil || observation.AttemptID != result.AttemptID || observation.Origin != t.origin || observation.ParentTurnID != t.id {
					return ErrIntegrity
				}
				if observation.ReplayObject != nil && observation.Complete && !observation.ContentError {
					appendEntry("model_response", result.AttemptID, uuid.UUID{}, loopResponseRefs{result.MessageObjects, *observation.ReplayObject})
				}
			case ledger.TypeRuntimeToolStarted:
				tool, err := ledger.DecodeRuntimeToolStarted(record.Payload)
				if err != nil {
					return err
				}
				appendEntry("tool_request", tool.AttemptID, tool.ToolID, loopRequestRef{tool.RequestObject})
			case ledger.TypeRuntimeToolResult:
				tool, err := ledger.DecodeRuntimeToolResult(record.Payload)
				if err != nil {
					return err
				}
				appendEntry("tool_result", uuid.UUID{}, tool.ToolID, loopResultRef{tool.ResultObject})
			}
		}
		return nil
	})
	if err != nil {
		return manifest, err
	}
	appendEntry("turn_end", uuid.UUID{}, uuid.UUID{}, loopEndRef{outcome, reason, final})
	return manifest, nil
}

func (r *loopRuntime) finish(t *loopTurn) (err error) {
	s := r.service
	defer func() {
		// A busy capture or step leaves everything for the retry: writers, live
		// text, the reservation and the turn context stay, and the scheduler
		// parks the turn. Steps already done are skipped on re-entry.
		if finishBusy(err) || errors.Is(err, ErrContinuationStale) {
			return
		}
		if err != nil {
			for _, writer := range t.writers {
				writer.Close()
			}
			r.mu.Lock()
			t.state = "recovery_pending"
			r.mu.Unlock()
			t.live.close("discarded", nil, ErrUnavailable)
		}
		r.mu.Lock()
		stopped := t.owner.StopReason != ""
		r.mu.Unlock()
		if err == nil || stopped {
			t.cancel()
		}
		s.mu.Lock()
		s.releaseScopedLocked(&t.reservation)
		s.mu.Unlock()
	}()
	if err := r.resumeFinishWait(t); err != nil {
		return err
	}
	r.mu.Lock()
	call := t.paid
	r.mu.Unlock()
	if call != nil {
		if call.admitted {
			s.mu.Lock()
			var a paidAttempt
			var found bool
			ids := submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}
			e := s.prepareHistoryLocked(context.Background(), &historyReadSet{}, &ids, true, func(reads *historyReadSet) error {
				var err error
				a, found, err = s.loadPaid(context.Background(), reads, call.id, s.p.through, ledger.Digest{})
				return err
			})
			s.mu.Unlock()
			if e != nil {
				return e
			}
			if !found {
				return ErrIntegrity
			}
			if !a.ended {
				if _, e := s.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: call.reconcileID, AttemptID: call.id, ExpectedVersion: a.version}); e != nil {
					return e
				}
			}
		}
		if call.prepared != nil {
			if e := call.prepared.Close(); e != nil {
				return e
			}
		}
		r.mu.Lock()
		t.paid = nil
		r.mu.Unlock()
	}
	for i := range t.tools {
		if t.tools[i].started != nil {
			if err := r.stopTool(t, &t.tools[i]); err != nil {
				return err
			}
		}
	}
	if _, err := s.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", nil, func(a *ledger.ContinuationArguments, owner continuationState, command uuid.UUID) error {
		a.Pending = []ledger.ContinuationPending{}
		if a.State == "stopping" {
			a.State, a.ResumeState, a.WaitReason = "finishing", "", "none"
		}
		return nil
	}); err != nil {
		return err
	}
	r.mu.Lock()
	waitReason := t.owner.WaitReason
	r.mu.Unlock()
	if waitReason == "child_quiescence" || waitReason == "effect_reconciliation" {
		if _, err := r.resumeOwner(t, waitReason); err != nil {
			return err
		}
	}
	if err := r.finishMerge(t); err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(t.writers))
	for id := range t.writers {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range ids {
		writer := t.writers[id]
		scratch := writer.Scratch()
		owner, err := s.currentContinuation(context.Background(), t.origin, t.id)
		if err != nil {
			return err
		}
		t.finishWait = loopFinishWait{kind: "workspace_capture", id: id, operation: owner.Snapshot.OperationID, generation: owner.Snapshot.Generation}
		if s.deps.beforeFinishCommit != nil {
			err = s.deps.beforeFinishCommit(t.id)
		}
		if err == nil {
			_, err = writer.CommitObserved(context.Background())
		}
		if err != nil {
			return err
		}
		writer.Close()
		delete(t.writers, id)
		if scratch.ParentScratchID != (uuid.UUID{}) {
			t.mergeScratch = scratch.ID
		}
		t.finishWait.owed = true
		if err := r.resumeFinishWait(t); err != nil {
			return err
		}
		if err := r.finishMerge(t); err != nil {
			return err
		}
	}
	ending, unlock, err := r.endNative(t)
	if err != nil {
		return err
	}
	defer unlock()
	outcome, reason, final, endingID := ending.outcome, ending.reason, ending.final, ending.endingID
	manifestOwner, err := s.currentContinuation(context.Background(), t.origin, t.id)
	if err != nil {
		return err
	}
	manifest, err := r.manifest(t, outcome, reason, final, &manifestOwner)
	if err != nil {
		return err
	}
	idsBinding := submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}
	ref, err := s.publishLoopMetadata(context.Background(), t.origin, t.credential, manifest)
	if err != nil {
		return err
	}
	s.mu.Lock()
	state := s.p.scopes[keyFor(idsBinding, "context")]
	version, prior := state.revision, state.heads["transcript"].SHA256
	s.mu.Unlock()
	revision := ledger.PrivateTurnRevision{Scope: "context", ExpectedVersion: int64(version), Revision: int64(version + 1), Changes: []ledger.PrivateChange{{Slot: "transcript", Mode: "append", SHA256: ref.SHA256, Bytes: ref.Bytes, PreviousSHA256: prior}}}
	owner, err := s.currentContinuation(context.Background(), t.origin, t.id)
	if err != nil {
		return err
	}
	modelCalls, toolCalls := owner.ModelCalls, owner.ToolDispatches
	_, err = s.commitLoop(context.Background(), t.endCommandID, ledger.LoopSubmission{Schema: 6, Actor: "core", Origin: t.origin, TurnID: t.id, Operation: "loop.end", ExpectedVersion: 1, Arguments: ledger.LoopEndArguments{TurnID: t.id, BoundSequence: int64(t.binding.first + 1), Outcome: outcome, Reason: reason, ModelCalls: modelCalls, ToolDispatches: toolCalls, FinalMessageObject: final, EndingCommandID: endingID}}, &revision)
	if err != nil {
		return err
	}
	owner, err = s.currentContinuation(context.Background(), t.origin, t.id)
	if err != nil {
		return err
	}
	a := continuationArguments(owner.Snapshot)
	a.State, a.ResumeState, a.WaitReason, a.Pending = "terminal", "", "none", []ledger.ContinuationPending{}
	a.ModelCalls, a.ToolDispatches, a.NativeEndingSequence = owner.ModelCalls, owner.ToolDispatches, int64(ending.sequence)
	closed, err := s.commitContinuation(context.Background(), uuid.NewV7(), continuationSubmission(owner, "owner.close", a), nil)
	if err != nil {
		return err
	}
	s.publishContinuation(closed)
	r.mu.Lock()
	t.state, t.reason = "finished", reason
	t.modelCalls, t.toolCalls = owner.ModelCalls, owner.ToolDispatches
	r.mu.Unlock()
	liveState := "discarded"
	if outcome == "finished" {
		liveState = "finished"
	}
	t.live.close(liveState, final, nil)
	return nil
}

// The workspace claim and its effects happen inside one call. After success,
// Close/delete make that step irreversible in RAM; a refused resume is owed
// before the next claim, never permission to repeat the workspace operation.
func (r *loopRuntime) resumeFinishWait(t *loopTurn) error {
	if !t.finishWait.owed {
		return nil
	}
	owner, err := r.service.currentContinuation(context.Background(), t.origin, t.id)
	if err != nil {
		return err
	}
	if owner.Snapshot.OperationID != t.finishWait.operation || owner.Snapshot.Generation != t.finishWait.generation {
		// Only a newer durable stop can void a successful old operation's
		// owed resume; it must never resume a new park under that stop.
		if owner.Snapshot.StopReason == "" || owner.Snapshot.Generation <= t.finishWait.generation {
			return ErrContinuationStale
		}
		if owner.Snapshot.State == "stopping" {
			if _, err := r.ownerPhase(t, "finishing", nil); err != nil {
				return err
			}
		}
		t.finishWait = loopFinishWait{}
		return nil
	}
	switch owner.Snapshot.State {
	case "waiting_resource":
		if owner.Snapshot.ResumeState != "finishing" || owner.Snapshot.WaitReason != t.finishWait.kind {
			return ErrContinuationStale
		}
		if _, err := r.service.updateContinuation(context.Background(), t.origin, t.id, "owner.advance", nil, func(a *ledger.ContinuationArguments, _ continuationState, _ uuid.UUID) error {
			if a.OperationID != t.finishWait.operation || a.Generation != t.finishWait.generation || a.State != "waiting_resource" || a.ResumeState != "finishing" || a.WaitReason != t.finishWait.kind {
				return ErrContinuationStale
			}
			a.State, a.ResumeState, a.WaitReason = "finishing", "", "none"
			return nil
		}); err != nil {
			return err
		}
	case "stopping":
		if _, err := r.ownerPhase(t, "finishing", nil); err != nil {
			return err
		}
	case "finishing":
	default:
		return ErrContinuationStale
	}
	t.finishWait = loopFinishWait{}
	return nil
}

func (r *loopRuntime) finishMerge(t *loopTurn) error {
	if t.mergeScratch == (uuid.UUID{}) {
		return nil
	}
	owner, err := r.ownerPhase(t, "finishing", nil)
	if err != nil {
		return err
	}
	t.finishWait = loopFinishWait{kind: "workspace_merge_back", id: t.mergeScratch, operation: owner.Snapshot.OperationID, generation: owner.Snapshot.Generation}
	if err := r.parkOwner(t, "workspace_merge_back"); err != nil {
		return err
	}
	_, mergeErr := r.workspace.MergeBack(context.Background(), t.mergeScratch)
	t.mergeScratch, t.finishWait.owed = uuid.UUID{}, true
	if mergeErr != nil {
		if err := r.noteStop(t, "scratch_pending"); err != nil {
			return err
		}
	}
	return r.resumeFinishWait(t)
}

// loopEnding is a finishing turn's committed native ending, as loop.end and
// the turn's manifest record it.
type loopEnding struct {
	outcome, reason string
	final           *ledger.ObjectRef
	endingID        uuid.UUID
	sequence        uint64
}

// endNative transfers the context gate to finish, which retains it through
// loop.end and owner.close. No workspace operation runs under this gate.
func (r *loopRuntime) endNative(t *loopTurn) (loopEnding, func(), error) {
	s := r.service
	if reason := s.loopBoundary(t); reason != "" {
		if err := r.noteStop(t, reason); err != nil {
			return loopEnding{}, nil, err
		}
	}
	ids := submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}
	unlock, err := s.acquireScope(context.Background(), keyFor(ids, "context"))
	if err != nil {
		return loopEnding{}, nil, err
	}
	ending, err := r.endNativeLocked(t)
	if err != nil {
		unlock()
		return loopEnding{}, nil, err
	}
	owner, err := s.currentContinuation(context.Background(), t.origin, t.id)
	if err != nil {
		unlock()
		return loopEnding{}, nil, err
	}
	if owner.Snapshot.State == "waiting_resource" {
		return ending, unlock, nil
	}
	a := continuationArguments(owner.Snapshot)
	a.NativeEndingSequence = int64(ending.sequence)
	a.ModelCalls, a.ToolDispatches = owner.ModelCalls, owner.ToolDispatches
	if !sameScopedValue(a, continuationArguments(owner.Snapshot)) {
		g, err := s.commitContinuation(context.Background(), uuid.NewV7(), continuationSubmission(owner, "owner.advance", a), nil)
		if err != nil {
			unlock()
			return loopEnding{}, nil, err
		}
		s.publishContinuation(g)
	}
	return ending, unlock, nil
}

func (r *loopRuntime) endNativeLocked(t *loopTurn) (loopEnding, error) {
	s := r.service
	ids := submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}
	owner, err := s.currentContinuation(context.Background(), t.origin, t.id)
	if err != nil {
		return loopEnding{}, err
	}
	if owner.Snapshot.Version == 0 || !owner.supported() {
		return loopEnding{}, ErrIntegrity
	}
	work, err := queryHistory(context.Background(), s, &ids, func(reads *historyReadSet) (scopedWorkState, error) {
		work, found, err := s.loadWork(context.Background(), reads, t.id, s.p.through)
		if err == nil && (!found || work.origin != t.origin) {
			err = ErrIntegrity
		}
		return work, err
	})
	if err != nil {
		return loopEnding{}, err
	}
	reason, final := owner.Snapshot.StopReason, owner.Snapshot.FinalMessageObject
	if owner.Snapshot.State == "reconciling" {
		final = t.answer // recovery supplies only a validated, already-selected final
		if !work.ended && reason == "" {
			reason, final = "shutdown", nil
		}
	}
	outcome, native := "finished", "released"
	if reason == "" {
		reason = "natural_stop"
	} else {
		final, outcome = nil, "failed"
		if reason == "cancelled" {
			outcome, native = "cancelled", "cancelled"
		}
		if reason == "shutdown" {
			outcome, native = "interrupted", "interrupted"
		}
	}
	ending := loopEnding{outcome: outcome, reason: reason, final: final, endingID: t.endingID}
	receipt := work.receipt
	if !work.ended {
		if native == "cancelled" {
			ending.endingID = owner.Snapshot.CancelCommandID
			if !validID(ending.endingID) {
				return loopEnding{}, ErrIntegrity
			}
			receipt, err = s.executeScoped(context.Background(), scopedCaller{actor: "core"}, "turn.cancel", TurnCancellation{CommandID: ending.endingID, TurnID: t.id, ExpectedVersion: uint64(work.version)})
		} else {
			receipt, err = s.EndTurn(context.Background(), TurnEnding{CommandID: ending.endingID, TurnID: t.id, ExpectedVersion: uint64(work.version), Outcome: native})
		}
		if err != nil {
			return loopEnding{}, err
		}
	} else {
		ending.endingID = receipt.CommandID
		if work.outcome == "cancelled" {
			ending.outcome, ending.reason, ending.final = "cancelled", "cancelled", nil
		}
		if work.outcome == "interrupted" {
			ending.outcome, ending.final = "interrupted", nil
			if ending.reason == "natural_stop" {
				ending.reason = "shutdown"
			}
		}
		if work.outcome == "released" && final == nil && owner.Snapshot.StopReason == "" {
			ending.outcome, ending.reason = "failed", "unresolved_result"
		}
	}
	ending.sequence = receipt.Sequence - 1
	r.mu.Lock()
	t.ending, t.cancelReceipt = &ending, &receipt
	r.mu.Unlock()
	return ending, nil
}

func decodeLoopJSON(raw []byte, out any) error {
	if _, ok := parseModelArguments(raw, maxObjectBytes); !ok {
		return ErrIntegrity
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrIntegrity
	}
	return nil
}
func loopObjectRef(raw json.RawMessage) (ledger.ObjectRef, error) {
	var wire struct {
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	}
	if decodeLoopJSON(raw, &wire) != nil || wire.Bytes <= 0 || wire.Bytes > maxObjectBytes {
		return ledger.ObjectRef{}, ErrIntegrity
	}
	digest, err := ledger.ParseDigest(wire.SHA256)
	if err != nil {
		return ledger.ObjectRef{}, ErrIntegrity
	}
	return ledger.ObjectRef{SHA256: digest, Bytes: wire.Bytes}, nil
}

func (r *loopRuntime) renderTranscript(ctx context.Context, t *loopTurn, raw []byte) (string, error) {
	var manifest struct {
		Schema  int `json:"schema"`
		Entries []struct {
			Schema    int             `json:"schema"`
			Kind      string          `json:"kind"`
			Origin    ledger.Origin   `json:"origin"`
			TurnID    uuid.UUID       `json:"turn_id"`
			Ordinal   int             `json:"ordinal"`
			Provider  string          `json:"provider,omitempty"`
			Model     string          `json:"model,omitempty"`
			AttemptID uuid.UUID       `json:"attempt_id,omitempty"`
			ToolID    uuid.UUID       `json:"tool_id,omitempty"`
			Content   json.RawMessage `json:"content"`
		} `json:"entries"`
	}
	if decodeLoopJSON(raw, &manifest) != nil || manifest.Schema != 1 || len(manifest.Entries) < 2 {
		return "", errLoopContent
	}
	var rendered strings.Builder
	turn := manifest.Entries[0].TurnID
	for i, entry := range manifest.Entries {
		if entry.Schema != 1 || entry.Ordinal != i || entry.Origin != t.origin || entry.TurnID != turn {
			return "", errLoopContent
		}
		switch entry.Kind {
		case "turn_input":
			if i != 0 {
				return "", errLoopContent
			}
			var content struct {
				InputObject json.RawMessage `json:"input_object"`
			}
			if decodeLoopJSON(entry.Content, &content) != nil {
				return "", errLoopContent
			}
			ref, err := loopObjectRef(content.InputObject)
			if err != nil {
				return "", err
			}
			s := r.service
			s.mu.Lock()
			through := s.p.through
			s.mu.Unlock()
			selected := false
			err = s.scanDirectory(ctx, directoryKey{kind: directoryTurnBound, id: turn}, 0, through, func(commit ledger.Commit) error {
				for _, record := range commit.Records {
					if record.Type != ledger.TypeRuntimeTurnBound || record.TurnID != turn || record.AgentID != t.origin.AgentID || record.ExecutionSessionID != t.origin.SessionID || record.ContextID != t.origin.ContextID {
						continue
					}
					bound, err := ledger.DecodeRuntimeTurnBound(record.Payload)
					if err != nil {
						return err
					}
					selected = bound.InputObject == ref
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			if !selected {
				return "", ErrDenied
			}
			input, err := r.service.readLoopObject(ctx, t.origin, ref)
			if err != nil {
				return "", err
			}
			var parsed loopInputObject
			if decodeLoopInput(input, &parsed) != nil || parsed.Origin != t.origin || parsed.TurnID != turn {
				return "", errLoopContent
			}
			rendered.WriteString("\nPrior owner input:\n" + parsed.Text)
		case "model_response", "tool_request", "tool_result":
			// Prior completed turns preserve these sources in the transcript but
			// do not turn old reasoning/tool commentary into the next owner input.
		case "turn_end":
			if i != len(manifest.Entries)-1 {
				return "", errLoopContent
			}
			var content struct {
				Outcome            string          `json:"outcome"`
				Reason             string          `json:"reason"`
				FinalMessageObject json.RawMessage `json:"final_message_object,omitempty"`
			}
			if decodeLoopJSON(entry.Content, &content) != nil {
				return "", errLoopContent
			}
			if content.Outcome == "finished" {
				ref, err := loopObjectRef(content.FinalMessageObject)
				if err != nil {
					return "", err
				}
				text, err := r.finalText(ctx, t.origin, turn, ref)
				if err != nil {
					return "", err
				}
				rendered.WriteString("\nPrior assistant answer:\n" + text)
			}
		default:
			return "", errLoopContent
		}
		if rendered.Len() > 512<<10 {
			return "", ErrTooLarge
		}
	}
	return rendered.String(), nil
}

func (r *loopRuntime) finalText(ctx context.Context, origin ledger.Origin, turn uuid.UUID, ref ledger.ObjectRef) (string, error) {
	s := r.service
	s.mu.Lock()
	through := s.p.through
	s.mu.Unlock()
	selected := false
	err := s.scanDirectory(ctx, directoryKey{kind: directoryTurnResult, id: turn}, 0, through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeTurnResult && record.TurnID == turn && record.AgentID == origin.AgentID && record.ExecutionSessionID == origin.SessionID && record.ContextID == origin.ContextID {
				result, err := ledger.DecodeRuntimeTurnResult(record.Payload)
				if err != nil {
					return err
				}
				selected = result.Outcome == "finished" && result.FinalMessageObject != nil && *result.FinalMessageObject == ref
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !selected {
		return "", ErrDenied
	}
	raw, err := s.readLoopObject(ctx, origin, ref)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Schema      int               `json:"schema"`
		Kind        string            `json:"kind"`
		Origin      ledger.Origin     `json:"origin"`
		TurnID      uuid.UUID         `json:"turn_id"`
		Provider    string            `json:"provider"`
		Model       string            `json:"model"`
		TextObjects []json.RawMessage `json:"text_objects"`
	}
	if decodeLoopJSON(raw, &manifest) != nil || manifest.Schema != 1 || manifest.Kind != "final_message" || manifest.Origin != origin || manifest.TurnID != turn {
		return "", ErrIntegrity
	}
	var text strings.Builder
	reads := &historyReadSet{}
	ids := submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}
	for _, rawRef := range manifest.TextObjects {
		object, err := loopObjectRef(rawRef)
		if err != nil {
			return "", err
		}
		raw, err := s.readLoopObject(ctx, origin, object)
		if err != nil {
			return "", err
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
		if decodeLoopJSON(raw, &output) != nil || output.Schema != 1 || output.Kind != "paid_output" || output.Origin != origin || output.ParentTurnID != turn || output.Block.Kind != "text" {
			return "", ErrIntegrity
		}
		s.mu.Lock()
		var attempt paidAttempt
		var found bool
		err = s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
			var err error
			attempt, found, err = s.loadPaid(ctx, reads, output.AttemptID, s.p.through, ledger.Digest{})
			return err
		})
		s.mu.Unlock()
		if err != nil {
			return "", err
		}
		if !found || attempt.origin != origin || attempt.admission.ParentTurnID != turn || attempt.profile.Provider != manifest.Provider || attempt.profile.ModelID != manifest.Model || attempt.result == nil || !slices.Contains(attempt.result.MessageObjects, object) {
			return "", ErrDenied
		}
		if _, err := r.readPaid(ctx, origin, output.AttemptID, object, nil); err != nil {
			return "", err
		}
		text.WriteString(output.Block.Text)
		if text.Len() > 8<<20 {
			return "", ErrTooLarge
		}
	}
	return text.String(), nil
}

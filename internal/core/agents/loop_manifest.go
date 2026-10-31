package agents

import (
	"bytes"
	"context"
	"encoding/json"
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

func (r *loopRuntime) manifest(t *loopTurn, outcome, reason string, final *ledger.ObjectRef) (loopManifest, error) {
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
	err := s.scan(context.Background(), t.binding.first-1, through, func(c ledger.Commit) error {
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
				raw, err := s.readLoopObject(context.Background(), t.origin, *result.EvidenceObject)
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
		if finishBusy(err) {
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
		t.cancel()
		s.mu.Lock()
		s.releaseScopedLocked(&t.reservation)
		s.mu.Unlock()
	}()
	r.mu.Lock()
	call := t.paid
	r.mu.Unlock()
	if call != nil {
		if call.admitted {
			s.mu.Lock()
			a, found, e := s.loadPaid(context.Background(), call.id, s.p.through, ledger.Digest{})
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
	ids := make([]uuid.UUID, 0, len(t.writers))
	for id := range t.writers {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range ids {
		writer := t.writers[id]
		scratch := writer.Scratch()
		var err error
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
			if _, err := r.workspace.MergeBack(context.Background(), scratch.ID); err != nil {
				r.mu.Lock()
				if t.reason == "" {
					t.reason = "scratch_pending"
				}
				r.mu.Unlock()
			}
		}
	}
	ending, err := r.endNative(t)
	if err != nil {
		return err
	}
	outcome, reason, final, endingID := ending.outcome, ending.reason, ending.final, ending.endingID
	manifest, err := r.manifest(t, outcome, reason, final)
	if err != nil {
		return err
	}
	idsBinding := submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}
	unlock, err := s.acquireScope(context.Background(), keyFor(idsBinding, "context"))
	if err != nil {
		return err
	}
	defer unlock()
	ref, err := s.publishLoopMetadata(context.Background(), t.origin, t.credential, manifest)
	if err != nil {
		return err
	}
	s.mu.Lock()
	state := s.p.scopes[keyFor(idsBinding, "context")]
	version, prior := state.revision, state.heads["transcript"].SHA256
	s.mu.Unlock()
	revision := ledger.PrivateTurnRevision{Scope: "context", ExpectedVersion: int64(version), Revision: int64(version + 1), Changes: []ledger.PrivateChange{{Slot: "transcript", Mode: "append", SHA256: ref.SHA256, Bytes: ref.Bytes, PreviousSHA256: prior}}}
	r.mu.Lock()
	modelCalls, toolCalls := t.modelCalls, t.toolCalls
	r.mu.Unlock()
	_, err = s.commitLoop(context.Background(), t.endCommandID, ledger.LoopSubmission{Schema: 6, Actor: "core", Origin: t.origin, TurnID: t.id, Operation: "loop.end", ExpectedVersion: 1, Arguments: ledger.LoopEndArguments{TurnID: t.id, BoundSequence: int64(t.binding.first + 1), Outcome: outcome, Reason: reason, ModelCalls: modelCalls, ToolDispatches: toolCalls, FinalMessageObject: final, EndingCommandID: endingID}}, &revision)
	if err != nil {
		return err
	}
	r.mu.Lock()
	t.state, t.reason = "finished", reason
	r.mu.Unlock()
	liveState := "discarded"
	if outcome == "finished" {
		liveState = "finished"
	}
	t.live.close(liveState, final, nil)
	return nil
}

// loopEnding is a finishing turn's committed native ending, as loop.end and
// the turn's manifest record it.
type loopEnding struct {
	outcome, reason string
	final           *ledger.ObjectRef
	endingID        uuid.UUID
}

// endNative returns the turn's committed native ending, first committing it
// unless an earlier attempt of this finish already has. The ending is kept on
// the turn and decides the outcome from then on: a finish re-entered after a
// busy step skips this step, and a stop reason that arrives after the ending
// changes nothing.
func (r *loopRuntime) endNative(t *loopTurn) (loopEnding, error) {
	r.mu.Lock()
	committed := t.ending
	r.mu.Unlock()
	if committed != nil {
		return *committed, nil
	}
	s := r.service
	if reason := s.loopBoundary(t); reason != "" {
		r.noteStop(t, reason)
	}
	r.mu.Lock()
	reason, final := t.reason, t.answer
	cancelID := t.cancelID
	r.mu.Unlock()
	outcome, native := "finished", "released"
	if reason == "" {
		reason = "natural_stop"
	} else {
		final = nil
		outcome = "failed"
		if reason == "cancelled" {
			outcome, native = "cancelled", "cancelled"
		} else if reason == "shutdown" {
			outcome, native = "interrupted", "interrupted"
		}
	}
	ending := loopEnding{outcome: outcome, reason: reason, final: final, endingID: t.endingID}
	var receipt *ScopedReceipt
	if native == "cancelled" {
		if cancelID == (uuid.UUID{}) {
			return loopEnding{}, ErrIntegrity
		}
		ending.endingID = cancelID
		cancelled, err := s.CancelTurn(context.Background(), bindingOf(t.origin), TurnCancellation{CommandID: cancelID, TurnID: t.id, ExpectedVersion: 1})
		if err != nil {
			return loopEnding{}, err
		}
		receipt = &cancelled
	} else if _, err := s.EndTurn(context.Background(), TurnEnding{CommandID: ending.endingID, TurnID: t.id, ExpectedVersion: 1, Outcome: native}); err != nil {
		return loopEnding{}, err
	}
	r.mu.Lock()
	if receipt != nil {
		t.cancelReceipt = receipt
	}
	t.ending = &ending
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
			err = s.scan(ctx, 0, through, func(commit ledger.Commit) error {
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
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
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
		attempt, found, err := s.loadPaid(ctx, output.AttemptID, s.p.through, ledger.Digest{})
		s.mu.Unlock()
		if err != nil {
			return "", err
		}
		if !found || attempt.origin != origin || attempt.admission.ParentTurnID != turn || attempt.profile.Provider != manifest.Provider || attempt.profile.ModelID != manifest.Model || attempt.result == nil || !slices.Contains(attempt.result.MessageObjects, object) {
			return "", ErrDenied
		}
		if _, err := r.readPaid(ctx, origin, output.AttemptID, object); err != nil {
			return "", err
		}
		text.WriteString(output.Block.Text)
		if text.Len() > 8<<20 {
			return "", ErrTooLarge
		}
	}
	return text.String(), nil
}

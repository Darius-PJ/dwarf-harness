package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

var errLoopContent = errors.New("agents: invalid continuation content")

type loopInvocation struct {
	attempt          uuid.UUID
	callID, name     string
	normalized       modelToolInvocation
	correction       *modelToolCorrection
	rawSHA256        ledger.Digest
	started          *loopGroup
	grant            uuid.UUID
	effectID, workID uuid.UUID
	counted          bool
}

func (s *Service) loopGrantLocked(ctx context.Context, origin ledger.Origin, operation string, targets []ledger.Resource, size int64) (uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(s.p.grants))
	for id, grant := range s.p.grants {
		if grant.definition.Grantee == ledger.Binding(bindingOf(origin)) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range ids {
		if _, err := s.authorize(ctx, origin, id, operation, targets, nil, size, s.p.through); err == nil {
			return id, nil
		} else if !errors.Is(err, ErrDenied) {
			return uuid.UUID{}, err
		}
	}
	return uuid.UUID{}, ErrDenied
}
func (s *Service) loopGrant(ctx context.Context, origin ledger.Origin, operation string, targets []ledger.Resource, size int64) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(ctx); err != nil {
		return uuid.UUID{}, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return uuid.UUID{}, err
	}
	return s.loopGrantLocked(ctx, origin, operation, targets, size)
}
func loopPrivateTarget(origin ledger.Origin) ledger.Resource {
	return ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(origin)), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}
}
func (r *loopRuntime) readPaid(t *loopTurn, attempt uuid.UUID, ref ledger.ObjectRef) ([]byte, error) {
	grant, err := r.service.loopGrant(t.ctx, t.origin, "private.read", []ledger.Resource{loopPrivateTarget(t.origin)}, ref.Bytes)
	if err != nil {
		return nil, err
	}
	agent := &Agent{service: r.service, ids: submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}}
	return agent.ReadPaidObject(t.ctx, PaidObjectLookup{AttemptID: attempt, GrantID: grant, Object: ref, MaxBytes: maxObjectBytes})
}

func (r *loopRuntime) model(t *loopTurn) error {
	s := r.service
	if reason := s.loopBoundary(t); reason != "" {
		r.noteStop(t, reason)
		return nil
	}
	r.mu.Lock()
	call := t.paid
	r.mu.Unlock()
	if call == nil {
		return ErrIntegrity
	}
	if call.prepared == nil {
		grant, err := s.loopGrant(t.ctx, t.origin, "model.call", []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + t.settings.RouteID.String()}}, 0)
		if err != nil {
			return err
		}
		call.grant = grant
		input := AnthropicInput{AttemptID: call.id, GrantID: grant, ParentTurnID: t.id, TaskBinding: t.settings.TaskBinding, RouteID: t.settings.RouteID, ExpectedRouteVersion: t.settings.ExpectedRouteVersion, CredentialVersion: t.credential, Request: t.request}
		switch t.profile.Provider {
		case "anthropic":
			call.prepared, err = s.PrepareAnthropic(t.ctx, bindingOf(t.origin), input)
		case "openai":
			call.prepared, err = s.PrepareOpenAI(t.ctx, bindingOf(t.origin), OpenAIInput(input))
		default:
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
	}
	if !call.admitted {
		if reason := s.loopBoundary(t); reason != "" {
			r.noteStop(t, reason)
			return nil
		}
		agent := &Agent{service: s, ids: submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}}
		_, _, err := agent.AdmitPaid(t.ctx, PaidAdmission{CommandID: call.admitID, AttemptID: call.id, GrantID: call.grant, ParentTurnID: t.id, TaskBinding: t.settings.TaskBinding, Prepared: call.prepared})
		if err != nil {
			return err
		}
		call.admitted = true
	}
	if reason := s.loopBoundary(t); reason != "" {
		r.noteStop(t, reason)
		return nil
	}
	out, err := s.DispatchPaidStreaming(t.ctx, PaidDispatch{CommandID: call.dispatchID, AttemptID: call.id, ExpectedVersion: 1}, t.live.append)
	if out.IntentReceipt.CommandID != (uuid.UUID{}) && !call.counted {
		r.mu.Lock()
		t.modelCalls++
		call.counted = true
		r.mu.Unlock()
	}
	if loopBusy(err) {
		call.dispatchID = uuid.NewV7()
		return err
	}
	if out.ObservedPaidCall != nil {
		_, recordErr := s.RecordPaidObservation(context.Background(), PaidObservation{CommandID: call.observeID, AttemptID: call.id, ExpectedVersion: 2, Observed: out.ObservedPaidCall})
		if recordErr != nil {
			return recordErr
		}
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	a, found, err := s.loadPaid(context.Background(), call.id, s.p.through, ledger.Digest{})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if !found || a.origin != t.origin || a.admission.ParentTurnID != t.id || a.profile.Provider != t.profile.Provider || a.profile.ModelID != t.profile.ModelID || a.result == nil || a.result.Kind != "usage" || a.result.EvidenceObject == nil {
		return errLoopContent
	}
	raw, err := r.readPaid(t, call.id, *a.result.EvidenceObject)
	if err != nil {
		return err
	}
	var observation paidObservationObject
	if json.Unmarshal(raw, &observation) != nil || observation.AttemptID != call.id || observation.Origin != t.origin || observation.ParentTurnID != t.id || observation.RequestSHA256 != a.admission.Quote.RequestSHA256 || observation.Model != t.profile.ModelID || !observation.Complete || observation.ContentError || observation.ReplayObject == nil {
		return errLoopContent
	}
	replay, err := r.readPaid(t, call.id, *observation.ReplayObject)
	if err != nil {
		return err
	}
	var tools []loopInvocation
	texts := []ledger.ObjectRef{}
	lastBlock, lastChunk := -1, -1
	for _, ref := range a.result.MessageObjects {
		raw, err := r.readPaid(t, call.id, ref)
		if err != nil {
			return err
		}
		var object struct {
			Schema       int           `json:"schema"`
			Kind         string        `json:"kind"`
			AttemptID    uuid.UUID     `json:"attempt_id"`
			Origin       ledger.Origin `json:"origin"`
			ParentTurnID uuid.UUID     `json:"parent_turn_id"`
			BlockIndex   int           `json:"block_index"`
			ChunkIndex   int           `json:"chunk_index"`
			Block        struct {
				Kind          string          `json:"kind"`
				ID            string          `json:"id"`
				Name          string          `json:"name"`
				Input         json.RawMessage `json:"input"`
				ArgumentsText *string         `json:"arguments_text"`
				Provider      string          `json:"provider"`
				Model         string          `json:"model"`
			} `json:"block"`
		}
		if json.Unmarshal(raw, &object) != nil || object.Schema != 1 || object.Kind != "paid_output" || object.AttemptID != call.id || object.Origin != t.origin || object.ParentTurnID != t.id || object.BlockIndex < lastBlock || object.BlockIndex == lastBlock && object.ChunkIndex != lastChunk+1 || object.BlockIndex > lastBlock && object.ChunkIndex != 0 {
			return errLoopContent
		}
		lastBlock, lastChunk = object.BlockIndex, object.ChunkIndex
		switch object.Block.Kind {
		case "text":
			texts = append(texts, ref)
		case "opaque_provider":
			if object.Block.Provider != t.profile.Provider || object.Block.Model != t.profile.ModelID {
				return errLoopContent
			}
		case "local_tool_call", "local_tool_request":
			arguments := object.Block.Input
			if object.Block.Kind == "local_tool_request" {
				if object.Block.ArgumentsText == nil {
					return errLoopContent
				}
				arguments = []byte(*object.Block.ArgumentsText)
			}
			normalized, correction := normalizeModelTool(object.Block.Name, arguments, ledger.Binding(bindingOf(t.origin)), t.policy.MaxArgumentBytes)
			if correction != nil && !correction.Renderable {
				return errLoopContent
			}
			tools = append(tools, loopInvocation{attempt: call.id, callID: object.Block.ID, name: object.Block.Name, normalized: normalized, correction: correction, rawSHA256: sha256.Sum256(arguments), effectID: uuid.NewV7(), workID: uuid.NewV7()})
		default:
			return errLoopContent
		}
	}
	if t.profile.Provider == "anthropic" && (len(tools) == 0 && observation.StopReason != "end_turn" || len(tools) != 0 && observation.StopReason != "tool_use") || t.profile.Provider == "openai" && observation.StopReason != "completed" {
		return errLoopContent
	}
	if len(tools) == 0 {
		manifest := loopFinalMessage{Schema: 1, Kind: "final_message", Origin: t.origin, TurnID: t.id, Provider: t.profile.Provider, Model: t.profile.ModelID, TextObjects: texts}
		ref, err := s.publishLoopMetadata(t.ctx, t.origin, t.credential, manifest)
		if err != nil {
			return err
		}
		r.mu.Lock()
		t.answer = &ref
		r.mu.Unlock()
	} else {
		// The provider's validated replay array is the only assistant content
		// admitted to continuation. Display blocks are never reconstructed.
		next := t.request
		next.Messages = append(slices.Clone(t.request.Messages), providers.Message{Role: "assistant", Content: []providers.Block{{Type: t.profile.Provider + "_replay", Replay: replay}}})
		if _, err := requestStorageBound(next); err != nil {
			return err
		}
		t.request = next
		r.mu.Lock()
		t.tools = tools
		r.mu.Unlock()
	}
	if err := call.prepared.Close(); err != nil {
		return err
	}
	r.mu.Lock()
	t.paid = nil
	r.mu.Unlock()
	return nil
}

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type loopToolEnvelope struct {
	Schema int                  `json:"schema"`
	ToolID uuid.UUID            `json:"tool_id"`
	Status string               `json:"status"`
	Data   any                  `json:"data,omitempty"`
	Error  *modelToolCorrection `json:"error,omitempty"`
}

func loopNativeOperation(operation string) bool {
	return strings.HasPrefix(operation, "file.") || strings.HasPrefix(operation, "task.") && operation != "task.inspect" && operation != "task.list" || operation == "private.read" || operation == "private.write" || operation == "artifact.read"
}
func (r *loopRuntime) passiveGrant(t *loopTurn, operation string) (uuid.UUID, error) {
	s := r.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(t.ctx, s.store.Durable()); err != nil {
		return uuid.UUID{}, err
	}
	ids := make([]uuid.UUID, 0, len(s.p.grants))
	for id, g := range s.p.grants {
		if g.definition.Grantee == ledger.Binding(bindingOf(t.origin)) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range ids {
		if err := s.loopPassiveAuthority(t.ctx, t.origin, ledger.RuntimeToolStarted{Operation: operation, GrantID: id}, s.p.through); err == nil {
			return id, nil
		} else if !errors.Is(err, ErrDenied) {
			return uuid.UUID{}, err
		}
	}
	return uuid.UUID{}, ErrDenied
}
func (r *loopRuntime) toolAuthority(t *loopTurn, inv *loopInvocation) error {
	if inv.normalized.Operation == "tools.discover" || inv.normalized.Operation == "task.list" {
		grant, err := r.passiveGrant(t, inv.normalized.Operation)
		inv.grant = grant
		return err
	}
	if inv.normalized.Target == nil {
		return ErrDenied
	}
	target := *inv.normalized.Target
	if inv.normalized.Operation == "context.inspect" {
		target.Slots = []string{"continuation", "handoff", "memory", "transcript"}
	}
	if inv.normalized.Operation == "artifact.read" {
		s := r.service
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.refreshLocked(t.ctx, s.store.Durable()); err != nil {
			return err
		}
		chain, err := s.artifactAuthority(t.ctx, t.origin, target.Artifact, uuid.UUID{}, s.p.through, nil)
		if err == nil {
			inv.grant = chain[0].id
		}
		return err
	}
	grant, err := r.service.loopGrant(t.ctx, t.origin, inv.normalized.Operation, []ledger.Resource{target}, 0)
	if err == nil {
		inv.grant = grant
	}
	return err
}

func (r *loopRuntime) tool(t *loopTurn) error {
	if reason := r.service.loopBoundary(t); reason != "" {
		r.noteStop(t, reason)
		return nil
	}
	inv := &t.tools[0]
	denied := false
	if inv.correction != nil && (inv.correction.Code == "unrecognized" || inv.name == "tools_invoke_v1" && inv.correction.Field == "tool_path") {
		denied = true
	}
	if inv.started == nil {
		if inv.correction == nil {
			if err := r.toolAuthority(t, inv); err != nil {
				if loopBusy(err) {
					return err
				}
				if !errors.Is(err, ErrDenied) && !errors.Is(err, ErrNotBound) {
					return err
				}
				denied = true
			}
		}
		request := inv.normalized.Bytes
		operation, name := inv.normalized.Operation, inv.normalized.ToolName
		var target *ledger.Resource
		var grant, effect uuid.UUID
		if denied || inv.correction != nil {
			operation, name = "invalid_argument", inv.name
			if denied {
				operation, name = "unrecognized", "unrecognized"
			}
			digest := inv.rawSHA256
			if digest == (ledger.Digest{}) {
				digest = sha256.Sum256(inv.normalized.Bytes)
			}
			request = modelJSON(struct {
				Schema             int           `json:"schema"`
				ToolName           string        `json:"tool_name"`
				RawArgumentsSHA256 ledger.Digest `json:"raw_arguments_sha256"`
			}{1, name, digest})
		} else {
			target = inv.normalized.Target
			if loopNativeOperation(operation) {
				effect = inv.effectID
			} else {
				grant = inv.grant
			}
		}
		ref, err := r.service.publishLoopObject(t.ctx, t.origin, t.credential, request)
		if err != nil {
			return err
		}
		start := ledger.RuntimeToolStarted{ToolID: uuid.NewV7(), ParentTurnID: t.id, AttemptID: inv.attempt, ProviderCallID: inv.callID, ToolName: name, ToolSchema: 1, RequestObject: ref, RequestSHA256: ref.SHA256, Operation: operation, Target: target, GrantID: grant, EffectCommandID: effect}
		g, err := r.service.commitLoop(t.ctx, uuid.NewV7(), ledger.LoopSubmission{Schema: 6, Actor: "agent", Origin: t.origin, TurnID: t.id, Operation: "tool.begin", Arguments: ledger.LoopToolBeginArguments(start)}, nil)
		if err != nil {
			return err
		}
		inv.started = g
	}
	if !inv.counted {
		r.mu.Lock()
		t.toolCalls++
		inv.counted = true
		r.mu.Unlock()
	}
	if inv.started.start.Operation == "unrecognized" {
		denied = true
	}
	var data any
	var receipt uint64
	var err error
	outcome := "ok"
	var correction *modelToolCorrection
	if denied {
		outcome, correction = "denied", &modelToolCorrection{Code: "denied"}
	} else if inv.correction != nil {
		outcome, correction = "invalid_argument", inv.correction
	} else {
		// A re-run after tool.result met a moved ledger tail resumes at the
		// journal write: the executed result is retained until tool.result
		// commits, and the tool never runs twice.
		if inv.executed == nil {
			if reason := r.service.loopBoundary(t); reason != "" {
				return r.stopTool(t, inv)
			}
			if strings.HasPrefix(inv.normalized.Operation, "file.") {
				data, receipt, err = r.file(t, inv)
			} else {
				data, receipt, err = r.coreTool(t, inv)
			}
			if loopBusy(err) {
				r.mu.Lock()
				t.toolCalls--
				inv.counted = false
				r.mu.Unlock()
				return err
			}
			executed := loopToolExecution{data: data, receipt: receipt, outcome: "ok"}
			if err != nil {
				executed.outcome = loopToolError(err)
				executed.correction = &modelToolCorrection{Code: executed.outcome}
			}
			inv.executed = &executed
		}
		data, receipt, outcome, correction = inv.executed.data, inv.executed.receipt, inv.executed.outcome, inv.executed.correction
		// Native effects and reads already checked their own authority. This
		// final gate also covers unlocked result formatting/catalogue disclosure.
		if outcome == "ok" {
			if e := r.recheckTool(t, inv); e != nil {
				data = nil
				outcome, correction = "denied", &modelToolCorrection{Code: "denied"}
			}
		}
	}
	raw, err := r.recordToolResult(t, inv, outcome, data, correction, receipt)
	if err != nil {
		return err
	}
	var delivered struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &delivered) != nil {
		return ErrIntegrity
	}
	isError := delivered.Status != "ok"
	t.toolResults = append(t.toolResults, providers.Block{Type: "tool_result", ToolUseID: inv.callID, Content: []providers.Text{{Type: "text", Text: string(raw)}}, IsError: &isError})
	r.mu.Lock()
	t.tools = t.tools[1:]
	remaining := len(t.tools)
	r.mu.Unlock()
	if outcome == "unknown_effect" {
		r.noteStop(t, "unresolved_result")
		return nil
	}
	if remaining == 0 {
		next := t.request
		next.Messages = append(slices.Clone(next.Messages), providers.Message{Role: "user", Content: t.toolResults})
		if _, err := requestStorageBound(next); err != nil {
			return err
		}
		t.request, t.toolResults = next, nil
	}
	return nil
}
func loopToolError(err error) string {
	var revision *RevisionError
	var assignment *AssignmentError
	switch {
	case errors.Is(err, errLoopUnknownEffect):
		return "unknown_effect"
	case errors.Is(err, ErrDenied), errors.Is(err, ErrNotBound):
		return "denied"
	case errors.Is(err, ErrTooLarge):
		return "too_large"
	case errors.Is(err, workspace.ErrBusy), errors.Is(err, workspace.ErrMergeBackPending):
		return "busy"
	case errors.Is(err, errLoopFileConflict), errors.As(err, &revision), errors.As(err, &assignment):
		return "conflict"
	case errors.Is(err, ErrInvalid):
		return "invalid_argument"
	default:
		return "unavailable"
	}
}
func (r *loopRuntime) recheckTool(t *loopTurn, inv *loopInvocation) error {
	if reason := r.service.loopBoundary(t); reason != "" {
		return ErrDenied
	}
	s := r.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(t.ctx, s.store.Durable()); err != nil {
		return err
	}
	if inv.normalized.Operation == "artifact.read" {
		_, err := s.artifactAuthority(t.ctx, t.origin, inv.normalized.Target.Artifact, inv.grant, s.p.through, nil)
		return err
	}
	return s.loopPassiveAuthority(t.ctx, t.origin, ledger.RuntimeToolStarted{Operation: inv.normalized.Operation, Target: inv.normalized.Target, GrantID: inv.grant}, s.p.through)
}
func (r *loopRuntime) recordToolResult(t *loopTurn, inv *loopInvocation, outcome string, data any, correction *modelToolCorrection, receipt uint64) ([]byte, error) {
	id := inv.started.start.ToolID
	value := loopToolEnvelope{Schema: 1, ToolID: id, Status: "ok", Data: data}
	if correction != nil {
		value.Status, value.Data, value.Error = "error", nil, correction
	}
	raw := modelJSON(value)
	if int64(len(raw)) > t.policy.MaxResultBytes {
		outcome = "too_large"
		correction = &modelToolCorrection{Code: outcome}
		value = loopToolEnvelope{Schema: 1, ToolID: id, Status: "error", Error: correction}
		raw = modelJSON(value)
	}
	if int64(len(raw)) > t.policy.MaxResultBytes {
		return nil, ErrTooLarge
	}
	var ref ledger.ObjectRef
	var err error
	if correction != nil && correction.Field == "" && correction.Allowed == "" {
		fixed := loopRefusal{Schema: 1, ToolID: id, Status: "error", Error: loopFixedError{Code: correction.Code}}
		raw = modelJSON(fixed)
		ref, err = r.service.publishLoopMetadata(context.Background(), t.origin, t.credential, fixed)
	} else {
		ref, err = r.service.publishLoopObject(context.Background(), t.origin, t.credential, raw)
	}
	if err != nil {
		// No content-bearing fallback: only this fixed refusal may survive a
		// revoked borrow or a literal credential echo in tool/file output.
		outcome = "unavailable"
		fixed := loopRefusal{Schema: 1, ToolID: id, Status: "error", Error: loopFixedError{Code: outcome}}
		raw = modelJSON(fixed)
		ref, err = r.service.publishLoopMetadata(context.Background(), t.origin, t.credential, fixed)
		r.mu.Lock()
		if t.reason == "" {
			t.reason = "content_invalid"
		}
		r.mu.Unlock()
	}
	if err != nil {
		return nil, err
	}
	if correction == nil && outcome == "ok" {
		if e := r.recheckTool(t, inv); e != nil {
			outcome = "denied"
			fixed := loopRefusal{Schema: 1, ToolID: id, Status: "error", Error: loopFixedError{Code: outcome}}
			raw = modelJSON(fixed)
			ref, err = r.service.publishLoopMetadata(context.Background(), t.origin, t.credential, fixed)
			if err != nil {
				return nil, err
			}
		}
	}
	_, err = r.service.commitLoop(context.Background(), uuid.NewV7(), ledger.LoopSubmission{Schema: 6, Actor: "core", Origin: t.origin, TurnID: t.id, Operation: "tool.result", ExpectedVersion: 1, Arguments: ledger.LoopToolResultArguments{ToolID: id, StartedSequence: int64(inv.started.first + 1), Outcome: outcome, ResultObject: ref, EffectReceiptSequence: int64(receipt)}}, nil)
	if err == nil {
		inv.started, inv.executed = nil, nil
	}
	return raw, err
}
func (r *loopRuntime) stopTool(t *loopTurn, inv *loopInvocation) error {
	if inv.started == nil {
		return nil
	}
	s := r.service
	var sequence uint64
	if effect := inv.started.start.EffectCommandID; effect != (uuid.UUID{}) {
		s.mu.Lock()
		err := s.refreshLocked(context.Background(), s.store.Durable())
		var match commandMatch
		if err == nil {
			match, err = s.findCommand(context.Background(), effect, 0, s.p.through)
		}
		var work scopedWorkState
		if err == nil && match.scoped != nil && match.scoped.admission != nil {
			work, _, err = s.loadWork(context.Background(), match.scoped.admission.WorkID, s.p.through)
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if match.found {
			if match.scoped == nil {
				return ErrIntegrity
			}
			sequence = match.scoped.first + uint64(match.scoped.receiptIndex)
			if match.scoped.admission != nil {
				if work.ended {
					sequence = work.receipt.Sequence
				} else {
					outcome := "released_unsent"
					if work.dispatched {
						outcome = "unknown"
					}
					receipt, err := s.EndLocal(context.Background(), LocalEnding{CommandID: uuid.NewV7(), WorkID: work.admission.WorkID, ExpectedVersion: uint64(work.version), Outcome: outcome})
					if err != nil {
						return err
					}
					sequence = receipt.Sequence
				}
			}
		}
	}
	if !inv.counted {
		r.mu.Lock()
		t.toolCalls++
		inv.counted = true
		r.mu.Unlock()
	}
	_, err := r.recordToolResult(t, inv, "unavailable", nil, &modelToolCorrection{Code: "unavailable"}, sequence)
	return err
}

package agents

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) loopPassiveAuthority(ctx context.Context, origin ledger.Origin, start ledger.RuntimeToolStarted, through uint64) error {
	if start.Operation != "tools.discover" && start.Operation != "task.list" {
		if start.Target == nil {
			return ErrDenied
		}
		_, err := s.authorize(ctx, origin, start.GrantID, start.Operation, []ledger.Resource{*start.Target}, nil, 0, through)
		return err
	}
	chain, err := s.currentChain(ctx, origin, start.GrantID, through)
	if err != nil {
		return err
	}
	if start.Operation == "task.list" {
		for _, resource := range chain[0].resources {
			if resource.Kind != "task" || !validID(resource.TaskID) {
				continue
			}
			if _, err := s.authorize(ctx, origin, start.GrantID, "task.inspect", []ledger.Resource{resource}, nil, 0, through); err == nil {
				return nil
			} else if !errors.Is(err, ErrDenied) {
				return err
			}
		}
		return ErrDenied
	}
	for _, tool := range modelCatalogue {
		if s.loopCatalogueAllowed(ctx, origin, start.GrantID, tool, chain, through) {
			return nil
		}
	}
	return ErrDenied
}

// Catalogue templates reveal schemas only, never the resource used to prove
// eligibility. One selected chain filters the whole page, not a union of grants.
func (s *Service) loopCatalogueAllowed(ctx context.Context, origin ledger.Origin, grant uuid.UUID, tool modelTool, chain []grantRules, through uint64) bool {
	op := tool.schema.operation
	if op == "task.list" {
		op = "task.inspect"
	}
	for _, resource := range chain[0].resources {
		if strings.HasPrefix(op, "task.") && resource.Kind != "task" || strings.HasPrefix(op, "private.") && resource.Kind != "context" || op == "artifact.read" && resource.Kind != "artifact" {
			continue
		}
		if resource.Kind == "context" {
			if resource.Binding != ledger.Binding(bindingOf(origin)) || resource.Scope != "context" {
				continue
			}
			if tool.name == "core.context.handoff.write" {
				resource.Slots = []string{"handoff"}
			}
		}
		if tool.name == "core.task.list" && !validID(resource.TaskID) {
			continue
		}
		if _, err := s.authorize(ctx, origin, grant, op, []ledger.Resource{resource}, nil, 0, through); err == nil {
			return true
		}
	}
	return false
}

func (s *Service) loopEffectReceipt(ctx context.Context, tool loopToolState, result ledger.RuntimeToolResult, through uint64) error {
	if !slices.Contains([]string{"ok", "invalid_argument", "denied", "busy", "conflict", "too_large", "unavailable", "unknown_effect"}, result.Outcome) {
		return ErrUnavailable
	}
	start := tool.start
	if start.EffectCommandID == (uuid.UUID{}) {
		if result.EffectReceiptSequence != 0 {
			return ErrInvalid
		}
		return nil
	}
	m, err := s.findCommand(ctx, start.EffectCommandID, 0, through)
	if err != nil {
		return err
	}
	if result.EffectReceiptSequence == 0 {
		if m.found || result.Outcome == "ok" || result.Outcome == "unknown_effect" {
			return ErrInvalid
		}
		return nil
	}
	if m.scoped == nil || m.scoped.receipt.Submission.Origin != tool.origin {
		return ErrInvalid
	}
	g := m.scoped
	if start.Target == nil || !sameScopedValue(*start.Target, g.receipt.Submission.Target) {
		return ErrInvalid
	}
	if strings.HasPrefix(start.Operation, "file.") {
		if g.admission == nil || g.admission.Kind != "local" || g.admission.ParentTurnID != start.ParentTurnID || g.admission.Operation != start.Operation || g.admission.ArgumentsSHA256 != start.RequestSHA256 || g.admission.ToolSchema == nil || *g.admission.ToolSchema != start.ToolSchema {
			return ErrInvalid
		}
		r, found, err := s.sourceRecord(ctx, result.EffectReceiptSequence, through)
		if err != nil {
			return err
		}
		if !found || r.Type != ledger.TypeRuntimeScopedReceipt || r.AgentID != tool.origin.AgentID || r.ExecutionSessionID != tool.origin.SessionID || r.ContextID != tool.origin.ContextID || r.TurnID != start.ParentTurnID {
			return ErrInvalid
		}
		end, err := s.loopNative(ctx, r.OperationID, tool.origin, through)
		if err != nil {
			return err
		}
		if end.ending == nil || end.receipt.Submission.Operation != "local.end" || end.ending.WorkID != g.admission.WorkID || int64(end.first)+int64(end.receiptIndex) != result.EffectReceiptSequence {
			return ErrInvalid
		}
		if result.Outcome == "ok" && end.ending.Outcome != "succeeded" || result.Outcome == "unknown_effect" && end.ending.Outcome != "unknown" {
			return ErrInvalid
		}
		return nil
	}
	if g.receipt.Submission.Operation != start.Operation || int64(g.first)+int64(g.receiptIndex) != result.EffectReceiptSequence {
		return ErrInvalid
	}
	return nil
}

// Refused native command IDs cannot gain an effect later, including after Open.
// A maximum proves the usual fresh-command case without retaining tombstones.
func (s *Service) loopEffectAvailable(ctx context.Context, id uuid.UUID, through uint64) error {
	if bytes.Compare(id[:], s.p.maxLoopEffect[:]) > 0 {
		return nil
	}
	return s.scan(ctx, 0, through, func(c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type != ledger.TypeRuntimeToolResult {
				continue
			}
			result, err := ledger.DecodeRuntimeToolResult(r.Payload)
			if err != nil {
				return err
			}
			if result.EffectReceiptSequence != 0 {
				continue
			}
			source, found, err := s.sourceRecord(ctx, result.StartedSequence, through)
			if err != nil {
				return err
			}
			if !found || source.Type != ledger.TypeRuntimeToolStarted {
				return ErrIntegrity
			}
			start, err := ledger.DecodeRuntimeToolStarted(source.Payload)
			if err != nil {
				return err
			}
			if start.EffectCommandID == id {
				return inconsistent(r.Seq, "ended tool native command")
			}
		}
		return nil
	})
}

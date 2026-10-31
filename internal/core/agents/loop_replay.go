package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type loopGroup struct {
	ids      submissionIDs
	first    uint64
	command  ledger.Command
	receipt  ledger.RuntimeLoopReceipt
	bound    *ledger.RuntimeTurnBound
	start    *ledger.RuntimeToolStarted
	result   *ledger.RuntimeToolResult
	ending   *ledger.RuntimeTurnResult
	revision *ledger.PrivateTurnRevision
}

func isLoopType(typ string) bool {
	switch typ {
	case ledger.TypePrivateTurnRevision, ledger.TypeRuntimeTurnBound, ledger.TypeRuntimeToolStarted, ledger.TypeRuntimeToolResult, ledger.TypeRuntimeTurnResult, ledger.TypeRuntimeLoopReceipt:
		return true
	}
	return false
}

func decodeLoopGroup(d []ledger.Draft, first uint64) (*loopGroup, error) {
	bad := func(reason string) (*loopGroup, error) { return nil, inconsistent(first, reason) }
	if len(d) != 3 && len(d) != 4 || d[0].Type != ledger.TypeCommand || d[len(d)-1].Type != ledger.TypeRuntimeLoopReceipt {
		return bad("loop group boundaries")
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad("loop command")
	}
	receipt, err := ledger.DecodeRuntimeLoopReceipt(d[len(d)-1].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 || receipt.Outcome != "applied" {
		return bad("loop receipt binding")
	}
	sub := receipt.Submission
	raw, err := sub.Encode()
	if err != nil || ledger.Digest(sha256.Sum256(raw)) != command.SubmissionSHA256 {
		return bad("loop submission digest")
	}
	g := &loopGroup{ids: submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}, first: first, command: command, receipt: receipt}
	want := []string{ledger.TypeCommand}
	switch sub.Operation {
	case "loop.bind":
		want = append(want, ledger.TypeRuntimeTurnBound)
		v, e := ledger.DecodeRuntimeTurnBound(d[1].Payload)
		g.bound, err = &v, e
	case "tool.begin":
		want = append(want, ledger.TypeRuntimeToolStarted)
		v, e := ledger.DecodeRuntimeToolStarted(d[1].Payload)
		g.start, err = &v, e
	case "tool.result":
		want = append(want, ledger.TypeRuntimeToolResult)
		v, e := ledger.DecodeRuntimeToolResult(d[1].Payload)
		g.result, err = &v, e
	case "loop.end":
		if len(d) != 4 {
			return bad("loop ending shape")
		}
		want = append(want, ledger.TypePrivateTurnRevision, ledger.TypeRuntimeTurnResult)
		v, e := ledger.DecodePrivateTurnRevision(d[1].Payload)
		if e != nil {
			return bad("loop private revision")
		}
		g.revision = &v
		end, e := ledger.DecodeRuntimeTurnResult(d[2].Payload)
		g.ending, err = &end, e
	default:
		return bad("loop operation")
	}
	want = append(want, ledger.TypeRuntimeLoopReceipt)
	if err != nil || len(want) != len(d) || receipt.EffectSequence != int64(first)+int64(len(d))-2 {
		return bad("loop effect or sequence")
	}
	for i, r := range d {
		actor := ledger.Actor(sub.Actor)
		if i > 0 && i < len(d)-1 {
			actor = ledger.ActorCore
		}
		if r.Type != want[i] || r.TypeVersion != 1 || r.Actor != actor || r.AgentID != g.ids.agent || r.ExecutionSessionID != g.ids.session || r.ContextID != g.ids.context || r.TurnID != sub.TurnID || r.OperationID != command.CommandID || r.MeasurementPath != "" || len(r.BodyRefs) != 0 {
			return bad("loop envelope or order")
		}
	}
	return g, nil
}

type loopCall struct {
	attempt uuid.UUID
	call    string
}

type loopTurnState struct {
	origin   ledger.Origin
	bound    ledger.RuntimeTurnBound
	sequence uint64
	calls    map[loopCall]uuid.UUID
}

type loopToolState struct {
	origin   ledger.Origin
	start    ledger.RuntimeToolStarted
	sequence uint64
}

type loopPlan struct {
	turn          *loopTurnState
	tool          *loopToolState
	endTool       uuid.UUID
	blockedEffect uuid.UUID
	endTurn       uuid.UUID
	private       map[scopeKey]*scopeState
}

// A source link is accepted only from a complete, already-accepted native group.
// The sequence names the effect/receipt, never a caller's guessed cursor.
func (s *Service) loopNative(ctx context.Context, id uuid.UUID, origin ledger.Origin, through uint64) (*scopedGroup, error) {
	m, err := s.findCommand(ctx, id, 0, through)
	if err != nil {
		return nil, err
	}
	if m.scoped == nil || m.scoped.receipt.Submission.Origin != origin {
		return nil, ErrInvalid
	}
	return m.scoped, nil
}

func (s *Service) planLoop(ctx context.Context, g *loopGroup, first uint64) (loopPlan, error) {
	plan := loopPlan{}
	sub, through := g.receipt.Submission, first-1
	if !s.p.registeredOrigin(sub.Origin) {
		return plan, ErrNotBound
	}
	turn, bound := s.p.loopTurns[sub.TurnID]
	switch a := sub.Arguments.(type) {
	case ledger.LoopBindArguments:
		if bound || g.bound == nil {
			return plan, ErrInvalid
		}
		native, err := s.loopNative(ctx, a.AdmissionCommandID, sub.Origin, through)
		if err != nil {
			return plan, err
		}
		work, found, err := s.loadWork(ctx, sub.TurnID, through)
		if err != nil {
			return plan, err
		}
		if !found || work.ended || native.admission == nil || native.admission.Kind != "turn" || native.admission.WorkID != sub.TurnID || native.turn != sub.TurnID {
			return plan, ErrInvalid
		}
		want := ledger.RuntimeTurnBound{TurnID: sub.TurnID, AdmissionSequence: int64(native.first) + int64(native.effectIndex), InputObject: a.InputObject, PolicyObject: a.PolicyObject, RenderObject: a.RenderObject}
		if !sameScopedValue(want, *g.bound) {
			return plan, ErrInvalid
		}
		// An ended binding cannot be rebound, even if the native slot still exists.
		duplicate := false
		err = s.scan(ctx, native.first-1, through, func(c ledger.Commit) error {
			for _, r := range c.Records {
				if r.Type == ledger.TypeRuntimeTurnBound && r.TurnID == sub.TurnID {
					duplicate = true
				}
			}
			return nil
		})
		if err != nil {
			return plan, err
		}
		if duplicate {
			return plan, ErrInvalid
		}
		plan.turn = &loopTurnState{origin: sub.Origin, bound: want, sequence: first + 1, calls: map[loopCall]uuid.UUID{}}
	case ledger.LoopToolBeginArguments:
		if !bound || turn.origin != sub.Origin || g.start == nil || !sameScopedValue(ledger.RuntimeToolStarted(a), *g.start) {
			return plan, ErrInvalid
		}
		work, found, err := s.loadWork(ctx, sub.TurnID, through)
		if err != nil {
			return plan, err
		}
		if !found || work.ended {
			return plan, ErrInvalid
		}
		if _, exists := s.p.loopTools[a.ToolID]; exists {
			return plan, ErrInvalid
		}
		if bytes.Compare(a.ToolID[:], s.p.maxLoopTool[:]) <= 0 {
			duplicate := false
			err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
				for _, r := range c.Records {
					if r.Type != ledger.TypeRuntimeToolStarted {
						continue
					}
					v, err := ledger.DecodeRuntimeToolStarted(r.Payload)
					if err != nil {
						return err
					}
					if v.ToolID == a.ToolID {
						duplicate = true
					}
				}
				return nil
			})
			if err != nil {
				return plan, err
			}
			if duplicate {
				return plan, ErrInvalid
			}
		}
		if !loopSupportedTool(ledger.RuntimeToolStarted(a)) {
			return plan, ErrUnavailable
		}
		if _, exists := turn.calls[loopCall{a.AttemptID, a.ProviderCallID}]; exists {
			return plan, ErrInvalid
		}
		attempt, found, err := s.loadPaid(ctx, a.AttemptID, through, ledger.Digest{})
		if err != nil {
			return plan, err
		}
		if !found || attempt.origin != sub.Origin || attempt.admission.ParentTurnID != sub.TurnID || attempt.result == nil || attempt.result.Kind != "usage" {
			return plan, ErrInvalid
		}
		if a.GrantID != (uuid.UUID{}) {
			if err := s.loopPassiveAuthority(ctx, sub.Origin, ledger.RuntimeToolStarted(a), through); err != nil {
				return plan, err
			}
		}
		plan.tool = &loopToolState{origin: sub.Origin, start: ledger.RuntimeToolStarted(a), sequence: first + 1}
	case ledger.LoopToolResultArguments:
		tool, found := s.p.loopTools[a.ToolID]
		if !bound || turn.origin != sub.Origin || !found || tool.origin != sub.Origin || tool.start.ParentTurnID != sub.TurnID || int64(tool.sequence) != a.StartedSequence || g.result == nil || !sameScopedValue(ledger.RuntimeToolResult(a), *g.result) {
			return plan, ErrInvalid
		}
		if err := s.loopEffectReceipt(ctx, tool, ledger.RuntimeToolResult(a), through); err != nil {
			return plan, err
		}
		plan.endTool = a.ToolID
		if a.EffectReceiptSequence == 0 {
			plan.blockedEffect = tool.start.EffectCommandID
		}
	case ledger.LoopEndArguments:
		if !bound || turn.origin != sub.Origin || int64(turn.sequence) != a.BoundSequence || s.p.toolChildren[sub.TurnID] != 0 || s.p.paidChildren[sub.TurnID] != 0 || g.ending == nil || g.revision == nil {
			return plan, ErrInvalid
		}
		native, err := s.loopNative(ctx, a.EndingCommandID, sub.Origin, through)
		if err != nil {
			return plan, err
		}
		if native.ending == nil || native.ending.WorkID != sub.TurnID || native.turn != sub.TurnID {
			return plan, ErrInvalid
		}
		outcome := "released"
		switch a.Outcome {
		case "cancelled":
			outcome = "cancelled"
		case "interrupted":
			outcome = "interrupted"
		case "finished", "failed":
		default:
			return plan, ErrUnavailable
		}
		if native.ending.Outcome != outcome {
			return plan, ErrInvalid
		}
		want := ledger.RuntimeTurnResult{TurnID: sub.TurnID, BoundSequence: a.BoundSequence, Outcome: a.Outcome, Reason: a.Reason, ModelCalls: a.ModelCalls, ToolDispatches: a.ToolDispatches, FinalMessageObject: a.FinalMessageObject, EndingSequence: int64(native.first) + int64(native.effectIndex)}
		if !sameScopedValue(want, *g.ending) {
			return plan, ErrInvalid
		}
		plan.private, err = s.p.planPrivateRevisions(g.ids, []ledger.PrivateRevision{ledger.PrivateRevision(*g.revision)}, []int{1}, first)
		if err != nil {
			return plan, err
		}
		plan.endTurn = sub.TurnID
	default:
		return plan, ErrInvalid
	}
	return plan, nil
}

func (p *projection) installLoop(plan loopPlan) {
	if bytes.Compare(plan.blockedEffect[:], p.maxLoopEffect[:]) > 0 {
		p.maxLoopEffect = plan.blockedEffect
	}
	if plan.turn != nil {
		p.loopTurns[plan.turn.bound.TurnID] = *plan.turn
	}
	if plan.tool != nil {
		v := *plan.tool
		p.loopTools[v.start.ToolID] = v
		if bytes.Compare(v.start.ToolID[:], p.maxLoopTool[:]) > 0 {
			p.maxLoopTool = v.start.ToolID
		}
		p.toolChildren[v.start.ParentTurnID]++
		p.loopTurns[v.start.ParentTurnID].calls[loopCall{v.start.AttemptID, v.start.ProviderCallID}] = v.start.ToolID
	}
	if plan.endTool != (uuid.UUID{}) {
		v := p.loopTools[plan.endTool]
		p.toolChildren[v.start.ParentTurnID]--
		if p.toolChildren[v.start.ParentTurnID] == 0 {
			delete(p.toolChildren, v.start.ParentTurnID)
		}
		delete(p.loopTools, plan.endTool)
	}
	if plan.endTurn != (uuid.UUID{}) {
		delete(p.loopTurns, plan.endTurn)
	}
	for k, st := range plan.private {
		p.scopes[k] = st
	}
}

func loopSupportedTool(start ledger.RuntimeToolStarted) bool {
	if start.ToolSchema != 1 {
		return false
	}
	if start.Operation == "unrecognized" {
		return start.ToolName == "unrecognized"
	}
	for _, tool := range modelTools {
		if tool.name != start.ToolName {
			continue
		}
		if start.Operation == "invalid_argument" {
			return true
		}
		var supports func(*modelSchema) bool
		supports = func(s *modelSchema) bool {
			if s.operation == start.Operation {
				return true
			}
			for _, choice := range s.choices {
				if supports(choice) {
					return true
				}
			}
			return false
		}
		return supports(tool.schema)
	}
	return false
}

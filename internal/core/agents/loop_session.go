package agents

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type loopSession struct {
	service       *Service
	ids           submissionIDs
	mu            sync.Mutex
	closed        bool
	subscriptions []*session.TextSubscription
}

func (s *Service) OpenSession(ctx context.Context, binding Binding) (session.Session, error) {
	if s == nil {
		return nil, ErrNotBound
	}
	ids, err := s.initialIDs(ctx, binding, false)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loop == nil || s.loopClosing {
		return nil, ErrUnavailable
	}
	return &loopSession{service: s, ids: ids}, nil
}
func (v *loopSession) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return
	}
	v.closed = true
	for _, sub := range v.subscriptions {
		sub.Close()
	}
	v.subscriptions = nil
}
func (v *loopSession) usable() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return session.ErrClosed
	}
	return nil
}
func (v *loopSession) bound(ctx context.Context, contextID uuid.UUID) (submissionIDs, error) {
	if err := v.usable(); err != nil {
		return submissionIDs{}, err
	}
	ids, err := v.service.initialIDs(ctx, Binding{AgentID: v.ids.agent, ContextID: contextID}, false)
	if err == nil && ids.session != v.ids.session {
		err = ErrNotBound
	}
	return ids, err
}
func (v *loopSession) Do(ctx context.Context, command session.Command) (session.CommandResult, error) {
	result := session.CommandResult{CommandID: command.CommandID, Outcome: "refused"}
	if command.Schema != 1 || !validID(command.CommandID) || !validID(command.ContextID) {
		return result, ErrInvalid
	}
	ids, err := v.bound(ctx, command.ContextID)
	if err != nil {
		return result, err
	}
	s, r := v.service, v.service.loop
	switch args := command.Arguments.(type) {
	case session.MessageSend:
		if command.Operation != "message.send" || !validID(args.TurnID) || !utf8.ValidString(args.Text) || len(args.Text) > maxObjectBytes || len(args.Artifacts) > s.limits.MaxSelectedObjects {
			return result, ErrInvalid
		}
		g, err := r.start(ctx, ids, command.CommandID, args)
		if err != nil {
			return result, err
		}
		return session.CommandResult{CommandID: command.CommandID, Outcome: "applied", ReceiptSequence: g.first + 2, EffectSequence: uint64(g.receipt.EffectSequence), TurnID: args.TurnID, Data: g.receipt}, nil
	case session.TurnCancel:
		if command.Operation != "turn.cancel" || !validID(args.TurnID) {
			return result, ErrInvalid
		}
		return v.cancelTurn(ctx, ids, command.CommandID, args.TurnID)
	case session.TurnStatus:
		if command.Operation != "turn.status" || !validID(args.TurnID) {
			return result, ErrInvalid
		}
		view, err := v.status(ctx, ids, args.TurnID)
		if err != nil {
			return result, err
		}
		return session.CommandResult{CommandID: command.CommandID, Outcome: "ok", TurnID: args.TurnID, Data: view}, nil
	case session.ContextStatus:
		if command.Operation != "context.status" {
			return result, ErrInvalid
		}
		s.mu.Lock()
		if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
			s.mu.Unlock()
			return result, err
		}
		st := s.p.scopes[keyFor(ids, "context")]
		view := session.ContextView{ContextID: ids.context, Revision: st.revision, ThroughSequence: s.p.through, Turns: []session.TurnView{}}
		for id, turn := range s.p.loopTurns {
			if turn.origin == originOf(ids) {
				view.Turns = append(view.Turns, session.TurnView{TurnID: id, State: "recovery_pending", BoundSequence: turn.sequence})
			}
		}
		s.mu.Unlock()
		r.mu.Lock()
		for i, turn := range view.Turns {
			if live := r.turns[turn.TurnID]; live != nil {
				view.Turns[i] = r.turnView(live)
			}
		}
		r.mu.Unlock()
		return session.CommandResult{CommandID: command.CommandID, Outcome: "ok", Data: view}, nil
	case session.PromotionRequest:
		if command.Operation != "promotion.request" || !validID(args.WorkspaceID) {
			return result, ErrInvalid
		}
		state, err := r.workspace.RequestPromotion(ctx, workspace.Request{RequestID: command.CommandID, Actor: ledger.ActorOwner, ContextID: ids.context, WorkspaceID: args.WorkspaceID, Head: args.Head, RestartFrom: args.RestartFrom})
		if err != nil {
			return result, err
		}
		return session.CommandResult{CommandID: command.CommandID, Outcome: state.State, Data: state}, nil
	default:
		return result, ErrInvalid
	}
}
func (v *loopSession) status(ctx context.Context, ids submissionIDs, id uuid.UUID) (session.TurnView, error) {
	r, s := v.service.loop, v.service
	r.mu.Lock()
	if t := r.turns[id]; t != nil {
		if t.origin != originOf(ids) {
			r.mu.Unlock()
			return session.TurnView{}, ErrDenied
		}
		view := r.turnView(t)
		r.mu.Unlock()
		return view, nil
	}
	r.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return session.TurnView{}, err
	}
	view := session.TurnView{TurnID: id, State: "recovery_pending"}
	err := s.scan(ctx, 0, s.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.AgentID != ids.agent || record.ExecutionSessionID != ids.session || record.ContextID != ids.context || record.TurnID != id {
				continue
			}
			switch record.Type {
			case ledger.TypeRuntimeTurnBound:
				view.BoundSequence = record.Seq
			case ledger.TypeRuntimeTurnResult:
				result, err := ledger.DecodeRuntimeTurnResult(record.Payload)
				if err != nil {
					return err
				}
				view.State, view.Reason, view.ModelCalls, view.ToolDispatches, view.Result = result.Outcome, result.Reason, result.ModelCalls, result.ToolDispatches, &result
			}
		}
		return nil
	})
	if err == nil && view.BoundSequence == 0 {
		err = ErrUnavailable
	}
	return view, err
}
func (v *loopSession) cancelTurn(ctx context.Context, ids submissionIDs, command, id uuid.UUID) (session.CommandResult, error) {
	result := session.CommandResult{CommandID: command, Outcome: "refused", TurnID: id}
	r, s := v.service.loop, v.service
	// Historical retries resolve globally before selecting a live cancellation.
	s.mu.Lock()
	err := s.refreshLocked(ctx, s.store.Durable())
	var match commandMatch
	if err == nil {
		match, err = s.findCommand(ctx, command, 0, s.p.through)
	}
	s.mu.Unlock()
	if err != nil {
		return result, err
	}
	if match.found {
		if match.scoped == nil || match.scoped.receipt.Submission.Operation != "turn.cancel" || match.scoped.receipt.Submission.Origin != originOf(ids) || match.scoped.ending == nil || match.scoped.ending.WorkID != id {
			return result, ErrCommandMismatch
		}
		receipt := scopedReceipt(match.scoped)
		return session.CommandResult{CommandID: command, Outcome: "applied", TurnID: id, ReceiptSequence: receipt.Sequence, EffectSequence: match.scoped.first + uint64(match.scoped.effectIndex), Data: receipt}, nil
	}
	r.mu.Lock()
	t := r.turns[id]
	if t == nil || t.origin != originOf(ids) {
		r.mu.Unlock()
		return result, ErrUnavailable
	}
	if t.cancelID != (uuid.UUID{}) && t.cancelID != command {
		r.mu.Unlock()
		return result, ErrCommandMismatch
	}
	t.cancelID, t.reason = command, "cancelled"
	t.cancel()
	done := t.done
	r.mu.Unlock()
	r.notify()
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-done:
	}
	r.mu.Lock()
	receipt, err := t.cancelReceipt, t.terminalError
	r.mu.Unlock()
	if receipt == nil {
		if err == nil {
			err = ErrUnavailable
		}
		return result, err
	}
	return session.CommandResult{CommandID: command, Outcome: "applied", TurnID: id, ReceiptSequence: receipt.Sequence, EffectSequence: receipt.Sequence - 1, Data: *receipt}, nil
}

var errLoopPageFull = errors.New("loop event page complete")

func (v *loopSession) Since(ctx context.Context, cursor uint64, limit int) (session.EventPage, error) {
	if err := v.usable(); err != nil {
		return session.EventPage{}, err
	}
	if limit == 0 {
		limit = 128
	}
	if limit < 1 || limit > 128 {
		return session.EventPage{}, ErrInvalid
	}
	s, r := v.service, v.service.loop
	r.mu.Lock()
	wake := r.changed
	r.mu.Unlock()
	if err := s.enter(ctx); err != nil {
		return session.EventPage{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		s.mu.Unlock()
		return session.EventPage{}, err
	}
	through := s.p.through
	ids, err := s.bindingLocked(Binding{v.ids.agent, v.ids.context}, false)
	s.mu.Unlock()
	if err != nil || ids.session != v.ids.session {
		return session.EventPage{}, ErrNotBound
	}
	if cursor > through {
		return session.EventPage{}, ErrInvalid
	}
	page := session.EventPage{Events: []session.Event{}, ThroughSequence: cursor, Wake: wake}
	size := 0
	err = s.scan(ctx, cursor, through, func(c ledger.Commit) error {
		var next []session.Event
		for _, record := range c.Records {
			if record.AgentID != v.ids.agent || record.ExecutionSessionID != v.ids.session {
				continue
			}
			events, err := loopEvents(record)
			if err != nil {
				return err
			}
			next = append(next, events...)
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if len(page.Events)+len(next) > limit || size+len(raw) > 1<<20 {
			if len(page.Events) == 0 {
				return ErrTooLarge
			}
			return errLoopPageFull
		}
		page.Events = append(page.Events, next...)
		size += len(raw)
		page.ThroughSequence = c.Last
		return nil
	})
	if errors.Is(err, errLoopPageFull) {
		err = nil
	}
	return page, err
}
func loopEvents(record ledger.Record) ([]session.Event, error) {
	e := session.Event{Sequence: record.Seq, AgentID: record.AgentID, ExecutionSessionID: record.ExecutionSessionID, ContextID: record.ContextID, TurnID: record.TurnID, OperationID: record.OperationID}
	var err error
	switch record.Type {
	case ledger.TypeRuntimeTurnBound:
		e.Type = "turn.started"
		e.Data, err = ledger.DecodeRuntimeTurnBound(record.Payload)
	case ledger.TypeRuntimeToolStarted:
		e.Type = "tool.started"
		e.Data, err = ledger.DecodeRuntimeToolStarted(record.Payload)
	case ledger.TypeRuntimeToolResult:
		result, err := ledger.DecodeRuntimeToolResult(record.Payload)
		if err != nil {
			return nil, err
		}
		e.Type, e.Data = "tool.finished", result
		output := e
		output.Type, output.Data = "tool.output", result.ResultObject
		return []session.Event{e, output}, nil
	case ledger.TypeRuntimePaidResult:
		e.Type = "usage"
		e.Data, err = ledger.DecodeRuntimePaidResult(record.Payload)
	case ledger.TypeRuntimeTurnResult:
		result, decodeErr := ledger.DecodeRuntimeTurnResult(record.Payload)
		err = decodeErr
		e.Type, e.Data = "turn.failed", result
		if result.Outcome == "finished" {
			e.Type = "turn.finished"
		} else if result.Outcome == "cancelled" {
			e.Type = "turn.cancelled"
		}
	default:
		return nil, nil
	}
	return []session.Event{e}, err
}
func (v *loopSession) LiveText(ctx context.Context, id uuid.UUID) (session.TextSnapshot, *session.TextSubscription, error) {
	if err := v.usable(); err != nil {
		return session.TextSnapshot{}, nil, err
	}
	r := v.service.loop
	r.mu.Lock()
	t := r.turns[id]
	if t != nil && (t.origin.AgentID != v.ids.agent || t.origin.SessionID != v.ids.session) {
		t = nil
	}
	r.mu.Unlock()
	if t == nil {
		return v.lateText(ctx, id)
	}
	snapshot, sub, err := t.live.attach()
	if err != nil {
		return snapshot, nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		sub.Close()
		return session.TextSnapshot{}, nil, session.ErrClosed
	}
	// Completed subscriptions retain no live text and need no view cleanup.
	active := v.subscriptions[:0]
	for _, old := range v.subscriptions {
		if old.Err() == nil {
			active = append(active, old)
		}
	}
	v.subscriptions = append(active, sub)
	return snapshot, sub, nil
}
func (v *loopSession) lateText(ctx context.Context, id uuid.UUID) (session.TextSnapshot, *session.TextSubscription, error) {
	s := v.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return session.TextSnapshot{}, nil, err
	}
	var result *ledger.RuntimeTurnResult
	err := s.scan(ctx, 0, s.p.through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type == ledger.TypeRuntimeTurnResult && record.TurnID == id && record.AgentID == v.ids.agent && record.ExecutionSessionID == v.ids.session {
				value, err := ledger.DecodeRuntimeTurnResult(record.Payload)
				if err != nil {
					return err
				}
				result = &value
			}
		}
		return nil
	})
	if err != nil {
		return session.TextSnapshot{}, nil, err
	}
	if result == nil {
		return session.TextSnapshot{}, nil, ErrUnavailable
	}
	return session.TextSnapshot{State: result.Outcome, FinalMessageObject: result.FinalMessageObject}, nil, nil
}

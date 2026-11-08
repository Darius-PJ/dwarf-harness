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
	ids, err := s.initialIDs(ctx, &historyReadSet{}, binding, false)
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
func (v *loopSession) bound(ctx context.Context, reads *historyReadSet, contextID uuid.UUID) (submissionIDs, error) {
	if err := v.usable(); err != nil {
		return submissionIDs{}, err
	}
	ids, err := v.service.initialIDs(ctx, reads, Binding{AgentID: v.ids.agent, ContextID: contextID}, false)
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
	reads := &historyReadSet{}
	ids, err := v.bound(ctx, reads, command.ContextID)
	if err != nil {
		return result, err
	}
	s, r := v.service, v.service.loop
	switch args := command.Arguments.(type) {
	case session.MessageSend:
		if command.Operation != "message.send" || !validID(args.TurnID) || !utf8.ValidString(args.Text) || len(args.Text) > maxObjectBytes || len(args.Artifacts) > s.limits.MaxSelectedObjects {
			return result, ErrInvalid
		}
		g, err := r.start(ctx, reads, ids, command.CommandID, args)
		if err != nil {
			return result, err
		}
		return session.CommandResult{CommandID: command.CommandID, Outcome: "applied", ReceiptSequence: g.first + 2, EffectSequence: uint64(g.receipt.EffectSequence), TurnID: args.TurnID, Data: g.receipt}, nil
	case session.TurnCancel:
		if command.Operation != "turn.cancel" || !validID(args.TurnID) {
			return result, ErrInvalid
		}
		return v.cancelTurn(ctx, reads, ids, command.CommandID, args.TurnID)
	case session.TurnStatus:
		if command.Operation != "turn.status" || !validID(args.TurnID) {
			return result, ErrInvalid
		}
		view, err := v.status(ctx, reads, ids, args.TurnID)
		if err != nil {
			return result, err
		}
		return session.CommandResult{CommandID: command.CommandID, Outcome: "ok", TurnID: args.TurnID, Data: view}, nil
	case session.ContextStatus:
		if command.Operation != "context.status" {
			return result, ErrInvalid
		}
		s.mu.Lock()
		if err := s.refreshLocked(ctx, reads, s.store.Durable()); err != nil {
			s.mu.Unlock()
			return result, err
		}
		st := s.p.scopes[keyFor(ids, "context")]
		view := session.ContextView{ContextID: ids.context, Revision: st.revision, ThroughSequence: s.p.through, Turns: []session.TurnView{}}
		for _, turn := range s.p.pendingLoopViews() {
			if turn.Origin == originOf(ids) && turn.ToolID == (uuid.UUID{}) {
				view.Turns = append(view.Turns, session.TurnView{TurnID: turn.TurnID})
			}
		}
		s.mu.Unlock()
		for i, turn := range view.Turns {
			view.Turns[i], err = v.status(ctx, reads, ids, turn.TurnID)
			if err != nil {
				return result, err
			}
		}
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
func (v *loopSession) status(ctx context.Context, reads *historyReadSet, ids submissionIDs, id uuid.UUID) (session.TurnView, error) {
	r, s := v.service.loop, v.service
	s.mu.Lock()
	defer s.mu.Unlock()
	var view session.TurnView
	err := s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		view = session.TurnView{}
		target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(originOf(ids))), Scope: "context", Slots: []string{}}
		if _, err := s.loopGrantLocked(ctx, reads, originOf(ids), "turn.inspect", []ledger.Resource{target}, 0); err != nil {
			return err
		}
		r.mu.Lock()
		live := r.turns[id]
		if live != nil && live.state == "admitted" {
			// Still binding: no owner or binding yet; report from the journal.
			live = nil
		}
		if live != nil && live.origin == originOf(ids) {
			view = r.turnView(live)
		}
		r.mu.Unlock()
		if live != nil {
			if view.TurnID == (uuid.UUID{}) {
				return ErrDenied
			}
			owner, err := s.loadContinuation(ctx, reads, id, s.p.through)
			if err != nil {
				return err
			}
			if owner.Origin != originOf(ids) {
				return ErrDenied
			}
			copyContinuationView(&view, owner)
			view.Pending = s.p.remainingContinuationPending(owner.Snapshot.Pending)
			// A stop or resume can already be durable while the worker still
			// carries the preceding park's RAM label. That is runnable work,
			// not a park under the new owner snapshot.
			if view.State == "waiting_resource" && owner.Snapshot.State != "waiting_resource" {
				view.State = "ready"
			}
			if owner.Snapshot.State != "terminal" {
				return nil
			}
			view = session.TurnView{}
		}
		var err error
		view, err = foldDirectory(ctx, s, reads, directoryKey{kind: directoryTurnStatus, id: id}, 0, s.p.through, directoryReadSelection{Purpose: "loop_status", Origin: originOf(ids)}, func(view *session.TurnView, c ledger.Commit) error {
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
		if err != nil {
			return err
		}
		owner, err := s.loadContinuation(ctx, reads, id, s.p.through)
		if err != nil {
			return err
		}
		if owner.Snapshot.Version != 0 {
			if owner.Origin != originOf(ids) {
				return ErrDenied
			}
			copyContinuationView(&view, owner)
			view.Pending = s.p.remainingContinuationPending(owner.Snapshot.Pending)
		}
		return nil
	})
	view.TurnID = id
	if view.State == "" {
		view.State = "recovery_pending"
	}
	if err == nil && view.BoundSequence == 0 {
		err = ErrUnavailable
	}
	return view, err
}
func (v *loopSession) cancelTurn(ctx context.Context, reads *historyReadSet, ids submissionIDs, command, id uuid.UUID) (session.CommandResult, error) {
	result := session.CommandResult{CommandID: command, Outcome: "refused", TurnID: id}
	s := v.service
	// Historical retries resolve globally before selecting a live cancellation.
	s.mu.Lock()
	var match commandMatch
	err := s.prepareHistoryLocked(ctx, reads, &ids, true, func(reads *historyReadSet) error {
		var err error
		match, err = s.findCommand(ctx, reads, command, 0, s.p.through)
		return err
	})
	s.mu.Unlock()
	if err != nil {
		return result, err
	}
	if match.found {
		if g := match.continuation; g != nil {
			if g.receipt.Submission.Operation != "owner.stop" || g.receipt.Submission.Actor != "owner" || g.receipt.Submission.Origin != originOf(ids) || g.change.TurnID != id || g.change.StopReason != "cancelled" {
				return result, ErrCommandMismatch
			}
			receipt := g.result()
			return session.CommandResult{CommandID: command, Outcome: "accepted", TurnID: id, ReceiptSequence: receipt.Sequence, EffectSequence: receipt.EffectSequence, Data: receipt}, nil
		}
		if match.scoped == nil || match.scoped.receipt.Submission.Operation != "turn.cancel" || match.scoped.receipt.Submission.Origin != originOf(ids) || match.scoped.ending == nil || match.scoped.ending.WorkID != id {
			return result, ErrCommandMismatch
		}
		receipt := scopedReceipt(match.scoped)
		return session.CommandResult{CommandID: command, Outcome: "applied", TurnID: id, ReceiptSequence: receipt.Sequence, EffectSequence: match.scoped.first + uint64(match.scoped.effectIndex), Data: receipt}, nil
	}
	receipt, outcome, err := s.stopContinuation(ctx, originOf(ids), id, command, "cancelled", "owner")
	if err != nil {
		return result, err
	}
	return session.CommandResult{CommandID: command, Outcome: outcome, TurnID: id, ReceiptSequence: receipt.Sequence, EffectSequence: receipt.EffectSequence, Data: receipt}, nil
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
	if err := s.refreshLocked(ctx, &historyReadSet{}, s.store.Durable()); err != nil {
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
	if cursor == through {
		return page, nil
	}
	hint := s.history.checkpoint(cursor)
	size := 0
	checkpoint, err := s.readRange(ctx, cursor, through, hint, func(c ledger.Commit) error {
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
	if joined, ok := err.(interface{ Unwrap() []error }); ok && errors.Is(err, errLoopPageFull) {
		// Remove only our early-stop sentinel, retaining any actual Close
		// failure. A cut needs its own completed numeric proof pass.
		var remaining []error
		for _, cause := range joined.Unwrap() {
			if cause != errLoopPageFull {
				remaining = append(remaining, cause)
			}
		}
		err = errors.Join(remaining...)
		if err == nil {
			checkpoint, err = s.readRange(ctx, cursor, page.ThroughSequence, hint, func(ledger.Commit) error { return nil })
		}
	}
	if err == nil {
		s.history.putCheckpoint(page.ThroughSequence, checkpoint)
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
	ids := v.ids
	var result *ledger.RuntimeTurnResult
	err := s.prepareHistoryLocked(ctx, &historyReadSet{}, &ids, true, func(reads *historyReadSet) error {
		var err error
		result, err = foldDirectory(ctx, s, reads, directoryKey{kind: directoryTurnResult, id: id}, 0, s.p.through, directoryReadSelection{Purpose: "loop_late_text", Origin: originOf(ids)}, func(result **ledger.RuntimeTurnResult, c ledger.Commit) error {
			for _, record := range c.Records {
				if record.Type == ledger.TypeRuntimeTurnResult && record.TurnID == id && record.AgentID == ids.agent && record.ExecutionSessionID == ids.session {
					value, err := ledger.DecodeRuntimeTurnResult(record.Payload)
					if err != nil {
						return err
					}
					*result = &value
				}
			}
			return nil
		})
		return err
	})
	if err != nil {
		return session.TextSnapshot{}, nil, err
	}
	if result == nil {
		return session.TextSnapshot{}, nil, ErrUnavailable
	}
	return session.TextSnapshot{State: result.Outcome, FinalMessageObject: result.FinalMessageObject}, nil, nil
}

// FinalText reads a finished turn's durable answer through the loop's own
// final-message path. Only a turn result recorded for this view's agent and
// session selects the origin; every other turn, an unended turn, a turn with
// no final message and a source without a current private.read chain are
// refused alike with ErrDenied.
func (v *loopSession) FinalText(ctx context.Context, id uuid.UUID) (string, error) {
	if err := v.usable(); err != nil {
		return "", err
	}
	if !validID(id) {
		return "", ErrInvalid
	}
	s := v.service
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer s.active.Done()
	s.mu.Lock()
	err := s.refreshLocked(ctx, &historyReadSet{}, s.store.Durable())
	through := s.p.through
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	var origin ledger.Origin
	var final *ledger.ObjectRef
	err = s.scanDirectory(ctx, directoryKey{kind: directoryTurnResult, id: id}, 0, through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type != ledger.TypeRuntimeTurnResult || record.TurnID != id || record.AgentID != v.ids.agent || record.ExecutionSessionID != v.ids.session {
				continue
			}
			result, err := ledger.DecodeRuntimeTurnResult(record.Payload)
			if err != nil {
				return err
			}
			origin, final = ledger.Origin{AgentID: record.AgentID, SessionID: record.ExecutionSessionID, ContextID: record.ContextID}, nil
			if result.Outcome == "finished" {
				final = result.FinalMessageObject
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if final == nil {
		return "", ErrDenied
	}
	// The manifest read itself carries no grant; an empty answer must still
	// require the source context's current private.read chain.
	if _, err := s.loopGrant(ctx, origin, "private.read", []ledger.Resource{loopPrivateTarget(origin)}, final.Bytes); err != nil {
		return "", err
	}
	return s.loop.finalText(ctx, origin, id, *final)
}

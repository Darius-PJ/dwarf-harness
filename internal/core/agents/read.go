package agents

import (
	"context"
	"maps"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) readScope(ctx context.Context, b ReadBinding) (submissionIDs, func(), error) {
	if b.Scope != "agent" && b.Scope != "context" {
		return submissionIDs{}, nil, ErrInvalid
	}
	ids, err := s.initialIDs(ctx, b.Binding, false)
	if err != nil {
		return ids, nil, err
	}
	unlock, err := s.acquireScope(ctx, keyFor(ids, b.Scope))
	if err != nil {
		return ids, nil, err
	}
	s.mu.Lock()
	err = s.usableLocked(ctx)
	if err == nil {
		err = s.refreshLocked(ctx, s.store.Durable())
	}
	if err == nil {
		_, err = s.bindingLocked(b.Binding, false)
	}
	s.mu.Unlock()
	if err != nil {
		unlock()
		return ids, nil, err
	}
	return ids, unlock, nil
}
func (s *Service) Snapshot(ctx context.Context, b ReadBinding) (Snapshot, error) {
	if err := s.enter(ctx); err != nil {
		return Snapshot{}, err
	}
	defer s.active.Done()
	ids, unlock, err := s.readScope(ctx, b)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.p.scopes[keyFor(ids, b.Scope)]
	result := Snapshot{ReadBinding: b, SessionID: ids.session, Revision: st.revision, ThroughSequence: s.p.through, Heads: maps.Clone(st.heads), Materializations: maps.Clone(st.materials), Issues: slices.Clone(st.issues)}
	if b.Scope == "context" {
		result.Issues = append(result.Issues, s.p.scopes[keyFor(ids, "agent")].issues...)
	}
	return result, nil
}
func (s *Service) ReadPrivate(ctx context.Context, r PrivateRead) (PrivatePage, error) {
	if err := s.enter(ctx); err != nil {
		return PrivatePage{}, err
	}
	defer s.active.Done()
	ids, unlock, err := s.readScope(ctx, r.ReadBinding)
	if err != nil {
		return PrivatePage{}, err
	}
	defer unlock()
	if r.MaxBytes <= 0 || r.MaxBytes > s.limits.MaxReadBytes {
		return PrivatePage{}, ErrTooLarge
	}
	if r.Revision == 0 {
		return PrivatePage{}, ErrInvalid
	}
	mode := "replace"
	if r.Slot == "transcript" {
		mode = "append"
	}
	// Validate the slot without supplying or retaining a private input.
	if err := validateChange(r.Scope, privateChange{slot: r.Slot, mode: mode, data: []byte{0}}); err != nil {
		return PrivatePage{}, err
	}
	if r.Slot != "transcript" && r.Cursor != 0 {
		return PrivatePage{}, ErrInvalid
	}
	s.mu.Lock()
	st := s.p.scopes[keyFor(ids, r.Scope)]
	through := s.p.through
	err = s.scopeIssuesLocked(ids, r.Scope)
	s.mu.Unlock()
	if err != nil {
		return PrivatePage{}, err
	}
	if r.Revision > st.revision {
		return PrivatePage{}, &RevisionError{Expected: r.Revision, Current: st.revision}
	}
	path, err := s.scopePath(ids, r.Scope)
	if err != nil {
		return PrivatePage{}, err
	}
	page := PrivatePage{Revision: r.Revision}
	var head Head
	cursorFound := r.Cursor == 0
	var selected []Head
	used := int64(0)
	more := false
	var last uint64
	revision, err := s.scopeHistory(ctx, ids, r.Scope, through, r.Revision, func(slot string, h Head, mode string) error {
		if slot != r.Slot {
			return nil
		}
		if r.Slot != "transcript" {
			head = h
			if mode == "clear" {
				head = Head{}
			}
			return nil
		}
		page.Present = true
		if !cursorFound {
			if h.Sequence == r.Cursor {
				cursorFound = true
			}
			return nil
		}
		if more {
			return nil
		}
		if h.Bytes > r.MaxBytes-used || len(selected) >= s.limits.MaxReadChunks {
			if len(selected) == 0 {
				return ErrTooLarge
			}
			more = true
			return nil
		}
		selected = append(selected, h)
		used += h.Bytes
		last = h.Sequence
		return nil
	})
	if err != nil {
		return PrivatePage{}, err
	}
	if revision != r.Revision {
		return PrivatePage{}, ErrUnavailable
	}
	if !cursorFound {
		return PrivatePage{}, ErrInvalid
	}
	if r.Slot == "transcript" {
		for _, h := range selected {
			data, err := readObject(path, h)
			if err != nil {
				return PrivatePage{}, s.objectIssue(path, r.ReadBinding, r.Slot, h)
			}
			page.Chunks = append(page.Chunks, Chunk{h.Revision, h.Sequence, data})
		}
		if more {
			page.NextCursor = last
		}
		return page, nil
	}
	if head.SHA256 == (ledger.Digest{}) {
		return page, nil
	}
	if head.Bytes > r.MaxBytes {
		return PrivatePage{}, ErrTooLarge
	}
	page.Bytes, err = readObject(path, head)
	if err != nil {
		return PrivatePage{}, s.objectIssue(path, r.ReadBinding, r.Slot, head)
	}
	page.Present = true
	return page, nil
}
func (s *Service) ScratchBinding(ctx context.Context, r ScratchLookup) (ScratchBinding, error) {
	if err := s.enter(ctx); err != nil {
		return ScratchBinding{}, err
	}
	defer s.active.Done()
	if !validID(r.WorkspaceID) {
		return ScratchBinding{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(ctx); err != nil {
		return ScratchBinding{}, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return ScratchBinding{}, err
	}
	ids, err := s.bindingLocked(r.Binding, false)
	if err != nil {
		return ScratchBinding{}, err
	}
	if err := s.scopeIssuesLocked(ids, "context"); err != nil {
		return ScratchBinding{}, err
	}
	result := ScratchBinding{WorkspaceID: r.WorkspaceID, ContextID: r.ContextID, State: "absent"}
	var chosen scratchState
	found := false
	if r.ExpectedScratchID != (uuid.UUID{}) {
		chosen, found = s.p.scratches[r.ExpectedScratchID]
		if !found || chosen.WorkspaceID != r.WorkspaceID || chosen.ContextID != r.ContextID {
			result.State = "mismatch"
			return result, nil
		}
	} else {
		for _, scratch := range s.p.scratches {
			if scratch.WorkspaceID != r.WorkspaceID || scratch.ContextID != r.ContextID || scratch.ParentScratchID != (uuid.UUID{}) {
				continue
			}
			if !found || chosen.closed && !scratch.closed || chosen.closed == scratch.closed && scratch.sequence > chosen.sequence {
				chosen = scratch
				found = true
			}
		}
	}
	if !found {
		return result, nil
	}
	result.ScratchID = chosen.ScratchID
	result.ParentScratchID = chosen.ParentScratchID
	result.Kind = "dedicated"
	if chosen.ParentScratchID != (uuid.UUID{}) {
		result.Kind = "temporary"
	}
	result.State = "present"
	if chosen.closed {
		result.State = "closed"
	}
	return result, nil
}

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
	return s.snapshotLocked(ids, b), nil
}

func (s *Service) snapshotLocked(ids submissionIDs, b ReadBinding) Snapshot {
	st := s.p.scopes[keyFor(ids, b.Scope)]
	result := Snapshot{ReadBinding: b, SessionID: ids.session, Revision: st.revision, ThroughSequence: s.p.through, Heads: maps.Clone(st.heads), Materializations: maps.Clone(st.materials), Issues: slices.Clone(st.issues)}
	if b.Scope == "context" {
		result.Issues = append(result.Issues, s.p.scopes[keyFor(ids, "agent")].issues...)
	}
	return result
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
	selection, err := s.selectPrivateRead(ctx, ids, r, through, s.limits.MaxReadChunks)
	if err != nil {
		return PrivatePage{}, err
	}
	return s.readPrivateSelection(path, r, selection)
}

type privateSelection struct {
	heads      []Head
	present    bool
	bytes      int64
	nextCursor uint64
}

// Selection is metadata only. Both owner and Agent reads use these exact
// historical occurrences; object reads begin only after the Agent reserves them.
func (s *Service) selectPrivateRead(ctx context.Context, ids submissionIDs, r PrivateRead, through uint64, maxSelected int) (privateSelection, error) {
	selection := privateSelection{heads: []Head{}}
	var head Head
	cursorFound := r.Cursor == 0
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
		selection.present = true
		if !cursorFound {
			if h.Sequence == r.Cursor {
				cursorFound = true
			}
			return nil
		}
		if more {
			return nil
		}
		if h.Bytes > r.MaxBytes-selection.bytes || len(selection.heads) >= maxSelected {
			if len(selection.heads) == 0 {
				return ErrTooLarge
			}
			more = true
			return nil
		}
		selection.heads = append(selection.heads, h)
		selection.bytes += h.Bytes
		last = h.Sequence
		return nil
	})
	if err != nil {
		return privateSelection{}, err
	}
	if revision != r.Revision {
		return privateSelection{}, ErrUnavailable
	}
	if !cursorFound {
		return privateSelection{}, ErrInvalid
	}
	if r.Slot == "transcript" {
		if more {
			selection.nextCursor = last
		}
		return selection, nil
	}
	if head.SHA256 != (ledger.Digest{}) {
		if head.Bytes > r.MaxBytes {
			return privateSelection{}, ErrTooLarge
		}
		selection.heads, selection.bytes, selection.present = []Head{head}, head.Bytes, true
	}
	return selection, nil
}

func (s *Service) readPrivateSelection(path privateScope, r PrivateRead, selection privateSelection) (PrivatePage, error) {
	page := PrivatePage{Revision: r.Revision, Present: selection.present, NextCursor: selection.nextCursor}
	for _, head := range selection.heads {
		data, err := readObject(path, head)
		if err != nil {
			return PrivatePage{}, s.objectIssue(path, r.ReadBinding, r.Slot, head)
		}
		if r.Slot == "transcript" {
			page.Chunks = append(page.Chunks, Chunk{head.Revision, head.Sequence, data})
		} else {
			page.Bytes = data
		}
	}
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
	return s.scratchBindingLocked(r), nil
}

func (s *Service) scratchBindingLocked(r ScratchLookup) ScratchBinding {
	result := ScratchBinding{WorkspaceID: r.WorkspaceID, ContextID: r.ContextID, State: "absent"}
	var chosen scratchState
	found := false
	if r.ExpectedScratchID != (uuid.UUID{}) {
		chosen, found = s.p.scratches[r.ExpectedScratchID]
		if !found || chosen.WorkspaceID != r.WorkspaceID || chosen.ContextID != r.ContextID {
			result.State = "mismatch"
			return result
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
		return result
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
	return result
}

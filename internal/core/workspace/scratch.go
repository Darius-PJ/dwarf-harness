package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Scratch is a private worktree bound to one context and workspace. Dir is absolute.
type Scratch struct {
	ID, WorkspaceID, ContextID          uuid.UUID
	Dir, Branch, BaseCommit, AnchorTree string
}

type scratchState struct {
	Scratch
	closed bool
}

type scratchPair struct{ context, workspace uuid.UUID }

func (s Scratch) worktree() scratchWorktree {
	return scratchWorktree{Dir: s.Dir, Branch: s.Branch, BaseCommit: s.BaseCommit, AnchorTree: s.AnchorTree, AnchorRef: "refs/dwarf-harness/anchors/" + s.ID.String()}
}

// Scratch returns the pair's open scratch, creating it on first use. Concurrent
// first uses of that pair wait within ctx; unrelated pairs proceed independently.
func (s *Service) Scratch(ctx context.Context, contextID, workspaceID uuid.UUID) (Scratch, error) {
	if err := s.start(); err != nil {
		return Scratch{}, err
	}
	defer s.ops.Done()
	if !validID(contextID) || !validID(workspaceID) {
		return Scratch{}, ErrInvalid
	}
	pair := scratchPair{contextID, workspaceID}
	for {
		if err := ctx.Err(); err != nil {
			return Scratch{}, err
		}
		s.mu.Lock()
		if s.poisoned {
			s.mu.Unlock()
			return Scratch{}, ErrPoisoned
		}
		w, ok := s.workspaces[workspaceID]
		if !ok {
			s.mu.Unlock()
			return Scratch{}, ErrUnknownWorkspace
		}
		if id, ok := s.pairs[pair]; ok {
			result := s.scratches[id].Scratch
			s.mu.Unlock()
			return result, nil
		}
		if waiting := s.creating[pair]; waiting != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return Scratch{}, ctx.Err()
			case <-waiting:
			}
			continue
		}
		s.creating[pair] = make(chan struct{})
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			close(s.creating[pair])
			delete(s.creating, pair)
			s.mu.Unlock()
		}()
		return s.createScratch(ctx, pair, w)
	}
}

func (s *Service) createScratch(ctx context.Context, pair scratchPair, w workspaceState) (Scratch, error) {
	if s.git.unavailable != nil {
		return Scratch{}, s.git.unavailable
	}
	if err := s.workspaceReady(ctx, w); err != nil {
		return Scratch{}, err
	}
	if err := s.git.checkInputsVersion(w.inputs.GitVersion); err != nil {
		return Scratch{}, err
	}
	var id uuid.UUID
	var dir string
	for range 3 {
		s.mu.Lock()
		id = s.deps.newID()
		name := shortID(id)
		_, used := s.usedDirs[name]
		_, pending := s.pendingDirs[name]
		if !validID(id) || used || pending {
			s.mu.Unlock()
			continue
		}
		s.pendingDirs[name] = true
		s.mu.Unlock()
		candidate := filepath.Join(s.colony, "scratch", name)
		_, statErr := os.Lstat(candidate)
		branch, err := s.git.readRef(ctx, s.colony, w.CommonDir, "refs/heads/dwarf-harness/"+name, oidLength(w.ObjectFormat))
		if os.IsNotExist(statErr) && err == nil && branch == "" {
			dir = candidate
			break
		}
		s.mu.Lock()
		delete(s.pendingDirs, name)
		s.mu.Unlock()
		if err != nil {
			return Scratch{}, err
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return Scratch{}, statErr
		}
	}
	if dir == "" {
		return Scratch{}, fmt.Errorf("%w: scratch names exhausted", ErrInvalid)
	}
	defer func() { s.mu.Lock(); delete(s.pendingDirs, filepath.Base(dir)); s.mu.Unlock() }()
	wt, err := s.git.createWorktree(ctx, s.colony, w.inputs, s.inputsDir(w), dir, id.String())
	if err != nil {
		return Scratch{}, err
	}
	result := Scratch{ID: id, WorkspaceID: pair.workspace, ContextID: pair.context, Dir: wt.Dir, Branch: wt.Branch, BaseCommit: wt.BaseCommit, AnchorTree: wt.AnchorTree}
	p := ledger.ScratchCreated{ScratchID: id, WorkspaceID: pair.workspace, ContextID: pair.context, Dir: filepath.Base(dir), Branch: wt.Branch, BaseCommit: wt.BaseCommit, AnchorTree: wt.AnchorTree}
	payload, err := p.Encode()
	if err == nil && s.deps.beforeAppend != nil {
		s.deps.beforeAppend()
	}
	if err == nil {
		err = s.append(ctx, []ledger.Draft{{Type: ledger.TypeScratchCreated, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: pair.context, Payload: payload}})
	}
	if err != nil {
		// An uncertain commit may be retained on recovery. Never remove its
		// worktree until replay establishes whether it was handed out.
		if !errors.Is(err, ErrPoisoned) {
			if cleanupErr := s.git.removeWorktree(context.WithoutCancel(ctx), s.colony, w.CommonDir, wt, s.deps.removeAll); cleanupErr != nil {
				return Scratch{}, fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
			}
		}
		return Scratch{}, err
	}
	return result, nil
}

// BeginWrite marks a host-managed writer. The idempotent release ends both the
// write reservation and its service operation; Close waits for it. An exclusive
// holder delays this call only within ctx. Closed scratches reject new writes.
func (s *Service) BeginWrite(ctx context.Context, scratchID uuid.UUID) (func(), error) {
	if err := s.start(); err != nil {
		return nil, err
	}
	finish := true
	defer func() {
		if finish {
			s.ops.Done()
		}
	}()
	if !validID(scratchID) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	scratch, ok := s.scratches[scratchID]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNoScratch
	}
	if scratch.closed {
		return nil, ErrClosed
	}
	release, err := s.pool.beginWrite(ctx, scratch.Dir)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	closed, poisoned := s.scratches[scratchID].closed, s.poisoned
	s.mu.Unlock()
	if closed || poisoned {
		release()
		if poisoned {
			return nil, ErrPoisoned
		}
		return nil, ErrClosed
	}
	finish = false
	var once sync.Once
	return func() { once.Do(func() { release(); s.ops.Done() }) }, nil
}

// Closure states the caller's authority. C3 authenticates the caller; the
// service checks an agent's context binding. Reason must be abandoned.
type Closure struct {
	ScratchID uuid.UUID
	Actor     ledger.Actor
	ContextID uuid.UUID
	Reason    string
}

// ClosureResult reports ignored entries discarded by this call's observation.
// Entries are those of Git's ignored listing; a wholly ignored directory counts
// once. No path is reported. Removal-only retries have no observation and report 0.
type ClosureResult struct{ IgnoredEntries int }

// CloseScratch records closure before removing a represented scratch. A refusal
// or unrepresented edit leaves it open. ErrRemovalPending means closure stands;
// this call or Open may retry removal without appending a second closure.
func (s *Service) CloseScratch(ctx context.Context, closure Closure) (ClosureResult, error) {
	var result ClosureResult
	if err := s.start(); err != nil {
		return result, err
	}
	defer s.ops.Done()
	if !validID(closure.ScratchID) {
		return result, ErrInvalid
	}
	s.mu.Lock()
	scratch, ok := s.scratches[closure.ScratchID]
	w := s.workspaces[scratch.WorkspaceID]
	s.mu.Unlock()
	if !ok {
		return result, ErrNoScratch
	}
	if closure.Actor != ledger.ActorOwner && (closure.Actor != ledger.ActorAgent || closure.ContextID != scratch.ContextID) {
		return result, ErrNotBound
	}
	if closure.Reason != "abandoned" {
		return result, ErrInvalid
	}
	r, err := s.pool.reserve(scratch.Dir, false)
	if err != nil {
		return result, err
	}
	defer r.release()
	// Another closure may have completed between the lookup and reservation.
	s.mu.Lock()
	scratch = s.scratches[closure.ScratchID]
	s.mu.Unlock()
	if !scratch.closed {
		_, statErr := os.Lstat(scratch.Dir)
		if statErr != nil && !os.IsNotExist(statErr) {
			return result, statErr
		}
		if statErr == nil {
			if s.pool.profile == nil {
				return result, &UnavailableError{Reason: "no_capacity_profile"}
			}
			if err := s.captureReady(ctx, w, scratch.Scratch); err != nil {
				return result, err
			}
			if err := r.reserveCaptureBytes(); err != nil {
				return result, err
			}
			observed, err := s.engine.capture(ctx, s.captureScratch(w, scratch.Scratch), scratch.AnchorTree, r, observeCapture)
			if err != nil {
				return result, err
			}
			if observed.Tree != scratch.AnchorTree {
				return result, ErrUnrepresentedEdits
			}
			result.IgnoredEntries = observed.IgnoredEntries
		}
		payload, err := (ledger.ScratchClosed{ScratchID: scratch.ID, ContextID: scratch.ContextID, Reason: closure.Reason}).Encode()
		if err != nil {
			return result, err
		}
		if err := s.append(ctx, []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: closure.Actor, ContextID: scratch.ContextID, Payload: payload}}); err != nil {
			return result, err
		}
	}
	err = s.git.removeWorktree(ctx, s.colony, w.CommonDir, scratch.worktree(), s.deps.removeAll)
	return result, err
}

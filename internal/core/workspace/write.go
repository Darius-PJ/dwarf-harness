package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (e *captureEngine) observationCommit(ctx context.Context, s captureScratch, tree, parent string, scratchID, observationID uuid.UUID) (string, error) {
	if !validID(scratchID) {
		return "", fmt.Errorf("%w: invalid scratch identity", ErrInvalid)
	}
	message := fmt.Sprintf("dwarf-harness scratch observation\n\nscratch %s\nobservation %s", scratchID, observationID)
	return e.deterministicCommit(ctx, s, tree, parent, observationID, message)
}

func (e *captureEngine) mergeBackCommit(ctx context.Context, s captureScratch, tree, parent string, scratchID, mergeBackID uuid.UUID) (string, error) {
	if !validID(scratchID) {
		return "", fmt.Errorf("%w: invalid scratch identity", ErrInvalid)
	}
	message := fmt.Sprintf("dwarf-harness scratch merge-back\n\nscratch %s\nmerge-back %s", scratchID, mergeBackID)
	return e.deterministicCommit(ctx, s, tree, parent, mergeBackID, message)
}

func (g *gitRunner) createScratchCommitRef(ctx context.Context, colony string, p Proposal, id uuid.UUID, commit string) error {
	return g.createCommitRef(ctx, colony, p, "scratch-commits", id, commit)
}

func (g *gitRunner) deleteScratchCommitRef(ctx context.Context, colony string, p Proposal, id uuid.UUID, commit string) error {
	return g.deleteCommitRef(ctx, colony, p, "scratch-commits", id, commit)
}

// WriteBinding is caller-stated authority; C3 authenticates it. ResumeScratchID
// explicitly attaches an interrupted foreign-context turn to its temporary.
type WriteBinding struct {
	WriterID, ContextID, AgentID, TaskAgentID, TaskScratchID, ResumeScratchID uuid.UUID
}

type writerKey struct{ context, task uuid.UUID }

// Writer separates a turn's routing lifetime from each physical tool lease.
// Its fields are service-owned; copying a handle does not copy its authority.
type Writer struct {
	service                     *Service
	self                        *Writer
	id                          uuid.UUID
	key                         writerKey
	binding                     WriteBinding
	scratchID                   uuid.UUID
	ready, changed              chan struct{}
	creationError               error
	closed, observing, observed bool
	calls, leases               int
}

// Observation identifies an acknowledged private commit, not a promotion.
type Observation struct {
	ID, ScratchID uuid.UUID
	Commit, Tree  string
	Sequence      uint64
}

type observationState struct {
	record   ledger.ScratchChanged
	sequence uint64
}

// lockTask serializes routing and private-tip publication for one dedicated
// task. No service mutex is held while waiting or doing Git work.
func (s *Service) lockTask(ctx context.Context, id uuid.UUID) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		waiting := s.routing[id]
		if waiting == nil {
			s.routing[id] = make(chan struct{})
			s.mu.Unlock()
			return func() {
				s.mu.Lock()
				close(s.routing[id])
				delete(s.routing, id)
				s.mu.Unlock()
			}, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-waiting:
		}
	}
}

func taskID(scratch Scratch) uuid.UUID {
	if scratch.ParentScratchID != (uuid.UUID{}) {
		return scratch.ParentScratchID
	}
	return scratch.ID
}

// BeginWrite routes only the task's own context to its dedicated directory.
// Another context of the same caller-stated agent receives a new temporary.
func (s *Service) BeginWrite(ctx context.Context, b WriteBinding) (_ *Writer, err error) {
	if err := s.start(); err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			s.ops.Done()
		}
	}()
	for _, id := range []uuid.UUID{b.WriterID, b.ContextID, b.AgentID, b.TaskAgentID, b.TaskScratchID} {
		if !validID(id) {
			return nil, ErrInvalid
		}
	}
	if b.ResumeScratchID != (uuid.UUID{}) && !validID(b.ResumeScratchID) {
		return nil, ErrInvalid
	}
	if b.AgentID != b.TaskAgentID {
		return nil, ErrNotBound
	}
	s.mu.Lock()
	parent, ok := s.scratches[b.TaskScratchID]
	if !ok || parent.closed || parent.ParentScratchID != (uuid.UUID{}) {
		s.mu.Unlock()
		return nil, ErrNoScratch
	}
	if previous := s.writers[b.WriterID]; previous != nil {
		if previous.closed {
			s.mu.Unlock()
			return nil, ErrClosed
		}
		if previous.binding != b {
			s.mu.Unlock()
			return nil, ErrWriterMismatch
		}
		ready := previous.ready
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ready:
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if previous.creationError != nil {
			return nil, previous.creationError
		}
		if err := previous.validLocked(); err != nil {
			return nil, err
		}
		return previous, nil
	}
	key := writerKey{b.ContextID, b.TaskScratchID}
	if s.writerKeys[key] != nil {
		s.mu.Unlock()
		return nil, &BusyError{Reason: "context_busy"}
	}
	writer := &Writer{service: s, id: b.WriterID, key: key, binding: b, ready: make(chan struct{}), changed: make(chan struct{})}
	writer.self = writer
	s.writers[b.WriterID], s.writerKeys[key] = writer, writer
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		writer.creationError = err
		if err != nil {
			delete(s.writers, writer.id)
			delete(s.writerKeys, writer.key)
			writer.closed = true
			writer.binding = WriteBinding{}
		}
		close(writer.ready)
		s.mu.Unlock()
	}()
	unlock, err := s.lockTask(ctx, b.TaskScratchID)
	if err != nil {
		return nil, err
	}
	defer func() { unlock() }()
	s.mu.Lock()
	parent = s.scratches[b.TaskScratchID]
	w := s.workspaces[parent.WorkspaceID]
	err = s.usableLocked()
	if err == nil && parent.closed {
		err = ErrNoScratch
	}
	if err == nil && parent.pendingMergeBack != (uuid.UUID{}) {
		err = ErrMergeBackPending
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	scratch := parent
	if b.ResumeScratchID != (uuid.UUID{}) {
		s.mu.Lock()
		resumed, exists := s.scratches[b.ResumeScratchID]
		s.mu.Unlock()
		if !exists || resumed.ParentScratchID != parent.ID || resumed.ContextID != b.ContextID {
			return nil, ErrNotBound
		}
		if resumed.pendingMergeBack != (uuid.UUID{}) {
			return nil, ErrMergeBackPending
		}
		if resumed.closed || resumed.appliedMergeBack != (uuid.UUID{}) {
			return nil, ErrClosed
		}
		scratch = resumed
	} else if b.ContextID != parent.ContextID {
		s.mu.Lock()
		if s.pendingChildren[parent.ID] == 0 {
			current := s.scratches[parent.ID]
			current.childrenChanged = make(chan struct{})
			s.scratches[parent.ID] = current
		}
		s.pendingChildren[parent.ID]++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.pendingChildren[parent.ID]--
			current := s.scratches[parent.ID]
			close(current.childrenChanged)
			current.childrenChanged = nil
			if s.pendingChildren[parent.ID] != 0 {
				current.childrenChanged = make(chan struct{})
			}
			s.scratches[parent.ID] = current
			s.mu.Unlock()
		}()
		if err := s.repairScratchTip(ctx, w, parent); err != nil {
			return nil, err
		}
		created, err := s.createScratch(ctx, scratchPair{b.ContextID, parent.WorkspaceID}, w, parent.ID, parent.TipCommit)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		scratch = s.scratches[created.ID]
		s.mu.Unlock()
	}
	// Do not hold routing while waiting on a merge-back's sole writer.
	// The sole writer must reacquire routing before publishing its tip.
	unlock()
	unlock = func() {}
	release, err := s.pool.beginWrite(ctx, scratch.Dir)
	if err != nil {
		return nil, err
	}
	defer release()
	relocked, err := s.lockTask(ctx, b.TaskScratchID)
	if err != nil {
		return nil, err
	}
	unlock = relocked
	s.mu.Lock()
	scratch = s.scratches[scratch.ID]
	parent = s.scratches[b.TaskScratchID]
	if scratch.pendingMergeBack != (uuid.UUID{}) || parent.pendingMergeBack != (uuid.UUID{}) {
		err = ErrMergeBackPending
	} else if scratch.closed || scratch.appliedMergeBack != (uuid.UUID{}) {
		err = ErrClosed
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := s.repairScratchTip(ctx, w, scratch); err != nil {
		return nil, err
	}
	s.mu.Lock()
	err = s.usableLocked()
	if err == nil && s.scratches[scratch.ID].closed {
		err = ErrClosed
	}
	if err == nil {
		writer.scratchID = scratch.ID
		keep = true
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *Writer) validLocked() error {
	if w.self != w || w.service.writers[w.id] != w {
		if w.self == w && w.closed {
			return ErrClosed
		}
		return ErrInvalid
	}
	if w.closed || w.observed {
		return ErrClosed
	}
	if w.service.poisoned {
		return ErrPoisoned
	}
	return nil
}

func (w *Writer) signalLocked() {
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *Writer) retireLocked() {
	if w.closed && w.calls == 0 && w.service.writers[w.id] == w {
		delete(w.service.writers, w.id)
		delete(w.service.writerKeys, w.key)
		w.service.ops.Done()
	}
}

// Scratch returns a current snapshot, or zero when the handle cannot write.
func (w *Writer) Scratch() Scratch {
	if w == nil || w.service == nil {
		return Scratch{}
	}
	w.service.mu.Lock()
	defer w.service.mu.Unlock()
	if w.validLocked() != nil {
		return Scratch{}
	}
	return w.service.scratches[w.scratchID].Scratch
}

// Lease waits within ctx while this writer observes or the pool is exclusive.
// Its idempotent release ends physical exclusion, not the logical writer.
func (w *Writer) Lease(ctx context.Context) (func(), error) {
	if w == nil || w.service == nil {
		return nil, ErrInvalid
	}
	s := w.service
	s.mu.Lock()
	if err := w.validLocked(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	w.calls++
	for w.observing {
		changed := w.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		s.mu.Lock()
		err := w.validLocked()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			w.calls--
			w.retireLocked()
			s.mu.Unlock()
			return nil, err
		}
	}
	w.leases++
	scratch := s.scratches[w.scratchID]
	s.mu.Unlock()
	release, err := s.pool.beginWrite(ctx, scratch.Dir)
	s.mu.Lock()
	if err == nil {
		err = w.validLocked()
	}
	if err == nil {
		err = s.scratches[w.scratchID].tipRepairFailed
	}
	if err != nil {
		if release != nil {
			release()
		}
		w.leases--
		w.calls--
		w.signalLocked()
		w.retireLocked()
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			release()
			s.mu.Lock()
			w.leases--
			w.calls--
			w.signalLocked()
			w.retireLocked()
			s.mu.Unlock()
		})
	}, nil
}

// Close releases logical occupancy after outstanding calls and leases drain.
// It remains safe on an invalid handle or a poisoned service.
func (w *Writer) Close() {
	if w == nil || w.service == nil {
		return
	}
	s := w.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.self != w || s.writers[w.id] != w || w.closed {
		return
	}
	w.closed = true
	w.binding = WriteBinding{}
	w.signalLocked()
	w.retireLocked()
}

// repairScratchTip projects only an acknowledged transition. It never moves an
// unexpected value, checks out files, changes the real index or appends records.
func (s *Service) repairScratchTip(ctx context.Context, w workspaceState, scratch scratchState) (err error) {
	defer func() {
		if err != nil && ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		current := s.scratches[scratch.ID]
		current.tipRepairFailed = err
		s.scratches[scratch.ID] = current
		s.mu.Unlock()
	}()
	if err := s.captureReady(ctx, w, scratch.Scratch); err != nil {
		return err
	}
	if _, err := scratchAdmin(scratch.worktree(), w.CommonDir); err != nil {
		return err
	}
	n := oidLength(w.ObjectFormat)
	current, err := s.git.readRef(ctx, s.colony, w.CommonDir, scratch.Branch, n)
	if err != nil {
		return err
	}
	if scratch.ParentScratchID == (uuid.UUID{}) && scratch.pendingMergeBack != (uuid.UUID{}) {
		s.mu.Lock()
		plan := s.mergeBacks[scratch.pendingMergeBack].record
		s.mu.Unlock()
		if current != plan.ParentCommit && current != plan.TargetCommit {
			return &UnavailableError{Reason: "merge_back_interference", Detail: current}
		}
		return nil
	}
	if current == scratch.TipCommit {
		return nil
	}
	if current != "" && current != scratch.previousTip {
		return &UnavailableError{Reason: "scratch_tip_mismatch", Detail: current}
	}
	old := current
	if old == "" {
		old = strings.Repeat("0", n)
	}
	if _, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "update-ref", "--end-of-options", scratch.Branch, scratch.TipCommit, old); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, readErr := s.git.readRef(ctx, s.colony, w.CommonDir, scratch.Branch, n)
		if readErr == nil && current == scratch.TipCommit {
			return nil
		}
		return &UnavailableError{Reason: "scratch_tip_mismatch", Detail: current}
	}
	return nil
}

func (s *Service) privateCaptureScratch(ctx context.Context, w workspaceState, scratch Scratch) (captureScratch, error) {
	cs := s.captureScratch(w, scratch)
	anchor, err := s.engine.treeNames(ctx, cs, scratch.AnchorTree)
	if err != nil {
		return cs, err
	}
	tip, err := s.engine.treeNames(ctx, cs, scratch.TipTree)
	if err != nil {
		return cs, err
	}
	cs.TrustedNames = append(anchor, tip...)
	slices.Sort(cs.TrustedNames)
	cs.TrustedNames = slices.Compact(cs.TrustedNames)
	return cs, nil
}

func (s *Service) scratchPaths(ctx context.Context, w workspaceState, previous, tree string) ([]string, error) {
	out, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "diff-tree", "-r", "-z", "--no-renames", "--name-only", "--end-of-options", previous, tree)
	if err != nil {
		return nil, err
	}
	paths, err := parseNULList(out)
	if err != nil {
		return nil, fmt.Errorf("%w: scratch paths: %v", ErrGit, err)
	}
	for _, path := range paths {
		if path == "" || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\\") {
			return nil, refuse("invalid_path", path, "not a repository path")
		}
		for component := range strings.SplitSeq(path, "/") {
			if component == "" || component == "." || component == ".." {
				return nil, refuse("invalid_path", path, "not a repository path")
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

func observationDrafts(p ledger.ScratchChanged, previousPin uuid.UUID) ([]ledger.Draft, error) {
	payload, err := p.Encode()
	if err != nil {
		return nil, err
	}
	pin, err := pinDraft(p.PinID, p.ManifestSHA256, p.ContextID)
	if err != nil {
		return nil, err
	}
	drafts := []ledger.Draft{{Type: ledger.TypeScratchChanged, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: p.ContextID, BodyRefs: []ledger.Digest{p.ManifestSHA256}, Payload: payload}, pin}
	if previousPin != (uuid.UUID{}) {
		unpin, err := unpinDraft(previousPin, p.ContextID)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, unpin)
	}
	return drafts, nil
}

// Size against the encoder's largest legal envelope, including framing, held
// clock and a 20-digit sequence. Other producers may append before our commit.
func preflightObservation(colony, id uuid.UUID, drafts []ledger.Draft) error {
	_, _, err := ledger.EncodeCommit(
		ledger.Chain{Seq: math.MaxUint64 - uint64(len(drafts)), Hash: ledger.ZeroHash, EventTimeNS: math.MaxInt64},
		ledger.CommitHeader{ColonyID: colony, CommitID: id, EventTimeNS: math.MaxInt64, ClockHeld: true}, drafts)
	if errors.Is(err, ledger.ErrTooLarge) {
		return refuse("pathset_cap", "", "encoded observation exceeds the ledger cap")
	}
	return err
}

// CommitObserved freezes tools, records the observation, then repairs the
// private branch to that acknowledged commit. A refusal leaves the writer live.
func (w *Writer) CommitObserved(ctx context.Context) (result Observation, err error) {
	if w == nil || w.service == nil {
		return result, ErrInvalid
	}
	s := w.service
	s.mu.Lock()
	if err := w.validLocked(); err != nil {
		s.mu.Unlock()
		return result, err
	}
	if w.observing {
		s.mu.Unlock()
		return result, &BusyError{Reason: "context_busy"}
	}
	w.observing = true
	w.calls++
	defer func() {
		s.mu.Lock()
		w.observing = false
		w.observed = result.ID != (uuid.UUID{})
		w.calls--
		w.signalLocked()
		w.retireLocked()
		s.mu.Unlock()
	}()
	if s.deps.afterWriterFreeze != nil {
		s.mu.Unlock()
		s.deps.afterWriterFreeze()
		s.mu.Lock()
	}
	for w.leases != 0 {
		changed := w.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		s.mu.Lock()
		err := w.validLocked()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.mu.Unlock()
			return result, err
		}
	}
	scratch := s.scratches[w.scratchID]
	s.mu.Unlock()
	unlock, err := s.lockTask(ctx, taskID(scratch.Scratch))
	if err != nil {
		return result, err
	}
	defer unlock()
	s.mu.Lock()
	err = w.validLocked()
	scratch = s.scratches[w.scratchID]
	workspace := s.workspaces[scratch.WorkspaceID]
	if err == nil && scratch.pendingMergeBack != (uuid.UUID{}) {
		err = ErrMergeBackPending
	}
	s.mu.Unlock()
	if err != nil {
		return result, err
	}
	r, err := s.pool.reserve(scratch.Dir, observationReservation)
	if err != nil {
		return result, err
	}
	defer r.release()
	if err := s.repairScratchTip(ctx, workspace, scratch); err != nil {
		return result, err
	}
	cs, err := s.privateCaptureScratch(ctx, workspace, scratch.Scratch)
	if err != nil {
		return result, err
	}
	captured, err := s.engine.capture(ctx, cs, scratch.TipTree, r, observeCapture)
	if err != nil {
		return result, err
	}
	written, err := s.scratchPaths(ctx, workspace, scratch.TipTree, captured.Tree)
	if err != nil {
		return result, err
	}
	changed, err := s.scratchPaths(ctx, workspace, scratch.AnchorTree, captured.Tree)
	if err != nil {
		return result, err
	}
	s.mu.Lock()
	id, pin := s.deps.newID(), s.deps.newID()
	s.mu.Unlock()
	// Git hashes have fixed encoded width. Preflight before making the commit,
	// publishing a body or creating a retention ref.
	p := ledger.ScratchChanged{ObservationID: id, WorkspaceID: scratch.WorkspaceID, ScratchID: scratch.ID, ContextID: scratch.ContextID, ParentScratchID: scratch.ParentScratchID, PreviousCommit: scratch.TipCommit, PreviousTree: scratch.TipTree, ScratchCommit: strings.Repeat("0", len(scratch.TipCommit)), ScratchTree: captured.Tree, WrittenPaths: written, ChangedPaths: changed, InputsRevision: workspace.Revision, CaptureInputsSHA256: workspace.InputsSHA256, ScratchRules: "scratch-rules/1", ManifestSHA256: ledger.Digest(captured.ManifestSHA256), PinID: pin}
	drafts, err := observationDrafts(p, scratch.observationPin)
	if err != nil {
		return result, err
	}
	if err := preflightObservation(s.store.Colony(), id, drafts); err != nil {
		return result, err
	}
	p.ScratchCommit, err = s.engine.observationCommit(ctx, cs, captured.Tree, scratch.TipCommit, scratch.ID, id)
	if err != nil {
		return result, err
	}
	if err := s.git.createScratchCommitRef(ctx, s.colony, workspace.inputs, id, p.ScratchCommit); err != nil {
		return result, err
	}
	defer func() {
		if err != nil && result.ID == (uuid.UUID{}) && !errors.Is(err, ErrPoisoned) {
			if cleanupErr := s.git.deleteScratchCommitRef(context.WithoutCancel(ctx), s.colony, workspace.inputs, id, p.ScratchCommit); cleanupErr != nil {
				err = fmt.Errorf("%w; observation release: %v", err, cleanupErr)
			}
		}
	}()
	digest, _, err := s.store.PublishBody(ctx, bytes.NewReader(captured.Manifest))
	if err != nil {
		return result, err
	}
	if digest != p.ManifestSHA256 {
		return result, fmt.Errorf("%w: observation manifest digest differs", ErrInvalid)
	}
	drafts, err = observationDrafts(p, scratch.observationPin)
	if err != nil {
		return result, err
	}
	if s.deps.beforeAppend != nil {
		s.deps.beforeAppend()
	}
	s.mu.Lock()
	err = s.appendLocked(ctx, drafts)
	if err == nil {
		scratch = s.scratches[scratch.ID]
		result = Observation{ID: id, ScratchID: scratch.ID, Commit: p.ScratchCommit, Tree: p.ScratchTree, Sequence: scratch.observationSequence}
	}
	s.mu.Unlock()
	if err != nil {
		return result, err
	}
	return result, s.repairScratchTip(ctx, workspace, scratch)
}

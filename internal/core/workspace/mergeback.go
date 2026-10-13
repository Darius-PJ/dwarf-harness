package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (e *captureEngine) treeNames(ctx context.Context, s captureScratch, tree string) ([]string, error) {
	out, _, err := e.git.repository(ctx, e.colony, s.Inputs.CommonDir, nil, []int{0}, "ls-tree", "-r", "-z", "--name-only", "--full-tree", "--end-of-options", tree)
	if err != nil {
		return nil, err
	}
	names, err := parseNULList(out)
	if err != nil {
		return nil, fmt.Errorf("%w: tree paths: %v", ErrGit, err)
	}
	return names, nil
}

// materialize takes an actual tree already observed under guard. On retry this
// is O from disk, not the original parent or the mutable worktree index. The
// caller owns the guard and any durable preparation; this primitive records
// nothing and does not promise rollback of a partially completed Git checkout.
func (e *captureEngine) materialize(ctx context.Context, s captureScratch, actual, target, previousCommit, targetCommit string, id uuid.UUID, guard *reservation) (err error) {
	n := oidLength(s.Inputs.ObjectFormat)
	if !validID(id) || !validOID(actual, n) || !validOID(target, n) || !validOID(previousCommit, n) || !validOID(targetCommit, n) {
		return fmt.Errorf("%w: invalid materialization inputs", ErrInvalid)
	}
	if guard == nil || guard.pool == nil || guard.scratch != s.Worktree.Dir || guard.kind != soleWriterReservation {
		return &reservationError{reason: "materialization requires its sole writer"}
	}
	token, err := guard.pool.reserveCaptureUnder(guard)
	if err != nil {
		return err
	}
	defer token.release()
	if _, err := e.prepareIntegration(ctx, s); err != nil {
		return err
	}
	marker, err := e.git.readBlob(ctx, e.colony, s.Inputs.CommonDir, "refs/dwarf-harness/colony")
	if err != nil {
		return err
	}
	if s.ColonyID == "" || string(marker) != "dwarf-harness colony "+s.ColonyID+"\n" {
		return &UnavailableError{Reason: "workspace_unverified"}
	}
	admin, err := scratchAdmin(s.Worktree, s.Inputs.CommonDir)
	if err != nil {
		return err
	}
	branch, err := e.git.readRef(ctx, e.colony, s.Inputs.CommonDir, s.Worktree.Branch, n)
	if err != nil {
		return err
	}
	if branch != previousCommit && branch != targetCommit {
		return &UnavailableError{Reason: "scratch_tip_mismatch", Detail: branch}
	}
	commitTree, err := e.git.readRef(ctx, e.colony, s.Inputs.CommonDir, targetCommit+"^{tree}", n)
	if err != nil {
		return err
	}
	if commitTree != target {
		return fmt.Errorf("%w: target commit has another tree", ErrInvalid)
	}
	actualNames, err := e.treeNames(ctx, s, actual)
	if err != nil {
		return err
	}
	targetNames, err := e.treeNames(ctx, s, target)
	if err != nil {
		return err
	}
	if err := checkCaseCollisions(targetNames); err != nil {
		return err
	}
	index := filepath.Join(s.TmpDir, uuid.NewV7().String()+".materialize.idx")
	// Never reuse even a private index left by an interrupted invocation.
	for _, path := range []string{index, index + ".lock"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			if err != nil {
				return err
			}
			return &BusyError{Reason: "scratch_busy"}
		}
	}
	defer func() {
		for _, path := range []string{index, index + ".lock"} {
			if cleanup := os.Remove(path); cleanup != nil && !os.IsNotExist(cleanup) {
				err = errors.Join(err, cleanup)
			}
		}
	}()
	env := captureEnvironment(e.git.environ(), e.colony, s.InputsDir, s.Worktree.Dir, index, s.Inputs.CommonDir)
	call := func(args ...string) error {
		_, _, err := e.git.run(ctx, s.Worktree.Dir, env, nil, []int{0}, args...)
		return err
	}
	if err := call("read-tree", "--end-of-options", actual); err != nil {
		return err
	}
	if err := call("update-index", "--refresh"); err != nil {
		return err
	}
	if err := preflightMaterialization(ctx, s.Worktree.Dir, actualNames, targetNames); err != nil {
		return err
	}
	if e.beforeMergeBackFiles != nil {
		e.beforeMergeBackFiles()
	}
	if err := call("read-tree", "-m", "-u", "--end-of-options", actual, target); err != nil {
		return err
	}
	if e.afterMergeBackFiles != nil {
		e.afterMergeBackFiles()
	}
	s.TrustedNames = append(actualNames, targetNames...)
	observed, err := e.capture(ctx, s, target, token, observeCapture)
	if err != nil {
		return err
	}
	if observed.Tree != target {
		return refuse("materialization_mismatch", "", "checkout did not produce the exact target tree")
	}
	if err := e.installScratchIndex(ctx, s, admin, index, target, id); err != nil {
		return err
	}
	// The branch is the last projection installed. A verified already-landed
	// retry needs no ref effect; an unexpected value is never overwritten.
	branch, err = e.git.readRef(ctx, e.colony, s.Inputs.CommonDir, s.Worktree.Branch, n)
	if err != nil {
		return err
	}
	if branch == targetCommit {
		return nil
	}
	_, _, err = e.git.repository(ctx, e.colony, s.Inputs.CommonDir, nil, []int{0}, "update-ref", "--end-of-options", s.Worktree.Branch, targetCommit, previousCommit)
	if err != nil && ctx.Err() == nil {
		current, readErr := e.git.readRef(ctx, e.colony, s.Inputs.CommonDir, s.Worktree.Branch, n)
		if readErr == nil && current != previousCommit && current != targetCommit {
			return &UnavailableError{Reason: "scratch_tip_mismatch", Detail: current}
		}
	}
	return err
}

func preflightMaterialization(ctx context.Context, root string, actual, target []string) error {
	files, dirs := map[string]bool{}, map[string]bool{}
	for _, path := range actual {
		files[path] = true
		for i := range path {
			if path[i] == '/' {
				dirs[path[:i]] = true
			}
		}
	}
	obstruction := func(path string) error {
		return refuse("obstructed_path", path, "checkout would replace unrepresented work")
	}
	for _, path := range target {
		if files[path] {
			continue
		}
		parts := strings.Split(path, "/")
		for i := range parts {
			if err := ctx.Err(); err != nil {
				return err
			}
			prefix := strings.Join(parts[:i+1], "/")
			full := filepath.Join(root, filepath.FromSlash(prefix))
			info, err := os.Lstat(full)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return err
			}
			if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				return obstruction(prefix)
			}
			if !info.IsDir() {
				if files[prefix] && info.Mode().IsRegular() {
					break
				}
				return obstruction(prefix)
			}
			if i != len(parts)-1 {
				continue
			}
			if !dirs[prefix] {
				return obstruction(prefix)
			}
			// Directory-to-file replacement must not erase an ignored child or
			// even an empty directory absent from the observed actual tree.
			if err := filepath.WalkDir(full, func(child string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				rel, err := filepath.Rel(root, child)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || (d.IsDir() && !dirs[rel]) || (!d.IsDir() && !files[rel]) {
					return obstruction(rel)
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// scratchAdmin applies the regular-gitfile/common-directory identity rules and
// also verifies the worktree's backpointer and symbolic private HEAD before an
// index can be installed. Same-user check/use races remain C10's boundary.
func scratchAdmin(s scratchWorktree, common string) (string, error) {
	unverified := &UnavailableError{Reason: "workspace_unverified"}
	info, err := os.Lstat(s.Dir)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return "", unverified
	}
	gitfile := filepath.Join(s.Dir, ".git")
	gitinfo, err := os.Lstat(gitfile)
	if err != nil || !gitinfo.Mode().IsRegular() {
		return "", unverified
	}
	b, err := os.ReadFile(gitfile)
	if err != nil {
		return "", err
	}
	admin, ok := strings.CutPrefix(strings.TrimSuffix(string(b), "\n"), "gitdir: ")
	if !ok || admin == "" || strings.ContainsAny(admin, "\x00\r\n") {
		return "", unverified
	}
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(s.Dir, admin)
	}
	admin = filepath.Clean(admin)
	parent := filepath.Dir(admin)
	if filepath.Base(parent) != "worktrees" {
		return "", unverified
	}
	actual, err := os.Stat(filepath.Dir(parent))
	if err != nil {
		return "", unverified
	}
	wanted, err := os.Stat(common)
	if err != nil || !os.SameFile(actual, wanted) {
		return "", unverified
	}
	info, err = os.Lstat(admin)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return "", unverified
	}
	b, err = os.ReadFile(filepath.Join(admin, "gitdir"))
	if err != nil {
		return "", unverified
	}
	back := strings.TrimSuffix(string(b), "\n")
	if !filepath.IsAbs(back) {
		back = filepath.Join(admin, back)
	}
	backinfo, err := os.Stat(back)
	if err != nil || !os.SameFile(backinfo, gitinfo) {
		return "", unverified
	}
	b, err = os.ReadFile(filepath.Join(admin, "HEAD"))
	if err != nil || !strings.HasPrefix(s.Branch, "refs/heads/dwarf-harness/") || string(b) != "ref: "+s.Branch+"\n" {
		return "", unverified
	}
	return admin, nil
}

func (e *captureEngine) installScratchIndex(ctx context.Context, s captureScratch, admin, index, target string, id uuid.UUID) (err error) {
	verified, err := scratchAdmin(s.Worktree, s.Inputs.CommonDir)
	if err != nil {
		return err
	}
	if filepath.Clean(admin) != verified || !validID(id) {
		return fmt.Errorf("%w: invalid index installation", ErrInvalid)
	}
	admin = verified
	if err := ctx.Err(); err != nil {
		return err
	}
	lock := filepath.Join(admin, "index.lock")
	marker := []byte("dwarf-harness scratch-index " + id.String() + "\n")
	file, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return &BusyError{Reason: "scratch_busy"}
	}
	if err != nil {
		return err
	}
	owned, statErr := file.Stat()
	_, writeErr := file.Write(marker)
	syncErr := file.Sync()
	closeErr := file.Close()
	// Only an unchanged lock created by this call can be removed.
	removeLock := func() error {
		now, err := os.Lstat(lock)
		if err != nil {
			return err
		}
		if owned == nil || !os.SameFile(owned, now) {
			return &BusyError{Reason: "scratch_busy"}
		}
		b, err := os.ReadFile(lock)
		if err != nil {
			return err
		}
		if !bytes.Equal(b, marker) {
			return &BusyError{Reason: "scratch_busy"}
		}
		return os.Remove(lock)
	}
	locked := true
	defer func() {
		if locked {
			err = errors.Join(err, removeLock())
		}
	}()
	if err := errors.Join(statErr, writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(index, filepath.Join(admin, "index")); err != nil {
		return err
	}
	if e.afterMergeBackIndex != nil {
		e.afterMergeBackIndex()
	}
	if err := removeLock(); err != nil {
		return err
	}
	locked = false
	// Never let verification Git opportunistically write the real index.
	copyPath := filepath.Join(s.TmpDir, uuid.NewV7().String()+".verify.idx")
	b, err := os.ReadFile(filepath.Join(admin, "index"))
	if err != nil {
		return err
	}
	copyFile, err := os.OpenFile(copyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		for _, path := range []string{copyPath, copyPath + ".lock"} {
			if cleanup := os.Remove(path); cleanup != nil && !os.IsNotExist(cleanup) {
				err = errors.Join(err, cleanup)
			}
		}
	}()
	_, writeErr = copyFile.Write(b)
	closeErr = copyFile.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	env := captureEnvironment(e.git.environ(), e.colony, s.InputsDir, s.Worktree.Dir, copyPath, s.Inputs.CommonDir)
	out, _, err := e.git.run(ctx, s.Worktree.Dir, env, nil, []int{0}, "write-tree")
	if err != nil {
		return err
	}
	if strings.TrimSuffix(string(out), "\n") != target {
		return refuse("materialization_mismatch", "", "installed index differs from target")
	}
	return nil
}

// MergeBackResult is a durable private-work outcome, never delivery.
type MergeBackResult struct {
	ID, ScratchID, ParentScratchID    uuid.UUID
	Outcome, TargetCommit, TargetTree string
	Sequence                          uint64
	ConflictSHA256                    ledger.Digest
}

// ScratchNotice is passive, task-scoped work for the next natural turn.
// ChangedPaths is nil when the result was not applied.
type ScratchNotice struct {
	Sequence                                                    uint64
	MergeBackID, ScratchID, ParentScratchID, AuthoringContextID uuid.UUID
	Outcome                                                     string
	WrittenPaths, ChangedPaths                                  []string
	ConflictSHA256                                              ledger.Digest
}

type mergeBackState struct {
	record      ledger.ScratchMergeBack
	sequence    uint64
	preparedPin uuid.UUID
}

func (m mergeBackState) applied() bool {
	return m.record.Outcome == ledger.MergeBackFastForward || m.record.Outcome == ledger.MergeBackMerged
}

func (m mergeBackState) pending() bool {
	return m.record.Outcome == ledger.MergeBackPrepared || m.record.Outcome == ledger.MergeBackWriteFailed
}

func (m mergeBackState) result() MergeBackResult {
	p := m.record
	return MergeBackResult{ID: p.MergeBackID, ScratchID: p.ScratchID, ParentScratchID: p.ParentScratchID, Outcome: p.Outcome, TargetCommit: p.TargetCommit, TargetTree: p.TargetTree, Sequence: m.sequence, ConflictSHA256: p.ConflictSHA256}
}

func sameMergeBackPlan(a, b ledger.ScratchMergeBack) bool {
	return a.MergeBackID == b.MergeBackID && a.WorkspaceID == b.WorkspaceID && a.ScratchID == b.ScratchID &&
		a.ParentScratchID == b.ParentScratchID && a.ContextID == b.ContextID && a.AuthoringContextID == b.AuthoringContextID &&
		a.SourceObservationID == b.SourceObservationID && a.ForkCommit == b.ForkCommit && a.ForkTree == b.ForkTree &&
		a.ParentCommit == b.ParentCommit && a.ParentTree == b.ParentTree && a.SourceCommit == b.SourceCommit &&
		a.SourceTree == b.SourceTree && a.InputsRevision == b.InputsRevision && a.CaptureInputsSHA256 == b.CaptureInputsSHA256 &&
		a.IntegrationRules == b.IntegrationRules && a.TargetCommit == b.TargetCommit && a.TargetTree == b.TargetTree &&
		slices.Equal(a.WrittenPaths, b.WrittenPaths)
}

func mergeBackRefs(manifest ledger.Digest, revision inputsRevision) []ledger.Digest {
	refs := append(slices.Clone(revision.refs), manifest)
	slices.SortFunc(refs, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(refs)
}

// applyMergeBackRecord shares apply's undo journal and pin obligations.
// It reads only recorded facts, never bodies, Git, time or policy capacity.
func (s *Service) applyMergeBackRecord(drafts []ledger.Draft, at int, seq uint64, producing bool, rules *requestCommitRules, journal *[]func()) error {
	d := drafts[at]
	p, err := ledger.DecodeScratchMergeBack(d.Payload)
	if err != nil {
		return err
	}
	bad := func(detail string) error { return errors.New(detail) }
	if d.Actor != ledger.ActorCore || d.ContextID != p.ContextID {
		return bad("merge-back actor or context")
	}
	source, exists := s.scratches[p.ScratchID]
	parent, hasParent := s.scratches[p.ParentScratchID]
	if !exists || !hasParent || source.closed || parent.closed || source.ParentScratchID != parent.ID || parent.ParentScratchID != (uuid.UUID{}) ||
		source.WorkspaceID != p.WorkspaceID || parent.WorkspaceID != p.WorkspaceID || source.ContextID != p.AuthoringContextID || parent.ContextID != p.ContextID {
		return bad("merge-back requires its open source temporary and dedicated parent")
	}
	observation := s.observations[source.ObservationID].record
	if source.ObservationID != p.SourceObservationID || observation.ScratchCommit != p.SourceCommit || observation.ScratchTree != p.SourceTree ||
		source.TipCommit != p.SourceCommit || source.TipTree != p.SourceTree || source.BaseCommit != p.ForkCommit || source.AnchorTree != p.ForkTree {
		return bad("merge-back source observation, tip or fork differs")
	}
	w := s.workspaces[p.WorkspaceID]
	if p.InputsRevision > w.Revision || w.revisions[p.InputsRevision].digest != p.CaptureInputsSHA256 {
		return bad("merge-back inputs revision or digest")
	}
	for _, oid := range []string{p.ForkCommit, p.ForkTree, p.ParentCommit, p.ParentTree, p.SourceCommit, p.SourceTree, p.TargetCommit, p.TargetTree} {
		if oid != "" && !validOID(oid, oidLength(w.ObjectFormat)) {
			return bad("merge-back Git ID length")
		}
	}
	old, used := s.mergeBacks[p.MergeBackID]
	next := mergeBackState{record: p, sequence: seq, preparedPin: old.preparedPin}
	nextSource, nextParent := source, parent
	switch p.Outcome {
	case ledger.MergeBackPrepared, ledger.MergeBackConflict:
		if used || source.pendingMergeBack != (uuid.UUID{}) || parent.pendingMergeBack != (uuid.UUID{}) {
			return bad("merge-back ID reused or scratch already prepared")
		}
		if p.ParentCommit != parent.TipCommit || p.ParentTree != parent.TipTree {
			return bad("merge-back parent differs from acknowledged tip")
		}
		if p.Outcome == ledger.MergeBackPrepared {
			refs := mergeBackRefs(p.ManifestSHA256, w.revisions[p.InputsRevision])
			if !slices.Equal(d.BodyRefs, refs) || !matchingPin(drafts, at+1, p.PinID, refs) {
				return bad("preparation refs union or immediate pin")
			}
			next.preparedPin = p.PinID
			nextSource.pendingMergeBack, nextParent.pendingMergeBack = p.MergeBackID, p.MergeBackID
			previous, existed := s.manifestPins[p.PinID]
			s.manifestPins[p.PinID] = p.MergeBackID
			*journal = append(*journal, func() {
				if existed {
					s.manifestPins[p.PinID] = previous
				} else {
					delete(s.manifestPins, p.PinID)
				}
			})
		} else {
			if !slices.Equal(d.BodyRefs, []ledger.Digest{p.ConflictSHA256}) || !matchingPin(drafts, at+1, p.ReportPinID, d.BodyRefs) {
				return bad("merge-back conflict refs or immediate pin")
			}
			nextParent.reportPins = append(slices.Clone(parent.reportPins), p.ReportPinID)
			previous, existed := s.reportPinScratch[p.ReportPinID]
			s.reportPinScratch[p.ReportPinID] = parent.ID
			*journal = append(*journal, func() {
				if existed {
					s.reportPinScratch[p.ReportPinID] = previous
				} else {
					delete(s.reportPinScratch, p.ReportPinID)
				}
			})
		}
	case ledger.MergeBackWriteFailed, ledger.MergeBackFastForward, ledger.MergeBackMerged:
		if !used || !old.pending() || source.pendingMergeBack != p.MergeBackID || parent.pendingMergeBack != p.MergeBackID || !sameMergeBackPlan(old.record, p) {
			return bad("merge-back result does not repeat its unresolved preparation")
		}
		if len(d.BodyRefs) != 0 {
			return bad("merge-back result carries body refs")
		}
		if producing && p.Outcome == ledger.MergeBackWriteFailed {
			switch p.Failure {
			case ledger.MergeBackFailureGit, ledger.MergeBackFailureIndexLock, ledger.MergeBackFailureRefMismatch,
				ledger.MergeBackFailureVerification, ledger.MergeBackFailureCancelled, ledger.MergeBackFailureInterference:
			default:
				return bad("producer does not define merge-back failure token")
			}
		}
		if p.Outcome != ledger.MergeBackWriteFailed {
			if (len(p.ChangedPaths) == 0) != (parent.AnchorTree == p.TargetTree) {
				return bad("applied changed_paths emptiness differs from anchor/tree identity")
			}
			if err := requireUnpins(drafts, at, []uuid.UUID{old.preparedPin}); err != nil {
				return err
			}
			rules.unpins[old.preparedPin] = at
			delete(s.manifestPins, old.preparedPin)
			*journal = append(*journal, func() { s.manifestPins[old.preparedPin] = p.MergeBackID })
			nextSource.pendingMergeBack, nextParent.pendingMergeBack = uuid.UUID{}, uuid.UUID{}
			nextSource.appliedMergeBack = p.MergeBackID
			nextParent.previousTip, nextParent.TipCommit, nextParent.TipTree = p.ParentCommit, p.TargetCommit, p.TargetTree
			nextParent.mergeBackIDs = append(slices.Clone(parent.mergeBackIDs), p.MergeBackID)
		}
	}
	if p.Outcome != ledger.MergeBackPrepared {
		notice := ScratchNotice{Sequence: seq, MergeBackID: p.MergeBackID, ScratchID: p.ScratchID, ParentScratchID: p.ParentScratchID,
			AuthoringContextID: p.AuthoringContextID, Outcome: p.Outcome, WrittenPaths: slices.Clone(p.WrittenPaths), ChangedPaths: slices.Clone(p.ChangedPaths), ConflictSHA256: p.ConflictSHA256}
		nextParent.notices = append(slices.Clone(parent.notices), notice)
	}
	s.mergeBacks[p.MergeBackID] = next
	s.scratches[source.ID], s.scratches[parent.ID] = nextSource, nextParent
	*journal = append(*journal, func() {
		if used {
			s.mergeBacks[p.MergeBackID] = old
		} else {
			delete(s.mergeBacks, p.MergeBackID)
		}
		s.scratches[source.ID], s.scratches[parent.ID] = source, parent
	})
	return nil
}

func (s *Service) admissionProvenance(scratch scratchState, parentID uuid.UUID, before uint64) (uuid.UUID, []uuid.UUID) {
	after := scratch.creationSequence
	if parentID != (uuid.UUID{}) {
		after = s.requests[parentID].sequence
	}
	var ids []uuid.UUID
	for _, id := range scratch.mergeBackIDs {
		m := s.mergeBacks[id]
		if m.sequence > after && m.sequence < before {
			ids = append(ids, id)
		}
	}
	return scratch.ObservationID, ids
}

// ScratchNotices reads results without consuming them, even after closure.
func (s *Service) ScratchNotices(ctx context.Context, taskContextID, taskScratchID uuid.UUID, after uint64) ([]ScratchNotice, error) {
	if err := s.start(); err != nil {
		return nil, err
	}
	defer s.ops.Done()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scratch, ok := s.scratches[taskScratchID]
	if !ok || scratch.ParentScratchID != (uuid.UUID{}) || scratch.ContextID != taskContextID {
		return nil, ErrNotBound
	}
	if s.poisoned {
		return nil, ErrPoisoned
	}
	var notices []ScratchNotice
	for _, notice := range scratch.notices {
		if notice.Sequence > after {
			notice.WrittenPaths, notice.ChangedPaths = slices.Clone(notice.WrittenPaths), slices.Clone(notice.ChangedPaths)
			notices = append(notices, notice)
		}
	}
	return notices, nil
}

func mergeBackDrafts(p ledger.ScratchMergeBack, refs []ledger.Digest, release uuid.UUID) ([]ledger.Draft, error) {
	payload, err := p.Encode()
	if err != nil {
		return nil, err
	}
	drafts := []ledger.Draft{{Type: ledger.TypeScratchMergeBack, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: p.ContextID, BodyRefs: refs, Payload: payload}}
	pinID := p.PinID
	if p.Outcome == ledger.MergeBackConflict {
		pinID = p.ReportPinID
	}
	if pinID != (uuid.UUID{}) {
		payload, err := (ledger.BodyPin{PinID: pinID}).Encode()
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, ledger.Draft{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: p.ContextID, BodyRefs: refs, Payload: payload})
	}
	if release != (uuid.UUID{}) {
		unpin, err := unpinDraft(release, p.ContextID)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, unpin)
	}
	return drafts, nil
}

func (s *Service) mergeSource(ctx context.Context, id, retry uuid.UUID) (scratchState, *reservation, error) {
	s.mu.Lock()
	source, ok := s.scratches[id]
	s.mu.Unlock()
	if !ok || source.ParentScratchID == (uuid.UUID{}) {
		return source, nil, ErrNoScratch
	}
	unlock, err := s.lockTask(ctx, source.ParentScratchID)
	if err != nil {
		return source, nil, err
	}
	defer unlock()
	s.mu.Lock()
	source = s.scratches[id]
	err = s.usableLocked()
	if err == nil {
		switch {
		case source.closed:
			err = ErrClosed
		case source.pendingMergeBack != retry:
			err = ErrMergeBackPending
		case source.ObservationID == (uuid.UUID{}):
			err = ErrUncommittedEdits
		case source.live != 0:
			err = ErrLiveRequests
		}
	}
	for _, writer := range s.writers {
		if err == nil && writer.scratchID == id {
			err = &BusyError{Reason: "scratch_busy"}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return source, nil, err
	}
	guard, err := s.pool.reserve(source.Dir, observationReservation)
	return source, guard, err
}

// The task lock is released before every wait. After sole acquisition it is
// reacquired and occupancy is checked again before any private-tip operation.
func (s *Service) mergeParent(ctx context.Context, id, retry uuid.UUID) (scratchState, *reservation, func(), error) {
	if s.deps.beforeMergeBackParent != nil {
		s.deps.beforeMergeBackParent()
	}
	for {
		unlock, err := s.lockTask(ctx, id)
		if err != nil {
			return scratchState{}, nil, nil, err
		}
		s.mu.Lock()
		parent := s.scratches[id]
		err = s.usableLocked()
		if err == nil && parent.closed {
			err = ErrClosed
		}
		if err == nil && parent.pendingMergeBack != retry {
			err = ErrMergeBackPending
		}
		var waiting <-chan struct{}
		for _, writer := range s.writers {
			if writer.scratchID == id {
				waiting = writer.changed
				break
			}
		}
		s.mu.Unlock()
		unlock()
		if err != nil {
			return parent, nil, nil, err
		}
		if waiting != nil {
			if s.deps.beforeMergeBackWait != nil {
				s.deps.beforeMergeBackWait()
			}
			select {
			case <-ctx.Done():
				return parent, nil, nil, ctx.Err()
			case <-waiting:
			}
			continue
		}
		guard, err := s.pool.beginSoleWrite(ctx, parent.Dir)
		if err != nil {
			return parent, nil, nil, err
		}
		unlock, err = s.lockTask(ctx, id)
		if err != nil {
			guard.release()
			return parent, nil, nil, err
		}
		s.mu.Lock()
		parent = s.scratches[id]
		err = s.usableLocked()
		if err == nil && parent.closed {
			err = ErrClosed
		}
		if err == nil && parent.pendingMergeBack != retry {
			err = ErrMergeBackPending
		}
		busy := s.pendingChildren[id] != 0
		for _, writer := range s.writers {
			busy = busy || writer.scratchID == id
		}
		if err == nil && !busy {
			parent.merging = true
			s.scratches[id] = parent
		}
		s.mu.Unlock()
		if err != nil || busy {
			unlock()
			guard.release()
			if err != nil {
				return parent, nil, nil, err
			}
			continue
		}
		releaseRouting := func() {
			s.mu.Lock()
			current := s.scratches[id]
			current.merging = false
			s.scratches[id] = current
			s.mu.Unlock()
			unlock()
		}
		return parent, guard, releaseRouting, nil
	}
}

func (s *Service) observeMergeParent(ctx context.Context, w workspaceState, parent scratchState, guard *reservation, target string) (captureResult, error) {
	cs, err := s.privateCaptureScratch(ctx, w, parent.Scratch)
	if err != nil {
		return captureResult{}, err
	}
	if target != "" {
		old, err := s.engine.treeNames(ctx, cs, parent.TipTree)
		if err != nil {
			return captureResult{}, err
		}
		newNames, err := s.engine.treeNames(ctx, cs, target)
		if err != nil {
			return captureResult{}, err
		}
		cs.TrustedNames = append(old, newNames...)
	}
	token, err := s.pool.reserveCaptureUnder(guard)
	if err != nil {
		return captureResult{}, err
	}
	defer token.release()
	return s.engine.capture(ctx, cs, parent.TipTree, token, observeCapture)
}

// MergeBack merges a completed temporary into its dedicated task scratch.
// Conflicts retain both scratches; failures after preparation require RetryMergeBack.
func (s *Service) MergeBack(ctx context.Context, temporaryID uuid.UUID) (result MergeBackResult, err error) {
	if err := s.start(); err != nil {
		return result, err
	}
	defer s.ops.Done()
	if !validID(temporaryID) {
		return result, ErrInvalid
	}
	s.mu.Lock()
	oldSource := s.scratches[temporaryID]
	applied := s.mergeBacks[oldSource.appliedMergeBack]
	s.mu.Unlock()
	if applied.applied() {
		return applied.result(), s.finishAppliedSource(ctx, applied)
	}
	source, sourceGuard, err := s.mergeSource(ctx, temporaryID, uuid.UUID{})
	if err != nil {
		return result, err
	}
	defer sourceGuard.release()
	// Keep the inputs revision's own pin through the handoff to preparation.
	if err := s.lockInputs(ctx); err != nil {
		return result, err
	}
	inputsHeld := true
	defer func() {
		if inputsHeld {
			<-s.inputsLock
		}
	}()
	s.mu.Lock()
	w := s.workspaces[source.WorkspaceID]
	observation := s.observations[source.ObservationID].record
	s.mu.Unlock()
	if err := s.repairScratchTip(ctx, w, source); err != nil {
		return result, err
	}
	cs, err := s.privateCaptureScratch(ctx, w, source.Scratch)
	if err != nil {
		return result, err
	}
	observed, err := s.engine.capture(ctx, cs, source.TipTree, sourceGuard, observeCapture)
	if err != nil {
		return result, err
	}
	if observed.Tree != source.TipTree {
		return result, ErrUncommittedEdits
	}
	parent, guard, unlock, err := s.mergeParent(ctx, source.ParentScratchID, uuid.UUID{})
	if err != nil {
		return result, err
	}
	defer guard.release()
	defer unlock()
	if err := s.repairScratchTip(ctx, w, parent); err != nil {
		return result, err
	}
	before, err := s.observeMergeParent(ctx, w, parent, guard, "")
	if err != nil {
		return result, err
	}
	if before.Tree != parent.TipTree {
		return result, ErrUncommittedEdits
	}
	s.mu.Lock()
	id, pin := s.deps.newID(), s.deps.newID()
	s.mu.Unlock()
	p := ledger.ScratchMergeBack{MergeBackID: id, WorkspaceID: source.WorkspaceID, ScratchID: source.ID,
		ParentScratchID: parent.ID, ContextID: parent.ContextID, AuthoringContextID: source.ContextID,
		SourceObservationID: source.ObservationID, ForkCommit: source.BaseCommit, ForkTree: source.AnchorTree,
		ParentCommit: parent.TipCommit, ParentTree: parent.TipTree, SourceCommit: source.TipCommit, SourceTree: source.TipTree,
		InputsRevision: w.Revision, CaptureInputsSHA256: w.InputsSHA256, IntegrationRules: "scratch-merge-rules/1",
		Outcome: ledger.MergeBackPrepared, TargetCommit: source.TipCommit, TargetTree: source.TipTree,
		WrittenPaths: slices.Clone(observation.ChangedPaths), ManifestSHA256: ledger.Digest(before.ManifestSHA256), PinID: pin}
	parentCS := s.captureScratch(w, parent.Scratch)
	if parent.TipCommit != source.BaseCommit {
		merged, err := s.engine.mergeTrees(ctx, parentCS, source.AnchorTree, parent.TipTree, source.TipTree)
		if err != nil {
			return result, err
		}
		if merged.Cause != "" {
			report, digest, err := conflictReport(merged.Cause, merged.Paths)
			if err != nil {
				return result, err
			}
			p.Outcome, p.TargetCommit, p.TargetTree = ledger.MergeBackConflict, "", ""
			p.ManifestSHA256, p.PinID = ledger.Digest{}, uuid.UUID{}
			p.ConflictSHA256, p.ReportPinID = ledger.Digest(digest), pin
			if _, _, err := s.store.PublishBody(ctx, bytes.NewReader(report)); err != nil {
				return result, err
			}
			drafts, err := mergeBackDrafts(p, []ledger.Digest{p.ConflictSHA256}, uuid.UUID{})
			if err != nil {
				return result, err
			}
			if err := s.append(ctx, drafts); err != nil {
				return result, err
			}
			s.mu.Lock()
			result = s.mergeBacks[id].result()
			s.mu.Unlock()
			return result, nil
		}
		p.TargetTree = merged.Tree
		p.TargetCommit, err = s.engine.mergeBackCommit(ctx, parentCS, merged.Tree, parent.TipCommit, source.ID, id)
		if err != nil {
			return result, err
		}
	}
	refs := mergeBackRefs(p.ManifestSHA256, w.revisions[p.InputsRevision])
	drafts, err := mergeBackDrafts(p, refs, uuid.UUID{})
	if err != nil {
		return result, err
	}
	if err := preflightObservation(s.store.Colony(), id, drafts); err != nil {
		return result, err
	}
	if err := s.git.createScratchCommitRef(ctx, s.colony, w.inputs, id, p.TargetCommit); err != nil {
		return result, err
	}
	prepared := false
	defer func() {
		if !prepared && !errors.Is(err, ErrPoisoned) {
			_ = s.git.deleteScratchCommitRef(context.WithoutCancel(ctx), s.colony, w.inputs, id, p.TargetCommit)
		}
	}()
	digest, _, err := s.store.PublishBody(ctx, bytes.NewReader(before.Manifest))
	if err != nil {
		return result, err
	}
	if digest != p.ManifestSHA256 {
		return result, fmt.Errorf("%w: merge-back manifest digest differs", ErrInvalid)
	}
	if s.deps.beforeAppend != nil {
		s.deps.beforeAppend()
	}
	if err := s.append(ctx, drafts); err != nil {
		return result, err
	}
	prepared = true
	<-s.inputsLock
	inputsHeld = false
	if s.deps.afterMergeBackPrepared != nil {
		s.deps.afterMergeBackPrepared()
	}
	return s.landMergeBack(ctx, w, parent, p, before.Tree, guard)
}

func (s *Service) landMergeBack(ctx context.Context, w workspaceState, parent scratchState, plan ledger.ScratchMergeBack, actual string, guard *reservation) (MergeBackResult, error) {
	if err := s.engine.materialize(ctx, s.captureScratch(w, parent.Scratch), actual, plan.TargetTree, plan.ParentCommit, plan.TargetCommit, plan.MergeBackID, guard); err != nil {
		return s.recordMergeBackFailure(ctx, plan, err)
	}
	if s.deps.beforeMergeBackApplied != nil {
		s.deps.beforeMergeBackApplied()
	}
	if err := ctx.Err(); err != nil {
		return s.recordMergeBackFailure(ctx, plan, err)
	}
	changed, err := s.scratchPaths(ctx, w, parent.AnchorTree, plan.TargetTree)
	if err != nil {
		return s.recordMergeBackFailure(ctx, plan, err)
	}
	p := plan
	p.Outcome = ledger.MergeBackMerged
	if plan.ParentCommit == plan.ForkCommit {
		p.Outcome = ledger.MergeBackFastForward
	}
	p.ManifestSHA256, p.PinID, p.Failure = ledger.Digest{}, uuid.UUID{}, ""
	p.ChangedPaths = append([]string{}, changed...)
	s.mu.Lock()
	pin := s.mergeBacks[p.MergeBackID].preparedPin
	s.mu.Unlock()
	drafts, err := mergeBackDrafts(p, nil, pin)
	if err != nil {
		return MergeBackResult{}, err
	}
	if err := s.append(ctx, drafts); err != nil {
		return MergeBackResult{}, err
	}
	guard.release()
	if s.deps.afterMergeBackApplied != nil {
		s.deps.afterMergeBackApplied()
	}
	s.mu.Lock()
	applied := s.mergeBacks[p.MergeBackID]
	source := s.scratches[p.ScratchID]
	s.mu.Unlock()
	return applied.result(), s.closeMergedSource(ctx, w, source)
}

func (s *Service) recordMergeBackFailure(ctx context.Context, plan ledger.ScratchMergeBack, cause error) (MergeBackResult, error) {
	var failure string
	var gitErr *GitError
	var busy *BusyError
	var refusal *RefusalError
	var unavailable *UnavailableError
	switch {
	case ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded):
		failure = ledger.MergeBackFailureCancelled
	case errors.As(cause, &unavailable):
		switch unavailable.Reason {
		case "scratch_tip_mismatch":
			failure = ledger.MergeBackFailureRefMismatch
			cause = &UnavailableError{Reason: "merge_back_interference", Detail: unavailable.Detail}
		case "merge_back_interference":
			failure = ledger.MergeBackFailureInterference
		}
	case errors.As(cause, &gitErr) && gitErr.Exit > 0:
		failure = ledger.MergeBackFailureGit
	case errors.As(cause, &busy) && busy.Reason == "scratch_busy":
		failure = ledger.MergeBackFailureIndexLock
	case errors.As(cause, &refusal) && refusal.Reason == "materialization_mismatch":
		failure = ledger.MergeBackFailureVerification
	}
	if failure == "" {
		return MergeBackResult{}, cause
	}
	p := plan
	p.Outcome, p.Failure = ledger.MergeBackWriteFailed, failure
	p.ManifestSHA256, p.PinID, p.ChangedPaths = ledger.Digest{}, uuid.UUID{}, nil
	drafts, err := mergeBackDrafts(p, nil, uuid.UUID{})
	if err == nil {
		err = s.append(context.WithoutCancel(ctx), drafts)
	}
	if err != nil {
		return MergeBackResult{}, errors.Join(cause, err)
	}
	s.mu.Lock()
	result := s.mergeBacks[p.MergeBackID].result()
	s.mu.Unlock()
	return result, cause
}

// closeMergedSource runs under the source guard and the task routing lock.
func (s *Service) closeMergedSource(ctx context.Context, w workspaceState, source scratchState) error {
	if !source.closed {
		payload, err := (ledger.ScratchClosed{ScratchID: source.ID, ContextID: source.ContextID, Reason: ledger.ReasonTemporaryMergedBack}).Encode()
		if err != nil {
			return err
		}
		drafts := []ledger.Draft{{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: source.ContextID, Payload: payload}}
		s.mu.Lock()
		for _, pin := range s.scratchPins(source) {
			draft, err := unpinDraft(pin, source.ContextID)
			if err != nil {
				s.mu.Unlock()
				return err
			}
			drafts = append(drafts, draft)
		}
		before := s.retainedRefs(w.ID)
		err = s.appendLocked(ctx, drafts)
		after := s.retainedRefs(w.ID)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		for ref, retained := range before {
			if _, keep := after[ref]; !keep {
				_, _, _ = s.git.repository(context.WithoutCancel(ctx), s.colony, w.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, retained.object)
			}
		}
	}
	return s.git.removeWorktree(ctx, s.colony, w.CommonDir, source.worktree(), s.deps.removeAll)
}

func (s *Service) finishAppliedSource(ctx context.Context, applied mergeBackState) error {
	s.mu.Lock()
	source := s.scratches[applied.record.ScratchID]
	w := s.workspaces[source.WorkspaceID]
	s.mu.Unlock()
	guard, err := s.pool.reserve(source.Dir, closureReservation)
	if err != nil {
		return err
	}
	defer guard.release()
	unlock, err := s.lockTask(ctx, source.ParentScratchID)
	if err != nil {
		return err
	}
	defer unlock()
	s.mu.Lock()
	source = s.scratches[source.ID]
	s.mu.Unlock()
	if !source.closed {
		if err := s.repairScratchTip(ctx, w, source); err != nil {
			return err
		}
		token, err := s.pool.reserveCaptureUnder(guard)
		if err != nil {
			return err
		}
		defer token.release()
		cs, err := s.privateCaptureScratch(ctx, w, source.Scratch)
		if err != nil {
			return err
		}
		observed, err := s.engine.capture(ctx, cs, source.TipTree, token, observeCapture)
		if err != nil {
			return err
		}
		if observed.Tree != source.TipTree {
			return ErrUnrepresentedEdits
		}
	}
	return s.closeMergedSource(ctx, w, source)
}

// RetryMergeBack continues only the recorded plan, never a newly computed merge.
func (s *Service) RetryMergeBack(ctx context.Context, mergeBackID uuid.UUID) (MergeBackResult, error) {
	if err := s.start(); err != nil {
		return MergeBackResult{}, err
	}
	defer s.ops.Done()
	if !validID(mergeBackID) {
		return MergeBackResult{}, ErrInvalid
	}
	s.mu.Lock()
	attempt, exists := s.mergeBacks[mergeBackID]
	s.mu.Unlock()
	if !exists {
		return MergeBackResult{}, ErrInvalid
	}
	if attempt.applied() {
		return attempt.result(), s.finishAppliedSource(ctx, attempt)
	}
	if !attempt.pending() {
		return MergeBackResult{}, ErrInvalid
	}
	p := attempt.record
	source, sourceGuard, err := s.mergeSource(ctx, p.ScratchID, mergeBackID)
	if err != nil {
		return MergeBackResult{}, err
	}
	defer sourceGuard.release()
	s.mu.Lock()
	w := s.workspaces[p.WorkspaceID]
	w.Revision, w.InputsSHA256, w.refs = p.InputsRevision, p.CaptureInputsSHA256, w.revisions[p.InputsRevision].refs
	s.mu.Unlock()
	// The preparation pin owns this historical revision, not the current
	// inputs pointer. Only explicit retry reads these superseded bodies.
	w.inputsError, w.inputsDetail = "", ""
	w, err = s.loadInputs(ctx, w)
	if err != nil {
		return MergeBackResult{}, err
	}
	if err := s.repairScratchTip(ctx, w, source); err != nil {
		return MergeBackResult{}, err
	}
	cs, err := s.privateCaptureScratch(ctx, w, source.Scratch)
	if err != nil {
		return MergeBackResult{}, err
	}
	observed, err := s.engine.capture(ctx, cs, source.TipTree, sourceGuard, observeCapture)
	if err != nil {
		return MergeBackResult{}, err
	}
	if observed.Tree != p.SourceTree {
		return s.recordMergeBackFailure(ctx, p, &UnavailableError{Reason: "merge_back_interference"})
	}
	parent, guard, unlock, err := s.mergeParent(ctx, p.ParentScratchID, mergeBackID)
	if err != nil {
		return MergeBackResult{}, err
	}
	defer guard.release()
	defer unlock()
	if err := s.repairScratchTip(ctx, w, parent); err != nil {
		return s.recordMergeBackFailure(ctx, p, err)
	}
	observed, err = s.observeMergeParent(ctx, w, parent, guard, p.TargetTree)
	if err != nil {
		return s.recordMergeBackFailure(ctx, p, err)
	}
	if err := s.verifyRetryTree(ctx, w, p.ParentTree, p.TargetTree, observed.Tree); err != nil {
		return s.recordMergeBackFailure(ctx, p, err)
	}
	if err := removePreparedIndexLock(parent.worktree(), w.CommonDir, mergeBackID); err != nil {
		return s.recordMergeBackFailure(ctx, p, err)
	}
	return s.landMergeBack(ctx, w, parent, p, observed.Tree, guard)
}

func (s *Service) verifyRetryTree(ctx context.Context, w workspaceState, previous, target, actual string) error {
	entries := func(tree string) (map[string]stageEntry, error) {
		out, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "ls-tree", "-r", "-z", "--full-tree", "--format=%(objectmode) %(objectname) 0%x09%(path)", "--end-of-options", tree)
		if err != nil {
			return nil, err
		}
		rows, err := parseStageEntries(out, oidLength(w.ObjectFormat))
		if err != nil {
			return nil, fmt.Errorf("%w: retry tree listing: %v", ErrGit, err)
		}
		result := make(map[string]stageEntry, len(rows))
		for _, row := range rows {
			result[row.Path] = row
		}
		return result, nil
	}
	old, err := entries(previous)
	if err != nil {
		return err
	}
	want, err := entries(target)
	if err != nil {
		return err
	}
	got, err := entries(actual)
	if err != nil {
		return err
	}
	// Comparing the complete union also rejects new unrepresented paths and
	// deletions outside the plan, not only third values on changed paths.
	for _, tree := range []map[string]stageEntry{old, want, got} {
		for path := range tree {
			if got[path] != old[path] && got[path] != want[path] {
				return &UnavailableError{Reason: "merge_back_interference", Detail: path}
			}
		}
	}
	return nil
}

func removePreparedIndexLock(scratch scratchWorktree, common string, id uuid.UUID) error {
	admin, err := scratchAdmin(scratch, common)
	if err != nil {
		return err
	}
	path := filepath.Join(admin, "index.lock")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return &BusyError{Reason: "scratch_busy"}
	}
	marker, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(marker) != "dwarf-harness scratch-index "+id.String()+"\n" {
		return &BusyError{Reason: "scratch_busy"}
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return &BusyError{Reason: "scratch_busy"}
	}
	return os.Remove(path)
}

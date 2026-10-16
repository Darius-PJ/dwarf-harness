package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

type mainUpdateState uint8

const (
	mainUpdateRefused mainUpdateState = iota
	mainUpdateMoved
	mainUpdateThird
)

// canonicalPreflight is repeated before the intent and inside the guarded CAS.
func (s *Service) canonicalPreflight(ctx context.Context, w workspaceState, old, target, targetTree string) error {
	n := oidLength(w.ObjectFormat)
	if !validOID(old, n) || !validOID(target, n) || !validOID(targetTree, n) {
		return ErrInvalid
	}
	backend, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "rev-parse", "--show-ref-format")
	if err != nil {
		return err
	}
	if string(backend) != "files\n" {
		return &UnavailableError{Reason: "ref_backend_unavailable"}
	}
	_, exit, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0, 1}, "symbolic-ref", "-q", "--end-of-options", "refs/heads/main")
	if err != nil {
		return err
	}
	if exit != 1 {
		return fmt.Errorf("%w: canonical main is symbolic", ErrGit)
	}
	entries, err := s.git.listWorktrees(ctx, s.colony, w.CommonDir, n)
	if err != nil {
		return err
	}
	if err := canonicalNotCheckedOut(entries); err != nil {
		return err
	}
	for _, check := range []struct{ suffix, want string }{{"^{commit}", target}, {"^{tree}", targetTree}, {"^1", old}, {"^2", ""}} {
		got, err := s.git.readRef(ctx, s.colony, w.CommonDir, target+check.suffix, n)
		if err != nil {
			return err
		}
		if got != check.want {
			return fmt.Errorf("%w: canonical target %s differs", ErrGit, check.suffix)
		}
	}
	return nil
}

// guardedMainUpdate is record-free and has no product caller. Its caller holds
// lockTrain. A moved ref is only an observed effect, never durable acceptance.
func (s *Service) guardedMainUpdate(ctx context.Context, w workspaceState, old, target, targetTree string) (mainUpdateState, error) {
	if err := s.canonicalPreflight(ctx, w, old, target, targetTree); err != nil {
		return mainUpdateRefused, err
	}
	n := oidLength(w.ObjectFormat)
	// Any exit, including cancellation or a lost acknowledgement, must be
	// classified by the ref. Never retry, roll back or delete another lock.
	_, _, _ = s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "update-ref", "--no-deref", "--end-of-options", "refs/heads/main", target, old)
	actual, err := s.git.readRef(context.WithoutCancel(ctx), s.colony, w.CommonDir, "refs/heads/main", n)
	if err != nil {
		return mainUpdateThird, err
	}
	switch actual {
	case target:
		return mainUpdateMoved, nil
	case old:
		return mainUpdateRefused, nil
	default:
		return mainUpdateThird, nil
	}
}

// hardenCanonicalStorage completes Git's file flushes with directory flushes,
// then checks the effect again. It never repairs or rolls back a moved ref.
func (s *Service) hardenCanonicalStorage(ctx context.Context, w workspaceState, target, targetTree string) error {
	objects := filepath.Join(w.CommonDir, "objects")
	refs := filepath.Join(w.CommonDir, "refs")
	heads := filepath.Join(refs, "heads")
	// Validate ancestors before listing or opening their children. This is not
	// OS containment: C10 fences hostile same-user replacements between calls.
	for _, path := range []string{w.CommonDir, objects, refs, heads} {
		if err := canonicalDirectory(path); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(objects)
	if err != nil {
		return err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if (len(name) == 2 && strings.Trim(name, "0123456789abcdef") == "") || name == "pack" {
			paths = append(paths, filepath.Join(objects, name))
		}
	}
	paths = append(paths, objects, heads, refs, w.CommonDir)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := canonicalDirectory(path); err != nil {
			return err
		}
		if err := durable.SyncDir(path); err != nil {
			return err
		}
	}
	commit, tree, err := s.mainTip(ctx, w)
	if err != nil {
		return err
	}
	if commit != target || tree != targetTree {
		return fmt.Errorf("%w: canonical effect changed during durability", ErrGit)
	}
	return nil
}

func canonicalDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return fmt.Errorf("%w: canonical storage is not a real directory: %s", ErrGit, path)
	}
	return nil
}

type acceptedWitness struct {
	sequence uint64
	record   ledger.PromotionAccepted
}

// witnessMatches is record-only; absence retains F2-06's observed-main rule.
// Call under s.mu. F2-08 is frozen (D123).
func (s *Service) witnessMatches(workspaceID uuid.UUID, seq int64, commit, tree string) bool {
	w, exists := s.witnesses[workspaceID]
	if !exists {
		return seq == 0
	}
	return seq > 0 && uint64(seq) == w.sequence && commit == w.record.AcceptedCommit && tree == w.record.AcceptedTree
}

func (s *Service) finalizationCandidate(id, generationID uuid.UUID) (requestState, error) {
	r, exists := s.requests[id]
	if !exists {
		return r, ErrInvalid
	}
	a := r.admission
	if _, pending := s.intents[a.WorkspaceID]; pending {
		return r, &UnavailableError{Reason: "finalization_pending"}
	}
	if r.ending != "" {
		return r, nil
	}
	scratch := s.scratches[a.ScratchID]
	if scratch.closed || scratch.ParentScratchID != (uuid.UUID{}) {
		return r, ErrNoScratch
	}
	train := s.trains[a.WorkspaceID]
	if len(train) == 0 || train[0] != id {
		return r, &UnavailableError{Reason: "request_not_leading"}
	}
	g := r.generation
	if g == nil || g.Outcome != ledger.GenerationClean || (generationID != (uuid.UUID{}) && generationID != g.GenerationID) {
		return r, &UnavailableError{Reason: "generation_not_current"}
	}
	if parent := a.ParentRequestID; parent != (uuid.UUID{}) && !s.requests[parent].successful() {
		return r, &UnavailableError{Reason: "parent_not_accepted"}
	}
	witness := s.witnesses[a.WorkspaceID]
	if !s.witnessMatches(a.WorkspaceID, int64(witness.sequence), g.PredecessorCommit, g.PredecessorTree) ||
		(g.PredecessorRequestID != (uuid.UUID{}) && (witness.record.RequestID != g.PredecessorRequestID || witness.record.GenerationID != g.PredecessorGenerationID)) {
		return r, &UnavailableError{Reason: "predecessor_not_accepted"}
	}
	return r, nil
}

func (s *Service) finalizeStep(step string) {
	if s.deps.beforeFinalizeStep != nil {
		s.deps.beforeFinalizeStep(step)
	}
}

// finalize and noChange are mechanical, package-test-only entry points until C4.
func (s *Service) finalize(ctx context.Context, requestID, generationID uuid.UUID) (RequestState, error) {
	if !validID(generationID) {
		return RequestState{}, ErrInvalid
	}
	return s.finalizeRequest(ctx, requestID, generationID, false)
}

func (s *Service) noChange(ctx context.Context, requestID uuid.UUID) (RequestState, error) {
	return s.finalizeRequest(ctx, requestID, uuid.UUID{}, true)
}

func (s *Service) finalizeRequest(ctx context.Context, requestID, generationID uuid.UUID, onlyNoChange bool) (state RequestState, err error) {
	if err := s.start(); err != nil {
		return state, err
	}
	defer s.ops.Done()
	s.mu.Lock()
	r, exists := s.requests[requestID]
	commonDir := s.workspaces[r.admission.WorkspaceID].CommonDir
	s.mu.Unlock()
	if !exists {
		return state, ErrInvalid
	}
	unlock, err := s.lockTrain(ctx, r.admission.WorkspaceID)
	if err != nil {
		return r.state, err
	}
	var released map[string]retainedRef
	// The terminal acknowledgement precedes the slot, blocker and train release;
	// recorded-object ref releases happen after the train lock is dropped.
	defer func() {
		unlock()
		for ref, retained := range released {
			_, _, _ = s.git.repository(context.WithoutCancel(ctx), s.colony, commonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, retained.object)
		}
	}()
	s.mu.Lock()
	r, err = s.finalizationCandidate(requestID, generationID)
	w := s.workspaces[r.admission.WorkspaceID]
	witness := s.witnesses[w.ID]
	s.mu.Unlock()
	if err != nil || r.ending != "" {
		return r.state, err
	}
	defer func() {
		s.mu.Lock()
		state = s.requests[requestID].state
		s.mu.Unlock()
	}()
	s.finalizeStep("preflight")
	if err := s.integrationReady(ctx, w); err != nil {
		return r.state, err
	}
	g := *r.generation
	commit, tree, err := s.mainTip(ctx, w)
	if err != nil {
		return r.state, err
	}
	if commit != g.PredecessorCommit || tree != g.PredecessorTree {
		return r.state, &UnavailableError{Reason: "predecessor_not_current"}
	}
	if g.DerivedTree == tree {
		released, err = s.noChangeLocked(ctx, r, witness)
		return r.state, err
	}
	if onlyNoChange {
		return r.state, &UnavailableError{Reason: "not_no_change"}
	}
	if err := s.canonicalPreflight(ctx, w, commit, g.DerivedCommit, g.DerivedTree); err != nil {
		return r.state, err
	}
	paths, err := s.scratchPaths(ctx, w, tree, g.DerivedTree)
	if err != nil {
		return r.state, err
	}
	s.mu.Lock()
	intentID := s.deps.newID()
	s.mu.Unlock()
	intent := ledger.FinalizationIntended{IntentID: intentID, RequestID: requestID, GenerationID: g.GenerationID, PredecessorWitnessSeq: int64(witness.sequence), PredecessorCommit: commit, PredecessorTree: tree, TargetCommit: g.DerivedCommit, TargetTree: g.DerivedTree, FinalizationRules: "finalization-rules/1"}
	accepted := ledger.PromotionAccepted{IntentID: intentID, RequestID: requestID, GenerationID: g.GenerationID, PredecessorWitnessSeq: intent.PredecessorWitnessSeq, PredecessorCommit: commit, PredecessorTree: tree, AcceptedCommit: g.DerivedCommit, AcceptedTree: g.DerivedTree, ChangedPaths: paths}
	payload, err := accepted.Encode()
	if err != nil {
		return r.state, err
	}
	unpin, err := unpinDraft(r.admission.PinID, r.admission.ContextID)
	if err != nil {
		return r.state, err
	}
	drafts := []ledger.Draft{{Type: ledger.TypePromotionAccepted, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r.admission.ContextID, Payload: payload}, unpin}
	// The same bounded encoder measures record framing and the complete commit.
	if err := preflightObservation(s.store.Colony(), intentID, drafts); err != nil {
		return r.state, err
	}
	payload, err = intent.Encode()
	if err != nil {
		return r.state, err
	}
	s.finalizeStep("intent")
	if err := s.append(ctx, []ledger.Draft{{Type: ledger.TypeFinalizationIntended, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r.admission.ContextID, Payload: payload}}); err != nil {
		return r.state, err
	}
	s.finalizeStep("cas")
	effect, effectErr := s.guardedMainUpdate(ctx, w, commit, g.DerivedCommit, g.DerivedTree)
	if effectErr != nil || effect == mainUpdateThird {
		return r.state, &UnavailableError{Reason: "finalization_pending", Detail: fmt.Sprint(effectErr)}
	}
	if effect == mainUpdateRefused {
		// Include any successor admitted during the CAS in the same ending.
		s.mu.Lock()
		drafts, ended, e := s.endingDrafts(s.requests[requestID], ledger.FailedInterrupted, ledger.ActorCore, r.admission.ContextID, uuid.UUID{})
		if e == nil {
			released, e = s.finishFinalizationLocked(ctx, w.ID, drafts, ended)
		}
		s.mu.Unlock()
		if e == nil {
			s.finalizeStep("release")
		}
		return r.state, e
	} else {
		s.finalizeStep("durability")
		if err := s.hardenCanonicalStorage(ctx, w, g.DerivedCommit, g.DerivedTree); err != nil {
			return r.state, &UnavailableError{Reason: "finalization_pending", Detail: err.Error()}
		}
		s.finalizeStep("acceptance")
	}
	released, err = s.finishFinalization(ctx, w.ID, drafts, []uuid.UUID{requestID})
	return r.state, err
}

func (s *Service) noChangeLocked(ctx context.Context, r requestState, witness acceptedWitness) (map[string]retainedRef, error) {
	s.finalizeStep("no_change")
	g := r.generation
	p := ledger.PromotionNoChange{RequestID: r.admission.RequestID, GenerationID: g.GenerationID, PredecessorWitnessSeq: int64(witness.sequence), PredecessorCommit: g.PredecessorCommit, PredecessorTree: g.PredecessorTree}
	payload, err := p.Encode()
	if err != nil {
		return nil, err
	}
	unpin, err := unpinDraft(r.admission.PinID, r.admission.ContextID)
	if err != nil {
		return nil, err
	}
	drafts := []ledger.Draft{{Type: ledger.TypePromotionNoChange, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r.admission.ContextID, Payload: payload}, unpin}
	return s.finishFinalization(ctx, r.admission.WorkspaceID, drafts, []uuid.UUID{r.admission.RequestID})
}

func (s *Service) finishFinalization(ctx context.Context, workspaceID uuid.UUID, drafts []ledger.Draft, ended []uuid.UUID) (map[string]retainedRef, error) {
	s.mu.Lock()
	released, err := s.finishFinalizationLocked(ctx, workspaceID, drafts, ended)
	s.mu.Unlock()
	if err == nil {
		s.finalizeStep("release")
	}
	return released, err
}

func (s *Service) finishFinalizationLocked(ctx context.Context, workspaceID uuid.UUID, drafts []ledger.Draft, ended []uuid.UUID) (map[string]retainedRef, error) {
	before := s.retainedRefs(workspaceID)
	if err := s.appendLocked(ctx, drafts); err != nil {
		return nil, err
	}
	for _, id := range ended {
		s.pool.freeSlot(s.scratches[s.requests[id].admission.ScratchID].Dir)
	}
	after := s.retainedRefs(workspaceID)
	for ref := range after {
		delete(before, ref)
	}
	return before, nil
}

// applyFinalizationRecord checks frozen (D123) record facts, never Git or bodies.
func (s *Service) applyFinalizationRecord(drafts []ledger.Draft, at int, seq uint64, rules *requestCommitRules, journal *[]func()) error {
	d := drafts[at]
	var requestID, generationID uuid.UUID
	var witnessSeq int64
	var predecessorCommit, predecessorTree string
	var intent ledger.FinalizationIntended
	var accepted ledger.PromotionAccepted
	switch d.Type {
	case ledger.TypeFinalizationIntended:
		p, err := ledger.DecodeFinalizationIntended(d.Payload)
		if err != nil {
			return err
		}
		intent = p
		requestID, generationID, witnessSeq, predecessorCommit, predecessorTree = p.RequestID, p.GenerationID, p.PredecessorWitnessSeq, p.PredecessorCommit, p.PredecessorTree
	case ledger.TypePromotionAccepted:
		p, err := ledger.DecodePromotionAccepted(d.Payload)
		if err != nil {
			return err
		}
		accepted = p
		requestID, generationID, witnessSeq, predecessorCommit, predecessorTree = p.RequestID, p.GenerationID, p.PredecessorWitnessSeq, p.PredecessorCommit, p.PredecessorTree
	case ledger.TypePromotionNoChange:
		p, err := ledger.DecodePromotionNoChange(d.Payload)
		if err != nil {
			return err
		}
		requestID, generationID, witnessSeq, predecessorCommit, predecessorTree = p.RequestID, p.GenerationID, p.PredecessorWitnessSeq, p.PredecessorCommit, p.PredecessorTree
	default:
		return nil
	}
	old, exists := s.requests[requestID]
	if !exists || old.ending != "" || d.Actor != ledger.ActorCore || d.ContextID != old.admission.ContextID || len(d.BodyRefs) != 0 {
		return errors.New("finalization requires a live request, core, request context and no body refs")
	}
	a, g := old.admission, old.generation
	train := s.trains[a.WorkspaceID]
	if len(train) == 0 || train[0] != requestID || g == nil || g.Outcome != ledger.GenerationClean || g.GenerationID != generationID || g.PredecessorCommit != predecessorCommit || g.PredecessorTree != predecessorTree {
		return errors.New("finalization does not bind the leading current clean generation")
	}
	if parent := a.ParentRequestID; parent != (uuid.UUID{}) && !s.requests[parent].successful() {
		return errors.New("finalization local parent has not succeeded")
	}
	if !s.witnessMatches(a.WorkspaceID, witnessSeq, predecessorCommit, predecessorTree) {
		return errors.New("finalization witness is absent, stale or differs from the predecessor")
	}
	if g.PredecessorRequestID != (uuid.UUID{}) {
		witness := s.witnesses[a.WorkspaceID].record
		if witness.RequestID != g.PredecessorRequestID || witness.GenerationID != g.PredecessorGenerationID {
			return errors.New("speculative predecessor has not become the current accepted witness")
		}
	}
	n := oidLength(s.workspaces[a.WorkspaceID].ObjectFormat)
	if !validOID(predecessorCommit, n) || !validOID(predecessorTree, n) {
		return errors.New("finalization Git ID length")
	}
	pending, unresolved := s.intents[a.WorkspaceID]
	next := old
	switch d.Type {
	case ledger.TypeFinalizationIntended:
		if unresolved || old.intent != nil || s.usedIntents[intent.IntentID] || intent.TargetCommit != g.DerivedCommit || intent.TargetTree != g.DerivedTree {
			return errors.New("intent repeats, overlaps or differs from generation")
		}
		next.intent = &intent
		next.state.State = RequestFinalizing
		s.requests[requestID] = next
		s.intents[a.WorkspaceID] = intent
		s.usedIntents[intent.IntentID] = true
		*journal = append(*journal, func() {
			s.requests[requestID] = old
			delete(s.intents, a.WorkspaceID)
			delete(s.usedIntents, intent.IntentID)
		})
		return nil
	case ledger.TypePromotionAccepted:
		if !unresolved || pending.IntentID != accepted.IntentID || pending.RequestID != requestID || pending.GenerationID != generationID || pending.PredecessorWitnessSeq != witnessSeq || pending.PredecessorCommit != predecessorCommit || pending.PredecessorTree != predecessorTree || pending.TargetCommit != accepted.AcceptedCommit || pending.TargetTree != accepted.AcceptedTree {
			return errors.New("acceptance differs from unresolved intent")
		}
		oldWitness, hadWitness := s.witnesses[a.WorkspaceID]
		s.witnesses[a.WorkspaceID] = acceptedWitness{seq, accepted}
		delete(s.intents, a.WorkspaceID)
		*journal = append(*journal, func() {
			s.intents[a.WorkspaceID] = pending
			if hadWitness {
				s.witnesses[a.WorkspaceID] = oldWitness
			} else {
				delete(s.witnesses, a.WorkspaceID)
			}
		})
		next.state.State = RequestAccepted
	case ledger.TypePromotionNoChange:
		if unresolved || old.intent != nil || g.DerivedTree != predecessorTree {
			return errors.New("no-change has an intent or unequal trees")
		}
		next.state.State = RequestNoChange
	}
	if err := requireUnpins(drafts, at, []uuid.UUID{a.PinID}); err != nil {
		return err
	}
	next.ending, next.generation = d.Type, nil
	s.requests[requestID] = next
	s.trains[a.WorkspaceID] = append([]uuid.UUID(nil), train[1:]...)
	scratch := s.scratches[a.ScratchID]
	nextScratch := scratch
	nextScratch.live--
	nextScratch.representedRequest, nextScratch.representedTree = requestID, a.SealedTree
	s.scratches[a.ScratchID] = nextScratch
	delete(s.manifestPins, a.PinID)
	*journal = append(*journal, func() {
		s.requests[requestID] = old
		s.trains[a.WorkspaceID] = train
		s.scratches[a.ScratchID] = scratch
		s.manifestPins[a.PinID] = requestID
	})
	rules.unpins[a.PinID] = at
	return nil
}

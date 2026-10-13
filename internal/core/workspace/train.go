package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// integrate has no product caller before C4. Its per-workspace exclusion must
// also cover later finalization and cancellation; admissions may append behind it.
func (s *Service) integrate(ctx context.Context, workspaceID uuid.UUID) error {
	if err := s.start(); err != nil {
		return err
	}
	defer s.ops.Done()
	if !validID(workspaceID) {
		return ErrInvalid
	}
	for {
		s.mu.Lock()
		if err := s.usableLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
		if _, ok := s.workspaces[workspaceID]; !ok {
			s.mu.Unlock()
			return ErrUnknownWorkspace
		}
		waiting := s.integrating[workspaceID]
		if waiting == nil {
			s.integrating[workspaceID] = make(chan struct{})
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waiting:
		}
	}
	defer func() {
		s.mu.Lock()
		close(s.integrating[workspaceID])
		delete(s.integrating, workspaceID)
		s.mu.Unlock()
	}()
	for {
		s.mu.Lock()
		w := s.workspaces[workspaceID]
		train := slices.Clone(s.trains[workspaceID])
		err := s.usableLocked()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if err := s.integrationReady(ctx, w); err != nil {
			return err
		}
		if s.pool.profile == nil {
			return &UnavailableError{Reason: "no_capacity_profile"}
		}
		commit, tree, err := s.mainTip(ctx, w)
		if err != nil {
			return err
		}
		var previousRequest, previousGeneration uuid.UUID
		restart := false
		for position, id := range train[:min(len(train), s.pool.profile.GenerationWindow)] {
			s.mu.Lock()
			r := s.requests[id]
			scratch := s.scratches[r.admission.ScratchID].Scratch
			s.mu.Unlock()
			g := r.generation
			if g == nil || g.PredecessorCommit != commit {
				derived, err := s.derive(ctx, w, scratch, r, previousRequest, previousGeneration, commit, tree)
				if err != nil {
					return err
				}
				if err := s.recordGeneration(ctx, w, r, &derived, position == 0); err != nil {
					return err
				}
				// Re-read the train and main after every acknowledged derivation.
				restart = true
				break
			}
			if g.Outcome == ledger.GenerationConflict {
				if position != 0 {
					return nil
				}
				if err := s.recordGeneration(ctx, w, r, nil, true); err != nil {
					return err
				}
				restart = true
				break
			}
			previousRequest, previousGeneration = id, g.GenerationID
			commit, tree = g.DerivedCommit, g.DerivedTree
		}
		if !restart {
			return nil
		}
	}
}

func (s *Service) integrationReady(ctx context.Context, w workspaceState) error {
	if s.git.unavailable != nil {
		return &UnavailableError{Reason: "git_unavailable", Detail: s.git.unavailable.Error()}
	}
	if w.inputsError != "" {
		return &UnavailableError{Reason: w.inputsError, Detail: w.inputsDetail}
	}
	if err := s.git.checkInputsVersion(w.inputs.GitVersion); err != nil {
		return err
	}
	return s.workspaceReady(ctx, w)
}

func (s *Service) mainTip(ctx context.Context, w workspaceState) (string, string, error) {
	var ids [2]string
	for i, kind := range []string{"commit", "tree"} {
		out, exit, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0, 128}, "rev-parse", "--verify", "--end-of-options", "refs/heads/main^{"+kind+"}")
		if err != nil {
			return "", "", err
		}
		if exit != 0 {
			return "", "", ErrNoCanonical
		}
		ids[i] = strings.TrimSuffix(string(out), "\n")
		if !validOID(ids[i], oidLength(w.ObjectFormat)) {
			return "", "", fmt.Errorf("%w: invalid canonical object ID", ErrGit)
		}
	}
	return ids[0], ids[1], nil
}

func (s *Service) derive(ctx context.Context, w workspaceState, scratch Scratch, r requestState, previousRequest, previousGeneration uuid.UUID, commit, tree string) (ledger.GenerationDerived, error) {
	s.mu.Lock()
	id := s.deps.newID()
	s.mu.Unlock()
	g := ledger.GenerationDerived{GenerationID: id, RequestID: r.admission.RequestID, PredecessorRequestID: previousRequest, PredecessorGenerationID: previousGeneration, PredecessorCommit: commit, PredecessorTree: tree, InputsRevision: w.Revision, IntegrationRules: "integration-rules/1"}
	if r.generation != nil {
		g.Supersedes = r.generation.GenerationID
	}
	cs := s.captureScratch(w, scratch)
	merged, err := s.engine.mergeTrees(ctx, cs, r.admission.DeltaBaseTree, tree, r.admission.SealedTree)
	if err != nil {
		return g, err
	}
	if merged.Cause == "" {
		g.Outcome, g.DerivedTree = ledger.GenerationClean, merged.Tree
		g.DerivedCommit, err = s.engine.integrationCommit(ctx, cs, merged.Tree, commit, r.admission.RequestID, id)
		if err != nil {
			return g, err
		}
		err = s.git.createGenerationRef(ctx, s.colony, w.inputs, id, g.DerivedCommit)
		return g, err
	}
	body, digest, err := conflictReport(merged.Cause, merged.Paths)
	if err != nil {
		return g, err
	}
	published, _, err := s.store.PublishBody(ctx, bytes.NewReader(body))
	if err != nil {
		return g, err
	}
	if published != ledger.Digest(digest) {
		return g, fmt.Errorf("%w: published conflict digest differs", ErrInvalid)
	}
	g.Outcome, g.ConflictSHA256 = ledger.GenerationConflict, published
	s.mu.Lock()
	g.ReportPinID = s.deps.newID()
	s.mu.Unlock()
	return g, nil
}

func pinDraft(pin uuid.UUID, digest ledger.Digest, contextID uuid.UUID) (ledger.Draft, error) {
	payload, err := (ledger.BodyPin{PinID: pin}).Encode()
	return ledger.Draft{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, BodyRefs: []ledger.Digest{digest}, Payload: payload}, err
}

func unpinDraft(pin, contextID uuid.UUID) (ledger.Draft, error) {
	payload, err := (ledger.BodyUnpin{PinID: pin}).Encode()
	return ledger.Draft{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, Payload: payload}, err
}

func (s *Service) endingDrafts(r requestState, generation uuid.UUID) ([]ledger.Draft, []uuid.UUID, error) {
	ended := []uuid.UUID{r.admission.RequestID}
	for _, id := range s.trains[r.admission.WorkspaceID] {
		a := s.requests[id].admission
		if a.LineageID == r.admission.LineageID && a.Checkpoint > r.admission.Checkpoint {
			ended = append(ended, id)
		}
	}
	var drafts []ledger.Draft
	for i, id := range ended {
		p := ledger.PromotionFailed{RequestID: id, Outcome: ledger.FailedConflict, GenerationID: generation}
		if i != 0 {
			p.Outcome, p.GenerationID, p.FailedRequestID = ledger.FailedDependencyFailed, uuid.UUID{}, r.admission.RequestID
		}
		payload, err := p.Encode()
		if err != nil {
			return nil, nil, err
		}
		drafts = append(drafts, ledger.Draft{Type: ledger.TypePromotionFailed, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r.admission.ContextID, Payload: payload})
	}
	for _, id := range ended {
		p := s.requests[id].admission
		d, err := unpinDraft(p.PinID, p.ContextID)
		if err != nil {
			return nil, nil, err
		}
		drafts = append(drafts, d)
	}
	return drafts, ended, nil
}

func (s *Service) recordGeneration(ctx context.Context, w workspaceState, r requestState, derived *ledger.GenerationDerived, leading bool) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, ErrPoisoned) && derived != nil && derived.Outcome == ledger.GenerationClean {
			if cleanupErr := s.git.deleteGenerationRef(context.WithoutCancel(ctx), s.colony, w.inputs, derived.GenerationID, derived.DerivedCommit); cleanupErr != nil {
				err = fmt.Errorf("%w; generation release: %v", err, cleanupErr)
			}
		}
	}()
	var drafts []ledger.Draft
	g := r.generation
	if derived != nil {
		g = derived
		payload, e := g.Encode()
		if e != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, e)
		}
		d := ledger.Draft{Type: ledger.TypeGenerationDerived, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: r.admission.ContextID, Payload: payload}
		if g.Outcome == ledger.GenerationConflict {
			d.BodyRefs = []ledger.Digest{g.ConflictSHA256}
		}
		drafts = append(drafts, d)
		if g.Outcome == ledger.GenerationConflict {
			pin, e := pinDraft(g.ReportPinID, g.ConflictSHA256, r.admission.ContextID)
			if e != nil {
				return e
			}
			drafts = append(drafts, pin)
		}
		if r.generation != nil && r.generation.ReportPinID != (uuid.UUID{}) {
			unpin, e := unpinDraft(r.generation.ReportPinID, r.admission.ContextID)
			if e != nil {
				return e
			}
			drafts = append(drafts, unpin)
		}
	}
	if s.deps.beforeAppend != nil {
		s.deps.beforeAppend()
	}
	s.mu.Lock()
	var ended []uuid.UUID
	if leading && g.Outcome == ledger.GenerationConflict {
		endings, ids, e := s.endingDrafts(r, g.GenerationID)
		if e != nil {
			s.mu.Unlock()
			return e
		}
		drafts, ended = append(drafts, endings...), ids
	}
	before := s.retainedRefs(w.ID)
	err = s.appendLocked(ctx, drafts)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	for _, id := range ended {
		s.pool.freeSlot(s.scratches[s.requests[id].admission.ScratchID].Dir)
	}
	after := s.retainedRefs(w.ID)
	s.mu.Unlock()
	for ref, retained := range before {
		if _, keep := after[ref]; !keep {
			// Failed post-acknowledgement releases belong to Open, not rollback.
			_, _, _ = s.git.repository(context.WithoutCancel(ctx), s.colony, w.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, retained.object)
		}
	}
	return nil
}

type retainedRef struct{ object, missing string }

// retainedRefs is a projection query, not a second ownership index. Hold mu.
func (s *Service) retainedRefs(workspaceID uuid.UUID) map[string]retainedRef {
	refs := make(map[string]retainedRef)
	// C2 has only failure endings, so every unfailed open head is live.
	// Ticket 08 adds retention of an unfailed open head after success.
	for _, id := range s.trains[workspaceID] {
		r := s.requests[id]
		a := r.admission
		refs["refs/dwarf-harness/captures/"+a.CaptureID.String()] = retainedRef{a.SealedTree, "capture_missing " + id.String()}
		if a.ParentRequestID != (uuid.UUID{}) {
			parent := s.requests[a.ParentRequestID].admission
			refs["refs/dwarf-harness/captures/"+parent.CaptureID.String()] = retainedRef{parent.SealedTree, "capture_missing " + a.ParentRequestID.String()}
		}
		if r.generation != nil && r.generation.Outcome == ledger.GenerationClean {
			g := r.generation
			refs["refs/dwarf-harness/generations/"+g.GenerationID.String()] = retainedRef{g.DerivedCommit, "generation_missing " + g.GenerationID.String()}
		}
	}
	return refs
}

// Open lists each namespace once. Never overwrite a retained ref that differs;
// every removal and restoration uses CAS and writes no ledger record.
func (s *Service) sweepRequestRefs(ctx context.Context, w workspaceState, report *Reconciliation) {
	retained := s.retainedRefs(w.ID)
	for _, prefix := range []string{"refs/dwarf-harness/captures/", "refs/dwarf-harness/generations/"} {
		refs, err := s.listRefs(ctx, w, prefix)
		if err != nil {
			report.Pending = append(report.Pending, fmt.Sprintf("%s: %v", prefix, err))
			continue
		}
		for ref, object := range refs {
			if _, keep := retained[ref]; keep {
				continue
			}
			if _, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", ref, object); err != nil {
				report.Pending = append(report.Pending, fmt.Sprintf("%s: %v", ref, err))
			}
		}
		for ref, want := range retained {
			if !strings.HasPrefix(ref, prefix) || refs[ref] == want.object {
				continue
			}
			if refs[ref] != "" {
				report.Pending = append(report.Pending, want.missing+": ref differs from recorded object")
				continue
			}
			if err := s.git.createRef(ctx, s.colony, w.CommonDir, ref, want.object); err != nil {
				report.Pending = append(report.Pending, fmt.Sprintf("%s: %v", want.missing, err))
			}
		}
	}
}

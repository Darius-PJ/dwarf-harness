package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// admit is reachable only from package tests until C4 reserves real validation.
// Its caller owns a service operation; admission owns the reservation on entry.
func (s *Service) admit(ctx context.Context, plan requestPlan, token *reservation) (state RequestState, err error) {
	defer token.release()
	r := plan.request
	s.mu.Lock()
	if err = s.usableLocked(); err != nil {
		s.mu.Unlock()
		return state, err
	}
	if previous, found, identityErr := s.identity(r); found {
		s.mu.Unlock()
		return previous, identityErr
	}
	s.inFlight[r.RequestID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inFlight, r.RequestID)
		s.mu.Unlock()
	}()
	sealed, err := s.engine.capture(ctx, s.captureScratch(plan.workspace, plan.scratch), plan.baseline, token, sealCapture)
	if err != nil {
		return state, err
	}
	defer func() {
		if err != nil && !errors.Is(err, ErrPoisoned) {
			_, _, cleanupErr := s.git.repository(context.WithoutCancel(ctx), s.colony, plan.workspace.CommonDir, nil, []int{0}, "update-ref", "-d", "--end-of-options", sealed.RetentionRef, sealed.Tree)
			if cleanupErr != nil {
				err = fmt.Errorf("%w; capture release: %v", err, cleanupErr)
			}
		}
	}()
	digest, _, err := s.store.PublishBody(ctx, bytes.NewReader(sealed.Manifest))
	if err != nil {
		return state, err
	}
	if digest != ledger.Digest(sealed.ManifestSHA256) {
		return state, fmt.Errorf("%w: published manifest digest differs", ErrInvalid)
	}
	s.mu.Lock()
	lineage := plan.lineage
	if lineage == (uuid.UUID{}) {
		lineage = s.deps.newID()
	}
	pin := s.deps.newID()
	s.mu.Unlock()
	p := ledger.PromotionAdmitted{
		RequestID: r.RequestID, WorkspaceID: r.WorkspaceID, ScratchID: plan.scratch.ID, ContextID: r.ContextID,
		LineageID: lineage, Checkpoint: plan.checkpoint, ParentRequestID: r.Head, RestartFrom: r.RestartFrom,
		ScratchBase: plan.scratch.BaseCommit, BaselineTree: plan.baseline, DeltaBaseTree: plan.baseline,
		CaptureID: sealed.ID, SealedTree: sealed.Tree, ManifestSHA256: digest,
		InputsRevision: int64(sealed.InputsRevision), CaptureInputsSHA256: ledger.Digest(sealed.InputsSHA256), PinID: pin,
	}
	payload, err := p.Encode()
	if err != nil {
		return state, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	pinPayload, err := (ledger.BodyPin{PinID: pin}).Encode()
	if err != nil {
		return state, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	refs := []ledger.Digest{digest}
	drafts := []ledger.Draft{
		{Type: ledger.TypePromotionAdmitted, TypeVersion: 1, Actor: r.Actor, ContextID: r.ContextID, BodyRefs: refs, Payload: payload},
		{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: r.Actor, ContextID: r.ContextID, BodyRefs: refs, Payload: pinPayload},
	}
	if s.deps.beforeAppend != nil {
		s.deps.beforeAppend()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Integration may have failed the local parent during the seal. Return the
	// public head refusal rather than exposing the record rule's ErrInvalid.
	if _, err = s.headPlan(r, s.scratches[plan.scratch.ID]); err != nil {
		return state, err
	}
	if err = s.appendLocked(ctx, drafts); err != nil {
		return state, err
	}
	token.keepSlot()
	return s.requests[r.RequestID].state, nil
}

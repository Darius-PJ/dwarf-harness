package workspace

import (
	"context"
	"os"
)

// RequestPromotion returns an admitted ID's state, or checks a new request
// through reservation and availability. C2's no_validator is unconditional:
// this call never captures, appends, queues, retries or changes a ref.
func (s *Service) RequestPromotion(ctx context.Context, r Request) (RequestState, error) {
	if err := s.start(); err != nil {
		return RequestState{}, err
	}
	defer s.ops.Done()
	_, token, state, err := s.beginRequest(ctx, r)
	if err != nil || token == nil {
		return state, err
	}
	defer token.release()
	return RequestState{}, &UnavailableError{Reason: "no_validator"}
}

func (s *Service) workspaceReady(ctx context.Context, w workspaceState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.inputsError != "" {
		return &UnavailableError{Reason: w.inputsError, Detail: w.inputsDetail}
	}
	info, err := os.Stat(w.CommonDir)
	if err != nil || !info.IsDir() {
		return &UnavailableError{Reason: "workspace_unverified"}
	}
	marker, err := s.git.readBlob(ctx, s.colony, w.CommonDir, "refs/dwarf-harness/colony")
	if err != nil && ctx.Err() != nil {
		return err
	}
	if err != nil || string(marker) != "dwarf-harness colony "+s.store.Colony().String()+"\n" {
		return &UnavailableError{Reason: "workspace_unverified"}
	}
	return nil
}

func (s *Service) captureReady(ctx context.Context, w workspaceState, scratch Scratch) error {
	if s.git.unavailable != nil {
		return &UnavailableError{Reason: "git_unavailable", Detail: s.git.unavailable.Error()}
	}
	if w.inputsError != "" {
		return &UnavailableError{Reason: w.inputsError, Detail: w.inputsDetail}
	}
	if err := s.git.checkInputsVersion(w.inputs.GitVersion); err != nil {
		return err
	}
	if err := s.workspaceReady(ctx, w); err != nil {
		return err
	}
	info, err := os.Lstat(scratch.Dir)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return &UnavailableError{Reason: "scratch_missing"}
	}
	anchor, err := s.git.readRef(ctx, s.colony, w.CommonDir, scratch.worktree().AnchorRef, oidLength(w.ObjectFormat))
	if err != nil && ctx.Err() != nil {
		return err
	}
	if err != nil || anchor != scratch.BaseCommit {
		return &UnavailableError{Reason: "anchor_missing"}
	}
	return nil
}

package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

type mainUpdateState uint8

const (
	mainUpdateRefused mainUpdateState = iota
	mainUpdateMoved
	mainUpdateThird
)

// guardedMainUpdate is record-free and has no product caller. Its caller holds
// lockTrain. A moved ref is only an observed effect, never durable acceptance.
func (s *Service) guardedMainUpdate(ctx context.Context, w workspaceState, old, target, targetTree string) (mainUpdateState, error) {
	n := oidLength(w.ObjectFormat)
	if !validOID(old, n) || !validOID(target, n) || !validOID(targetTree, n) {
		return mainUpdateRefused, ErrInvalid
	}
	backend, _, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0}, "rev-parse", "--show-ref-format")
	if err != nil {
		return mainUpdateRefused, err
	}
	if string(backend) != "files\n" {
		return mainUpdateRefused, &UnavailableError{Reason: "ref_backend_unavailable"}
	}
	_, exit, err := s.git.repository(ctx, s.colony, w.CommonDir, nil, []int{0, 1}, "symbolic-ref", "-q", "--end-of-options", "refs/heads/main")
	if err != nil {
		return mainUpdateRefused, err
	}
	if exit != 1 {
		return mainUpdateRefused, fmt.Errorf("%w: canonical main is symbolic", ErrGit)
	}
	entries, err := s.git.listWorktrees(ctx, s.colony, w.CommonDir, n)
	if err != nil {
		return mainUpdateRefused, err
	}
	if err := canonicalNotCheckedOut(entries); err != nil {
		return mainUpdateRefused, err
	}
	for _, check := range []struct{ suffix, want string }{{"^{tree}", targetTree}, {"^1", old}, {"^2", ""}} {
		got, err := s.git.readRef(ctx, s.colony, w.CommonDir, target+check.suffix, n)
		if err != nil {
			return mainUpdateRefused, err
		}
		if got != check.want {
			return mainUpdateRefused, fmt.Errorf("%w: canonical target %s differs", ErrGit, check.suffix)
		}
	}
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

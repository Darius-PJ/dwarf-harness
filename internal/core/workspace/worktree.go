package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type scratchWorktree struct{ Dir, Branch, BaseCommit, AnchorTree, AnchorRef string }

func (g *gitRunner) listWorktrees(ctx context.Context, colony, common string, n int) ([]worktreeEntry, error) {
	b, _, err := g.repository(ctx, colony, common, nil, []int{0}, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	entries, err := parseWorktreeList(b, n)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGit, err)
	}
	return entries, nil
}

func (g *gitRunner) readRef(ctx context.Context, colony, common, ref string, n int) (string, error) {
	b, exit, err := g.repository(ctx, colony, common, nil, []int{0, 1}, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err != nil {
		return "", err
	}
	if exit == 1 {
		return "", nil
	}
	oid := strings.TrimSuffix(string(b), "\n")
	if !validOID(oid, n) {
		return "", fmt.Errorf("%w: invalid object ID", ErrGit)
	}
	return oid, nil
}

func (g *gitRunner) createRef(ctx context.Context, colony, common, ref, oid string) error {
	if !validOID(oid, len(oid)) {
		return fmt.Errorf("%w: invalid ref object", ErrInvalid)
	}
	_, _, err := g.repository(ctx, colony, common, nil, []int{0}, "update-ref", "--end-of-options", ref, oid, strings.Repeat("0", len(oid)))
	return err
}

func (g *gitRunner) readBlob(ctx context.Context, colony, common, ref string) ([]byte, error) {
	b, _, err := g.repository(ctx, colony, common, nil, []int{0}, "cat-file", "--end-of-options", "blob", ref)
	return b, err
}

func (g *gitRunner) ensureMarker(ctx context.Context, colony string, p Proposal, colonyID string) error {
	const ref = "refs/dwarf-harness/colony"
	expected := []byte("dwarf-harness colony " + colonyID + "\n")
	check := func() (bool, error) {
		oid, err := g.readRef(ctx, colony, p.CommonDir, ref, oidLength(p.ObjectFormat))
		if err != nil || oid == "" {
			return false, err
		}
		b, err := g.readBlob(ctx, colony, p.CommonDir, oid)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(b, expected) {
			return false, ErrOtherColony
		}
		return true, nil
	}
	present, err := check()
	if err != nil || present {
		return err
	}
	b, _, err := g.repository(ctx, colony, p.CommonDir, bytes.NewReader(expected), []int{0}, "hash-object", "-w", "--stdin", "--no-filters")
	if err != nil {
		return err
	}
	oid := strings.TrimSuffix(string(b), "\n")
	if !validOID(oid, oidLength(p.ObjectFormat)) {
		return fmt.Errorf("%w: invalid marker object", ErrGit)
	}
	if err := g.createRef(ctx, colony, p.CommonDir, ref, oid); err != nil {
		present, readErr := check()
		if readErr != nil {
			return readErr
		}
		if !present {
			return err
		}
	}
	return nil
}

func (g *gitRunner) createWorktree(ctx context.Context, colony string, p Proposal, inputs, dir, id string) (scratchWorktree, error) {
	var zero scratchWorktree
	if g.unavailable != nil {
		return zero, g.unavailable
	}
	if err := g.checkInputsVersion(p.GitVersion); err != nil {
		return zero, err
	}
	var err error
	dir, err = filepath.Abs(dir)
	if err != nil {
		return zero, err
	}
	name := filepath.Base(dir)
	if len(name) != 8 || strings.Trim(name, "0123456789abcdef") != "" || id == "" || strings.ContainsAny(id, " /\\\x00\r\n\t") {
		return zero, fmt.Errorf("%w: invalid scratch name", ErrInvalid)
	}
	if _, err := os.Lstat(dir); err == nil {
		return zero, fmt.Errorf("%w: scratch directory exists", ErrInvalid)
	} else if !os.IsNotExist(err) {
		return zero, err
	}
	entries, err := g.listWorktrees(ctx, colony, p.CommonDir, oidLength(p.ObjectFormat))
	if err != nil {
		return zero, err
	}
	if err := canonicalNotCheckedOut(entries); err != nil {
		return zero, err
	}
	base, err := g.readRef(ctx, colony, p.CommonDir, "refs/heads/main^{commit}", oidLength(p.ObjectFormat))
	if err != nil {
		return zero, err
	}
	if base == "" {
		return zero, ErrNoCanonical
	}
	tree, err := g.readRef(ctx, colony, p.CommonDir, base+"^{tree}", oidLength(p.ObjectFormat))
	if err != nil {
		return zero, err
	}
	if tree == "" {
		return zero, fmt.Errorf("%w: missing anchor tree", ErrGit)
	}
	names, _, err := g.repository(ctx, colony, p.CommonDir, nil, []int{0}, "ls-tree", "-r", "-z", "--name-only", "--end-of-options", tree)
	if err != nil {
		return zero, err
	}
	paths, err := parseNULList(names)
	if err != nil {
		return zero, fmt.Errorf("%w: baseline paths: %v", ErrGit, err)
	}
	if err := checkCaseCollisions(paths); err != nil {
		return zero, err
	}
	s := scratchWorktree{Dir: dir, Branch: "refs/heads/dwarf-harness/" + name, BaseCommit: base, AnchorTree: tree, AnchorRef: "refs/dwarf-harness/anchors/" + id}
	old, err := g.readRef(ctx, colony, p.CommonDir, s.Branch, oidLength(p.ObjectFormat))
	if err != nil {
		return zero, err
	}
	if old != "" {
		return zero, fmt.Errorf("%w: scratch branch exists", ErrInvalid)
	}
	if err := materializeInputs(colony, inputs, p); err != nil {
		return zero, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return zero, err
	}
	if err := g.createRef(ctx, colony, p.CommonDir, s.AnchorRef, base); err != nil {
		return zero, err
	}
	rollback := func(cause error) (scratchWorktree, error) {
		if err := g.removeWorktree(ctx, colony, p.CommonDir, s, nil); err != nil {
			return zero, fmt.Errorf("%w; cleanup: %v", cause, err)
		}
		return zero, cause
	}
	_, _, err = g.repository(ctx, colony, p.CommonDir, nil, []int{0}, "worktree", "add", "--no-checkout", "-b", strings.TrimPrefix(s.Branch, "refs/heads/"), "--end-of-options", dir, base)
	if err != nil {
		return rollback(err)
	}
	gitfile, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return rollback(err)
	}
	admin := strings.TrimSuffix(string(gitfile), "\n")
	if !strings.HasPrefix(admin, "gitdir: ") {
		return rollback(fmt.Errorf("%w: invalid worktree gitfile", ErrGit))
	}
	admin = strings.TrimPrefix(admin, "gitdir: ")
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(dir, admin)
	}
	env := captureEnvironment(g.environ(), colony, inputs, dir, filepath.Join(admin, "index"), p.CommonDir)
	_, _, err = g.run(ctx, dir, env, nil, []int{0}, "read-tree", "-u", "--reset", "--end-of-options", tree)
	if err != nil {
		return rollback(err)
	}
	return s, nil
}

// removeWorktree is used only after a durable closure or for a never-exposed
// orphan. The service, not this record-free mechanic, proves representability.
func (g *gitRunner) removeWorktree(ctx context.Context, colony, common string, s scratchWorktree, removeAll func(string) error) error {
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	if err := removeScratchDirectory(s.Dir, removeAll); err != nil {
		return fmt.Errorf("%w: %v", ErrRemovalPending, err)
	}
	if _, _, err := g.repository(ctx, colony, common, nil, []int{0}, "worktree", "prune", "--expire=now"); err != nil {
		return fmt.Errorf("%w: %v", ErrRemovalPending, err)
	}
	current, err := g.readRef(ctx, colony, common, s.Branch, len(s.BaseCommit))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRemovalPending, err)
	}
	if current == s.BaseCommit {
		if _, _, err := g.repository(ctx, colony, common, nil, []int{0}, "update-ref", "-d", "--end-of-options", s.Branch, s.BaseCommit); err != nil {
			return fmt.Errorf("%w: %v", ErrRemovalPending, err)
		}
	}
	if s.AnchorRef != "" {
		anchor, err := g.readRef(ctx, colony, common, s.AnchorRef, len(s.BaseCommit))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRemovalPending, err)
		}
		if anchor != "" {
			if g.beforeAnchorDelete != nil {
				g.beforeAnchorDelete()
			}
			if _, _, err := g.repository(ctx, colony, common, nil, []int{0}, "update-ref", "-d", "--end-of-options", s.AnchorRef, anchor); err != nil {
				return fmt.Errorf("%w: %v", ErrRemovalPending, err)
			}
		}
	}
	return nil
}

// removeScratchDirectory never descends through a link or reparse point. Go
// reports Windows junctions and mount points as ModeIrregular. Files are
// unlinked during the walk; RemoveAll sees only real, empty directories.
func removeScratchDirectory(root string, removeAll func(string) error) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return os.Remove(root)
	}
	var unlink func(string) error
	var rootGit string
	unlink = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			kind := entry.Type()
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			kind |= info.Mode().Type()
			if dir == root && entry.Name() == ".git" && info.Mode().IsRegular() {
				rootGit = path
				continue
			}
			if kind&(os.ModeSymlink|os.ModeIrregular) != 0 || !info.IsDir() {
				if err := os.Remove(path); err != nil {
					return err
				}
				continue
			}
			if err := unlink(path); err != nil {
				return err
			}
			if err := removeAll(path); err != nil {
				return err
			}
		}
		return nil
	}
	if err := unlink(root); err != nil {
		return err
	}
	// Preserve the workspace link while another entry prevents removal.
	if rootGit != "" {
		if err := os.Remove(rootGit); err != nil {
			return err
		}
	}
	return removeAll(root)
}

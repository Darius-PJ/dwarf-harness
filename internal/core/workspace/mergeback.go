package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"uuid"
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
		return refuse("scratch_tip_mismatch", "", "unexpected private branch")
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

package validation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func regularPath(root, relative string, makeParents bool) (string, error) {
	if relative == "" || !ledger.ValidResourcePath(relative) {
		return "", ledger.ErrInvalid
	}
	parts := strings.Split(relative, "/")
	path := root
	for i, component := range parts {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && i < len(parts)-1 && makeParents {
			if err = os.Mkdir(path, 0700); err != nil {
				return "", err
			}
			info, err = os.Lstat(path)
		}
		if errors.Is(err, os.ErrNotExist) && i == len(parts)-1 {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if info.Name() != component {
			return "", fmt.Errorf("%w: path case differs from pinned input", ledger.ErrInvalid)
		}
		if i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return "", fmt.Errorf("%w: nonregular input", ledger.ErrInvalid)
		}
	}
	return path, nil
}

func fileDigest(ctx context.Context, path string, limit int64) (ledger.Digest, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return ledger.Digest{}, 0, err
	}
	defer file.Close()
	h := sha256.New()
	var n int64
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return ledger.Digest{}, n, err
		}
		read, err := file.Read(buf)
		if int64(read) > limit-n {
			return ledger.Digest{}, n, ledger.ErrTooLarge
		}
		n += int64(read)
		_, _ = h.Write(buf[:read])
		if err == io.EOF {
			break
		}
		if err != nil {
			return ledger.Digest{}, n, err
		}
	}
	var digest ledger.Digest
	copy(digest[:], h.Sum(nil))
	return digest, n, nil
}

func manifestBytes(files []ledger.ValidationFile) ([]byte, error) {
	b, err := (ledger.FileManifest{Format: ledger.FileManifestFormat, Schema: 1, Files: files}).Encode()
	if len(b) > ledgerstore.MaxBodyBytes {
		return nil, ledger.ErrTooLarge
	}
	return b, err
}

func (r *Runner) manifest(ctx context.Context, digest ledger.Digest) ([]ledger.ValidationFile, error) {
	b, err := r.store.ReadBody(ctx, digest)
	if err != nil {
		return nil, err
	}
	if len(b) > ledgerstore.MaxBodyBytes {
		return nil, ledger.ErrTooLarge
	}
	files := []ledger.ValidationFile{}
	got, err := ledger.LoadFileManifest(io.LimitReader(bytes.NewReader(b), ledgerstore.MaxBodyBytes+1), int64(r.maxFiles), func(f ledger.ValidationFile) error { files = append(files, f); return nil })
	if err != nil {
		return nil, err
	}
	if got != digest {
		return nil, ledger.ErrInvalid
	}
	return files, nil
}

func verifyFiles(ctx context.Context, root string, files []ledger.ValidationFile, maxBytes int64) ([]ledger.ValidationFile, string) {
	actual := make([]ledger.ValidationFile, 0, len(files))
	state := "verified"
	var total int64
	for _, f := range files {
		path, err := regularPath(root, f.Path, false)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return actual, "missing"
			}
			return actual, "unavailable"
		}
		sum, n, err := fileDigest(ctx, path, maxBytes-total)
		if errors.Is(err, os.ErrNotExist) {
			return actual, "missing"
		}
		if err != nil {
			return actual, "unavailable"
		}
		mode := f.Mode
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return actual, "unavailable"
		}
		if runtime.GOOS != "windows" {
			mode = "100644"
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
		}
		actual = append(actual, ledger.ValidationFile{Path: f.Path, Mode: mode, Bytes: n, SHA256: sum})
		total += n
		if n != f.Bytes || sum != f.SHA256 || mode != f.Mode {
			state = "changed"
		}
	}
	return actual, state
}

func verifyTree(ctx context.Context, root string, files []ledger.ValidationFile, maxFiles int) error {
	if !filepath.IsAbs(root) {
		return ledger.ErrInvalid
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return fmt.Errorf("invalid manifest root")
	}
	paths := make([]string, 0, len(files))
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return ledger.ErrInvalid
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || len(paths) >= maxFiles {
			return ledger.ErrInvalid
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(paths)
	if len(paths) != len(files) {
		return ledger.ErrInvalid
	}
	for i, path := range paths {
		if path != files[i].Path {
			return ledger.ErrInvalid
		}
	}
	_, state := verifyFiles(ctx, root, files, 1<<63-1)
	if state != "verified" {
		return fmt.Errorf("manifest content %s", state)
	}
	return nil
}

func (r *Runner) overlay(ctx context.Context, subject string, check ledger.CheckSpec, canary []byte) ([]ledger.ValidationFile, []byte, error) {
	files := slices.Clone(check.DefiningFiles)
	canaryPath := filepath.ToSlash(filepath.Join(check.WorkingDirectory, check.Package, check.Canary.File))
	path, err := regularPath(subject, canaryPath, false)
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("reserved canary path exists")
	}
	files = append(files, ledger.ValidationFile{Path: canaryPath, Mode: "100644", Bytes: int64(len(canary)), SHA256: sha256.Sum256(canary)})
	slices.SortFunc(files, func(a, b ledger.ValidationFile) int { return strings.Compare(a.Path, b.Path) })
	var total int64
	for _, f := range files {
		if f.Bytes > r.limits.MaterializedBytes-total {
			return nil, nil, ledger.ErrTooLarge
		}
		total += f.Bytes
		b := canary
		if f.Path != canaryPath {
			b, err = r.store.ReadBody(ctx, f.SHA256)
			if err != nil {
				return nil, nil, err
			}
		}
		if int64(len(b)) != f.Bytes || sha256.Sum256(b) != f.SHA256 {
			return nil, nil, ledger.ErrInvalid
		}
		path, err := regularPath(subject, f.Path, true)
		if err != nil {
			return nil, nil, err
		}
		if err := os.Chmod(path, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			return nil, nil, err
		}
		mode := fs.FileMode(0444)
		if f.Mode == "100755" {
			mode = 0555
		}
		if err := os.Chmod(path, mode); err != nil {
			return nil, nil, err
		}
	}
	b, err := manifestBytes(files)
	return files, b, err
}

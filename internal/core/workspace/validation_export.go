package workspace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// ValidationExport describes the private copy. It owns no workspace references;
// the caller removes its run root only after positive closure and bookkeeping.
type ValidationExport struct {
	directory string
	manifest  []byte
}

func (e *ValidationExport) Directory() string { return e.directory }
func (e *ValidationExport) Manifest() []byte  { return bytes.Clone(e.manifest) }

type exportEntry struct {
	stageEntry
	size int64
}

// ExportValidationSubject materializes only the opaque descriptor's exact tree.
// root is an exclusively owned, empty run directory outside all scratches.
func (s *Service) ExportValidationSubject(ctx context.Context, subject ValidationSubject, root string, maxBytes int64) (_ *ValidationExport, err error) {
	if subject.service != s || maxBytes < 1 || !filepath.IsAbs(root) {
		return nil, ErrInvalid
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	defer s.ops.Done()
	if err := s.validationCurrent(ctx, subject); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, ErrInvalid
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	children, err := os.ReadDir(root)
	if err != nil || len(children) != 0 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	for _, scratch := range s.scratches {
		rel, relErr := filepath.Rel(scratch.Dir, root)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			s.mu.Unlock()
			return nil, ErrInvalid
		}
	}
	s.mu.Unlock()
	s.pool.mu.Lock()
	profile := s.pool.profile
	maxFiles := 0
	if profile != nil {
		maxFiles = profile.MaxFiles
	}
	s.pool.mu.Unlock()
	if maxFiles < 1 {
		return nil, &UnavailableError{Reason: "capture_unavailable"}
	}
	common, tree := subject.inputs.CommonDir, subject.descriptor.Tree
	private := filepath.Join(root, "export")
	work := filepath.Join(root, "subject")
	handed := false
	defer func() {
		if !handed {
			err = errors.Join(err, os.RemoveAll(private), os.RemoveAll(work))
		}
	}()
	for _, dir := range []string{private, work, filepath.Join(private, "blobs")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	}
	inputsDir := filepath.Join(private, "inputs")
	if err := materializeInputs(s.colony, inputsDir, subject.inputs); err != nil {
		return nil, err
	}
	gitDir := filepath.Join(inputsDir, "git")
	if err := os.Mkdir(filepath.Join(gitDir, "objects"), 0700); err != nil {
		return nil, err
	}
	index := filepath.Join(private, "index")
	env := append(captureEnvironment(s.git.environ(), s.colony, inputsDir, work, index, gitDir), "GIT_NO_LAZY_FETCH=1")
	var entries []exportEntry
	var total int64
	err = s.artifactStream(ctx, work, append(repositoryEnvironment(s.git.environ(), s.colony), "GIT_NO_LAZY_FETCH=1"), common, nil, func(reader io.Reader) error {
		scan := bufio.NewScanner(reader)
		scan.Split(artifactNUL)
		scan.Buffer(make([]byte, 4096), 64<<10)
		for scan.Scan() {
			header, path, ok := strings.Cut(scan.Text(), "\t")
			fields := strings.Fields(header)
			if !ok || len(fields) != 4 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || !ledger.ValidResourcePath(path) || !validOID(fields[2], oidLength(subject.descriptor.ObjectFormat)) {
				return ErrInvalid
			}
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 || size > maxBytes-total || len(entries) >= maxFiles {
				return refuse("byte_cap", path, "export exceeds capacity")
			}
			total += size
			entries = append(entries, exportEntry{stageEntry: stageEntry{Mode: fields[0], OID: fields[2], Path: path}, size: size})
		}
		return scan.Err()
	}, "ls-tree", "-r", "-z", "-l", "--full-tree", "--end-of-options", tree)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b exportEntry) int { return strings.Compare(a.Path, b.Path) })
	paths := make([]string, 0, len(entries))
	sizes := map[string]int64{}
	for _, entry := range entries {
		paths = append(paths, entry.Path)
		sizes[entry.OID] = entry.size
	}
	if err := checkCaseCollisions(paths); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(sizes))
	for id := range sizes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	batch := newBlobBatchHasher(ids, oidLength(subject.descriptor.ObjectFormat))
	if len(ids) != 0 {
		err = s.artifactStream(ctx, work, append(repositoryEnvironment(s.git.environ(), s.colony), "GIT_NO_LAZY_FETCH=1"), common, strings.NewReader(strings.Join(ids, "\n")+"\n"), func(reader io.Reader) error {
			b := bufio.NewReaderSize(reader, 64<<10)
			for _, id := range ids {
				header, err := b.ReadSlice('\n')
				if err != nil {
					return err
				}
				parsed, err := parseCatFileHeader(header, oidLength(subject.descriptor.ObjectFormat))
				if err != nil || parsed.OID != id || parsed.Kind != "blob" || parsed.Size != sizes[id] {
					return ErrInvalid
				}
				_, _ = batch.Write(header)
				f, err := os.OpenFile(filepath.Join(private, "blobs", id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return err
				}
				_, copyErr := io.CopyN(io.MultiWriter(f, batch), b, parsed.Size)
				closeErr := f.Close()
				if err := errors.Join(copyErr, closeErr); err != nil {
					return err
				}
				end, err := b.ReadByte()
				if err != nil || end != '\n' {
					return ErrInvalid
				}
				_, _ = batch.Write([]byte{end})
			}
			if _, err := b.ReadByte(); err != io.EOF {
				return ErrInvalid
			}
			return batch.finish()
		}, "cat-file", "--batch")
		if err != nil {
			return nil, err
		}
		var input strings.Builder
		for _, id := range ids {
			input.WriteString(strconv.Quote(filepath.ToSlash(filepath.Join(private, "blobs", id))))
			input.WriteByte('\n')
		}
		out, _, err := s.git.run(ctx, work, env, strings.NewReader(input.String()), []int{0}, "hash-object", "-w", "--no-filters", "--stdin-paths")
		if err != nil || string(out) != strings.Join(ids, "\n")+"\n" {
			return nil, errors.Join(ErrInvalid, err)
		}
	}
	var indexInput strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&indexInput, "%s %s\t%s%c", entry.Mode, entry.OID, entry.Path, byte(0))
	}
	if _, _, err := s.git.run(ctx, work, env, strings.NewReader(indexInput.String()), []int{0}, "update-index", "-z", "--index-info"); err != nil {
		return nil, err
	}
	out, _, err := s.git.run(ctx, work, env, nil, []int{0}, "write-tree")
	if err != nil || strings.TrimSuffix(string(out), "\n") != tree {
		return nil, errors.Join(ErrInvalid, err)
	}
	// Check conversions from the populated private index before read-tree can
	// invoke any conversion. Tree .gitattributes remain part of that index.
	if len(paths) != 0 {
		attrs, _, err := s.git.run(ctx, work, env, strings.NewReader(strings.Join(paths, "\x00")+"\x00"), []int{0}, "check-attr", "--cached", "-z", "--stdin", "filter", "working-tree-encoding", "ident")
		if err != nil {
			return nil, err
		}
		parsed, err := parseCheckAttr(attrs)
		if err != nil {
			return nil, err
		}
		for _, a := range parsed {
			if a.Value != "unspecified" && a.Value != "unset" {
				return nil, refuse("unsupported_conversion", a.Path, a.Name)
			}
		}
	}
	if _, _, err := s.git.run(ctx, work, env, nil, []int{0}, "read-tree", "-u", "--reset", "--end-of-options", tree); err != nil {
		return nil, err
	}
	walked, err := walkCapture(ctx, work, nil)
	if err != nil {
		return nil, err
	}
	if len(walked) != len(entries) {
		return nil, ErrInvalid
	}
	files := make([]ledger.ValidationFile, 0, len(entries))
	total = 0
	for _, entry := range entries {
		observed, ok := walked[entry.Path]
		if !ok || observed.info.Size() > maxBytes-total {
			return nil, ErrInvalid
		}
		f, err := openCaptureFile(filepath.Join(work, filepath.FromSlash(entry.Path)))
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		count, copyErr := io.Copy(h, io.LimitReader(contextReader{ctx: ctx, r: f}, observed.info.Size()))
		var extra [1]byte
		if n, readErr := f.Read(extra[:]); n != 0 || readErr != io.EOF {
			copyErr = errors.Join(copyErr, ErrRevision)
		}
		closeErr := f.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return nil, err
		}
		current, statErr := os.Lstat(filepath.Join(work, filepath.FromSlash(entry.Path)))
		if statErr != nil || !sameObservation(observed.info, current) || count != observed.info.Size() {
			return nil, ErrInvalid
		}
		var sum ledger.Digest
		copy(sum[:], h.Sum(nil))
		blob := batch.sums[entry.OID]
		if sum != blob.raw && sum != blob.crlf {
			return nil, refuse("lossy_normalization", entry.Path, "export normalization changed content")
		}
		files = append(files, ledger.ValidationFile{Path: entry.Path, Mode: entry.Mode, Bytes: count, SHA256: sum})
		total += count
	}
	// Force content hashing rather than trusting the checkout's stat cache.
	var normalize strings.Builder
	for _, path := range paths {
		normalize.WriteString(strconv.Quote(path))
		normalize.WriteByte('\n')
	}
	if len(paths) != 0 {
		out, _, err := s.git.run(ctx, work, env, strings.NewReader(normalize.String()), []int{0}, "hash-object", "--stdin-paths")
		if err != nil {
			return nil, err
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if len(got) != len(entries) {
			return nil, ErrInvalid
		}
		for i, entry := range entries {
			if got[i] != entry.OID {
				return nil, ErrInvalid
			}
		}
	}
	// Once the complete private copy exists it no longer needs F2 retention.
	// A source released during copying is refused and the private copy removed.
	if err := s.validationCurrent(ctx, subject); err != nil {
		return nil, err
	}
	manifest, err := (ledger.FileManifest{Format: ledger.FileManifestFormat, Schema: 1, Files: files}).Encode()
	if err != nil || len(manifest) > ledgerstore.MaxBodyBytes {
		return nil, errors.Join(ErrInvalid, err)
	}
	if err := os.RemoveAll(private); err != nil {
		return nil, err
	}
	handed = true
	return &ValidationExport{directory: work, manifest: manifest}, nil
}

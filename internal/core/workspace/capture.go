package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type captureMode uint8

const (
	observeCapture captureMode = iota
	sealCapture
)

type captureScratch struct {
	Worktree                    scratchWorktree
	Inputs                      Proposal
	Revision                    uint64
	InputsDir, TmpDir, ColonyID string
}

type captureResult struct {
	ID             uuid.UUID
	Tree, Baseline string
	InputsRevision uint64
	InputsSHA256   [32]byte
	Manifest       []byte
	ManifestSHA256 [32]byte
	Files          int
	RawBytes       int64
	RetentionRef   string
	// IgnoredEntries counts Git's ignored listing; a wholly ignored directory
	// is one entry. Its contents are not traversed, and no ignored path is returned.
	IgnoredEntries int
}

type captureEngine struct {
	git          *gitRunner
	colony       string
	afterRawRead func(string)
	manifestCap  int
}

func newCaptureEngine(g *gitRunner, colony string) *captureEngine {
	return &captureEngine{git: g, colony: colony, manifestCap: ledgerstore.MaxBodyBytes}
}

type manifestEntry struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type fileObservation struct {
	info fs.FileInfo
	sum  [32]byte
	size int64
}

func refuse(reason, path, detail string) error {
	return &RefusalError{Reason: reason, Path: path, Detail: detail}
}

func checkCaseCollisions(paths []string) error {
	type seenPath struct{ part, path string }
	seen := map[string]seenPath{}
	for _, path := range paths {
		parts := strings.Split(path, "/")
		prefix := ""
		for _, part := range parts {
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			key := strings.ToUpper(prefix)
			if previous, ok := seen[key]; ok && previous.part != prefix {
				return refuse("case_collision", previous.path, fmt.Sprintf("paths %q and %q collide", previous.path, path))
			}
			seen[key] = seenPath{part: prefix, path: path}
		}
	}
	return nil
}

// capture uses a token once and returns only its bytes share on every exit.
// Its caller owns the exclusion through any subsequent record and removal.
// A separate baseline supports ticket 06 without trusting scratch index or HEAD.
func (e *captureEngine) capture(ctx context.Context, s captureScratch, baseline string, r *reservation, mode captureMode) (result captureResult, err error) {
	profile, err := r.beginCapture(s.Worktree.Dir)
	if err != nil {
		return result, err
	}
	defer r.releaseCaptureBytes()
	if mode != observeCapture && mode != sealCapture {
		return result, fmt.Errorf("%w: invalid capture mode", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := e.git.checkInputsVersion(s.Inputs.GitVersion); err != nil {
		return result, err
	}
	commonInfo, statErr := os.Stat(s.Inputs.CommonDir)
	if statErr != nil || !commonInfo.IsDir() || s.ColonyID == "" {
		return result, &UnavailableError{Reason: "workspace_unverified"}
	}
	marker, markerErr := e.git.readBlob(ctx, e.colony, s.Inputs.CommonDir, "refs/dwarf-harness/colony")
	if markerErr != nil && ctx.Err() != nil {
		return result, markerErr
	}
	if markerErr != nil || string(marker) != "dwarf-harness colony "+s.ColonyID+"\n" {
		return result, &UnavailableError{Reason: "workspace_unverified"}
	}
	info, statErr := os.Lstat(s.Worktree.Dir)
	if statErr != nil || !info.IsDir() {
		return result, &UnavailableError{Reason: "scratch_missing"}
	}
	if !validOID(baseline, oidLength(s.Inputs.ObjectFormat)) || s.Revision == 0 {
		return result, fmt.Errorf("%w: invalid capture baseline or inputs revision", ErrInvalid)
	}
	if err := materializeInputs(e.colony, s.InputsDir, s.Inputs); err != nil {
		return result, err
	}
	if err := os.MkdirAll(s.TmpDir, 0o700); err != nil {
		return result, err
	}
	id := uuid.NewV7()
	index := filepath.Join(s.TmpDir, id.String()+".idx")
	cleanup := func() error {
		for _, path := range []string{index, index + ".lock"} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; index cleanup: %v", err, cleanupErr)
			} else {
				err = fmt.Errorf("%w: index cleanup: %v", ErrGit, cleanupErr)
			}
			result = captureResult{}
		}
	}()
	env := captureEnvironment(e.git.environ(), e.colony, s.InputsDir, s.Worktree.Dir, index, s.Inputs.CommonDir)
	call := func(in io.Reader, args ...string) ([]byte, error) {
		b, _, err := e.git.run(ctx, s.Worktree.Dir, env, in, []int{0}, args...)
		return b, err
	}
	if _, err := call(nil, "read-tree", "--end-of-options", baseline); err != nil {
		return result, err
	}
	b, err := call(nil, "ls-files", "-z", "-s")
	if err != nil {
		return result, err
	}
	tracked, err := parseStageEntries(b, oidLength(s.Inputs.ObjectFormat))
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrGit, err)
	}
	var baselinePaths []string
	for _, entry := range tracked {
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return result, refuse("unsupported_kind", entry.Path, "baseline is not a regular file")
		}
		baselinePaths = append(baselinePaths, entry.Path)
	}
	if err := checkCaseCollisions(baselinePaths); err != nil {
		return result, err
	}
	b, err = call(nil, "ls-files", "-z", "-o", "--exclude-standard")
	if err != nil {
		return result, err
	}
	untracked, err := parseNULList(b)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrGit, err)
	}
	for _, path := range untracked {
		if strings.HasSuffix(path, "/") {
			return result, refuse("unsupported_kind", path, "nested repository")
		}
	}
	b, err = call(nil, "ls-files", "-z", "-o", "-i", "--exclude-standard", "--directory")
	if err != nil {
		return result, err
	}
	ignored, err := parseNULList(b)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrGit, err)
	}
	ignoredSet := map[string]bool{}
	for _, path := range ignored {
		ignoredSet[path] = true
	}
	walked, err := walkCapture(ctx, s.Worktree.Dir, ignoredSet)
	if err != nil {
		return result, err
	}
	candidates, deleted, err := reconcileCapture(tracked, untracked, ignoredSet, walked, s.Inputs.Normalization.IgnoreCase == "true")
	if err != nil {
		return result, err
	}
	if len(candidates) > profile.MaxFiles {
		return result, refuse("file_cap", "", "candidate file count exceeds profile")
	}
	var total int64
	for _, path := range candidates {
		size := walked[path].info.Size()
		if size > profile.MaxBytes-total {
			return result, refuse("byte_cap", path, "candidate sizes exceed profile")
		}
		total += size
	}
	if len(candidates) > 0 {
		b, err = call(strings.NewReader(strings.Join(candidates, "\x00")+"\x00"), "check-attr", "-z", "--stdin", "filter", "working-tree-encoding", "ident")
		if err != nil {
			return result, err
		}
		attrs, err := parseCheckAttr(b)
		if err != nil {
			return result, fmt.Errorf("%w: %v", ErrGit, err)
		}
		for _, a := range attrs {
			if a.Value == "unspecified" || a.Value == "unset" {
				continue
			}
			reason := "unsupported_conversion"
			if a.Name == "filter" {
				reason = "unsupported_filter"
			}
			return result, refuse(reason, a.Path, a.Name+"="+a.Value)
		}
	}
	total = 0
	for _, path := range candidates {
		observation := walked[path]
		full := filepath.Join(s.Worktree.Dir, filepath.FromSlash(path))
		before, err := os.Lstat(full)
		if err != nil || !sameObservation(observation.info, before) {
			return result, refuse("scratch_changed", path, "file changed after walk")
		}
		file, err := os.Open(full)
		if err != nil {
			return result, refuse("unreadable", path, err.Error())
		}
		raw := sha256.New()
		limit := profile.MaxBytes - total
		if limit < 1<<63-1 {
			limit++
		}
		n, readErr := io.Copy(raw, io.LimitReader(contextReader{ctx: ctx, r: file}, limit))
		closeErr := file.Close()
		if n > profile.MaxBytes-total {
			return result, refuse("byte_cap", path, "raw reads exceed profile")
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, refuse("unreadable", path, readErr.Error())
		}
		if closeErr != nil {
			return result, refuse("unreadable", path, closeErr.Error())
		}
		after, err := os.Lstat(full)
		if err != nil || !sameObservation(before, after) {
			return result, refuse("scratch_changed", path, "file changed during read")
		}
		copy(observation.sum[:], raw.Sum(nil))
		observation.size = n
		walked[path] = observation
		total += n
		if e.afterRawRead != nil {
			e.afterRawRead(path)
		}
	}
	if len(deleted) > 0 {
		if _, err := call(strings.NewReader(strings.Join(deleted, "\x00")+"\x00"), "update-index", "-z", "--force-remove", "--stdin"); err != nil {
			return result, err
		}
	}
	if len(candidates) > 0 {
		if _, err := call(strings.NewReader(strings.Join(candidates, "\x00")+"\x00"), "update-index", "-z", "--add", "--stdin"); err != nil {
			return result, err
		}
	}
	b, err = call(nil, "write-tree")
	if err != nil {
		return result, err
	}
	tree := strings.TrimSuffix(string(b), "\n")
	if !validOID(tree, oidLength(s.Inputs.ObjectFormat)) {
		return result, fmt.Errorf("%w: invalid captured tree", ErrGit)
	}
	b, err = call(nil, "ls-files", "-z", "-s")
	if err != nil {
		return result, err
	}
	entries, err := parseStageEntries(b, oidLength(s.Inputs.ObjectFormat))
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrGit, err)
	}
	if len(entries) != len(candidates) {
		return result, refuse("scratch_changed", "", "candidate path set changed")
	}
	distinct := map[string]bool{}
	for i, entry := range entries {
		if entry.Path != candidates[i] || entry.Stage != 0 {
			return result, refuse("scratch_changed", entry.Path, "candidate path set changed")
		}
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return result, refuse("unsupported_kind", entry.Path, "captured object is not a regular file")
		}
		distinct[entry.OID] = true
	}
	if err := checkCaseCollisions(candidates); err != nil {
		return result, err
	}
	var ids []string
	for id := range distinct {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	batch := newBlobBatchHasher(ids, oidLength(s.Inputs.ObjectFormat))
	if len(ids) > 0 {
		if _, err := e.git.runOutput(ctx, s.Worktree.Dir, env, strings.NewReader(strings.Join(ids, "\n")+"\n"), batch, []int{0}, "cat-file", "--batch"); err != nil {
			return result, err
		}
		if err := batch.finish(); err != nil {
			return result, err
		}
	}
	for _, entry := range entries {
		observed := walked[entry.Path]
		hashed := batch.sums[entry.OID]
		if observed.sum != hashed.raw && observed.sum != hashed.crlf {
			now, statErr := os.Lstat(filepath.Join(s.Worktree.Dir, filepath.FromSlash(entry.Path)))
			if statErr != nil || !sameObservation(observed.info, now) {
				return result, refuse("scratch_changed", entry.Path, "file changed before Git hashing")
			}
			return result, refuse("lossy_normalization", entry.Path, "raw bytes match neither blob nor crlf(blob)")
		}
	}
	manifest, err := buildManifest(s.Inputs.ObjectFormat, entries, walked, e.manifestCap)
	if err != nil {
		return result, err
	}
	for _, path := range candidates {
		now, statErr := os.Lstat(filepath.Join(s.Worktree.Dir, filepath.FromSlash(path)))
		if statErr != nil || !sameObservation(walked[path].info, now) {
			return result, refuse("scratch_changed", path, "file changed before sealing")
		}
	}
	if err := cleanup(); err != nil {
		return result, fmt.Errorf("%w: index cleanup: %v", ErrGit, err)
	}
	_, inputsHash, err := encodeCaptureInputs(s.Inputs, "capture-rules/1")
	if err != nil {
		return result, err
	}
	result = captureResult{ID: id, Tree: tree, Baseline: baseline, InputsRevision: s.Revision, InputsSHA256: inputsHash, Manifest: manifest, ManifestSHA256: sha256.Sum256(manifest), Files: len(candidates), RawBytes: total, IgnoredEntries: len(ignored)}
	if mode == sealCapture {
		result.RetentionRef = "refs/dwarf-harness/captures/" + id.String()
		if err := e.git.createRef(ctx, e.colony, s.Inputs.CommonDir, result.RetentionRef, tree); err != nil {
			return captureResult{}, err
		}
	}
	return result, nil
}

func walkCapture(ctx context.Context, root string, ignored map[string]bool) (map[string]fileObservation, error) {
	walked := map[string]fileObservation{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return refuse("unreadable", path, err.Error())
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		kind := d.Type()
		info, err := os.Lstat(path)
		if err != nil {
			return refuse("unreadable", rel, err.Error())
		}
		kind |= info.Mode().Type()
		if rel == ".git" && info.Mode().IsRegular() {
			return nil
		}
		if kind&os.ModeSymlink != 0 {
			return refuse("unsupported_kind", rel, "symbolic link")
		}
		if kind&os.ModeIrregular != 0 {
			return refuse("special_file", rel, "reparse point or irregular file")
		}
		if info.IsDir() {
			if ignored[rel+"/"] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return refuse("special_file", rel, "not a regular file")
		}
		walked[rel] = fileObservation{info: info}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return walked, nil
}

func reconcileCapture(tracked []stageEntry, untracked []string, ignored map[string]bool, walked map[string]fileObservation, ignoreCase bool) ([]string, []string, error) {
	candidateSet := map[string]bool{}
	var deleted []string
	folded := map[string]string{}
	if ignoreCase {
		for path := range walked {
			folded[strings.ToUpper(path)] = path
		}
	}
	for _, entry := range tracked {
		if _, ok := walked[entry.Path]; ok {
			candidateSet[entry.Path] = true
			continue
		}
		if ignoreCase {
			if actual, ok := folded[strings.ToUpper(entry.Path)]; ok {
				return nil, nil, refuse("name_mismatch", entry.Path, "observed path "+actual)
			}
		}
		deleted = append(deleted, entry.Path)
	}
	for _, path := range untracked {
		if _, ok := walked[path]; !ok {
			return nil, nil, refuse("name_mismatch", path, "Git path not found by Core")
		}
		candidateSet[path] = true
	}
	for path := range walked {
		if !candidateSet[path] && !ignored[path] {
			return nil, nil, refuse("unobservable_path", path, "Core path not listed by Git")
		}
	}
	var candidates []string
	for path := range candidateSet {
		if !utf8.ValidString(path) {
			return nil, nil, refuse("invalid_path", path, "path is not UTF-8")
		}
		candidates = append(candidates, path)
	}
	sort.Strings(candidates)
	return candidates, deleted, nil
}

func sameObservation(a, b fs.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func buildManifest(format string, entries []stageEntry, observed map[string]fileObservation, limit int) ([]byte, error) {
	var b bytes.Buffer
	appendLine := func(v any) error {
		line, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if len(line)+1 > limit-b.Len() {
			return refuse("manifest_cap", "", "raw manifest exceeds body cap")
		}
		b.Write(line)
		b.WriteByte('\n')
		return nil
	}
	header := struct {
		Format       string `json:"format"`
		Version      int    `json:"version"`
		ObjectFormat string `json:"object_format"`
	}{"dwarf-harness/capture-manifest", 1, format}
	if err := appendLine(header); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		raw := observed[entry.Path]
		if err := appendLine(manifestEntry{Path: entry.Path, Mode: entry.Mode, ID: entry.OID, Size: raw.size, SHA256: fmt.Sprintf("%x", raw.sum)}); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

type blobHashes struct{ raw, crlf [32]byte }
type blobBatchHasher struct {
	ids             []string
	oidLength, next int
	header          []byte
	remaining       int64
	active, needLF  bool
	raw             hash.Hash
	crlf            *crlfHasher
	sums            map[string]blobHashes
	err             error
}

func newBlobBatchHasher(ids []string, n int) *blobBatchHasher {
	return &blobBatchHasher{ids: ids, oidLength: n, sums: map[string]blobHashes{}}
}

// Write drains stdout even after a malformed header, preventing a blocked Git
// child; finish then refuses the whole result, never a partial hash map.
func (b *blobBatchHasher) Write(p []byte) (int, error) {
	original := len(p)
	for len(p) > 0 && b.err == nil {
		if b.active {
			n := min(int64(len(p)), b.remaining)
			_, _ = b.raw.Write(p[:n])
			_, _ = b.crlf.Write(p[:n])
			p = p[n:]
			b.remaining -= n
			if b.remaining == 0 {
				b.active = false
				b.needLF = true
			}
			continue
		}
		if b.needLF {
			if p[0] != '\n' {
				b.err = fmt.Errorf("%w: missing cat-file body terminator", ErrGit)
				break
			}
			sum := blobHashes{crlf: b.crlf.Sum()}
			copy(sum.raw[:], b.raw.Sum(nil))
			b.sums[b.ids[b.next]] = sum
			b.next++
			b.needLF = false
			p = p[1:]
			continue
		}
		at := bytes.IndexByte(p, '\n')
		n := len(p)
		if at >= 0 {
			n = at + 1
		}
		if len(b.header)+n > 256 {
			b.err = fmt.Errorf("%w: oversized cat-file header", ErrGit)
			break
		}
		b.header = append(b.header, p[:n]...)
		p = p[n:]
		if at < 0 {
			continue
		}
		header, err := parseCatFileHeader(b.header, b.oidLength)
		if err != nil || b.next >= len(b.ids) || header.OID != b.ids[b.next] || header.Kind != "blob" {
			b.err = fmt.Errorf("%w: invalid cat-file batch response", ErrGit)
			break
		}
		b.header = nil
		b.remaining = header.Size
		b.raw = sha256.New()
		b.crlf = newCRLFHasher()
		b.active = header.Size > 0
		b.needLF = header.Size == 0
	}
	return original, nil
}

func (b *blobBatchHasher) finish() error {
	if b.err != nil {
		return b.err
	}
	if b.next != len(b.ids) || len(b.header) != 0 || b.active || b.needLF {
		return fmt.Errorf("%w: incomplete cat-file batch", ErrGit)
	}
	return nil
}

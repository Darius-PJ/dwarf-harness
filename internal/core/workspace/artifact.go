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
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ArtifactRequest reads an immutable tree of a registered workspace. MaxBytes
// bounds returned blob bytes (at most 8 MiB); WorkAllowance is 1..10,000.
// Every scanned entry and every 256 stream bytes (rounded up) costs work,
// including entries at or before AfterPath on repeated scans.
type ArtifactRequest struct {
	WorkspaceID             uuid.UUID
	Tree, AfterPath         string
	MaxBytes, WorkAllowance uint64
}

// Artifact is a regular immutable blob, or an explicit unknown entry. Unknown
// entries have Reason set and no bytes/digest; symlinks and gitlinks are never read.
type Artifact struct {
	Path, Mode, GitOID string
	SHA256             ledger.Digest
	Bytes              []byte
	Reason             string
}

// ArtifactBatch is a byte-ordered page. Complete means end-of-tree was reached
// and both Git children were successfully waited, not complete language coverage.
// Reason describes a page-level limit/unavailability; no failed child proves EOF.
type ArtifactBatch struct {
	Entries  []Artifact
	NextPath string
	Complete bool
	WorkUsed uint64
	Reason   string
}

var errArtifactLimit = errors.New("artifact read limit")

func artifactPath(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\\") {
		return false
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." {
			return false
		}
	}
	return true
}

// ReadArtifacts reads no mutable index/worktree and runs no filters or hooks.
// Repository identity comes exclusively from this service's frozen registration;
// the caller is responsible for binding Tree to its accepted ledger subject.
func (s *Service) ReadArtifacts(ctx context.Context, r ArtifactRequest) (ArtifactBatch, error) {
	b := ArtifactBatch{NextPath: r.AfterPath}
	if !validID(r.WorkspaceID) || !validOID(r.Tree, len(r.Tree)) || r.AfterPath != "" && !artifactPath(r.AfterPath) || r.MaxBytes == 0 || r.MaxBytes > 8<<20 || r.WorkAllowance == 0 || r.WorkAllowance > 10000 {
		return b, ErrInvalid
	}
	if err := s.start(); err != nil {
		return b, err
	}
	defer s.ops.Done()
	s.mu.Lock()
	w, ok := s.workspaces[r.WorkspaceID]
	s.mu.Unlock()
	if !ok {
		return b, ErrUnknownWorkspace
	}
	if !validOID(r.Tree, oidLength(w.ObjectFormat)) {
		return b, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return b, err
	}
	cwd, err := os.MkdirTemp("", "workspace-artifacts-")
	if err != nil {
		return b, err
	}
	defer os.RemoveAll(cwd)
	env := append(repositoryEnvironment(s.git.environ(), s.colony), "GIT_NO_LAZY_FETCH=1")
	charge := func(n uint64) error {
		if n > r.WorkAllowance-b.WorkUsed {
			b.WorkUsed = r.WorkAllowance
			return errArtifactLimit
		}
		b.WorkUsed += n
		return nil
	}
	var entries []Artifact
	err = s.artifactStream(ctx, cwd, env, w.CommonDir, nil, func(in io.Reader) error {
		scan := bufio.NewScanner(in)
		scan.Split(artifactNUL)
		scan.Buffer(make([]byte, 4096), 8192)
		for scan.Scan() {
			row := scan.Text()
			if err := charge(1 + (uint64(len(row))+1+255)/256); err != nil {
				return err
			}
			header, path, ok := strings.Cut(row, "\t")
			parts := strings.Split(header, " ")
			if !ok || len(parts) != 3 || !artifactPath(path) || !validOID(parts[2], len(r.Tree)) {
				return fmt.Errorf("invalid immutable tree entry")
			}
			if path <= r.AfterPath {
				continue
			}
			e := Artifact{Path: path, Mode: parts[0], GitOID: parts[2]}
			if parts[1] != "blob" || e.Mode != "100644" && e.Mode != "100755" {
				e.Reason = ledger.ReasonUnsupportedSyntax
			}
			entries = append(entries, e)
		}
		if errors.Is(scan.Err(), bufio.ErrTooLong) {
			if err := charge(1 + 8192/256); err != nil {
				return err
			}
			b.Reason = ledger.ReasonHelperLimit
			return errArtifactLimit
		}
		return scan.Err()
	}, "ls-tree", "-rz", "--full-tree", r.Tree)
	if err != nil {
		if errors.Is(err, errArtifactLimit) {
			if b.Reason == "" {
				b.Reason = ledger.ReasonWorkExhausted
			}
			return b, nil
		}
		return b, err
	}
	slices.SortFunc(entries, func(a, b Artifact) int { return strings.Compare(a.Path, b.Path) })
	var input strings.Builder
	for _, e := range entries {
		if e.Reason == "" {
			input.WriteString(e.GitOID)
			input.WriteByte('\n')
		}
	}
	var used uint64
	consume := func(in io.Reader) error {
		reader := bufio.NewReaderSize(in, 4096)
		for _, e := range entries {
			if e.Reason == "" {
				header, err := reader.ReadSlice('\n')
				if err != nil {
					return err
				}
				if err := charge((uint64(len(header)) + 255) / 256); err != nil {
					return err
				}
				parts := strings.Fields(string(header))
				if len(parts) == 2 && parts[0] == e.GitOID && parts[1] == "missing" {
					e.Reason = ledger.ReasonSourceUnavailable
				} else {
					if len(parts) != 3 || parts[0] != e.GitOID || parts[1] != "blob" {
						return fmt.Errorf("invalid immutable blob header")
					}
					size, err := strconv.ParseUint(parts[2], 10, 64)
					if err != nil {
						return err
					}
					if size > 1<<20 {
						b.Reason = ledger.ReasonHelperLimit
						return errArtifactLimit
					}
					if size > r.MaxBytes-used {
						b.Reason = ledger.ReasonTemporaryCap
						return errArtifactLimit
					}
					if err := charge((size + 1 + 255) / 256); err != nil {
						return err
					}
					e.Bytes = make([]byte, int(size))
					if _, err := io.ReadFull(reader, e.Bytes); err != nil {
						return err
					}
					end, err := reader.ReadByte()
					if err != nil {
						return err
					}
					if end != '\n' {
						return fmt.Errorf("invalid immutable blob terminator")
					}
					e.SHA256 = ledger.Digest(sha256.Sum256(e.Bytes))
					used += size
				}
			}
			b.Entries = append(b.Entries, e)
			b.NextPath = e.Path
		}
		_, err := reader.ReadByte()
		if err != io.EOF {
			return fmt.Errorf("unexpected immutable blob trailer: %w", err)
		}
		return nil
	}
	if input.Len() > 0 {
		err = s.artifactStream(ctx, cwd, env, w.CommonDir, strings.NewReader(input.String()), consume, "cat-file", "--batch")
	} else {
		err = consume(bytes.NewReader(nil))
	}
	if errors.Is(err, errArtifactLimit) {
		if b.Reason == "" {
			b.Reason = ledger.ReasonWorkExhausted
		}
		return b, nil
	}
	if err != nil {
		return b, err
	}
	b.Complete = true
	return b, nil
}

func artifactNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return 0, nil, nil
}

// Parsing, cancellation and Wait are joined before returning. The existing Git
// launcher remains the sole process implementation and its environments unchanged.
func (s *Service) artifactStream(ctx context.Context, cwd string, env []string, common string, input io.Reader, consume func(io.Reader) error, args ...string) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := s.git.runOutput(child, cwd, env, input, writer, []int{0}, append([]string{"--git-dir=" + common}, args...)...)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	parseErr := consume(reader)
	_ = reader.Close()
	if parseErr != nil {
		cancel()
	}
	waitErr := <-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if parseErr != nil {
		return parseErr
	}
	return waitErr
}

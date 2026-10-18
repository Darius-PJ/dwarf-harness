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
	"strconv"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ArtifactRequest reads an immutable tree of a registered workspace. MaxBytes
// bounds returned blob bytes (at most 8 MiB); WorkAllowance is 1..10,000.
// A replayed prefix costs one nominal unit per 256 skipped rows, rounded up,
// rather than charging its bytes again on every page. Each new row costs one
// entry unit plus ceil((row bytes + NUL)/256). Before admitting a regular blob,
// its maximum header/body cost (1 + ceil((size + LF)/256)) is charged as well;
// this conservative charge is not refunded if the object subsequently vanishes.
// Listing stops at the first byte/work boundary and is cancelled and waited.
// With at most 50,000 entries, prefix cost is at most 196 units and each admitted
// entry at most 4,355 (64 KiB row including NUL, 1 MiB blob). Thus 10,000-unit
// pages always advance when MaxBytes fits the next supported blob. Oversized
// blobs are unknown entries, not page barriers. A row exceeding the residual
// 64 KiB scanner cap is a page-level helper_limit; such paths are outside this
// reader's bounded profile, not silently complete or assigned a new identity.
// ListOnly charges listing work but reads no blob bytes. Paths, when nonempty,
// contains at most 10,000 strictly sorted exact paths; filtered reads answer
// these through cat-file --batch-check, follow no link and match no prefix.
// GIT_LITERAL_PATHSPECS is not needed: names travel as NUL-terminated tree:path
// queries on stdin, not as pathspecs. Missing and directory answers cost a row
// and advance NextPath, but return no entry.
type ArtifactRequest struct {
	WorkspaceID             uuid.UUID
	Tree, AfterPath         string
	MaxBytes, WorkAllowance uint64
	ListOnly                bool
	Paths                   []string
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
	if len(r.Paths) > 10000 {
		return b, ErrInvalid
	}
	for i, path := range r.Paths {
		if !artifactPath(path) || i > 0 && path <= r.Paths[i-1] {
			return b, ErrInvalid
		}
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
	type listedArtifact struct {
		Artifact
		size uint64
	}
	var entries []listedArtifact
	var admittedBytes, skipped uint64
	admit := func(path string, parts []string, rowBytes int) error {
		if len(parts) != 4 || !validOID(parts[2], len(r.Tree)) {
			return fmt.Errorf("invalid immutable tree entry")
		}
		switch parts[0] {
		case "100644", "100755", "120000":
			if parts[1] != "blob" {
				return fmt.Errorf("invalid immutable tree entry")
			}
		case "160000":
			if parts[1] != "commit" {
				return fmt.Errorf("invalid immutable tree entry")
			}
		case "040000":
			if parts[1] != "tree" {
				return fmt.Errorf("invalid immutable tree entry")
			}
		default:
			return fmt.Errorf("invalid immutable tree entry")
		}
		if err := charge(1 + (uint64(rowBytes)+1+255)/256); err != nil {
			return err
		}
		e := listedArtifact{Artifact: Artifact{Path: path, Mode: parts[0], GitOID: parts[2]}}
		if parts[1] != "blob" || e.Mode != "100644" && e.Mode != "100755" {
			e.Reason = ledger.ReasonUnsupportedSyntax
		} else if parts[3] == "BAD" {
			e.Reason = ledger.ReasonSourceUnavailable
		} else {
			size, err := strconv.ParseUint(parts[3], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid immutable tree size: %w", err)
			}
			e.size = size
			if size > 1<<20 {
				e.Reason = ledger.ReasonHelperLimit
			} else if !r.ListOnly {
				if size > r.MaxBytes-admittedBytes {
					b.Reason = ledger.ReasonTemporaryCap
					return errArtifactLimit
				}
				if err := charge(1 + (size+1+255)/256); err != nil {
					return err
				}
				admittedBytes += size
			}
		}
		if len(r.Paths) == 0 || parts[1] == "blob" || parts[0] == "160000" {
			entries = append(entries, e)
		}
		return nil
	}
	args := []string{"ls-tree", "-rzl", "--full-tree", r.Tree}
	var queries strings.Builder
	var paths []string
	if len(r.Paths) != 0 {
		for _, path := range r.Paths {
			if path > r.AfterPath {
				paths = append(paths, path)
				queries.WriteString(r.Tree + ":" + path)
				queries.WriteByte(0)
			}
		}
		if len(paths) == 0 {
			b.Complete = true
			return b, nil
		}
		args = []string{"cat-file", "--batch-check=%(objectmode) %(objectname) %(objecttype) %(objectsize)", "-Z"}
	}
	answered := r.AfterPath
	err = s.artifactStream(ctx, cwd, env, w.CommonDir, strings.NewReader(queries.String()), func(in io.Reader) error {
		scan := bufio.NewScanner(in)
		scan.Split(artifactNUL)
		scan.Buffer(make([]byte, 4096), 64<<10)
		previous, index := "", 0
		for scan.Scan() {
			row := scan.Text()
			if len(r.Paths) != 0 {
				if index >= len(paths) {
					return fmt.Errorf("invalid immutable tree entry")
				}
				path := paths[index]
				if row == r.Tree+":"+path+" missing" {
					if err := charge(1 + (uint64(len(row))+1+255)/256); err != nil {
						return err
					}
				} else {
					parts := strings.Fields(row)
					if len(parts) == 2 && parts[1] == "submodule" {
						parts = []string{"160000", "commit", parts[0], "-"}
					} else {
						if len(parts) != 4 {
							return fmt.Errorf("invalid immutable tree entry")
						}
						parts[1], parts[2] = parts[2], parts[1]
						if _, err := strconv.ParseUint(parts[3], 10, 64); err != nil {
							return fmt.Errorf("invalid immutable tree entry")
						}
					}
					if err := admit(path, parts, len(row)); err != nil {
						return err
					}
				}
				answered = path
				index++
				continue
			}
			header, path, ok := strings.Cut(row, "\t")
			parts := strings.Fields(header)
			if !ok || !artifactPath(path) || path <= previous {
				return fmt.Errorf("invalid immutable tree entry")
			}
			previous = path
			if path <= r.AfterPath {
				if skipped%256 == 0 {
					if err := charge(1); err != nil {
						return err
					}
				}
				skipped++
				continue
			}
			if err := admit(path, parts, len(row)); err != nil {
				return err
			}
			answered = path
		}
		if errors.Is(scan.Err(), bufio.ErrTooLong) {
			if err := charge(1 + (64<<10)/256); err != nil {
				return err
			}
			b.Reason = ledger.ReasonHelperLimit
			return errArtifactLimit
		}
		if scan.Err() == nil && len(r.Paths) != 0 && index != len(paths) {
			return fmt.Errorf("invalid immutable tree entry")
		}
		return scan.Err()
	}, args...)
	listingComplete := err == nil
	if errors.Is(err, errArtifactLimit) {
		if b.Reason == "" {
			b.Reason = ledger.ReasonWorkExhausted
		}
	} else if err != nil {
		return b, err
	}
	var input strings.Builder
	for _, e := range entries {
		if e.Reason == "" && !r.ListOnly {
			input.WriteString(e.GitOID)
			input.WriteByte('\n')
		}
	}
	consume := func(in io.Reader) error {
		reader := bufio.NewReaderSize(in, 4096)
		for _, e := range entries {
			if e.Reason == "" && !r.ListOnly {
				header, err := reader.ReadSlice('\n')
				if err != nil {
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
					if size != e.size {
						return fmt.Errorf("immutable blob size changed")
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
				}
			}
			b.Entries = append(b.Entries, e.Artifact)
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
	if err != nil {
		return b, err
	}
	b.NextPath = answered
	b.Complete = listingComplete
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

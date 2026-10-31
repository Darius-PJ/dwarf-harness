package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type loopFileArgs struct {
	Schema         int       `json:"schema"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	TaskContextID  uuid.UUID `json:"task_context_id"`
	Path           string    `json:"path"`
	MaxBytes       int64     `json:"max_bytes"`
	Mode           string    `json:"mode"`
	Query          string    `json:"query"`
	ExpectedSHA256 string    `json:"expected_sha256"`
	Text           string    `json:"text"`
	OldText        string    `json:"old_text"`
	NewText        string    `json:"new_text"`
}
type loopFileResult struct {
	Path           string        `json:"path"`
	PreviousSHA256 ledger.Digest `json:"previous_sha256,omitzero"`
	SHA256         ledger.Digest `json:"sha256"`
	Bytes          int64         `json:"bytes"`
	Text           *string       `json:"text,omitempty"`
}
type loopSearchMatch struct {
	Path string  `json:"path"`
	Line int     `json:"line,omitempty"`
	Text *string `json:"text,omitempty"`
}
type loopSearchResult struct {
	Matches    []loopSearchMatch `json:"matches"`
	Examined   int64             `json:"examined"`
	Incomplete bool              `json:"incomplete"`
}

type loopFileRoot struct {
	root *os.Root
	name string
}

func loopSafePath(name string) bool {
	if name == "" {
		return true
	}
	if !utf8.ValidString(name) || strings.ContainsAny(name, "\\:\x00") || path.IsAbs(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}
func loopRealDirectory(name string) (os.FileInfo, error) {
	for current := name; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil, ErrDenied
		}
		parent := filepath.Dir(current)
		if current == parent {
			break
		}
	}
	return os.Lstat(name)
}
func loopRootAt(scratch workspace.Scratch, prefix, name string) (loopFileRoot, error) {
	if !loopSafePath(prefix) || !loopSafePath(name) || prefix != "" && name != prefix && !strings.HasPrefix(name, prefix+"/") {
		return loopFileRoot{}, ErrDenied
	}
	info, err := loopRealDirectory(scratch.Dir)
	if err != nil {
		return loopFileRoot{}, err
	}
	root, err := os.OpenRoot(scratch.Dir)
	if err != nil {
		return loopFileRoot{}, ErrUnavailable
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return loopFileRoot{}, ErrDenied
	}
	// A grant can name a single file. Its fixed leaf is retained while its
	// containing directory becomes the rooted capability; no sibling is used.
	rootPrefix := prefix
	if prefix != "" {
		stat, err := root.Lstat(prefix)
		if err == nil && !stat.IsDir() || errors.Is(err, os.ErrNotExist) && prefix == name {
			rootPrefix = path.Dir(prefix)
			if rootPrefix == "." {
				rootPrefix = ""
			}
		}
	}
	if rootPrefix != "" {
		for _, part := range strings.Split(rootPrefix, "/") {
			info, err := root.Lstat(part)
			if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				root.Close()
				return loopFileRoot{}, ErrDenied
			}
			next, err := root.OpenRoot(part)
			if err != nil {
				root.Close()
				return loopFileRoot{}, ErrDenied
			}
			opened, err := next.Stat(".")
			root.Close()
			if err != nil || !os.SameFile(info, opened) {
				next.Close()
				return loopFileRoot{}, ErrDenied
			}
			root = next
		}
	}
	relative := name
	if rootPrefix != "" {
		relative = strings.TrimPrefix(name, rootPrefix)
		relative = strings.TrimPrefix(relative, "/")
	}
	if relative == "" {
		relative = "."
	}
	return loopFileRoot{root, relative}, nil
}
func loopCheckEntries(root *os.Root, name string, absent bool) error {
	if name == "." {
		return nil
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if errors.Is(err, os.ErrNotExist) && absent && i == len(parts)-1 {
			return nil
		}
		if err != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.IsDir() && !info.Mode().IsRegular() {
			return ErrDenied
		}
	}
	return nil
}
func loopReadFile(root *os.Root, name string, limit int64) ([]byte, os.FileInfo, error) {
	if err := loopCheckEntries(root, name, false); err != nil {
		return nil, nil, err
	}
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, ErrUnavailable
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, nil, ErrTooLarge
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, nil, ErrDenied
	}
	data := make([]byte, info.Size()+1)
	n, readErr := io.ReadFull(file, data)
	closeErr := file.Close()
	if readErr != io.EOF && readErr != io.ErrUnexpectedEOF || closeErr != nil || int64(n) != info.Size() {
		return nil, nil, ErrUnavailable
	}
	return data[:n], info, nil
}

func (r *loopRuntime) fileAuthority(t *loopTurn, inv *loopInvocation) (string, uuid.UUID, error) {
	s, target := r.service, *inv.normalized.Target
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(t.ctx, s.store.Durable()); err != nil {
		return "", uuid.UUID{}, err
	}
	chain, err := s.authorize(t.ctx, t.origin, inv.grant, inv.normalized.Operation, []ledger.Resource{target}, nil, 0, s.p.through)
	if err != nil {
		return "", uuid.UUID{}, err
	}
	if target.Binding.AgentID != t.origin.AgentID {
		return "", uuid.UUID{}, ErrDenied
	}
	if err := s.workspaceReady(t.ctx, target, s.p.through); err != nil {
		return "", uuid.UUID{}, ErrDenied
	}
	prefix, found := "", false
	for _, resource := range chain[0].resources {
		if resource.Kind == "workspace" && resource.WorkspaceID == target.WorkspaceID && resource.Binding == target.Binding && (resource.PathPrefix == "" || target.PathPrefix == resource.PathPrefix || strings.HasPrefix(target.PathPrefix, resource.PathPrefix+"/")) {
			if !found || len(resource.PathPrefix) > len(prefix) {
				prefix, found = resource.PathPrefix, true
			}
		}
	}
	if !found {
		return "", uuid.UUID{}, ErrDenied
	}
	for id, scratch := range s.p.scratches {
		if scratch.WorkspaceID == target.WorkspaceID && scratch.ContextID == target.Binding.ContextID && scratch.ParentScratchID == (uuid.UUID{}) && !scratch.closed {
			return prefix, id, nil
		}
	}
	return "", uuid.UUID{}, ErrDenied
}

func (r *loopRuntime) file(t *loopTurn, inv *loopInvocation) (data any, receipt uint64, err error) {
	s := r.service
	var args loopFileArgs
	if decodeLoopJSON(inv.normalized.Arguments, &args) != nil {
		return nil, 0, ErrInvalid
	}
	prefix, scratchID, err := r.fileAuthority(t, inv)
	if err != nil {
		return nil, 0, err
	}
	if !loopSafePath(args.Path) {
		return nil, 0, ErrDenied
	}
	maxBytes := int64(len(inv.normalized.Bytes))*6 + 6*t.policy.MaxResultBytes + 2*max(args.MaxBytes, int64(len(args.Text)+len(args.NewText))) + 4096
	if maxBytes > s.limits.MaxScopedBytes {
		return nil, 0, ErrTooLarge
	}
	agent := &Agent{service: s, ids: submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}}
	s.mu.Lock()
	work, found, err := s.loadWork(t.ctx, inv.workID, s.p.through)
	s.mu.Unlock()
	if err != nil {
		return nil, 0, err
	}
	if !found {
		_, err := agent.AdmitLocal(t.ctx, LocalAdmission{CommandID: inv.effectID, GrantID: inv.grant, WorkID: inv.workID, ParentTurnID: t.id, LocalOperation: inv.normalized.Operation, Target: *inv.normalized.Target, ArgumentsSHA256: inv.normalized.SHA256, ToolSchema: 1, MaxBytes: maxBytes, TaskBinding: t.settings.TaskBinding})
		if err != nil {
			return nil, 0, err
		}
	} else if work.dispatched || work.ended {
		return nil, work.receipt.Sequence, ErrUnavailable
	}
	started := false
	uncertain := false
	physicalSucceeded := false
	defer func() {
		if loopBusy(err) && !started {
			return
		}
		outcome, version := "released_unsent", uint64(1)
		if started {
			version = 2
			outcome = "failed"
			if physicalSucceeded {
				outcome = "succeeded"
			}
			if uncertain {
				outcome = "unknown"
			}
		}
		ending, endErr := s.EndLocal(context.Background(), LocalEnding{CommandID: uuid.NewV7(), WorkID: inv.workID, ExpectedVersion: version, Outcome: outcome})
		if endErr != nil {
			err = endErr
		} else {
			receipt = ending.Sequence
		}
		if uncertain {
			err = errLoopUnknownEffect
		}
	}()
	writer := t.writers[scratchID]
	write := inv.normalized.Operation == "file.write" || inv.normalized.Operation == "file.edit"
	if writer == nil && write {
		writer, err = r.workspace.BeginWrite(t.ctx, workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: t.origin.ContextID, AgentID: t.origin.AgentID, TaskAgentID: inv.normalized.Target.Binding.AgentID, TaskScratchID: scratchID})
		if err != nil {
			return nil, 0, err
		}
		t.writers[scratchID] = writer
	}
	var scratch workspace.Scratch
	if writer != nil {
		release, e := writer.Lease(t.ctx)
		if e != nil {
			return nil, 0, e
		}
		defer release()
		scratch = writer.Scratch()
		if scratch.ID == (uuid.UUID{}) || scratch.ContextID != t.origin.ContextID || scratch.WorkspaceID != args.WorkspaceID || scratch.ID != scratchID && scratch.ParentScratchID != scratchID {
			return nil, 0, ErrDenied
		}
	} else {
		scratch, err = r.workspace.Scratch(t.ctx, args.TaskContextID, args.WorkspaceID)
		if err != nil {
			return nil, 0, err
		}
		if scratch.ID != scratchID || scratch.ContextID != args.TaskContextID {
			return nil, 0, ErrDenied
		}
	}
	root, err := loopRootAt(scratch, prefix, args.Path)
	if err != nil {
		return nil, 0, err
	}
	defer root.root.Close()
	if reason := s.loopBoundary(t); reason != "" {
		return nil, 0, ErrDenied
	}
	if _, _, err := r.fileAuthority(t, inv); err != nil {
		return nil, 0, err
	}
	if _, err := s.BeginLocal(t.ctx, LocalDispatch{CommandID: uuid.NewV7(), WorkID: inv.workID, ExpectedVersion: 1, ArgumentsSHA256: inv.normalized.SHA256}); err != nil {
		return nil, 0, err
	}
	started = true
	if reason := s.loopBoundary(t); reason != "" {
		return nil, 0, ErrDenied
	}
	if _, _, err := r.fileAuthority(t, inv); err != nil {
		return nil, 0, err
	}
	switch inv.normalized.Operation {
	case "file.read":
		body, _, e := loopReadFile(root.root, root.name, min(args.MaxBytes, t.policy.MaxResultBytes))
		if e != nil {
			return nil, 0, e
		}
		if !utf8.Valid(body) {
			return nil, 0, ErrUnavailable
		}
		text := string(body)
		data = loopFileResult{Path: args.Path, SHA256: sha256.Sum256(body), Bytes: int64(len(body)), Text: &text}
	case "file.search":
		data, err = loopSearch(t.ctx, root, args, t.policy)
	case "file.write", "file.edit":
		data, uncertain, err = r.writeFile(t, inv, root, args)
	default:
		return nil, 0, ErrUnavailable
	}
	physicalSucceeded = err == nil
	if err == nil {
		if _, _, e := r.fileAuthority(t, inv); e != nil {
			data, err = nil, e
		}
	}
	return data, 0, err
}

var errLoopUnknownEffect = errors.New("agents: uncertain tool effect")
var errLoopFileConflict = errors.New("agents: file expectation conflict")

func (r *loopRuntime) writeFile(t *loopTurn, inv *loopInvocation, root loopFileRoot, args loopFileArgs) (any, bool, error) {
	if err := loopCheckEntries(root.root, root.name, true); err != nil {
		return nil, false, err
	}
	body, info, err := loopReadFile(root.root, root.name, maxObjectBytes)
	absent := false
	if err != nil {
		_, statErr := root.root.Lstat(root.name)
		if !errors.Is(statErr, os.ErrNotExist) {
			return nil, false, err
		}
		absent = true
	}
	previous := ledger.Digest{}
	mode := os.FileMode(0600)
	if !absent {
		previous = sha256.Sum256(body)
		mode = info.Mode().Perm()
	}
	if args.ExpectedSHA256 == "absent" {
		if !absent {
			return nil, false, errLoopFileConflict
		}
	} else if absent || args.ExpectedSHA256 != previous.String() {
		return nil, false, errLoopFileConflict
	}
	text := args.Text
	if inv.normalized.Operation == "file.edit" {
		if !utf8.Valid(body) || bytes.Count(body, []byte(args.OldText)) != 1 {
			return nil, false, errLoopFileConflict
		}
		text = strings.Replace(string(body), args.OldText, args.NewText, 1)
	}
	if len(text) > maxObjectBytes {
		return nil, false, ErrTooLarge
	}
	temporary := path.Join(path.Dir(root.name), ".dwarf-tool-"+uuid.NewV7().String())
	file, err := root.root.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, false, ErrUnavailable
	}
	defer root.root.Remove(temporary)
	_, writeErr := io.WriteString(file, text)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return nil, false, ErrUnavailable
	}
	if reason := r.service.loopBoundary(t); reason != "" {
		return nil, false, ErrDenied
	}
	if _, _, err := r.fileAuthority(t, inv); err != nil {
		return nil, false, err
	}
	if err := root.root.Rename(temporary, root.name); err != nil {
		return nil, true, ErrUnavailable
	}
	actual, _, err := loopReadFile(root.root, root.name, int64(len(text)))
	if err != nil || ledger.Digest(sha256.Sum256(actual)) != ledger.Digest(sha256.Sum256([]byte(text))) {
		return nil, true, ErrUnavailable
	}
	return loopFileResult{Path: args.Path, PreviousSHA256: previous, SHA256: sha256.Sum256(actual), Bytes: int64(len(actual))}, false, nil
}

func loopSearch(ctx context.Context, root loopFileRoot, args loopFileArgs, policy TurnPolicy) (loopSearchResult, error) {
	result := loopSearchResult{Matches: []loopSearchMatch{}}
	var walk func(string, string) error
	stopped := false
	appendMatch := func(match loopSearchMatch) {
		if int64(len(result.Matches)) >= policy.MaxSearchMatches {
			result.Incomplete, stopped = true, true
			return
		}
		candidate := append(result.Matches, match)
		if int64(len(modelJSON(candidate)))+256 > min(policy.MaxResultBytes, args.MaxBytes) {
			result.Incomplete, stopped = true, true
			return
		}
		result.Matches = candidate
	}
	walk = func(name, display string) error {
		if stopped {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := loopCheckEntries(root.root, name, false); err != nil {
			return err
		}
		dir, err := root.root.Open(name)
		if err != nil {
			return ErrUnavailable
		}
		// The retained directory window shares the turn's bounded scratch
		// buffer. A larger configured examination cap never permits an
		// unbounded ReadDir allocation.
		remaining := min(policy.MaxSearchEntries, 4096) - result.Examined
		if remaining <= 0 {
			dir.Close()
			result.Incomplete, stopped = true, true
			return nil
		}
		entries, readErr := dir.ReadDir(int(remaining) + 1)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
			return ErrUnavailable
		}
		if int64(len(entries)) > remaining {
			entries = entries[:remaining]
			result.Incomplete = true
		}
		slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, entry := range entries {
			if stopped {
				break
			}
			if !loopSafePath(entry.Name()) || entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				continue
			}
			if result.Examined >= min(policy.MaxSearchEntries, 4096) {
				result.Incomplete, stopped = true, true
				break
			}
			result.Examined++
			child, visible := path.Join(name, entry.Name()), path.Join(display, entry.Name())
			info, err := root.root.Lstat(child)
			if err != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				continue
			}
			if args.Mode == "path" && strings.Contains(visible, args.Query) {
				appendMatch(loopSearchMatch{Path: visible})
			}
			if info.IsDir() {
				if err := walk(child, visible); err != nil {
					return err
				}
				continue
			}
			if args.Mode != "text" || !info.Mode().IsRegular() {
				continue
			}
			body, _, err := loopReadFile(root.root, child, min(args.MaxBytes, policy.MaxResultBytes))
			if errors.Is(err, ErrTooLarge) {
				result.Incomplete = true
				continue
			}
			if err != nil {
				return err
			}
			if !utf8.Valid(body) {
				continue
			}
			for line, text := range strings.Split(string(body), "\n") {
				if strings.Contains(text, args.Query) {
					value := text
					appendMatch(loopSearchMatch{Path: visible, Line: line + 1, Text: &value})
					if stopped {
						break
					}
				}
			}
		}
		return nil
	}
	err := walk(root.name, args.Path)
	slices.SortFunc(result.Matches, func(a, b loopSearchMatch) int {
		if order := strings.Compare(a.Path, b.Path); order != 0 {
			return order
		}
		if a.Line < b.Line {
			return -1
		}
		if a.Line > b.Line {
			return 1
		}
		return 0
	})
	return result, err
}

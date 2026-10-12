package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Workspace is the durable registration and its current approved inputs.
type Workspace struct {
	ID                      uuid.UUID
	CommonDir, ObjectFormat string
	Revision                int64
	InputsSHA256            ledger.Digest
	PinID                   uuid.UUID
}

type workspaceState struct {
	Workspace
	inputs                    Proposal
	refs                      []ledger.Digest
	inputsSeq                 uint64
	inputsError, inputsDetail string
}

// Register is the owner's approval of a proposal. The caller authenticates the
// owner. Live observed settings are rechecked; the five reviewed files are used
// exactly as approved. The registration and its initial body pin are one commit.
func (s *Service) Register(ctx context.Context, p Proposal) (Workspace, error) {
	if err := s.start(); err != nil {
		return Workspace{}, err
	}
	defer s.ops.Done()
	if err := s.lockInputs(ctx); err != nil {
		return Workspace{}, err
	}
	defer func() { <-s.inputsLock }()
	if err := s.freshProposal(ctx, p); err != nil {
		return Workspace{}, err
	}
	info, err := os.Stat(p.CommonDir)
	if err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	for _, w := range s.workspaces {
		other, err := os.Stat(w.CommonDir)
		if w.CommonDir == p.CommonDir || (err == nil && os.SameFile(info, other)) {
			s.mu.Unlock()
			return Workspace{}, ErrExists
		}
	}
	id, err := s.workspaceID()
	s.mu.Unlock()
	if err != nil {
		return Workspace{}, err
	}
	// Validate approved bytes before installing the marker or publishing bodies.
	if _, _, err := encodeCaptureInputs(p, "capture-rules/1"); err != nil {
		return Workspace{}, err
	}
	registered, err := (ledger.WorkspaceRegistered{WorkspaceID: id, CommonDir: p.CommonDir, ObjectFormat: p.ObjectFormat}).Encode()
	if err != nil {
		return Workspace{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.git.ensureMarker(ctx, s.colony, p, s.store.Colony().String()); err != nil {
		return Workspace{}, err
	}
	pin, refs, err := s.publishInputs(ctx, id, 1, p)
	if err != nil {
		return Workspace{}, err
	}
	pinned, err := pin.Encode()
	if err != nil {
		return Workspace{}, err
	}
	bodyPin, err := (ledger.BodyPin{PinID: pin.PinID}).Encode()
	if err != nil {
		return Workspace{}, err
	}
	drafts := []ledger.Draft{
		{Type: ledger.TypeWorkspaceRegistered, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: registered},
		{Type: ledger.TypeCaptureInputsPinned, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: pinned},
		{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: bodyPin},
	}
	if err := s.append(ctx, drafts); err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	result := s.workspaces[id].Workspace
	s.mu.Unlock()
	return result, nil
}

// UpdateInputs atomically replaces the approved revision and releases its old
// pin. expectedRevision is a compare-and-swap. Already-running captures keep
// their immutable input snapshot; later captures use the acknowledged revision.
func (s *Service) UpdateInputs(ctx context.Context, workspaceID uuid.UUID, expectedRevision int64, p Proposal) (Workspace, error) {
	if err := s.start(); err != nil {
		return Workspace{}, err
	}
	defer s.ops.Done()
	if !validID(workspaceID) {
		return Workspace{}, ErrInvalid
	}
	if err := s.lockInputs(ctx); err != nil {
		return Workspace{}, err
	}
	defer func() { <-s.inputsLock }()
	s.mu.Lock()
	w, ok := s.workspaces[workspaceID]
	s.mu.Unlock()
	if !ok {
		return Workspace{}, ErrUnknownWorkspace
	}
	if err := s.freshProposal(ctx, p); err != nil {
		return Workspace{}, err
	}
	if p.CommonDir != w.CommonDir || p.ObjectFormat != w.ObjectFormat {
		return Workspace{}, ErrStaleProposal
	}
	if expectedRevision != w.Revision {
		return Workspace{}, ErrRevision
	}
	if w.Revision == math.MaxInt64 {
		return Workspace{}, fmt.Errorf("%w: revision exhausted", ErrRevision)
	}
	pin, refs, err := s.publishInputs(ctx, workspaceID, w.Revision+1, p)
	if err != nil {
		return Workspace{}, err
	}
	pinned, err := pin.Encode()
	if err != nil {
		return Workspace{}, err
	}
	bodyPin, err := (ledger.BodyPin{PinID: pin.PinID}).Encode()
	if err != nil {
		return Workspace{}, err
	}
	unpin, err := (ledger.BodyUnpin{PinID: w.PinID}).Encode()
	if err != nil {
		return Workspace{}, err
	}
	if err := s.append(ctx, []ledger.Draft{
		{Type: ledger.TypeCaptureInputsPinned, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: pinned},
		{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorOwner, BodyRefs: refs, Payload: bodyPin},
		{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: unpin},
	}); err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	result := s.workspaces[workspaceID].Workspace
	s.mu.Unlock()
	return result, nil
}

func (s *Service) lockInputs(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.inputsLock <- struct{}{}:
	}
	s.mu.Lock()
	poisoned := s.poisoned
	s.mu.Unlock()
	if poisoned {
		<-s.inputsLock
		return ErrPoisoned
	}
	return nil
}

func (s *Service) freshProposal(ctx context.Context, p Proposal) error {
	fresh, err := s.git.inspect(ctx, p.CommonDir)
	if err != nil {
		return err
	}
	if fresh.CommonDir != p.CommonDir || fresh.GitVersion != p.GitVersion || fresh.ObjectFormat != p.ObjectFormat || fresh.Normalization != p.Normalization {
		return ErrStaleProposal
	}
	return nil
}

// workspaceID is called under mu; input operations also hold inputsLock.
func (s *Service) workspaceID() (uuid.UUID, error) {
	for range 3 {
		id := s.deps.newID()
		if !validID(id) {
			continue
		}
		collision := false
		for old := range s.workspaces {
			if shortID(old) == shortID(id) {
				collision = true
				break
			}
		}
		if collision {
			continue
		}
		_, err := os.Lstat(filepath.Join(s.colony, "workspaces", shortID(id)))
		if os.IsNotExist(err) {
			return id, nil
		}
		if err != nil {
			return uuid.UUID{}, err
		}
	}
	return uuid.UUID{}, fmt.Errorf("%w: workspace names exhausted", ErrInvalid)
}

func (s *Service) publishInputs(ctx context.Context, workspaceID uuid.UUID, revision int64, p Proposal) (ledger.CaptureInputsPinned, []ledger.Digest, error) {
	var pin ledger.CaptureInputsPinned
	document, sum, err := encodeCaptureInputs(p, "capture-rules/1")
	if err != nil {
		return pin, nil, err
	}
	for _, f := range []HostFile{p.LocalExcludes, p.GlobalExcludes, p.LocalAttributes, p.GlobalAttributes, p.SystemAttributes, {Present: true, Bytes: document}} {
		if f.Present {
			if _, _, err := s.store.PublishBody(ctx, bytes.NewReader(f.Bytes)); err != nil {
				return pin, nil, err
			}
		}
	}
	d, err := decodeCaptureInputs(document)
	if err != nil {
		return pin, nil, err
	}
	s.mu.Lock()
	id := s.deps.newID()
	s.mu.Unlock()
	pin = ledger.CaptureInputsPinned{WorkspaceID: workspaceID, Revision: revision, CaptureInputsSHA256: ledger.Digest(sum), PinID: id}
	return pin, documentRefs(pin.CaptureInputsSHA256, d), nil
}

func documentRefs(document ledger.Digest, d inputsDocument) []ledger.Digest {
	refs := []ledger.Digest{document}
	for _, value := range []string{d.LocalExcludes, d.GlobalExcludes, d.LocalAttributes, d.GlobalAttributes, d.SystemAttributes} {
		if value != "" {
			digest, _ := ledger.ParseDigest(value)
			refs = append(refs, digest)
		}
	}
	slices.SortFunc(refs, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(refs)
}

func (s *Service) inputsDir(w workspaceState) string {
	return filepath.Join(s.colony, "workspaces", shortID(w.ID), "inputs", strconv.FormatInt(w.Revision, 10))
}

func (s *Service) captureScratch(w workspaceState, scratch Scratch) captureScratch {
	return captureScratch{Worktree: scratch.worktree(), Inputs: w.inputs, Revision: uint64(w.Revision), InputsDir: s.inputsDir(w), TmpDir: filepath.Join(s.colony, "workspaces", shortID(w.ID), "tmp"), ColonyID: s.store.Colony().String()}
}

// loadInputs reads only a current revision. Superseded bodies are never read.
func (s *Service) loadInputs(ctx context.Context, w workspaceState) (workspaceState, error) {
	unavailable := func(digest ledger.Digest, cause error) (workspaceState, error) {
		w.inputsError = "inputs_unavailable"
		w.inputsDetail = fmt.Sprintf("body %s: %v", digest, cause)
		return w, nil
	}
	bad := func(cause string) (workspaceState, error) {
		return w, &InconsistentError{Seq: w.inputsSeq, Detail: cause}
	}
	body, err := s.store.ReadBody(ctx, w.InputsSHA256)
	if err != nil {
		return unavailable(w.InputsSHA256, err)
	}
	var header struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		return bad("malformed capture inputs: " + err.Error())
	}
	if (header.Format != "" && header.Format != "dwarf-harness/capture-inputs") || (header.Version != 0 && header.Version != 1) {
		return unavailable(w.InputsSHA256, fmt.Errorf("unsupported capture inputs format %q version %d", header.Format, header.Version))
	}
	d, err := decodeCaptureInputs(body)
	if err != nil {
		return bad("malformed capture inputs: " + err.Error())
	}
	if !slices.Equal(documentRefs(w.InputsSHA256, d), w.refs) {
		return bad("capture inputs body_refs differ from document")
	}
	if d.ObjectFormat != w.ObjectFormat {
		return bad("capture inputs object format differs from workspace")
	}
	p := Proposal{CommonDir: w.CommonDir, GitVersion: d.GitVersion, ObjectFormat: d.ObjectFormat, Normalization: Normalization{AutoCRLF: d.AutoCRLF, EOL: d.EOL, FileMode: d.FileMode, IgnoreCase: d.IgnoreCase, LongPaths: d.LongPaths, PrecomposeUnicode: d.PrecomposeUnicode, Symlinks: d.Symlinks}}
	for _, file := range []struct {
		digest string
		dst    *HostFile
	}{{d.LocalExcludes, &p.LocalExcludes}, {d.GlobalExcludes, &p.GlobalExcludes}, {d.LocalAttributes, &p.LocalAttributes}, {d.GlobalAttributes, &p.GlobalAttributes}, {d.SystemAttributes, &p.SystemAttributes}} {
		if file.digest == "" {
			continue
		}
		digest, err := ledger.ParseDigest(file.digest)
		if err != nil {
			return bad(err.Error())
		}
		b, err := s.store.ReadBody(ctx, digest)
		if err != nil {
			return unavailable(digest, err)
		}
		if len(b) > maxInputBytes {
			return bad("capture input file exceeds 1 MiB")
		}
		*file.dst = HostFile{Present: true, Bytes: b}
	}
	if d.Rules != "capture-rules/1" {
		return unavailable(w.InputsSHA256, errors.New("unsupported capture rules "+d.Rules))
	}
	w.inputs, w.inputsError, w.inputsDetail = p, "", ""
	return w, nil
}

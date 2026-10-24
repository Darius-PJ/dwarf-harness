package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// ReadBody allocates the listed file size plus one growth-detection byte. Its
// allocation is bounded by the store, not by a caller's claimed artifact size.
const artifactReadBufferBytes = int64(ledgerstore.MaxBodyBytes) + 1

type scopedResult struct {
	receipt ScopedReceipt
	page    PrivatePage
	body    []byte
}

func agentError(err error) error {
	if errors.Is(err, ErrIntegrity) {
		return ErrIntegrity
	}
	return err
}

func (a *Agent) WritePrivate(ctx context.Context, request ScopedPrivateWrite) (ScopedReceipt, error) {
	return a.command(ctx, "private.write", request)
}

func (a *Agent) ReadPrivate(ctx context.Context, request ScopedPrivateRead) (ScopedPrivatePage, error) {
	if a == nil || a.service == nil {
		return ScopedPrivatePage{}, ErrNotBound
	}
	result, err := a.service.executeScopedResult(ctx, scopedCaller{actor: "agent", binding: Binding{a.ids.agent, a.ids.context}, fixed: a.ids}, "private.read", request)
	if err != nil {
		return ScopedPrivatePage{}, agentError(err)
	}
	return ScopedPrivatePage{Receipt: result.receipt, Page: result.page}, nil
}

func (a *Agent) ReadArtifact(ctx context.Context, request ArtifactRead) (ArtifactPage, error) {
	if a == nil || a.service == nil {
		return ArtifactPage{}, ErrNotBound
	}
	result, err := a.service.executeScopedResult(ctx, scopedCaller{actor: "agent", binding: Binding{a.ids.agent, a.ids.context}, fixed: a.ids}, "artifact.read", request)
	if err != nil {
		return ArtifactPage{}, agentError(err)
	}
	return ArtifactPage{Receipt: result.receipt, Bytes: result.body}, nil
}

func (a *Agent) Snapshot(ctx context.Context, request ScopedSnapshotLookup) (Snapshot, error) {
	if a == nil || a.service == nil {
		return Snapshot{}, ErrNotBound
	}
	s := a.service
	done, err := s.scopedQuery(ctx, &a.ids)
	if err != nil {
		return Snapshot{}, agentError(err)
	}
	defer done()
	var slots []string
	switch request.Scope {
	case "agent":
		slots = []string{"instructions", "memory"}
	case "context":
		slots = []string{"continuation", "handoff", "memory", "transcript"}
	default:
		return Snapshot{}, ErrDenied
	}
	binding := Binding{a.ids.agent, a.ids.context}
	target := ledger.Resource{Kind: "context", Binding: ledger.Binding(binding), Scope: request.Scope, Slots: slots}
	if _, err := s.authorize(ctx, originOf(a.ids), request.GrantID, "context.inspect", []ledger.Resource{target}, nil, 0, s.p.through); err != nil {
		return Snapshot{}, agentError(err)
	}
	state := s.p.scopes[keyFor(a.ids, request.Scope)]
	if state == nil {
		return Snapshot{}, ErrDenied
	}
	result := s.snapshotLocked(a.ids, ReadBinding{Binding: binding, Scope: request.Scope})
	if request.Scope == "context" {
		result.Issues = slices.Clone(state.issues)
		if len(s.p.scopes[keyFor(a.ids, "agent")].issues) != 0 {
			result.Issues = append(result.Issues, IntegrityIssue{ReadBinding: ReadBinding{Binding: binding, Scope: "agent"}, Reason: "agent_integrity"})
		}
	}
	return result, nil
}

func (a *Agent) ScratchBinding(ctx context.Context, request ScopedScratchLookup) (ScratchBinding, error) {
	if a == nil || a.service == nil {
		return ScratchBinding{}, ErrNotBound
	}
	if !validID(request.WorkspaceID) {
		return ScratchBinding{}, ErrInvalid
	}
	s := a.service
	done, err := s.scopedQuery(ctx, &a.ids)
	if err != nil {
		return ScratchBinding{}, agentError(err)
	}
	defer done()
	binding := Binding{a.ids.agent, a.ids.context}
	target := ledger.Resource{Kind: "workspace", WorkspaceID: request.WorkspaceID, Binding: ledger.Binding(binding), PathPrefix: ""}
	if _, err := s.authorize(ctx, originOf(a.ids), request.GrantID, "file.read", []ledger.Resource{target}, nil, 0, s.p.through); err != nil {
		return ScratchBinding{}, agentError(err)
	}
	if err := s.scopeIssuesLocked(a.ids, "context"); err != nil {
		return ScratchBinding{}, agentError(err)
	}
	result := s.scratchBindingLocked(ScratchLookup{Binding: binding, WorkspaceID: request.WorkspaceID, ExpectedScratchID: request.ExpectedScratchID})
	if result.State == "mismatch" {
		return ScratchBinding{}, ErrDenied
	}
	return result, nil
}

// ReadArtifact is the owner's passive, verified body query. Its process slot
// reserves a possible artifactReadBufferBytes allocation without requiring book
// enrollment, consuming a unit, or writing a command.
func (s *Service) ReadArtifact(ctx context.Context, origin Binding, request ArtifactLookup) ([]byte, error) {
	if s == nil {
		return nil, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, origin, false)
	if err != nil {
		return nil, err
	}
	if !validResource(ledger.Resource{Kind: "artifact", Artifact: request.Artifact}) || request.MaxBytes < 0 {
		return nil, ErrInvalid
	}
	if request.Artifact.Bytes > request.MaxBytes || request.MaxBytes > s.limits.MaxScopedBytes {
		return nil, ErrTooLarge
	}
	sub := ledger.ScopedSubmission{Actor: "owner", Origin: originOf(ids)}
	s.mu.Lock()
	err = s.refreshScopedLocked(ctx, sub)
	if err == nil {
		err = s.verifyArtifactSource(ctx, request.Artifact, s.p.through)
	}
	if err == nil && s.preparing >= s.limits.MaxConcurrentWrites {
		err = &BusyError{Reason: "preparing"}
	}
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.preparing++
	s.mu.Unlock()
	defer s.releasePreparation()
	if s.deps.beforeRead != nil {
		if err := s.deps.beforeRead(origin, "artifact"); err != nil {
			return nil, err
		}
	}
	data, readErr := s.readArtifactBytes(ctx, request.Artifact)
	if s.deps.afterRead != nil {
		s.deps.afterRead(origin, "artifact")
	}
	s.mu.Lock()
	err = s.refreshScopedLocked(ctx, sub)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return data, readErr
}

func (s *Service) readArtifactBytes(ctx context.Context, ref ledger.ArtifactRef) ([]byte, error) {
	data, err := s.store.ReadBody(ctx, ref.SHA256)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != ref.Bytes {
		return nil, fmt.Errorf("%w: artifact size mismatch", ledgerstore.ErrBodyCorrupt)
	}
	return data, nil
}

func privateReadInput(sub ledger.ScopedSubmission) PrivateRead {
	read := sub.Arguments.(ledger.PrivateReadArguments).Read
	return PrivateRead{ReadBinding: ReadBinding{Binding: bindingOf(sub.Origin), Scope: read.Scope}, Slot: read.Slot, Revision: uint64(read.Revision), Cursor: uint64(read.Cursor), MaxBytes: read.MaxBytes}
}

func selectionFromUsage(usage ledger.RuntimeLocalUsed) privateSelection {
	selection := privateSelection{heads: make([]Head, len(usage.Selected)), bytes: usage.Bytes, present: len(usage.Selected) != 0 || usage.Read.Slot == "transcript" && usage.Read.Cursor != 0}
	for i, object := range usage.Selected {
		selection.heads[i] = Head{SHA256: object.SHA256, Bytes: object.Bytes, Revision: uint64(object.Revision), Sequence: uint64(object.Sequence)}
	}
	if usage.NextCursor != nil {
		selection.nextCursor = uint64(*usage.NextCursor)
	}
	return selection
}

func (selection privateSelection) selectedObjects() []ledger.SelectedObject {
	objects := make([]ledger.SelectedObject, len(selection.heads))
	for i, head := range selection.heads {
		objects[i] = ledger.SelectedObject{SHA256: head.SHA256, Bytes: head.Bytes, Revision: int64(head.Revision), Sequence: int64(head.Sequence)}
	}
	return objects
}

func (selection privateSelection) bufferBytes() (int64, error) {
	// readObject retains one growth-detection byte for every selected object,
	// including an empty, present head.
	if selection.bytes > math.MaxInt64-int64(len(selection.heads)) {
		return 0, ErrTooLarge
	}
	return selection.bytes + int64(len(selection.heads)), nil
}

// This phase runs outside mu and inside its finite preparation reservation.
// Scope gates are held by the caller where private state must be serialized.
func (s *Service) prepareScopedIO(ctx context.Context, input *scopedInput) (scopedResult, error) {
	sub := input.sub
	binding := bindingOf(sub.Origin)
	ids := submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}
	switch sub.Operation {
	case "task.create":
		retained := bytes.Clone(input.body)
		description := sub.Arguments.(ledger.TaskCreateArguments).Description
		if int64(len(retained)) != description.Bytes || ledger.Digest(sha256.Sum256(retained)) != description.SHA256 {
			return scopedResult{}, ErrInvalid
		}
		if s.deps.beforePublish != nil {
			if err := s.deps.beforePublish(binding, "task"); err != nil {
				return scopedResult{}, err
			}
		}
		digest, size, err := s.store.PublishBody(ctx, bytes.NewReader(retained))
		if err != nil {
			return scopedResult{}, err
		}
		if digest != description.SHA256 || size != description.Bytes {
			return scopedResult{}, ErrInconsistent
		}
		if s.deps.afterPublish != nil {
			s.deps.afterPublish(binding, "task")
		}
	case "private.write":
		changes := sub.Arguments.(ledger.PrivateWriteArguments).Changes
		for i := range input.changes {
			input.changes[i].data = bytes.Clone(input.changes[i].data)
			change := changes[i]
			if int64(len(input.changes[i].data)) != change.Bytes {
				return scopedResult{}, ErrInvalid
			}
			if change.Mode != "clear" {
				digest, err := hashObject(input.changes[i].data)
				if err != nil || digest != change.SHA256 {
					return scopedResult{}, ErrInvalid
				}
			}
		}
		if err := s.inspectLive(ctx, ids, sub.Target.Scope); err != nil {
			return scopedResult{}, err
		}
		path, err := s.scopePath(ids, sub.Target.Scope)
		if err != nil {
			return scopedResult{}, err
		}
		if s.deps.beforePublish != nil {
			if err := s.deps.beforePublish(binding, sub.Target.Scope); err != nil {
				return scopedResult{}, err
			}
		}
		for _, change := range input.changes {
			if change.mode != "clear" {
				if _, err := path.publishObject(change.data); err != nil {
					return scopedResult{}, err
				}
			}
		}
		if s.deps.afterPublish != nil {
			s.deps.afterPublish(binding, sub.Target.Scope)
		}
		for _, change := range changes {
			if change.Mode != "clear" {
				if _, err := readObject(path, Head{SHA256: change.SHA256, Bytes: change.Bytes}); err != nil {
					return scopedResult{}, err
				}
			}
		}
	case "private.read", "artifact.read":
		scope := "artifact"
		if sub.Operation == "private.read" {
			scope = sub.Target.Scope
		}
		if s.deps.beforeRead != nil {
			if err := s.deps.beforeRead(binding, scope); err != nil {
				return scopedResult{}, err
			}
		}
		var result scopedResult
		var err error
		if sub.Operation == "private.read" {
			path, pathErr := s.scopePath(ids, scope)
			if pathErr != nil {
				err = pathErr
			} else {
				result.page, err = s.readPrivateSelection(path, privateReadInput(sub), input.selection)
			}
		} else {
			result.body, err = s.readArtifactBytes(ctx, sub.Arguments.(ledger.ArtifactReadArguments).Artifact)
		}
		if s.deps.afterRead != nil {
			s.deps.afterRead(binding, scope)
		}
		return result, err
	case "turn.start":
		if err := s.inspectLive(ctx, ids, "agent"); err != nil {
			return scopedResult{}, err
		}
		if err := s.inspectLive(ctx, ids, "context"); err != nil {
			return scopedResult{}, err
		}
	}
	return scopedResult{}, nil
}

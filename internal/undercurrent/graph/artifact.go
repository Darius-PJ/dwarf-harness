package graph

import (
	"context"
	"crypto/sha256"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ArtifactRequest is answered by Core against a registered immutable repository.
// ListOnly returns presence without bytes; Paths names sorted exact entries,
// never a directory prefix, pathspec, symlink traversal or mutable worktree.
type ArtifactRequest struct {
	WorkspaceID             uuid.UUID
	Tree, AfterPath         string
	MaxBytes, WorkAllowance uint64
	ListOnly                bool
	Paths                   []string
}
type Artifact struct {
	Path, Mode, GitOID string
	SHA256             ledger.Digest
	Bytes              []byte
	Reason             string
}
type ArtifactBatch struct {
	Entries  []Artifact
	NextPath string
	Complete bool
	WorkUsed uint64
	Reason   string
}

type generationState struct {
	births   map[string]int64
	accepted int
	tree     string
	memory   uint64
}
type generationWalk struct {
	generationState
	workspace    uuid.UUID
	registration int64
	history      []acceptance
	initial      bool
	after        string
	stalls       int
}

func newGenerationWalk(workspace uuid.UUID, registered ledger.RecordRef, history []acceptance, cached generationState) *generationWalk {
	g := &generationWalk{generationState: cached, workspace: workspace, registration: registered.Seq, history: history}
	if g.births == nil {
		g.births = make(map[string]int64)
		g.initial = true
		g.tree = history[0].predecessor
	}
	return g
}
func (g *generationWalk) done() bool { return !g.initial && g.accepted == len(g.history) }

func validArtifactOID(oid string, length int) bool {
	if len(oid) != length || length != 40 && length != 64 {
		return false
	}
	for _, c := range oid {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// The callback's report is not authority to exceed the reservation, byte cap,
// cursor or immutable-input binding. Invalid output is discarded as unknown.
func readArtifactPage(ctx context.Context, reader func(context.Context, ArtifactRequest) (ArtifactBatch, error), request ArtifactRequest) (ArtifactBatch, string, error) {
	if reader == nil {
		return ArtifactBatch{}, ledger.ReasonSourceUnavailable, nil
	}
	batch, err := reader(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			return ArtifactBatch{}, "", ctx.Err()
		}
		return ArtifactBatch{}, ledger.ReasonSourceUnavailable, nil
	}
	if batch.WorkUsed > request.WorkAllowance || len(batch.Entries) > maxItems || batch.NextPath < request.AfterPath || batch.NextPath != "" && !pathValid(batch.NextPath) {
		return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
	}
	minimum, bytes := uint64(0), uint64(0)
	previous := request.AfterPath
	for _, entry := range batch.Entries {
		if !pathValid(entry.Path) || entry.Path <= previous || entry.Path > batch.NextPath || !validArtifactOID(entry.GitOID, len(request.Tree)) {
			return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
		}
		previous = entry.Path
		if len(request.Paths) != 0 {
			if _, found := slices.BinarySearch(request.Paths, entry.Path); !found {
				return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
			}
		}
		if entry.Mode != "100644" && entry.Mode != "100755" && entry.Mode != "120000" && entry.Mode != "160000" {
			return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
		}
		minimum++
		if request.ListOnly {
			if entry.Bytes != nil || entry.SHA256 != (ledger.Digest{}) {
				return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
			}
		} else if entry.Reason != "" {
			if entry.Bytes != nil || entry.SHA256 != (ledger.Digest{}) || entry.Reason != ledger.ReasonUnsupportedSyntax && entry.Reason != ledger.ReasonSourceUnavailable && entry.Reason != ledger.ReasonHelperLimit {
				return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
			}
		} else {
			if entry.Mode != "100644" && entry.Mode != "100755" || len(entry.Bytes) > maxBlob || uint64(len(entry.Bytes)) > request.MaxBytes-bytes {
				return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
			}
			bytes += uint64(len(entry.Bytes))
			minimum += (uint64(len(entry.Bytes)) + 255) / 256
			if ledger.Digest(sha256.Sum256(entry.Bytes)) != entry.SHA256 {
				return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
			}
		}
	}
	if len(request.Paths) != 0 {
		answered := uint64(0)
		for _, path := range request.Paths {
			if path > request.AfterPath && path <= batch.NextPath {
				answered++
			}
		}
		if answered > minimum {
			minimum = answered
		}
		last := request.Paths[len(request.Paths)-1]
		if batch.Complete && last > request.AfterPath && batch.NextPath != last {
			return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
		}
		if batch.NextPath != request.AfterPath {
			if _, found := slices.BinarySearch(request.Paths, batch.NextPath); !found {
				return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
			}
		}
	}
	if batch.NextPath > request.AfterPath && minimum == 0 {
		minimum = 1
	}
	if minimum > batch.WorkUsed {
		return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
	}
	switch batch.Reason {
	case "", ledger.ReasonWorkExhausted, ledger.ReasonTemporaryCap:
		return batch, "", nil
	case ledger.ReasonSourceUnavailable, ledger.ReasonHelperLimit:
		return ArtifactBatch{}, batch.Reason, nil
	default:
		return ArtifactBatch{}, ledger.ReasonHelperLimit, nil
	}
}

func (g *generationWalk) step(ctx context.Context, reader func(context.Context, ArtifactRequest) (ArtifactBatch, error), allowance int64, memoryAvailable uint64) (int64, string, error) {
	request := ArtifactRequest{WorkspaceID: g.workspace, Tree: g.tree, AfterPath: g.after, MaxBytes: maxFrame, WorkAllowance: uint64(allowance), ListOnly: true}
	birth := g.registration
	if !g.initial {
		accepted := g.history[g.accepted]
		if g.tree != accepted.predecessor {
			return 0, ledger.ReasonSourceUnavailable, nil
		}
		request.Tree, birth = accepted.tree, accepted.ref.Seq
		start, _ := slices.BinarySearch(accepted.paths, g.after)
		for start < len(accepted.paths) && accepted.paths[start] <= g.after {
			start++
		}
		request.Paths = accepted.paths[start:min(start+maxItems, len(accepted.paths))]
	}
	batch, reason, err := readArtifactPage(ctx, reader, request)
	if err != nil || reason != "" {
		return int64(batch.WorkUsed), reason, err
	}
	if allowance == quantum && batch.NextPath == g.after && !batch.Complete {
		g.stalls++
	} else {
		g.stalls = 0
	}
	if g.stalls >= 2 {
		return int64(batch.WorkUsed), ledger.ReasonHelperLimit, nil
	}
	for _, entry := range batch.Entries {
		if _, exists := g.births[entry.Path]; !exists {
			cost := uint64(512 + len(entry.Path))
			if cost > memoryAvailable || cost > stagingLimit-g.memory {
				return int64(batch.WorkUsed), ledger.ReasonTemporaryCap, nil
			}
			memoryAvailable -= cost
			g.memory += cost
			g.births[entry.Path] = birth
		}
	}
	if !g.initial {
		i := 0
		for _, path := range request.Paths {
			if path > batch.NextPath {
				break
			}
			if i < len(batch.Entries) && batch.Entries[i].Path == path {
				i++
			} else {
				if _, exists := g.births[path]; exists {
					g.memory -= uint64(512 + len(path))
				}
				delete(g.births, path)
			}
		}
	}
	g.after = batch.NextPath
	if batch.Complete {
		if g.initial {
			g.initial, g.after = false, ""
		} else {
			accepted := g.history[g.accepted]
			if g.after == accepted.paths[len(accepted.paths)-1] {
				g.accepted++
				g.tree, g.after = accepted.tree, ""
			}
		}
	}
	return int64(batch.WorkUsed), "", nil
}

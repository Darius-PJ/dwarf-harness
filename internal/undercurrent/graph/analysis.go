package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/law"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

const (
	helperProtocol = "graph-helper/v1"
	maxFrame       = 8 << 20
	maxBlob        = 1 << 20
	maxItems       = 10000
)

// sourceAdapter matches the helpers' case-sensitive source suffixes. JSON is
// TypeScript metadata, not a source whose coverage the helper must supply.
func sourceAdapter(name string) string {
	switch path.Ext(name) {
	case ".py":
		return ledger.AdapterPython
	case ".sql":
		return ledger.AdapterSQL
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return ledger.AdapterTypeScript
	case ".go":
		return "go"
	default:
		return ledger.AdapterOpaque
	}
}

// CorpusArtifact carries only immutable regular blob bytes and a parent-derived
// generation key. Helpers may not invent artifact identities.
type CorpusArtifact struct {
	Path   string         `json:"path"`
	Key    ledger.NodeKey `json:"artifact_key"`
	SHA256 ledger.Digest  `json:"sha256"`
	Bytes  []byte         `json:"bytes_base64"`
}

// AnalysisRequest is a single bounded, record-free corpus analysis. The caller
// owns durable reservations, source-history generations and later publication.
type AnalysisRequest struct {
	AnalysisKey         ledger.Digest
	Source              ledger.GraphSource
	ConfigurationSHA256 ledger.Digest
	Corpus              []CorpusArtifact
	Allowance           int64
}

// Cost describes observed invocation resources, not hard native CPU/RSS bounds.
type Cost struct {
	WallNS      int64 `json:"wall_ns"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}

// AnalysisResult never treats a missing or failed adapter as complete-empty.
// A scope-level limit discards all of that invocation's partial causal output.
type AnalysisResult struct {
	NextCursor ledger.GraphCursor     `json:"next_cursor"`
	WorkUsed   int64                  `json:"work_used"`
	Nodes      []ledger.GraphNode     `json:"nodes"`
	Edges      []ledger.GraphEdge     `json:"edges"`
	Coverage   []ledger.CoverageEntry `json:"coverage"`
	Cost       Cost                   `json:"cost"`
}

func unknown(r AnalysisRequest, adapter, reason string, work int64) AnalysisResult {
	return AnalysisResult{WorkUsed: work, Nodes: []ledger.GraphNode{}, Edges: []ledger.GraphEdge{}, Coverage: []ledger.CoverageEntry{{Scope: workspaceScope(r.Source.WorkspaceID), Adapter: adapter, State: ledger.CoverageUnknown, Reasons: []string{reason}}}}
}
func covered(k ledger.NodeKey, adapter, reason string) ledger.CoverageEntry {
	c := ledger.CoverageEntry{Scope: keyScope(k), Adapter: adapter, State: ledger.CoverageComplete, Reasons: []string{}}
	if reason != "" {
		c.State = ledger.CoverageUnknown
		c.Reasons = []string{reason}
	}
	return c
}

var errCorpusLimit = errors.New("graph corpus limit")

func validateRequest(r AnalysisRequest) error {
	if !sourceValid(r.Source) || r.AnalysisKey == (ledger.Digest{}) || r.ConfigurationSHA256 == (ledger.Digest{}) || r.Allowance < 1 || r.Allowance > maxItems {
		return ErrInvalid
	}
	if len(r.Corpus) > maxItems {
		return errCorpusLimit
	}
	var total int
	for i, a := range r.Corpus {
		if len(a.Bytes) > maxBlob || len(a.Bytes) > maxFrame-total {
			return errCorpusLimit
		}
		if !pathValid(a.Path) || !keyValid(a.Key) || a.Key.Kind != ledger.NodeKindArtifact || a.Key.Namespace != ledger.NodeNamespaceWorkspace || a.Key.OriginID != r.Source.WorkspaceID || a.SHA256 != ledger.Digest(sha256.Sum256(a.Bytes)) || i > 0 && r.Corpus[i-1].Path >= a.Path {
			return ErrInvalid
		}
		var pair []json.RawMessage
		if err := json.Unmarshal([]byte(a.Key.LocalKey), &pair); err != nil || len(pair) != 2 {
			return ErrInvalid
		}
		var birth int64
		var p string
		if json.Unmarshal(pair[0], &birth) != nil || json.Unmarshal(pair[1], &p) != nil || p != a.Path {
			return ErrInvalid
		}
		k, err := ArtifactKey(a.Key.OriginID, birth, p)
		if err != nil || k != a.Key {
			return ErrInvalid
		}
		total += len(a.Bytes)
	}
	return nil
}

func dependency(from ledger.NodeKey, a CorpusArtifact, adapter, reference string, configuration ledger.Digest) ledger.GraphEdge {
	typ := string(law.StaticDependency)
	return ledger.GraphEdge{EdgeID: ledger.EdgeID(from, a.Key, typ), From: from, To: a.Key, Type: typ, Geometry: ledger.GeometryCoupling, WeightPPM: 1000000, Evidence: ledger.EdgeEvidence{Artifact: a.Key, ArtifactSHA256: a.SHA256, Adapter: adapter, ConfigurationSHA256: configuration, Reference: reference}}
}
func sortResult(r *AnalysisResult) {
	slices.SortFunc(r.Nodes, func(a, b ledger.GraphNode) int { return bytes.Compare(keyBytes(a.Key), keyBytes(b.Key)) })
	slices.SortFunc(r.Edges, func(a, b ledger.GraphEdge) int {
		if order := bytes.Compare(a.EdgeID[:], b.EdgeID[:]); order != 0 {
			return order
		}
		left, _ := json.Marshal(a.Evidence)
		right, _ := json.Marshal(b.Evidence)
		return bytes.Compare(left, right)
	})
	r.Edges = slices.CompactFunc(r.Edges, func(a, b ledger.GraphEdge) bool { return a.EdgeID == b.EdgeID })
	slices.SortFunc(r.Coverage, coverageOrder)
}

// AnalyzeOpaque records content presence only. Unrecognized files and Go have
// explicit unknown dependency coverage; checks/declarations/resources/usage wait
// for their producer freezes. It does not invoke any tool or infer hyperlinks.
func AnalyzeOpaque(ctx context.Context, r AnalysisRequest) (AnalysisResult, error) {
	if err := validateRequest(r); err != nil {
		if errors.Is(err, errCorpusLimit) {
			return unknown(r, ledger.AdapterOpaque, ledger.ReasonHelperLimit, r.Allowance), nil
		}
		return AnalysisResult{}, err
	}
	out := AnalysisResult{Nodes: []ledger.GraphNode{}, Edges: []ledger.GraphEdge{}, Coverage: []ledger.CoverageEntry{}}
	for _, a := range r.Corpus {
		if err := ctx.Err(); err != nil {
			return AnalysisResult{}, err
		}
		units := int64(2 + (len(a.Bytes)+255)/256)
		if units > r.Allowance-out.WorkUsed {
			return unknown(r, ledger.AdapterOpaque, ledger.ReasonWorkExhausted, r.Allowance), nil
		}
		out.WorkUsed += units
		out.Nodes = append(out.Nodes, ledger.GraphNode{Key: a.Key, VersionSHA256: a.SHA256, Adapter: ledger.AdapterOpaque, ConfigurationSHA256: r.ConfigurationSHA256})
		adapter, reason := sourceAdapter(a.Path), ledger.ReasonUnsupportedSyntax
		switch adapter {
		case "go":
			reason = ledger.ReasonDependencyUnapproved
		case ledger.AdapterPython, ledger.AdapterSQL, ledger.AdapterTypeScript:
			continue
		}
		out.Coverage = append(out.Coverage, covered(a.Key, adapter, reason))
	}
	for _, adapter := range []string{ledger.AdapterChecks, ledger.AdapterDeclarations, ledger.AdapterResources, ledger.AdapterUsage} {
		out.Coverage = append(out.Coverage, ledger.CoverageEntry{Scope: workspaceScope(r.Source.WorkspaceID), Adapter: adapter, State: ledger.CoverageUnknown, Reasons: []string{ledger.ReasonUpstreamUnfrozen}})
	}
	out.NextCursor.ArtifactIndex = int64(len(r.Corpus))
	sortResult(&out)
	return out, nil
}

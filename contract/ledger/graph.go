package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
	"uuid"
)

// The F-U3-61 record types, each at version 1: ticket 61's part, U3's first
// producer family, frozen (D130). These codecs enforce payload rules only.
const (
	TypeGraphAnalysisStarted  = "graph.analysis_started"
	TypeGraphAnalysisProgress = "graph.analysis_progress"
	TypeGraphDeltaChunk       = "graph.delta_chunk"
	TypeGraphVersionCommitted = "graph.version_committed"
)

// F-U3-61's closed phase and coverage tokens, frozen (D130).
const (
	GraphPhaseReserved   = "reserved"
	GraphPhaseCheckpoint = "checkpoint"
	GraphPhaseUnknown    = "unknown"
	GraphPhaseSuperseded = "superseded"
	CoverageComplete     = "complete"
	CoverageUnknown      = "unknown"
)

// F-U3-61's version-1 writer values under open token grammars, frozen
// (D130). Readers also accept other well-formed tokens.
const (
	GraphReasonIncremental     = "incremental"
	GraphReasonLoad            = "load"
	GraphReasonStructural100   = "structural_100"
	GraphReasonAnalyzerEpoch   = "analyzer_epoch"
	GraphReasonDigestMismatch  = "digest_mismatch"
	NodeKindArtifact           = "artifact"
	NodeKindModule             = "module"
	NodeNamespaceWorkspace     = "workspace"
	AdapterOpaque              = "opaque"
	AdapterPython              = "python"
	AdapterTypeScript          = "typescript"
	AdapterSQL                 = "sql"
	AdapterChecks              = "checks"
	AdapterDeclarations        = "declarations"
	AdapterResources           = "resources"
	AdapterUsage               = "usage"
	GeometryCoupling           = "coupling"
	GraphScopeCanonical        = "canonical"
	ReasonUnsupportedSyntax    = "unsupported_syntax"
	ReasonUnresolvedReference  = "unresolved_reference"
	ReasonAdapterUnavailable   = "adapter_unavailable"
	ReasonDependencyUnapproved = "dependency_unapproved"
	ReasonUpstreamUnfrozen     = "upstream_unfrozen"
	ReasonSourceUnavailable    = "source_unavailable"
	ReasonWorkExhausted        = "work_exhausted"
	ReasonStateCap             = "state_cap"
	ReasonTemporaryCap         = "temporary_cap"
	ReasonHelperLimit          = "helper_limit"
	ReasonStaleSource          = "stale_source"
	ReasonRecoveryUnknown      = "recovery_unknown"
)

// F-U3-61's version-1 profile identifiers, frozen (D130). Later
// identifiers use the existing versioned-identifier grammar.
const (
	LimitsProfileV1 = "engine-limits/1"
	HelperProfileV1 = "graph-helper-limits/1"
)

// EmptyGraphDigest hashes the exact empty canonical graph state bytes.
var EmptyGraphDigest = Digest(sha256.Sum256([]byte(`{"scopes":[],"nodes":[],"edges":[],"coverage":[]}`)))

// EmptyChunksDigest hashes zero bytes: a zero-net confirmation has no chunks.
var EmptyChunksDigest = Digest(sha256.Sum256(nil))

// RecordRef names an earlier committed envelope, not a payload hash.
type RecordRef struct {
	Seq  int64  `json:"seq"`
	Hash Digest `json:"hash"`
}

// GraphSource binds a workspace's registration and accepted immutable tree.
type GraphSource struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Registered  RecordRef `json:"registered"`
	Subject     RecordRef `json:"subject"`
	Tree        string    `json:"tree"`
}

// NodeKey is a stable identity. Kind-specific LocalKey tuples are service rules.
type NodeKey struct {
	Namespace string    `json:"namespace"`
	OriginID  uuid.UUID `json:"origin_id"`
	Kind      string    `json:"kind"`
	LocalKey  string    `json:"local_key"`
}

// GraphNode binds a stable key to content and analyzer configuration.
type GraphNode struct {
	Key                 NodeKey `json:"key"`
	VersionSHA256       Digest  `json:"version_sha256"`
	Adapter             string  `json:"adapter"`
	ConfigurationSHA256 Digest  `json:"configuration_sha256"`
}

// EdgeEvidence identifies the artifact and native reference proving an edge.
type EdgeEvidence struct {
	Artifact            NodeKey `json:"artifact"`
	ArtifactSHA256      Digest  `json:"artifact_sha256"`
	Adapter             string  `json:"adapter"`
	ConfigurationSHA256 Digest  `json:"configuration_sha256"`
	Reference           string  `json:"reference"`
}

// GraphEdge is an evidenced directed edge, weighted in parts per million.
type GraphEdge struct {
	EdgeID    Digest       `json:"edge_id"`
	From      NodeKey      `json:"from"`
	To        NodeKey      `json:"to"`
	Type      string       `json:"type"`
	Geometry  string       `json:"geometry"`
	WeightPPM int64        `json:"weight_ppm"`
	Evidence  EdgeEvidence `json:"evidence"`
}

// CoverageEntry records completeness or explicit unknown reasons for an adapter.
type CoverageEntry struct {
	Scope   string   `json:"scope"`
	Adapter string   `json:"adapter"`
	State   string   `json:"state"`
	Reasons []string `json:"reasons"`
}

// GraphCursor names durably verified work, not speculative progress.
type GraphCursor struct {
	ArtifactIndex int64 `json:"artifact_index"`
	OutputIndex   int64 `json:"output_index"`
}

// GraphAnalysisStarted is the graph.analysis_started v1 payload.
type GraphAnalysisStarted struct {
	AnalysisID          uuid.UUID   `json:"analysis_id"`
	AnalysisKey         Digest      `json:"analysis_key"`
	Source              GraphSource `json:"source"`
	InputFrontier       RecordRef   `json:"input_frontier"`
	Scope               string      `json:"scope"`
	BaseVersion         int64       `json:"base_version"`
	BaseDigest          Digest      `json:"base_digest"`
	AnalyzerEpoch       int64       `json:"analyzer_epoch"`
	ConfigurationSHA256 Digest      `json:"configuration_sha256"`
	LimitsProfile       string      `json:"limits_profile"`
	HelperProfile       string      `json:"helper_profile"`
	Reason              string      `json:"reason"`
}

// GraphAnalysisProgress is the graph.analysis_progress v1 payload.
type GraphAnalysisProgress struct {
	AnalysisID     uuid.UUID       `json:"analysis_id"`
	AnalysisKey    Digest          `json:"analysis_key"`
	Revision       int64           `json:"revision"`
	Phase          string          `json:"phase"`
	ReservedTotal  int64           `json:"reserved_total"`
	VerifiedCursor GraphCursor     `json:"verified_cursor"`
	Coverage       []CoverageEntry `json:"coverage"`
}

// GraphDeltaChunk is the graph.delta_chunk v1 payload. All five arrays are required.
type GraphDeltaChunk struct {
	AnalysisID  uuid.UUID       `json:"analysis_id"`
	AnalysisKey Digest          `json:"analysis_key"`
	ChunkIndex  int64           `json:"chunk_index"`
	RemoveEdges []Digest        `json:"remove_edges"`
	RemoveNodes []NodeKey       `json:"remove_nodes"`
	PutNodes    []GraphNode     `json:"put_nodes"`
	PutEdges    []GraphEdge     `json:"put_edges"`
	Coverage    []CoverageEntry `json:"coverage"`
}

// GraphVersionCommitted is the graph.version_committed v1 payload. A zero
// ChunkCount confirms BaseVersion without advancing the effective version.
type GraphVersionCommitted struct {
	AnalysisID             uuid.UUID `json:"analysis_id"`
	AnalysisKey            Digest    `json:"analysis_key"`
	BaseVersion            int64     `json:"base_version"`
	BaseDigest             Digest    `json:"base_digest"`
	ChunkCount             int64     `json:"chunk_count"`
	ChunksSHA256           Digest    `json:"chunks_sha256"`
	GraphDigest            Digest    `json:"graph_digest"`
	StructuralSinceRebuild int64     `json:"structural_since_rebuild"`
}

// EdgeID is SHA-256 of the compact JSON array [from,to,typ], using this
// package's NodeKey member order and JSON string escaping. Evidence is excluded.
func EdgeID(from, to NodeKey, typ string) Digest {
	js, _ := json.Marshal([3]any{from, to, typ})
	return Digest(sha256.Sum256(js))
}

func graphToken(s string) bool {
	n, ok := dottedTokens(s)
	return ok && n == 1 && len(s) <= 64
}

func graphText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s)
}

func (p RecordRef) check() error {
	if p.Seq < 1 || p.Hash == (Digest{}) {
		return errors.New("record reference requires a positive sequence and non-zero hash")
	}
	return nil
}

func (p GraphSource) check() error {
	if !isV7(p.WorkspaceID) || p.Registered.Seq >= p.Subject.Seq || !validGitID(p.Tree) {
		return errors.New("invalid workspace, reference order or tree")
	}
	return errors.Join(p.Registered.check(), p.Subject.check())
}

func (p NodeKey) check() error {
	if !graphToken(p.Namespace) || !isV7(p.OriginID) || !graphToken(p.Kind) || !graphText(p.LocalKey, 4096) {
		return errors.New("invalid node namespace, origin, kind or local key")
	}
	return nil
}

func (p NodeKey) compact() string {
	js, _ := json.Marshal(p)
	return string(js)
}

func (p GraphNode) check() error {
	if p.VersionSHA256 == (Digest{}) || p.ConfigurationSHA256 == (Digest{}) || !graphToken(p.Adapter) {
		return errors.New("invalid node digest or adapter")
	}
	return p.Key.check()
}

func (p EdgeEvidence) check() error {
	if p.ArtifactSHA256 == (Digest{}) || p.ConfigurationSHA256 == (Digest{}) || !graphToken(p.Adapter) || !graphText(p.Reference, 4096) {
		return errors.New("invalid evidence digest, adapter or reference")
	}
	return p.Artifact.check()
}

func (p GraphEdge) check() error {
	if p.From == p.To || !graphToken(p.Type) || !graphToken(p.Geometry) || p.WeightPPM < 1 || p.WeightPPM > 1000000 || p.EdgeID != EdgeID(p.From, p.To, p.Type) {
		return errors.New("invalid edge endpoints, type, geometry, weight or identity")
	}
	return errors.Join(p.From.check(), p.To.check(), p.Evidence.check())
}

func (p CoverageEntry) check() error {
	if !graphText(p.Scope, 8192) || !graphToken(p.Adapter) || p.State != CoverageComplete && p.State != CoverageUnknown || (p.State == CoverageComplete) != (len(p.Reasons) == 0) {
		return errors.New("invalid coverage scope, adapter, state or reasons")
	}
	for i, reason := range p.Reasons {
		if !graphToken(reason) || i > 0 && p.Reasons[i-1] >= reason {
			return errors.New("coverage reasons are not strictly ascending tokens")
		}
	}
	return nil
}

func (p GraphCursor) check() error {
	if p.ArtifactIndex < 0 || p.OutputIndex < 0 {
		return errors.New("negative cursor")
	}
	return nil
}

func checkGraphCoverage(entries []CoverageEntry) error {
	for i, p := range entries {
		if err := p.check(); err != nil {
			return fmt.Errorf("coverage element %d: %w", i, err)
		}
		if i > 0 {
			prev := entries[i-1]
			if prev.Scope > p.Scope || prev.Scope == p.Scope && prev.Adapter >= p.Adapter {
				return errors.New("coverage is not strictly ascending by scope and adapter")
			}
		}
	}
	return nil
}

func graphCoverageWire(entries []CoverageEntry) []CoverageEntry {
	out := make([]CoverageEntry, len(entries))
	copy(out, entries)
	for i := range out {
		if out[i].Reasons == nil {
			out[i].Reasons = []string{}
		}
	}
	return out
}

func (p GraphAnalysisStarted) check() error {
	switch {
	case !isV7(p.AnalysisID) || p.AnalysisKey == (Digest{}) || p.BaseDigest == (Digest{}) || p.ConfigurationSHA256 == (Digest{}):
		return errors.New("invalid analysis identity or digest")
	case !graphToken(p.Scope) || !graphToken(p.Reason):
		return errors.New("invalid scope or reason token")
	case p.BaseVersion < 0 || p.AnalyzerEpoch < 1:
		return errors.New("invalid base version or analyzer epoch")
	case (p.BaseVersion == 0) != (p.BaseDigest == EmptyGraphDigest):
		return errors.New("zero base version must have the empty graph digest and conversely")
	case !validIntegrationRules(p.LimitsProfile) || !validIntegrationRules(p.HelperProfile):
		return errors.New("invalid limits or helper profile")
	case p.Source.Subject.Seq > p.InputFrontier.Seq:
		return errors.New("source subject is beyond input frontier")
	}
	return errors.Join(p.Source.check(), p.InputFrontier.check())
}

func (p GraphAnalysisProgress) check() error {
	if !isV7(p.AnalysisID) || p.AnalysisKey == (Digest{}) || p.Revision < 1 || p.ReservedTotal < 0 {
		return errors.New("invalid analysis identity, revision or reservation")
	}
	switch p.Phase {
	case GraphPhaseReserved, GraphPhaseCheckpoint:
	case GraphPhaseUnknown, GraphPhaseSuperseded:
		if len(p.Coverage) == 0 {
			return errors.New("terminal progress requires unknown coverage")
		}
		for _, c := range p.Coverage {
			if c.State != CoverageUnknown {
				return errors.New("terminal progress has complete coverage")
			}
		}
	default:
		return errors.New("invalid graph phase")
	}
	return errors.Join(p.VerifiedCursor.check(), checkGraphCoverage(p.Coverage))
}

func (p GraphDeltaChunk) check() error {
	if !isV7(p.AnalysisID) || p.AnalysisKey == (Digest{}) || p.ChunkIndex < 0 {
		return errors.New("invalid analysis identity or chunk index")
	}
	for i, id := range p.RemoveEdges {
		if id == (Digest{}) || i > 0 && bytes.Compare(p.RemoveEdges[i-1][:], id[:]) >= 0 {
			return errors.New("remove_edges is not strictly ascending non-zero digests")
		}
	}
	for i, key := range p.RemoveNodes {
		if err := key.check(); err != nil {
			return err
		}
		if i > 0 && p.RemoveNodes[i-1].compact() >= key.compact() {
			return errors.New("remove_nodes is not strictly ascending")
		}
	}
	for i, node := range p.PutNodes {
		if err := node.check(); err != nil {
			return err
		}
		if i > 0 && p.PutNodes[i-1].Key.compact() >= node.Key.compact() {
			return errors.New("put_nodes is not strictly ascending")
		}
		if _, found := slices.BinarySearchFunc(p.RemoveNodes, node.Key.compact(), func(k NodeKey, s string) int { return bytes.Compare([]byte(k.compact()), []byte(s)) }); found {
			return errors.New("node is both removed and put")
		}
	}
	for i, edge := range p.PutEdges {
		if err := edge.check(); err != nil {
			return err
		}
		if i > 0 && bytes.Compare(p.PutEdges[i-1].EdgeID[:], edge.EdgeID[:]) >= 0 {
			return errors.New("put_edges is not strictly ascending")
		}
		if _, found := slices.BinarySearchFunc(p.RemoveEdges, edge.EdgeID, func(a, b Digest) int { return bytes.Compare(a[:], b[:]) }); found {
			return errors.New("edge is both removed and put")
		}
	}
	return checkGraphCoverage(p.Coverage)
}

func (p GraphVersionCommitted) check() error {
	switch {
	case !isV7(p.AnalysisID) || p.AnalysisKey == (Digest{}) || p.BaseDigest == (Digest{}) || p.ChunksSHA256 == (Digest{}) || p.GraphDigest == (Digest{}):
		return errors.New("invalid analysis identity or digest")
	case p.BaseVersion < 0 || p.ChunkCount < 0 || p.StructuralSinceRebuild < 0:
		return errors.New("negative version, chunk count or structural count")
	case (p.BaseVersion == 0) != (p.BaseDigest == EmptyGraphDigest):
		return errors.New("zero base version must have the empty graph digest and conversely")
	case (p.ChunkCount == 0) != (p.ChunksSHA256 == EmptyChunksDigest):
		return errors.New("zero chunks must have the empty chunks digest and conversely")
	case (p.ChunkCount == 0) != (p.GraphDigest == p.BaseDigest):
		return errors.New("zero chunks must leave the graph unchanged and conversely")
	}
	return nil
}

// Encode returns the graph.analysis_started v1 payload; errors wrap ErrInvalid.
func (p GraphAnalysisStarted) Encode() (json.RawMessage, error) {
	return encodePayload(TypeGraphAnalysisStarted, p, p.check())
}

// Encode returns the graph.analysis_progress v1 payload; errors wrap ErrInvalid.
func (p GraphAnalysisProgress) Encode() (json.RawMessage, error) {
	p.Coverage = graphCoverageWire(p.Coverage)
	return encodePayload(TypeGraphAnalysisProgress, p, p.check())
}

// Encode returns the graph.delta_chunk v1 payload; errors wrap ErrInvalid.
func (p GraphDeltaChunk) Encode() (json.RawMessage, error) {
	if p.RemoveEdges == nil {
		p.RemoveEdges = []Digest{}
	}
	if p.RemoveNodes == nil {
		p.RemoveNodes = []NodeKey{}
	}
	if p.PutNodes == nil {
		p.PutNodes = []GraphNode{}
	}
	if p.PutEdges == nil {
		p.PutEdges = []GraphEdge{}
	}
	p.Coverage = graphCoverageWire(p.Coverage)
	return encodePayload(TypeGraphDeltaChunk, p, p.check())
}

// Encode returns the graph.version_committed v1 payload; errors wrap ErrInvalid.
func (p GraphVersionCommitted) Encode() (json.RawMessage, error) {
	return encodePayload(TypeGraphVersionCommitted, p, p.check())
}

// graphObject and graphArray retain nested members' first error at their
// parent. The whole-payload walk already rejects repeated keys at any depth.
func graphObject[T any](m *members, key string, decode func(*members) T) T {
	var zero T
	raw, ok := m.get(key, true)
	if !ok {
		return zero
	}
	b := members{}
	if raw[0] != '{' || json.Unmarshal(raw, &b.m) != nil {
		m.err = fmt.Errorf("%s is not an object", key)
		return zero
	}
	p := decode(&b)
	if b.err != nil {
		m.err = fmt.Errorf("%s: %w", key, b.err)
	}
	return p
}

func graphArray[T any](m *members, key string, decode func(*members) T) []T {
	raw, ok := m.get(key, true)
	if !ok {
		return nil
	}
	var elems []json.RawMessage
	if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
		m.err = fmt.Errorf("%s is not an array", key)
		return nil
	}
	out := make([]T, len(elems))
	for i, el := range elems {
		b := members{m: map[string]json.RawMessage{"element": el}}
		out[i] = decode(&b)
		if b.err != nil {
			m.err = fmt.Errorf("%s element %d: %w", key, i, b.err)
			return nil
		}
	}
	return out
}

func readRecordRef(m *members) RecordRef {
	return RecordRef{Seq: m.nat("seq", true), Hash: m.digest("hash")}
}

func readGraphSource(m *members) GraphSource {
	return GraphSource{WorkspaceID: m.id("workspace_id"), Registered: graphObject(m, "registered", readRecordRef), Subject: graphObject(m, "subject", readRecordRef), Tree: m.text("tree")}
}

func readNodeKey(m *members) NodeKey {
	return NodeKey{Namespace: m.text("namespace"), OriginID: m.id("origin_id"), Kind: m.text("kind"), LocalKey: m.text("local_key")}
}

func readGraphNode(m *members) GraphNode {
	return GraphNode{Key: graphObject(m, "key", readNodeKey), VersionSHA256: m.digest("version_sha256"), Adapter: m.text("adapter"), ConfigurationSHA256: m.digest("configuration_sha256")}
}

func readEdgeEvidence(m *members) EdgeEvidence {
	return EdgeEvidence{Artifact: graphObject(m, "artifact", readNodeKey), ArtifactSHA256: m.digest("artifact_sha256"), Adapter: m.text("adapter"), ConfigurationSHA256: m.digest("configuration_sha256"), Reference: m.text("reference")}
}

func readGraphEdge(m *members) GraphEdge {
	return GraphEdge{EdgeID: m.digest("edge_id"), From: graphObject(m, "from", readNodeKey), To: graphObject(m, "to", readNodeKey), Type: m.text("type"), Geometry: m.text("geometry"), WeightPPM: m.nat("weight_ppm", true), Evidence: graphObject(m, "evidence", readEdgeEvidence)}
}

func readCoverageEntry(m *members) CoverageEntry {
	return CoverageEntry{Scope: m.text("scope"), Adapter: m.text("adapter"), State: m.text("state"), Reasons: graphArray(m, "reasons", func(b *members) string { return b.text("element") })}
}

func readGraphCoverage(m *members) []CoverageEntry {
	return graphArray(m, "coverage", func(b *members) CoverageEntry { return graphObject(b, "element", readCoverageEntry) })
}

// DecodeGraphAnalysisStarted decodes graph.analysis_started v1; errors wrap ErrInvalid.
func DecodeGraphAnalysisStarted(payload json.RawMessage) (GraphAnalysisStarted, error) {
	var p GraphAnalysisStarted
	if err := decodePayload(TypeGraphAnalysisStarted, payload, func(m *members) error {
		p = GraphAnalysisStarted{
			AnalysisID: m.id("analysis_id"), AnalysisKey: m.digest("analysis_key"),
			Source: graphObject(m, "source", readGraphSource), InputFrontier: graphObject(m, "input_frontier", readRecordRef),
			Scope: m.text("scope"), BaseVersion: m.nat("base_version", true), BaseDigest: m.digest("base_digest"),
			AnalyzerEpoch: m.nat("analyzer_epoch", true), ConfigurationSHA256: m.digest("configuration_sha256"),
			LimitsProfile: m.text("limits_profile"), HelperProfile: m.text("helper_profile"), Reason: m.text("reason"),
		}
		return p.check()
	}); err != nil {
		return GraphAnalysisStarted{}, err
	}
	return p, nil
}

// DecodeGraphAnalysisProgress decodes graph.analysis_progress v1; errors wrap ErrInvalid.
func DecodeGraphAnalysisProgress(payload json.RawMessage) (GraphAnalysisProgress, error) {
	var p GraphAnalysisProgress
	if err := decodePayload(TypeGraphAnalysisProgress, payload, func(m *members) error {
		p = GraphAnalysisProgress{
			AnalysisID: m.id("analysis_id"), AnalysisKey: m.digest("analysis_key"), Revision: m.nat("revision", true),
			Phase: m.text("phase"), ReservedTotal: m.nat("reserved_total", true),
			VerifiedCursor: graphObject(m, "verified_cursor", func(b *members) GraphCursor {
				return GraphCursor{ArtifactIndex: b.nat("artifact_index", true), OutputIndex: b.nat("output_index", true)}
			}),
			Coverage: readGraphCoverage(m),
		}
		return p.check()
	}); err != nil {
		return GraphAnalysisProgress{}, err
	}
	return p, nil
}

// DecodeGraphDeltaChunk decodes graph.delta_chunk v1; errors wrap ErrInvalid.
func DecodeGraphDeltaChunk(payload json.RawMessage) (GraphDeltaChunk, error) {
	var p GraphDeltaChunk
	if err := decodePayload(TypeGraphDeltaChunk, payload, func(m *members) error {
		p = GraphDeltaChunk{
			AnalysisID: m.id("analysis_id"), AnalysisKey: m.digest("analysis_key"), ChunkIndex: m.nat("chunk_index", true),
			RemoveEdges: graphArray(m, "remove_edges", func(b *members) Digest { return b.digest("element") }),
			RemoveNodes: graphArray(m, "remove_nodes", func(b *members) NodeKey { return graphObject(b, "element", readNodeKey) }),
			PutNodes:    graphArray(m, "put_nodes", func(b *members) GraphNode { return graphObject(b, "element", readGraphNode) }),
			PutEdges:    graphArray(m, "put_edges", func(b *members) GraphEdge { return graphObject(b, "element", readGraphEdge) }),
			Coverage:    readGraphCoverage(m),
		}
		return p.check()
	}); err != nil {
		return GraphDeltaChunk{}, err
	}
	return p, nil
}

// DecodeGraphVersionCommitted decodes graph.version_committed v1; errors wrap ErrInvalid.
func DecodeGraphVersionCommitted(payload json.RawMessage) (GraphVersionCommitted, error) {
	var p GraphVersionCommitted
	if err := decodePayload(TypeGraphVersionCommitted, payload, func(m *members) error {
		p = GraphVersionCommitted{
			AnalysisID: m.id("analysis_id"), AnalysisKey: m.digest("analysis_key"), BaseVersion: m.nat("base_version", true), BaseDigest: m.digest("base_digest"),
			ChunkCount: m.nat("chunk_count", true), ChunksSHA256: m.digest("chunks_sha256"), GraphDigest: m.digest("graph_digest"), StructuralSinceRebuild: m.nat("structural_since_rebuild", true),
		}
		return p.check()
	}); err != nil {
		return GraphVersionCommitted{}, err
	}
	return p, nil
}

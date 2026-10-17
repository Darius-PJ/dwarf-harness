package graph

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ErrInvalid denotes an invalid analysis input or canonical graph value.
var ErrInvalid = errors.New("invalid graph input")

// Scope binds the exact source and analyzer identity covered by an image.
type Scope struct {
	Source              ledger.GraphSource `json:"source"`
	Scope               string             `json:"scope"`
	AnalyzerEpoch       int64              `json:"analyzer_epoch"`
	ConfigurationSHA256 ledger.Digest      `json:"configuration_sha256"`
}

// State is a record-free graph image. Canonical never mutates its input.
type State struct {
	Scopes   []Scope                `json:"scopes"`
	Nodes    []ledger.GraphNode     `json:"nodes"`
	Edges    []ledger.GraphEdge     `json:"edges"`
	Coverage []ledger.CoverageEntry `json:"coverage"`
}

// ModuleMember is native-confirmed membership, not directory containment.
type ModuleMember struct {
	Path   string
	SHA256 ledger.Digest
}

func text(s string, max int) bool { return s != "" && len(s) <= max && utf8.ValidString(s) }
func token(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
func pathValid(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func keyValid(k ledger.NodeKey) bool {
	return token(k.Namespace) && token(k.Kind) && k.OriginID[6]>>4 == 7 && k.OriginID[8]&0xc0 == 0x80 && text(k.LocalKey, 4096)
}
func keyBytes(k ledger.NodeKey) []byte { b, _ := json.Marshal(k); return b }
func keyScope(k ledger.NodeKey) string { return string(keyBytes(k)) }
func workspaceScope(id uuid.UUID) string {
	return "workspace:" + id.String() + ":" + ledger.GraphScopeCanonical
}

// ArtifactKey binds a path to its absent-to-present birth sequence. Content is
// deliberately absent from identity. Paths retain the frozen Git byte grammar.
func ArtifactKey(workspace uuid.UUID, birth int64, path string) (ledger.NodeKey, error) {
	if birth < 1 || !pathValid(path) {
		return ledger.NodeKey{}, ErrInvalid
	}
	b, _ := json.Marshal([2]any{birth, path})
	k := ledger.NodeKey{Namespace: ledger.NodeNamespaceWorkspace, OriginID: workspace, Kind: ledger.NodeKindArtifact, LocalKey: string(b)}
	if !keyValid(k) {
		return ledger.NodeKey{}, ErrInvalid
	}
	return k, nil
}

// ModuleKey identifies a module justified by native resolution or a SQL CREATE.
func ModuleKey(workspace uuid.UUID, language, resolver, name string) (ledger.NodeKey, error) {
	if !token(language) || !text(resolver, 4096) || !text(name, 4096) {
		return ledger.NodeKey{}, ErrInvalid
	}
	b, _ := json.Marshal([3]string{language, resolver, name})
	k := ledger.NodeKey{Namespace: ledger.NodeNamespaceWorkspace, OriginID: workspace, Kind: ledger.NodeKindModule, LocalKey: string(b)}
	if !keyValid(k) {
		return ledger.NodeKey{}, ErrInvalid
	}
	return k, nil
}

// ModuleVersion hashes sorted, native-confirmed members using ledger's JSON
// escaping and digest representation. Duplicate paths are invalid.
func ModuleVersion(name string, members []ModuleMember) (ledger.Digest, error) {
	if !text(name, 4096) || len(members) == 0 {
		return ledger.Digest{}, ErrInvalid
	}
	sorted := slices.Clone(members)
	slices.SortFunc(sorted, func(a, b ModuleMember) int { return strings.Compare(a.Path, b.Path) })
	pairs := make([][2]any, 0, len(sorted))
	for i, m := range sorted {
		if !pathValid(m.Path) || m.SHA256 == (ledger.Digest{}) || i > 0 && sorted[i-1].Path == m.Path {
			return ledger.Digest{}, ErrInvalid
		}
		pairs = append(pairs, [2]any{m.Path, m.SHA256})
	}
	b, _ := json.Marshal(struct {
		Name    string   `json:"module_name"`
		Members [][2]any `json:"members"`
	}{name, pairs})
	return ledger.Digest(sha256.Sum256(b)), nil
}

func nodeValid(n ledger.GraphNode) bool {
	return keyValid(n.Key) && n.VersionSHA256 != (ledger.Digest{}) && n.ConfigurationSHA256 != (ledger.Digest{}) && token(n.Adapter)
}
func edgeValid(e ledger.GraphEdge) bool {
	return keyValid(e.From) && keyValid(e.To) && e.From != e.To && token(e.Type) && token(e.Geometry) && e.WeightPPM >= 1 && e.WeightPPM <= 1000000 && e.EdgeID == ledger.EdgeID(e.From, e.To, e.Type) && keyValid(e.Evidence.Artifact) && e.Evidence.ArtifactSHA256 != (ledger.Digest{}) && e.Evidence.ConfigurationSHA256 != (ledger.Digest{}) && token(e.Evidence.Adapter) && text(e.Evidence.Reference, 4096)
}
func coverageValid(c ledger.CoverageEntry) bool {
	if !text(c.Scope, 8192) || !token(c.Adapter) || (c.State != ledger.CoverageComplete && c.State != ledger.CoverageUnknown) || (c.State == ledger.CoverageComplete) != (len(c.Reasons) == 0) {
		return false
	}
	for i, r := range c.Reasons {
		if !token(r) || i > 0 && c.Reasons[i-1] >= r {
			return false
		}
	}
	return true
}
func coverageOrder(a, b ledger.CoverageEntry) int {
	if n := strings.Compare(a.Scope, b.Scope); n != 0 {
		return n
	}
	return strings.Compare(a.Adapter, b.Adapter)
}

// Canonical returns compact wire-compatible bytes and their SHA-256. Ordering
// is by workspace UUID bytes, NodeKey JSON bytes, raw edge ID, then coverage
// (scope,adapter). Duplicate identities and dangling edges are refused.
func (s State) Canonical() ([]byte, ledger.Digest, error) {
	c := State{Scopes: append([]Scope{}, s.Scopes...), Nodes: append([]ledger.GraphNode{}, s.Nodes...), Edges: append([]ledger.GraphEdge{}, s.Edges...), Coverage: append([]ledger.CoverageEntry{}, s.Coverage...)}
	slices.SortFunc(c.Scopes, func(a, b Scope) int { return bytes.Compare(a.Source.WorkspaceID[:], b.Source.WorkspaceID[:]) })
	for i, v := range c.Scopes {
		if v.AnalyzerEpoch < 1 || v.ConfigurationSHA256 == (ledger.Digest{}) || !token(v.Scope) || !sourceValid(v.Source) || i > 0 && c.Scopes[i-1].Source.WorkspaceID == v.Source.WorkspaceID {
			return nil, ledger.Digest{}, ErrInvalid
		}
	}
	slices.SortFunc(c.Nodes, func(a, b ledger.GraphNode) int { return bytes.Compare(keyBytes(a.Key), keyBytes(b.Key)) })
	keys := make(map[ledger.NodeKey]bool, len(c.Nodes))
	for _, n := range c.Nodes {
		if !nodeValid(n) || keys[n.Key] {
			return nil, ledger.Digest{}, ErrInvalid
		}
		keys[n.Key] = true
	}
	slices.SortFunc(c.Edges, func(a, b ledger.GraphEdge) int { return bytes.Compare(a.EdgeID[:], b.EdgeID[:]) })
	for i, e := range c.Edges {
		if !edgeValid(e) || !keys[e.From] || !keys[e.To] || !keys[e.Evidence.Artifact] || i > 0 && c.Edges[i-1].EdgeID == e.EdgeID {
			return nil, ledger.Digest{}, ErrInvalid
		}
	}
	slices.SortFunc(c.Coverage, coverageOrder)
	for i, v := range c.Coverage {
		if !coverageValid(v) || i > 0 && coverageOrder(c.Coverage[i-1], v) == 0 {
			return nil, ledger.Digest{}, ErrInvalid
		}
		c.Coverage[i].Reasons = append([]string{}, v.Reasons...)
	}
	b, err := json.Marshal(c)
	return b, ledger.Digest(sha256.Sum256(b)), err
}

func sourceValid(s ledger.GraphSource) bool {
	if s.WorkspaceID[6]>>4 != 7 || s.WorkspaceID[8]&0xc0 != 0x80 || s.Registered.Seq < 1 || s.Subject.Seq <= s.Registered.Seq || s.Registered.Hash == (ledger.Digest{}) || s.Subject.Hash == (ledger.Digest{}) || (len(s.Tree) != 40 && len(s.Tree) != 64) {
		return false
	}
	for _, c := range s.Tree {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

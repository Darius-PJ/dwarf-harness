package graph

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

const (
	quantum       = int64(10000)
	attemptLimit  = int64(2000000)
	stagingLimit  = uint64(64 << 20)
	retainedLimit = uint64(512 << 20)
	chunkLimit    = 512 << 10
)

func graphConsumption() ledger.Consumption {
	return ledger.Consumption{
		ledger.TypeWorkspaceRegistered: {1}, ledger.TypePromotionAdmitted: {1}, ledger.TypePromotionAccepted: {1},
		ledger.TypeGraphAnalysisStarted: {1}, ledger.TypeGraphAnalysisProgress: {1}, ledger.TypeGraphDeltaChunk: {1}, ledger.TypeGraphVersionCommitted: {1},
	}
}

// AnalysisKey excludes the attempt UUID and reason. Recomputing it is a service
// rule, not a guarantee supplied by the frozen payload decoder.
func AnalysisKey(started ledger.GraphAnalysisStarted) ledger.Digest {
	return tupleDigest([]any{started.Source, started.InputFrontier, started.Scope, started.BaseVersion, started.BaseDigest, started.AnalyzerEpoch, started.ConfigurationSHA256, started.LimitsProfile, started.HelperProfile})
}
func exhaustionKey(started ledger.GraphAnalysisStarted) ledger.Digest {
	return tupleDigest([]any{started.Source, started.Scope, started.AnalyzerEpoch, started.ConfigurationSHA256, started.LimitsProfile, started.HelperProfile})
}
func tupleDigest(value []any) ledger.Digest {
	encoded, _ := json.Marshal(value)
	return ledger.Digest(sha256.Sum256(encoded))
}
func coverageKey(entry ledger.CoverageEntry) string { return entry.Scope + "\x00" + entry.Adapter }
func startedScope(started ledger.GraphAnalysisStarted) Scope {
	return Scope{Source: started.Source, Scope: started.Scope, AnalyzerEpoch: started.AnalyzerEpoch, ConfigurationSHA256: started.ConfigurationSHA256}
}

type registration struct {
	ref ledger.RecordRef
}
type acceptance struct {
	ref               ledger.RecordRef
	predecessor, tree string
	paths             []string
}
type attemptSummary struct {
	started            ledger.GraphAnalysisStarted
	revision, reserved int64
	cursor             ledger.GraphCursor
	phase              string
	serviceReason      string
}
type replayAttempt struct {
	attemptSummary
	chunks                 [][]byte
	stage                  *graphIndex
	mutationPhase          int
	removedNodes, putNodes map[ledger.NodeKey]bool
	removedEdges, putEdges map[ledger.Digest]bool
	retained               uint64
}
type graphIndex struct {
	scopes    map[uuid.UUID]Scope
	nodes     map[ledger.NodeKey]ledger.GraphNode
	edges     map[ledger.Digest]ledger.GraphEdge
	coverage  map[string]ledger.CoverageEntry
	adjacency map[ledger.NodeKey]map[ledger.Digest]bool
	memory    uint64
}
type graphImage struct {
	state   State
	index   *graphIndex
	version int64
	digest  ledger.Digest
	memory  uint64
}
type replayState struct {
	through           uint64
	chainHash         string
	registrations     map[uuid.UUID]registration
	admissions        map[uuid.UUID]uuid.UUID
	acceptances       map[uuid.UUID][]acceptance
	frontier          ledger.RecordRef
	current           *graphImage
	attempt           *replayAttempt
	terminal          map[ledger.Digest]attemptSummary
	exhausted         map[ledger.Digest]bool
	analysisIDs       map[uuid.UUID]bool
	lastAttempt       attemptSummary
	lastEpoch         int64
	lastConfiguration ledger.Digest
	structural        int64
	metadataMemory    uint64
	retainedCap       uint64
	nodeCap           int64
}

func newGraphIndex() *graphIndex {
	return &graphIndex{scopes: make(map[uuid.UUID]Scope), nodes: make(map[ledger.NodeKey]ledger.GraphNode), edges: make(map[ledger.Digest]ledger.GraphEdge), coverage: make(map[string]ledger.CoverageEntry), adjacency: make(map[ledger.NodeKey]map[ledger.Digest]bool)}
}
func (g *graphIndex) clone() *graphIndex {
	c := &graphIndex{scopes: maps.Clone(g.scopes), nodes: maps.Clone(g.nodes), edges: maps.Clone(g.edges), coverage: maps.Clone(g.coverage), adjacency: make(map[ledger.NodeKey]map[ledger.Digest]bool), memory: g.memory}
	for key, edges := range g.adjacency {
		c.adjacency[key] = maps.Clone(edges)
	}
	return c
}
func (g *graphIndex) putScope(scope Scope) {
	if old, exists := g.scopes[scope.Source.WorkspaceID]; exists {
		g.memory -= scopeMemory(old)
	}
	g.scopes[scope.Source.WorkspaceID] = scope
	g.memory += scopeMemory(scope)
}
func (g *graphIndex) putNode(node ledger.GraphNode) {
	if old, exists := g.nodes[node.Key]; exists {
		g.memory -= nodeMemory(old)
	}
	g.nodes[node.Key] = node
	g.memory += nodeMemory(node)
}
func (g *graphIndex) removeNode(key ledger.NodeKey) {
	g.memory -= nodeMemory(g.nodes[key])
	delete(g.nodes, key)
}
func (g *graphIndex) putEdge(edge ledger.GraphEdge) {
	if _, exists := g.edges[edge.EdgeID]; exists {
		g.removeEdge(edge.EdgeID)
	}
	g.edges[edge.EdgeID] = edge
	g.memory += edgeMemory(edge)
	for _, key := range []ledger.NodeKey{edge.From, edge.To, edge.Evidence.Artifact} {
		if g.adjacency[key] == nil {
			g.adjacency[key] = make(map[ledger.Digest]bool)
		}
		g.adjacency[key][edge.EdgeID] = true
	}
}
func (g *graphIndex) removeEdge(id ledger.Digest) {
	edge := g.edges[id]
	g.memory -= edgeMemory(edge)
	delete(g.edges, id)
	for _, key := range []ledger.NodeKey{edge.From, edge.To, edge.Evidence.Artifact} {
		delete(g.adjacency[key], id)
		if len(g.adjacency[key]) == 0 {
			delete(g.adjacency, key)
		}
	}
}
func (g *graphIndex) putCoverage(entry ledger.CoverageEntry) {
	key := coverageKey(entry)
	if old, exists := g.coverage[key]; exists {
		g.memory -= coverageMemory(old)
	}
	g.coverage[key] = entry
	g.memory += coverageMemory(entry)
}
func (g *graphIndex) state() State {
	s := State{Scopes: slices.Collect(maps.Values(g.scopes)), Nodes: slices.Collect(maps.Values(g.nodes)), Edges: slices.Collect(maps.Values(g.edges)), Coverage: slices.Collect(maps.Values(g.coverage))}
	keys := make(map[ledger.NodeKey][]byte, len(g.nodes))
	for key := range g.nodes {
		keys[key] = keyBytes(key)
	}
	slices.SortFunc(s.Scopes, func(a, b Scope) int { return bytes.Compare(a.Source.WorkspaceID[:], b.Source.WorkspaceID[:]) })
	slices.SortFunc(s.Nodes, func(a, b ledger.GraphNode) int { return bytes.Compare(keys[a.Key], keys[b.Key]) })
	slices.SortFunc(s.Edges, func(a, b ledger.GraphEdge) int { return bytes.Compare(a.EdgeID[:], b.EdgeID[:]) })
	slices.SortFunc(s.Coverage, coverageOrder)
	return s
}
func newReplayState() *replayState {
	index := newGraphIndex()
	return &replayState{retainedCap: retainedLimit, nodeCap: 50000, chainHash: ledger.ZeroHash, registrations: make(map[uuid.UUID]registration), admissions: make(map[uuid.UUID]uuid.UUID), acceptances: make(map[uuid.UUID][]acceptance), current: &graphImage{state: index.state(), index: index, digest: ledger.EmptyGraphDigest}, terminal: make(map[ledger.Digest]attemptSummary), exhausted: make(map[ledger.Digest]bool), analysisIDs: make(map[uuid.UUID]bool)}
}
func (r *replayState) clone() *replayState {
	c := *r
	c.registrations, c.admissions, c.acceptances = maps.Clone(r.registrations), maps.Clone(r.admissions), maps.Clone(r.acceptances)
	c.terminal, c.exhausted, c.analysisIDs = maps.Clone(r.terminal), maps.Clone(r.exhausted), maps.Clone(r.analysisIDs)
	if r.attempt != nil {
		a := *r.attempt
		a.chunks = slices.Clone(a.chunks)
		if a.stage != nil {
			a.stage = a.stage.clone()
		}
		a.removedNodes, a.putNodes = maps.Clone(a.removedNodes), maps.Clone(a.putNodes)
		a.removedEdges, a.putEdges = maps.Clone(a.removedEdges), maps.Clone(a.putEdges)
		c.attempt = &a
	}
	return &c
}
func (r *replayState) latest(id uuid.UUID) (acceptance, bool) {
	all := r.acceptances[id]
	if len(all) == 0 {
		return acceptance{}, false
	}
	return all[len(all)-1], true
}
func (r *replayState) startFor(id uuid.UUID, configuration ledger.Digest, epoch int64) ledger.GraphAnalysisStarted {
	latest, _ := r.latest(id)
	p := ledger.GraphAnalysisStarted{Source: ledger.GraphSource{WorkspaceID: id, Registered: r.registrations[id].ref, Subject: latest.ref, Tree: latest.tree}, InputFrontier: r.frontier, Scope: ledger.GraphScopeCanonical, BaseVersion: r.current.version, BaseDigest: r.current.digest, AnalyzerEpoch: epoch, ConfigurationSHA256: configuration, LimitsProfile: ledger.LimitsProfileV1, HelperProfile: ledger.HelperProfileV1, Reason: ledger.GraphReasonIncremental}
	p.AnalysisKey = AnalysisKey(p)
	return p
}
func (r *replayState) eligible(started ledger.GraphAnalysisStarted) bool {
	latest, ok := r.latest(started.Source.WorkspaceID)
	return ok && latest.ref == started.Source.Subject && r.frontier == started.InputFrontier && r.current.version == started.BaseVersion && r.current.digest == started.BaseDigest
}
func (r *replayState) pending() []uuid.UUID {
	var pending []uuid.UUID
	for id := range r.registrations {
		latest, ok := r.latest(id)
		if ok && r.current.index.scopes[id].Source.Subject != latest.ref {
			pending = append(pending, id)
		}
	}
	slices.SortFunc(pending, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return pending
}
func (r *replayState) next(configuration ledger.Digest, epoch int64) (ledger.GraphAnalysisStarted, bool) {
	for _, id := range r.pending() {
		started := r.startFor(id, configuration, epoch)
		_, terminal := r.terminal[started.AnalysisKey]
		if !terminal && !r.exhausted[exhaustionKey(started)] {
			return started, true
		}
	}
	return ledger.GraphAnalysisStarted{}, false
}
func (r *replayState) view(configuration ledger.Digest, epoch int64) *serviceImage {
	status := Status{State: "ready", Through: r.through, ChainHash: r.chainHash, Version: r.current.version, Digest: r.current.digest, Pending: r.pending()}
	reasons := make(map[string]bool)
	for _, c := range r.current.state.Coverage {
		for _, reason := range c.Reasons {
			reasons[reason] = true
		}
	}
	status.Reasons = slices.Sorted(maps.Keys(reasons))
	if len(status.Pending) != 0 {
		status.State = "unknown"
	}
	if _, ok := r.next(configuration, epoch); ok {
		status.State = "pending"
	}
	last := r.lastAttempt
	if r.attempt != nil {
		last = r.attempt.attemptSummary
		status.State = "pending"
	}
	if last.serviceReason != "" {
		status.State = "unknown"
	}
	if last.serviceReason != "" && !slices.Contains(status.Reasons, last.serviceReason) {
		status.Reasons = append(status.Reasons, last.serviceReason)
		slices.Sort(status.Reasons)
	}
	status.Attempt, status.AttemptKey, status.Charged, status.VerifiedCursor = last.started.AnalysisID, last.started.AnalysisKey, last.reserved, last.cursor
	view := &serviceImage{status: status, state: r.current.state}
	for _, scope := range r.current.state.Scopes {
		latest, _ := r.latest(scope.Source.WorkspaceID)
		view.scopes = append(view.scopes, ScopeView{Scope: scope, Latest: latest.ref, GraphPending: latest.ref != scope.Source.Subject})
	}
	return view
}

func graphType(typ string) bool {
	return typ == ledger.TypeGraphAnalysisStarted || typ == ledger.TypeGraphAnalysisProgress || typ == ledger.TypeGraphDeltaChunk || typ == ledger.TypeGraphVersionCommitted
}
func recordRef(record ledger.Record) ledger.RecordRef {
	hash, _ := ledger.ParseDigest(record.Hash)
	return ledger.RecordRef{Seq: int64(record.Seq), Hash: hash}
}
func history(record ledger.Record, rule string) error {
	return &HistoryError{Seq: record.Seq, Rule: rule}
}

func (r *replayState) reduce(commit ledger.Commit) error {
	if len(commit.Records) == 0 {
		return &HistoryError{Seq: commit.First, Rule: "commit_shape"}
	}
	graph := false
	for _, record := range commit.Records {
		graph = graph || graphType(record.Type)
	}
	if graph {
		if err := r.reduceGraphCommit(commit); err != nil {
			return err
		}
	} else {
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypeWorkspaceRegistered:
				p, err := ledger.DecodeWorkspaceRegistered(record.Payload)
				if err != nil {
					return history(record, "source_binding")
				}
				if _, exists := r.registrations[p.WorkspaceID]; exists {
					return history(record, "source_binding")
				}
				if !r.room(512) {
					return errRetainedCap
				}
				r.metadataMemory += 512
				r.registrations[p.WorkspaceID] = registration{ref: recordRef(record)}
			case ledger.TypePromotionAdmitted:
				p, err := ledger.DecodePromotionAdmitted(record.Payload)
				if err != nil {
					return history(record, "source_binding")
				}
				if _, exists := r.registrations[p.WorkspaceID]; !exists {
					return history(record, "source_binding")
				}
				if _, exists := r.admissions[p.RequestID]; exists {
					return history(record, "source_binding")
				}
				if !r.room(512) {
					return errRetainedCap
				}
				r.metadataMemory += 512
				r.admissions[p.RequestID] = p.WorkspaceID
			case ledger.TypePromotionAccepted:
				p, err := ledger.DecodePromotionAccepted(record.Payload)
				if err != nil {
					return history(record, "source_binding")
				}
				workspace, exists := r.admissions[p.RequestID]
				if !exists {
					return history(record, "source_binding")
				}
				cost := uint64(512 + len(p.PredecessorTree) + len(p.AcceptedTree))
				for _, path := range p.ChangedPaths {
					cost += uint64(512 + len(path))
				}
				if !r.room(cost) {
					return errRetainedCap
				}
				r.metadataMemory += cost
				a := acceptance{ref: recordRef(record), predecessor: p.PredecessorTree, tree: p.AcceptedTree, paths: slices.Clone(p.ChangedPaths)}
				r.acceptances[workspace] = append(slices.Clone(r.acceptances[workspace]), a)
				r.frontier = a.ref
				r.structural++
			}
		}
	}
	r.through, r.chainHash = commit.Last, commit.Records[len(commit.Records)-1].Hash
	return nil
}

func (r *replayState) reduceGraphCommit(commit ledger.Commit) error {
	records := commit.Records
	for _, record := range records {
		if !graphType(record.Type) {
			return history(record, "commit_shape")
		}
	}
	if len(records) > 1 {
		for _, record := range records[:len(records)-1] {
			if record.Type != ledger.TypeGraphDeltaChunk {
				return history(record, "commit_shape")
			}
		}
		last := records[len(records)-1]
		if last.Type != ledger.TypeGraphAnalysisProgress {
			return history(last, "commit_shape")
		}
		p, err := ledger.DecodeGraphAnalysisProgress(last.Payload)
		if err != nil || p.Phase != ledger.GraphPhaseCheckpoint {
			return history(last, "commit_shape")
		}
	} else if records[0].Type == ledger.TypeGraphDeltaChunk {
		return history(records[0], "commit_shape")
	}
	for _, record := range records {
		var analysisID uuid.UUID
		var payload any
		switch record.Type {
		case ledger.TypeGraphAnalysisStarted:
			p, err := ledger.DecodeGraphAnalysisStarted(record.Payload)
			if err != nil {
				return history(record, "source_binding")
			}
			analysisID, payload = p.AnalysisID, p
		case ledger.TypeGraphAnalysisProgress:
			p, err := ledger.DecodeGraphAnalysisProgress(record.Payload)
			if err != nil {
				return history(record, "reservation")
			}
			if len(records) == 1 && p.Phase == ledger.GraphPhaseCheckpoint {
				return history(record, "commit_shape")
			}
			analysisID, payload = p.AnalysisID, p
		case ledger.TypeGraphDeltaChunk:
			p, err := ledger.DecodeGraphDeltaChunk(record.Payload)
			if err != nil {
				return history(record, "chunk_order")
			}
			analysisID, payload = p.AnalysisID, p
		case ledger.TypeGraphVersionCommitted:
			p, err := ledger.DecodeGraphVersionCommitted(record.Payload)
			if err != nil {
				return history(record, "graph_digest")
			}
			analysisID, payload = p.AnalysisID, p
		}
		if record.Actor != ledger.ActorCore || record.OperationID != analysisID || record.MeasurementPath != "graph.analysis" || record.AgentID != (uuid.UUID{}) || record.ExecutionSessionID != (uuid.UUID{}) || record.ContextID != (uuid.UUID{}) || record.TurnID != (uuid.UUID{}) || len(record.BodyRefs) != 0 {
			return history(record, "record_envelope")
		}
		var err error
		switch p := payload.(type) {
		case ledger.GraphAnalysisStarted:
			err = r.start(record, p)
		case ledger.GraphAnalysisProgress:
			err = r.progress(record, p)
		case ledger.GraphDeltaChunk:
			err = r.chunk(record, p)
		case ledger.GraphVersionCommitted:
			err = r.finish(record, p)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *replayState) start(record ledger.Record, p ledger.GraphAnalysisStarted) error {
	if p.AnalysisKey != AnalysisKey(p) {
		return history(record, "analysis_key")
	}
	if r.attempt != nil || r.analysisIDs[p.AnalysisID] || r.exhausted[exhaustionKey(p)] {
		return history(record, "attempt_state")
	}
	if _, exists := r.terminal[p.AnalysisKey]; exists {
		return history(record, "attempt_state")
	}
	registered, exists := r.registrations[p.Source.WorkspaceID]
	if !exists || registered.ref != p.Source.Registered {
		return history(record, "source_binding")
	}
	foundSubject, foundFrontier := false, false
	for id, acceptances := range r.acceptances {
		for _, accepted := range acceptances {
			if accepted.ref == p.InputFrontier {
				foundFrontier = true
			}
			if id == p.Source.WorkspaceID && accepted.ref == p.Source.Subject && accepted.tree == p.Source.Tree {
				foundSubject = true
			}
		}
	}
	if !foundSubject || !foundFrontier {
		return history(record, "source_binding")
	}
	if p.BaseVersion != r.current.version || p.BaseDigest != r.current.digest {
		return history(record, "version")
	}
	if !r.room(1024) {
		return errRetainedCap
	}
	r.metadataMemory += 512
	r.attempt = &replayAttempt{attemptSummary: attemptSummary{started: p}, retained: 512, removedNodes: make(map[ledger.NodeKey]bool), putNodes: make(map[ledger.NodeKey]bool), removedEdges: make(map[ledger.Digest]bool), putEdges: make(map[ledger.Digest]bool)}
	r.analysisIDs[p.AnalysisID] = true
	r.lastEpoch, r.lastConfiguration = p.AnalyzerEpoch, p.ConfigurationSHA256
	return nil
}
func (r *replayState) progress(record ledger.Record, p ledger.GraphAnalysisProgress) error {
	a := r.attempt
	if a == nil || p.AnalysisID != a.started.AnalysisID || p.AnalysisKey != a.started.AnalysisKey || p.Revision != a.revision+1 {
		return history(record, "attempt_state")
	}
	if p.ReservedTotal < a.reserved || p.ReservedTotal > attemptLimit || p.ReservedTotal-a.reserved > quantum {
		return history(record, "reservation")
	}
	if p.Phase == ledger.GraphPhaseReserved {
		if p.ReservedTotal == a.reserved || p.VerifiedCursor != a.cursor {
			return history(record, "reservation")
		}
	} else if p.ReservedTotal != a.reserved {
		return history(record, "reservation")
	}
	if p.Phase == ledger.GraphPhaseCheckpoint {
		if p.VerifiedCursor.OutputIndex != int64(len(a.chunks)) || p.VerifiedCursor.ArtifactIndex < a.cursor.ArtifactIndex || a.reserved == 0 {
			return history(record, "checkpoint_cursor")
		}
	} else if p.VerifiedCursor != a.cursor {
		return history(record, "checkpoint_cursor")
	}
	a.revision, a.reserved, a.cursor, a.phase = p.Revision, p.ReservedTotal, p.VerifiedCursor, p.Phase
	if p.Phase == ledger.GraphPhaseUnknown || p.Phase == ledger.GraphPhaseSuperseded {
		exhausted := false
		for _, coverage := range p.Coverage {
			exhausted = exhausted || slices.Contains(coverage.Reasons, ledger.ReasonWorkExhausted)
			for _, reason := range coverage.Reasons {
				if reason == ledger.ReasonStateCap || reason == ledger.ReasonRecoveryUnknown {
					a.serviceReason = reason
				}
			}
		}
		if exhausted {
			if p.Phase != ledger.GraphPhaseUnknown || a.reserved != attemptLimit {
				return history(record, "reservation")
			}
			if !r.room(512) {
				return errRetainedCap
			}
			r.metadataMemory += 512
			r.exhausted[exhaustionKey(a.started)] = true
		}
		return r.closeAttempt(a.phase)
	}
	return nil
}
func (r *replayState) closeAttempt(phase string) error {
	if r.metadataMemory+r.current.memory > r.retainedCap || 512 > r.retainedCap-(r.metadataMemory+r.current.memory) {
		return errRetainedCap
	}
	r.metadataMemory += 512
	summary := r.attempt.attemptSummary
	summary.phase = phase
	r.terminal[summary.started.AnalysisKey], r.lastAttempt = summary, summary
	r.attempt = nil
	return nil
}

func coverageWorkspace(entry ledger.CoverageEntry, workspace uuid.UUID) bool {
	if entry.Scope == workspaceScope(workspace) {
		return true
	}
	var key ledger.NodeKey
	return json.Unmarshal([]byte(entry.Scope), &key) == nil && key.OriginID == workspace
}
func (r *replayState) chunk(record ledger.Record, p ledger.GraphDeltaChunk) error {
	a := r.attempt
	if a == nil || p.AnalysisID != a.started.AnalysisID || p.AnalysisKey != a.started.AnalysisKey || a.reserved == 0 {
		return history(record, "attempt_state")
	}
	if p.ChunkIndex != int64(len(a.chunks)) || len(record.Payload) > chunkLimit {
		return history(record, "chunk_order")
	}
	if a.stage == nil {
		if !r.room(r.current.memory + scopeMemory(startedScope(a.started))) {
			return errRetainedCap
		}
		a.stage = r.current.index.clone()
		a.stage.putScope(startedScope(a.started))
		for key, coverage := range a.stage.coverage {
			if coverageWorkspace(coverage, a.started.Source.WorkspaceID) {
				a.stage.memory -= coverageMemory(coverage)
				delete(a.stage.coverage, key)
			}
		}
	}
	for phase, count := range []int{len(p.RemoveEdges), len(p.RemoveNodes), len(p.PutNodes), len(p.PutEdges), len(p.Coverage)} {
		if count == 0 {
			continue
		}
		if phase < a.mutationPhase {
			return history(record, "chunk_order")
		}
		a.mutationPhase = phase
	}
	for _, key := range p.RemoveEdges {
		if a.putEdges[key] || a.removedEdges[key] {
			return history(record, "chunk_order")
		}
		if _, exists := a.stage.edges[key]; !exists {
			return history(record, "dangling")
		}
		a.stage.removeEdge(key)
		a.retained += 256
		a.removedEdges[key] = true
	}
	for _, key := range p.RemoveNodes {
		if a.putNodes[key] || a.removedNodes[key] {
			return history(record, "chunk_order")
		}
		if _, exists := a.stage.nodes[key]; !exists {
			return history(record, "dangling")
		}
		if len(a.stage.adjacency[key]) != 0 {
			return history(record, "dangling")
		}
		a.stage.removeNode(key)
		a.retained += 512
		a.removedNodes[key] = true
	}
	for _, node := range p.PutNodes {
		if a.removedNodes[node.Key] || a.putNodes[node.Key] || a.stage.nodes[node.Key] == node {
			return history(record, "chunk_order")
		}
		if node.Key.OriginID != a.started.Source.WorkspaceID {
			return history(record, "source_binding")
		}
		if _, exists := a.stage.nodes[node.Key]; !exists && int64(len(a.stage.nodes)) == r.nodeCap {
			return history(record, "state_cap")
		}
		if !r.room(nodeMemory(node) + 512) {
			return errRetainedCap
		}
		a.stage.putNode(node)
		a.putNodes[node.Key] = true
		a.retained += 512
	}
	for _, edge := range p.PutEdges {
		if a.removedEdges[edge.EdgeID] || a.putEdges[edge.EdgeID] || a.stage.edges[edge.EdgeID] == edge {
			return history(record, "chunk_order")
		}
		if _, ok := a.stage.nodes[edge.From]; !ok {
			return history(record, "dangling")
		}
		if _, ok := a.stage.nodes[edge.To]; !ok {
			return history(record, "dangling")
		}
		if _, ok := a.stage.nodes[edge.Evidence.Artifact]; !ok {
			return history(record, "dangling")
		}
		if _, exists := a.stage.edges[edge.EdgeID]; !exists && len(a.stage.edges) == 200000 {
			return history(record, "state_cap")
		}
		if !r.room(edgeMemory(edge) + 256) {
			return errRetainedCap
		}
		a.stage.putEdge(edge)
		a.putEdges[edge.EdgeID] = true
		a.retained += 256
	}
	for _, entry := range p.Coverage {
		key := coverageKey(entry)
		if _, duplicate := a.stage.coverage[key]; duplicate || !coverageWorkspace(entry, a.started.Source.WorkspaceID) {
			return history(record, "chunk_order")
		}
		if !r.room(coverageMemory(entry)) {
			return errRetainedCap
		}
		a.stage.putCoverage(entry)
	}
	if int64(len(a.stage.nodes)) > r.nodeCap || len(a.stage.edges) > 200000 {
		return history(record, "state_cap")
	}
	if !r.room(uint64(len(record.Payload)) + 512) {
		return errRetainedCap
	}
	a.retained += uint64(len(record.Payload)) + 512
	a.chunks = append(a.chunks, slices.Clone(record.Payload))
	return nil
}
func (r *replayState) finish(record ledger.Record, p ledger.GraphVersionCommitted) error {
	a := r.attempt
	if a == nil || p.AnalysisID != a.started.AnalysisID || p.AnalysisKey != a.started.AnalysisKey {
		return history(record, "attempt_state")
	}
	if p.BaseVersion != a.started.BaseVersion || p.BaseDigest != a.started.BaseDigest {
		return history(record, "version")
	}
	// Stale work is not a malformed current image, and cannot reset its counter.
	if !r.eligible(a.started) {
		return r.closeAttempt(ledger.GraphPhaseSuperseded)
	}
	if p.ChunkCount != int64(len(a.chunks)) {
		return history(record, "chunk_order")
	}
	hash := sha256.New()
	for _, chunk := range a.chunks {
		_, _ = hash.Write(chunk)
	}
	if p.ChunksSHA256 != ledger.Digest(hash.Sum(nil)) {
		return history(record, "chunk_digest")
	}
	index := a.stage
	if index == nil {
		index = r.current.index
	}
	state := index.state()
	_, digest, err := state.Canonical()
	if err != nil {
		return history(record, "dangling")
	}
	if digest != p.GraphDigest || p.ChunkCount == 0 && index.scopes[a.started.Source.WorkspaceID] != startedScope(a.started) {
		return history(record, "graph_digest")
	}
	structural := r.structural
	if a.started.Reason != ledger.GraphReasonIncremental {
		structural = 0
	}
	if p.StructuralSinceRebuild != structural {
		return history(record, "counter")
	}
	version := r.current.version
	if p.ChunkCount != 0 {
		version = int64(record.Seq)
	}
	r.current = &graphImage{state: state, index: index, version: version, digest: digest, memory: index.memory}
	r.structural = structural
	return r.closeAttempt(ledger.TypeGraphVersionCommitted)
}

// The fixed coefficients include map/index entries, pointers and allocator
// overhead. Variable text and serialized copies are charged separately.
func nodeMemory(node ledger.GraphNode) uint64 {
	return 1024 + uint64(len(keyBytes(node.Key))+len(node.Adapter))*2
}
func edgeMemory(edge ledger.GraphEdge) uint64 {
	encoded, _ := json.Marshal(edge)
	return 5*256 + uint64(len(encoded))*2
}
func coverageMemory(entry ledger.CoverageEntry) uint64 {
	n := 512 + uint64(len(entry.Scope)+len(entry.Adapter))*2
	for _, reason := range entry.Reasons {
		n += uint64(len(reason)) * 2
	}
	return n
}
func scopeMemory(scope Scope) uint64 {
	encoded, _ := json.Marshal(scope)
	return 512 + uint64(len(encoded))*2
}

func graphDraft(typ string, id uuid.UUID, payload []byte) ledger.Draft {
	return ledger.Draft{Type: typ, TypeVersion: 1, Actor: ledger.ActorCore, OperationID: id, MeasurementPath: "graph.analysis", Payload: payload}
}
func reasonCoverage(workspace uuid.UUID, reason string) []ledger.CoverageEntry {
	return []ledger.CoverageEntry{{Scope: workspaceScope(workspace), Adapter: ledger.AdapterOpaque, State: ledger.CoverageUnknown, Reasons: []string{reason}}}
}

var errRetainedCap = errors.New("graph retained projection limit")

func (r *replayState) retained() uint64 {
	total := r.metadataMemory + r.current.memory
	if r.attempt != nil {
		total += r.attempt.retained
		if r.attempt.stage != nil {
			total += r.attempt.stage.memory
		}
	}
	return total
}
func (r *replayState) room(additional uint64) bool {
	used := r.retained()
	return used <= r.retainedCap && additional <= r.retainedCap-used
}

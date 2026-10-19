package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"path"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type toolSlot struct {
	verification *ToolVerification
	verified     *VerifiedTool
	failed       bool
}
type workOutput struct {
	chunk  []byte
	final  bool
	reason string
}
type workState struct {
	started                      ledger.GraphAnalysisStarted
	scopes                       scopeSet
	orderedScopes                []Scope
	scopeIndex                   int
	failure                      string
	generation                   *generationWalk
	stage                        *graphIndex
	memory                       uint64
	phase                        int
	corpus                       []CorpusArtifact
	languages                    [3][]CorpusArtifact
	languageBytes                [3]uint64
	after                        string
	stalls                       int
	opaqueAt                     int
	cursor                       int64
	nodes                        boundedMerge[ledger.GraphNode]
	edges                        boundedMerge[ledger.GraphEdge]
	coverage                     boundedMerge[ledger.CoverageEntry]
	sorted                       State
	diff                         ledger.GraphDeltaChunk
	diffPhase, diffAt            int
	formedNodes, formedEdges     int
	formedMemory                 uint64
	builder                      ledger.GraphDeltaChunk
	builderSize, builderElements int
	splitPhase, splitAt          int
	chunks                       [][]byte
	chunksHash                   hash.Hash
	canonical                    *canonicalWork
	digest                       ledger.Digest
	baseCanonical                *canonicalWork
	objects                      map[string]corpusObject
	catalog, fetch               []Artifact
	catalogAt, fetchAt           int
	requested                    map[string]bool
	failures                     map[string]string
	corpusMemory, catalogMemory  uint64
}

func (s *Service) newWork(started ledger.GraphAnalysisStarted) *workState {
	scopes := s.replay.attempt.scopes
	w := &workState{started: started, scopes: scopes, stage: newGraphIndex(), memory: 2 * maxFrame, chunksHash: sha256.New(), objects: make(map[string]corpusObject)}
	w.formedNodes, w.formedEdges, w.formedMemory = len(s.replay.current.state.Nodes), len(s.replay.current.state.Edges), s.replay.current.memory
	for id, scope := range scopes {
		cost := 2 * scopeMemory(scope)
		if w.memory > stagingLimit || cost > stagingLimit-w.memory {
			w.failure = ledger.ReasonTemporaryCap
			return w
		}
		w.memory += cost
		if old, exists := s.replay.current.index.scopes[id]; exists {
			w.formedMemory -= scopeMemory(old)
		}
		w.formedMemory += scopeMemory(scope)
		w.stage.putScope(scope)
	}
	w.orderedScopes = scopes.sorted()
	w.beginScope(s)
	w.nodes.compare = func(a, b ledger.GraphNode) int { return bytes.Compare(keyBytes(a.Key), keyBytes(b.Key)) }
	w.edges.compare = func(a, b ledger.GraphEdge) int { return bytes.Compare(a.EdgeID[:], b.EdgeID[:]) }
	w.coverage.compare = coverageOrder
	return w
}

func (s *Service) drive(ctx context.Context, resume *uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if resume != nil {
		if s.replay.attempt == nil || s.replay.attempt.started.AnalysisID != *resume {
			return ErrInvalid
		}
		if !s.replay.eligible(s.replay.attempt.started) {
			return s.endAttempt(ctx, ledger.GraphPhaseSuperseded, ledger.ReasonStaleSource)
		}
		if s.replay.attempt.started.ConfigurationSHA256 != s.configuration {
			return s.endAttempt(ctx, ledger.GraphPhaseUnknown, ledger.ReasonRecoveryUnknown)
		}
		s.work = s.newWork(s.replay.attempt.started)
		s.recovery = false
	} else if s.recovery {
		return ErrRecovery
	}
	if s.replay.attempt == nil {
		selection := s.replay.next(s.configuration, s.epoch, s.rebuildReason())
		if !selection.runnable {
			return nil
		}
		started := selection.started
		if !s.replay.room(1024 + s.generationMemory) {
			s.stopProjection(s.replay.through, s.replay.chainHash)
			return nil
		}
		started.AnalysisID = uuid.NewV7()
		payload, err := started.Encode()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if err := s.append(ctx, []ledger.Draft{graphDraft(ledger.TypeGraphAnalysisStarted, started.AnalysisID, payload)}); err != nil {
			return err
		}
		if !s.replay.eligible(started) {
			return s.endAttempt(ctx, ledger.GraphPhaseSuperseded, ledger.ReasonStaleSource)
		}
		s.work = s.newWork(started)
		return nil
	}
	a := s.replay.attempt
	if !s.replay.eligible(a.started) {
		return s.endAttempt(ctx, ledger.GraphPhaseSuperseded, ledger.ReasonStaleSource)
	}
	if a.reserved == attemptLimit {
		return s.endAttempt(ctx, ledger.GraphPhaseUnknown, ledger.ReasonWorkExhausted)
	}
	if s.work == nil {
		s.recovery = true
		return ErrRecovery
	}
	allowance := min(quantum, attemptLimit-a.reserved)
	reservation := ledger.GraphAnalysisProgress{AnalysisID: a.started.AnalysisID, AnalysisKey: a.started.AnalysisKey, Revision: a.revision + 1, Phase: ledger.GraphPhaseReserved, ReservedTotal: a.reserved + allowance, VerifiedCursor: a.cursor}
	payload, err := reservation.Encode()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := s.append(ctx, []ledger.Draft{graphDraft(ledger.TypeGraphAnalysisProgress, a.started.AnalysisID, payload)}); err != nil {
		return err
	}
	if !s.replay.eligible(s.replay.attempt.started) {
		return s.endAttempt(ctx, ledger.GraphPhaseSuperseded, ledger.ReasonStaleSource)
	}
	output, err := s.work.unit(ctx, s, allowance)
	if err != nil {
		return err
	}
	if output == nil {
		return nil
	}
	if output.reason != "" {
		return s.endAttempt(ctx, ledger.GraphPhaseUnknown, output.reason)
	}
	a = s.replay.attempt
	if output.final {
		final := ledger.GraphVersionCommitted{AnalysisID: a.started.AnalysisID, AnalysisKey: a.started.AnalysisKey, BaseVersion: a.started.BaseVersion, BaseDigest: a.started.BaseDigest, ChunkCount: int64(len(s.work.chunks)), ChunksSHA256: ledger.Digest(s.work.chunksHash.Sum(nil)), GraphDigest: s.work.digest, StructuralSinceRebuild: s.replay.structural}
		if a.started.Reason != ledger.GraphReasonIncremental {
			final.StructuralSinceRebuild = 0
		}
		payload, err := final.Encode()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		work := s.work
		if err := s.append(ctx, []ledger.Draft{graphDraft(ledger.TypeGraphVersionCommitted, a.started.AnalysisID, payload)}); err != nil {
			return err
		}
		if a.started.Reason == ledger.GraphReasonIncremental && s.replay.current.index.scopes[a.started.Source.WorkspaceID].Source == a.started.Source {
			s.generations[a.started.Source.WorkspaceID] = work.generation.generationState
			s.generationMemory += work.generation.memory
		} else if a.started.Reason != ledger.GraphReasonIncremental && s.replay.lastAttempt.phase == ledger.TypeGraphVersionCommitted {
			s.generations = make(map[uuid.UUID]generationState)
			s.generationMemory = 0
		}
		s.work = nil
		return nil
	}
	checkpoint := ledger.GraphAnalysisProgress{AnalysisID: a.started.AnalysisID, AnalysisKey: a.started.AnalysisKey, Revision: a.revision + 1, Phase: ledger.GraphPhaseCheckpoint, ReservedTotal: a.reserved, VerifiedCursor: ledger.GraphCursor{ArtifactIndex: s.work.cursor, OutputIndex: int64(len(a.chunks) + 1)}}
	payload, err = checkpoint.Encode()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	drafts := []ledger.Draft{graphDraft(ledger.TypeGraphDeltaChunk, a.started.AnalysisID, output.chunk), graphDraft(ledger.TypeGraphAnalysisProgress, a.started.AnalysisID, payload)}
	if len(output.chunk)+len(payload)+2*2048 > ledger.MaxCommitBytes {
		return s.endAttempt(ctx, ledger.GraphPhaseUnknown, ledger.ReasonTemporaryCap)
	}
	if err := s.append(ctx, drafts); err != nil {
		return err
	}
	return nil
}

func (s *Service) endAttempt(ctx context.Context, phase, reason string) error {
	a := s.replay.attempt
	if !s.replay.room(1024) {
		s.stopProjection(s.replay.through, s.replay.chainHash)
		return nil
	}
	workspace := a.started.Source.WorkspaceID
	if s.work != nil && s.work.generation != nil {
		workspace = s.work.source().WorkspaceID
	}
	progress := ledger.GraphAnalysisProgress{AnalysisID: a.started.AnalysisID, AnalysisKey: a.started.AnalysisKey, Revision: a.revision + 1, Phase: phase, ReservedTotal: a.reserved, VerifiedCursor: a.cursor, Coverage: reasonCoverage(workspace, reason)}
	payload, err := progress.Encode()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := s.append(ctx, []ledger.Draft{graphDraft(ledger.TypeGraphAnalysisProgress, a.started.AnalysisID, payload)}); err != nil {
		return err
	}
	s.work, s.recovery = nil, false
	return nil
}

func (w *workState) retain(cost uint64) bool {
	used := w.memory + w.generation.memory
	if used > stagingLimit || cost > stagingLimit-used {
		return false
	}
	w.memory += cost
	return true
}
func (w *workState) request(corpus []CorpusArtifact, allowance int64) AnalysisRequest {
	return AnalysisRequest{AnalysisKey: w.started.AnalysisKey, Source: w.source(), ConfigurationSHA256: w.started.ConfigurationSHA256, Corpus: corpus, Allowance: allowance}
}
func (w *workState) addCoverage(entries []ledger.CoverageEntry) bool {
	var fresh []ledger.CoverageEntry
	for _, entry := range entries {
		key := coverageKey(entry)
		if _, exists := w.stage.coverage[key]; !exists {
			if !w.retain(coverageMemory(entry) + 1024) {
				return false
			}
			fresh = append(fresh, entry)
		}
		w.stage.putCoverage(entry)
	}
	if len(fresh) != 0 {
		w.coverage.runs = append(w.coverage.runs, fresh)
	}
	return true
}
func (w *workState) addResult(result AnalysisResult, allowance int64) string {
	if result.WorkUsed < 0 || result.WorkUsed > allowance {
		return ledger.ReasonHelperLimit
	}
	for _, node := range result.Nodes {
		if _, duplicate := w.stage.nodes[node.Key]; duplicate {
			return ledger.ReasonHelperLimit
		}
		if !w.retain(nodeMemory(node) + 1024) {
			return ledger.ReasonTemporaryCap
		}
		w.stage.putNode(node)
	}
	for _, edge := range result.Edges {
		if _, duplicate := w.stage.edges[edge.EdgeID]; duplicate {
			return ledger.ReasonHelperLimit
		}
		if !w.retain(edgeMemory(edge) + 1024) {
			return ledger.ReasonTemporaryCap
		}
		w.stage.putEdge(edge)
	}
	if len(result.Nodes) != 0 {
		w.nodes.runs = append(w.nodes.runs, result.Nodes)
	}
	if len(result.Edges) != 0 {
		w.edges.runs = append(w.edges.runs, result.Edges)
	}
	if !w.addCoverage(result.Coverage) {
		return ledger.ReasonTemporaryCap
	}
	return ""
}

func (w *workState) unit(ctx context.Context, service *Service, allowance int64) (*workOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.failure != "" {
		return &workOutput{reason: w.failure}, nil
	}
	var reason string
	switch w.phase {
	case 0:
		for allowance >= 512 && !w.generation.done() {
			g := w.generation
			beforeAccepted, beforeAfter, beforeInitial := g.accepted, g.after, g.initial
			available := stagingLimit - min(stagingLimit, w.memory+g.memory)
			used, failure, err := g.step(ctx, service.opts.ReadArtifacts, allowance, available)
			if err != nil {
				return nil, err
			}
			reason = failure
			allowance -= used
			if reason != "" || beforeAccepted == g.accepted && beforeAfter == g.after && beforeInitial == g.initial {
				break
			}
		}
		if w.generation.done() && reason == "" {
			if w.started.Reason == ledger.GraphReasonIncremental && service.deps.incrementalBirths != nil {
				service.deps.incrementalBirths(w.source().WorkspaceID, w.generation.births)
			}
			w.phase = 1
		}
	case 1, 13:
		phase := w.phase
		for allowance >= 512 && w.phase == phase {
			beforeAfter, beforeFetch := w.after, w.fetchAt
			read := w.listCorpus
			if phase == 13 {
				read = w.readCorpus
			}
			used, failure, err := read(ctx, service, allowance)
			if err != nil {
				return nil, err
			}
			reason = failure
			allowance -= used
			if reason != "" || beforeAfter == w.after && beforeFetch == w.fetchAt && w.phase == phase {
				break
			}
		}
	case 14:
		reason = w.formCorpus(allowance)
	case 2:
		for allowance >= 512 && w.phase == 2 {
			end, charge := w.opaqueAt, int64(0)
			for end < len(w.corpus) {
				units := int64(2 + (len(w.corpus[end].Bytes)+255)/256)
				if units > allowance-charge {
					break
				}
				charge += units
				end++
			}
			if end == w.opaqueAt && end < len(w.corpus) {
				break
			}
			result, err := AnalyzeOpaque(ctx, w.request(w.corpus[w.opaqueAt:end], allowance))
			if err != nil {
				return nil, err
			}
			reason = w.addResult(result, allowance)
			allowance -= result.WorkUsed
			w.cursor += int64(end - w.opaqueAt)
			w.opaqueAt = end
			if end == len(w.corpus) {
				w.phase = 3
			}
			if reason != "" {
				break
			}
		}
	case 3, 4, 5:
		language := w.phase - 3
		adapter := []string{ledger.AdapterPython, ledger.AdapterSQL, ledger.AdapterTypeScript}[language]
		if len(w.languages[language]) == 0 {
			w.phase++
			return nil, nil
		}
		if language != 1 && len(w.languages[language]) <= maxItems && w.languageBytes[language] <= maxFrame {
			consumed, err := service.verifyAdapter(ctx, adapter, allowance)
			if err != nil {
				return nil, err
			}
			if consumed {
				return nil, nil
			}
		}
		request := w.request(w.languages[language], allowance)
		var result AnalysisResult
		var err error
		if len(request.Corpus) > maxItems || w.languageBytes[language] > maxFrame {
			result = unknown(request, adapter, ledger.ReasonHelperLimit, 1)
		} else if language == 1 {
			result, err = AnalyzeSQL(ctx, request)
		} else {
			result, err = service.analyzer.Analyze(ctx, adapter, request)
		}
		if err != nil {
			return nil, err
		}
		reason = w.addResult(result, allowance)
		w.cursor += int64(len(request.Corpus))
		w.phase++
	case 6:
		coverage := reasonCoverage(w.source().WorkspaceID, ledger.ReasonDependencyUnapproved)
		coverage[0].Adapter = "go"
		if !w.addCoverage(coverage) {
			reason = ledger.ReasonTemporaryCap
		}
		if reason == "" && w.scopeIndex+1 < len(w.orderedScopes) {
			w.scopeIndex++
			w.beginScope(service)
		} else {
			w.phase = 7
		}
	case 7:
		remaining := allowance
		if !w.nodes.step(&remaining) || !w.edges.step(&remaining) || !w.coverage.step(&remaining) {
			return nil, nil
		}
		w.sorted = State{Scopes: w.orderedScopes, Nodes: w.nodes.result(), Edges: w.edges.result(), Coverage: w.coverage.result()}
		if len(service.replay.attempt.chunks) != 0 && service.replay.attempt.cursor.ArtifactIndex != w.cursor {
			reason = ledger.ReasonRecoveryUnknown
		} else {
			w.phase = 8
		}
	case 8:
		var done bool
		done, reason = w.buildDiff(service.replay.current, allowance)
		if done && reason == "" {
			if int64(w.formedNodes) > service.replay.nodeCap || int64(w.formedEdges) > service.replay.edgeCap || !service.replay.room(w.formedMemory+w.memory+w.generation.memory+service.generationMemory) {
				reason = ledger.ReasonStateCap
			} else {
				w.phase = 11
			}
		}
	case 9:
		return w.emitChunk(service.replay.attempt, allowance)
	case 10:
		if len(w.chunks) < len(service.replay.attempt.chunks) {
			reason = ledger.ReasonRecoveryUnknown
		} else {
			return &workOutput{final: true}, nil
		}
	case 11:
		if w.canonical == nil {
			w.canonical = newCanonicalWork(service.replay.current.state, w.sorted, w.scopes)
		}
		if w.canonical.step(allowance) {
			w.digest = ledger.Digest(w.canonical.hash.Sum(nil))
			w.phase = 9
			if w.started.Reason != ledger.GraphReasonIncremental {
				w.phase = 12
			}
		}
	case 12:
		if w.baseCanonical == nil {
			w.baseCanonical = newCanonicalWork(State{}, service.replay.current.state, nil)
		}
		if w.baseCanonical.step(allowance) {
			baseDigest := ledger.Digest(w.baseCanonical.hash.Sum(nil))
			if baseDigest != service.replay.current.digest {
				reason = ledger.ReasonRecoveryUnknown
			} else if baseDigest == w.digest {
				if len(service.replay.attempt.chunks) != 0 {
					reason = ledger.ReasonRecoveryUnknown
				} else {
					return &workOutput{final: true}, nil
				}
			} else {
				w.phase = 9
			}
		}
	}
	if reason != "" {
		return &workOutput{reason: reason}, nil
	}
	return nil, nil
}

func (w *workState) formCorpus(allowance int64) string {
	for w.catalogAt < len(w.catalog) {
		entry := w.catalog[w.catalogAt]
		cost := int64(2 + (len(entry.Path)+255)/256)
		if cost > allowance {
			return ""
		}
		allowance -= cost
		birth, present := w.generation.births[entry.Path]
		if !present {
			return ledger.ReasonSourceUnavailable
		}
		key, err := ArtifactKey(w.source().WorkspaceID, birth, entry.Path)
		if err != nil {
			return ledger.ReasonHelperLimit
		}
		object, exists := w.objects[entry.GitOID]
		reason := entry.Reason
		if reason == "" && !exists {
			reason = w.failures[entry.GitOID]
			if reason == "" {
				return ledger.ReasonSourceUnavailable
			}
		}
		if reason != "" {
			if !w.addCoverage([]ledger.CoverageEntry{covered(key, ledger.AdapterOpaque, reason)}) {
				return ledger.ReasonTemporaryCap
			}
		} else {
			memory := uint64(1024 + len(entry.Path) + len(key.LocalKey))
			if !w.retain(memory) {
				return ledger.ReasonTemporaryCap
			}
			w.corpusMemory += memory
			artifact := CorpusArtifact{Path: entry.Path, Key: key, SHA256: object.digest, Bytes: object.body}
			w.corpus = append(w.corpus, artifact)
			language := -1
			switch sourceAdapter(entry.Path) {
			case ledger.AdapterPython:
				language = 0
			case ledger.AdapterSQL:
				language = 1
			case ledger.AdapterTypeScript:
				language = 2
			}
			if path.Ext(entry.Path) == ".json" {
				language = 2
			}
			if language >= 0 {
				w.languages[language] = append(w.languages[language], artifact)
				w.languageBytes[language] += uint64(len(object.body))
			}
		}
		w.catalogAt++
	}
	w.memory -= w.catalogMemory
	w.catalogMemory = 0
	w.catalog, w.fetch, w.requested, w.failures = nil, nil, nil, nil
	w.phase = 2
	return ""
}

func (s *Service) verifyAdapter(ctx context.Context, adapter string, allowance int64) (bool, error) {
	indices := []int{0}
	if adapter == ledger.AdapterTypeScript {
		indices = []int{1, 2}
	}
	configured := []*Tool{s.opts.Tools.Python, s.opts.Tools.Node, s.opts.Tools.TypeScriptParser}
	for _, index := range indices {
		slot := &s.tools[index]
		if configured[index] == nil || slot.failed {
			continue
		}
		if slot.verified != nil {
			if slot.verified.unchanged() {
				continue
			}
			slot.verified, slot.verification = nil, nil
		}
		if slot.verification == nil {
			verification, err := NewToolVerification(*configured[index])
			if err != nil {
				slot.failed = true
				return true, nil
			}
			slot.verification = verification
		}
		progress, err := slot.verification.Step(ctx, allowance)
		if err != nil {
			if ctx.Err() != nil {
				return true, ctx.Err()
			}
			slot.failed = true
			_ = slot.verification.Close()
			slot.verification = nil
			return true, nil
		}
		if progress.WorkUsed > allowance {
			slot.failed = true
			_ = slot.verification.Close()
			slot.verification = nil
			return true, nil
		}
		if progress.Verified != nil {
			slot.verified, slot.verification = progress.Verified, nil
		}
		return true, nil
	}
	s.analyzer.tools = Tools{Python: s.tools[0].verified, Node: s.tools[1].verified, TypeScriptParser: s.tools[2].verified}
	return false, nil
}

// boundedMerge normalizes already sorted adapter runs without an uncharged
// whole-workspace sort. Each comparison, dequeue and enqueue is charged.
type boundedMerge[T any] struct {
	runs, next        [][]T
	compare           func(T, T) int
	pair, left, right int
	output            []T
}

func (m *boundedMerge[T]) step(remaining *int64) bool {
	for len(m.runs) > 1 {
		if m.pair == len(m.runs) {
			m.runs, m.next, m.pair = m.next, nil, 0
			continue
		}
		if m.pair+1 == len(m.runs) {
			m.next = append(m.next, m.runs[m.pair])
			m.pair++
			continue
		}
		left, right := m.runs[m.pair], m.runs[m.pair+1]
		if m.output == nil {
			if *remaining < 1 {
				return false
			}
			*remaining -= 1
			m.output = make([]T, 0, len(left)+len(right))
		}
		for m.left < len(left) || m.right < len(right) {
			if *remaining < 3 {
				return false
			}
			*remaining -= 3
			if m.right == len(right) || m.left < len(left) && m.compare(left[m.left], right[m.right]) <= 0 {
				m.output = append(m.output, left[m.left])
				m.left++
			} else {
				m.output = append(m.output, right[m.right])
				m.right++
			}
		}
		m.next = append(m.next, m.output)
		m.output, m.left, m.right = nil, 0, 0
		m.pair += 2
	}
	return true
}
func (m *boundedMerge[T]) result() []T {
	if len(m.runs) == 0 {
		return nil
	}
	return m.runs[0]
}

// elementUnits bounds encoded-byte work before allocating JSON. A JSON string
// expands by at most six bytes per UTF-8 input byte; fixed fields fit the base.
func elementUnits(value any) int64 {
	size := 512
	keySize := func(key ledger.NodeKey) int { return len(key.Namespace) + len(key.Kind) + len(key.LocalKey) + 36 }
	switch value := value.(type) {
	case ledger.GraphNode:
		size += 6 * (keySize(value.Key) + len(value.Adapter))
	case ledger.GraphEdge:
		size += 6*(keySize(value.From)+keySize(value.To)+keySize(value.Evidence.Artifact)+len(value.Type)+len(value.Geometry)+len(value.Evidence.Adapter)+len(value.Evidence.Reference)) + 512
	case ledger.CoverageEntry:
		size += 6 * (len(value.Scope) + len(value.Adapter) + len(value.State))
		for _, reason := range value.Reasons {
			size += 6 * len(reason)
		}
	case Scope:
		size += 6 * (len(value.Source.Tree) + len(value.Scope))
	case ledger.NodeKey:
		size += 6 * keySize(value)
	case ledger.Digest:
		size = 66
	}
	return 1 + int64((size+255)/256)
}

func (w *workState) buildDiff(base *graphImage, allowance int64) (bool, string) {
	for allowance > 0 {
		var value any
		switch w.diffPhase {
		case 0:
			if w.diffAt == len(base.state.Edges) {
				w.diffPhase++
				w.diffAt = 0
				continue
			}
			value = base.state.Edges[w.diffAt]
		case 1:
			if w.diffAt == len(base.state.Nodes) {
				w.diffPhase++
				w.diffAt = 0
				continue
			}
			value = base.state.Nodes[w.diffAt]
		case 2:
			if w.diffAt == len(w.sorted.Nodes) {
				w.diffPhase++
				w.diffAt = 0
				continue
			}
			value = w.sorted.Nodes[w.diffAt]
		case 3:
			if w.diffAt == len(w.sorted.Edges) {
				w.diffPhase++
				w.diffAt = 0
				continue
			}
			value = w.sorted.Edges[w.diffAt]
		case 4:
			if w.diffAt == len(base.state.Coverage) {
				w.diffPhase++
				w.diffAt = 0
				continue
			}
			value = base.state.Coverage[w.diffAt]
		case 5:
			if w.diffAt == len(w.sorted.Coverage) {
				return true, ""
			}
			value = w.stage.coverage[coverageKey(w.sorted.Coverage[w.diffAt])]
		}
		cost := elementUnits(value)
		if cost > allowance {
			return false, ""
		}
		allowance -= cost
		switch w.diffPhase {
		case 0:
			edge := value.(ledger.GraphEdge)
			if _, exists := w.stage.edges[edge.EdgeID]; w.scopes.ownsEdge(edge) && !exists {
				if !w.retain(256) {
					return false, ledger.ReasonTemporaryCap
				}
				w.diff.RemoveEdges = append(w.diff.RemoveEdges, edge.EdgeID)
				w.formedEdges--
				w.formedMemory -= edgeMemory(edge)
			}
		case 1:
			node := value.(ledger.GraphNode)
			if _, exists := w.stage.nodes[node.Key]; w.scopes.ownsNode(node.Key) && !exists {
				if !w.retain(512) {
					return false, ledger.ReasonTemporaryCap
				}
				w.diff.RemoveNodes = append(w.diff.RemoveNodes, node.Key)
				w.formedNodes--
				w.formedMemory -= nodeMemory(node)
			}
		case 2:
			node := value.(ledger.GraphNode)
			if old, exists := base.index.nodes[node.Key]; old != node {
				w.diff.PutNodes = append(w.diff.PutNodes, node)
				if exists {
					w.formedMemory -= nodeMemory(old)
				} else {
					w.formedNodes++
				}
				w.formedMemory += nodeMemory(node)
			}
		case 3:
			edge := value.(ledger.GraphEdge)
			if old, exists := base.index.edges[edge.EdgeID]; old != edge {
				w.diff.PutEdges = append(w.diff.PutEdges, edge)
				if exists {
					w.formedMemory -= edgeMemory(old)
				} else {
					w.formedEdges++
				}
				w.formedMemory += edgeMemory(edge)
			}
		case 4:
			entry := value.(ledger.CoverageEntry)
			if w.scopes.ownsCoverage(entry) {
				w.formedMemory -= coverageMemory(entry)
			}
		case 5:
			entry := value.(ledger.CoverageEntry)
			w.sorted.Coverage[w.diffAt] = entry
			w.diff.Coverage = append(w.diff.Coverage, entry)
			w.formedMemory += coverageMemory(entry)
		}
		w.diffAt++
	}
	return false, ""
}

func (w *workState) nextMutation() (any, bool) {
	for w.splitPhase < 5 {
		switch w.splitPhase {
		case 0:
			if w.splitAt < len(w.diff.RemoveEdges) {
				return w.diff.RemoveEdges[w.splitAt], true
			}
		case 1:
			if w.splitAt < len(w.diff.RemoveNodes) {
				return w.diff.RemoveNodes[w.splitAt], true
			}
		case 2:
			if w.splitAt < len(w.diff.PutNodes) {
				return w.diff.PutNodes[w.splitAt], true
			}
		case 3:
			if w.splitAt < len(w.diff.PutEdges) {
				return w.diff.PutEdges[w.splitAt], true
			}
		case 4:
			if w.splitAt < len(w.diff.Coverage) {
				return w.diff.Coverage[w.splitAt], true
			}
		}
		w.splitPhase++
		w.splitAt = 0
	}
	return nil, false
}
func (w *workState) emitChunk(attempt *replayAttempt, allowance int64) (*workOutput, error) {
	if w.builderSize == 0 {
		w.builder = ledger.GraphDeltaChunk{AnalysisID: w.started.AnalysisID, AnalysisKey: w.started.AnalysisKey, ChunkIndex: int64(len(w.chunks))}
		empty, err := w.builder.Encode()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		w.builderSize = len(empty)
		allowance -= 1 + int64((len(empty)+255)/256)
	}
	for {
		value, exists := w.nextMutation()
		if exists {
			cost := elementUnits(value)
			if cost > allowance {
				return nil, nil
			}
			allowance -= cost
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
			}
			additional := len(encoded)
			switch value.(type) {
			case ledger.Digest:
				if len(w.builder.RemoveEdges) > 0 {
					additional++
				}
			case ledger.NodeKey:
				if len(w.builder.RemoveNodes) > 0 {
					additional++
				}
			case ledger.GraphNode:
				if len(w.builder.PutNodes) > 0 {
					additional++
				}
			case ledger.GraphEdge:
				if len(w.builder.PutEdges) > 0 {
					additional++
				}
			case ledger.CoverageEntry:
				if len(w.builder.Coverage) > 0 {
					additional++
				}
			}
			if w.builderSize+additional <= chunkLimit {
				switch value := value.(type) {
				case ledger.Digest:
					w.builder.RemoveEdges = append(w.builder.RemoveEdges, value)
				case ledger.NodeKey:
					w.builder.RemoveNodes = append(w.builder.RemoveNodes, value)
				case ledger.GraphNode:
					w.builder.PutNodes = append(w.builder.PutNodes, value)
				case ledger.GraphEdge:
					w.builder.PutEdges = append(w.builder.PutEdges, value)
				case ledger.CoverageEntry:
					w.builder.Coverage = append(w.builder.Coverage, value)
				}
				w.builderSize += additional
				w.builderElements++
				w.splitAt++
				continue
			}
			if w.builderElements == 0 {
				return &workOutput{reason: ledger.ReasonTemporaryCap}, nil
			}
		}
		if w.builderElements == 0 {
			w.phase = 10
			return nil, nil
		}
		// Encoding, recovery comparison and chunk hashing each have their own
		// byte charge. Even a maximum-size chunk fits one fresh quantum.
		cost := 1 + 3*int64((w.builderSize+255)/256)
		if cost > allowance {
			return nil, nil
		}
		if !w.retain(uint64(w.builderSize)*2 + 512) {
			return &workOutput{reason: ledger.ReasonTemporaryCap}, nil
		}
		encoded, err := w.builder.Encode()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if len(encoded) != w.builderSize || len(encoded) > chunkLimit {
			return nil, ErrInvalid
		}
		index := len(w.chunks)
		if index < len(attempt.chunks) && !bytes.Equal(encoded, attempt.chunks[index]) {
			return &workOutput{reason: ledger.ReasonRecoveryUnknown}, nil
		}
		w.chunks = append(w.chunks, encoded)
		_, _ = w.chunksHash.Write(encoded)
		w.builder, w.builderSize, w.builderElements = ledger.GraphDeltaChunk{}, 0, 0
		if !exists {
			w.phase = 10
		}
		if index < len(attempt.chunks) {
			return nil, nil
		}
		return &workOutput{chunk: encoded}, nil
	}
}

type canonicalElement struct {
	key, encoded []byte
	owned        bool
}
type canonicalWork struct {
	base, replacement    State
	scopes               scopeSet
	section, left, right int
	first, opened        bool
	old, next            *canonicalElement
	hash                 hash.Hash
}

func newCanonicalWork(base, replacement State, scopes scopeSet) *canonicalWork {
	return &canonicalWork{base: base, replacement: replacement, scopes: scopes, hash: sha256.New()}
}
func (c *canonicalWork) value(state State, index int) (any, bool) {
	switch c.section {
	case 0:
		if index < len(state.Scopes) {
			return state.Scopes[index], true
		}
	case 1:
		if index < len(state.Nodes) {
			return state.Nodes[index], true
		}
	case 2:
		if index < len(state.Edges) {
			return state.Edges[index], true
		}
	case 3:
		if index < len(state.Coverage) {
			return state.Coverage[index], true
		}
	}
	return nil, false
}
func (c *canonicalWork) fetch(value any) *canonicalElement {
	e := &canonicalElement{}
	e.encoded, _ = json.Marshal(value)
	switch value := value.(type) {
	case Scope:
		e.key = slices.Clone(value.Source.WorkspaceID[:])
		_, e.owned = c.scopes[value.Source.WorkspaceID]
	case ledger.GraphNode:
		e.key, e.owned = keyBytes(value.Key), c.scopes.ownsNode(value.Key)
	case ledger.GraphEdge:
		e.key, e.owned = slices.Clone(value.EdgeID[:]), c.scopes.ownsEdge(value)
	case ledger.CoverageEntry:
		e.key, e.owned = []byte(coverageKey(value)), c.scopes.ownsCoverage(value)
	}
	return e
}
func (c *canonicalWork) step(allowance int64) bool {
	for c.section < 4 && allowance > 0 {
		if !c.opened {
			prefix := []string{`{"scopes":[`, `],"nodes":[`, `],"edges":[`, `],"coverage":[`}[c.section]
			_, _ = c.hash.Write([]byte(prefix))
			allowance--
			c.opened, c.first = true, true
		}
		if c.old == nil {
			if value, exists := c.value(c.base, c.left); exists {
				cost := elementUnits(value)
				if cost > allowance {
					return false
				}
				allowance -= cost
				c.old = c.fetch(value)
				c.left++
				if c.old.owned {
					c.old = nil
					continue
				}
			}
		}
		if c.next == nil {
			if value, exists := c.value(c.replacement, c.right); exists {
				cost := elementUnits(value)
				if cost > allowance {
					return false
				}
				allowance -= cost
				c.next = c.fetch(value)
				c.right++
			}
		}
		if c.old == nil && c.next == nil {
			c.section++
			c.left, c.right, c.opened = 0, 0, false
			continue
		}
		selected, fromOld := c.next, false
		if c.next == nil || c.old != nil && bytes.Compare(c.old.key, c.next.key) < 0 {
			selected, fromOld = c.old, true
		}
		cost := 1 + int64((len(selected.encoded)+1+255)/256)
		if cost > allowance {
			return false
		}
		allowance -= cost
		if !c.first {
			_, _ = c.hash.Write([]byte(","))
		}
		_, _ = c.hash.Write(selected.encoded)
		c.first = false
		if fromOld {
			c.old = nil
		} else {
			c.next = nil
		}
	}
	if c.section == 4 && allowance > 0 {
		_, _ = c.hash.Write([]byte("]}"))
		c.section++
		return true
	}
	return c.section == 5
}

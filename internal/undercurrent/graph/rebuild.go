package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Scope membership comes from the consumed C2 prefix, never from the image
// being checked. A racing acceptance cannot enlarge an already started rebuild.
type scopeSet map[uuid.UUID]Scope

func (r *replayState) historyAt(workspace uuid.UUID, frontier ledger.RecordRef) []acceptance {
	all := r.acceptances[workspace]
	end, found := slices.BinarySearchFunc(all, frontier.Seq, func(a acceptance, seq int64) int {
		if a.ref.Seq < seq {
			return -1
		}
		if a.ref.Seq > seq {
			return 1
		}
		return 0
	})
	if found {
		end++
	}
	return all[:end]
}

func (r *replayState) scopesAt(started ledger.GraphAnalysisStarted) scopeSet {
	if started.Reason == ledger.GraphReasonIncremental {
		// Replay may encounter an already-stale incremental subject. Preserve its
		// binding so eligibility can supersede it without reserving more work.
		return scopeSet{started.Source.WorkspaceID: startedScope(started)}
	}
	scopes := make(scopeSet)
	for id, registered := range r.registrations {
		history := r.historyAt(id, started.InputFrontier)
		if len(history) == 0 {
			continue
		}
		last := history[len(history)-1]
		scopes[id] = Scope{Source: ledger.GraphSource{WorkspaceID: id, Registered: registered.ref, Subject: last.ref, Tree: last.tree}, Scope: started.Scope, AnalyzerEpoch: started.AnalyzerEpoch, ConfigurationSHA256: started.ConfigurationSHA256}
	}
	return scopes
}

func (scopes scopeSet) sorted() []Scope {
	ordered := make([]Scope, 0, len(scopes))
	for _, scope := range scopes {
		ordered = append(ordered, scope)
	}
	slices.SortFunc(ordered, func(a, b Scope) int { return bytes.Compare(a.Source.WorkspaceID[:], b.Source.WorkspaceID[:]) })
	return ordered
}

func (scopes scopeSet) ownsNode(key ledger.NodeKey) bool {
	_, exists := scopes[key.OriginID]
	return exists
}

func (scopes scopeSet) ownsEdge(edge ledger.GraphEdge) bool {
	return scopes.ownsNode(edge.From) || scopes.ownsNode(edge.To) || scopes.ownsNode(edge.Evidence.Artifact)
}

func (scopes scopeSet) ownsCoverage(entry ledger.CoverageEntry) bool {
	if text, ok := strings.CutPrefix(entry.Scope, "workspace:"); ok {
		text, ok = strings.CutSuffix(text, ":canonical")
		if !ok {
			return false
		}
		id, err := uuid.Parse(text)
		_, exists := scopes[id]
		return err == nil && id.String() == text && exists
	}
	var key ledger.NodeKey
	return json.Unmarshal([]byte(entry.Scope), &key) == nil && scopes.ownsNode(key)
}

func scopeExhaustionKey(started ledger.GraphAnalysisStarted, scope Scope) ledger.Digest {
	started.Source, started.Scope = scope.Source, scope.Scope
	return exhaustionKey(started)
}

func (r *replayState) blocked(started ledger.GraphAnalysisStarted, scopes scopeSet) bool {
	for _, scope := range scopes {
		if r.exhausted[scopeExhaustionKey(started, scope)] {
			return true
		}
	}
	return false
}

func (r *replayState) rebuildStart(configuration ledger.Digest, epoch int64, reason string) (ledger.GraphAnalysisStarted, bool) {
	if reason == "" {
		return ledger.GraphAnalysisStarted{}, false
	}
	for id := range r.registrations {
		latest, ok := r.latest(id)
		if ok && latest.ref == r.frontier {
			started := r.startFor(id, configuration, epoch)
			started.Reason = reason
			return started, true
		}
	}
	return ledger.GraphAnalysisStarted{}, false
}

func (s *Service) rebuildReason() string {
	switch {
	case s.epochDue:
		return ledger.GraphReasonAnalyzerEpoch
	case s.mismatchDue:
		return ledger.GraphReasonDigestMismatch
	case s.replay.structural >= 100:
		return ledger.GraphReasonStructural100
	case s.loadDue:
		return ledger.GraphReasonLoad
	default:
		return ""
	}
}

// The installed identity was verified during replay or reserved work. Checking
// it here is constant-time; selection never mints an uncharged hash pass.
func (s *Service) checkProjection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image != nil && s.image.available && (s.image.status.Version != s.replay.current.version || s.image.status.Digest != s.replay.current.digest) {
		s.mismatchDue = true
	}
}

type corpusObject struct {
	body   []byte
	digest ledger.Digest
}

func (w *workState) source() ledger.GraphSource {
	return w.orderedScopes[w.scopeIndex].Source
}

func (w *workState) beginScope(service *Service) {
	source := w.source()
	cached := generationState{}
	if w.started.Reason == ledger.GraphReasonIncremental {
		// This cache was independently walked, never extracted from graph nodes.
		cached = service.generations[source.WorkspaceID]
		delete(service.generations, source.WorkspaceID)
		service.generationMemory -= cached.memory
	}
	w.generation = newGenerationWalk(source.WorkspaceID, source.Registered, service.replay.historyAt(source.WorkspaceID, w.started.InputFrontier), cached)
	w.memory -= w.corpusMemory + w.catalogMemory
	w.corpusMemory, w.catalogMemory = 0, 0
	w.corpus, w.catalog, w.fetch = nil, nil, nil
	w.languages, w.languageBytes = [3][]CorpusArtifact{}, [3]uint64{}
	w.requested, w.failures = make(map[string]bool), make(map[string]string)
	w.after, w.stalls, w.opaqueAt, w.catalogAt, w.fetchAt, w.phase = "", 0, 0, 0, 0, 0
	for i := range service.tools {
		service.tools[i].failed = false
	}
}

// First list the immutable subject, then read one representative path for each
// uncached OID. Cached bytes are reused, not path-sensitive analyzer results.
// Missing-object failures are local to this repository, never memoized globally.
func (w *workState) listCorpus(ctx context.Context, service *Service, allowance int64) (int64, string, error) {
	source := w.source()
	request := ArtifactRequest{WorkspaceID: source.WorkspaceID, Tree: source.Tree, AfterPath: w.after, MaxBytes: maxFrame, WorkAllowance: uint64(allowance), ListOnly: true}
	batch, reason, err := readArtifactPage(ctx, service.opts.ReadArtifacts, request)
	used := int64(batch.WorkUsed)
	if err != nil || reason != "" {
		return used, reason, err
	}
	if allowance == quantum && batch.NextPath == w.after && !batch.Complete {
		w.stalls++
	} else {
		w.stalls = 0
	}
	if w.stalls >= 2 {
		return used, ledger.ReasonHelperLimit, nil
	}
	for _, entry := range batch.Entries {
		memory := uint64(1024 + 2*(len(entry.Path)+len(entry.GitOID)))
		if !w.retain(memory) {
			return used, ledger.ReasonTemporaryCap, nil
		}
		w.catalogMemory += memory
		w.catalog = append(w.catalog, entry)
		_, cached := w.objects[entry.GitOID]
		if entry.Reason == "" && !cached && !w.requested[entry.GitOID] {
			w.fetch = append(w.fetch, entry)
			w.requested[entry.GitOID] = true
		}
	}
	w.after = batch.NextPath
	if batch.Complete {
		w.phase, w.stalls = 13, 0
	}
	return used, "", nil
}

func (w *workState) readCorpus(ctx context.Context, service *Service, allowance int64) (int64, string, error) {
	if w.fetchAt == len(w.fetch) {
		w.phase = 14
		return 0, "", nil
	}
	source := w.source()
	pending := w.fetch[w.fetchAt:min(w.fetchAt+maxItems, len(w.fetch))]
	paths := make([]string, len(pending))
	for i, entry := range pending {
		paths[i] = entry.Path
	}
	request := ArtifactRequest{WorkspaceID: source.WorkspaceID, Tree: source.Tree, Paths: paths, MaxBytes: maxFrame, WorkAllowance: uint64(allowance)}
	batch, reason, err := readArtifactPage(ctx, service.opts.ReadArtifacts, request)
	used := int64(batch.WorkUsed)
	if err != nil || reason != "" {
		return used, reason, err
	}
	if allowance == quantum && batch.NextPath == "" && !batch.Complete {
		w.stalls++
	} else {
		w.stalls = 0
	}
	if w.stalls >= 2 {
		return used, ledger.ReasonHelperLimit, nil
	}
	index := 0
	for _, expected := range pending {
		if expected.Path > batch.NextPath {
			break
		}
		if index == len(batch.Entries) || batch.Entries[index].Path != expected.Path {
			w.failures[expected.GitOID] = ledger.ReasonSourceUnavailable
		} else {
			entry := batch.Entries[index]
			index++
			if entry.GitOID != expected.GitOID || entry.Mode != expected.Mode {
				return used, ledger.ReasonSourceUnavailable, nil
			}
			if entry.Reason != "" {
				w.failures[entry.GitOID] = entry.Reason
			} else {
				memory := uint64(512 + 2*len(entry.GitOID) + 2*len(entry.Bytes))
				if !w.retain(memory) {
					return used, ledger.ReasonTemporaryCap, nil
				}
				body := make([]byte, len(entry.Bytes))
				copy(body, entry.Bytes)
				w.objects[entry.GitOID] = corpusObject{body: body, digest: entry.SHA256}
			}
		}
		w.fetchAt++
	}
	if w.fetchAt == len(w.fetch) {
		w.phase = 14
	}
	return used, "", nil
}

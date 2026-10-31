package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func readObject(path privateScope, h Head) (data []byte, err error) {
	if h.Bytes < 0 || h.Bytes > maxObjectBytes {
		return nil, ErrIntegrity
	}
	if err := path.dirs.checkDirectories(append(path.components(), "core", "objects"), false); err != nil {
		return nil, errors.Join(ErrIntegrity, err)
	}
	name := filepath.Join(path.objects(), h.SHA256.String())
	info, err := regularEntry(name)
	if err != nil {
		return nil, errors.Join(ErrIntegrity, err)
	}
	if info.Size() != h.Bytes {
		return nil, ErrIntegrity
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, errors.Join(ErrIntegrity, err)
	}
	// A selected object reserves exactly its declared bytes plus this one
	// growth-detection byte, rather than ReadAll's growing backing arrays.
	buffer := make([]byte, h.Bytes+1)
	n, readErr := io.ReadFull(f, buffer)
	err = f.Close()
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		err = errors.Join(readErr, err)
	}
	data = buffer[:n]
	if err != nil {
		return nil, errors.Join(ErrIntegrity, err)
	}
	if int64(len(data)) != h.Bytes || ledger.Digest(sha256.Sum256(data)) != h.SHA256 {
		return nil, ErrIntegrity
	}
	return data, nil
}
func refuseInitialMarkdown(path privateScope) error {
	slots := []string{"handoff", "memory"}
	if path.context == "" {
		slots = []string{"instructions", "memory"}
	}
	for _, slot := range slots {
		name, _ := path.markdownPath(slot)
		_, err := os.Lstat(name)
		if err == nil {
			return ErrIntegrity
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.Join(ErrIntegrity, err)
		}
	}
	return nil
}
func (s *Service) scopeHistory(ctx context.Context, ids submissionIDs, scope string, through, revision uint64, visit func(string, Head, string) error) (uint64, error) {
	var current uint64
	err := s.scan(ctx, 0, through, func(c ledger.Commit) error {
		g, scoped, _, _, _, loop, err := decodeFamily(draftsOf(c), c.First)
		if err != nil {
			return err
		}
		origin, revisions, indexes := scopedRevisions(g, scoped, loop)
		if origin.agent != ids.agent || origin.session != ids.session || scope == "context" && origin.context != ids.context {
			return nil
		}
		for i, rev := range revisions {
			if rev.Scope != scope || uint64(rev.Revision) > revision {
				continue
			}
			current = uint64(rev.Revision)
			for _, change := range rev.Changes {
				if err := visit(change.Slot, Head{change.SHA256, change.Bytes, current, c.First + uint64(indexes[i])}, change.Mode); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return current, err
}

// Unknown regular bytes need only one same-slot membership answer, not the
// lifetime collection of all digests. A miss remains foreign.
func (s *Service) historicalMaterialization(ctx context.Context, ids submissionIDs, scope, slot string, through, revision uint64, digest ledger.Digest) (bool, error) {
	found := false
	_, err := s.scopeHistory(ctx, ids, scope, through, revision, func(changed string, h Head, mode string) error {
		if changed == slot && mode != "clear" && h.SHA256 == digest {
			found = true
		}
		return nil
	})
	return found, err
}
func (s *Service) objectIssue(path privateScope, b ReadBinding, slot string, h Head) IntegrityIssue {
	issue := IntegrityIssue{ReadBinding: b, Slot: slot, Revision: h.Revision, ExpectedSHA256: h.SHA256, Reason: "object_missing_or_invalid"}
	if err := path.dirs.checkDirectories(append(path.components(), "core", "objects"), false); err != nil {
		return issue
	}
	name := filepath.Join(path.objects(), h.SHA256.String())
	if _, err := regularEntry(name); err == nil {
		observed, _, err := hashPrivateFile(name)
		if err == nil {
			issue.ObservedSHA256 = observed
			issue.Reason = "object_mismatch"
		}
	}
	return issue
}
func (s *Service) inspectOpen(ctx context.Context) (Reconciliation, error) {
	var report Reconciliation
	// Stable order makes a report useful without granting map order authority.
	keys := make([]scopeKey, 0, len(s.p.scopes))
	for k := range s.p.scopes {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b scopeKey) int {
		if c := bytes.Compare(a.agent[:], b.agent[:]); c != 0 {
			return c
		}
		return bytes.Compare(a.context[:], b.context[:])
	})
	type inspection struct {
		path     privateScope
		observed map[string]materializationObservation
		issues   []IntegrityIssue
	}
	scopes := make(map[scopeKey]*inspection, len(keys))
	for _, k := range keys {
		st := s.p.scopes[k]
		scope := "context"
		if k.context == ([16]byte{}) {
			scope = "agent"
		}
		path, err := s.scopePath(st.ids, scope)
		if err != nil {
			return report, err
		}
		check := &inspection{path: path, observed: map[string]materializationObservation{}}
		for slot, head := range st.heads {
			if markdownSlot(slot) {
				// Completion observes again; errors and foreign bytes are reported
				// there, never used as permission to overwrite.
				check.observed[slot], _ = path.classifyMaterialization(slot, head.SHA256, nil)
			}
		}
		scopes[k] = check
	}
	// One global traversal preserves every occurrence in all revision families.
	// Reports are collected per scope to retain sorted-scope/occurrence order.
	err := s.scan(ctx, 0, s.p.through, func(c ledger.Commit) error {
		g, scoped, _, _, _, loop, err := decodeFamily(draftsOf(c), c.First)
		if err != nil {
			return err
		}
		ids, revisions, indexes := scopedRevisions(g, scoped, loop)
		for i, rev := range revisions {
			k := keyFor(ids, rev.Scope)
			st, check := s.p.scopes[k], scopes[k]
			if st == nil || check == nil || st.ids.session != ids.session || uint64(rev.Revision) > st.revision {
				continue
			}
			b := ReadBinding{Binding{st.ids.agent, st.ids.context}, rev.Scope}
			for _, change := range rev.Changes {
				if err := ctx.Err(); err != nil {
					return err
				}
				if change.Mode == "clear" {
					continue
				}
				h := Head{change.SHA256, change.Bytes, uint64(rev.Revision), c.First + uint64(indexes[i])}
				if observed := check.observed[change.Slot]; observed.kind == observationRegular && observed.digest == h.SHA256 {
					observed.state = materializationStale
					check.observed[change.Slot] = observed
				}
				if _, err := readObject(check.path, h); err != nil {
					issue := s.objectIssue(check.path, b, change.Slot, h)
					check.issues = append(check.issues, issue)
					if h == st.heads[change.Slot] || change.Slot == "transcript" {
						st.issues = append(st.issues, issue)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	for _, k := range keys {
		st, check := s.p.scopes[k], scopes[k]
		report.Issues = append(report.Issues, check.issues...)
		if len(st.issues) != 0 {
			continue
		}
		scope := "context"
		if k.context == ([16]byte{}) {
			scope = "agent"
		}
		if err := s.inspectScope(ctx, st.ids, scope, true, true, &report, check.observed); err != nil {
			if !errors.Is(err, ErrIntegrity) && !errors.Is(err, ErrMaterialization) {
				return report, err
			}
		}
	}
	return report, nil
}
func (s *Service) inspectLive(ctx context.Context, ids submissionIDs, scope string) error {
	return s.inspectScope(ctx, ids, scope, false, false, nil, nil)
}
func (s *Service) completeScope(ctx context.Context, ids submissionIDs, scope string, opening bool, report *Reconciliation) error {
	return s.inspectScope(ctx, ids, scope, true, opening, report, nil)
}
func (s *Service) inspectScope(ctx context.Context, ids submissionIDs, scope string, complete, opening bool, report *Reconciliation, observed map[string]materializationObservation) error {
	path, err := s.scopePath(ids, scope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	st := s.p.scopes[keyFor(ids, scope)]
	through := s.p.through
	var heads map[string]Head
	var hints map[string]materializationHint
	var revision uint64
	if st != nil {
		heads, hints, revision = maps.Clone(st.heads), maps.Clone(st.materialized), st.revision
	}
	s.mu.Unlock()
	if st == nil {
		return ErrNotBound
	}
	if hints == nil {
		hints = map[string]materializationHint{}
	}
	observed = maps.Clone(observed)
	if observed == nil {
		observed = map[string]materializationObservation{}
	}
	var historyErr error
	// Scope gates serialize completion; copy its mutable proof fields under mu.
	// Neither a valid hint nor the startup pass remembers health.
	classify := func(slot string, h Head) (materializationObservation, error) {
		var earlier []ledger.Digest
		if hint := hints[slot]; hint.valid(ids, scope, slot, revision, through) {
			earlier = append(earlier, hint.head.SHA256)
		} else {
			delete(hints, slot)
		}
		observation, err := path.classifyMaterialization(slot, h.SHA256, earlier)
		if err == nil && observation.state == materializationForeign && observation.kind == observationRegular {
			prior, known := observed[slot]
			if known && prior.kind == observationRegular && prior.digest == observation.digest {
				if prior.state != materializationForeign {
					observation.state = materializationStale
				}
			} else {
				var member bool
				member, historyErr = s.historicalMaterialization(ctx, ids, scope, slot, through, revision, observation.digest)
				if member {
					observation.state = materializationStale
				}
			}
		}
		if err == nil && historyErr == nil {
			observed[slot] = observation
		}
		return observation, err
	}
	b := ReadBinding{Binding{ids.agent, ids.context}, scope}
	states := map[string]string{}
	var issues []IntegrityIssue
	var materialErr error
	slots := make([]string, 0, len(heads))
	for slot := range heads {
		slots = append(slots, slot)
	}
	slices.Sort(slots)
	for _, slot := range slots {
		if err := ctx.Err(); err != nil {
			return err
		}
		h := heads[slot]
		_, err := readObject(path, h)
		if err != nil {
			issues = append(issues, s.objectIssue(path, b, slot, h))
			continue
		}
		if !markdownSlot(slot) {
			continue
		}
		observation, err := classify(slot, h)
		if historyErr != nil {
			return historyErr
		}
		states[slot] = string(observation.state)
		if err != nil || observation.state == materializationForeign {
			issues = append(issues, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "foreign_materialization"})
			continue
		}
		if observation.state == materializationStale && complete {
			if s.deps.beforeMaterialize != nil {
				err = s.deps.beforeMaterialize(b.Binding, scope, slot)
			}
			if err == nil {
				// Hooks and external edits may change the file after inspection.
				// Reobserve before replacement, falling back only for new bytes.
				observation, err = classify(slot, h)
				if historyErr != nil {
					return historyErr
				}
				if err != nil || observation.state == materializationForeign {
					issues = append(issues, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "foreign_materialization"})
					states[slot] = string(materializationForeign)
					continue
				}
				data, readErr := readObject(path, h)
				if readErr != nil {
					issues = append(issues, s.objectIssue(path, b, slot, h))
					continue
				}
				err = path.materialize(slot, data)
			}
			if err != nil {
				materialErr = errors.Join(materialErr, ErrMaterialization, err)
				if opening && report != nil {
					report.Issues = append(report.Issues, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "stale_completion_failed"})
				}
				continue
			}
			hints[slot] = materializationHint{head: h, sum: materializationChecksum(ids, scope, slot, h)}
			states[slot] = string(materializationCurrent)
			if opening && report != nil {
				report.Completed = append(report.Completed, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "stale_completed"})
			}
		}
	}
	if complete && s.deps.beforeProjection != nil {
		s.deps.beforeProjection(b.Binding, scope)
	}
	s.mu.Lock()
	st.materials = states
	st.materialized = hints
	if len(issues) > 0 {
		st.issues = append(st.issues, issues...)
	}
	s.mu.Unlock()
	if report != nil {
		report.Issues = append(report.Issues, issues...)
	}
	if len(issues) > 0 {
		return issues[0]
	}
	return materialErr
}

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
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
	data, err = io.ReadAll(io.LimitReader(f, maxObjectBytes+1))
	err = errors.Join(err, f.Close())
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
		g, scoped, err := decodeFamily(draftsOf(c), c.First)
		if err != nil {
			return err
		}
		origin, revisions, indexes := scopedRevisions(g, scoped)
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
func (s *Service) historyDigests(ctx context.Context, ids submissionIDs, scope string, through, revision uint64) (map[string][]ledger.Digest, error) {
	history := map[string][]ledger.Digest{}
	_, err := s.scopeHistory(ctx, ids, scope, through, revision, func(slot string, h Head, mode string) error {
		if markdownSlot(slot) && mode != "clear" && !slices.Contains(history[slot], h.SHA256) {
			history[slot] = append(history[slot], h.SHA256)
		}
		return nil
	})
	return history, err
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
		b := ReadBinding{Binding{st.ids.agent, st.ids.context}, scope}
		_, err = s.scopeHistory(ctx, st.ids, scope, s.p.through, st.revision, func(slot string, h Head, mode string) error {
			if mode == "clear" {
				return nil
			}
			if _, err := readObject(path, h); err != nil {
				issue := s.objectIssue(path, b, slot, h)
				report.Issues = append(report.Issues, issue)
				if h == st.heads[slot] || slot == "transcript" {
					st.issues = append(st.issues, issue)
				}
			}
			return nil
		})
		if err != nil {
			return report, err
		}
		if len(st.issues) == 0 {
			if err := s.completeScope(ctx, st.ids, scope, true, &report); err != nil {
				if !errors.Is(err, ErrIntegrity) && !errors.Is(err, ErrMaterialization) {
					return report, err
				}
			}
		}
	}
	return report, nil
}
func (s *Service) inspectLive(ctx context.Context, ids submissionIDs, scope string) error {
	return s.inspectScope(ctx, ids, scope, false, false, nil)
}
func (s *Service) completeScope(ctx context.Context, ids submissionIDs, scope string, opening bool, report *Reconciliation) error {
	return s.inspectScope(ctx, ids, scope, true, opening, report)
}
func (s *Service) inspectScope(ctx context.Context, ids submissionIDs, scope string, complete, opening bool, report *Reconciliation) error {
	path, err := s.scopePath(ids, scope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	st := s.p.scopes[keyFor(ids, scope)]
	through := s.p.through
	s.mu.Unlock()
	if st == nil {
		return ErrNotBound
	}
	history, err := s.historyDigests(ctx, ids, scope, through, st.revision)
	if err != nil {
		return err
	}
	b := ReadBinding{Binding{ids.agent, ids.context}, scope}
	states := map[string]string{}
	var issues []IntegrityIssue
	var materialErr error
	slots := make([]string, 0, len(st.heads))
	for slot := range st.heads {
		slots = append(slots, slot)
	}
	slices.Sort(slots)
	for _, slot := range slots {
		if err := ctx.Err(); err != nil {
			return err
		}
		h := st.heads[slot]
		data, err := readObject(path, h)
		if err != nil {
			issues = append(issues, s.objectIssue(path, b, slot, h))
			continue
		}
		if !markdownSlot(slot) {
			continue
		}
		observation, err := path.classifyMaterialization(slot, h.SHA256, history[slot])
		states[slot] = string(observation.state)
		if err != nil || observation.state == materializationForeign {
			issues = append(issues, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: st.revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "foreign_materialization"})
			continue
		}
		if observation.state == materializationStale && complete {
			if s.deps.beforeMaterialize != nil {
				err = s.deps.beforeMaterialize(b.Binding, scope, slot)
			}
			if err == nil {
				err = path.materialize(slot, data)
			}
			if err != nil {
				materialErr = errors.Join(materialErr, ErrMaterialization, err)
				if opening && report != nil {
					report.Issues = append(report.Issues, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: st.revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "stale_completion_failed"})
				}
				continue
			}
			states[slot] = string(materializationCurrent)
			if opening && report != nil {
				report.Completed = append(report.Completed, IntegrityIssue{ReadBinding: b, Slot: slot, Revision: st.revision, ExpectedSHA256: h.SHA256, ObservedSHA256: observation.digest, Reason: "stale_completed"})
			}
		}
	}
	if complete && s.deps.beforeProjection != nil {
		s.deps.beforeProjection(b.Binding, scope)
	}
	s.mu.Lock()
	st.materials = states
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

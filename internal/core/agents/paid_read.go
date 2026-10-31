package agents

import (
	"context"
	"encoding/json"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type PaidObjectLookup struct {
	AttemptID, GrantID uuid.UUID
	Object             ledger.ObjectRef
	MaxBytes           int64
}

// ReadPaidObject is an owner-only passive read of an attempt's selected object.
func (s *Service) ReadPaidObject(ctx context.Context, request PaidObjectLookup) ([]byte, error) {
	return s.readPaidObject(ctx, nil, request)
}

// ReadPaidObject requires current private.read over every originating context
// slot. A slot-limited grant cannot read a whole exchange assembled from them.
func (a *Agent) ReadPaidObject(ctx context.Context, request PaidObjectLookup) ([]byte, error) {
	if a == nil || a.service == nil {
		return nil, ErrNotBound
	}
	return a.service.readPaidObject(ctx, &a.ids, request)
}

func (s *Service) paidObjectSelected(ctx context.Context, a paidAttempt, ref ledger.ObjectRef) (bool, error) {
	if a.admission.InputObject == ref {
		return true, nil
	}
	selected := func(v *ledger.RuntimePaidResult) bool {
		return v != nil && (v.EvidenceObject != nil && *v.EvidenceObject == ref || slices.Contains(v.MessageObjects, ref))
	}
	if selected(a.result) {
		return true, nil
	}
	// Prior evidence remains selectable after a correction. This is an explicit
	// historical query, not a scan in dispatch or an ordinary settlement.
	found := false
	err := s.scan(ctx, a.sequence-2, s.p.through, func(c ledger.Commit) error {
		_, _, _, g, _, _, err := decodeFamily(draftsOf(c), c.First)
		if err == nil && g != nil && g.receipt.AttemptID == a.admission.AttemptID && selected(g.result) {
			found = true
		}
		return err
	})
	return found, err
}

func (s *Service) readPaidObject(ctx context.Context, bound *submissionIDs, request PaidObjectLookup) ([]byte, error) {
	if s == nil {
		return nil, ErrNotBound
	}
	if !validID(request.AttemptID) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return nil, ErrInvalid
	}
	if request.MaxBytes < 0 || request.MaxBytes > maxObjectBytes || request.Object.Bytes > request.MaxBytes {
		return nil, ErrTooLarge
	}
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.active.Done()
	var a paidAttempt
	var chain []grantRules
	var replayEvidence ledger.ObjectRef
	var replayRequired bool
	var selectedThrough uint64
	var evidenceBytes int64
	check := func() error {
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
			return err
		}
		var found bool
		var err error
		a, found, err = s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
		if err != nil {
			return err
		}
		if !found || bound != nil && a.origin != originOf(*bound) {
			return ErrDenied
		}
		if bound != nil {
			target := ledger.Resource{Kind: "context", Binding: ledger.Binding(bindingOf(a.origin)), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}
			chain, err = s.authorize(ctx, a.origin, request.GrantID, "private.read", []ledger.Resource{target}, nil, request.Object.Bytes, s.p.through)
			if err != nil {
				return reviewDenied(err)
			}
		}
		found, err = s.paidObjectSelected(ctx, a, request.Object)
		if err == nil && !found {
			replayRequired = true
			selectedThrough = s.p.through
			if replayEvidence != (ledger.ObjectRef{}) {
				selected, err := s.paidObjectSelected(ctx, a, replayEvidence)
				if err != nil {
					return err
				}
				if !selected {
					return ErrDenied
				}
			}
			if replayEvidence == (ledger.ObjectRef{}) {
				err = s.scan(ctx, a.sequence-2, selectedThrough, func(c ledger.Commit) error {
					_, _, _, group, _, _, err := decodeFamily(draftsOf(c), c.First)
					if err == nil && group != nil && group.result != nil && group.receipt.AttemptID == a.admission.AttemptID && group.result.EvidenceObject != nil {
						evidenceBytes = max(evidenceBytes, group.result.EvidenceObject.Bytes)
					}
					return err
				})
				if err != nil {
					return err
				}
				if evidenceBytes <= 0 || evidenceBytes > maxObjectBytes {
					return ErrDenied
				}
			}
		}
		return err
	}
	s.mu.Lock()
	err := check()
	reservation := scopedReservation{origin: a.origin}
	if err == nil {
		err = s.reserveScopedProcessLocked(&reservation)
	}
	if err == nil && bound != nil {
		reservation.scopes, err = chainScopes(a.origin, chain)
		if err == nil {
			size := request.Object.Bytes
			extra := int64(0)
			if replayRequired {
				extra = evidenceBytes + 1
			}
			preparation := budgetPreparation{hold: [4]int64{0, 0, size + 1 + extra, 0}, operationBytes: &size}
			err = s.p.book.reservePreparation(a.origin, reservation.scopes, preparation)
			if err == nil {
				reservation.budget = preparation
			}
		}
	}
	if err != nil {
		s.releaseScopedLocked(&reservation)
		s.mu.Unlock()
		return nil, err
	}
	directory := s.p.agents[a.origin.AgentID].directory
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.releaseScopedLocked(&reservation); s.mu.Unlock() }()
	if s.deps.beforeRead != nil {
		if err := s.deps.beforeRead(bindingOf(a.origin), "paid_object"); err != nil {
			return nil, err
		}
	}
	scope, err := newPrivateScope(s.colony, directory, a.origin.ContextID.String(), &s.dirs)
	var body []byte
	if err == nil {
		if replayRequired {
			// A correction does not erase earlier selected observation objects.
			// Scan metadata first; read only bounded evidence, one at a time.
			err = s.scan(ctx, a.sequence-2, selectedThrough, func(c ledger.Commit) error {
				if replayEvidence != (ledger.ObjectRef{}) {
					return nil
				}
				_, _, _, group, _, _, err := decodeFamily(draftsOf(c), c.First)
				if err != nil {
					return err
				}
				if group == nil || group.result == nil || group.receipt.AttemptID != a.admission.AttemptID || group.result.EvidenceObject == nil {
					return nil
				}
				evidence := *group.result.EvidenceObject
				if evidence.Bytes > 0 && evidence.Bytes <= maxObjectBytes && verifyPaidReplayObject(scope, a, evidence, request.Object) == nil {
					replayEvidence = evidence
				}
				return nil
			})
			if err == nil && replayEvidence == (ledger.ObjectRef{}) {
				err = ErrDenied
			}
		}
		if err == nil {
			body, err = readObject(scope, Head{SHA256: request.Object.SHA256, Bytes: request.Object.Bytes})
		}
	}
	if s.deps.afterRead != nil {
		s.deps.afterRead(bindingOf(a.origin), "paid_object")
	}
	s.mu.Lock()
	authorityErr := check()
	s.mu.Unlock()
	if authorityErr != nil {
		return nil, authorityErr
	}
	if err != nil {
		return nil, ErrDenied
	}
	return body, nil
}

func verifyPaidReplayObject(scope privateScope, a paidAttempt, evidence, replay ledger.ObjectRef) error {
	raw, err := readObject(scope, Head{SHA256: evidence.SHA256, Bytes: evidence.Bytes})
	if err != nil {
		return ErrDenied
	}
	var o paidObservationObject
	if json.Unmarshal(raw, &o) != nil || o.Schema != 1 || o.Kind != "paid_observation" || o.AttemptID != a.admission.AttemptID || o.Origin != a.origin || o.ParentTurnID != a.admission.ParentTurnID || o.RequestSHA256 != a.admission.Quote.RequestSHA256 || o.Model != a.profile.ModelID || !o.Complete || o.ContentError || o.ReplayObject == nil || *o.ReplayObject != replay {
		return ErrDenied
	}
	return nil
}

package workspace

import (
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ValidationSelector names durable workspace provenance, never a raw tree or
// scratch path. ContextID is the authenticated source context supplied by Core;
// it is not a grant. GenerationID must name the request's current clean result.
type ValidationSelector struct {
	Kind                                                           string
	WorkspaceID, ContextID, ObservationID, RequestID, GenerationID uuid.UUID
}

// ValidationSubject is a detached, opaque source resolved by one Service.
// Its descriptor must still compare equal at dispatch and terminal guards.
type ValidationSubject struct {
	service    *Service
	selector   ValidationSelector
	descriptor ledger.ValidationSubject
	inputs     Proposal
}

func (v ValidationSubject) Descriptor() ledger.ValidationSubject { return v.descriptor }

func (s *Service) validationCurrent(ctx context.Context, v ValidationSubject) error {
	if v.service != s {
		return ErrInvalid
	}
	current, err := s.ResolveValidationSubject(ctx, v.selector)
	if err != nil {
		return err
	}
	if current.descriptor != v.descriptor {
		return ErrRevision
	}
	return nil
}

// ResolveValidationSubject reads acknowledged F2 state only. It never captures,
// admits, integrates or finalizes work. A newer scratch edit is not a new source.
func (s *Service) ResolveValidationSubject(ctx context.Context, selector ValidationSelector) (ValidationSubject, error) {
	var result ValidationSubject
	if !validID(selector.WorkspaceID) {
		return result, ErrInvalid
	}
	if err := s.start(); err != nil {
		return result, err
	}
	defer s.ops.Done()
	unlock, err := s.lockTrain(ctx, selector.WorkspaceID)
	if err != nil {
		return result, err
	}
	defer unlock()
	s.mu.Lock()
	w := s.workspaces[selector.WorkspaceID]
	w.revisions = cloneRevisions(w.revisions)
	witness := s.witnesses[selector.WorkspaceID]
	observation, observed := s.observations[selector.ObservationID]
	observationScratch := s.scratches[observation.record.ScratchID]
	request, requested := s.requests[selector.RequestID]
	requestScratch := s.scratches[request.admission.ScratchID]
	train := slices.Clone(s.trains[selector.WorkspaceID])
	requests := make([]requestState, 0, len(train))
	for _, id := range train {
		requests = append(requests, s.requests[id])
	}
	_, intent := s.intents[selector.WorkspaceID]
	pending := s.recoveryPending[selector.WorkspaceID]
	s.mu.Unlock()
	if pending || intent {
		return result, &UnavailableError{Reason: "subject_unavailable"}
	}
	if err := s.integrationReady(ctx, w); err != nil {
		return result, err
	}
	d := ledger.ValidationSubject{Kind: selector.Kind, WorkspaceID: w.ID, ObjectFormat: w.ObjectFormat, CaptureInputsRevision: w.Revision, CaptureInputsSHA256: w.InputsSHA256}
	var sourceSeq uint64
	var generationID uuid.UUID
	var sourceRef string
	switch selector.Kind {
	case "canonical":
		if selector.ObservationID != (uuid.UUID{}) || selector.RequestID != (uuid.UUID{}) || selector.GenerationID != (uuid.UUID{}) {
			return result, ErrInvalid
		}
		commit, tree, err := s.mainTip(ctx, w)
		if err != nil {
			return result, err
		}
		if witness.sequence != 0 && (commit != witness.record.AcceptedCommit || tree != witness.record.AcceptedTree) {
			return result, ErrRevision
		}
		d.Commit, d.Tree = commit, tree
		sourceSeq = witness.sequence
	case "observation":
		if !observed || !validID(selector.ContextID) || selector.RequestID != (uuid.UUID{}) || selector.GenerationID != (uuid.UUID{}) {
			return result, ErrInvalid
		}
		o := observation.record
		if o.WorkspaceID != w.ID || o.ContextID != selector.ContextID {
			return result, ErrNotBound
		}
		if observationScratch.closed || observationScratch.ID != o.ScratchID {
			return result, ErrRevision
		}
		sourceRef = "refs/dwarf-harness/scratch-commits/" + o.ObservationID.String()
		d.Commit, d.Tree, d.SourceManifestSHA256 = o.ScratchCommit, o.ScratchTree, o.ManifestSHA256
		d.CaptureInputsRevision, d.CaptureInputsSHA256 = o.InputsRevision, o.CaptureInputsSHA256
		sourceSeq = observation.sequence
	case "generation":
		if !requested || !validID(selector.ContextID) || selector.ObservationID != (uuid.UUID{}) {
			return result, ErrInvalid
		}
		a, g := request.admission, request.generation
		if a.WorkspaceID != w.ID || a.ContextID != selector.ContextID {
			return result, ErrNotBound
		}
		if requestScratch.closed || requestScratch.ID != a.ScratchID || request.ending != "" || g == nil || g.Outcome != ledger.GenerationClean || g.GenerationID != selector.GenerationID {
			return result, ErrRevision
		}
		commit, tree, err := s.mainTip(ctx, w)
		if err != nil {
			return result, err
		}
		if witness.sequence != 0 && (commit != witness.record.AcceptedCommit || tree != witness.record.AcceptedTree) {
			return result, ErrRevision
		}
		found := false
		predecessorRequest, predecessorGeneration := witness.record.RequestID, witness.record.GenerationID
		for i, prior := range requests {
			pg := prior.generation
			if s.pool.profile == nil || i >= s.pool.profile.GenerationWindow || prior.ending != "" || pg == nil || pg.Outcome != ledger.GenerationClean || pg.PredecessorCommit != commit || pg.PredecessorTree != tree {
				return result, ErrRevision
			}
			if pg.PredecessorRequestID != (uuid.UUID{}) {
				if pg.PredecessorRequestID != predecessorRequest || pg.PredecessorGenerationID != predecessorGeneration {
					return result, ErrRevision
				}
			} else if pg.PredecessorWitnessSeq != int64(witness.sequence) || i != 0 {
				return result, ErrRevision
			}
			predecessorRequest, predecessorGeneration = prior.admission.RequestID, pg.GenerationID
			commit, tree = pg.DerivedCommit, pg.DerivedTree
			if prior.admission.RequestID == a.RequestID {
				found = true
				break
			}
		}
		if !found {
			return result, ErrRevision
		}
		d.Commit, d.Tree = g.DerivedCommit, g.DerivedTree
		d.CaptureInputsRevision = g.InputsRevision
		input, ok := w.revisions[g.InputsRevision]
		if !ok {
			return result, ErrRevision
		}
		d.CaptureInputsSHA256 = input.digest
		d.SourceManifestSHA256, d.RequestID, d.CaptureID = a.ManifestSHA256, a.RequestID, a.CaptureID
		d.SubmissionTree, d.SubmissionBase = a.SealedTree, a.DeltaBaseTree
		d.GenerationID, d.PredecessorRequestID, d.PredecessorGenerationID = g.GenerationID, g.PredecessorRequestID, g.PredecessorGenerationID
		d.PredecessorWitnessSeq, d.PredecessorCommit, d.PredecessorTree, d.IntegrationRules = g.PredecessorWitnessSeq, g.PredecessorCommit, g.PredecessorTree, g.IntegrationRules
		generationID = g.GenerationID
		sourceRef = "refs/dwarf-harness/generations/" + g.GenerationID.String()
	default:
		return result, ErrInvalid
	}
	reader, err := ledger.OpenReader(s.store.LogDir(), ledger.ReadOptions{Colony: s.store.Colony(), Through: s.store.Durable(), Consume: workspaceConsumption(), AllTypes: true})
	if err != nil {
		return result, err
	}
	defer reader.Close()
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, err
		}
		for _, record := range commit.Records {
			match := record.Seq == sourceSeq && sourceSeq != 0
			if selector.Kind == "canonical" && sourceSeq == 0 && record.Type == ledger.TypeWorkspaceRegistered {
				registered, err := ledger.DecodeWorkspaceRegistered(record.Payload)
				if err != nil {
					return result, err
				}
				match = registered.WorkspaceID == w.ID
			}
			if selector.Kind == "generation" && record.Type == ledger.TypePromotionAdmitted {
				admitted, err := ledger.DecodePromotionAdmitted(record.Payload)
				if err != nil {
					return result, err
				}
				match = admitted.RequestID == selector.RequestID
			}
			if match {
				if record.Seq > math.MaxInt64 {
					return result, ErrInvalid
				}
				d.SourceSequence = int64(record.Seq)
				d.SourceRecordHash, err = ledger.ParseDigest(record.Hash)
				if err != nil {
					return result, err
				}
			}
			if generationID != (uuid.UUID{}) && record.Type == ledger.TypeGenerationDerived {
				g, err := ledger.DecodeGenerationDerived(record.Payload)
				if err != nil {
					return result, err
				}
				if g.GenerationID == generationID {
					if record.Seq > math.MaxInt64 {
						return result, ErrInvalid
					}
					d.GenerationSequence = int64(record.Seq)
					d.GenerationHash, err = ledger.ParseDigest(record.Hash)
					if err != nil {
						return result, err
					}
				}
			}
		}
	}
	if d.SourceSequence == 0 || generationID != (uuid.UUID{}) && d.GenerationSequence == 0 {
		return result, ErrInvalid
	}
	revision, ok := w.revisions[d.CaptureInputsRevision]
	if !ok || revision.digest != d.CaptureInputsSHA256 {
		return result, ErrRevision
	}
	w.Revision, w.InputsSHA256, w.refs = d.CaptureInputsRevision, d.CaptureInputsSHA256, revision.refs
	w, err = s.loadInputs(ctx, w)
	if err != nil {
		return result, err
	}
	if w.inputsError != "" {
		return result, &UnavailableError{Reason: "subject_unavailable"}
	}
	if err := s.git.checkInputsVersion(w.inputs.GitVersion); err != nil {
		return result, err
	}
	if sourceRef != "" {
		commit, err := s.git.readRef(ctx, s.colony, w.CommonDir, sourceRef, oidLength(w.ObjectFormat))
		if err != nil || commit != d.Commit {
			return result, errors.Join(ErrRevision, err)
		}
	}
	return ValidationSubject{service: s, selector: selector, descriptor: d, inputs: w.inputs}, nil
}

func cloneRevisions(in map[int64]inputsRevision) map[int64]inputsRevision {
	out := make(map[int64]inputsRevision, len(in))
	for revision, v := range in {
		v.refs = slices.Clone(v.refs)
		out[revision] = v
	}
	return out
}

package workspace

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Request binds a caller's immutable request ID to its local scratch head.
// Actor states authority, not identity; C3 authenticates the caller.
type Request struct {
	RequestID   uuid.UUID
	Actor       ledger.Actor
	ContextID   uuid.UUID
	WorkspaceID uuid.UUID
	Head        uuid.UUID
	RestartFrom uuid.UUID
}

// RequestState contains only the requesting context's submission and outcome.
// It never discloses a train predecessor or speculative generation or report.
type RequestState struct {
	RequestID      uuid.UUID
	State          string
	LineageID      uuid.UUID
	Checkpoint     int64
	SealedTree     string
	FailedRequest  uuid.UUID
	ConflictReport ledger.Digest
	FailureOutcome string
}

const (
	RequestPending          = "pending"
	RequestConflict         = "conflict"
	RequestDependencyFailed = "dependency_failed"
	RequestFinalizing       = "finalizing"
	RequestAccepted         = "accepted"
	RequestNoChange         = "no_change"
	RequestCancelled        = "cancelled"
	RequestInterrupted      = "interrupted"
	RequestUnknownFailure   = "unknown_failure"
)

type requestState struct {
	admission  ledger.PromotionAdmitted
	state      RequestState
	ending     string
	generation *ledger.GenerationDerived
	sequence   uint64
	intent     *ledger.FinalizationIntended
}

func (r requestState) successful() bool {
	return r.ending == ledger.TypePromotionAccepted || r.ending == ledger.TypePromotionNoChange
}

type requestPlan struct {
	request    Request
	scratch    Scratch
	workspace  workspaceState
	baseline   string
	lineage    uuid.UUID
	checkpoint int64
}

// identity and headPlan run under the service lock, including their second
// check under the scratch exclusion. The caller owns a service operation.
func (s *Service) identity(r Request) (RequestState, bool, error) {
	if old, ok := s.requests[r.RequestID]; ok {
		p := old.admission
		if p.ContextID != r.ContextID || p.WorkspaceID != r.WorkspaceID || p.ParentRequestID != r.Head || p.RestartFrom != r.RestartFrom {
			return RequestState{}, true, ErrRequestMismatch
		}
		return old.state, true, nil
	}
	if s.inFlight[r.RequestID] {
		return RequestState{}, true, &BusyError{Reason: "request_in_flight"}
	}
	return RequestState{}, false, nil
}

func (s *Service) headPlan(r Request, scratch scratchState) (requestPlan, error) {
	plan := requestPlan{request: r, scratch: scratch.Scratch, workspace: s.workspaces[r.WorkspaceID], baseline: scratch.representedTree, checkpoint: 1}
	h := s.requests[scratch.head]
	failed := h.ending == ledger.TypePromotionFailed
	zero := uuid.UUID{}
	switch {
	case scratch.head == zero && r.Head == zero && r.RestartFrom == zero:
		return plan, nil
	case scratch.head != zero && !failed && r.Head == scratch.head && r.RestartFrom == zero:
		if h.admission.Checkpoint == math.MaxInt64 {
			return requestPlan{}, ErrInvalid
		}
		plan.baseline, plan.lineage, plan.checkpoint = h.admission.SealedTree, h.admission.LineageID, h.admission.Checkpoint+1
		return plan, nil
	case failed && r.Head == zero && r.RestartFrom == scratch.head:
		return plan, nil
	default:
		return requestPlan{}, &HeadError{Head: scratch.head, RestartRequired: failed}
	}
}

func (s *Service) usableLocked() error {
	if s.closed {
		return ErrClosed
	}
	if s.poisoned {
		return ErrPoisoned
	}
	return nil
}

func (s *Service) beginRequest(ctx context.Context, r Request) (requestPlan, *reservation, RequestState, error) {
	var zero requestPlan
	var state RequestState
	s.mu.Lock()
	if err := s.usableLocked(); err != nil {
		s.mu.Unlock()
		return zero, nil, state, err
	}
	if !validID(r.RequestID) || !validID(r.ContextID) || !validID(r.WorkspaceID) || (r.Head != (uuid.UUID{}) && !validID(r.Head)) || (r.RestartFrom != (uuid.UUID{}) && !validID(r.RestartFrom)) || (r.Actor != ledger.ActorOwner && r.Actor != ledger.ActorAgent) {
		s.mu.Unlock()
		return zero, nil, state, ErrInvalid
	}
	if _, ok := s.workspaces[r.WorkspaceID]; !ok {
		s.mu.Unlock()
		return zero, nil, state, ErrUnknownWorkspace
	}
	if previous, found, err := s.identity(r); found {
		s.mu.Unlock()
		return zero, nil, previous, err
	}
	id, ok := s.pairs[scratchPair{r.ContextID, r.WorkspaceID}]
	if !ok {
		s.mu.Unlock()
		return zero, nil, state, ErrNoScratch
	}
	if scratch := s.scratches[id]; scratch.pendingMergeBack != (uuid.UUID{}) && !scratch.merging {
		s.mu.Unlock()
		return zero, nil, state, ErrMergeBackPending
	}
	plan, err := s.headPlan(r, s.scratches[id])
	s.mu.Unlock()
	if err != nil {
		return zero, nil, state, err
	}
	if s.deps.beforeRequestReserve != nil {
		s.deps.beforeRequestReserve()
	}
	token, err := s.pool.reserve(plan.scratch.Dir, admissionReservation)
	if err != nil {
		return zero, nil, state, err
	}
	s.mu.Lock()
	err = s.usableLocked()
	if err == nil && s.scratches[id].closed {
		err = ErrNoScratch
	}
	if err == nil && s.scratches[id].pendingMergeBack != (uuid.UUID{}) {
		err = ErrMergeBackPending
	}
	if err == nil {
		plan, err = s.headPlan(r, s.scratches[id])
	}
	scratch := s.scratches[id]
	s.mu.Unlock()
	if err == nil {
		err = s.repairScratchTip(ctx, plan.workspace, scratch)
	}
	if err != nil {
		token.release()
		return zero, nil, state, err
	}
	return plan, token, state, nil
}

func (s *Service) liveCount(scratchID uuid.UUID) int {
	return s.scratches[scratchID].live
}

// These commit-local obligations share the same checker during append and
// replay. Persistent projections change through apply's undo journal only.
type requestCommitRules struct {
	unpins                  map[uuid.UUID]int
	ended                   []uuid.UUID
	firstEnding, lastEnding int
	cascades                map[uuid.UUID][]uuid.UUID
}

func matchingPin(drafts []ledger.Draft, at int, id uuid.UUID, refs []ledger.Digest) bool {
	if at >= len(drafts) || drafts[at].Type != ledger.TypeBodyPin {
		return false
	}
	p, err := ledger.DecodeBodyPin(drafts[at].Payload)
	return err == nil && p.PinID == id && slices.Equal(drafts[at].BodyRefs, refs)
}

// requireUnpins requires each named pin once, after the operation and in order.
// Other producers' unrelated records may share a commit.
func requireUnpins(drafts []ledger.Draft, after int, ids []uuid.UUID) error {
	next := 0
	for i, d := range drafts {
		if d.Type != ledger.TypeBodyUnpin {
			continue
		}
		p, err := ledger.DecodeBodyUnpin(d.Payload)
		if err != nil {
			return err
		}
		if !slices.Contains(ids, p.PinID) {
			continue
		}
		if i <= after || next == len(ids) || p.PinID != ids[next] || len(d.BodyRefs) != 0 {
			return errors.New("unpin precedes its operation, repeats or is out of order")
		}
		next++
	}
	if next != len(ids) {
		return errors.New("required unpin missing")
	}
	return nil
}

func (s *Service) applyRequestRecord(drafts []ledger.Draft, at int, seq uint64, producing bool, rules *requestCommitRules, journal *[]func()) error {
	d := drafts[at]
	bad := func(detail string) error { return errors.New(detail) }
	switch d.Type {
	case ledger.TypePromotionAdmitted:
		p, err := ledger.DecodePromotionAdmitted(d.Payload)
		if err != nil {
			return err
		}
		if (d.Actor != ledger.ActorOwner && d.Actor != ledger.ActorAgent) || d.ContextID != p.ContextID {
			return bad("admission actor or context")
		}
		if !slices.Equal(d.BodyRefs, []ledger.Digest{p.ManifestSHA256}) || !matchingPin(drafts, at+1, p.PinID, d.BodyRefs) {
			return bad("admission manifest refs or immediate pin")
		}
		if _, exists := s.requests[p.RequestID]; exists || s.usedCaptures[p.CaptureID] {
			return bad("admission request or capture ID reused")
		}
		scratch, exists := s.scratches[p.ScratchID]
		if !exists || scratch.closed || scratch.ContextID != p.ContextID || scratch.WorkspaceID != p.WorkspaceID || s.pairs[scratchPair{p.ContextID, p.WorkspaceID}] != p.ScratchID || p.ScratchBase != scratch.BaseCommit {
			return bad("admission does not name the pair's open scratch and base")
		}
		if scratch.pendingMergeBack != (uuid.UUID{}) {
			return bad("admission names an unresolved merge-back parent")
		}
		observation, merges := s.admissionProvenance(scratch, p.ParentRequestID, seq)
		if p.ScratchObservationID != observation || !slices.Equal(p.MergeBackIDs, merges) {
			return bad("admission provenance differs from its observation or admission window")
		}
		r := Request{RequestID: p.RequestID, Actor: d.Actor, ContextID: p.ContextID, WorkspaceID: p.WorkspaceID, Head: p.ParentRequestID, RestartFrom: p.RestartFrom}
		plan, err := s.headPlan(r, scratch)
		if err != nil {
			return err
		}
		if p.Checkpoint != plan.checkpoint || p.BaselineTree != plan.baseline || p.DeltaBaseTree != plan.baseline {
			return bad("admission checkpoint, baseline or delta base")
		}
		if p.ParentRequestID != (uuid.UUID{}) {
			if p.LineageID != plan.lineage {
				return bad("successor changed lineage")
			}
		} else if s.usedLineages[p.LineageID] {
			return bad("root lineage reused")
		}
		w := plan.workspace
		if p.InputsRevision > w.Revision || w.revisions[p.InputsRevision].digest != p.CaptureInputsSHA256 {
			return bad("admission inputs revision or digest")
		}
		for _, oid := range []string{p.ScratchBase, p.BaselineTree, p.DeltaBaseTree, p.SealedTree} {
			if !validOID(oid, oidLength(w.ObjectFormat)) {
				return bad("admission Git ID length")
			}
		}
		if producing && (s.pool.profile == nil || s.liveCount(p.ScratchID) >= s.pool.profile.CheckpointsPerPair) {
			return bad("admission exceeds checkpoint policy")
		}
		oldTrain := s.trains[p.WorkspaceID]
		s.trains[p.WorkspaceID] = append(slices.Clone(oldTrain), p.RequestID)
		s.requests[p.RequestID] = requestState{admission: p, sequence: seq, state: RequestState{RequestID: p.RequestID, State: RequestPending, LineageID: p.LineageID, Checkpoint: p.Checkpoint, SealedTree: p.SealedTree}}
		s.usedCaptures[p.CaptureID] = true
		if p.ParentRequestID == (uuid.UUID{}) {
			s.usedLineages[p.LineageID] = true
			*journal = append(*journal, func() { delete(s.usedLineages, p.LineageID) })
		}
		oldPinRequest, hadPin := s.manifestPins[p.PinID]
		s.manifestPins[p.PinID] = p.RequestID
		next := scratch
		next.head = p.RequestID
		next.live++
		s.scratches[p.ScratchID] = next
		*journal = append(*journal, func() {
			delete(s.requests, p.RequestID)
			delete(s.usedCaptures, p.CaptureID)
			if hadPin {
				s.manifestPins[p.PinID] = oldPinRequest
			} else {
				delete(s.manifestPins, p.PinID)
			}
			s.trains[p.WorkspaceID] = oldTrain
			s.scratches[p.ScratchID] = scratch
		})
	case ledger.TypeGenerationDerived:
		p, err := ledger.DecodeGenerationDerived(d.Payload)
		if err != nil {
			return err
		}
		old, exists := s.requests[p.RequestID]
		if !exists || old.ending != "" || old.intent != nil {
			return bad("generation requires a live admitted request")
		}
		a := old.admission
		if d.Actor != ledger.ActorCore || d.ContextID != a.ContextID || s.usedGenerations[p.GenerationID] {
			return bad("generation actor, context or reused ID")
		}
		if p.Outcome == ledger.GenerationClean {
			if len(d.BodyRefs) != 0 {
				return bad("clean generation has body refs")
			}
		} else if !slices.Equal(d.BodyRefs, []ledger.Digest{p.ConflictSHA256}) || !matchingPin(drafts, at+1, p.ReportPinID, d.BodyRefs) {
			return bad("conflict report refs or immediate pin")
		}
		if old.generation == nil {
			if p.Supersedes != (uuid.UUID{}) {
				return bad("first generation supersedes")
			}
		} else {
			g := old.generation
			if p.Supersedes != g.GenerationID || p.PredecessorCommit == g.PredecessorCommit {
				return bad("supersession requires current generation and changed predecessor")
			}
			if g.ReportPinID != (uuid.UUID{}) {
				after := at
				if p.Outcome == ledger.GenerationConflict {
					after++
				}
				if err := requireUnpins(drafts, after, []uuid.UUID{g.ReportPinID}); err != nil {
					return err
				}
				rules.unpins[g.ReportPinID] = after
			}
		}
		train := s.trains[a.WorkspaceID]
		position := slices.Index(train, p.RequestID)
		if position < 0 {
			return bad("generation request absent from train")
		}
		if position == 0 {
			if p.PredecessorRequestID != (uuid.UUID{}) {
				return bad("leading generation must name canonical predecessor")
			}
			if !s.witnessMatches(a.WorkspaceID, p.PredecessorWitnessSeq, p.PredecessorCommit, p.PredecessorTree) {
				return bad("generation witness is absent, stale or differs from predecessor")
			}
			// Historical main tips are not in F2-06. Replay checks the absence
			// of a live predecessor; integrate binds the observed main objects.
		} else {
			if p.PredecessorWitnessSeq != 0 {
				return bad("speculative generation carries a canonical witness")
			}
			prev := s.requests[train[position-1]].generation
			if prev == nil || prev.Outcome != ledger.GenerationClean || p.PredecessorRequestID != train[position-1] || p.PredecessorGenerationID != prev.GenerationID || p.PredecessorCommit != prev.DerivedCommit || p.PredecessorTree != prev.DerivedTree {
				return bad("generation predecessor is not the nearest live clean generation")
			}
		}
		w := s.workspaces[a.WorkspaceID]
		if _, exists := w.revisions[p.InputsRevision]; !exists || p.InputsRevision > w.Revision {
			return bad("generation inputs revision is unrecorded")
		}
		for _, oid := range []string{p.PredecessorCommit, p.PredecessorTree, p.DerivedCommit, p.DerivedTree} {
			if oid != "" && !validOID(oid, oidLength(w.ObjectFormat)) {
				return bad("generation Git ID length")
			}
		}
		next := old
		next.generation = &p
		s.requests[p.RequestID] = next
		s.usedGenerations[p.GenerationID] = true
		*journal = append(*journal, func() { s.requests[p.RequestID] = old; delete(s.usedGenerations, p.GenerationID) })
		if p.ReportPinID != (uuid.UUID{}) {
			scratch := s.scratches[a.ScratchID]
			next := scratch
			next.reportPins = append(slices.Clone(scratch.reportPins), p.ReportPinID)
			s.scratches[a.ScratchID] = next
			oldPinScratch, hadPin := s.reportPinScratch[p.ReportPinID]
			s.reportPinScratch[p.ReportPinID] = a.ScratchID
			*journal = append(*journal, func() {
				s.scratches[a.ScratchID] = scratch
				if hadPin {
					s.reportPinScratch[p.ReportPinID] = oldPinScratch
				} else {
					delete(s.reportPinScratch, p.ReportPinID)
				}
			})
		}
	case ledger.TypePromotionFailed:
		p, err := ledger.DecodePromotionFailed(d.Payload)
		if err != nil {
			return err
		}
		old, exists := s.requests[p.RequestID]
		if !exists || old.ending != "" {
			return bad("failure requires a live admitted request")
		}
		a := old.admission
		if d.ContextID != a.ContextID || len(d.BodyRefs) != 0 {
			return bad("failure context or body refs")
		}
		next := old
		next.ending, next.state.State = ledger.TypePromotionFailed, p.Outcome
		if old.intent != nil && p.Outcome != ledger.FailedInterrupted {
			return bad("only interrupted may fail an unresolved intent")
		}
		for root, suffix := range rules.cascades {
			if position := slices.Index(suffix, p.RequestID); position >= 0 {
				if position != 0 || p.Outcome != ledger.FailedDependencyFailed || p.FailedRequestID != root {
					return bad("cancelled or interrupted suffix must be dependency_failed in checkpoint order")
				}
				rules.cascades[root] = suffix[1:]
			}
		}
		switch p.Outcome {
		case ledger.FailedConflict:
			if d.Actor != ledger.ActorCore || old.generation == nil || old.generation.GenerationID != p.GenerationID || old.generation.Outcome != ledger.GenerationConflict || s.trains[a.WorkspaceID][0] != p.RequestID {
				return bad("conflict failure requires the leading current conflict")
			}
			next.state.ConflictReport = old.generation.ConflictSHA256
		case ledger.FailedDependencyFailed:
			parent, exists := s.requests[p.FailedRequestID]
			if d.Actor != ledger.ActorCore || !exists || parent.ending != ledger.TypePromotionFailed || parent.admission.LineageID != a.LineageID || parent.admission.Checkpoint >= a.Checkpoint {
				return bad("dependency failure requires an earlier failed local checkpoint")
			}
			next.state.FailedRequest = p.FailedRequestID
		case ledger.FailedCancelled, ledger.FailedInterrupted:
			if p.GenerationID != (uuid.UUID{}) || p.FailedRequestID != (uuid.UUID{}) {
				return bad("cancelled or interrupted has optional failure keys")
			}
			if rules.cascades == nil {
				rules.cascades = make(map[uuid.UUID][]uuid.UUID)
			}
			for _, id := range s.trains[a.WorkspaceID] {
				other := s.requests[id].admission
				if other.LineageID == a.LineageID && other.Checkpoint > a.Checkpoint {
					rules.cascades[p.RequestID] = append(rules.cascades[p.RequestID], id)
				}
			}
			if p.Outcome == ledger.FailedCancelled {
				if d.Actor != ledger.ActorOwner && d.Actor != ledger.ActorAgent {
					return bad("cancellation requires owner or request context agent")
				}
			} else {
				if d.Actor != ledger.ActorCore {
					return bad("interrupted requires core")
				}
				if pending, ok := s.intents[a.WorkspaceID]; ok && pending.RequestID == p.RequestID {
					delete(s.intents, a.WorkspaceID)
					*journal = append(*journal, func() { s.intents[a.WorkspaceID] = pending })
				}
			}
		default:
			if producing {
				return bad("producer cannot write an undefined failure outcome")
			}
			next.state.State, next.state.FailureOutcome = RequestUnknownFailure, p.Outcome
		}
		next.generation = nil
		oldTrain := s.trains[a.WorkspaceID]
		s.trains[a.WorkspaceID] = slices.DeleteFunc(slices.Clone(oldTrain), func(id uuid.UUID) bool { return id == p.RequestID })
		s.requests[p.RequestID] = next
		scratch := s.scratches[a.ScratchID]
		nextScratch := scratch
		nextScratch.live--
		s.scratches[a.ScratchID] = nextScratch
		delete(s.manifestPins, a.PinID)
		*journal = append(*journal, func() {
			s.requests[p.RequestID] = old
			s.trains[a.WorkspaceID] = oldTrain
			s.scratches[a.ScratchID] = scratch
			s.manifestPins[a.PinID] = p.RequestID
		})
		if len(rules.ended) == 0 {
			rules.firstEnding = at
		}
		rules.lastEnding = at
		rules.ended = append(rules.ended, p.RequestID)
		rules.unpins[a.PinID] = at
	case ledger.TypeScratchClosed:
		p, err := ledger.DecodeScratchClosed(d.Payload)
		if err != nil {
			return err
		}
		if s.liveCount(p.ScratchID) != 0 {
			return bad("closure has live requests")
		}
		scratch := s.scratches[p.ScratchID]
		pins := s.scratchPins(scratch)
		if err := requireUnpins(drafts, at, pins); err != nil {
			return err
		}
		for _, pin := range pins {
			rules.unpins[pin] = at
		}
		if scratch.observationPin != (uuid.UUID{}) {
			delete(s.manifestPins, scratch.observationPin)
			next := scratch
			next.observationPin = uuid.UUID{}
			s.scratches[p.ScratchID] = next
			*journal = append(*journal, func() {
				s.scratches[p.ScratchID] = scratch
				s.manifestPins[scratch.observationPin] = scratch.ObservationID
			})
		}
	case ledger.TypeBodyUnpin:
		p, err := ledger.DecodeBodyUnpin(d.Payload)
		if err != nil {
			return err
		}
		if id, exists := s.reportPinScratch[p.PinID]; exists {
			scratch := s.scratches[id]
			after, allowed := rules.unpins[p.PinID]
			if !allowed || at <= after || len(d.BodyRefs) != 0 {
				return bad("report unpin requires supersession or closure, never an ending")
			}
			next := scratch
			next.reportPins = slices.DeleteFunc(slices.Clone(scratch.reportPins), func(id uuid.UUID) bool { return id == p.PinID })
			s.scratches[id] = next
			delete(s.reportPinScratch, p.PinID)
			*journal = append(*journal, func() {
				s.scratches[id] = scratch
				s.reportPinScratch[p.PinID] = id
			})
		}
		if _, live := s.manifestPins[p.PinID]; live {
			return bad("manifest unpin without its ending")
		}
	}
	return nil
}

func (s *Service) finishRequestCommit(drafts []ledger.Draft, rules requestCommitRules) error {
	var pins []uuid.UUID
	for _, id := range rules.ended {
		a := s.requests[id].admission
		for _, liveID := range s.trains[a.WorkspaceID] {
			r := s.requests[liveID]
			if r.admission.LineageID == a.LineageID && r.admission.Checkpoint > a.Checkpoint {
				return fmt.Errorf("failure leaves checkpoint %d live", r.admission.Checkpoint)
			}
		}
		pins = append(pins, a.PinID)
	}
	return requireUnpins(drafts, rules.lastEnding, pins)
}

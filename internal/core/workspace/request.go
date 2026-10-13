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
}

const (
	RequestPending          = "pending"
	RequestConflict         = "conflict"
	RequestDependencyFailed = "dependency_failed"
)

type requestState struct {
	admission  ledger.PromotionAdmitted
	state      RequestState
	ending     string
	generation *ledger.GenerationDerived
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
	plan := requestPlan{request: r, scratch: scratch.Scratch, workspace: s.workspaces[r.WorkspaceID], baseline: scratch.AnchorTree, checkpoint: 1}
	h := s.requests[scratch.head]
	failed := h.ending != ""
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
	if err == nil {
		plan, err = s.headPlan(r, s.scratches[id])
	}
	s.mu.Unlock()
	if err == nil {
		err = s.captureReady(ctx, plan.workspace, plan.scratch)
	}
	if err != nil {
		token.release()
		return zero, nil, state, err
	}
	return plan, token, state, nil
}

func (s *Service) liveCount(scratchID uuid.UUID) int {
	n := 0
	for _, r := range s.requests {
		if r.admission.ScratchID == scratchID && r.ending == "" {
			n++
		}
	}
	return n
}

// These commit-local obligations share the same checker during append and
// replay. Persistent projections change through apply's undo journal only.
type requestCommitRules struct {
	unpins                  map[uuid.UUID]int
	ended                   []uuid.UUID
	firstEnding, lastEnding int
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

func (s *Service) applyRequestRecord(drafts []ledger.Draft, at int, producing bool, rules *requestCommitRules, journal *[]func()) error {
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
		} else {
			for _, old := range s.requests {
				if old.admission.LineageID == p.LineageID {
					return bad("root lineage reused")
				}
			}
		}
		w := plan.workspace
		if p.InputsRevision > w.Revision || w.revisions[p.InputsRevision] != p.CaptureInputsSHA256 {
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
		s.requests[p.RequestID] = requestState{admission: p, state: RequestState{RequestID: p.RequestID, State: RequestPending, LineageID: p.LineageID, Checkpoint: p.Checkpoint, SealedTree: p.SealedTree}}
		s.usedCaptures[p.CaptureID] = true
		next := scratch
		next.head = p.RequestID
		s.scratches[p.ScratchID] = next
		*journal = append(*journal, func() {
			delete(s.requests, p.RequestID)
			delete(s.usedCaptures, p.CaptureID)
			s.trains[p.WorkspaceID] = oldTrain
			s.scratches[p.ScratchID] = scratch
		})
	case ledger.TypeGenerationDerived:
		p, err := ledger.DecodeGenerationDerived(d.Payload)
		if err != nil {
			return err
		}
		old, exists := s.requests[p.RequestID]
		if !exists || old.ending != "" {
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
			// Historical main tips are not in F2-06. Replay checks the absence
			// of a live predecessor; integrate binds the observed main objects.
		} else {
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
			*journal = append(*journal, func() { s.scratches[a.ScratchID] = scratch })
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
		next.ending, next.state.State = p.Outcome, p.Outcome
		switch p.Outcome {
		case ledger.FailedConflict:
			if d.Actor != ledger.ActorCore || old.generation == nil || old.generation.GenerationID != p.GenerationID || old.generation.Outcome != ledger.GenerationConflict || s.trains[a.WorkspaceID][0] != p.RequestID {
				return bad("conflict failure requires the leading current conflict")
			}
			next.state.ConflictReport = old.generation.ConflictSHA256
		case ledger.FailedDependencyFailed:
			parent, exists := s.requests[p.FailedRequestID]
			if d.Actor != ledger.ActorCore || !exists || parent.ending == "" || parent.admission.LineageID != a.LineageID || parent.admission.Checkpoint >= a.Checkpoint {
				return bad("dependency failure requires an earlier failed local checkpoint")
			}
			next.state.FailedRequest = p.FailedRequestID
		default:
			if producing {
				return bad("producer cannot write an undefined failure outcome")
			}
		}
		next.generation = nil
		oldTrain := s.trains[a.WorkspaceID]
		s.trains[a.WorkspaceID] = slices.DeleteFunc(slices.Clone(oldTrain), func(id uuid.UUID) bool { return id == p.RequestID })
		s.requests[p.RequestID] = next
		*journal = append(*journal, func() { s.requests[p.RequestID] = old; s.trains[a.WorkspaceID] = oldTrain })
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
		pins := s.scratches[p.ScratchID].reportPins
		if err := requireUnpins(drafts, at, pins); err != nil {
			return err
		}
		for _, pin := range pins {
			rules.unpins[pin] = at
		}
	case ledger.TypeBodyUnpin:
		p, err := ledger.DecodeBodyUnpin(d.Payload)
		if err != nil {
			return err
		}
		for id, scratch := range s.scratches {
			if !slices.Contains(scratch.reportPins, p.PinID) {
				continue
			}
			after, allowed := rules.unpins[p.PinID]
			if !allowed || at <= after || len(d.BodyRefs) != 0 {
				return bad("report unpin requires supersession or closure, never an ending")
			}
			next := scratch
			next.reportPins = slices.DeleteFunc(slices.Clone(scratch.reportPins), func(id uuid.UUID) bool { return id == p.PinID })
			s.scratches[id] = next
			*journal = append(*journal, func() { s.scratches[id] = scratch })
		}
		for _, r := range s.requests {
			if r.admission.PinID == p.PinID {
				if _, allowed := rules.unpins[p.PinID]; !allowed {
					return bad("manifest unpin without its ending")
				}
			}
		}
	}
	return nil
}

func (s *Service) finishRequestCommit(drafts []ledger.Draft, rules requestCommitRules) error {
	var pins []uuid.UUID
	for _, id := range rules.ended {
		a := s.requests[id].admission
		for _, r := range s.requests {
			if r.ending == "" && r.admission.LineageID == a.LineageID && r.admission.Checkpoint > a.Checkpoint {
				return fmt.Errorf("failure leaves checkpoint %d live", r.admission.Checkpoint)
			}
		}
		pins = append(pins, a.PinID)
	}
	return requireUnpins(drafts, rules.lastEnding, pins)
}

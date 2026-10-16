package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

func activationCallerFieldsAbsent(d ledger.Draft) bool {
	zero := uuid.UUID{}
	return d.AgentID == zero && d.ExecutionSessionID == zero && d.ContextID == zero && d.TurnID == zero && d.OperationID == zero
}

func activationBodyRefs(target ActivationTarget) []ledger.Digest {
	refs := []ledger.Digest{target.FieldLawSHA256, target.ValidationPolicyHash}
	for _, p := range target.WorkspacePolicies {
		refs = append(refs, p.CheckPolicyHash, p.EnvironmentPolicyHash)
	}
	slices.SortFunc(refs, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(refs)
}

// All replay checks are record-only. No clock, body store, Git or timer is used.
func (s *Service) applyActivationRecord(drafts []ledger.Draft, at int, seq uint64, producing bool, journal *[]func()) error {
	d := drafts[at]
	bad := func(text string) error { return errors.New(text) }
	switch d.Type {
	case ledger.TypePolicyActivationRequested:
		p, err := ledger.DecodePolicyActivationRequested(d.Payload)
		if err != nil {
			return err
		}
		if d.Actor != ledger.ActorOwner || !activationCallerFieldsAbsent(d) {
			return bad("activation request actor or caller context")
		}
		if seq > math.MaxInt64 {
			return bad("activation revision exhausted")
		}
		if s.pendingActivation != (uuid.UUID{}) {
			return bad("activation pending slot occupied")
		}
		for _, id := range []uuid.UUID{p.RequestID, p.OwnerApprovalID, p.PinID} {
			if s.activationIdentities[id] {
				return bad("activation identity reused")
			}
		}
		if at+1 >= len(drafts) || drafts[at+1].Type != ledger.TypeBodyPin {
			return bad("activation permanent pin is not immediate")
		}
		pin := drafts[at+1]
		pp, err := ledger.DecodeBodyPin(pin.Payload)
		if err != nil || pp.PinID != p.PinID || pin.Actor != ledger.ActorOwner || !activationCallerFieldsAbsent(pin) || !slices.Equal(pin.BodyRefs, d.BodyRefs) {
			return bad("activation permanent pin binding")
		}
		if p.ReplacesRequestID == (uuid.UUID{}) {
			if at != 0 || len(drafts) != 2 {
				return bad("activation request commit shape")
			}
		} else {
			if at != 1 || len(drafts) != 3 || drafts[0].Type != ledger.TypePolicyActivationEnded {
				return bad("activation replacement commit shape")
			}
			ended, err := ledger.DecodePolicyActivationEnded(drafts[0].Payload)
			if err != nil || ended.Outcome != ledger.ActivationReplaced || ended.RequestID != p.ReplacesRequestID || ended.ExpectedPendingRevision != p.ReplacesRevision || ended.ReplacementRequestID != p.RequestID {
				return bad("activation replacement CAS keys differ")
			}
		}
		target := activationRequestTarget(p)
		if !slices.Equal(d.BodyRefs, activationBodyRefs(target)) {
			return bad("activation body_refs differ from exact target union")
		}
		if p.ExpectedActiveRecordHash != s.activePolicy.RecordHash {
			return bad("activation expected manifest is not current")
		}
		for _, policy := range p.WorkspacePolicies {
			if _, exists := s.workspaces[policy.WorkspaceID]; !exists {
				return bad("activation policy workspace is unregistered")
			}
		}
		if !slices.Equal(p.AffectedWorkspaceIDs, activationAffectedWorkspaces(s.priorActivationTargetLocked(), target)) {
			return bad("activation affected set differs from manifest delta")
		}
		for _, id := range p.AffectedWorkspaceIDs {
			if _, exists := s.workspaces[id]; !exists {
				return bad("activation affected workspace is unregistered")
			}
		}
		if p.AdmissionUTCNS < s.activationFloor {
			return bad("activation admission below clock floor")
		}
		oldPending, oldFloor := s.pendingActivation, s.activationFloor
		s.activations[p.RequestID] = activationState{request: p, refs: slices.Clone(d.BodyRefs), state: ActivationState{RequestID: p.RequestID, OwnerApprovalID: p.OwnerApprovalID, Revision: seq, State: "pending", AdmissionUTCNS: p.AdmissionUTCNS, NotBeforeUTCNS: p.NotBeforeUTCNS}}
		s.pendingActivation = p.RequestID
		s.activationFloor = max(s.activationFloor, p.AdmissionUTCNS)
		for _, id := range []uuid.UUID{p.RequestID, p.OwnerApprovalID, p.PinID} {
			s.activationIdentities[id] = true
		}
		s.activationPins[p.PinID] = p.RequestID
		*journal = append(*journal, func() {
			delete(s.activations, p.RequestID)
			for _, id := range []uuid.UUID{p.RequestID, p.OwnerApprovalID, p.PinID} {
				delete(s.activationIdentities, id)
			}
			delete(s.activationPins, p.PinID)
			s.pendingActivation, s.activationFloor = oldPending, oldFloor
		})
	case ledger.TypePolicyActivationHoldChanged:
		p, err := ledger.DecodePolicyActivationHoldChanged(d.Payload)
		if err != nil {
			return err
		}
		if d.Actor != ledger.ActorCore || !activationCallerFieldsAbsent(d) || len(d.BodyRefs) != 0 || len(drafts) != 1 {
			return bad("activation hold actor, context, body_refs or commit shape")
		}
		old, exists := s.activations[p.RequestID]
		if !exists || s.pendingActivation != p.RequestID || old.state.RequestRecordHash != p.ActivationRequestRecordHash {
			return bad("activation hold does not name pending request")
		}
		if seq > math.MaxInt64 {
			return bad("activation revision exhausted")
		}
		if old.state.Hold == ledger.HoldDue || p.Hold == old.state.Hold {
			return bad("activation hold is repeated or leaves due")
		}
		switch p.Hold {
		case ledger.HoldNone:
			if old.state.Hold != ledger.HoldClockUntrusted || p.ObservedUTCNS < s.activationFloor || p.ObservedUTCNS >= old.request.NotBeforeUTCNS {
				return bad("activation none transition or observation")
			}
		case ledger.HoldClockUntrusted:
			if old.state.Hold != "" && old.state.Hold != ledger.HoldNone {
				return bad("activation clock hold transition")
			}
		case ledger.HoldDue:
			if p.ObservedUTCNS < old.request.NotBeforeUTCNS || p.ObservedUTCNS < s.activationFloor {
				return bad("activation due before eligibility or floor")
			}
		}
		next, floor := old, s.activationFloor
		next.state.Hold, next.state.Revision = p.Hold, seq
		s.activations[p.RequestID] = next
		if p.Hold != ledger.HoldClockUntrusted {
			s.activationFloor = max(s.activationFloor, p.ObservedUTCNS)
		}
		*journal = append(*journal, func() { s.activations[p.RequestID], s.activationFloor = old, floor })
	case ledger.TypePolicyActivationEnded:
		p, err := ledger.DecodePolicyActivationEnded(d.Payload)
		if err != nil {
			return err
		}
		if !activationCallerFieldsAbsent(d) || len(d.BodyRefs) != 0 {
			return bad("activation ending caller context or body_refs")
		}
		old, exists := s.activations[p.RequestID]
		if !exists || s.pendingActivation != p.RequestID || old.state.RequestRecordHash != p.ActivationRequestRecordHash || old.state.Revision != uint64(p.ExpectedPendingRevision) {
			return bad("activation ending pending identity or revision")
		}
		if seq > math.MaxInt64 {
			return bad("activation revision exhausted")
		}
		switch p.Outcome {
		case ledger.ActivationCancelled:
			if d.Actor != ledger.ActorOwner || len(drafts) != 1 {
				return bad("activation cancellation actor or commit shape")
			}
		case ledger.ActivationReplaced:
			if d.Actor != ledger.ActorOwner || at != 0 || len(drafts) != 3 || drafts[1].Type != ledger.TypePolicyActivationRequested || drafts[2].Type != ledger.TypeBodyPin {
				return bad("activation replacement actor or commit shape")
			}
			request, err := ledger.DecodePolicyActivationRequested(drafts[1].Payload)
			if err != nil || request.RequestID != p.ReplacementRequestID || request.ReplacesRequestID != p.RequestID || request.ReplacesRevision != p.ExpectedPendingRevision {
				return bad("activation replacement request differs from ending CAS")
			}
		default:
			if producing {
				return bad("producer cannot write unknown activation ending")
			}
		}
		next := old
		next.state.State, next.state.Hold, next.state.Reason = p.Outcome, "", ""
		// Future outcome tokens are terminal, never interpreted as application.
		if p.Outcome != ledger.ActivationCancelled && p.Outcome != ledger.ActivationReplaced {
			next.state.State = "cancelled"
		}
		next.state.Revision, next.state.ReplacementRequestID = seq, p.ReplacementRequestID
		s.activations[p.RequestID], s.pendingActivation = next, uuid.UUID{}
		*journal = append(*journal, func() { s.activations[p.RequestID], s.pendingActivation = old, p.RequestID })
	case ledger.TypePolicyActivated:
		p, err := ledger.DecodePolicyActivated(d.Payload)
		if err != nil {
			return err
		}
		if d.Actor != ledger.ActorCore || !activationCallerFieldsAbsent(d) || len(drafts) != 1 {
			return bad("activation switch actor, context or commit shape")
		}
		old, exists := s.activations[p.ActivationID]
		if !exists || s.pendingActivation != p.ActivationID || old.state.Hold != ledger.HoldDue {
			return bad("activation switch has no due pending request")
		}
		r := old.request
		if p.ActivationRequestRecordHash != old.state.RequestRecordHash || p.OwnerApprovalID != r.OwnerApprovalID || p.FieldLawSHA256 != r.FieldLawSHA256 || p.ValidationPolicyHash != r.ValidationPolicyHash || !slices.Equal(p.WorkspacePolicies, r.WorkspacePolicies) || p.ExpectedActiveRecordHash != r.ExpectedActiveRecordHash || p.NotBeforeUTCNS != r.NotBeforeUTCNS || !slices.Equal(d.BodyRefs, old.refs) {
			return bad("activation switch differs from request")
		}
		if p.ExpectedActiveRecordHash != s.activePolicy.RecordHash || s.activePolicy.Epoch >= math.MaxInt64 || uint64(p.Epoch) != s.activePolicy.Epoch+1 || p.EffectiveUTCNS < s.activationFloor || p.EffectiveUTCNS < r.NotBeforeUTCNS {
			return bad("activation switch epoch, expected hash or time")
		}
		for _, id := range r.AffectedWorkspaceIDs {
			if _, exists := s.intents[id]; exists {
				return bad("activation switch has unresolved affected intent")
			}
		}
		previous, floor := s.activePolicy, s.activationFloor
		active := ActivePolicy{ActivationID: p.ActivationID, OwnerApprovalID: p.OwnerApprovalID, Epoch: uint64(p.Epoch), EffectiveSequence: seq, RequestRecordHash: p.ActivationRequestRecordHash, ExpectedActiveRecordHash: p.ExpectedActiveRecordHash, NotBeforeUTCNS: p.NotBeforeUTCNS, EffectiveUTCNS: p.EffectiveUTCNS, Target: activationRequestTarget(r)}
		next := old
		next.state.State, next.state.Hold, next.state.Reason, next.state.Applied = "applied", "", "", &active
		s.activations[p.ActivationID], s.activePolicy, s.pendingActivation = next, active, uuid.UUID{}
		s.activationFloor = max(s.activationFloor, p.EffectiveUTCNS)
		*journal = append(*journal, func() {
			s.activations[p.ActivationID], s.activePolicy, s.pendingActivation, s.activationFloor = old, previous, p.ActivationID, floor
		})
	case ledger.TypeBodyUnpin:
		p, err := ledger.DecodeBodyUnpin(d.Payload)
		if err != nil {
			return err
		}
		if _, permanent := s.activationPins[p.PinID]; permanent {
			return bad("activation approval pin is permanent")
		}
	case ledger.TypeFinalizationIntended:
		p, err := ledger.DecodeFinalizationIntended(d.Payload)
		if err != nil {
			return err
		}
		id := s.requests[p.RequestID].admission.WorkspaceID
		if p.ActivationRecordHash != s.activePolicy.RecordHash {
			return bad("intent activation is not the current manifest")
		}
		if p.ActivationRecordHash != (ledger.Digest{}) && !s.activationHasWorkspaceLocked(id) {
			return bad("intent workspace has no active policy")
		}
	}
	return nil
}

func (s *Service) activationHasWorkspaceLocked(id uuid.UUID) bool {
	return slices.ContainsFunc(s.activePolicy.Target.WorkspacePolicies, func(p ledger.WorkspacePolicy) bool { return p.WorkspaceID == id })
}

// hydrateActivationCommit consumes verified envelopes, never locally hashed drafts.
// It is also called by Open with the commit its reader has already verified.
func (s *Service) hydrateActivationCommit(commit ledger.Commit, drafts []ledger.Draft, first, last uint64) error {
	if commit.First != first || commit.Last != last || len(commit.Records) != len(drafts) || last < first || last-first+1 != uint64(len(drafts)) {
		return errors.New("activation hydration commit bounds differ")
	}
	for i, record := range commit.Records {
		d := drafts[i]
		if record.Seq != first+uint64(i) || record.Type != d.Type || !bytes.Equal(record.Payload, d.Payload) {
			return errors.New("activation hydration record type or identity differs")
		}
		switch record.Type {
		case ledger.TypePolicyActivationRequested, ledger.TypePolicyActivated:
			hash, err := ledger.ParseDigest(record.Hash)
			if err != nil {
				return fmt.Errorf("activation hydration envelope hash: %w", err)
			}
			if record.Type == ledger.TypePolicyActivationRequested {
				p, err := ledger.DecodePolicyActivationRequested(record.Payload)
				if err != nil {
					return err
				}
				r, exists := s.activations[p.RequestID]
				if !exists {
					return errors.New("activation hydration request absent")
				}
				r.state.RequestRecordHash, r.state.Revision = hash, record.Seq
				s.activations[p.RequestID] = r
			} else {
				p, err := ledger.DecodePolicyActivated(record.Payload)
				if err != nil || s.activePolicy.ActivationID != p.ActivationID {
					return errors.New("activation hydration active identity differs")
				}
				s.activePolicy.RecordHash, s.activePolicy.EffectiveSequence = hash, record.Seq
				s.activationHashes[hash] = true
				r := s.activations[p.ActivationID]
				active := s.activePolicy
				r.state.Applied = &active
				s.activations[p.ActivationID] = r
			}
		}
	}
	return nil
}

func (s *Service) hydrateActivationAppend(drafts []ledger.Draft, committed appender.Committed) error {
	needsHashes := false
	for i, draft := range drafts {
		var id uuid.UUID
		switch draft.Type {
		case ledger.TypePolicyActivationRequested, ledger.TypePolicyActivated:
			needsHashes = true
		case ledger.TypePolicyActivationHoldChanged:
			p, _ := ledger.DecodePolicyActivationHoldChanged(draft.Payload)
			id = p.RequestID
		case ledger.TypePolicyActivationEnded:
			p, _ := ledger.DecodePolicyActivationEnded(draft.Payload)
			id = p.RequestID
		}
		if id != (uuid.UUID{}) {
			r := s.activations[id]
			r.state.Revision = committed.First + uint64(i)
			s.activations[id] = r
		}
	}
	if !needsHashes {
		return nil
	}
	reader, err := ledger.OpenReader(s.store.LogDir(), ledger.ReadOptions{Colony: s.store.Colony(), After: committed.First - 1, Through: committed.Last, Consume: workspaceConsumption(), AllTypes: true})
	if err == nil {
		var commit ledger.Commit
		commit, err = reader.Next()
		if err == nil {
			err = s.hydrateActivationCommit(commit, drafts, committed.First, committed.Last)
		}
		err = errors.Join(err, reader.Close())
	}
	if err != nil {
		s.poisoned = true
		return fmt.Errorf("%w: activation hash hydration: %w", ErrPoisoned, err)
	}
	return nil
}

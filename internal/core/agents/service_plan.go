package agents

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// errServiceDueHeld refuses a clear while a due pending member of its cause has
// not expired. Replay treats it as ErrInvalid; a live drain skips the clear
// until the member can expire, so it never wedges independent evidence.
var errServiceDueHeld = fmt.Errorf("%w: due service member held", ErrInvalid)

func serviceDue(src ledger.ServiceSource) (*int64, error) {
	grace := slices.Contains([]string{"rate_limited", "overloaded", "unreachable"}, src.Reason) || slices.Contains([]string{"quota_exhausted", "money_exhausted"}, src.Reason) && src.ResetUTCNS != nil
	if !grace {
		return nil, nil
	}
	if src.ObservedUTCNS > math.MaxInt64-ledger.ServiceGraceNS {
		return nil, ErrInvalid
	}
	return new(src.ObservedUTCNS + ledger.ServiceGraceNS), nil
}
func (p *serviceProjection) reserve(id uuid.UUID, key ledger.Digest, spec ledger.ServiceCauseSpec) error {
	if existing, ok := p.keys[key]; ok && existing != id {
		return ErrInvalid
	}
	if c, ok := p.causes[id]; ok {
		if c.key != key || !sameScopedValue(c.spec, spec) {
			return ErrInvalid
		}
		return nil
	}
	p.keys[key] = id
	p.causes[id] = serviceCause{id: id, key: key, spec: spec, unsupported: !serviceCauseSupported(spec)}
	return nil
}
func serviceWithdrawalMatches(c serviceCause, src ledger.ServiceSource) bool {
	clear := c.snapshot.Source
	return c.snapshot.Condition == "cleared" && src.Kind == "demand_withdrawn" &&
		src.Reason == clear.Reason && src.ObservedUTCNS == clear.ObservedUTCNS &&
		src.ClockHeld == clear.ClockHeld && sameScopedValue(src.Money, clear.Money) &&
		src.ExecutionRunID == clear.ExecutionRunID && src.ExecutionProof == clear.ExecutionProof &&
		(src.SourceSequence == nil || *src.SourceSequence == int64(c.sequence))
}

func serviceCompletionAfterOnset(m serviceMember, src ledger.ServiceSource) bool {
	return m.snapshot.Phase == "lost" && uint64(src.ObservedThrough) >= m.onsetSequence && src.ObservedUTCNS > m.snapshot.Source.ObservedUTCNS
}

func serviceSourceBase(src ledger.ServiceSource) ledger.ServiceSource {
	if src.Completion != nil {
		c := *src.Completion
		c.Permit = nil
		src.Completion = &c
	}
	return src
}
func (p serviceProjection) selectedSource(sub ledger.ServiceSubmission, src ledger.ServiceSource) (serviceObservation, error) {
	if src.Kind == "grace_expiry" {
		if _, exists := p.observations[src.ObservationID]; exists {
			return serviceObservation{}, ErrInvalid
		}
		return serviceObservation{}, nil
	}
	e, ok := p.observations[src.ObservationID]
	if !ok || !sameScopedValue(e.observed.Source, serviceSourceBase(src)) {
		return serviceObservation{}, ErrInvalid
	}
	if sub.AgentID != (uuid.UUID{}) && sub.AgentID != e.observed.AgentID {
		return serviceObservation{}, ErrInvalid
	}
	if src.Kind != "successful_call" && e.observed.CauseID != sub.CauseID {
		return serviceObservation{}, ErrInvalid
	}
	if src.Completion != nil {
		permit := src.Completion.Permit
		if permit == nil {
			return serviceObservation{}, ErrInvalid
		}
		if e.completion != nil && !slices.ContainsFunc(e.completion.Permits, func(p ledger.ServiceCompletionPermit) bool { return sameScopedValue(p, *permit) }) {
			return serviceObservation{}, ErrInvalid
		}
	}
	return e, nil
}
func (p serviceProjection) observationDone(e serviceObservation) bool {
	if e.unsupported || e.issue != nil {
		return false
	}
	if first, ok := p.semantic[e.semantic]; ok && first != e.observed.ObservationID {
		return true
	}
	if e.observed.Source.Kind == "successful_call" {
		if e.completion == nil {
			return false
		}
		for _, permit := range e.completion.Permits {
			if p.applied[serviceAction{e.observed.ObservationID, permit.CauseID}] {
				continue
			}
			c := p.causes[permit.CauseID]
			m := p.members[serviceMemberKey{e.observed.AgentID, permit.CauseID}]
			if c.unsupported || m.unsupported {
				return false
			}
			if permit.Kind == "blocked" || permit.Kind == "all_work_moved" && m.snapshot.Phase == "pending" || c.snapshot.Version != permit.CauseVersion || m.snapshot.Generation != permit.MemberGeneration || m.snapshot.Phase != "lost" && m.snapshot.Phase != "pending" {
				continue
			}
			if !serviceCompletionAfterOnset(m, e.observed.Source) && (c.snapshot.Condition == "cleared" || permit.Kind == "all_work_moved") {
				continue
			}
			return false
		}
		return true
	}
	if p.applied[serviceAction{e.observed.ObservationID, e.observed.CauseID}] {
		return true
	}
	c := p.causes[e.observed.CauseID]
	m := p.members[serviceMemberKey{e.observed.AgentID, e.observed.CauseID}]
	if c.unsupported || m.unsupported {
		return false
	}
	// Ignoring is permanent. A refusal recorded before its member's latest
	// generation ended was absorbed by that generation; a clear recorded
	// before its cause last activated (or for a cause never activated) was
	// overtaken. Neither becomes new evidence when the generation or cause
	// changes again, so coverage below the cursor stays stable.
	switch e.observed.Source.Kind {
	case "paid_call", "money_refusal", "integrity_refusal", "process_refusal":
		switch m.snapshot.Phase {
		case "pending", "lost":
			return m.snapshot.FirstSource.ObservationID != e.observed.ObservationID
		case "restored", "withdrawn":
			return e.sequence < m.sequence
		}
		return false
	case "cause_clear":
		return c.snapshot.Condition != "active" || e.sequence < c.sequence
	case "demand_withdrawn":
		return m.snapshot.Phase != "pending" || !serviceWithdrawalMatches(c, e.observed.Source)
	}
	return false
}
func (p *serviceProjection) advance() {
	for _, id := range p.order {
		e := p.observations[id]
		if int64(e.last) <= p.through {
			continue
		}
		if !p.observationDone(e) {
			break
		}
		p.through = int64(e.last)
	}
}

func (s *Service) deriveService(ctx context.Context, reads *historyReadSet, sub ledger.ServiceSubmission, first uint64) (servicePlan, error) {
	plan := servicePlan{next: s.p.service.clone()}
	p := &plan.next
	if _, err := sub.Encode(); err != nil {
		return servicePlan{}, ErrInvalid
	}
	if a, ok := sub.Arguments.(ledger.ServiceRecordArguments); ok {
		e := ledger.RuntimeServiceObserved{ObservationID: a.ObservationID, AgentID: sub.AgentID, CauseID: sub.CauseID, CauseKey: a.CauseKey, Cause: a.Cause, Source: a.Source, CompletionPermitsObject: a.CompletionPermitsObject}
		if err := s.validateServiceSource(ctx, reads, sub.AgentID, a.Source, first-1); err != nil {
			return servicePlan{}, err
		}
		if _, ok := p.observations[e.ObservationID]; ok {
			return servicePlan{}, ErrInvalid
		}
		if a.Cause != nil {
			if err := p.reserve(sub.CauseID, a.CauseKey, *a.Cause); err != nil {
				return servicePlan{}, err
			}
		}
		if e.CauseID != (uuid.UUID{}) {
			if _, ok := p.causes[e.CauseID]; !ok {
				return servicePlan{}, ErrInvalid
			}
			if err := s.validateServiceCause(ctx, reads, p.causes[e.CauseID], a.Source, sub.AgentID); err != nil {
				return servicePlan{}, err
			}
		}
		entry := serviceObservation{observed: e, sequence: first + 1, last: first + 2, unsupported: !serviceSourceSupported(a.Source), semantic: serviceSemantic(e)}
		if a.CompletionPermitsObject != nil {
			object, issue, err := s.loadServiceCompletion(ctx, reads, e, first-1)
			if err != nil {
				return servicePlan{}, err
			}
			entry.completion, entry.issue = object, issue
			if object != nil {
				entry.unsupported = entry.unsupported || !serviceCompletionSupported(object)
			}
		}
		if c, ok := p.causes[e.CauseID]; ok && entry.unsupported {
			c.unsupported = true
			p.causes[e.CauseID] = c
		}
		p.observations[e.ObservationID] = entry
		p.order = append(p.order, e.ObservationID)
		if _, exists := p.semantic[entry.semantic]; !exists {
			p.semantic[entry.semantic] = e.ObservationID
		}
		plan.observed = &e
		return plan, nil
	}
	a := sub.Arguments.(ledger.ServiceStateArguments)
	c, ok := p.causes[sub.CauseID]
	if !ok || c.key != a.CauseKey || !sameScopedValue(c.spec, a.Cause) || c.snapshot.Version != *sub.ExpectedCauseVersion {
		return servicePlan{}, ErrRevision
	}
	mkey := serviceMemberKey{sub.AgentID, sub.CauseID}
	m := p.members[mkey]
	if sub.Operation != "service.clear" && (m.snapshot.Version != *sub.ExpectedMemberVersion || m.snapshot.Generation != *sub.ExpectedGeneration) {
		return servicePlan{}, ErrRevision
	}
	if c.unsupported || m.unsupported || !serviceSourceSupported(a.Source) {
		return servicePlan{}, ErrUnavailable
	}
	e, err := p.selectedSource(sub, a.Source)
	if err != nil {
		return servicePlan{}, err
	}
	agent := sub.AgentID
	if agent == (uuid.UUID{}) {
		agent = e.observed.AgentID
	}
	if err := s.validateServiceSource(ctx, reads, agent, a.Source, first-1); err != nil {
		return servicePlan{}, err
	}
	if a.Source.Kind != "grace_expiry" && p.applied[serviceAction{a.Source.ObservationID, sub.CauseID}] {
		return servicePlan{}, ErrNoEffect
	}
	changeCause := func(condition string) error {
		if c.snapshot.Version == math.MaxInt64 {
			return ErrInvalid
		}
		// Every non-money clear, including a restoration's K, follows due losses.
		if condition == "cleared" {
			for key, member := range p.members {
				if key.cause == c.id && member.snapshot.Phase == "pending" && member.snapshot.DueUTCNS != nil && a.Source.ObservedUTCNS >= *member.snapshot.DueUTCNS && a.Source.Reason != "budget_available" {
					return errServiceDueHeld
				}
			}
		}
		k := ledger.RuntimeServiceCauseChanged{CauseID: c.id, CauseKey: c.key, ExpectedVersion: c.snapshot.Version, Version: c.snapshot.Version + 1, Cause: c.spec, Condition: condition, Source: a.Source}
		plan.cause = &k
		c.snapshot = k
		c.sequence = first + 1
		p.causes[c.id] = c
		return nil
	}
	setMember := func(phase string) {
		m.snapshot.ExpectedVersion = m.snapshot.Version
		m.snapshot.Version++
		m.snapshot.Phase = phase
		m.snapshot.AgentID = sub.AgentID
		m.snapshot.CauseID = sub.CauseID
		m.snapshot.Source = a.Source
		m.sequence = first + 1
		if plan.cause != nil {
			m.sequence++
		}
		plan.member = new(m.snapshot)
	}
	switch sub.Operation {
	case "service.observe":
		if m.snapshot.Phase == "pending" || m.snapshot.Phase == "lost" {
			return servicePlan{}, ErrNoEffect
		}
		if m.snapshot.Version > 0 && !slices.Contains([]string{"restored", "withdrawn"}, m.snapshot.Phase) || m.snapshot.Generation == math.MaxInt64 {
			return servicePlan{}, ErrUnavailable
		}
		if a.FirstSource == nil || !sameScopedValue(*a.FirstSource, e.observed.Source) || e.observed.Cause == nil || !sameScopedValue(*e.observed.Cause, c.spec) {
			return servicePlan{}, ErrInvalid
		}
		due, err := serviceDue(*a.FirstSource)
		if err != nil || !sameScopedValue(due, a.DueUTCNS) {
			return servicePlan{}, ErrInvalid
		}
		if c.spec.Code != "owner_money_exhausted" && c.spec.Code != a.Source.Reason || c.spec.Code == "owner_money_exhausted" && a.Source.Reason != "money_exhausted" {
			return servicePlan{}, ErrInvalid
		}
		if c.snapshot.Version == 0 || c.snapshot.Condition == "cleared" {
			if err := changeCause("active"); err != nil {
				return servicePlan{}, err
			}
		} else if c.snapshot.Condition != "active" {
			return servicePlan{}, ErrUnavailable
		}
		m.snapshot.Generation++
		m.snapshot.FirstSource = *a.FirstSource
		m.snapshot.DueUTCNS = due
		m.snapshot.OutageID = uuid.UUID{}
		if due != nil {
			setMember("pending")
			if a.OutageID != (uuid.UUID{}) || a.NoticeID != (uuid.UUID{}) {
				return servicePlan{}, ErrInvalid
			}
		} else {
			setMember("lost")
			if err := plan.joinOutage(mkey, a, first); err != nil {
				return servicePlan{}, err
			}
			m.snapshot = *plan.member
			m.onsetSequence = m.sequence
		}
	case "service.expire":
		if m.snapshot.Phase != "pending" || m.snapshot.DueUTCNS == nil || a.Source.ObservedUTCNS < *m.snapshot.DueUTCNS || a.Source.Reason != "grace_due" || c.snapshot.Condition != "active" {
			return servicePlan{}, ErrInvalid
		}
		setMember("lost")
		if err := plan.joinOutage(mkey, a, first); err != nil {
			return servicePlan{}, err
		}
		m.snapshot = *plan.member
		m.onsetSequence = m.sequence
	case "service.clear":
		if c.snapshot.Condition != "active" {
			return servicePlan{}, ErrNoEffect
		}
		if a.Source.Completion != nil {
			member := p.members[serviceMemberKey{e.observed.AgentID, c.id}]
			if serviceCompletionAfterOnset(member, a.Source) || a.Source.Completion.Permit.Kind == "all_work_moved" {
				return servicePlan{}, ErrInvalid
			}
			if err := s.checkServicePermit(ctx, reads, c, member, a.Source, first-1, false); err != nil {
				return servicePlan{}, err
			}
		}
		if err := changeCause("cleared"); err != nil {
			return servicePlan{}, err
		}
	case "service.withdraw":
		if m.snapshot.Phase != "pending" || !serviceWithdrawalMatches(c, a.Source) {
			return servicePlan{}, ErrInvalid
		}
		if m.snapshot.DueUTCNS == nil || a.Source.ObservedUTCNS >= *m.snapshot.DueUTCNS && a.Source.Reason != "budget_available" {
			return servicePlan{}, ErrInvalid
		}
		setMember("withdrawn")
	case "service.complete":
		if !serviceCompletionAfterOnset(m, a.Source) || m.snapshot.OutageID != a.OutageID {
			return servicePlan{}, ErrInvalid
		}
		if err := s.checkServicePermit(ctx, reads, c, m, a.Source, first-1, true); err != nil {
			return servicePlan{}, err
		}
		if c.snapshot.Condition == "active" && a.Source.Completion.Permit.Kind != "all_work_moved" {
			if err := changeCause("cleared"); err != nil {
				return servicePlan{}, err
			}
		}
		setMember("restored")
		if err := plan.leaveOutage(mkey, a, m.sequence); err != nil {
			return servicePlan{}, err
		}
	default:
		return servicePlan{}, ErrInvalid
	}
	if plan.member != nil {
		m.snapshot = *plan.member
		p.members[mkey] = m
	}
	if a.Source.Kind != "grace_expiry" {
		p.applied[serviceAction{a.Source.ObservationID, c.id}] = true
	}
	if a.Source.Completion != nil {
		e.selected = append(slices.Clone(e.selected), *a.Source.Completion.Permit)
		p.observations[e.observed.ObservationID] = e
	}
	p.advance()
	return plan, nil
}

func (p *servicePlan) joinOutage(key serviceMemberKey, a ledger.ServiceStateArguments, first uint64) error {
	id := p.next.open[key.cause]
	edge := id == (uuid.UUID{})
	if edge {
		id = a.OutageID
		if !validID(id) || !validID(a.NoticeID) || p.next.notices[a.NoticeID] {
			return ErrInvalid
		}
		if _, exists := p.next.outages[id]; exists {
			return ErrInvalid
		}
		p.next.open[key.cause] = id
		p.next.outages[id] = serviceOutage{id: id, cause: key.cause, beginNotice: a.NoticeID, beginSequence: first + uint64(1+btoi(p.cause != nil)) + 1, members: make(map[uuid.UUID]serviceOutageMember)}
	} else if id != a.OutageID || a.NoticeID != (uuid.UUID{}) {
		return ErrInvalid
	}
	o := p.next.outages[id]
	o.members = maps.Clone(o.members)
	if o.members[key.agent].active {
		return ErrInvalid
	}
	o.members[key.agent] = serviceOutageMember{p.member.Generation, true}
	p.next.outages[id] = o
	p.member.OutageID = id
	if edge {
		p.notice = &ledger.RuntimeServiceOutageNotice{OutageID: id, CauseID: key.cause, Edge: "began", NoticeID: a.NoticeID, MemberSourceSequence: int64(first) + int64(1+btoi(p.cause != nil))}
		p.next.notices[a.NoticeID] = true
	}
	return nil
}
func (p *servicePlan) leaveOutage(key serviceMemberKey, a ledger.ServiceStateArguments, memberSequence uint64) error {
	o, ok := p.next.outages[a.OutageID]
	if !ok || p.next.open[key.cause] != o.id || !o.members[key.agent].active {
		return ErrInvalid
	}
	o.members = maps.Clone(o.members)
	entry := o.members[key.agent]
	entry.active = false
	o.members[key.agent] = entry
	active := false
	for _, m := range o.members {
		active = active || m.active
	}
	if !active {
		if !validID(a.NoticeID) || p.next.notices[a.NoticeID] {
			return ErrInvalid
		}
		o.endNotice = a.NoticeID
		o.endSequence = memberSequence + 1
		p.notice = &ledger.RuntimeServiceOutageNotice{OutageID: o.id, CauseID: key.cause, Edge: "ended", NoticeID: a.NoticeID, MemberSourceSequence: int64(memberSequence)}
		p.next.notices[a.NoticeID] = true
		delete(p.next.open, key.cause)
	} else if a.NoticeID != (uuid.UUID{}) {
		return ErrInvalid
	}
	p.next.outages[o.id] = o
	return nil
}
func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Service) checkServicePermit(ctx context.Context, reads *historyReadSet, c serviceCause, m serviceMember, src ledger.ServiceSource, through uint64, restoring bool) error {
	if src.Completion == nil || src.Completion.Permit == nil {
		return ErrInvalid
	}
	permit := *src.Completion.Permit
	if permit.CauseID != c.id || permit.CauseVersion != c.snapshot.Version || permit.MemberGeneration != m.snapshot.Generation {
		return ErrRevision
	}
	if permit.Kind == "blocked" {
		return ErrNoEffect
	}
	switch permit.Kind {
	case "same_scope":
		if c.spec.Family != "provider" || c.spec.Provider != src.Completion.Provider {
			return ErrInvalid
		}
		a, found, err := s.loadPaid(ctx, reads, src.AttemptID, uint64(src.ObservedThrough), ledger.Digest{})
		if err != nil {
			return err
		}
		if !found || !servicePaidScopeMatches(c.spec, a) {
			return ErrInvalid
		}
	case "cleared", "execution_clear":
		if c.snapshot.Condition != "cleared" || permit.ProofSequence == nil || uint64(*permit.ProofSequence) != c.sequence || permit.Kind == "execution_clear" && c.spec.Family != "execution" {
			return ErrInvalid
		}
	case "credential_replaced":
		if c.spec.CredentialVersion == (uuid.UUID{}) || permit.ProofSequence == nil {
			return ErrInvalid
		}
		records, err := s.sourceRecords(ctx, reads, []int64{*permit.ProofSequence}, through)
		if err != nil {
			return err
		}
		r := records[*permit.ProofSequence]
		if r.Type != ledger.TypeRuntimeCredentialChanged || r.Actor != ledger.ActorOwner {
			return ErrInvalid
		}
		replacement, err := ledger.DecodeRuntimeCredentialChanged(r.Payload)
		if err != nil || replacement.ScopeID != c.spec.CredentialScopeID || replacement.CredentialVersion == c.spec.CredentialVersion || replacement.Action != "bind" || replacement.RevokePreviousVersion != c.spec.CredentialVersion {
			return ErrInvalid
		}
		a, found, err := s.loadPaid(ctx, reads, src.AttemptID, uint64(src.ObservedThrough), ledger.Digest{})
		if err != nil {
			return err
		}
		repaired := c.spec
		repaired.CredentialVersion = replacement.CredentialVersion
		if !found || !servicePaidScopeMatches(repaired, a) {
			return ErrInvalid
		}
	case "all_work_moved":
		if permit.FailedWorkCount == nil || *permit.FailedWorkCount != 0 || src.WorksetSHA256 == (ledger.Digest{}) || src.FailedWorkCount == nil || *src.FailedWorkCount != 0 {
			return ErrInvalid
		}
	default:
		return ErrUnavailable
	}
	if restoring && c.spec.Family != "provider" && c.snapshot.Condition != "cleared" {
		return ErrInvalid
	}
	return nil
}

func serviceCompletionSupported(object *ServiceCompletionObject) bool {
	for _, permit := range object.Permits {
		switch permit.Kind {
		case "same_scope", "cleared", "credential_replaced", "all_work_moved", "execution_clear", "blocked":
		default:
			return false
		}
	}
	return true
}

// A timer cannot overtake already captured positive evidence. With an unreadable
// completion object, its agent's contemporaneous memberships bound the possible
// permit set; unrelated causes remain eligible for expiry.
func (p serviceProjection) pendingPositiveSource(key serviceMemberKey, before uuid.UUID) bool {
	for _, id := range p.order {
		e := p.observations[id]
		if before != (uuid.UUID{}) && e.sequence >= p.observations[before].sequence {
			continue
		}
		if p.observationDone(e) {
			continue
		}
		switch e.observed.Source.Kind {
		case "cause_clear", "demand_withdrawn":
			if e.observed.CauseID == key.cause {
				return true
			}
		case "successful_call":
			member := p.members[serviceMemberKey{e.observed.AgentID, key.cause}].snapshot
			if e.completion == nil {
				if member.Generation > 0 && member.FirstSource.ObservedThrough <= e.observed.Source.ObservedThrough && member.FirstSource.ObservedUTCNS <= e.observed.Source.ObservedUTCNS {
					return true
				}
				continue
			}
			for _, permit := range e.completion.Permits {
				if permit.CauseID != key.cause || permit.Kind == "blocked" || p.applied[serviceAction{e.observed.ObservationID, key.cause}] ||
					permit.CauseVersion != p.causes[key.cause].snapshot.Version || permit.MemberGeneration != member.Generation ||
					member.Phase != "pending" && member.Phase != "lost" {
					continue
				}
				if permit.Kind == "all_work_moved" && member.Phase == "pending" {
					continue
				}
				if key.agent == e.observed.AgentID || permit.Kind == "same_scope" || permit.Kind == "credential_replaced" {
					return true
				}
			}
		}
	}
	return false
}

package agents

import (
	"bytes"
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func compareServiceIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }
func (p serviceProjection) unverifiable(key serviceMemberKey) bool {
	for _, e := range p.observations {
		if e.issue == nil && !e.unsupported {
			continue
		}
		if e.issue != nil && e.completion == nil && e.observed.AgentID == key.agent {
			m := p.members[key].snapshot
			if m.Generation > 0 && m.FirstSource.ObservedThrough <= e.observed.Source.ObservedThrough && m.FirstSource.ObservedUTCNS <= e.observed.Source.ObservedUTCNS {
				return true
			}
		}
		permits := e.selected
		if e.unsupported && e.completion != nil {
			permits = e.completion.Permits
		}
		for _, permit := range permits {
			if permit.CauseID == key.cause && (e.observed.AgentID == key.agent || p.causes[key.cause].snapshot.Source.ObservationID == e.observed.ObservationID) {
				return true
			}
		}
	}
	return false
}
func (s *Service) serviceRecoveryLocked() ServiceRecovery {
	r := ServiceRecovery{LastErrorClass: s.serviceError, RetainedSources: len(s.serviceRetained)}
	for _, id := range s.p.service.order {
		e := s.p.service.observations[id]
		if !s.p.service.observationDone(e) {
			r.UncoveredObservations++
		}
		if e.issue != nil && r.LastErrorClass == "" {
			r.LastErrorClass = "integrity"
		}
	}
	return r
}
func serviceCauseView(c serviceCause, m serviceMember) ServiceCauseView {
	v := ServiceCauseView{CauseID: c.id, Cause: c.spec, CauseVersion: c.snapshot.Version, Condition: c.snapshot.Condition, OutageID: m.snapshot.OutageID, MemberVersion: m.snapshot.Version, Generation: m.snapshot.Generation, Phase: m.snapshot.Phase, FirstSource: m.snapshot.FirstSource, DueUTCNS: m.snapshot.DueUTCNS}
	// Queries own the nested pointers too; callers cannot mutate the reducer.
	v.Cause.Region = cloneServicePointer(c.spec.Region)
	v.Cause.Revision = cloneServicePointer(c.spec.Revision)
	v.FirstSource = cloneServiceSource(m.snapshot.FirstSource)
	v.DueUTCNS = cloneServicePointer(m.snapshot.DueUTCNS)
	return v
}
func (s *Service) serviceViewLocked(agent uuid.UUID) ServiceView {
	p := s.p.service
	v := ServiceView{AgentID: agent, State: "available", ThroughSequence: s.p.through, SourceThrough: p.through, Recovery: s.serviceRecoveryLocked()}
	seen := make(map[uuid.UUID]bool)
	for key, m := range p.members {
		if key.agent != agent {
			continue
		}
		c := p.causes[key.cause]
		seen[c.id] = true
		view := serviceCauseView(c, m)
		if c.unsupported || m.unsupported || p.unverifiable(key) {
			view.Phase = "unsupported"
			v.Unsupported = append(v.Unsupported, view)
			if v.State != "lost" {
				v.State = "unsupported"
			}
			continue
		}
		switch m.snapshot.Phase {
		case "lost":
			v.Active = append(v.Active, view)
			v.State = "lost"
		case "pending":
			v.Pending = append(v.Pending, view)
		}
	}
	for _, e := range p.observations {
		if e.observed.AgentID != agent || seen[e.observed.CauseID] {
			continue
		}
		c, ok := p.causes[e.observed.CauseID]
		if ok && c.unsupported {
			view := serviceCauseView(c, serviceMember{})
			view.Phase = "unsupported"
			v.Unsupported = append(v.Unsupported, view)
			if v.State != "lost" {
				v.State = "unsupported"
			}
			seen[c.id] = true
		}
	}
	for _, list := range [][]ServiceCauseView{v.Active, v.Pending, v.Unsupported} {
		slices.SortFunc(list, func(a, b ServiceCauseView) int { return compareServiceIDs(a.CauseID, b.CauseID) })
	}
	return v
}

func (s *Service) ServiceStatus(ctx context.Context, agent uuid.UUID) (ServiceView, error) {
	if s == nil || !validID(agent) {
		return ServiceView{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return ServiceView{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, &historyReadSet{}, s.store.Durable()); err != nil {
		return ServiceView{}, err
	}
	if _, ok := s.p.agents[agent]; !ok {
		return ServiceView{}, ErrNotBound
	}
	return s.serviceViewLocked(agent), nil
}
func (s *Service) serviceOutageRowsLocked() []ServiceOutageRow {
	var rows []ServiceOutageRow
	for _, o := range s.p.service.outages {
		unsupported := make(map[uuid.UUID]bool)
		for id := range o.members {
			key := serviceMemberKey{id, o.cause}
			unsupported[id] = s.p.service.causes[o.cause].unsupported || s.p.service.members[key].unsupported || s.p.service.unverifiable(key)
		}
		members, active := []uuid.UUID{}, []uuid.UUID{}
		for id, m := range o.members {
			members = append(members, id)
			if m.active && !unsupported[id] {
				active = append(active, id)
			}
		}
		slices.SortFunc(members, compareServiceIDs)
		slices.SortFunc(active, compareServiceIDs)
		for _, id := range members {
			m := o.members[id]
			c := serviceCauseView(s.p.service.causes[o.cause], serviceMember{}).Cause
			rows = append(rows, ServiceOutageRow{OutageID: o.id, CauseID: o.cause, AgentID: id, Cause: c, Generation: m.generation, Active: m.active && !unsupported[id], Unsupported: unsupported[id], BeginNoticeID: o.beginNotice, EndNoticeID: o.endNotice, BeginSequence: o.beginSequence, EndSequence: o.endSequence, Members: slices.Clone(members), ActiveMembers: slices.Clone(active)})
		}
	}
	slices.SortFunc(rows, func(a, b ServiceOutageRow) int {
		if n := compareServiceIDs(a.OutageID, b.OutageID); n != 0 {
			return n
		}
		return compareServiceIDs(a.AgentID, b.AgentID)
	})
	return rows
}
func (s *Service) ServiceOutages(ctx context.Context, q ServiceOutageQuery) (ServiceOutagePage, error) {
	if s == nil || q.Limit < 0 || q.Limit > 128 {
		return ServiceOutagePage{}, ErrInvalid
	}
	if q.Limit == 0 {
		q.Limit = 128
	}
	if err := s.enter(ctx); err != nil {
		return ServiceOutagePage{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, &historyReadSet{}, s.store.Durable()); err != nil {
		return ServiceOutagePage{}, err
	}
	if c := q.Next; c != nil && (c.ThroughSequence != s.p.through || c.CallerID != q.CallerID || c.FilterAgentID != q.AgentID || c.FilterCauseID != q.CauseID || c.FilterOutageID != q.OutageID) {
		return ServiceOutagePage{}, ErrServicePageStale
	}
	page := ServiceOutagePage{ThroughSequence: s.p.through}
	for _, row := range s.serviceOutageRowsLocked() {
		if q.AgentID != (uuid.UUID{}) && q.AgentID != row.AgentID || q.CauseID != (uuid.UUID{}) && q.CauseID != row.CauseID || q.OutageID != (uuid.UUID{}) && q.OutageID != row.OutageID {
			continue
		}
		if c := q.Next; c != nil {
			n := compareServiceIDs(row.OutageID, c.OutageID)
			if n < 0 || n == 0 && compareServiceIDs(row.AgentID, c.AgentID) <= 0 {
				continue
			}
		}
		if len(page.Rows) == q.Limit {
			last := page.Rows[len(page.Rows)-1]
			page.Next = &ServiceOutageCursor{OutageID: last.OutageID, AgentID: last.AgentID, ThroughSequence: page.ThroughSequence, CallerID: q.CallerID, FilterAgentID: q.AgentID, FilterCauseID: q.CauseID, FilterOutageID: q.OutageID}
			break
		}
		page.Rows = append(page.Rows, row)
	}
	return page, nil
}
func (s *Service) ServiceStatusSnapshot(ctx context.Context) (ServiceSnapshot, error) {
	if s == nil {
		return ServiceSnapshot{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return ServiceSnapshot{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx, &historyReadSet{}, s.store.Durable()); err != nil {
		return ServiceSnapshot{}, err
	}
	v := ServiceSnapshot{ThroughSequence: s.p.through, SourceThrough: s.p.service.through, Recovery: s.serviceRecoveryLocked(), Outages: s.serviceOutageRowsLocked()}
	for agent := range s.p.agents {
		v.Agents = append(v.Agents, s.serviceViewLocked(agent))
	}
	slices.SortFunc(v.Agents, func(a, b ServiceView) int { return compareServiceIDs(a.AgentID, b.AgentID) })
	return v, nil
}

func (s *Service) serviceIntegrityIssues() []IntegrityIssue {
	var issues []IntegrityIssue
	for _, id := range s.p.service.order {
		if e := s.p.service.observations[id]; e.issue != nil {
			issues = append(issues, *e.issue)
		}
	}
	return issues
}

func cloneServicePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return new(*value)
}
func cloneServiceSource(src ledger.ServiceSource) ledger.ServiceSource {
	src.SourceSequence = cloneServicePointer(src.SourceSequence)
	src.Origin = cloneServicePointer(src.Origin)
	src.Object = cloneServicePointer(src.Object)
	src.ResetUTCNS = cloneServicePointer(src.ResetUTCNS)
	src.FailedWorkCount = cloneServicePointer(src.FailedWorkCount)
	if src.Money != nil {
		m := *src.Money
		m.CeilingMicros = cloneServicePointer(m.CeilingMicros)
		m.EligibleWorkCount = cloneServicePointer(m.EligibleWorkCount)
		m.FailedWorkCount = cloneServicePointer(m.FailedWorkCount)
		src.Money = &m
	}
	if src.Completion != nil {
		c := *src.Completion
		c.NativeResultSequence = cloneServicePointer(c.NativeResultSequence)
		if c.Permit != nil {
			p := *c.Permit
			p.ProofSequence = cloneServicePointer(p.ProofSequence)
			p.FailedWorkCount = cloneServicePointer(p.FailedWorkCount)
			c.Permit = &p
		}
		src.Completion = &c
	}
	return src
}

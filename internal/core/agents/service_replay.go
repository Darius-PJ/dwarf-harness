package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func isServiceType(typ string) bool {
	return slices.Contains([]string{ledger.TypeRuntimeServiceObserved, ledger.TypeRuntimeServiceCauseChanged, ledger.TypeRuntimeServiceAgentChanged, ledger.TypeRuntimeServiceOutageNotice, ledger.TypeRuntimeServiceReceipt}, typ)
}

// decodeServiceFamily is also used by command-history readers. A recognized
// service group cannot fall through as an unknown/private family.
func decodeServiceFamily(d []ledger.Draft, first uint64) (*serviceGroup, error) {
	found := false
	for _, r := range d {
		found = found || isServiceType(r.Type)
	}
	if !found {
		return nil, nil
	}
	bad := func() (*serviceGroup, error) { return nil, inconsistent(first, "service group") }
	if len(d) < 3 || len(d) > 5 || d[0].Type != ledger.TypeCommand || d[len(d)-1].Type != ledger.TypeRuntimeServiceReceipt {
		return bad()
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad()
	}
	r, err := ledger.DecodeRuntimeServiceReceipt(d[len(d)-1].Payload)
	if err != nil || r.CommandID != command.CommandID || r.SubmissionSHA256 != command.SubmissionSHA256 || len(r.Effects) != len(d)-2 {
		return bad()
	}
	raw, err := r.Submission.Encode()
	if err != nil || ledger.Digest(sha256.Sum256(raw)) != command.SubmissionSHA256 {
		return bad()
	}
	g := &serviceGroup{first: first, command: command, receipt: r}
	for i, v := range d {
		if v.TypeVersion != 1 || v.Actor != ledger.ActorCore || v.AgentID != (uuid.UUID{}) || v.ExecutionSessionID != (uuid.UUID{}) || v.ContextID != (uuid.UUID{}) || v.TurnID != (uuid.UUID{}) || v.OperationID != command.CommandID || v.MeasurementPath != "" || len(v.BodyRefs) != 0 {
			return bad()
		}
		if i == 0 || i == len(d)-1 {
			continue
		}
		if r.Effects[i-1].Type != v.Type || r.Effects[i-1].Sequence != int64(first)+int64(i) {
			return bad()
		}
		switch v.Type {
		case ledger.TypeRuntimeServiceObserved:
			p, e := ledger.DecodeRuntimeServiceObserved(v.Payload)
			err = e
			g.observed = &p
		case ledger.TypeRuntimeServiceCauseChanged:
			p, e := ledger.DecodeRuntimeServiceCauseChanged(v.Payload)
			err = e
			g.cause = &p
		case ledger.TypeRuntimeServiceAgentChanged:
			p, e := ledger.DecodeRuntimeServiceAgentChanged(v.Payload)
			err = e
			g.member = &p
		case ledger.TypeRuntimeServiceOutageNotice:
			p, e := ledger.DecodeRuntimeServiceOutageNotice(v.Payload)
			err = e
			g.notice = &p
		default:
			return bad()
		}
		if err != nil {
			return bad()
		}
	}
	if r.SourceThrough >= int64(first) {
		return bad()
	}
	if r.Submission.Operation == "service.record" {
		a := r.Submission.Arguments.(ledger.ServiceRecordArguments)
		want := ledger.RuntimeServiceObserved{ObservationID: a.ObservationID, AgentID: r.Submission.AgentID, CauseID: r.Submission.CauseID, CauseKey: a.CauseKey, Cause: a.Cause, Source: a.Source, CompletionPermitsObject: a.CompletionPermitsObject}
		if g.observed == nil || !sameScopedValue(*g.observed, want) {
			return bad()
		}
	} else {
		a := r.Submission.Arguments.(ledger.ServiceStateArguments)
		if g.cause != nil && (g.cause.CauseID != r.Submission.CauseID || g.cause.CauseKey != a.CauseKey || !sameScopedValue(g.cause.Cause, a.Cause) || !sameScopedValue(g.cause.Source, a.Source)) {
			return bad()
		}
		if g.member != nil && (g.member.AgentID != r.Submission.AgentID || g.member.CauseID != r.Submission.CauseID || !sameScopedValue(g.member.Source, a.Source)) {
			return bad()
		}
		if g.notice != nil && (g.member == nil || g.notice.CauseID != r.Submission.CauseID || g.notice.OutageID != a.OutageID || g.notice.NoticeID != a.NoticeID || g.notice.MemberSourceSequence != int64(first+uint64(len(d))-3)) {
			return bad()
		}
	}
	return g, nil
}

func serviceSourceSupported(src ledger.ServiceSource) bool {
	if !slices.Contains([]string{"paid_call", "money_refusal", "integrity_refusal", "process_refusal", "cause_clear", "successful_call", "grace_expiry", "demand_withdrawn"}, src.Kind) || !slices.Contains([]string{"rate_limited", "overloaded", "unreachable", "credential_denied", "scoped_denied", "quota_exhausted", "money_exhausted", "private_integrity", "process_unconfirmed", "provider_completed", "budget_available", "integrity_verified", "process_closed", "all_work_moved", "grace_due"}, src.Reason) {
		return false
	}
	if src.Provenance != "" && !slices.Contains([]string{"provider_response", "transport"}, src.Provenance) {
		return false
	}
	if src.ExecutionProof != "" && !slices.Contains([]string{"live", "retained_job", "name_absent"}, src.ExecutionProof) {
		return false
	}
	if src.Money != nil && !slices.Contains([]string{"mandatory_scope", "complete_workset"}, src.Money.Coverage) {
		return false
	}
	if c := src.Completion; c != nil {
		if c.Provider != "anthropic" && c.Provider != "openai" {
			return false
		}
		if c.Provider == "anthropic" && !slices.Contains([]string{"end_turn", "max_tokens", "stop_sequence", "tool_use", "model_context_window_exceeded"}, c.Terminal) || c.Provider == "openai" && c.Terminal != "completed" {
			return false
		}
		if c.FinancialKind != "" && !slices.Contains([]string{"usage", "unknown"}, c.FinancialKind) {
			return false
		}
		if c.Permit != nil && !slices.Contains([]string{"same_scope", "cleared", "credential_replaced", "all_work_moved", "execution_clear", "blocked"}, c.Permit.Kind) {
			return false
		}
	}
	return true
}
func serviceCauseSupported(c ledger.ServiceCauseSpec) bool {
	return slices.Contains([]string{"provider", "money", "execution"}, c.Family) && slices.Contains([]string{"credential_denied", "scoped_denied", "quota_exhausted", "rate_limited", "overloaded", "unreachable", "owner_money_exhausted", "private_integrity", "process_unconfirmed"}, c.Code) && slices.Contains([]string{"provider_route", "endpoint", "credential", "account", "account_route", "colony", "agent", "private"}, c.ScopeKind) && (c.Provider == "" || c.Provider == "anthropic" || c.Provider == "openai") && (c.PrivateScope == "" || c.PrivateScope == "agent" || c.PrivateScope == "context")
}

// A semantic source is the native call/refusal, not its callback UUID or a new
// service command. Keep that identity after an incident ends.
func serviceSemantic(e ledger.RuntimeServiceObserved) ledger.Digest {
	src := e.Source
	v := struct {
		Agent, Cause, Attempt, Command, Turn uuid.UUID
		Kind                                 string
		Sequence                             *int64
		Digest                               ledger.Digest
		Proof                                string
		Run                                  uuid.UUID
	}{e.AgentID, e.CauseID, src.AttemptID, src.CommandID, src.TurnID, src.Kind, src.SourceSequence, src.SubmissionSHA256, src.ExecutionProof, src.ExecutionRunID}
	if src.Kind == "demand_withdrawn" {
		v.Attempt = src.ObservationID
	}
	if src.Kind == "cause_clear" && src.SourceSequence == nil {
		v.Attempt = src.ObservationID
	}
	raw, _ := json.Marshal(v)
	return ledger.Digest(sha256.Sum256(raw))
}

func (s *Service) validateServiceSource(ctx context.Context, reads *historyReadSet, agent uuid.UUID, src ledger.ServiceSource, through uint64) error {
	if _, ok := s.p.agents[agent]; !ok || src.ObservedThrough < 0 || uint64(src.ObservedThrough) > through {
		return ErrInvalid
	}
	if src.Origin != nil && (!s.p.registeredOrigin(*src.Origin) || src.Origin.AgentID != agent) {
		return ErrNotBound
	}
	seqs := []int64{}
	if src.ObservedThrough > 0 {
		seqs = append(seqs, src.ObservedThrough)
	}
	if src.SourceSequence != nil {
		seqs = append(seqs, *src.SourceSequence)
	}
	if src.Money != nil {
		seqs = append(seqs, src.Money.SpendSequence)
	}
	if src.Completion != nil && src.Completion.NativeResultSequence != nil {
		seqs = append(seqs, *src.Completion.NativeResultSequence)
	}
	if src.Completion != nil && src.Completion.Permit != nil && src.Completion.Permit.ProofSequence != nil {
		seqs = append(seqs, *src.Completion.Permit.ProofSequence)
	}
	records, err := s.sourceRecords(ctx, reads, seqs, through)
	if err != nil {
		return err
	}
	if src.ObservedThrough > 0 {
		r := records[src.ObservedThrough]
		if r.CommitIndex+1 != r.CommitCount {
			return ErrInvalid
		}
	}
	if src.Origin != nil && src.TurnID != (uuid.UUID{}) {
		work, found, err := s.loadWork(ctx, reads, src.TurnID, uint64(src.ObservedThrough))
		if err != nil {
			return err
		}
		// A turn refused at admission has no native record: its integrity
		// refusal names the refused turn's own identity with the refused
		// command pair. An existing turn of that ID must still match.
		refusedAdmission := !found && src.Kind == "integrity_refusal" && src.SourceSequence == nil && src.CommandID != (uuid.UUID{})
		if !refusedAdmission && (!found || work.origin != *src.Origin || work.admission.Kind != "turn" || work.turn != src.TurnID) {
			return ErrNotBound
		}
	}
	if src.SourceSequence != nil && src.AttemptID != (uuid.UUID{}) {
		r := records[*src.SourceSequence]
		if r.Type != ledger.TypeRuntimePaidDispatchIntent || src.Origin == nil || r.AgentID != agent || r.ExecutionSessionID != src.Origin.SessionID || r.ContextID != src.Origin.ContextID || r.TurnID != src.TurnID {
			return ErrInvalid
		}
		intent, err := ledger.DecodeRuntimePaidDispatchIntent(r.Payload)
		if err != nil || intent.AttemptID != src.AttemptID || intent.ObservedUTCNS > src.ObservedUTCNS {
			return ErrInvalid
		}
		a, found, err := s.loadPaid(ctx, reads, src.AttemptID, uint64(src.ObservedThrough), ledger.Digest{})
		if err != nil {
			return err
		}
		if !found || a.origin != *src.Origin || src.Completion != nil && src.Completion.Provider != a.profile.Provider {
			return ErrInvalid
		}
		if src.Completion != nil && src.Completion.NativeResultSequence != nil {
			r := records[*src.Completion.NativeResultSequence]
			if r.Type != ledger.TypeRuntimePaidResult || r.AgentID != agent || r.ContextID != src.Origin.ContextID || r.TurnID != src.TurnID {
				return ErrInvalid
			}
			result, err := ledger.DecodeRuntimePaidResult(r.Payload)
			if err != nil || result.AttemptID != src.AttemptID || result.Kind != src.Completion.FinancialKind || result.EvidenceObject == nil || src.Object == nil || *result.EvidenceObject != *src.Object {
				return ErrInvalid
			}
		}
	} else if src.SourceSequence != nil && src.Origin != nil && src.Kind != "cause_clear" && src.Kind != "demand_withdrawn" {
		r := records[*src.SourceSequence]
		if r.AgentID != agent || r.ExecutionSessionID != src.Origin.SessionID || r.ContextID != src.Origin.ContextID || r.TurnID != src.TurnID {
			return ErrInvalid
		}
	}
	// A refused native command has no ledger receipt. Its digest is a witness,
	// not a command-directory lookup. A provided sequence is checked above.
	if src.Money != nil {
		if err := s.validateServiceMoney(ctx, reads, agent, src, records); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) planService(ctx context.Context, reads *historyReadSet, g *serviceGroup) (serviceProjection, error) {
	if g.cause != nil && g.cause.Condition != "active" && g.cause.Condition != "cleared" ||
		g.member != nil && g.member.Phase != "pending" && g.member.Phase != "lost" && g.member.Phase != "restored" && g.member.Phase != "withdrawn" ||
		g.notice != nil && g.notice.Edge != "began" && g.notice.Edge != "ended" ||
		g.observed == nil && g.receipt.Outcome != "recorded" {
		return s.retainUnsupportedService(ctx, reads, g)
	}
	plan, err := s.deriveService(ctx, reads, g.receipt.Submission, g.first)
	if err == ErrUnavailable {
		return s.retainUnsupportedService(ctx, reads, g)
	}
	if err != nil {
		return serviceProjection{}, err
	}
	if !sameScopedValue(plan.observed, g.observed) || !sameScopedValue(plan.cause, g.cause) || !sameScopedValue(plan.member, g.member) || !sameScopedValue(plan.notice, g.notice) || !plan.next.validServiceCoverage(g.receipt.SourceThrough, g.first) {
		return serviceProjection{}, ErrInvalid
	}
	if g.observed != nil {
		entry := plan.next.observations[g.observed.ObservationID]
		entry.receipt = g.receipt
		entry.unsupported = entry.unsupported || g.receipt.Outcome != "recorded"
		plan.next.observations[g.observed.ObservationID] = entry
		if entry.unsupported && entry.observed.CauseID != (uuid.UUID{}) {
			c := plan.next.causes[entry.observed.CauseID]
			c.unsupported = true
			plan.next.causes[c.id] = c
		}
	}
	if g.receipt.Outcome != "recorded" && g.cause != nil {
		c := plan.next.causes[g.cause.CauseID]
		c.unsupported = true
		plan.next.causes[c.id] = c
	}
	if g.receipt.Outcome != "recorded" && g.member != nil {
		key := serviceMemberKey{g.member.AgentID, g.member.CauseID}
		m := plan.next.members[key]
		m.unsupported = true
		plan.next.members[key] = m
	}
	plan.next.recordedThrough = max(plan.next.recordedThrough, g.receipt.SourceThrough)
	return plan.next, nil
}

// Unknown vocabulary is retained, not interpreted. The same group, binding,
// immutable identity and CAS checks still protect its structural history.
func (s *Service) retainUnsupportedService(ctx context.Context, reads *historyReadSet, g *serviceGroup) (serviceProjection, error) {
	p := s.p.service.clone()
	sub := g.receipt.Submission
	a, ok := sub.Arguments.(ledger.ServiceStateArguments)
	if !ok {
		return serviceProjection{}, ErrInvalid
	}
	c, ok := p.causes[sub.CauseID]
	if !ok || c.key != a.CauseKey || !sameScopedValue(c.spec, a.Cause) || c.snapshot.Version != *sub.ExpectedCauseVersion {
		return serviceProjection{}, ErrInvalid
	}
	e, err := p.selectedSource(sub, a.Source)
	if err != nil {
		return serviceProjection{}, err
	}
	if e.observed.ObservationID != (uuid.UUID{}) {
		e.unsupported = true
		p.observations[e.observed.ObservationID] = e
	}
	agent := sub.AgentID
	if agent == (uuid.UUID{}) {
		agent = e.observed.AgentID
	}
	if err := s.validateServiceSource(ctx, reads, agent, a.Source, g.first-1); err != nil {
		return serviceProjection{}, err
	}
	c.unsupported = true
	if g.cause != nil {
		if g.cause.ExpectedVersion != c.snapshot.Version {
			return serviceProjection{}, ErrInvalid
		}
		c.snapshot = *g.cause
		c.sequence = g.first + 1
	}
	p.causes[c.id] = c
	if g.member != nil {
		key := serviceMemberKey{sub.AgentID, sub.CauseID}
		m := p.members[key]
		if m.snapshot.Version != *sub.ExpectedMemberVersion || m.snapshot.Generation != *sub.ExpectedGeneration || g.member.ExpectedVersion != m.snapshot.Version {
			return serviceProjection{}, ErrInvalid
		}
		m.snapshot = *g.member
		m.sequence = g.first + uint64(1+btoi(g.cause != nil))
		m.unsupported = true
		p.members[key] = m
	}
	if g.notice != nil {
		if p.notices[g.notice.NoticeID] {
			return serviceProjection{}, ErrInvalid
		}
		p.notices[g.notice.NoticeID] = true
	}
	if !p.validServiceCoverage(g.receipt.SourceThrough, g.first) {
		return serviceProjection{}, ErrInvalid
	}
	p.recordedThrough = max(p.recordedThrough, g.receipt.SourceThrough)
	return p, nil
}

// Conservative coverage remains valid after metadata repair advances the local
// cursor. Above it, only unverifiable observations may explain a coverage gap.
func (p serviceProjection) validServiceCoverage(claim int64, first uint64) bool {
	if claim == p.through {
		return true
	}
	if claim < 0 || claim >= int64(first) {
		return false
	}
	boundary := claim == 0
	for _, id := range p.order {
		e := p.observations[id]
		if int64(e.last) > claim {
			break
		}
		boundary = int64(e.last) == claim
		if !p.observationDone(e) && e.issue == nil && !e.unsupported {
			return false
		}
	}
	return boundary
}

func servicePaidScopeMatches(c ledger.ServiceCauseSpec, a paidAttempt) bool {
	return c.Provider == a.profile.Provider &&
		(c.CredentialScopeID == (uuid.UUID{}) || c.CredentialScopeID == a.admission.Request.Credential.ScopeID) &&
		(c.CredentialVersion == (uuid.UUID{}) || c.CredentialVersion == a.admission.Request.Credential.Version) &&
		(c.Model == "" || c.Model == a.profile.ModelID) &&
		(c.Region == nil || *c.Region == a.profile.Region) &&
		(c.AuthoritySHA256 == (ledger.Digest{}) || c.AuthoritySHA256 == ledger.Digest(sha256.Sum256([]byte(a.profile.Authority))))
}

func (s *Service) validateServiceCause(ctx context.Context, reads *historyReadSet, c serviceCause, src ledger.ServiceSource, agent uuid.UUID) error {
	if c.unsupported || !serviceSourceSupported(src) {
		return nil
	}
	switch src.Kind {
	case "paid_call":
		if c.spec.Family != "provider" || c.spec.Code != src.Reason {
			return ErrInvalid
		}
		a, found, err := s.loadPaid(ctx, reads, src.AttemptID, uint64(src.ObservedThrough), ledger.Digest{})
		if err != nil {
			return err
		}
		if !found || !servicePaidScopeMatches(c.spec, a) {
			return ErrInvalid
		}
	case "money_refusal":
		if c.spec.Family != "money" || src.Reason != "money_exhausted" {
			return ErrInvalid
		}
	case "integrity_refusal":
		if c.spec.Code != "private_integrity" || src.Reason != "private_integrity" || c.spec.AgentID != agent || c.spec.ContextID != (uuid.UUID{}) && (src.Origin == nil || c.spec.ContextID != src.Origin.ContextID) {
			return ErrInvalid
		}
	case "process_refusal":
		if c.spec.Code != "process_unconfirmed" || src.Reason != "process_unconfirmed" {
			return ErrInvalid
		}
	case "demand_withdrawn":
		if !serviceWithdrawalMatches(c, src) {
			return ErrInvalid
		}
	case "cause_clear":
		if c.spec.Family == "money" && src.Reason != "budget_available" ||
			c.spec.Code == "private_integrity" && src.Reason != "integrity_verified" ||
			c.spec.Code == "process_unconfirmed" && src.Reason != "process_closed" ||
			c.spec.Family == "provider" {
			return ErrInvalid
		}
	}
	if src.Money != nil {
		scope := src.Money.Scope
		if c.spec.Family != "money" || c.spec.ScopeKind != scope.Kind ||
			scope.Kind == "agent" && c.spec.AgentID != scope.ID ||
			scope.Kind == "credential" && c.spec.CredentialScopeID != scope.ID {
			return ErrInvalid
		}
	}
	return nil
}

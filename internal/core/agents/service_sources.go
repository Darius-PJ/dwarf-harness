package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type serviceCompletionHistoryKey struct {
	Origin      ledger.Origin
	Object      ledger.ObjectRef
	Observation uuid.UUID
}

func (serviceCompletionHistoryKey) historyKind() historyNeedKind { return needServiceCompletion }

type serviceCompletionRead struct {
	Raw     []byte
	Missing bool
}

func decodeServiceCompletion(raw []byte, e ledger.RuntimeServiceObserved) (*ServiceCompletionObject, error) {
	if len(raw) > maxObjectBytes {
		return nil, ErrTooLarge
	}
	type wireObject ServiceCompletionObject
	var wire struct {
		wireObject
		Workset string `json:"workset_sha256,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, ErrIntegrity
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrIntegrity
	}
	object := ServiceCompletionObject(wire.wireObject)
	if wire.Workset != "" {
		var err error
		object.WorksetSHA256, err = ledger.ParseDigest(wire.Workset)
		if err != nil {
			return nil, ErrIntegrity
		}
	}
	if object.Schema != 1 || object.Kind != "service_completion" || object.ObservationID != e.ObservationID || object.ThroughSequence != e.Source.ObservedThrough || object.WorksetSHA256 != e.Source.WorksetSHA256 {
		return nil, ErrIntegrity
	}
	var previous uuid.UUID
	for _, p := range object.Permits {
		if !validID(p.CauseID) || p.CauseVersion <= 0 || p.MemberGeneration <= 0 || p.Kind == "" || compareServiceIDs(previous, p.CauseID) >= 0 || p.ProofSequence != nil && (*p.ProofSequence <= 0 || *p.ProofSequence > object.ThroughSequence) || p.FailedWorkCount != nil && *p.FailedWorkCount < 0 {
			return nil, ErrIntegrity
		}
		switch p.Kind {
		case "same_scope", "blocked":
			if p.ProofSequence != nil || p.FailedWorkCount != nil {
				return nil, ErrIntegrity
			}
		case "cleared", "credential_replaced", "execution_clear":
			if p.ProofSequence == nil || p.FailedWorkCount != nil {
				return nil, ErrIntegrity
			}
		case "all_work_moved":
			if p.FailedWorkCount == nil || *p.FailedWorkCount != 0 || object.WorksetSHA256 == (ledger.Digest{}) {
				return nil, ErrIntegrity
			}
		}
		previous = p.CauseID
	}
	return &object, nil
}
func (s *Service) loadServiceCompletion(ctx context.Context, reads *historyReadSet, e ledger.RuntimeServiceObserved, through uint64) (*ServiceCompletionObject, *IntegrityIssue, error) {
	if e.CompletionPermitsObject == nil || e.Source.Origin == nil {
		return nil, nil, ErrInvalid
	}
	ref := *e.CompletionPermitsObject
	origin := *e.Source.Origin
	if ref.Bytes < 0 || ref.Bytes > maxObjectBytes {
		return nil, nil, ErrTooLarge
	}
	agent, ok := s.p.agents[origin.AgentID]
	if !ok {
		return nil, nil, ErrNotBound
	}
	scope, err := newPrivateScope(s.colony, agent.directory, origin.ContextID.String(), &s.dirs)
	if err != nil {
		return nil, nil, err
	}
	answer, err := readHistory(ctx, reads, 0, through, serviceCompletionHistoryKey{origin, ref, e.ObservationID}, func(ctx context.Context) (serviceCompletionRead, error) {
		if err := ctx.Err(); err != nil {
			return serviceCompletionRead{}, err
		}
		raw, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
		return serviceCompletionRead{Raw: raw, Missing: err != nil}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	var object *ServiceCompletionObject
	if !answer.Missing {
		object, err = decodeServiceCompletion(answer.Raw, e)
	}
	if answer.Missing || err != nil {
		return nil, &IntegrityIssue{ReadBinding: ReadBinding{Binding: Binding{AgentID: origin.AgentID, ContextID: origin.ContextID}, Scope: "context"}, ExpectedSHA256: ref.SHA256, Reason: "service_completion_missing_or_invalid"}, nil
	}
	return object, nil, nil
}

// publishServiceCompletion publishes only the separate nonfinancial metadata
// object. It never opens, rewrites or interprets the paid response object.
func (s *Service) publishServiceCompletion(ctx context.Context, origin ledger.Origin, object ServiceCompletionObject) (ledger.ObjectRef, error) {
	if s == nil {
		return ledger.ObjectRef{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return ledger.ObjectRef{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	if !s.p.registeredOrigin(origin) {
		s.mu.Unlock()
		return ledger.ObjectRef{}, ErrNotBound
	}
	s.mu.Unlock()
	raw, err := json.Marshal(object)
	if err != nil {
		return ledger.ObjectRef{}, err
	}
	e := ledger.RuntimeServiceObserved{ObservationID: object.ObservationID, Source: ledger.ServiceSource{ObservedThrough: object.ThroughSequence, WorksetSHA256: object.WorksetSHA256}}
	if _, err := decodeServiceCompletion(raw, e); err != nil {
		return ledger.ObjectRef{}, err
	}
	unlock, err := s.acquireScope(ctx, scopeKey{agent: origin.AgentID, context: origin.ContextID})
	if err != nil {
		return ledger.ObjectRef{}, err
	}
	defer unlock()
	path, err := s.scopePath(submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}, "context")
	if err != nil {
		return ledger.ObjectRef{}, err
	}
	digest, err := path.publishObject(raw)
	if err != nil {
		return ledger.ObjectRef{}, err
	}
	return ledger.ObjectRef{SHA256: digest, Bytes: int64(len(raw))}, nil
}

func serviceDrafts(command uuid.UUID, sub ledger.ServiceSubmission, plan servicePlan, first uint64) ([]ledger.Draft, error) {
	raw, err := sub.Encode()
	if err != nil {
		return nil, err
	}
	digest := ledger.Digest(sha256.Sum256(raw))
	cmd := ledger.Command{CommandID: command, SubmissionSHA256: digest}
	payload, err := cmd.Encode()
	if err != nil {
		return nil, err
	}
	d := []ledger.Draft{{Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorCore, OperationID: command, Payload: payload}}
	effects := []ledger.ServiceEffect{}
	appendEffect := func(typ string, value interface {
		Encode() (json.RawMessage, error)
	}) error {
		payload, err := value.Encode()
		if err != nil {
			return err
		}
		effects = append(effects, ledger.ServiceEffect{Type: typ, Sequence: int64(first) + int64(len(d))})
		d = append(d, ledger.Draft{Type: typ, TypeVersion: 1, Actor: ledger.ActorCore, OperationID: command, Payload: payload})
		return nil
	}
	if plan.observed != nil {
		if err := appendEffect(ledger.TypeRuntimeServiceObserved, *plan.observed); err != nil {
			return nil, err
		}
	}
	if plan.cause != nil {
		if err := appendEffect(ledger.TypeRuntimeServiceCauseChanged, *plan.cause); err != nil {
			return nil, err
		}
	}
	if plan.member != nil {
		if err := appendEffect(ledger.TypeRuntimeServiceAgentChanged, *plan.member); err != nil {
			return nil, err
		}
	}
	if plan.notice != nil {
		if err := appendEffect(ledger.TypeRuntimeServiceOutageNotice, *plan.notice); err != nil {
			return nil, err
		}
	}
	r := ledger.RuntimeServiceReceipt{CommandID: command, SubmissionSHA256: digest, Submission: sub, Outcome: "recorded", Effects: effects, SourceThrough: plan.next.through}
	payload, err = r.Encode()
	if err != nil {
		return nil, err
	}
	d = append(d, ledger.Draft{Type: ledger.TypeRuntimeServiceReceipt, TypeVersion: 1, Actor: ledger.ActorCore, OperationID: command, Payload: payload})
	return d, nil
}

// commitServiceLocked uses the same tail-bounded journal protocol as other
// metadata families. The read set survives acknowledgement, so selected
// service_completion bytes are never read while holding mu after SubmitAt.
func (s *Service) commitServiceLocked(ctx context.Context, command uuid.UUID, build func(*historyReadSet) (ledger.ServiceSubmission, error)) (ledger.RuntimeServiceReceipt, error) {
	var zero ledger.RuntimeServiceReceipt
	for range tailAttempts {
		reads := &historyReadSet{}
		var d []ledger.Draft
		var prior *serviceGroup
		err := s.prepareHistoryLocked(ctx, reads, nil, true, func(reads *historyReadSet) error {
			sub, err := build(reads)
			if err != nil {
				return err
			}
			raw, err := sub.Encode()
			if err != nil || !validID(command) {
				return ErrInvalid
			}
			digest := ledger.Digest(sha256.Sum256(raw))
			match, err := s.findCommand(ctx, reads, command, 0, s.p.through)
			if err != nil {
				return err
			}
			if match.found {
				if match.service == nil || match.command.SubmissionSHA256 != digest || !sameScopedValue(match.service.receipt.Submission, sub) {
					return ErrCommandMismatch
				}
				prior = match.service
				return nil
			}
			if a, ok := sub.Arguments.(ledger.ServiceStateArguments); ok && (s.p.service.causes[sub.CauseID].unsupported || s.p.service.members[serviceMemberKey{sub.AgentID, sub.CauseID}].unsupported || s.p.service.unverifiable(serviceMemberKey{sub.AgentID, sub.CauseID}) || !serviceSourceSupported(a.Source)) {
				return ErrUnavailable
			}
			plan, err := s.deriveService(ctx, reads, sub, s.p.through+1)
			if err != nil {
				return err
			}
			if plan.observed != nil {
				entry := plan.next.observations[plan.observed.ObservationID]
				if entry.issue != nil {
					return *entry.issue
				}
				if entry.unsupported {
					return ErrUnavailable
				}
			}
			d, err = serviceDrafts(command, sub, plan, s.p.through+1)
			if err != nil {
				return err
			}
			if s.deps.beforeSubmit != nil {
				d = s.deps.beforeSubmit(d)
			}
			g, err := decodeServiceFamily(d, s.p.through+1)
			if err != nil {
				return err
			}
			if g == nil || g.command.CommandID != command || g.command.SubmissionSHA256 != digest {
				return ErrCommandMismatch
			}
			_, err = s.planService(ctx, reads, g)
			return err
		})
		if err != nil {
			return zero, err
		}
		if prior != nil {
			return prior.receipt, nil
		}
		through := s.p.through
		var receipt ledgerstore.Receipt
		if s.deps.beforeServiceSubmit != nil {
			err = s.deps.beforeServiceSubmit()
		}
		if err == nil {
			receipt, err = s.store.SubmitAt(ctx, through, d[0], d[1:])
		}
		if errors.Is(err, ledgerstore.ErrTailMoved) {
			continue
		}
		if err != nil {
			if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
				s.poisoned = true
			}
			if errors.Is(err, ledgerstore.ErrCommandMismatch) {
				err = ErrCommandMismatch
			}
			return zero, err
		}
		c, checkpoint, err := s.readCommittedRange(context.Background(), receipt.First, receipt.Last, s.history.checkpoint(through))
		if err != nil {
			s.poisoned = true
			return zero, err
		}
		match, install, err := s.stageCommitLocked(context.Background(), reads, c, nil)
		if err != nil || match.service == nil {
			s.poisoned = true
			if err == nil {
				err = ErrInconsistent
			}
			return zero, err
		}
		install()
		s.history.putCheckpoint(c.Last, checkpoint)
		if s.deps.afterServiceCommit != nil {
			s.deps.afterServiceCommit(match.service)
		}
		return match.service.receipt, nil
	}
	return zero, &BusyError{Reason: "ledger_tail"}
}

func serviceRecordSubmission(e ledger.RuntimeServiceObserved) ledger.ServiceSubmission {
	return ledger.ServiceSubmission{Schema: 9, Actor: "core", Operation: "service.record", AgentID: e.AgentID, CauseID: e.CauseID, Arguments: ledger.ServiceRecordArguments{ObservationID: e.ObservationID, CauseKey: e.CauseKey, Cause: e.Cause, Source: e.Source, CompletionPermitsObject: e.CompletionPermitsObject}}
}
func (s *Service) recordServiceEvidence(ctx context.Context, command uuid.UUID, e ledger.RuntimeServiceObserved) (ledger.RuntimeServiceReceipt, error) {
	if s == nil {
		return ledger.RuntimeServiceReceipt{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return ledger.RuntimeServiceReceipt{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	var known bool
	if err := s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
		match, err := s.findCommand(ctx, reads, command, 0, s.p.through)
		known = match.found
		return err
	}); err != nil {
		return ledger.RuntimeServiceReceipt{}, err
	}
	if known {
		return s.recordServiceEvidenceLocked(ctx, command, e)
	}
	if err := s.drainServiceEvidenceLocked(ctx); err != nil && !errors.Is(err, ErrIntegrity) {
		return ledger.RuntimeServiceReceipt{}, err
	}
	r, err := s.recordServiceEvidenceLocked(ctx, command, e)
	if err != nil {
		s.retainServiceErrorLocked(err)
		return r, err
	}
	if err = s.drainServiceEvidenceLocked(ctx); err != nil {
		s.retainServiceErrorLocked(err)
	}
	return r, err
}
func (s *Service) recordServiceEvidenceLocked(ctx context.Context, command uuid.UUID, e ledger.RuntimeServiceObserved) (ledger.RuntimeServiceReceipt, error) {
	if command != e.ObservationID {
		return ledger.RuntimeServiceReceipt{}, ErrInvalid
	}
	var original *ledger.RuntimeServiceReceipt
	r, err := s.commitServiceLocked(ctx, command, func(reads *historyReadSet) (ledger.ServiceSubmission, error) {
		original = nil
		if e.Cause != nil {
			key, err := e.Cause.Key()
			if err != nil {
				return ledger.ServiceSubmission{}, err
			}
			e.CauseKey = key
			if id := s.p.service.keys[key]; id != (uuid.UUID{}) {
				e.CauseID = id
			} else if e.CauseID == (uuid.UUID{}) {
				e.CauseID = uuid.NewV7()
			}
		}
		raw, err := e.Encode()
		if err != nil {
			return ledger.ServiceSubmission{}, err
		}
		e, err = ledger.DecodeRuntimeServiceObserved(raw)
		if err != nil {
			return ledger.ServiceSubmission{}, err
		}
		sub := serviceRecordSubmission(e)
		match, err := s.findCommand(ctx, reads, command, 0, s.p.through)
		if err != nil || match.found {
			return sub, err
		}
		if c, ok := s.p.service.causes[e.CauseID]; ok && c.unsupported || s.p.service.unverifiable(serviceMemberKey{e.AgentID, e.CauseID}) {
			return ledger.ServiceSubmission{}, ErrUnavailable
		}
		if !serviceSourceSupported(e.Source) || e.Cause != nil && !serviceCauseSupported(*e.Cause) {
			return ledger.ServiceSubmission{}, ErrUnavailable
		}
		if e.CompletionPermitsObject != nil {
			object, _, err := s.loadServiceCompletion(ctx, reads, e, s.p.through)
			if err != nil {
				return ledger.ServiceSubmission{}, err
			}
			if object != nil {
				if !serviceCompletionSupported(object) {
					return ledger.ServiceSubmission{}, ErrUnavailable
				}
				for _, permit := range object.Permits {
					key := serviceMemberKey{e.AgentID, permit.CauseID}
					if s.p.service.causes[permit.CauseID].unsupported || s.p.service.members[key].unsupported || s.p.service.unverifiable(key) {
						return ledger.ServiceSubmission{}, ErrUnavailable
					}
				}
			}
		}
		if id, ok := s.p.service.semantic[serviceSemantic(e)]; ok {
			original = new(s.p.service.observations[id].receipt)
			return ledger.ServiceSubmission{}, ErrNoEffect
		}
		if e.Source.Kind == "paid_call" || e.Source.Kind == "money_refusal" || e.Source.Kind == "integrity_refusal" || e.Source.Kind == "process_refusal" {
			m := s.p.service.members[serviceMemberKey{e.AgentID, e.CauseID}]
			if m.snapshot.Phase == "pending" || m.snapshot.Phase == "lost" {
				original = new(s.p.service.observations[m.snapshot.FirstSource.ObservationID].receipt)
				return ledger.ServiceSubmission{}, ErrNoEffect
			}
		}
		return sub, nil
	})
	if errors.Is(err, ErrNoEffect) && original != nil {
		return *original, nil
	}
	return r, err
}

func (s *Service) serviceStateSubmission(operation string, agent, cause uuid.UUID, src ledger.ServiceSource) (ledger.ServiceSubmission, error) {
	c, ok := s.p.service.causes[cause]
	if !ok {
		return ledger.ServiceSubmission{}, ErrInvalid
	}
	m := s.p.service.members[serviceMemberKey{agent, cause}]
	a := ledger.ServiceStateArguments{CauseKey: c.key, Cause: c.spec, Source: src}
	sub := ledger.ServiceSubmission{Schema: 9, Actor: "core", Operation: operation, CauseID: cause, ExpectedCauseVersion: new(c.snapshot.Version)}
	if operation != "service.clear" {
		sub.AgentID = agent
		sub.ExpectedMemberVersion = new(m.snapshot.Version)
		sub.ExpectedGeneration = new(m.snapshot.Generation)
	}
	switch operation {
	case "service.observe":
		a.FirstSource = new(src)
		var err error
		a.DueUTCNS, err = serviceDue(src)
		if err != nil {
			return ledger.ServiceSubmission{}, err
		}
		if a.DueUTCNS == nil {
			a.OutageID = s.p.service.open[cause]
			if a.OutageID == (uuid.UUID{}) {
				a.OutageID = uuid.NewV7()
				a.NoticeID = uuid.NewV7()
			}
		}
	case "service.expire":
		a.OutageID = s.p.service.open[cause]
		if a.OutageID == (uuid.UUID{}) {
			a.OutageID = uuid.NewV7()
			a.NoticeID = uuid.NewV7()
		}
	case "service.complete":
		a.OutageID = m.snapshot.OutageID
		o := s.p.service.outages[a.OutageID]
		count := 0
		for _, member := range o.members {
			if member.active {
				count++
			}
		}
		if count == 1 {
			a.NoticeID = uuid.NewV7()
		}
	}
	sub.Arguments = a
	return sub, nil
}
func (s *Service) applyServiceSourceLocked(ctx context.Context, operation string, agent, cause uuid.UUID, src ledger.ServiceSource) error {
	command := uuid.NewV7()
	_, err := s.commitServiceLocked(ctx, command, func(*historyReadSet) (ledger.ServiceSubmission, error) {
		return s.serviceStateSubmission(operation, agent, cause, src)
	})
	return err
}
func (s *Service) retainServiceErrorLocked(err error) {
	if err == nil {
		return
	}
	switch {
	case s.poisoned:
		s.serviceError = "uncertain"
	case errors.Is(err, ErrIntegrity):
		s.serviceError = "integrity"
	case errors.Is(err, ErrUnavailable):
		s.serviceError = "unsupported"
	case errors.Is(err, context.Canceled):
		s.serviceError = "cancelled"
	default:
		s.serviceError = "service_journal"
	}
}

// Repair is metadata-only. Invalid selected entries are corruption, whereas a
// missing object remains a scoped integrity issue and does not block other E's.
func (s *Service) repairServiceMetadataLocked(ctx context.Context) error {
	for _, id := range slices.Clone(s.p.service.order) {
		entry := s.p.service.observations[id]
		if entry.issue == nil {
			continue
		}
		reads := &historyReadSet{}
		var object *ServiceCompletionObject
		var issue *IntegrityIssue
		err := s.prepareHistoryLocked(ctx, reads, nil, true, func(reads *historyReadSet) error {
			entry = s.p.service.observations[id]
			var err error
			object, issue, err = s.loadServiceCompletion(ctx, reads, entry.observed, s.p.through)
			return err
		})
		if err != nil {
			return err
		}
		if issue != nil {
			continue
		}
		for _, selected := range entry.selected {
			if !slices.ContainsFunc(object.Permits, func(p ledger.ServiceCompletionPermit) bool { return sameScopedValue(p, selected) }) {
				s.poisoned = true
				return inconsistent(entry.sequence, "selected service permit")
			}
		}
		entry.completion, entry.issue = object, nil
		entry.unsupported = entry.unsupported || !serviceCompletionSupported(object)
		if int64(entry.last) <= s.p.service.recordedThrough && !entry.unsupported && !s.p.service.observationDone(entry) {
			s.poisoned = true
			return inconsistent(entry.sequence, "service source coverage")
		}
		s.p.service.observations[id] = entry
	}
	return nil
}

func (s *Service) drainServiceEvidenceLocked(ctx context.Context) error {
	if err := s.refreshLocked(ctx, &historyReadSet{}, s.store.Durable()); err != nil {
		return err
	}
	if err := s.repairServiceMetadataLocked(ctx); err != nil {
		return err
	}
	for {
		if err := s.usableLocked(ctx); err != nil {
			return err
		}
		progress := false
		for _, id := range slices.Clone(s.p.service.order) {
			e := s.p.service.observations[id]
			if e.unsupported || e.issue != nil || s.p.service.observationDone(e) {
				continue
			}
			if c, ok := s.p.service.causes[e.observed.CauseID]; ok && c.unsupported {
				continue
			}
			changed, err := s.drainServiceObservationLocked(ctx, e)
			if err != nil {
				s.retainServiceErrorLocked(err)
				return err
			}
			if changed {
				progress = true
				break
			}
		}
		if !progress {
			// Ignore-only observations advance this process's scan frontier, not
			// the ledger. The next real receipt persists this exact coverage.
			s.p.service.advance()
			if err := s.withdrawClearedServiceLocked(ctx); err != nil {
				return err
			}
			for _, e := range s.p.service.observations {
				if e.issue != nil {
					s.serviceError = "integrity"
					return *e.issue
				}
			}
			s.serviceError = ""
			return nil
		}
	}
}

func (s *Service) drainServiceObservationLocked(ctx context.Context, e serviceObservation) (bool, error) {
	src := e.observed.Source
	agent, cause := e.observed.AgentID, e.observed.CauseID
	switch src.Kind {
	case "paid_call", "money_refusal", "integrity_refusal", "process_refusal":
		m := s.p.service.members[serviceMemberKey{agent, cause}]
		if m.unsupported || s.p.service.unverifiable(serviceMemberKey{agent, cause}) {
			return false, nil
		}
		if m.snapshot.Phase == "pending" || m.snapshot.Phase == "lost" {
			return false, nil
		}
		return true, s.applyServiceSourceLocked(ctx, "service.observe", agent, cause, src)
	case "cause_clear":
		if err := s.expireBeforeServiceClearLocked(ctx, cause, src); err != nil {
			return false, err
		}
		return true, s.applyServiceSourceLocked(ctx, "service.clear", agent, cause, src)
	case "demand_withdrawn":
		return true, s.applyServiceSourceLocked(ctx, "service.withdraw", agent, cause, src)
	case "successful_call":
		if e.completion == nil {
			return false, nil
		}
		for _, permit := range e.completion.Permits {
			if s.p.service.applied[serviceAction{e.observed.ObservationID, permit.CauseID}] {
				continue
			}
			c := s.p.service.causes[permit.CauseID]
			key := serviceMemberKey{agent, permit.CauseID}
			m := s.p.service.members[key]
			if c.unsupported || m.unsupported || s.p.service.unverifiable(key) {
				continue
			}
			if permit.Kind == "blocked" || permit.Kind == "all_work_moved" && m.snapshot.Phase == "pending" || c.snapshot.Version != permit.CauseVersion || m.snapshot.Generation != permit.MemberGeneration || m.snapshot.Phase != "lost" && m.snapshot.Phase != "pending" {
				continue
			}
			selected := cloneServiceSource(src)
			selected.Completion.Permit = new(permit)
			if !serviceCompletionAfterOnset(m, src) {
				if c.snapshot.Condition == "cleared" || permit.Kind == "all_work_moved" {
					continue
				}
				if err := s.expireBeforeServiceClearLocked(ctx, c.id, selected); err != nil {
					return false, err
				}
				return true, s.applyServiceSourceLocked(ctx, "service.clear", agent, c.id, selected)
			}
			if c.snapshot.Condition == "active" && permit.Kind != "all_work_moved" {
				if err := s.expireBeforeServiceClearLocked(ctx, c.id, selected); err != nil {
					return false, err
				}
			}
			return true, s.applyServiceSourceLocked(ctx, "service.complete", agent, c.id, selected)
		}
	}
	return false, nil
}
func (s *Service) expireBeforeServiceClearLocked(ctx context.Context, cause uuid.UUID, src ledger.ServiceSource) error {
	if src.Reason == "budget_available" {
		return nil
	}
	var keys []serviceMemberKey
	for key, m := range s.p.service.members {
		if key.cause == cause && m.snapshot.Phase == "pending" && m.snapshot.DueUTCNS != nil && src.ObservedUTCNS >= *m.snapshot.DueUTCNS {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b serviceMemberKey) int { return compareServiceIDs(a.agent, b.agent) })
	for _, key := range keys {
		if err := s.expireServiceMemberLocked(ctx, key, src.ObservationID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) withdrawClearedServiceLocked(ctx context.Context) error {
	var keys []serviceMemberKey
	for key, m := range s.p.service.members {
		c := s.p.service.causes[key.cause]
		if !c.unsupported && !m.unsupported && m.snapshot.Phase == "pending" && c.snapshot.Condition == "cleared" {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b serviceMemberKey) int {
		if n := compareServiceIDs(a.cause, b.cause); n != 0 {
			return n
		}
		return compareServiceIDs(a.agent, b.agent)
	})
	for _, key := range keys {
		m := s.p.service.members[key]
		c := s.p.service.causes[key.cause]
		if m.snapshot.Phase != "pending" || c.snapshot.Condition != "cleared" {
			continue
		}
		clear := c.snapshot.Source
		if m.snapshot.DueUTCNS == nil || clear.ObservedUTCNS >= *m.snapshot.DueUTCNS && clear.Reason != "budget_available" {
			continue
		}
		id := uuid.NewV7()
		src := ledger.ServiceSource{Kind: "demand_withdrawn", ObservationID: id, ObservedThrough: int64(s.p.through), SourceSequence: new(int64(c.sequence)), ObservedUTCNS: clear.ObservedUTCNS, ClockHeld: clear.ClockHeld, Reason: clear.Reason, Money: clear.Money, ExecutionRunID: clear.ExecutionRunID, ExecutionProof: clear.ExecutionProof}
		e := ledger.RuntimeServiceObserved{ObservationID: id, AgentID: key.agent, CauseID: key.cause, Source: src}
		if _, err := s.recordServiceEvidenceLocked(ctx, id, e); err != nil {
			return err
		}
		if err := s.applyServiceSourceLocked(ctx, "service.withdraw", key.agent, key.cause, src); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileServiceEvidence catches up existing service metadata and real due
// timers only. It never probes a provider, executes work or repairs money.
func (s *Service) ReconcileServiceEvidence(ctx context.Context) (ServiceRecovery, error) {
	if s == nil {
		return ServiceRecovery{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return ServiceRecovery{}, err
	}
	defer s.active.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.drainServiceEvidenceLocked(ctx)
	if usable := s.usableLocked(ctx); usable != nil {
		err = usable
	} else {
		timerErr := s.expireDueServiceLocked(ctx)
		if timerErr != nil {
			err = timerErr
		}
	}
	if err != nil {
		s.retainServiceErrorLocked(err)
	}
	if err == nil {
		s.notifyServiceTimerLocked()
	}
	return s.serviceRecoveryLocked(), err
}

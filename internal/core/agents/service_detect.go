package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// Live service adapters turn existing refusals and completions into F3-91
// sources. They never change a native return value, stop reason, tool result,
// turn state, hold or charge: a source that cannot be recorded is a retained
// service condition, and only an uncertain append poisons as before.

// servicePaidLive is one live paid observation's service evidence, decided
// once from the validated paid_observation object and the dispatch token.
type servicePaidLive struct {
	attempt uuid.UUID
	// intent is the attempt's dispatch intent record, read while that intent
	// was still the attempt's latest group: a retry after the financial
	// result cannot find it from the attempt's projection.
	intent           int64
	evidence         ledger.ObjectRef
	provenance       string
	terminal         string
	failure          providers.ServiceFailure
	retryAfter       []string
	observedUTCNS    int64
	clockHeld        bool
	completion       *ServiceCompletionObject
	completionObject ledger.ObjectRef
}

var errServiceSnapshotStale = errors.New("agents: service completion snapshot is stale")

// servicePaidEvidence returns nil unless the live call is a qualifying success
// or a diagnosed provider failure. Only the live branch with a token reaches it.
func servicePaidEvidence(a paidAttempt, o paidObservationObject, ref ledger.ObjectRef, provenance string) *servicePaidLive {
	if provenance == "" {
		return nil
	}
	live := &servicePaidLive{attempt: a.admission.AttemptID, evidence: ref, provenance: provenance}
	if a.intent != nil && a.result == nil && a.receiptSequence > 1 {
		live.intent = int64(a.receiptSequence - 1)
	}
	if terminal, ok := paidServiceTerminal(a, o); ok {
		live.terminal = terminal
		return live
	}
	failure, ok := providers.DiagnoseServiceFailure(o.ErrorClass, o.HTTPStatus, provenance)
	if !ok {
		return nil
	}
	live.failure = failure
	for _, h := range o.Headers {
		if h.Name == "retry-after" {
			live.retryAfter = append(live.retryAfter, h.Values...)
		}
	}
	return live
}

// paidServiceCause binds a diagnosis to the attempt's sealed request scope.
func paidServiceCause(a paidAttempt, f providers.ServiceFailure) ledger.ServiceCauseSpec {
	spec := ledger.ServiceCauseSpec{Family: "provider", Code: f.Code, ScopeKind: f.ScopeKind, Provider: a.profile.Provider}
	if f.CredentialScope {
		spec.CredentialScopeID = a.admission.Request.Credential.ScopeID
	}
	if f.CredentialVersion {
		spec.CredentialVersion = a.admission.Request.Credential.Version
	}
	if f.Model {
		spec.Model = a.profile.ModelID
	}
	if f.Region {
		spec.Region = new(a.profile.Region)
	}
	if f.Authority {
		spec.AuthoritySHA256 = ledger.Digest(sha256.Sum256([]byte(a.profile.Authority)))
	}
	return spec
}

// sampleServiceLiveLocked keeps a live source's first sample across retries.
func (s *Service) sampleServiceLiveLocked(live *servicePaidLive) error {
	if live.observedUTCNS != 0 {
		return nil
	}
	now, held, err := s.sampleServiceLocked()
	if err != nil {
		return err
	}
	live.observedUTCNS, live.clockHeld = now, held
	return nil
}

// servicePaidObservedLocked builds E for a live paid source at the current
// prefix. Its source_sequence selects the attempt's own dispatch intent.
func (s *Service) servicePaidObservedLocked(ctx context.Context, reads *historyReadSet, live *servicePaidLive) (ledger.RuntimeServiceObserved, bool, error) {
	a, found, err := s.loadPaid(ctx, reads, live.attempt, s.p.through, ledger.Digest{})
	if err != nil || !found || a.intent == nil || live.intent == 0 {
		return ledger.RuntimeServiceObserved{}, false, err
	}
	intent := live.intent
	if err := s.sampleServiceLiveLocked(live); err != nil {
		return ledger.RuntimeServiceObserved{}, false, err
	}
	id := uuid.NewV7()
	if live.completion != nil {
		id = live.completion.ObservationID
	}
	src := ledger.ServiceSource{ObservationID: id, ObservedThrough: int64(s.p.through), SourceSequence: new(intent), Origin: new(a.origin), TurnID: a.admission.ParentTurnID, AttemptID: a.admission.AttemptID, Object: new(live.evidence), ObservedUTCNS: live.observedUTCNS, ClockHeld: live.clockHeld, Provenance: live.provenance}
	e := ledger.RuntimeServiceObserved{ObservationID: id, AgentID: a.origin.AgentID, Source: src}
	if live.completion == nil {
		e.Source.Kind, e.Source.Reason = "paid_call", live.failure.Code
		if reset, ok := providers.StatedServiceReset(live.failure.Code, live.retryAfter, live.observedUTCNS); ok {
			e.Source.ResetUTCNS = &reset
		}
		cause := paidServiceCause(a, live.failure)
		e.Cause = &cause
		return e, true, nil
	}
	e.Source.Kind, e.Source.Reason = "successful_call", "provider_completed"
	e.Source.ObservedThrough = live.completion.ThroughSequence
	e.Source.Completion = &ledger.ServiceCompletion{Provider: a.profile.Provider, Terminal: live.terminal}
	if a.result != nil && a.result.EvidenceObject != nil && *a.result.EvidenceObject == live.evidence && (a.result.Kind == "usage" || a.result.Kind == "unknown") && int64(a.resultSequence) <= e.Source.ObservedThrough {
		e.Source.Completion.NativeResultSequence = new(int64(a.resultSequence))
		e.Source.Completion.FinancialKind = a.result.Kind
	}
	if live.completion.WorksetSHA256 != (ledger.Digest{}) {
		e.Source.Reason, e.Source.WorksetSHA256, e.Source.FailedWorkCount = "all_work_moved", live.completion.WorksetSHA256, new(int64(0))
	}
	e.CompletionPermitsObject = new(live.completionObject)
	return e, true, nil
}

// serviceWorkItem is one configured or durably admitted unit of an agent's
// future paid work. Its compact sorted encoding is the workset digest preimage.
type serviceWorkItem struct {
	Kind              string    `json:"kind"`
	ID                uuid.UUID `json:"id"`
	Provider          string    `json:"provider"`
	CredentialScopeID uuid.UUID `json:"credential_scope_id"`
	CredentialVersion uuid.UUID `json:"credential_version,omitzero"`
	Model             string    `json:"model"`
	Region            string    `json:"region"`
	Authority         string    `json:"authority_sha256"`
}

// serviceWorksetLocked enumerates the enabled loop registry's paid bindings of
// the agent and its admitted but undispatched paid attempts. Without an enabled
// registry no complete workset exists. Lock order: Service.mu -> runtime.mu.
func (s *Service) serviceWorksetLocked(agent uuid.UUID) ([]serviceWorkItem, bool) {
	if s.loop == nil || s.paid == nil {
		return nil, false
	}
	s.loop.mu.Lock()
	contexts := slices.Clone(s.loop.options.Contexts)
	s.loop.mu.Unlock()
	var items []serviceWorkItem
	for _, setting := range contexts {
		if setting.Binding.AgentID != agent {
			continue
		}
		route := s.p.paidRoutes[setting.RouteID]
		item := serviceWorkItem{Kind: "binding", ID: setting.Binding.ContextID, Provider: route.Profile.Provider, CredentialScopeID: setting.CredentialScopeID, Model: route.Profile.ModelID, Region: route.Profile.Region, Authority: serviceAuthority(route.Profile.Authority)}
		var newest int64
		for id, c := range s.p.paidCredentials {
			if c.ScopeID == setting.CredentialScopeID && c.Provider == route.Profile.Provider && c.Version > newest && s.paid.secrets[id] != nil {
				item.CredentialVersion, newest = id, c.Version
			}
		}
		items = append(items, item)
	}
	for id, a := range s.p.paidAttempts {
		if a.origin.AgentID != agent || !a.held || a.intent != nil || a.ended {
			continue
		}
		items = append(items, serviceWorkItem{Kind: "admission", ID: id, Provider: a.profile.Provider, CredentialScopeID: a.admission.Request.Credential.ScopeID, CredentialVersion: a.admission.Request.Credential.Version, Model: a.profile.ModelID, Region: a.profile.Region, Authority: serviceAuthority(a.profile.Authority)})
	}
	slices.SortFunc(items, func(a, b serviceWorkItem) int {
		if a.Kind != b.Kind {
			return bytes.Compare([]byte(a.Kind), []byte(b.Kind))
		}
		return compareServiceIDs(a.ID, b.ID)
	})
	return items, true
}

func serviceAuthority(authority string) string {
	sum := sha256.Sum256([]byte(authority))
	return hex.EncodeToString(sum[:])
}

func serviceWorksetDigest(items []serviceWorkItem) ledger.Digest {
	raw, _ := json.Marshal(items)
	return ledger.Digest(sha256.Sum256(raw))
}

// serviceWorkMatches is the failed-scope test for one work item, with the same
// field rule as servicePaidScopeMatches for an attempt.
func serviceWorkMatches(c ledger.ServiceCauseSpec, item serviceWorkItem) bool {
	return c.Provider == item.Provider &&
		(c.CredentialScopeID == (uuid.UUID{}) || c.CredentialScopeID == item.CredentialScopeID) &&
		(c.CredentialVersion == (uuid.UUID{}) || c.CredentialVersion == item.CredentialVersion) &&
		(c.Model == "" || c.Model == item.Model) &&
		(c.Region == nil || *c.Region == item.Region) &&
		(c.AuthoritySHA256 == (ledger.Digest{}) || hex.EncodeToString(c.AuthoritySHA256[:]) == item.Authority)
}

// serviceCompletionLocked selects one permit for each of the agent's pending or
// lost supported memberships, with checkServicePermit's predicate plus the
// live barrier. It returns nil when no membership can change.
func (s *Service) serviceCompletionLocked(ctx context.Context, reads *historyReadSet, live *servicePaidLive, observation uuid.UUID) (*ServiceCompletionObject, error) {
	a, found, err := s.loadPaid(ctx, reads, live.attempt, s.p.through, ledger.Digest{})
	if err != nil || !found {
		return nil, err
	}
	agent := a.origin.AgentID
	object := &ServiceCompletionObject{Schema: 1, Kind: "service_completion", ObservationID: observation, ThroughSequence: int64(s.p.through), Permits: []ledger.ServiceCompletionPermit{}}
	items, registry := s.serviceWorksetLocked(agent)
	changes := false
	for key, m := range s.p.service.members {
		if key.agent != agent || m.snapshot.Phase != "pending" && m.snapshot.Phase != "lost" {
			continue
		}
		c := s.p.service.causes[key.cause]
		if c.unsupported || m.unsupported || s.p.service.unverifiable(key) {
			continue
		}
		permit := ledger.ServiceCompletionPermit{CauseID: c.id, CauseVersion: c.snapshot.Version, MemberGeneration: m.snapshot.Generation, Kind: "blocked"}
		cleared := c.snapshot.Condition == "cleared"
		switch c.spec.Family {
		case "provider":
			switch {
			case servicePaidScopeMatches(c.spec, a):
				permit.Kind = "same_scope"
			case cleared:
				permit.Kind, permit.ProofSequence = "cleared", new(int64(c.sequence))
			default:
				proof, err := s.serviceCredentialReplacement(ctx, reads, c.spec, a)
				if err != nil {
					return nil, err
				}
				if proof != 0 {
					permit.Kind, permit.ProofSequence = "credential_replaced", new(proof)
				} else if registry && len(items) != 0 && !slices.ContainsFunc(items, func(item serviceWorkItem) bool { return serviceWorkMatches(c.spec, item) }) {
					permit.Kind, permit.FailedWorkCount = "all_work_moved", new(int64(0))
					object.WorksetSHA256 = serviceWorksetDigest(items)
				}
			}
		case "money":
			if cleared {
				available, err := s.serviceMoneyAvailable(ctx, reads, c.spec, live.observedUTCNS)
				if err != nil {
					return nil, err
				}
				if available {
					permit.Kind, permit.ProofSequence = "cleared", new(int64(c.sequence))
				}
			}
		case "execution":
			if cleared && !s.serviceExecutionBarrierLocked(c.spec) {
				permit.Kind, permit.ProofSequence = "execution_clear", new(int64(c.sequence))
			}
		}
		changes = changes || permit.Kind != "blocked"
		object.Permits = append(object.Permits, permit)
	}
	if !changes {
		return nil, nil
	}
	slices.SortFunc(object.Permits, func(a, b ledger.ServiceCompletionPermit) int { return compareServiceIDs(a.CauseID, b.CauseID) })
	return object, nil
}

// serviceCredentialReplacement returns the owner's bind sequence that retired
// the refused version, when this attempt uses that replacement at the same
// scope; zero otherwise.
func (s *Service) serviceCredentialReplacement(ctx context.Context, reads *historyReadSet, c ledger.ServiceCauseSpec, a paidAttempt) (int64, error) {
	version := a.admission.Request.Credential.Version
	if c.CredentialVersion == (uuid.UUID{}) || version == c.CredentialVersion {
		return 0, nil
	}
	bind, ok := s.p.paidCredentials[version]
	repaired := c
	repaired.CredentialVersion = version
	if !ok || bind.Action != "bind" || bind.ScopeID != c.CredentialScopeID || bind.RevokePreviousVersion != c.CredentialVersion || !servicePaidScopeMatches(repaired, a) {
		return 0, nil
	}
	return readHistory(ctx, reads, 0, s.p.through, serviceCredentialBindKey{version}, func(ctx context.Context) (int64, error) {
		var found int64
		err := s.scan(ctx, 0, s.p.through, func(commit ledger.Commit) error {
			for _, r := range commit.Records {
				if r.Type != ledger.TypeRuntimeCredentialChanged || r.Actor != ledger.ActorOwner {
					continue
				}
				changed, err := ledger.DecodeRuntimeCredentialChanged(r.Payload)
				if err != nil {
					return err
				}
				if changed.Action == "bind" && changed.CredentialVersion == version {
					found = int64(r.Seq)
				}
			}
			return nil
		})
		return found, err
	})
}

type serviceCredentialBindKey struct{ Version uuid.UUID }

func (serviceCredentialBindKey) historyKind() historyNeedKind { return needServiceCredential }

func serviceMoneyScope(c ledger.ServiceCauseSpec) ledger.PaidScope {
	switch c.ScopeKind {
	case "agent":
		return ledger.PaidScope{Kind: "agent", ID: c.AgentID}
	case "credential":
		return ledger.PaidScope{Kind: "credential", ID: c.CredentialScopeID}
	}
	return ledger.PaidScope{Kind: "colony"}
}

// serviceMoneyAvailable is the live money barrier: the cause scope's current
// book is available now. A disabled or unconfigured scope is not available.
func (s *Service) serviceMoneyAvailable(ctx context.Context, reads *historyReadSet, c ledger.ServiceCauseSpec, now int64) (bool, error) {
	_, available, err := s.serviceMoneyWitness(ctx, reads, ledger.ServiceMoney{Scope: serviceMoneyScope(c)}, now)
	if isHistoryNeed(err) {
		return false, err
	}
	return err == nil && available, nil
}

// serviceExecutionBarrierLocked is the live execution witness: the named scope
// still has an issue (or no state), or this incarnation classified a workspace
// error unconfirmed.
func (s *Service) serviceExecutionBarrierLocked(c ledger.ServiceCauseSpec) bool {
	switch c.Code {
	case "private_integrity":
		st := s.p.scopes[scopeKey{c.AgentID, c.ContextID}]
		return st == nil || len(st.issues) != 0
	case "process_unconfirmed":
		return s.serviceProcessBlocked
	}
	return true
}

// prepareServiceCompletion takes the permit snapshot under Service.mu after
// earlier evidence drains, then publishes the object outside it under the
// context gate. A nil completion means no membership can change.
func (s *Service) prepareServiceCompletion(ctx context.Context, live *servicePaidLive) error {
	s.mu.Lock()
	if err := s.drainServiceEvidenceLocked(ctx); err != nil && !errors.Is(err, ErrIntegrity) {
		s.retainServiceErrorLocked(err)
	}
	var object *ServiceCompletionObject
	var origin ledger.Origin
	err := s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
		object = nil
		a, found, err := s.loadPaid(ctx, reads, live.attempt, s.p.through, ledger.Digest{})
		if err != nil || !found {
			return err
		}
		origin = a.origin
		// Without a pending or lost member the success can name no permit,
		// so it needs neither a sample nor an object.
		if !s.serviceAgentOpenLocked(a.origin.AgentID) {
			return nil
		}
		if err := s.sampleServiceLiveLocked(live); err != nil {
			return err
		}
		object, err = s.serviceCompletionLocked(ctx, reads, live, uuid.NewV7())
		return err
	})
	s.mu.Unlock()
	live.completion, live.completionObject = nil, ledger.ObjectRef{}
	if err != nil || object == nil {
		return err
	}
	ref, err := s.publishServiceCompletion(ctx, origin, *object)
	if err != nil {
		return err
	}
	live.completion, live.completionObject = object, ref
	return nil
}

// serviceAgentOpenLocked reports whether the agent has a pending or lost
// member that a completion could name.
func (s *Service) serviceAgentOpenLocked(agent uuid.UUID) bool {
	for key, m := range s.p.service.members {
		if key.agent == agent && (m.snapshot.Phase == "pending" || m.snapshot.Phase == "lost") {
			return true
		}
	}
	return false
}

// recordServicePaidLocked drains earlier evidence, revalidates a success
// snapshot, then records this source's E through the locked producer. A stale
// snapshot is never submitted; the caller retains the source to rebuild it.
func (s *Service) recordServicePaidLocked(ctx context.Context, live *servicePaidLive) error {
	if err := s.drainServiceEvidenceLocked(ctx); err != nil && !errors.Is(err, ErrIntegrity) {
		s.retainServiceErrorLocked(err)
		if s.poisoned {
			return err
		}
	}
	if live.terminal != "" {
		if live.completion == nil {
			return errServiceSnapshotStale
		}
		var current *ServiceCompletionObject
		if err := s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
			var err error
			current, err = s.serviceCompletionLocked(ctx, reads, live, live.completion.ObservationID)
			return err
		}); err != nil {
			return err
		}
		if current == nil || current.WorksetSHA256 != live.completion.WorksetSHA256 || !slices.EqualFunc(current.Permits, live.completion.Permits, func(a, b ledger.ServiceCompletionPermit) bool { return sameScopedValue(a, b) }) {
			return errServiceSnapshotStale
		}
	}
	var e ledger.RuntimeServiceObserved
	var ok bool
	if err := s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
		var err error
		e, ok, err = s.servicePaidObservedLocked(ctx, reads, live)
		return err
	}); err != nil || !ok {
		return err
	}
	_, err := s.recordServiceEvidenceLocked(ctx, e.ObservationID, e)
	return err
}

// retainServicePaidLocked keeps a definitely failed live source for the next
// drain opportunity of this incarnation. A planner refusal is final.
func (s *Service) retainServicePaidLocked(live *servicePaidLive, err error) {
	s.retainServiceErrorLocked(err)
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotBound) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNoEffect) {
		return
	}
	retained := *live
	retained.completion, retained.completionObject = nil, ledger.ObjectRef{}
	s.serviceRetained = append(s.serviceRetained, retained)
}

// retryServicePaid retries retained live sources with Service.mu released
// between them, because a success publishes a fresh completion object.
func (s *Service) retryServicePaid(ctx context.Context) {
	s.mu.Lock()
	pending := s.serviceRetained
	s.serviceRetained = nil
	s.mu.Unlock()
	for _, live := range pending {
		var err error
		if live.terminal != "" {
			err = s.prepareServiceCompletion(ctx, &live)
			if err == nil && live.completion == nil {
				continue
			}
		}
		s.mu.Lock()
		if err == nil {
			err = s.recordServicePaidLocked(ctx, &live)
		}
		if err == nil {
			if err = s.drainServiceEvidenceLocked(ctx); err != nil {
				s.retainServiceErrorLocked(err)
			}
		} else if !s.poisoned {
			s.retainServicePaidLocked(&live, err)
		}
		s.mu.Unlock()
	}
}

// drainServiceAfterPaid is the paid path's own drain cycle after the financial
// phase released Service.mu. Its failures are retained status only.
func (s *Service) drainServiceAfterPaid(ctx context.Context) {
	s.mu.Lock()
	if err := s.drainServiceEvidenceLocked(ctx); err != nil {
		s.retainServiceErrorLocked(err)
	}
	s.mu.Unlock()
	s.retryServicePaid(ctx)
}

// recordServiceDemandLocked records one pre-native refusal or host clear with
// the wrapper's order: earlier evidence drains first, this source is recorded,
// then drained. Failures are retained; the caller's native outcome stands.
func (s *Service) recordServiceDemandLocked(ctx context.Context, e ledger.RuntimeServiceObserved) error {
	if err := s.drainServiceEvidenceLocked(ctx); err != nil && !errors.Is(err, ErrIntegrity) {
		s.retainServiceErrorLocked(err)
		if s.poisoned {
			return err
		}
	}
	if _, err := s.recordServiceEvidenceLocked(ctx, e.ObservationID, e); err != nil {
		s.retainServiceErrorLocked(err)
		return err
	}
	err := s.drainServiceEvidenceLocked(ctx)
	if err != nil {
		s.retainServiceErrorLocked(err)
	}
	return err
}

// serviceMoneyRefusalLocked records an exhausted owner money ceiling after the
// planner refused a paid call with budget_exposure, from the book at this
// prefix. Only an enabled finite colony, agent or credential scope with
// occupied >= ceiling counts; a credential scope also needs the enabled
// registry to show all of the agent's paid work on it. One refusal records the
// first such scope, in admission order, whose cause has no open member for
// this agent (PM fix-1 answer 1). Nothing is reserved, charged or released,
// and every other refusal writes nothing.
func (s *Service) serviceMoneyRefusalLocked(ctx context.Context, refusal error, origin ledger.Origin, turn uuid.UUID, admission ledger.RuntimePaidReserved, command uuid.UUID, sub ledger.PaidSubmission) {
	var busy *BusyError
	if !errors.As(refusal, &busy) || busy.Reason != "budget_exposure" || !validID(turn) {
		return
	}
	// The hook runs on every refusal, including each wake of a parked turn.
	// Without a candidate scope it reads no history and samples no clock.
	var candidates []serviceMoneyCandidate
	for _, scope := range admission.Scopes {
		if c, ok := s.serviceMoneyCandidateLocked(origin.AgentID, admission, scope); ok {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return
	}
	digest, err := paidDigest(sub)
	if err != nil {
		return
	}
	var e ledger.RuntimeServiceObserved
	found := false
	err = s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
		found = false
		now, held, err := s.sampleServiceLocked()
		if err != nil {
			return err
		}
		for _, c := range candidates {
			witness, available, err := s.serviceMoneyWitness(ctx, reads, c.first, now)
			if isHistoryNeed(err) {
				return err
			}
			if err != nil || available {
				continue
			}
			id := uuid.NewV7()
			src := ledger.ServiceSource{Kind: "money_refusal", ObservationID: id, ObservedThrough: int64(s.p.through), CommandID: command, SubmissionSHA256: digest, Origin: new(origin), TurnID: turn, ObservedUTCNS: now, ClockHeld: held, Reason: "money_exhausted", ResetUTCNS: serviceMoneyReset(c.period, now), Money: &witness}
			e, found = ledger.RuntimeServiceObserved{ObservationID: id, AgentID: origin.AgentID, Cause: new(c.cause), Source: src}, true
			return nil
		}
		return nil
	})
	if err != nil {
		s.retainServiceErrorLocked(err)
		return
	}
	if found {
		_ = s.recordServiceDemandLocked(ctx, e)
	}
}

// serviceMoneyCandidate is one scope the refusal hook may record: its cause,
// the witness's coverage fields and the scope's period kind.
type serviceMoneyCandidate struct {
	cause  ledger.ServiceCauseSpec
	first  ledger.ServiceMoney
	period string
}

// serviceMoneyCandidateLocked is the in-memory test of one admission scope
// before any witness: an enabled finite colony, agent or credential scope,
// exhausted in its book's current period, whose cause has no pending or lost
// member for the agent (a repeat appends nothing), and, for a credential
// scope, with the enabled registry showing all of the agent's work on it. A
// book whose period has since rolled over settled nothing in the new period,
// so its new occupancy is only the carried hold and cannot exceed this one.
func (s *Service) serviceMoneyCandidateLocked(agent uuid.UUID, admission ledger.RuntimePaidReserved, scope ledger.PaidScope) (serviceMoneyCandidate, bool) {
	a := s.p.book.paid[scope]
	if scope.Kind != "colony" && scope.Kind != "agent" && scope.Kind != "credential" || a.version == 0 || !a.terms.Enabled || a.terms.Mode != "finite" || a.terms.CeilingMicros == nil {
		return serviceMoneyCandidate{}, false
	}
	if paidOccupied(a, a.period).Cmp(big.NewInt(*a.terms.CeilingMicros)) < 0 {
		return serviceMoneyCandidate{}, false
	}
	c := serviceMoneyCandidate{cause: ledger.ServiceCauseSpec{Family: "money", Code: "owner_money_exhausted", ScopeKind: scope.Kind}, first: ledger.ServiceMoney{Scope: scope, Coverage: "mandatory_scope"}, period: a.terms.PeriodKind}
	switch scope.Kind {
	case "agent":
		c.cause.AgentID = scope.ID
	case "credential":
		c.cause.Provider, c.cause.CredentialScopeID = admission.Request.Provider, scope.ID
	}
	if key, err := c.cause.Key(); err == nil {
		if id, ok := s.p.service.keys[key]; ok {
			if phase := s.p.service.members[serviceMemberKey{agent, id}].snapshot.Phase; phase == "pending" || phase == "lost" {
				return serviceMoneyCandidate{}, false
			}
		}
	}
	if scope.Kind == "credential" {
		items, registry := s.serviceWorksetLocked(agent)
		failed := 0
		for _, item := range items {
			if item.CredentialScopeID == scope.ID {
				failed++
			}
		}
		if !registry || len(items) == 0 || failed != len(items) {
			return serviceMoneyCandidate{}, false
		}
		c.first.Coverage, c.first.WorksetSHA256 = "complete_workset", serviceWorksetDigest(items)
		c.first.EligibleWorkCount, c.first.FailedWorkCount = new(int64(len(items))), new(int64(failed))
	}
	return c, true
}

// serviceMoneyReset is the next UTC period boundary; a lifetime ceiling has none.
func serviceMoneyReset(kind string, now int64) *int64 {
	at := time.Unix(0, now).UTC()
	switch kind {
	case "utc_day":
		return new(time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, time.UTC).UnixNano())
	case "utc_month":
		return new(time.Date(at.Year(), at.Month()+1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	}
	return nil
}

// serviceAdmissionClearLocked writes budget_available for every active money
// cause whose scope this committed admission names and whose book, just before
// the admission's group, was available (PM S2 answer 11). The coverage fields
// restate the activating refusal's witness; the book alone decides.
func (s *Service) serviceAdmissionClearLocked(ctx context.Context, origin ledger.Origin, admission ledger.RuntimePaidReserved, sequence uint64) {
	if sequence < 2 {
		return
	}
	var causes []serviceCause
	for _, c := range s.p.service.causes {
		if !c.unsupported && c.spec.Code == "owner_money_exhausted" && c.snapshot.Condition == "active" && c.snapshot.Source.Money != nil && slices.Contains(admission.Scopes, serviceMoneyScope(c.spec)) {
			causes = append(causes, c)
		}
	}
	slices.SortFunc(causes, func(a, b serviceCause) int { return compareServiceIDs(a.id, b.id) })
	before := sequence - 2
	for _, c := range causes {
		var e ledger.RuntimeServiceObserved
		found := false
		err := s.prepareHistoryLocked(ctx, &historyReadSet{}, nil, true, func(reads *historyReadSet) error {
			found = false
			scope := serviceMoneyScope(c.spec)
			config, err := s.serviceSpendAt(ctx, reads, scope, before)
			if err != nil {
				return err
			}
			if config.Sequence == 0 || !config.Terms.Enabled {
				return nil
			}
			settled, held, _, err := s.paidHistoryTotal(ctx, reads, scope, admission.AdmissionPeriod, nil, before)
			if err != nil {
				return err
			}
			occupied := new(big.Int).Add(settled, held)
			w := *c.snapshot.Source.Money
			w.Period, w.SpendSequence, w.Mode, w.OccupiedMicros, w.CeilingMicros = admission.AdmissionPeriod, config.Sequence, config.Terms.Mode, occupied.String(), nil
			if config.Terms.Mode == "finite" {
				if config.Terms.CeilingMicros == nil || occupied.Cmp(big.NewInt(*config.Terms.CeilingMicros)) >= 0 {
					return nil
				}
				w.CeilingMicros = new(strconv.FormatInt(*config.Terms.CeilingMicros, 10))
			}
			now, clockHeld, err := s.sampleServiceLocked()
			if err != nil {
				return err
			}
			id := uuid.NewV7()
			src := ledger.ServiceSource{Kind: "cause_clear", ObservationID: id, ObservedThrough: int64(s.p.through), SourceSequence: new(int64(sequence)), Origin: new(origin), TurnID: admission.ParentTurnID, ObservedUTCNS: max(now, admission.AdmissionUTCNS), ClockHeld: clockHeld, Reason: "budget_available", Money: &w}
			e, found = ledger.RuntimeServiceObserved{ObservationID: id, AgentID: origin.AgentID, CauseID: c.id, Source: src}, true
			return nil
		})
		if err != nil {
			s.retainServiceErrorLocked(err)
			return
		}
		if found {
			_ = s.recordServiceDemandLocked(ctx, e)
		}
	}
}

// serviceIntegrityCause maps the guard's recorded issue to its frozen cause:
// agent_id, private_scope, the context for a context scope, and the revision.
// An issue that cannot form a valid cause establishes nothing.
func serviceIntegrityCause(issue IntegrityIssue) (ledger.ServiceCauseSpec, bool) {
	if issue.Revision == 0 || issue.Revision > math.MaxInt64 || issue.Scope != "agent" && issue.Scope != "context" {
		return ledger.ServiceCauseSpec{}, false
	}
	spec := ledger.ServiceCauseSpec{Family: "execution", Code: "private_integrity", ScopeKind: "private", AgentID: issue.AgentID, PrivateScope: issue.Scope, Revision: new(int64(issue.Revision))}
	if issue.Scope == "context" {
		spec.ContextID = issue.ContextID
	}
	if _, err := spec.Key(); err != nil {
		return ledger.ServiceCauseSpec{}, false
	}
	return spec, true
}

// serviceIntegrityRefusalLocked records integrity demand for one refused call
// of an existing turn identity. src supplies its identity members.
func (s *Service) serviceIntegrityRefusalLocked(ctx context.Context, issue IntegrityIssue, src ledger.ServiceSource) {
	cause, ok := serviceIntegrityCause(issue)
	if !ok || src.Origin == nil || cause.AgentID != src.Origin.AgentID || cause.ContextID != (uuid.UUID{}) && cause.ContextID != src.Origin.ContextID {
		return
	}
	now, held, err := s.sampleServiceLocked()
	if err != nil {
		s.retainServiceErrorLocked(err)
		return
	}
	id := uuid.NewV7()
	src.Kind, src.ObservationID, src.ObservedThrough, src.ObservedUTCNS, src.ClockHeld, src.Reason = "integrity_refusal", id, int64(s.p.through), now, held, "private_integrity"
	_ = s.recordServiceDemandLocked(ctx, ledger.RuntimeServiceObserved{ObservationID: id, AgentID: src.Origin.AgentID, Cause: &cause, Source: src})
}

// serviceScopedIntegrityLocked classifies a scoped refusal before agentError
// flattens it. turn.start is the refused turn's own admission; a private read
// or write counts only as the running loop turn's own tool effect.
func (s *Service) serviceScopedIntegrityLocked(ctx context.Context, input scopedInput, refusal error) {
	var issue IntegrityIssue
	if !errors.As(refusal, &issue) || input.sub.Actor != "agent" {
		return
	}
	var turn uuid.UUID
	switch input.sub.Operation {
	case "turn.start":
		if args, ok := input.sub.Arguments.(ledger.TurnStartArguments); ok {
			turn = args.TurnID
		}
	case "private.read", "private.write":
		turn = s.serviceEffectTurnLocked(input.sub.Origin, input.id)
	}
	if !validID(turn) {
		return
	}
	s.serviceIntegrityRefusalLocked(ctx, issue, ledger.ServiceSource{Origin: new(input.sub.Origin), TurnID: turn, CommandID: input.id, SubmissionSHA256: input.digest})
}

// serviceEffectTurnLocked names the running loop turn whose tool invocation
// owns this effect command. Lock order: Service.mu -> runtime.mu.
func (s *Service) serviceEffectTurnLocked(origin ledger.Origin, command uuid.UUID) uuid.UUID {
	if s.loop == nil {
		return uuid.UUID{}
	}
	s.loop.mu.Lock()
	defer s.loop.mu.Unlock()
	for _, t := range s.loop.turns {
		if t.origin != origin || t.binding == nil {
			continue
		}
		for _, inv := range t.tools {
			if inv.effectID == command {
				return t.id
			}
		}
	}
	return uuid.UUID{}
}

// serviceProcessRefusal records process demand for a bound live turn whose
// workspace call returned the unconfirmed class, before anything transforms
// the error. It returns nothing: the native stop, state and tool result stand.
func (r *loopRuntime) serviceProcessRefusal(t *loopTurn, err error) {
	blocked, run := workspace.ClassifyExecutionFailure(err)
	if !blocked {
		return
	}
	s := r.service
	s.mu.Lock()
	defer s.mu.Unlock()
	// Where the Owner proves no tree closure no process cause can exist or
	// clear, so the native refusal stands alone with no barrier (D138;
	// clarification answer 1(d)). Otherwise the barrier holds for the rest of
	// the incarnation, also for an unbound turn or a repeat refusal.
	if !r.workspace.ProcessProofSupported() {
		return
	}
	s.serviceProcessBlocked = true
	if t.binding == nil {
		return
	}
	now, held, sampleErr := s.sampleServiceLocked()
	if sampleErr != nil {
		s.retainServiceErrorLocked(sampleErr)
		return
	}
	id := uuid.NewV7()
	cause := ledger.ServiceCauseSpec{Family: "execution", Code: "process_unconfirmed", ScopeKind: "colony"}
	src := ledger.ServiceSource{Kind: "process_refusal", ObservationID: id, ObservedThrough: int64(s.p.through), SourceSequence: new(int64(t.binding.first + 1)), Origin: new(t.origin), TurnID: t.id, ObservedUTCNS: now, ClockHeld: held, Reason: "process_unconfirmed", ExecutionRunID: run}
	_ = s.recordServiceDemandLocked(context.Background(), ledger.RuntimeServiceObserved{ObservationID: id, AgentID: t.origin.AgentID, Cause: &cause, Source: src})
}

// ServiceHostReports carries the host's existing startup reports to the one
// explicit reports reconcile after an unrefused start. Its presence enables
// the integrity_verified and process_closed clears; nil means drain only.
type ServiceHostReports struct {
	ProcessClosures []workspace.ProcessClosure
}

// serviceHostClearsLocked writes one clear for each active cause the reports
// prove: a private_integrity cause whose named scope exists in this
// incarnation without issues, and the process cause from the first closure.
// A clear never restores; it only permits a later call to.
func (s *Service) serviceHostClearsLocked(ctx context.Context, reports ServiceHostReports) error {
	var causes []serviceCause
	for _, c := range s.p.service.causes {
		if !c.unsupported && c.snapshot.Condition == "active" && (c.spec.Code == "private_integrity" || c.spec.Code == "process_unconfirmed") {
			causes = append(causes, c)
		}
	}
	slices.SortFunc(causes, func(a, b serviceCause) int { return compareServiceIDs(a.id, b.id) })
	var failures error
	for _, c := range causes {
		c = s.p.service.causes[c.id]
		if c.unsupported || c.snapshot.Condition != "active" {
			continue
		}
		src := ledger.ServiceSource{Kind: "cause_clear"}
		agent := c.spec.AgentID
		switch c.spec.Code {
		case "private_integrity":
			if s.serviceExecutionBarrierLocked(c.spec) {
				continue
			}
			src.Reason = "integrity_verified"
		case "process_unconfirmed":
			// A refusal this incarnation already classified postdates the
			// Open-time closures, so they no longer prove closure.
			if len(reports.ProcessClosures) == 0 || s.serviceProcessBlocked {
				continue
			}
			first := reports.ProcessClosures[0]
			src.Reason, src.ExecutionProof = "process_closed", first.Proof
			if validID(first.RunID) {
				src.ExecutionRunID = first.RunID
			}
			// A colony cause has no owner; attribute it to its smallest member
			// agent, as the timer's shared clear does.
			agent = uuid.UUID{}
			for key := range s.p.service.members {
				if key.cause == c.id && (agent == (uuid.UUID{}) || compareServiceIDs(key.agent, agent) < 0) {
					agent = key.agent
				}
			}
		}
		if !validID(agent) {
			continue
		}
		now, held, err := s.sampleServiceLocked()
		if err != nil {
			return err
		}
		id := uuid.NewV7()
		src.ObservationID, src.ObservedThrough, src.ObservedUTCNS, src.ClockHeld = id, int64(s.p.through), now, held
		if err := s.recordServiceDemandLocked(ctx, ledger.RuntimeServiceObserved{ObservationID: id, AgentID: agent, CauseID: c.id, Source: src}); err != nil {
			failures = errors.Join(failures, err)
			if s.poisoned {
				return failures
			}
		}
	}
	return failures
}

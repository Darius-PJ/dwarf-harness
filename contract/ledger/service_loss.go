package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"uuid"
)

// F3-91's five version-1 types are proposed for freeze (D125).
const (
	TypeRuntimeServiceObserved     = "runtime.service_observed"
	TypeRuntimeServiceCauseChanged = "runtime.service_cause_changed"
	TypeRuntimeServiceAgentChanged = "runtime.service_agent_changed"
	TypeRuntimeServiceOutageNotice = "runtime.service_outage_notice"
	TypeRuntimeServiceReceipt      = "runtime.service_receipt"

	// MaxServicePayloadBytes independently bounds complete inputs and outputs.
	MaxServicePayloadBytes = 64 << 10
	// ServiceGraceNS is the first refusal's absolute not-before offset.
	ServiceGraceNS int64 = 120_000_000_000
)

func encodeServicePayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxServicePayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxServicePayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeServicePayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxServicePayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxServicePayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

// ServiceCauseSpec identifies a semantic cause, not an incident or authority.
// Region is a pointer because route scopes require even the empty region.
type ServiceCauseSpec struct {
	Family            string    `json:"family"`
	Code              string    `json:"code"`
	ScopeKind         string    `json:"scope_kind"`
	Provider          string    `json:"provider,omitempty"`
	CredentialScopeID uuid.UUID `json:"credential_scope_id,omitzero"`
	CredentialVersion uuid.UUID `json:"credential_version,omitzero"`
	Model             string    `json:"model,omitempty"`
	Region            *string   `json:"region,omitempty"`
	AuthoritySHA256   Digest    `json:"authority_sha256,omitzero"`
	AgentID           uuid.UUID `json:"agent_id,omitzero"`
	ContextID         uuid.UUID `json:"context_id,omitzero"`
	PrivateScope      string    `json:"private_scope,omitempty"`
	Revision          *int64    `json:"revision,omitempty"`
}

func (p ServiceCauseSpec) check() error {
	if !scopedToken(p.Family) || !scopedToken(p.Code) || !scopedToken(p.ScopeKind) || p.Provider != "" && !scopedToken(p.Provider) || p.Model != "" && !paidASCII(p.Model, 128) || p.Region != nil && *p.Region != "" && !scopedToken(*p.Region) || p.PrivateScope != "" && !scopedToken(p.PrivateScope) || p.Revision != nil && *p.Revision <= 0 {
		return errors.New("invalid service cause value")
	}
	for _, id := range []uuid.UUID{p.CredentialScopeID, p.CredentialVersion, p.AgentID, p.ContextID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("invalid service cause identity")
		}
	}
	// Unknown vocabulary survives as unsupported. Known combinations have one
	// exact shape; optional fields cannot widen a known cause's scope.
	if !slices.Contains([]string{"provider", "money", "execution"}, p.Family) || !slices.Contains([]string{"credential_denied", "scoped_denied", "quota_exhausted", "rate_limited", "overloaded", "unreachable", "owner_money_exhausted", "private_integrity", "process_unconfirmed"}, p.Code) || !slices.Contains([]string{"provider_route", "endpoint", "credential", "account", "account_route", "colony", "agent", "private"}, p.ScopeKind) {
		return nil
	}
	var need []string
	valid := false
	switch p.Code {
	case "credential_denied":
		valid = p.Family == "provider" && p.ScopeKind == "credential"
		need = []string{"provider", "credential_scope_id", "credential_version"}
	case "scoped_denied", "rate_limited":
		valid = p.Family == "provider" && p.ScopeKind == "account_route"
		need = []string{"provider", "credential_scope_id", "model", "region", "authority_sha256"}
		if p.Code == "scoped_denied" {
			need = append(need, "credential_version")
		}
	case "quota_exhausted":
		valid = p.Family == "provider" && p.ScopeKind == "account"
		need = []string{"provider", "credential_scope_id", "authority_sha256"}
	case "overloaded":
		valid = p.Family == "provider" && p.ScopeKind == "provider_route"
		need = []string{"provider", "model", "region", "authority_sha256"}
	case "unreachable":
		valid = p.Family == "provider" && p.ScopeKind == "endpoint"
		need = []string{"provider", "authority_sha256"}
	case "owner_money_exhausted":
		valid = p.Family == "money" && slices.Contains([]string{"colony", "agent", "credential"}, p.ScopeKind)
		if p.ScopeKind == "agent" {
			need = []string{"agent_id"}
		}
		if p.ScopeKind == "credential" {
			need = []string{"provider", "credential_scope_id"}
		}
	case "private_integrity":
		valid = p.Family == "execution" && p.ScopeKind == "private"
		need = []string{"agent_id", "private_scope", "revision"}
		if p.PrivateScope == "context" {
			need = append(need, "context_id")
		}
		if p.PrivateScope != "agent" && p.PrivateScope != "context" && scopedToken(p.PrivateScope) {
			return nil
		}
	case "process_unconfirmed":
		valid = p.Family == "execution" && p.ScopeKind == "colony"
	}
	if !valid {
		return errors.New("service cause family/code/scope mismatch")
	}
	for key, present := range map[string]bool{
		"provider": p.Provider != "", "credential_scope_id": p.CredentialScopeID != (uuid.UUID{}), "credential_version": p.CredentialVersion != (uuid.UUID{}),
		"model": p.Model != "", "region": p.Region != nil, "authority_sha256": p.AuthoritySHA256 != (Digest{}),
		"agent_id": p.AgentID != (uuid.UUID{}), "context_id": p.ContextID != (uuid.UUID{}), "private_scope": p.PrivateScope != "", "revision": p.Revision != nil,
	} {
		if present != slices.Contains(need, key) {
			return fmt.Errorf("service cause %s presence mismatch", key)
		}
	}
	return nil
}

func (p ServiceCauseSpec) Encode() (json.RawMessage, error) {
	return encodeServicePayload("service cause", p, p.check())
}

// Key hashes only the canonical known-field encoding, never unknown extensions.
func (p ServiceCauseSpec) Key() (Digest, error) {
	js, err := p.Encode()
	if err != nil {
		return Digest{}, err
	}
	return Digest(sha256.Sum256(js)), nil
}

func DecodeServiceCauseSpec(js json.RawMessage) (ServiceCauseSpec, error) {
	var p ServiceCauseSpec
	err := decodeServicePayload("service cause", js, func(m *members) error { p = readServiceCause(m); return p.check() })
	if err != nil {
		return ServiceCauseSpec{}, err
	}
	return p, nil
}

func readServiceCause(m *members) ServiceCauseSpec {
	p := ServiceCauseSpec{Family: m.text("family"), Code: m.text("code"), ScopeKind: m.text("scope_kind")}
	p.Provider, _ = m.optionalText("provider")
	p.CredentialScopeID, _ = m.optionalID("credential_scope_id")
	p.CredentialVersion, _ = m.optionalID("credential_version")
	p.Model, _ = m.optionalText("model")
	if _, ok := m.get("region", false); ok {
		p.Region = new(m.text("region"))
	}
	p.AuthoritySHA256, _ = m.optionalDigest("authority_sha256")
	p.AgentID, _ = m.optionalID("agent_id")
	p.ContextID, _ = m.optionalID("context_id")
	p.PrivateScope, _ = m.optionalText("private_scope")
	p.Revision = optionalScopedNat(m, "revision")
	return p
}

// ServiceMoney is a nonfinancial witness of the existing book and work coverage.
// OccupiedMicros may exceed MaxInt64; the complete payload cap is its only bound.
type ServiceMoney struct {
	Scope             PaidScope `json:"scope"`
	Period            string    `json:"period"`
	SpendSequence     int64     `json:"spend_sequence"`
	Mode              string    `json:"mode"`
	OccupiedMicros    string    `json:"occupied_micros"`
	CeilingMicros     *string   `json:"ceiling_micros,omitempty"`
	Coverage          string    `json:"coverage"`
	WorksetSHA256     Digest    `json:"workset_sha256,omitzero"`
	EligibleWorkCount *int64    `json:"eligible_work_count,omitempty"`
	FailedWorkCount   *int64    `json:"failed_work_count,omitempty"`
}

func serviceDecimal(s string) bool {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (p ServiceMoney) check(refusal bool) error {
	if !validPaidScope(p.Scope) || !slices.Contains([]string{"colony", "agent", "credential"}, p.Scope.Kind) || !validPaidPeriod(p.Period) || p.SpendSequence <= 0 || !slices.Contains([]string{"finite", "unlimited"}, p.Mode) || !serviceDecimal(p.OccupiedMicros) || !scopedToken(p.Coverage) || p.CeilingMicros != nil && !paidDecimal(*p.CeilingMicros, false) || p.EligibleWorkCount != nil && *p.EligibleWorkCount < 0 || p.FailedWorkCount != nil && *p.FailedWorkCount < 0 {
		return errors.New("invalid service money witness")
	}
	if p.Mode == "finite" && p.CeilingMicros == nil || p.Mode == "unlimited" && p.CeilingMicros != nil {
		return errors.New("service money ceiling presence")
	}
	if p.Coverage == "mandatory_scope" || p.Coverage == "complete_workset" {
		complete := p.Coverage == "complete_workset"
		if complete != (p.Scope.Kind == "credential") || complete != (p.WorksetSHA256 != (Digest{})) || complete != (p.EligibleWorkCount != nil) || complete != (p.FailedWorkCount != nil) {
			return errors.New("service money coverage mismatch")
		}
		if complete && *p.FailedWorkCount > *p.EligibleWorkCount {
			return errors.New("service money coverage count")
		}
	}
	if refusal {
		if p.Mode == "unlimited" {
			return errors.New("unlimited money refusal")
		}
		if p.Mode == "finite" && (len(p.OccupiedMicros) < len(*p.CeilingMicros) || len(p.OccupiedMicros) == len(*p.CeilingMicros) && p.OccupiedMicros < *p.CeilingMicros) {
			return errors.New("money refusal without exhaustion")
		}
		if p.Coverage == "complete_workset" && (*p.EligibleWorkCount == 0 || *p.EligibleWorkCount != *p.FailedWorkCount) {
			return errors.New("money refusal lacks all-work coverage")
		}
	}
	return nil
}

func readServiceMoney(m *members) ServiceMoney {
	p := ServiceMoney{Scope: graphObject(m, "scope", readPaidScope), Period: m.text("period"), SpendSequence: m.nat("spend_sequence", true), Mode: m.text("mode"), OccupiedMicros: m.text("occupied_micros"), Coverage: m.text("coverage"), EligibleWorkCount: optionalScopedNat(m, "eligible_work_count"), FailedWorkCount: optionalScopedNat(m, "failed_work_count")}
	if v, ok := m.optionalText("ceiling_micros"); ok {
		p.CeilingMicros = &v
	}
	p.WorksetSHA256, _ = m.optionalDigest("workset_sha256")
	return p
}

// ServiceCompletionPermit is one cause-selected service_completion object entry.
// It authorizes no financial transition or execution.
type ServiceCompletionPermit struct {
	CauseID          uuid.UUID `json:"cause_id"`
	CauseVersion     int64     `json:"cause_version"`
	MemberGeneration int64     `json:"member_generation"`
	Kind             string    `json:"kind"`
	ProofSequence    *int64    `json:"proof_sequence,omitempty"`
	FailedWorkCount  *int64    `json:"failed_work_count,omitempty"`
}

func (p ServiceCompletionPermit) check() error {
	if !isV7(p.CauseID) || p.CauseVersion <= 0 || p.MemberGeneration <= 0 || !scopedToken(p.Kind) || p.ProofSequence != nil && *p.ProofSequence <= 0 || p.FailedWorkCount != nil && *p.FailedWorkCount < 0 {
		return errors.New("invalid service completion permit")
	}
	switch p.Kind {
	case "same_scope", "blocked":
		if p.ProofSequence != nil || p.FailedWorkCount != nil {
			return errors.New("unexpected service permit proof")
		}
	case "cleared", "credential_replaced", "execution_clear":
		if p.ProofSequence == nil || p.FailedWorkCount != nil {
			return errors.New("missing service permit proof")
		}
	case "all_work_moved":
		if p.FailedWorkCount == nil || *p.FailedWorkCount != 0 {
			return errors.New("incomplete moved work")
		}
	}
	return nil
}

func readServicePermit(m *members) ServiceCompletionPermit {
	return ServiceCompletionPermit{m.id("cause_id"), m.nat("cause_version", true), m.nat("member_generation", true), m.text("kind"), optionalScopedNat(m, "proof_sequence"), optionalScopedNat(m, "failed_work_count")}
}

// ServiceCompletion records the nonfinancial terminal, independently of billing.
// Permit is absent in E, and selected from E's object for each state operation.
type ServiceCompletion struct {
	Provider             string                   `json:"provider"`
	Terminal             string                   `json:"terminal"`
	NativeResultSequence *int64                   `json:"native_result_sequence,omitempty"`
	FinancialKind        string                   `json:"financial_kind,omitempty"`
	Permit               *ServiceCompletionPermit `json:"permit,omitempty"`
}

func (p ServiceCompletion) check(selected bool) error {
	if !scopedToken(p.Provider) || !scopedToken(p.Terminal) || (p.NativeResultSequence != nil) != (p.FinancialKind != "") || p.NativeResultSequence != nil && *p.NativeResultSequence <= 0 || p.FinancialKind != "" && !scopedToken(p.FinancialKind) || (p.Permit != nil) != selected {
		return errors.New("invalid service completion shape")
	}
	if p.Permit != nil {
		if err := p.Permit.check(); err != nil {
			return err
		}
	}
	if p.FinancialKind != "" && slices.Contains([]string{"no_charge", "released_unsent", "correction"}, p.FinancialKind) {
		return errors.New("noncompletion financial result")
	}
	// Known unsuccessful terminals cannot become restoration evidence. Unknown
	// tokens are retained unsupported, not silently interpreted as success.
	if slices.Contains([]string{"refusal", "incomplete", "failed", "cancelled"}, p.Terminal) {
		return errors.New("unsuccessful service terminal")
	}
	if p.Provider == "anthropic" && p.Terminal == "completed" || p.Provider == "openai" && slices.Contains([]string{"end_turn", "max_tokens", "stop_sequence", "tool_use", "model_context_window_exceeded"}, p.Terminal) {
		return errors.New("service terminal provider mismatch")
	}
	return nil
}

func readServiceCompletion(m *members) ServiceCompletion {
	p := ServiceCompletion{Provider: m.text("provider"), Terminal: m.text("terminal"), NativeResultSequence: optionalScopedNat(m, "native_result_sequence"), Permit: optionalObject(m, "permit", readServicePermit)}
	p.FinancialKind, _ = m.optionalText("financial_kind")
	return p
}

// ServiceSource is host-observed metadata. Sequence references select actual
// prior effects; observation_id names the durable journal source, not a retry.
type ServiceSource struct {
	Kind             string             `json:"kind"`
	ObservationID    uuid.UUID          `json:"observation_id"`
	ObservedThrough  int64              `json:"observed_through"`
	SourceSequence   *int64             `json:"source_sequence,omitempty"`
	CommandID        uuid.UUID          `json:"command_id,omitzero"`
	SubmissionSHA256 Digest             `json:"submission_sha256,omitzero"`
	Origin           *Origin            `json:"origin,omitempty"`
	TurnID           uuid.UUID          `json:"turn_id,omitzero"`
	AttemptID        uuid.UUID          `json:"attempt_id,omitzero"`
	Object           *ObjectRef         `json:"object,omitempty"`
	ObservedUTCNS    int64              `json:"observed_utc_ns"`
	ClockHeld        bool               `json:"clock_held"`
	Reason           string             `json:"reason"`
	Provenance       string             `json:"provenance,omitempty"`
	ResetUTCNS       *int64             `json:"reset_utc_ns,omitempty"`
	Money            *ServiceMoney      `json:"money,omitempty"`
	Completion       *ServiceCompletion `json:"completion,omitempty"`
	WorksetSHA256    Digest             `json:"workset_sha256,omitzero"`
	FailedWorkCount  *int64             `json:"failed_work_count,omitempty"`
	ExecutionRunID   uuid.UUID          `json:"execution_run_id,omitzero"`
	ExecutionProof   string             `json:"execution_proof,omitempty"`
}

func serviceFailureKind(kind string) bool {
	return slices.Contains([]string{"paid_call", "money_refusal", "integrity_refusal", "process_refusal"}, kind)
}

func (p ServiceSource) check(selected bool) error {
	if !scopedToken(p.Kind) || !isV7(p.ObservationID) || p.ObservedThrough < 0 || p.ObservedUTCNS <= 0 || !scopedToken(p.Reason) || p.Provenance != "" && !scopedToken(p.Provenance) || p.ExecutionProof != "" && !scopedToken(p.ExecutionProof) || p.SourceSequence != nil && (*p.SourceSequence <= 0 || *p.SourceSequence > p.ObservedThrough) || p.ResetUTCNS != nil && *p.ResetUTCNS <= p.ObservedUTCNS || p.FailedWorkCount != nil && *p.FailedWorkCount < 0 {
		return errors.New("invalid service source")
	}
	for _, id := range []uuid.UUID{p.CommandID, p.TurnID, p.AttemptID, p.ExecutionRunID} {
		if id != (uuid.UUID{}) && !isV7(id) {
			return errors.New("invalid service source identity")
		}
	}
	if (p.CommandID != (uuid.UUID{})) != (p.SubmissionSHA256 != (Digest{})) || p.Origin != nil && !validOrigin(*p.Origin) || p.Object != nil && !validPaidObject(*p.Object) || (p.WorksetSHA256 != (Digest{})) != (p.FailedWorkCount != nil) {
		return errors.New("invalid service source binding")
	}
	if p.Money != nil {
		if err := p.Money.check(p.Kind == "money_refusal"); err != nil {
			return err
		}
		if p.Money.SpendSequence > p.ObservedThrough {
			return errors.New("future spend witness")
		}
	}
	if p.Completion != nil {
		if err := p.Completion.check(selected); err != nil {
			return err
		}
		if p.Completion.NativeResultSequence != nil && *p.Completion.NativeResultSequence > p.ObservedThrough || p.Completion.Permit != nil && p.Completion.Permit.ProofSequence != nil && *p.Completion.Permit.ProofSequence > p.ObservedThrough {
			return errors.New("future completion proof")
		}
	}
	known := slices.Contains([]string{"paid_call", "money_refusal", "integrity_refusal", "process_refusal", "cause_clear", "successful_call", "grace_expiry", "demand_withdrawn"}, p.Kind)
	if !known {
		return nil
	}
	paid := p.Kind == "paid_call" || p.Kind == "successful_call"
	if paid != (p.Provenance != "") || paid != (p.AttemptID != (uuid.UUID{})) || paid != (p.Object != nil) || (p.Kind == "successful_call") != (p.Completion != nil) || (p.Kind == "money_refusal" || p.Reason == "budget_available") != (p.Money != nil) || (p.Reason == "process_closed") != (p.ExecutionProof != "") {
		return errors.New("service source member presence")
	}
	if paid && (p.SourceSequence == nil || p.Origin == nil || !isV7(p.TurnID)) {
		return errors.New("paid source lacks native binding")
	}
	if serviceFailureKind(p.Kind) && (p.Origin == nil || !isV7(p.TurnID) || p.SourceSequence == nil && p.CommandID == (uuid.UUID{})) {
		return errors.New("refusal lacks demand identity")
	}
	if p.ExecutionRunID != (uuid.UUID{}) && p.Kind != "process_refusal" && p.Reason != "process_closed" {
		return errors.New("unrelated execution run")
	}
	if p.WorksetSHA256 != (Digest{}) && p.Reason != "all_work_moved" {
		return errors.New("unrelated moved-work evidence")
	}
	if p.Reason == "all_work_moved" && (p.Kind != "successful_call" || p.WorksetSHA256 == (Digest{}) || *p.FailedWorkCount != 0) {
		return errors.New("incomplete moved-work source")
	}
	if p.ResetUTCNS != nil && !(p.Kind == "paid_call" && slices.Contains([]string{"quota_exhausted", "rate_limited"}, p.Reason) || p.Kind == "money_refusal" && p.Money.Period != "lifetime") {
		return errors.New("unrelated reset evidence")
	}
	if p.Kind == "money_refusal" && (p.ResetUTCNS != nil) != (p.Money.Period != "lifetime") {
		return errors.New("money period reset presence")
	}
	return nil
}

func readServiceSource(m *members) ServiceSource {
	p := ServiceSource{Kind: m.text("kind"), ObservationID: m.id("observation_id"), ObservedThrough: m.nat("observed_through", true), SourceSequence: optionalScopedNat(m, "source_sequence"), Origin: optionalObject(m, "origin", readOrigin), Object: optionalObject(m, "object", readObject), ObservedUTCNS: m.nat("observed_utc_ns", true), ClockHeld: m.boolean("clock_held"), Reason: m.text("reason"), ResetUTCNS: optionalScopedNat(m, "reset_utc_ns"), Money: optionalObject(m, "money", readServiceMoney), Completion: optionalObject(m, "completion", readServiceCompletion), FailedWorkCount: optionalScopedNat(m, "failed_work_count")}
	p.CommandID, _ = m.optionalID("command_id")
	p.SubmissionSHA256, _ = m.optionalDigest("submission_sha256")
	p.TurnID, _ = m.optionalID("turn_id")
	p.AttemptID, _ = m.optionalID("attempt_id")
	p.Provenance, _ = m.optionalText("provenance")
	p.WorksetSHA256, _ = m.optionalDigest("workset_sha256")
	p.ExecutionRunID, _ = m.optionalID("execution_run_id")
	p.ExecutionProof, _ = m.optionalText("execution_proof")
	return p
}

// RuntimeServiceObserved durably selects facts before any service transition.
type RuntimeServiceObserved struct {
	ObservationID           uuid.UUID         `json:"observation_id"`
	AgentID                 uuid.UUID         `json:"agent_id"`
	CauseID                 uuid.UUID         `json:"cause_id,omitzero"`
	CauseKey                Digest            `json:"cause_key,omitzero"`
	Cause                   *ServiceCauseSpec `json:"cause,omitempty"`
	Source                  ServiceSource     `json:"source"`
	CompletionPermitsObject *ObjectRef        `json:"completion_permits_object,omitempty"`
}

func (p RuntimeServiceObserved) check() error {
	if !isV7(p.ObservationID) || !isV7(p.AgentID) || p.Source.ObservationID != p.ObservationID || p.Source.Origin != nil && p.Source.Origin.AgentID != p.AgentID || p.CauseID != (uuid.UUID{}) && !isV7(p.CauseID) || p.CompletionPermitsObject != nil && !validPaidObject(*p.CompletionPermitsObject) {
		return errors.New("invalid service observation binding")
	}
	if err := p.Source.check(false); err != nil {
		return err
	}
	if p.Cause != nil {
		key, err := p.Cause.Key()
		if err != nil {
			return err
		}
		if p.CauseKey != key {
			return errors.New("service observation cause key mismatch")
		}
	} else if p.CauseKey != (Digest{}) {
		return errors.New("service observation key without cause")
	}
	switch {
	case serviceFailureKind(p.Source.Kind):
		if !isV7(p.CauseID) || p.Cause == nil || p.CompletionPermitsObject != nil {
			return errors.New("service failure cause presence")
		}
	case p.Source.Kind == "successful_call":
		if p.CauseID != (uuid.UUID{}) || p.Cause != nil || p.CompletionPermitsObject == nil {
			return errors.New("service success cause/permit presence")
		}
	case p.Source.Kind == "cause_clear" || p.Source.Kind == "demand_withdrawn":
		if !isV7(p.CauseID) || p.Cause != nil || p.CompletionPermitsObject != nil {
			return errors.New("service clear/withdraw cause presence")
		}
	case p.Source.Kind == "grace_expiry":
		return errors.New("timer source is not a journal observation")
	}
	return nil
}

func (p RuntimeServiceObserved) Encode() (json.RawMessage, error) {
	return encodeServicePayload(TypeRuntimeServiceObserved, p, p.check())
}
func DecodeRuntimeServiceObserved(js json.RawMessage) (RuntimeServiceObserved, error) {
	var p RuntimeServiceObserved
	err := decodeServicePayload(TypeRuntimeServiceObserved, js, func(m *members) error {
		p = RuntimeServiceObserved{ObservationID: m.id("observation_id"), AgentID: m.id("agent_id"), Cause: optionalObject(m, "cause", readServiceCause), Source: graphObject(m, "source", readServiceSource), CompletionPermitsObject: optionalObject(m, "completion_permits_object", readObject)}
		p.CauseID, _ = m.optionalID("cause_id")
		p.CauseKey, _ = m.optionalDigest("cause_key")
		return p.check()
	})
	if err != nil {
		return RuntimeServiceObserved{}, err
	}
	return p, nil
}

// RuntimeServiceCauseChanged changes the shared condition, never membership.
type RuntimeServiceCauseChanged struct {
	CauseID         uuid.UUID        `json:"cause_id"`
	CauseKey        Digest           `json:"cause_key"`
	ExpectedVersion int64            `json:"expected_version"`
	Version         int64            `json:"version"`
	Cause           ServiceCauseSpec `json:"cause"`
	Condition       string           `json:"condition"`
	Source          ServiceSource    `json:"source"`
}

func (p RuntimeServiceCauseChanged) check() error {
	if !isV7(p.CauseID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || !scopedToken(p.Condition) || p.ExpectedVersion == 0 && p.Condition == "cleared" {
		return errors.New("invalid service cause change")
	}
	key, err := p.Cause.Key()
	if err != nil {
		return err
	}
	if key != p.CauseKey {
		return errors.New("service cause key mismatch")
	}
	if err := p.Source.check(true); err != nil {
		return err
	}
	if p.Source.Completion != nil && p.Source.Completion.Permit.CauseID != p.CauseID {
		return errors.New("foreign cause completion permit")
	}
	return nil
}
func (p RuntimeServiceCauseChanged) Encode() (json.RawMessage, error) {
	return encodeServicePayload(TypeRuntimeServiceCauseChanged, p, p.check())
}
func DecodeRuntimeServiceCauseChanged(js json.RawMessage) (RuntimeServiceCauseChanged, error) {
	var p RuntimeServiceCauseChanged
	err := decodeServicePayload(TypeRuntimeServiceCauseChanged, js, func(m *members) error {
		p = RuntimeServiceCauseChanged{m.id("cause_id"), m.digest("cause_key"), m.nat("expected_version", true), m.nat("version", true), graphObject(m, "cause", readServiceCause), m.text("condition"), graphObject(m, "source", readServiceSource)}
		return p.check()
	})
	if err != nil {
		return RuntimeServiceCauseChanged{}, err
	}
	return p, nil
}

// RuntimeServiceAgentChanged is one agent/cause generation's complete snapshot.
type RuntimeServiceAgentChanged struct {
	AgentID         uuid.UUID     `json:"agent_id"`
	CauseID         uuid.UUID     `json:"cause_id"`
	ExpectedVersion int64         `json:"expected_version"`
	Version         int64         `json:"version"`
	Generation      int64         `json:"generation"`
	Phase           string        `json:"phase"`
	FirstSource     ServiceSource `json:"first_source"`
	DueUTCNS        *int64        `json:"due_utc_ns,omitempty"`
	OutageID        uuid.UUID     `json:"outage_id,omitzero"`
	Source          ServiceSource `json:"source"`
}

func serviceDue(first ServiceSource, due *int64) bool {
	if due != nil && (first.ObservedUTCNS > math.MaxInt64-ServiceGraceNS || *due != first.ObservedUTCNS+ServiceGraceNS) {
		return false
	}
	switch first.Reason {
	case "rate_limited", "overloaded", "unreachable":
		return due != nil
	case "quota_exhausted", "money_exhausted":
		return (due != nil) == (first.ResetUTCNS != nil)
	case "credential_denied", "scoped_denied", "private_integrity", "process_unconfirmed":
		return due == nil
	}
	return true
}

func (p RuntimeServiceAgentChanged) check() error {
	if !isV7(p.AgentID) || !isV7(p.CauseID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || p.Generation <= 0 || p.ExpectedVersion == 0 && p.Generation != 1 || !scopedToken(p.Phase) || p.OutageID != (uuid.UUID{}) && !isV7(p.OutageID) || !serviceDue(p.FirstSource, p.DueUTCNS) {
		return errors.New("invalid service member snapshot")
	}
	if err := p.FirstSource.check(false); err != nil {
		return err
	}
	if slices.Contains([]string{"cause_clear", "successful_call", "grace_expiry", "demand_withdrawn"}, p.FirstSource.Kind) {
		return errors.New("first source is not a refusal")
	}
	if err := p.Source.check(true); err != nil {
		return err
	}
	for _, source := range []ServiceSource{p.FirstSource, p.Source} {
		if source.Origin != nil && source.Origin.AgentID != p.AgentID {
			return errors.New("foreign service member source")
		}
	}
	if p.Source.Completion != nil && (p.Source.Completion.Permit.CauseID != p.CauseID || p.Source.Completion.Permit.MemberGeneration != p.Generation) {
		return errors.New("foreign service member permit")
	}
	switch p.Phase {
	case "pending", "withdrawn":
		if p.DueUTCNS == nil || p.OutageID != (uuid.UUID{}) {
			return errors.New("pending/withdrawn incident shape")
		}
	case "lost", "restored":
		if !isV7(p.OutageID) {
			return errors.New("lost/restored member lacks incident")
		}
	}
	return nil
}
func (p RuntimeServiceAgentChanged) Encode() (json.RawMessage, error) {
	return encodeServicePayload(TypeRuntimeServiceAgentChanged, p, p.check())
}
func DecodeRuntimeServiceAgentChanged(js json.RawMessage) (RuntimeServiceAgentChanged, error) {
	var p RuntimeServiceAgentChanged
	err := decodeServicePayload(TypeRuntimeServiceAgentChanged, js, func(m *members) error {
		p = RuntimeServiceAgentChanged{AgentID: m.id("agent_id"), CauseID: m.id("cause_id"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Generation: m.nat("generation", true), Phase: m.text("phase"), FirstSource: graphObject(m, "first_source", readServiceSource), DueUTCNS: optionalScopedNat(m, "due_utc_ns"), Source: graphObject(m, "source", readServiceSource)}
		p.OutageID, _ = m.optionalID("outage_id")
		return p.check()
	})
	if err != nil {
		return RuntimeServiceAgentChanged{}, err
	}
	return p, nil
}

// RuntimeServiceOutageNotice refers to the incident's cumulative membership.
// MemberSourceSequence names the opening/closing M envelope in this commit.
type RuntimeServiceOutageNotice struct {
	OutageID             uuid.UUID `json:"outage_id"`
	CauseID              uuid.UUID `json:"cause_id"`
	Edge                 string    `json:"edge"`
	NoticeID             uuid.UUID `json:"notice_id"`
	MemberSourceSequence int64     `json:"member_source_sequence"`
}

func (p RuntimeServiceOutageNotice) check() error {
	if !isV7(p.OutageID) || !isV7(p.CauseID) || !scopedToken(p.Edge) || !isV7(p.NoticeID) || p.MemberSourceSequence <= 0 {
		return errors.New("invalid service notice")
	}
	return nil
}
func (p RuntimeServiceOutageNotice) Encode() (json.RawMessage, error) {
	return encodeServicePayload(TypeRuntimeServiceOutageNotice, p, p.check())
}
func DecodeRuntimeServiceOutageNotice(js json.RawMessage) (RuntimeServiceOutageNotice, error) {
	var p RuntimeServiceOutageNotice
	err := decodeServicePayload(TypeRuntimeServiceOutageNotice, js, func(m *members) error {
		p = RuntimeServiceOutageNotice{m.id("outage_id"), m.id("cause_id"), m.text("edge"), m.id("notice_id"), m.nat("member_source_sequence", true)}
		return p.check()
	})
	if err != nil {
		return RuntimeServiceOutageNotice{}, err
	}
	return p, nil
}

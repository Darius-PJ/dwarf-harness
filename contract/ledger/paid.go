package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// F3-13's version-1 record types are frozen (D148).
const (
	TypeRuntimePaidRouteConfigured     = "runtime.paid_route_configured"
	TypeRuntimeCredentialChanged       = "runtime.credential_changed"
	TypeRuntimeSpendConfigured         = "runtime.spend_configured"
	TypeRuntimePaidReserved            = "runtime.paid_reserved"
	TypeRuntimePaidDispatchIntent      = "runtime.paid_dispatch_intent"
	TypeRuntimePaidResult              = "runtime.paid_result"
	TypeRuntimePaidOverrunAcknowledged = "runtime.paid_overrun_acknowledged"
	TypeRuntimePaidReceipt             = "runtime.paid_receipt"
)

type RuntimePaidRouteConfigured struct {
	RouteID           uuid.UUID `json:"route_id"`
	ExpectedVersion   int64     `json:"expected_version"`
	Version           int64     `json:"version"`
	Enabled           bool      `json:"enabled"`
	CredentialScopeID uuid.UUID `json:"credential_scope_id"`
	Profile           Profile   `json:"profile"`
	ObservedUTCNS     int64     `json:"observed_utc_ns,omitempty"`
}

func (p RuntimePaidRouteConfigured) check() error {
	if !isV7(p.RouteID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || !isV7(p.CredentialScopeID) || p.Profile.check() != nil || p.Enabled && (p.ObservedUTCNS < p.Profile.VerifiedUTCNS || p.ObservedUTCNS >= p.Profile.ExpiresUTCNS) || !p.Enabled && p.ObservedUTCNS != 0 {
		return errors.New("invalid paid route configuration")
	}
	return nil
}
func (p RuntimePaidRouteConfigured) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimePaidRouteConfigured, p, p.check())
}
func DecodeRuntimePaidRouteConfigured(js json.RawMessage) (RuntimePaidRouteConfigured, error) {
	var p RuntimePaidRouteConfigured
	err := decodePaidPayload(TypeRuntimePaidRouteConfigured, js, func(m *members) error {
		p = RuntimePaidRouteConfigured{RouteID: m.id("route_id"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Enabled: m.boolean("enabled"), CredentialScopeID: m.id("credential_scope_id"), Profile: graphObject(m, "profile", readPaidProfile)}
		if p.Enabled {
			p.ObservedUTCNS = m.nat("observed_utc_ns", true)
		} else {
			forbidScoped(m, "observed_utc_ns")
		}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimePaidRouteConfigured{}, err
	}
	return p, nil
}

// RuntimeCredentialChanged carries only the nonsecret source locator.
// SourceKind is open; an unfamiliar store cannot authorize dispatch.
type RuntimeCredentialChanged struct {
	ScopeID               uuid.UUID `json:"scope_id"`
	Provider              string    `json:"provider"`
	ExpectedVersion       int64     `json:"expected_version"`
	Version               int64     `json:"version"`
	Action                string    `json:"action"`
	CredentialVersion     uuid.UUID `json:"credential_version"`
	SourceKind            string    `json:"source_kind,omitempty"`
	SourceLocator         string    `json:"source_locator,omitempty"`
	RevokePreviousVersion uuid.UUID `json:"revoke_previous_version,omitzero"`
}

func (p RuntimeCredentialChanged) check() error {
	if !isV7(p.ScopeID) || !scopedToken(p.Provider) || !nextScopedVersion(p.ExpectedVersion, p.Version) || !isV7(p.CredentialVersion) {
		return errors.New("invalid credential change")
	}
	switch p.Action {
	case "bind":
		if !scopedToken(p.SourceKind) || !paidASCII(p.SourceLocator, 256) || p.RevokePreviousVersion != (uuid.UUID{}) && (!isV7(p.RevokePreviousVersion) || p.RevokePreviousVersion == p.CredentialVersion) {
			return errors.New("invalid credential binding")
		}
	case "revoke":
		if p.SourceKind != "" || p.SourceLocator != "" || p.RevokePreviousVersion != (uuid.UUID{}) {
			return errors.New("credential revocation has binding members")
		}
	default:
		return errors.New("invalid credential action")
	}
	return nil
}
func (p RuntimeCredentialChanged) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimeCredentialChanged, p, p.check())
}
func DecodeRuntimeCredentialChanged(js json.RawMessage) (RuntimeCredentialChanged, error) {
	var p RuntimeCredentialChanged
	err := decodePaidPayload(TypeRuntimeCredentialChanged, js, func(m *members) error {
		p = readCredentialChanged(m)
		p.Version = m.nat("version", true)
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeCredentialChanged{}, err
	}
	return p, nil
}
func readCredentialChanged(m *members) RuntimeCredentialChanged {
	p := RuntimeCredentialChanged{ScopeID: m.id("scope_id"), Provider: m.text("provider"), ExpectedVersion: m.nat("expected_version", true), Action: m.text("action"), CredentialVersion: m.id("credential_version")}
	if p.Action == "bind" {
		p.SourceKind, p.SourceLocator = m.text("source_kind"), m.text("source_locator")
		p.RevokePreviousVersion, _ = m.optionalID("revoke_previous_version")
	} else {
		forbidScoped(m, "source_kind", "source_locator", "revoke_previous_version")
	}
	return p
}

type RuntimeSpendConfigured struct {
	Scope            PaidScope  `json:"scope"`
	ExpectedVersion  int64      `json:"expected_version"`
	Version          int64      `json:"version"`
	Terms            SpendTerms `json:"terms"`
	ParentGrantID    uuid.UUID  `json:"parent_grant_id,omitzero"`
	AuthorityVersion int64      `json:"authority_version"`
}

func (p RuntimeSpendConfigured) check() error {
	if !validPaidScope(p.Scope) || !nextScopedVersion(p.ExpectedVersion, p.Version) || p.Terms.check() != nil || p.AuthorityVersion < 0 || p.ParentGrantID != (uuid.UUID{}) && (p.Scope.Kind != "grant" || !isV7(p.ParentGrantID) || p.ParentGrantID == p.Scope.ID) {
		return errors.New("invalid spend configuration")
	}
	return nil
}
func (p RuntimeSpendConfigured) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimeSpendConfigured, p, p.check())
}
func DecodeRuntimeSpendConfigured(js json.RawMessage) (RuntimeSpendConfigured, error) {
	var p RuntimeSpendConfigured
	err := decodePaidPayload(TypeRuntimeSpendConfigured, js, func(m *members) error {
		p = RuntimeSpendConfigured{Scope: graphObject(m, "scope", readPaidScope), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Terms: graphObject(m, "terms", readSpendTerms), AuthorityVersion: m.nat("authority_version", true)}
		p.ParentGrantID, _ = m.optionalID("parent_grant_id")
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeSpendConfigured{}, err
	}
	return p, nil
}

// RuntimePaidReserved installs the same full finite hold in every original scope.
type RuntimePaidReserved struct {
	AttemptID        uuid.UUID        `json:"attempt_id"`
	Version          int64            `json:"version"`
	AuthorityVersion int64            `json:"authority_version"`
	GrantID          uuid.UUID        `json:"grant_id"`
	ParentTurnID     uuid.UUID        `json:"parent_turn_id"`
	TaskBinding      *TaskBinding     `json:"task_binding,omitempty"`
	RouteID          uuid.UUID        `json:"route_id"`
	RouteVersion     int64            `json:"route_version"`
	ProfileSHA256    Digest           `json:"profile_sha256"`
	Request          SealedDescriptor `json:"request"`
	Quote            CostBound        `json:"quote"`
	AdmissionUTCNS   int64            `json:"admission_utc_ns"`
	AdmissionPeriod  string           `json:"admission_period"`
	Scopes           []PaidScope      `json:"scopes"`
	InputObject      ObjectRef        `json:"input_object"`
}

func (p RuntimePaidReserved) check() error {
	if !isV7(p.AttemptID) || p.Version != 1 || p.AuthorityVersion < 0 || !isV7(p.GrantID) || !isV7(p.ParentTurnID) || !validTaskBinding(p.TaskBinding) || !isV7(p.RouteID) || p.RouteVersion < 1 || p.ProfileSHA256 == (Digest{}) || p.Request.check() != nil || p.Quote.check() != nil || p.AdmissionUTCNS <= 0 || !validPaidPeriod(p.AdmissionPeriod) || !validPaidScopes(p.Scopes) || !validPaidObject(p.InputObject) || p.InputObject.SHA256 != p.Request.BodySHA256 || p.InputObject.Bytes != p.Request.BodyBytes {
		return errors.New("invalid paid reservation")
	}
	return nil
}
func validPaidPeriod(s string) bool {
	if s == "lifetime" {
		return true
	}
	for _, layout := range []string{"2006-01-02", "2006-01"} {
		if t, err := time.Parse(layout, s); err == nil && t.Format(layout) == s {
			return true
		}
	}
	return false
}
func (p RuntimePaidReserved) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimePaidReserved, p, p.check())
}
func DecodeRuntimePaidReserved(js json.RawMessage) (RuntimePaidReserved, error) {
	var p RuntimePaidReserved
	err := decodePaidPayload(TypeRuntimePaidReserved, js, func(m *members) error {
		p = RuntimePaidReserved{AttemptID: m.id("attempt_id"), Version: m.nat("version", true), AuthorityVersion: m.nat("authority_version", true), GrantID: m.id("grant_id"), ParentTurnID: m.id("parent_turn_id"), TaskBinding: optionalObject(m, "task_binding", readTaskBinding), RouteID: m.id("route_id"), RouteVersion: m.nat("route_version", true), ProfileSHA256: m.digest("profile_sha256"), Request: graphObject(m, "request", readSealedDescriptor), Quote: graphObject(m, "quote", readCostBound), AdmissionUTCNS: m.nat("admission_utc_ns", true), AdmissionPeriod: m.text("admission_period"), Scopes: readScopedObjects(m, "scopes", readPaidScope), InputObject: graphObject(m, "input_object", readObject)}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimePaidReserved{}, err
	}
	return p, nil
}

// RuntimePaidDispatchIntent means SENT, not provider acceptance or success.
type RuntimePaidDispatchIntent struct {
	AttemptID        uuid.UUID     `json:"attempt_id"`
	ExpectedVersion  int64         `json:"expected_version"`
	Version          int64         `json:"version"`
	AuthorityVersion int64         `json:"authority_version"`
	RequestSHA256    Digest        `json:"request_sha256"`
	QuoteSHA256      Digest        `json:"quote_sha256"`
	Credential       CredentialRef `json:"credential"`
	ObservedUTCNS    int64         `json:"observed_utc_ns"`
}

func (p RuntimePaidDispatchIntent) check() error {
	if !isV7(p.AttemptID) || p.ExpectedVersion != 1 || p.Version != 2 || p.AuthorityVersion < 0 || p.RequestSHA256 == (Digest{}) || p.QuoteSHA256 == (Digest{}) || !validCredentialRef(p.Credential) || p.ObservedUTCNS <= 0 {
		return errors.New("invalid paid dispatch intent")
	}
	return nil
}
func (p RuntimePaidDispatchIntent) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimePaidDispatchIntent, p, p.check())
}
func DecodeRuntimePaidDispatchIntent(js json.RawMessage) (RuntimePaidDispatchIntent, error) {
	var p RuntimePaidDispatchIntent
	err := decodePaidPayload(TypeRuntimePaidDispatchIntent, js, func(m *members) error {
		p = RuntimePaidDispatchIntent{m.id("attempt_id"), m.nat("expected_version", true), m.nat("version", true), m.nat("authority_version", true), m.digest("request_sha256"), m.digest("quote_sha256"), graphObject(m, "credential", readCredentialRef), m.nat("observed_utc_ns", true)}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimePaidDispatchIntent{}, err
	}
	return p, nil
}

// RuntimePaidResult preserves financial evidence independently of model success.
// Raw provider metadata lives only in the referenced private observation object.
type RuntimePaidResult struct {
	AttemptID              uuid.UUID         `json:"attempt_id"`
	ExpectedVersion        int64             `json:"expected_version"`
	Version                int64             `json:"version"`
	Kind                   string            `json:"kind"`
	OutputStarted          *bool             `json:"output_started,omitempty"`
	ProviderRequestID      string            `json:"provider_request_id,omitempty"`
	ProviderMessageID      string            `json:"provider_message_id,omitempty"`
	EvidenceSHA256         Digest            `json:"evidence_sha256,omitzero"`
	EvidenceObject         *ObjectRef        `json:"evidence_object,omitempty"`
	MessageObjects         []ObjectRef       `json:"message_objects,omitempty"`
	Usage                  *PaidUsage        `json:"usage,omitempty"`
	NoCharge               *NoChargeEvidence `json:"no_charge,omitempty"`
	CostMicros             *int64            `json:"cost_micros,omitempty"`
	BoundViolation         bool              `json:"bound_violation"`
	ErrorClass             string            `json:"error_class,omitempty"`
	PreviousResultSequence int64             `json:"previous_result_sequence,omitempty"`
	PreviousEvidenceSHA256 Digest            `json:"previous_evidence_sha256,omitzero"`
}

func paidProviderID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func validPaidMessages(objects []ObjectRef) bool {
	if objects == nil {
		return true
	}
	if len(objects) == 0 {
		return false
	}
	seen := make(map[Digest]bool)
	for _, o := range objects {
		if !validPaidObject(o) || seen[o.SHA256] {
			return false
		}
		seen[o.SHA256] = true
	}
	return true
}
func (p RuntimePaidResult) check() error {
	if !isV7(p.AttemptID) || p.ExpectedVersion < 1 || !nextScopedVersion(p.ExpectedVersion, p.Version) || p.ProviderRequestID != "" && !paidProviderID(p.ProviderRequestID) || p.ProviderMessageID != "" && !paidProviderID(p.ProviderMessageID) || p.ErrorClass != "" && !scopedToken(p.ErrorClass) || !validPaidMessages(p.MessageObjects) {
		return errors.New("invalid paid result")
	}
	if p.Kind == "released_unsent" {
		if p.ExpectedVersion != 1 || p.OutputStarted != nil || p.ProviderRequestID != "" || p.ProviderMessageID != "" || p.EvidenceSHA256 != (Digest{}) || p.EvidenceObject != nil || p.MessageObjects != nil || p.Usage != nil || p.NoCharge != nil || p.CostMicros != nil || p.BoundViolation || p.ErrorClass != "" || p.PreviousResultSequence != 0 || p.PreviousEvidenceSHA256 != (Digest{}) {
			return errors.New("release contains sent evidence")
		}
		return nil
	}
	if p.ExpectedVersion < 2 || p.OutputStarted == nil || p.EvidenceObject == nil || !validPaidObject(*p.EvidenceObject) || p.EvidenceSHA256 != p.EvidenceObject.SHA256 {
		return errors.New("missing paid evidence")
	}
	if p.Kind == "correction" {
		if p.PreviousResultSequence <= 0 || p.PreviousEvidenceSHA256 == (Digest{}) {
			return errors.New("missing previous settlement")
		}
	} else if p.PreviousResultSequence != 0 || p.PreviousEvidenceSHA256 != (Digest{}) {
		return errors.New("previous settlement outside correction")
	}
	switch p.Kind {
	case "unknown":
		if p.Usage != nil || p.NoCharge != nil || p.CostMicros != nil || p.MessageObjects != nil || p.ErrorClass == "" {
			return errors.New("unknown result has financial members")
		}
		return nil
	case "usage":
		if p.Usage == nil || p.NoCharge != nil {
			return errors.New("usage result lacks exclusive usage")
		}
	case "no_charge":
		if p.NoCharge == nil || p.Usage != nil {
			return errors.New("no-charge result lacks exclusive evidence")
		}
	case "correction":
		if (p.Usage == nil) == (p.NoCharge == nil) {
			return errors.New("correction lacks exclusive financial evidence")
		}
	default:
		return errors.New("invalid paid result kind")
	}
	if p.CostMicros == nil || *p.CostMicros < 0 || p.ProviderMessageID == "" {
		return errors.New("missing settlement amount or message")
	}
	if p.Usage != nil && p.Usage.check() != nil {
		return errors.New("invalid settlement usage")
	}
	if p.NoCharge != nil && (p.NoCharge.check() != nil || *p.CostMicros != 0 || *p.OutputStarted || p.MessageObjects != nil || p.BoundViolation) {
		return errors.New("invalid no-charge settlement")
	}
	return nil
}
func (p RuntimePaidResult) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimePaidResult, p, p.check())
}
func DecodeRuntimePaidResult(js json.RawMessage) (RuntimePaidResult, error) {
	var p RuntimePaidResult
	err := decodePaidPayload(TypeRuntimePaidResult, js, func(m *members) error {
		p = RuntimePaidResult{AttemptID: m.id("attempt_id"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Kind: m.text("kind"), EvidenceObject: optionalObject(m, "evidence_object", readObject), Usage: optionalObject(m, "usage", readPaidUsage), NoCharge: optionalObject(m, "no_charge", readNoChargeEvidence), CostMicros: optionalScopedNat(m, "cost_micros"), BoundViolation: m.boolean("bound_violation")}
		if _, ok := m.get("output_started", false); ok {
			b := m.boolean("output_started")
			p.OutputStarted = &b
		}
		p.ProviderRequestID, _ = m.optionalText("provider_request_id")
		p.ProviderMessageID, _ = m.optionalText("provider_message_id")
		p.ErrorClass, _ = m.optionalText("error_class")
		p.EvidenceSHA256, _ = m.optionalDigest("evidence_sha256")
		p.PreviousEvidenceSHA256, _ = m.optionalDigest("previous_evidence_sha256")
		p.PreviousResultSequence, _ = m.optionalNat("previous_result_sequence")
		if _, ok := m.get("message_objects", false); ok {
			p.MessageObjects = readScopedObjects(m, "message_objects", readObject)
		}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimePaidResult{}, err
	}
	return p, nil
}

// RuntimePaidOverrunAcknowledged retires a profile without refunding its charge.
type RuntimePaidOverrunAcknowledged struct {
	AttemptID            uuid.UUID `json:"attempt_id"`
	ExpectedVersion      int64     `json:"expected_version"`
	Version              int64     `json:"version"`
	ResultSequence       int64     `json:"result_sequence"`
	EvidenceSHA256       Digest    `json:"evidence_sha256"`
	RetiredProfileSHA256 Digest    `json:"retired_profile_sha256"`
}

func (p RuntimePaidOverrunAcknowledged) check() error {
	if !isV7(p.AttemptID) || p.ExpectedVersion < 2 || !nextScopedVersion(p.ExpectedVersion, p.Version) || p.ResultSequence <= 0 || p.EvidenceSHA256 == (Digest{}) || p.RetiredProfileSHA256 == (Digest{}) {
		return errors.New("invalid paid overrun acknowledgement")
	}
	return nil
}
func (p RuntimePaidOverrunAcknowledged) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimePaidOverrunAcknowledged, p, p.check())
}
func DecodeRuntimePaidOverrunAcknowledged(js json.RawMessage) (RuntimePaidOverrunAcknowledged, error) {
	var p RuntimePaidOverrunAcknowledged
	err := decodePaidPayload(TypeRuntimePaidOverrunAcknowledged, js, func(m *members) error {
		p = RuntimePaidOverrunAcknowledged{m.id("attempt_id"), m.nat("expected_version", true), m.nat("version", true), m.nat("result_sequence", true), m.digest("evidence_sha256"), m.digest("retired_profile_sha256")}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimePaidOverrunAcknowledged{}, err
	}
	return p, nil
}

// RuntimePaidReceipt binds an immutable result to its schema-4 submission.
type RuntimePaidReceipt struct {
	CommandID        uuid.UUID       `json:"command_id"`
	SubmissionSHA256 Digest          `json:"submission_sha256"`
	Submission       PaidSubmission  `json:"submission"`
	Outcome          string          `json:"outcome"`
	Version          int64           `json:"version"`
	AttemptID        uuid.UUID       `json:"attempt_id,omitzero"`
	Usage            *PaidLocalUsage `json:"usage,omitempty"`
}

func (p RuntimePaidReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) || p.Version <= 0 || (paidAttemptOperation(p.Submission.Operation)) != (p.AttemptID != (uuid.UUID{})) || p.AttemptID != (uuid.UUID{}) && !isV7(p.AttemptID) || (p.Submission.Operation == "paid.spend.delegate") != (p.Usage != nil) {
		return errors.New("invalid paid receipt")
	}
	if u := p.Usage; u != nil && (u.AuthorityVersion < 0 || !isV7(u.GrantID) || u.GrantID != p.Submission.GrantID || !validScopes(u.Scopes) || u.Units != 1 || u.Bytes != 0) {
		return errors.New("invalid paid local usage")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("paid submission digest mismatch")
	}
	return nil
}
func (p RuntimePaidReceipt) Encode() (json.RawMessage, error) {
	return encodePaidPayload(TypeRuntimePaidReceipt, p, p.check())
}
func DecodeRuntimePaidReceipt(js json.RawMessage) (RuntimePaidReceipt, error) {
	var p RuntimePaidReceipt
	err := decodePaidPayload(TypeRuntimePaidReceipt, js, func(m *members) error {
		p = RuntimePaidReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Submission: graphObject(m, "submission", readPaidSubmission), Outcome: m.text("outcome"), Version: m.nat("version", true), Usage: optionalObject(m, "usage", readPaidLocalUsage)}
		p.AttemptID, _ = m.optionalID("attempt_id")
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimePaidReceipt{}, err
	}
	return p, nil
}

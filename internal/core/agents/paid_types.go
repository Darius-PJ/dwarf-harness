package agents

import (
	"errors"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

var (
	ErrPaidClock  = errors.New("agents: paid clock unavailable or regressed")
	ErrPaidSent   = errors.New("agents: paid attempt already intended")
	ErrPaidPeriod = errors.New("agents: colony spend period required or immutable")
)

// PaidLimits bound live producers and owned memory, never historical truth.
type PaidLimits struct {
	MaxPaidRoutes, MaxCredentialScopes, MaxCredentialVersions   int
	MaxPaidEnrolledScopes, MaxPreparedPaid, MaxUnresolvedPaid   int
	MaxPaidHotResults, MaxPaidScopes, MaxProfileBytes           int
	MaxRequestBytes                                             int64
	MaxActivePaid                                               int
	DispatchTimeout, TLSHandshakeTimeout, ResponseHeaderTimeout time.Duration
}

func DefaultPaidLimits() PaidLimits {
	return PaidLimits{
		MaxPaidRoutes: 16, MaxCredentialScopes: 16, MaxCredentialVersions: 64,
		MaxPaidEnrolledScopes: 256, MaxPreparedPaid: 16, MaxUnresolvedPaid: 256,
		MaxPaidHotResults: 128, MaxPaidScopes: 16, MaxProfileBytes: 32 << 10,
		MaxRequestBytes: 512 << 10, MaxActivePaid: 3, DispatchTimeout: 120 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}
}
func (l PaidLimits) validate() error {
	for _, n := range []int{l.MaxPaidRoutes, l.MaxCredentialScopes, l.MaxCredentialVersions, l.MaxPaidEnrolledScopes, l.MaxPreparedPaid, l.MaxUnresolvedPaid, l.MaxPaidHotResults, l.MaxPaidScopes, l.MaxProfileBytes} {
		if n <= 0 {
			return ErrInvalid
		}
	}
	if l.MaxProfileBytes > providers.ProfileLimit || l.MaxRequestBytes <= 0 || l.MaxRequestBytes > 512<<10 {
		return ErrInvalid
	}
	if l.MaxActivePaid <= 0 || l.DispatchTimeout <= 0 || l.TLSHandshakeTimeout <= 0 || l.ResponseHeaderTimeout <= 0 {
		return ErrInvalid
	}
	return nil
}

type PaidOptions struct{ Limits PaidLimits }

type PaidRouteConfiguration struct {
	CommandID, GrantID, RouteID uuid.UUID
	ExpectedVersion             int64
	Enabled                     bool
	CredentialScopeID           uuid.UUID
	// Profile is strict nonsecret installation JSON, including on disable.
	Profile []byte
}
type CredentialBinding struct {
	CommandID, GrantID, ScopeID, CredentialVersion uuid.UUID
	ExpectedVersion                                int64
	Provider, SourceKind, SourceLocator            string
	RevokePreviousVersion                          uuid.UUID
}
type CredentialRevocation struct {
	CommandID, GrantID, ScopeID, CredentialVersion uuid.UUID
	ExpectedVersion                                int64
	Provider                                       string
}
type SpendTermsInput struct {
	Enabled               bool
	Mode, USD, PeriodKind string
	WarningPPM            *int64
}
type SpendConfiguration struct {
	CommandID, GrantID uuid.UUID
	Scope              ledger.PaidScope
	ExpectedVersion    int64
	Terms              SpendTermsInput
	ParentGrantID      uuid.UUID
}
type SpendDelegation struct {
	CommandID, GrantID, ChildGrantID uuid.UUID
	ExpectedVersion                  int64
	Terms                            SpendTermsInput
}

type AnthropicInput struct {
	AttemptID, GrantID, ParentTurnID uuid.UUID
	TaskBinding                      *ledger.TaskBinding
	RouteID                          uuid.UUID
	ExpectedRouteVersion             int64
	CredentialVersion                uuid.UUID
	Request                          providers.Request
}

// OpenAIInput uses the same local request vocabulary and admission identities.
type OpenAIInput AnthropicInput
type PaidAdmission struct {
	CommandID, AttemptID, GrantID, ParentTurnID uuid.UUID
	TaskBinding                                 *ledger.TaskBinding
	Prepared                                    *PreparedPaidCall
}
type PaidReconciliation struct {
	CommandID, AttemptID uuid.UUID
	ExpectedVersion      int64
}

type PaidDispatch struct {
	CommandID, AttemptID uuid.UUID
	ExpectedVersion      int64
}

type PaidDispatchOutcome struct {
	IntentReceipt    PaidReceipt
	ObservedPaidCall *ObservedPaidCall
	Result           *PaidAttemptView
}

// ObservedPaidCall binds published evidence, never authority or caller dollars.
type ObservedPaidCall struct {
	service  *Service
	attempt  uuid.UUID
	origin   ledger.Origin
	evidence ledger.ObjectRef
}

func (*ObservedPaidCall) String() string               { return "<observed paid call>" }
func (*ObservedPaidCall) GoString() string             { return "<observed paid call>" }
func (*ObservedPaidCall) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

// Evidence returns only the content address; bytes require private.read.
func (o *ObservedPaidCall) Evidence() (ledger.ObjectRef, error) {
	if o == nil || o.service == nil {
		return ledger.ObjectRef{}, ErrNotBound
	}
	return o.evidence, nil
}

type PaidObservation struct {
	CommandID, AttemptID uuid.UUID
	ExpectedVersion      int64
	Observed             *ObservedPaidCall
	// TrustedEvidence is an explicit Core-host attestation of late evidence,
	// never an Agent entrance. Exactly one evidence source must be supplied.
	TrustedEvidence *ledger.ObjectRef
}

type PaidCorrection struct {
	PaidObservation
	PreviousResultSequence int64
	PreviousEvidenceSHA256 ledger.Digest
}

type PaidOverrunAcknowledgement struct {
	CommandID, GrantID, AttemptID        uuid.UUID
	ExpectedVersion, ResultSequence      int64
	EvidenceSHA256, RetiredProfileSHA256 ledger.Digest
}

// PreparedPaidCall is an incarnation-bound handle, not permission to admit.
// Its backing state is owned exclusively by Service and guarded by Service.mu.
type PreparedPaidCall struct {
	service *Service
	state   *preparedPaid
}

func (*PreparedPaidCall) String() string               { return "<prepared paid call>" }
func (*PreparedPaidCall) GoString() string             { return "<prepared paid call>" }
func (*PreparedPaidCall) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (p *PreparedPaidCall) Close() error {
	if p == nil || p.service == nil {
		return nil
	}
	p.service.mu.Lock()
	defer p.service.mu.Unlock()
	if p.state != nil && !p.state.consumed {
		p.service.releasePaidPreparation(p.state)
	}
	return nil
}

// BoundCall carries immutable nonsecret admission metadata, never a send token.
type BoundCall struct {
	service   *Service
	admission ledger.RuntimePaidReserved
}

func (*BoundCall) String() string               { return "<bound paid call>" }
func (*BoundCall) GoString() string             { return "<bound paid call>" }
func (*BoundCall) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (b *BoundCall) Admission() (ledger.RuntimePaidReserved, error) {
	if b == nil || b.service == nil {
		return ledger.RuntimePaidReserved{}, ErrNotBound
	}
	raw, err := b.admission.Encode()
	if err != nil {
		return ledger.RuntimePaidReserved{}, err
	}
	return ledger.DecodeRuntimePaidReserved(raw)
}

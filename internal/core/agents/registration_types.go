package agents

import (
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ResourceRegistration binds a stable identity to an exact immutable external
// address. The trusted host supplies nonsecret opaque identity, never a URL,
// account identifier or credential. This API does not resolve or classify keys.
type ResourceRegistration struct {
	CommandID, ResourceID uuid.UUID
	ExpectedVersion       uint64
	Resource              ledger.Resource
	Audience              []uuid.UUID
}

type WorkDomainRegistration struct {
	CommandID, WorkDomainID uuid.UUID
	ExpectedVersion         uint64
	Audience                []uuid.UUID
}

// DeclarationChange replaces all consumers, including an empty withdrawal.
// Subject pins the source version current at commit, not a moving alias.
type DeclarationChange struct {
	CommandID, DeclarationID uuid.UUID
	ExpectedVersion          uint64
	Subject                  ledger.RegistrationRef
	Audience                 []uuid.UUID
	Consumers                []ledger.DeclarationConsumer
}

type RegistrationReceipt struct {
	ledger.RuntimeRegistrationReceipt
	EffectSequence, Sequence uint64
}

// RegistrationLookup selects a complete prefix: zero is genesis and ToEnd
// captures the current durable boundary. Numeric cursors cannot be future ones.
type RegistrationLookup struct {
	ID              uuid.UUID
	ThroughSequence uint64
}

type ResourceRegistrationLookup struct {
	Resource        ledger.Resource
	ThroughSequence uint64
}

type RegistrationVersionLookup struct {
	Ref             ledger.RegistrationRef
	ThroughSequence uint64
}

type RegistrationCommandLookup struct {
	CommandID        uuid.UUID
	SubmissionSHA256 ledger.Digest
}

type ResourceRegistrationView struct {
	Record                                           ledger.RuntimeResourceRegistered
	Origin                                           ledger.Origin
	EffectSequence, ReceiptSequence, ThroughSequence uint64
}

type WorkDomainRegistrationView struct {
	Record                                           ledger.RuntimeWorkDomainRegistered
	Origin                                           ledger.Origin
	EffectSequence, ReceiptSequence, ThroughSequence uint64
}

type DeclarationView struct {
	Record                                           ledger.RuntimeDeclarationChanged
	Origin                                           ledger.Origin
	EffectSequence, ReceiptSequence, ThroughSequence uint64
}

// RegistrationEvidence contains exactly one source snapshot, selected by kind.
type RegistrationEvidence struct {
	Resource   *ResourceRegistrationView
	WorkDomain *WorkDomainRegistrationView
}

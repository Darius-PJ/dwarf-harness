package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// F3-139's version-1 records are frozen (D154).
const (
	TypeRuntimeResourceRegistered   = "runtime.resource_registered"
	TypeRuntimeWorkDomainRegistered = "runtime.work_domain_registered"
	TypeRuntimeDeclarationChanged   = "runtime.declaration_changed"
	TypeRuntimeRegistrationReceipt  = "runtime.registration_receipt"

	// MaxRegistrationPayloadBytes bounds complete input and encoded output,
	// including embedded submissions and ignored optional input members.
	MaxRegistrationPayloadBytes = 64 << 10
)

// RegistrationRef pins one resource or work-domain version. IDs are kind-scoped.
type RegistrationRef struct {
	Kind    string    `json:"kind"`
	ID      uuid.UUID `json:"id"`
	Version int64     `json:"version"`
}

func (r RegistrationRef) check() error {
	if (r.Kind != "resource" && r.Kind != "work_domain") || !isV7(r.ID) || r.Version <= 0 {
		return errors.New("invalid registration reference")
	}
	return nil
}

// DeclarationConsumer names either a stable task or an exact context binding.
// Only the field belonging to Kind may be populated.
type DeclarationConsumer struct {
	Kind    string
	TaskID  uuid.UUID
	Binding Binding
}

func (c DeclarationConsumer) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case "task":
		if !isV7(c.TaskID) || c.Binding != (Binding{}) {
			return nil, errors.New("invalid task consumer")
		}
		return json.Marshal(struct {
			Kind   string    `json:"kind"`
			TaskID uuid.UUID `json:"task_id"`
		}{c.Kind, c.TaskID})
	case "context":
		if !validBinding(c.Binding) || c.TaskID != (uuid.UUID{}) {
			return nil, errors.New("invalid context consumer")
		}
		return json.Marshal(struct {
			Kind    string  `json:"kind"`
			Binding Binding `json:"binding"`
		}{c.Kind, c.Binding})
	default:
		return nil, errors.New("unknown declaration consumer kind")
	}
}

func validDeclarationConsumers(consumers []DeclarationConsumer) bool {
	if consumers == nil {
		return false
	}
	var previous []byte
	for _, consumer := range consumers {
		encoded, err := consumer.MarshalJSON()
		if err != nil || previous != nil && bytes.Compare(previous, encoded) >= 0 {
			return false
		}
		previous = encoded
	}
	return true
}

func readRegistrationRef(m *members) RegistrationRef {
	return RegistrationRef{m.text("kind"), m.id("id"), m.nat("version", true)}
}

func readDeclarationConsumer(m *members) DeclarationConsumer {
	c := DeclarationConsumer{Kind: m.text("kind")}
	switch c.Kind {
	case "task":
		forbidScoped(m, "binding")
		c.TaskID = m.id("task_id")
	case "context":
		forbidScoped(m, "task_id")
		c.Binding = graphObject(m, "binding", readBinding)
	default:
		m.err = errors.New("unknown declaration consumer kind")
	}
	return c
}

func encodeRegistrationPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxRegistrationPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxRegistrationPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeRegistrationPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxRegistrationPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxRegistrationPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

// RuntimeResourceRegistered is a complete selected version, not permission.
// Resource must explicitly supply the exact external grant address. The trusted
// host supplies nonsecret opaque identity, never a URL, account or credential;
// the byte grammar cannot establish that semantic precondition. Address
// immutability and uniqueness are service rules, not codec history lookups.
type RuntimeResourceRegistered struct {
	ResourceID      uuid.UUID   `json:"resource_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Version         int64       `json:"version"`
	Resource        Resource    `json:"resource"`
	Audience        []uuid.UUID `json:"audience"`
}

func (p RuntimeResourceRegistered) check() error {
	if !isV7(p.ResourceID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || p.Resource.Kind != "external" || p.Resource.check() != nil || !scopedIDs(p.Audience, false) {
		return errors.New("invalid resource registration")
	}
	return nil
}

func (p RuntimeResourceRegistered) Encode() (json.RawMessage, error) {
	return encodeRegistrationPayload(TypeRuntimeResourceRegistered, p, p.check())
}

func DecodeRuntimeResourceRegistered(js json.RawMessage) (RuntimeResourceRegistered, error) {
	var p RuntimeResourceRegistered
	err := decodeRegistrationPayload(TypeRuntimeResourceRegistered, js, func(m *members) error {
		forbidScoped(m, "work_domain_id", "declaration_id", "subject", "consumers")
		p = RuntimeResourceRegistered{ResourceID: m.id("resource_id"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Resource: graphObject(m, "resource", readResource)}
		p.Audience, _ = m.sortedIDs("audience")
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeResourceRegistered{}, err
	}
	return p, nil
}

// RuntimeWorkDomainRegistered records a complete version of an opaque domain.
// A domain is not a new Resource kind or an authority scope.
type RuntimeWorkDomainRegistered struct {
	WorkDomainID    uuid.UUID   `json:"work_domain_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Version         int64       `json:"version"`
	Audience        []uuid.UUID `json:"audience"`
}

func (p RuntimeWorkDomainRegistered) check() error {
	if !isV7(p.WorkDomainID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || !scopedIDs(p.Audience, false) {
		return errors.New("invalid work-domain registration")
	}
	return nil
}

func (p RuntimeWorkDomainRegistered) Encode() (json.RawMessage, error) {
	return encodeRegistrationPayload(TypeRuntimeWorkDomainRegistered, p, p.check())
}

func DecodeRuntimeWorkDomainRegistered(js json.RawMessage) (RuntimeWorkDomainRegistered, error) {
	var p RuntimeWorkDomainRegistered
	err := decodeRegistrationPayload(TypeRuntimeWorkDomainRegistered, js, func(m *members) error {
		forbidScoped(m, "resource_id", "resource", "declaration_id", "subject", "consumers")
		p = RuntimeWorkDomainRegistered{WorkDomainID: m.id("work_domain_id"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true)}
		p.Audience, _ = m.sortedIDs("audience")
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeWorkDomainRegistered{}, err
	}
	return p, nil
}

// RuntimeDeclarationChanged replaces the complete consumer set. An explicit
// empty Consumers array withdraws edges without deleting the declaration.
type RuntimeDeclarationChanged struct {
	DeclarationID   uuid.UUID             `json:"declaration_id"`
	ExpectedVersion int64                 `json:"expected_version"`
	Version         int64                 `json:"version"`
	Subject         RegistrationRef       `json:"subject"`
	Audience        []uuid.UUID           `json:"audience"`
	Consumers       []DeclarationConsumer `json:"consumers"`
}

func (p RuntimeDeclarationChanged) check() error {
	if !isV7(p.DeclarationID) || !nextScopedVersion(p.ExpectedVersion, p.Version) || p.Subject.check() != nil || !scopedIDs(p.Audience, false) || !validDeclarationConsumers(p.Consumers) {
		return errors.New("invalid declaration change")
	}
	return nil
}

func (p RuntimeDeclarationChanged) Encode() (json.RawMessage, error) {
	return encodeRegistrationPayload(TypeRuntimeDeclarationChanged, p, p.check())
}

func DecodeRuntimeDeclarationChanged(js json.RawMessage) (RuntimeDeclarationChanged, error) {
	var p RuntimeDeclarationChanged
	err := decodeRegistrationPayload(TypeRuntimeDeclarationChanged, js, func(m *members) error {
		forbidScoped(m, "resource_id", "resource", "work_domain_id")
		p = RuntimeDeclarationChanged{DeclarationID: m.id("declaration_id"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), Subject: graphObject(m, "subject", readRegistrationRef), Consumers: readScopedObjects(m, "consumers", readDeclarationConsumer)}
		p.Audience, _ = m.sortedIDs("audience")
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeDeclarationChanged{}, err
	}
	return p, nil
}

// RuntimeRegistrationReceipt binds a command to its canonical schema-5 request.
// Outcome is open for decoding; v1 writers emit applied and Core must refuse to
// apply unfamiliar outcomes. Replay additionally verifies the complete group.
type RuntimeRegistrationReceipt struct {
	CommandID        uuid.UUID              `json:"command_id"`
	SubmissionSHA256 Digest                 `json:"submission_sha256"`
	Submission       RegistrationSubmission `json:"submission"`
	Outcome          string                 `json:"outcome"`
	Version          int64                  `json:"version"`
}

func (p RuntimeRegistrationReceipt) check() error {
	if !isV7(p.CommandID) || !scopedToken(p.Outcome) {
		return errors.New("invalid registration receipt identity or outcome")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 == (Digest{}) || p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("registration receipt submission digest mismatch")
	}
	if !nextScopedVersion(p.Submission.expectedVersion(), p.Version) {
		return errors.New("registration receipt version mismatch")
	}
	return nil
}

func (p RuntimeRegistrationReceipt) Encode() (json.RawMessage, error) {
	return encodeRegistrationPayload(TypeRuntimeRegistrationReceipt, p, p.check())
}

func DecodeRuntimeRegistrationReceipt(js json.RawMessage) (RuntimeRegistrationReceipt, error) {
	var p RuntimeRegistrationReceipt
	err := decodeRegistrationPayload(TypeRuntimeRegistrationReceipt, js, func(m *members) error {
		p = RuntimeRegistrationReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Submission: graphObject(m, "submission", readRegistrationSubmission), Outcome: m.text("outcome"), Version: m.nat("version", true)}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeRegistrationReceipt{}, err
	}
	return p, nil
}

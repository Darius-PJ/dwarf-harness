package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"uuid"
)

// The F3-10 record types, each at version 1, proposed for freeze (D125).
// These codecs enforce payload rules; the service enforces record rules.
const (
	TypeRuntimeAgentRegistered = "runtime.agent_registered"
	TypeRuntimeContextCreated  = "runtime.context_created"
	TypePrivateRevision        = "private.revision"
	TypeRuntimeCommandReceipt  = "runtime.command_receipt"
)

// F3-10's closed scope, slot and mode values, proposed for freeze (D125).
const (
	PrivateScopeAgent       = "agent"
	PrivateScopeContext     = "context"
	PrivateSlotInstructions = "instructions"
	PrivateSlotMemory       = "memory"
	PrivateSlotHandoff      = "handoff"
	PrivateSlotTranscript   = "transcript"
	PrivateSlotContinuation = "continuation"
	PrivateModeReplace      = "replace"
	PrivateModeAppend       = "append"
	PrivateModeClear        = "clear"
)

// F3-10's version-1 writer values under open token grammars, proposed for
// freeze (D125). Readers also accept other well-formed tokens.
const (
	RuntimeKindGeneral            = "general"
	RuntimeKindWork               = "work"
	RuntimeOperationAgentRegister = "agent_register"
	RuntimeOperationContextCreate = "context_create"
	RuntimeOperationPrivateWrite  = "private_write"
	RuntimeOutcomeApplied         = "applied"
)

// MaxPrivatePayloadBytes is F3-10's complete payload cap, proposed for freeze (D125).
const MaxPrivatePayloadBytes = 16 << 10

// RuntimeAgentRegistered binds a safe directory and the agent's general context.
type RuntimeAgentRegistered struct {
	Directory        string    `json:"directory"`
	GeneralContextID uuid.UUID `json:"general_context_id"`
}

// RuntimeContextCreated gives a new context its kind, an open token.
type RuntimeContextCreated struct {
	Kind string `json:"kind"`
}

// PrivateChange selects exact bytes within one scope. Bytes is required even
// when zero, except for clear, which omits SHA256, Bytes and PreviousSHA256.
// PreviousSHA256 is present only for a transcript append after an earlier chunk.
type PrivateChange struct {
	Slot           string `json:"slot"`
	Mode           string `json:"mode"`
	SHA256         Digest `json:"sha256,omitzero"`
	Bytes          int64  `json:"bytes"`
	PreviousSHA256 Digest `json:"previous_sha256,omitzero"`
}

// PrivateRevision atomically advances one private scope by one revision.
type PrivateRevision struct {
	Scope           string          `json:"scope"`
	ExpectedVersion int64           `json:"expected_version"`
	Revision        int64           `json:"revision"`
	Changes         []PrivateChange `json:"changes"`
}

// PrivateTarget is a closed scope with a strictly sorted set of its slots.
// Identity operations use an empty slot set.
type PrivateTarget struct {
	Scope string   `json:"scope"`
	Slots []string `json:"slots"`
}

// RuntimeCommandReceipt binds one command's canonical submission and result.
// RequestID is optional; version-1 producers leave it absent.
type RuntimeCommandReceipt struct {
	CommandID        uuid.UUID     `json:"command_id"`
	SubmissionSHA256 Digest        `json:"submission_sha256"`
	Operation        string        `json:"operation"`
	Target           PrivateTarget `json:"target"`
	ExpectedVersion  int64         `json:"expected_version"`
	Outcome          string        `json:"outcome"`
	RequestID        uuid.UUID     `json:"request_id,omitzero"`
}

func encodePrivatePayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err != nil {
		return nil, err
	}
	if len(js) > MaxPrivatePayloadBytes {
		return nil, fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxPrivatePayloadBytes)
	}
	return js, nil
}

func decodePrivatePayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxPrivatePayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxPrivatePayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

// Encode returns a runtime.agent_registered v1 payload; errors wrap ErrInvalid.
func (p RuntimeAgentRegistered) Encode() (json.RawMessage, error) {
	return encodePrivatePayload(TypeRuntimeAgentRegistered, p, p.check())
}

// DecodeRuntimeAgentRegistered decodes a runtime.agent_registered v1 payload.
// Its errors wrap ErrInvalid.
func DecodeRuntimeAgentRegistered(js json.RawMessage) (RuntimeAgentRegistered, error) {
	var p RuntimeAgentRegistered
	if err := decodePrivatePayload(TypeRuntimeAgentRegistered, js, func(m *members) error {
		p = RuntimeAgentRegistered{Directory: m.text("directory"), GeneralContextID: m.id("general_context_id")}
		return p.check()
	}); err != nil {
		return RuntimeAgentRegistered{}, err
	}
	return p, nil
}

func (p RuntimeAgentRegistered) check() error {
	if !isV7(p.GeneralContextID) {
		return errors.New("general_context_id is not a UUIDv7")
	}
	if len(p.Directory) < 1 || len(p.Directory) > 128 {
		return errors.New("directory must have 1 through 128 ASCII bytes")
	}
	for i, c := range []byte(p.Directory) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '_' || c == '-')) {
			return errors.New("directory is not a safe basename")
		}
	}
	upper := strings.ToUpper(p.Directory)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || len(upper) == 4 && (upper[:3] == "COM" || upper[:3] == "LPT") && upper[3] >= '1' && upper[3] <= '9' {
		return errors.New("directory is a reserved device basename")
	}
	return nil
}

// Encode returns a runtime.context_created v1 payload; errors wrap ErrInvalid.
func (p RuntimeContextCreated) Encode() (json.RawMessage, error) {
	return encodePrivatePayload(TypeRuntimeContextCreated, p, p.check())
}

// DecodeRuntimeContextCreated decodes a runtime.context_created v1 payload.
// Its errors wrap ErrInvalid.
func DecodeRuntimeContextCreated(js json.RawMessage) (RuntimeContextCreated, error) {
	var p RuntimeContextCreated
	if err := decodePrivatePayload(TypeRuntimeContextCreated, js, func(m *members) error {
		p.Kind = m.text("kind")
		return p.check()
	}); err != nil {
		return RuntimeContextCreated{}, err
	}
	return p, nil
}

func (p RuntimeContextCreated) check() error {
	if n, ok := dottedTokens(p.Kind); !ok || n != 1 || len(p.Kind) > 64 {
		return errors.New("kind is not a token of at most 64 bytes")
	}
	return nil
}

// privateChangeWire represents conditional fields without losing bytes:0.
type privateChangeWire struct {
	Slot           string `json:"slot"`
	Mode           string `json:"mode"`
	SHA256         Digest `json:"sha256,omitzero"`
	Bytes          *int64 `json:"bytes,omitempty"`
	PreviousSHA256 Digest `json:"previous_sha256,omitzero"`
}

// Encode returns a private.revision v1 payload; errors wrap ErrInvalid.
func (p PrivateRevision) Encode() (json.RawMessage, error) {
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("%w: %s payload: %w", ErrInvalid, TypePrivateRevision, err)
	}
	wire := struct {
		Scope           string              `json:"scope"`
		ExpectedVersion int64               `json:"expected_version"`
		Revision        int64               `json:"revision"`
		Changes         []privateChangeWire `json:"changes"`
	}{Scope: p.Scope, ExpectedVersion: p.ExpectedVersion, Revision: p.Revision, Changes: make([]privateChangeWire, len(p.Changes))}
	for i, c := range p.Changes {
		wire.Changes[i] = privateChangeWire{Slot: c.Slot, Mode: c.Mode, SHA256: c.SHA256, PreviousSHA256: c.PreviousSHA256}
		if c.Mode != PrivateModeClear {
			wire.Changes[i].Bytes = &p.Changes[i].Bytes
		}
	}
	return encodePrivatePayload(TypePrivateRevision, wire, nil)
}

// DecodePrivateRevision decodes a private.revision v1 payload.
// Its errors wrap ErrInvalid.
func DecodePrivateRevision(js json.RawMessage) (PrivateRevision, error) {
	var p PrivateRevision
	if err := decodePrivatePayload(TypePrivateRevision, js, func(m *members) error {
		p = PrivateRevision{Scope: m.text("scope"), ExpectedVersion: m.nat("expected_version", true), Revision: m.nat("revision", true)}
		raw, ok := m.get("changes", true)
		if !ok {
			return nil
		}
		var elems []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
			return errors.New("changes is not an array")
		}
		p.Changes = make([]PrivateChange, 0, len(elems))
		for i, el := range elems {
			b := members{}
			if el[0] != '{' || json.Unmarshal(el, &b.m) != nil {
				return fmt.Errorf("changes element %d is not an object", i)
			}
			c := PrivateChange{Slot: b.text("slot"), Mode: b.text("mode")}
			if c.Mode == PrivateModeClear {
				for _, key := range []string{"sha256", "bytes", "previous_sha256"} {
					if _, present := b.m[key]; present {
						return fmt.Errorf("changes element %d clear forbids %s", i, key)
					}
				}
			} else {
				c.SHA256, c.Bytes = b.digest("sha256"), b.nat("bytes", true)
				c.PreviousSHA256, _ = b.optionalDigest("previous_sha256")
			}
			if b.err != nil {
				return fmt.Errorf("changes element %d: %w", i, b.err)
			}
			p.Changes = append(p.Changes, c)
		}
		return p.check()
	}); err != nil {
		return PrivateRevision{}, err
	}
	return p, nil
}

func privateSlot(scope, slot string) bool {
	switch scope {
	case PrivateScopeAgent:
		return slot == PrivateSlotInstructions || slot == PrivateSlotMemory
	case PrivateScopeContext:
		return slot == PrivateSlotContinuation || slot == PrivateSlotHandoff || slot == PrivateSlotMemory || slot == PrivateSlotTranscript
	}
	return false
}

func (p PrivateRevision) check() error {
	if p.ExpectedVersion < 0 || p.ExpectedVersion == math.MaxInt64 || p.Revision != p.ExpectedVersion+1 {
		return errors.New("revision is not expected_version plus one in 1 through MaxInt64")
	}
	if len(p.Changes) == 0 || len(p.Changes) > 4 {
		return errors.New("changes must have 1 through 4 elements")
	}
	for i, c := range p.Changes {
		if !privateSlot(p.Scope, c.Slot) || i > 0 && p.Changes[i-1].Slot >= c.Slot {
			return errors.New("changes slots are not a strictly ascending set for the scope")
		}
		switch c.Slot {
		case PrivateSlotTranscript:
			if c.Mode != PrivateModeAppend {
				return errors.New("transcript requires append")
			}
		case PrivateSlotContinuation:
			if c.Mode != PrivateModeReplace && c.Mode != PrivateModeClear {
				return errors.New("continuation requires replace or clear")
			}
		default:
			if c.Mode != PrivateModeReplace {
				return errors.New("slot requires replace mode")
			}
		}
		if c.Mode == PrivateModeClear {
			if c.SHA256 != (Digest{}) || c.Bytes != 0 || c.PreviousSHA256 != (Digest{}) {
				return errors.New("clear forbids hash, bytes and predecessor")
			}
		} else if c.SHA256 == (Digest{}) || c.Bytes < 0 || (c.Slot == PrivateSlotTranscript || c.Slot == PrivateSlotContinuation) && c.Bytes == 0 {
			return errors.New("change has an invalid digest or byte count")
		}
		if c.Slot != PrivateSlotTranscript && c.PreviousSHA256 != (Digest{}) {
			return errors.New("only transcript may have a predecessor")
		}
	}
	if p.ExpectedVersion == 0 {
		first := PrivateSlotHandoff
		if p.Scope == PrivateScopeAgent {
			first = PrivateSlotInstructions
		}
		if len(p.Changes) != 2 || p.Changes[0].Slot != first || p.Changes[1].Slot != PrivateSlotMemory {
			return errors.New("initialization requires both Markdown slots")
		}
	}
	return nil
}

// Encode returns a runtime.command_receipt v1 payload; errors wrap ErrInvalid.
func (p RuntimeCommandReceipt) Encode() (json.RawMessage, error) {
	if p.Target.Slots == nil {
		p.Target.Slots = []string{}
	}
	return encodePrivatePayload(TypeRuntimeCommandReceipt, p, p.check())
}

// DecodeRuntimeCommandReceipt decodes a runtime.command_receipt v1 payload.
// Its errors wrap ErrInvalid.
func DecodeRuntimeCommandReceipt(js json.RawMessage) (RuntimeCommandReceipt, error) {
	var p RuntimeCommandReceipt
	if err := decodePrivatePayload(TypeRuntimeCommandReceipt, js, func(m *members) error {
		p = RuntimeCommandReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Operation: m.text("operation"), ExpectedVersion: m.nat("expected_version", true), Outcome: m.text("outcome")}
		p.RequestID, _ = m.optionalID("request_id")
		raw, ok := m.get("target", true)
		if !ok {
			return nil
		}
		b := members{}
		if raw[0] != '{' || json.Unmarshal(raw, &b.m) != nil {
			return errors.New("target is not an object")
		}
		p.Target.Scope = b.text("scope")
		raw, ok = b.get("slots", true)
		if !ok {
			return b.err
		}
		var elems []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
			return errors.New("target slots is not an array")
		}
		p.Target.Slots = make([]string, len(elems))
		for i, el := range elems {
			if p.Target.Slots[i], ok = parseString(el); !ok {
				return fmt.Errorf("target slots element %d is not a string", i)
			}
		}
		return p.check()
	}); err != nil {
		return RuntimeCommandReceipt{}, err
	}
	return p, nil
}

func (p RuntimeCommandReceipt) check() error {
	if !isV7(p.CommandID) || p.RequestID != (uuid.UUID{}) && !isV7(p.RequestID) {
		return errors.New("command_id or request_id is not a UUIDv7")
	}
	if p.SubmissionSHA256 == (Digest{}) {
		return errors.New("submission_sha256 is zero")
	}
	for _, token := range []string{p.Operation, p.Outcome} {
		if n, ok := dottedTokens(token); !ok || n != 1 || len(token) > 64 {
			return errors.New("operation or outcome is not a token of at most 64 bytes")
		}
	}
	if p.ExpectedVersion < 0 {
		return errors.New("expected_version is negative")
	}
	if p.Target.Scope != PrivateScopeAgent && p.Target.Scope != PrivateScopeContext {
		return errors.New("target scope is not agent or context")
	}
	for i, slot := range p.Target.Slots {
		if !privateSlot(p.Target.Scope, slot) || i > 0 && p.Target.Slots[i-1] >= slot {
			return errors.New("target slots are not a strictly ascending set for the scope")
		}
	}
	switch p.Operation {
	case RuntimeOperationAgentRegister, RuntimeOperationContextCreate:
		scope := PrivateScopeAgent
		if p.Operation == RuntimeOperationContextCreate {
			scope = PrivateScopeContext
		}
		if p.Target.Scope != scope || len(p.Target.Slots) != 0 || p.ExpectedVersion != 0 {
			return errors.New("identity operation requires its scope, empty slots and expectation zero")
		}
	case RuntimeOperationPrivateWrite:
		if len(p.Target.Slots) == 0 || p.ExpectedVersion < 1 || p.ExpectedVersion == math.MaxInt64 {
			return errors.New("private_write requires slots and expectation in 1 through MaxInt64-1")
		}
	}
	return nil
}

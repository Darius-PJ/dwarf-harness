package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"uuid"
)

// F3-150's version-1 records are frozen (D187).
const (
	TypeRuntimeSetupApplied = "runtime.setup_applied"
	TypeRuntimeSetupReceipt = "runtime.setup_receipt"

	// MaxSetupPayloadBytes bounds each complete payload and submission on
	// input and output, including ignored optional input members.
	MaxSetupPayloadBytes = 64 << 10
)

// RuntimeSetupApplied records owner setup evidence or complete configuration.
// Its group, native-effect ordering and revision checks belong to Core setup.
type RuntimeSetupApplied struct {
	Submission SetupSubmission `json:"submission"`
}

func (p RuntimeSetupApplied) Encode() (json.RawMessage, error) {
	_, err := p.Submission.Encode()
	return encodeSetupPayload(TypeRuntimeSetupApplied, p, err)
}

func DecodeRuntimeSetupApplied(js json.RawMessage) (RuntimeSetupApplied, error) {
	var p RuntimeSetupApplied
	err := decodeSetupPayload(TypeRuntimeSetupApplied, js, func(m *members) error {
		p = RuntimeSetupApplied{Submission: graphObject(m, "submission", readSetupSubmission)}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeSetupApplied{}, err
	}
	return p, nil
}

// RuntimeSetupReceipt binds the command and canonical submission digest to the
// resulting revision. Outcome is open; writers emit applied. The service checks
// the digest and revision against setup_applied and refuses unfamiliar outcomes.
type RuntimeSetupReceipt struct {
	CommandID        uuid.UUID `json:"command_id"`
	SubmissionSHA256 Digest    `json:"submission_sha256"`
	Outcome          string    `json:"outcome"`
	Revision         int64     `json:"revision"`
}

func (p RuntimeSetupReceipt) check() error {
	if !isV7(p.CommandID) || p.SubmissionSHA256 == (Digest{}) || !scopedToken(p.Outcome) || p.Revision <= 0 {
		return errors.New("invalid setup receipt identity, digest, outcome or revision")
	}
	return nil
}

func (p RuntimeSetupReceipt) Encode() (json.RawMessage, error) {
	return encodeSetupPayload(TypeRuntimeSetupReceipt, p, p.check())
}

func DecodeRuntimeSetupReceipt(js json.RawMessage) (RuntimeSetupReceipt, error) {
	var p RuntimeSetupReceipt
	err := decodeSetupPayload(TypeRuntimeSetupReceipt, js, func(m *members) error {
		p = RuntimeSetupReceipt{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256"), Outcome: m.text("outcome"), Revision: m.nat("revision", true)}
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RuntimeSetupReceipt{}, err
	}
	return p, nil
}

func encodeSetupPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxSetupPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxSetupPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeSetupPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxSetupPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxSetupPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

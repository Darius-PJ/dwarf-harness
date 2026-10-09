package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"uuid"
)

// The F0 record types, each at version 1. The package documentation
// specifies their payloads and record rules.
const (
	TypeCommand         = "ledger.command"          // version 1
	TypeBodyPin         = "ledger.body_pin"         // version 1
	TypeBodyUnpin       = "ledger.body_unpin"       // version 1
	TypeBodiesCollected = "ledger.bodies_collected" // version 1
)

// Command is the payload of a ledger.command v1 record, the identity
// tombstone of one command. It carries no request content.
type Command struct {
	CommandID        uuid.UUID `json:"command_id"`        // a UUIDv7
	SubmissionSHA256 Digest    `json:"submission_sha256"` // the digest of the whole binding
}

// BodyPin is the payload of a ledger.body_pin v1 record. The pinned bodies
// are the record's BodyRefs.
type BodyPin struct {
	PinID uuid.UUID `json:"pin_id"` // a UUIDv7
}

// BodyUnpin is the payload of a ledger.body_unpin v1 record.
type BodyUnpin struct {
	PinID uuid.UUID `json:"pin_id"` // a UUIDv7
}

// BodiesCollected is the payload of a ledger.bodies_collected v1 record.
type BodiesCollected struct {
	BeforeNS  int64           `json:"before_ns"`           // above 0
	KeepBytes int64           `json:"keep_bytes,omitzero"` // 0 = absent
	Bodies    []CollectedBody `json:"bodies"`              // non-empty, strictly ascending by SHA256
}

// CollectedBody is one body that a ledger.bodies_collected record lists.
type CollectedBody struct {
	SHA256 Digest `json:"sha256"` // not zero
	Bytes  int64  `json:"bytes"`  // at least 0
}

// Encode returns p as a ledger.command v1 payload. It refuses, with an error
// wrapping ErrInvalid, a CommandID that is not a UUIDv7 and a zero
// SubmissionSHA256.
func (p Command) Encode() (json.RawMessage, error) {
	return encodePayload(TypeCommand, p, p.check())
}

// DecodeCommand decodes a ledger.command v1 payload. Its errors wrap
// ErrInvalid.
func DecodeCommand(payload json.RawMessage) (Command, error) {
	var p Command
	if err := decodePayload(TypeCommand, payload, func(m *members) error {
		p = Command{CommandID: m.id("command_id"), SubmissionSHA256: m.digest("submission_sha256")}
		return p.check()
	}); err != nil {
		return Command{}, err
	}
	return p, nil
}

func (p Command) check() error {
	switch {
	case !isV7(p.CommandID):
		return errors.New("command_id is not a UUIDv7")
	case p.SubmissionSHA256 == Digest{}:
		return errors.New("submission_sha256 is zero")
	}
	return nil
}

// Encode returns p as a ledger.body_pin v1 payload. It refuses, with an
// error wrapping ErrInvalid, a PinID that is not a UUIDv7.
func (p BodyPin) Encode() (json.RawMessage, error) {
	return encodePayload(TypeBodyPin, p, checkPinID(p.PinID))
}

// DecodeBodyPin decodes a ledger.body_pin v1 payload. Its errors wrap
// ErrInvalid.
func DecodeBodyPin(payload json.RawMessage) (BodyPin, error) {
	var p BodyPin
	if err := decodePayload(TypeBodyPin, payload, func(m *members) error {
		p = BodyPin{PinID: m.id("pin_id")}
		return checkPinID(p.PinID)
	}); err != nil {
		return BodyPin{}, err
	}
	return p, nil
}

// Encode returns p as a ledger.body_unpin v1 payload. It refuses, with an
// error wrapping ErrInvalid, a PinID that is not a UUIDv7.
func (p BodyUnpin) Encode() (json.RawMessage, error) {
	return encodePayload(TypeBodyUnpin, p, checkPinID(p.PinID))
}

// DecodeBodyUnpin decodes a ledger.body_unpin v1 payload. Its errors wrap
// ErrInvalid.
func DecodeBodyUnpin(payload json.RawMessage) (BodyUnpin, error) {
	var p BodyUnpin
	if err := decodePayload(TypeBodyUnpin, payload, func(m *members) error {
		p = BodyUnpin{PinID: m.id("pin_id")}
		return checkPinID(p.PinID)
	}); err != nil {
		return BodyUnpin{}, err
	}
	return p, nil
}

func checkPinID(id uuid.UUID) error {
	if !isV7(id) {
		return errors.New("pin_id is not a UUIDv7")
	}
	return nil
}

// Encode returns p as a ledger.bodies_collected v1 payload, omitting
// keep_bytes when KeepBytes is 0. It refuses, with an error wrapping
// ErrInvalid, a BeforeNS below 1, a negative KeepBytes, an empty Bodies, and
// a body whose SHA256 is zero, whose Bytes is negative or whose SHA256 is not
// above the one before it.
func (p BodiesCollected) Encode() (json.RawMessage, error) {
	return encodePayload(TypeBodiesCollected, p, p.check())
}

// DecodeBodiesCollected decodes a ledger.bodies_collected v1 payload. Its
// errors wrap ErrInvalid.
func DecodeBodiesCollected(payload json.RawMessage) (BodiesCollected, error) {
	var p BodiesCollected
	if err := decodePayload(TypeBodiesCollected, payload, func(m *members) error {
		p = BodiesCollected{BeforeNS: m.nat("before_ns", true), KeepBytes: m.nat("keep_bytes", false)}
		if _, present := m.m["keep_bytes"]; present && p.KeepBytes == 0 && m.err == nil {
			return errors.New("keep_bytes is present and 0")
		}
		raw, ok := m.get("bodies", true)
		if !ok {
			return nil
		}
		var elems []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &elems) != nil {
			return errors.New("bodies is not an array")
		}
		for i, el := range elems {
			b := members{}
			if el[0] != '{' || json.Unmarshal(el, &b.m) != nil {
				return fmt.Errorf("bodies element %d is not an object", i)
			}
			body := CollectedBody{SHA256: b.digest("sha256"), Bytes: b.nat("bytes", true)}
			if b.err != nil {
				return fmt.Errorf("bodies element %d %w", i, b.err)
			}
			p.Bodies = append(p.Bodies, body)
		}
		return p.check()
	}); err != nil {
		return BodiesCollected{}, err
	}
	return p, nil
}

func (p BodiesCollected) check() error {
	switch {
	case p.BeforeNS < 1:
		return fmt.Errorf("before_ns %d is below 1", p.BeforeNS)
	case p.KeepBytes < 0:
		return fmt.Errorf("keep_bytes %d is negative", p.KeepBytes)
	case len(p.Bodies) == 0:
		return errors.New("bodies is empty")
	}
	for i, b := range p.Bodies {
		switch {
		case b.SHA256 == Digest{}:
			return fmt.Errorf("bodies element %d sha256 is zero", i)
		case b.Bytes < 0:
			return fmt.Errorf("bodies element %d bytes %d is negative", i, b.Bytes)
		case i > 0 && bytes.Compare(p.Bodies[i-1].SHA256[:], b.SHA256[:]) >= 0:
			return fmt.Errorf("bodies element %d sha256 is not above the one before it", i)
		}
	}
	return nil
}

// encodePayload returns the compact JSON of p, a payload of type typ, unless
// invalid, p's validation result, is not nil.
func encodePayload(typ string, p any, invalid error) (json.RawMessage, error) {
	if invalid != nil {
		return nil, fmt.Errorf("%w: %s payload: %w", ErrInvalid, typ, invalid)
	}
	js, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("%w: %s payload: %w", ErrInvalid, typ, err)
	}
	return js, nil
}

// decodePayload checks payload, of type typ, as a payload with no repeated
// key at any depth, and calls decode with its top-level members, of which
// decode ignores those it does not know. A member error that decode's
// accessors recorded takes precedence over decode's result.
func decodePayload(typ string, payload json.RawMessage, decode func(*members) error) error {
	err := checkPayload(payload)
	if err == nil {
		m := members{}
		if err = json.Unmarshal(payload, &m.m); err == nil {
			err = decode(&m)
			if m.err != nil {
				err = m.err
			}
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %s payload: %w", ErrInvalid, typ, err)
	}
	return nil
}

// members holds the members of one payload object. The first decoding error
// is kept in err, and every later accessor then returns a zero value.
type members struct {
	m   map[string]json.RawMessage
	err error
}

// get returns the value of key, recording an error for a missing required
// key or a null.
func (ms *members) get(key string, required bool) (json.RawMessage, bool) {
	if ms.err != nil {
		return nil, false
	}
	raw, ok := ms.m[key]
	switch {
	case !ok && required:
		ms.err = fmt.Errorf("required key %s is missing", key)
	case !ok:
	case string(raw) == "null":
		ms.err = fmt.Errorf("%s is null", key)
	default:
		return raw, true
	}
	return nil, false
}

// id returns the required canonical lowercase UUIDv7 key.
func (ms *members) id(key string) uuid.UUID {
	raw, ok := ms.get(key, true)
	if !ok {
		return uuid.UUID{}
	}
	s, ok := parseString(raw)
	if !ok {
		ms.err = fmt.Errorf("%s is not a string", key)
		return uuid.UUID{}
	}
	u, ok := parseID(s)
	if !ok {
		ms.err = fmt.Errorf("%s %w", key, errID)
	}
	return u
}

// digest returns the required digest key.
func (ms *members) digest(key string) Digest {
	raw, ok := ms.get(key, true)
	if !ok {
		return Digest{}
	}
	s, ok := parseString(raw)
	if !ok {
		ms.err = fmt.Errorf("%s is not a string", key)
		return Digest{}
	}
	d, err := parseDigest(s)
	if err != nil {
		ms.err = fmt.Errorf("%s %w", key, err)
	}
	return d
}

// nat returns the integer key in [0, MaxInt64], or 0 if it is optional and
// absent.
func (ms *members) nat(key string, required bool) int64 {
	raw, ok := ms.get(key, required)
	if !ok {
		return 0
	}
	n, ok := parseNat(raw, math.MaxInt64)
	if !ok {
		ms.err = fmt.Errorf("%s %w", key, errNat)
	}
	return int64(n)
}

package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"uuid"
)

// F7-30 is frozen (D188), independently of the launcher wire draft.
const (
	TypeLauncherCommandBound   = "launcher.command_bound"
	TypeLauncherCommandReceipt = "launcher.command_receipt"
	MaxLauncherPayloadBytes    = 65_536
)

// LauncherPrincipal records an authenticated native identity, not authority.
// UID is present only for unix_uid, including uid zero. AuthenticationID is
// retained for windows_logon but is not part of stable retry identity.
type LauncherPrincipal struct {
	Kind             string `json:"kind"`
	UserSID          string `json:"user_sid,omitempty"`
	AuthenticationID string `json:"authentication_id,omitempty"`
	UID              *int64 `json:"uid,omitempty"`
}

func (p LauncherPrincipal) valid() bool {
	switch p.Kind {
	case "unix_uid":
		return p.UID != nil && *p.UID >= 0 && *p.UID <= 1<<32-1 && p.UserSID == "" && p.AuthenticationID == ""
	case "windows_logon":
		if p.UID != nil || !launcherSID(p.UserSID) || len(p.AuthenticationID) != 16 {
			return false
		}
		for _, c := range p.AuthenticationID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func launcherSID(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 3 || len(parts) > 18 || parts[0] != "S" || parts[1] != "1" {
		return false
	}
	for i, part := range parts[2:] {
		bits := 32
		if i == 0 {
			bits = 48
		}
		n, err := strconv.ParseUint(part, 10, bits)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return false
		}
	}
	return true
}

// SameIdentity compares only the stable native principal; logon authentication
// IDs never turn an otherwise identical owner retry into a different request.
func (p LauncherPrincipal) SameIdentity(other LauncherPrincipal) bool {
	if !p.valid() || !other.valid() || p.Kind != other.Kind {
		return false
	}
	if p.Kind == "windows_logon" {
		return p.UserSID == other.UserSID
	}
	return *p.UID == *other.UID
}

// LauncherTarget is the immutable producer revision submitted by the owner.
type LauncherTarget struct {
	Kind     string    `json:"kind"`
	ID       uuid.UUID `json:"id"`
	Revision int64     `json:"revision"`
}

func (p LauncherTarget) valid() bool {
	return (p.Kind == "context" || p.Kind == "continuation") && isV7(p.ID) && p.Revision >= 0
}

// LauncherCommandBound promises native identities without admitting work.
type LauncherCommandBound struct {
	RequestID         uuid.UUID         `json:"request_id"`
	Principal         LauncherPrincipal `json:"principal"`
	Origin            Origin            `json:"origin"`
	Operation         string            `json:"operation"`
	TurnID            uuid.UUID         `json:"turn_id,omitzero"`
	TaskBinding       *TaskBinding      `json:"task_binding,omitempty"`
	Target            LauncherTarget    `json:"target"`
	RequestSHA256     Digest            `json:"request_sha256"`
	NativeAdmissionID uuid.UUID         `json:"native_admission_id,omitzero"`
}

func (p LauncherCommandBound) check() error {
	if !isV7(p.RequestID) || !p.Principal.valid() || !validOrigin(p.Origin) || !p.Target.valid() || p.RequestSHA256 == (Digest{}) || !validTaskBinding(p.TaskBinding) {
		return errors.New("invalid launcher identity, origin or target")
	}
	switch p.Operation {
	case "message.send":
		if !isV7(p.TurnID) || !isV7(p.NativeAdmissionID) || p.NativeAdmissionID == p.RequestID || p.Target.Kind != "context" || p.Target.ID != p.Origin.ContextID {
			return errors.New("invalid message binding")
		}
	case "turn.cancel":
		if !isV7(p.TurnID) || p.NativeAdmissionID != (uuid.UUID{}) || p.TaskBinding != nil || p.Target.Kind != "continuation" || p.Target.ID != p.TurnID {
			return errors.New("invalid cancellation binding")
		}
	case "promotion.request":
		if p.TurnID != (uuid.UUID{}) || p.NativeAdmissionID != (uuid.UUID{}) || p.TaskBinding != nil || p.Target.Kind != "context" || p.Target.ID != p.Origin.ContextID {
			return errors.New("invalid promotion binding")
		}
	default:
		return errors.New("unknown bound launcher operation")
	}
	return nil
}

func encodeLauncherPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxLauncherPayloadBytes {
		err = fmt.Errorf("%w: launcher payload exceeds cap", ErrInvalid)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeLauncherPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxLauncherPayloadBytes {
		return fmt.Errorf("%w: launcher payload exceeds cap", ErrInvalid)
	}
	return decodePayload(typ, js, decode)
}

func (p LauncherCommandBound) Encode() (json.RawMessage, error) {
	return encodeLauncherPayload(TypeLauncherCommandBound, p, p.check())
}

func DecodeLauncherCommandBound(js json.RawMessage) (LauncherCommandBound, error) {
	var p LauncherCommandBound
	err := decodeLauncherPayload(TypeLauncherCommandBound, js, func(m *members) error {
		id := m.id("request_id")
		p = readLauncherBindArguments(m).bound(id)
		return p.check()
	})
	if err != nil {
		return LauncherCommandBound{}, err
	}
	return p, nil
}

func readLauncherPrincipal(m *members) LauncherPrincipal {
	p := LauncherPrincipal{Kind: m.text("kind")}
	switch p.Kind {
	case "unix_uid":
		n := m.nat("uid", true)
		p.UID = &n
		forbidScoped(m, "user_sid", "authentication_id")
	case "windows_logon":
		p.UserSID, p.AuthenticationID = m.text("user_sid"), m.text("authentication_id")
		forbidScoped(m, "uid")
	default:
		m.err = errors.New("unknown launcher principal")
	}
	return p
}

func readLauncherTarget(m *members) LauncherTarget {
	return LauncherTarget{m.text("kind"), m.id("id"), m.nat("revision", true)}
}

// LauncherCommandReceipt certifies the binding, not execution or success.
type LauncherCommandReceipt struct {
	CommandID        uuid.UUID          `json:"command_id"`
	SubmissionSHA256 Digest             `json:"submission_sha256"`
	Submission       LauncherSubmission `json:"submission"`
	Outcome          string             `json:"outcome"`
	EffectSequence   int64              `json:"effect_sequence"`
}

func (p LauncherCommandReceipt) check() error {
	if !isV7(p.CommandID) || p.Outcome != "recorded" || p.EffectSequence <= 0 || p.CommandID == p.Submission.RequestID || p.CommandID == p.Submission.Arguments.NativeAdmissionID {
		return errors.New("invalid launcher receipt")
	}
	js, err := p.Submission.Encode()
	if err != nil {
		return err
	}
	if p.SubmissionSHA256 != Digest(sha256.Sum256(js)) {
		return errors.New("launcher submission digest mismatch")
	}
	return nil
}

func (p LauncherCommandReceipt) Encode() (json.RawMessage, error) {
	return encodeLauncherPayload(TypeLauncherCommandReceipt, p, p.check())
}

func DecodeLauncherCommandReceipt(js json.RawMessage) (LauncherCommandReceipt, error) {
	var p LauncherCommandReceipt
	err := decodeLauncherPayload(TypeLauncherCommandReceipt, js, func(m *members) error {
		p = LauncherCommandReceipt{m.id("command_id"), m.digest("submission_sha256"), graphObject(m, "submission", readLauncherSubmission), m.text("outcome"), m.nat("effect_sequence", true)}
		return p.check()
	})
	if err != nil {
		return LauncherCommandReceipt{}, err
	}
	return p, nil
}

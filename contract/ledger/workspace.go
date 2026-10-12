package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
	"uuid"
)

// The F2-05 record types, each at version 1. The package
// documentation specifies their payloads and service-owned record rules.
const (
	TypeWorkspaceRegistered = "workspace.registered"
	TypeCaptureInputsPinned = "workspace.capture_inputs_pinned"
	TypeScratchCreated      = "workspace.scratch_created"
	TypeScratchClosed       = "workspace.scratch_closed"
)

// WorkspaceRegistered is the payload of a workspace.registered v1 record.
type WorkspaceRegistered struct {
	WorkspaceID  uuid.UUID `json:"workspace_id"`  // a UUIDv7
	CommonDir    string    `json:"common_dir"`    // Git's absolute, forward-slash common directory
	ObjectFormat string    `json:"object_format"` // sha1 or sha256
}

// CaptureInputsPinned is the payload of a workspace.capture_inputs_pinned
// v1 record. The document and every body it names are listed in BodyRefs.
type CaptureInputsPinned struct {
	WorkspaceID         uuid.UUID `json:"workspace_id"`          // a UUIDv7
	Revision            int64     `json:"revision"`              // at least 1
	CaptureInputsSHA256 Digest    `json:"capture_inputs_sha256"` // not zero
	PinID               uuid.UUID `json:"pin_id"`                // a UUIDv7
}

// ScratchCreated is the payload of a workspace.scratch_created v1 record.
type ScratchCreated struct {
	ScratchID   uuid.UUID `json:"scratch_id"`   // a UUIDv7
	WorkspaceID uuid.UUID `json:"workspace_id"` // a UUIDv7
	ContextID   uuid.UUID `json:"context_id"`   // a UUIDv7
	Dir         string    `json:"dir"`          // 8 lowercase hex digits
	Branch      string    `json:"branch"`       // a full refs/heads/ name
	BaseCommit  string    `json:"base_commit"`  // 40 or 64 lowercase hex digits
	AnchorTree  string    `json:"anchor_tree"`  // 40 or 64 lowercase hex digits
}

// ScratchClosed is the payload of a workspace.scratch_closed v1 record.
// Reason is an open token; v1 defines abandoned. Core's producer writes only
// reasons its reading version defines, but encoding and decoding preserve
// every well-formed token so newer reasons remain readable.
type ScratchClosed struct {
	ScratchID uuid.UUID `json:"scratch_id"` // a UUIDv7
	ContextID uuid.UUID `json:"context_id"` // a UUIDv7
	Reason    string    `json:"reason"`     // [a-z][a-z0-9_]*, at most 64 bytes
}

// Encode returns p as a workspace.registered v1 payload. Its errors wrap
// ErrInvalid. It checks the payload rules, not the service's record rules.
func (p WorkspaceRegistered) Encode() (json.RawMessage, error) {
	return encodePayload(TypeWorkspaceRegistered, p, p.check())
}

// DecodeWorkspaceRegistered decodes a workspace.registered v1 payload.
// Its errors wrap ErrInvalid.
func DecodeWorkspaceRegistered(payload json.RawMessage) (WorkspaceRegistered, error) {
	var p WorkspaceRegistered
	if err := decodePayload(TypeWorkspaceRegistered, payload, func(m *members) error {
		p = WorkspaceRegistered{WorkspaceID: m.id("workspace_id"), CommonDir: m.text("common_dir"), ObjectFormat: m.text("object_format")}
		return p.check()
	}); err != nil {
		return WorkspaceRegistered{}, err
	}
	return p, nil
}

func (p WorkspaceRegistered) check() error {
	switch {
	case !isV7(p.WorkspaceID):
		return errors.New("workspace_id is not a UUIDv7")
	case !validCommonDir(p.CommonDir):
		return errors.New("common_dir does not match the absolute forward-slash grammar")
	case p.ObjectFormat != "sha1" && p.ObjectFormat != "sha256":
		return errors.New("object_format is not sha1 or sha256")
	}
	return nil
}

// Encode returns p as a workspace.capture_inputs_pinned v1 payload. Its
// errors wrap ErrInvalid. Revision sequencing is a service record rule.
func (p CaptureInputsPinned) Encode() (json.RawMessage, error) {
	return encodePayload(TypeCaptureInputsPinned, p, p.check())
}

// DecodeCaptureInputsPinned decodes a workspace.capture_inputs_pinned v1
// payload. Its errors wrap ErrInvalid.
func DecodeCaptureInputsPinned(payload json.RawMessage) (CaptureInputsPinned, error) {
	var p CaptureInputsPinned
	if err := decodePayload(TypeCaptureInputsPinned, payload, func(m *members) error {
		p = CaptureInputsPinned{WorkspaceID: m.id("workspace_id"), Revision: m.nat("revision", true), CaptureInputsSHA256: m.digest("capture_inputs_sha256"), PinID: m.id("pin_id")}
		return p.check()
	}); err != nil {
		return CaptureInputsPinned{}, err
	}
	return p, nil
}

func (p CaptureInputsPinned) check() error {
	switch {
	case !isV7(p.WorkspaceID):
		return errors.New("workspace_id is not a UUIDv7")
	case p.Revision < 1:
		return errors.New("revision is below 1")
	case p.CaptureInputsSHA256 == Digest{}:
		return errors.New("capture_inputs_sha256 is zero")
	}
	return checkPinID(p.PinID)
}

// Encode returns p as a workspace.scratch_created v1 payload. Its errors
// wrap ErrInvalid. Git ID lengths matching the workspace's object format
// and Core's branch naming scheme are service record rules.
func (p ScratchCreated) Encode() (json.RawMessage, error) {
	return encodePayload(TypeScratchCreated, p, p.check())
}

// DecodeScratchCreated decodes a workspace.scratch_created v1 payload.
// Its errors wrap ErrInvalid.
func DecodeScratchCreated(payload json.RawMessage) (ScratchCreated, error) {
	var p ScratchCreated
	if err := decodePayload(TypeScratchCreated, payload, func(m *members) error {
		p = ScratchCreated{
			ScratchID: m.id("scratch_id"), WorkspaceID: m.id("workspace_id"), ContextID: m.id("context_id"),
			Dir: m.text("dir"), Branch: m.text("branch"), BaseCommit: m.text("base_commit"), AnchorTree: m.text("anchor_tree"),
		}
		return p.check()
	}); err != nil {
		return ScratchCreated{}, err
	}
	return p, nil
}

func (p ScratchCreated) check() error {
	switch {
	case !isV7(p.ScratchID):
		return errors.New("scratch_id is not a UUIDv7")
	case !isV7(p.WorkspaceID):
		return errors.New("workspace_id is not a UUIDv7")
	case !isV7(p.ContextID):
		return errors.New("context_id is not a UUIDv7")
	case len(p.Dir) != 8 || !isLowerHex([]byte(p.Dir)):
		return errors.New("dir is not 8 lowercase hex digits")
	case !validScratchBranch(p.Branch):
		return errors.New("branch does not match the refs/heads/ grammar")
	case !validGitID(p.BaseCommit):
		return errors.New("base_commit is not 40 or 64 lowercase hex digits")
	case !validGitID(p.AnchorTree):
		return errors.New("anchor_tree is not 40 or 64 lowercase hex digits")
	}
	return nil
}

// Encode returns p as a workspace.scratch_closed v1 payload, accepting every
// well-formed reason token. Its errors wrap ErrInvalid.
func (p ScratchClosed) Encode() (json.RawMessage, error) {
	return encodePayload(TypeScratchClosed, p, p.check())
}

// DecodeScratchClosed decodes a workspace.scratch_closed v1 payload,
// including unknown well-formed reasons. Its errors wrap ErrInvalid.
func DecodeScratchClosed(payload json.RawMessage) (ScratchClosed, error) {
	var p ScratchClosed
	if err := decodePayload(TypeScratchClosed, payload, func(m *members) error {
		p = ScratchClosed{ScratchID: m.id("scratch_id"), ContextID: m.id("context_id"), Reason: m.text("reason")}
		return p.check()
	}); err != nil {
		return ScratchClosed{}, err
	}
	return p, nil
}

func (p ScratchClosed) check() error {
	switch {
	case !isV7(p.ScratchID):
		return errors.New("scratch_id is not a UUIDv7")
	case !isV7(p.ContextID):
		return errors.New("context_id is not a UUIDv7")
	}
	if n, ok := dottedTokens(p.Reason); !ok || n != 1 || len(p.Reason) > 64 {
		return errors.New("reason is not a token of at most 64 bytes")
	}
	return nil
}

func validGitID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && isLowerHex([]byte(s))
}

// validWorkspacePathText applies the common byte rules for the two lexical
// path forms. These checks are deliberately independent of the host OS.
func validWorkspacePathText(s string) bool {
	if len(s) == 0 || len(s) > 4096 || !utf8.ValidString(s) || strings.HasSuffix(s, "/") {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x20 || s[i] == 0x7f || s[i] == '\\' {
			return false
		}
	}
	return true
}

func validCommonDir(s string) bool {
	if !validWorkspacePathText(s) {
		return false
	}
	var rest string
	switch {
	case strings.HasPrefix(s, "//"):
		rest = s[2:]
		if !strings.Contains(rest, "/") { // Both host and share are required.
			return false
		}
	case strings.HasPrefix(s, "/"):
		rest = s[1:]
	case len(s) >= 3 && (s[0] >= 'A' && s[0] <= 'Z' || s[0] >= 'a' && s[0] <= 'z') && s[1:3] == ":/":
		rest = s[3:]
	default:
		return false
	}
	for component := range strings.SplitSeq(rest, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func validScratchBranch(s string) bool {
	return validWorkspacePathText(s) && strings.HasPrefix(s, "refs/heads/") &&
		!strings.Contains(s, "//") && !strings.Contains(s, " ")
}

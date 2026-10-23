package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"uuid"
)

// RuntimeWorkAdmitted reserves work; it is not evidence of execution.
type RuntimeWorkAdmitted struct {
	WorkID           uuid.UUID    `json:"work_id"`
	Kind             string       `json:"kind"`
	ExpectedVersion  int64        `json:"expected_version"`
	Version          int64        `json:"version"`
	AuthorityVersion int64        `json:"authority_version"`
	GrantID          uuid.UUID    `json:"grant_id"`
	Scopes           []ScopeKey   `json:"scopes"`
	Operation        string       `json:"operation"`
	Target           Resource     `json:"target"`
	TaskBinding      *TaskBinding `json:"task_binding,omitempty"`
	ParentTurnID     uuid.UUID    `json:"parent_turn_id,omitzero"`
	ArgumentsSHA256  Digest       `json:"arguments_sha256,omitzero"`
	ToolSchema       *int64       `json:"tool_schema,omitempty"`
	MaxBytes         *int64       `json:"max_bytes,omitempty"`
}

func (p RuntimeWorkAdmitted) check() error {
	if !isV7(p.WorkID) || p.ExpectedVersion != 0 || p.Version != 1 || p.AuthorityVersion < 0 || !isV7(p.GrantID) || !validScopes(p.Scopes) || p.Target.check() != nil || !validTaskBinding(p.TaskBinding) {
		return errors.New("invalid admission identity, version, target or scopes")
	}
	switch p.Kind {
	case "turn":
		if p.Operation != "turn.start" || p.Target.Kind != "context" || p.Target.Scope != "context" || p.ParentTurnID != (uuid.UUID{}) || p.ArgumentsSHA256 != (Digest{}) || p.ToolSchema != nil || p.MaxBytes != nil {
			return errors.New("invalid turn admission")
		}
	case "local":
		if !scopedClassOperation("workspace", p.Operation) || p.Target.Kind != "workspace" || !isV7(p.ParentTurnID) || p.ArgumentsSHA256 == (Digest{}) || p.ToolSchema == nil || *p.ToolSchema != 1 || p.MaxBytes == nil || *p.MaxBytes <= 0 {
			return errors.New("invalid local admission")
		}
	default:
		return errors.New("unknown work kind")
	}
	return nil
}
func (p RuntimeWorkAdmitted) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeWorkAdmitted, p, p.check())
}
func DecodeRuntimeWorkAdmitted(js json.RawMessage) (RuntimeWorkAdmitted, error) {
	var p RuntimeWorkAdmitted
	err := decodeScopedPayload(TypeRuntimeWorkAdmitted, js, func(m *members) error {
		p = RuntimeWorkAdmitted{WorkID: m.id("work_id"), Kind: m.text("kind"), ExpectedVersion: m.nat("expected_version", true), Version: m.nat("version", true), AuthorityVersion: m.nat("authority_version", true), GrantID: m.id("grant_id"), Scopes: readScopedObjects(m, "scopes", readScope), Operation: m.text("operation"), Target: graphObject(m, "target", readResource), TaskBinding: optionalObject(m, "task_binding", readTaskBinding), ToolSchema: optionalScopedNat(m, "tool_schema"), MaxBytes: optionalScopedNat(m, "max_bytes")}
		p.ParentTurnID, _ = m.optionalID("parent_turn_id")
		p.ArgumentsSHA256, _ = m.optionalDigest("arguments_sha256")
		return p.check()
	})
	if err != nil {
		return RuntimeWorkAdmitted{}, err
	}
	return p, nil
}

// RuntimeWorkDispatched consumes the admitted local unit and retains its holds.
type RuntimeWorkDispatched struct {
	WorkID           uuid.UUID `json:"work_id"`
	ExpectedVersion  int64     `json:"expected_version"`
	Version          int64     `json:"version"`
	AuthorityVersion int64     `json:"authority_version"`
	GrantID          uuid.UUID `json:"grant_id"`
	ArgumentsSHA256  Digest    `json:"arguments_sha256"`
}

func (p RuntimeWorkDispatched) check() error {
	if !isV7(p.WorkID) || p.ExpectedVersion != 1 || p.Version != 2 || p.AuthorityVersion < 0 || !isV7(p.GrantID) || p.ArgumentsSHA256 == (Digest{}) {
		return errors.New("invalid dispatch")
	}
	return nil
}
func (p RuntimeWorkDispatched) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeWorkDispatched, p, p.check())
}
func DecodeRuntimeWorkDispatched(js json.RawMessage) (RuntimeWorkDispatched, error) {
	var p RuntimeWorkDispatched
	err := decodeScopedPayload(TypeRuntimeWorkDispatched, js, func(m *members) error {
		p = RuntimeWorkDispatched{m.id("work_id"), m.nat("expected_version", true), m.nat("version", true), m.nat("authority_version", true), m.id("grant_id"), m.digest("arguments_sha256")}
		return p.check()
	})
	if err != nil {
		return RuntimeWorkDispatched{}, err
	}
	return p, nil
}

// RuntimeWorkEnded records an ending observation. Unknown outcomes match a
// start but release nothing; a later proven ending may advance them again.
type RuntimeWorkEnded struct {
	WorkID          uuid.UUID `json:"work_id"`
	ExpectedVersion int64     `json:"expected_version"`
	Version         int64     `json:"version"`
	Outcome         string    `json:"outcome"`
}

func (p RuntimeWorkEnded) check() error {
	if !isV7(p.WorkID) || p.ExpectedVersion < 1 || !nextScopedVersion(p.ExpectedVersion, p.Version) || !scopedToken(p.Outcome) {
		return errors.New("invalid ending")
	}
	return nil
}
func (p RuntimeWorkEnded) Encode() (json.RawMessage, error) {
	return encodeScopedPayload(TypeRuntimeWorkEnded, p, p.check())
}
func DecodeRuntimeWorkEnded(js json.RawMessage) (RuntimeWorkEnded, error) {
	var p RuntimeWorkEnded
	err := decodeScopedPayload(TypeRuntimeWorkEnded, js, func(m *members) error {
		p = RuntimeWorkEnded{m.id("work_id"), m.nat("expected_version", true), m.nat("version", true), m.text("outcome")}
		return p.check()
	})
	if err != nil {
		return RuntimeWorkEnded{}, err
	}
	return p, nil
}

// RuntimeLocalUsed is one successful atomic charge. Selected is nil when
// absent and non-nil (possibly empty) for a private read.
type RuntimeLocalUsed struct {
	AuthorityVersion int64            `json:"authority_version"`
	GrantID          uuid.UUID        `json:"grant_id"`
	Scopes           []ScopeKey       `json:"scopes"`
	Units            int64            `json:"units"`
	Bytes            int64            `json:"bytes"`
	Read             *PrivateReadRef  `json:"read,omitempty"`
	Artifact         *ArtifactRef     `json:"artifact,omitempty"`
	Selected         []SelectedObject `json:"selected,omitempty"`
	NextCursor       *int64           `json:"next_cursor,omitempty"`
}

func (p RuntimeLocalUsed) check() error {
	if p.AuthorityVersion < 0 || !isV7(p.GrantID) || !validScopes(p.Scopes) || p.Units != 1 || p.Bytes < 0 {
		return errors.New("invalid local usage")
	}
	if p.Read == nil {
		if p.Selected != nil || p.NextCursor != nil || p.Artifact != nil && (!validArtifact(*p.Artifact) || p.Bytes != p.Artifact.Bytes) {
			return errors.New("invalid nonprivate usage fields")
		}
		return nil
	}
	r := *p.Read
	if !validPrivateRead(r) || p.Artifact != nil || p.Selected == nil {
		return errors.New("invalid private read fields")
	}
	if r.Slot != "transcript" && (len(p.Selected) > 1 || len(p.Selected) == 0 && r.Slot != "continuation" || p.NextCursor != nil) {
		return errors.New("invalid selected head")
	}
	var total int64
	for i, s := range p.Selected {
		if s.SHA256 == (Digest{}) || s.Bytes < 0 || s.Revision < 1 || s.Revision > r.Revision || s.Sequence < 1 || total > math.MaxInt64-s.Bytes {
			return errors.New("invalid selected occurrence")
		}
		if r.Slot == "transcript" && (s.Bytes == 0 || s.Sequence <= r.Cursor || i > 0 && (p.Selected[i-1].Sequence >= s.Sequence || p.Selected[i-1].Revision >= s.Revision)) {
			return errors.New("unordered transcript selection")
		}
		total += s.Bytes
	}
	if total != p.Bytes || total > r.MaxBytes {
		return errors.New("selected byte total mismatch")
	}
	if p.NextCursor != nil && (r.Slot != "transcript" || len(p.Selected) == 0 || *p.NextCursor != p.Selected[len(p.Selected)-1].Sequence || *p.NextCursor <= r.Cursor) {
		return errors.New("invalid next cursor")
	}
	return nil
}
func (p RuntimeLocalUsed) Encode() (json.RawMessage, error) {
	// A pointer distinguishes absent from the required empty selected array.
	wire := struct {
		AuthorityVersion int64             `json:"authority_version"`
		GrantID          uuid.UUID         `json:"grant_id"`
		Scopes           []ScopeKey        `json:"scopes"`
		Units            int64             `json:"units"`
		Bytes            int64             `json:"bytes"`
		Read             *PrivateReadRef   `json:"read,omitempty"`
		Artifact         *ArtifactRef      `json:"artifact,omitempty"`
		Selected         *[]SelectedObject `json:"selected,omitempty"`
		NextCursor       *int64            `json:"next_cursor,omitempty"`
	}{p.AuthorityVersion, p.GrantID, p.Scopes, p.Units, p.Bytes, p.Read, p.Artifact, nil, p.NextCursor}
	if p.Selected != nil {
		wire.Selected = &p.Selected
	}
	return encodeScopedPayload(TypeRuntimeLocalUsed, wire, p.check())
}
func DecodeRuntimeLocalUsed(js json.RawMessage) (RuntimeLocalUsed, error) {
	var p RuntimeLocalUsed
	err := decodeScopedPayload(TypeRuntimeLocalUsed, js, func(m *members) error {
		p = RuntimeLocalUsed{AuthorityVersion: m.nat("authority_version", true), GrantID: m.id("grant_id"), Scopes: readScopedObjects(m, "scopes", readScope), Units: m.nat("units", true), Bytes: m.nat("bytes", true), Read: optionalObject(m, "read", readPrivateRead), Artifact: optionalObject(m, "artifact", readArtifact), NextCursor: optionalScopedNat(m, "next_cursor")}
		if _, ok := m.get("selected", false); ok {
			p.Selected = readScopedObjects(m, "selected", readSelected)
		}
		return p.check()
	})
	if err != nil {
		return RuntimeLocalUsed{}, err
	}
	return p, nil
}

// PrivateScopedRevision advances the same private head as PrivateRevision,
// using its unchanged manifest rules, but never initializes a scope.
type PrivateScopedRevision struct {
	Scope           string          `json:"scope"`
	ExpectedVersion int64           `json:"expected_version"`
	Revision        int64           `json:"revision"`
	Changes         []PrivateChange `json:"changes"`
}

func (p PrivateScopedRevision) check() error {
	if p.ExpectedVersion < 1 {
		return errors.New("scoped revision cannot initialize")
	}
	return PrivateRevision(p).check()
}
func (p PrivateScopedRevision) Encode() (json.RawMessage, error) {
	wire := struct {
		Scope           string              `json:"scope"`
		ExpectedVersion int64               `json:"expected_version"`
		Revision        int64               `json:"revision"`
		Changes         []privateChangeWire `json:"changes"`
	}{p.Scope, p.ExpectedVersion, p.Revision, scopedChanges(p.Changes)}
	return encodeScopedPayload(TypePrivateScopedRevision, wire, p.check())
}
func DecodePrivateScopedRevision(js json.RawMessage) (PrivateScopedRevision, error) {
	var p PrivateScopedRevision
	err := decodeScopedPayload(TypePrivateScopedRevision, js, func(m *members) error {
		p = PrivateScopedRevision{m.text("scope"), m.nat("expected_version", true), m.nat("revision", true), readScopedChanges(m, false)}
		return p.check()
	})
	if err != nil {
		return PrivateScopedRevision{}, err
	}
	return p, nil
}

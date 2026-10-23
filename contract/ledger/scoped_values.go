package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// MaxScopedPayloadBytes is F3-11's complete payload cap, frozen (D140).
const MaxScopedPayloadBytes = 64 << 10

const maxResourcePathBytes = 1024

// ValidResourcePath checks F3-11's lexical, portable Resource path grammar.
// It neither normalizes paths nor establishes filesystem/symlink containment.
func ValidResourcePath(path string) bool {
	if len(path) > maxResourcePathBytes || !utf8.ValidString(path) || strings.ContainsAny(path, "\\:") || strings.ContainsFunc(path, unicode.IsControl) {
		return false
	}
	if path == "" {
		return true
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." || len(component) == 4 && component[0] == '.' && (component[1] == 'g' || component[1] == 'G') && (component[2] == 'i' || component[2] == 'I') && (component[3] == 't' || component[3] == 'T') {
			return false
		}
	}
	return true
}

// Binding identifies one agent/context pair.
type Binding struct {
	AgentID   uuid.UUID `json:"agent_id"`
	ContextID uuid.UUID `json:"context_id"`
}

// Origin identifies the registered agent, its sole session and selected context.
type Origin struct {
	AgentID   uuid.UUID `json:"agent_id"`
	SessionID uuid.UUID `json:"session_id"`
	ContextID uuid.UUID `json:"context_id"`
}

// Assignment is a complete task assignment and its assigning principal.
type Assignment struct {
	AssignmentID  uuid.UUID `json:"assignment_id"`
	Generation    int64     `json:"generation"`
	Assignee      Binding   `json:"assignee"`
	AssignerActor string    `json:"assigner_actor"`
	Assigner      Origin    `json:"assigner"`
}

// TaskBinding restricts authority to one task assignment generation.
type TaskBinding struct {
	TaskID       uuid.UUID `json:"task_id"`
	AssignmentID uuid.UUID `json:"assignment_id"`
	Generation   int64     `json:"generation"`
}

// ObjectRef identifies exact bytes; a task description's source is its envelope.
type ObjectRef struct {
	SHA256 Digest `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// ArtifactRef additionally identifies the record whose body_refs contains SHA256.
type ArtifactRef struct {
	SourceSequence int64  `json:"source_sequence"`
	SHA256         Digest `json:"sha256"`
	Bytes          int64  `json:"bytes"`
}

// SubjectRef is an open, fail-closed task subject. Artifact is used only for kind
// artifact. For unfamiliar kinds, Members holds a JSON object of the kind's
// members (excluding kind), preserving member order and values. The service must
// refuse to apply an unfamiliar kind, rather than interpreting it as an artifact.
type SubjectRef struct {
	Kind     string
	Artifact ArtifactRef
	Members  json.RawMessage
}

func (s SubjectRef) MarshalJSON() ([]byte, error) {
	if !scopedToken(s.Kind) {
		return nil, errors.New("invalid subject kind")
	}
	if s.Kind == "artifact" {
		if s.Members != nil || !validArtifact(s.Artifact) {
			return nil, errors.New("invalid artifact subject")
		}
		return json.Marshal(struct {
			Kind     string      `json:"kind"`
			Artifact ArtifactRef `json:"artifact"`
		}{s.Kind, s.Artifact})
	}
	if s.Artifact != (ArtifactRef{}) {
		return nil, errors.New("unknown subject has typed artifact")
	}
	raw := s.Members
	if raw == nil {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > MaxScopedPayloadBytes || checkPayload(raw) != nil {
		return nil, errors.New("invalid subject members")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("invalid subject object")
	}
	if _, ok := fields["kind"]; ok {
		return nil, errors.New("subject members repeat kind")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	kind, _ := json.Marshal(s.Kind)
	out := append([]byte(`{"kind":`), kind...)
	if compact.Len() > 2 {
		out = append(out, ',')
		out = append(out, compact.Bytes()[1:compact.Len()-1]...)
	}
	return append(out, '}'), nil
}

func subjectAt(m *members, key string) *SubjectRef {
	raw, ok := m.get(key, false)
	if !ok {
		return nil
	}
	b := members{}
	if raw[0] != '{' || json.Unmarshal(raw, &b.m) != nil {
		m.err = fmt.Errorf("%s is not an object", key)
		return nil
	}
	s := &SubjectRef{Kind: b.text("kind")}
	if s.Kind == "artifact" {
		s.Artifact = graphObject(&b, "artifact", readArtifact)
	} else {
		// Decode values as RawMessage, not interface{}, so integers, ordering and
		// unfamiliar members survive without float conversion or map sorting.
		d := json.NewDecoder(bytes.NewReader(raw))
		_, _ = d.Token()
		var fields bytes.Buffer
		fields.WriteByte('{')
		for d.More() {
			name, err := d.Token()
			if err != nil {
				b.err = err
				break
			}
			var value json.RawMessage
			if err := d.Decode(&value); err != nil {
				b.err = err
				break
			}
			if name == "kind" {
				continue
			}
			if fields.Len() > 1 {
				fields.WriteByte(',')
			}
			encoded, _ := json.Marshal(name)
			fields.Write(encoded)
			fields.WriteByte(':')
			compact, err := json.Marshal(value)
			if err != nil {
				b.err = err
				break
			}
			fields.Write(compact)
		}
		fields.WriteByte('}')
		s.Members = fields.Bytes()
	}
	if b.err != nil {
		m.err = b.err
	}
	return s
}

// Resource carries only the members of its closed kind. TaskID may be absent
// for a task-creation region; grant and submission rules constrain its use.
type Resource struct {
	Kind        string
	Binding     Binding
	Scope       string
	Slots       []string
	TaskID      uuid.UUID
	WorkspaceID uuid.UUID
	PathPrefix  string
	Artifact    ArtifactRef
	ResourceKey string
}

func (r Resource) MarshalJSON() ([]byte, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	switch r.Kind {
	case "context":
		return json.Marshal(struct {
			Kind    string   `json:"kind"`
			Binding Binding  `json:"binding"`
			Scope   string   `json:"scope"`
			Slots   []string `json:"slots"`
		}{r.Kind, r.Binding, r.Scope, r.Slots})
	case "task":
		return json.Marshal(struct {
			Kind   string    `json:"kind"`
			TaskID uuid.UUID `json:"task_id,omitzero"`
		}{r.Kind, r.TaskID})
	case "workspace":
		return json.Marshal(struct {
			Kind        string    `json:"kind"`
			WorkspaceID uuid.UUID `json:"workspace_id"`
			Binding     Binding   `json:"binding"`
			PathPrefix  string    `json:"path_prefix"`
		}{r.Kind, r.WorkspaceID, r.Binding, r.PathPrefix})
	case "artifact":
		return json.Marshal(struct {
			Kind     string      `json:"kind"`
			Artifact ArtifactRef `json:"artifact"`
		}{r.Kind, r.Artifact})
	default:
		return json.Marshal(struct {
			Kind        string `json:"kind"`
			ResourceKey string `json:"resource_key"`
		}{r.Kind, r.ResourceKey})
	}
}

func (r Resource) check() error {
	base := Resource{Kind: r.Kind}
	switch r.Kind {
	case "context":
		if !validBinding(r.Binding) || (r.Scope != "agent" && r.Scope != "context") || r.Slots == nil || !strictScopedStrings(r.Slots) {
			return errors.New("invalid context resource")
		}
		for _, slot := range r.Slots {
			if !privateSlot(r.Scope, slot) {
				return errors.New("invalid context slot")
			}
		}
		base.Binding, base.Scope, base.Slots = r.Binding, r.Scope, r.Slots
	case "task":
		if r.TaskID != (uuid.UUID{}) && !isV7(r.TaskID) {
			return errors.New("invalid task resource")
		}
		base.TaskID = r.TaskID
	case "workspace":
		if !isV7(r.WorkspaceID) || !validBinding(r.Binding) || !ValidResourcePath(r.PathPrefix) {
			return errors.New("invalid workspace resource")
		}
		base.WorkspaceID, base.Binding, base.PathPrefix = r.WorkspaceID, r.Binding, r.PathPrefix
	case "artifact":
		if !validArtifact(r.Artifact) {
			return errors.New("invalid artifact resource")
		}
		base.Artifact = r.Artifact
	case "external":
		if len(r.ResourceKey) < 1 || len(r.ResourceKey) > 256 || !utf8.ValidString(r.ResourceKey) || strings.ContainsFunc(r.ResourceKey, unicode.IsControl) {
			return errors.New("invalid external resource")
		}
		base.ResourceKey = r.ResourceKey
	default:
		return errors.New("unknown resource kind")
	}
	if base.Binding != r.Binding || base.Scope != r.Scope || !slices.Equal(base.Slots, r.Slots) || base.TaskID != r.TaskID || base.WorkspaceID != r.WorkspaceID || base.PathPrefix != r.PathPrefix || base.Artifact != r.Artifact || base.ResourceKey != r.ResourceKey {
		return errors.New("resource has members of another kind")
	}
	return nil
}

// Capacity's independent amounts are nonnegative. Runtime calibration ceilings
// are producer guards, not version-1 codec rules.
type Capacity struct {
	TurnSlots         int64 `json:"turn_slots"`
	LocalSlots        int64 `json:"local_slots"`
	BufferBytes       int64 `json:"buffer_bytes"`
	LocalUnits        int64 `json:"local_units"`
	MaxOperationBytes int64 `json:"max_operation_bytes"`
}

// ScopeKey omits ID for colony; other kinds require a UUIDv7.
type ScopeKey struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id,omitzero"`
}

// PrivateReadRef records the exact requested private revision and page.
type PrivateReadRef struct {
	Scope    string `json:"scope"`
	Slot     string `json:"slot"`
	Revision int64  `json:"revision"`
	Cursor   int64  `json:"cursor"`
	MaxBytes int64  `json:"max_bytes"`
}

// SelectedObject identifies a returned head or transcript occurrence, in order.
type SelectedObject struct {
	SHA256   Digest `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Revision int64  `json:"revision"`
	Sequence int64  `json:"sequence"`
}

func encodeScopedPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxScopedPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxScopedPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}

func decodeScopedPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxScopedPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxScopedPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

func scopedToken(s string) bool   { n, ok := dottedTokens(s); return ok && n == 1 && len(s) <= 64 }
func validBinding(b Binding) bool { return isV7(b.AgentID) && isV7(b.ContextID) }
func validOrigin(o Origin) bool   { return isV7(o.AgentID) && isV7(o.SessionID) && isV7(o.ContextID) }
func validArtifact(a ArtifactRef) bool {
	return a.SourceSequence > 0 && a.SHA256 != (Digest{}) && a.Bytes >= 0
}
func validObject(a ObjectRef) bool { return a.SHA256 != (Digest{}) && a.Bytes >= 0 }
func validCapacity(c Capacity) bool {
	return c.TurnSlots >= 0 && c.LocalSlots >= 0 && c.BufferBytes >= 0 && c.LocalUnits >= 0 && c.MaxOperationBytes >= 0
}
func nextScopedVersion(expected, version int64) bool {
	return expected >= 0 && expected < math.MaxInt64 && version == expected+1
}
func validTaskBinding(t *TaskBinding) bool {
	return t == nil || isV7(t.TaskID) && isV7(t.AssignmentID) && t.Generation > 0
}
func validAssignment(a *Assignment) bool {
	return a == nil || isV7(a.AssignmentID) && a.Generation > 0 && validBinding(a.Assignee) && (a.AssignerActor == "owner" || a.AssignerActor == "agent") && validOrigin(a.Assigner)
}
func strictScopedStrings(ss []string) bool {
	for i, s := range ss {
		if i > 0 && ss[i-1] >= s {
			return false
		}
	}
	return true
}
func scopedIDs(ids []uuid.UUID, empty bool) bool {
	if ids == nil || !empty && len(ids) == 0 {
		return false
	}
	for i, id := range ids {
		if !isV7(id) || i > 0 && bytes.Compare(ids[i-1][:], id[:]) >= 0 {
			return false
		}
	}
	return true
}
func scopeRank(kind string) int {
	return slices.Index([]string{"colony", "agent", "context", "grant"}, kind)
}
func validScope(s ScopeKey) bool {
	return scopeRank(s.Kind) >= 0 && (s.Kind == "colony" && s.ID == (uuid.UUID{}) || s.Kind != "colony" && isV7(s.ID))
}
func validScopes(ss []ScopeKey) bool {
	if len(ss) < 4 {
		return false
	}
	for i, s := range ss {
		if !validScope(s) {
			return false
		}
		if i > 0 {
			p := ss[i-1]
			if scopeRank(p.Kind) > scopeRank(s.Kind) || p.Kind == s.Kind && bytes.Compare(p.ID[:], s.ID[:]) >= 0 {
				return false
			}
		}
	}
	return true
}
func validPrivateRead(r PrivateReadRef) bool {
	return privateSlot(r.Scope, r.Slot) && r.Revision > 0 && r.Cursor >= 0 && (r.Slot == "transcript" || r.Cursor == 0) && r.MaxBytes > 0
}
func validSubject(s *SubjectRef) bool {
	if s == nil {
		return true
	}
	_, err := s.MarshalJSON()
	return err == nil
}

func optionalObject[T any](m *members, key string, decode func(*members) T) *T {
	if _, ok := m.get(key, false); !ok {
		return nil
	}
	p := graphObject(m, key, decode)
	return &p
}
func optionalScopedNat(m *members, key string) *int64 {
	if _, ok := m.get(key, false); !ok {
		return nil
	}
	n := m.nat(key, true)
	return &n
}
func forbidScoped(m *members, keys ...string) {
	for _, key := range keys {
		if _, ok := m.m[key]; ok && m.err == nil {
			m.err = fmt.Errorf("%s is forbidden", key)
		}
	}
}
func readBinding(m *members) Binding { return Binding{m.id("agent_id"), m.id("context_id")} }
func readOrigin(m *members) Origin {
	return Origin{m.id("agent_id"), m.id("session_id"), m.id("context_id")}
}
func readArtifact(m *members) ArtifactRef {
	return ArtifactRef{m.nat("source_sequence", true), m.digest("sha256"), m.nat("bytes", true)}
}
func readObject(m *members) ObjectRef { return ObjectRef{m.digest("sha256"), m.nat("bytes", true)} }
func readTaskBinding(m *members) TaskBinding {
	return TaskBinding{m.id("task_id"), m.id("assignment_id"), m.nat("generation", true)}
}
func readAssignment(m *members) Assignment {
	return Assignment{m.id("assignment_id"), m.nat("generation", true), graphObject(m, "assignee", readBinding), m.text("assigner_actor"), graphObject(m, "assigner", readOrigin)}
}
func readCapacity(m *members) Capacity {
	return Capacity{m.nat("turn_slots", true), m.nat("local_slots", true), m.nat("buffer_bytes", true), m.nat("local_units", true), m.nat("max_operation_bytes", true)}
}
func readScope(m *members) ScopeKey {
	s := ScopeKey{Kind: m.text("kind")}
	if s.Kind == "colony" {
		forbidScoped(m, "id")
	} else {
		s.ID = m.id("id")
	}
	return s
}
func readPrivateRead(m *members) PrivateReadRef {
	return PrivateReadRef{m.text("scope"), m.text("slot"), m.nat("revision", true), m.nat("cursor", true), m.nat("max_bytes", true)}
}
func readSelected(m *members) SelectedObject {
	return SelectedObject{m.digest("sha256"), m.nat("bytes", true), m.nat("revision", true), m.nat("sequence", true)}
}
func readScopedStrings(m *members, key string) []string {
	return graphArray(m, key, func(b *members) string { return b.text("element") })
}
func readScopedObjects[T any](m *members, key string, decode func(*members) T) []T {
	return graphArray(m, key, func(b *members) T { return graphObject(b, "element", decode) })
}
func readResource(m *members) Resource {
	r := Resource{Kind: m.text("kind")}
	allowed := []string{"kind"}
	switch r.Kind {
	case "context":
		r.Binding = graphObject(m, "binding", readBinding)
		r.Scope = m.text("scope")
		r.Slots = readScopedStrings(m, "slots")
		allowed = append(allowed, "binding", "scope", "slots")
	case "task":
		r.TaskID, _ = m.optionalID("task_id")
		allowed = append(allowed, "task_id")
	case "workspace":
		r.WorkspaceID = m.id("workspace_id")
		r.Binding = graphObject(m, "binding", readBinding)
		r.PathPrefix = m.text("path_prefix")
		allowed = append(allowed, "workspace_id", "binding", "path_prefix")
	case "artifact":
		r.Artifact = graphObject(m, "artifact", readArtifact)
		allowed = append(allowed, "artifact")
	case "external":
		r.ResourceKey = m.text("resource_key")
		allowed = append(allowed, "resource_key")
	}
	for _, key := range []string{"binding", "scope", "slots", "task_id", "workspace_id", "path_prefix", "artifact", "resource_key"} {
		if !slices.Contains(allowed, key) {
			forbidScoped(m, key)
		}
	}
	return r
}

func scopedChanges(changes []PrivateChange) []privateChangeWire {
	out := make([]privateChangeWire, len(changes))
	for i, c := range changes {
		out[i] = privateChangeWire{Slot: c.Slot, Mode: c.Mode, SHA256: c.SHA256, PreviousSHA256: c.PreviousSHA256}
		if c.Mode != "clear" {
			out[i].Bytes = &changes[i].Bytes
		}
	}
	return out
}
func readScopedChanges(m *members, submission bool) []PrivateChange {
	return readScopedObjects(m, "changes", func(b *members) PrivateChange {
		c := PrivateChange{Slot: b.text("slot"), Mode: b.text("mode")}
		if c.Mode == "clear" {
			forbidScoped(b, "sha256", "bytes", "previous_sha256")
		} else {
			c.SHA256 = b.digest("sha256")
			c.Bytes = b.nat("bytes", true)
			if submission {
				forbidScoped(b, "previous_sha256")
			} else {
				c.PreviousSHA256, _ = b.optionalDigest("previous_sha256")
			}
		}
		return c
	})
}

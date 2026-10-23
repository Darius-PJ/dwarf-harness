package agents

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// These values describe authority, not ledger records or filesystem handles.
// Workspace containment is lexical only; an executor must separately check links.
type grantResource struct {
	kind            string
	binding         Binding
	scope           string
	slots           []string
	task, workspace uuid.UUID
	path            string
	artifact        artifactIdentity
	key             string
}

type artifactIdentity struct {
	SourceSequence uint64        `json:"source_sequence"`
	SHA256         ledger.Digest `json:"sha256"`
	Bytes          int64         `json:"bytes"`
}

type scopedBinding struct {
	AgentID   uuid.UUID `json:"agent_id"`
	ContextID uuid.UUID `json:"context_id"`
}

const maxResourcePathBytes = 1024

func validScopedPath(path string) bool {
	if len(path) > maxResourcePathBytes || !utf8.ValidString(path) || strings.ContainsAny(path, `\:`) {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return false
		}
	}
	if path == "" {
		return true
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func resourceContains(parent, child grantResource) bool {
	if parent.kind != child.kind {
		return false
	}
	switch parent.kind {
	case "context":
		return parent.binding == child.binding && parent.scope == child.scope && stringSubset(parent.slots, child.slots)
	case "task":
		return parent.task == (uuid.UUID{}) || parent.task == child.task
	case "workspace":
		return parent.workspace == child.workspace && parent.binding == child.binding &&
			validScopedPath(parent.path) && validScopedPath(child.path) &&
			(parent.path == "" || child.path == parent.path || strings.HasPrefix(child.path, parent.path+"/"))
	case "artifact":
		return parent.artifact == child.artifact
	case "external":
		return parent.key == child.key
	default:
		return false
	}
}

func stringSubset(parent, child []string) bool {
	if !strictStrings(parent) || !strictStrings(child) {
		return false
	}
	for _, value := range child {
		if _, ok := slices.BinarySearch(parent, value); !ok {
			return false
		}
	}
	return true
}

func strictStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] >= values[i] {
			return false
		}
	}
	return true
}

func compareBinding(a, b Binding) int {
	if n := bytes.Compare(a.AgentID[:], b.AgentID[:]); n != 0 {
		return n
	}
	return bytes.Compare(a.ContextID[:], b.ContextID[:])
}

// MarshalJSON fixes the kind-conditional Resource order without defining a
// ledger record. Validation belongs to the caller's authority checks.
func (r grantResource) MarshalJSON() ([]byte, error) {
	binding := scopedBinding{r.binding.AgentID, r.binding.ContextID}
	switch r.kind {
	case "context":
		return json.Marshal(struct {
			Kind    string        `json:"kind"`
			Binding scopedBinding `json:"binding"`
			Scope   string        `json:"scope"`
			Slots   []string      `json:"slots"`
		}{r.kind, binding, r.scope, append([]string{}, r.slots...)})
	case "task":
		return json.Marshal(struct {
			Kind   string    `json:"kind"`
			TaskID uuid.UUID `json:"task_id,omitzero"`
		}{r.kind, r.task})
	case "workspace":
		return json.Marshal(struct {
			Kind        string        `json:"kind"`
			WorkspaceID uuid.UUID     `json:"workspace_id"`
			Binding     scopedBinding `json:"binding"`
			PathPrefix  string        `json:"path_prefix"`
		}{r.kind, r.workspace, binding, r.path})
	case "artifact":
		return json.Marshal(struct {
			Kind     string           `json:"kind"`
			Artifact artifactIdentity `json:"artifact"`
		}{r.kind, r.artifact})
	case "external":
		return json.Marshal(struct {
			Kind        string `json:"kind"`
			ResourceKey string `json:"resource_key"`
		}{r.kind, r.key})
	default:
		return nil, ErrInvalid
	}
}

func compareResource(a, b grantResource) int {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return cmp.Compare(string(left), string(right))
}

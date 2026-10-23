package agents

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Workspace containment is lexical only; an executor must separately check links.
func resourceContains(parent, child ledger.Resource) bool {
	if parent.Kind != child.Kind {
		return false
	}
	switch parent.Kind {
	case "context":
		return parent.Binding == child.Binding && parent.Scope == child.Scope && stringSubset(parent.Slots, child.Slots)
	case "task":
		return parent.TaskID == (uuid.UUID{}) || parent.TaskID == child.TaskID
	case "workspace":
		return parent.WorkspaceID == child.WorkspaceID && parent.Binding == child.Binding &&
			ledger.ValidResourcePath(parent.PathPrefix) && ledger.ValidResourcePath(child.PathPrefix) &&
			(parent.PathPrefix == "" || child.PathPrefix == parent.PathPrefix || strings.HasPrefix(child.PathPrefix, parent.PathPrefix+"/"))
	case "artifact":
		return parent.Artifact == child.Artifact
	case "external":
		return parent.ResourceKey == child.ResourceKey
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

func compareResource(a, b ledger.Resource) int {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return cmp.Compare(string(left), string(right))
}

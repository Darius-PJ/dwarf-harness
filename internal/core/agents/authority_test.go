package agents

import (
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScopedResourceContainment(t *testing.T) {
	t.Parallel()
	binding := ledger.Binding{AgentID: privateID(1), ContextID: privateID(2)}
	context := ledger.Resource{Kind: "context", Binding: binding, Scope: "context", Slots: []string{"handoff", "memory"}}
	workspace := ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(3), PathPrefix: "a/b"}
	digest, err := hashObject([]byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := hashObject([]byte("else"))
	if err != nil {
		t.Fatal(err)
	}
	artifact := ledger.Resource{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 1, SHA256: digest, Bytes: 4}}
	for _, row := range []struct {
		name          string
		parent, child ledger.Resource
		contains      bool
	}{
		{"context_equal", context, context, true},
		{"context_slots", context, ledger.Resource{Kind: "context", Binding: binding, Scope: "context", Slots: []string{"memory"}}, true},
		{"context_extra_slot", context, ledger.Resource{Kind: "context", Binding: binding, Scope: "context", Slots: []string{"transcript"}}, false},
		{"context_scope", context, ledger.Resource{Kind: "context", Binding: binding, Scope: "agent", Slots: []string{"memory"}}, false},
		{"context_binding", context, ledger.Resource{Kind: "context", Binding: ledger.Binding{AgentID: privateID(1), ContextID: privateID(4)}, Scope: "context", Slots: []string{"memory"}}, false},
		{"task_equal", ledger.Resource{Kind: "task", TaskID: privateID(5)}, ledger.Resource{Kind: "task", TaskID: privateID(5)}, true},
		{"task_unrestricted", ledger.Resource{Kind: "task"}, ledger.Resource{Kind: "task", TaskID: privateID(5)}, true},
		{"task_remove_binding", ledger.Resource{Kind: "task", TaskID: privateID(5)}, ledger.Resource{Kind: "task"}, false},
		{"task_other", ledger.Resource{Kind: "task", TaskID: privateID(5)}, ledger.Resource{Kind: "task", TaskID: privateID(6)}, false},
		{"workspace_equal", workspace, workspace, true},
		{"workspace_descendant", workspace, ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(3), PathPrefix: "a/b/c"}, true},
		{"workspace_string_prefix", workspace, ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(3), PathPrefix: "a/bc"}, false},
		{"workspace_root", ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(3)}, workspace, true},
		{"workspace_other", workspace, ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(4), PathPrefix: "a/b"}, false},
		{"workspace_binding", workspace, ledger.Resource{Kind: "workspace", Binding: ledger.Binding{AgentID: privateID(1), ContextID: privateID(4)}, WorkspaceID: privateID(3), PathPrefix: "a/b"}, false},
		{"workspace_invalid_target", workspace, ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(3), PathPrefix: "a/b/../secret"}, false},
		{"workspace_invalid_prefix", ledger.Resource{Kind: "workspace", Binding: binding, WorkspaceID: privateID(3), PathPrefix: "a/../a/b"}, workspace, false},
		{"artifact_equal", artifact, artifact, true},
		{"artifact_source", artifact, ledger.Resource{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 2, SHA256: digest, Bytes: 4}}, false},
		{"artifact_hash", artifact, ledger.Resource{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 1, SHA256: otherDigest, Bytes: 4}}, false},
		{"artifact_bytes", artifact, ledger.Resource{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 1, SHA256: digest, Bytes: 5}}, false},
		{"external_equal", ledger.Resource{Kind: "external", ResourceKey: "endpoint"}, ledger.Resource{Kind: "external", ResourceKey: "endpoint"}, true},
		{"external_other", ledger.Resource{Kind: "external", ResourceKey: "endpoint"}, ledger.Resource{Kind: "external", ResourceKey: "other"}, false},
		{"different_kinds", context, workspace, false},
		{"unknown_kind", ledger.Resource{Kind: "future"}, ledger.Resource{Kind: "future"}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := resourceContains(row.parent, row.child); got != row.contains {
				t.Fatalf("containment = %v, want %v", got, row.contains)
			}
		})
	}
}

func TestScopedArtifactDigestRequired(t *testing.T) {
	t.Parallel()
	digest, err := hashObject(nil)
	if err != nil {
		t.Fatal(err)
	}
	resource := ledger.Resource{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 1, SHA256: digest}}
	if !validResource(resource) {
		t.Fatal("valid empty artifact refused")
	}
	resource.Artifact.SHA256 = ledger.Digest{}
	if validResource(resource) {
		t.Fatal("artifact without its digest accepted")
	}
}

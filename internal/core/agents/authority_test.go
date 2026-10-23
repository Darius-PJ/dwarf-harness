package agents

import (
	"strings"
	"testing"
)

func TestScopedPathGrammar(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, path string
		valid      bool
	}{
		{"root", "", true}, {"relative", "a/b", true}, {"unicode", "é/文", true},
		{"byte_boundary", strings.Repeat("é", 512), true},
		{"too_many_bytes", strings.Repeat("é", 513), false},
		{"invalid_utf8", "a\xff", false}, {"absolute", "/a", false},
		{"unc", "//host/share", false}, {"drive", "C:/a", false},
		{"drive_relative", "C:a", false}, {"backslash", `a\b`, false},
		{"empty_component", "a//b", false}, {"trailing_slash", "a/", false},
		{"dot", "a/./b", false}, {"dotdot", "a/../b", false},
		{"nul", "a\x00b", false}, {"control", "a\x1fb", false},
		{"delete", "a\x7fb", false}, {"unicode_control", "a\u0085b", false},
		{"git_root", ".git", false}, {"git_component", "a/.GIT/b", false},
		{"git_suffix", "a/.gitignore", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := validScopedPath(row.path); got != row.valid {
				t.Fatalf("path %q valid = %v, want %v", row.path, got, row.valid)
			}
		})
	}
}

func TestScopedResourceContainment(t *testing.T) {
	t.Parallel()
	binding := Binding{privateID(1), privateID(2)}
	context := grantResource{kind: "context", binding: binding, scope: "context", slots: []string{"handoff", "memory"}}
	workspace := grantResource{kind: "workspace", binding: binding, workspace: privateID(3), path: "a/b"}
	digest, err := hashObject([]byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := hashObject([]byte("else"))
	if err != nil {
		t.Fatal(err)
	}
	artifact := grantResource{kind: "artifact", artifact: artifactIdentity{SourceSequence: 1, SHA256: digest, Bytes: 4}}
	for _, row := range []struct {
		name          string
		parent, child grantResource
		contains      bool
	}{
		{"context_equal", context, context, true},
		{"context_slots", context, grantResource{kind: "context", binding: binding, scope: "context", slots: []string{"memory"}}, true},
		{"context_extra_slot", context, grantResource{kind: "context", binding: binding, scope: "context", slots: []string{"transcript"}}, false},
		{"context_scope", context, grantResource{kind: "context", binding: binding, scope: "agent", slots: []string{"memory"}}, false},
		{"context_binding", context, grantResource{kind: "context", binding: Binding{privateID(1), privateID(4)}, scope: "context", slots: []string{"memory"}}, false},
		{"task_equal", grantResource{kind: "task", task: privateID(5)}, grantResource{kind: "task", task: privateID(5)}, true},
		{"task_unrestricted", grantResource{kind: "task"}, grantResource{kind: "task", task: privateID(5)}, true},
		{"task_remove_binding", grantResource{kind: "task", task: privateID(5)}, grantResource{kind: "task"}, false},
		{"task_other", grantResource{kind: "task", task: privateID(5)}, grantResource{kind: "task", task: privateID(6)}, false},
		{"workspace_equal", workspace, workspace, true},
		{"workspace_descendant", workspace, grantResource{kind: "workspace", binding: binding, workspace: privateID(3), path: "a/b/c"}, true},
		{"workspace_string_prefix", workspace, grantResource{kind: "workspace", binding: binding, workspace: privateID(3), path: "a/bc"}, false},
		{"workspace_root", grantResource{kind: "workspace", binding: binding, workspace: privateID(3)}, workspace, true},
		{"workspace_other", workspace, grantResource{kind: "workspace", binding: binding, workspace: privateID(4), path: "a/b"}, false},
		{"workspace_binding", workspace, grantResource{kind: "workspace", binding: Binding{privateID(1), privateID(4)}, workspace: privateID(3), path: "a/b"}, false},
		{"workspace_invalid_target", workspace, grantResource{kind: "workspace", binding: binding, workspace: privateID(3), path: "a/b/../secret"}, false},
		{"workspace_invalid_prefix", grantResource{kind: "workspace", binding: binding, workspace: privateID(3), path: "a/../a/b"}, workspace, false},
		{"artifact_equal", artifact, artifact, true},
		{"artifact_source", artifact, grantResource{kind: "artifact", artifact: artifactIdentity{SourceSequence: 2, SHA256: digest, Bytes: 4}}, false},
		{"artifact_hash", artifact, grantResource{kind: "artifact", artifact: artifactIdentity{SourceSequence: 1, SHA256: otherDigest, Bytes: 4}}, false},
		{"artifact_bytes", artifact, grantResource{kind: "artifact", artifact: artifactIdentity{SourceSequence: 1, SHA256: digest, Bytes: 5}}, false},
		{"external_equal", grantResource{kind: "external", key: "endpoint"}, grantResource{kind: "external", key: "endpoint"}, true},
		{"external_other", grantResource{kind: "external", key: "endpoint"}, grantResource{kind: "external", key: "other"}, false},
		{"different_kinds", context, workspace, false},
		{"unknown_kind", grantResource{kind: "future"}, grantResource{kind: "future"}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := resourceContains(row.parent, row.child); got != row.contains {
				t.Fatalf("containment = %v, want %v", got, row.contains)
			}
		})
	}
}

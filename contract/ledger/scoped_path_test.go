package ledger

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
			if got := ValidResourcePath(row.path); got != row.valid {
				t.Fatalf("path %q valid = %v, want %v", row.path, got, row.valid)
			}
		})
	}
}

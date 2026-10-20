//go:build !windows

package graph

import (
	"os"
	"path/filepath"
	"testing"
)

func goTempAlias(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "symlink")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	return alias
}

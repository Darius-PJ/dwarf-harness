//go:build !windows

package validation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidationUnsupportedBeforeEffects(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "must-not-exist")
	if _, err := New(Config{RunsRoot: root}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("constructor refusal: %v", err)
	}
	var runner *Runner
	called := false
	if _, err := runner.Run(context.Background(), Attempt{}, Notices{Prepared: func(context.Context, Inputs) error { called = true; return nil }, Started: func(int) { called = true }}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("runner refusal: %v", err)
	}
	if called {
		t.Fatal("unsupported runner invoked an effect callback")
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported runner created a directory: %v", err)
	}
}

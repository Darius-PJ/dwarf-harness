package gitprocess

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectFences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "never-opened")
	if _, err := InspectFences(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created initial metadata: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := seedFence()
	identity, err := canonicalJSON(rootIdentity{Format: identityFormat, Version: 1, RootID: f.RootID})
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(root, "identity.json")
	if err := os.WriteFile(identityPath, identity, 0o600); err != nil {
		t.Fatal(err)
	}
	f.State, f.Proof = "closed", "name_absent"
	closed, err := canonicalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "runs", f.RunID.String()+".json")
	if err := os.WriteFile(path, closed, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectFences(ctx, root)
	if err != nil || len(report.Pending) != 0 || len(report.Closed) != 0 {
		t.Fatalf("closed fence: %+v %v", report, err)
	}
	f.State, f.Proof = "launch_pending", ""
	pending, err := canonicalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pending, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = InspectFences(ctx, root)
	if !errors.Is(err, ErrUnconfirmed) || len(report.Pending) != 1 || report.Pending[0].RunID != f.RunID {
		t.Fatalf("unclosed fence: %+v %v", report, err)
	}
	for name, want := range map[string][]byte{path: pending, identityPath: identity} {
		got, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("inspection changed fence bytes: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created owner lock: %v", err)
	}
	malformed := []byte("{truncated")
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectFences(ctx, root); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed fence: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, malformed) {
		t.Fatalf("inspection changed malformed fence: %v", err)
	}
}

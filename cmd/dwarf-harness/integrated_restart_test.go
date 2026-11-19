package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

var startupUnconfirmedFence func(*testing.T)

func integratedStartupSettings(t *testing.T, root string, origin ledger.Origin) string {
	t.Helper()
	t.Setenv("DWARF_INTEGRATED_SYNTHETIC_SOURCE", "synthetic-integration-only")
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: ledger.ToEnd, AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var scope uuid.UUID
	for scope == (uuid.UUID{}) {
		commit, err := reader.Next()
		if err != nil {
			t.Fatal("fixture credential scope is absent", err)
		}
		for _, record := range commit.Records {
			if record.Type == ledger.TypeRuntimeCredentialChanged {
				binding, err := ledger.DecodeRuntimeCredentialChanged(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				scope = binding.ScopeID
				break
			}
		}
	}
	settings := fmt.Sprintf(`{"schema":1,"contexts":[{"agent_id":%q,"context_id":%q,"route_id":%q,"expected_route_version":1,"credential_scope_id":%q,"max_output_tokens":32,"display_title":"General work","turn_policy":{}}],"credential_bindings":[{"scope_id":%q,"provider":"anthropic","source_kind":"environment","source_locator":"DWARF_INTEGRATED_SYNTHETIC_SOURCE"}]}`, origin.AgentID, origin.ContextID, uuid.NewV7(), scope, scope)
	name := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(name, []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func integratedStartupRun(t *testing.T, root, settings string) (int, string) {
	t.Helper()
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var output bytes.Buffer
	code := run([]string{"-colony-dir", root, "-settings", settings}, input, &output, nil)
	return code, output.String()
}

func testShutdownUnconfirmed(t *testing.T) {
	root, _, origin := restartPrefix(t, 99)
	settings := integratedStartupSettings(t, root, origin)
	var captured *agents.Service
	calls := 0
	workspaceCloseOverride = func(service *agents.Service, closeWorkspace func() error) error {
		calls++
		captured = service
		if err := closeWorkspace(); err != nil {
			t.Error(err)
		}
		return errors.New("synthetic unconfirmed closure detail")
	}
	defer func() { workspaceCloseOverride = nil }()
	code, output := integratedStartupRun(t, root, settings)
	const refusal = "[terminal] shutdown failed: closure unconfirmed\n"
	if code != 1 || calls != 1 || strings.Count(output, refusal) != 1 || !strings.Contains(output, "ready") || strings.Contains(output, "startup refused") {
		t.Fatalf("shutdown refusal was not the drained executable outcome: code=%d output=%s", code, output)
	}
	for _, forbidden := range []string{root, settings, "synthetic unconfirmed closure detail"} {
		if strings.Contains(output, forbidden) {
			t.Fatal("shutdown output disclosed a path or error value")
		}
	}
	if captured == nil {
		t.Fatal("workspace drain was skipped")
	}
	view, err := captured.OpenSession(context.Background(), agents.Binding{AgentID: origin.AgentID, ContextID: origin.ContextID})
	if view != nil {
		view.Close()
	}
	if !errors.Is(err, agents.ErrClosed) {
		t.Fatal("service teardown did not finish before the workspace failure", err)
	}
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal("store drain did not run after the workspace failure", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

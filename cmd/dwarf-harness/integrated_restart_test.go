package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
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

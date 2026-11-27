package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func serviceStartupFixture(t *testing.T, through uint64) (string, string, ledger.Origin, agents.ServiceSnapshot) {
	t.Helper()
	root, _, origin := startupPrefix(t, "91", through)
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	service, _, err := agents.Open(context.Background(), store, root, agents.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Settle the fixture's recorded source and old grace before startup. The
	// refused startup itself must neither invent a cause nor clear one.
	if _, err := service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.ServiceStatusSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	name := integratedStartupSettings(t, root, origin)
	return root, name, origin, snapshot
}

func serviceStartupRecords(t *testing.T, root string) []ledger.Record {
	t.Helper()
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
	var records []ledger.Record
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return records
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, commit.Records...)
	}
}

func requireServiceStartupRefusal(t *testing.T, code int, output, stage, class string, snapshot agents.ServiceSnapshot) {
	t.Helper()
	line := "[terminal] startup refused: " + stage + ": "
	refusal := strings.Index(output, line+"code="+class+" ")
	status := strings.Index(output, "service-status")
	if code != 1 || status < 0 || refusal < status || strings.Count(output, line) != 1 || strings.Contains(output, " ready ") {
		t.Fatalf("snapshot did not precede the fixed refusal: code=%d output=%s", code, output)
	}
	for _, agent := range snapshot.Agents {
		if !strings.Contains(output[:refusal], "agent="+agent.AgentID.String()) {
			t.Fatalf("refused startup omitted registered agent %s", agent.AgentID)
		}
	}
	for _, row := range snapshot.Outages {
		if !strings.Contains(output[:refusal], "outage="+row.OutageID.String()) || !strings.Contains(output[:refusal], "agent="+row.AgentID.String()) {
			t.Fatal("refused startup omitted recorded outage membership")
		}
	}
	if !strings.Contains(output[:refusal], "state=service_loss") {
		t.Fatal("refused startup hid recorded service loss")
	}
}

func TestServiceStartupRefusal(t *testing.T) {
	t.Run("integrity", func(t *testing.T) {
		root, settings, origin, snapshot := serviceStartupFixture(t, 142)
		var directory, digest string
		for _, record := range serviceStartupRecords(t, root) {
			if record.AgentID != origin.AgentID {
				continue
			}
			if record.Type == ledger.TypeRuntimeAgentRegistered {
				v, err := ledger.DecodeRuntimeAgentRegistered(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				directory = v.Directory
			}
			var revision ledger.PrivateRevision
			var err error
			switch record.Type {
			case ledger.TypePrivateRevision:
				revision, err = ledger.DecodePrivateRevision(record.Payload)
			case ledger.TypePrivateScopedRevision:
				var scoped ledger.PrivateScopedRevision
				scoped, err = ledger.DecodePrivateScopedRevision(record.Payload)
				revision = ledger.PrivateRevision(scoped)
			}
			if err != nil {
				t.Fatal(err)
			}
			if revision.Scope == "context" && record.ContextID == origin.ContextID {
				for _, change := range revision.Changes {
					if change.Mode != "clear" {
						digest = change.SHA256.String()
					}
				}
			}
		}
		if directory == "" || digest == "" {
			t.Fatal("fixture lacks an authoritative context object")
		}
		if err := os.Remove(filepath.Join(root, "agents", directory, "contexts", origin.ContextID.String(), "core", "objects", digest)); err != nil {
			t.Fatal(err)
		}
		code, output := integratedStartupRun(t, root, settings)
		requireServiceStartupRefusal(t, code, output, "turn loop", "private_integrity", snapshot)
		if !strings.Contains(output, "private_integrity") {
			t.Fatal("integrity refusal omitted typed condition")
		}
		for _, record := range serviceStartupRecords(t, root) {
			if record.Seq > snapshot.ThroughSequence && strings.HasPrefix(record.Type, "runtime.service_") {
				t.Fatal("integrity startup invented or cleared service loss")
			}
		}
	})
}

func TestServiceStartupNormal(t *testing.T) {
	// Other executable tests own process-wide seams; keep startup serialized.
	root, settings, _, snapshot := serviceStartupFixture(t, 142)
	code, output := integratedStartupRun(t, root, settings)
	status, ready := strings.Index(output, "service-status"), strings.Index(output, " ready ")
	if code != 0 || status < 0 || ready < status || !strings.Contains(output, "state=service_loss") {
		t.Fatalf("normal startup did not show passive status before Run: code=%d output=%s", code, output)
	}
	for _, record := range serviceStartupRecords(t, root) {
		if record.Seq > snapshot.ThroughSequence && strings.HasPrefix(record.Type, "runtime.service_") {
			t.Fatal("normal status replay repeated a provider effect or notice")
		}
	}
}

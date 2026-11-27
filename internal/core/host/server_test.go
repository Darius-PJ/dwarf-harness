package host

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
)

func TestHandshakeRefusals(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "colony")
	store, err := ledgerstore.Create(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	candidate, failure := NewCandidate(root)
	if failure != nil {
		t.Fatal(failure)
	}
	t.Cleanup(func() {
		if err := candidate.Abort(); err != nil {
			t.Error(err)
		}
	})
	status := candidate.Status()
	owner, err := localipc.CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := owner.ReadDiscovery(root, status.ColonyID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name    string
		version int64
		colony  uuid.UUID
		code    launcher.Code
	}{
		{"incompatible", launcher.DraftVersion + 1, status.ColonyID, launcher.Incompatible},
		{"wrong_colony", launcher.DraftVersion, uuid.NewV7(), launcher.WrongColony},
	} {
		t.Run(row.name, func(t *testing.T) {
			conn, err := owner.Dial(discovery)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			raw, err := json.Marshal(launcher.Hello{Kind: "hello", Versions: []int64{row.version}, Profile: "base", ColonyID: row.colony, ConnectionID: uuid.NewV7()})
			if err != nil {
				t.Fatal(err)
			}
			if err := localipc.WriteFrame(conn, raw); err != nil {
				t.Fatal(err)
			}
			raw, err = localipc.ReadFrame(conn, true)
			if err != nil {
				t.Fatal(err)
			}
			message, err := launcher.Decode(raw, "")
			if err != nil {
				t.Fatal(err)
			}
			refusal, ok := message.(launcher.Refused)
			if !ok || refusal.Code != row.code {
				t.Fatalf("handshake = %#v, want %s", message, row.code)
			}
		})
	}
}

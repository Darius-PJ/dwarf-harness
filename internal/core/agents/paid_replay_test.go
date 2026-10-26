package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func paidGoldenID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1300-0000-7000-8000-%012x", n))
}
func openPaidFixture(t testing.TB, prefix int) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	f, commits := scopedGoldenFixture(t, "13", prefix)
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return f, commits
}

func TestPaidGroupRulesAndFrozenReplay(t *testing.T) {
	t.Parallel()
	t.Run("rules", testPaidGroupRules)
	for _, chapter := range []string{"10", "11", "12", "13"} {
		t.Run("frozen-"+chapter, func(t *testing.T) {
			t.Parallel()
			f, commits := scopedGoldenFixture(t, chapter, -1)
			before := f.store.Durable()
			segment := filepath.Join(f.root, "log", ledger.SegmentName(1))
			original := readTestFile(t, segment)
			var err error
			f.service, _, err = openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) { t.Fatal("replay sampled a clock"); return 0, ErrInvalid }, beforeRead: func(Binding, string) error { t.Fatal("replay read paid content"); return ErrInvalid }})
			if err != nil {
				t.Fatal(err)
			}
			if f.store.Durable() != before || !reflect.DeepEqual(original, readTestFile(t, segment)) {
				t.Fatal("Open appended or rewrote the frozen ledger")
			}
			if chapter != "13" {
				return
			}
			for _, row := range []struct {
				id          int
				state, cost string
				version     int64
			}{{0x500, "released_unsent", "0", 2}, {0x501, "settled", "21", 5}, {0x502, "no_charge", "0", 3}, {0x503, "settled", "1", 3}, {0x504, "settled", "122", 4}, {0x505, "settled", "2", 3}} {
				view, err := f.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: paidGoldenID(row.id)})
				if err != nil || view.State != row.state || view.SettledMicros != row.cost || view.HeldMicros != "0" || view.Version != row.version || view.ThroughSequence != before {
					t.Fatalf("attempt %x: %+v %v", row.id, view, err)
				}
			}
			for _, row := range []struct{ period, cost string }{{"2026-10-24", "21"}, {"2026-10-25", "125"}} {
				view, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}, Period: row.period})
				if err != nil || view.SettledMicros != row.cost || view.HeldMicros != "0" || view.Fenced {
					t.Fatalf("period %s: %+v %v", row.period, view, err)
				}
			}
			local, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || local.Used.LocalUnits != 2 {
				t.Fatalf("paid delegation lost shared local unit: %+v %v", local, err)
			}
			route := f.service.p.paidRoutes[paidGoldenID(0x400)]
			credential := f.service.p.paidCredentialScopes[paidGoldenID(0x401)]
			if route.Version != 3 || route.Profile.CapabilityVersion != "local-cap-3" || credential.version != 3 || credential.provider != "anthropic" || len(f.service.p.paidCredentials) != 0 {
				t.Fatal("route identity or credential rotation/revocation was lost")
			}
			budget, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "credential", ID: paidGoldenID(0x401)}})
			if err != nil || budget.Version != 2 || budget.Terms == nil || *budget.Terms.CeilingMicros != 900000 {
				t.Fatalf("credential spend update lost: %+v %v", budget, err)
			}
			for _, commit := range commits {
				last := commit.Records[len(commit.Records)-1]
				if last.Type != ledger.TypeRuntimePaidReceipt {
					continue
				}
				recorded := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, last.Payload)
				receipt, err := f.service.PaidCommandStatus(context.Background(), PaidCommandLookup{CommandID: recorded.CommandID, SubmissionSHA256: recorded.SubmissionSHA256})
				if err != nil || !reflect.DeepEqual(receipt.RuntimePaidReceipt, recorded) || receipt.Sequence != commit.Last {
					t.Fatalf("frozen paid receipt changed: %+v %v", receipt, err)
				}
			}
			baseline := f.service.p
			for _, c := range commits {
				for _, r := range c.Records {
					if r.Type != ledger.TypeRuntimePaidResult {
						continue
					}
					result := scopedDecode(t, ledger.DecodeRuntimePaidResult, r.Payload)
					objects := append([]ledger.ObjectRef{}, result.MessageObjects...)
					if result.EvidenceObject != nil {
						objects = append(objects, *result.EvidenceObject)
					}
					for _, o := range objects {
						name := filepath.Join(f.root, "agents", "Ada-a1", "contexts", r.ContextID.String(), "core", "objects", o.SHA256.String())
						if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
							t.Fatal(err)
						}
					}
				}
			}
			reopened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatalf("missing output changed replay: %v", err)
			}
			defer reopened.Close()
			if !reflect.DeepEqual(baseline, reopened.p) {
				t.Fatal("output availability changed ledger money or state")
			}
		})
	}
}

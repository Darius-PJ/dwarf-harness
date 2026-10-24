package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestScopedBudgetDurabilityAndRelease(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name         string
		prefix       int
		used         int64
		held         Exposure
		localVersion int64
		localOutcome string
		turnPending  bool
	}{
		{"turn-admitted", 21, 4, Exposure{TurnSlots: 1}, 0, "", true},
		{"local-admitted-unit-held", 22, 4, Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024, LocalUnits: 1}, 1, "", true},
		{"dispatch-consumes-once", 23, 5, Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024}, 2, "", true},
		{"unknown-releases-nothing", 24, 5, Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024}, 3, "unknown", true},
		{"proven-failure-releases-not-refunds", 25, 5, Exposure{TurnSlots: 1}, 0, "", true},
		{"turn-ended", 26, 5, Exposure{}, 0, "", false},
		{"capacity-lowered-below-consumption", 39, 8, Exposure{}, 0, "", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, commits := scopedGoldenFixture(t, "11", row.prefix)
			before := f.store.Durable()
			var report Reconciliation
			var err error
			f.service, report, err = Open(ctx, f.store, f.root, DefaultLimits())
			if err != nil || len(report.Issues) != 0 || f.store.Durable() != before {
				t.Fatalf("inert occupied-prefix Open: %+v %v", report, err)
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Used != (Exposure{LocalUnits: row.used}) || book.Held != row.held {
				t.Fatalf("durable exposure: %+v want used %d held %+v; %v", book, row.used, row.held, err)
			}
			if row.prefix == 39 && (book.Limits.LocalUnits != 2 || book.Available == nil || book.Available.LocalUnits != 0) {
				t.Fatalf("lowering reset or underflowed consumption: %+v", book)
			}
			wantPending := 0
			if row.turnPending {
				wantPending++
			}
			if row.localVersion != 0 {
				wantPending++
			}
			if len(report.PendingWork) != wantPending {
				t.Fatalf("pending work inferred a completion or lost a hold: %+v", report.PendingWork)
			}
			for _, work := range report.PendingWork {
				if work.State != "recovery_pending" {
					t.Fatalf("Open resumed work: %+v", work)
				}
				switch work.Admission.WorkID {
				case scopedGoldenID(0x500):
					if !row.turnPending || work.Version != 1 || work.Sequence != 69 || work.Held != (Exposure{TurnSlots: 1}) {
						t.Fatalf("turn recovery identity/exposure: %+v", work)
					}
				case scopedGoldenID(0x501):
					want := row.held
					want.TurnSlots = 0
					if work.Version != row.localVersion || work.Outcome != row.localOutcome || work.Sequence != 72 || work.Held != want {
						t.Fatalf("local recovery identity/exposure: %+v", work)
					}
				default:
					t.Fatalf("unexpected pending work: %+v", work)
				}
			}
			origin := Binding{AgentID: commits[0].Records[0].AgentID, ContextID: commits[0].Records[0].ContextID}
			general := scopedBound(t, f.service, origin)
			parent, err := general.CapacityStatus(ctx, GrantLookup{GrantID: scopedGoldenID(0x300)})
			if err != nil || parent.Used != (Exposure{LocalUnits: 1}) || parent.Held != (Exposure{}) || parent.Available != nil {
				t.Fatalf("ancestor view disclosed descendant-origin usage or shared availability: %+v %v", parent, err)
			}
			if row.prefix == 39 && parent.Band != "busy" {
				t.Fatalf("exhausted current authority reported denial or admission: %+v", parent)
			}
			parentOwner, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "grant", ID: scopedGoldenID(0x300)}})
			parentUsed := int64(1)
			if row.prefix >= 23 {
				parentUsed = 2
			}
			parentHeld := row.held
			parentHeld.TurnSlots = 0
			if err != nil || parentOwner.Used != (Exposure{LocalUnits: parentUsed}) || parentOwner.Held != parentHeld {
				t.Fatalf("child received a separate ancestor allowance: %+v %v", parentOwner, err)
			}
			worker := scopedBound(t, f.service, Binding{AgentID: origin.AgentID, ContextID: commits[1].Records[0].ContextID})
			child, err := worker.CapacityStatus(ctx, GrantLookup{GrantID: scopedGoldenID(0x302)})
			childUsed := int64(0)
			if row.prefix >= 23 {
				childUsed = 1
			}
			if err != nil || child.Used != (Exposure{LocalUnits: childUsed}) || child.Held != parentHeld || child.Available != nil {
				t.Fatalf("originating child exposure lost on replay/retirement: %+v %v", child, err)
			}
			if row.prefix == 39 && child.Band != "denied" {
				t.Fatalf("revocation reported exhaustion instead of denial: %+v", child)
			}
			if f.store.Durable() != before {
				t.Fatal("passive recovery queries appended records")
			}
		})
	}
	t.Run("reconfiguration-and-restart-preserve-over-limit-holds", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f, commits := scopedGoldenFixture(t, "11", 23)
		var err error
		f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		origin := Binding{AgentID: commits[0].Records[0].AgentID, ContextID: commits[0].Records[0].ContextID}
		agentScope := ledger.ScopeKey{Kind: "agent", ID: origin.AgentID}
		current, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: agentScope})
		if err != nil {
			t.Fatal(err)
		}
		larger := current.Limits
		larger.TurnSlots = 4
		request := CapacitySpec{CommandID: privateID(900), ExpectedVersion: 1, Scope: agentScope, Limits: larger}
		before := f.store.Durable()
		if _, err := f.service.ConfigureCapacity(ctx, origin, request); !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
			t.Fatalf("agent turn cap did not use the default ceiling: %v", err)
		}
		limits := DefaultLimits()
		limits.MaxAgentTurnSlots = 4
		reopenScoped(t, f, limits)
		if _, err := f.service.ConfigureCapacity(ctx, origin, request); err != nil {
			t.Fatalf("agent turn cap is a constant rather than Limits: %v", err)
		}
		if _, err := f.service.ConfigureCapacity(ctx, origin, CapacitySpec{CommandID: privateID(901), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: ledger.Capacity{}}); err != nil {
			t.Fatal(err)
		}
		limits.MaxAgentTurnSlots = 1
		report := reopenScoped(t, f, limits)
		if len(report.PendingWork) != 2 {
			t.Fatalf("lower runtime/configuration limits lost occupied work: %+v", report.PendingWork)
		}
		assertExposure := func(held Exposure) {
			t.Helper()
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Version != 2 || book.Limits != (ledger.Capacity{}) || book.Held != held || book.Used != (Exposure{LocalUnits: 5}) || book.Available == nil || *book.Available != (Exposure{}) {
				t.Fatalf("configuration/restart reset or clipped occupied exposure: %+v %v", book, err)
			}
		}
		assertExposure(Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024})
		request.CommandID, request.ExpectedVersion, request.Limits.TurnSlots = privateID(902), 2, 2
		before = f.store.Durable()
		if _, err := f.service.ConfigureCapacity(ctx, origin, request); !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
			t.Fatalf("lower Limits ceiling was not a new-producer guard: %v", err)
		}
		stale := CapacitySpec{CommandID: privateID(903), ExpectedVersion: 1, Scope: ledger.ScopeKey{Kind: "colony"}, Limits: scopedCapacity()}
		if _, err := f.service.ConfigureCapacity(ctx, origin, stale); !errors.Is(err, ErrRevision) || f.store.Durable() != before {
			t.Fatalf("configuration stale CAS reset exposure: %v", err)
		}
		appendScopedFixture(t, f, scopedGoldenDrafts(commits[23]))
		assertExposure(Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024})
		report = reopenScoped(t, f, limits)
		if len(report.PendingWork) != 2 || report.PendingWork[1].Outcome != "unknown" {
			t.Fatalf("unknown ending became release on restart: %+v", report.PendingWork)
		}
		appendScopedFixture(t, f, scopedGoldenDrafts(commits[24]))
		assertExposure(Exposure{TurnSlots: 1})
		appendScopedFixture(t, f, scopedGoldenDrafts(commits[25]))
		assertExposure(Exposure{})
		report = reopenScoped(t, f, limits)
		if len(report.PendingWork) != 0 {
			t.Fatalf("proven endings did not retire occupied work: %+v", report.PendingWork)
		}
		assertExposure(Exposure{})
	})
	t.Run("producer-admission-dispatch-and-proven-release", testScopedAdmissionAccounting)
}

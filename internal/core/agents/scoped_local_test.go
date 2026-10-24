package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// The frozen prefix supplies a real pinned workspace, dedicated scratch, active
// assignment, and attenuated file grant. New work below uses the public API.
func scopedLocalFixture(t *testing.T, limits Limits) (*serviceFixture, *Agent, TurnAdmission, LocalAdmission) {
	t.Helper()
	f, commits := scopedGoldenFixture(t, "11", 20)
	var err error
	f.service, _, err = Open(context.Background(), f.store, f.root, limits)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := ledger.DecodeRuntimeWorkAdmitted(commits[20].Records[1].Payload)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ledger.DecodeRuntimeWorkAdmitted(commits[21].Records[1].Payload)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{AgentID: commits[20].Records[0].AgentID, ContextID: commits[20].Records[0].ContextID}
	start := TurnAdmission{CommandID: privateID(9000), TurnID: privateID(9001), GrantID: turn.GrantID, TaskBinding: turn.TaskBinding}
	admit := LocalAdmission{CommandID: privateID(9002), WorkID: privateID(9003), ParentTurnID: start.TurnID, GrantID: local.GrantID, LocalOperation: local.Operation, Target: local.Target, ArgumentsSHA256: local.ArgumentsSHA256, ToolSchema: *local.ToolSchema, MaxBytes: *local.MaxBytes, TaskBinding: local.TaskBinding}
	return f, scopedBound(t, f.service, binding), start, admit
}

func TestScopedDispatchRevocationRace(t *testing.T) {
	t.Parallel()
	for _, revokeFirst := range []bool{true, false} {
		name := "dispatch_before_ancestor_revocation"
		if revokeFirst {
			name = "ancestor_revocation_before_dispatch"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, agent, start, admit := scopedLocalFixture(t, DefaultLimits())
			if _, err := agent.StartTurn(ctx, start); err != nil {
				t.Fatal(err)
			}
			if _, err := agent.AdmitLocal(ctx, admit); err != nil {
				t.Fatal(err)
			}
			request := LocalDispatch{CommandID: privateID(9010), WorkID: admit.WorkID, ExpectedVersion: 1, ArgumentsSHA256: admit.ArgumentsSHA256}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var block atomic.Bool
			f.service.deps.beforeLookup = func() {
				if block.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
			}
			var first ScopedReceipt
			result := make(chan error, 1)
			if revokeFirst {
				block.Store(true)
				go func() { _, err := f.service.BeginLocal(ctx, request); result <- err }()
				<-entered
			} else {
				var err error
				first, err = f.service.BeginLocal(ctx, request)
				if err != nil || !reflect.DeepEqual(first.Target, admit.Target) {
					t.Fatalf("dispatch lost the admitted target: %+v %v", first, err)
				}
			}
			origin := Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}
			if _, err := f.service.RevokeGrant(ctx, origin, GrantRevocation{CommandID: privateID(9011), RevokeGrantID: scopedGoldenID(0x300), ExpectedVersion: 1, ExpectedAuthorityVersion: 4}); err != nil {
				t.Fatal(err)
			}
			before := f.store.Durable()
			if revokeFirst {
				once.Do(func() { close(release) })
				if err := <-result; !errors.Is(err, ErrDenied) || f.store.Durable() != before {
					t.Fatalf("admission-time authority escaped revocation: %v", err)
				}
			} else if _, err := f.service.BeginLocal(ctx, request); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
				t.Fatalf("dispatch replay became a reusable permit: %v", err)
			}
			status, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: admit.WorkID})
			if err != nil || status.Dispatched == revokeFirst || status.Ended {
				t.Fatalf("dispatch boundary: %+v %v", status, err)
			}
			outcome, version := "released_unsent", uint64(1)
			if !revokeFirst {
				outcome, version = "failed", 2
			}
			if _, err := f.service.EndLocal(ctx, LocalEnding{CommandID: privateID(9012), WorkID: admit.WorkID, ExpectedVersion: version, Outcome: outcome}); err != nil {
				t.Fatalf("honest host ending required revoked authority: %v", err)
			}
			if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(9013), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
				t.Fatal(err)
			}
			reopenScoped(t, f, DefaultLimits())
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "grant", ID: admit.GrantID}})
			used := int64(0)
			if !revokeFirst {
				used = 1
			}
			if err != nil || book.Held != (Exposure{}) || book.Used != (Exposure{LocalUnits: used}) {
				t.Fatalf("dispatch charge/refund survived incorrectly: %+v %v", book, err)
			}
		})
	}
}

func testScopedAdmissionAccounting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	limits := DefaultLimits()
	limits.MaxPendingLocalWork = 1
	f, agent, start, admit := scopedLocalFixture(t, limits)
	if _, err := agent.StartTurn(ctx, start); err != nil {
		t.Fatal(err)
	}
	unsupported := admit
	unsupported.ToolSchema = 2
	beforeSchema := f.store.Durable()
	if _, err := agent.AdmitLocal(ctx, unsupported); err != ErrInvalid || f.store.Durable() != beforeSchema {
		t.Fatalf("producer admitted an unsupported tool schema: %v", err)
	}
	if _, err := agent.AdmitLocal(ctx, admit); err != nil {
		t.Fatal(err)
	}
	assertExposure := func(held Exposure, used int64) {
		t.Helper()
		view, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "grant", ID: admit.GrantID}})
		if err != nil || view.Held != held || view.Used != (Exposure{LocalUnits: used}) {
			t.Fatalf("local exposure: %+v want %+v/%d; %v", view, held, used, err)
		}
	}
	held := Exposure{LocalSlots: 1, BufferBytes: admit.MaxBytes, LocalUnits: 1}
	assertExposure(held, 0)
	second := admit
	second.CommandID, second.WorkID = privateID(9020), privateID(9021)
	before := f.store.Durable()
	var busy *BusyError
	if _, err := agent.AdmitLocal(ctx, second); !errors.As(err, &busy) || f.store.Durable() != before {
		t.Fatalf("pending-work ceiling did not guard the producer: %v", err)
	}
	if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(9022), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "interrupted"}); !errors.As(err, &busy) || busy.Reason != "child_unended" || f.store.Durable() != before {
		t.Fatalf("parent ended before a child's ending record: %v", err)
	}
	dispatch := LocalDispatch{CommandID: privateID(9030), WorkID: admit.WorkID, ExpectedVersion: 1, ArgumentsSHA256: admit.ArgumentsSHA256}
	receipt, err := f.service.BeginLocal(ctx, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	held.LocalUnits = 0
	assertExposure(held, 1)
	before = f.store.Durable()
	if replay, err := f.service.BeginLocal(ctx, dispatch); err != nil || !reflect.DeepEqual(receipt, replay) || f.store.Durable() != before {
		t.Fatalf("dispatch retry charged again: %+v %v", replay, err)
	}
	unknown, err := f.service.EndLocal(ctx, LocalEnding{CommandID: privateID(9031), WorkID: admit.WorkID, ExpectedVersion: 2, Outcome: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	assertExposure(held, 1)
	if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(9032), TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
		t.Fatalf("unknown is an ending, not an unended child: %v", err)
	}
	report := reopenScoped(t, f, limits)
	if len(report.PendingWork) != 1 || report.PendingWork[0].Admission.WorkID != admit.WorkID || !report.PendingWork[0].Ended || !report.PendingWork[0].Dispatched {
		t.Fatalf("unknown held work was lost or conflated with unsent work: %+v", report.PendingWork)
	}
	assertExposure(held, 1)
	before = f.store.Durable()
	if replay, err := f.service.ReconcileAdmissions(ctx, EndingRequest{CommandID: privateID(9033), WorkID: admit.WorkID, ExpectedVersion: 3}); err != nil || !reflect.DeepEqual(replay, unknown) || f.store.Durable() != before {
		t.Fatalf("reconciliation guessed an unknown outcome: %+v %v", replay, err)
	}
	if _, err := f.service.EndLocal(ctx, LocalEnding{CommandID: privateID(9034), WorkID: admit.WorkID, ExpectedVersion: 3, Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	assertExposure(Exposure{}, 1)
	agent = scopedBound(t, f.service, Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context})
	start.CommandID, start.TurnID = privateID(9040), privateID(9041)
	if _, err := agent.StartTurn(ctx, start); err != nil {
		t.Fatal(err)
	}
	second.ParentTurnID = start.TurnID
	if _, err := agent.AdmitLocal(ctx, second); err != nil {
		t.Fatalf("busy consumed the second local work ID: %v", err)
	}
	held.LocalUnits = 1
	assertExposure(held, 1)
	if _, err := f.service.EndLocal(ctx, LocalEnding{CommandID: privateID(9042), WorkID: second.WorkID, ExpectedVersion: 1, Outcome: "released_unsent"}); err != nil {
		t.Fatal(err)
	}
	assertExposure(Exposure{}, 1)
}

func TestScopedRebuildAndInterruptedAdmissions(t *testing.T) {
	t.Parallel()
	for _, dispatched := range []bool{false, true} {
		name := "proven_unsent"
		if dispatched {
			name = "dispatch_outcome_unknown"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, agent, start, admit := scopedLocalFixture(t, DefaultLimits())
			if _, err := agent.StartTurn(ctx, start); err != nil {
				t.Fatal(err)
			}
			if _, err := agent.AdmitLocal(ctx, admit); err != nil {
				t.Fatal(err)
			}
			version := uint64(1)
			if dispatched {
				if _, err := f.service.BeginLocal(ctx, LocalDispatch{CommandID: privateID(9050), WorkID: admit.WorkID, ExpectedVersion: 1, ArgumentsSHA256: admit.ArgumentsSHA256}); err != nil {
					t.Fatal(err)
				}
				version = 2
			}
			before := f.store.Durable()
			occupied, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			report := reopenScoped(t, f, DefaultLimits())
			if len(report.PendingWork) != 2 || f.store.Durable() != before {
				t.Fatalf("Open inferred or dispatched work: %+v", report)
			}
			restored, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || !reflect.DeepEqual(restored, occupied) {
				t.Fatalf("Open reset occupied or consumed capacity: %+v want %+v; %v", restored, occupied, err)
			}
			local, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: admit.WorkID})
			if err != nil || local.Dispatched != dispatched || local.Ended || local.Held.LocalSlots != 1 {
				t.Fatalf("pending local state: %+v %v", local, err)
			}
			request := EndingRequest{CommandID: privateID(9051), WorkID: admit.WorkID, ExpectedVersion: version}
			ended, err := f.service.ReconcileAdmissions(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			before = f.store.Durable()
			if replay, err := f.service.ReconcileAdmissions(ctx, request); err != nil || !reflect.DeepEqual(replay, ended) || f.store.Durable() != before {
				t.Fatalf("repeated recovery repeated an effect: %+v %v", replay, err)
			}
			if _, err := f.service.ReconcileAdmissions(ctx, EndingRequest{CommandID: privateID(9052), WorkID: start.TurnID, ExpectedVersion: 1}); err != nil {
				t.Fatal(err)
			}
			turn, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: start.TurnID})
			if err != nil || !turn.Ended || turn.Outcome != "interrupted" || turn.Held != (Exposure{}) {
				t.Fatalf("unmatched turn was not interrupted: %+v %v", turn, err)
			}
			local, err = f.service.TurnStatus(ctx, WorkLookup{WorkID: admit.WorkID})
			want, held := "released_unsent", Exposure{}
			if dispatched {
				want, held = "unknown", Exposure{LocalSlots: 1, BufferBytes: admit.MaxBytes}
			}
			if err != nil || !local.Ended || local.Outcome != want || local.Held != held {
				t.Fatalf("recovery guessed a local effect: %+v %v", local, err)
			}
			report = reopenScoped(t, f, DefaultLimits())
			wantPending := 0
			if dispatched {
				wantPending = 1
			}
			if len(report.PendingWork) != wantPending {
				t.Fatalf("repeated Open changed retained exposure: %+v", report)
			}
		})
	}
}

func TestScopedNoExternalAuthorityFromLocalGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, agent, start, admit := scopedLocalFixture(t, DefaultLimits())
	if _, err := agent.StartTurn(ctx, start); err != nil {
		t.Fatal(err)
	}
	before := f.store.Durable()
	request := admit
	request.LocalOperation = "model.call"
	request.Target = ledger.Resource{Kind: "external", ResourceKey: "fixture_model"}
	request.ArgumentsSHA256 = ledger.Digest(sha256.Sum256([]byte("model arguments")))
	request.GrantID = start.GrantID
	if _, err := agent.AdmitLocal(ctx, request); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatalf("turn.start inferred model authority: %v", err)
	}
	request.GrantID = admit.GrantID
	if _, err := agent.AdmitLocal(ctx, request); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatalf("file authority became external authority: %v", err)
	}
	origin := Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}
	spec := GrantSpec{GrantID: privateID(9060), Grantee: origin, Operations: []string{"model.call"}, Resources: []ledger.Resource{request.Target}, DataClass: "external", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked", TaskBinding: admit.TaskBinding}
	issueScopedRoot(t, f.service, origin, 9061, spec)
	request.GrantID = spec.GrantID
	before = f.store.Durable()
	book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.AdmitLocal(ctx, request); !errors.Is(err, ErrUnavailable) || f.store.Durable() != before {
		t.Fatalf("external class acquired a producer: %v", err)
	}
	after, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
	if err != nil || after.Held != book.Held || after.Used != book.Used {
		t.Fatalf("external refusal reserved or charged: %+v %v", after, err)
	}
	if _, err := agent.AdmitLocal(ctx, admit); err != nil {
		t.Fatalf("external refusal consumed the command or work ID: %v", err)
	}
}

func testScopedOldContextTurnFence(t *testing.T) {
	t.Parallel()
	for _, owner := range []bool{false, true} {
		name := "host_end"
		if owner {
			name = "owner_cancel"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, agent, start, _ := scopedLocalFixture(t, DefaultLimits())
			binding := Binding{AgentID: agent.ids.agent, ContextID: agent.ids.context}
			next := scopedWorkContext(t, f.service, binding, 9110, 9111)
			cancelGrant := privateScopedGrant(binding, 9112, scopedCapacity(), "turn.cancel")
			issueScopedRoot(t, f.service, binding, 9113, cancelGrant)
			if _, err := agent.StartTurn(ctx, start); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ReassignTask(ctx, binding, TaskAssignment{
				CommandID: privateID(9114), TaskID: start.TaskBinding.TaskID, ExpectedVersion: 3,
				ExpectedAssignmentID: start.TaskBinding.AssignmentID, ExpectedGeneration: start.TaskBinding.Generation,
				Assignment: ledger.AssignmentInput{AssignmentID: privateID(9115), Assignee: ledger.Binding(next)},
			}); err != nil {
				t.Fatal(err)
			}
			view, err := f.service.GrantStatus(ctx, GrantLookup{GrantID: cancelGrant.GrantID})
			if err != nil || !view.Current {
				t.Fatalf("unbound cancel grant unexpectedly expired: %+v %v", view, err)
			}
			before := f.store.Durable()
			cancel := TurnCancellation{CommandID: privateID(9116), GrantID: cancelGrant.GrantID, TurnID: start.TurnID, ExpectedVersion: 1}
			if _, err := agent.CancelTurn(ctx, cancel); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
				t.Fatalf("old context escaped the reassignment fence: %v", err)
			}
			if owner {
				cancel.GrantID = start.GrantID
				if _, err := f.service.CancelTurn(ctx, binding, cancel); !errors.Is(err, ErrInvalid) {
					t.Fatalf("owner accepted a supplied admission grant: %v", err)
				}
				cancel.GrantID = [16]byte{}
				if _, err := f.service.CancelTurn(ctx, binding, cancel); err != nil {
					t.Fatalf("owner could not drain the obsolete assignment: %v", err)
				}
			} else if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: cancel.CommandID, TurnID: start.TurnID, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
				t.Fatalf("host ending required current assignment authority: %v", err)
			}
			reopenScoped(t, f, DefaultLimits())
			status, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: start.TurnID})
			if err != nil || !status.Ended || status.Held != (Exposure{}) {
				t.Fatalf("old assignment's hold was not released on replay: %+v %v", status, err)
			}
		})
	}
}

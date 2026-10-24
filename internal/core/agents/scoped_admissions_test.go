package agents

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func issueScopedRoot(t *testing.T, s *Service, origin Binding, command int, spec GrantSpec) ScopedReceipt {
	t.Helper()
	s.mu.Lock()
	authority := s.p.authority
	s.mu.Unlock()
	return scopedRoot(t, s, origin, uint64(authority), command, spec)
}

func privateScopedGrant(binding Binding, id int, capacity ledger.Capacity, operations ...string) GrantSpec {
	return GrantSpec{GrantID: privateID(id), Grantee: binding, Operations: operations, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"}
}

func TestScopedStartTurnAdmission(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, reason                        string
		contexts, agents, capacity, winners int
	}{
		{"one_per_context", "context_active", 1, 1, 3, 1},
		{"configured_agent_one", "agent_capacity", 2, 1, 1, 1},
		{"agent_three_across_contexts", "agent_capacity", 4, 1, 3, 3},
		{"colony_shared_across_agents", "colony_capacity", 2, 2, 1, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			origin := registerPrivate(t, f.service, 10)
			bindings := []Binding{origin}
			for i := 1; i < row.contexts; i++ {
				if row.agents > 1 {
					bindings = append(bindings, registerPrivate(t, f.service, 10+i*10))
				} else {
					bindings = append(bindings, scopedWorkContext(t, f.service, origin, 20+i*2, 21+i*2))
				}
			}
			// Turn capacity is independent of local slots, bytes, and units.
			capacity := ledger.Capacity{TurnSlots: int64(row.capacity), MaxOperationBytes: 1}
			colony := capacity
			if row.agents == 1 {
				colony.TurnSlots = 8
			}
			if _, err := f.service.ConfigureCapacity(ctx, origin, CapacitySpec{CommandID: privateID(100), Scope: ledger.ScopeKey{Kind: "colony"}, Limits: colony}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < row.agents; i++ {
				if _, err := f.service.ConfigureCapacity(ctx, origin, CapacitySpec{CommandID: privateID(110 + i), Scope: ledger.ScopeKey{Kind: "agent", ID: bindings[i].AgentID}, Limits: capacity}); err != nil {
					t.Fatal(err)
				}
			}
			agents := make([]*Agent, len(bindings))
			grants := make([]uuid.UUID, len(bindings))
			for i, binding := range bindings {
				grant := privateScopedGrant(binding, 200+i*2, ledger.Capacity{TurnSlots: 3}, "turn.start", "turn.inspect")
				issueScopedRoot(t, f.service, origin, 201+i*2, grant)
				agents[i], grants[i] = scopedBound(t, f.service, binding), grant.GrantID
			}
			count := row.winners + 1
			requests := make([]TurnAdmission, count)
			type result struct {
				receipt ScopedReceipt
				err     error
			}
			results := make([]result, count)
			start := make(chan struct{})
			var done sync.WaitGroup
			for i := range requests {
				requests[i] = TurnAdmission{CommandID: privateID(300 + i*2), GrantID: grants[i%len(grants)], TurnID: privateID(301 + i*2)}
				done.Go(func() {
					<-start
					results[i].receipt, results[i].err = agents[i%len(agents)].StartTurn(ctx, requests[i])
				})
			}
			close(start)
			done.Wait()
			winner, loser, successes := -1, -1, 0
			for i, result := range results {
				if result.err == nil {
					winner, successes = i, successes+1
					continue
				}
				var busy *BusyError
				if !errors.As(result.err, &busy) || busy.Reason != row.reason {
					t.Fatalf("contended admission %d: %v", i, result.err)
				}
				loser = i
			}
			if successes != row.winners || loser < 0 {
				t.Fatalf("capacity admitted %d, want %d; results %+v", successes, row.winners, results)
			}
			before := f.store.Durable()
			replay, err := agents[winner%len(agents)].StartTurn(ctx, requests[winner])
			if err != nil || !reflect.DeepEqual(replay, results[winner].receipt) || f.store.Durable() != before {
				t.Fatalf("full-capacity retry: %+v %v", replay, err)
			}
			denied := requests[loser]
			denied.GrantID = privateID(999)
			var deniedBusy *BusyError
			if _, err := agents[loser%len(agents)].StartTurn(ctx, denied); !errors.Is(err, ErrDenied) || errors.As(err, &deniedBusy) || f.store.Durable() != before {
				t.Fatalf("authority must precede exhaustion: %v", err)
			}
			if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: privateID(400), TurnID: requests[winner].TurnID, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
				t.Fatal(err)
			}
			if _, err := agents[loser%len(agents)].StartTurn(ctx, requests[loser]); err != nil {
				t.Fatalf("busy consumed a command or turn ID: %v", err)
			}
			status, err := agents[loser%len(agents)].TurnStatus(ctx, WorkLookup{WorkID: requests[loser].TurnID, GrantID: requests[loser].GrantID})
			if err != nil || status.Ended || status.Dispatched || status.Held.TurnSlots != 1 {
				t.Fatalf("admission status: %+v %v", status, err)
			}
			capacityView, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || capacityView.Held.TurnSlots != int64(row.winners) || capacityView.Used != (Exposure{}) {
				t.Fatalf("turn accounting charged a local unit or escaped shared capacity: %+v %v", capacityView, err)
			}
		})
	}
	t.Run("separate_cancel_grant", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin := registerPrivate(t, f.service, 10)
		other := scopedWorkContext(t, f.service, origin, 20, 21)
		capacity := ledger.Capacity{TurnSlots: 1}
		scopedEnrol(t, f.service, origin, capacity, 100, origin.AgentID)
		start := privateScopedGrant(origin, 200, capacity, "turn.start", "turn.inspect")
		cancel := privateScopedGrant(origin, 210, capacity, "turn.cancel")
		foreign := privateScopedGrant(other, 220, capacity, "turn.cancel")
		for i, grant := range []GrantSpec{start, cancel, foreign} {
			issueScopedRoot(t, f.service, origin, 201+i*10, grant)
		}
		agent := scopedBound(t, f.service, origin)
		admission := TurnAdmission{CommandID: privateID(300), TurnID: privateID(301), GrantID: start.GrantID}
		if _, err := agent.StartTurn(ctx, admission); err != nil {
			t.Fatal(err)
		}
		request := TurnCancellation{CommandID: privateID(310), TurnID: admission.TurnID, ExpectedVersion: 1}
		before := f.store.Durable()
		for _, grant := range []uuid.UUID{start.GrantID, foreign.GrantID} {
			request.GrantID = grant
			if _, err := agent.CancelTurn(ctx, request); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
				t.Fatalf("cancel without the operation or from another context: %v", err)
			}
		}
		request.GrantID = cancel.GrantID
		if _, err := f.service.CancelTurn(ctx, origin, request); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
			t.Fatalf("owner accepted a caller-selected non-admission grant: %v", err)
		}
		receipt, err := agent.CancelTurn(ctx, request)
		if err != nil || receipt.GrantID != cancel.GrantID {
			t.Fatalf("separate current cancel grant: %+v %v", receipt, err)
		}
		reopenScoped(t, f, DefaultLimits())
		status, err := f.service.TurnStatus(ctx, WorkLookup{WorkID: admission.TurnID})
		if err != nil || !status.Ended || status.Outcome != "cancelled" || status.Held != (Exposure{}) {
			t.Fatalf("cancel grant replay released the wrong scope set: %+v %v", status, err)
		}
	})
	t.Run("old_context_fence", testScopedOldContextTurnFence)
}

func TestScopedOwnerGrantSelectionShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	unknown := Binding{AgentID: privateID(700), ContextID: privateID(701)}
	grant := privateID(702)
	for _, row := range []struct {
		name string
		call func() (ScopedReceipt, error)
	}{
		{"issue", func() (ScopedReceipt, error) {
			return f.service.IssueGrant(ctx, unknown, GrantIssue{CommandID: privateID(703), GrantID: grant})
		}},
		{"revoke", func() (ScopedReceipt, error) {
			return f.service.RevokeGrant(ctx, unknown, GrantRevocation{CommandID: privateID(703), GrantID: grant})
		}},
		{"create", func() (ScopedReceipt, error) {
			return f.service.CreateTask(ctx, unknown, TaskCreation{CommandID: privateID(703), GrantID: grant})
		}},
		{"assign", func() (ScopedReceipt, error) {
			return f.service.AssignTask(ctx, unknown, TaskAssignment{CommandID: privateID(703), GrantID: grant})
		}},
		{"transition", func() (ScopedReceipt, error) {
			return f.service.TransitionTask(ctx, unknown, TaskMutation{CommandID: privateID(703), GrantID: grant, Action: "cancel"})
		}},
		{"turn_cancel", func() (ScopedReceipt, error) {
			return f.service.CancelTurn(ctx, unknown, TurnCancellation{CommandID: privateID(703), GrantID: grant, TurnID: privateID(704), ExpectedVersion: 1})
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			before := f.store.Durable()
			if _, err := row.call(); err != ErrInvalid || f.store.Durable() != before {
				t.Fatalf("owner grant selection read identity or state before refusing its shape: %v", err)
			}
		})
	}
}

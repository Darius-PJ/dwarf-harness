package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func scopedCapacity() ledger.Capacity {
	return ledger.Capacity{TurnSlots: 3, LocalSlots: 4, BufferBytes: 1 << 20, LocalUnits: 100, MaxOperationBytes: 1 << 20}
}

func scopedEnrol(t *testing.T, s *Service, origin Binding, capacity ledger.Capacity, command int, agents ...uuid.UUID) {
	t.Helper()
	scopes := []ledger.ScopeKey{{Kind: "colony"}}
	for _, agent := range agents {
		scopes = append(scopes, ledger.ScopeKey{Kind: "agent", ID: agent})
	}
	for i, scope := range scopes {
		if _, err := s.ConfigureCapacity(context.Background(), origin, CapacitySpec{CommandID: privateID(command + i), Scope: scope, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
	}
}

func scopedWorkContext(t *testing.T, s *Service, agent Binding, id, command int) Binding {
	t.Helper()
	binding := Binding{AgentID: agent.AgentID, ContextID: privateID(id)}
	if _, err := s.CreateContext(context.Background(), ContextCreation{Binding: binding, CommandID: privateID(command), Kind: "work"}); err != nil {
		t.Fatal(err)
	}
	return binding
}

func scopedCreate(t *testing.T, s *Service, origin Binding, id, command int, audience ...uuid.UUID) ScopedReceipt {
	t.Helper()
	receipt, err := s.CreateTask(context.Background(), origin, TaskCreation{CommandID: privateID(command), TaskID: privateID(id), Description: []byte("A bounded task description\n"), AcceptanceSHA256: ledger.Digest(sha256.Sum256([]byte("acceptance"))), Audience: audience})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func scopedRoot(t *testing.T, s *Service, origin Binding, authority uint64, command int, spec GrantSpec) ScopedReceipt {
	t.Helper()
	receipt, err := s.IssueGrant(context.Background(), origin, GrantIssue{CommandID: privateID(command), ExpectedAuthorityVersion: authority, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func reopenScoped(t *testing.T, f *serviceFixture, limits Limits) Reconciliation {
	t.Helper()
	before := f.store.Durable()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.service, report, err = Open(context.Background(), f.store, f.root, limits)
	if err != nil || len(report.Issues) != 0 || f.store.Durable() != before {
		t.Fatalf("inert real-store reopen: %+v %v", report, err)
	}
	return report
}

func TestScopedIdentityAndDefaultDeny(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newServiceFixture(t, serviceDeps{})
	origin := registerPrivate(t, f.service, 100)
	other := registerPrivate(t, f.service, 200)
	work := scopedWorkContext(t, f.service, other, 210, 211)
	scopedEnrol(t, f.service, origin, scopedCapacity(), 300, origin.AgentID, other.AgentID)
	scopedCreate(t, f.service, origin, 400, 401, other.AgentID)
	scopedCreate(t, f.service, origin, 410, 411, other.AgentID)
	assigned := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.assign"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(400)}}, DataClass: "task", Recipients: []Binding{work}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	scopedRoot(t, f.service, origin, 0, 501, assigned)
	borrowed := GrantSpec{GrantID: privateID(510), Grantee: other, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{other}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
	scopedRoot(t, f.service, origin, 1, 511, borrowed)
	agent, err := f.service.BindAgent(ctx, origin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.BindAgent(ctx, Binding{origin.AgentID, other.ContextID}); !errors.Is(err, ErrNotBound) {
		t.Fatalf("foreign context binding: %v", err)
	}
	if _, err := f.service.BindAgent(ctx, Binding{privateID(999), origin.ContextID}); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unknown identity binding: %v", err)
	}
	for _, grant := range []uuid.UUID{privateID(999), borrowed.GrantID} {
		before := f.store.Durable()
		_, err := agent.CreateTask(ctx, TaskCreation{CommandID: privateID(600), GrantID: grant, TaskID: privateID(601), Description: []byte("must remain private"), AcceptanceSHA256: ledger.Digest(sha256.Sum256([]byte("accept"))), Audience: []uuid.UUID{other.AgentID}})
		if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
			t.Fatalf("unknown/foreign grant disclosed or committed: %v", err)
		}
	}
	request := TaskAssignment{CommandID: privateID(610), GrantID: assigned.GrantID, TaskID: privateID(410), ExpectedVersion: 1, Assignment: ledger.AssignmentInput{AssignmentID: privateID(611), Assignee: ledger.Binding(work)}}
	before := f.store.Durable()
	if _, err := agent.AssignTask(ctx, request); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
		t.Fatalf("different task escaped exact resource: %v", err)
	}
	request.TaskID = privateID(400)
	receipt, err := agent.AssignTask(ctx, request)
	if err != nil || receipt.Actor != "agent" || receipt.Origin != (ledger.Origin{AgentID: origin.AgentID, SessionID: privateID(101), ContextID: origin.ContextID}) || receipt.Version != 2 {
		t.Fatalf("non-audience assigner or bound origin: %+v %v", receipt, err)
	}
	for _, unbound := range []*Agent{nil, {}} {
		if _, err := unbound.AssignTask(ctx, request); !errors.Is(err, ErrNotBound) {
			t.Fatalf("unbound entrance: %v", err)
		}
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.AssignTask(ctx, request); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed bound entrance: %v", err)
	}
}

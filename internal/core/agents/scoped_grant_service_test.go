package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func scopedBound(t *testing.T, s *Service, binding Binding) *Agent {
	t.Helper()
	agent, err := s.BindAgent(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func scopedNewTask(command, task int, grant uuid.UUID, audience ...uuid.UUID) TaskCreation {
	return TaskCreation{CommandID: privateID(command), GrantID: grant, TaskID: privateID(task), Description: []byte("bounded description\n"), AcceptanceSHA256: ledger.Digest(sha256.Sum256([]byte("acceptance"))), Audience: audience}
}

func TestScopedGrantAttenuationAndCurrentChain(t *testing.T) {
	t.Parallel()
	t.Run("operation-resource-and-query-copy", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		issuer := registerPrivate(t, f.service, 100)
		recipient := registerPrivate(t, f.service, 200)
		scopedEnrol(t, f.service, issuer, scopedCapacity(), 300, issuer.AgentID, recipient.AgentID)
		scopedCreate(t, f.service, issuer, 400, 401, recipient.AgentID)
		scopedCreate(t, f.service, issuer, 410, 411, recipient.AgentID)
		limit := scopedCapacity()
		limit.LocalUnits = 1
		root := GrantSpec{GrantID: privateID(500), Grantee: issuer, Operations: []string{"grant.issue", "task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(400)}}, DataClass: "task", Recipients: []Binding{recipient}, Limits: limit, DelegationDepth: 1, Lifetime: "until_revoked"}
		scopedRoot(t, f.service, issuer, 0, 501, root)
		a, b := scopedBound(t, f.service, issuer), scopedBound(t, f.service, recipient)
		child := GrantSpec{GrantID: privateID(600), Grantee: recipient, ParentGrantID: root.GrantID, Operations: []string{"task.inspect"}, Resources: slices.Clone(root.Resources), DataClass: "task", Recipients: []Binding{recipient}, Limits: limit, Lifetime: "until_revoked"}
		for _, row := range []struct {
			name      string
			operation string
			task      uuid.UUID
		}{
			{"operation", "task.assign", privateID(400)},
			{"resource", "task.inspect", privateID(410)},
		} {
			wide := child
			wide.Operations = []string{row.operation}
			wide.Resources = []ledger.Resource{{Kind: "task", TaskID: row.task}}
			before := f.store.Durable()
			_, err := a.IssueGrant(ctx, GrantIssue{CommandID: privateID(601), GrantID: root.GrantID, ExpectedAuthorityVersion: 1, Spec: wide})
			if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
				t.Fatalf("%s widening: %v", row.name, err)
			}
		}
		if _, err := a.IssueGrant(ctx, GrantIssue{CommandID: privateID(601), GrantID: root.GrantID, ExpectedAuthorityVersion: 1, Spec: child}); err != nil {
			t.Fatal(err)
		}
		view, err := b.GrantStatus(ctx, GrantLookup{GrantID: child.GrantID})
		if err != nil {
			t.Fatal(err)
		}
		view.Grant.Resources[0].TaskID = privateID(410)
		before := f.store.Durable()
		if _, err := b.TaskStatus(ctx, TaskLookup{TaskID: privateID(410), GrantID: child.GrantID}); !errors.Is(err, ErrDenied) {
			t.Fatalf("caller mutated granted authority through a view: %v", err)
		}
		task, err := b.TaskStatus(ctx, TaskLookup{TaskID: privateID(400), GrantID: child.GrantID})
		if err != nil || task.Readiness != "waiting_blocked" || task.Reason != "unassigned" || f.store.Durable() != before {
			t.Fatalf("authorized passive query at full allowance: %+v %v", task, err)
		}
	})
	t.Run("attenuation-shared-ceilings", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		issuer := registerPrivate(t, f.service, 100)
		recipient := registerPrivate(t, f.service, 200)
		scopedEnrol(t, f.service, issuer, scopedCapacity(), 300, issuer.AgentID, recipient.AgentID)
		limit := scopedCapacity()
		limit.LocalUnits = 4
		root := GrantSpec{GrantID: privateID(500), Grantee: issuer, Operations: []string{"grant.issue", "grant.revoke", "task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{recipient}, Limits: limit, DelegationDepth: 2, Lifetime: "until_revoked"}
		scopedRoot(t, f.service, issuer, 0, 501, root)
		a, b := scopedBound(t, f.service, issuer), scopedBound(t, f.service, recipient)
		child := GrantSpec{GrantID: privateID(600), Grantee: recipient, ParentGrantID: root.GrantID, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{recipient}, Limits: limit, Lifetime: "until_revoked"}
		for _, row := range []struct {
			name  string
			widen func(*GrantSpec)
		}{
			{"grantee", func(g *GrantSpec) { g.Grantee = issuer }},
			{"recipient", func(g *GrantSpec) { g.Recipients[0] = issuer }},
			{"allowance", func(g *GrantSpec) { g.Limits.LocalUnits++ }},
			{"depth", func(g *GrantSpec) { g.DelegationDepth = root.DelegationDepth }},
		} {
			wide := child
			wide.Operations, wide.Resources, wide.Recipients = slices.Clone(child.Operations), slices.Clone(child.Resources), slices.Clone(child.Recipients)
			row.widen(&wide)
			before := f.store.Durable()
			_, err := a.IssueGrant(ctx, GrantIssue{CommandID: privateID(601), GrantID: root.GrantID, ExpectedAuthorityVersion: 1, Spec: wide})
			if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
				t.Fatalf("%s attenuation: %v", row.name, err)
			}
		}
		first, err := a.IssueGrant(ctx, GrantIssue{CommandID: privateID(601), GrantID: root.GrantID, ExpectedAuthorityVersion: 1, Spec: child})
		if err != nil {
			t.Fatal(err)
		}
		sibling := child
		sibling.GrantID = privateID(610)
		if _, err := a.IssueGrant(ctx, GrantIssue{CommandID: privateID(611), GrantID: root.GrantID, ExpectedAuthorityVersion: uint64(first.Version), Spec: sibling}); err != nil {
			t.Fatal(err)
		}
		request := scopedNewTask(701, 700, child.GrantID, recipient.AgentID)
		if _, err := b.CreateTask(ctx, request); err != nil {
			t.Fatal(err)
		}
		second := scopedNewTask(711, 710, sibling.GrantID, recipient.AgentID)
		secondReceipt, err := b.CreateTask(ctx, second)
		if err != nil {
			t.Fatal(err)
		}
		parent, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "grant", ID: root.GrantID}})
		if err != nil || parent.Used.LocalUnits != 4 || parent.Held != (Exposure{}) {
			t.Fatalf("shared parent charge: %+v %v", parent, err)
		}
		own, err := a.CapacityStatus(ctx, GrantLookup{GrantID: root.GrantID})
		if err != nil || own.Used.LocalUnits != 2 || own.Available != nil || own.Band != "busy" {
			t.Fatalf("issuer saw descendant occupancy or fresh allowance: %+v %v", own, err)
		}
		before := f.store.Durable()
		_, err = b.CreateTask(ctx, scopedNewTask(721, 720, child.GrantID, recipient.AgentID))
		var busy *BusyError
		if !errors.As(err, &busy) || f.store.Durable() != before {
			t.Fatalf("child bypassed shared exhaustion: %v", err)
		}
		if _, err := a.RevokeGrant(ctx, GrantRevocation{CommandID: privateID(801), GrantID: root.GrantID, RevokeGrantID: child.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 3}); err != nil {
			t.Fatalf("uncharged issuer revoke at exhaustion: %v", err)
		}
		closed, err := b.GrantStatus(ctx, GrantLookup{GrantID: child.GrantID})
		if err != nil || closed.Current || !closed.Revoked || closed.Reason != "revoked" {
			t.Fatalf("own revoked grant: %+v %v", closed, err)
		}
		own, err = b.CapacityStatus(ctx, GrantLookup{GrantID: child.GrantID})
		if err != nil || own.Band != "denied" || own.Used.LocalUnits != 1 || own.Available != nil {
			t.Fatalf("cold originating usage: %+v %v", own, err)
		}
		if _, err := f.service.RevokeGrant(ctx, issuer, GrantRevocation{CommandID: privateID(811), RevokeGrantID: root.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 4}); err != nil {
			t.Fatal(err)
		}
		closed, err = b.GrantStatus(ctx, GrantLookup{GrantID: sibling.GrantID})
		if err != nil || closed.Current || closed.Reason != "ancestor_not_current" || len(closed.Ancestors) != 1 || closed.Ancestors[0].Current {
			t.Fatalf("current ancestor validity: %+v %v", closed, err)
		}
		before = f.store.Durable()
		if _, err := b.CreateTask(ctx, second); !errors.Is(err, ErrDenied) || f.store.Durable() != before {
			t.Fatalf("revoked ancestor allowed prior-command disclosure: %v", err)
		}
		if _, err := b.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: second.CommandID, GrantID: sibling.GrantID, SubmissionSHA256: secondReceipt.SubmissionSHA256}); !errors.Is(err, ErrDenied) {
			t.Fatalf("status trusted stale leaf: %v", err)
		}
		if _, err := f.service.IssueGrant(ctx, issuer, GrantIssue{CommandID: privateID(821), ExpectedAuthorityVersion: 5, Spec: root}); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
			t.Fatalf("retired identity reused after eviction: %v", err)
		}
		for _, id := range []uuid.UUID{root.GrantID, privateID(999)} {
			if _, err := b.GrantStatus(ctx, GrantLookup{GrantID: id}); err != ErrDenied {
				t.Fatalf("foreign/absent grant oracle: %v", err)
			}
			if _, err := b.CapacityStatus(ctx, GrantLookup{GrantID: id}); err != ErrDenied {
				t.Fatalf("foreign/absent capacity oracle: %v", err)
			}
		}
	})
	t.Run("revocation-during-preparation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		started, release := make(chan struct{}), make(chan struct{})
		var once, releaseOnce sync.Once
		f := newServiceFixture(t, serviceDeps{beforePublish: func(_ Binding, scope string) error {
			if scope == "task" {
				once.Do(func() { close(started); <-release })
			}
			return nil
		}})
		t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
		origin := registerPrivate(t, f.service, 100)
		scopedEnrol(t, f.service, origin, scopedCapacity(), 300, origin.AgentID)
		root := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, root)
		a := scopedBound(t, f.service, origin)
		request := scopedNewTask(601, 600, root.GrantID, origin.AgentID)
		finished := make(chan error, 1)
		go func() { _, err := a.CreateTask(ctx, request); finished <- err }()
		select {
		case <-started:
		case err := <-finished:
			t.Fatalf("request never reached reserved publication: %v", err)
		}
		if _, err := f.service.RevokeGrant(ctx, origin, GrantRevocation{CommandID: privateID(701), RevokeGrantID: root.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 1}); err != nil {
			t.Fatal(err)
		}
		view, err := a.CapacityStatus(ctx, GrantLookup{GrantID: root.GrantID})
		if err != nil || view.Band != "denied" || view.Held.LocalSlots != 1 || view.Held.BufferBytes != int64(len(request.Description)) || view.Held.LocalUnits != 1 {
			t.Fatalf("revocation hid own retained preparation: %+v %v", view, err)
		}
		afterRevoke := f.store.Durable()
		releaseOnce.Do(func() { close(release) })
		if err := <-finished; !errors.Is(err, ErrDenied) || f.store.Durable() != afterRevoke {
			t.Fatalf("publication escaped current-authority recheck: %v", err)
		}
		view, err = f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || view.Held != (Exposure{}) || view.Used != (Exposure{}) {
			t.Fatalf("refused publication retained or consumed exposure: %+v %v", view, err)
		}
		root.GrantID = privateID(800)
		scopedRoot(t, f.service, origin, 2, 801, root)
		request.GrantID = root.GrantID
		if _, err := a.CreateTask(ctx, request); err != nil {
			t.Fatalf("refusal consumed command/task identity or preparation: %v", err)
		}
	})
}

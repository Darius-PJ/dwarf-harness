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

func TestScopedProducerCeilings(t *testing.T) {
	t.Parallel()
	for _, dimension := range []string{"depth", "operations", "resources", "recipients"} {
		t.Run("grant-"+dimension, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			origin, other := registerPrivate(t, f.service, 100), registerPrivate(t, f.service, 200)
			scopedCreate(t, f.service, origin, 400, 401, origin.AgentID, other.AgentID)
			scopedCreate(t, f.service, origin, 410, 411, origin.AgentID, other.AgentID)
			limits := DefaultLimits()
			good := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(400)}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
			bad := good
			switch dimension {
			case "depth":
				limits.MaxDelegationDepth = 0
				bad.DelegationDepth = 1
				bad.Operations = []string{"grant.issue", "task.inspect"}
			case "operations":
				limits.MaxGrantOperations = 1
				bad.Operations = []string{"task.assign", "task.inspect"}
			case "resources":
				limits.MaxGrantResources = 1
				bad.Resources = []ledger.Resource{good.Resources[0], {Kind: "task", TaskID: privateID(410)}}
			case "recipients":
				limits.MaxGrantRecipients = 1
				bad.Recipients = []Binding{origin, other}
			}
			reopenScoped(t, f, limits)
			before := f.store.Durable()
			_, err := f.service.IssueGrant(ctx, origin, GrantIssue{CommandID: privateID(501), Spec: bad})
			if !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
				t.Fatalf("oversize grant crossed configured ceiling: %v", err)
			}
			scopedRoot(t, f.service, origin, 0, 501, good)
			view, err := scopedBound(t, f.service, origin).TaskStatus(ctx, TaskLookup{TaskID: privateID(400), GrantID: good.GrantID})
			if err != nil || view.Task.TaskID != privateID(400) {
				t.Fatalf("refusal consumed command identity or boundary grant was unusable: %+v %v", view, err)
			}
		})
	}
	for _, dimension := range []string{"audience", "dependencies", "utf8-description-bytes"} {
		t.Run("task-"+dimension, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newServiceFixture(t, serviceDeps{})
			origin, other := registerPrivate(t, f.service, 100), registerPrivate(t, f.service, 200)
			scopedCreate(t, f.service, origin, 400, 401, origin.AgentID)
			limits := DefaultLimits()
			good := scopedNewTask(501, 500, uuid.UUID{}, origin.AgentID)
			good.Description = []byte("é")
			bad := good
			switch dimension {
			case "audience":
				limits.MaxTaskAudience = 1
				bad.Audience = []uuid.UUID{origin.AgentID, other.AgentID}
			case "dependencies":
				limits.MaxTaskDependencies = 0
				bad.Dependencies = []uuid.UUID{privateID(400)}
			case "utf8-description-bytes":
				limits.MaxTaskDescriptionBytes = 2
				bad.Description = []byte("éa")
			}
			reopenScoped(t, f, limits)
			before := f.store.Durable()
			_, err := f.service.CreateTask(ctx, origin, bad)
			if !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
				t.Fatalf("oversize task crossed configured ceiling: %v", err)
			}
			if _, err := f.store.ReadBody(ctx, ledger.Digest(sha256.Sum256(bad.Description))); !errors.Is(err, ledgerstore.ErrBodyMissing) {
				t.Fatalf("refused description was published: %v", err)
			}
			if _, err := f.service.CreateTask(ctx, origin, good); err != nil {
				t.Fatalf("refusal consumed command identity or exact byte boundary: %v", err)
			}
			view, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: good.TaskID})
			if err != nil || view.Task.Description.Bytes != 2 {
				t.Fatalf("description was measured in characters instead of UTF-8 bytes: %+v %v", view, err)
			}
		})
	}
	t.Run("live-grants-retire-without-identity-reuse", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin := registerPrivate(t, f.service, 100)
		scopedCreate(t, f.service, origin, 400, 401, origin.AgentID)
		limits := DefaultLimits()
		limits.MaxLiveGrants = 1
		reopenScoped(t, f, limits)
		spec := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.inspect"}, Resources: []ledger.Resource{{Kind: "task", TaskID: privateID(400)}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, spec)
		next := spec
		next.GrantID = privateID(510)
		request := GrantIssue{CommandID: privateID(511), ExpectedAuthorityVersion: 1, Spec: next}
		before := f.store.Durable()
		_, err := f.service.IssueGrant(ctx, origin, request)
		var busy *BusyError
		if !errors.As(err, &busy) || f.store.Durable() != before {
			t.Fatalf("live grant ceiling: %v", err)
		}
		if _, err := f.service.RevokeGrant(ctx, origin, GrantRevocation{CommandID: privateID(521), RevokeGrantID: spec.GrantID, ExpectedVersion: 1, ExpectedAuthorityVersion: 1}); err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, limits)
		before = f.store.Durable()
		if _, err := f.service.IssueGrant(ctx, origin, GrantIssue{CommandID: privateID(531), ExpectedAuthorityVersion: 2, Spec: spec}); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
			t.Fatalf("retired grant ID reused after restart: %v", err)
		}
		request.ExpectedAuthorityVersion = 2
		if _, err := f.service.IssueGrant(ctx, origin, request); err != nil {
			t.Fatalf("revoked grant retained its live slot or refusal consumed command: %v", err)
		}
	})
	t.Run("terminal-tasks-release-live-slots-not-identities", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin := registerPrivate(t, f.service, 100)
		scopedCreate(t, f.service, origin, 400, 401, origin.AgentID)
		limits := DefaultLimits()
		limits.MaxLiveTasks = 1
		reopenScoped(t, f, limits)
		request := scopedNewTask(501, 500, uuid.UUID{}, origin.AgentID)
		before := f.store.Durable()
		_, err := f.service.CreateTask(ctx, origin, request)
		var busy *BusyError
		if !errors.As(err, &busy) || f.store.Durable() != before {
			t.Fatalf("live task ceiling: %v", err)
		}
		if _, err := f.service.TransitionTask(ctx, origin, TaskMutation{CommandID: privateID(411), TaskID: privateID(400), ExpectedVersion: 1, Action: "cancel"}); err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, limits)
		before = f.store.Durable()
		if _, err := f.service.CreateTask(ctx, origin, scopedNewTask(421, 400, uuid.UUID{}, origin.AgentID)); !errors.Is(err, ErrRevision) || f.store.Durable() != before {
			t.Fatalf("retired task ID reused after restart: %v", err)
		}
		if _, err := f.service.CreateTask(ctx, origin, request); err != nil {
			t.Fatalf("terminal task retained live slot or refusal consumed command: %v", err)
		}
		old, err := f.service.TaskStatus(ctx, TaskLookup{TaskID: privateID(400)})
		if err != nil || old.Task.State != "cancelled" || old.Task.Version != 2 {
			t.Fatalf("new live task displaced historical task facts: %+v %v", old, err)
		}
	})
	t.Run("recomputed-budget-scopes", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin := registerPrivate(t, f.service, 100)
		scopedEnrol(t, f.service, origin, scopedCapacity(), 300, origin.AgentID)
		spec := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, spec)
		limits := DefaultLimits()
		limits.MaxBudgetScopes = 3
		reopenScoped(t, f, limits)
		request := scopedNewTask(601, 600, spec.GrantID, origin.AgentID)
		before := f.store.Durable()
		_, err := scopedBound(t, f.service, origin).CreateTask(ctx, request)
		if !errors.Is(err, ErrTooLarge) || f.store.Durable() != before {
			t.Fatalf("colony, agent, context and grant scopes bypassed ceiling: %v", err)
		}
		book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Held != (Exposure{}) || book.Used != (Exposure{}) {
			t.Fatalf("refused scope set changed usage: %+v %v", book, err)
		}
		if _, err := f.store.ReadBody(ctx, ledger.Digest(sha256.Sum256(request.Description))); !errors.Is(err, ledgerstore.ErrBodyMissing) {
			t.Fatalf("scope refusal followed publication: %v", err)
		}
		limits.MaxBudgetScopes = 4
		reopenScoped(t, f, limits)
		if _, err := scopedBound(t, f.service, origin).CreateTask(ctx, request); err != nil {
			t.Fatalf("exact scope boundary required optional context enrolment: %v", err)
		}
		book, err = f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Held != (Exposure{}) || book.Used.LocalUnits != 1 {
			t.Fatalf("accepted boundary did not charge once: %+v %v", book, err)
		}
	})
	t.Run("recipient-authority-precedes-audience-registration", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := newServiceFixture(t, serviceDeps{})
		origin, other := registerPrivate(t, f.service, 100), registerPrivate(t, f.service, 200)
		scopedEnrol(t, f.service, origin, scopedCapacity(), 300, origin.AgentID)
		spec := GrantSpec{GrantID: privateID(500), Grantee: origin, Operations: []string{"task.create"}, Resources: []ledger.Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{origin}, Limits: scopedCapacity(), Lifetime: "until_revoked"}
		scopedRoot(t, f.service, origin, 0, 501, spec)
		agent := scopedBound(t, f.service, origin)
		before := f.store.Durable()
		for _, audience := range []uuid.UUID{other.AgentID, privateID(999)} {
			if _, err := agent.CreateTask(ctx, scopedNewTask(601, 600, spec.GrantID, audience)); err != ErrDenied || f.store.Durable() != before {
				t.Fatalf("unauthorized recipient disclosed registration: %v", err)
			}
		}
		if _, err := agent.CreateTask(ctx, scopedNewTask(601, 600, spec.GrantID, origin.AgentID)); err != nil {
			t.Fatalf("denial consumed command or task identity: %v", err)
		}
	})
}

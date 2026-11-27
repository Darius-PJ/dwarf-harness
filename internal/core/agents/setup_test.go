package agents

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func TestPaidRefusalCauses(t *testing.T) {
	t.Parallel()
	for _, cause := range []string{"route_missing", "route_revision", "credential_missing"} {
		t.Run(cause, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int32
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) { posts.Add(1); paidProviderSuccess(t, "anthropic", w, r) }, nil)
			switch cause {
			case "route_missing":
				f.setting.RouteID = uuid.NewV7()
			case "route_revision":
				f.setting.ExpectedRouteVersion++
			case "credential_missing":
				_, err := f.service.RevokeCredential(context.Background(), bindingOf(f.origin), CredentialRevocation{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, Provider: f.credential.Provider, ExpectedVersion: f.credential.Version, CredentialVersion: f.credential.CredentialVersion})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.enable(t)
			before := f.store.Durable()
			_, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "message.send", Arguments: session.MessageSend{TurnID: uuid.NewV7(), Text: "Do not dispatch without prerequisites."}})
			var detail *RefusalCause
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &detail) || detail.Code != cause {
				t.Fatalf("safe cause %s: %v", cause, err)
			}
			if f.store.Durable() != before || posts.Load() != 0 {
				t.Fatal("prerequisite refusal wrote or dispatched")
			}
		})
	}
	t.Run("explicit_agent_spend_before_intent", func(t *testing.T) {
		t.Parallel()
		var posts atomic.Int32
		server := paidServer(t, func(w http.ResponseWriter, r *http.Request) { posts.Add(1); paidProviderSuccess(t, "anthropic", w, r) })
		f := newLivePaidFixture(t, paidServerDeps(server, nil))
		ctx := context.Background()
		owner := bindingOf(f.origin)
		binding := Binding{AgentID: uuid.NewV7(), ContextID: uuid.NewV7()}
		if _, err := f.service.RegisterAgent(ctx, AgentRegistration{CommandID: uuid.NewV7(), AgentID: binding.AgentID, ContextID: binding.ContextID, SessionID: uuid.NewV7(), Directory: "ExplicitSpend", AgentMarkdown: []byte("Synthetic paid refusal test.")}); err != nil {
			t.Fatal(err)
		}
		capacity := paidPhysicalCapacity()
		if _, err := f.service.ConfigureCapacity(ctx, owner, CapacitySpec{CommandID: uuid.NewV7(), Scope: ledger.ScopeKey{Kind: "agent", ID: binding.AgentID}, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
		model, turn := uuid.NewV7(), uuid.NewV7()
		for _, spec := range []GrantSpec{
			{GrantID: model, Grantee: binding, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
			{GrantID: turn, Grantee: binding, Operations: []string{"turn.start"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
		} {
			snapshot, err := f.service.OwnerSetupSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(snapshot.AuthorityVersion), Spec: spec}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.service.ConfigureSpend(ctx, owner, SpendConfiguration{CommandID: uuid.NewV7(), Scope: ledger.PaidScope{Kind: "grant", ID: model}, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"}}); err != nil {
			t.Fatal(err)
		}
		agent := scopedBound(t, f.service, binding)
		turnID := uuid.NewV7()
		if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: uuid.NewV7(), TurnID: turnID, GrantID: turn}); err != nil {
			t.Fatal(err)
		}
		input := f.input()
		input.GrantID, input.ParentTurnID = model, turnID
		scope := ledger.PaidScope{Kind: "agent", ID: binding.AgentID}
		for version, code := range []string{"spend_ceiling_missing", "spend_disabled"} {
			if version == 1 {
				if _, err := f.service.ConfigureSpend(ctx, owner, SpendConfiguration{CommandID: uuid.NewV7(), Scope: scope, Terms: SpendTermsInput{Enabled: false, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"}}); err != nil {
					t.Fatal(err)
				}
			}
			before := f.store.Durable()
			prepared, err := f.service.PrepareAnthropic(ctx, binding, input)
			if prepared != nil {
				_ = prepared.Close()
			}
			var cause *RefusalCause
			if !errors.Is(err, ErrDenied) || !errors.As(err, &cause) || cause.Code != code || cause.Scope != scope {
				t.Fatalf("agent spend %s: %v", code, err)
			}
			_, err = f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: input.AttemptID})
			if !errors.Is(err, ErrDenied) || f.store.Durable() != before || posts.Load() != 0 {
				t.Fatalf("refusal reserved or sent: %v", err)
			}
		}
		if _, err := f.service.ConfigureSpend(ctx, owner, SpendConfiguration{CommandID: uuid.NewV7(), Scope: scope, ExpectedVersion: 1, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"}}); err != nil {
			t.Fatal(err)
		}
		prepared, err := f.service.PrepareAnthropic(ctx, binding, input)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		_, bound, err := agent.AdmitPaid(ctx, PaidAdmission{CommandID: uuid.NewV7(), AttemptID: input.AttemptID, GrantID: model, ParentTurnID: turnID, Prepared: prepared})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bound.Admission(); err != nil {
			t.Fatal(err)
		}
		dispatch := PaidDispatch{CommandID: uuid.NewV7(), AttemptID: input.AttemptID, ExpectedVersion: 1}
		out, err := f.service.DispatchPaid(ctx, dispatch)
		if err != nil || out.ObservedPaidCall == nil || out.IntentReceipt.Outcome != "sent" || posts.Load() != 1 {
			t.Fatalf("explicit spend did not admit one loopback dispatch: %v", err)
		}
		paidRecord(t, f, dispatch, out)
		snapshot, err := f.service.OwnerSetupSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var busy bool
		for _, c := range snapshot.Contexts {
			if c.ContextID == binding.ContextID {
				busy = c.Busy
			}
		}
		if !busy {
			t.Fatal("owner query missed live native turn")
		}
		if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: uuid.NewV7(), TurnID: turnID, ExpectedVersion: 1, Outcome: "released"}); err != nil {
			t.Fatal(err)
		}
		snapshot, err = f.service.OwnerSetupSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range snapshot.Contexts {
			if c.ContextID == binding.ContextID && c.Busy {
				t.Fatal("settled ended context remained busy")
			}
		}
	})
}

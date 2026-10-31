package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type turnFixture struct {
	*paidFixture
	workspace    *workspace.Service
	view         session.Session
	setting      LoopContextSettings
	privateGrant uuid.UUID
}

func newTurnFixture(t testing.TB, provider string, handler http.HandlerFunc, configure func(*serviceDeps)) *turnFixture {
	t.Helper()
	server := paidServer(t, handler)
	deps := paidServerDeps(server, nil)
	if configure != nil {
		configure(&deps)
	}
	f := paidProviderFixture(t, provider, deps)
	ctx, binding := context.Background(), bindingOf(f.origin)
	if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: uuid.NewV7(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "released"}); err != nil {
		t.Fatal(err)
	}
	profile := providerProfile(f.route.Profile)
	profile.ContextMax, profile.InputMax, profile.OutputMax = 200000, 196000, 8192
	profile.CapabilityVersion += "-turns"
	raw, err := profile.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfigurePaidRoute(ctx, binding, PaidRouteConfiguration{CommandID: uuid.NewV7(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw}); err != nil {
		t.Fatal(err)
	}
	f.route = f.service.p.paidRoutes[f.route.RouteID]
	ids := make([]uuid.UUID, 0, len(f.service.p.grants))
	for id := range f.service.p.grants {
		ids = append(ids, id)
	}
	for _, id := range ids {
		_, err := f.service.RevokeGrant(ctx, binding, GrantRevocation{CommandID: uuid.NewV7(), RevokeGrantID: id, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
		if err != nil {
			t.Fatal(err)
		}
	}
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes = 512 << 20
	capacity.MaxOperationBytes = 4 << 20
	for _, scope := range []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}} {
		if _, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: capacity}); err != nil {
			t.Fatal(err)
		}
	}
	private := uuid.NewV7()
	f.grant = uuid.NewV7()
	for _, spec := range []GrantSpec{
		{GrantID: f.grant, Grantee: binding, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
		{GrantID: private, Grantee: binding, Operations: []string{"context.inspect", "private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "agent", Slots: []string{"instructions", "memory"}}, {Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
	} {
		if _, err := f.service.IssueGrant(ctx, binding, GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}, {Kind: "credential", ID: f.credential.ScopeID}, {Kind: "grant", ID: f.grant}} {
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
	}
	ws, _, err := workspace.Open(ctx, f.store, f.root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if err := f.service.Close(); err != nil {
			t.Error(err)
		}
	})
	setting := LoopContextSettings{Binding: binding, RouteID: f.route.RouteID, ExpectedRouteVersion: f.route.Version, CredentialScopeID: f.credential.ScopeID, MaxOutputTokens: 4096, DisplayTitle: "General work", TimeoutMS: 600000, Policy: TurnPolicy{Schema: 1, MaxModelCalls: 4, MaxToolCalls: 8, MaxArgumentBytes: 64 << 10, MaxResultBytes: 64 << 10, MaxDiscoveryEntries: 8, MaxSearchEntries: 4096, MaxSearchMatches: 100, MaxLiveBytes: 64 << 10}}
	return &turnFixture{paidFixture: f, workspace: ws, setting: setting, privateGrant: private}
}
func (f *turnFixture) enable(t testing.TB, settings ...LoopContextSettings) {
	t.Helper()
	if err := f.service.EnableTurnLoop(context.Background(), f.workspace, LoopOptions{Contexts: append([]LoopContextSettings{f.setting}, settings...)}); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.OpenSession(context.Background(), bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	f.view = view
	t.Cleanup(view.Close)
}
func (f *turnFixture) send(t testing.TB) (session.Command, session.CommandResult) {
	t.Helper()
	command := session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.origin.ContextID, Operation: "message.send", Arguments: session.MessageSend{TurnID: uuid.NewV7(), Text: "Perform the bounded synthetic task."}}
	result, err := f.view.Do(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	return command, result
}
func (f *turnFixture) finished(t testing.TB, turn uuid.UUID) (ledger.RuntimeTurnResult, []session.Event) {
	t.Helper()
	return f.finishedFor(t, f.view, turn)
}
func (f *turnFixture) finishedFor(t testing.TB, view session.Session, turn uuid.UUID) (ledger.RuntimeTurnResult, []session.Event) {
	t.Helper()
	ctx := context.Background()
	var events []session.Event
	var cursor uint64
	for {
		page, err := view.Since(ctx, cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Events {
			if event.TurnID != turn {
				continue
			}
			events = append(events, event)
			if result, ok := event.Data.(ledger.RuntimeTurnResult); ok {
				return result, events
			}
		}
		cursor = page.ThroughSequence
		<-page.Wake
	}
}
func turnRequest(t testing.TB, r *http.Request) (string, []byte) {
	t.Helper()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		t.Error("request body unavailable")
		return "", nil
	}
	var request struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &request) != nil {
		t.Error("request JSON invalid")
	}
	return request.Model, raw
}
func turnAnthropic(model string, calls []struct{ ID, Name, Arguments string }, text string) string {
	if len(calls) == 0 {
		return paidSSE(model, `{"input_tokens":100,"output_tokens":1}`, `{"input_tokens":100,"output_tokens":20}`, text, "end_turn", "", true)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"message-one\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n", model)
	for index, call := range calls {
		fmt.Fprintf(&out, "data: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q,\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", index, call.ID, call.Name, index, call.Arguments, index)
	}
	out.WriteString("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_details\":null},\"usage\":{\"input_tokens\":100,\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	return out.String()
}
func turnCalls(name, args string) []struct{ ID, Name, Arguments string } {
	return []struct{ ID, Name, Arguments string }{{"call-one", name, args}}
}

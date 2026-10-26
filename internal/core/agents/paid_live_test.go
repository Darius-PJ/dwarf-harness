package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

const paidTestEnvironment = "DWARF_PAID_INTEGRATION_SYNTHETIC"

// Set one immutable process fixture before parallel tests start. Rotation tests
// use a different variable with t.Setenv and therefore stay sequential.
func TestMain(m *testing.M) {
	prior, present := os.LookupEnv(paidTestEnvironment)
	if err := os.Setenv(paidTestEnvironment, "synthetic-paid-integration-value"); err != nil {
		os.Exit(1)
	}
	code := m.Run()
	if present {
		_ = os.Setenv(paidTestEnvironment, prior)
	} else {
		_ = os.Unsetenv(paidTestEnvironment)
	}
	os.Exit(code)
}

func paidPhysicalCapacity() ledger.Capacity {
	return ledger.Capacity{TurnSlots: 3, LocalSlots: 4, BufferBytes: 64 << 20, LocalUnits: 1000, MaxOperationBytes: 1 << 20}
}
func newLivePaidFixture(t testing.TB) *paidFixture {
	t.Helper()
	f := newPaidFixture(t)
	ctx := context.Background()
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.service, _, err = openPaidService(ctx, f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, serviceDeps{paidTestAuthority: f.route.Profile.Authority})
	if err != nil {
		t.Fatal(err)
	}
	binding := bindingOf(f.origin)
	for _, scope := range []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}} {
		_, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: f.id(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: paidPhysicalCapacity()})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.grant = f.id()
	_, err = f.service.IssueGrant(ctx, binding, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: f.grant, Grantee: binding, Operations: []string{"grant.issue", "model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: paidPhysicalCapacity(), DelegationDepth: 4, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	version := f.id()
	_, err = f.service.BindCredential(ctx, binding, CredentialBinding{CommandID: f.id(), ScopeID: f.credential.ScopeID, ExpectedVersion: 1, Provider: "anthropic", CredentialVersion: version, SourceKind: "environment", SourceLocator: paidTestEnvironment, RevokePreviousVersion: f.credential.CredentialVersion})
	if err != nil {
		t.Fatal(err)
	}
	f.credential = f.service.p.paidCredentials[version]
	p := providerProfile(f.route.Profile)
	p.CapabilityVersion += "-live"
	p.VerifiedUTCNS = f.service.p.reviewFloor - 1
	p.ExpiresUTCNS = p.VerifiedUTCNS + int64(7*24*time.Hour)
	p.Sources = nil
	for _, topic := range []string{"endpoint_api_version", "inclusive_usage", "lifecycle", "local_tool_billing", "model_limits", "no_charge_billing", "tariff", "tier_region_speed"} {
		p.Sources = append(p.Sources, providers.SourceQuote{Topic: topic, URL: "https://localhost/" + topic, RetrievedUTCNS: p.VerifiedUTCNS, Quote: "Synthetic local finite assumptions; no live provider evidence."})
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.ConfigurePaidRoute(ctx, binding, PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: 1, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw})
	if err != nil {
		t.Fatal(err)
	}
	f.route = f.service.p.paidRoutes[f.route.RouteID]
	f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: f.grant}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"})
	return f
}
func (f *paidFixture) liveSpend(t testing.TB, scope ledger.PaidScope, terms SpendTermsInput) PaidReceipt {
	t.Helper()
	parent := uuid.UUID{}
	if scope.Kind == "grant" {
		parent = f.service.p.grants[scope.ID].definition.ParentGrantID
	}
	r, err := f.service.ConfigureSpend(context.Background(), bindingOf(f.origin), SpendConfiguration{CommandID: f.id(), Scope: scope, ExpectedVersion: f.service.p.book.paid[scope].version, Terms: terms, ParentGrantID: parent})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *paidFixture) input() AnthropicInput {
	return AnthropicInput{AttemptID: f.id(), GrantID: f.grant, ParentTurnID: f.turn, RouteID: f.route.RouteID, ExpectedRouteVersion: f.route.Version, CredentialVersion: f.credential.CredentialVersion, Request: providers.Request{MaxTokens: 20, Messages: []providers.Message{{Role: "user", Content: []providers.Block{{Type: "text", Text: "Synthetic request."}}}}}}
}
func (f *paidFixture) prepare(t testing.TB) PaidAdmission {
	t.Helper()
	input := f.input()
	p, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, TaskBinding: input.TaskBinding, Prepared: p}
}
func (f *paidFixture) admit(t *testing.T, r PaidAdmission) (PaidReceipt, *BoundCall) {
	t.Helper()
	a := scopedBound(t, f.service, bindingOf(f.origin))
	receipt, bound, err := a.AdmitPaid(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, bound
}
func (f *paidFixture) child(t *testing.T, parent uuid.UUID, micros int64) uuid.UUID {
	t.Helper()
	binding := bindingOf(f.origin)
	a := scopedBound(t, f.service, binding)
	id := f.id()
	parentGrant := f.service.p.grants[parent].definition
	_, err := a.IssueGrant(context.Background(), GrantIssue{CommandID: f.id(), GrantID: parent, ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: id, ParentGrantID: parent, Grantee: binding, Operations: []string{"grant.issue", "model.call"}, Resources: parentGrant.Resources, DataClass: "external", Recipients: []Binding{binding}, Limits: paidPhysicalCapacity(), DelegationDepth: parentGrant.DelegationDepth - 1, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DelegateSpend(context.Background(), SpendDelegation{CommandID: f.id(), GrantID: parent, ChildGrantID: id, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: fmt.Sprintf("0.%06d", micros), PeriodKind: "utc_day"}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPaidSealAndQuote(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"input_plus_requested_output_above_context", "input_rate_above_output_rate", "one_final_rounding"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			p := providerProfile(f.route.Profile)
			p.CapabilityVersion += "-" + name
			p.TariffVersion += "-" + name
			p.ContextMax, p.InputMax, p.OutputMax = 10, 10, 20
			for i := range p.Tariff.Rates {
				p.Tariff.Rates[i].Numerator, p.Tariff.Rates[i].Denominator = "1", "1"
				if name == "input_rate_above_output_rate" && p.Tariff.Rates[i].Class != providers.Generated {
					p.Tariff.Rates[i].Numerator = "9"
				}
				if name == "one_final_rounding" {
					p.Tariff.Rates[i].Denominator = "100"
				}
			}
			raw, _ := p.Canonical()
			_, err := f.service.ConfigurePaidRoute(context.Background(), bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw})
			if err != nil {
				t.Fatal(err)
			}
			f.route = f.service.p.paidRoutes[f.route.RouteID]
			input := f.input()
			h, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			input.Request.Messages[0].Content[0].Text = "Caller mutation must not change sealed bytes."
			r := PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: h}
			receipt, bound := f.admit(t, r)
			admission, err := bound.Admission()
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]int64{"input_plus_requested_output_above_context": 30, "input_rate_above_output_rate": 110, "one_final_rounding": 1}[name]
			if admission.Quote.MaximumMicros != want {
				t.Fatalf("quote=%d want=%d", admission.Quote.MaximumMicros, want)
			}
			body := readTestFile(t, paidInputPath(f, admission))
			var request map[string]json.RawMessage
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			var messages []providers.Message
			if err := json.Unmarshal(request["messages"], &messages); err != nil {
				t.Fatal(err)
			}
			if messages[0].Content[0].Text != "Synthetic request." {
				t.Fatal("prepared input aliased caller")
			}
			if ledger.Digest(providers.RequestSHA256(paidDescriptor(admission.Request), body)) != admission.Quote.RequestSHA256 {
				t.Fatal("published bytes differ from sealed bytes")
			}
			admission.Quote.Components[0].Classes[0] = "mutation"
			copy, _ := bound.Admission()
			if slices.Contains(copy.Quote.Components[0].Classes, "mutation") {
				t.Fatal("bound admission is mutable")
			}
			if receipt.Outcome != "reserved" {
				t.Fatal("admission not durable")
			}
		})
	}
}

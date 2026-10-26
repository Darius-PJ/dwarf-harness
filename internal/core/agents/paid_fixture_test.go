package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

type paidFixture struct {
	*serviceFixture
	origin               ledger.Origin
	route                ledger.RuntimePaidRouteConfigured
	credential           ledger.RuntimeCredentialChanged
	grant, inspect, turn uuid.UUID
	template             ledger.RuntimePaidReserved
	next                 int
}

type paidPayload interface {
	Encode() (json.RawMessage, error)
}

func paidEncode(t testing.TB, v paidPayload) json.RawMessage {
	t.Helper()
	b, err := v.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Only the golden's identity prefix and typed profile/descriptor examples are
// used here. Every paid transition is independently constructed and submitted
// through the real ledger store; the frozen paid history is not rewritten.
func newPaidFixture(t testing.TB) *paidFixture {
	t.Helper()
	base, commits := openPaidFixture(t, 2)
	r := commits[0].Records[0]
	f := &paidFixture{serviceFixture: base, origin: ledger.Origin{AgentID: r.AgentID, SessionID: r.ExecutionSessionID, ContextID: r.ContextID}, grant: paidGoldenID(0x900), inspect: paidGoldenID(0x901), turn: paidGoldenID(0x902), next: 0xa00}
	var inputContext uuid.UUID
	for _, c := range commits {
		for _, r := range c.Records {
			var err error
			switch {
			case r.Type == ledger.TypeRuntimePaidRouteConfigured && f.route.Version == 0:
				f.route, err = ledger.DecodeRuntimePaidRouteConfigured(r.Payload)
			case r.Type == ledger.TypeRuntimeCredentialChanged && f.credential.Version == 0:
				f.credential, err = ledger.DecodeRuntimeCredentialChanged(r.Payload)
			case r.Type == ledger.TypeRuntimePaidReserved && f.template.Version == 0:
				f.template, err = ledger.DecodeRuntimePaidReserved(r.Payload)
				inputContext = r.ContextID
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	source, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, inputContext.String(), &f.service.dirs)
	if err != nil {
		t.Fatal(err)
	}
	body, err := readObject(source, Head{SHA256: f.template.InputObject.SHA256, Bytes: f.template.InputObject.Bytes})
	if err != nil {
		t.Fatal(err)
	}
	target, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.publishObject(body); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	binding := bindingOf(f.origin)
	for _, scope := range []ledger.ScopeKey{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}} {
		_, err := f.service.ConfigureCapacity(ctx, binding, CapacitySpec{CommandID: f.id(), Scope: scope, Limits: scopedCapacity()})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range []GrantSpec{
		{GrantID: f.grant, Grantee: binding, Operations: []string{"grant.issue", "model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: scopedCapacity(), Lifetime: "until_revoked"},
		{GrantID: f.inspect, Grantee: binding, Operations: []string{"turn.start", "turn.inspect", "turn.cancel"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: scopedCapacity(), Lifetime: "until_revoked"},
	} {
		_, err := f.service.IssueGrant(ctx, binding, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec})
		if err != nil {
			t.Fatal(err)
		}
	}
	agent, err := f.service.BindAgent(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.StartTurn(ctx, TurnAdmission{CommandID: f.id(), GrantID: f.inspect, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	f.credential.ExpectedVersion, f.credential.Version = 0, 1
	f.append(t, f.credential)
	f.route.Profile.VerifiedUTCNS = f.service.p.reviewFloor - 1
	f.route.Profile.ExpiresUTCNS = f.service.p.reviewFloor + int64(7*24*time.Hour)
	f.route.ObservedUTCNS = f.service.p.reviewFloor
	f.append(t, f.route)
	for _, scope := range []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}, {Kind: "credential", ID: f.credential.ScopeID}, {Kind: "grant", ID: f.grant}} {
		f.spend(t, scope, ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "finite", CeilingMicros: new(int64(100000)), WarningPPM: new(int64(800000))})
	}
	return f
}
func (f *paidFixture) id() uuid.UUID { f.next++; return paidGoldenID(f.next) }
func (f *paidFixture) spend(t testing.TB, scope ledger.PaidScope, terms ledger.SpendTerms) {
	t.Helper()
	version := f.service.p.book.paid[scope].version
	f.append(t, ledger.RuntimeSpendConfigured{Scope: scope, ExpectedVersion: version, Version: version + 1, Terms: terms, AuthorityVersion: f.service.p.authority})
}
func (f *paidFixture) drafts(t testing.TB, effect paidPayload) []ledger.Draft {
	t.Helper()
	sub := ledger.PaidSubmission{Schema: 4, Actor: "owner", Origin: f.origin}
	var typ, outcome string
	var version int64
	var attempt, turn uuid.UUID
	switch p := effect.(type) {
	case ledger.RuntimePaidRouteConfigured:
		digest, err := p.Profile.SHA256()
		if err != nil {
			t.Fatal(err)
		}
		typ, outcome, version = ledger.TypeRuntimePaidRouteConfigured, "configured", p.Version
		sub.Operation, sub.Arguments = "paid.route.configure", ledger.PaidRouteArguments{RouteID: p.RouteID, ExpectedVersion: p.ExpectedVersion, Enabled: p.Enabled, CredentialScopeID: p.CredentialScopeID, ProfileSHA256: digest}
	case ledger.RuntimeCredentialChanged:
		typ, version = ledger.TypeRuntimeCredentialChanged, p.Version
		if p.Action == "bind" {
			outcome, sub.Operation, sub.Arguments = "bound", "paid.credential.bind", ledger.PaidCredentialBindArguments{ScopeID: p.ScopeID, ExpectedVersion: p.ExpectedVersion, Provider: p.Provider, CredentialVersion: p.CredentialVersion, SourceKind: p.SourceKind, SourceLocator: p.SourceLocator, RevokePreviousVersion: p.RevokePreviousVersion}
		} else {
			outcome, sub.Operation, sub.Arguments = "revoked", "paid.credential.revoke", ledger.PaidCredentialRevokeArguments{ScopeID: p.ScopeID, ExpectedVersion: p.ExpectedVersion, Provider: p.Provider, CredentialVersion: p.CredentialVersion}
		}
	case ledger.RuntimeSpendConfigured:
		typ, outcome, version = ledger.TypeRuntimeSpendConfigured, "configured", p.Version
		sub.Operation, sub.Arguments = "paid.spend.configure", ledger.PaidSpendArguments{Scope: p.Scope, ExpectedVersion: p.ExpectedVersion, Terms: p.Terms, ParentGrantID: p.ParentGrantID}
	case ledger.RuntimePaidReserved:
		typ, outcome, version, attempt, turn = ledger.TypeRuntimePaidReserved, "reserved", p.Version, p.AttemptID, p.ParentTurnID
		sub.Actor, sub.Operation, sub.GrantID, sub.Arguments = "agent", "paid.admit", p.GrantID, paidAdmitArguments(p)
	case ledger.RuntimePaidDispatchIntent:
		typ, outcome, version, attempt, turn = ledger.TypeRuntimePaidDispatchIntent, "sent", p.Version, p.AttemptID, f.turn
		sub.Actor, sub.Operation, sub.GrantID, sub.Arguments = "core", "paid.dispatch", f.grant, ledger.PaidAttemptArguments{AttemptID: p.AttemptID, ExpectedVersion: p.ExpectedVersion}
	case ledger.RuntimePaidResult:
		typ, version, attempt, turn = ledger.TypeRuntimePaidResult, p.Version, p.AttemptID, f.turn
		sub.Actor, sub.GrantID = "core", f.grant
		switch p.Kind {
		case "released_unsent":
			outcome, sub.Operation, sub.Arguments = "released_unsent", "paid.release", ledger.PaidAttemptArguments{AttemptID: p.AttemptID, ExpectedVersion: p.ExpectedVersion}
		case "correction":
			outcome, sub.Operation, sub.Arguments = "corrected", "paid.correct", ledger.PaidCorrectArguments{AttemptID: p.AttemptID, ExpectedVersion: p.ExpectedVersion, PreviousResultSequence: p.PreviousResultSequence, PreviousEvidenceSHA256: p.PreviousEvidenceSHA256, EvidenceSHA256: p.EvidenceSHA256, EvidenceObject: *p.EvidenceObject, MessageObjects: p.MessageObjects}
		default:
			outcome = map[string]string{"unknown": "unknown", "usage": "settled", "no_charge": "no_charge"}[p.Kind]
			sub.Operation, sub.Arguments = "paid.observe", ledger.PaidObserveArguments{AttemptID: p.AttemptID, ExpectedVersion: p.ExpectedVersion, Kind: p.Kind, EvidenceSHA256: p.EvidenceSHA256, EvidenceObject: *p.EvidenceObject, MessageObjects: p.MessageObjects}
		}
	case ledger.RuntimePaidOverrunAcknowledged:
		typ, outcome, version, attempt = ledger.TypeRuntimePaidOverrunAcknowledged, "acknowledged", p.Version, p.AttemptID
		sub.Operation, sub.Arguments = "paid.overrun.acknowledge", ledger.PaidOverrunArguments{AttemptID: p.AttemptID, ExpectedVersion: p.ExpectedVersion, ResultSequence: p.ResultSequence, EvidenceSHA256: p.EvidenceSHA256, RetiredProfileSHA256: p.RetiredProfileSHA256}
	default:
		t.Fatalf("unsupported fixture effect %T", effect)
	}
	digest := ledger.Digest(sha256.Sum256(paidEncode(t, sub)))
	id := f.id()
	r := ledger.RuntimePaidReceipt{CommandID: id, SubmissionSHA256: digest, Submission: sub, Outcome: outcome, Version: version, AttemptID: attempt}
	result := make([]ledger.Draft, 3)
	for i, v := range []paidPayload{ledger.Command{CommandID: id, SubmissionSHA256: digest}, effect, r} {
		actor := ledger.Actor(sub.Actor)
		if i == 1 && sub.Operation == "paid.admit" {
			actor = ledger.ActorCore
		}
		result[i] = ledger.Draft{Type: []string{ledger.TypeCommand, typ, ledger.TypeRuntimePaidReceipt}[i], TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, TurnID: turn, OperationID: id, Payload: paidEncode(t, v)}
	}
	return result
}
func (f *paidFixture) append(t testing.TB, effect paidPayload) ledger.RuntimePaidReceipt {
	t.Helper()
	d := f.drafts(t, effect)
	_, _, _, g, err := decodeFamily(d, f.store.Durable()+1)
	if err == nil {
		_, err = f.service.planPaid(context.Background(), g, f.store.Durable()+1)
	}
	if err != nil {
		t.Fatalf("preflight %T: %v", effect, err)
	}
	if _, err = f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	err = f.service.refreshLocked(context.Background(), f.store.Durable())
	f.service.mu.Unlock()
	if err != nil {
		t.Fatalf("replay %T: %v", effect, err)
	}
	return g.receipt
}
func (f *paidFixture) reserve(t testing.TB) ledger.RuntimePaidReserved {
	t.Helper()
	p := f.reservation(t)
	f.append(t, p)
	return p
}

func (f *paidFixture) reservation(t testing.TB) ledger.RuntimePaidReserved {
	t.Helper()
	p := f.template
	p.AttemptID, p.GrantID, p.ParentTurnID, p.TaskBinding = f.id(), f.grant, f.turn, nil
	p.AuthorityVersion = f.service.p.authority
	p.RouteID, p.RouteVersion = f.route.RouteID, f.route.Version
	p.ProfileSHA256, _ = f.route.Profile.SHA256()
	p.Request.Credential = ledger.CredentialRef{ScopeID: f.credential.ScopeID, Version: f.credential.CredentialVersion}
	p.AdmissionUTCNS = f.service.p.reviewFloor
	p.AdmissionPeriod = paidPeriod(f.service.p.book.paidPeriodKind, p.AdmissionUTCNS)
	p.Scopes = []ledger.PaidScope{{Kind: "colony"}, {Kind: "agent", ID: f.origin.AgentID}, {Kind: "credential", ID: f.credential.ScopeID}, {Kind: "grant", ID: f.grant}}
	// Keep the request bytes and descriptor unchanged; only the profile's
	// historical verification lifetime was extended for these draft fixtures.
	p.Quote.TariffSHA256, _ = f.route.Profile.TariffSHA256()
	p.Quote.CapabilityVersion, p.Quote.TariffVersion = f.route.Profile.CapabilityVersion, f.route.Profile.TariffVersion
	q, err := providers.QuoteSHA256([32]byte(p.Quote.RequestSHA256), p.Quote.CapabilityVersion, p.Quote.TariffVersion, [32]byte(p.Quote.TariffSHA256), p.Quote.MaximumMicros, paidComponents(p.Quote))
	if err != nil {
		t.Fatal(err)
	}
	p.Quote.QuoteSHA256 = ledger.Digest(q)
	return p
}
func (f *paidFixture) dispatch(t testing.TB, p ledger.RuntimePaidReserved) ledger.RuntimePaidDispatchIntent {
	t.Helper()
	v := ledger.RuntimePaidDispatchIntent{AttemptID: p.AttemptID, ExpectedVersion: 1, Version: 2, AuthorityVersion: f.service.p.authority, RequestSHA256: p.Quote.RequestSHA256, QuoteSHA256: p.Quote.QuoteSHA256, Credential: p.Request.Credential, ObservedUTCNS: f.service.p.reviewFloor}
	f.append(t, v)
	return v
}
func (f *paidFixture) result(t testing.TB, p ledger.RuntimePaidReserved, input, output int64) ledger.RuntimePaidResult {
	t.Helper()
	units := []providers.Units{{Class: providers.CacheRead}, {Class: providers.CacheWrite1h}, {Class: providers.CacheWrite5m}, {Class: providers.Uncached, Count: input}, {Class: providers.Generated, Count: output}}
	cost, err := providers.PriceHistoricalUsage(providerProfile(f.route.Profile), units)
	if err != nil {
		t.Fatal(err)
	}
	usage := &ledger.PaidUsage{PricingTier: "standard", ServedTier: "standard"}
	for _, u := range units {
		usage.Classes = append(usage.Classes, ledger.PaidUnits{Class: u.Class, Units: u.Count})
	}
	evidence := ledger.Digest(sha256.Sum256([]byte(fmt.Sprintf("evidence-%s-%d", p.AttemptID, f.next))))
	return ledger.RuntimePaidResult{AttemptID: p.AttemptID, ExpectedVersion: 2, Version: 3, Kind: "usage", OutputStarted: new(output > 0), ProviderRequestID: "req-" + p.AttemptID.String(), ProviderMessageID: "msg-" + p.AttemptID.String(), EvidenceSHA256: evidence, EvidenceObject: &ledger.ObjectRef{SHA256: evidence, Bytes: 17}, Usage: usage, CostMicros: &cost, BoundViolation: cost > p.Quote.MaximumMicros}
}
func (f *paidFixture) view(t testing.TB, id uuid.UUID) PaidAttemptView {
	t.Helper()
	v, err := f.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: id})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *paidFixture) budget(t testing.TB, period string) PaidBudgetView {
	t.Helper()
	v, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}, Period: period})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *paidFixture) refuse(t *testing.T, d []ledger.Draft) {
	t.Helper()
	before := f.store.Durable()
	baseline, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Close()
	baseline.p.paidHotLimit = f.service.p.paidHotLimit
	baseline.p.prunePaid()
	_, _, _, g, err := decodeFamily(d, before+1)
	if err == nil {
		_, err = f.service.planPaid(context.Background(), g, before+1)
	}
	if err == nil || !reflect.DeepEqual(f.service.p, baseline.p) || f.store.Durable() != before {
		t.Fatalf("invalid preflight changed state or succeeded: %v", err)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	f.service = nil
	for _, draft := range d {
		if draft.TypeVersion == 1 {
			continue
		}
		var commits []ledger.Commit
		if err := baseline.scan(context.Background(), 0, before, func(c ledger.Commit) error {
			commits = append(commits, c)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		reviewFutureCatalogue(t, f.serviceFixture, commits, d)
		break
	}
	if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
		t.Fatal(err)
	}
	service, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if service != nil {
		_ = service.Close()
	}
	if err == nil || service != nil {
		t.Fatalf("invalid replay accepted: %v", err)
	}
}

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func serviceGolden(t testing.TB, through uint64, deps serviceDeps) (*serviceFixture, []ledger.Commit) {
	t.Helper()
	f, commits := scopedGoldenFixture(t, "91", 0)
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if through > 0 {
		complete := false
		for _, c := range commits {
			complete = complete || c.Last == through
		}
		if !complete {
			t.Fatalf("not a complete prefix: %d", through)
		}
		raw := readTestFile(t, filepath.Join("../../../contract/ledger/testdata/f3/91/log", ledger.SegmentName(1)))
		lines := bytes.SplitAfter(raw, []byte{'\n'})
		writeTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)), bytes.Join(lines[:through], nil))
	}
	var err error
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.service, _, err = openService(context.Background(), f.store, f.root, DefaultLimits(), deps)
	if err != nil {
		t.Fatal(err)
	}
	return f, commits
}
func serviceObservedAt(t testing.TB, commits []ledger.Commit, seq uint64) ledger.RuntimeServiceObserved {
	t.Helper()
	for _, c := range commits {
		for _, r := range c.Records {
			if r.Seq == seq {
				v, err := ledger.DecodeRuntimeServiceObserved(r.Payload)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
		}
	}
	t.Fatalf("missing service observation %d", seq)
	return ledger.RuntimeServiceObserved{}
}
func serviceCommitAt(t testing.TB, commits []ledger.Commit, first uint64) ledger.Commit {
	t.Helper()
	for _, c := range commits {
		if c.First == first {
			return c
		}
	}
	t.Fatalf("missing commit %d", first)
	return ledger.Commit{}
}
func serviceReopen(t testing.TB, f *serviceFixture, deps serviceDeps) Reconciliation {
	t.Helper()
	if f.service != nil {
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var report Reconciliation
	var err error
	f.service, report, err = openService(context.Background(), f.store, f.root, DefaultLimits(), deps)
	if err != nil {
		t.Fatal(err)
	}
	return report
}
func serviceRecord(t testing.TB, s *Service, e ledger.RuntimeServiceObserved) ledger.RuntimeServiceReceipt {
	t.Helper()
	r, err := s.recordServiceEvidence(context.Background(), e.ObservationID, e)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func serviceSnapshot(t testing.TB, s *Service) ServiceSnapshot {
	t.Helper()
	v, err := s.ServiceStatusSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func serviceCountRecords(t testing.TB, s *Service, typ string) int {
	t.Helper()
	count := 0
	if err := s.scan(context.Background(), 0, s.store.Durable(), func(c ledger.Commit) error {
		for _, r := range c.Records {
			if r.Type == typ {
				count++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

// Refused money demand names a real native turn and current spend configuration.
func serviceMoneyRefusal(t testing.TB, f *paidFixture, now int64) ledger.RuntimeServiceObserved {
	t.Helper()
	scope := ledger.PaidScope{Kind: "colony"}
	kind := f.service.p.book.paidPeriodKind
	f.spend(t, scope, ledger.SpendTerms{Enabled: true, PeriodKind: kind, Mode: "finite", CeilingMicros: new(int64(0))})
	at := time.Unix(0, now).UTC()
	var reset *int64
	switch kind {
	case "utc_day":
		reset = new(time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, time.UTC).UnixNano())
	case "utc_month":
		reset = new(time.Date(at.Year(), at.Month()+1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	}
	id := uuid.NewV7()
	cause := ledger.ServiceCauseSpec{Family: "money", Code: "owner_money_exhausted", ScopeKind: "colony"}
	key, err := cause.Key()
	if err != nil {
		t.Fatal(err)
	}
	src := ledger.ServiceSource{Kind: "money_refusal", ObservationID: id, ObservedThrough: int64(f.store.Durable()), CommandID: uuid.NewV7(), SubmissionSHA256: ledger.Digest(sha256.Sum256([]byte("bounded money demand"))), Origin: new(f.origin), TurnID: f.turn, ObservedUTCNS: now, Reason: "money_exhausted", ResetUTCNS: reset, Money: &ledger.ServiceMoney{Scope: scope, Period: paidPeriod(kind, now), SpendSequence: int64(f.store.Durable() - 1), Mode: "finite", OccupiedMicros: paidOccupied(f.service.p.book.paid[scope], paidPeriod(kind, now)).String(), CeilingMicros: new("0"), Coverage: "mandatory_scope"}}
	return ledger.RuntimeServiceObserved{ObservationID: id, AgentID: f.origin.AgentID, CauseID: uuid.NewV7(), CauseKey: key, Cause: &cause, Source: src}
}
func serviceReserveSource(t testing.TB, f *paidFixture) ledger.RuntimePaidReserved {
	t.Helper()
	if f.service.paid == nil {
		return f.reserve(t)
	}
	agent, err := f.service.BindAgent(context.Background(), bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	_, bound, err := agent.AdmitPaid(context.Background(), f.prepare(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bound.Admission()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func servicePaidFailure(t testing.TB, f *paidFixture, reason string, now int64) ledger.RuntimeServiceObserved {
	t.Helper()
	paid := serviceReserveSource(t, f)
	intent := f.dispatch(t, paid)
	ref := ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256([]byte("nonfinancial service source"))), Bytes: 27}
	id := uuid.NewV7()
	cause := ledger.ServiceCauseSpec{Family: "provider", Code: reason, ScopeKind: "provider_route", Provider: f.route.Profile.Provider, Model: f.route.Profile.ModelID, Region: new(f.route.Profile.Region), AuthoritySHA256: ledger.Digest(sha256.Sum256([]byte(f.route.Profile.Authority)))}
	switch reason {
	case "rate_limited", "scoped_denied":
		cause.ScopeKind = "account_route"
		cause.CredentialScopeID = f.credential.ScopeID
		if reason == "scoped_denied" {
			cause.CredentialVersion = f.credential.CredentialVersion
		}
	case "credential_denied":
		cause.ScopeKind = "credential"
		cause.CredentialScopeID = f.credential.ScopeID
		cause.CredentialVersion = f.credential.CredentialVersion
		cause.Model = ""
		cause.Region = nil
		cause.AuthoritySHA256 = ledger.Digest{}
	case "quota_exhausted":
		cause.ScopeKind = "account"
		cause.CredentialScopeID = f.credential.ScopeID
		cause.Model = ""
		cause.Region = nil
	case "unreachable":
		cause.ScopeKind = "endpoint"
		cause.Model = ""
		cause.Region = nil
	}
	key, err := cause.Key()
	if err != nil {
		t.Fatal(err)
	}
	src := ledger.ServiceSource{Kind: "paid_call", ObservationID: id, ObservedThrough: int64(f.store.Durable()), SourceSequence: new(int64(f.store.Durable() - 1)), Origin: new(f.origin), TurnID: f.turn, AttemptID: intent.AttemptID, Object: &ref, ObservedUTCNS: now, Reason: reason, Provenance: "provider_response"}
	if reason == "unreachable" {
		src.Provenance = "transport"
	}
	return ledger.RuntimeServiceObserved{ObservationID: id, AgentID: f.origin.AgentID, CauseID: uuid.NewV7(), CauseKey: key, Cause: &cause, Source: src}
}
func serviceSuccessfulCall(t testing.TB, f *paidFixture, cause uuid.UUID, now int64) ledger.RuntimeServiceObserved {
	t.Helper()
	paid := serviceReserveSource(t, f)
	intent := f.dispatch(t, paid)
	id := uuid.NewV7()
	s := f.service
	c := s.p.service.causes[cause]
	m := s.p.service.members[serviceMemberKey{f.origin.AgentID, cause}]
	object := ServiceCompletionObject{Schema: 1, Kind: "service_completion", ObservationID: id, ThroughSequence: int64(f.store.Durable()), Permits: []ledger.ServiceCompletionPermit{{CauseID: cause, CauseVersion: c.snapshot.Version, MemberGeneration: m.snapshot.Generation, Kind: "same_scope"}}}
	ref, err := s.publishServiceCompletion(context.Background(), f.origin, object)
	if err != nil {
		t.Fatal(err)
	}
	src := ledger.ServiceSource{Kind: "successful_call", ObservationID: id, ObservedThrough: object.ThroughSequence, SourceSequence: new(int64(f.store.Durable() - 1)), Origin: new(f.origin), TurnID: f.turn, AttemptID: intent.AttemptID, Object: &ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256([]byte("successful provider metadata"))), Bytes: 28}, ObservedUTCNS: now, Reason: "provider_completed", Provenance: "provider_response", Completion: &ledger.ServiceCompletion{Provider: f.route.Profile.Provider, Terminal: "end_turn"}}
	return ledger.RuntimeServiceObserved{ObservationID: id, AgentID: f.origin.AgentID, Source: src, CompletionPermitsObject: &ref}
}

func serviceAdmissionClear(t testing.TB, f *paidFixture, cause uuid.UUID, now int64) ledger.RuntimeServiceObserved {
	t.Helper()
	scope := ledger.PaidScope{Kind: "colony"}
	kind := f.service.p.book.paidPeriodKind
	f.spend(t, scope, ledger.SpendTerms{Enabled: true, PeriodKind: kind, Mode: "unlimited"})
	spend := int64(f.store.Durable() - 1)
	period := paidPeriod(kind, f.service.p.reviewFloor)
	occupied := paidOccupied(f.service.p.book.paid[scope], period).String()
	f.reserve(t)
	id := uuid.NewV7()
	src := ledger.ServiceSource{Kind: "cause_clear", ObservationID: id, ObservedThrough: int64(f.store.Durable()), SourceSequence: new(int64(f.store.Durable() - 1)), Origin: new(f.origin), TurnID: f.turn, ObservedUTCNS: now, Reason: "budget_available", Money: &ledger.ServiceMoney{Scope: scope, Period: period, SpendSequence: spend, Mode: "unlimited", OccupiedMicros: occupied, Coverage: "mandatory_scope"}}
	return ledger.RuntimeServiceObserved{ObservationID: id, AgentID: f.origin.AgentID, CauseID: cause, Source: src}
}

func serviceNativePaidFixture(t testing.TB, f *serviceFixture, src ledger.ServiceSource, next int) *paidFixture {
	t.Helper()
	a, found, err := f.service.loadPaid(context.Background(), nil, src.AttemptID, f.store.Durable(), ledger.Digest{})
	if err != nil || !found {
		t.Fatalf("native paid fixture binding: %v", err)
	}
	return &paidFixture{serviceFixture: f, origin: a.origin, grant: a.admission.GrantID, turn: a.admission.ParentTurnID, template: a.admission, next: next,
		route:      ledger.RuntimePaidRouteConfigured{RouteID: a.admission.RouteID, Version: a.admission.RouteVersion, Profile: a.profile},
		credential: ledger.RuntimeCredentialChanged{ScopeID: a.admission.Request.Credential.ScopeID, CredentialVersion: a.admission.Request.Credential.Version}}
}

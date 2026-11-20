package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

func serviceID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a9100-0000-7000-8000-%012x", n))
}

func serviceCauseFixtures() []ServiceCauseSpec {
	authority := scopedHash("synthetic authority")
	return []ServiceCauseSpec{
		{Family: "provider", Code: "credential_denied", ScopeKind: "credential", Provider: "anthropic", CredentialScopeID: serviceID(1), CredentialVersion: serviceID(2)},
		{Family: "provider", Code: "scoped_denied", ScopeKind: "account_route", Provider: "anthropic", CredentialScopeID: serviceID(1), CredentialVersion: serviceID(2), Model: "synthetic-model", Region: new(""), AuthoritySHA256: authority},
		{Family: "provider", Code: "quota_exhausted", ScopeKind: "account", Provider: "anthropic", CredentialScopeID: serviceID(1), AuthoritySHA256: authority},
		{Family: "provider", Code: "rate_limited", ScopeKind: "account_route", Provider: "anthropic", CredentialScopeID: serviceID(1), Model: "synthetic-model", Region: new("global"), AuthoritySHA256: authority},
		{Family: "provider", Code: "overloaded", ScopeKind: "provider_route", Provider: "anthropic", Model: "synthetic-model", Region: new("global"), AuthoritySHA256: authority},
		{Family: "provider", Code: "unreachable", ScopeKind: "endpoint", Provider: "anthropic", AuthoritySHA256: authority},
		{Family: "money", Code: "owner_money_exhausted", ScopeKind: "colony"},
		{Family: "money", Code: "owner_money_exhausted", ScopeKind: "agent", AgentID: serviceID(3)},
		{Family: "money", Code: "owner_money_exhausted", ScopeKind: "credential", Provider: "anthropic", CredentialScopeID: serviceID(1)},
		{Family: "execution", Code: "private_integrity", ScopeKind: "private", AgentID: serviceID(3), PrivateScope: "agent", Revision: new(int64(1))},
		{Family: "execution", Code: "private_integrity", ScopeKind: "private", AgentID: serviceID(3), ContextID: serviceID(4), PrivateScope: "context", Revision: new(int64(1))},
		{Family: "execution", Code: "process_unconfirmed", ScopeKind: "colony"},
	}
}

func TestServiceCauseCanonicalIdentity(t *testing.T) {
	t.Parallel()
	seen := map[Digest]bool{}
	for _, p := range serviceCauseFixtures() {
		wire := mustEncode(p)
		key, err := p.Key()
		if err != nil || key != Digest(sha256.Sum256(wire)) || seen[key] {
			t.Fatalf("cause identity collision or wrong preimage: %v", err)
		}
		seen[key] = true
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		fields["future"] = json.RawMessage(`{"x":[null,true]}`)
		reordered, err := json.MarshalIndent(fields, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeServiceCauseSpec(reordered)
		if err != nil || !reflect.DeepEqual(got, p) || !bytes.Equal(mustEncode(got), wire) {
			t.Fatalf("canonical cause changed: %v", err)
		}
		gotKey, err := got.Key()
		if err != nil || gotKey != key {
			t.Fatal("extension changed CauseKey")
		}
	}
	p := serviceCauseFixtures()[1]
	if !slices.Equal(loopMemberOrder(t, mustEncode(p)), []string{"family", "code", "scope_kind", "provider", "credential_scope_id", "credential_version", "model", "region", "authority_sha256"}) {
		t.Fatal("cause preimage order")
	}
	for _, edit := range []func(*ServiceCauseSpec){
		func(p *ServiceCauseSpec) { p.Region = nil },
		func(p *ServiceCauseSpec) { p.Model = "" },
		func(p *ServiceCauseSpec) { p.CredentialVersion = uuid.UUID{} },
		func(p *ServiceCauseSpec) { p.AuthoritySHA256 = Digest{} },
		func(p *ServiceCauseSpec) { p.AgentID = serviceID(3) },
		func(p *ServiceCauseSpec) { p.ScopeKind = "provider_route" },
	} {
		bad := p
		edit(&bad)
		serviceRejectBoth(t, "cause", bad)
	}
	endpoint := serviceCauseFixtures()[5]
	endpoint.Model = "synthetic-model"
	serviceRejectBoth(t, "cause", endpoint)
	for _, field := range []string{"family", "code", "scope_kind"} {
		raw := registrationReplace(t, mustEncode(p), `"`+field+`":"`+map[string]string{"family": p.Family, "code": p.Code, "scope_kind": p.ScopeKind}[field]+`"`, `"`+field+`":"future_value"`)
		got, err := DecodeServiceCauseSpec(raw)
		if err != nil || !bytes.Equal(mustEncode(got), raw) {
			t.Fatalf("unknown %s lost: %v", field, err)
		}
	}
}

func serviceSourceFixture() ServiceSource {
	return ServiceSource{Kind: "paid_call", ObservationID: serviceID(10), ObservedThrough: 20, SourceSequence: new(int64(18)), Origin: new(Origin{serviceID(3), serviceID(5), serviceID(4)}), TurnID: serviceID(6), AttemptID: serviceID(7), Object: new(ObjectRef{scopedHash("synthetic observation"), 21}), ObservedUTCNS: 1000, Reason: "overloaded", Provenance: "provider_response"}
}

func serviceObservationFixture() RuntimeServiceObserved {
	cause := serviceCauseFixtures()[4]
	key, _ := cause.Key()
	return RuntimeServiceObserved{ObservationID: serviceID(10), AgentID: serviceID(3), CauseID: serviceID(8), CauseKey: key, Cause: &cause, Source: serviceSourceFixture()}
}

func serviceSuccessFixture(selected bool) ServiceSource {
	p := serviceSourceFixture()
	p.Kind, p.Reason = "successful_call", "provider_completed"
	p.ObservationID, p.AttemptID, p.ObservedUTCNS = serviceID(11), serviceID(12), 2000
	p.Completion = &ServiceCompletion{Provider: "anthropic", Terminal: "end_turn"}
	if selected {
		p.Completion.Permit = &ServiceCompletionPermit{CauseID: serviceID(8), CauseVersion: 1, MemberGeneration: 1, Kind: "same_scope"}
	}
	return p
}

func serviceSubmissionFixtures() []ServiceSubmission {
	e := serviceObservationFixture()
	first := e.Source
	a := ServiceStateArguments{CauseKey: e.CauseKey, Cause: *e.Cause, Source: first, FirstSource: &first, DueUTCNS: new(first.ObservedUTCNS + ServiceGraceNS)}
	state := func(op string, a ServiceStateArguments) ServiceSubmission {
		return ServiceSubmission{Schema: 9, Actor: "core", Operation: op, CauseID: e.CauseID, AgentID: e.AgentID, ExpectedCauseVersion: new(int64(1)), ExpectedMemberVersion: new(int64(1)), ExpectedGeneration: new(int64(1)), Arguments: a}
	}
	record := ServiceSubmission{Schema: 9, Actor: "core", Operation: "service.record", CauseID: e.CauseID, AgentID: e.AgentID, Arguments: ServiceRecordArguments{e.ObservationID, e.CauseKey, e.Cause, e.Source, nil}}
	observe := state("service.observe", a)
	observe.ExpectedCauseVersion, observe.ExpectedMemberVersion, observe.ExpectedGeneration = new(int64(0)), new(int64(0)), new(int64(0))
	a.FirstSource, a.DueUTCNS = nil, nil
	a.Source = ServiceSource{Kind: "grace_expiry", ObservationID: serviceID(13), ObservedThrough: 20, ObservedUTCNS: first.ObservedUTCNS + ServiceGraceNS, Reason: "grace_due"}
	a.OutageID, a.NoticeID = serviceID(14), serviceID(15)
	expire := state("service.expire", a)
	a.Source, a.NoticeID = serviceSuccessFixture(true), uuid.UUID{}
	complete := state("service.complete", a)
	a.OutageID = uuid.UUID{}
	clear := state("service.clear", a)
	clear.AgentID, clear.ExpectedMemberVersion, clear.ExpectedGeneration = uuid.UUID{}, nil, nil
	a.Source = ServiceSource{Kind: "demand_withdrawn", ObservationID: serviceID(16), ObservedThrough: 20, ObservedUTCNS: 2000, Reason: "budget_available", Money: &ServiceMoney{Scope: PaidScope{Kind: "colony"}, Period: "lifetime", SpendSequence: 17, Mode: "unlimited", OccupiedMicros: "0", Coverage: "mandatory_scope"}}
	withdraw := state("service.withdraw", a)
	return []ServiceSubmission{record, observe, expire, clear, complete, withdraw}
}

func serviceReceiptFixture(s ServiceSubmission) RuntimeServiceReceipt {
	command := serviceID(30)
	types := []string{TypeRuntimeServiceAgentChanged}
	switch s.Operation {
	case "service.record":
		command = s.Arguments.(ServiceRecordArguments).ObservationID
		types = []string{TypeRuntimeServiceObserved}
	case "service.observe":
		types = []string{TypeRuntimeServiceCauseChanged, TypeRuntimeServiceAgentChanged}
	case "service.clear":
		types = []string{TypeRuntimeServiceCauseChanged}
	case "service.expire":
		types = []string{TypeRuntimeServiceAgentChanged, TypeRuntimeServiceOutageNotice}
	}
	effects := make([]ServiceEffect, len(types))
	for i, typ := range types {
		effects[i] = ServiceEffect{typ, int64(22 + i)}
	}
	return RuntimeServiceReceipt{command, Digest(sha256.Sum256(mustEncode(s))), s, "recorded", effects, 0}
}

func serviceCodecs() []struct {
	typ string
	p   payload
} {
	e := serviceObservationFixture()
	k := RuntimeServiceCauseChanged{e.CauseID, e.CauseKey, 0, 1, *e.Cause, "active", e.Source}
	m := RuntimeServiceAgentChanged{AgentID: e.AgentID, CauseID: e.CauseID, Version: 1, Generation: 1, Phase: "pending", FirstSource: e.Source, DueUTCNS: new(e.Source.ObservedUTCNS + ServiceGraceNS), Source: e.Source}
	return []struct {
		typ string
		p   payload
	}{
		{TypeRuntimeServiceObserved, e}, {TypeRuntimeServiceCauseChanged, k}, {TypeRuntimeServiceAgentChanged, m},
		{TypeRuntimeServiceOutageNotice, RuntimeServiceOutageNotice{serviceID(14), e.CauseID, "began", serviceID(15), 23}},
		{TypeRuntimeServiceReceipt, serviceReceiptFixture(serviceSubmissionFixtures()[1])},
		{"schema-9", serviceSubmissionFixtures()[0]}, {"cause", *e.Cause},
	}
}

func decodeF3_91(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeServiceObserved:
		return DecodeRuntimeServiceObserved(js)
	case TypeRuntimeServiceCauseChanged:
		return DecodeRuntimeServiceCauseChanged(js)
	case TypeRuntimeServiceAgentChanged:
		return DecodeRuntimeServiceAgentChanged(js)
	case TypeRuntimeServiceOutageNotice:
		return DecodeRuntimeServiceOutageNotice(js)
	case TypeRuntimeServiceReceipt:
		return DecodeRuntimeServiceReceipt(js)
	case "schema-9":
		return DecodeServiceSubmission(js)
	case "cause":
		return DecodeServiceCauseSpec(js)
	default:
		return nil, fmt.Errorf("unknown service codec %s", typ)
	}
}

func serviceRejectBoth(t testing.TB, typ string, p payload) {
	t.Helper()
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid %s encoded: %v", typ, err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeF3_91(typ, raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid %s decoded: %v", typ, err)
	}
}

func TestServicePayloads(t *testing.T) {
	t.Parallel()
	for _, f := range serviceCodecs() {
		t.Run(f.typ, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(f.p)
			p, err := decodeF3_91(f.typ, wire)
			if err != nil || !reflect.DeepEqual(p, f.p) {
				t.Fatalf("roundtrip: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			for key := range fields {
				bad := make(map[string]json.RawMessage, len(fields))
				for k, v := range fields {
					bad[k] = v
				}
				bad[key] = json.RawMessage("null")
				raw, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := decodeF3_91(f.typ, raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted null %s: %v", key, err)
				}
			}
			for _, suffix := range []string{`,"future":{"array":[{"x":1,"x":2}]}}`, `,"future":{"x":1,"\u0078":2}}`, ",\"future\":\"\xff\"}", `} {}`} {
				if _, err := decodeF3_91(f.typ, append(bytes.Clone(wire[:len(wire)-1]), suffix...)); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted malformed extension: %v", err)
				}
			}
			extended := append(bytes.Clone(wire[:len(wire)-1]), `,"future":{"array":[null,{"new":true}]}}`...)
			p, err = decodeF3_91(f.typ, extended)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("extension changed preimage: %v", err)
			}
			for _, raw := range [][]byte{[]byte(`[]`), []byte(`null`), wire[:len(wire)-1]} {
				if _, err := decodeF3_91(f.typ, raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted incomplete/nonobject: %v", err)
				}
			}
			for _, size := range []int{MaxServicePayloadBytes, MaxServicePayloadBytes + 1} {
				raw := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, size-len(wire))...)
				_, err := decodeF3_91(f.typ, raw)
				if size == MaxServicePayloadBytes && err != nil || size > MaxServicePayloadBytes && !errors.Is(err, ErrInvalid) {
					t.Fatalf("complete input bound %d: %v", size, err)
				}
			}
		})
	}
}

func TestServiceCanonicalSubmission(t *testing.T) {
	t.Parallel()
	for _, s := range serviceSubmissionFixtures() {
		t.Run(s.Operation, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(s)
			want := []string{"schema", "actor", "operation", "cause_id"}
			if s.AgentID != (uuid.UUID{}) {
				want = append(want, "agent_id")
			}
			if s.ExpectedCauseVersion != nil {
				want = append(want, "expected_cause_version")
			}
			if s.ExpectedMemberVersion != nil {
				want = append(want, "expected_member_version", "expected_generation")
			}
			want = append(want, "arguments")
			if !slices.Equal(loopMemberOrder(t, wire), want) {
				t.Fatal("submission preimage order")
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			reordered, err := json.MarshalIndent(fields, "", " ")
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeServiceSubmission(reordered)
			if err != nil || !bytes.Equal(mustEncode(got), wire) {
				t.Fatalf("canonicalization: %v", err)
			}
			for _, key := range []string{"expected_cause_version", "expected_member_version", "expected_generation"} {
				bad := map[string]json.RawMessage{}
				for k, v := range fields {
					bad[k] = v
				}
				if _, present := fields[key]; present {
					delete(bad, key)
				} else {
					bad[key] = json.RawMessage("0")
				}
				js, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeServiceSubmission(js); !errors.Is(err, ErrInvalid) {
					t.Fatalf("wrong expectation presence %s", key)
				}
			}
			bad := s
			bad.Actor = "owner"
			serviceRejectBoth(t, "schema-9", bad)
			bad = s
			bad.Operation = "service.future"
			serviceRejectBoth(t, "schema-9", bad)
			bad = s
			bad.Arguments = map[string]any{}
			if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("arbitrary arguments")
			}
			r := serviceReceiptFixture(s)
			raw := mustEncode(r)
			if !slices.Equal(loopMemberOrder(t, raw), []string{"command_id", "submission_sha256", "submission", "outcome", "effects", "source_through"}) {
				t.Fatal("receipt preimage order")
			}
			r.Submission.AgentID = serviceID(99)
			serviceRejectBoth(t, TypeRuntimeServiceReceipt, r)
		})
	}
}

func TestServiceObservationPresence(t *testing.T) {
	t.Parallel()
	e := serviceObservationFixture()
	success := e
	success.ObservationID, success.CauseID, success.CauseKey, success.Cause = serviceID(11), uuid.UUID{}, Digest{}, nil
	success.Source = serviceSuccessFixture(false)
	success.CompletionPermitsObject = &ObjectRef{scopedHash("synthetic permits"), 16}
	for _, good := range []RuntimeServiceObserved{e, success} {
		if _, err := DecodeRuntimeServiceObserved(mustEncode(good)); err != nil {
			t.Fatal(err)
		}
	}
	bad := success
	bad.Source = serviceSuccessFixture(true)
	serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	bad = success
	bad.CompletionPermitsObject = nil
	serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	bad = success
	bad.CauseID = e.CauseID
	serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	for _, kind := range []string{"cause_clear", "demand_withdrawn"} {
		clear := RuntimeServiceObserved{ObservationID: serviceID(16), AgentID: e.AgentID, CauseID: e.CauseID, Source: ServiceSource{Kind: kind, ObservationID: serviceID(16), ObservedThrough: 20, ObservedUTCNS: 2000, Reason: "integrity_verified"}}
		if _, err := DecodeRuntimeServiceObserved(mustEncode(clear)); err != nil {
			t.Fatal(err)
		}
		bad = clear
		bad.CauseKey, bad.Cause = e.CauseKey, e.Cause
		serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
		bad = clear
		bad.CauseID = uuid.UUID{}
		serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	}
	expire := serviceSubmissionFixtures()[2]
	timer := expire.Arguments.(ServiceStateArguments).Source
	bad = RuntimeServiceObserved{ObservationID: timer.ObservationID, AgentID: e.AgentID, CauseID: e.CauseID, Source: timer}
	serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	record := ServiceSubmission{Schema: 9, Actor: "core", Operation: "service.record", AgentID: bad.AgentID, CauseID: bad.CauseID, Arguments: ServiceRecordArguments{ObservationID: timer.ObservationID, Source: timer}}
	serviceRejectBoth(t, "schema-9", record)
	if _, err := DecodeServiceSubmission(mustEncode(expire)); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{3, 4} {
		s := serviceSubmissionFixtures()[index]
		a := s.Arguments.(ServiceStateArguments)
		a.Source.Completion.Permit = nil
		s.Arguments = a
		serviceRejectBoth(t, "schema-9", s)
	}
}

func TestServiceNumbersTokensAndBounds(t *testing.T) {
	t.Parallel()
	wire := mustEncode(serviceObservationFixture())
	for _, row := range []struct{ old, replacement string }{
		{`"observed_through":20`, `"observed_through":-0`},
		{`"observed_through":20`, `"observed_through":1e1`},
		{`"observed_through":20`, `"observed_through":1.0`},
		{`"observed_utc_ns":1000`, `"observed_utc_ns":9223372036854775808`},
		{`"source_sequence":18`, `"source_sequence":0`},
		{serviceID(10).String(), "019a9100-0000-4000-8000-00000000000a"},
		{serviceID(10).String(), strings.ToUpper(serviceID(10).String())},
		{scopedHash("synthetic observation").String(), strings.Repeat("0", 64)},
		{scopedHash("synthetic observation").String(), strings.ToUpper(scopedHash("synthetic observation").String())},
		{`"clock_held":false`, `"clock_held":null`},
	} {
		if _, err := DecodeRuntimeServiceObserved(registrationReplace(t, wire, row.old, row.replacement)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted numeric/identity vector %s", row.replacement)
		}
	}
	for _, token := range []string{"", "Upper", "has-dash", "a.b", "é", strings.Repeat("a", 65)} {
		p := serviceObservationFixture()
		p.Source.Reason = token
		serviceRejectBoth(t, TypeRuntimeServiceObserved, p)
	}
	e := serviceObservationFixture()
	e.Source.Reason = strings.Repeat("a", 64)
	if _, err := DecodeRuntimeServiceObserved(mustEncode(e)); err != nil {
		t.Fatal(err)
	}
	m := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
	m.ExpectedVersion, m.Version, m.Generation, m.Phase = math.MaxInt64-1, math.MaxInt64, math.MaxInt64, "future_phase"
	if _, err := DecodeRuntimeServiceAgentChanged(mustEncode(m)); err != nil {
		t.Fatal(err)
	}
	m.ExpectedVersion = math.MaxInt64
	serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, m)
	m = serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
	m.FirstSource.ObservedUTCNS = math.MaxInt64 - ServiceGraceNS + 1
	serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, m)
	// The receipt has its own complete-input ceiling, independently of schema 9.
	s := serviceSubmissionFixtures()[0]
	js := mustEncode(s)
	extended := append(bytes.Clone(js[:len(js)-1]), `,"future":"`+strings.Repeat("x", MaxServicePayloadBytes-len(js)-12)+`"}`...)
	if len(extended) != MaxServicePayloadBytes {
		t.Fatal("cap vector")
	}
	if _, err := DecodeServiceSubmission(extended); err != nil {
		t.Fatal(err)
	}
	receipt := bytes.Replace(mustEncode(serviceReceiptFixture(s)), js, extended, 1)
	if _, err := DecodeRuntimeServiceReceipt(receipt); !errors.Is(err, ErrInvalid) {
		t.Fatal("receipt escaped independent cap")
	}
}

func TestServiceMoneyWitness(t *testing.T) {
	t.Parallel()
	e := serviceObservationFixture()
	cause := serviceCauseFixtures()[6]
	e.Cause, e.CauseKey = &cause, Digest{}
	e.CauseKey, _ = cause.Key()
	e.Source = ServiceSource{Kind: "money_refusal", ObservationID: e.ObservationID, ObservedThrough: 20, CommandID: serviceID(40), SubmissionSHA256: scopedHash("synthetic rejected command"), Origin: serviceSourceFixture().Origin, TurnID: serviceID(6), ObservedUTCNS: 1000, Reason: "money_exhausted", Money: &ServiceMoney{Scope: PaidScope{Kind: "colony"}, Period: "lifetime", SpendSequence: 18, Mode: "finite", OccupiedMicros: "92233720368547758080", CeilingMicros: new("9223372036854775807"), Coverage: "mandatory_scope"}}
	if _, err := DecodeRuntimeServiceObserved(mustEncode(e)); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ServiceMoney){
		func(p *ServiceMoney) { p.OccupiedMicros = "01" },
		func(p *ServiceMoney) { p.OccupiedMicros = "0" },
		func(p *ServiceMoney) { p.CeilingMicros = new("9223372036854775808") },
		func(p *ServiceMoney) { p.CeilingMicros = nil },
		func(p *ServiceMoney) { p.Scope = PaidScope{"task", serviceID(44)} },
		func(p *ServiceMoney) { p.Mode, p.CeilingMicros = "unlimited", nil },
		func(p *ServiceMoney) { p.Mode = "future_mode" },
		func(p *ServiceMoney) { p.SpendSequence = 21 },
	} {
		bad := e
		bad.Source.Money = new(*e.Source.Money)
		mutate(bad.Source.Money)
		serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	}
	e.Source.Money.OccupiedMicros, e.Source.Money.CeilingMicros = "0", new("0")
	if _, err := DecodeRuntimeServiceObserved(mustEncode(e)); err != nil {
		t.Fatal(err)
	}
	e.Source.Money.Scope = PaidScope{"credential", serviceID(1)}
	e.Cause = new(serviceCauseFixtures()[8])
	e.CauseKey, _ = e.Cause.Key()
	e.Source.Money.Coverage, e.Source.Money.WorksetSHA256 = "complete_workset", scopedHash("synthetic workset")
	e.Source.Money.EligibleWorkCount, e.Source.Money.FailedWorkCount = new(int64(2)), new(int64(2))
	if _, err := DecodeRuntimeServiceObserved(mustEncode(e)); err != nil {
		t.Fatal(err)
	}
	e.Source.Money.FailedWorkCount = new(int64(1))
	serviceRejectBoth(t, TypeRuntimeServiceObserved, e)
	e.Source.Money.FailedWorkCount = new(int64(2))
	e.Source.Money.OccupiedMicros = strings.Repeat("9", MaxServicePayloadBytes)
	if _, err := e.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded compact output")
	}
}

func TestServiceCompletionAndPermitShapes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ provider, terminal string }{
		{"anthropic", "end_turn"}, {"anthropic", "max_tokens"}, {"anthropic", "stop_sequence"},
		{"anthropic", "tool_use"}, {"anthropic", "model_context_window_exceeded"}, {"openai", "completed"},
		{"future_provider", "future_terminal"},
	} {
		s := serviceSubmissionFixtures()[4]
		a := s.Arguments.(ServiceStateArguments)
		a.Source.Completion.Provider, a.Source.Completion.Terminal = row.provider, row.terminal
		for _, financial := range []string{"usage", "unknown"} {
			a.Source.Completion.NativeResultSequence, a.Source.Completion.FinancialKind = new(int64(19)), financial
			s.Arguments = a
			if _, err := DecodeServiceSubmission(mustEncode(s)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, mutate := range []func(*ServiceCompletion){
		func(p *ServiceCompletion) { p.Terminal = "refusal" },
		func(p *ServiceCompletion) { p.Terminal = "incomplete" },
		func(p *ServiceCompletion) { p.Terminal = "completed" },
		func(p *ServiceCompletion) { p.NativeResultSequence = new(int64(19)) },
		func(p *ServiceCompletion) { p.FinancialKind = "usage" },
		func(p *ServiceCompletion) { p.NativeResultSequence = new(int64(21)); p.FinancialKind = "usage" },
		func(p *ServiceCompletion) { p.NativeResultSequence = new(int64(19)); p.FinancialKind = "no_charge" },
		func(p *ServiceCompletion) { p.Permit.CauseID = serviceID(99) },
		func(p *ServiceCompletion) { p.Permit.CauseVersion = 2 },
		func(p *ServiceCompletion) { p.Permit.MemberGeneration = 2 },
		func(p *ServiceCompletion) { p.Permit.ProofSequence = new(int64(0)) },
	} {
		s := serviceSubmissionFixtures()[4]
		a := s.Arguments.(ServiceStateArguments)
		mutate(a.Source.Completion)
		s.Arguments = a
		serviceRejectBoth(t, "schema-9", s)
	}
	for _, kind := range []string{"same_scope", "blocked", "cleared", "credential_replaced", "execution_clear", "all_work_moved", "future_permit"} {
		s := serviceSubmissionFixtures()[4]
		a := s.Arguments.(ServiceStateArguments)
		permit := a.Source.Completion.Permit
		permit.Kind = kind
		if slices.Contains([]string{"cleared", "credential_replaced", "execution_clear"}, kind) {
			permit.ProofSequence = new(int64(18))
		}
		if kind == "all_work_moved" {
			permit.FailedWorkCount = new(int64(0))
			a.Source.WorksetSHA256 = scopedHash("complete workset")
			a.Source.FailedWorkCount = new(int64(0))
			a.Source.Reason = "all_work_moved"
		}
		s.Arguments = a
		if _, err := DecodeServiceSubmission(mustEncode(s)); err != nil {
			t.Fatal(err)
		}
		if permit.ProofSequence != nil {
			permit.ProofSequence = nil
			s.Arguments = a
			serviceRejectBoth(t, "schema-9", s)
		}
		if kind == "all_work_moved" {
			permit.FailedWorkCount = new(int64(1))
			s.Arguments = a
			serviceRejectBoth(t, "schema-9", s)
		}
	}
}

func TestServiceSourcePresenceAndUnknowns(t *testing.T) {
	t.Parallel()
	e := serviceObservationFixture()
	sourceWire, err := json.Marshal(e.Source)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kind", "observation_id", "observed_through", "source_sequence", "origin", "turn_id", "attempt_id", "object", "observed_utc_ns", "clock_held", "reason", "provenance"}
	if !slices.Equal(loopMemberOrder(t, sourceWire), want) {
		t.Fatal("source canonical order")
	}
	for _, key := range []string{"source_sequence", "command_id", "submission_sha256", "origin", "turn_id", "attempt_id", "object", "reset_utc_ns", "money", "completion", "workset_sha256", "failed_work_count", "execution_run_id", "execution_proof"} {
		var source map[string]json.RawMessage
		if err := json.Unmarshal(sourceWire, &source); err != nil {
			t.Fatal(err)
		}
		source[key] = json.RawMessage("null")
		raw, err := json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		wire := bytes.Replace(mustEncode(e), sourceWire, raw, 1)
		if _, err := DecodeRuntimeServiceObserved(wire); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted nested null %s", key)
		}
	}
	for _, mutate := range []func(*ServiceSource){
		func(p *ServiceSource) { p.SourceSequence = nil },
		func(p *ServiceSource) { p.Origin = nil },
		func(p *ServiceSource) { p.TurnID = uuid.UUID{} },
		func(p *ServiceSource) { p.AttemptID = uuid.UUID{} },
		func(p *ServiceSource) { p.Object = nil },
		func(p *ServiceSource) { p.Provenance = "" },
		func(p *ServiceSource) { p.CommandID = serviceID(90) },
		func(p *ServiceSource) { p.SubmissionSHA256 = scopedHash("unpaired") },
		func(p *ServiceSource) { p.ResetUTCNS = new(p.ObservedUTCNS) },
		func(p *ServiceSource) { p.ResetUTCNS = new(p.ObservedUTCNS + 1) },
		func(p *ServiceSource) { p.ExecutionRunID = serviceID(90) },
		func(p *ServiceSource) { p.WorksetSHA256 = scopedHash("unpaired") },
	} {
		bad := e
		mutate(&bad.Source)
		serviceRejectBoth(t, TypeRuntimeServiceObserved, bad)
	}
	for _, kind := range []string{"integrity_refusal", "process_refusal"} {
		p := e
		p.Source = ServiceSource{Kind: kind, ObservationID: e.ObservationID, ObservedThrough: 20, CommandID: serviceID(90), SubmissionSHA256: scopedHash("synthetic refusal"), Origin: e.Source.Origin, TurnID: e.Source.TurnID, ObservedUTCNS: 1000, Reason: "private_integrity"}
		cause := serviceCauseFixtures()[9]
		if kind == "process_refusal" {
			p.Source.Reason = "process_unconfirmed"
			cause = serviceCauseFixtures()[11]
		}
		p.Cause, p.CauseKey = &cause, Digest{}
		p.CauseKey, _ = cause.Key()
		if _, err := DecodeRuntimeServiceObserved(mustEncode(p)); err != nil {
			t.Fatal(err)
		}
		p.Source.CommandID, p.Source.SubmissionSHA256 = uuid.UUID{}, Digest{}
		serviceRejectBoth(t, TypeRuntimeServiceObserved, p)
	}
	for _, field := range []string{"kind", "reason", "provenance"} {
		wire := mustEncode(e)
		old := map[string]string{"kind": "paid_call", "reason": "overloaded", "provenance": "provider_response"}[field]
		wire = registrationReplace(t, wire, `"`+field+`":"`+old+`"`, `"`+field+`":"future_value"`)
		got, err := DecodeRuntimeServiceObserved(wire)
		if err != nil || !bytes.Equal(mustEncode(got), wire) {
			t.Fatalf("open source token %s: %v", field, err)
		}
	}
	for _, fixture := range []struct {
		typ, field, old string
		p               payload
	}{
		{TypeRuntimeServiceCauseChanged, "condition", "active", serviceCodecs()[1].p},
		{TypeRuntimeServiceAgentChanged, "phase", "pending", serviceCodecs()[2].p},
		{TypeRuntimeServiceOutageNotice, "edge", "began", serviceCodecs()[3].p},
		{TypeRuntimeServiceReceipt, "outcome", "recorded", serviceCodecs()[4].p},
	} {
		wire := registrationReplace(t, mustEncode(fixture.p), `"`+fixture.field+`":"`+fixture.old+`"`, `"`+fixture.field+`":"future_value"`)
		got, err := decodeF3_91(fixture.typ, wire)
		if err != nil || !bytes.Equal(mustEncode(got), wire) {
			t.Fatalf("open record token %s: %v", fixture.field, err)
		}
	}
	m := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
	m.FirstSource = serviceSuccessFixture(false)
	m.DueUTCNS = nil
	m.Phase = "lost"
	m.OutageID = serviceID(14)
	serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, m)
}

func TestServiceReceiptEffectGrammar(t *testing.T) {
	t.Parallel()
	s := serviceSubmissionFixtures()[1]
	for _, effects := range [][]ServiceEffect{
		nil, {}, {{TypeRuntimeServiceOutageNotice, 22}},
		{{TypeRuntimeServiceAgentChanged, 22}, {TypeRuntimeServiceCauseChanged, 23}},
		{{TypeRuntimeServiceAgentChanged, 22}, {TypeRuntimeServiceAgentChanged, 23}},
		{{TypeRuntimeServiceCauseChanged, 22}, {TypeRuntimeServiceAgentChanged, 24}},
		{{TypeRuntimeServiceCauseChanged, math.MaxInt64}, {TypeRuntimeServiceAgentChanged, math.MaxInt64}},
		{{TypeRuntimePaidResult, 22}},
	} {
		r := serviceReceiptFixture(s)
		r.Effects = effects
		serviceRejectBoth(t, TypeRuntimeServiceReceipt, r)
	}
	r := serviceReceiptFixture(s)
	r.SourceThrough = r.Effects[0].Sequence - 2
	if _, err := DecodeRuntimeServiceReceipt(mustEncode(r)); err != nil {
		t.Fatal(err)
	}
	r.SourceThrough++
	serviceRejectBoth(t, TypeRuntimeServiceReceipt, r)
	r = serviceReceiptFixture(serviceSubmissionFixtures()[0])
	r.CommandID = serviceID(99)
	serviceRejectBoth(t, TypeRuntimeServiceReceipt, r)
}

func TestServiceFrozenSchema(t *testing.T) {
	t.Parallel()
	good := serviceSubmissionFixtures()[0]
	wire, err := good.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeServiceSubmission(wire); err != nil {
		t.Fatal(err)
	}
	for _, schema := range []int64{8, 10} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			t.Parallel()
			bad := good
			bad.Schema = schema
			serviceRejectBoth(t, "schema-9", bad)
			raw := registrationReplace(t, wire, `"schema":9`, fmt.Sprintf(`"schema":%d`, schema))
			if _, err := DecodeServiceSubmission(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted schema %d: %v", schema, err)
			}
		})
	}
}

func TestServiceFrozenRequiredMembers(t *testing.T) {
	t.Parallel()
	for _, f := range serviceCodecs() {
		t.Run(f.typ, func(t *testing.T) {
			t.Parallel()
			wire, err := f.p.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeF3_91(f.typ, wire); err != nil {
				t.Fatal(err)
			}
			for _, key := range loopMemberOrder(t, wire) {
				t.Run("without_"+key, func(t *testing.T) {
					t.Parallel()
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(wire, &fields); err != nil {
						t.Fatal(err)
					}
					delete(fields, key)
					raw, err := json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := decodeF3_91(f.typ, raw); !errors.Is(err, ErrInvalid) {
						t.Fatalf("accepted missing %s: %v", key, err)
					}
				})
			}
		})
	}
	t.Run("source", func(t *testing.T) {
		t.Parallel()
		e := serviceObservationFixture()
		e.CauseKey, e.Cause = Digest{}, nil
		e.Source = ServiceSource{Kind: "cause_clear", ObservationID: e.ObservationID, ObservedUTCNS: 1000, Reason: "integrity_verified"}
		wire, err := e.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRuntimeServiceObserved(wire); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"kind", "observation_id", "observed_through", "observed_utc_ns", "clock_held", "reason"} {
			t.Run("without_"+key, func(t *testing.T) {
				t.Parallel()
				var fields, source map[string]json.RawMessage
				if err := json.Unmarshal(wire, &fields); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(fields["source"], &source); err != nil {
					t.Fatal(err)
				}
				delete(source, key)
				raw, err := json.Marshal(source)
				if err != nil {
					t.Fatal(err)
				}
				fields["source"] = raw
				raw, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeRuntimeServiceObserved(raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted missing source %s: %v", key, err)
				}
			})
		}
	})
}

func TestServiceFrozenFailurePresence(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		edit func(*RuntimeServiceObserved)
	}{
		{"without_cause_id", func(p *RuntimeServiceObserved) { p.CauseID = uuid.UUID{} }},
		{"without_cause_key_and_spec", func(p *RuntimeServiceObserved) { p.CauseKey, p.Cause = Digest{}, nil }},
		{"with_completion_permits_object", func(p *RuntimeServiceObserved) {
			p.CompletionPermitsObject = &ObjectRef{scopedHash("synthetic permits"), 16}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			good := serviceObservationFixture()
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeServiceObserved(wire); err != nil {
				t.Fatal(err)
			}
			row.edit(&good)
			serviceRejectBoth(t, TypeRuntimeServiceObserved, good)
		})
	}
}

func TestServiceFrozenCauseKeyBindings(t *testing.T) {
	t.Parallel()
	other, err := serviceCauseFixtures()[0].Key()
	if err != nil {
		t.Fatal(err)
	}
	e := serviceObservationFixture()
	badE := e
	badE.CauseKey = other
	k := serviceCodecs()[1].p.(RuntimeServiceCauseChanged)
	badK := k
	badK.CauseKey = other
	s := serviceSubmissionFixtures()[1]
	badS := s
	a := s.Arguments.(ServiceStateArguments)
	a.CauseKey = other
	badS.Arguments = a
	for _, row := range []struct {
		name, typ string
		good, bad payload
	}{
		{"E", TypeRuntimeServiceObserved, e, badE},
		{"K", TypeRuntimeServiceCauseChanged, k, badK},
		{"state", "schema-9", s, badS},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wire, err := row.good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeF3_91(row.typ, wire); err != nil {
				t.Fatal(err)
			}
			serviceRejectBoth(t, row.typ, row.bad)
		})
	}
}

func TestServiceFrozenMemberPhases(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"pending", "withdrawn", "lost", "restored"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			good := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
			good.ExpectedVersion, good.Version, good.Phase = 1, 2, phase
			switch phase {
			case "withdrawn":
				good.Source = serviceSubmissionFixtures()[5].Arguments.(ServiceStateArguments).Source
			case "lost", "restored":
				good.OutageID = serviceID(14)
				if phase == "restored" {
					good.Source = serviceSuccessFixture(true)
					good.Source.ObservedUTCNS = *good.DueUTCNS + 1
				}
			}
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeServiceAgentChanged(wire); err != nil {
				t.Fatal(err)
			}
			if phase == "lost" || phase == "restored" {
				good.OutageID = uuid.UUID{}
				serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, good)
				return
			}
			t.Run("with_outage", func(t *testing.T) {
				t.Parallel()
				bad := good
				bad.OutageID = serviceID(14)
				serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, bad)
			})
			t.Run("without_due", func(t *testing.T) {
				t.Parallel()
				// An open reason imposes no onset rule, isolating the phase rule.
				good := good
				good.FirstSource.Reason = "future_reason"
				wire, err := good.Encode()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeRuntimeServiceAgentChanged(wire); err != nil {
					t.Fatal(err)
				}
				good.DueUTCNS = nil
				serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, good)
			})
		})
	}
}

func TestServiceFrozenCauseVersions(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		edit func(*RuntimeServiceCauseChanged)
	}{
		{"created_cleared", func(p *RuntimeServiceCauseChanged) { p.Condition = "cleared" }},
		{"skipped_version", func(p *RuntimeServiceCauseChanged) { p.Version = 2 }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			good := serviceCodecs()[1].p.(RuntimeServiceCauseChanged)
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeServiceCauseChanged(wire); err != nil {
				t.Fatal(err)
			}
			row.edit(&good)
			serviceRejectBoth(t, TypeRuntimeServiceCauseChanged, good)
		})
	}
}

func TestServiceFrozenOnsetDue(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, kind, reason, period string
		reset, due                 bool
	}{
		{"overloaded", "paid_call", "overloaded", "", false, true},
		{"credential_denied", "paid_call", "credential_denied", "", false, false},
		{"quota_reset", "paid_call", "quota_exhausted", "", true, true},
		{"quota_no_reset", "paid_call", "quota_exhausted", "", false, false},
		{"money_day_reset", "money_refusal", "money_exhausted", "1970-01-01", true, true},
		{"money_lifetime_no_reset", "money_refusal", "money_exhausted", "lifetime", false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			first := serviceSourceFixture()
			first.Kind, first.Reason = row.kind, row.reason
			if row.kind == "money_refusal" {
				first.Provenance, first.AttemptID, first.Object = "", uuid.UUID{}, nil
				first.Money = &ServiceMoney{Scope: PaidScope{Kind: "colony"}, Period: row.period, SpendSequence: 18, Mode: "finite", OccupiedMicros: "10", CeilingMicros: new("10"), Coverage: "mandatory_scope"}
			}
			if row.reset {
				first.ResetUTCNS = new(int64(86_400_000_000_000))
			}
			good := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
			good.FirstSource, good.Source = first, first
			good.Phase, good.OutageID = "lost", serviceID(14)
			good.DueUTCNS = nil
			if row.due {
				good.DueUTCNS = new(first.ObservedUTCNS + ServiceGraceNS)
			}
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeServiceAgentChanged(wire); err != nil {
				t.Fatal(err)
			}
			if row.due {
				good.DueUTCNS = nil
			} else {
				good.DueUTCNS = new(first.ObservedUTCNS + ServiceGraceNS)
			}
			serviceRejectBoth(t, TypeRuntimeServiceAgentChanged, good)
		})
	}
}

func TestServiceFrozenMoneyReset(t *testing.T) {
	t.Parallel()
	for _, period := range []string{"lifetime", "1970-01-01"} {
		t.Run(period, func(t *testing.T) {
			t.Parallel()
			good := serviceObservationFixture()
			good.Cause = new(serviceCauseFixtures()[6])
			good.CauseKey, _ = good.Cause.Key()
			good.Source.Kind, good.Source.Reason = "money_refusal", "money_exhausted"
			good.Source.Provenance, good.Source.AttemptID, good.Source.Object = "", uuid.UUID{}, nil
			good.Source.Money = &ServiceMoney{Scope: PaidScope{Kind: "colony"}, Period: period, SpendSequence: 18, Mode: "finite", OccupiedMicros: "10", CeilingMicros: new("10"), Coverage: "mandatory_scope"}
			if period != "lifetime" {
				good.Source.ResetUTCNS = new(int64(86_400_000_000_000))
			}
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeServiceObserved(wire); err != nil {
				t.Fatal(err)
			}
			if period == "lifetime" {
				good.Source.ResetUTCNS = new(int64(86_400_000_000_000))
			} else {
				good.Source.ResetUTCNS = nil
			}
			serviceRejectBoth(t, TypeRuntimeServiceObserved, good)
		})
	}
}

func TestServiceFrozenObserveIncident(t *testing.T) {
	t.Parallel()
	for _, grace := range []bool{true, false} {
		t.Run(fmt.Sprintf("grace_%t", grace), func(t *testing.T) {
			t.Parallel()
			good := serviceSubmissionFixtures()[1]
			a := good.Arguments.(ServiceStateArguments)
			if !grace {
				a.Cause = serviceCauseFixtures()[0]
				a.CauseKey, _ = a.Cause.Key()
				a.Source.Reason = "credential_denied"
				a.FirstSource = new(a.Source)
				a.DueUTCNS, a.OutageID = nil, serviceID(14)
				good.Arguments = a
			}
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeServiceSubmission(wire); err != nil {
				t.Fatal(err)
			}
			if grace {
				a.OutageID = serviceID(14)
			} else {
				a.OutageID = uuid.UUID{}
			}
			good.Arguments = a
			serviceRejectBoth(t, "schema-9", good)
		})
	}
}

func TestServiceFrozenOperationSourceKinds(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		index int
		kind  string
	}{
		{4, "cause_clear"}, {4, "grace_expiry"}, {2, "paid_call"},
		{1, "cause_clear"}, {5, "cause_clear"}, {3, "demand_withdrawn"}, {3, "paid_call"},
	} {
		good := serviceSubmissionFixtures()[row.index]
		t.Run(good.Operation+"/"+row.kind, func(t *testing.T) {
			t.Parallel()
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeServiceSubmission(wire); err != nil {
				t.Fatal(err)
			}
			a := good.Arguments.(ServiceStateArguments)
			switch row.kind {
			case "cause_clear":
				a.Source = ServiceSource{Kind: row.kind, ObservationID: a.Source.ObservationID, ObservedThrough: 20, ObservedUTCNS: 2000, Reason: "integrity_verified"}
			case "grace_expiry":
				a.Source = serviceSubmissionFixtures()[2].Arguments.(ServiceStateArguments).Source
			case "paid_call":
				a.Source = serviceSourceFixture()
			case "demand_withdrawn":
				a.Source = serviceSubmissionFixtures()[5].Arguments.(ServiceStateArguments).Source
			}
			good.Arguments = a
			serviceRejectBoth(t, "schema-9", good)
		})
	}
}

func TestServiceFrozenSubmissionPresence(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name  string
		index int
		edit  func(*ServiceSubmission, *ServiceStateArguments)
	}{
		{"clear_with_agent", 3, func(p *ServiceSubmission, _ *ServiceStateArguments) { p.AgentID = serviceID(3) }},
		{"member_without_agent", 2, func(p *ServiceSubmission, _ *ServiceStateArguments) { p.AgentID = uuid.UUID{} }},
		{"state_without_cause", 1, func(p *ServiceSubmission, _ *ServiceStateArguments) { p.CauseID = uuid.UUID{} }},
		{"expire_with_first_source", 2, func(_ *ServiceSubmission, a *ServiceStateArguments) { a.FirstSource = new(serviceSourceFixture()) }},
		{"complete_with_due", 4, func(_ *ServiceSubmission, a *ServiceStateArguments) {
			a.DueUTCNS = new(serviceSourceFixture().ObservedUTCNS + ServiceGraceNS)
		}},
		{"clear_with_outage", 3, func(_ *ServiceSubmission, a *ServiceStateArguments) { a.OutageID = serviceID(14) }},
		{"withdraw_with_outage", 5, func(_ *ServiceSubmission, a *ServiceStateArguments) { a.OutageID = serviceID(14) }},
		{"expire_without_outage", 2, func(_ *ServiceSubmission, a *ServiceStateArguments) { a.OutageID = uuid.UUID{} }},
		{"complete_without_outage", 4, func(_ *ServiceSubmission, a *ServiceStateArguments) { a.OutageID = uuid.UUID{} }},
		{"grace_observe_with_notice_without_outage", 1, func(_ *ServiceSubmission, a *ServiceStateArguments) { a.NoticeID = serviceID(15) }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			good := serviceSubmissionFixtures()[row.index]
			a := good.Arguments.(ServiceStateArguments)
			// Optional notice evidence is absent so removing an outage tests
			// the operation's requirement, not the notice/outage pairing.
			a.NoticeID = uuid.UUID{}
			good.Arguments = a
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeServiceSubmission(wire); err != nil {
				t.Fatal(err)
			}
			row.edit(&good, &a)
			good.Arguments = a
			serviceRejectBoth(t, "schema-9", good)
		})
	}
}

func TestServiceFrozenReceiptEffects(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		index  int
		effect string
	}{
		{3, TypeRuntimeServiceAgentChanged},
		{4, TypeRuntimeServiceCauseChanged},
	} {
		s := serviceSubmissionFixtures()[row.index]
		t.Run(s.Operation, func(t *testing.T) {
			t.Parallel()
			good := serviceReceiptFixture(s)
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeServiceReceipt(wire); err != nil {
				t.Fatal(err)
			}
			good.Effects = []ServiceEffect{{row.effect, 22}}
			serviceRejectBoth(t, TypeRuntimeServiceReceipt, good)
		})
	}
}

func TestServiceFrozenSourceBindings(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, typ string
		makePair  func() (payload, payload)
	}{
		{"E_foreign_agent", TypeRuntimeServiceObserved, func() (payload, payload) {
			good := serviceObservationFixture()
			bad := good
			bad.AgentID = serviceID(99)
			return good, bad
		}},
		{"M_foreign_agent", TypeRuntimeServiceAgentChanged, func() (payload, payload) {
			good := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
			bad := good
			bad.AgentID = serviceID(99)
			return good, bad
		}},
		{"complete_foreign_agent", "schema-9", func() (payload, payload) {
			good := serviceSubmissionFixtures()[4]
			bad := good
			bad.AgentID = serviceID(99)
			return good, bad
		}},
		{"K_foreign_permit_cause", TypeRuntimeServiceCauseChanged, func() (payload, payload) {
			good := serviceCodecs()[1].p.(RuntimeServiceCauseChanged)
			good.ExpectedVersion, good.Version, good.Condition, good.Source = 1, 2, "cleared", serviceSuccessFixture(true)
			bad := good
			bad.Source = serviceSuccessFixture(true)
			bad.Source.Completion.Permit.CauseID = serviceID(99)
			return good, bad
		}},
		{"M_foreign_permit_cause", TypeRuntimeServiceAgentChanged, func() (payload, payload) {
			good := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
			good.ExpectedVersion, good.Version, good.Phase, good.OutageID = 1, 2, "restored", serviceID(14)
			good.Source = serviceSuccessFixture(true)
			good.Source.ObservedUTCNS = *good.DueUTCNS + 1
			bad := good
			bad.Source = serviceSuccessFixture(true)
			bad.Source.ObservedUTCNS = good.Source.ObservedUTCNS
			bad.Source.Completion.Permit.CauseID = serviceID(99)
			return good, bad
		}},
		{"M_foreign_permit_generation", TypeRuntimeServiceAgentChanged, func() (payload, payload) {
			good := serviceCodecs()[2].p.(RuntimeServiceAgentChanged)
			good.ExpectedVersion, good.Version, good.Phase, good.OutageID = 1, 2, "restored", serviceID(14)
			good.Source = serviceSuccessFixture(true)
			good.Source.ObservedUTCNS = *good.DueUTCNS + 1
			bad := good
			bad.Source = serviceSuccessFixture(true)
			bad.Source.ObservedUTCNS = good.Source.ObservedUTCNS
			bad.Source.Completion.Permit.MemberGeneration = 2
			return good, bad
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			good, bad := row.makePair()
			wire, err := good.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeF3_91(row.typ, wire); err != nil {
				t.Fatal(err)
			}
			serviceRejectBoth(t, row.typ, bad)
		})
	}
}

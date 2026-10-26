package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func paidProfileFixture() Profile {
	p := Profile{Schema: 1, CapabilityVersion: "local-cap-1", TariffVersion: "local-tariff-1", Provider: "anthropic", Authority: "127.0.0.1:443", Method: "POST", Path: "/v1/messages", APIVersion: "2026-10-21", ModelID: "claude-haiku-4-5-20251001", Region: "global", RequestedTier: "standard_only", AllowedServedTiers: []string{"standard"}, Speed: "standard", ContextMax: 100, InputMax: 120, OutputMax: 50, UsageSchema: "anthropic-messages-text/v1", CacheClasses: []string{"input.cache_read", "input.cache_write_1h", "input.cache_write_5m"}, ToolChoices: []string{"auto", "none"}, NoChargeRules: []string{}, Tariff: Tariff{Currency: "USD"}, VerifiedUTCNS: 100, ExpiresUTCNS: 200}
	for _, class := range []string{"input.cache_read", "input.cache_write_1h", "input.cache_write_5m", "input.uncached", "output.generated"} {
		p.Tariff.Rates = append(p.Tariff.Rates, TariffRate{class, "1", "1"})
	}
	for _, topic := range []string{"endpoint_api_version", "inclusive_usage", "lifecycle", "local_tool_billing", "model_limits", "no_charge_billing", "tariff", "tier_region_speed"} {
		p.Sources = append(p.Sources, ProfileSource{topic, "https://localhost/facts/" + topic, 99, "Synthetic loopback assumptions & <facts>; not live provider facts."})
	}
	return p
}

func paidRouteFixture() RuntimePaidRouteConfigured {
	return RuntimePaidRouteConfigured{RouteID: fid("51"), Version: 1, Enabled: true, CredentialScopeID: fid("52"), Profile: paidProfileFixture(), ObservedUTCNS: 110}
}

func paidSpendFixture() RuntimeSpendConfigured {
	ceiling, warning := int64(1000000), int64(800000)
	return RuntimeSpendConfigured{Scope: PaidScope{Kind: "colony"}, Version: 1, Terms: SpendTerms{Enabled: true, Mode: "finite", CeilingMicros: &ceiling, PeriodKind: "lifetime", WarningPPM: &warning}}
}

func paidResultFixture(kind string) RuntimePaidResult {
	p := RuntimePaidResult{AttemptID: fid("61"), ExpectedVersion: 2, Version: 3, Kind: kind}
	if kind == "released_unsent" {
		p.ExpectedVersion, p.Version = 1, 2
		return p
	}
	started := false
	p.OutputStarted = &started
	p.EvidenceObject = &ObjectRef{scopedHash("paid observation"), 16}
	p.EvidenceSHA256 = p.EvidenceObject.SHA256
	if kind == "unknown" {
		p.ErrorClass = "future_error"
		return p
	}
	p.ProviderMessageID = "synthetic-message"
	cost := int64(0)
	p.CostMicros = &cost
	if kind == "no_charge" {
		p.NoCharge = &NoChargeEvidence{Rule: "anthropic_refusal_before_output_2026_09_24", StopReason: "refusal", StopDetailsType: "refusal", CategoryKind: "null", Complete: true}
	} else {
		p.Usage = &PaidUsage{Classes: []PaidUnits{{"input.cache_read", 0}, {"input.cache_write_1h", 0}, {"input.cache_write_5m", 0}, {"input.uncached", 1}, {"output.generated", 1}}, PricingTier: "standard"}
		*p.CostMicros = 2
		p.MessageObjects = []ObjectRef{{scopedHash("paid output"), 8}}
	}
	if kind == "correction" {
		p.PreviousResultSequence, p.PreviousEvidenceSHA256 = 10, scopedHash("previous evidence")
	}
	return p
}

func decodeF3_13(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimePaidRouteConfigured:
		return DecodeRuntimePaidRouteConfigured(js)
	case TypeRuntimeCredentialChanged:
		return DecodeRuntimeCredentialChanged(js)
	case TypeRuntimeSpendConfigured:
		return DecodeRuntimeSpendConfigured(js)
	case TypeRuntimePaidReserved:
		return DecodeRuntimePaidReserved(js)
	case TypeRuntimePaidDispatchIntent:
		return DecodeRuntimePaidDispatchIntent(js)
	case TypeRuntimePaidResult:
		return DecodeRuntimePaidResult(js)
	case TypeRuntimePaidOverrunAcknowledged:
		return DecodeRuntimePaidOverrunAcknowledged(js)
	case TypeRuntimePaidReceipt:
		return DecodeRuntimePaidReceipt(js)
	case "schema-4":
		return DecodePaidSubmission(js)
	}
	panic(typ)
}

func assertPaidInvalid(t *testing.T, typ, wire string) {
	t.Helper()
	if _, err := decodeF3_13(typ, []byte(wire)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%s accepted invalid payload: %v", typ, err)
	}
}

func TestF3_13Payloads(t *testing.T) {
	t.Parallel()
	for _, fixture := range f3_13Payloads() {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			wire, err := fixture.p.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if fixture.wire != "" && string(wire) != fixture.wire {
				t.Fatalf("canonical bytes = %s; want %s", wire, fixture.wire)
			}
			for _, input := range [][]byte{wire, append([]byte(`{"future":{"nested":[null,1]},`), wire[1:]...)} {
				got, err := decodeF3_13(fixture.typ, input)
				if err != nil || !reflect.DeepEqual(got, fixture.p) {
					t.Fatalf("decode = %#v, %v; want %#v", got, err, fixture.p)
				}
				again, err := got.Encode()
				if err != nil || !bytes.Equal(again, wire) {
					t.Fatalf("roundtrip = %s, %v", again, err)
				}
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				assertPaidInvalid(t, fixture.typ, strings.Replace(string(wire), `"`+key+`":`+string(value), `"`+key+`":null`, 1))
				assertPaidInvalid(t, fixture.typ, `{"`+key+`":`+string(value)+`,`+string(wire[1:]))
			}
			for _, bad := range []string{`null`, `[]`, `{}`, string(wire) + ` {}`, string(wire[:len(wire)-1]), string([]byte{0xff}), `{"future":{"nested":[{"x":1,"x":2}]},` + string(wire[1:])} {
				assertPaidInvalid(t, fixture.typ, bad)
			}
			for _, size := range []int{65536, 65537} {
				_, err := decodeF3_13(fixture.typ, []byte(string(wire)+strings.Repeat(" ", size-len(wire))))
				if size == 65536 && err != nil || size == 65537 && !errors.Is(err, ErrInvalid) {
					t.Fatalf("%d-byte payload = %v", size, err)
				}
			}
		})
	}
	t.Run("producer_maximum", testPaidProducerMaximum)
}

func TestF3_13Conditionals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"released_unsent", "unknown", "usage", "no_charge", "correction"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			p := paidResultFixture(kind)
			wire := string(mustEncode(p))
			assertPaidInvalid(t, TypeRuntimePaidResult, strings.Replace(wire, `"kind":"`+kind+`"`, `"kind":"future_kind"`, 1))
			for _, key := range []string{"previous_result_sequence", "previous_evidence_sha256"} {
				if kind == "correction" {
					var fields map[string]json.RawMessage
					_ = json.Unmarshal([]byte(wire), &fields)
					delete(fields, key)
					bad, _ := json.Marshal(fields)
					assertPaidInvalid(t, TypeRuntimePaidResult, string(bad))
				} else {
					value := `10`
					if key == "previous_evidence_sha256" {
						value = `"` + scopedHash("old").String() + `"`
					}
					assertPaidInvalid(t, TypeRuntimePaidResult, `{"`+key+`":`+value+`,`+wire[1:])
				}
			}
			if kind == "released_unsent" || kind == "unknown" {
				assertPaidInvalid(t, TypeRuntimePaidResult, `{"cost_micros":0,`+wire[1:])
			}
			if p.Usage != nil {
				// Keep both candidate financial values individually valid, so this
				// catches exclusivity rather than a conflicting amount or output.
				*p.CostMicros = 0
				p.MessageObjects = nil
				for i := range p.Usage.Classes {
					p.Usage.Classes[i].Units = 0
				}
				nc, _ := json.Marshal(paidResultFixture("no_charge").NoCharge)
				financial := string(mustEncode(p))
				assertPaidInvalid(t, TypeRuntimePaidResult, `{"no_charge":`+string(nc)+`,`+financial[1:])
			}
			if kind == "no_charge" {
				assertPaidInvalid(t, TypeRuntimePaidResult, strings.Replace(wire, `"category_kind":"null"`, `"category_kind":"null","category":"safe"`, 1))
			}
		})
	}
	spend := paidSpendFixture()
	wire := string(mustEncode(spend))
	for _, bad := range []string{strings.Replace(wire, `"mode":"finite"`, `"mode":"unlimited"`, 1), strings.Replace(wire, `"mode":"finite"`, `"mode":"future_mode"`, 1), strings.Replace(wire, `"period_kind":"lifetime"`, `"period_kind":"future_period"`, 1), strings.Replace(wire, `"kind":"colony"`, `"kind":"future_scope"`, 1), strings.Replace(wire, `"kind":"colony"`, `"kind":"colony","id":"`+fid("51").String()+`"`, 1), `{"parent_grant_id":null,` + wire[1:]} {
		assertPaidInvalid(t, TypeRuntimeSpendConfigured, bad)
	}
	route := string(mustEncode(paidRouteFixture()))
	assertPaidInvalid(t, TypeRuntimePaidRouteConfigured, strings.Replace(route, `"profile":{"schema":1`, `"profile":{"schema":1,"schema":1`, 1))
	assertPaidInvalid(t, TypeRuntimePaidRouteConfigured, strings.Replace(route, `"profile":{"schema":1`, `"profile":{"schema":2`, 1))
}

func testPaidProducerMaximum(t *testing.T) {
	t.Parallel()
	route := paidRouteFixture()
	for len(mustPaidProfile(t, route.Profile)) < 32768 {
		n := 32768 - len(mustPaidProfile(t, route.Profile))
		for i := range route.Profile.Sources {
			add := min(n, 4096-len(route.Profile.Sources[i].Quote))
			route.Profile.Sources[i].Quote += strings.Repeat("x", add)
			n -= add
		}
		if n > 0 {
			route.Profile.Sources = append(route.Profile.Sources, ProfileSource{fmt.Sprintf("zz_extra_%03d", len(route.Profile.Sources)), "https://localhost/extra", 99, "x"})
		}
	}
	profile := mustPaidProfile(t, route.Profile)
	if len(profile) != 32768 {
		t.Fatalf("profile bytes = %d", len(profile))
	}
	routeBytes, err := route.Encode()
	if err != nil {
		t.Fatal(err)
	}
	sub := PaidSubmission{Schema: 4, Actor: "owner", Origin: scopedOriginFixture(), Operation: "paid.route.configure", Arguments: PaidRouteArguments{route.RouteID, 0, true, route.CredentialScopeID, Digest(sha256.Sum256(profile))}}
	receipt := RuntimePaidReceipt{CommandID: fid("71"), Submission: sub, SubmissionSHA256: Digest(sha256.Sum256(mustEncode(sub))), Outcome: "configured", Version: 1}
	receiptBytes, err := receipt.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(routeBytes) > 65536 || len(receiptBytes) > 65536 {
		t.Fatal("producer profile cannot be recorded")
	}
	p := paidResultFixture("usage")
	p.MessageObjects = nil
	// Conservative producer envelope: 256 blocks plus eight extra 1 MiB chunks
	// for S1's 8 MiB output bound. This is not a history decoder list cap.
	const messageObjects = 256 + (8<<20)/(1<<20)
	for i := range messageObjects {
		p.MessageObjects = append(p.MessageObjects, ObjectRef{scopedHash(fmt.Sprintf("message %d", i)), 1 << 20})
	}
	resultBytes, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	sub = PaidSubmission{Schema: 4, Actor: "core", Origin: scopedOriginFixture(), Operation: "paid.observe", GrantID: fid("54"), Arguments: PaidObserveArguments{p.AttemptID, p.ExpectedVersion, p.Kind, p.EvidenceSHA256, *p.EvidenceObject, p.MessageObjects}}
	receipt.Submission, receipt.SubmissionSHA256, receipt.Outcome, receipt.Version, receipt.AttemptID = sub, Digest(sha256.Sum256(mustEncode(sub))), "settled", p.Version, p.AttemptID
	resultReceipt, err := receipt.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRuntimePaidResult(resultBytes); err != nil {
		t.Fatal(err)
	}
	if len(resultBytes) > 65536 || len(resultReceipt) > 65536 {
		t.Fatal("producer output cannot be recorded")
	}
	t.Logf("producer bytes: profile=%d route=%d receipt=%d result=%d result_receipt=%d message_objects=%d", len(profile), len(routeBytes), len(receiptBytes), len(resultBytes), len(resultReceipt), messageObjects)
}

func mustPaidProfile(t *testing.T, p Profile) []byte {
	t.Helper()
	b, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

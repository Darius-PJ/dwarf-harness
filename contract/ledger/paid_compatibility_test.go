package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestF3_13Compatibility(t *testing.T) {
	t.Parallel()
	t.Run("profile_escaping_and_unknown_fields", func(t *testing.T) {
		t.Parallel()
		route := paidRouteFixture()
		route.Profile.Sources[0].Quote = "Synthetic <>&\u2028\u2029 facts"
		direct, err := route.Profile.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(direct, []byte(`Synthetic \u003c\u003e\u0026\u2028\u2029 facts`)) {
			t.Fatalf("profile escaping: %s", direct)
		}
		wire := mustEncode(route)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(direct, fields["profile"]) {
			t.Fatal("nested profile digest bytes differ")
		}
		hash, err := route.Profile.SHA256()
		if err != nil || hash != Digest(sha256.Sum256(direct)) {
			t.Fatal("profile digest changed")
		}
		unknown := bytes.Replace(wire, []byte(`"profile":{`), []byte(`"profile":{"future":{"x":null,"integer":18446744073709551615},`), 1)
		decoded, err := DecodeRuntimePaidRouteConfigured(unknown)
		if err != nil {
			t.Fatal(err)
		}
		again, err := decoded.Profile.SHA256()
		if err != nil || again != hash {
			t.Fatal("unknown optional member entered profile hash")
		}
	})
	t.Run("open_tokens", func(t *testing.T) {
		t.Parallel()
		route := paidRouteFixture()
		route.Profile.Provider = "future_provider"
		route.Profile.NoChargeRules = []string{"future_rule"}
		route.Profile.Tariff.Rates[0].Class = "future.class"
		if _, err := DecodeRuntimePaidRouteConfigured(mustEncode(route)); err != nil {
			t.Fatal(err)
		}
		for _, fixture := range f3_13Payloads() {
			if fixture.typ == TypeRuntimePaidReceipt || fixture.name == "bind" || fixture.name == "unknown" {
				if _, err := decodeF3_13(fixture.typ, mustEncode(fixture.p)); err != nil {
					t.Fatal(err)
				}
			}
		}
		r := paidResultFixture("no_charge")
		r.NoCharge.Rule = "future_rule"
		if _, err := DecodeRuntimePaidResult(mustEncode(r)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no_installation_profile_cap", func(t *testing.T) {
		t.Parallel()
		route := paidRouteFixture()
		for i := range route.Profile.Sources {
			route.Profile.Sources[i].Quote = strings.Repeat("x", 4096)
		}
		wire := mustEncode(route)
		if len(wire) <= 32768 || len(wire) > 65536 {
			t.Fatalf("fixture size=%d", len(wire))
		}
		if _, err := DecodeRuntimePaidRouteConfigured(wire); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("earlier_golden_bytes", func(t *testing.T) {
		t.Parallel()
		for _, g := range []*goldenLog{mustGolden(t), mustCatalogue(t), mustWorkspace(t), mustPromotion(t), mustScratchChange(t), mustFinalization(t), mustActivation(t), mustGraph(t), mustPrivate(t), mustScoped(t), mustReview(t)} {
			for name, want := range g.files {
				got, err := os.ReadFile(filepath.Join(g.dir, name))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("earlier golden changed: %s/%s: %v", g.dir, name, err)
				}
			}
		}
	})
	t.Run("catalogue_additions", func(t *testing.T) {
		t.Parallel()
		c := Defined()
		for _, typ := range []string{TypeRuntimePaidRouteConfigured, TypeRuntimeCredentialChanged, TypeRuntimeSpendConfigured, TypeRuntimePaidReserved, TypeRuntimePaidDispatchIntent, TypeRuntimePaidResult, TypeRuntimePaidOverrunAcknowledged, TypeRuntimePaidReceipt} {
			if !c.Contains(TypeVersion{typ, 1}) || c.Contains(TypeVersion{typ, 2}) {
				t.Fatalf("catalogue type %s", typ)
			}
		}
	})
}

func TestF3_13NestedValidation(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, typ        string
		value            payload
		old, replacement string
	}{
		{"duplicate_quote", TypeRuntimePaidReserved, paidReservationFixture(), `"quote":{"currency":"USD"`, `"quote":{"currency":"USD","currency":"USD"`},
		{"duplicate_source", TypeRuntimePaidRouteConfigured, paidRouteFixture(), `"retrieved_utc_ns":99`, `"retrieved_utc_ns":99,"retrieved_utc_ns":99`},
		{"null_no_charge_rules", TypeRuntimePaidRouteConfigured, paidRouteFixture(), `"no_charge_rules":[]`, `"no_charge_rules":null`},
		{"unknown_credential_action", TypeRuntimeCredentialChanged, f3_13Payloads()[1].p, `"action":"bind"`, `"action":"future_action"`},
		{"unknown_operation", "schema-4", paidSubmissionFixtures()[0], `"operation":"paid.route.configure"`, `"operation":"paid.future"`},
		{"zero_denominator", TypeRuntimePaidReserved, paidReservationFixture(), `"rate_denominator":"1"`, `"rate_denominator":"0"`},
		{"leading_zero_rate", TypeRuntimePaidReserved, paidReservationFixture(), `"rate_numerator":"1"`, `"rate_numerator":"01"`},
		{"rate_overflow", TypeRuntimePaidReserved, paidReservationFixture(), `"rate_numerator":"1"`, `"rate_numerator":"9223372036854775808"`},
		{"count_overflow", TypeRuntimePaidReserved, paidReservationFixture(), `"max_units":100`, `"max_units":9223372036854775808`},
		{"money_fraction", TypeRuntimePaidResult, paidResultFixture("usage"), `"cost_micros":2`, `"cost_micros":2.0`},
		{"money_exponent", TypeRuntimePaidResult, paidResultFixture("usage"), `"cost_micros":2`, `"cost_micros":2e0`},
		{"money_negative", TypeRuntimePaidResult, paidResultFixture("usage"), `"cost_micros":2`, `"cost_micros":-1`},
		{"money_string", TypeRuntimePaidResult, paidResultFixture("usage"), `"cost_micros":2`, `"cost_micros":"2"`},
		{"optional_null_category", TypeRuntimePaidResult, paidResultFixture("no_charge"), `"category_kind":"null"`, `"category_kind":"null","category":null`},
		{"optional_null_output", TypeRuntimePaidResult, paidResultFixture("no_charge"), `"content_blocks":0`, `"content_blocks":0,"output_tokens":null`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wire := string(mustEncode(row.value))
			bad := strings.Replace(wire, row.old, row.replacement, 1)
			if bad == wire {
				t.Fatal("mutation matched nothing")
			}
			assertPaidInvalid(t, row.typ, bad)
		})
	}
	for _, mutate := range []func(*RuntimePaidReserved){
		func(p *RuntimePaidReserved) { p.Quote.Components[1].Classes = []string{"input.uncached"} },
		func(p *RuntimePaidReserved) { p.Request.Headers[1].Name = "accept" },
		func(p *RuntimePaidReserved) { p.Request.Headers[1].Name = "authorization" },
		func(p *RuntimePaidReserved) { p.Request.BodyBytes++ },
		func(p *RuntimePaidReserved) { p.Scopes[0].ID = p.GrantID },
	} {
		p := paidReservationFixture()
		mutate(&p)
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid nested value encoded: %v", err)
		}
	}
}

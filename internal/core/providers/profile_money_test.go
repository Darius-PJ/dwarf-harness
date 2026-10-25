package providers

import (
	"bytes"
	"math"
	"slices"
	"strings"
	"testing"
)

func fixtureProfile() Profile {
	p := Profile{Schema: 1, CapabilityVersion: "local-cap-1", TariffVersion: "local-tariff-1", Provider: "anthropic", Authority: "127.0.0.1:443", Method: "POST", Path: "/v1/messages", APIVersion: APIVersion, ModelID: Haiku, Region: "global", RequestedTier: "standard_only", AllowedServedTiers: []string{"standard"}, Speed: "standard", ContextMax: 100, InputMax: 120, OutputMax: 50, UsageSchema: "anthropic-messages-text/v1", CacheClasses: []string{CacheRead, CacheWrite1h, CacheWrite5m}, ToolChoices: []string{"auto", "none"}, NoChargeRules: []string{}, Tariff: Tariff{Currency: "USD"}, VerifiedUTCNS: 100, ExpiresUTCNS: 200}
	for _, class := range allClasses {
		p.Tariff.Rates = append(p.Tariff.Rates, Rate{class, "1", "1"})
	}
	topics := slices.Clone(requiredTopics)
	slices.Sort(topics)
	for _, topic := range topics {
		p.Sources = append(p.Sources, SourceQuote{topic, "https://localhost/facts/" + topic, 99, "Synthetic loopback assumptions; not live provider facts."})
	}
	return p
}
func fixtureRequest() Request {
	return Request{MaxTokens: 20, Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "local arithmetic fixture"}}}}}
}
func fixtureCompiled(t *testing.T, p Profile) Compiled {
	t.Helper()
	c, e := CompileAnthropic(p, fixtureRequest())
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func fixtureHeaders() []Header {
	return []Header{{"content-type", "application/json"}, {"anthropic-version", APIVersion}, {"accept", "text/event-stream"}}
}
func fixtureEnvelope(t *testing.T, p Profile) Envelope {
	t.Helper()
	e, err := Seal(p, fixtureCompiled(t, p), "scope-fixture", "version-fixture", fixtureHeaders())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestProfileRefusesStaleIncompleteAndAmbiguousFacts(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name string
		raw  []byte
	}{
		{"duplicate", bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)},
		{"unknown", bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"surprise":0`), 1)},
		{"case_variant", bytes.Replace(raw, []byte(`"schema":1`), []byte(`"Schema":1`), 1)},
		{"nested_case_variant", bytes.Replace(raw, []byte(`"currency":"USD"`), []byte(`"Currency":"USD"`), 1)},
		{"null", bytes.Replace(raw, []byte(`"no_charge_rules":[]`), []byte(`"no_charge_rules":null`), 1)},
		{"missing_required_empty_set", bytes.Replace(raw, []byte(`"no_charge_rules":[],`), nil, 1)},
		{"float", bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1.0`), 1)},
		{"exponent", bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1e0`), 1)},
		{"negative", bytes.Replace(raw, []byte(`"input_max":120`), []byte(`"input_max":-1`), 1)},
		{"oversize", append(bytes.Clone(raw), bytes.Repeat([]byte(" "), ProfileLimit)...)},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if _, e := ParseProfile(row.raw); e == nil {
				t.Fatal("accepted invalid profile")
			}
		})
	}
	for _, topic := range requiredTopics {
		t.Run("missing_"+topic, func(t *testing.T) {
			t.Parallel()
			q := fixtureProfile()
			q.Sources = slices.DeleteFunc(q.Sources, func(s SourceQuote) bool { return s.Topic == topic })
			b, _ := compact(q)
			if _, e := ParseProfile(b); e == nil {
				t.Fatal("accepted unsourced profile")
			}
		})
	}
	for _, row := range []struct {
		name  string
		n     int64
		valid bool
	}{{"before", 99, false}, {"verified", 100, true}, {"before_expiry", 199, true}, {"at_expiry", 200, false}, {"after", 201, false}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if e := p.Current(row.n); (e == nil) != row.valid {
				t.Fatalf("freshness result: %v", e)
			}
		})
	}
	q, e := ParseProfile(raw)
	if e != nil {
		t.Fatal(e)
	}
	again, _ := q.Canonical()
	if !bytes.Equal(raw, again) {
		t.Fatal("profile encoding not stable")
	}
}

func TestProfileNoChargeRulesAreAnOpenCanonicalSet(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name  string
		rules []string
		valid bool
	}{
		{"empty", []string{}, true},
		{"unknown_token", []string{"future_documented_rule"}, true},
		{"duplicate", []string{"rule", "rule"}, false},
		{"unsorted", []string{"z_rule", "a_rule"}, false},
		{"uppercase", []string{"Rule"}, false},
		{"oversize", []string{strings.Repeat("a", 65)}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			p.ModelID, p.RequestedTier, p.NoChargeRules = Opus, "auto", row.rules
			raw, e := compact(p)
			if e != nil {
				t.Fatal(e)
			}
			parsed, e := ParseProfile(raw)
			if (e == nil) != row.valid {
				t.Fatalf("open token set: %v", e)
			}
			if row.valid && !slices.Equal(parsed.NoChargeRules, row.rules) {
				t.Fatal("no-charge rules changed")
			}
		})
	}
}

func TestProfileDigestBindsPricingAndSourceRevisions(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	base, _ := p.SHA256()
	for _, change := range []func(*Profile){func(q *Profile) { q.CapabilityVersion = "cap-2" }, func(q *Profile) { q.TariffVersion = "tariff-2" }, func(q *Profile) { q.ContextMax++ }, func(q *Profile) { q.InputMax++ }, func(q *Profile) { q.OutputMax++ }, func(q *Profile) { q.VerifiedUTCNS++ }, func(q *Profile) { q.ExpiresUTCNS++ }, func(q *Profile) { q.Sources[0].Quote += " Revised." }, func(q *Profile) { q.Tariff.Rates[0].Numerator = "2" }} {
		q := fixtureProfile()
		change(&q)
		h, e := q.SHA256()
		if e != nil || h == base {
			t.Fatalf("changed fact not bound: %v", e)
		}
	}
	q := fixtureProfile()
	q.Authority = "api.anthropic.com"
	if q.Validate() == nil {
		t.Fatal("local evidence enabled live authority")
	}
}

func TestMoneyGrammarAndCheckedBoundary(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		s string
		n int64
	}{{"0", 0}, {"0.000001", 1}, {"12.34", 12340000}, {"9223372036854.775807", math.MaxInt64}} {
		t.Run(row.s, func(t *testing.T) {
			t.Parallel()
			n, e := ParseMoney(row.s)
			if e != nil || n != row.n {
				t.Fatalf("money result %d %v", n, e)
			}
		})
	}
	for _, s := range []string{"", "00", "01", "+1", "-1", " 1", "1 ", ".1", "1.", "1e2", "1.0000000", "1..2", "NaN", "unlimited", "9223372036854.775808", "9223372036855"} {
		t.Run("refuse_"+s, func(t *testing.T) {
			t.Parallel()
			if _, e := ParseMoney(s); e == nil {
				t.Fatal("accepted invalid money")
			}
		})
	}
}
func TestRectangularBoundAndOneFinalRounding(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		modify func(*Profile)
		output int64
		want   int64
	}{
		{"input_plus_requested_output_above_context", func(*Profile) {}, 50, 150},
		{"input_rate_above_output_rate", func(p *Profile) {
			for i := range p.Tariff.Rates {
				if p.Tariff.Rates[i].Class != Generated {
					p.Tariff.Rates[i].Numerator = "9"
				}
			}
		}, 20, 920},
		{"one_final_rounding", func(p *Profile) {
			p.InputMax = 1
			for i := range p.Tariff.Rates {
				p.Tariff.Rates[i].Denominator = "3"
			}
		}, 1, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := fixtureProfile()
			row.modify(&p)
			cs, n, e := Bound(p, row.output, inputClasses)
			if e != nil || n != row.want || len(cs) != 2 {
				t.Fatalf("bound %d, want %d: %v", n, row.want, e)
			}
		})
	}
	t.Run("input_class_overlap", func(t *testing.T) {
		t.Parallel()
		if _, e := Maximum([]Component{{[]string{Uncached}, 1, "1", "1"}, {[]string{Uncached}, 1, "1", "1"}}); e == nil {
			t.Fatal("accepted overlapping input")
		}
	})
	t.Run("quote_overflow", func(t *testing.T) {
		t.Parallel()
		p := fixtureProfile()
		p.ContextMax = math.MaxInt64
		p.InputMax = math.MaxInt64
		if _, _, e := Bound(p, 1, []string{Uncached}); e == nil {
			t.Fatal("saturated bound")
		}
	})
	t.Run("output_limit", func(t *testing.T) {
		t.Parallel()
		p := fixtureProfile()
		if _, _, e := Bound(p, p.OutputMax+1, []string{Uncached}); e == nil {
			t.Fatal("accepted excess output")
		}
	})
}
func FuzzProviderProfile(f *testing.F) {
	p := fixtureProfile()
	b, _ := p.Canonical()
	f.Add(b)
	f.Add([]byte(`{"schema":1,"schema":1}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, e := ParseProfile(b)
		if e != nil {
			return
		}
		canonical, e := p.Canonical()
		if e != nil {
			t.Fatal(e)
		}
		q, e := ParseProfile(canonical)
		if e != nil {
			t.Fatal(e)
		}
		a, _ := p.SHA256()
		z, _ := q.SHA256()
		if a != z {
			t.Fatal("digest instability")
		}
	})
}
func FuzzPaidMoney(f *testing.F) {
	for _, s := range []string{"0", "0.000001", "1.999999", "9223372036854.775807", "01", "1e2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, e := ParseMoney(s)
		if e == nil && (n < 0 || strings.TrimSpace(s) != s) {
			t.Fatal("invalid successful amount")
		}
	})
}

package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
	"testing"
)

func openAIUsage(input, cached, write, output int64) string {
	return fmt.Sprintf(`{"input_tokens":%d,"input_tokens_details":{"cached_tokens":%d,"cache_write_tokens":%d},"output_tokens":%d}`, input, cached, write, output)
}

func TestOpenAIBoundAndSettlement(t *testing.T) {
	t.Parallel()
	// Fixed independent vectors catch omitted write/long rates, a sloped input
	// bound, cache/reasoning double charges, separate rounding and clamping.
	p := openAIProfile()
	for _, row := range []struct {
		name         string
		output, want int64
	}{{"admission", 16, 230512}, {"drill", 4096, 233572}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c := openAICompiled(t, p, row.output)
			parts, maximum, err := Bound(p, row.output, c.InputClasses())
			if err != nil || maximum != row.want || len(parts) != 2 || parts[0].MaxUnits != 922000 || !slices.Equal(parts[1].Classes, []string{"output.generated.long", "output.generated.short"}) {
				t.Fatalf("bound %d: %v", maximum, err)
			}
			historical, total, err := BoundHistorical(p, row.output, c.InputClasses())
			if err != nil || total != maximum || !reflect.DeepEqual(parts, historical) {
				t.Fatal("historical bound diverged")
			}
		})
	}
	for _, row := range []struct {
		name                               string
		input, cached, write, output, want int64
		ambiguous                          bool
	}{
		{"partition", 20, 4, 8, 16, 10, false}, {"short_edge", 272000, 0, 0, 16, 27208, false},
		{"ambiguous_lower", 272001, 0, 0, 16, 0, true}, {"ambiguous_upper", 278528, 0, 0, 16, 0, true},
		{"long_edge", 278529, 0, 0, 16, 55718, false}, {"round_once", 1, 1, 0, 17, 9, false},
		{"input_over_bound", 922001, 0, 0, 16, 184413, false}, {"output_over_bound", 922000, 0, 922000, 17, 230513, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var usage Usage
			_ = json.Unmarshal([]byte(openAIUsage(row.input, row.cached, row.write, row.output)), &usage)
			usage["output_tokens_details"] = json.RawMessage(`{"reasoning_tokens":12}`)
			c := openAICompiled(t, p, max(16, row.output))
			units, violation, err := NormalizeUsage(p, c, usage)
			if violation || errors.Is(err, ErrTariffAmbiguous) != row.ambiguous {
				t.Fatalf("normalization: %v violation=%v", err, violation)
			}
			if row.ambiguous {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			price, err := PriceUsage(p, units)
			if err != nil || price != row.want {
				t.Fatalf("price=%d want=%d error=%v", price, row.want, err)
			}
			price, err = PriceHistoricalUsage(p, units)
			if err != nil || price != row.want {
				t.Fatal("historical price diverged")
			}
			late, v, err := NormalizeSealedUsage(p, c.InputClasses(), usage)
			if err != nil || v || !reflect.DeepEqual(units, late) {
				t.Fatal("sealed evidence diverged")
			}
		})
	}
	for _, row := range []struct {
		model  string
		factor int64
		want   int64
	}{{"gpt-6-sol", 20, 4610240}, {"gpt-6-astra", 100, 23051200}} {
		q := openAIProfile()
		q.ModelID = row.model
		for i := range q.Tariff.Rates {
			r := &q.Tariff.Rates[i]
			value, _ := rational(r.Numerator, r.Denominator)
			value.Mul(value, new(big.Rat).SetInt64(row.factor))
			r.Numerator, r.Denominator = value.Num().String(), value.Denom().String()
		}
		_, got, err := Bound(q, 16, openAIClasses[:6])
		if err != nil || got != row.want {
			t.Fatal("model maximum changed")
		}
	}
	q := openAIProfile()
	for i := range q.Tariff.Rates {
		r := &q.Tariff.Rates[i]
		value, _ := rational(r.Numerator, r.Denominator)
		value.Mul(value, big.NewRat(11, 10))
		r.Numerator, r.Denominator = value.Num().String(), value.Denom().String()
	}
	if _, got, err := Bound(q, 16, openAIClasses[:6]); err != nil || got != 253564 {
		t.Fatal("exact uplift maximum changed")
	}
	q = openAIProfile()
	q.InputMax, q.ContextMax = 1, 1
	if _, got, err := Bound(q, 17, openAIClasses[:6]); err != nil || got != 13 {
		t.Fatal("dimensions rounded separately")
	}
}

func TestOpenAIUsageUnknownAndNull(t *testing.T) {
	t.Parallel()
	// Omission/null cannot borrow an earlier counter; unknown positive billing
	// must hold, while explicit null/plain zero extensions cannot invent charges.
	for _, row := range []struct {
		name, raw        string
		valid, violation bool
	}{
		{"valid", openAIUsage(20, 4, 8, 16), true, false},
		{"missing_cached", `{"input_tokens":20,"input_tokens_details":{"cache_write_tokens":8},"output_tokens":16}`, false, false},
		{"null_cached", `{"input_tokens":20,"input_tokens_details":{"cached_tokens":null,"cache_write_tokens":8},"output_tokens":16}`, false, false},
		{"missing_write", `{"input_tokens":20,"input_tokens_details":{"cached_tokens":4},"output_tokens":16}`, false, false},
		{"null_write", `{"input_tokens":20,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":null},"output_tokens":16}`, false, false},
		{"overlapping", openAIUsage(20, 12, 12, 16), false, true},
		{"overflow_sum", openAIUsage(math.MaxInt64, math.MaxInt64, math.MaxInt64, 16), false, true},
		{"overflow_counter", `{"input_tokens":9223372036854775808}`, false, false},
		{"null_input", `{"input_tokens":null}`, false, false},
		{"null_output", `{"input_tokens":20,"output_tokens":null}`, false, false},
		{"fraction", `{"input_tokens":20.0}`, false, false},
		{"exponent", `{"input_tokens":2e1}`, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var usage Usage
			_ = json.Unmarshal([]byte(row.raw), &usage)
			p := openAIProfile()
			_, v, err := NormalizeUsage(p, openAICompiled(t, p, 16), usage)
			if (err == nil) != row.valid || v != row.violation {
				t.Fatalf("available=%v violation=%v", err == nil, v)
			}
		})
	}
	for _, location := range []string{"root", "input", "output"} {
		for _, value := range []string{"null", "0", "1", "-1", "0.0", "0e0", "false", `"0"`, `{}`, `{"future":0}`, `[]`} {
			t.Run(location+"_"+value, func(t *testing.T) {
				t.Parallel()
				var usage Usage
				_ = json.Unmarshal([]byte(openAIUsage(20, 4, 8, 16)), &usage)
				if location == "root" {
					usage["future"] = json.RawMessage(value)
				} else {
					key := "input_tokens_details"
					if location == "output" {
						key = "output_tokens_details"
						usage[key] = json.RawMessage(`{"reasoning_tokens":12}`)
					}
					nested, _ := rawObject(usage[key])
					nested["future"] = json.RawMessage(value)
					usage[key], _ = json.Marshal(nested)
				}
				p := openAIProfile()
				_, v, err := NormalizeUsage(p, openAICompiled(t, p, 16), usage)
				want := value == "null" || value == "0"
				if (err == nil) != want || v == want {
					t.Fatal("unknown billing member policy changed")
				}
			})
		}
	}
	for _, extra := range []string{`"total_tokens":37`, `"total_tokens":null`, `"output_tokens_details":{"reasoning_tokens":17}`, `"output_tokens_details":{"reasoning_tokens":null}`} {
		var usage Usage
		raw := openAIUsage(20, 4, 8, 16)
		_ = json.Unmarshal([]byte(raw[:len(raw)-1]+","+extra+"}"), &usage)
		p := openAIProfile()
		if _, _, err := NormalizeUsage(p, openAICompiled(t, p, 16), usage); err == nil {
			t.Fatal("inconsistent inclusive counters accepted")
		}
	}
}

func TestOpenAIHistoricalBandValidation(t *testing.T) {
	t.Parallel()
	// Replay cannot price wrong-band units, both bands, unpriced dimensions or
	// an ambiguous inclusive total merely because all counters are well formed.
	for _, row := range []struct {
		name  string
		units []Units
		want  error
	}{
		{"wrong_long", []Units{{"input.uncached.long", 20}}, ErrUsage},
		{"wrong_short", []Units{{"input.uncached.short", 278529}}, ErrUsage},
		{"both_bands", []Units{{"input.uncached.short", 20}, {"output.generated.long", 16}}, ErrUsage},
		{"ambiguous", []Units{{"input.uncached.short", 272001}}, ErrTariffAmbiguous},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			units := make([]Units, len(openAIClasses))
			for i, class := range openAIClasses {
				units[i] = Units{Class: class}
				for _, u := range row.units {
					if u.Class == class {
						units[i] = u
					}
				}
			}
			if _, err := PriceHistoricalUsage(openAIProfile(), units); !errors.Is(err, row.want) {
				t.Fatal("invalid recorded band accepted")
			}
		})
	}
	p := openAIProfile()
	p.VerifiedUTCNS, p.ExpiresUTCNS = 0, 0
	p.Sources = nil
	units := make([]Units, len(openAIClasses))
	for i, class := range openAIClasses {
		units[i] = Units{Class: class}
	}
	if _, err := PriceHistoricalUsage(p, units); err != nil {
		t.Fatal("historical pricing requires current installation")
	}
	units[0].Class = "unknown"
	if _, err := PriceHistoricalUsage(p, units); err == nil {
		t.Fatal("unpriced class accepted")
	}
	p = openAIProfile()
	for i := range p.Tariff.Rates {
		p.Tariff.Rates[i].Numerator = "9223372036854775807"
		p.Tariff.Rates[i].Denominator = "1"
	}
	if _, _, err := Bound(p, 16, openAIClasses[:6]); !errors.Is(err, ErrOverflow) {
		t.Fatal("maximum saturated")
	}
}

package providers

import (
	"bytes"
	"encoding/json"
	"math/big"
	"slices"
)

// Usage preserves the final raw cumulative snapshot, including explicit null.
// A missing key and a null key are different facts.
type Usage map[string]json.RawMessage

type Units struct {
	Class string
	Count int64
}

func mergeUsage(dst Usage, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrUsage
	}
	var m Usage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return ErrProtocol
	}
	for k, v := range m {
		if k == "cache_creation" && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			var children Usage
			if json.Unmarshal(v, &children) != nil || children == nil {
				return ErrProtocol
			}
			merged := Usage{}
			if prev := dst[k]; len(prev) > 0 {
				_ = json.Unmarshal(prev, &merged)
				if merged == nil {
					merged = Usage{}
				}
			}
			for ck, cv := range children {
				merged[ck] = bytes.Clone(cv)
			}
			encoded, e := compact(merged)
			if e != nil {
				return e
			}
			dst[k] = encoded
		} else {
			dst[k] = bytes.Clone(v)
		}
	}
	return nil
}

func counter(m Usage, key string) (n int64, present, available bool) {
	raw, ok := m[key]
	if !ok {
		return 0, false, false
	}
	n, e := decimal(string(raw))
	return n, true, e == nil
}
func zeroWhenAbsent(m Usage, key string, impossible bool) (int64, error) {
	n, p, a := counter(m, key)
	if a {
		return n, nil
	}
	if !p && impossible {
		return 0, nil
	}
	return 0, ErrUsage
}

// NormalizeUsage preserves inclusive billing: aggregate cache writes are a
// consistency check, and reasoning is a subset, never an additional class.
// A known but unexpected priced class remains chargeable with violation=true.
func NormalizeUsage(p Profile, c Compiled, raw Usage) (units []Units, violation bool, err error) {
	if e := p.Validate(); e != nil {
		return nil, false, e
	}
	digest, _ := p.SHA256()
	if c.profile != digest {
		return nil, false, ErrInvalid
	}
	known := []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation", "output_tokens_details", "service_tier", "inference_geo", "speed", "server_tool_use", "iterations"}
	for k, v := range raw {
		if !slices.Contains(known, k) && string(v) != "null" {
			return nil, true, ErrUsage
		}
	}
	for key, want := range map[string]string{"service_tier": "standard", "inference_geo": p.Region, "speed": p.Speed} {
		if v, ok := raw[key]; ok {
			var got string
			if json.Unmarshal(v, &got) != nil || got != want {
				return nil, true, ErrUsage
			}
		}
	}
	for _, key := range []string{"server_tool_use", "iterations"} {
		if v, ok := raw[key]; ok && string(v) != "null" {
			if key == "iterations" {
				if string(v) != "[]" {
					return nil, true, ErrUsage
				}
				continue
			}
			var counts Usage
			if json.Unmarshal(v, &counts) != nil || counts == nil {
				return nil, true, ErrUsage
			}
			for k := range counts {
				n, _, ok := counter(counts, k)
				if !ok || n != 0 || k != "web_fetch_requests" && k != "web_search_requests" {
					return nil, true, ErrUsage
				}
			}
		}
	}
	input, e := zeroWhenAbsent(raw, "input_tokens", false)
	if e != nil {
		return nil, false, e
	}
	output, e := zeroWhenAbsent(raw, "output_tokens", false)
	if e != nil {
		return nil, false, e
	}
	if details, present := raw["output_tokens_details"]; present && string(details) != "null" {
		var breakdown Usage
		if json.Unmarshal(details, &breakdown) != nil || breakdown == nil {
			return nil, true, ErrUsage
		}
		for key, value := range breakdown {
			if key != "thinking_tokens" && string(value) != "null" {
				return nil, true, ErrUsage
			}
		}
		if n, present, ok := counter(breakdown, "thinking_tokens"); present && (!ok || n > output) {
			return nil, true, ErrUsage
		}
	}
	read, e := zeroWhenAbsent(raw, "cache_read_input_tokens", !c.cache1 && !c.cache5)
	if e != nil {
		return nil, false, e
	}
	aggregate, aggregatePresent, aggregateOK := counter(raw, "cache_creation_input_tokens")
	if aggregatePresent && !aggregateOK {
		return nil, false, ErrUsage
	}
	children := Usage{}
	childrenRaw, childrenPresent := raw["cache_creation"]
	if childrenPresent {
		if json.Unmarshal(childrenRaw, &children) != nil || children == nil {
			return nil, false, ErrUsage
		}
		for k, v := range children {
			if k != "ephemeral_5m_input_tokens" && k != "ephemeral_1h_input_tokens" && string(v) != "null" {
				return nil, true, ErrUsage
			}
		}
	}
	five, fp, fa := counter(children, "ephemeral_5m_input_tokens")
	hour, hp, ha := counter(children, "ephemeral_1h_input_tokens")
	if fp && !fa || hp && !ha {
		return nil, false, ErrUsage
	}
	if aggregateOK && aggregate == 0 {
		if !fp {
			five, fa = 0, true
		}
		if !hp {
			hour, ha = 0, true
		}
	}
	if !fa && !fp && !c.cache5 {
		five, fa = 0, true
	}
	if !ha && !hp && !c.cache1 {
		hour, ha = 0, true
	}
	if aggregateOK {
		if c.cache5 && !c.cache1 && !fa && hour == 0 {
			five, fa = aggregate, true
		}
		if c.cache1 && !c.cache5 && !ha && five == 0 {
			hour, ha = aggregate, true
		}
	}
	if !fa || !ha {
		return nil, false, ErrUsage
	}
	total := new(big.Int).Add(big.NewInt(five), big.NewInt(hour))
	if aggregateOK && total.Cmp(big.NewInt(aggregate)) != 0 {
		return nil, true, ErrUsage
	}
	// Explicit positive data in a request-proved-impossible class is retained.
	violation = (!c.cache5 && five > 0) || (!c.cache1 && hour > 0) || (!c.cache5 && !c.cache1 && read > 0)
	return []Units{{CacheRead, read}, {CacheWrite1h, hour}, {CacheWrite5m, five}, {Uncached, input}, {Generated, output}}, violation, nil
}

// PriceUsage applies the pinned per-class tariff with one final rounding. Each
// counter is int64, but sums use big integers and are never clamped.
func PriceUsage(p Profile, units []Units) (int64, error) {
	if e := p.Validate(); e != nil {
		return 0, e
	}
	if len(units) != len(allClasses) {
		return 0, ErrUsage
	}
	seen := map[string]bool{}
	sum := new(big.Rat)
	for _, u := range units {
		if u.Count < 0 || !slices.Contains(allClasses, u.Class) || seen[u.Class] {
			return 0, ErrUsage
		}
		seen[u.Class] = true
		for _, r := range p.Tariff.Rates {
			if r.Class == u.Class {
				rate, _ := rational(r.Numerator, r.Denominator)
				sum.Add(sum, rate.Mul(rate, new(big.Rat).SetInt64(u.Count)))
			}
		}
	}
	return ceilMicros(sum)
}

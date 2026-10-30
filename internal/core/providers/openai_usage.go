package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"strings"
)

// ErrTariffAmbiguous retains the full hold without asserting a provider failure
// or a bound violation. This usage schema cannot resolve the guarded interval.
var ErrTariffAmbiguous = errors.New("providers: ambiguous tariff band")

const openAIUsageSchema = "openai-responses-text-30m-kguard/v1"
const openAIUsageSchemaV2 = "openai-responses-text-30m-kguard/v2"

var openAIClasses = []string{
	"input.cached.long", "input.cached.short",
	"input.uncached.long", "input.uncached.short",
	"input.write.long", "input.write.short",
	"output.generated.long", "output.generated.short",
}

func providerClasses(provider string) (inputs, outputs, all []string, err error) {
	switch provider {
	case "anthropic":
		return inputClasses, []string{Generated}, allClasses, nil
	case "openai":
		return openAIClasses[:6], openAIClasses[6:], openAIClasses, nil
	default:
		return nil, nil, nil, ErrUnsupported
	}
}

func openAIBand(input *big.Int) (string, error) {
	if input.Sign() < 0 {
		return "", ErrUsage
	}
	if input.Cmp(big.NewInt(272000)) <= 0 {
		return "short", nil
	}
	if input.Cmp(big.NewInt(278528)) <= 0 {
		return "", ErrTariffAmbiguous
	}
	return "long", nil
}

// The caller checks the whole usage object's JSON depth before this recursion.
func absentCharge(raw json.RawMessage, nested bool) bool {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("0")) {
		return true
	}
	if !nested {
		return false
	}
	m, err := rawObject(raw)
	if err != nil {
		return false
	}
	for _, value := range m {
		if !absentCharge(value, true) {
			return false
		}
	}
	return true
}

func knownUsage(m Usage, nested bool, known ...string) bool {
	for key, raw := range m {
		if !slices.Contains(known, key) && !absentCharge(raw, nested) {
			return false
		}
	}
	return true
}

// Tool usage is a separate billing surface. Only an absent/null object or a
// tree of objects with plain-zero leaves proves no additional tool charge.
func emptyOpenAIToolUsage(raw json.RawMessage) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	if checkJSON(raw, streamLineLimit, false, false) != nil {
		return false
	}
	var zeroObject func(json.RawMessage) bool
	zeroObject = func(raw json.RawMessage) bool {
		m, err := rawObject(raw)
		if err != nil {
			return false
		}
		for _, value := range m {
			if !bytes.Equal(bytes.TrimSpace(value), []byte("0")) && !zeroObject(value) {
				return false
			}
		}
		return true
	}
	return zeroObject(raw)
}

func normalizeOpenAIUsage(schema string, raw Usage) ([]Units, bool, error) {
	v2 := false
	switch schema {
	case openAIUsageSchema:
	case openAIUsageSchemaV2:
		v2 = true
	default:
		return nil, false, ErrUnsupported
	}
	encoded, err := json.Marshal(raw)
	if err != nil || checkJSON(encoded, streamLineLimit, false, false) != nil {
		return nil, false, ErrUsage
	}
	if !knownUsage(raw, v2, "input_tokens", "input_tokens_details", "output_tokens", "output_tokens_details", "total_tokens", "tool_usage") || !emptyOpenAIToolUsage(raw["tool_usage"]) {
		return nil, true, ErrUsage
	}
	input, err := zeroWhenAbsent(raw, "input_tokens", false)
	if err != nil {
		return nil, false, err
	}
	output, err := zeroWhenAbsent(raw, "output_tokens", false)
	if err != nil {
		return nil, false, err
	}
	details, err := rawObject(raw["input_tokens_details"])
	if err != nil {
		return nil, false, ErrUsage
	}
	knownDetails := []string{"cached_tokens", "cache_write_tokens"}
	if v2 {
		knownDetails = append(knownDetails, "cache_write_tokens_details")
	}
	if !knownUsage(details, v2, knownDetails...) {
		return nil, true, ErrUsage
	}
	cached, err := zeroWhenAbsent(details, "cached_tokens", false)
	if err != nil {
		return nil, false, err
	}
	write, err := zeroWhenAbsent(details, "cache_write_tokens", false)
	if err != nil {
		return nil, false, err
	}
	if split := details["cache_write_tokens_details"]; v2 && len(split) != 0 && !bytes.Equal(bytes.TrimSpace(split), []byte("null")) {
		breakdown, err := rawObject(split)
		if err != nil || !knownUsage(breakdown, true, "ttl_30m_tokens", "ttl_12h_tokens") {
			return nil, true, ErrUsage
		}
		var counts [2]int64
		for i, key := range []string{"ttl_30m_tokens", "ttl_12h_tokens"} {
			value, present := breakdown[key]
			if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			n, _, ok := counter(breakdown, key)
			if !ok {
				return nil, true, ErrUsage
			}
			counts[i] = n
		}
		if counts[1] != 0 {
			return nil, true, ErrUsage
		}
		if new(big.Int).Add(big.NewInt(counts[0]), big.NewInt(counts[1])).Cmp(big.NewInt(write)) != 0 {
			return nil, true, ErrUsage
		}
	}
	cache := new(big.Int).Add(big.NewInt(cached), big.NewInt(write))
	if cache.Cmp(big.NewInt(input)) > 0 {
		return nil, true, ErrUsage
	}
	if total, present, ok := counter(raw, "total_tokens"); present {
		if !ok || new(big.Int).Add(big.NewInt(input), big.NewInt(output)).Cmp(big.NewInt(total)) != 0 {
			return nil, true, ErrUsage
		}
	}
	if value, present := raw["output_tokens_details"]; present && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		breakdown, err := rawObject(value)
		if err != nil || !knownUsage(breakdown, v2, "reasoning_tokens") {
			return nil, true, ErrUsage
		}
		if n, present, ok := counter(breakdown, "reasoning_tokens"); present && (!ok || n > output) {
			return nil, true, ErrUsage
		}
	}
	band, err := openAIBand(big.NewInt(input))
	if err != nil {
		return nil, false, err
	}
	counts := map[string]int64{
		"input.cached." + band:     cached,
		"input.uncached." + band:   input - cached - write,
		"input.write." + band:      write,
		"output.generated." + band: output,
	}
	units := make([]Units, 0, len(openAIClasses))
	for _, class := range openAIClasses {
		units = append(units, Units{class, counts[class]})
	}
	return units, false, nil
}

func validateOpenAIUnits(units []Units) error {
	input := new(big.Int)
	for _, u := range units {
		if strings.HasPrefix(u.Class, "input.") {
			input.Add(input, big.NewInt(u.Count))
		}
	}
	band, err := openAIBand(input)
	if err != nil {
		return err
	}
	for _, u := range units {
		if u.Count != 0 && !strings.HasSuffix(u.Class, "."+band) {
			return ErrUsage
		}
	}
	return nil
}

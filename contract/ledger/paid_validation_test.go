package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// Mutate one occurrence at a time, including every object within arrays. These
// cases exercise actual decoding, not struct tags or source-text wiring.
func paidNullVariants(raw json.RawMessage) []json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && object != nil {
		var out []json.RawMessage
		for key, value := range object {
			object[key] = json.RawMessage("null")
			b, _ := json.Marshal(object)
			out = append(out, b)
			for _, variant := range paidNullVariants(value) {
				object[key] = variant
				b, _ := json.Marshal(object)
				out = append(out, b)
			}
			object[key] = value
		}
		return out
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil && array != nil {
		var out []json.RawMessage
		for i, value := range array {
			for _, variant := range paidNullVariants(value) {
				array[i] = variant
				b, _ := json.Marshal(array)
				out = append(out, b)
			}
			array[i] = value
		}
		return out
	}
	return nil
}

func TestF3_13RequiredMembers(t *testing.T) {
	t.Parallel()
	for _, fixture := range f3_13Payloads() {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(fixture.p)
			for _, variant := range paidNullVariants(wire) {
				assertPaidInvalid(t, fixture.typ, string(variant))
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				if slices.Contains([]string{"task_binding", "parent_grant_id", "revoke_previous_version", "message_objects", "provider_request_id"}, key) {
					continue
				}
				delete(fields, key)
				variant, err := json.Marshal(fields)
				fields[key] = value
				if err != nil {
					t.Fatal(err)
				}
				assertPaidInvalid(t, fixture.typ, string(variant))
			}
		})
	}
}

func TestF3_13NumericAndSizeBoundaries(t *testing.T) {
	t.Parallel()
	p := paidResultFixture("usage")
	*p.CostMicros = math.MaxInt64
	p.Usage.Classes[3].Units = math.MaxInt64
	wire := mustEncode(p)
	decoded, err := DecodeRuntimePaidResult(wire)
	if err != nil || *decoded.CostMicros != math.MaxInt64 || decoded.Usage.Classes[3].Units != math.MaxInt64 {
		t.Fatal("int64 boundary changed")
	}
	assertPaidInvalid(t, TypeRuntimePaidResult, strings.Replace(string(wire), `"cost_micros":9223372036854775807`, `"cost_micros":9223372036854775808`, 1))
	for _, value := range []string{"-0", "-1", "1.5", "1e1", "9223372036854775808"} {
		assertPaidInvalid(t, TypeRuntimePaidResult, strings.Replace(string(wire), `"expected_version":2`, `"expected_version":`+value, 1))
	}
	p.ExpectedVersion, p.Version = math.MaxInt64, math.MaxInt64
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("overflowing CAS encoded")
	}
	p = paidResultFixture("usage")
	for i := range 800 {
		p.MessageObjects = append(p.MessageObjects, ObjectRef{scopedHash(fmt.Sprintf("oversize %d", i)), 1})
	}
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize encoder accepted")
	}
	route := paidRouteFixture()
	for i := range route.Profile.Sources {
		route.Profile.Sources[i].Quote = strings.Repeat("&", 2000)
	}
	// The unescaped input fits, but its canonical known-field encoding exceeds
	// the record cap. Decoding must not produce a value that cannot be emitted.
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(route); err != nil {
		t.Fatal(err)
	}
	if raw.Len() >= MaxPaidPayloadBytes {
		t.Fatal("expansion fixture too large")
	}
	assertPaidInvalid(t, TypeRuntimePaidRouteConfigured, raw.String())
}

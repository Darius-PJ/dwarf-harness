package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func FuzzDecodeF3_13(f *testing.F) {
	types := []string{TypeRuntimePaidRouteConfigured, TypeRuntimeCredentialChanged, TypeRuntimeSpendConfigured, TypeRuntimePaidReserved, TypeRuntimePaidDispatchIntent, TypeRuntimePaidResult, TypeRuntimePaidOverrunAcknowledged, TypeRuntimePaidReceipt, "schema-4"}
	for _, fixture := range f3_13Payloads() {
		kind := uint8(slices.Index(types, fixture.typ))
		wire := string(mustEncode(fixture.p))
		for _, input := range []string{wire, `{}`, `null`, `[]`, wire + ` {}`, `{"future":{"nested":[{"x":1,"x":2}]},` + wire[1:], `{"future":{"x":null},` + wire[1:], wire + strings.Repeat(" ", 65536-len(wire)), wire + strings.Repeat(" ", 65537-len(wire))} {
			f.Add(kind, []byte(input))
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(wire), &fields); err != nil {
			f.Fatal(err)
		}
		for key, value := range fields {
			f.Add(kind, []byte(`{"`+key+`":`+string(value)+`,`+wire[1:]))
			f.Add(kind, []byte(strings.Replace(wire, `"`+key+`":`+string(value), `"`+key+`":null`, 1)))
		}
	}
	route := paidRouteFixture()
	route.Profile.Provider = "future_provider"
	route.Profile.NoChargeRules = []string{"future_rule"}
	route.Profile.Sources[0].Quote = "<>&\u2028\u2029"
	f.Add(uint8(0), []byte(mustEncode(route)))
	f.Add(uint8(0), bytes.Replace(mustEncode(route), []byte(`"profile":{`), []byte(`"profile":{"schema":1,`), 1))
	for _, number := range []string{"-0", "-1", "1.0", "1e0", "9223372036854775807", "9223372036854775808"} {
		f.Add(uint8(0), bytes.Replace(mustEncode(route), []byte(`"input_max":120`), []byte(`"input_max":`+number), 1))
	}
	f.Fuzz(func(t *testing.T, kind uint8, js []byte) {
		t.Parallel()
		typ := types[int(kind)%len(types)]
		p, err := decodeF3_13(typ, js)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("noncontract error: %v", err)
			}
			return
		}
		encoded, err := p.Encode()
		if err != nil || len(encoded) > MaxPaidPayloadBytes {
			t.Fatalf("accepted payload cannot encode: %v", err)
		}
		again, err := decodeF3_13(typ, encoded)
		if err != nil || !reflect.DeepEqual(p, again) {
			t.Fatalf("roundtrip changed value: %v", err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(encoded, stable) {
			t.Fatalf("unstable encoding: %v", err)
		}
	})
}

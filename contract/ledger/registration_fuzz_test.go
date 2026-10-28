package ledger

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzDecodeF3_139(f *testing.F) {
	types := []string{TypeRuntimeResourceRegistered, TypeRuntimeWorkDomainRegistered, TypeRuntimeDeclarationChanged, TypeRuntimeRegistrationReceipt, "schema-5"}
	for _, commit := range registrationInputs()[5:] {
		for _, draft := range commit.drafts[1:] {
			for tag, typ := range types {
				if draft.Type == typ {
					f.Add(uint8(tag), []byte(draft.Payload))
				}
			}
		}
		receipt, err := DecodeRuntimeRegistrationReceipt(commit.drafts[2].Payload)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint8(4), []byte(mustEncode(receipt.Submission)))
	}
	for tag, fixture := range registrationCodecFixtures() {
		wire := mustEncode(fixture.p)
		for _, suffix := range []string{`,"future":{"x":[{"a":1,"a":2}]}}`, `,"future":{"a":null}}`} {
			f.Add(uint8(tag), append(bytes.Clone(wire[:len(wire)-1]), suffix...))
		}
		atCap := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, MaxRegistrationPayloadBytes-len(wire))...)
		f.Add(uint8(tag), atCap)
		f.Add(uint8(tag), append(bytes.Clone(atCap), ' '))
		for _, raw := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{"a":1,"\u0061":2}`), []byte{0xff}, wire[:len(wire)-1]} {
			f.Add(uint8(tag), raw)
		}
	}
	declaration := mustEncode(registrationCodecFixtures()[2].p)
	for _, pair := range [][2]string{
		{`"kind":"task"`, `"kind":"task","binding":null`},
		{`"expected_version":0`, `"expected_version":9223372036854775807`},
		{`"version":1`, `"version":1e0`},
		{`"consumers":[`, `"consumers":[null,`},
		{`"audience":[`, `"audience":["019a1390-0000-7000-8000-000000000200",`},
	} {
		f.Add(uint8(2), bytes.Replace(declaration, []byte(pair[0]), []byte(pair[1]), 1))
	}
	f.Fuzz(func(t *testing.T, tag uint8, data []byte) {
		typ := types[int(tag)%len(types)]
		value, err := decodeF3_139(typ, json.RawMessage(data))
		if err != nil {
			return
		}
		if len(data) > MaxRegistrationPayloadBytes {
			t.Fatal("accepted oversized complete input")
		}
		canonical, err := value.Encode()
		if err != nil || len(canonical) > MaxRegistrationPayloadBytes {
			t.Fatalf("accepted input cannot encode: %v", err)
		}
		again, err := decodeF3_139(typ, canonical)
		if err != nil {
			t.Fatalf("canonical output cannot decode: %v", err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(stable, canonical) {
			t.Fatalf("canonical output is unstable: %v", err)
		}
	})
}

package ledger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzDecodeF3_91(f *testing.F) {
	fixtures := serviceCodecs()
	for tag, fixture := range fixtures {
		wire := mustEncode(fixture.p)
		f.Add(uint8(tag), []byte(wire))
		for _, suffix := range []string{`,"future":{"array":[{"x":1,"x":2}]}}`, `,"future":{"x":null}}`, `,"future":{"array":[{"x":[]}]}}`} {
			f.Add(uint8(tag), append(bytes.Clone(wire[:len(wire)-1]), suffix...))
		}
		atCap := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, MaxServicePayloadBytes-len(wire))...)
		f.Add(uint8(tag), atCap)
		f.Add(uint8(tag), append(bytes.Clone(atCap), ' '))
		for _, raw := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{"a":1,"\u0061":2}`), {0xff}, wire[:len(wire)-1]} {
			f.Add(uint8(tag), raw)
		}
	}
	wire := mustEncode(serviceObservationFixture())
	for _, row := range []struct{ old, replacement string }{
		{`"observed_through":20`, `"observed_through":9223372036854775808`},
		{`"observed_through":20`, `"observed_through":-0`},
		{`"observed_through":20`, `"observed_through":2e1`},
		{`"clock_held":false`, `"clock_held":null`},
		{`"source":{`, `"source":null,"source":{`},
		{`"kind":"paid_call"`, `"kind":"future_kind"`},
		{`"reason":"overloaded"`, `"reason":"future_reason"`},
		{`"reason":"overloaded"`, `"reason":"` + strings.Repeat("a", 64) + `"`},
		{`"reason":"overloaded"`, `"reason":"` + strings.Repeat("a", 65) + `"`},
		{`"source_sequence":18`, `"source_sequence":0`},
		{serviceID(10).String(), "019a9100-0000-4000-8000-00000000000a"},
	} {
		f.Add(uint8(0), registrationReplace(f, wire, row.old, row.replacement))
	}
	for _, s := range serviceSubmissionFixtures() {
		wire := mustEncode(s)
		f.Add(uint8(5), []byte(wire))
		f.Add(uint8(4), []byte(mustEncode(serviceReceiptFixture(s))))
		f.Add(uint8(5), registrationReplace(f, wire, `"schema":9`, `"schema":8`))
		f.Add(uint8(5), registrationReplace(f, wire, `"actor":"core"`, `"actor":"agent"`))
		f.Add(uint8(5), registrationReplace(f, wire, `"arguments":`, `"arguments":null,"arguments":`))
	}
	inputs, _ := serviceInputs()
	for _, c := range inputs {
		for _, d := range c.drafts {
			for tag, fixture := range fixtures {
				if d.Type == fixture.typ {
					f.Add(uint8(tag), []byte(d.Payload))
				}
			}
			if d.Type == TypeRuntimeServiceReceipt {
				r, err := DecodeRuntimeServiceReceipt(d.Payload)
				if err != nil {
					f.Fatal(err)
				}
				f.Add(uint8(5), []byte(mustEncode(r.Submission)))
			}
		}
	}
	f.Fuzz(func(t *testing.T, tag uint8, data []byte) {
		typ := fixtures[int(tag)%len(fixtures)].typ
		p, err := decodeF3_91(typ, json.RawMessage(data))
		if err != nil {
			return
		}
		if len(data) > MaxServicePayloadBytes {
			t.Fatal("accepted oversized complete input")
		}
		canonical, err := p.Encode()
		if err != nil || len(canonical) > MaxServicePayloadBytes {
			t.Fatalf("accepted input cannot encode: %v", err)
		}
		again, err := decodeF3_91(typ, canonical)
		if err != nil {
			t.Fatalf("canonical input refused: %v", err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(stable, canonical) {
			t.Fatalf("unstable canonical bytes: %v", err)
		}
	})
}

package ledger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzDecodeF3_90(f *testing.F) {
	fixtures := eligibilityCodecs()
	for tag, fixture := range fixtures {
		wire := mustEncode(fixture.p)
		f.Add(uint8(tag), []byte(wire))
		for _, suffix := range []string{`,"future":{"x":[{"a":1,"a":2}]}}`, `,"future":{"x":null}}`, `,"future":{"a":{"b":{"c":[]}}}}`} {
			f.Add(uint8(tag), append(bytes.Clone(wire[:len(wire)-1]), suffix...))
		}
		atCap := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, MaxEligibilityPayloadBytes-len(wire))...)
		f.Add(uint8(tag), atCap)
		f.Add(uint8(tag), append(bytes.Clone(atCap), ' '))
		for _, raw := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{"a":1,"\u0061":2}`), []byte{0xff}, wire[:len(wire)-1]} {
			f.Add(uint8(tag), raw)
		}
	}
	wire := mustEncode(eligibilityFixture())
	for _, row := range []struct{ old, replacement string }{
		{`"expected_version":0`, `"expected_version":9223372036854775807`},
		{`"version":1`, `"version":9223372036854775808`},
		{`"generation":1`, `"generation":1e0`},
		{`"expected_version":0`, `"expected_version":-0`},
		{`"state":"first_action_required"`, `"state":"future_state"`},
		{`"reason":"first_action"`, `"reason":"none"`},
		{`"reason":"first_action"`, `"reason":"future_reason"`},
		{`"reason":"first_action"`, `"reason":"` + strings.Repeat("a", 64) + `"`},
		{`"reason":"first_action"`, `"reason":"` + strings.Repeat("a", 65) + `"`},
		{`"initial_turn":{`, `"initial_turn":null,"initial_turn":{`},
		{`"task_binding":{`, `"task_binding":null,"task_binding":{`},
		{`"generation":1`, `"generation":0`},
		{`"input_sha256":"`, `"input_sha256":"x`},
		{eligibilityID(7).String(), "019a9000-0000-4000-8000-000000000007"},
	} {
		f.Add(uint8(0), registrationReplace(f, wire, row.old, row.replacement))
	}
	for _, s := range eligibilitySubmissionFixtures() {
		wire := mustEncode(s)
		f.Add(uint8(2), []byte(wire))
		f.Add(uint8(2), registrationReplace(f, wire, `"schema":8`, `"schema":7`))
		f.Add(uint8(2), registrationReplace(f, wire, `"actor":"`+s.Actor+`"`, `"actor":"agent"`))
		f.Add(uint8(2), registrationReplace(f, wire, `"arguments":`, `"arguments":null,"arguments":`))
		f.Add(uint8(1), []byte(mustEncode(eligibilityReceipt(s))))
	}
	inputs, _ := eligibilityInputs()
	for _, c := range inputs {
		last := c.drafts[len(c.drafts)-1]
		if last.Type != TypeRuntimeEligibilityReceipt {
			continue
		}
		r, err := DecodeRuntimeEligibilityReceipt(last.Payload)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint8(0), []byte(c.drafts[1].Payload))
		f.Add(uint8(1), []byte(last.Payload))
		f.Add(uint8(2), []byte(mustEncode(r.Submission)))
	}
	f.Fuzz(func(t *testing.T, tag uint8, data []byte) {
		typ := fixtures[int(tag)%len(fixtures)].typ
		p, err := decodeF3_90(typ, json.RawMessage(data))
		if err != nil {
			return
		}
		if len(data) > MaxEligibilityPayloadBytes {
			t.Fatal("accepted oversized complete input")
		}
		canonical, err := p.Encode()
		if err != nil || len(canonical) > MaxEligibilityPayloadBytes {
			t.Fatalf("accepted input cannot encode: %v", err)
		}
		again, err := decodeF3_90(typ, canonical)
		if err != nil {
			t.Fatalf("canonical input refused: %v", err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(stable, canonical) {
			t.Fatalf("unstable canonical bytes: %v", err)
		}
	})
}

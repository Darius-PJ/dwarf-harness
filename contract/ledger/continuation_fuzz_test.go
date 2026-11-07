package ledger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzDecodeF3_16(f *testing.F) {
	fixtures := continuationCodecs()
	for tag, fixture := range fixtures {
		wire := mustEncode(fixture.p)
		f.Add(uint8(tag), []byte(wire))
		for _, suffix := range []string{`,"future":{"x":[{"a":1,"a":2}]}}`, `,"future":{"x":null}}`, `,"future":{"a":{"b":{"c":[]}}}}`} {
			f.Add(uint8(tag), append(bytes.Clone(wire[:len(wire)-1]), suffix...))
		}
		atCap := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, MaxContinuationPayloadBytes-len(wire))...)
		f.Add(uint8(tag), atCap)
		f.Add(uint8(tag), append(bytes.Clone(atCap), ' '))
		for _, raw := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{"a":1,"\u0061":2}`), []byte{0xff}, wire[:len(wire)-1]} {
			f.Add(uint8(tag), raw)
		}
	}
	wait := mustEncode(continuationWaitFixture())
	f.Add(uint8(0), []byte(wait))
	for _, row := range []struct{ old, replacement string }{
		{`"expected_version":2`, `"expected_version":9223372036854775807`},
		{`"version":3`, `"version":9223372036854775808`},
		{`"generation":2`, `"generation":2e0`},
		{`"model_calls":0`, `"model_calls":-0`},
		{`"resume_state":"model_pending"`, `"resume_state":null`},
		{`"resume_state":"model_pending"`, `"resume_state":"future_phase"`},
		{`"wait_reason":"paid_prepare"`, `"wait_reason":"none"`},
		{`"wait_reason":"paid_prepare"`, `"wait_reason":"future_reason"`},
		{`"wait_reason":"paid_prepare"`, `"wait_reason":"` + strings.Repeat("a", 64) + `"`},
		{`"wait_reason":"paid_prepare"`, `"wait_reason":"` + strings.Repeat("a", 65) + `"`},
		{`"kind":"paid_attempt"`, `"kind":"future_kind"`},
		{`"source_sequence":18`, `"source_sequence":0`},
		{`"source_sequence":18`, `"source_sequence":null`},
		{`"pending":[`, `"pending":[null,`},
		{`"notice_cursors":[`, `"notice_cursors":[null,`},
		{`"after_sequence":0`, `"after_sequence":0,"after_sequence":1`},
	} {
		f.Add(uint8(0), registrationReplace(f, wait, row.old, row.replacement))
	}
	inputs, _ := continuationInputs()
	for _, c := range inputs {
		last := c.drafts[len(c.drafts)-1]
		if last.Type != TypeRuntimeContinuationReceipt {
			continue
		}
		r, err := DecodeRuntimeContinuationReceipt(last.Payload)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint8(0), []byte(c.drafts[1].Payload))
		f.Add(uint8(1), []byte(last.Payload))
		f.Add(uint8(2), []byte(mustEncode(r.Submission)))
	}
	f.Fuzz(func(t *testing.T, tag uint8, data []byte) {
		typ := fixtures[int(tag)%len(fixtures)].typ
		p, err := decodeF3_16(typ, json.RawMessage(data))
		if err != nil {
			return
		}
		if len(data) > MaxContinuationPayloadBytes {
			t.Fatal("accepted oversized complete input")
		}
		canonical, err := p.Encode()
		if err != nil || len(canonical) > MaxContinuationPayloadBytes {
			t.Fatalf("accepted input cannot encode: %v", err)
		}
		again, err := decodeF3_16(typ, canonical)
		if err != nil {
			t.Fatalf("canonical input refused: %v", err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(stable, canonical) {
			t.Fatalf("unstable canonical bytes: %v", err)
		}
	})
}

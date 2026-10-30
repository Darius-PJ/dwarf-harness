package ledger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzDecodeF3_15(f *testing.F) {
	fixtures := loopCodecFixtures()
	for tag, fixture := range fixtures {
		wire := mustEncode(fixture.p)
		f.Add(uint8(tag), []byte(wire))
		for _, suffix := range []string{`,"future":{"x":[{"a":1,"a":2}]}}`, `,"future":{"x":null}}`, `,"future":{"a":{"b":{"c":[]}}}}`} {
			f.Add(uint8(tag), append(bytes.Clone(wire[:len(wire)-1]), suffix...))
		}
		atCap := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, MaxLoopPayloadBytes-len(wire))...)
		f.Add(uint8(tag), atCap)
		f.Add(uint8(tag), append(bytes.Clone(atCap), ' '))
		for _, raw := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{"a":1,"\u0061":2}`), []byte{0xff}, wire[:len(wire)-1]} {
			f.Add(uint8(tag), raw)
		}
	}
	for _, row := range []struct {
		tag              uint8
		old, replacement string
	}{
		{0, `"expected_version":1`, `"expected_version":9223372036854775807`},
		{0, `"revision":2`, `"revision":9223372036854775808`},
		{0, `"mode":"append"`, `"mode":"append","previous_sha256":null`},
		{1, `"bytes":21`, `"bytes":null`},
		{1, `"admission_sequence":12`, `"admission_sequence":12e0`},
		{2, `"tool_name":"core_status_v1"`, `"tool_name":"file.read"`},
		{2, `"tool_name":"core_status_v1"`, `"tool_name":"` + strings.Repeat("n", 64) + `"`},
		{2, `"tool_name":"core_status_v1"`, `"tool_name":"` + strings.Repeat("n", 65) + `"`},
		{2, `"provider_call_id":"synthetic-call"`, `"provider_call_id":"` + strings.Repeat("c", 256) + `"`},
		{2, `"provider_call_id":"synthetic-call"`, `"provider_call_id":"` + strings.Repeat("c", 257) + `"`},
		{2, `"operation":"turn.inspect"`, `"operation":"Turn.inspect"`},
		{2, `"operation":"turn.inspect"`, `"operation":"future.operation"`},
		{2, `"operation":"turn.inspect"`, `"operation":"` + strings.Repeat("o", 64) + `"`},
		{2, `"operation":"turn.inspect"`, `"operation":"` + strings.Repeat("o", 65) + `"`},
		{3, `"outcome":"ok"`, `"outcome":"future_outcome"`},
		{3, `"outcome":"ok"`, `"outcome":"ok","effect_receipt_sequence":0`},
		{4, `"outcome":"finished"`, `"outcome":"future_outcome"`},
		{4, `"reason":"natural_stop"`, `"reason":"future_reason"`},
		{4, `"model_calls":2`, `"model_calls":-0`},
		{5, `"outcome":"applied"`, `"outcome":"future_outcome"`},
		{6, `"schema":6`, `"schema":6,"schema":6`},
		{6, `"input_object":{`, `"input_object":{"bytes":0,`},
	} {
		f.Add(row.tag, registrationReplace(f, mustEncode(fixtures[row.tag].p), row.old, row.replacement))
	}
	inputs, _ := loopInputs()
	for _, c := range inputs {
		last := c.drafts[len(c.drafts)-1]
		if last.Type != TypeRuntimeLoopReceipt {
			continue
		}
		r, err := DecodeRuntimeLoopReceipt(last.Payload)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint8(6), []byte(mustEncode(r.Submission)))
		for _, d := range c.drafts {
			for tag, fixture := range fixtures[:6] {
				if fixture.typ == d.Type {
					f.Add(uint8(tag), []byte(d.Payload))
				}
			}
		}
	}
	f.Fuzz(func(t *testing.T, tag uint8, data []byte) {
		typ := fixtures[int(tag)%len(fixtures)].typ
		p, err := decodeF3_15(typ, json.RawMessage(data))
		if err != nil {
			return
		}
		if len(data) > MaxLoopPayloadBytes {
			t.Fatal("accepted oversized complete input")
		}
		canonical, err := p.Encode()
		if err != nil || len(canonical) > MaxLoopPayloadBytes {
			t.Fatalf("accepted input cannot encode: %v", err)
		}
		again, err := decodeF3_15(typ, canonical)
		if err != nil {
			t.Fatalf("canonical input refused: %v", err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(stable, canonical) {
			t.Fatalf("unstable canonical bytes: %v", err)
		}
	})
}

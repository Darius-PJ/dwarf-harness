package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func FuzzDecodeF3_12(f *testing.F) {
	types := []string{TypeRuntimeReviewRequested, TypeRuntimeReviewResult, TypeRuntimeReviewReceipt, "schema-3"}
	for _, fixture := range f3_12Payloads() {
		var kind uint8
		for i, typ := range types {
			if typ == fixture.typ {
				kind = uint8(i)
			}
		}
		for _, wire := range []string{fixture.wire, `{}`, `null`, `[]`, fixture.wire + ` {}`, `{"future":{"nested":[{"x":1,"x":2}]},` + fixture.wire[1:], `{"future":{"x":null},` + fixture.wire[1:], fixture.wire + strings.Repeat(" ", 65536-len(fixture.wire)), fixture.wire + strings.Repeat(" ", 65537-len(fixture.wire))} {
			f.Add(kind, []byte(wire))
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(fixture.wire), &fields); err != nil {
			f.Fatal(err)
		}
		for key, value := range fields {
			f.Add(kind, []byte(`{"`+key+`":`+string(value)+`,`+fixture.wire[1:]))
			f.Add(kind, []byte(strings.Replace(fixture.wire, `"`+key+`":`+string(value), `"`+key+`":null`, 1)))
		}
	}
	request := reviewRequestFixture()
	private := PrivateRevisionRef{Binding: request.Binding.Assignee, Scope: "context", Slot: "memory", Revision: 1, SourceSequence: 9, SHA256: scopedHash("memory"), Bytes: 0}
	for _, subject := range []SubjectRef{
		request.Binding.Subject,
		{Kind: "private_revision", PrivateRevision: &private},
		{Kind: "future_subject", Members: json.RawMessage(`{"z":null,"text":"<>&\u2028\u2029","number":18446744073709551615,"decimal":1.50}`)},
	} {
		p := request
		p.Binding.Subject, p.Binding.RequiredEvidence = subject, []SubjectRef{subject}
		wire := mustEncode(p)
		f.Add(uint8(0), []byte(wire))
		f.Add(uint8(0), bytes.ReplaceAll(wire, []byte(`\u003c\u003e\u0026`), []byte("<>&")))
	}
	for _, number := range []string{"-0", "1.0", "1e0", "9223372036854775807", "9223372036854775808"} {
		f.Add(uint8(0), bytes.Replace(mustEncode(request), []byte(`"task_version":3`), []byte(`"task_version":`+number), 1))
	}
	f.Add(uint8(0), bytes.Replace(mustEncode(request), []byte(`"binding":{`), []byte(`"binding":{"generation":1,`), 1))
	f.Add(uint8(2), []byte(mustEncode(reviewReceiptFixture(true))))
	f.Fuzz(func(t *testing.T, kind uint8, js []byte) {
		t.Parallel()
		typ := types[int(kind)%len(types)]
		p, err := decodeF3_12(typ, js)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("noncontract error: %v", err)
			}
			return
		}
		encoded, err := p.Encode()
		if err != nil || len(encoded) > MaxReviewPayloadBytes {
			t.Fatalf("accepted payload cannot encode: %v", err)
		}
		again, err := decodeF3_12(typ, encoded)
		if err != nil || !reflect.DeepEqual(p, again) {
			t.Fatalf("roundtrip changed value: %#v %#v %v", p, again, err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(encoded, stable) {
			t.Fatalf("unstable encoding: %v", err)
		}
	})
}

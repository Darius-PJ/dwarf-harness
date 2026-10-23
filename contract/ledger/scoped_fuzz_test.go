package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func FuzzDecodeF3_11(f *testing.F) {
	fixtures := f3_11Payloads()
	for i, tc := range fixtures {
		for _, s := range []string{tc.wire, `{}`, `null`, `[]`, tc.wire + ` {}`, `{"future":{"x":[{"a":1,"a":2}]},` + tc.wire[1:], `{"future":{"x":null},` + tc.wire[1:], tc.wire + strings.Repeat(" ", MaxScopedPayloadBytes-len(tc.wire)), tc.wire + strings.Repeat(" ", MaxScopedPayloadBytes+1-len(tc.wire))} {
			f.Add(uint8(i), []byte(s))
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.wire), &fields); err != nil {
			f.Fatal(err)
		}
		for key, value := range fields {
			f.Add(uint8(i), []byte(`{"`+key+`":`+string(value)+`,`+tc.wire[1:]))
			f.Add(uint8(i), []byte(strings.Replace(tc.wire, `"`+key+`":`+string(value), `"`+key+`":null`, 1)))
		}
	}
	for _, number := range []string{"-0", "1.0", "1e0", "9223372036854775807", "9223372036854775808"} {
		f.Add(uint8(6), []byte(strings.Replace(fixtures[6].wire, `"expected_version":2`, `"expected_version":`+number, 1)))
	}
	future := scopedTaskFixture()
	future.Action, future.State, future.ExpectedVersion, future.Version = "fail", "failed", 1, 2
	future.Assignment = &Assignment{fid("22"), 1, Binding{fid("a1"), fid("d1")}, "owner", scopedOriginFixture()}
	future.FailureCause = "future_cause"
	future.FailureEvidence = &SubjectRef{Kind: "future_subject", Members: json.RawMessage(`{"z":null,"text":"<>&","amount":18446744073709551615}`)}
	f.Add(uint8(2), bytes.ReplaceAll(mustEncode(future), []byte(`\u003c\u003e\u0026`), []byte("<>&")))
	for _, tc := range scopedArgumentFixtures() {
		s := ScopedSubmission{2, "agent", scopedOriginFixture(), tc.operation, tc.target, fixtures[0].p.(RuntimeGrantIssued).GrantID, tc.expected, tc.arguments}
		r := scopedReceipt(s, 1, "future_outcome", [16]byte{})
		if scopedWorkOperation(s.Operation) {
			r.WorkID = fixtures[4].p.(RuntimeWorkAdmitted).WorkID
		}
		wire, err := r.Encode()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint8(9), []byte(wire))
	}
	f.Fuzz(func(t *testing.T, kind uint8, js []byte) {
		t.Parallel()
		typ := fixtures[int(kind)%len(fixtures)].typ
		p, err := decodeF3_11(typ, js)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("noncontract error: %v", err)
			}
			return
		}
		encoded, err := p.Encode()
		if err != nil || len(encoded) > MaxScopedPayloadBytes {
			t.Fatalf("accepted payload cannot encode: %v", err)
		}
		again, err := decodeF3_11(typ, encoded)
		if err != nil || !reflect.DeepEqual(p, again) {
			t.Fatalf("roundtrip changed value: %#v %#v %v", p, again, err)
		}
		stable, err := again.Encode()
		if err != nil || !bytes.Equal(encoded, stable) {
			t.Fatalf("unstable encoding: %v", err)
		}
	})
}

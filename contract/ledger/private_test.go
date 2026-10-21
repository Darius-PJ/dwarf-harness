package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"uuid"
)

func decodeF3_10(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeAgentRegistered:
		return DecodeRuntimeAgentRegistered(js)
	case TypeRuntimeContextCreated:
		return DecodeRuntimeContextCreated(js)
	case TypePrivateRevision:
		return DecodePrivateRevision(js)
	case TypeRuntimeCommandReceipt:
		return DecodeRuntimeCommandReceipt(js)
	}
	panic("not an F3-10 type: " + typ)
}

func f3_10Payloads() []struct {
	name string
	typ  string
	p    payload
	wire string
} {
	return []struct {
		name string
		typ  string
		p    payload
		wire string
	}{
		{"registered", TypeRuntimeAgentRegistered, RuntimeAgentRegistered{Directory: "Ada-a1", GeneralContextID: fid("d1")}, `{"directory":"Ada-a1","general_context_id":"0199c000-0000-7000-8000-0000000000d1"}`},
		{"created", TypeRuntimeContextCreated, RuntimeContextCreated{Kind: "work"}, `{"kind":"work"}`},
		{"revision", TypePrivateRevision, PrivateRevision{Scope: "context", ExpectedVersion: 1, Revision: 2, Changes: []PrivateChange{{Slot: "memory", Mode: "replace", SHA256: goldenB1}}}, `{"scope":"context","expected_version":1,"revision":2,"changes":[{"slot":"memory","mode":"replace","sha256":"` + goldenB1.String() + `","bytes":0}]}`},
		{"receipt", TypeRuntimeCommandReceipt, RuntimeCommandReceipt{CommandID: fid("31"), SubmissionSHA256: goldenSubmission, Operation: "private_write", Target: PrivateTarget{Scope: "context", Slots: []string{"memory"}}, ExpectedVersion: 1, Outcome: "applied"}, `{"command_id":"0199c000-0000-7000-8000-000000000031","submission_sha256":"` + goldenSubmission.String() + `","operation":"private_write","target":{"scope":"context","slots":["memory"]},"expected_version":1,"outcome":"applied"}`},
	}
}

// TestF3_10Payloads pins the public wire order and strict, additive decoding.
func TestF3_10Payloads(t *testing.T) {
	t.Parallel()
	for _, tc := range f3_10Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, err := tc.p.Encode()
			if err != nil || string(js) != tc.wire {
				t.Fatalf("Encode = %s, %v; want %s", js, err, tc.wire)
			}
			for _, data := range []string{tc.wire, `{"future":{"a":[null,1]},` + tc.wire[1:]} {
				got, err := decodeF3_10(tc.typ, []byte(data))
				if err != nil || !reflect.DeepEqual(got, tc.p) {
					t.Fatalf("Decode = %+v, %v; want %+v", got, err, tc.p)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encode = %s, %v; want %s", again, err, js)
				}
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(js, &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				for _, replacement := range []string{"", "null"} {
					bad := strings.Replace(tc.wire, `"`+key+`":`+string(value), `"`+key+`":null`, 1)
					if replacement == "" {
						copyFields := make(map[string]json.RawMessage, len(fields))
						for k, v := range fields {
							if k != key {
								copyFields[k] = v
							}
						}
						b, err := json.Marshal(copyFields)
						if err != nil {
							t.Fatal(err)
						}
						bad = string(b)
					}
					if _, err := decodeF3_10(tc.typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
						t.Errorf("missing/null %s: %v", key, err)
					}
				}
				bad := `{"` + key + `":` + string(value) + `,` + tc.wire[1:]
				if _, err := decodeF3_10(tc.typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
					t.Errorf("duplicate %s: %v", key, err)
				}
			}
			for _, bad := range []string{`{"future":{"a":[{"x":1,"x":2}]},` + tc.wire[1:], tc.wire + ` {}`, `null`, `[]`, `{}`, tc.wire[:len(tc.wire)-1], string([]byte{0xff})} {
				if _, err := decodeF3_10(tc.typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
					t.Errorf("malformed input accepted: %q, %v", bad, err)
				}
			}
			// The cap includes whitespace and ignored extensions, not just known fields.
			for _, n := range []int{16384, 16385} {
				data := tc.wire + strings.Repeat(" ", n-len(tc.wire))
				_, err := decodeF3_10(tc.typ, []byte(data))
				if n == 16384 && err != nil || n > 16384 && !errors.Is(err, ErrInvalid) {
					t.Errorf("%d-byte payload: %v", n, err)
				}
			}
		})
	}
}

// TestF3_10PayloadsValues exercises closed values, conditional members,
// open-token evolution and numeric boundaries through both public codecs.
func TestF3_10PayloadsValues(t *testing.T) {
	t.Parallel()
	revision := func(scope string, expected int64, changes ...PrivateChange) PrivateRevision {
		return PrivateRevision{scope, expected, expected + 1, changes}
	}
	replace := PrivateChange{Slot: "memory", Mode: "replace", SHA256: goldenB1}
	appendChunk := PrivateChange{Slot: "transcript", Mode: "append", SHA256: goldenB1, Bytes: 1}
	clear := PrivateChange{Slot: "continuation", Mode: "clear"}
	receipt := f3_10Payloads()[3].p.(RuntimeCommandReceipt)
	valid := []struct {
		typ string
		p   payload
	}{
		{TypeRuntimeAgentRegistered, RuntimeAgentRegistered{strings.Repeat("A", 128), fid("d1")}},
		{TypeRuntimeAgentRegistered, RuntimeAgentRegistered{"COM0", fid("d1")}},
		{TypeRuntimeContextCreated, RuntimeContextCreated{"future_kind"}},
		{TypeRuntimeContextCreated, RuntimeContextCreated{"a" + strings.Repeat("0_", 31) + "z"}},
		{TypePrivateRevision, revision("context", 1, appendChunk)},
		{TypePrivateRevision, revision("context", 1, clear)},
		{TypePrivateRevision, revision("agent", math.MaxInt64-1, replace)},
		{TypePrivateRevision, revision("context", 0, PrivateChange{Slot: "handoff", Mode: "replace", SHA256: goldenB1}, replace)},
		{TypePrivateRevision, revision("agent", 0, PrivateChange{Slot: "instructions", Mode: "replace", SHA256: goldenB1}, replace)},
	}
	previous := appendChunk
	previous.PreviousSHA256, previous.Bytes = goldenB2, math.MaxInt64
	valid = append(valid, struct {
		typ string
		p   payload
	}{TypePrivateRevision, revision("context", 1, previous)})
	allSlots := []PrivateChange{{Slot: "continuation", Mode: "replace", SHA256: goldenB2, Bytes: 1}, {Slot: "handoff", Mode: "replace", SHA256: goldenB1}, replace, previous}
	valid = append(valid, struct {
		typ string
		p   payload
	}{TypePrivateRevision, revision("context", 1, allSlots...)})
	for _, operation := range []string{"agent_register", "context_create", "future_operation", strings.Repeat("a", 64)} {
		p := receipt
		p.Operation, p.ExpectedVersion, p.Target.Slots = operation, 0, []string{}
		if operation == "agent_register" {
			p.Target.Scope = "agent"
		}
		valid = append(valid, struct {
			typ string
			p   payload
		}{TypeRuntimeCommandReceipt, p})
	}
	for _, outcome := range []string{"future_outcome", strings.Repeat("a", 64)} {
		p := receipt
		p.Outcome, p.RequestID = outcome, fid("e1")
		valid = append(valid, struct {
			typ string
			p   payload
		}{TypeRuntimeCommandReceipt, p})
	}
	for i, tc := range valid {
		js, err := tc.p.Encode()
		if err != nil {
			t.Fatalf("valid %d Encode: %v", i, err)
		}
		p, err := decodeF3_10(tc.typ, js)
		if err != nil || !reflect.DeepEqual(p, tc.p) {
			t.Errorf("valid %d roundtrip: %+v, %v; want %+v", i, p, err, tc.p)
		}
	}
	// Empty slot sets always encode as [], never null.
	identity := RuntimeCommandReceipt{CommandID: fid("31"), SubmissionSHA256: goldenB1, Operation: "agent_register", Target: PrivateTarget{Scope: "agent"}, Outcome: "applied"}
	if js := mustEncode(identity); !bytes.Contains(js, []byte(`"slots":[]`)) {
		t.Errorf("nil slots: %s", js)
	}
	invalid := []struct {
		typ string
		p   payload
	}{}
	for _, directory := range []string{"", strings.Repeat("a", 129), "_a", "-a", ".", "../a", `a\b`, "a/b", "a:b", "a.b", "é", "con", "PrN", "AUX", "nul", "COM1", "com9", "LPT1", "lpt9"} {
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeAgentRegistered, RuntimeAgentRegistered{directory, fid("d1")}})
	}
	for _, id := range []uuid.UUID{{}, uuid.MustParse("6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f")} {
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeAgentRegistered, RuntimeAgentRegistered{"Ada", id}})
		p := receipt
		p.CommandID = id
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeCommandReceipt, p})
	}
	for _, token := range []string{"", "General", "1work", "a.b", "a-b", "a b", strings.Repeat("a", 65), "é"} {
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeContextCreated, RuntimeContextCreated{token}})
		for _, field := range []string{"operation", "outcome"} {
			p := receipt
			if field == "operation" {
				p.Operation = token
			} else {
				p.Outcome = token
			}
			invalid = append(invalid, struct {
				typ string
				p   payload
			}{TypeRuntimeCommandReceipt, p})
		}
	}
	badChanges := []PrivateChange{
		{Slot: "future", Mode: "replace", SHA256: goldenB1},
		{Slot: "instructions", Mode: "replace", SHA256: goldenB1},
		{Slot: "memory", Mode: "future", SHA256: goldenB1},
		{Slot: "memory", Mode: "append", SHA256: goldenB1, Bytes: 1},
		{Slot: "memory", Mode: "clear"},
		{Slot: "memory", Mode: "replace"},
		{Slot: "memory", Mode: "replace", SHA256: goldenB1, Bytes: -1},
		{Slot: "memory", Mode: "replace", SHA256: goldenB1, PreviousSHA256: goldenB2},
		{Slot: "transcript", Mode: "replace", SHA256: goldenB1, Bytes: 1},
		{Slot: "transcript", Mode: "append", SHA256: goldenB1},
		{Slot: "continuation", Mode: "append", SHA256: goldenB1, Bytes: 1},
		{Slot: "continuation", Mode: "replace", SHA256: goldenB1},
		{Slot: "continuation", Mode: "clear", SHA256: goldenB1},
		{Slot: "continuation", Mode: "clear", Bytes: 1},
		{Slot: "continuation", Mode: "clear", PreviousSHA256: goldenB1},
	}
	for _, c := range badChanges {
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypePrivateRevision, revision("context", 1, c)})
	}
	for _, p := range []PrivateRevision{
		revision("future", 1, replace), revision("agent", 1, appendChunk),
		revision("context", 1), revision("context", 1, replace, replace),
		revision("context", 1, appendChunk, replace), revision("context", 0, replace),
		revision("context", -1, replace), revision("context", math.MaxInt64, replace),
		{Scope: "context", ExpectedVersion: 1, Revision: 1, Changes: []PrivateChange{replace}},
	} {
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypePrivateRevision, p})
	}
	for _, target := range []PrivateTarget{{"future", []string{"memory"}}, {"context", []string{"future"}}, {"agent", []string{"handoff"}}, {"context", []string{"memory", "memory"}}, {"context", []string{"transcript", "memory"}}, {"context", nil}} {
		p := receipt
		p.Target = target
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeCommandReceipt, p})
	}
	for _, expected := range []int64{-1, 0, math.MaxInt64} {
		p := receipt
		p.ExpectedVersion = expected
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeCommandReceipt, p})
	}
	for _, operation := range []string{"agent_register", "context_create"} {
		p := receipt
		p.Operation = operation
		invalid = append(invalid, struct {
			typ string
			p   payload
		}{TypeRuntimeCommandReceipt, p})
	}
	p := receipt
	p.SubmissionSHA256 = Digest{}
	invalid = append(invalid, struct {
		typ string
		p   payload
	}{TypeRuntimeCommandReceipt, p})
	p = receipt
	p.RequestID = uuid.MustParse("6f1c2b6e-4b1d-4c3a-9e2f-0a1b2c3d4e5f")
	invalid = append(invalid, struct {
		typ string
		p   payload
	}{TypeRuntimeCommandReceipt, p})
	for i, tc := range invalid {
		if js, err := tc.p.Encode(); !errors.Is(err, ErrInvalid) || js != nil {
			t.Errorf("invalid %d Encode=%s,%v", i, js, err)
		}
		// Plain marshaling deliberately bypasses the contract encoder. Values
		// representable as malformed wire data must also fail decoding.
		js, err := json.Marshal(tc.p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeF3_10(tc.typ, js); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid %d Decode(%s)=%v", i, js, err)
		}
	}
}

func TestF3_10PayloadsNestedMembers(t *testing.T) {
	t.Parallel()
	revision := f3_10Payloads()[2].wire
	receipt := f3_10Payloads()[3].wire
	for _, tc := range []struct{ typ, wire, member string }{
		{TypePrivateRevision, revision, `"slot":"memory"`},
		{TypePrivateRevision, revision, `"mode":"replace"`},
		{TypePrivateRevision, revision, `"sha256":"` + goldenB1.String() + `"`},
		{TypePrivateRevision, revision, `"bytes":0`},
		{TypeRuntimeCommandReceipt, receipt, `"scope":"context"`},
		{TypeRuntimeCommandReceipt, receipt, `"slots":["memory"]`},
	} {
		key, _, _ := strings.Cut(tc.member, ":")
		for _, replacement := range []string{key + `:null`, tc.member + `,` + tc.member} {
			bad := strings.Replace(tc.wire, tc.member, replacement, 1)
			if _, err := decodeF3_10(tc.typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
				t.Errorf("nested %s accepted: %s, %v", key, bad, err)
			}
		}
		bad := strings.Replace(tc.wire, tc.member+",", "", 1)
		if bad == tc.wire {
			bad = strings.Replace(tc.wire, ","+tc.member, "", 1)
		}
		if _, err := decodeF3_10(tc.typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Errorf("missing nested %s: %v", key, err)
		}
	}
	for _, tc := range []struct{ typ, wire, old string }{
		{TypePrivateRevision, revision, `"bytes":0`},
		{TypePrivateRevision, revision, `"expected_version":1`},
		{TypePrivateRevision, revision, `"revision":2`},
		{TypeRuntimeCommandReceipt, receipt, `"expected_version":1`},
	} {
		key, _, _ := strings.Cut(tc.old, ":")
		for _, number := range []string{"-1", "-0", "01", "1.0", "1e0", `"1"`, "9223372036854775808", "true"} {
			bad := strings.Replace(tc.wire, tc.old, key+":"+number, 1)
			if _, err := decodeF3_10(tc.typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
				t.Errorf("numeric %s=%s: %v", key, number, err)
			}
		}
	}
	for _, tc := range []struct{ typ, wire, key string }{
		{TypeRuntimeAgentRegistered, f3_10Payloads()[0].wire, "general_context_id"},
		{TypeRuntimeCommandReceipt, receipt, "command_id"},
		{TypeRuntimeCommandReceipt, receipt, "request_id"},
	} {
		for _, value := range []string{`null`, `""`, `1`, `"0199C000-0000-7000-8000-0000000000d1"`, `"00000000-0000-0000-0000-000000000000"`} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.wire), &fields); err != nil {
				t.Fatal(err)
			}
			fields[tc.key] = json.RawMessage(value)
			bad, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeF3_10(tc.typ, bad); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s=%s: %v", tc.key, value, err)
			}
		}
	}
	for _, digest := range []string{`null`, `1`, `"abc"`, `"` + strings.Repeat("A", 64) + `"`, `"` + strings.Repeat("0", 64) + `"`} {
		for _, bad := range []string{
			strings.Replace(revision, `"`+goldenB1.String()+`"`, digest, 1),
			strings.Replace(revision, `"bytes":0`, `"bytes":0,"previous_sha256":`+digest, 1),
		} {
			if _, err := DecodePrivateRevision([]byte(bad)); !errors.Is(err, ErrInvalid) {
				t.Errorf("invalid digest accepted: %s, %v", bad, err)
			}
		}
		bad := strings.Replace(receipt, `"`+goldenSubmission.String()+`"`, digest, 1)
		if _, err := DecodeRuntimeCommandReceipt([]byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Errorf("submission digest accepted: %s, %v", bad, err)
		}
	}
	for _, member := range []string{`"sha256":null`, `"bytes":0`, `"previous_sha256":"` + goldenB1.String() + `"`} {
		bad := `{"scope":"context","expected_version":1,"revision":2,"changes":[{"slot":"continuation","mode":"clear",` + member + `}]}`
		if _, err := DecodePrivateRevision([]byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Errorf("clear member accepted: %s, %v", bad, err)
		}
	}
	for _, tc := range []struct{ typ, wire, old string }{
		{TypePrivateRevision, revision, `"slot":"memory"`},
		{TypeRuntimeCommandReceipt, receipt, `"scope":"context"`},
	} {
		extended := strings.Replace(tc.wire, tc.old, `"future":{"items":[null,1]},`+tc.old, 1)
		p, err := decodeF3_10(tc.typ, []byte(extended))
		if err != nil {
			t.Fatal(err)
		}
		if js := mustEncode(p); string(js) != tc.wire {
			t.Errorf("nested extension changed value: %s", js)
		}
	}
	for _, bad := range []string{
		strings.Replace(revision, `"changes":[`, `"changes":[null,`, 1),
		strings.Replace(revision, `"changes":[`, `"changes":[[],`, 1),
		strings.Replace(receipt, `["memory"]`, `[null]`, 1),
		strings.Replace(receipt, `["memory"]`, `"memory"`, 1),
	} {
		typ := TypePrivateRevision
		if strings.Contains(bad, `"target"`) {
			typ = TypeRuntimeCommandReceipt
		}
		if _, err := decodeF3_10(typ, []byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Errorf("nested wrong shape: %s, %v", bad, err)
		}
	}
}

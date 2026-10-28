package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"uuid"
)

func decodeF3_139(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeResourceRegistered:
		return DecodeRuntimeResourceRegistered(js)
	case TypeRuntimeWorkDomainRegistered:
		return DecodeRuntimeWorkDomainRegistered(js)
	case TypeRuntimeDeclarationChanged:
		return DecodeRuntimeDeclarationChanged(js)
	case TypeRuntimeRegistrationReceipt:
		return DecodeRuntimeRegistrationReceipt(js)
	case "schema-5":
		return DecodeRegistrationSubmission(js)
	default:
		return nil, fmt.Errorf("unknown registration codec %s", typ)
	}
}

func registrationSubmissionFixture() RegistrationSubmission {
	return RegistrationSubmission{5, "owner", Origin{registrationID(0x100), registrationID(0x101), registrationID(0x102)}, "resource.set", ResourceRegistrationArguments{registrationID(0x400), 0, registrationAddress(), []uuid.UUID{registrationID(0x100), registrationID(0x200)}}}
}

func registrationReceiptFixture(s RegistrationSubmission) RuntimeRegistrationReceipt {
	return RuntimeRegistrationReceipt{registrationID(0x1006), Digest(sha256.Sum256(mustEncode(s))), s, "applied", s.expectedVersion() + 1}
}

func registrationCodecFixtures() []struct {
	typ string
	p   payload
} {
	s := registrationSubmissionFixture()
	a := s.Arguments.(ResourceRegistrationArguments)
	return []struct {
		typ string
		p   payload
	}{
		{TypeRuntimeResourceRegistered, RuntimeResourceRegistered{a.ResourceID, 0, 1, a.Resource, a.Audience}},
		{TypeRuntimeWorkDomainRegistered, RuntimeWorkDomainRegistered{registrationID(0x401), 0, 1, a.Audience}},
		{TypeRuntimeDeclarationChanged, RuntimeDeclarationChanged{registrationID(0x500), 0, 1, RegistrationRef{"resource", a.ResourceID, 1}, a.Audience, []DeclarationConsumer{{Kind: "context", Binding: Binding{registrationID(0x100), registrationID(0x103)}}, {Kind: "task", TaskID: registrationID(0x300)}}}},
		{TypeRuntimeRegistrationReceipt, registrationReceiptFixture(s)},
		{"schema-5", s},
	}
}

func registrationReplace(t testing.TB, js []byte, old, replacement string) []byte {
	t.Helper()
	if !bytes.Contains(js, []byte(old)) {
		t.Fatalf("mutation missing %q", old)
	}
	return bytes.Replace(js, []byte(old), []byte(replacement), 1)
}

func TestRegistrationStrictObjects(t *testing.T) {
	t.Parallel()
	for _, fixture := range registrationCodecFixtures() {
		t.Run(fixture.typ, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(fixture.p)
			decoded, err := decodeF3_139(fixture.typ, wire)
			if err != nil || !reflect.DeepEqual(decoded, fixture.p) {
				t.Fatalf("roundtrip: %+v %v", decoded, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			for key := range fields {
				for _, remove := range []bool{false, true} {
					copyFields := make(map[string]json.RawMessage, len(fields))
					for k, v := range fields {
						copyFields[k] = v
					}
					copyFields[key] = json.RawMessage("null")
					if remove {
						delete(copyFields, key)
					}
					bad, err := json.Marshal(copyFields)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := decodeF3_139(fixture.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Fatalf("known field %s remove=%v accepted: %v", key, remove, err)
					}
				}
			}
			for name, suffix := range map[string]string{
				"recursive_duplicate": `,"future":{"array":[{"x":1,"x":2}]}}`,
				"escaped_duplicate":   `,"future":{"x":1,"\u0078":2}}`,
				"invalid_utf8":        ",\"future\":\"\xff\"}",
				"trailing_object":     `} {}`,
			} {
				bad := append(bytes.Clone(wire[:len(wire)-1]), suffix...)
				if _, err := decodeF3_139(fixture.typ, bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("%s accepted: %v", name, err)
				}
			}
			extended := append(bytes.Clone(wire[:len(wire)-1]), `,"future":{"array":[null,{"new":true}]}}`...)
			p, err := decodeF3_139(fixture.typ, extended)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("optional extension changed known encoding: %v", err)
			}
			for _, raw := range [][]byte{[]byte(`[]`), []byte(`null`), wire[:len(wire)-1]} {
				if _, err := decodeF3_139(fixture.typ, raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("incomplete/nonobject accepted: %v", err)
				}
			}
		})
	}
}

func TestRegistrationNestedValidation(t *testing.T) {
	t.Parallel()
	fixtures := registrationCodecFixtures()
	for _, row := range []struct {
		name, old, replacement string
		fixture                int
	}{
		{"address_missing", `,"resource_key":"paid-route/019a1390-0000-7000-8000-000000000450"`, ``, 0},
		{"address_null", `"resource_key":"paid-route/019a1390-0000-7000-8000-000000000450"`, `"resource_key":null`, 0},
		{"address_wrong_kind_member", `"kind":"external"`, `"kind":"external","binding":null`, 0},
		{"address_duplicate", `"kind":"external"`, `"kind":"external","kind":"external"`, 0},
		{"resource_nonexternal", `"kind":"external","resource_key":"paid-route/019a1390-0000-7000-8000-000000000450"`, `"kind":"task","task_id":"019a1390-0000-7000-8000-000000000300"`, 0},
		{"domain_resource_member", `"expected_version":0`, `"resource":null,"expected_version":0`, 1},
		{"subject_unknown_kind", `"kind":"resource"`, `"kind":"declaration"`, 2},
		{"subject_zero_version", `"version":1},"audience"`, `"version":0},"audience"`, 2},
		{"subject_null_id", `"id":"019a1390-0000-7000-8000-000000000400"`, `"id":null`, 2},
		{"consumer_task_binding", `"kind":"task"`, `"kind":"task","binding":null`, 2},
		{"consumer_context_task", `"kind":"context"`, `"kind":"context","task_id":null`, 2},
		{"consumer_unknown_kind", `"kind":"task"`, `"kind":"artifact"`, 2},
		{"binding_null", `"binding":{"agent_id":"019a1390-0000-7000-8000-000000000100","context_id":"019a1390-0000-7000-8000-000000000103"}`, `"binding":null`, 2},
		{"binding_duplicate", `"context_id":"019a1390-0000-7000-8000-000000000103"`, `"context_id":"019a1390-0000-7000-8000-000000000103","context_id":null`, 2},
		{"origin_null_session", `"session_id":"019a1390-0000-7000-8000-000000000101"`, `"session_id":null`, 4},
		{"origin_duplicate", `"session_id":"019a1390-0000-7000-8000-000000000101"`, `"session_id":"019a1390-0000-7000-8000-000000000101","session_id":null`, 4},
		{"argument_wrong_operation_field", `"expected_version":0`, `"subject":null,"expected_version":0`, 4},
		{"argument_missing_resource", `"resource":{"kind":"external","resource_key":"paid-route/019a1390-0000-7000-8000-000000000450"},`, ``, 4},
		{"closed_actor", `"actor":"owner"`, `"actor":"agent"`, 4},
		{"closed_schema", `"schema":5`, `"schema":4`, 4},
		{"no_register_operation", `"operation":"resource.set"`, `"operation":"resource.register"`, 4},
		{"no_revise_operation", `"operation":"resource.set"`, `"operation":"resource.revise"`, 4},
		{"uuid_uppercase", `019a1390`, `019A1390`, 0},
		{"uuid_wrong_version", `-7000-`, `-4000-`, 0},
		{"noninteger_version", `"version":1`, `"version":1.0`, 0},
		{"signed_expected", `"expected_version":0`, `"expected_version":-0`, 0},
		{"exponent_expected", `"expected_version":0`, `"expected_version":0e0`, 0},
		{"string_expected", `"expected_version":0`, `"expected_version":"0"`, 0},
		{"overflow_expected", `"expected_version":0`, `"expected_version":9223372036854775808`, 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			fixture := fixtures[row.fixture]
			bad := registrationReplace(t, mustEncode(fixture.p), row.old, row.replacement)
			if _, err := decodeF3_139(fixture.typ, bad); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid nested shape accepted: %s: %v", bad, err)
			}
		})
	}
}

func TestRegistrationVersionsAndSets(t *testing.T) {
	t.Parallel()
	base := registrationCodecFixtures()[2].p.(RuntimeDeclarationChanged)
	for _, row := range []struct {
		name   string
		mutate func(*RuntimeDeclarationChanged)
		valid  bool
	}{
		{"create", func(*RuntimeDeclarationChanged) {}, true},
		{"revision", func(p *RuntimeDeclarationChanged) { p.ExpectedVersion, p.Version = 1, 2 }, true},
		{"maximum", func(p *RuntimeDeclarationChanged) { p.ExpectedVersion, p.Version = math.MaxInt64-1, math.MaxInt64 }, true},
		{"overflow", func(p *RuntimeDeclarationChanged) { p.ExpectedVersion, p.Version = math.MaxInt64, math.MinInt64 }, false},
		{"skip_version", func(p *RuntimeDeclarationChanged) { p.Version = 2 }, false},
		{"negative_expected", func(p *RuntimeDeclarationChanged) { p.ExpectedVersion = -1 }, false},
		{"withdraw", func(p *RuntimeDeclarationChanged) { p.Consumers = []DeclarationConsumer{} }, true},
		{"nil_consumers", func(p *RuntimeDeclarationChanged) { p.Consumers = nil }, false},
		{"empty_audience", func(p *RuntimeDeclarationChanged) { p.Audience = []uuid.UUID{} }, false},
		{"duplicate_audience", func(p *RuntimeDeclarationChanged) {
			p.Audience = []uuid.UUID{registrationID(0x100), registrationID(0x100)}
		}, false},
		{"unordered_audience", func(p *RuntimeDeclarationChanged) {
			p.Audience = []uuid.UUID{registrationID(0x200), registrationID(0x100)}
		}, false},
		{"duplicate_consumers", func(p *RuntimeDeclarationChanged) {
			p.Consumers = []DeclarationConsumer{p.Consumers[0], p.Consumers[0]}
		}, false},
		{"unordered_consumers", func(p *RuntimeDeclarationChanged) {
			p.Consumers = []DeclarationConsumer{p.Consumers[1], p.Consumers[0]}
		}, false},
		{"consumer_foreign_typed_field", func(p *RuntimeDeclarationChanged) {
			p.Consumers = []DeclarationConsumer{{Kind: "task", TaskID: registrationID(0x300), Binding: Binding{registrationID(0x100), registrationID(0x103)}}}
		}, false},
		{"kind_scoped_identity", func(p *RuntimeDeclarationChanged) { p.DeclarationID = p.Subject.ID }, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := base
			row.mutate(&p)
			wire, err := p.Encode()
			if row.valid {
				if err != nil {
					t.Fatal(err)
				}
				got, err := DecodeRuntimeDeclarationChanged(wire)
				if err != nil || !reflect.DeepEqual(got, p) {
					t.Fatalf("valid value changed: %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid value encoded: %s %v", wire, err)
			}
			// Exercise decoding independently of the encoder's validation.
			raw, err := json.Marshal(p)
			if err == nil {
				_, err = DecodeRuntimeDeclarationChanged(raw)
				if (err == nil) != row.valid {
					t.Fatalf("decode valid=%v: %v", row.valid, err)
				}
			}
		})
	}
}

func TestRegistrationOpaqueAddress(t *testing.T) {
	t.Parallel()
	for _, key := range []string{registrationID(0x450).String(), "paid-route/" + registrationID(0x450).String(), "Shared Compute: East / α & <queue>\u2028", " e\u0301 ", strings.Repeat("x", 256)} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			s := registrationSubmissionFixture()
			a := s.Arguments.(ResourceRegistrationArguments)
			a.Resource.ResourceKey = key
			s.Arguments = a
			receipt := registrationReceiptFixture(s)
			got, err := DecodeRuntimeRegistrationReceipt(mustEncode(receipt))
			if err != nil || got.Submission.Arguments.(ResourceRegistrationArguments).Resource.ResourceKey != key {
				t.Fatalf("opaque key changed: %+v %v", got, err)
			}
			changed := a
			changed.Resource.ResourceKey = "different identity"
			got.Submission.Arguments = changed
			if _, err := got.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("address change did not invalidate receipt digest")
			}
		})
	}
	for _, key := range []string{"", strings.Repeat("x", 257), "control\n", "\xff"} {
		p := registrationCodecFixtures()[0].p.(RuntimeResourceRegistered)
		p.Resource.ResourceKey = key
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid key %q accepted: %v", key, err)
		}
	}
}

func TestRegistrationCanonicalSubmission(t *testing.T) {
	t.Parallel()
	base := registrationSubmissionFixture()
	audience := `[` + fmt.Sprintf("%q,%q", registrationID(0x100), registrationID(0x200)) + `]`
	root := fmt.Sprintf(`{"schema":5,"actor":"owner","origin":{"agent_id":%q,"session_id":%q,"context_id":%q},"operation":`, base.Origin.AgentID, base.Origin.SessionID, base.Origin.ContextID)
	for _, row := range []struct {
		operation string
		arguments any
		wantArgs  string
	}{
		{"resource.set", base.Arguments, fmt.Sprintf(`{"resource_id":%q,"expected_version":0,"resource":{"kind":"external","resource_key":%q},"audience":%s}`, registrationID(0x400), registrationAddress().ResourceKey, audience)},
		{"work_domain.set", WorkDomainRegistrationArguments{registrationID(0x400), 0, []uuid.UUID{registrationID(0x100), registrationID(0x200)}}, fmt.Sprintf(`{"work_domain_id":%q,"expected_version":0,"audience":%s}`, registrationID(0x400), audience)},
		{"declaration.set", DeclarationArguments{registrationID(0x400), 1, RegistrationRef{"work_domain", registrationID(0x400), 2}, []uuid.UUID{registrationID(0x100), registrationID(0x200)}, []DeclarationConsumer{}}, fmt.Sprintf(`{"declaration_id":%q,"expected_version":1,"subject":{"kind":"work_domain","id":%q,"version":2},"audience":%s,"consumers":[]}`, registrationID(0x400), registrationID(0x400), audience)},
	} {
		t.Run(row.operation, func(t *testing.T) {
			t.Parallel()
			s := base
			s.Operation, s.Arguments = row.operation, row.arguments
			want := root + fmt.Sprintf("%q", row.operation) + `,"arguments":` + row.wantArgs + `}`
			if got := mustEncode(s); string(got) != want {
				t.Fatalf("canonical submission\ngot  %s\nwant %s", got, want)
			}
			receipt := registrationReceiptFixture(s)
			if receipt.SubmissionSHA256 != Digest(sha256.Sum256([]byte(want))) {
				t.Fatal("digest does not bind exact canonical request")
			}
			wire := mustEncode(receipt)
			extended := registrationReplace(t, wire, `"arguments":{`, `"arguments":{"optional":{"x":1},`)
			got, err := DecodeRuntimeRegistrationReceipt(extended)
			if err != nil || !bytes.Equal(mustEncode(got), wire) {
				t.Fatalf("optional argument changed digest: %v", err)
			}
			for _, replacement := range []string{`"expected_version":2`, `"expected_version":9223372036854775807`} {
				bad := registrationReplace(t, wire, fmt.Sprintf(`"expected_version":%d`, s.expectedVersion()), replacement)
				if _, err := DecodeRuntimeRegistrationReceipt(bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("changed request/overflow accepted: %v", err)
				}
			}
			receipt.Version++
			if _, err := receipt.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("receipt accepted wrong resulting version")
			}
			s.Operation = "resource.set"
			if row.operation != s.Operation {
				if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
					t.Fatal("operation accepted foreign concrete arguments")
				}
			}
		})
	}
	for _, args := range []any{&ResourceRegistrationArguments{}, json.RawMessage(`{}`), map[string]any{}} {
		s := base
		s.Arguments = args
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nonconcrete arguments %T accepted", args)
		}
	}
}

func TestRegistrationReceiptBinding(t *testing.T) {
	t.Parallel()
	p := registrationReceiptFixture(registrationSubmissionFixture())
	p.Outcome = "future_result_2"
	wire := mustEncode(p)
	got, err := DecodeRuntimeRegistrationReceipt(wire)
	if err != nil || got.Outcome != p.Outcome {
		t.Fatalf("open outcome lost: %+v %v", got, err)
	}
	for _, row := range []struct{ old, replacement string }{
		{p.SubmissionSHA256.String(), strings.Repeat("0", 64)},
		{p.SubmissionSHA256.String(), strings.ToUpper(p.SubmissionSHA256.String())},
		{p.SubmissionSHA256.String(), scopedHash("wrong submission").String()},
		{`"outcome":"future_result_2"`, `"outcome":"Applied"`},
		{`"outcome":"future_result_2"`, `"outcome":"` + strings.Repeat("a", 65) + `"`},
		{`"version":1}`, `"version":2}`},
	} {
		bad := registrationReplace(t, wire, row.old, row.replacement)
		if _, err := DecodeRuntimeRegistrationReceipt(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid receipt accepted: %s: %v", bad, err)
		}
	}
}

func TestRegistrationCompleteInputCap(t *testing.T) {
	t.Parallel()
	for _, fixture := range registrationCodecFixtures() {
		t.Run(fixture.typ, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(fixture.p)
			for _, extension := range []bool{false, true} {
				atCap := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, MaxRegistrationPayloadBytes-len(wire))...)
				if extension {
					prefix := append(bytes.Clone(wire[:len(wire)-1]), `,"future":"`...)
					atCap = append(prefix, bytes.Repeat([]byte{'x'}, MaxRegistrationPayloadBytes-len(prefix)-2)...)
					atCap = append(atCap, '"', '}')
				}
				got, err := decodeF3_139(fixture.typ, atCap)
				if err != nil || !bytes.Equal(mustEncode(got), wire) {
					t.Fatalf("exact complete input cap extension=%v: %v", extension, err)
				}
				if _, err := decodeF3_139(fixture.typ, append(atCap, ' ')); !errors.Is(err, ErrInvalid) {
					t.Fatalf("oversized complete input accepted extension=%v: %v", extension, err)
				}
			}
		})
	}
}

func TestRegistrationEncodedCaps(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{TypeRuntimeResourceRegistered, "schema-5", TypeRuntimeRegistrationReceipt} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			s := registrationSubmissionFixture()
			a := s.Arguments.(ResourceRegistrationArguments)
			a.Resource.ResourceKey = "x"
			a.Audience = []uuid.UUID{}
			makePayload := func() payload {
				s.Arguments = a
				switch typ {
				case TypeRuntimeResourceRegistered:
					return RuntimeResourceRegistered{a.ResourceID, 0, 1, a.Resource, a.Audience}
				case "schema-5":
					return s
				default:
					return registrationReceiptFixture(s)
				}
			}
			// UUID entries add 39 bytes each; reserve room to tune the explicit
			// opaque key to an exact boundary without an ignored extension.
			for i := 1; ; i++ {
				a.Audience = append(a.Audience, registrationID(0x10000+i))
				wire, err := json.Marshal(makePayload())
				if err != nil {
					t.Fatal(err)
				}
				gap := MaxRegistrationPayloadBytes - len(wire)
				if gap <= 200 {
					if gap < 1 {
						t.Fatal("boundary construction overshot")
					}
					a.Resource.ResourceKey += strings.Repeat("x", gap)
					break
				}
			}
			p := makePayload()
			wire, err := p.Encode()
			if err != nil || len(wire) != MaxRegistrationPayloadBytes {
				t.Fatalf("encoded boundary size=%d: %v", len(wire), err)
			}
			if _, err := decodeF3_139(typ, wire); err != nil {
				t.Fatal(err)
			}
			// Literal '<' fits the input cap, but canonical JSON escaping
			// expands it. Decoding must also enforce the encoded-output cap.
			key := a.Resource.ResourceKey
			a.Resource.ResourceKey = key[:len(key)-1] + "<"
			expanded, err := json.Marshal(makePayload())
			if err != nil {
				t.Fatal(err)
			}
			literal := bytes.ReplaceAll(expanded, []byte(`\u003c`), []byte("<"))
			if len(literal) != MaxRegistrationPayloadBytes {
				t.Fatalf("literal boundary size=%d", len(literal))
			}
			if _, err := decodeF3_139(typ, literal); !errors.Is(err, ErrInvalid) {
				t.Fatalf("oversized canonical output accepted: %v", err)
			}
			a.Resource.ResourceKey = key
			a.Resource.ResourceKey += "x"
			if _, err := makePayload().Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("oversized encoded %s accepted: %v", typ, err)
			}
		})
	}
	// Array population is bounded by encoded bytes, not future producer limits.
	p := registrationCodecFixtures()[2].p.(RuntimeDeclarationChanged)
	p.Audience, p.Consumers = []uuid.UUID{}, []DeclarationConsumer{}
	for i := range 300 {
		p.Audience = append(p.Audience, registrationID(0x10000+i))
		p.Consumers = append(p.Consumers, DeclarationConsumer{Kind: "task", TaskID: registrationID(0x20000 + i)})
	}
	if got, err := DecodeRuntimeDeclarationChanged(mustEncode(p)); err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("codec imposed a population limit: %v", err)
	}
}

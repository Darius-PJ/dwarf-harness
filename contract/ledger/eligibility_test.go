package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

func eligibilityID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a9000-0000-7000-8000-%012x", n))
}

func eligibilityTurnFixture() EligibilityTurn {
	return EligibilityTurn{eligibilityID(1), eligibilityID(2), eligibilityID(3), eligibilityID(4), scopedHash("Synthetic first input."), &TaskBinding{eligibilityID(5), eligibilityID(6), 1}}
}

func eligibilityFixture() RuntimeEligibilityChanged {
	turn := eligibilityTurnFixture()
	return RuntimeEligibilityChanged{0, 1, "first_action_required", "first_action", eligibilityID(7), &EligibilityFirstAction{eligibilityID(8), turn, turn, nil}}
}

func eligibilitySubmissionFixtures() []EligibilitySubmission {
	o, turn := scopedOriginFixture(), eligibilityTurnFixture()
	return []EligibilitySubmission{
		{8, "owner", o, "eligibility.hold", 0, uuid.UUID{}, EligibilityHoldArguments{"explicit_hold"}},
		{8, "owner", o, "eligibility.require_first", 1, eligibilityID(7), EligibilityRequireFirstArguments{"first_action", eligibilityID(8), turn}},
		{8, "owner", o, "eligibility.first_next", 2, eligibilityID(7), EligibilityFirstNextArguments{eligibilityID(8), turn}},
		{8, "owner", o, "eligibility.release", 3, eligibilityID(7), EligibilityReleaseArguments{}},
		{8, "core", o, "eligibility.first_refused", 2, eligibilityID(7), EligibilityFirstRefusedArguments{eligibilityID(8), turn.TurnID, turn.StartCommandID, "turn_admission", "denied"}},
	}
}

func eligibilityReceipt(s EligibilitySubmission) RuntimeEligibilityReceipt {
	return RuntimeEligibilityReceipt{eligibilityID(9), Digest(sha256.Sum256(mustEncode(s))), s, "recorded", 15}
}

func eligibilityCodecs() []struct {
	typ string
	p   payload
} {
	s := eligibilitySubmissionFixtures()[0]
	return []struct {
		typ string
		p   payload
	}{{TypeRuntimeEligibilityChanged, eligibilityFixture()}, {TypeRuntimeEligibilityReceipt, eligibilityReceipt(s)}, {"schema-8", s}}
}

func decodeF3_90(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeEligibilityChanged:
		return DecodeRuntimeEligibilityChanged(js)
	case TypeRuntimeEligibilityReceipt:
		return DecodeRuntimeEligibilityReceipt(js)
	case "schema-8":
		return DecodeEligibilitySubmission(js)
	default:
		return nil, fmt.Errorf("unknown eligibility codec %s", typ)
	}
}

func TestEligibilityPayloads(t *testing.T) {
	t.Parallel()
	for _, f := range eligibilityCodecs() {
		t.Run(f.typ, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(f.p)
			p, err := decodeF3_90(f.typ, wire)
			if err != nil || !reflect.DeepEqual(p, f.p) {
				t.Fatalf("roundtrip: %+v %v", p, err)
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
					if _, err := decodeF3_90(f.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Fatalf("accepted %s remove=%v: %v", key, remove, err)
					}
				}
			}
			for _, suffix := range []string{`,"future":{"array":[{"x":1,"x":2}]}}`, `,"future":{"x":1,"\u0078":2}}`, ",\"future\":\"\xff\"}", `} {}`} {
				bad := append(bytes.Clone(wire[:len(wire)-1]), suffix...)
				if _, err := decodeF3_90(f.typ, bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted malformed extension: %v", err)
				}
			}
			extended := append(bytes.Clone(wire[:len(wire)-1]), `,"future":{"array":[null,{"new":true}]}}`...)
			p, err = decodeF3_90(f.typ, extended)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("extension changed canonical meaning: %v", err)
			}
			for _, raw := range [][]byte{[]byte(`[]`), []byte(`null`), wire[:len(wire)-1]} {
				if _, err := decodeF3_90(f.typ, raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted incomplete/nonobject: %v", err)
				}
			}
			for _, size := range []int{MaxEligibilityPayloadBytes, MaxEligibilityPayloadBytes + 1} {
				raw := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, size-len(wire))...)
				_, err := decodeF3_90(f.typ, raw)
				if size == MaxEligibilityPayloadBytes && err != nil || size > MaxEligibilityPayloadBytes && !errors.Is(err, ErrInvalid) {
					t.Fatalf("complete input bound %d: %v", size, err)
				}
			}
		})
	}
}

func TestEligibilityCanonicalSubmission(t *testing.T) {
	t.Parallel()
	keys := [][]string{{"reason"}, {"reason", "action_id", "turn"}, {"action_id", "turn"}, {}, {"action_id", "turn_id", "start_command_id", "stage", "code"}}
	for i, s := range eligibilitySubmissionFixtures() {
		t.Run(s.Operation, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(s)
			want := []string{"schema", "actor", "origin", "operation", "expected_version"}
			if s.ExpectedOperationID != (uuid.UUID{}) {
				want = append(want, "expected_operation_id")
			}
			want = append(want, "arguments")
			if !slices.Equal(loopMemberOrder(t, wire), want) {
				t.Fatalf("schema-8 preimage order: %s", wire)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(loopMemberOrder(t, fields["arguments"]), keys[i]) {
				t.Fatalf("argument order: %s", fields["arguments"])
			}
			reordered, err := json.MarshalIndent(fields, "", " ")
			if err != nil {
				t.Fatal(err)
			}
			p, err := DecodeEligibilitySubmission(reordered)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("canonical preimage changed: %v", err)
			}
			r := eligibilityReceipt(s)
			raw := mustEncode(r)
			if !slices.Equal(loopMemberOrder(t, raw), []string{"command_id", "submission_sha256", "submission", "outcome", "effect_sequence"}) {
				t.Fatal("receipt order")
			}
			extended := registrationReplace(t, raw, `"origin":{`, `"origin":{"future":{"x":[null,true]},`)
			got, err := DecodeRuntimeEligibilityReceipt(extended)
			if err != nil || !bytes.Equal(mustEncode(got), raw) {
				t.Fatalf("embedded extension entered digest: %v", err)
			}
			for _, key := range []string{"reason", "action_id", "turn", "turn_id", "start_command_id", "stage", "code"} {
				if slices.Contains(keys[i], key) {
					continue
				}
				bad := registrationReplace(t, wire, `"arguments":{`, `"arguments":{"`+key+`":null,`)
				if len(keys[i]) == 0 {
					bad = bytes.Replace(bad, []byte(`null,}`), []byte(`null}`), 1)
				}
				if !json.Valid(bad) {
					t.Fatal("cross-operation vector is not valid JSON")
				}
				if _, err := DecodeEligibilitySubmission(bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("cross-operation member %s accepted: %s", key, bad)
				}
			}
			for _, actor := range []string{"agent", "future_actor", map[string]string{"owner": "core", "core": "owner"}[s.Actor]} {
				bad := s
				bad.Actor = actor
				eligibilityRejectBoth(t, "schema-8", bad)
			}
			bad := s
			bad.Arguments = map[string]any{}
			if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("arbitrary arguments encoded")
			}
			bad = s
			bad.Arguments = &EligibilityReleaseArguments{}
			if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("non-concrete argument encoded")
			}
			r.Submission.Origin.ContextID = eligibilityID(99)
			eligibilityRejectBoth(t, TypeRuntimeEligibilityReceipt, r)
		})
	}
	// A new operation can follow an earlier release; positive version alone
	// does not imply that expected_operation_id belongs in the request.
	for _, index := range []int{0, 1} {
		s := eligibilitySubmissionFixtures()[index]
		s.ExpectedVersion, s.ExpectedOperationID = 8, uuid.UUID{}
		if _, err := DecodeEligibilitySubmission(mustEncode(s)); err != nil {
			t.Fatal(err)
		}
		s.ExpectedOperationID = eligibilityID(7)
		if _, err := DecodeEligibilitySubmission(mustEncode(s)); err != nil {
			t.Fatal(err)
		}
		s.ExpectedVersion = 0
		eligibilityRejectBoth(t, "schema-8", s)
	}
	for _, index := range []int{2, 3, 4} {
		s := eligibilitySubmissionFixtures()[index]
		s.ExpectedOperationID = uuid.UUID{}
		eligibilityRejectBoth(t, "schema-8", s)
	}
}

func eligibilityRejectBoth(t testing.TB, typ string, p payload) {
	t.Helper()
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid %s encoded: %v", typ, err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeF3_90(typ, raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid %s decoded: %s (%v)", typ, raw, err)
	}
}

func TestEligibilitySnapshotRules(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		edit func(*RuntimeEligibilityChanged)
	}{
		{"unknown_state", func(p *RuntimeEligibilityChanged) { p.State = "future_state" }},
		{"missing_first", func(p *RuntimeEligibilityChanged) { p.FirstAction = nil }},
		{"held_none", func(p *RuntimeEligibilityChanged) { p.Reason = "none" }},
		{"eligible_reason", func(p *RuntimeEligibilityChanged) { p.ExpectedVersion, p.Version, p.State = 1, 2, "eligible" }},
		{"initial_eligible", func(p *RuntimeEligibilityChanged) { p.State, p.Reason = "eligible", "none" }},
		{"initial_ordinary_declaration", func(p *RuntimeEligibilityChanged) { p.State = "ordinary_work_ineligible" }},
		{"negative_version", func(p *RuntimeEligibilityChanged) { p.ExpectedVersion, p.Version = -1, 0 }},
		{"version_jump", func(p *RuntimeEligibilityChanged) { p.Version = 2 }},
		{"overflow", func(p *RuntimeEligibilityChanged) { p.ExpectedVersion, p.Version = math.MaxInt64, math.MinInt64 }},
		{"zero_operation", func(p *RuntimeEligibilityChanged) { p.OperationID = uuid.UUID{} }},
		{"zero_action", func(p *RuntimeEligibilityChanged) { p.FirstAction.ActionID = uuid.UUID{} }},
		{"changed_context", func(p *RuntimeEligibilityChanged) { p.FirstAction.PermittedTurn.ContextID = eligibilityID(99) }},
		{"changed_binding", func(p *RuntimeEligibilityChanged) { p.FirstAction.PermittedTurn.TaskBinding = nil }},
		{"changed_input_same_turn", func(p *RuntimeEligibilityChanged) { p.FirstAction.PermittedTurn.InputSHA256 = scopedHash("changed") }},
		{"reused_commands_next_turn", func(p *RuntimeEligibilityChanged) { p.FirstAction.PermittedTurn.TurnID = eligibilityID(99) }},
		{"same_start_admission", func(p *RuntimeEligibilityChanged) {
			p.FirstAction.InitialTurn.StartCommandID = p.FirstAction.InitialTurn.AdmissionCommandID
		}},
		{"wrong_refusal_turn", func(p *RuntimeEligibilityChanged) {
			p.FirstAction.Refusal = &EligibilityRefusal{eligibilityID(99), p.FirstAction.PermittedTurn.StartCommandID, "turn_admission", "denied"}
		}},
		{"paid_stage", func(p *RuntimeEligibilityChanged) {
			p.FirstAction.Refusal = &EligibilityRefusal{p.FirstAction.PermittedTurn.TurnID, p.FirstAction.PermittedTurn.StartCommandID, "paid_admission", "denied"}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := eligibilityFixture()
			row.edit(&p)
			eligibilityRejectBoth(t, TypeRuntimeEligibilityChanged, p)
		})
	}
	p := eligibilityFixture()
	p.ExpectedVersion, p.Version = math.MaxInt64-1, math.MaxInt64
	p.Reason = "future_reason"
	p.FirstAction.InitialTurn.TaskBinding.Generation = math.MaxInt64
	p.FirstAction.Refusal = &EligibilityRefusal{p.FirstAction.PermittedTurn.TurnID, p.FirstAction.PermittedTurn.StartCommandID, "turn_admission", "future_refusal"}
	for _, state := range []string{"first_action_required", "ordinary_work_ineligible", "eligible"} {
		p.State = state
		if state == "eligible" {
			p.Reason = "none"
		}
		got, err := DecodeRuntimeEligibilityChanged(mustEncode(p))
		if err != nil || !reflect.DeepEqual(got, p) {
			t.Fatalf("retained history/open tokens: %+v %v", got, err)
		}
	}
	for _, f := range eligibilityCodecs()[:2] {
		if !Defined().Contains(TypeVersion{f.typ, 1}) || Defined().Contains(TypeVersion{f.typ, 2}) {
			t.Fatalf("catalogue version %s", f.typ)
		}
	}
}

func TestEligibilityNestedShapeAndOrder(t *testing.T) {
	t.Parallel()
	p := eligibilityFixture()
	p.ExpectedVersion, p.Version = 1, 2
	p.FirstAction.Refusal = &EligibilityRefusal{p.FirstAction.PermittedTurn.TurnID, p.FirstAction.PermittedTurn.StartCommandID, "turn_admission", "denied"}
	wire := mustEncode(p)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loopMemberOrder(t, wire), []string{"expected_version", "version", "state", "reason", "operation_id", "first_action"}) {
		t.Fatal("snapshot order")
	}
	first := fields["first_action"]
	if !slices.Equal(loopMemberOrder(t, first), []string{"action_id", "initial_turn", "permitted_turn", "refusal"}) {
		t.Fatal("declaration order")
	}
	if err := json.Unmarshal(first, &fields); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loopMemberOrder(t, fields["refusal"]), []string{"turn_id", "start_command_id", "stage", "code"}) {
		t.Fatal("refusal order")
	}
	turn := fields["initial_turn"]
	if !slices.Equal(loopMemberOrder(t, turn), []string{"context_id", "turn_id", "start_command_id", "admission_command_id", "input_sha256", "task_binding"}) {
		t.Fatal("turn order")
	}
	if err := json.Unmarshal(turn, &fields); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loopMemberOrder(t, fields["task_binding"]), []string{"task_id", "assignment_id", "generation"}) {
		t.Fatal("task order")
	}
	// Replace each nested known member, including omission-only optionals, with
	// null; required member omission and duplicate spellings must also refuse.
	for _, key := range []string{"action_id", "initial_turn", "permitted_turn", "refusal", "context_id", "turn_id", "start_command_id", "admission_command_id", "input_sha256", "task_binding", "task_id", "assignment_id", "generation", "stage", "code"} {
		for _, mode := range []string{"null", "missing", "duplicate"} {
			var tree map[string]any
			if err := json.Unmarshal(wire, &tree); err != nil {
				t.Fatal(err)
			}
			var change func(map[string]any) bool
			change = func(m map[string]any) bool {
				if _, ok := m[key]; ok {
					if mode == "missing" {
						delete(m, key)
					} else {
						m[key] = nil
					}
					return true
				}
				for _, v := range m {
					if child, ok := v.(map[string]any); ok && change(child) {
						return true
					}
				}
				return false
			}
			if !change(tree) {
				t.Fatal("missing vector member", key)
			}
			bad, err := json.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "duplicate" {
				bad = registrationReplace(t, wire, `"`+key+`":`, `"`+key+`":null,"`+key+`":`)
			}
			_, err = DecodeRuntimeEligibilityChanged(bad)
			optional := key == "refusal"
			// Removing one task_binding still contradicts the other turn's binding.
			if mode == "missing" && optional {
				if err != nil {
					t.Fatalf("optional omission: %s %v", key, err)
				}
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted nested %s %s: %v", key, mode, err)
			}
		}
	}
	p.FirstAction.Refusal = nil
	p.FirstAction.InitialTurn.TaskBinding, p.FirstAction.PermittedTurn.TaskBinding = nil, nil
	wire = mustEncode(p)
	if bytes.Contains(wire, []byte(`"task_binding"`)) || bytes.Contains(wire, []byte(`"refusal"`)) {
		t.Fatal("absent optional encoded")
	}
	if _, err := DecodeRuntimeEligibilityChanged(wire); err != nil {
		t.Fatal(err)
	}
}

func TestEligibilityNumbersTokensAndBounds(t *testing.T) {
	t.Parallel()
	wire := mustEncode(eligibilityFixture())
	for _, row := range []struct{ old, replacement string }{
		{`"expected_version":0`, `"expected_version":-0`},
		{`"version":1`, `"version":1e0`},
		{`"version":1`, `"version":1.0`},
		{`"version":1`, `"version":9223372036854775808`},
		{`"generation":1`, `"generation":0`},
		{`"generation":1`, `"generation":9223372036854775808`},
		{eligibilityID(7).String(), "019a9000-0000-4000-8000-000000000007"},
		{eligibilityID(7).String(), strings.ToUpper(eligibilityID(7).String())},
		{scopedHash("Synthetic first input.").String(), strings.Repeat("0", 64)},
		{scopedHash("Synthetic first input.").String(), strings.ToUpper(scopedHash("Synthetic first input.").String())},
	} {
		if _, err := DecodeRuntimeEligibilityChanged(registrationReplace(t, wire, row.old, row.replacement)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted numeric/identity vector %s: %v", row.replacement, err)
		}
	}
	for _, token := range []string{"", "Upper", "has-dash", "a.b", "é", strings.Repeat("a", 65)} {
		p := eligibilityFixture()
		p.Reason = token
		eligibilityRejectBoth(t, TypeRuntimeEligibilityChanged, p)
		s := eligibilitySubmissionFixtures()[4]
		a := s.Arguments.(EligibilityFirstRefusedArguments)
		a.Code = token
		s.Arguments = a
		eligibilityRejectBoth(t, "schema-8", s)
		r := eligibilityReceipt(eligibilitySubmissionFixtures()[0])
		r.Outcome = token
		eligibilityRejectBoth(t, TypeRuntimeEligibilityReceipt, r)
	}
	p := eligibilityFixture()
	p.Reason = strings.Repeat("a", 64)
	if _, err := DecodeRuntimeEligibilityChanged(mustEncode(p)); err != nil {
		t.Fatal(err)
	}
	r := eligibilityReceipt(eligibilitySubmissionFixtures()[4])
	r.Outcome, r.EffectSequence = "future_outcome", math.MaxInt64
	if got, err := DecodeRuntimeEligibilityReceipt(mustEncode(r)); err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("open outcome/MaxInt64 lost: %v", err)
	}
	// Independently bound the receipt even when its extended submission fits.
	s := eligibilitySubmissionFixtures()[0]
	js := mustEncode(s)
	extra := `,"future":"` + strings.Repeat("x", MaxEligibilityPayloadBytes-len(js)-12) + `"}`
	extended := append(bytes.Clone(js[:len(js)-1]), extra...)
	if len(extended) != MaxEligibilityPayloadBytes || func() bool { _, err := DecodeEligibilitySubmission(extended); return err != nil }() {
		t.Fatal("submission at whole-input cap refused")
	}
	receipt := bytes.Replace(mustEncode(eligibilityReceipt(s)), js, extended, 1)
	if _, err := DecodeRuntimeEligibilityReceipt(receipt); !errors.Is(err, ErrInvalid) {
		t.Fatal("containing receipt escaped independent bound")
	}
	for _, row := range []struct{ old, replacement string }{
		{`"schema":8`, `"schema":7`},
		{`"operation":"eligibility.hold"`, `"operation":"eligibility.future"`},
		{`"expected_version":0`, `"expected_version":9223372036854775807`},
		{`"expected_version":0`, `"expected_version":-1`},
		{`"arguments":{`, `"expected_operation_id":null,"arguments":{`},
		{`"reason":"explicit_hold"`, `"reason":"none"`},
	} {
		if _, err := DecodeEligibilitySubmission(registrationReplace(t, js, row.old, row.replacement)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted closed submission vector %s: %v", row.replacement, err)
		}
	}
	s.ExpectedVersion = math.MaxInt64 - 1
	if got, err := DecodeEligibilitySubmission(mustEncode(s)); err != nil || got.ExpectedVersion != s.ExpectedVersion {
		t.Fatalf("last incrementable expectation refused: %v", err)
	}
	for _, edit := range []func(*RuntimeEligibilityReceipt){
		func(r *RuntimeEligibilityReceipt) { r.CommandID = uuid.UUID{} },
		func(r *RuntimeEligibilityReceipt) { r.SubmissionSHA256 = Digest{} },
		func(r *RuntimeEligibilityReceipt) { r.EffectSequence = 0 },
	} {
		r := eligibilityReceipt(s)
		edit(&r)
		eligibilityRejectBoth(t, TypeRuntimeEligibilityReceipt, r)
	}
}

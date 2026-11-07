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

func continuationID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("019a1600-0000-7000-8000-%012x", n))
}

func continuationFixture() RuntimeContinuationChanged {
	return RuntimeContinuationChanged{TurnID: continuationID(1), BoundSequence: 12, Version: 1, ExpectedState: "absent", State: "ready", OperationID: continuationID(2), Generation: 1, WaitReason: "none", DeadlineUTCNS: 123456789, Pending: []ContinuationPending{}, NoticeCursors: []ContinuationNoticeCursor{}}
}

func continuationSubmissionFixture() ContinuationSubmission {
	p := continuationFixture()
	return ContinuationSubmission{7, "core", scopedOriginFixture(), p.TurnID, "owner.initialize", p.ExpectedVersion, p.ExpectedState, p.arguments()}
}

func continuationReceipt(s ContinuationSubmission) RuntimeContinuationReceipt {
	return RuntimeContinuationReceipt{continuationID(3), Digest(sha256.Sum256(mustEncode(s))), s, "recorded", 15}
}

func continuationCodecs() []struct {
	typ string
	p   payload
} {
	s := continuationSubmissionFixture()
	return []struct {
		typ string
		p   payload
	}{
		{TypeRuntimeContinuationChanged, continuationFixture()},
		{TypeRuntimeContinuationReceipt, continuationReceipt(s)},
		{"schema-7", s},
	}
}

func decodeF3_16(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeContinuationChanged:
		return DecodeRuntimeContinuationChanged(js)
	case TypeRuntimeContinuationReceipt:
		return DecodeRuntimeContinuationReceipt(js)
	case "schema-7":
		return DecodeContinuationSubmission(js)
	default:
		return nil, fmt.Errorf("unknown continuation codec %s", typ)
	}
}

func TestContinuationPayloads(t *testing.T) {
	t.Parallel()
	for _, f := range continuationCodecs() {
		t.Run(f.typ, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(f.p)
			p, err := decodeF3_16(f.typ, wire)
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
					if _, err := decodeF3_16(f.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Fatalf("accepted %s remove=%v: %v", key, remove, err)
					}
				}
			}
			for _, suffix := range []string{`,"future":{"array":[{"x":1,"x":2}]}}`, `,"future":{"x":1,"\u0078":2}}`, ",\"future\":\"\xff\"}", `} {}`} {
				bad := append(bytes.Clone(wire[:len(wire)-1]), suffix...)
				if _, err := decodeF3_16(f.typ, bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted malformed extension: %v", err)
				}
			}
			extended := append(bytes.Clone(wire[:len(wire)-1]), `,"future":{"array":[null,{"new":true}]}}`...)
			p, err = decodeF3_16(f.typ, extended)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("extension changed canonical meaning: %v", err)
			}
			for _, raw := range [][]byte{[]byte(`[]`), []byte(`null`), wire[:len(wire)-1]} {
				if _, err := decodeF3_16(f.typ, raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted incomplete/nonobject: %v", err)
				}
			}
			for _, size := range []int{MaxContinuationPayloadBytes, MaxContinuationPayloadBytes + 1} {
				raw := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, size-len(wire))...)
				_, err := decodeF3_16(f.typ, raw)
				if size == MaxContinuationPayloadBytes && err != nil || size > MaxContinuationPayloadBytes && !errors.Is(err, ErrInvalid) {
					t.Fatalf("complete input bound %d: %v", size, err)
				}
			}
		})
	}
}

func continuationWaitFixture() RuntimeContinuationChanged {
	p := continuationFixture()
	p.ExpectedVersion, p.Version, p.ExpectedState = 2, 3, "model_pending"
	p.State, p.ResumeState, p.WaitReason, p.Generation = "waiting_resource", "model_pending", "paid_prepare", 2
	p.Pending = []ContinuationPending{{"paid_attempt", continuationID(4), continuationID(5), 1, 18}, {"tool", continuationID(6), uuid.UUID{}, 0, 0}}
	p.NoticeCursors = []ContinuationNoticeCursor{{continuationID(7), continuationID(8), 0}, {continuationID(7), continuationID(9), 11}}
	return p
}

func TestContinuationConditionalMembers(t *testing.T) {
	t.Parallel()
	wait := continuationWaitFixture()
	for _, row := range []struct{ name, old, replacement string }{
		{"version_gap", `"version":3`, `"version":4`},
		{"version_overflow", `"expected_version":2`, `"expected_version":9223372036854775807`},
		{"expectation_absent", `"expected_state":"model_pending"`, `"expected_state":"absent"`},
		{"terminal_reopen", `"expected_state":"model_pending"`, `"expected_state":"terminal"`},
		{"zero_generation", `"generation":2`, `"generation":0`},
		{"number_exponent", `"generation":2`, `"generation":2e0`},
		{"number_fraction", `"generation":2`, `"generation":2.0`},
		{"number_string", `"generation":2`, `"generation":"2"`},
		{"number_overflow", `"generation":2`, `"generation":9223372036854775808`},
		{"number_signed_zero", `"model_calls":0`, `"model_calls":-0`},
		{"negative_deadline", `"deadline_utc_ns":123456789`, `"deadline_utc_ns":-1`},
		{"zero_bound", `"bound_sequence":12`, `"bound_sequence":0`},
		{"wait_missing_resume", `,"resume_state":"model_pending"`, ``},
		{"wait_null_resume", `"resume_state":"model_pending"`, `"resume_state":null`},
		{"wait_empty_resume", `"resume_state":"model_pending"`, `"resume_state":""`},
		{"wait_wrong_resume", `"resume_state":"model_pending"`, `"resume_state":"ready"`},
		{"wait_no_reason", `"wait_reason":"paid_prepare"`, `"wait_reason":"none"`},
		{"nonwait_resume", `"state":"waiting_resource"`, `"state":"ready"`},
		{"null_stop", `"model_calls":0`, `"model_calls":0,"stop_reason":null`},
		{"natural_stop", `"model_calls":0`, `"model_calls":0,"stop_reason":"natural_stop"`},
		{"missing_cancel", `"model_calls":0`, `"model_calls":0,"stop_reason":"cancelled"`},
		{"extra_cancel", `"model_calls":0`, `"model_calls":0,"cancel_command_id":"` + continuationID(10).String() + `"`},
		{"zero_source", `"source_sequence":18`, `"source_sequence":0`},
		{"null_source", `"source_sequence":18`, `"source_sequence":null`},
		{"null_command", `"command_id":"` + continuationID(5).String() + `"`, `"command_id":null`},
		{"native_missing_command", `,"command_id":"` + continuationID(5).String() + `"`, ``},
		{"wrong_uuid_version", continuationID(4).String(), "019a1600-0000-4000-8000-000000000004"},
		{"uppercase_uuid", continuationID(4).String(), "019A1600-0000-7000-8000-000000000004"},
		{"zero_uuid", continuationID(4).String(), "00000000-0000-0000-0000-000000000000"},
		{"pending_null_element", `"pending":[`, `"pending":[null,`},
		{"cursor_null_element", `"notice_cursors":[`, `"notice_cursors":[null,`},
		{"cursor_negative", `"after_sequence":0`, `"after_sequence":-1`},
		{"nested_duplicate", `"after_sequence":0`, `"after_sequence":0,"after_sequence":1`},
		{"nested_missing", `"after_sequence":0`, `"future_sequence":0`},
		{"null_final", `"model_calls":0`, `"model_calls":0,"final_message_object":null`},
		{"zero_ending", `"model_calls":0`, `"model_calls":0,"native_ending_sequence":0`},
		{"null_ending", `"model_calls":0`, `"model_calls":0,"native_ending_sequence":null`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			bad := registrationReplace(t, mustEncode(wait), row.old, row.replacement)
			if _, err := DecodeRuntimeContinuationChanged(bad); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid member: %v", err)
			}
		})
	}
	for _, mutate := range []func(*RuntimeContinuationChanged){
		func(p *RuntimeContinuationChanged) { p.Pending = nil },
		func(p *RuntimeContinuationChanged) { p.NoticeCursors = nil },
		func(p *RuntimeContinuationChanged) { p.Pending = []ContinuationPending{p.Pending[0], p.Pending[0]} },
		func(p *RuntimeContinuationChanged) { p.Pending = []ContinuationPending{p.Pending[1], p.Pending[0]} },
		func(p *RuntimeContinuationChanged) {
			p.NoticeCursors = []ContinuationNoticeCursor{p.NoticeCursors[0], p.NoticeCursors[0]}
		},
		func(p *RuntimeContinuationChanged) {
			p.NoticeCursors = []ContinuationNoticeCursor{p.NoticeCursors[1], p.NoticeCursors[0]}
		},
		func(p *RuntimeContinuationChanged) { p.State, p.ResumeState = "terminal", "" },
		func(p *RuntimeContinuationChanged) { p.State, p.ResumeState = "stopping", "" },
	} {
		p := wait
		mutate(&p)
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid output accepted: %+v", p)
		}
		wire, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRuntimeContinuationChanged(wire); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid input accepted: %s", wire)
		}
	}
}

func TestContinuationCanonicalSubmission(t *testing.T) {
	t.Parallel()
	base := continuationSubmissionFixture()
	for _, op := range []string{"owner.initialize", "owner.advance", "owner.stop", "owner.reconcile", "owner.close"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			s := base
			s.Operation = op
			if op != "owner.initialize" {
				s.ExpectedVersion, s.ExpectedState = 1, "ready"
			}
			switch op {
			case "owner.advance":
				s.Arguments = continuationWaitFixture().arguments()
				s.ExpectedState = "model_pending"
			case "owner.stop":
				s.Actor = "owner"
				s.Arguments.State, s.Arguments.StopReason, s.Arguments.CancelCommandID = "stopping", "cancelled", continuationID(10)
			case "owner.reconcile":
				s.Arguments.State = "reconciling"
			case "owner.close":
				s.ExpectedState = "reconciling"
				s.Arguments.State, s.Arguments.NativeEndingSequence = "terminal", 20
			}
			wire := mustEncode(s)
			if keys := loopMemberOrder(t, wire); !slices.Equal(keys, []string{"schema", "actor", "origin", "turn_id", "operation", "expected_version", "expected_state", "arguments"}) {
				t.Fatalf("schema-7 preimage keys: %v", keys)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			argumentKeys := []string{"bound_sequence", "state"}
			if s.Arguments.ResumeState != "" {
				argumentKeys = append(argumentKeys, "resume_state")
			}
			argumentKeys = append(argumentKeys, "operation_id", "generation", "wait_reason", "model_calls", "tool_dispatches", "deadline_utc_ns", "pending")
			if s.Arguments.StopReason != "" {
				argumentKeys = append(argumentKeys, "stop_reason")
			}
			if s.Arguments.CancelCommandID != (uuid.UUID{}) {
				argumentKeys = append(argumentKeys, "cancel_command_id")
			}
			if s.Arguments.NativeEndingSequence != 0 {
				argumentKeys = append(argumentKeys, "native_ending_sequence")
			}
			argumentKeys = append(argumentKeys, "notice_cursors")
			if !slices.Equal(loopMemberOrder(t, fields["arguments"]), argumentKeys) {
				t.Fatal("argument preimage order")
			}
			reordered, err := json.MarshalIndent(fields, "", " ")
			if err != nil {
				t.Fatal(err)
			}
			p, err := DecodeContinuationSubmission(reordered)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("canonical preimage changed: %v", err)
			}
			r := continuationReceipt(s)
			if _, err := DecodeRuntimeContinuationReceipt(mustEncode(r)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(loopMemberOrder(t, mustEncode(r)), []string{"command_id", "submission_sha256", "submission", "outcome", "effect_sequence"}) {
				t.Fatal("receipt key order")
			}
			extended := registrationReplace(t, mustEncode(r), `"arguments":{`, `"arguments":{"future":{"x":[null,true]},`)
			decoded, err := DecodeRuntimeContinuationReceipt(extended)
			if err != nil || !bytes.Equal(mustEncode(decoded), mustEncode(r)) {
				t.Fatalf("nested extension entered digest: %v", err)
			}
			future := s
			future.Arguments.State, future.Arguments.WaitReason = "future_state", "future_reason"
			if op != "owner.initialize" {
				future.ExpectedState = "future_previous"
			}
			retained, err := DecodeContinuationSubmission(mustEncode(future))
			if err != nil || !reflect.DeepEqual(retained, future) {
				t.Fatalf("unknown phase lost in %s: %v", op, err)
			}
			r.Submission.Origin.ContextID = continuationID(11)
			if _, err := r.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("receipt accepted changed meaning")
			}
			bad, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRuntimeContinuationReceipt(bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("decoded mismatched receipt")
			}
			for _, actor := range []string{"agent", "future_actor"} {
				s.Actor = actor
				if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
					t.Fatal("open actor")
				}
			}
		})
	}
	for _, row := range []struct{ old, replacement string }{
		{`"schema":7`, `"schema":6`}, {`"operation":"owner.initialize"`, `"operation":"owner.future"`},
		{`"actor":"core"`, `"actor":"owner"`}, {`"expected_version":0`, `"expected_version":1`},
		{`"generation":1`, `"generation":2`}, {`"state":"ready"`, `"state":"finishing"`},
		{`"arguments":{`, `"arguments":{"version":null,`}, {`"arguments":{`, `"arguments":{"expected_state":"absent",`},
	} {
		bad := registrationReplace(t, mustEncode(base), row.old, row.replacement)
		if _, err := DecodeContinuationSubmission(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("closed submission accepted: %s", bad)
		}
	}
	for _, f := range continuationCodecs()[:2] {
		if !Defined().Contains(TypeVersion{f.typ, 1}) || Defined().Contains(TypeVersion{f.typ, 2}) {
			t.Fatalf("catalogue version %s", f.typ)
		}
	}
}

func TestContinuationOpenTokensAndOutputBounds(t *testing.T) {
	t.Parallel()
	p := continuationWaitFixture()
	p.ExpectedVersion, p.Version = math.MaxInt64-1, math.MaxInt64
	p.Generation, p.ModelCalls, p.ToolDispatches, p.DeadlineUTCNS = math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64
	p.State, p.ExpectedState, p.ResumeState, p.WaitReason = "future_state", "future_state", "future_resume", "future_wait"
	p.Pending = []ContinuationPending{{Kind: "future_kind", ID: continuationID(4), ExpectedVersion: math.MaxInt64}}
	got, err := DecodeRuntimeContinuationChanged(mustEncode(p))
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("unknown state lost: %+v %v", got, err)
	}
	for _, token := range []string{"", "Upper", "has-dash", "a.b", "é", strings.Repeat("a", 65)} {
		q := p
		q.State = token
		if _, err := q.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid token %q", token)
		}
	}
	p.State = strings.Repeat("a", 64)
	if _, err := DecodeRuntimeContinuationChanged(mustEncode(p)); err != nil {
		t.Fatal(err)
	}
	// The decoder does not impose the runtime's 128-pending policy ceiling.
	p = continuationFixture()
	for i := range 129 {
		p.Pending = append(p.Pending, ContinuationPending{Kind: "tool", ID: continuationID(100 + i)})
	}
	if _, err := DecodeRuntimeContinuationChanged(mustEncode(p)); err != nil {
		t.Fatal(err)
	}
	// Find a valid submission whose containing receipt, but not itself, is too big.
	s := continuationSubmissionFixture()
	found := false
	for i := range 1000 {
		s.Arguments.Pending = append(s.Arguments.Pending, ContinuationPending{Kind: "tool", ID: continuationID(1000 + i)})
		wire, err := s.Encode()
		if err != nil {
			break
		}
		r := RuntimeContinuationReceipt{continuationID(3), Digest(sha256.Sum256(wire)), s, "recorded", 15}
		if _, err := r.Encode(); errors.Is(err, ErrInvalid) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("receipt failed to enforce whole-payload bound independently")
	}
	s.Arguments.Pending = append(s.Arguments.Pending, s.Arguments.Pending...)
	// Distinct sorted identities keep this an output-size case, not a duplicate case.
	for i := range s.Arguments.Pending {
		s.Arguments.Pending[i].ID = continuationID(1000 + i)
	}
	if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized submission encoded")
	}
	p.Pending = s.Arguments.Pending
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized snapshot encoded")
	}
}

func TestContinuationOrderedOptionalMembers(t *testing.T) {
	t.Parallel()
	p := continuationWaitFixture()
	p.ResumeState, p.WaitReason, p.StopReason, p.CancelCommandID = "reconciling", "child_quiescence", "cancelled", continuationID(10)
	p.NativeEndingSequence = 25
	wire := mustEncode(p)
	want := []string{"turn_id", "bound_sequence", "expected_version", "version", "expected_state", "state", "resume_state", "operation_id", "generation", "wait_reason", "model_calls", "tool_dispatches", "deadline_utc_ns", "pending", "stop_reason", "cancel_command_id", "native_ending_sequence", "notice_cursors"}
	if !slices.Equal(loopMemberOrder(t, wire), want) {
		t.Fatalf("snapshot key order: %s", wire)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	var pending, cursors []json.RawMessage
	if err := json.Unmarshal(fields["pending"], &pending); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fields["notice_cursors"], &cursors); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loopMemberOrder(t, pending[0]), []string{"kind", "id", "command_id", "expected_version", "source_sequence"}) || !slices.Equal(loopMemberOrder(t, pending[1]), []string{"kind", "id", "expected_version"}) {
		t.Fatal("pending optional member order/omission")
	}
	if !slices.Equal(loopMemberOrder(t, cursors[0]), []string{"workspace_id", "scratch_id", "after_sequence"}) {
		t.Fatal("cursor order")
	}
	decoded, err := DecodeRuntimeContinuationChanged(wire)
	if err != nil || !reflect.DeepEqual(decoded, p) {
		t.Fatalf("optional roundtrip: %v", err)
	}
	p.State, p.ResumeState, p.WaitReason, p.StopReason, p.CancelCommandID = "terminal", "", "none", "", uuid.UUID{}
	p.Pending = []ContinuationPending{}
	p.FinalMessageObject = &ObjectRef{scopedHash("synthetic final"), 15}
	wire = mustEncode(p)
	keys := loopMemberOrder(t, wire)
	i := slices.Index(keys, "final_message_object")
	if i < 0 || keys[i+1] != "native_ending_sequence" {
		t.Fatal("final reference order")
	}
	if _, err := DecodeRuntimeContinuationChanged(wire); err != nil {
		t.Fatal(err)
	}
	p.StopReason = "shutdown"
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("stopped final selection")
	}
}

func TestContinuationArrayKeys(t *testing.T) {
	t.Parallel()
	p := continuationFixture()
	// Equal kind/ID prefixes compare the optional command last; absent is first.
	p.Pending = []ContinuationPending{
		{Kind: "tool", ID: continuationID(20)},
		{Kind: "tool", ID: continuationID(20), CommandID: continuationID(21)},
		{Kind: "tool", ID: continuationID(20), CommandID: continuationID(22)},
		{Kind: "tool", ID: continuationID(23)},
	}
	p.NoticeCursors = []ContinuationNoticeCursor{
		{continuationID(30), continuationID(31), 1},
		{continuationID(30), continuationID(32), 2},
		{continuationID(33), continuationID(31), 3},
	}
	decoded, err := DecodeRuntimeContinuationChanged(mustEncode(p))
	if err != nil || !reflect.DeepEqual(decoded, p) {
		t.Fatalf("lexicographic keys: %v", err)
	}
	p.Pending[1], p.Pending[2] = p.Pending[2], p.Pending[1]
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("command tie-break ignored")
	}
	p.Pending[1], p.Pending[2] = p.Pending[2], p.Pending[1]
	p.NoticeCursors[1], p.NoticeCursors[2] = p.NoticeCursors[2], p.NoticeCursors[1]
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("workspace tie-break ignored")
	}
}

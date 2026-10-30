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

func loopID(n int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("019a1500-0000-7000-8000-%012x", n)) }

func decodeF3_15(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypePrivateTurnRevision:
		return DecodePrivateTurnRevision(js)
	case TypeRuntimeTurnBound:
		return DecodeRuntimeTurnBound(js)
	case TypeRuntimeToolStarted:
		return DecodeRuntimeToolStarted(js)
	case TypeRuntimeToolResult:
		return DecodeRuntimeToolResult(js)
	case TypeRuntimeTurnResult:
		return DecodeRuntimeTurnResult(js)
	case TypeRuntimeLoopReceipt:
		return DecodeRuntimeLoopReceipt(js)
	case "schema-6":
		return DecodeLoopSubmission(js)
	default:
		return nil, fmt.Errorf("unknown loop codec %s", typ)
	}
}

func loopSubmissionFixture() LoopSubmission {
	o := ObjectRef{scopedHash("synthetic loop object"), 21}
	return LoopSubmission{6, "owner", scopedOriginFixture(), loopID(1), "loop.bind", 0, LoopBindArguments{loopID(1), loopID(2), o, o, o}}
}

func loopStartedFixture() RuntimeToolStarted {
	o := ObjectRef{scopedHash("synthetic invocation"), 20}
	return RuntimeToolStarted{ToolID: loopID(3), ParentTurnID: loopID(1), AttemptID: loopID(4), ProviderCallID: "synthetic-call", ToolName: "core_status_v1", ToolSchema: 1, RequestObject: o, RequestSHA256: o.SHA256, Operation: "turn.inspect", GrantID: loopID(5)}
}

func loopCodecFixtures() []struct {
	typ string
	p   payload
} {
	s := loopSubmissionFixture()
	a := s.Arguments.(LoopBindArguments)
	return []struct {
		typ string
		p   payload
	}{
		{TypePrivateTurnRevision, PrivateTurnRevision{Scope: "context", ExpectedVersion: 1, Revision: 2, Changes: []PrivateChange{{Slot: "transcript", Mode: "append", SHA256: a.InputObject.SHA256, Bytes: a.InputObject.Bytes}}}},
		{TypeRuntimeTurnBound, RuntimeTurnBound{a.TurnID, 12, a.InputObject, a.PolicyObject, a.RenderObject}},
		{TypeRuntimeToolStarted, loopStartedFixture()},
		{TypeRuntimeToolResult, RuntimeToolResult{ToolID: loopID(3), StartedSequence: 15, Outcome: "ok", ResultObject: a.InputObject}},
		{TypeRuntimeTurnResult, RuntimeTurnResult{TurnID: a.TurnID, BoundSequence: 14, Outcome: "finished", Reason: "natural_stop", ModelCalls: 2, ToolDispatches: 1, FinalMessageObject: &a.InputObject, EndingSequence: 24}},
		{TypeRuntimeLoopReceipt, RuntimeLoopReceipt{loopID(6), Digest(sha256.Sum256(mustEncode(s))), s, "applied", 14}},
		{"schema-6", s},
	}
}

func TestF3_15Payloads(t *testing.T) {
	t.Parallel()
	for _, f := range loopCodecFixtures() {
		t.Run(f.typ, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(f.p)
			p, err := decodeF3_15(f.typ, wire)
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
					if _, err := decodeF3_15(f.typ, bad); !errors.Is(err, ErrInvalid) {
						t.Fatalf("known field %s remove=%v accepted: %v", key, remove, err)
					}
				}
			}
			for _, suffix := range []string{`,"future":{"array":[{"x":1,"x":2}]}}`, `,"future":{"x":1,"\u0078":2}}`, ",\"future\":\"\xff\"}", `} {}`} {
				bad := append(bytes.Clone(wire[:len(wire)-1]), suffix...)
				if _, err := decodeF3_15(f.typ, bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("malformed input accepted: %v", err)
				}
			}
			extended := append(bytes.Clone(wire[:len(wire)-1]), `,"future":{"array":[null,{"new":true}]}}`...)
			p, err = decodeF3_15(f.typ, extended)
			if err != nil || !bytes.Equal(mustEncode(p), wire) {
				t.Fatalf("optional extension changed known encoding: %v", err)
			}
			for _, raw := range [][]byte{[]byte(`[]`), []byte(`null`), wire[:len(wire)-1]} {
				if _, err := decodeF3_15(f.typ, raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("incomplete/nonobject accepted: %v", err)
				}
			}
			for _, size := range []int{MaxLoopPayloadBytes, MaxLoopPayloadBytes + 1} {
				raw := append(bytes.Clone(wire), bytes.Repeat([]byte{' '}, size-len(wire))...)
				_, err := decodeF3_15(f.typ, raw)
				if size == MaxLoopPayloadBytes && err != nil || size > MaxLoopPayloadBytes && !errors.Is(err, ErrInvalid) {
					t.Fatalf("complete input cap %d: %v", size, err)
				}
			}
		})
	}
}

func TestLoopConditionalMembers(t *testing.T) {
	t.Parallel()
	f := loopCodecFixtures()
	for _, row := range []struct {
		name             string
		i                int
		old, replacement string
	}{
		{"scope", 0, `"scope":"context"`, `"scope":"agent"`},
		{"initialization", 0, `"expected_version":1,"revision":2`, `"expected_version":0,"revision":1`},
		{"revision_gap", 0, `"revision":2`, `"revision":3`},
		{"revision_overflow", 0, `"expected_version":1`, `"expected_version":9223372036854775807`},
		{"nontranscript", 0, `"slot":"transcript"`, `"slot":"memory"`},
		{"replace", 0, `"mode":"append"`, `"mode":"replace"`},
		{"empty_transcript", 0, `"bytes":21`, `"bytes":0`},
		{"empty_changes", 0, `"changes":[`, `"changes":[null,`},
		{"predecessor_null", 0, `"mode":"append"`, `"mode":"append","previous_sha256":null`},
		{"nested_missing_bytes", 1, `"bytes":21`, `"other":21`},
		{"nested_null", 1, `"input_object":{`, `"input_object":null,"other":{`},
		{"nested_duplicate", 1, `"bytes":21`, `"bytes":21,"bytes":21`},
		{"sequence_zero", 1, `"admission_sequence":12`, `"admission_sequence":0`},
		{"sequence_exponent", 1, `"admission_sequence":12`, `"admission_sequence":12e0`},
		{"sequence_string", 1, `"admission_sequence":12`, `"admission_sequence":"12"`},
		{"sequence_overflow", 1, `"admission_sequence":12`, `"admission_sequence":9223372036854775808`},
		{"zero_schema", 2, `"tool_schema":1`, `"tool_schema":0`},
		{"dotted_tool_name", 2, `"tool_name":"core_status_v1"`, `"tool_name":"core.status"`},
		{"uppercase_operation", 2, `"operation":"turn.inspect"`, `"operation":"Turn.inspect"`},
		{"null_grant", 2, `"grant_id":"` + loopID(5).String() + `"`, `"grant_id":null`},
		{"zero_grant", 2, `"grant_id":"` + loopID(5).String() + `"`, `"grant_id":"00000000-0000-0000-0000-000000000000"`},
		{"empty_call", 2, `"provider_call_id":"synthetic-call"`, `"provider_call_id":""`},
		{"missing_grant", 2, `,"grant_id":"` + loopID(5).String() + `"`, ``},
		{"null_target", 2, `"tool_schema":1`, `"tool_schema":1,"target":null`},
		{"null_effect", 2, `"tool_schema":1`, `"tool_schema":1,"effect_command_id":null`},
		{"grant_and_effect", 2, `"tool_schema":1`, `"tool_schema":1,"effect_command_id":"` + loopID(7).String() + `"`},
		{"invalid_has_grant", 2, `"operation":"turn.inspect"`, `"operation":"invalid_argument"`},
		{"request_hash_mismatch", 2, `"request_sha256":"` + loopStartedFixture().RequestSHA256.String() + `"`, `"request_sha256":"` + scopedHash("other").String() + `"`},
		{"call_id_space", 2, `"provider_call_id":"synthetic-call"`, `"provider_call_id":"has space"`},
		{"optional_sequence_zero", 3, `"outcome":"ok"`, `"outcome":"ok","effect_receipt_sequence":0`},
		{"optional_sequence_null", 3, `"outcome":"ok"`, `"outcome":"ok","effect_receipt_sequence":null`},
		{"failed_final", 4, `"outcome":"finished"`, `"outcome":"failed"`},
		{"negative_count", 4, `"model_calls":2`, `"model_calls":-1`},
		{"closed_actor", 6, `"actor":"owner"`, `"actor":"agent"`},
		{"closed_schema", 6, `"schema":6`, `"schema":5`},
		{"closed_operation", 6, `"operation":"loop.bind"`, `"operation":"loop.future"`},
		{"wrong_expectation", 6, `"expected_version":0`, `"expected_version":1`},
		{"different_turn", 6, `"arguments":{"turn_id":"` + loopID(1).String() + `"`, `"arguments":{"turn_id":"` + loopID(8).String() + `"`},
		{"wrong_arguments", 6, `"arguments":{`, `"arguments":{"ending_command_id":null,`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			bad := registrationReplace(t, mustEncode(f[row.i].p), row.old, row.replacement)
			if _, err := decodeF3_15(f[row.i].typ, bad); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid conditional member accepted: %v", err)
			}
		})
	}
	start := loopStartedFixture()
	for _, operation := range []string{"invalid_argument", "unrecognized"} {
		start.Operation, start.GrantID = operation, uuid.UUID{}
		if _, err := DecodeRuntimeToolStarted(mustEncode(start)); err != nil {
			t.Fatal(err)
		}
		start.Target = &Resource{Kind: "task", TaskID: loopID(9)}
		if _, err := start.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid invocation disclosed target")
		}
		start.Target = nil
	}
	start = loopStartedFixture()
	start.GrantID, start.EffectCommandID = uuid.UUID{}, loopID(7)
	start.Operation = "file.read"
	start.Target = &Resource{Kind: "workspace", WorkspaceID: loopID(9), Binding: Binding{start.ParentTurnID, loopID(8)}, PathPrefix: "src"}
	if _, err := DecodeRuntimeToolStarted(mustEncode(start)); err != nil {
		t.Fatal(err)
	}
	result := f[3].p.(RuntimeToolResult)
	result.EffectReceiptSequence = 18
	if _, err := DecodeRuntimeToolResult(mustEncode(result)); err != nil {
		t.Fatal(err)
	}
	end := f[4].p.(RuntimeTurnResult)
	for _, outcome := range []string{"cancelled", "failed", "interrupted", "future_outcome"} {
		end.Outcome, end.FinalMessageObject = outcome, nil
		end.Reason, end.ModelCalls, end.ToolDispatches = "request_too_large", 0, 0
		if _, err := DecodeRuntimeTurnResult(mustEncode(end)); err != nil {
			t.Fatal(err)
		}
	}
	end.Outcome = "finished"
	if _, err := end.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("finished without final message")
	}
}

func TestLoopOpenTokensAndBoundaries(t *testing.T) {
	t.Parallel()
	start := loopStartedFixture()
	start.Operation, start.ToolSchema, start.ProviderCallID = "future.operation", math.MaxInt64, strings.Repeat("x", 256)
	if _, err := DecodeRuntimeToolStarted(mustEncode(start)); err != nil {
		t.Fatal(err)
	}
	start.ProviderCallID += "x"
	if _, err := start.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized call ID")
	}
	for _, name := range []string{"A0_-", strings.Repeat("n", 64)} {
		p := loopStartedFixture()
		p.ToolName = name
		if _, err := DecodeRuntimeToolStarted(mustEncode(p)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"", "file.read", "not valid", "é", strings.Repeat("n", 65)} {
		p := loopStartedFixture()
		p.ToolName = name
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid tool name %q", name)
		}
	}
	for _, operation := range []string{"File.read", "file..read", "file-read", strings.Repeat("a", 65)} {
		p := loopStartedFixture()
		p.Operation = operation
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid operation %q", operation)
		}
	}
	for _, operation := range []string{"tools.discover", "task.list", strings.Repeat("a", 64)} {
		p := loopStartedFixture()
		p.Operation = operation
		if _, err := DecodeRuntimeToolStarted(mustEncode(p)); err != nil {
			t.Fatal(err)
		}
		if operation != strings.Repeat("a", 64) {
			p.Target = &Resource{Kind: "task", TaskID: loopID(9)}
			if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("catalogue query target accepted")
			}
		}
	}
	result := loopCodecFixtures()[3].p.(RuntimeToolResult)
	result.Outcome = strings.Repeat("a", 64)
	if _, err := DecodeRuntimeToolResult(mustEncode(result)); err != nil {
		t.Fatal(err)
	}
	result.Outcome += "a"
	if _, err := result.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized token")
	}
	receipt := loopCodecFixtures()[5].p.(RuntimeLoopReceipt)
	receipt.Outcome = "future_outcome"
	if _, err := DecodeRuntimeLoopReceipt(mustEncode(receipt)); err != nil {
		t.Fatal(err)
	}
	receipt.SubmissionSHA256 = scopedHash("changed")
	if _, err := receipt.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("receipt digest mismatch")
	}
}

func loopMemberOrder(t *testing.T, wire []byte) []string {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(wire))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("not an object: %v", err)
	}
	var keys []string
	for d.More() {
		key, err := d.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestLoopPayloadKeyOrder(t *testing.T) {
	t.Parallel()
	fixtures := loopCodecFixtures()
	orders := []string{
		"scope expected_version revision changes",
		"turn_id admission_sequence input_object policy_object render_object",
		"tool_id parent_turn_id attempt_id provider_call_id tool_name tool_schema request_object request_sha256 operation grant_id",
		"tool_id started_sequence outcome result_object",
		"turn_id bound_sequence outcome reason model_calls tool_dispatches final_message_object ending_sequence",
		"command_id submission_sha256 submission outcome effect_sequence",
		"schema actor origin turn_id operation expected_version arguments",
	}
	for i, f := range fixtures {
		if got := loopMemberOrder(t, mustEncode(f.p)); !reflect.DeepEqual(got, strings.Fields(orders[i])) {
			t.Fatalf("%s order %v", f.typ, got)
		}
	}
	for _, row := range []struct {
		operation, actor, order string
		expected                int64
		args                    any
	}{
		{"loop.bind", "owner", "turn_id admission_command_id input_object policy_object render_object", 0, loopSubmissionFixture().Arguments},
		{"tool.begin", "agent", orders[2], 0, LoopToolBeginArguments(loopStartedFixture())},
		{"tool.result", "core", orders[3], 1, LoopToolResultArguments(fixtures[3].p.(RuntimeToolResult))},
		{"loop.end", "core", "turn_id bound_sequence outcome reason model_calls tool_dispatches final_message_object ending_command_id", 1, LoopEndArguments{loopID(1), 14, "finished", "natural_stop", 2, 1, fixtures[4].p.(RuntimeTurnResult).FinalMessageObject, loopID(8)}},
	} {
		s := loopSubmissionFixture()
		s.Operation, s.Actor, s.ExpectedVersion, s.Arguments = row.operation, row.actor, row.expected, row.args
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(mustEncode(s), &fields); err != nil {
			t.Fatal(err)
		}
		if got := loopMemberOrder(t, fields["arguments"]); !reflect.DeepEqual(got, strings.Fields(row.order)) {
			t.Fatalf("%s argument order %v", row.operation, got)
		}
	}
}

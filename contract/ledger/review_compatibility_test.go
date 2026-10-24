package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestF3_12Compatibility(t *testing.T) {
	t.Parallel()
	t.Run("unknown_direct_and_outer_escape_identically", func(t *testing.T) {
		t.Parallel()
		subject := SubjectRef{Kind: "future_subject", Members: json.RawMessage("{ \"z\": {\"text\": \"<>&\u2028\u2029\"}, \"large\": 18446744073709551615, \"decimal\": 1.50, \"absent\": null }")}
		want := `{"kind":"future_subject","z":{"text":"\u003c\u003e\u0026\u2028\u2029"},"large":18446744073709551615,"decimal":1.50,"absent":null}`
		direct, err := subject.MarshalJSON()
		if err != nil || string(direct) != want {
			t.Fatalf("direct encoding = %s, %v; want %s", direct, err, want)
		}
		outer, err := json.Marshal(subject)
		if err != nil || !bytes.Equal(direct, outer) {
			t.Fatalf("outer encoding diverges: %s, %v", outer, err)
		}
		request := reviewRequestFixture()
		request.Binding.Subject = subject
		wire := mustEncode(request)
		decoded, err := DecodeRuntimeReviewRequested(wire)
		if err != nil {
			t.Fatal(err)
		}
		again, err := decoded.Binding.Subject.MarshalJSON()
		if err != nil || string(again) != want {
			t.Fatalf("decode changed unfamiliar member identity: %s, %v", again, err)
		}
	})
	t.Run("artifact_frozen_bytes", func(t *testing.T) {
		t.Parallel()
		subject := SubjectRef{Kind: "artifact", Artifact: ArtifactRef{4, scopedHash("artifact"), 8}}
		want := `{"kind":"artifact","artifact":{"source_sequence":4,"sha256":"` + scopedHash("artifact").String() + `","bytes":8}}`
		got, err := subject.MarshalJSON()
		if err != nil || string(got) != want {
			t.Fatalf("historical artifact bytes changed: %s, %v", got, err)
		}
	})
	t.Run("private_occurrence_decodes_through_schema_two", func(t *testing.T) {
		t.Parallel()
		ref := PrivateRevisionRef{Binding: Binding{fid("a1"), fid("d1")}, Scope: "context", Slot: "transcript", Revision: 2, SourceSequence: 9, SHA256: scopedHash("chunk"), Bytes: 5}
		subject := &SubjectRef{Kind: "private_revision", PrivateRevision: &ref}
		sub := ScopedSubmission{Schema: 2, Actor: "owner", Origin: scopedOriginFixture(), Operation: "task.fail", Target: Resource{Kind: "task", TaskID: fid("21")}, Expected: ScopedExpected{Version: 1}, Arguments: TaskMutationArguments{TaskID: fid("21"), Action: "fail", FailureCause: "future_cause", FailureEvidence: subject}}
		wire, err := sub.Encode()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeScopedSubmission(wire)
		if err != nil || !reflect.DeepEqual(decoded, sub) {
			t.Fatalf("new known occurrence lost in old codec: %+v, %v", decoded, err)
		}
		for _, row := range []struct{ name, old, replacement string }{
			{"scope_closed", `"scope":"context"`, `"scope":"future"`},
			{"slot_closed", `"slot":"transcript"`, `"slot":"future"`},
			{"scope_slot_combination", `"scope":"context"`, `"scope":"agent"`},
			{"revision_positive", `"revision":2`, `"revision":0`},
			{"sequence_positive", `"source_sequence":9`, `"source_sequence":0`},
			{"revision_overflow", `"revision":2`, `"revision":9223372036854775808`},
			{"binding_null", `"binding":{`, `"binding":null,"ignored":{`},
			{"typed_artifact_forbidden", `"private_revision":{`, `"artifact":null,"private_revision":{`},
		} {
			t.Run(row.name, func(t *testing.T) {
				t.Parallel()
				bad := strings.Replace(string(wire), row.old, row.replacement, 1)
				if _, err := DecodeScopedSubmission([]byte(bad)); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid private occurrence accepted: %v", err)
				}
			})
		}
	})
	t.Run("private_shapes_and_typed_exclusion", func(t *testing.T) {
		t.Parallel()
		ref := PrivateRevisionRef{Binding: Binding{fid("a1"), fid("d1")}, Scope: "context", Slot: "memory", Revision: 1, SourceSequence: 9, SHA256: scopedHash(""), Bytes: 0}
		// A map key is a compile-time guarantee that occurrence identity remains
		// comparable, in addition to the wire roundtrip below.
		occurrences := map[PrivateRevisionRef]bool{ref: true}
		p := reviewRequestFixture()
		p.Binding.Subject = SubjectRef{Kind: "private_revision", PrivateRevision: &ref}
		wire := mustEncode(p)
		decoded, err := DecodeRuntimeReviewRequested(wire)
		if err != nil || !occurrences[*decoded.Binding.Subject.PrivateRevision] {
			t.Fatalf("private occurrence changed: %#v %v", decoded, err)
		}
		for _, slot := range []struct{ scope, slot string }{
			{"agent", "instructions"}, {"agent", "memory"}, {"context", "continuation"},
			{"context", "handoff"}, {"context", "memory"}, {"context", "transcript"},
		} {
			r := ref
			r.Scope, r.Slot = slot.scope, slot.slot
			p.Binding.Subject.PrivateRevision = &r
			if _, err := DecodeRuntimeReviewRequested(mustEncode(p)); err != nil {
				t.Fatalf("valid scope/slot refused: %v", err)
			}
		}
		for _, key := range []string{"binding", "scope", "slot", "revision", "source_sequence", "sha256", "bytes"} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(mustJSON(ref), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, key)
			assertReviewInvalid(t, TypeRuntimeReviewRequested, string(bytes.Replace(wire, mustJSON(ref), mustJSON(fields), 1)))
		}
		for _, subject := range []SubjectRef{
			{Kind: "private_revision"},
			{Kind: "private_revision", PrivateRevision: &ref, Members: json.RawMessage(`{}`)},
			{Kind: "private_revision", PrivateRevision: &ref, Artifact: reviewBindingFixture().Subject.Artifact},
			{Kind: "artifact", Artifact: reviewBindingFixture().Subject.Artifact, PrivateRevision: &ref},
			{Kind: "future_subject", PrivateRevision: &ref},
			{Kind: "future_subject", Members: json.RawMessage(`{"kind":"another"}`)},
			{Kind: "future_subject", Members: json.RawMessage(`{"nested":{"x":1,"x":2}}`)},
			{Kind: "future_subject", Members: json.RawMessage(`[]`)},
		} {
			p.Binding.Subject = subject
			if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid subject encoded: %#v %v", subject, err)
			}
		}
	})
}

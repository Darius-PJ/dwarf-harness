package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"uuid"
)

// These digests are shared with agents' S1 vectors, independently hashed in
// Python. The artifact fixture's hash is SHA-256("artifact"), not zero.
func TestF3_11CanonicalBridge(t *testing.T) {
	t.Parallel()
	id := func(n int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("019a0700-0000-7000-8000-%012x", n)) }
	origin := Origin{id(1), id(2), id(3)}
	for _, tc := range []struct {
		name, digest string
		s            ScopedSubmission
	}{
		{"task_complete", "41a99254652ee2b64d3ebca47501647dd9b6012b93b6c7ef52033f1c312829b3", ScopedSubmission{Schema: 2, Actor: "agent", Origin: origin, Operation: "task.complete", Target: Resource{Kind: "task", TaskID: id(50)}, GrantID: id(10), Expected: ScopedExpected{Version: 9, AssignmentID: id(51), Generation: new(int64(2))}, Arguments: TaskMutationArguments{TaskID: id(50), Action: "complete", Submission: &SubjectRef{Kind: "artifact", Artifact: ArtifactRef{4, scopedHash("artifact"), 8}}}}},
		{"owner_revoke", "17f804c5c7f7d656f74c2277cf627183f6780af9aec596330027ffde34692da2", ScopedSubmission{Schema: 2, Actor: "owner", Origin: origin, Operation: "grant.revoke", Target: Resource{Kind: "task"}, Expected: ScopedExpected{Version: 1, AuthorityVersion: new(int64(7))}, Arguments: GrantRevokeArguments{id(10)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, err := tc.s.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if digest := scopedHash(string(js)).String(); digest != tc.digest {
				t.Fatalf("canonical digest %s; want %s: %s", digest, tc.digest, js)
			}
			r := scopedReceipt(tc.s, 10, "applied", uuid.UUID{})
			wire := mustEncode(r)
			got, err := DecodeRuntimeScopedReceipt(wire)
			if err != nil {
				t.Fatal(err)
			}
			again, err := got.Submission.Encode()
			if err != nil || !bytes.Equal(again, js) || got.SubmissionSHA256.String() != tc.digest {
				t.Fatalf("receipt lost S1 submission: %s, %v", again, err)
			}
			// Unknown optional members do not alter canonical meaning or its hash.
			extended := strings.Replace(string(wire), `"expected":{`, `"expected":{"future":{"x":null},`, 1)
			decoded, err := DecodeRuntimeScopedReceipt([]byte(extended))
			if err != nil || !reflect.DeepEqual(decoded, got) {
				t.Fatalf("optional extension: %#v %v", decoded, err)
			}
			bad := strings.Replace(string(wire), `"version":`+fmt.Sprint(tc.s.Expected.Version), `"version":8`, 1)
			assertScopedInvalid(t, TypeRuntimeScopedReceipt, bad)
		})
	}
}

type scopedArgumentFixture struct {
	name, operation string
	expected        ScopedExpected
	target          Resource
	arguments       any
	keys            string
}

func scopedArgumentFixtures() []scopedArgumentFixture {
	g := scopedGrantFixture()
	subject := &SubjectRef{Kind: "artifact", Artifact: ArtifactRef{1, goldenB1, 1}}
	task := fid("21")
	assignment := &AssignmentInput{fid("22"), g.Grantee}
	workspace := Resource{Kind: "workspace", WorkspaceID: fid("51"), Binding: g.Grantee, PathPrefix: "a/b"}
	return []scopedArgumentFixture{
		{"issue", "grant.issue", ScopedExpected{AuthorityVersion: new(int64(0))}, Resource{Kind: "task"}, scopedIssueArguments(g), "grant_id grantee operations resources data_class recipients limits delegation_depth lifetime"},
		{"revoke", "grant.revoke", ScopedExpected{Version: 1, AuthorityVersion: new(int64(1))}, Resource{Kind: "task"}, GrantRevokeArguments{g.GrantID}, "grant_id"},
		{"configure", "capacity.configure", ScopedExpected{}, scopedContext(), CapacityArguments{ScopeKey{Kind: "colony"}, Capacity{}}, "scope limits"},
		{"create", "task.create", ScopedExpected{}, Resource{Kind: "task", TaskID: task}, TaskCreateArguments{task, ObjectRef{goldenB1, 1}, goldenSubmission, []uuid.UUID{}, []uuid.UUID{fid("a1")}, assignment}, "task_id description acceptance_sha256 dependencies audience assignment"},
		{"reassign", "task.reassign", ScopedExpected{Version: 1, AssignmentID: fid("23"), Generation: new(int64(1))}, Resource{Kind: "task", TaskID: task}, TaskMutationArguments{TaskID: task, Action: "reassign", NewAssignment: assignment}, "task_id action new_assignment"},
		{"wait", "task.wait", ScopedExpected{Version: 1}, Resource{Kind: "task", TaskID: task}, TaskMutationArguments{TaskID: task, Action: "wait", WaitReason: "input", WaitArtifact: subject}, "task_id action wait_reason wait_artifact"},
		{"fail", "task.fail", ScopedExpected{Version: 1}, Resource{Kind: "task", TaskID: task}, TaskMutationArguments{TaskID: task, Action: "fail", FailureCause: "future_cause", FailureEvidence: subject}, "task_id action failure_cause failure_evidence"},
		{"turn", "turn.start", ScopedExpected{}, scopedContext(), TurnStartArguments{fid("41"), &TaskBinding{task, fid("22"), 1}}, "turn_id task_binding"},
		{"admit", "local.admit", ScopedExpected{}, workspace, LocalAdmissionArguments{fid("42"), fid("41"), "file.read", goldenSubmission, 1, 1024, &TaskBinding{task, fid("22"), 1}}, "work_id parent_turn_id local_operation arguments_sha256 tool_schema max_bytes task_binding"},
		{"dispatch", "local.begin", ScopedExpected{Version: 1}, workspace, WorkDispatchArguments{fid("42"), goldenSubmission}, "work_id arguments_sha256"},
		{"end", "local.end", ScopedExpected{Version: 2}, workspace, WorkEndArguments{fid("42"), "future_outcome"}, "work_id outcome"},
		{"write", "private.write", ScopedExpected{Version: 1}, scopedContext(), PrivateWriteArguments{[]PrivateChange{{Slot: "memory", Mode: "replace", SHA256: goldenB1, Bytes: 1}}}, "changes"},
		{"read", "private.read", ScopedExpected{}, scopedContext(), PrivateReadArguments{PrivateReadRef{"context", "memory", 1, 0, 1}}, "read"},
		{"artifact", "artifact.read", ScopedExpected{}, Resource{Kind: "artifact", Artifact: subject.Artifact}, ArtifactReadArguments{subject.Artifact, 1}, "artifact max_bytes"},
	}
}

func TestF3_11CanonicalArguments(t *testing.T) {
	t.Parallel()
	for _, tc := range scopedArgumentFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := ScopedSubmission{2, "agent", scopedOriginFixture(), tc.operation, tc.target, fid("11"), tc.expected, tc.arguments}
			wire := mustEncode(s)
			got, err := DecodeScopedSubmission(wire)
			if err != nil || !reflect.DeepEqual(s, got) {
				t.Fatalf("argument roundtrip %#v %v", got, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			d := json.NewDecoder(bytes.NewReader(fields["arguments"]))
			if _, err := d.Token(); err != nil {
				t.Fatal(err)
			}
			for _, want := range strings.Fields(tc.keys) {
				key, err := d.Token()
				if err != nil || key != want {
					t.Fatalf("key=%v %v; want %s", key, err, want)
				}
				var value json.RawMessage
				if err := d.Decode(&value); err != nil {
					t.Fatal(err)
				}
			}
			if d.More() {
				t.Fatal("extra argument member")
			}
			// Null is not omission, including optional nested objects/fields.
			var args map[string]json.RawMessage
			if err := json.Unmarshal(fields["arguments"], &args); err != nil {
				t.Fatal(err)
			}
			for key := range args {
				mutated := strings.Replace(string(wire), `"`+key+`":`+string(args[key]), `"`+key+`":null`, 1)
				if _, err := DecodeScopedSubmission([]byte(mutated)); !errors.Is(err, ErrInvalid) {
					t.Fatalf("null %s accepted", key)
				}
			}
		})
	}
}

func TestF3_11UnknownSubjects(t *testing.T) {
	t.Parallel()
	task := scopedTaskFixture()
	task.Action, task.State, task.ExpectedVersion, task.Version = "fail", "failed", 1, 2
	task.Assignment = &Assignment{fid("22"), 1, Binding{fid("a1"), fid("d1")}, "owner", scopedOriginFixture()}
	task.FailureCause = "future_cause"
	task.FailureEvidence = &SubjectRef{Kind: "future_subject", Members: json.RawMessage(`{"z":null,"a":1.5,"huge":18446744073709551615,"text":"\u003c\u003e\u0026","artifact":null}`)}
	wire := mustEncode(task)
	wire = bytes.ReplaceAll(wire, []byte(`\u003c\u003e\u0026`), []byte("<>&"))
	first, err := DecodeRuntimeTaskChanged(wire)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := first.Encode()
	if err != nil {
		t.Fatal(err)
	}
	second, err := DecodeRuntimeTaskChanged(canonical)
	if err != nil || !reflect.DeepEqual(first, second) || !bytes.Equal(second.FailureEvidence.Members, task.FailureEvidence.Members) {
		t.Fatalf("unknown members changed: %#v, %v", second.FailureEvidence, err)
	}
	for _, bad := range []string{
		strings.Replace(string(wire), `"kind":"future_subject"`, `"kind":"bad.kind"`, 1),
		strings.Replace(string(wire), `"z":null`, `"z":{"x":1,"x":2}`, 1),
		strings.Replace(string(wire), `"kind":"future_subject"`, `"kind":"artifact"`, 1),
	} {
		assertScopedInvalid(t, TypeRuntimeTaskChanged, bad)
	}
}

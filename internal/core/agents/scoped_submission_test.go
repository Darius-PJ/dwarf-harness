package agents

import (
	"encoding/json"
	"strings"
	"testing"
	"uuid"
)

func TestScopedCanonicalSubmission(t *testing.T) {
	t.Parallel()
	artifact := taskArtifact()
	digest, err := hashObject([]byte("artifact"))
	if err != nil {
		t.Fatal(err)
	}
	artifact.Artifact.SHA256 = digest
	generation := int64(2)
	authority := uint64(7)
	origin := testSubmissionIDs(t)
	for _, row := range []struct {
		name      string
		envelope  scopedEnvelope
		arguments any
		json      string
	}{
		{"task_complete", scopedEnvelope{actor: "agent", origin: origin, operation: "task.complete", target: grantResource{kind: "task", task: privateID(50)}, grant: privateID(10), expected: scopedExpected{Version: 9, AssignmentID: privateID(51), Generation: &generation}},
			taskMutationArguments{TaskID: privateID(50), Action: "complete", Submission: artifact},
			`{"schema":2,"actor":"agent","origin":{"agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003"},"operation":"task.complete","target":{"kind":"task","task_id":"019a0700-0000-7000-8000-000000000032"},"grant_id":"019a0700-0000-7000-8000-00000000000a","expected":{"version":9,"assignment_id":"019a0700-0000-7000-8000-000000000033","generation":2},"arguments":{"task_id":"019a0700-0000-7000-8000-000000000032","action":"complete","submission":{"kind":"artifact","artifact":{"source_sequence":4,"sha256":"c7c5c1d70c5dec4416ab6158afd0b223ef40c29b1dc1f97ed9428b94d4cadb1c","bytes":8}}}}`},
		{"owner_revoke", scopedEnvelope{actor: "owner", origin: origin, operation: "grant.revoke", target: grantResource{kind: "task"}, expected: scopedExpected{Version: 1, AuthorityVersion: &authority}},
			grantRevokeArguments{GrantID: privateID(10)},
			`{"schema":2,"actor":"owner","origin":{"agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003"},"operation":"grant.revoke","target":{"kind":"task"},"expected":{"version":1,"authority_version":7},"arguments":{"grant_id":"019a0700-0000-7000-8000-00000000000a"}}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			data, digest, err := encodeScopedSubmission(row.envelope, row.arguments)
			if err != nil || string(data) != row.json {
				t.Fatalf("canonical=%s, %v; want %s", data, err, row.json)
			}
			// The literal vectors below are independently SHA-256 hashed, not an
			// expectation recomputed through the producer's canonical encoder.
			want := scopedDigestVectors[row.name]
			if digest.String() != want {
				t.Fatalf("digest=%s, want %s", digest, want)
			}
		})
	}
}

var scopedDigestVectors = map[string]string{
	"task_complete": "41a99254652ee2b64d3ebca47501647dd9b6012b93b6c7ef52033f1c312829b3",
	"owner_revoke":  "17f804c5c7f7d656f74c2277cf627183f6780af9aec596330027ffde34692da2",
}

func TestScopedCanonicalArgumentOrder(t *testing.T) {
	t.Parallel()
	id := privateID(1)
	binding := scopedBinding{privateID(1), privateID(2)}
	for _, row := range []struct {
		name      string
		arguments any
		keys      []string
	}{
		{"issue", grantIssueArguments{GrantID: id, Grantee: binding, Operations: []string{"task.create"}, Resources: []grantResource{{kind: "task"}}, DataClass: "task", Recipients: []scopedBinding{binding}, Lifetime: "until_revoked"}, []string{"grant_id", "grantee", "operations", "resources", "data_class", "recipients", "limits", "delegation_depth", "lifetime"}},
		{"configure", capacityArguments{Scope: budgetScope{Kind: "colony"}}, []string{"scope", "limits"}},
		{"create", taskCreateArguments{TaskID: id, Description: objectIdentity{Bytes: 1}, Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{id}, Assignment: &taskAssignmentInput{AssignmentID: id, Assignee: binding}}, []string{"task_id", "description", "acceptance_sha256", "dependencies", "audience", "assignment"}},
		{"reassign", taskMutationArguments{TaskID: id, Action: "reassign", NewAssignment: &taskAssignmentInput{AssignmentID: id, Assignee: binding}}, []string{"task_id", "action", "new_assignment"}},
		{"wait", taskMutationArguments{TaskID: id, Action: "wait", WaitReason: "input", WaitArtifact: taskArtifact()}, []string{"task_id", "action", "wait_reason", "wait_artifact"}},
		{"fail", taskMutationArguments{TaskID: id, Action: "fail", FailureCause: "failed", FailureEvidence: taskArtifact()}, []string{"task_id", "action", "failure_cause", "failure_evidence"}},
		{"turn", turnStartArguments{TurnID: id, TaskBinding: &taskRestriction{id, id, 1}}, []string{"turn_id", "task_binding"}},
		{"admit", localAdmissionArguments{WorkID: id, ParentTurnID: id, LocalOperation: "file.read", ToolSchema: 1, MaxBytes: 2, TaskBinding: &taskRestriction{id, id, 1}}, []string{"work_id", "parent_turn_id", "local_operation", "arguments_sha256", "tool_schema", "max_bytes", "task_binding"}},
		{"dispatch", workDispatchArguments{WorkID: id}, []string{"work_id", "arguments_sha256"}},
		{"end", workEndArguments{WorkID: id, Outcome: "unknown"}, []string{"work_id", "outcome"}},
		{"private_write", writeArguments{Changes: []canonicalChange{{Slot: "continuation", Mode: "clear"}}}, []string{"changes"}},
		{"private_read", privateReadArguments{Read: privateReadSelection{Scope: "context", Slot: "memory", Revision: 1, MaxBytes: 4}}, []string{"read"}},
		{"artifact_read", artifactReadArguments{Artifact: taskArtifact().Artifact, MaxBytes: 8}, []string{"artifact", "max_bytes"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(row.arguments)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			if _, err := decoder.Token(); err != nil {
				t.Fatal(err)
			}
			for _, key := range row.keys {
				got, err := decoder.Token()
				if err != nil || got != key {
					t.Fatalf("ordered key=%v, %v; want %s: %s", got, err, key, data)
				}
				var value json.RawMessage
				if err := decoder.Decode(&value); err != nil {
					t.Fatal(err)
				}
			}
			if decoder.More() {
				t.Fatalf("extra canonical key: %s", data)
			}
		})
	}
}

func TestScopedCanonicalExpectedRefusals(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*scopedEnvelope)
	}{
		{"actor", func(e *scopedEnvelope) { e.actor = "model" }},
		{"origin", func(e *scopedEnvelope) { e.origin.context[6] = 0 }},
		{"unrelated_authority", func(e *scopedEnvelope) { value := uint64(1); e.expected.AuthorityVersion = &value }},
		{"partial_assignment", func(e *scopedEnvelope) { value := int64(1); e.expected.Generation = &value }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			e := scopedEnvelope{actor: "agent", origin: testSubmissionIDs(t), operation: "private.read", target: grantResource{kind: "context", binding: Binding{privateID(1), privateID(3)}, scope: "context", slots: []string{"memory"}}, grant: privateID(10)}
			row.change(&e)
			if _, _, err := encodeScopedSubmission(e, privateReadArguments{}); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
}

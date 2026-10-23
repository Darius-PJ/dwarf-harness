package agents

import (
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
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
	authority := int64(7)
	ids := testSubmissionIDs(t)
	origin := ledger.Origin{AgentID: ids.agent, SessionID: ids.session, ContextID: ids.context}
	for _, row := range []struct {
		name      string
		envelope  ledger.ScopedSubmission
		arguments any
		json      string
	}{
		{"task_complete", ledger.ScopedSubmission{Schema: 2, Actor: "agent", Origin: origin, Operation: "task.complete", Target: ledger.Resource{Kind: "task", TaskID: privateID(50)}, GrantID: privateID(10), Expected: ledger.ScopedExpected{Version: 9, AssignmentID: privateID(51), Generation: &generation}},
			ledger.TaskMutationArguments{TaskID: privateID(50), Action: "complete", Submission: &artifact},
			`{"schema":2,"actor":"agent","origin":{"agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003"},"operation":"task.complete","target":{"kind":"task","task_id":"019a0700-0000-7000-8000-000000000032"},"grant_id":"019a0700-0000-7000-8000-00000000000a","expected":{"version":9,"assignment_id":"019a0700-0000-7000-8000-000000000033","generation":2},"arguments":{"task_id":"019a0700-0000-7000-8000-000000000032","action":"complete","submission":{"kind":"artifact","artifact":{"source_sequence":4,"sha256":"c7c5c1d70c5dec4416ab6158afd0b223ef40c29b1dc1f97ed9428b94d4cadb1c","bytes":8}}}}`},
		{"owner_revoke", ledger.ScopedSubmission{Schema: 2, Actor: "owner", Origin: origin, Operation: "grant.revoke", Target: ledger.Resource{Kind: "task"}, Expected: ledger.ScopedExpected{Version: 1, AuthorityVersion: &authority}},
			ledger.GrantRevokeArguments{GrantID: privateID(10)},
			`{"schema":2,"actor":"owner","origin":{"agent_id":"019a0700-0000-7000-8000-000000000001","session_id":"019a0700-0000-7000-8000-000000000002","context_id":"019a0700-0000-7000-8000-000000000003"},"operation":"grant.revoke","target":{"kind":"task"},"expected":{"version":1,"authority_version":7},"arguments":{"grant_id":"019a0700-0000-7000-8000-00000000000a"}}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			row.envelope.Arguments = row.arguments
			data, digest, err := encodeScopedSubmission(row.envelope)
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

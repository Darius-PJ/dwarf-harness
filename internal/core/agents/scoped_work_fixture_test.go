package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// S3a has no work producers. These are real committed contract fixtures, not
// substitutes for the S3b API: their effects must pass the production reducer.
func scopedTurnFixture(t *testing.T, command, turn uuid.UUID, sub ledger.ScopedSubmission, effectType string, effect json.RawMessage, version int64, outcome string) []ledger.Draft {
	t.Helper()
	digest := ledger.Digest(sha256.Sum256(testPayload(t, sub)))
	payloads := []json.RawMessage{
		testPayload(t, ledger.Command{CommandID: command, SubmissionSHA256: digest}),
		effect,
		testPayload(t, ledger.RuntimeScopedReceipt{CommandID: command, SubmissionSHA256: digest, Submission: sub, Outcome: outcome, Version: version, WorkID: turn}),
	}
	types := []string{ledger.TypeCommand, effectType, ledger.TypeRuntimeScopedReceipt}
	drafts := make([]ledger.Draft, len(types))
	for i, typ := range types {
		actor := ledger.Actor(sub.Actor)
		if i == 1 {
			actor = ledger.ActorCore
		}
		drafts[i] = ledger.Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, OperationID: command, TurnID: turn, Payload: payloads[i]}
	}
	return drafts
}

func appendScopedFixture(t *testing.T, f *serviceFixture, drafts []ledger.Draft) {
	t.Helper()
	if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
		t.Fatal(err)
	}
	// A public owner query refreshes through the actual committed prefix.
	if _, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}}); err != nil {
		t.Fatalf("committed fixture reducer: %v", err)
	}
}

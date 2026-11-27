package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"uuid"
)

func TestValidationPassedRequiresFullProof(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	d := Digest(sha256.Sum256([]byte("proof fixture")))
	exit := int64(1)
	p := ValidationAttemptFinished{
		ValidationID: id, AttemptID: id, RunID: id, Outcome: "passed", Reason: "required_passed",
		Started: true, ExitKnown: true, ExitCode: &exit, RootExited: true, TreeClosed: true, IOJoined: true,
		ClosureProof: "live", Canary: ValidationCanaryResult{State: "expected_failed", TemplateSHA256: d, CanarySHA256: d, NonceSHA256: d, ObservedPID: 42, MarkerSHA256: d},
		RequiredResults: []ValidationRequiredResult{{Package: "example.test/unit", Name: "TestUnit", Outcome: "passed"}},
		Artifacts:       []ValidationArtifact{}, InputIntegrity: "verified",
	}
	if _, err := p.Encode(); err != nil {
		t.Fatal(err)
	}
	p.TreeClosed = false
	js, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeValidationAttemptFinished(js); !errors.Is(err, ErrInvalid) {
		t.Fatalf("passing without tree closure: %v", err)
	}
}

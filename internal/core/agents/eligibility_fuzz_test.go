package agents

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func FuzzEligibilityGroupReplay(f *testing.F) {
	_, commits := eligibilityGolden(f, 0)
	var prefixes []loopFuzzPrefix
	operations := map[string]bool{}
	for index, commit := range commits {
		if commit.Records[len(commit.Records)-1].Type != ledger.TypeRuntimeEligibilityReceipt {
			continue
		}
		fixture, _ := scopedGoldenFixture(f, "90", index)
		prefix := retainLoopFuzzPrefix(f, fixture)
		prefix.draft = draftsOf(commit)
		_, _, _, _, _, _, _, group, err := decodeFamily(prefix.draft, prefix.through+1)
		if err != nil || group == nil {
			f.Fatal("invalid frozen eligibility seed", err)
		}
		operations[group.receipt.Submission.Operation] = true
		prefixes = append(prefixes, prefix)
	}
	for _, operation := range []string{"eligibility.hold", "eligibility.release", "eligibility.require_first", "eligibility.first_next", "eligibility.first_refused"} {
		if !operations[operation] {
			f.Fatal("missing reachable frozen operation", operation)
		}
	}
	for shape := range prefixes {
		for mode := byte(0); mode < 12; mode++ {
			f.Add(byte(shape), mode, uint64(1), []byte(`{}`))
		}
	}
	f.Fuzz(func(t *testing.T, shape, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxEligibilityPayloadBytes {
			return
		}
		prefix := prefixes[int(shape)%len(prefixes)]
		_, _, _, _, _, _, _, original, err := decodeFamily(prefix.draft, prefix.through+1)
		if err != nil || original == nil {
			t.Fatal("invalid retained eligibility prefix", err)
		}
		sub, change := original.receipt.Submission, original.change
		change.FirstAction = cloneEligibilityFirst(change.FirstAction)
		valid := mode%12 == 0
		switch mode % 12 {
		case 3:
			sub.Origin.ContextID[15] ^= byte(value)
			valid = byte(value) == 0
		case 4:
			sub.ExpectedVersion += int64(value % 4)
			valid = value%4 == 0
		case 5:
			sub.ExpectedOperationID[15] ^= byte(value)
			valid = byte(value) == 0
		case 6:
			change.Version += int64(value % 4)
			valid = value%4 == 0
		case 7:
			change.OperationID[15] ^= byte(value)
			valid = byte(value) == 0
		case 10:
			if change.FirstAction != nil {
				change.FirstAction.PermittedTurn.AdmissionCommandID[15] ^= byte(value)
			} else {
				change.Reason = "unrequested_reason"
			}
		case 11:
			sub.Actor = "agent"
		}
		drafts, err := eligibilityDrafts(original.command.CommandID, sub, change, prefix.through+1)
		if err != nil {
			if valid {
				t.Fatal(err)
			}
			return
		}
		switch mode % 12 {
		case 1:
			drafts[1].Payload = bytes.Clone(input)
		case 2:
			drafts[2].Payload = bytes.Clone(input)
		case 8:
			drafts = append(drafts, drafts[1])
		case 9:
			drafts = drafts[:2]
		}
		s := prefix.fixture.service
		s.mu.Lock()
		_, _, _, _, _, _, _, accepted, err := decodeFamily(drafts, prefix.through+1)
		if err == nil {
			if accepted == nil {
				err = ErrInvalid
			} else {
				_, err = s.planEligibility(context.Background(), nil, accepted, prefix.through+1)
			}
		}
		unchanged := sameProjectionState(s.p, prefix.baseline.p)
		if err != nil {
			_, replayErr := s.acceptCommitLocked(context.Background(), replayDraftCommit(drafts, prefix.through+1), nil)
			unchanged = unchanged && sameProjectionState(s.p, prefix.baseline.p)
			s.mu.Unlock()
			if valid || replayErr == nil || !unchanged || prefix.fixture.store.Durable() != prefix.through {
				t.Fatalf("rejected eligibility group mutated authority/exposure: valid=%v preflight=%v replay=%v unchanged=%v", valid, err, replayErr, unchanged)
			}
			return
		}
		s.mu.Unlock()
		if !unchanged || prefix.fixture.store.Durable() != prefix.through {
			t.Fatal("eligibility preflight installed a decision")
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prefix.prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err := store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		through := store.Durable()
		reopened, _, err := Open(context.Background(), store, root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		gate := reopened.p.agents[sub.Origin.AgentID].eligibility
		if !reflect.DeepEqual(gate.snapshot, accepted.change) || store.Durable() != through {
			t.Fatal("accepted eligibility changed on inert replay")
		}
	})
}

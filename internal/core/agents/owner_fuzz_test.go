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

func FuzzContinuationGroupReplay(f *testing.F) {
	commits, _ := continuationFrozen(f)
	var prefixes []loopFuzzPrefix
	for index, commit := range commits {
		if commit.Records[len(commit.Records)-1].Type != ledger.TypeRuntimeContinuationReceipt {
			continue
		}
		fixture, _ := scopedGoldenFixture(f, "16", index)
		prefix := retainLoopFuzzPrefix(f, fixture)
		prefix.draft = draftsOf(commit)
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) != 7 {
		f.Fatal("frozen continuation transitions missing")
	}
	for shape := range prefixes {
		for mode := byte(0); mode < 12; mode++ {
			f.Add(byte(shape), mode, uint64(1), []byte(`{}`))
		}
	}
	f.Fuzz(func(t *testing.T, shape, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > ledger.MaxContinuationPayloadBytes {
			return
		}
		prefix := prefixes[int(shape)%len(prefixes)]
		_, _, _, _, _, _, original, err := decodeFamily(prefix.draft, prefix.through+1)
		if err != nil || original == nil {
			t.Fatal("invalid frozen prefix", err)
		}
		sub := original.receipt.Submission
		sub.Arguments = cloneContinuation(sub.Arguments)
		valid := mode%12 == 0
		switch mode % 12 {
		case 3:
			sub.Origin.ContextID[15] ^= byte(value)
			valid = byte(value) == 0
		case 4:
			sub.ExpectedVersion += int64(value % 4)
			valid = value%4 == 0
		case 5:
			sub.Arguments.Generation += int64(value % 4)
			valid = value%4 == 0
		case 6:
			sub.Arguments.ModelCalls += int64(value % 4)
			valid = value%4 == 0
		case 7:
			if len(sub.Arguments.Pending) != 0 {
				sub.Arguments.Pending[0].SourceSequence += int64(value % 4)
			} else {
				sub.Arguments.BoundSequence += int64(value % 4)
			}
			valid = value%4 == 0
		case 10:
			sub.ExpectedState = "terminal"
			sub.Arguments.State = "ready"
		case 11:
			sub.Arguments.WaitReason = "future_wait"
		}
		drafts, err := continuationDrafts(original.command.CommandID, sub, prefix.through+1)
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
		_, _, _, _, _, _, accepted, err := decodeFamily(drafts, s.p.through+1)
		if err == nil {
			if accepted == nil {
				err = ErrInvalid
			} else {
				_, err = s.planContinuation(context.Background(), nil, accepted, s.p.through+1)
			}
		}
		unchanged := sameProjectionState(s.p, prefix.baseline.p)
		if err != nil {
			_, replayErr := s.acceptCommitLocked(context.Background(), replayDraftCommit(drafts, prefix.through+1), nil)
			unchanged = unchanged && sameProjectionState(s.p, prefix.baseline.p)
			s.mu.Unlock()
			if valid || replayErr == nil || !unchanged || prefix.fixture.store.Durable() != prefix.through {
				t.Fatalf("refused continuation installed or replayed: valid=%v preflight=%v replay=%v unchanged=%v", valid, err, replayErr, unchanged)
			}
			return
		}
		s.mu.Unlock()
		if !unchanged || prefix.fixture.store.Durable() != prefix.through {
			t.Fatal("preflight installed a continuation")
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
		owner, err := reopened.currentContinuation(context.Background(), sub.Origin, sub.TurnID)
		if err != nil || !reflect.DeepEqual(owner.Snapshot, accepted.change) || store.Durable() != through {
			t.Fatalf("accepted continuation changed on inert replay: %v", err)
		}
	})
}

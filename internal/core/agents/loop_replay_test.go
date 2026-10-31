package agents

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestLoopGroupRulesAndFrozenReplay(t *testing.T) {
	t.Parallel()
	t.Run("group_rules", testLoopGroupRules)
	t.Run("late_native_effect", testLoopLateNativeEffect)
	for _, chapter := range []string{"10", "11", "12", "13", "14", "139", "15"} {
		t.Run("frozen_"+chapter, func(t *testing.T) {
			t.Parallel()
			f, commits := scopedGoldenFixture(t, chapter, -1)
			through := f.store.Durable()
			s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			f.service = s
			if len(report.Issues) != 0 || f.store.Durable() != through {
				t.Fatal("frozen replay changed the ledger or reported an integrity issue")
			}
			original := readTestFile(t, filepath.Join("../../../contract/ledger/testdata/f3", chapter, "log", ledger.SegmentName(1)))
			if !bytes.Equal(original, readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))) {
				t.Fatal("Open rewrote frozen bytes")
			}
			for _, commit := range commits {
				for _, record := range commit.Records {
					if record.Type != ledger.TypePrivateTurnRevision {
						continue
					}
					revision, err := ledger.DecodePrivateTurnRevision(record.Payload)
					if err != nil {
						t.Fatal(err)
					}
					view, err := s.Snapshot(context.Background(), ReadBinding{Binding: Binding{record.AgentID, record.ContextID}, Scope: "context"})
					if err != nil {
						t.Fatal(err)
					}
					if view.Revision < uint64(revision.Revision) || view.Heads["transcript"].Sequence < record.Seq {
						t.Fatal("turn revision did not join the shared private head and transcript chain")
					}
				}
			}
		})
	}
}

package agents

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type loopGoldenGroup struct {
	index int
	group *loopGroup
}

func loopGoldenGroups(t testing.TB) ([]ledger.Commit, []loopGoldenGroup) {
	t.Helper()
	_, commits := scopedGoldenFixture(t, "15", 0)
	var groups []loopGoldenGroup
	for index, commit := range commits {
		_, _, _, _, _, group, err := decodeFamily(draftsOf(commit), commit.First)
		if err != nil {
			t.Fatal(err)
		}
		if group != nil {
			groups = append(groups, loopGoldenGroup{index, group})
		}
	}
	return commits, groups
}
func loopGroupDrafts(t testing.TB, g *loopGroup, first uint64) []ledger.Draft {
	t.Helper()
	var effects []paidEncoding
	var types []string
	switch {
	case g.bound != nil:
		effects, types = []paidEncoding{*g.bound}, []string{ledger.TypeRuntimeTurnBound}
	case g.start != nil:
		effects, types = []paidEncoding{*g.start}, []string{ledger.TypeRuntimeToolStarted}
	case g.result != nil:
		effects, types = []paidEncoding{*g.result}, []string{ledger.TypeRuntimeToolResult}
	case g.ending != nil:
		effects, types = []paidEncoding{*g.revision, *g.ending}, []string{ledger.TypePrivateTurnRevision, ledger.TypeRuntimeTurnResult}
	}
	drafts, err := loopDrafts(g.command.CommandID, g.receipt.Submission, first, effects, types)
	if err != nil {
		t.Fatal(err)
	}
	return drafts
}
func loopPreflight(t testing.TB, s, baseline *Service, drafts []ledger.Draft) (*loopGroup, error) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _, _, _, group, err := decodeFamily(drafts, s.p.through+1)
	if err == nil {
		if group == nil {
			err = ErrInvalid
		} else {
			_, err = s.planLoop(context.Background(), group, s.p.through+1)
		}
	}
	if !sameProjectionState(s.p, baseline.p) {
		t.Fatal("loop preflight installed a partial transition")
	}
	return group, err
}
func testLoopGroupRules(t *testing.T) {
	t.Parallel()
	commits, groups := loopGoldenGroups(t)
	first := func(operation string) loopGoldenGroup {
		for _, group := range groups {
			if group.group.receipt.Submission.Operation == operation {
				return group
			}
		}
		t.Fatal("frozen loop shape missing")
		return loopGoldenGroup{}
	}
	for _, row := range []struct {
		name, operation string
		mutate          func(*loopGroup)
	}{
		{"bind_valid", "loop.bind", nil},
		{"bind_foreign_admission", "loop.bind", func(g *loopGroup) {
			a := g.receipt.Submission.Arguments.(ledger.LoopBindArguments)
			a.AdmissionCommandID = uuid.NewV7()
			g.receipt.Submission.Arguments = a
		}},
		{"start_valid", "tool.begin", nil},
		{"start_foreign_attempt", "tool.begin", func(g *loopGroup) {
			a := g.receipt.Submission.Arguments.(ledger.LoopToolBeginArguments)
			a.AttemptID = uuid.NewV7()
			g.receipt.Submission.Arguments = a
			v := ledger.RuntimeToolStarted(a)
			g.start = &v
		}},
		{"result_valid", "tool.result", nil},
		{"result_wrong_start", "tool.result", func(g *loopGroup) {
			a := g.receipt.Submission.Arguments.(ledger.LoopToolResultArguments)
			a.StartedSequence++
			g.receipt.Submission.Arguments = a
			v := ledger.RuntimeToolResult(a)
			g.result = &v
		}},
		{"result_wrong_tool", "tool.result", func(g *loopGroup) {
			a := g.receipt.Submission.Arguments.(ledger.LoopToolResultArguments)
			a.ToolID = uuid.NewV7()
			g.receipt.Submission.Arguments = a
			v := ledger.RuntimeToolResult(a)
			g.result = &v
		}},
		{"result_false_native_receipt", "tool.result", func(g *loopGroup) {
			a := g.receipt.Submission.Arguments.(ledger.LoopToolResultArguments)
			a.EffectReceiptSequence = 1
			g.receipt.Submission.Arguments = a
			v := ledger.RuntimeToolResult(a)
			g.result = &v
		}},
		{"end_valid", "loop.end", nil},
		{"end_stale_shared_head", "loop.end", func(g *loopGroup) { g.revision.ExpectedVersion++; g.revision.Revision++ }},
		{"end_false_transcript_predecessor", "loop.end", func(g *loopGroup) { g.revision.Changes[0].PreviousSHA256[0] ^= 1 }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			chosen := first(row.operation)
			f, _ := scopedGoldenFixture(t, "15", chosen.index)
			s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil || len(report.Issues) != 0 {
				t.Fatal("invalid frozen prefix", err)
			}
			f.service = s
			baseline, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer baseline.Close()
			_, _, _, _, _, g, err := decodeFamily(draftsOf(commits[chosen.index]), commits[chosen.index].First)
			if err != nil {
				t.Fatal(err)
			}
			if row.mutate != nil {
				row.mutate(g)
			}
			before := f.store.Durable()
			_, err = loopPreflight(t, s, baseline, loopGroupDrafts(t, g, before+1))
			if row.mutate == nil && err != nil || row.mutate != nil && err == nil || f.store.Durable() != before {
				t.Fatalf("loop transition accepted invalid binding or rejected its control: %v", err)
			}
		})
	}
	for _, mode := range []string{"duplicate_call_after_result", "duplicate_tool_after_result"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			selected := first("tool.result")
			f, _ := scopedGoldenFixture(t, "15", selected.index+1)
			s, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			f.service = s
			baseline, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer baseline.Close()
			started := first("tool.begin")
			_, _, _, _, _, g, err := decodeFamily(draftsOf(commits[started.index]), commits[started.index].First)
			if err != nil {
				t.Fatal(err)
			}
			g.command.CommandID = uuid.NewV7()
			a := g.receipt.Submission.Arguments.(ledger.LoopToolBeginArguments)
			if mode == "duplicate_call_after_result" {
				a.ToolID = uuid.NewV7()
			} else {
				a.ProviderCallID += "-changed"
			}
			g.receipt.Submission.Arguments = a
			value := ledger.RuntimeToolStarted(a)
			g.start = &value
			if _, err := loopPreflight(t, s, baseline, loopGroupDrafts(t, g, f.store.Durable()+1)); err == nil {
				t.Fatal("an ended provider call or tool identity was reused")
			}
		})
	}
	t.Run("recovered_started_child", func(t *testing.T) {
		t.Parallel()
		selected := first("tool.begin")
		f, _ := scopedGoldenFixture(t, "15", selected.index+1)
		ref := selected.group.start.RequestObject
		removed := false
		err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && entry.Name() == ref.SHA256.String() {
				removed = true
				return os.Remove(path)
			}
			return nil
		})
		if err != nil || !removed {
			t.Fatal("request-object absence fixture failed", err)
		}
		before := f.store.Durable()
		s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if err != nil || len(report.Issues) != 0 {
			t.Fatal("metadata-only recovered child read its unneeded request object", err)
		}
		f.service = s
		found := false
		for _, pending := range report.PendingLoops {
			if pending.ToolID == selected.group.start.ToolID {
				found = pending.State == "recovery_pending" && pending.Origin == selected.group.receipt.Submission.Origin && pending.TurnID == selected.group.start.ParentTurnID && pending.StartedSequence == selected.group.first+1 && pending.BoundSequence != 0
			}
		}
		if !found || f.store.Durable() != before {
			t.Fatal("Open resumed or lost an unmatched tool child")
		}
		turn := selected.group.start.ParentTurnID
		for _, operation := range []string{"end", "cancel", "reconcile"} {
			var err error
			switch operation {
			case "end":
				_, err = s.EndTurn(context.Background(), TurnEnding{CommandID: uuid.NewV7(), TurnID: turn, ExpectedVersion: 1, Outcome: "released"})
			case "cancel":
				_, err = s.CancelTurn(context.Background(), bindingOf(selected.group.receipt.Submission.Origin), TurnCancellation{CommandID: uuid.NewV7(), TurnID: turn, ExpectedVersion: 1})
			case "reconcile":
				_, err = s.ReconcileAdmissions(context.Background(), EndingRequest{CommandID: uuid.NewV7(), WorkID: turn, ExpectedVersion: 1})
			}
			var busy *BusyError
			if !errors.As(err, &busy) || f.store.Durable() != before {
				t.Fatal("native ending ignored the F3-15 child", err)
			}
		}
	})
}

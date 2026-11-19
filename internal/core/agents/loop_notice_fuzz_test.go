package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// The scheduler fixtures' native enrollment and privateWorkspace setup, with
// testing.TB so the transcript fuzzer can retain one real producer prefix.
func prepareLoopTranscriptTask(t testing.TB, f *turnFixture) (LoopContextSettings, workspace.Scratch) {
	t.Helper()
	ctx := context.Background()
	owner := bindingOf(f.origin)
	binding := Binding{AgentID: owner.AgentID, ContextID: uuid.NewV7()}
	if _, err := f.service.CreateContext(ctx, ContextCreation{Binding: binding, CommandID: uuid.NewV7(), Kind: "work"}); err != nil {
		t.Fatal(err)
	}
	capacity := paidPhysicalCapacity()
	capacity.BufferBytes, capacity.MaxOperationBytes = 512<<20, 4<<20
	scope := ledger.ScopeKey{Kind: "agent", ID: owner.AgentID}
	if _, err := f.service.ConfigureCapacity(ctx, owner, CapacitySpec{CommandID: uuid.NewV7(), ExpectedVersion: uint64(f.service.p.book.accounts[scope].version), Scope: scope, Limits: capacity}); err != nil {
		t.Fatal(err)
	}
	model := uuid.NewV7()
	for _, spec := range []GrantSpec{
		{GrantID: model, Grantee: binding, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
		{GrantID: uuid.NewV7(), Grantee: binding, Operations: []string{"context.inspect", "private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, Resources: []ledger.Resource{{Kind: "context", Binding: ledger.Binding(binding), Scope: "agent", Slots: []string{"instructions", "memory"}}, {Kind: "context", Binding: ledger.Binding(binding), Scope: "context", Slots: []string{"continuation", "handoff", "memory", "transcript"}}}, DataClass: "private", Recipients: []Binding{binding}, Limits: capacity, Lifetime: "until_revoked"},
	} {
		if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []ledger.PaidScope{{Kind: "agent", ID: owner.AgentID}, {Kind: "grant", ID: model}} {
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "100.000000", PeriodKind: "utc_day"})
	}
	task, assignment := uuid.NewV7(), uuid.NewV7()
	if _, err := f.service.CreateTask(ctx, owner, TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("Synthetic transcript task"), AcceptanceSHA256: sha256.Sum256([]byte("synthetic acceptance")), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{owner.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(binding)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "start"}); err != nil {
		t.Fatal(err)
	}
	setting := f.setting
	setting.Binding, setting.DisplayTitle, setting.TaskBinding = binding, "Transcript task", &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	root := testRoot(t)
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Synthetic Test", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"}, {"checkout", "--detach"}} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = root
		if _, err := command.CombinedOutput(); err != nil {
			t.Fatal("fixture Git failed", err)
		}
	}
	proposal, err := workspace.Inspect(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	ws, _, err := workspace.Open(ctx, f.store, f.root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = ws
	t.Cleanup(func() { _ = f.service.Close(); _ = ws.Close() })
	registered, err := ws.Register(ctx, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Scratch(ctx, owner.ContextID, registered.ID); err != nil {
		t.Fatal(err)
	}
	parent, err := ws.Scratch(ctx, binding.ContextID, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	return setting, parent
}

func loopTranscriptTaskSeeds(t testing.TB, f *turnFixture, setting LoopContextSettings, parent workspace.Scratch) (ledger.Origin, [][]byte) {
	t.Helper()
	ctx := context.Background()
	origin := f.origin
	origin.ContextID = setting.Binding.ContextID
	view, err := f.service.OpenSession(ctx, setting.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	var seeds [][]byte
	for step := range 2 {
		if step == 1 {
			writer, err := f.workspace.BeginWrite(ctx, workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: f.origin.ContextID, AgentID: f.origin.AgentID, TaskAgentID: setting.Binding.AgentID, TaskScratchID: parent.ID})
			if err != nil {
				t.Fatal(err)
			}
			scratch := writer.Scratch()
			if err := os.MkdirAll(filepath.Join(scratch.Dir, "safe"), 0700); err != nil {
				writer.Close()
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(scratch.Dir, "safe", "transcript-notice.txt"), []byte("schema two transcript notice"), 0600); err != nil {
				writer.Close()
				t.Fatal(err)
			}
			_, err = writer.CommitObserved(ctx)
			writer.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.workspace.MergeBack(ctx, scratch.ID); err != nil {
				t.Fatal(err)
			}
		}
		started, err := view.Do(ctx, turnMessage(setting.Binding.ContextID, "Natural transcript seed"))
		if err != nil {
			t.Fatal(err)
		}
		result, _ := f.finishedFor(t, view, started.TurnID)
		if result.Outcome != "finished" {
			t.Fatal("schema-two transcript producer did not finish")
		}
		_, input := passiveInput(t, f, origin, started.TurnID)
		if input.Schema != 2 || len(input.TaskResults) != step {
			t.Fatal("transcript seed did not reach its schema-two input shape")
		}
		snapshot, err := f.service.Snapshot(ctx, ReadBinding{Binding: setting.Binding, Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		page, err := f.service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: setting.Binding, Scope: "context"}, Slot: "transcript", Revision: snapshot.Revision, MaxBytes: maxObjectBytes})
		if err != nil || len(page.Chunks) != step+1 {
			t.Fatal("missing committed schema-two transcript", err)
		}
		seeds = append(seeds, bytes.Clone(page.Chunks[len(page.Chunks)-1].Bytes))
	}
	return origin, seeds
}

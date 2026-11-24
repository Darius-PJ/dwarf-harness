package agents

import (
	"context"
	"crypto/sha256"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func integratedParentSetting(t *testing.T, f *continuationCaptureFixture) LoopContextSettings {
	t.Helper()
	parent := f.contextSetting(t, Binding{AgentID: f.origin.AgentID, ContextID: f.parent.ContextID})
	task, assignment := uuid.NewV7(), uuid.NewV7()
	_, err := f.service.CreateTask(context.Background(), bindingOf(f.origin), TaskCreation{CommandID: uuid.NewV7(), TaskID: task, Description: []byte("synthetic parent work"), AcceptanceSHA256: sha256.Sum256([]byte("synthetic acceptance")), Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{parent.Binding.AgentID}, Assignment: &ledger.AssignmentInput{AssignmentID: assignment, Assignee: ledger.Binding(parent.Binding)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TransitionTask(context.Background(), bindingOf(f.origin), TaskMutation{CommandID: uuid.NewV7(), TaskID: task, ExpectedVersion: 1, ExpectedAssignmentID: assignment, ExpectedGeneration: 1, Action: "start"}); err != nil {
		t.Fatal(err)
	}
	parent.DisplayTitle, parent.TaskBinding = "Parent task", &ledger.TaskBinding{TaskID: task, AssignmentID: assignment, Generation: 1}
	return parent
}

func TestTurnLoopTemporaryMergeBack(t *testing.T) {
	t.Parallel()
	t.Run("crash_after_prepared", testIntegratedMergeCrash)
	t.Run("workspace_close_capture_refusal", func(t *testing.T) {
		t.Parallel()
		testIntegratedWorkspaceClose(t)
	})
	t.Run("clean_three_way", func(t *testing.T) {
		t.Parallel()
		testIntegratedMergeOutcome(t, "clean_three_way", false)
	})
}

package workspace

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
)

// TestServiceProcessBridge: only the gitprocess unconfirmed class blocks, and
// only a UUIDv7 run from an UnconfirmedError is ever named.
func TestServiceProcessBridge(t *testing.T) {
	t.Parallel()
	run := uuid.NewV7()
	named := &gitprocess.UnconfirmedError{RunID: run, Reason: "job_create_failed"}
	for _, row := range []struct {
		name    string
		err     error
		blocked bool
		run     uuid.UUID
	}{
		{"bare_class", &GitError{Args: []string{"status"}, Exit: -1, Err: gitprocess.ErrUnconfirmed}, true, uuid.UUID{}},
		{"named_run", &GitError{Args: []string{"status"}, Exit: -1, Err: named}, true, run},
		{"joined_context", errors.Join(context.Canceled, &GitError{Args: []string{"status"}, Exit: -1, Err: named}), true, run},
		{"unversioned_run", &GitError{Args: []string{"status"}, Exit: -1, Err: &gitprocess.UnconfirmedError{RunID: uuid.UUID{1}, Reason: "cancelled"}}, true, uuid.UUID{}},
		{"exit", &GitError{Args: []string{"status"}, Exit: 1, Err: gitprocess.ErrExit}, false, uuid.UUID{}},
		{"limit", &GitError{Args: []string{"status"}, Exit: -1, Err: gitprocess.ErrLimit}, false, uuid.UUID{}},
		{"closed", gitprocess.ErrClosed, false, uuid.UUID{}},
		{"busy", &BusyError{Reason: "context_busy"}, false, uuid.UUID{}},
		{"git_unavailable", ErrGitUnavailable, false, uuid.UUID{}},
		{"nil", nil, false, uuid.UUID{}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if blocked, id := ClassifyExecutionFailure(row.err); blocked != row.blocked || id != row.run {
				t.Fatalf("classified (%v, %s), want (%v, %s)", blocked, id, row.blocked, row.run)
			}
		})
	}
}

// TestServiceProcessClosures: with no fence, Open reports exactly its own
// first proven run where the Owner proves tree closure, and nothing elsewhere.
func TestServiceProcessClosures(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	report := f.reopen(t, serviceConfig())
	if runtime.GOOS != "windows" {
		if f.svc.ProcessProofSupported() || len(report.ProcessClosures) != 0 {
			t.Fatalf("closure proof without tree proof: %+v", report.ProcessClosures)
		}
		return
	}
	if !f.svc.ProcessProofSupported() || len(report.ProcessClosures) != 1 || !validID(report.ProcessClosures[0].RunID) || report.ProcessClosures[0].Proof != "live" {
		t.Fatalf("Open's own run was not its only closure: %+v", report.ProcessClosures)
	}
}

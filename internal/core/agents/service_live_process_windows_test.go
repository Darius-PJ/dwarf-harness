//go:build windows

package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
	"golang.org/x/sys/windows"
)

// serviceCollideOwnerJobs makes a real Git launch inside the finish commit
// fail with the Owner's own unconfirmed error. It reads the Owner's slot
// batches (gitprocess slots.go layout: a NUL-padded JSON header of 4096 bytes
// naming 32 run IDs, 32 frames of 512 bytes, then one retired byte per slot),
// finds the unused slots, which the Owner takes in index order, and pre-creates
// the named Job of every unused slot after the first skip. CommitObserved's
// first two launches (the colony-marker and anchor reads, whose errors the
// workspace reports as its own unavailability) take the skipped slots; the
// third, repairScratchTip's branch read, returns its error unchanged and meets
// ERROR_ALREADY_EXISTS in createJob, so the Owner retains that run. This is
// the only place a test reads the Owner's private slot layout. The returned
// function closes every planted handle, leaving the names absent for the next
// incarnation's reconciliation.
func serviceCollideOwnerJobs(t *testing.T, root string, skip int) func() {
	t.Helper()
	const header, frame, slots = 4096, 512, 32
	batches, err := filepath.Glob(filepath.Join(root, "workspaces", "processes", "runs", "*.slots"))
	if err != nil || len(batches) == 0 {
		t.Fatalf("no Owner slot batch to collide with: %v", err)
	}
	var jobs []windows.Handle
	planted := 0
	for _, batch := range batches {
		raw, err := os.ReadFile(batch)
		if err != nil || len(raw) != header+slots*frame+frame {
			t.Fatalf("Owner slot batch unreadable: %v", err)
		}
		prefix := raw[:header]
		if n := bytes.IndexByte(prefix, 0); n >= 0 {
			prefix = prefix[:n]
		}
		var h struct {
			RootID uuid.UUID   `json:"root_id"`
			RunIDs []uuid.UUID `json:"run_ids"`
		}
		if err := json.Unmarshal(prefix, &h); err != nil || len(h.RunIDs) != slots {
			t.Fatalf("Owner slot header: %v", err)
		}
		for i, run := range h.RunIDs {
			used := raw[header+slots*frame+i] != 0 || !bytes.Equal(raw[header+i*frame:header+(i+1)*frame], make([]byte, frame))
			if used {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			name, err := windows.UTF16PtrFromString(`Local\dwarf-harness-git-` + h.RootID.String() + "-" + run.String())
			if err != nil {
				t.Fatal(err)
			}
			job, err := windows.CreateJobObject(nil, name)
			if err != nil {
				t.Fatal(err)
			}
			jobs = append(jobs, job)
			planted++
		}
	}
	if planted == 0 {
		t.Fatal("no unused Owner slot after the next launch")
	}
	return func() {
		for _, job := range jobs {
			if err := windows.CloseHandle(job); err != nil {
				t.Error(err)
			}
		}
		jobs = nil
	}
}

type serviceProcessSignal struct {
	Root    string
	Agent   uuid.UUID
	Through uint64
}

// TestServiceProcessFailure: a real retained Owner refusal at the finish
// commit is process demand; only the next incarnation's closure proof clears
// it, and the clear alone restores nothing. A retained Owner keeps its root
// locked for the life of its process, so the refusal runs in a child process
// and the next incarnation opens after the child is killed.
func TestServiceProcessFailure(t *testing.T) {
	if os.Getenv("DWARF_SERVICE_PROCESS_CHILD") != "" {
		serviceProcessChild(t)
		return
	}
	t.Parallel()
	container := testRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestServiceProcessFailure$")
	cmd.Env = append(os.Environ(), "DWARF_SERVICE_PROCESS_CHILD=1", "TMP="+container, "TEMP="+container, "TMPDIR="+container)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var signal serviceProcessSignal
	decodeErr := gob.NewDecoder(stdout).Decode(&signal)
	_ = cmd.Process.Kill()
	waitErr := cmd.Wait()
	if decodeErr != nil {
		// The child's own assertion is in its output.
		out, _ := io.ReadAll(stdout)
		t.Fatalf("refusal child failed: %v %v\n%s%s", decodeErr, waitErr, out, stderr.Bytes())
	}
	if relative, err := filepath.Rel(container, signal.Root); err != nil || strings.HasPrefix(relative, "..") {
		t.Fatal("child root escaped its container")
	}
	store, _, err := ledgerstore.Open(signal.Root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if store.Durable() != signal.Through {
		t.Fatal("child records were not preserved")
	}
	ws, report, err := workspace.Open(context.Background(), store, signal.Root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	if !ws.ProcessProofSupported() || len(report.ProcessClosures) == 0 {
		t.Fatalf("restart produced no closure proof: %+v", report.ProcessClosures)
	}
	s, _, err := Open(context.Background(), store, signal.Root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	before := store.Durable()
	if _, err := s.ReconcileServiceEvidence(context.Background(), &ServiceHostReports{}); err != nil || store.Durable() != before {
		t.Fatalf("empty closures appended: %v", err)
	}
	reports := &ServiceHostReports{ProcessClosures: report.ProcessClosures}
	if _, err := s.ReconcileServiceEvidence(context.Background(), reports); err != nil {
		t.Fatal(err)
	}
	es := serviceObservations(t, s)
	clear := es[len(es)-1]
	if len(es) != 2 || clear.Source.Reason != "process_closed" || clear.Source.ExecutionRunID != report.ProcessClosures[0].RunID || clear.Source.ExecutionProof != report.ProcessClosures[0].Proof || serviceCountRecords(t, s, ledger.TypeRuntimeServiceCauseChanged) != 2 {
		t.Fatalf("closure clear witness: %+v", es)
	}
	if v, _ := s.ServiceStatus(context.Background(), signal.Agent); v.State != "lost" {
		t.Fatal("a clear restored the member")
	}
	after := store.Durable()
	if _, err := s.ReconcileServiceEvidence(context.Background(), reports); err != nil || store.Durable() != after {
		t.Fatalf("second reports reconcile appended: %v", err)
	}
}

// serviceProcessChild drives one real Owner refusal at the finish commit,
// checks its native and service outcome, signals the parent and waits to be
// killed with the Owner still retaining the run.
func serviceProcessChild(t *testing.T) {
	f := newContinuationCapture(t, true, nil)
	if !f.workspace.ProcessProofSupported() {
		t.Fatal("Windows Owner did not report tree proof")
	}
	entered := make(chan uuid.UUID, 1)
	f.service.deps.beforeFinishCommit = func(turn uuid.UUID) error {
		_ = serviceCollideOwnerJobs(t, f.root, 2)
		entered <- turn
		return nil
	}
	f.enable(t)
	_, started := f.send(t)
	if <-entered != started.TurnID {
		t.Fatal("wrong turn reached the finish commit")
	}
	f.service.loop.mu.Lock()
	turn := f.service.loop.turns[started.TurnID]
	f.service.loop.mu.Unlock()
	<-turn.done
	f.service.loop.mu.Lock()
	state, terminal := turn.state, turn.terminalError
	f.service.loop.mu.Unlock()
	var unconfirmed *gitprocess.UnconfirmedError
	if state != "recovery_pending" || !errors.As(terminal, &unconfirmed) {
		t.Fatalf("native finish refusal changed: state=%s err=%v", state, terminal)
	}
	es := serviceObservations(t, f.service)
	if len(es) != 1 || es[0].Source.Kind != "process_refusal" || es[0].Source.Reason != "process_unconfirmed" || es[0].Source.ExecutionRunID != unconfirmed.RunID || es[0].Source.TurnID != started.TurnID {
		t.Fatalf("Owner refusal witness: %+v", es)
	}
	if records := foreignRecords(t, f, started.TurnID); len(records.observed) != 0 || len(records.merged) != 0 {
		t.Fatal("workspace work followed the unconfirmed refusal")
	}
	if v, _ := f.service.ServiceStatus(context.Background(), f.origin.AgentID); v.State != "lost" {
		t.Fatalf("process demand did not lose the member: %+v", v)
	}
	signal := serviceProcessSignal{Root: f.root, Agent: f.origin.AgentID, Through: f.store.Durable()}
	if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	_, _ = os.Stdin.Read(one[:])
	t.Fatal("process child unexpectedly released")
}

//go:build windows

package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// TestServiceProcessClosuresAfterCrash: a Core killed during an owned Git run
// leaves a running fence; the next Open reconciles it and reports its closure
// first, then Open's own first proven run.
func TestServiceProcessClosuresAfterCrash(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	crashFinalization(t, f, "owned_git", nil)
	var err error
	f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.svc, report, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
	if err != nil {
		t.Fatal(err)
	}
	closures := report.ProcessClosures
	if len(closures) < 2 || closures[0].Proof != "name_absent" || !validID(closures[0].RunID) || closures[len(closures)-1].Proof != "live" || closures[0].RunID == closures[len(closures)-1].RunID {
		t.Fatalf("crashed run's closure was not reported first: %+v", closures)
	}
}

// processFenceResult is the unreconcilable-fence child's report.
type processFenceResult struct {
	Planted, Blocked bool
	Closures         int
}

// TestServiceProcessUnreconcilable: a running fence whose named Job exists
// without the Owner's limits cannot be proved closed, so Open fails with the
// blocked class and reports no closure. A refused Owner keeps its root locked
// for the life of its process, so the refused Open runs in a child; once the
// child exits the parent's Open reconciles the same fence.
func TestServiceProcessUnreconcilable(t *testing.T) {
	if colony := os.Getenv("DWARF_PROCESS_FENCE_COLONY"); colony != "" {
		serviceProcessFenceChild(t, colony)
		return
	}
	t.Parallel()
	f := newServiceFixture(t)
	crashFinalization(t, f, "owned_git", nil)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestServiceProcessUnreconcilable$")
	cmd.Env = append(slices.Clone(f.env), "DWARF_PROCESS_FENCE_COLONY="+f.colony)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var result processFenceResult
	if decodeErr := json.Unmarshal(bytes.TrimSpace(bytes.SplitN(out, []byte("\n"), 2)[0]), &result); decodeErr != nil {
		t.Fatalf("fence child: %v %v %s %s", err, decodeErr, out, stderr.Bytes())
	}
	if !result.Planted || !result.Blocked || result.Closures != 0 {
		t.Fatalf("unreconcilable fence: %+v", result)
	}
	f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	f.svc, report, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ProcessClosures) < 2 || report.ProcessClosures[0].Proof != "name_absent" {
		t.Fatalf("fence was not reconciled after the child: %+v", report.ProcessClosures)
	}
}

// serviceProcessFenceChild pre-creates each running fence's named Job without
// the Owner's limits, then opens the workspace over the colony and reports the
// outcome. Running fences are slot frames (gitprocess slots.go layout: a
// 4096-byte header, then 32 NUL-padded 512-byte frames).
func serviceProcessFenceChild(t *testing.T, colony string) {
	const header, frame, slots = 4096, 512, 32
	createJob := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateJobObjectW")
	batches, err := filepath.Glob(filepath.Join(colony, "workspaces", "processes", "runs", "*.slots"))
	if err != nil {
		t.Fatal(err)
	}
	var result processFenceResult
	for _, path := range batches {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) < header+slots*frame {
			t.Fatalf("slot batch unreadable: %v", err)
		}
		for i := range slots {
			body := raw[header+i*frame : header+(i+1)*frame]
			if n := bytes.IndexByte(body, 0); n >= 0 {
				body = body[:n]
			}
			var fence struct {
				JobName string `json:"job_name"`
				State   string `json:"state"`
			}
			if len(body) == 0 || json.Unmarshal(body, &fence) != nil || fence.State != "running" {
				continue
			}
			name, err := syscall.UTF16PtrFromString(fence.JobName)
			if err != nil {
				t.Fatal(err)
			}
			// The handle stays open until this process exits.
			if job, _, callErr := createJob.Call(0, uintptr(unsafe.Pointer(name))); job == 0 {
				t.Fatal(callErr)
			}
			result.Planted = true
		}
	}
	store, _, err := ledgerstore.Open(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{})
	result.Blocked, _ = ClassifyExecutionFailure(err)
	result.Closures = len(report.ProcessClosures)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

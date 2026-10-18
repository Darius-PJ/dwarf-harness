package winexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testObserveFrameProcesses(t *testing.T, frame supervisorFrame, handles *[]windows.Handle) bool {
	t.Helper()
	if frame.Receipt.TreeExited {
		return true
	}
	if frame.RootPID == 0 && frame.Active == 0 && len(frame.PIDs) == 0 {
		return true
	}
	if !frame.AccountingKnown || frame.Active == 0 || int(frame.Active) != len(frame.PIDs) {
		t.Errorf("native live-process observation is incomplete: %+v", frame)
		return false
	}
	for _, pid := range frame.PIDs {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err != nil {
			t.Error(err)
			return false
		}
		*handles = append(*handles, h)
		state, err := windows.WaitForSingleObject(h, 0)
		if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Errorf("reported native process %d is not actually live: wait=%d error=%v", pid, state, err)
			return false
		}
	}
	return true
}

func testWaitAndCloseProcesses(t *testing.T, handles *[]windows.Handle) bool {
	t.Helper()
	proved := true
	for _, handle := range *handles {
		state, err := windows.WaitForSingleObject(handle, 10000)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			proved = false
			t.Errorf("known process did not positively exit: wait=%d error=%v", state, err)
		}
		if err := windows.CloseHandle(handle); err != nil {
			proved = false
			t.Error(err)
		}
	}
	*handles = nil
	return proved
}

// Trusted fixture teardown only, after the test positively waits its processes.
// This never changes or clears a product quarantine fence.
func testRemoveOwnedProfile(t *testing.T, f runFence) {
	t.Helper()
	if !f.ProfileCreated {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	binding, err := openDirectory(f.ProfilePath, true)
	existed := err == nil
	if err == nil {
		if binding.Volume != f.ProfileVolume || binding.ID != f.ProfileFileID {
			_ = windows.CloseHandle(binding.Handle)
			t.Errorf("refusing fixture teardown of replaced native profile %q", f.ProfilePath)
			return
		}
		if !binding.Reparse {
			err = emptyDirectory(ctx, binding.Path)
		}
		err = errors.Join(err, windows.CloseHandle(binding.Handle))
		if err == nil && binding.Reparse {
			err = unlink(ctx, binding.Path)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		t.Errorf("owned native profile teardown: %v", err)
		return
	}
	if err := deleteProfile(f.ProfileName); err != nil {
		t.Errorf("owned native profile deletion: %v", err)
		return
	}
	if existed {
		t.Logf("fixture teardown removed test-owned profile moniker=%s path=%q", f.ProfileName, f.ProfilePath)
	}
}

func testQuarantinedReopen(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e, err := Open(ctx, root)
	if e != nil {
		_ = e.Close()
		t.Fatal("uncertain native run was reopened")
	}
	if !errors.Is(err, ErrQuarantined) {
		t.Fatalf("uncertain reopen did not quarantine: %v", err)
	}
}

func TestSupervisorLossFenceMatrix(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"unconfirmed", "profile_bound", "suspended_created", "resumed", "tree_exited", "closed_fence", "fence_removed", "run_removed"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			var frame supervisorFrame
			var child *supervisorProcess
			var handles []windows.Handle
			var observedAll bool
			t.Cleanup(func() {
				testStopSupervisor(t, child)
				if testWaitAndCloseProcesses(t, &handles) && observedAll {
					testRemoveOwnedProfile(t, frame.Fence)
				}
			})
			child = testStartSupervisor(t, root, "crash", phase)
			testReleaseSupervisor(t, child)
			frame = testReadSupervisor(t, child)
			observedAll = testObserveFrameProcesses(t, frame, &handles)
			if !observedAll {
				t.FailNow()
			}
			if phase == "run_removed" {
				if frame.Phase != "running-before-stop" || frame.Active != 3 || len(frame.PIDs) != 3 {
					t.Fatalf("former payload was not a real held tree: %+v", frame)
				}
				// These live observations must not retain natural-exit accounting.
				for _, handle := range handles {
					if err := windows.CloseHandle(handle); err != nil {
						t.Fatal(err)
					}
				}
				handles = nil
				observedAll = false
				testReleaseSupervisor(t, child)
				frame = testReadSupervisor(t, child)
				observedAll = testObserveFrameProcesses(t, frame, &handles)
				if !observedAll {
					t.FailNow()
				}
			}
			if frame.Phase != phase {
				t.Fatalf("supervisor did not reach the actual death point: %+v", frame)
			}
			switch phase {
			case "unconfirmed":
				if frame.Fence.ProfileCreated || frame.RootPID != 0 || frame.Job != 0 {
					t.Fatalf("native resources preceded the initial fence: %+v", frame)
				}
			case "profile_bound":
				if !frame.Fence.ProfileCreated || frame.RootPID != 0 {
					t.Fatalf("profile boundary was not pre-process: %+v", frame)
				}
			case "suspended_created":
				if len(frame.PIDs) != 1 || frame.PIDs[0] != frame.RootPID || frame.Active != 1 || !frame.Receipt.ResumedUTC.IsZero() {
					t.Fatalf("atomic suspended creation was not observed: %+v", frame)
				}
			case "resumed":
				if len(frame.PIDs) != 3 || frame.Active != 3 || frame.Receipt.ResumedUTC.IsZero() {
					t.Fatalf("running root/child/grandchild not observed: %+v", frame)
				}
			case "tree_exited", "closed_fence":
				if !frame.Receipt.TreeExited || !frame.Receipt.RootExited || !frame.AccountingKnown || frame.Active != 0 || len(frame.PIDs) != 0 {
					t.Fatalf("native zero-active closure not observed: %+v", frame)
				}
			default:
				if !frame.Receipt.TreeExited || !frame.Receipt.RootExited || !frame.Receipt.UsageKnown || frame.Job != 0 {
					t.Fatalf("deletion tail preceded recorded native closure/accounting: %+v", frame)
				}
			}
			runDir := filepath.Join(root, "runs", frame.Fence.RunID)
			marker := filepath.Join(runDir, "fence.json")
			var before []byte
			var canary string
			if phase != "fence_removed" && phase != "run_removed" {
				var err error
				before, err = os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := decodeFence(before, frame.Fence.RootID, frame.Fence.RunID)
				if err != nil || actual != frame.Fence {
					t.Fatalf("native phase disagrees with actual canonical fence: %+v %v", actual, err)
				}
				work := filepath.Join(runDir, "work")
				if err := os.MkdirAll(work, 0o700); err != nil {
					t.Fatal(err)
				}
				canary = filepath.Join(work, "crash-canary")
				if err := os.WriteFile(canary, []byte("uncertain-private-data"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			profileCanary := ""
			if frame.Fence.ProfileCreated {
				if _, err := os.Stat(frame.Fence.ProfilePath); err == nil {
					profileCanary = filepath.Join(frame.Fence.ProfilePath, "crash-canary")
					if err := os.WriteFile(profileCanary, []byte("uncertain-native-profile"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
			testStopSupervisor(t, child)
			if !testWaitAndCloseProcesses(t, &handles) {
				t.FailNow()
			}
			if phase == "closed_fence" || phase == "run_removed" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				reopened, err := Open(ctx, root)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if _, err := os.Stat(runDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("old run was retained or replayed: %v", err)
				}
				next, err := reopened.Run(context.Background(), testRequest(t, "output", "1"))
				assertReusable(t, next, err)
				if next.RunID.String() == frame.Fence.RunID || next.ProfileSID == frame.Receipt.ProfileSID || string(next.Stdout) != "o" || string(next.Stderr) != "e" {
					t.Fatalf("fresh explicit run reused former identity/output: %+v", next)
				}
			} else {
				testQuarantinedReopen(t, root)
				if phase == "fence_removed" {
					entries, err := os.ReadDir(runDir)
					if err != nil || len(entries) != 0 {
						t.Fatalf("empty orphan was changed: %v %v", entries, err)
					}
				} else {
					after, err := os.ReadFile(marker)
					if err != nil || !bytes.Equal(after, before) {
						t.Fatalf("uncertain canonical fence was changed: %q %v", after, err)
					}
					data, err := os.ReadFile(canary)
					if err != nil || string(data) != "uncertain-private-data" {
						t.Fatalf("uncertain private data was removed: %q %v", data, err)
					}
				}
				if profileCanary != "" {
					data, err := os.ReadFile(profileCanary)
					if err != nil || string(data) != "uncertain-native-profile" {
						t.Fatalf("uncertain native profile was removed/adopted: %q %v", data, err)
					}
				}
			}
			t.Logf("actual abrupt supervisor loss at %s: native PIDs=%v, active_known=%t active=%d, canonical state=%s; reopen=%s", phase, frame.PIDs, frame.AccountingKnown, frame.Active, frame.Fence.State, map[bool]string{true: "safe cleanup/fresh request only", false: "quarantined"}[phase == "closed_fence" || phase == "run_removed"])
		})
	}
}

func TestAmbiguousStopAndClosedFenceWriteFailure(t *testing.T) {
	t.Parallel()
	t.Run("retained-native-Job", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		var frame supervisorFrame
		var child *supervisorProcess
		var handles []windows.Handle
		var retained windows.Handle
		var observedAll bool
		t.Cleanup(func() {
			if retained != 0 {
				if err := windows.TerminateJobObject(retained, 1); err != nil {
					t.Error(err)
				}
			}
			testStopSupervisor(t, child)
			proved := testWaitAndCloseProcesses(t, &handles)
			if retained != 0 {
				if err := windows.CloseHandle(retained); err != nil {
					t.Error(err)
				}
				retained = 0
			}
			if proved && observedAll {
				testRemoveOwnedProfile(t, frame.Fence)
			}
		})
		child = testStartSupervisor(t, root, "ambiguous", "resumed")
		testReleaseSupervisor(t, child)
		frame = testReadSupervisor(t, child)
		observedAll = testObserveFrameProcesses(t, frame, &handles)
		if !observedAll {
			t.FailNow()
		}
		if frame.Phase != "resumed" || frame.Active != 3 || len(frame.PIDs) != 3 || frame.Job == 0 {
			t.Fatalf("actual held tree not ready: %+v", frame)
		}
		if err := windows.DuplicateHandle(child.process, windows.Handle(frame.Job), windows.CurrentProcess(), &retained, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			t.Fatal(err)
		}
		marker := fencePath(root, frame.Fence.RunID)
		before, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		parents := make([]uint32, len(frame.PIDs))
		for i, pid := range frame.PIDs {
			parents[i], err = testNativeParent(pid)
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("retained-Job pre-kill readiness: root=%d PIDs=%v parents=%v stdout=%q stderr=%q", frame.RootPID, frame.PIDs, parents, frame.Receipt.Stdout, frame.Receipt.Stderr)
		testStopSupervisor(t, child)
		accounting, accountErr := jobAccounting(retained)
		allLive := true
		for i, handle := range handles {
			state, waitErr := windows.WaitForSingleObject(handle, 0)
			query, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, frame.PIDs[i])
			var code uint32
			codeErr := openErr
			if openErr == nil {
				codeErr = errors.Join(windows.GetExitCodeProcess(query, &code), windows.CloseHandle(query))
			}
			t.Logf("retained-Job post-kill: PID=%d parent=%d root=%d wait=%d waitError=%v exit=%#x exitError=%v Job=%+v queryError=%v", frame.PIDs[i], parents[i], frame.RootPID, state, waitErr, code, codeErr, accounting, accountErr)
			allLive = allLive && waitErr == nil && state == uint32(windows.WAIT_TIMEOUT) && codeErr == nil && code == 259 // STILL_ACTIVE
		}
		if !allLive || accountErr != nil || accounting.Active != 3 {
			t.Fatalf("retained native Job did not preserve the actual live tree: %+v %v", accounting, accountErr)
		}
		testQuarantinedReopen(t, root)
		if err := windows.TerminateJobObject(retained, 1); err != nil {
			t.Fatal(err)
		}
		if !testWaitAndCloseProcesses(t, &handles) {
			t.FailNow()
		}
		accounting, err = jobAccounting(retained)
		if err != nil || accounting.Active != 0 {
			t.Fatalf("explicit test teardown did not reach native zero after closing observers: %+v %v", accounting, err)
		}
		testQuarantinedReopen(t, root)
		after, err := os.ReadFile(marker)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("external live/dead knowledge replaced durable proof: %q %v", after, err)
		}
		t.Logf("actual retained Job kept all native PIDs=%v live after supervisor exit; native termination/waits reached zero, and both reopens still quarantined", frame.PIDs)
	})
	t.Run("closed-rename-sharing-violation", func(t *testing.T) {
		t.Parallel()
		e := testExecutor(t)
		var held windows.Handle
		var f runFence
		var before []byte
		var observationErr error
		var nativeClosed bool
		t.Cleanup(func() {
			if held != 0 {
				_ = windows.CloseHandle(held)
			}
			_ = e.Close()
			if nativeClosed {
				testRemoveOwnedProfile(t, f)
			}
		})
		e.observe = func(phase string, r *nativeRun) {
			if phase != "tree_exited" {
				return
			}
			f = r.fence
			accounting, err := jobAccounting(r.job)
			nativeClosed = err == nil && accounting.Active == 0 && r.receipt.RootExited && r.receipt.TreeExited
			if !nativeClosed {
				observationErr = fmt.Errorf("actual tree closure missing: %+v %v", accounting, err)
				return
			}
			marker := fencePath(e.root, f.RunID)
			before, err = os.ReadFile(marker)
			if err != nil {
				observationErr = err
				return
			}
			name, err := windows.UTF16PtrFromString(marker)
			if err == nil {
				held, err = windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
			}
			observationErr = err
			if observationErr == nil {
				probe := marker + ".rename-control"
				blocked := os.Rename(marker, probe)
				if blocked == nil {
					observationErr = errors.Join(fmt.Errorf("destination hold did not prevent actual rename"), os.Rename(probe, marker))
				} else if !errors.Is(blocked, windows.ERROR_ACCESS_DENIED) && !errors.Is(blocked, windows.ERROR_SHARING_VIOLATION) {
					observationErr = blocked
				}
			}
		}
		r, err := e.Run(context.Background(), testRequest(t, "output", "1"))
		if held != 0 {
			closeErr := windows.CloseHandle(held)
			held = 0
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		if observationErr != nil {
			t.Fatal(observationErr)
		}
		if !nativeClosed || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || !errors.Is(err, ErrQuarantined) || r.State != Quarantined || !r.RootExited || !r.TreeExited || r.ResourcesReusable || r.OutputState != Unpublishable {
			t.Fatalf("real closed-fence replacement failure: %+v error=%v", r, err)
		}
		marker := fencePath(e.root, f.RunID)
		renameCode := windows.ERROR_ACCESS_DENIED
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			renameCode = windows.ERROR_SHARING_VIOLATION
		}
		// A control on this same canonical object fails while held and works
		// immediately after release. Preserve its unconfirmed contents; never
		// promote the complete temporary closed proof during this control.
		if err := os.Rename(marker, marker+".rename-control"); err != nil {
			t.Fatalf("released destination rename positive control: %v", err)
		}
		if err := os.Rename(marker+".rename-control", marker); err != nil {
			t.Fatal(err)
		}
		temporary, tempErr := os.ReadFile(marker + ".tmp")
		closed, decodeErr := decodeFence(temporary, f.RootID, f.RunID)
		if tempErr != nil || decodeErr != nil || closed.State != "closed" || closed.Proof != "job_empty" {
			t.Fatalf("actual complete closed temporary proof: %+v read=%v decode=%v", closed, tempErr, decodeErr)
		}
		after, readErr := os.ReadFile(marker)
		if readErr != nil || !bytes.Equal(after, before) {
			t.Fatalf("failed rename changed canonical proof: %q %v", after, readErr)
		}
		if _, err := e.Run(context.Background(), testRequest(t, "output", "1")); !errors.Is(err, ErrQuarantined) {
			t.Fatalf("same executor admitted after lost closed proof: %v", err)
		}
		if err := e.Close(); !errors.Is(err, ErrQuarantined) {
			t.Fatalf("Close lost quarantine: %v", err)
		}
		testQuarantinedReopen(t, e.root)
		after, readErr = os.ReadFile(marker)
		if readErr != nil || !bytes.Equal(after, before) {
			t.Fatalf("reopen trusted the complete temporary proof: %q %v", after, readErr)
		}
		t.Logf("native zero-active proof followed by sharing-blocked rename, native=%d; same-object rename works after releasing the hold; canonical unconfirmed and closed/job_empty temporary retained; explicit reopen quarantined", renameCode)
	})
}

func testClosedFenceSeed(t *testing.T) (string, runFence, []byte) {
	t.Helper()
	e := testExecutor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var held windows.Handle
	var f runFence
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase == "unconfirmed" {
			cancel()
		}
		if phase != "closed_fence" {
			return
		}
		f = r.fence
		name, err := windows.UTF16PtrFromString(fencePath(e.root, f.RunID))
		if err == nil {
			held, err = windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
		}
		observationErr = err
	}
	request := testRequest(t, "output", "1")
	request.Limits.StopGrace = 100 * time.Millisecond
	r, err := e.Run(ctx, request)
	if held != 0 {
		if err := windows.CloseHandle(held); err != nil {
			t.Fatal(err)
		}
	}
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	if !errors.Is(err, ErrCleanupPending) || r.State != Closed || r.ResourcesReusable || f.State != "closed" || f.Proof != "not_created" || f.ProfileCreated || !r.CreatedUTC.IsZero() {
		t.Fatalf("real written closed-fence seed: %+v fence=%+v error=%v", r, f, err)
	}
	if err := e.Close(); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("seed Close: %v", err)
	}
	data, err := os.ReadFile(fencePath(e.root, f.RunID))
	if err != nil {
		t.Fatal(err)
	}
	return e.root, f, data
}

func TestCorruptWrittenFenceCannotAuthorizeCleanup(t *testing.T) {
	t.Parallel()
	seed, original, data := testClosedFenceSeed(t)
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"unknown-format", func(v map[string]any) { v["format"] = 2 }},
		{"unknown-state", func(v map[string]any) { v["state"] = "future" }},
		{"wrong-root", func(v map[string]any) { v["root_id"] = "01900000-0000-7000-8000-000000000001" }},
		{"wrong-run", func(v map[string]any) { v["run_id"] = "01900000-0000-7000-8000-000000000002" }},
		{"wrong-profile", func(v map[string]any) { v["profile_name"] = "dwarf-harness-01900000-0000-7000-8000-000000000003" }},
		{"missing-key", func(v map[string]any) { delete(v, "profile_created") }},
		{"invalid-UUID", func(v map[string]any) { v["run_id"] = "not-a-uuid"; v["profile_name"] = "dwarf-harness-not-a-uuid" }},
		{"closed-without-proof", func(v map[string]any) { delete(v, "closed_proof") }},
		{"job-empty-without-created-profile", func(v map[string]any) { v["closed_proof"] = "job_empty" }},
		{"duplicate-key", nil}, {"partial-JSON", nil}, {"trailing-JSON", nil}, {"reparse-marker", nil}, {"directory-marker", nil}, {"hardlinked-marker", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.CopyFS(root, os.DirFS(seed)); err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := json.Unmarshal(data, &values); err != nil {
				t.Fatal(err)
			}
			if tc.change != nil {
				tc.change(values)
			}
			changed, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			runDir := filepath.Join(root, "runs", original.RunID)
			if tc.name == "invalid-UUID" {
				renamed := filepath.Join(root, "runs", "not-a-uuid")
				if err := os.Rename(runDir, renamed); err != nil {
					t.Fatal(err)
				}
				runDir = renamed
			}
			marker := filepath.Join(runDir, "fence.json")
			switch tc.name {
			case "duplicate-key":
				changed = append([]byte(`{"format":1,`), changed[1:]...)
			case "partial-JSON":
				changed = changed[:len(changed)/2]
			case "trailing-JSON":
				changed = append(changed, []byte(" {}")...)
			}
			if err := os.WriteFile(marker, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(runDir, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				t.Fatal(err)
			}
			canary := filepath.Join(work, "keep")
			if err := os.WriteFile(canary, []byte("uncertain-owned-data"), 0o600); err != nil {
				t.Fatal(err)
			}
			external := t.TempDir()
			externalCanary := filepath.Join(external, "keep")
			if err := os.WriteFile(externalCanary, []byte("unrelated-owner-data"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.name == "directory-marker" || tc.name == "reparse-marker" || tc.name == "hardlinked-marker" {
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				switch tc.name {
				case "directory-marker":
					if err := os.Mkdir(marker, 0o700); err != nil {
						t.Fatal(err)
					}
				case "reparse-marker":
					if err := testMakeJunction(marker, external); err != nil {
						t.Fatal(err)
					}
				case "hardlinked-marker":
					source := filepath.Join(external, "fence-source")
					if err := os.WriteFile(source, changed, 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(source, marker); err != nil {
						t.Fatal(err)
					}
				}
			}
			testQuarantinedReopen(t, root)
			for path, want := range map[string]string{canary: "uncertain-owned-data", externalCanary: "unrelated-owner-data"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("malformed private proof authorized deletion: path=%q bytes=%q error=%v", path, got, err)
				}
			}
			if tc.name != "directory-marker" && tc.name != "reparse-marker" {
				after, err := os.ReadFile(marker)
				if err != nil || !bytes.Equal(after, changed) {
					t.Fatalf("malformed canonical proof changed: %q %v", after, err)
				}
			}
		})
	}
}

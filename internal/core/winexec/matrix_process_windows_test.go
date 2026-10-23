package winexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type descendantProbe struct {
	PID                               uint32
	Protected                         protectedProbe
	BreakawayCode, OutsideProcessCode uint32
}

type descendantReport struct {
	Descendants []descendantProbe
}

func payloadCommand(t *testing.T, operation string, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments := append([]string{"-test.run=^TestWinexecPayload$", "--", "--winexec-payload", operation}, args...)
	command := exec.Command(executable, arguments...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	return command
}

func payloadWaitFile(t *testing.T, name string, guard time.Duration, accept func([]byte) bool) []byte {
	t.Helper()
	deadline := time.NewTimer(guard)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(os.Getenv("DWARF_WORK"), name))
		if err == nil && accept(data) {
			return data
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("native work-file barrier %q did not arrive", name)
		}
	}
}

func payloadReleaseFile(t *testing.T, name string) {
	t.Helper()
	payloadWaitFile(t, name, 10*time.Second, func(data []byte) bool { return string(data) == "release-nonce" })
}

func payloadDescendantRoot(t *testing.T, args []string) {
	t.Helper()
	if len(args) != 2 {
		t.Fatal("missing descendant owner pipe/Core PID")
	}
	var self windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.CurrentProcess(), windows.CurrentProcess(), &self, windows.SYNCHRONIZE, true, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(self)
	command := payloadCommand(t, "descendant-child", args[0], args[1], strconv.FormatUint(uint64(self), 10))
	command.SysProcAttr.AdditionalInheritedHandles = []syscall.Handle{syscall.Handle(self)}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct{ PID int }{command.Process.Pid}); err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Release(); err != nil {
		t.Fatal(err)
	}
	// The behavioral child and grandchild remain Go helpers. Retire this root
	// before starting the grandchild; held-tree cases use the cmd fixture.
	windows.ExitProcess(0)
}

func payloadDescendantChild(t *testing.T, args []string) {
	t.Helper()
	if len(args) != 3 {
		t.Fatal("missing descendant arguments")
	}
	raw, err := strconv.ParseUint(args[2], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	parent := windows.Handle(raw)
	state, waitErr := windows.WaitForSingleObject(parent, 10000)
	closeErr := windows.CloseHandle(parent)
	if waitErr != nil || closeErr != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("actual root synchronization: wait=%d errors=%v/%v", state, waitErr, closeErr)
	}
	// The host releases this only after also closing its exited-root handles.
	payloadReleaseFile(t, "root-closed")
	own := payloadDescendantProbe(t, args[:2])
	command := payloadCommand(t, "descendant-grandchild", args[:2]...)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	var grandchild descendantProbe
	payloadWaitFile(t, "grandchild-proof.json", 10*time.Second, func(data []byte) bool { return json.Unmarshal(data, &grandchild) == nil })
	if grandchild.PID != uint32(command.Process.Pid) {
		t.Fatal("grandchild report is not the actual spawned process")
	}
	if err := json.NewEncoder(os.Stdout).Encode(descendantReport{[]descendantProbe{own, grandchild}}); err != nil {
		t.Fatal(err)
	}
	payloadReleaseFile(t, "release")
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	windows.ExitProcess(0)
}

func payloadDescendantGrandchild(t *testing.T, args []string) {
	t.Helper()
	probe := payloadDescendantProbe(t, args)
	data, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("DWARF_WORK"), "grandchild-proof.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	payloadReleaseFile(t, "release")
	windows.ExitProcess(0)
}

func payloadDescendantProbe(t *testing.T, args []string) descendantProbe {
	t.Helper()
	if len(args) != 2 {
		t.Fatal("missing protected targets/Core PID")
	}
	probe := descendantProbe{PID: windows.GetCurrentProcessId(), Protected: payloadProtectedProbe(t, args[0])}
	outside, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE|windows.PROCESS_VM_READ, false, uint32(outside))
	if err == nil {
		_ = windows.CloseHandle(handle)
	} else {
		var code syscall.Errno
		if !errors.As(err, &code) {
			t.Fatal(err)
		}
		probe.OutsideProcessCode = uint32(code)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	makeCommand := func(flags uint32) *exec.Cmd {
		command := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/q", "/c", "exit", "0")
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | flags}
		return command
	}
	if err := makeCommand(0).Run(); err != nil {
		t.Fatalf("ordinary native spawn positive control: %v", err)
	}
	escape := makeCommand(windows.CREATE_BREAKAWAY_FROM_JOB)
	if err := escape.Start(); err == nil {
		_ = escape.Wait()
	} else {
		var code syscall.Errno
		if !errors.As(err, &code) {
			t.Fatal(err)
		}
		probe.BreakawayCode = uint32(code)
	}
	return probe
}

func testJobPIDs(job windows.Handle) ([]uint32, error) {
	var list struct {
		Assigned, Count uint32
		IDs             [16]uintptr
	}
	if err := queryJob(job, windows.JobObjectBasicProcessIdList, unsafe.Pointer(&list), unsafe.Sizeof(list)); err != nil {
		return nil, err
	}
	if list.Count > uint32(len(list.IDs)) || list.Assigned != list.Count {
		return nil, fmt.Errorf("incomplete native Job PID list: %+v", list)
	}
	pids := make([]uint32, list.Count)
	for i := range pids {
		if list.IDs[i] == 0 || uint64(list.IDs[i]) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("invalid native Job PID %d", list.IDs[i])
		}
		pids[i] = uint32(list.IDs[i])
	}
	return pids, nil
}

func testNativeParent(pid uint32) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == pid {
			return entry.ParentProcessID, nil
		}
	}
	return 0, fmt.Errorf("live native process %d absent from snapshot", pid)
}

func testRunDescendants(t *testing.T, e *Executor, request Request, pipe string) (Receipt, []protectedProbe, error) {
	t.Helper()
	outside, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE|windows.PROCESS_VM_READ, false, windows.GetCurrentProcessId())
	if err != nil {
		t.Fatalf("host process-handle positive control: %v", err)
	}
	if err := windows.CloseHandle(outside); err != nil {
		t.Fatal(err)
	}
	request.Args = []string{"-test.run=^TestWinexecPayload$", "--", "--winexec-payload", "descendant-root", pipe, strconv.FormatUint(uint64(windows.GetCurrentProcessId()), 10)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var report descendantReport
	var observationErr error
	var observedRoot bool
	e.observe = func(phase string, r *nativeRun) {
		if phase == "suspended_created" {
			observationErr = processInJob(r.pi.Process, r.job)
			observedRoot = observationErr == nil
			if observationErr != nil {
				// A missing JOB_LIST mutation must not leave an unassigned,
				// suspended real root behind the test's failed observation.
				observationErr = errors.Join(observationErr, windows.TerminateProcess(r.pi.Process, 1))
				cancel()
			}
		}
		if phase != "root_exited" || observationErr != nil {
			return
		}
		observationErr = func() error {
			if err := os.WriteFile(filepath.Join(r.dir, "work", "root-closed"), []byte("release-nonce"), 0o600); err != nil {
				return err
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for len(report.Descendants) != 2 {
				out, stderr := r.pipes.output()
				for _, line := range strings.Split(string(out), "\n") {
					var candidate descendantReport
					if json.Unmarshal([]byte(line), &candidate) == nil && len(candidate.Descendants) == 2 {
						report = candidate
					}
				}
				if len(report.Descendants) == 2 {
					break
				}
				select {
				case <-r.pipes.event:
				case <-deadline.C:
					accounting, accountErr := jobAccounting(r.job)
					return fmt.Errorf("behavioral descendants not ready: stdout=%q stderr=%q Job=%+v query=%v", out, stderr, accounting, accountErr)
				}
			}
			pids, err := testJobPIDs(r.job)
			if err != nil || len(pids) != 2 {
				return fmt.Errorf("actual descendant Job members=%v: %v", pids, err)
			}
			parent := r.pi.ProcessId
			for _, probe := range report.Descendants {
				member := false
				for _, pid := range pids {
					member = member || pid == probe.PID
				}
				if !member {
					return fmt.Errorf("reported descendant %d absent from native Job %v", probe.PID, pids)
				}
				h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, probe.PID)
				if err != nil {
					return err
				}
				state, waitErr := windows.WaitForSingleObject(h, 0)
				memberErr := processInJob(h, r.job)
				closeErr := windows.CloseHandle(h)
				if waitErr != nil || memberErr != nil || closeErr != nil || state != uint32(windows.WAIT_TIMEOUT) {
					return fmt.Errorf("live descendant %d native state=%d: %v", probe.PID, state, errors.Join(waitErr, memberErr, closeErr))
				}
				actualParent, err := testNativeParent(probe.PID)
				if err != nil || actualParent != parent {
					return fmt.Errorf("native ancestry PID=%d parent=%d expected=%d: %v", probe.PID, actualParent, parent, err)
				}
				if probe.BreakawayCode != uint32(windows.ERROR_ACCESS_DENIED) || probe.OutsideProcessCode != uint32(windows.ERROR_ACCESS_DENIED) {
					return fmt.Errorf("descendant authority escaped: %+v", probe)
				}
				parent = probe.PID
			}
			return os.WriteFile(filepath.Join(r.dir, "work", "release"), []byte("release-nonce"), 0o600)
		}()
		if observationErr != nil {
			cancel()
		}
	}
	receipt, runErr := e.Run(ctx, request)
	if observationErr != nil || !observedRoot || len(report.Descendants) != 2 {
		t.Fatalf("native descendant observation: root=%t report=%+v error=%v; receipt=%+v run=%v", observedRoot, report, observationErr, receipt, runErr)
	}
	t.Logf("actual root, child and grandchild Job membership and native ancestry verified; descendant breakaway/outside-process errors=%d/%d,%d/%d", report.Descendants[0].BreakawayCode, report.Descendants[0].OutsideProcessCode, report.Descendants[1].BreakawayCode, report.Descendants[1].OutsideProcessCode)
	return receipt, []protectedProbe{report.Descendants[0].Protected, report.Descendants[1].Protected}, runErr
}

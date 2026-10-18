package winexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type supervisorFrame struct {
	Phase            string
	Fence            runFence
	RootPID          uint32
	PIDs             []uint32
	Job              uint64
	Active           uint32
	AccountingKnown  bool
	Receipt          Receipt
	Error, NativeAPI string
	NativeCode       uint32
}

type supervisorProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	stderr  bytes.Buffer
	process windows.Handle
	waited  bool
	cancel  context.CancelFunc
}

func testStartSupervisor(t *testing.T, root, mode, phase string) *supervisorProcess {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	child := &supervisorProcess{cancel: cancel}
	child.command = exec.CommandContext(ctx, executable, "-test.run=^TestWinexecSupervisor$", "-test.timeout=35s", "--", "--winexec-supervisor", root, mode, phase)
	child.command.Stderr = &child.stderr
	child.input, err = child.command.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := child.command.StdoutPipe()
	if err != nil {
		_ = child.input.Close()
		cancel()
		t.Fatal(err)
	}
	child.output = bufio.NewReaderSize(output, 64<<10)
	if err := child.command.Start(); err != nil {
		_ = child.input.Close()
		_ = output.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { testStopSupervisor(t, child) })
	child.process, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_DUP_HANDLE|windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(child.command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	frame := testReadSupervisor(t, child)
	if frame.Phase != "supervisor-ready" {
		t.Fatalf("supervisor failed before admission: %+v", frame)
	}
	return child
}

func testStopSupervisor(t *testing.T, child *supervisorProcess) {
	t.Helper()
	if child == nil {
		return
	}
	if !child.waited {
		_ = child.command.Process.Kill()
		_ = child.command.Wait()
		child.waited = true
	}
	if child.process != 0 {
		state, err := windows.WaitForSingleObject(child.process, 10000)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Errorf("supervisor did not actually exit: wait=%d error=%v", state, err)
		}
		if err := windows.CloseHandle(child.process); err != nil {
			t.Error(err)
		}
		child.process = 0
	}
	_ = child.input.Close()
	child.cancel()
}

func testReadSupervisor(t *testing.T, child *supervisorProcess) supervisorFrame {
	t.Helper()
	type result struct {
		frame supervisorFrame
		err   error
	}
	done := make(chan result, 1)
	go func() {
		line, err := child.output.ReadSlice('\n')
		var frame supervisorFrame
		if err == nil {
			err = json.Unmarshal(line, &frame)
		}
		done <- result{frame, err}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			testStopSupervisor(t, child)
			t.Fatalf("bounded supervisor handshake: %v; stderr=%s", value.err, child.stderr.String())
		}
		return value.frame
	case <-time.After(15 * time.Second):
		testStopSupervisor(t, child)
		t.Fatalf("supervisor handshake guard expired; stderr=%s", child.stderr.String())
		return supervisorFrame{}
	}
}

func testReleaseSupervisor(t *testing.T, child *supervisorProcess) {
	t.Helper()
	if _, err := io.WriteString(child.input, "continue\n"); err != nil {
		t.Fatal(err)
	}
}

func testSupervisorBarrier(reader *bufio.Reader, frame supervisorFrame) error {
	if err := json.NewEncoder(os.Stdout).Encode(frame); err != nil {
		return err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if line != "continue\n" {
		return fmt.Errorf("invalid supervisor control input")
	}
	return nil
}

func testRunFrame(phase string, r *nativeRun) (supervisorFrame, error) {
	frame := supervisorFrame{Phase: phase, Fence: r.fence, RootPID: r.pi.ProcessId, Job: uint64(r.job), Receipt: r.receipt}
	if r.pipes != nil {
		frame.Receipt.Stdout, frame.Receipt.Stderr = r.pipes.output()
	}
	if r.job != 0 {
		accounting, err := jobAccounting(r.job)
		if err != nil {
			return frame, err
		}
		frame.Active = accounting.Active
		frame.AccountingKnown = true
		frame.PIDs, err = testJobPIDs(r.job)
		if err != nil {
			return frame, err
		}
	}
	return frame, nil
}

func testWaitLiveJob(r *nativeRun, count int) ([]uint32, error) {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		pids, err := testJobPIDs(r.job)
		if err != nil {
			return nil, err
		}
		if len(pids) == count {
			for _, pid := range pids {
				h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
				if err != nil {
					return nil, err
				}
				state, waitErr := windows.WaitForSingleObject(h, 0)
				memberErr := processInJob(h, r.job)
				closeErr := windows.CloseHandle(h)
				if waitErr != nil || memberErr != nil || closeErr != nil || state != uint32(windows.WAIT_TIMEOUT) {
					return nil, fmt.Errorf("Job process %d is not live: wait=%d %v", pid, state, errors.Join(waitErr, memberErr, closeErr))
				}
			}
			return pids, nil
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			out, stderr := r.pipes.output()
			return nil, fmt.Errorf("expected %d live native Job members, got %v: stdout=%q stderr=%q", count, pids, out, stderr)
		}
	}
}

func testWaitHeldTree(r *nativeRun) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		out, stderr := r.pipes.output()
		// Both Go actors must finish their checked stdout writes before
		// supervisor loss disconnects stdio. Native existence alone races startup.
		if bytes.Count(out, []byte("held-ready\n")) == 2 {
			_, err := testWaitLiveJob(r, 3)
			return err
		}
		select {
		case <-r.pipes.event:
		case <-deadline.C:
			return fmt.Errorf("held root and child did not acknowledge readiness: stdout=%q stderr=%q", out, stderr)
		}
	}
}

func TestWinexecSupervisor(t *testing.T) {
	t.Parallel()
	marker := -1
	for i, arg := range os.Args {
		if arg == "--winexec-supervisor" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	if len(args) != 3 {
		t.Fatal("missing supervisor root/mode/phase")
	}
	root, mode, target := args[0], args[1], args[2]
	e, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	reader := bufio.NewReader(os.Stdin)
	if err := testSupervisorBarrier(reader, supervisorFrame{Phase: "supervisor-ready"}); err != nil {
		t.Fatal(err)
	}
	request := testRequest(t, "output", "1")
	if mode == "outer-count" {
		request = testRequest(t, "process-count")
		request.Limits.Processes = 2
	}
	if mode == "ambiguous" || (mode == "crash" && (target == "resumed" || target == "run_removed")) {
		request = testRequest(t, "held-tree")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var last supervisorFrame
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if observationErr != nil {
			return
		}
		if phase == "resumed" && (mode == "ambiguous" || (mode == "crash" && (target == "resumed" || target == "run_removed"))) {
			if err := testWaitHeldTree(r); err != nil {
				observationErr = err
				cancel()
				return
			}
		}
		if phase == "resumed" && mode == "outer-count" {
			observationErr = testWaitProcessCountProbe(r)
			if observationErr != nil {
				cancel()
				return
			}
			phase = "count-ready"
		}
		last, observationErr = testRunFrame(phase, r)
		if observationErr != nil {
			cancel()
			return
		}
		pause := (mode == "outer-count" && phase == "count-ready") || ((mode == "crash" || mode == "ambiguous") && phase == target)
		if mode == "crash" && target == "run_removed" && phase == "resumed" {
			last.Phase = "running-before-stop"
			observationErr = testSupervisorBarrier(reader, last)
			cancel()
			return
		}
		if pause {
			observationErr = testSupervisorBarrier(reader, last)
			if mode == "outer-count" && observationErr == nil {
				observationErr = os.WriteFile(filepath.Join(r.dir, "work", "release"), []byte("release-nonce"), 0o600)
			}
			if observationErr != nil {
				cancel()
			}
		}
	}
	receipt, runErr := e.Run(ctx, request)
	closeErr := e.Close()
	last.Phase, last.Receipt = "done", receipt
	if err := errors.Join(runErr, observationErr, closeErr); err != nil {
		last.Error = err.Error()
	}
	var native *NativeError
	if errors.As(runErr, &native) {
		last.NativeAPI, last.NativeCode = native.Stage, native.Code
	}
	if err := json.NewEncoder(os.Stdout).Encode(last); err != nil {
		t.Fatal(err)
	}
}

type processCountProbe struct {
	PID       uint32
	SpawnCode uint32
}

func payloadProcessCount(t *testing.T) {
	t.Helper()
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	child := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/q", "/c", "set /p x=")
	child.Stdin, child.Stdout, child.Stderr = read, os.Stdout, os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	extra := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/q", "/c", "exit", "0")
	extra.Stdin, extra.Stdout, extra.Stderr = os.Stdin, os.Stdout, os.Stderr
	extra.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	probe := processCountProbe{PID: uint32(child.Process.Pid)}
	if err := extra.Start(); err == nil {
		_ = extra.Wait()
	} else {
		var code syscall.Errno
		if !errors.As(err, &code) {
			t.Fatal(err)
		}
		probe.SpawnCode = uint32(code)
	}
	if err := json.NewEncoder(os.Stdout).Encode(probe); err != nil {
		t.Fatal(err)
	}
	payloadReleaseFile(t, "release")
	if _, err := io.WriteString(write, "released\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	windows.ExitProcess(0)
}

func testWaitProcessCountProbe(r *nativeRun) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		out, stderr := r.pipes.output()
		var probe processCountProbe
		if json.Unmarshal(bytes.TrimSpace(out), &probe) == nil && probe.PID != 0 {
			if probe.SpawnCode != uint32(windows.ERROR_NOT_ENOUGH_QUOTA) {
				return fmt.Errorf("extra actual spawn was not quota denied: %+v", probe)
			}
			pids, err := testWaitLiveJob(r, 2)
			if err != nil {
				return err
			}
			for _, pid := range pids {
				if pid == probe.PID {
					return nil
				}
			}
			return fmt.Errorf("held child %d absent from actual Job %v", probe.PID, pids)
		}
		select {
		case <-r.pipes.event:
		case <-deadline.C:
			return fmt.Errorf("process-count payload not ready: stdout=%q stderr=%q", out, stderr)
		}
	}
}

func payloadHeldTree(t *testing.T) {
	t.Helper()
	child := payloadCommand(t, "held-tree-child")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct{ PID int }{child.Process.Pid}); err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Release(); err != nil {
		t.Fatal(err)
	}
	payloadHold(t)
}

func testOuterJob(t *testing.T, count uint32, breakaway bool) windows.Handle {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pids, err := testJobPIDs(job)
		if err != nil {
			t.Error(err)
		}
		var handles []windows.Handle
		for _, pid := range pids {
			h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
			if err == nil {
				handles = append(handles, h)
			} else if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				t.Error(err)
			}
		}
		if err := windows.TerminateJobObject(job, 1); err != nil {
			t.Error(err)
		}
		testWaitAndCloseProcesses(t, &handles)
		accounting, err := jobAccounting(job)
		if err != nil || accounting.Active != 0 {
			t.Errorf("outer Job teardown did not prove zero active processes: %+v %v", accounting, err)
		}
		if err := windows.CloseHandle(job); err != nil {
			t.Error(err)
		}
	})
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	if breakaway {
		limits.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	}
	limits.BasicLimitInformation.ActiveProcessLimit = count
	if err := setJob(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestNativeProcessLimitAndHostJobNesting(t *testing.T) {
	t.Parallel()
	outer := testOuterJob(t, 4, false)
	child := testStartSupervisor(t, t.TempDir(), "outer-count", "")
	var handles []windows.Handle
	t.Cleanup(func() {
		testStopSupervisor(t, child)
		testWaitAndCloseProcesses(t, &handles)
	})
	if err := windows.AssignProcessToJobObject(outer, child.process); err != nil {
		t.Fatal(err)
	}
	testReleaseSupervisor(t, child)
	frame := testReadSupervisor(t, child)
	if !testObserveFrameProcesses(t, frame, &handles) {
		t.FailNow()
	}
	if frame.Phase != "count-ready" || frame.Active != 2 || len(frame.PIDs) != 2 {
		t.Fatalf("per-run limit was not exercised: %+v", frame)
	}
	pids, err := testJobPIDs(outer)
	if err != nil || len(pids) != 3 {
		t.Fatalf("outer Job did not retain supervisor/root/child: %v %v", pids, err)
	}
	for _, pid := range append(frame.PIDs, uint32(child.command.Process.Pid)) {
		found := false
		for _, member := range pids {
			found = found || member == pid
		}
		if !found {
			t.Fatalf("process %d escaped outer Job %v", pid, pids)
		}
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(outer, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		t.Fatal(err)
	}
	if limits.BasicLimitInformation.ActiveProcessLimit != 4 || limits.BasicLimitInformation.LimitFlags != windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE|windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS {
		t.Fatalf("outer constraints were changed: %+v", limits)
	}
	for len(handles) != 0 {
		h := handles[len(handles)-1]
		handles = handles[:len(handles)-1]
		if err := windows.CloseHandle(h); err != nil {
			t.Fatal(err)
		}
	}
	testReleaseSupervisor(t, child)
	done := testReadSupervisor(t, child)
	if done.Phase != "done" || done.Error != "" {
		t.Fatalf("nested run did not finish: %+v", done)
	}
	assertReusable(t, done.Receipt, nil)
	if err := child.command.Wait(); err != nil {
		child.waited = true
		t.Fatalf("supervisor exit: %v: %s", err, child.stderr.String())
	}
	child.waited = true
	t.Logf("native per-run active limit=2 denied extra spawn with %d while outer active=3 below its unchanged limit=4", windows.ERROR_NOT_ENOUGH_QUOTA)
}

func TestRequiredEnforcementUnavailable(t *testing.T) {
	t.Parallel()
	outer := testOuterJob(t, 1, true)
	child := testStartSupervisor(t, t.TempDir(), "outer-denied", "")
	if err := windows.AssignProcessToJobObject(outer, child.process); err != nil {
		t.Fatal(err)
	}
	testReleaseSupervisor(t, child)
	done := testReadSupervisor(t, child)
	r := done.Receipt
	if done.Phase != "done" || done.NativeCode != uint32(windows.ERROR_NOT_ENOUGH_QUOTA) || done.NativeAPI != "CreateProcess" || !r.CreatedUTC.IsZero() || !r.ResumedUTC.IsZero() || len(r.Stdout)+len(r.Stderr) != 0 || r.OutputState != Unpublishable || r.State != Closed || !r.ResourcesReusable {
		t.Fatalf("outer limit was bypassed or refusal was not safely closed: %+v", done)
	}
	if err := child.command.Wait(); err != nil {
		child.waited = true
		t.Fatalf("supervisor exit: %v: %s", err, child.stderr.String())
	}
	child.waited = true
	t.Logf("real outer active limit=1 already occupied; breakaway permitted by outer but never requested: %s native=%d, no creation/resume/output", done.NativeAPI, done.NativeCode)
}

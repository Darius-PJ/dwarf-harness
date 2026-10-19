package winexec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type runResult struct {
	receipt Receipt
	err     error
}

func awaitResult(t *testing.T, ch <-chan runResult) runResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(30 * time.Second):
		t.Fatal("executor did not finish")
		return runResult{}
	}
}
func TestPermittedInputAndBoundedOutput(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	request, protected, ambient := testExchange(t)
	var profile string
	var inputErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase == "profile_bound" {
			profile = r.profile.Path
		}
		if phase == "tree_exited" {
			b, err := os.ReadFile(filepath.Join(r.dir, "input", "nonce"))
			if err != nil || string(b) != "authorized-nonce" {
				inputErr = fmt.Errorf("sealed input %q: %w", b, err)
			}
		}
	}
	receipt, err := e.Run(context.Background(), request)
	assertReusable(t, receipt, err)
	if inputErr != nil {
		t.Fatal(inputErr)
	}
	digest := sha256.Sum256([]byte("authorized-noncestdin-nonce"))
	if !strings.Contains(string(receipt.Stdout), fmt.Sprintf("digest=%x\n", digest)) || string(receipt.Stderr) != "stderr-nonce\n" {
		t.Fatalf("exchange stdout=%q stderr=%q", receipt.Stdout, receipt.Stderr)
	}
	var temp string
	for _, line := range strings.Split(string(receipt.Stdout), "\n") {
		if strings.HasPrefix(line, "temp=") {
			temp = strings.TrimPrefix(line, "temp=")
		}
	}
	rel, err := filepath.Rel(profile, temp)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		t.Fatalf("TEMP %q not within bound profile %q: %v", temp, profile, err)
	}
	for path, want := range map[string]string{protected: "protected-nonce", ambient: "host-localappdata-nonce"} {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want {
			t.Fatalf("protected target changed: %q %v", b, err)
		}
	}
	if receipt.Usage.TotalProcesses != 1 || receipt.CreatedUTC.IsZero() || receipt.ResumedUTC.IsZero() || receipt.ClosedUTC.IsZero() {
		t.Fatalf("native process facts: %+v", receipt)
	}
	before := sha256.Sum256(append(append([]byte(nil), receipt.Stdout...), receipt.Stderr...))
	e.observe = nil
	next, nextErr := e.Run(context.Background(), testRequest(t, "output", "1"))
	assertReusable(t, next, nextErr)
	if next.RunID == receipt.RunID || next.ProfileSID == receipt.ProfileSID || next.RootID != receipt.RootID || string(next.Stdout) != "o" || string(next.Stderr) != "e" {
		t.Fatalf("run isolation: %+v", next)
	}
	after := sha256.Sum256(append(append([]byte(nil), receipt.Stdout...), receipt.Stderr...))
	if before != after {
		t.Fatal("later run mutated returned output")
	}
	t.Logf("label-only writable work/profile, profile=%s; native closure, accounting, immutable bounded exchange and fresh reuse", profile)
}
func TestNaturalExitKeepsStopGrace(t *testing.T) {
	t.Parallel()
	for _, late := range []string{"wall", "cancel", "close"} {
		t.Run(late, func(t *testing.T) {
			t.Parallel()
			e := testExecutor(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := testRequest(t, "output", "1")
			if late == "wall" {
				// Cover native setup, then retain drain time after the real wall.
				request.Limits.Wall = 10 * time.Second
				request.Limits.StopGrace = 15 * time.Second
			}
			observed := false
			var closeResult chan error
			e.observe = func(phase string, r *nativeRun) {
				if phase != "tree_exited" {
					return
				}
				observed = true
				t.Logf("NATURAL_EXIT measurement late=%s elapsed=%s wallRemaining=%s created=%s resumed=%s cause=%d exit=%#x stopAt=%s", late, time.Since(r.admitted), time.Until(r.wallEnd), r.receipt.CreatedUTC, r.receipt.ResumedUTC, r.cause, r.receipt.ExitCode, r.stopAt)
				switch late {
				case "wall":
					timer := time.NewTimer(time.Until(r.wallEnd))
					defer timer.Stop()
					<-timer.C
				case "cancel":
					cancel()
				case "close":
					closeResult = make(chan error, 1)
					go func() { closeResult <- e.Close() }()
					<-r.stop
				}
			}
			r, err := e.Run(ctx, request)
			t.Logf("NATURAL_RESULT measurement late=%s receipt=%+v error=%v", late, r, err)
			if closeResult != nil {
				if closeErr := <-closeResult; closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if !observed {
				t.Fatal("natural tree-exit boundary was not observed")
			}
			assertReusable(t, r, err)
			if string(r.Stdout) != "o" || string(r.Stderr) != "e" {
				t.Fatalf("natural output was not sealed: stdout=%q stderr=%q", r.Stdout, r.Stderr)
			}
		})
	}
}

func TestNativeUserCPULimit(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	request := testRequest(t, "cpu")
	request.Limits.UserCPU = 2 * time.Second
	receipt, err := e.Run(context.Background(), request)
	quota := windows.NTStatus(receipt.ExitCode).Errno() == windows.ERROR_NOT_ENOUGH_QUOTA || receipt.ExitCode == uint32(windows.ERROR_NOT_ENOUGH_QUOTA)
	if !errors.Is(err, ErrProcessExit) || receipt.Outcome != Exited || !receipt.ExitCodeKnown || !quota || !receipt.TreeExited || !receipt.ResourcesReusable || !receipt.UsageKnown || receipt.Usage.User100ns < uint64(request.Limits.UserCPU/(100*time.Nanosecond)) || !strings.Contains(string(receipt.Stdout), "cpu-ready") {
		t.Fatalf("native quota result=%+v error=%v output=%q", receipt, err, receipt.Stdout)
	}
	t.Logf("native quota raw=0x%08x mapped=%v Job-user=%d configured=%d; Job-zero before wall guard", receipt.ExitCode, windows.NTStatus(receipt.ExitCode).Errno(), receipt.Usage.User100ns, uint64(request.Limits.UserCPU/(100*time.Nanosecond)))
}
func descendantPID(r *nativeRun) (uint32, error) {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		out, _ := r.pipes.output()
		for _, line := range strings.Split(string(out), "\n") {
			var value struct{ PID uint32 }
			if json.Unmarshal([]byte(line), &value) == nil && value.PID != 0 {
				return value.PID, nil
			}
		}
		select {
		case <-r.pipes.event:
		case <-deadline.C:
			return 0, fmt.Errorf("descendant PID missing: stdout=%q", out)
		}
	}
}
func liveHeldDescendant(r *nativeRun) error {
	var list struct {
		Assigned, Count uint32
		IDs             [4]uintptr
	}
	if err := queryJob(r.job, windows.JobObjectBasicProcessIdList, unsafe.Pointer(&list), unsafe.Sizeof(list)); err != nil {
		return err
	}
	if list.Count != 1 || list.IDs[0] == 0 || list.IDs[0] == uintptr(r.pi.ProcessId) {
		return fmt.Errorf("held descendant Job PID list: %+v", list)
	}
	a, err := jobAccounting(r.job)
	if err != nil {
		return err
	}
	if a.Active != 1 {
		return fmt.Errorf("held descendant active count: %d", a.Active)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(list.IDs[0]))
	if err != nil {
		return err
	}
	state, waitErr := windows.WaitForSingleObject(h, 0)
	err = errors.Join(waitErr, processInJob(h, r.job), windows.CloseHandle(h))
	if state != uint32(windows.WAIT_TIMEOUT) {
		err = errors.Join(err, fmt.Errorf("held descendant is not live: wait=%d", state))
	}
	return err
}

func TestJobZeroWithObserverHandle(t *testing.T) {
	t.Parallel()
	for _, natural := range []bool{false, true} {
		name := "terminate"
		if natural {
			name = "natural"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := testExecutor(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type observation struct {
				process, job windows.Handle
				work         string
				err          error
			}
			observed := make(chan observation, 1)
			release := make(chan struct{})
			e.observe = func(phase string, r *nativeRun) {
				if phase != "root_exited" {
					return
				}
				pid, err := descendantPID(r)
				var process, job windows.Handle
				if err == nil {
					process, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
				}
				if err == nil {
					self := windows.CurrentProcess()
					err = windows.DuplicateHandle(self, r.job, self, &job, 0, false, windows.DUPLICATE_SAME_ACCESS)
				}
				observed <- observation{process, job, filepath.Join(r.dir, "work"), err}
				<-release
			}
			result := make(chan runResult, 1)
			operation := "descendant"
			if natural {
				operation = "descendant-natural"
			}
			request := testRequest(t, operation)
			go func() { r, err := e.Run(ctx, request); result <- runResult{r, err} }()
			var observedRun observation
			select {
			case observedRun = <-observed:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("root exit was not observed")
			}
			process := observedRun.process
			defer func() {
				if process != 0 {
					_ = windows.CloseHandle(process)
				}
				if observedRun.job != 0 {
					_ = windows.CloseHandle(observedRun.job)
				}
			}()
			if observedRun.err != nil {
				close(release)
				t.Fatal(observedRun.err)
			}
			if err := processInJob(process, observedRun.job); err != nil {
				close(release)
				t.Fatal(err)
			}
			if natural {
				if err := os.WriteFile(filepath.Join(observedRun.work, "release"), []byte("release-nonce"), 0o600); err != nil {
					close(release)
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			close(release)
			state, err := windows.WaitForSingleObject(process, 3000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("descendant exit: %d %v", state, err)
			}
			accounting, err := jobAccounting(observedRun.job)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("signaled descendant with observer open: ActiveProcesses=%d", accounting.Active)
			if err := windows.CloseHandle(process); err != nil {
				t.Fatal(err)
			}
			process = 0
			got := awaitResult(t, result)
			if natural {
				assertReusable(t, got.receipt, got.err)
			} else if !errors.Is(got.err, context.Canceled) || got.receipt.State != Closed || got.receipt.Outcome != Canceled || !got.receipt.TreeExited || !got.receipt.ResourcesReusable || got.receipt.OutputState != Unpublishable {
				t.Fatalf("result=%+v error=%v", got.receipt, got.err)
			}
			accounting, err = jobAccounting(observedRun.job)
			if err != nil || accounting.Active != 0 {
				t.Fatalf("observer closed: ActiveProcesses=%d error=%v", accounting.Active, err)
			}
		})
	}
}
func TestCancelWaitsForEveryDescendant(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan error, 1)
	var liveErr error
	notified := false
	e.observe = func(phase string, r *nativeRun) {
		if phase == "root_exited" {
			liveErr = liveHeldDescendant(r)
		}
		if phase == "job_nonempty" && !notified {
			done, _, _ := r.pipes.snapshot()
			if done == 3 {
				notified = true
				ready <- liveErr
			}
		}
	}
	request := testRequest(t, "descendant-quiet")
	result := make(chan runResult, 1)
	go func() { r, err := e.Run(ctx, request); result <- runResult{r, err} }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case early := <-result:
		t.Fatalf("root exit and stdio EOF were mistaken for tree closure: %+v %v", early.receipt, early.err)
	case <-time.After(10 * time.Second):
		t.Fatal("descendant was not observed")
	}
	if r, err := e.Run(context.Background(), request); !errors.Is(err, ErrBusy) || r.State != NotAdmitted || r.RunID != (Receipt{}).RunID {
		t.Fatalf("overlap admitted: %+v %v", r, err)
	}
	cancel()
	got := awaitResult(t, result)
	if !errors.Is(got.err, context.Canceled) || got.receipt.State != Closed || !got.receipt.RootExited || !got.receipt.TreeExited || !got.receipt.ResourcesReusable || got.receipt.OutputState != Unpublishable {
		t.Fatalf("cancellation: %+v %v", got.receipt, got.err)
	}
	e.observe = nil
	r, err := e.Run(context.Background(), testRequest(t, "output", "1"))
	assertReusable(t, r, err)
}
func TestExitCodeDoesNotProveClosure(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	r, err := e.Run(context.Background(), testRequest(t, "exit259"))
	if !errors.Is(err, ErrProcessExit) || r.Outcome != Exited || !r.ExitCodeKnown || r.ExitCode != 259 || !r.RootExited || !r.TreeExited || !r.ResourcesReusable || r.OutputState != CompleteSealed {
		t.Fatalf("signaled exit 259: %+v %v", r, err)
	}
}
func TestConcurrentRunIsBusy(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	ready := make(chan struct{})
	release := make(chan struct{})
	e.observe = func(phase string, _ *nativeRun) {
		if phase == "suspended_verified" {
			close(ready)
			<-release
		}
	}
	request := testRequest(t, "held")
	result := make(chan runResult, 1)
	go func() { r, err := e.Run(context.Background(), request); result <- runResult{r, err} }()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("suspended barrier not reached")
	}
	if r, err := e.Run(context.Background(), request); !errors.Is(err, ErrBusy) || r.RunID != (Receipt{}).RunID {
		close(release)
		t.Fatalf("overlap result=%+v error=%v", r, err)
	}
	closed := make(chan error, 1)
	go func() { closed <- e.Close() }()
	// Wait for Close's actual admission fence, not an elapsed delay.
	e.mu.Lock()
	stop := e.active.stop
	e.mu.Unlock()
	<-stop
	close(release)
	got := awaitResult(t, result)
	if !errors.Is(got.err, ErrClosed) || got.receipt.Outcome != Shutdown || !got.receipt.TreeExited || !got.receipt.ResourcesReusable || !got.receipt.ResumedUTC.IsZero() {
		t.Fatalf("Close before resume: %+v %v", got.receipt, got.err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(context.Background(), request); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed admission: %v", err)
	}
}

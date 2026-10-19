package winexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var resourceMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// PROCESS_MEMORY_COUNTERS, following ledgerstore/peakmem_windows_test.go.
// These are process-scoped working-set and pagefile-commit counters.
type resourceMemoryCounters struct {
	Size, PageFaults               uint32
	PeakWorkingSet, WorkingSet     uintptr
	PeakPagedPool, PagedPool       uintptr
	PeakNonPagedPool, NonPagedPool uintptr
	Pagefile, PeakPagefile         uintptr
}

type resourceProcessMemory struct {
	Scope                              string
	PID                                uint32
	WorkingSetBytes                    uint64
	PeakWorkingSetBytesUntilSample     uint64
	PagefileCommitBytes                uint64
	PeakPagefileCommitBytesUntilSample uint64
}

func resourceProcessSample(scope string, pid uint32, handle windows.Handle) (resourceProcessMemory, error) {
	if err := resourceMemoryInfo.Find(); err != nil {
		return resourceProcessMemory{}, err
	}
	var counters resourceMemoryCounters
	counters.Size = uint32(unsafe.Sizeof(counters))
	result, _, callErr := resourceMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	runtime.KeepAlive(&counters)
	if result == 0 {
		return resourceProcessMemory{}, fmt.Errorf("K32GetProcessMemoryInfo %s: %w", scope, callErr)
	}
	return resourceProcessMemory{scope, pid, uint64(counters.WorkingSet), uint64(counters.PeakWorkingSet), uint64(counters.Pagefile), uint64(counters.PeakPagefile)}, nil
}

type resourceLogicalFiles struct{ Bytes, OrdinaryFiles, ReparseEntriesSkipped uint64 }

func resourceCountLogicalFiles(path string) (resourceLogicalFiles, error) {
	var total resourceLogicalFiles
	var walk func(string) error
	walk = func(path string) error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			total.ReparseEntriesSkipped++
			return nil
		}
		if info.Mode().IsRegular() {
			total.Bytes += uint64(info.Size())
			total.OrdinaryFiles++
			return nil
		}
		if !info.IsDir() {
			return fmt.Errorf("nonregular evidence file %q", path)
		}
		dir, err := os.Open(path)
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			entries, err := dir.ReadDir(128)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			for _, entry := range entries {
				if err := walk(filepath.Join(path, entry.Name())); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	}
	err := walk(path)
	return total, err
}

type resourceReady struct {
	PID    uint32
	Digest string
}

func payloadResourceEvidence(t *testing.T) {
	t.Helper()
	nonce, err := os.ReadFile("nonce")
	if err != nil || string(nonce) != "resource-input-nonce" {
		t.Fatalf("resource authorized input: %q %v", nonce, err)
	}
	stdin, err := io.ReadAll(io.LimitReader(os.Stdin, 64))
	if err != nil || string(stdin) != "resource-stdin-nonce" {
		t.Fatalf("resource standard input: %q %v", stdin, err)
	}
	scratch := bytes.Repeat([]byte{0xa5}, 256)
	path := filepath.Join(os.Getenv("DWARF_WORK"), "resource-scratch")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(scratch)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
	readback, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(readback, scratch) {
		t.Fatalf("resource private scratch readback: %x %v", readback, err)
	}
	hash := sha256.New()
	_, _ = hash.Write(nonce)
	_, _ = hash.Write(stdin)
	_, _ = hash.Write(readback)
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
	if err := windows.SetHandleInformation(windows.Handle(write.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/q", "/c", "set /p x=")
	child.Stdin, child.Stdout, child.Stderr = read, os.Stdout, os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS, AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(write.Fd()), syscall.Handle(os.Stdin.Fd())}}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	fmt.Fprintln(os.Stderr, "resource-stderr-nonce")
	if err := json.NewEncoder(os.Stdout).Encode(resourceReady{uint32(child.Process.Pid), fmt.Sprintf("%x", hash.Sum(nil))}); err != nil {
		t.Fatal(err)
	}
	payloadWaitFile(t, "resource-scratch", func(data []byte) bool { return len(data) == 256 && bytes.HasPrefix(data, []byte("release-nonce")) })
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

func resourceWaitReady(r *nativeRun) (resourceReady, error) {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		out, stderr := r.pipes.output()
		var ready resourceReady
		if json.Unmarshal(bytes.TrimSpace(out), &ready) == nil && ready.PID != 0 {
			return ready, nil
		}
		select {
		case <-r.pipes.event:
		case <-deadline.C:
			return ready, fmt.Errorf("resource payload did not reach real readiness: stdout=%q stderr=%q", out, stderr)
		}
	}
}

func resourceGoHeap() map[string]uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return map[string]uint64{"heapAllocBytes": stats.HeapAlloc, "heapInuseBytes": stats.HeapInuse, "totalAllocBytes": stats.TotalAlloc, "sysBytes": stats.Sys}
}

func TestDefenderResourceEvidence(t *testing.T) {
	t.Parallel()
	openStart := time.Now()
	e := testExecutor(t)
	openElapsed := time.Since(openStart)
	version := windows.RtlGetVersion()
	buildSettings := make(map[string]string)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			buildSettings[setting.Key] = setting.Value
		}
	}
	for _, mode := range []string{"ordinary", "cancel", "timeout"} {
		request := testRequest(t, "resource")
		request.Limits.Wall = 10 * time.Second
		request.Limits.Processes = 2
		request.Stdin = []byte("resource-stdin-nonce")
		request.Files = append(request.Files, InputFile{Path: "nonce", Data: []byte("resource-input-nonce")})
		inputPayloadBytes := len(request.Stdin)
		for _, file := range request.Files {
			inputPayloadBytes += len(file.Data)
		}
		expected := sha256.Sum256(bytes.Join([][]byte{[]byte("resource-input-nonce"), request.Stdin, bytes.Repeat([]byte{0xa5}, 256)}, nil))
		ctx, cancel := context.WithCancel(context.Background())
		beforeHeap := resourceGoHeap()
		var readyHeap map[string]uint64
		var samples []resourceProcessMemory
		logical := make(map[string]resourceLogicalFiles)
		type phaseSample struct {
			Phase           string
			UTC             time.Time
			ElapsedNS       int64
			StopStarted     bool
			StopElapsedNS   int64
			StopRemainingNS int64
			PumpsKnown      bool
			PumpsRetired    int
			ChannelComplete [3]bool
			PipeFailure     string
		}
		var phases []phaseSample
		var gaps []string
		var observationErr error
		var ready resourceReady
		started := time.Now()
		var observedRun *nativeRun
		recordPhase := func(phase string, r *nativeRun) {
			now := time.Now()
			sample := phaseSample{Phase: phase, UTC: now.UTC(), ElapsedNS: now.Sub(started).Nanoseconds(), StopStarted: !r.stopAt.IsZero()}
			if sample.StopStarted {
				sample.StopElapsedNS = now.Sub(r.stopAt).Nanoseconds()
				sample.StopRemainingNS = r.stopAt.Add(r.request.Limits.StopGrace).Sub(now).Nanoseconds()
			}
			if r.pipes != nil {
				r.pipes.mu.Lock()
				sample.PumpsKnown = true
				sample.PumpsRetired = r.pipes.done
				sample.ChannelComplete = r.pipes.complete
				failure := r.pipes.failure
				r.pipes.mu.Unlock()
				if failure != nil {
					sample.PipeFailure = failure.Error()
				}
			}
			phases = append(phases, sample)
		}
		e.observe = func(phase string, r *nativeRun) {
			observedRun = r
			recordPhase(phase, r)
			defer recordPhase(phase+"_observer_return", r)
			if phase == "suspended_verified" {
				var actual windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
				queryErr := queryJob(r.job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&actual), unsafe.Sizeof(actual))
				t.Logf("RESOURCE_LIMIT_READBACK expected=%+v actual=%+v error=%v", jobLimits(r.request.Limits), actual, queryErr)
			}
			if observationErr != nil {
				return
			}
			if phase == "resumed" {
				observationErr = func() error {
					var err error
					ready, err = resourceWaitReady(r)
					if err != nil {
						return err
					}
					if ready.Digest != fmt.Sprintf("%x", expected) {
						return fmt.Errorf("computed input/stdin/private-scratch digest differs: %+v", ready)
					}
					pids, err := testWaitLiveJob(r, 2)
					if err != nil {
						return err
					}
					if !((pids[0] == r.pi.ProcessId && pids[1] == ready.PID) || (pids[1] == r.pi.ProcessId && pids[0] == ready.PID)) {
						return fmt.Errorf("resource PID is not the actual Job descendant: reported=%d native=%v", ready.PID, pids)
					}
					child, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ|windows.SYNCHRONIZE, false, ready.PID)
					if err != nil {
						return err
					}
					defer windows.CloseHandle(child)
					for _, actor := range []struct {
						scope  string
						pid    uint32
						handle windows.Handle
					}{
						{"entire Core test supervisor process, including observers", windows.GetCurrentProcessId(), windows.CurrentProcess()},
						{"restricted root process", r.pi.ProcessId, r.pi.Process},
						{"System32 cmd held descendant", ready.PID, child},
					} {
						sample, err := resourceProcessSample(actor.scope, actor.pid, actor.handle)
						if err != nil {
							return err
						}
						samples = append(samples, sample)
					}
					readyHeap = resourceGoHeap()
					recordPhase("readiness-memory-sample", r)
					return nil
				}()
				// The query handle is closed before natural exit/cancel/timeout.
				// No product path relies on observer-handle retention semantics.
				if observationErr == nil {
					switch mode {
					case "ordinary":
						release := make([]byte, 256)
						copy(release, "release-nonce")
						observationErr = os.WriteFile(filepath.Join(r.dir, "work", "resource-scratch"), release, 0o600)
					case "cancel":
						cancel()
					}
				}
			}
			if phase == "tree_exited" {
				for name, path := range map[string]string{"input": filepath.Join(r.dir, "input"), "work": filepath.Join(r.dir, "work"), "nativeProfile": r.profile.Path} {
					recordPhase("logical-"+name+"-start", r)
					count, err := resourceCountLogicalFiles(path)
					recordPhase("logical-"+name+"-return", r)
					if err != nil {
						observationErr = errors.Join(observationErr, fmt.Errorf("logical %s: %w", name, err))
						continue
					}
					logical[name] = count
				}
			}
			if observationErr != nil {
				cancel()
			}
		}
		receipt, runErr := e.Run(ctx, request)
		cancel()
		elapsed := time.Since(started)
		if observedRun != nil {
			recordPhase("run_returned", observedRun)
		}
		afterHeap := resourceGoHeap()
		if observationErr != nil {
			gaps = append(gaps, observationErr.Error())
		}
		if readyHeap == nil {
			gaps = append(gaps, "readiness Go-heap sample unavailable")
		}
		if len(samples) != 3 {
			gaps = append(gaps, "one or more native process-memory samples unavailable; no zero substituted")
		}
		if len(logical) != 3 {
			gaps = append(gaps, "one or more no-follow logical-file counts unavailable; no zero substituted")
		}
		var jobCounters any
		if receipt.UsageKnown {
			jobCounters = receipt.Usage
		} else {
			gaps = append(gaps, "final native Job accounting unavailable; no zero substituted")
		}
		errorText := ""
		if runErr != nil {
			errorText = runErr.Error()
		}
		evidence := map[string]any{
			"assumption": map[string]any{
				"workload":            "one copied current test executable plus one real held cmd descendant; finite input/output; one small private scratch file; no provider/model work in this workload",
				"callsPerObservedRun": 1, "scenario": mode,
				"scope": "focused invocation alone is the evidence run; whole-suite samples are not isolated figures; no figure is capacity",
			},
			"calculation": map[string]any{
				"inputFileAndStdinPayloadBytes": inputPayloadBytes,
				"retainedOutputBytes":           len(receipt.Stdout) + len(receipt.Stderr),
				"maximumHostPumpCount":          3, "maximumHostPumpBufferBytes": 3 * pipeBufferSize,
				"inputPayloadWithinDeclaredBudget":   uint64(inputPayloadBytes) <= request.Limits.InputBytes,
				"retainedOutputWithinDeclaredBudget": uint64(len(receipt.Stdout)+len(receipt.Stderr)) <= request.Limits.OutputBytes,
				"slotStructure":                      "one native Job and fresh profile per admitted run, three fixed pump goroutines and one synchronous control owner; not a throughput claim",
				"supervisorGoTotalAllocDeltaBytes":   afterHeap["totalAllocBytes"] - beforeHeap["totalAllocBytes"],
				"allocationScope":                    "entire supervisor during the observed interval, observers included; not attributable slot overhead or native/kernel memory",
			},
			"measurement": map[string]any{
				"os": runtime.GOOS, "architecture": runtime.GOARCH, "nativeWindowsVersion": fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber),
				"go": runtime.Version(), "cgoEnvironment": os.Getenv("CGO_ENABLED"), "buildSettings": buildSettings,
				"sharedRootFixtureAndOpenNS": openElapsed.Nanoseconds(), "runID": receipt.RunID, "rootID": receipt.RootID, "appContainerSID": receipt.ProfileSID,
				"declaredLimits": request.Limits, "declaredInputFileCount": len(request.Files), "declaredScratchFileBytes": 256,
				"chargedInputBytes": receipt.InputBytes, "stdoutBytes": len(receipt.Stdout), "stderrBytes": len(receipt.Stderr),
				"createdUTC": receipt.CreatedUTC, "resumedUTC": receipt.ResumedUTC, "closedUTC": receipt.ClosedUTC,
				"setupNS": receipt.Setup.Nanoseconds(), "executionNS": receipt.Execution.Nanoseconds(), "stopNS": receipt.Stop.Nanoseconds(), "observedCallNS": elapsed.Nanoseconds(), "phases": phases,
				"processMemoryAtReadiness": samples, "memoryScope": "per-process working sets and pagefile commit, peaks only through sampling time; never sum these into job or whole-machine RAM",
				"phaseScope":  "observer entry/return and logical-file walk boundaries; StopElapsedNS/StopRemainingNS apply only when StopStarted; PumpsRetired counts ownership retirement, while ChannelComplete records clean completion, not per-channel retirement",
				"jobCounters": jobCounters, "jobCounterScope": "User/Kernel are native 100-ns totals; PeakJobMemoryBytes/PeakProcessMemoryBytes are reported native PeakJobMemoryUsed/PeakProcessMemoryUsed, not a RAM or commit claim; I/O fields are native basic-and-I/O transfer totals",
				"logicalFilesAtProvedTreeExit": logical, "fileScope": "ordinary no-follow logical file bytes before cleanup; not physical allocation and not Job I/O",
				"goHeapBefore": beforeHeap, "goHeapAtReadiness": readyHeap, "goHeapAfter": afterHeap,
				"state": receipt.State, "outcome": receipt.Outcome, "exitCodeKnown": receipt.ExitCodeKnown, "exitCode": receipt.ExitCode,
				"rootExited": receipt.RootExited, "treeExited": receipt.TreeExited, "resourcesReusable": receipt.ResourcesReusable, "outputState": receipt.OutputState, "error": errorText, "gaps": gaps,
			},
			"plan": "declared-workload throughput, admission economics, arbitrary C4 environments and qualification belong to ticket 46; race instruments the host executor and adds payload overhead, while plain runs prove payload behavior",
		}
		encoded, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("RESOURCE_EVIDENCE %s", encoded)
		if errors.Is(runErr, ErrQuarantined) {
			stack := make([]byte, 512*1024)
			n := runtime.Stack(stack, true)
			t.Logf("RESOURCE_QUARANTINE_STACK scenario=%s truncated=%t\n%s", mode, n == len(stack), stack[:n])
		}
		if len(gaps) != 0 {
			t.Fatalf("resource observer gaps: %v; native result=%+v error=%v", gaps, receipt, runErr)
		}
		if logical["work"].OrdinaryFiles != 1 || logical["work"].Bytes != 256 || receipt.Usage.TotalProcesses != 2 {
			t.Fatalf("declared real workload differs from observations: files=%+v native=%+v", logical, receipt.Usage)
		}
		if mode == "ordinary" {
			assertReusable(t, receipt, runErr)
			if string(receipt.Stderr) != "resource-stderr-nonce\n" {
				t.Fatalf("positive standard error exchange: %q", receipt.Stderr)
			}
		} else {
			wantOutcome, wantError := Canceled, error(context.Canceled)
			if mode == "timeout" {
				wantOutcome, wantError = TimedOut, context.DeadlineExceeded
			}
			if !errors.Is(runErr, wantError) || receipt.Outcome != wantOutcome || receipt.State != Closed || !receipt.RootExited || !receipt.TreeExited || !receipt.ResourcesReusable || receipt.OutputState != Unpublishable {
				t.Fatalf("bounded resource stop: %+v error=%v", receipt, runErr)
			}
		}
	}
}

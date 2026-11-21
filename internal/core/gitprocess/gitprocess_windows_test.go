package gitprocess

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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
	"golang.org/x/sys/windows"
)

type processNotice struct {
	Kind    string
	PID     int
	Job     string
	RunID   uuid.UUID
	Batch   string
	Syncs   int
	Renames int
}

func nativeOwner(t *testing.T, root string) *Owner {
	t.Helper()
	o, _, err := Open(context.Background(), root, Options{RequireTree: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	return o
}
func gitProgram(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func heldGit(t *testing.T) Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	alias := `!"` + filepath.ToSlash(exe) + `" -test.run=^TestGitProcessHelper$`
	return Spec{Program: gitProgram(t), Args: []string{"-c", "alias.hold=" + alias, "hold"}, Env: append(os.Environ(), "DWARF_GITPROCESS_HELPER=worker")}
}
func observer(t *testing.T, pid int, readyAt time.Time) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(h) })
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		var code uint32
		exitErr := windows.GetExitCodeProcess(h, &code)
		t.Fatalf("observer was not live: %d %v; exit=%d (%v); since-readiness=%s", state, err, code, exitErr, time.Since(readyAt))
	}
	return h
}
func assertExited(t *testing.T, h windows.Handle) {
	t.Helper()
	state, err := windows.WaitForSingleObject(h, windows.INFINITE)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("observed descendant did not exit: %d %v", state, err)
	}
}

type noticeWriter struct {
	mu      sync.Mutex
	data    bytes.Buffer
	partial []byte
	notices chan processNotice
}

func (w *noticeWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data.Write(b)
	w.partial = append(w.partial, b...)
	for {
		line, rest, ok := bytes.Cut(w.partial, []byte("\n"))
		if !ok {
			break
		}
		var n processNotice
		if err := json.Unmarshal(line, &n); err == nil {
			w.notices <- n
		}
		w.partial = rest
	}
	return len(b), nil
}
func TestGitProcessAdmissionAndConcurrentClose(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	o, _, err := Open(context.Background(), root, Options{RequireTree: true, MaxConcurrent: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	ready := make(chan int, 2)
	results := make(chan Result, 2)
	var writers []*io.PipeWriter
	for range 2 {
		input, send := io.Pipe()
		defer input.Close()
		defer send.Close()
		writers = append(writers, send)
		go func() {
			r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"hash-object", "--stdin"}, Env: os.Environ(), Stdin: input, Started: func(pid int) { ready <- pid }})
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
			results <- r
		}()
	}
	for range 2 {
		<-ready
	}
	waiting, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := o.Run(waiting, Spec{Program: gitProgram(t), Args: []string{"version"}, Started: func(int) { t.Error("cancelled admission launched a child") }})
		queued <- err
	}()
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation lost: %v", err)
	}
	closed := make(chan error, 2)
	for range 2 {
		go func() { closed <- o.Close() }()
	}
	for _, send := range writers {
		_ = send.Close()
	}
	for range 2 {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
	a, b := <-results, <-results
	for _, r := range []Result{a, b} {
		if !r.RootExited || !r.TreeClosed || !r.IOJoined {
			t.Fatalf("Close returned before a running child joined: %+v", r)
		}
	}
	if a.RunID == b.RunID {
		t.Fatal("concurrent runs reused an identity")
	}
	reopened := nativeOwner(t, root)
	r, err := reopened.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}})
	if err != nil || !r.TreeClosed {
		t.Fatalf("proved Close left the root unusable: %+v %v", r, err)
	}
}

func TestGitProcessLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{1, 64} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			o, _, err := Open(context.Background(), t.TempDir(), Options{RequireTree: true, MaxDescendants: limit})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := o.Close(); err != nil {
					t.Error(err)
				}
			})
			var out bytes.Buffer
			r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"-c", "alias.limit-proof=!echo limit-proof", "limit-proof"}, Env: os.Environ(), Stdout: &out})
			if !r.RootExited || !r.TreeClosed || !r.IOJoined || r.ExitCode < 0 {
				t.Fatalf("process-limit run not joined: %+v %v", r, err)
			}
			if limit == 1 {
				if !errors.Is(err, ErrLimit) || errors.Is(err, ErrExit) {
					t.Fatalf("limit refusal accepted as ordinary exit: %+v %v", r, err)
				}
			} else if err != nil || r.ExitCode != 0 || !strings.Contains(out.String(), "limit-proof") {
				t.Fatalf("normal-limit control changed: %+v %v %q", r, err, out.String())
			}
		})
	}
}

func TestGitProcessTree(t *testing.T) {
	for _, mode := range []string{"natural", "cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			o := nativeOwner(t, t.TempDir())
			input, send := io.Pipe()
			defer input.Close()
			defer send.Close()
			output := &noticeWriter{notices: make(chan processNotice, 4)}
			spec := heldGit(t)
			spec.Stdin, spec.Stdout = input, output
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type ending struct {
				result Result
				err    error
			}
			done := make(chan ending, 1)
			var observedJob windows.Handle
			var observerErr error
			o.observe = func(phase string, f fence) {
				if phase == "resumed" {
					observedJob, observerErr = openJob(f.JobName)
				}
			}
			go func() { result, err := o.Run(ctx, spec); done <- ending{result, err} }()
			var n processNotice
			var readyAt time.Time
			select {
			case n = <-output.notices:
				readyAt = time.Now()
			case early := <-done:
				t.Fatalf("Git failed before readiness: %+v %v", early.result, early.err)
			}
			if n.Kind != "child" {
				t.Fatalf("missing held-descendant readiness: %+v", n)
			}
			h := observer(t, n.PID, readyAt)
			switch mode {
			case "natural":
				_, _ = send.Write([]byte("x"))
				_ = send.Close()
			case "cancel":
				cancel()
				_ = send.Close()
			case "close":
				o.Cancel()
				_ = send.Close()
			}
			got := <-done
			if observerErr != nil {
				t.Fatal(observerErr)
			}
			defer func() { _ = windows.TerminateJobObject(observedJob, 1); _ = windows.CloseHandle(observedJob) }()
			_, zero, queryErr := jobZero(observedJob)
			if queryErr != nil || !zero {
				t.Fatalf("root exit accepted while the Job still had descendants: %v", queryErr)
			}
			if !got.result.RootExited || !got.result.TreeClosed || !got.result.IOJoined || got.result.TotalProcesses < 3 || got.result.Proof != "live" {
				t.Fatalf("root exit accepted without tree/I/O closure: %+v %v", got.result, got.err)
			}
			state, err := windows.WaitForSingleObject(h, uint32(o.options.StopGrace/time.Millisecond))
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("claimed tree closure left a live descendant: %d %v", state, err)
			}
			if mode == "natural" {
				if got.err != nil || got.result.ExitCode != 0 {
					t.Fatalf("natural exit changed: %+v %v", got.result, got.err)
				}
				if !strings.Contains(output.data.String(), "output-before-root-exit\n") {
					t.Fatal("drain discarded output")
				}
			} else if mode == "cancel" && !errors.Is(got.err, context.Canceled) {
				t.Fatalf("cancel cause lost: %v", got.err)
			}
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			if err := o.Close(); err != nil {
				t.Fatal("repeated Close changed", err)
			}
		})
	}
	t.Run("root-only", func(t *testing.T) {
		t.Parallel()
		o := nativeOwner(t, t.TempDir())
		var out bytes.Buffer
		r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ(), Stdout: &out})
		if err != nil || r.ExitCode != 0 || !r.TreeClosed || !r.IOJoined || !strings.HasPrefix(out.String(), "git version ") {
			t.Fatalf("root-only control: %+v %v %q", r, err, out.String())
		}
	})
	t.Run("breakaway", func(t *testing.T) {
		o := nativeOwner(t, t.TempDir())
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		var output, diagnostic bytes.Buffer
		r, err := o.Run(context.Background(), Spec{Program: exe, Args: []string{"-test.run=^TestGitProcessHelper$"}, Env: append(os.Environ(), "DWARF_GITPROCESS_HELPER=breakaway"), Stdout: &output, Stderr: &diagnostic})
		if err != nil || r.ExitCode != 0 || !r.TreeClosed || !r.IOJoined {
			t.Fatalf("breakaway permission bypass: %+v %v %s %s", r, err, output.String(), diagnostic.String())
		}
	})
}

// The worker is a real Git alias child. Its System32 cmd descendant waits on
// an OS signal, not a sleep or a test deadline, independently of Core's pipes.
func TestGitProcessHelper(t *testing.T) {
	switch os.Getenv("DWARF_GITPROCESS_HELPER") {
	case "slot":
		slotHelper(t, os.Getenv("DWARF_GITPROCESS_SLOT_MODE"))
		os.Exit(0)
	case "breakaway":
		system, err := windows.GetSystemDirectory()
		if err != nil {
			t.Fatal(err)
		}
		child := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/c", "exit 0")
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x01000000}
		if err := child.Run(); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("child escaped non-breakaway Job: %v", err)
		}
		os.Exit(0)
	case "retention":
		root := os.Getenv("DWARF_GITPROCESS_ROOT")
		hold := &heldReader{entered: make(chan struct{}), release: make(chan struct{})}
		func() {
			o, _, err := Open(context.Background(), root, Options{RequireTree: true, StopGrace: 20 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ(), Stdin: hold, Started: func(int) { <-hold.entered }})
			if !errors.Is(err, ErrUnconfirmed) || r.IOJoined || r.TreeClosed {
				t.Fatalf("unjoined stdin authorized reuse: %+v %v", r, err)
			}
			if err := o.Close(); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("Close hid unjoined stdin", err)
			}
			if err := o.Close(); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("repeated Close changed", err)
			}
		}()
		runtime.GC()
		runtime.GC()
		reopened, _, err := Open(context.Background(), root, Options{RequireTree: true})
		if reopened != nil {
			_ = reopened.Close()
		}
		if !errors.Is(err, durable.ErrLocked) {
			t.Fatal("GC released an unconfirmed Owner lock", err)
		}
		close(hold.release)
		os.Exit(0)
	case "proven-closure", "cleanup-failure":
		mode := os.Getenv("DWARF_GITPROCESS_HELPER")
		o, _, err := Open(context.Background(), os.Getenv("DWARF_GITPROCESS_ROOT"), Options{RequireTree: true})
		if err != nil {
			t.Fatal(err)
		}
		var rootObserver, heldFence windows.Handle
		var batchPath string
		defer func() {
			if rootObserver != 0 {
				_ = windows.CloseHandle(rootObserver)
			}
			if heldFence != 0 {
				_ = windows.CloseHandle(heldFence)
			}
		}()
		if mode == "cleanup-failure" {
			o.observe = func(phase string, f fence) {
				if phase == "suspended" {
					if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "prefill", PID: int(f.RootPID), RunID: f.RunID}); err != nil {
						t.Fatal(err)
					}
					var ack [1]byte
					if _, err := io.ReadFull(os.Stdin, ack[:]); err != nil {
						t.Fatal(err)
					}
				}
			}
			for range slotCount - 1 {
				r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()})
				if err != nil || !r.TreeClosed {
					t.Fatalf("cleanup prefill failed: %+v %v", r, err)
				}
			}
		}
		o.observe = func(phase string, f fence) {
			if phase == "suspended" {
				batchPath, _, _ = readSlotRun(t, o.root, f.RunID)
			}
			if phase == "suspended" && mode == "proven-closure" {
				// Keep the exited PID openable, without retaining the Job.
				rootObserver, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, f.RootPID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase != "closed" {
				return
			}
			if mode == "cleanup-failure" {
				path, err := windows.UTF16PtrFromString(batchPath)
				if err != nil {
					t.Fatal(err)
				}
				heldFence, err = windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "closed", PID: int(f.RootPID), RunID: f.RunID, Batch: batchPath}); err != nil {
				t.Fatal(err)
			}
			if mode == "proven-closure" {
				var b [1]byte
				_, _ = os.Stdin.Read(b[:])
				t.Fatal("proven-closure crash barrier was released")
			}
		}
		r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()})
		var unconfirmed *UnconfirmedError
		if !errors.As(err, &unconfirmed) || unconfirmed.Reason != "cleanup_failed" || !r.TreeClosed || !r.RootExited || !r.IOJoined {
			t.Fatalf("removal failure lost proven closure or retained ownership: %+v %v", r, err)
		}
		if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}); !errors.Is(err, ErrUnconfirmed) {
			t.Fatal("cleanup failure admitted another Run", err)
		}
		for range 2 {
			if err := o.Close(); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("Close hid cleanup failure", err)
			}
		}
		_, stored, _ := readSlotRun(t, o.root, r.RunID)
		raw, err := canonicalJSON(stored)
		if err != nil {
			t.Fatal(err)
		}
		f, err := decodeFence(raw)
		if err != nil || f.State != "running" || f.RunID != r.RunID {
			t.Fatalf("cleanup failure erased or published over running fence: %+v %v", f, err)
		}
		if err := windows.CloseHandle(heldFence); err != nil {
			t.Fatal(err)
		}
		heldFence = 0
		os.Exit(0)
	case "worker":
		system, err := windows.GetSystemDirectory()
		if err != nil {
			t.Fatal(err)
		}
		child := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/q", "/c", "waitfor /t 86400 dwarfgit"+strings.ReplaceAll(uuid.NewV7().String(), "-", ""))
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "child", PID: child.Process.Pid}); err != nil {
			t.Fatal(err)
		}
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		fmt.Fprintln(os.Stdout, "output-before-root-exit")
		os.Exit(0)
	case "core":
		o, _, err := Open(context.Background(), os.Getenv("DWARF_GITPROCESS_ROOT"), Options{RequireTree: true})
		if err != nil {
			t.Fatal(err)
		}
		spec := heldGit(t)
		spec.Stdin, spec.Stdout, spec.Stderr = os.Stdin, os.Stdout, os.Stderr
		o.observe = func(phase string, f fence) {
			if phase == "resumed" {
				_ = json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "root", PID: int(f.RootPID), Job: f.JobName})
			}
		}
		_, err = o.Run(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatal("Core survived crash barrier")
	}
}
func coreDeath(t *testing.T, retained bool) {
	t.Helper()
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestGitProcessHelper$")
	cmd.Env = append(os.Environ(), "DWARF_GITPROCESS_HELPER=core", "DWARF_GITPROCESS_ROOT="+root)
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	scan := bufio.NewScanner(stdout)
	var rootNotice, childNotice processNotice
	var rootReadyAt, childReadyAt time.Time
	for rootNotice.PID == 0 || childNotice.PID == 0 {
		if !scan.Scan() {
			t.Fatalf("helper failed readiness: %s", stderr.String())
		}
		receivedAt := time.Now()
		var n processNotice
		if err := json.Unmarshal(scan.Bytes(), &n); err != nil {
			t.Fatal(err)
		}
		if n.Kind == "root" {
			rootNotice, rootReadyAt = n, receivedAt
		} else if n.Kind == "child" {
			childNotice, childReadyAt = n, receivedAt
		}
	}
	rootHandle, childHandle := observer(t, rootNotice.PID, rootReadyAt), observer(t, childNotice.PID, childReadyAt)
	var job windows.Handle
	if retained {
		job, err = openJob(rootNotice.Job)
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(job)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("crash helper did not die")
	}
	waited = true
	if !retained {
		assertExited(t, rootHandle)
		assertExited(t, childHandle)
	} else {
		state, err := windows.WaitForSingleObject(childHandle, 0)
		if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("retained-Job control already exited: %d %v", state, err)
		}
	}
	o := nativeOwner(t, root)
	report := o.report()
	if len(report.Pending) != 1 {
		t.Fatalf("crash lost fence: %+v", report)
	}
	old := report.Pending[0].RunID
	if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}); !errors.Is(err, ErrUnconfirmed) {
		t.Fatal("pending fence admitted Git", err)
	}
	report, err = o.Reconcile(context.Background())
	proof := "name_absent"
	if retained {
		proof = "retained_job"
	}
	if err != nil || len(report.Pending) != 0 || len(report.Closed) != 1 || report.Closed[0].Proof != proof || report.Closed[0].ExitCode != -1 {
		t.Fatalf("recovery did not prove %s: %+v %v", proof, report, err)
	}
	assertExited(t, rootHandle)
	assertExited(t, childHandle)
	if retained {
		_, zero, err := jobZero(job)
		if err != nil || !zero {
			t.Fatal("retained Job not empty", err)
		}
	}
	r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()})
	if err != nil || r.RunID == old || !r.TreeClosed {
		t.Fatalf("same-root reuse failed: %+v %v", r, err)
	}
}
func TestGitProcessCoreDeath(t *testing.T)           { coreDeath(t, false) }
func TestGitProcessRetainedJobRecovery(t *testing.T) { coreDeath(t, true) }

func TestGitProcessProvenClosureCrash(t *testing.T)      { closureRecovery(t, true) }
func TestGitProcessCleanupFailureRetention(t *testing.T) { closureRecovery(t, false) }

func closureRecovery(t *testing.T, crash bool) {
	t.Helper()
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mode := "cleanup-failure"
	if crash {
		mode = "proven-closure"
	}
	cmd := exec.Command(exe, "-test.run=^TestGitProcessHelper$")
	cmd.Env = append(os.Environ(), "DWARF_GITPROCESS_HELPER="+mode, "DWARF_GITPROCESS_ROOT="+root)
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var notice processNotice
	decoder := json.NewDecoder(stdout)
	for {
		if err := decoder.Decode(&notice); err != nil {
			t.Fatalf("closure helper failed readiness: %v %s", err, stderr.String())
		}
		if notice.Kind != "prefill" {
			break
		}
		observer(t, notice.PID, time.Now())
		if _, err := stdin.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if notice.Kind != "closed" || !validID(notice.RunID) || notice.PID == 0 {
		t.Fatalf("invalid closure notice: %+v", notice)
	}
	if crash {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(notice.PID))
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(h)
		state, err := windows.WaitForSingleObject(h, 0)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("closed root is not signaled: %d %v", state, err)
		}
		var code uint32
		if err := windows.GetExitCodeProcess(h, &code); err != nil || code != 0 {
			t.Fatalf("closed git version exit: %d %v", code, err)
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
	}
	diagnostic, readErr := io.ReadAll(io.MultiReader(decoder.Buffered(), stdout))
	if readErr != nil {
		t.Fatal(readErr)
	}
	err = cmd.Wait()
	waited = true
	if crash && err == nil || !crash && err != nil {
		t.Fatalf("closure helper exit: crash=%v error=%v stdout=%s stderr=%s", crash, err, diagnostic, stderr.String())
	}
	o := nativeOwner(t, root)
	report := o.report()
	if len(report.Pending) != 1 || report.Pending[0].RunID != notice.RunID {
		t.Fatalf("proof-to-removal window lost its running fence: %+v", report)
	}
	if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}); !errors.Is(err, ErrUnconfirmed) {
		t.Fatal("pending closure admitted Git", err)
	}
	report, err = o.Reconcile(context.Background())
	if err != nil || len(report.Pending) != 0 || len(report.Closed) != 1 {
		t.Fatalf("running fence did not reconcile: %+v %v", report, err)
	}
	closed := report.Closed[0]
	if closed.RunID != notice.RunID || closed.Proof != "name_absent" || closed.ExitCode != -1 || closed.TotalProcesses != 0 {
		t.Fatalf("recovery inferred live success or retained the Job: %+v", closed)
	}
	if _, err := os.Stat(notice.Batch); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reconciled fence survived", err)
	}
	r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()})
	if err != nil || r.RunID == notice.RunID || !r.TreeClosed {
		t.Fatalf("fresh Run after proven closure: %+v %v", r, err)
	}
}

func installFence(t *testing.T, o *Owner, f fence) {
	t.Helper()
	if err := o.writeFence(f); err != nil {
		t.Fatal(err)
	}
	o.pending[f.RunID] = f
}
func TestGitProcessFenceErrors(t *testing.T) {
	for _, row := range []string{"session", "access-denied", "limits", "query", "cancel", "live-root"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			o := nativeOwner(t, t.TempDir())
			o.options.StopGrace = 20 * time.Millisecond // Exercise the refusal boundary with a positively held root.
			id := uuid.NewV7()
			session, err := currentSession()
			if err != nil {
				t.Fatal(err)
			}
			f := fence{Format: fenceFormat, Version: 1, RootID: o.rootID, RunID: id, JobName: jobName(o.rootID, id), Session: session, State: "launch_pending"}
			ctx := context.Background()
			var job windows.Handle
			var pi windows.ProcessInformation
			switch row {
			case "session":
				f.Session++
			case "access-denied":
				sd, err := windows.SecurityDescriptorFromString("D:P(D;;GA;;;WD)")
				if err != nil {
					t.Fatal(err)
				}
				job, err = createJob(f.JobName, o.options.MaxDescendants)
				if err != nil {
					t.Fatal(err)
				}
				dacl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetSecurityInfo(job, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
					t.Fatal(err)
				}
				probe, err := openJob(f.JobName)
				if probe != 0 {
					_ = windows.CloseHandle(probe)
				}
				if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					t.Fatal("deny DACL control failed", err)
				}
			case "limits":
				job, err = createJob(f.JobName, o.options.MaxDescendants+1)
			case "query":
				job, err = createJob(f.JobName, o.options.MaxDescendants)
				o.queryFault = func() error { return windows.ERROR_INVALID_HANDLE }
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "live-root":
				job, err = createJob(jobName(o.rootID, uuid.NewV7()), o.options.MaxDescendants)
				if err != nil {
					t.Fatal(err)
				}
				pipes, pipeErr := newPipes(false)
				if pipeErr != nil {
					t.Fatal(pipeErr)
				}
				system, pathErr := windows.GetSystemDirectory()
				if pathErr != nil {
					t.Fatal(pathErr)
				}
				pi, err = createSuspended(Spec{Program: filepath.Join(system, "cmd.exe"), Args: []string{"/d", "/q", "/c", "waitfor /t 86400 dwarfgit" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")}, Env: os.Environ()}, job, pipes)
				_ = pipes.closeChildren()
				pipes.closeParents()
				if err != nil {
					t.Fatal(err)
				}
				// Keeping the root suspended is a deterministic live identity;
				// it cannot run down while name_absent recovery waits for it.
				f.State, f.RootPID = "running", pi.ProcessId
				f.RootCreated, err = processCreated(pi.Process)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if job != 0 {
					_ = windows.TerminateJobObject(job, 1)
					_ = windows.CloseHandle(job)
				}
				if pi.Process != 0 {
					assertExited(t, pi.Process)
					_ = windows.CloseHandle(pi.Process)
					_ = windows.CloseHandle(pi.Thread)
				}
			}()
			installFence(t, o, f)
			before, err := os.ReadFile(o.fencePath(f.RunID))
			if err != nil {
				t.Fatal(err)
			}
			report, err := o.Reconcile(ctx)
			if (row == "cancel" && !errors.Is(err, context.Canceled)) || (row != "cancel" && !errors.Is(err, ErrUnconfirmed)) || len(report.Pending) != 1 || len(report.Closed) != 0 {
				t.Fatalf("%s accepted without exact proof: %+v %v", row, report, err)
			}
			after, err := os.ReadFile(o.fencePath(f.RunID))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("%s erased or changed unconfirmed fence: %v", row, err)
			}
			if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t)}); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("unconfirmed fence admitted process", err)
			}
			if row == "live-root" {
				if err := windows.TerminateJobObject(job, 1); err != nil {
					t.Fatal(err)
				}
				assertExited(t, pi.Process)
				report, err := o.Reconcile(context.Background())
				if err != nil || len(report.Closed) != 1 || report.Closed[0].Proof != "name_absent" {
					t.Fatalf("ended root refused: %+v %v", report, err)
				}
			}
			if row != "live-root" {
				// Remove the test's actual obstruction, then require real
				// recovery rather than deleting or inventing a closed fence.
				if job != 0 {
					if err := windows.CloseHandle(job); err != nil {
						t.Fatal(err)
					}
					job = 0
				}
				o.queryFault = nil
				if row == "session" {
					f.Session = session
					if err := o.writeFence(f); err != nil {
						t.Fatal(err)
					}
					o.pending[f.RunID] = f
				}
				if _, err := o.Reconcile(context.Background()); err != nil {
					t.Fatal("unobstructed recovery failed", err)
				}
			}
		})
	}
}

type heldReader struct{ entered, release chan struct{} }

func (r *heldReader) Read([]byte) (int, error) {
	close(r.entered)
	<-r.release
	return 0, io.EOF
}
func TestGitProcessUnconfirmedRetention(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestGitProcessHelper$")
	cmd.Env = append(os.Environ(), "DWARF_GITPROCESS_HELPER=retention", "DWARF_GITPROCESS_ROOT="+t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("retention regression probe: %s %v", output, err)
	}
}

func TestGitProcessNameCollision(t *testing.T) {
	t.Parallel()
	name := jobName(uuid.NewV7(), uuid.NewV7())
	job, err := createJob(name, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	other, err := createJob(name, 8)
	if other != 0 {
		_ = windows.CloseHandle(other)
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		t.Fatal("existing Job name adopted", err)
	}
	if err := verifyJob(job, 4); err != nil {
		t.Fatal("collision changed another Job's limits", err)
	}
}

func readSlotRun(t *testing.T, root string, id uuid.UUID) (string, fence, byte) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".slots") {
			continue
		}
		path := filepath.Join(root, "runs", entry.Name())
		raw, err := readRegularBounded(path, slotsSize)
		if err != nil {
			t.Fatal(err)
		}
		c, err := decodeSlots(raw)
		if err != nil {
			t.Fatal(err)
		}
		for i, run := range c.Header.RunIDs {
			if run == id {
				return path, c.Frames[i], c.Retired[i]
			}
		}
	}
	t.Fatalf("reserved invocation %s lost its container", id)
	return "", fence{}, 0
}

type slotHelperProcess struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	decoder *json.Decoder
	stderr  bytes.Buffer
	waited  bool
}

func startSlotHelper(t *testing.T, root, mode string) *slotHelperProcess {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &slotHelperProcess{cmd: exec.Command(exe, "-test.run=^TestGitProcessHelper$")}
	p.cmd.Env = append(os.Environ(), "DWARF_GITPROCESS_HELPER=slot", "DWARF_GITPROCESS_ROOT="+root, "DWARF_GITPROCESS_SLOT_MODE="+mode)
	p.input, err = p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.output = output
	p.decoder = json.NewDecoder(output)
	p.cmd.Stderr = &p.stderr
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !p.waited {
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
		_ = p.input.Close()
	})
	return p
}

func (p *slotHelperProcess) notice(t *testing.T) processNotice {
	t.Helper()
	var n processNotice
	if err := p.decoder.Decode(&n); err != nil {
		_ = p.cmd.Process.Kill()
		output, _ := io.ReadAll(io.MultiReader(p.decoder.Buffered(), p.output))
		_ = p.cmd.Wait()
		p.waited = true
		t.Fatalf("slot helper readiness: %v %s %s", err, output, p.stderr.String())
	}
	return n
}
func (p *slotHelperProcess) ack(t *testing.T) {
	t.Helper()
	if _, err := p.input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
}
func (p *slotHelperProcess) kill(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Wait(); err == nil {
		t.Fatal("slot crash helper survived")
	}
	p.waited = true
}
func (p *slotHelperProcess) finish(t *testing.T) {
	t.Helper()
	remaining, readErr := io.ReadAll(io.MultiReader(p.decoder.Buffered(), p.output))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("slot helper failed: %v %s %s", err, remaining, p.stderr.String())
	}
	p.waited = true
}

func slotHelper(t *testing.T, mode string) {
	if strings.HasPrefix(mode, "error:") {
		slotErrorHelper(t, strings.TrimPrefix(mode, "error:"))
		return
	}
	o, _, err := Open(context.Background(), os.Getenv("DWARF_GITPROCESS_ROOT"), Options{RequireTree: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}
	var recoverySyncs, recoveryRenames int
	emit := func(kind string, f fence, path string) {
		if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: kind, PID: int(f.RootPID), Job: f.JobName, RunID: f.RunID, Batch: path, Syncs: recoverySyncs, Renames: recoveryRenames}); err != nil {
			t.Fatal(err)
		}
	}
	ack := func() {
		var b [1]byte
		if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	park := func(kind string, f fence, path string) {
		emit(kind, f, path)
		ack()
		t.Fatal("crash barrier was released")
	}
	if mode == "recovery_closed" || mode == "recovery_temp" {
		o.observe = func(phase string, f fence) {
			if mode == "recovery_closed" && phase == "reconciled" {
				park(phase, f, o.fencePath(f.RunID))
			}
		}
		o.metadataHook = func(op, path string, success bool) error {
			if success && strings.HasSuffix(path, ".json") {
				if op == "temp_sync" {
					recoverySyncs++
				} else if op == "rename" {
					recoveryRenames++
				}
			}
			if mode == "recovery_temp" && op == "temp_sync" && success && strings.HasSuffix(path, ".json") {
				raw, err := readRegular(path + ".pending")
				if err != nil {
					t.Fatal(err)
				}
				f, err := decodeFence(raw)
				if err != nil {
					t.Fatal(err)
				}
				park("recovery_temp", f, path)
			}
			return nil
		}
		if _, err := o.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Fatal("recovery failed to publish closed proof")
	}
	assertBeforeJob := func(phase string, f fence) {
		if phase != "launch_pending" {
			return
		}
		h, err := openJob(f.JobName)
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			// The exhaustion helper may be in a goroutine whose parent awaits
			// readiness; fail the subprocess rather than strand that parent.
			fmt.Fprintln(os.Stderr, "reserved Job existed before publication admission", err)
			os.Exit(1)
		}
	}
	if mode == "exhausted_with_prelaunch_member" {
		ready, parked := make(chan struct{}), make(chan struct{})
		var target uuid.UUID
		o.observe = func(phase string, f fence) {
			assertBeforeJob(phase, f)
			if phase == "root_created" && target == (uuid.UUID{}) {
				target = f.RunID
				path, _, _ := readSlotRun(t, o.root, f.RunID)
				emit("root_created", f, path)
				close(ready)
				<-parked
			} else if phase == "suspended" {
				emit("prefill", f, "")
				ack()
			}
		}
		go func() {
			if _, err := o.Run(context.Background(), spec); err != nil {
				t.Error(err)
			}
		}()
		<-ready
		ack()
		for range slotCount - 1 {
			r, err := o.Run(context.Background(), spec)
			if err != nil || !r.TreeClosed {
				t.Fatalf("exhaustion prefill: %+v %v", r, err)
			}
		}
		path, _, _ := readSlotRun(t, o.root, target)
		park("exhausted", fence{RunID: target}, path)
	}
	if mode == "temp_written" || mode == "file_synced" || mode == "renamed" {
		wanted := map[string]string{"temp_written": "temp_write", "file_synced": "temp_sync", "renamed": "rename"}[mode]
		o.metadataHook = func(op, path string, success bool) error {
			if op == wanted && success {
				file := path + ".pending"
				if mode == "renamed" {
					file = path
				}
				raw, err := readRegularBounded(file, slotsSize)
				if err != nil {
					t.Fatal(err)
				}
				c, err := decodeSlots(raw)
				if err != nil {
					t.Fatal(err)
				}
				park(mode, c.Header.candidate(0), path)
			}
			return nil
		}
	}
	retirement := mode == "before_remove" || mode == "after_remove" || mode == "partial_at_close"
	if retirement && mode != "partial_at_close" {
		o.observe = func(phase string, f fence) {
			if phase == "suspended" {
				emit("prefill", f, "")
				ack()
			}
		}
		for range slotCount - 1 {
			r, err := o.Run(context.Background(), spec)
			if err != nil || !r.TreeClosed {
				t.Fatalf("retirement prefill: %+v %v", r, err)
			}
		}
	}
	var path string
	var rootObserver windows.Handle
	o.observe = func(phase string, f fence) {
		assertBeforeJob(phase, f)
		if phase == "root_created" {
			path, _, _ = readSlotRun(t, o.root, f.RunID)
			if mode == "fresh" {
				park(phase, f, path)
			}
		}
		if phase == "suspended" && (retirement || mode == "running_durable") {
			rootObserver, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, f.RootPID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if mode == "published" && phase == "reservation_published" {
			path, _, _ = readSlotRun(t, o.root, f.RunID)
			park(phase, f, path)
		}
		if mode == "running_durable" && phase == "suspended" {
			park(phase, f, path)
		}
		if retirement && phase == "closed" {
			emit("target", f, path)
			ack()
		}
		if (mode == "before_remove" || mode == "partial_at_close") && phase == "retirement_before_remove" {
			park(phase, f, path)
		}
		if mode == "after_remove" && phase == "retirement_after_remove" {
			park(phase, f, path)
		}
	}
	r, err := o.Run(context.Background(), spec)
	if err != nil || !r.TreeClosed {
		t.Fatalf("slot helper run: %+v %v", r, err)
	}
	if mode == "partial_at_close" {
		if err := o.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if rootObserver != 0 {
		_ = windows.CloseHandle(rootObserver)
	}
	t.Fatal("slot helper missed its crash boundary")
}

func readSlotsFile(t *testing.T, path string) slotsContainer {
	t.Helper()
	raw, err := readRegularBounded(path, slotsSize)
	if err != nil {
		t.Fatal(err)
	}
	c, err := decodeSlots(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func assertFreshSlotRun(t *testing.T, o *Owner, old []uuid.UUID) {
	t.Helper()
	r, err := o.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()})
	if err != nil || !r.TreeClosed || r.ExitCode != 0 {
		t.Fatalf("fresh admission failed: %+v %v", r, err)
	}
	for _, id := range old {
		if r.RunID == id {
			t.Fatal("reopened Owner reused a reserved identity")
		}
	}
}

func TestGitProcessSlotCrash(t *testing.T) {
	for _, mode := range []string{"temp_written", "file_synced", "renamed", "published", "running_durable", "recovery_closed", "recovery_temp"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			start := mode
			if mode == "recovery_closed" || mode == "recovery_temp" {
				start = "fresh"
			}
			p := startSlotHelper(t, root, start)
			n := p.notice(t)
			path := n.Batch
			file := path
			debris := mode == "temp_written" || mode == "file_synced"
			if debris {
				file += ".pending"
			}
			c := readSlotsFile(t, file)
			var rootHandle, job windows.Handle
			if n.PID != 0 {
				rootHandle = observer(t, n.PID, time.Now())
			}
			if start == "fresh" {
				var err error
				job, err = openJob(n.Job)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if job != 0 {
						_ = windows.TerminateJobObject(job, 1)
						_ = windows.CloseHandle(job)
					}
				}()
			}
			p.kill(t)
			if mode == "running_durable" {
				assertExited(t, rootHandle)
			}
			if start == "fresh" {
				recovery := startSlotHelper(t, root, mode)
				proofNotice := recovery.notice(t)
				wantRenames := 1
				if mode == "recovery_temp" {
					wantRenames = 0
				}
				if proofNotice.Syncs != 1 || proofNotice.Renames != wantRenames {
					t.Fatalf("found-Job recovery skipped full publication: %+v", proofNotice)
				}
				proofPath := proofNotice.Batch
				if mode == "recovery_temp" {
					proofPath += ".pending"
				}
				raw, err := readRegular(proofPath)
				if err != nil {
					t.Fatal("recovery did not persist its closed publication", err)
				}
				proof, err := decodeFence(raw)
				if err != nil || proof.State != "closed" || proof.Proof != "retained_job" || proof.RunID != n.RunID || proof.RootPID != 0 {
					t.Fatalf("recovery publication lost retained zero-slot proof: %+v %v", proof, err)
				}
				recovery.kill(t)
				assertExited(t, rootHandle)
				if err := windows.CloseHandle(job); err != nil {
					t.Fatal(err)
				}
				job = 0
			}
			o := nativeOwner(t, root)
			report := o.report()
			wantPending, wantReservations := 0, 1
			if debris {
				wantReservations = 0
			}
			if mode == "running_durable" {
				wantPending = 1
			}
			if len(report.Pending) != wantPending || report.Reservations != wantReservations {
				t.Fatalf("crash classification lost reservations or invented invocations: %+v", report)
			}
			if debris {
				if report.Debris.PendingRuns != 1 {
					t.Fatal("initial publication temp not counted as one debris file", report)
				}
			} else if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t)}); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("loaded reservation admitted Git before reconciliation", err)
			}
			counts := make(map[string]int)
			o.metadataHook = func(op, _ string, success bool) error {
				if success {
					counts[op]++
				}
				return nil
			}
			report, err := o.Reconcile(context.Background())
			wantClosed := 0
			if mode == "running_durable" || mode == "recovery_closed" {
				wantClosed = 1
			}
			if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != wantClosed {
				t.Fatalf("crash recovery failed or invented runs: %+v %v", report, err)
			}
			if mode == "running_durable" {
				r := report.Closed[0]
				if r.RunID != n.RunID || r.Proof != "name_absent" || r.ExitCode != -1 || r.TotalProcesses != 0 || counts["temp_sync"] != 1 || counts["rename"] != 1 {
					t.Fatalf("root-bearing recovery skipped durable proof: %+v counts=%v", r, counts)
				}
			}
			if mode == "recovery_closed" && (report.Closed[0].Proof != "retained_job" || report.Closed[0].ExitCode != -1) {
				t.Fatalf("durable recovered proof was discarded: %+v", report)
			}
			if mode == "recovery_temp" && (counts["temp_sync"] != 0 || counts["rename"] != 0 || counts["directory_sync"] != 1) {
				t.Fatalf("zero-slot stale closure did not elide and durably sweep: %v", counts)
			}
			o.metadataHook = nil
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("proved container survived", err)
			}
			entries, err := os.ReadDir(filepath.Join(root, "runs"))
			if err != nil || len(entries) != 0 {
				t.Fatal("recovery left a sidecar or temp", entries, err)
			}
			assertFreshSlotRun(t, o, c.Header.RunIDs)
		})
	}
}

func TestGitProcessSlotPreResumeCrash(t *testing.T) {
	for _, mode := range []string{"fresh", "exhausted_with_prelaunch_member"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			p := startSlotHelper(t, root, mode)
			n := p.notice(t)
			if n.Kind != "root_created" || n.PID == 0 {
				t.Fatalf("missing suspended pre-frame root: %+v", n)
			}
			h := observer(t, n.PID, time.Now())
			job, err := openJob(n.Job)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.TerminateJobObject(job, 1); _ = windows.CloseHandle(job) }()
			if mode != "fresh" {
				p.ack(t)
				for range slotCount - 1 {
					prefill := p.notice(t)
					if prefill.Kind != "prefill" {
						t.Fatalf("missing real prefill: %+v", prefill)
					}
					observer(t, prefill.PID, time.Now())
					p.ack(t)
				}
				exhausted := p.notice(t)
				if exhausted.Kind != "exhausted" || exhausted.Batch != n.Batch {
					t.Fatal("exhaustion changed pending batch", exhausted)
				}
			}
			c := readSlotsFile(t, n.Batch)
			for i, id := range c.Header.RunIDs {
				if id == n.RunID && (c.Frames[i] != (fence{}) || c.Retired[i] != 0) {
					t.Fatal("pre-frame slot acquired an invented root or closure")
				}
			}
			p.kill(t)
			state, err := windows.WaitForSingleObject(h, 0)
			if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
				t.Fatal("retained pre-resume root was not live", state, err)
			}
			o := nativeOwner(t, root)
			before := o.report()
			if before.Reservations != 1 || len(before.Pending) != 0 {
				t.Fatalf("prelaunch member was retired or invented: %+v", before)
			}
			if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t)}); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("pre-resume reservation admitted Git", err)
			}
			report, err := o.Reconcile(context.Background())
			if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != 1 {
				t.Fatalf("retained zero-slot resource lost or duplicated: %+v %v", report, err)
			}
			r := report.Closed[0]
			if r.RunID != n.RunID || r.Proof != "retained_job" || r.ExitCode != -1 || r.TotalProcesses != 1 {
				t.Fatalf("zero-slot resource proof lost: %+v", r)
			}
			assertExited(t, h)
			assertFreshSlotRun(t, o, c.Header.RunIDs)
		})
	}
}

func TestGitProcessSlotRetirementCrash(t *testing.T) {
	for _, mode := range []string{"before_remove", "after_remove", "partial_at_close"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			p := startSlotHelper(t, root, mode)
			var target processNotice
			for {
				n := p.notice(t)
				if n.Kind == "prefill" {
					observer(t, n.PID, time.Now())
					p.ack(t)
					continue
				}
				target = n
				break
			}
			if target.Kind != "target" {
				t.Fatal("missing proven target", target)
			}
			h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(target.PID))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(h)
			c := readSlotsFile(t, target.Batch)
			p.ack(t)
			n := p.notice(t)
			if !strings.HasPrefix(n.Kind, "retirement_") {
				t.Fatal("missing retirement boundary", n)
			}
			p.kill(t)
			assertExited(t, h)
			o := nativeOwner(t, root)
			report := o.report()
			wantReservations, wantPending := 1, 0
			if mode == "after_remove" {
				wantReservations = 0
			}
			if mode == "before_remove" {
				wantPending = 1
			}
			if report.Reservations != wantReservations || len(report.Pending) != wantPending {
				t.Fatalf("retirement lost unfinished target or invented completed runs: %+v", report)
			}
			report, err = o.Reconcile(context.Background())
			if err != nil || len(report.Pending) != 0 || report.Reservations != 0 || len(report.Closed) != wantPending {
				t.Fatalf("retirement recovery failed: %+v %v", report, err)
			}
			if wantPending == 1 && (report.Closed[0].RunID != target.RunID || report.Closed[0].Proof != "name_absent" || report.Closed[0].ExitCode != -1) {
				t.Fatalf("retirement proof changed: %+v", report)
			}
			if _, err := os.Stat(target.Batch); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("retired container survived", err)
			}
			assertFreshSlotRun(t, o, c.Header.RunIDs)
		})
	}
}

func TestGitProcessSlotSetupFailure(t *testing.T) {
	t.Parallel()
	o := nativeOwner(t, t.TempDir())
	started := false
	r, err := o.Run(context.Background(), Spec{Program: filepath.Join(o.root, "nonexistent.exe"), Started: func(int) { started = true }})
	if err == nil || errors.Is(err, ErrUnconfirmed) || started || !r.RootExited || !r.TreeClosed || !r.IOJoined {
		t.Fatalf("definitive setup failure stranded resources or resumed: %+v %v", r, err)
	}
	_, frame, hint := readSlotRun(t, o.root, r.RunID)
	if frame != (fence{}) || hint != 0 {
		t.Fatal("setup failure invented a durable root or retired zero slot")
	}
	assertFreshSlotRun(t, o, []uuid.UUID{r.RunID})
}

func TestGitProcessDurableBound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	o := nativeOwner(t, root)
	var mu sync.Mutex
	counts, attempts := make(map[string]int), make(map[string]int)
	reserved := make(map[uuid.UUID]string)
	frames := make(map[uuid.UUID]fence)
	identities := make(map[uint32]fence)
	oldFrames := make(map[string]map[int][]byte)
	o.metadataHook = func(op, path string, success bool) error {
		mu.Lock()
		defer mu.Unlock()
		if success {
			counts[op]++
		} else {
			attempts[op]++
		}
		if success && op == "rename" && strings.HasSuffix(path, ".slots") {
			c := readSlotsFile(t, path)
			if !c.initial() {
				t.Error("new reservation published nonzero frames")
			}
			for _, id := range c.Header.RunIDs {
				if _, exists := reserved[id]; exists {
					t.Error("batch reused a reserved name")
				}
				reserved[id] = path
			}
		}
		if op == "frame_write" && !success || op == "footer_write" && success {
			raw, err := readRegularBounded(path, slotsSize)
			if err != nil {
				t.Fatal(err)
			}
			for i, old := range oldFrames[path] {
				if !bytes.Equal(raw[slotHeaderSize+i*slotFrameSize:slotHeaderSize+(i+1)*slotFrameSize], old) {
					t.Error("write-once root frame changed after its durable barrier")
				}
			}
		}
		return nil
	}
	o.observe = func(phase string, f fence) {
		if phase == "launch_pending" || phase == "root_created" {
			mu.Lock()
			path, exists := reserved[f.RunID]
			mu.Unlock()
			if !exists || path == "" {
				t.Error("native root preceded durable reservation publication")
			}
			if phase == "launch_pending" {
				h, err := openJob(f.JobName)
				if h != 0 {
					_ = windows.CloseHandle(h)
				}
				if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
					t.Error("Job existed before launch_pending", err)
				}
			}
		}
		if phase == "suspended" {
			o.reservationMu.Lock()
			path, stored, _ := readSlotRun(t, o.root, f.RunID)
			c := readSlotsFile(t, path)
			o.reservationMu.Unlock()
			if stored != f {
				t.Error("suspended root was not recorded exactly")
			}
			mu.Lock()
			if _, exists := frames[f.RunID]; exists {
				t.Error("invocation reused a consumed identity")
			}
			frames[f.RunID], identities[f.RootPID] = f, f
			if oldFrames[path] == nil {
				oldFrames[path] = make(map[int][]byte)
			}
			for i, frame := range c.Frames {
				if frame != (fence{}) {
					encoded, err := encodeSlotFrame(frame)
					if err != nil {
						t.Fatal(err)
					}
					if old, exists := oldFrames[path][i]; exists && !bytes.Equal(old, encoded) {
						t.Error("old root frame was rewritten")
					}
					oldFrames[path][i] = encoded
				}
			}
			mu.Unlock()
		}
	}
	program := gitProgram(t)
	started := func(pid int) {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			t.Fatal(err)
		}
		created, err := processCreated(h)
		_ = windows.CloseHandle(h)
		mu.Lock()
		f, exists := identities[uint32(pid)]
		mu.Unlock()
		if err != nil || !exists || f.RootPID != uint32(pid) || f.RootCreated != created {
			t.Error("resumed root identity disagreed with independent handle query", err)
		}
	}
	checkCounts := func(flushes, renames, opens, closes int) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if counts["temp_sync"]+counts["frame_sync"]+counts["rename"]+counts["directory_sync"] != flushes || counts["rename"] != renames || counts["writer_open"] != opens || counts["writer_close"] != closes {
			t.Fatalf("durable admission bound exceeded or barrier omitted: %v want flush/rename/open/close=%d/%d/%d/%d", counts, flushes, renames, opens, closes)
		}
	}
	for range 33 {
		r, err := o.Run(context.Background(), Spec{Program: program, Args: []string{"version"}, Env: os.Environ(), Started: started})
		if err != nil || !r.TreeClosed {
			t.Fatalf("serial bound run: %+v %v", r, err)
		}
	}
	checkCounts(37, 2, 2, 1)
	ready, ended := make(chan struct{}, 8), make(chan error, 8)
	var writers []*io.PipeWriter
	for range 8 {
		reader, writer := io.Pipe()
		writers = append(writers, writer)
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		go func() {
			r, err := o.Run(context.Background(), Spec{Program: program, Args: []string{"hash-object", "--stdin"}, Env: os.Environ(), Stdin: reader, Started: func(pid int) { started(pid); ready <- struct{}{} }})
			if !r.TreeClosed || !r.IOJoined {
				err = errors.Join(err, ErrUnconfirmed)
			}
			ended <- err
		}()
	}
	for range 8 {
		<-ready
	}
	for _, writer := range writers {
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for range 8 {
		if err := <-ended; err != nil {
			t.Fatal(err)
		}
	}
	checkCounts(45, 2, 2, 1)
	if len(frames) != 41 || counts["frame_write"] != 41 || counts["frame_sync"] != 41 || counts["temp_sync"] != 2 {
		t.Fatal("a resumed child lacked its own durability completion", counts)
	}
	var all []uuid.UUID
	for id := range reserved {
		all = append(all, id)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	checkCounts(45, 2, 2, 2)
	for op, count := range counts {
		if attempts[op] != count {
			t.Fatal("successful bound run concealed a failed operation", op, counts, attempts)
		}
	}
	t.Logf("serial=37/2/2 overlap=45/2/2 final writer closes=2; operations=%v", counts)
	reopened := nativeOwner(t, root)
	assertFreshSlotRun(t, reopened, all)
}

func TestGitProcessSlotErrors(t *testing.T) {
	t.Run("layout", slotLayoutErrors)
	t.Run("native", slotNativeErrors)
	t.Run("io", slotIOErrors)
}

func slotNativeErrors(t *testing.T) {
	for _, row := range []string{"session", "access-denied", "limits", "query", "cancel", "live-retired", "recovered-sync", "zero-temp-remove", "zero-temp-sync"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			seed := nativeOwner(t, root)
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			c := fixtureSlots(t, seed)
			session := c.Header.Session
			f := c.Header.candidate(0)
			var job windows.Handle
			var pi windows.ProcessInformation
			var err error
			switch row {
			case "session":
				c.Header.Session++
			case "access-denied", "limits", "query", "recovered-sync":
				limit := seed.options.MaxDescendants
				if row == "limits" {
					limit++
				}
				job, err = createJob(f.JobName, limit)
				if err != nil {
					t.Fatal(err)
				}
				if row == "access-denied" {
					sd, err := windows.SecurityDescriptorFromString("D:P(D;;GA;;;WD)")
					if err != nil {
						t.Fatal(err)
					}
					dacl, _, err := sd.DACL()
					if err != nil {
						t.Fatal(err)
					}
					if err := windows.SetSecurityInfo(job, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
						t.Fatal(err)
					}
					probe, err := openJob(f.JobName)
					if probe != 0 {
						_ = windows.CloseHandle(probe)
					}
					if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
						t.Fatal("reservation deny DACL control failed", err)
					}
				}
			case "live-retired":
				job, err = createJob(jobName(seed.rootID, uuid.NewV7()), seed.options.MaxDescendants)
				if err != nil {
					t.Fatal(err)
				}
				pipes, err := newPipes(false)
				if err != nil {
					t.Fatal(err)
				}
				pi, err = createSuspended(Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}, job, pipes)
				_ = pipes.closeChildren()
				pipes.closeParents()
				if err != nil {
					t.Fatal(err)
				}
				f.State, f.RootPID = "running", pi.ProcessId
				f.RootCreated, err = processCreated(pi.Process)
				if err != nil {
					t.Fatal(err)
				}
				c.Frames[0], c.Retired[0] = f, 1
			}
			defer func() {
				if job != 0 {
					_ = windows.TerminateJobObject(job, 1)
					_ = windows.CloseHandle(job)
				}
				if pi.Process != 0 {
					assertExited(t, pi.Process)
					_ = windows.CloseHandle(pi.Process)
					_ = windows.CloseHandle(pi.Thread)
				}
			}()
			raw, err := encodeSlots(c)
			if err != nil {
				t.Fatal(err)
			}
			path := seed.slotsPath(c.Header.BatchID)
			if err := writeMarkerBytes(path, raw, nil); err != nil {
				t.Fatal(err)
			}
			if row == "zero-temp-remove" || row == "zero-temp-sync" {
				total := uint32(1)
				f.State, f.Proof, f.TotalProcesses = "closed", "retained_job", &total
				encoded, err := canonicalJSON(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(seed.fencePath(f.RunID)+".pending", encoded, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			o := nativeOwner(t, root)
			o.options.StopGrace = 20 * time.Millisecond
			if row == "query" {
				o.queryFault = func() error { return windows.ERROR_INVALID_HANDLE }
			}
			if row == "recovered-sync" {
				o.metadataHook = func(op, path string, success bool) error {
					if op == "temp_sync" && !success && strings.HasSuffix(path, ".json") {
						return windows.ERROR_WRITE_FAULT
					}
					return nil
				}
			}
			if row == "zero-temp-remove" || row == "zero-temp-sync" {
				o.metadataHook = func(op, path string, success bool) error {
					if !success && (row == "zero-temp-remove" && op == "remove" && strings.HasSuffix(path, ".json.pending") || row == "zero-temp-sync" && op == "directory_sync") {
						return windows.ERROR_WRITE_FAULT
					}
					return nil
				}
			}
			ctx := context.Background()
			if row == "cancel" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			report, err := o.Reconcile(ctx)
			if !errors.Is(err, ErrUnconfirmed) || report.Reservations != 1 || len(report.Pending) != 0 || len(report.Closed) != 0 {
				t.Fatalf("reservation-only or retired state accepted without exact proof: %+v %v", report, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("unconfirmed reservation bytes changed", err)
			}
			if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t)}); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("unproved reservation admitted Git", err)
			}
			if job != 0 {
				if err := windows.TerminateJobObject(job, 1); err != nil {
					t.Fatal(err)
				}
				if pi.Process != 0 {
					assertExited(t, pi.Process)
				}
				if err := windows.CloseHandle(job); err != nil {
					t.Fatal(err)
				}
				job = 0
			}
			o.queryFault, o.metadataHook = nil, nil
			retrySyncs := 0
			if row == "zero-temp-remove" || row == "zero-temp-sync" {
				o.metadataHook = func(op, _ string, success bool) error {
					if op == "directory_sync" && success {
						retrySyncs++
					}
					return nil
				}
			}
			if row == "session" {
				batch := o.loadedSlots[c.Header.BatchID]
				batch.data.Header.Session = session
				fixed, err := encodeSlots(batch.data)
				if err != nil {
					t.Fatal(err)
				}
				if n, err := batch.writer.WriteAt(fixed, 0); err != nil || n != len(fixed) {
					t.Fatal("fixture session repair failed", err)
				}
			}
			report, err = o.Reconcile(context.Background())
			if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != 0 {
				t.Fatal("unobstructed reservation recovery failed", report, err)
			}
			if (row == "zero-temp-remove" || row == "zero-temp-sync") && retrySyncs != 1 {
				t.Fatal("recovery retired before completing the interrupted debris barrier", retrySyncs)
			}
		})
	}
}

func slotIOErrors(t *testing.T) {
	for _, operation := range []string{"temp_sync", "rename", "writer_open", "frame_write", "frame_sync", "footer_write", "short_footer", "writer_close", "partial_delete"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			p := startSlotHelper(t, root, "error:"+operation)
			var done processNotice
			for {
				n := p.notice(t)
				if n.Kind == "pin" {
					observer(t, n.PID, time.Now())
					p.ack(t)
					continue
				}
				done = n
				break
			}
			if done.Kind != "done" {
				t.Fatal("fault helper omitted retention proof", done)
			}
			p.finish(t)
			o := nativeOwner(t, root)
			report := o.report()
			wantClosed := 0
			if operation == "frame_sync" || operation == "footer_write" || operation == "short_footer" {
				wantClosed = 1
			}
			if len(report.Pending) != wantClosed {
				t.Fatal("fault changed root-frame recovery obligations", report)
			}
			report, err := o.Reconcile(context.Background())
			if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != wantClosed {
				t.Fatal("fault's surviving layout could not recover", report, err)
			}
			if wantClosed == 1 && (report.Closed[0].RunID != done.RunID || report.Closed[0].Proof != "name_absent" || report.Closed[0].ExitCode != -1) {
				t.Fatal("fault recovery inferred success", report)
			}
			assertFreshSlotRun(t, o, []uuid.UUID{done.RunID})
		})
	}
}

func slotErrorHelper(t *testing.T, operation string) {
	o, _, err := Open(context.Background(), os.Getenv("DWARF_GITPROCESS_ROOT"), Options{RequireTree: true})
	if err != nil {
		t.Fatal(err)
	}
	var c slotsContainer
	var path string
	faultSeen, resumed, roots := false, 0, 0
	var held windows.Handle
	defer func() {
		if held != 0 {
			_ = windows.CloseHandle(held)
		}
	}()
	o.metadataHook = func(op, file string, success bool) error {
		if op == "temp_write" && success && strings.HasSuffix(file, ".slots") {
			c = readSlotsFile(t, file+".pending")
			path = file
		}
		target := operation
		if target == "short_footer" {
			target = "footer_write"
		}
		if op == target && !success {
			faultSeen = true
			if operation == "short_footer" {
				return io.ErrShortWrite
			}
			return windows.ERROR_WRITE_FAULT
		}
		return nil
	}
	o.observe = func(phase string, f fence) {
		if phase == "root_created" {
			roots++
			if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "pin", PID: int(f.RootPID), RunID: f.RunID}); err != nil {
				t.Fatal(err)
			}
			var b [1]byte
			if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "resumed" {
			resumed++
		}
		if phase == "closed" && operation == "partial_delete" {
			p, err := windows.UTF16PtrFromString(path)
			if err != nil {
				t.Fatal(err)
			}
			held, err = windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal("compatible deletion obstruction failed", err)
			}
		}
	}
	spec := Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}
	r, runErr := o.Run(context.Background(), spec)
	atClose := operation == "writer_close" || operation == "partial_delete"
	if atClose {
		if runErr != nil || !r.TreeClosed {
			t.Fatalf("partial batch failed before Close: %+v %v", r, runErr)
		}
		runErr = o.Close()
	}
	if !errors.Is(runErr, ErrUnconfirmed) || operation != "partial_delete" && !faultSeen {
		t.Fatalf("metadata failure authorized reuse or skipped its barrier: %+v %v fault=%v resumed=%d", r, runErr, faultSeen, resumed)
	}
	if operation == "frame_write" || operation == "frame_sync" {
		if resumed != 0 || !r.RootExited || !r.TreeClosed || !r.IOJoined {
			t.Fatalf("failed frame barrier resumed or lost suspended cleanup: %+v resumed=%d", r, resumed)
		}
	}
	if operation == "temp_sync" || operation == "rename" || operation == "writer_open" {
		if roots != 0 || resumed != 0 {
			t.Fatal("failed reservation publication created a native root")
		}
		for _, id := range c.Header.RunIDs {
			h, err := openJob(jobName(c.Header.RootID, id))
			if h != 0 {
				_ = windows.CloseHandle(h)
			}
			if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
				t.Fatal("failed publication created a reserved Job", err)
			}
		}
	}
	if _, err := o.Run(context.Background(), spec); !errors.Is(err, ErrUnconfirmed) && !(atClose && errors.Is(err, ErrClosed)) {
		t.Fatal("metadata failure admitted another Run", err)
	}
	first, second := o.Close(), o.Close()
	if !errors.Is(first, ErrUnconfirmed) || first != second {
		t.Fatal("Close discarded or changed retained metadata failure", first, second)
	}
	reopened, _, err := Open(context.Background(), o.root, Options{RequireTree: true})
	if reopened != nil {
		_ = reopened.Close()
	}
	if !errors.Is(err, durable.ErrLocked) {
		t.Fatal("metadata failure released the Owner lock", err)
	}
	if operation == "footer_write" || operation == "short_footer" || atClose {
		_, frame, hint := readSlotRun(t, o.root, r.RunID)
		if frame.State != "running" || frame.RunID != r.RunID {
			t.Fatal("cleanup failure cleared or rewrote root frame", frame)
		}
		if !atClose && hint != 0 {
			t.Fatal("failed footer write retired the invocation")
		}
	}
	if held != 0 {
		if err := windows.CloseHandle(held); err != nil {
			t.Fatal(err)
		}
		held = 0
	}
	if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "done", RunID: r.RunID, Batch: path}); err != nil {
		t.Fatal(err)
	}
}

// A suspended holder that denies the caller keeps one PID. A frame recording
// that PID with a creation time 100 ns before the holder's, the nearest
// distinct identity, is the state PID reuse leaves behind.
func TestGitProcessRootReuse(t *testing.T) {
	for _, row := range []string{"foreign-denied-retired", "foreign-denied-live", "own-denied", "snapshot-fault", "list-guards"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			seed := nativeOwner(t, root)
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			job, err := createJob(jobName(seed.rootID, uuid.NewV7()), seed.options.MaxDescendants)
			if err != nil {
				t.Fatal(err)
			}
			var pi windows.ProcessInformation
			defer func() {
				_ = windows.TerminateJobObject(job, 1)
				_ = windows.CloseHandle(job)
				if pi.Process != 0 {
					assertExited(t, pi.Process)
					_ = windows.CloseHandle(pi.Process)
					_ = windows.CloseHandle(pi.Thread)
				}
			}()
			pipes, err := newPipes(false)
			if err != nil {
				t.Fatal(err)
			}
			pi, err = createSuspended(Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}, job, pipes)
			_ = pipes.closeChildren()
			pipes.closeParents()
			if err != nil {
				t.Fatal(err)
			}
			created, err := processCreated(pi.Process)
			if err != nil {
				t.Fatal(err)
			}
			original, err := windows.GetSecurityInfo(pi.Process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			setDACL := func(sd *windows.SECURITY_DESCRIPTOR) {
				dacl, _, err := sd.DACL()
				if err == nil {
					err = windows.SetSecurityInfo(pi.Process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			// debugPrivilege reports SeDebugPrivilege's state in this process's
			// token, the one token state that bypasses a process DACL.
			debugPrivilege := func() string {
				name, err := windows.UTF16PtrFromString("SeDebugPrivilege")
				if err != nil {
					return err.Error()
				}
				var luid windows.LUID
				if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
					return err.Error()
				}
				var token windows.Token
				if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
					return err.Error()
				}
				defer token.Close()
				privileges := make([]byte, 4096)
				var n uint32
				if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &privileges[0], uint32(len(privileges)), &n); err != nil {
					return err.Error()
				}
				// TOKEN_PRIVILEGES: a DWORD count, then 12-byte LUID_AND_ATTRIBUTES.
				word := func(at int) uint32 {
					return uint32(privileges[at]) | uint32(privileges[at+1])<<8 | uint32(privileges[at+2])<<16 | uint32(privileges[at+3])<<24
				}
				for i := range min(int(word(0)), (int(n)-4)/12) {
					if at := 4 + 12*i; word(at) == luid.LowPart && int32(word(at+4)) == luid.HighPart {
						return strconv.FormatBool(word(at+8)&windows.SE_PRIVILEGE_ENABLED != 0)
					}
				}
				return "not held"
			}
			deny, err := windows.SecurityDescriptorFromString("D:P(D;;GA;;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			setDACL(deny)
			probe, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pi.ProcessId)
			if probe != 0 {
				_ = windows.CloseHandle(probe)
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("deny DACL control failed: %v; SeDebugPrivilege enabled: %s", err, debugPrivilege())
			}
			if row == "list-guards" {
				selfCreated, err := processCreated(windows.CurrentProcess())
				if err != nil {
					t.Fatal(err)
				}
				self, holder := processIdentity{windows.GetCurrentProcessId(), selfCreated}, processIdentity{pi.ProcessId, created}
				list, n, err := systemProcesses(nil)
				if err != nil {
					t.Fatal(err)
				}
				// Self, a stale identity of the holder's PID and the holder, one
				// stride apart, written through the x/sys struct the scan reads.
				stride := uint32(processEntrySize) + 64
				synthesized := func(links [3]uint32) []uint64 {
					buf := make([]uint64, 3*stride/8)
					for i, id := range []processIdentity{self, {holder.pid, holder.created - 1}, holder} {
						entry := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Add(unsafe.Pointer(&buf[0]), i*int(stride)))
						entry.NextEntryOffset, entry.UniqueProcessID, entry.CreateTime = links[i], uintptr(id.pid), int64(id.created)
					}
					return buf
				}
				synth := synthesized([3]uint32{stride, stride, 0})
				// Each refusal changes one input of the listed control above it.
				for _, c := range []struct {
					name string
					buf  []uint64
					n    uint32
					self processIdentity
					want error // nil: listed
				}{
					{"real-control", list, n, self, nil},
					{"self-absent", list, n, processIdentity{1, self.created}, errProcessListSelf},
					{"self-time-mismatched", list, n, processIdentity{self.pid, self.created + 1}, errProcessListSelf},
					{"synthesized-control", synth, 3 * stride, self, nil},
					{"cut", synth, stride + uint32(processEntrySize)/2, self, errProcessListBound},
					{"short-link", synthesized([3]uint32{stride, uint32(processEntrySize) - 8, 0}), 3 * stride, self, errProcessListBound},
					{"past-buffer", synth, 3*stride + 8, self, errProcessListBound},
				} {
					t.Run(c.name, func(t *testing.T) {
						listed, err := scanProcesses(c.buf, c.n, holder, c.self)
						if c.want == nil && (!listed || err != nil) || c.want != nil && (listed || !errors.Is(err, c.want)) {
							t.Fatalf("scan verdict listed=%t err=%v, want listed=%t err=%v", listed, err, c.want == nil, c.want)
						}
					})
				}
				return
			}
			c := fixtureSlots(t, seed)
			f := c.Header.candidate(0)
			f.State, f.RootPID, f.RootCreated = "running", pi.ProcessId, created-1
			if row == "own-denied" {
				f.RootCreated = created
			}
			c.Frames[0] = f
			if row == "foreign-denied-retired" || row == "snapshot-fault" {
				c.Retired[0] = 1
			}
			raw, err := encodeSlots(c)
			if err != nil {
				t.Fatal(err)
			}
			path := seed.slotsPath(c.Header.BatchID)
			if err := writeMarkerBytes(path, raw, nil); err != nil {
				t.Fatal(err)
			}
			o := nativeOwner(t, root)
			grace := o.options.StopGrace
			o.options.StopGrace = 20 * time.Millisecond // Recovery's verdicts need no wait.
			if row == "snapshot-fault" {
				o.queryFault = func() error { return windows.ERROR_INVALID_HANDLE }
			}
			holderLive := func() {
				if state, err := windows.WaitForSingleObject(pi.Process, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
					t.Fatal("recovery disturbed the PID's current holder", state, err)
				}
			}
			refused := func() {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(raw, after) {
					t.Fatal("unconfirmed reservation bytes changed", err)
				}
				if _, err := o.Run(context.Background(), Spec{Program: gitProgram(t)}); !errors.Is(err, ErrUnconfirmed) {
					t.Fatal("unproved reservation admitted Git", err)
				}
			}
			report, err := o.Reconcile(context.Background())
			reason, cause := "", error(nil)
			var refusal *UnconfirmedError
			if errors.As(err, &refusal) {
				reason, cause = refusal.Reason, refusal.Err
			}
			switch row {
			case "foreign-denied-retired", "foreign-denied-live":
				closed := 0 // A retired hint is processed but not reported.
				if row == "foreign-denied-live" {
					closed = 1
				}
				if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != closed || closed == 1 && (report.Closed[0].RunID != f.RunID || report.Closed[0].Proof != "name_absent") {
					t.Fatalf("reused root PID refused: reason=%q cause=%v access_denied=%t report=%+v err=%v", reason, cause, errors.Is(err, windows.ERROR_ACCESS_DENIED), report, err)
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("reconciled container survived", err)
				}
				holderLive()
				// A fresh Run's own closure proof keeps the calibrated grace.
				o.options.StopGrace = grace
				assertFreshSlotRun(t, o, c.Header.RunIDs)
			case "own-denied":
				// A hint-zero frame stays pending until its proof succeeds.
				if reason != "root_unconfirmed" || !errors.Is(err, windows.ERROR_ACCESS_DENIED) || report.Reservations != 1 || len(report.Pending) != 1 || report.Pending[0].RunID != f.RunID || len(report.Closed) != 0 {
					t.Fatalf("live unopenable root accepted: reason=%q cause=%v report=%+v err=%v", reason, cause, report, err)
				}
				if want := "root_unconfirmed: " + windows.ERROR_ACCESS_DENIED.Error(); !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal message lacks its cause: %q", err)
				}
				refused()
				// Remove the cause: an opened, signaled root is gone.
				setDACL(original)
				if err := windows.TerminateJobObject(job, 1); err != nil {
					t.Fatal(err)
				}
				assertExited(t, pi.Process)
				report, err := o.Reconcile(context.Background())
				if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != 1 || report.Closed[0].RunID != f.RunID || report.Closed[0].Proof != "name_absent" {
					t.Fatalf("ended root refused: %+v %v", report, err)
				}
			case "snapshot-fault":
				if reason != "root_unconfirmed" || !errors.Is(err, windows.ERROR_INVALID_HANDLE) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) || report.Reservations != 1 || len(report.Pending) != 0 || len(report.Closed) != 0 {
					t.Fatalf("failed process list taken as absence: reason=%q cause=%v report=%+v err=%v", reason, cause, report, err)
				}
				refused()
				holderLive()
				o.queryFault = nil
				report, err := o.Reconcile(context.Background())
				if err != nil || report.Reservations != 0 || len(report.Pending) != 0 || len(report.Closed) != 0 {
					t.Fatalf("unobstructed process list refused: %+v %v", report, err)
				}
				holderLive()
			}
		})
	}
}

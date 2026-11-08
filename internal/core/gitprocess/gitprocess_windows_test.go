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
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
	"golang.org/x/sys/windows"
)

type processNotice struct {
	Kind  string
	PID   int
	Job   string
	RunID uuid.UUID
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
		defer func() {
			if rootObserver != 0 {
				_ = windows.CloseHandle(rootObserver)
			}
			if heldFence != 0 {
				_ = windows.CloseHandle(heldFence)
			}
		}()
		o.observe = func(phase string, f fence) {
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
				path, err := windows.UTF16PtrFromString(o.fencePath(f.RunID))
				if err != nil {
					t.Fatal(err)
				}
				heldFence, err = windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := json.NewEncoder(os.Stdout).Encode(processNotice{Kind: "closed", PID: int(f.RootPID), RunID: f.RunID}); err != nil {
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
		raw, err := readRegular(o.fencePath(r.RunID))
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
	if err := decoder.Decode(&notice); err != nil {
		t.Fatalf("closure helper failed readiness: %v %s", err, stderr.String())
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
	if _, err := os.Stat(o.fencePath(notice.RunID)); !errors.Is(err, os.ErrNotExist) {
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

package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
)

var runnerKernel = syscall.NewLazyDLL("kernel32.dll")
var runnerOpenProcess = runnerKernel.NewProc("OpenProcess")
var runnerWaitProcess = runnerKernel.NewProc("WaitForSingleObject")
var runnerCloseHandle = runnerKernel.NewProc("CloseHandle")

func TestGitRunnerProcessLimitIsNeverExpected(t *testing.T) {
	t.Parallel()
	g := testGitRunner(t, nil)
	owner, _, err := gitprocess.Open(context.Background(), t.TempDir(), gitprocess.Options{RequireTree: true, MaxDescendants: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	// Validate the installed Git normally, then limit only this invocation.
	// Git for Windows' launcher itself may need to spawn the actual git.exe.
	g.owner = owner
	_, exit, err := g.run(context.Background(), "", inspectionEnvironment(os.Environ()), nil, []int{0, 1, 128}, "-c", "alias.limit-proof=!echo limit-proof", "limit-proof")
	if !errors.Is(err, ErrGit) || !errors.Is(err, gitprocess.ErrLimit) || exit < 0 {
		t.Fatalf("expected exit swallowed process limit: exit=%d err=%v", exit, err)
	}
}

func TestGitRunnerTree(t *testing.T) {
	g := testGitRunner(t, nil)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, send := io.Pipe()
	defer input.Close()
	defer send.Close()
	output, receive := io.Pipe()
	defer output.Close()
	defer receive.Close()
	var proof gitprocess.Result
	g.afterRun = func(result gitprocess.Result) { proof = result }
	done := make(chan error, 1)
	go func() {
		_, err := g.runOutput(context.Background(), "", append(inspectionEnvironment(os.Environ()), "DWARF_GIT_RUNNER_CHILD=1"), input, receive, []int{0}, "-c", `alias.held=!"`+filepath.ToSlash(exe)+`" -test.run=^TestGitRunnerHelper$`, "held")
		_ = receive.Close()
		done <- err
	}()
	decoder := json.NewDecoder(output)
	var pid int
	if err := decoder.Decode(&pid); err != nil {
		t.Fatal(err)
	}
	h, _, nativeErr := runnerOpenProcess.Call(0x00100000|0x1000, 0, uintptr(pid))
	if h == 0 {
		t.Fatal(nativeErr)
	}
	defer runnerCloseHandle.Call(h)
	state, _, _ := runnerWaitProcess.Call(h, 0)
	if state != 258 {
		t.Fatal("held descendant control was not live", state)
	}
	if _, err := send.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = send.Close()
	var drained bytes.Buffer
	_, err = io.Copy(&drained, io.MultiReader(decoder.Buffered(), output))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !proof.RootExited || !proof.TreeClosed || !proof.IOJoined || proof.ExitCode != 0 {
		t.Fatalf("runner returned before tree closure: %+v", proof)
	}
	// Use the package's existing owned-Git exit bound for kernel rundown.
	state, _, nativeErr = runnerWaitProcess.Call(h, uintptr(time.Minute/time.Millisecond))
	if state != 0 {
		t.Fatalf("runner descendant survived: %d %v", state, nativeErr)
	}
	if !strings.Contains(drained.String(), "runner-output-before-exit\n") {
		t.Fatal("stream drain lost written output")
	}
}
func TestGitRunnerHelper(t *testing.T) {
	if os.Getenv("DWARF_GIT_RUNNER_CHILD") != "1" {
		return
	}
	program := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	child := exec.Command(program, "/d", "/q", "/c", "waitfor /t 86400 dwarfrunner"+strings.ReplaceAll(uuid.NewV7().String(), "-", ""))
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
	fmt.Fprintln(os.Stdout, "runner-output-before-exit")
	os.Exit(0)
}

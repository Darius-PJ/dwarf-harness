package winexec

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Every payload/control byte uses the same capped standard streams as user data.
func TestWinexecPayload(t *testing.T) {
	t.Parallel()
	marker := -1
	for i, arg := range os.Args {
		if arg == "--winexec-payload" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	if len(args) == 0 {
		t.Fatal("missing payload operation")
	}
	switch args[0] {
	case "resource":
		payloadResourceEvidence(t)
	case "exchange":
		if len(args) != 3 {
			t.Fatal("missing protected targets")
		}
		// This denial is deliberately the first operation after initial resume.
		if err := os.WriteFile(args[1], []byte("changed"), 0o600); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("protected write: %v", err)
		}
		if _, err := os.ReadFile(args[2]); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("host LocalAppData read: %v", err)
		}
		file, err := os.ReadFile("nonce")
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
		if err != nil || len(stdin) > 4096 {
			t.Fatalf("stdin: %v, %d bytes", err, len(stdin))
		}
		if err := os.WriteFile("nonce", []byte("changed"), 0o600); !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("sealed input write: %v", err)
		}
		fmt.Fprintf(os.Stdout, "temp=%s\n", os.Getenv("TEMP"))
		payloadFileRoundTrip(t, filepath.Join(os.Getenv("TEMP"), "temp-write-proof"), stdin)
		payloadFileRoundTrip(t, filepath.Join(os.Getenv("DWARF_WORK"), "work-write-proof"), stdin)
		sum := sha256.Sum256(append(file, stdin...))
		fmt.Fprintf(os.Stdout, "digest=%x\n", sum)
		fmt.Fprintln(os.Stderr, "stderr-nonce")
	case "canaries":
		if len(args) != 4 {
			t.Fatal("missing canaries")
		}
		if _, err := os.ReadFile(args[1]); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("Core-only read: %v", err)
		}
		for i, path := range args[2:] {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(os.Stdout, "canary-%d=%s\n", i, b)
		}
	case "network":
		if len(args) != 2 {
			t.Fatal("missing listener")
		}
		conn, err := net.DialTimeout("tcp", args[1], 2*time.Second)
		if err == nil {
			_, _ = conn.Write([]byte("sandbox-network-nonce"))
			_ = conn.Close()
			t.Fatal("network unexpectedly connected")
		}
		var code syscall.Errno
		native := errors.As(err, &code)
		fmt.Fprintf(os.Stdout, "network-denied native=%t code=%d error=%v\n", native, uint32(code), err)
	case "process-count":
		payloadProcessCount(t)
	case "held-tree":
		payloadHeldTree(t)
	case "held-tree-child":
		payloadHeldCommand(t, false, true)
	case "descendant-root":
		payloadDescendantRoot(t, args[1:])
	case "descendant-child":
		payloadDescendantChild(t, args[1:])
	case "descendant-grandchild":
		payloadDescendantGrandchild(t, args[1:])
	case "links":
		payloadLinks(t)
	case "protected":
		payloadProtectedTargets(t, args[1:])
	case "inherited-handles":
		payloadInheritedHandles(t)
	case "peer":
		payloadPeer(t, args[1:])
	case "memory":
		payloadMemory(t)
	case "cpu":
		fmt.Fprintln(os.Stdout, "cpu-ready")
		var n uint64
		for {
			n++
			if n == 0 {
				fmt.Fprintln(os.Stdout, n)
			}
		}
	case "descendant-held", "descendant-quiet":
		payloadHeldCommand(t, args[0] == "descendant-quiet", false)
	case "descendant", "descendant-natural":
		operation := "held"
		if args[0] == "descendant-natural" {
			operation = "held-file"
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestWinexecPayload$", "--", "--winexec-payload", operation)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(struct{ PID int }{cmd.Process.Pid}); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Process.Release(); err != nil {
			t.Fatal(err)
		}
		windows.ExitProcess(0)
	case "held":
		payloadHold(t)
	case "held-file":
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			b, err := os.ReadFile(filepath.Join(os.Getenv("DWARF_WORK"), "release"))
			if err == nil {
				if string(b) != "release-nonce" {
					t.Fatal("invalid release nonce")
				}
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			<-tick.C
		}
	case "output":
		if len(args) != 2 {
			t.Fatal("missing output size")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 0 || n > 65536 {
			t.Fatal("invalid output size")
		}
		for range n {
			if _, err := os.Stdout.Write([]byte{'o'}); err != nil {
				windows.ExitProcess(2)
			}
			if _, err := os.Stderr.Write([]byte{'e'}); err != nil {
				windows.ExitProcess(3)
			}
		}
		windows.ExitProcess(0)
	case "exit259":
		windows.ExitProcess(259)
	default:
		t.Fatalf("unknown payload operation %q", args[0])
	}
}

// This descendant needs no Go behavior. A private stdin pipe with its write
// end inherited keeps cmd blocked after the root exits, without another race
// runtime's fixed-address reservations. Both builds use this same native tree.
func payloadHeldCommand(t *testing.T, quiet, keepAlive bool) {
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
	if err := windows.SetHandleInformation(windows.Handle(write.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/q", "/c", "set /p x=")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = read, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS,
		AdditionalInheritedHandles: []syscall.Handle{
			syscall.Handle(write.Fd()),
			// Hold the original stdin unread too: the wall test must cancel
			// its genuinely pending host write, not substitute early EOF.
			syscall.Handle(os.Stdin.Fd()),
		},
	}
	if quiet {
		sink, err := os.OpenFile(filepath.Join(os.Getenv("DWARF_WORK"), "held-output"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer sink.Close()
		cmd.Stdout, cmd.Stderr = sink, sink
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct{ PID int }{cmd.Process.Pid}); err != nil {
		t.Fatal(err)
	}
	if !quiet && !keepAlive {
		fmt.Fprintln(os.Stdout, "held-ready")
	}
	if err := cmd.Process.Release(); err != nil {
		t.Fatal(err)
	}
	if keepAlive {
		payloadHold(t)
	}
	windows.ExitProcess(0)
}

func payloadHold(t *testing.T) {
	t.Helper()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	fmt.Fprintln(os.Stdout, "held-ready")
	state, err := windows.WaitForSingleObject(event, windows.INFINITE)
	t.Fatalf("held native process unexpectedly woke: wait=%d error=%v", state, err)
}

func payloadFileRoundTrip(t *testing.T, path string, nonce []byte) {
	t.Helper()
	report := func(stage string, err error) {
		var code syscall.Errno
		native := errors.As(err, &code)
		fmt.Fprintf(os.Stdout, "file-stage=%s native=%t code=%d error=%v\n", stage, native, uint32(code), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	report("create", err)
	if err != nil {
		t.Fatal(err)
	}
	n, writeErr := f.Write(nonce)
	report("write", writeErr)
	closeErr := f.Close()
	report("close-writer", closeErr)
	if writeErr != nil || closeErr != nil || n != len(nonce) {
		t.Fatalf("write/close: bytes=%d write=%v close=%v", n, writeErr, closeErr)
	}
	f, err = os.Open(path)
	report("reopen", err)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(io.LimitReader(f, int64(len(nonce)+1)))
	report("read", readErr)
	closeErr = f.Close()
	report("close-reader", closeErr)
	if readErr != nil || closeErr != nil || string(got) != string(nonce) {
		t.Fatalf("readback: got=%q read=%v close=%v", got, readErr, closeErr)
	}
}

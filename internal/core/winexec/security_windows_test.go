package winexec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
	"uuid"

	"golang.org/x/sys/windows"
)

var testExecutable = sync.OnceValues(func() ([]byte, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

func testRequest(t *testing.T, operation ...string) Request {
	t.Helper()
	image, err := testExecutable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestWinexecPayload$", "--", "--winexec-payload"}
	args = append(args, operation...)
	return Request{Program: "payload.exe", Args: args, Files: []InputFile{{Path: "payload.exe", Data: image}}, Deadline: time.Now().Add(25 * time.Second), Limits: Limits{Wall: 20 * time.Second, UserCPU: 10 * time.Second, CPUPercent: 100, JobCommitBytes: 2 << 30, Processes: 4, InputBytes: uint64(len(image)) + 1<<20, OutputBytes: 16384, StopGrace: 5 * time.Second}}
}
func testExecutor(t *testing.T) *Executor {
	t.Helper()
	e, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.mu.Lock()
		alreadyClosed := e.closed
		e.mu.Unlock()
		if err := e.Close(); err != nil && !alreadyClosed {
			t.Error(err)
		}
	})
	return e
}
func testProtectedFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(contents)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	owner, err := ownerSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := securePath(path, owner, "", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
	})
	return path
}
func testExchange(t *testing.T) (Request, string, string) {
	t.Helper()
	protected := testProtectedFile(t, t.TempDir(), "protected", "protected-nonce")
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	ambient := testProtectedFile(t, local, "dwarf-harness-canary-"+uuid.NewV7().String(), "host-localappdata-nonce")
	r := testRequest(t, "exchange", protected, ambient)
	r.Stdin = []byte("stdin-nonce")
	r.Files = append(r.Files, InputFile{Path: "nonce", Data: []byte("authorized-nonce")})
	return r, protected, ambient
}
func assertReusable(t *testing.T, r Receipt, err error) {
	t.Helper()
	if err != nil || r.State != Closed || r.Outcome != Exited || !r.RootExited || !r.TreeExited || !r.ExitCodeKnown || r.ExitCode != 0 || !r.ResourcesReusable || r.OutputState != CompleteSealed || !r.UsageKnown {
		t.Fatalf("result: %+v error=%v stdout=%q stderr=%q", r, err, r.Stdout, r.Stderr)
	}
}
func TestAppContainerJobAndSuspendOrder(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	request, _, _ := testExchange(t)
	var observed bool
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase != "suspended_verified" {
			return
		}
		observed = true
		var token windows.Token
		if err := windows.OpenProcessToken(r.pi.Process, windows.TOKEN_QUERY, &token); err != nil {
			observationErr = err
			return
		}
		defer token.Close()
		for _, class := range []uint32{29, 30, 31} {
			var n uint32
			err := windows.GetTokenInformation(token, class, nil, 0, &n)
			if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || n == 0 {
				observationErr = fmt.Errorf("class %d size=%d: %w", class, n, err)
				return
			}
			b := make([]byte, n)
			if err := windows.GetTokenInformation(token, class, &b[0], n, &n); err != nil {
				observationErr = err
				return
			}
			switch class {
			case 29:
				if len(b) != 4 || binary.LittleEndian.Uint32(b) != 1 {
					observationErr = fmt.Errorf("AppContainer token: %x", b)
				}
			case 30:
				if binary.LittleEndian.Uint32(b) != 0 {
					observationErr = fmt.Errorf("capabilities: %x", b)
				}
			case 31:
				actual := *(**windows.SID)(unsafe.Pointer(&b[0]))
				if actual == nil || actual.String() != r.receipt.ProfileSID {
					observationErr = errors.New("wrong package SID")
				}
				runtime.KeepAlive(b)
			}
		}
		var member int32
		ret, _, err := inJobProc.Call(uintptr(r.pi.Process), uintptr(r.job), uintptr(unsafe.Pointer(&member)))
		if ret == 0 || member != 1 {
			observationErr = fmt.Errorf("membership %d: %w", member, err)
		}
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if err := windows.QueryInformationJobObject(r.job, 9, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
			observationErr = err
			return
		}
		want := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_TIME)
		if limits.BasicLimitInformation.LimitFlags != want || limits.BasicLimitInformation.ActiveProcessLimit != request.Limits.Processes || limits.JobMemoryLimit != uintptr(request.Limits.JobCommitBytes) || limits.BasicLimitInformation.PerJobUserTimeLimit != int64(request.Limits.UserCPU/(100*time.Nanosecond)) {
			observationErr = fmt.Errorf("native limits: %+v", limits)
		}
		var rate jobCPURate
		if err := windows.QueryInformationJobObject(r.job, 15, uintptr(unsafe.Pointer(&rate)), uint32(unsafe.Sizeof(rate)), nil); err != nil || rate.Flags != 5 || rate.Rate != 10000 {
			observationErr = fmt.Errorf("CPU rate %+v: %w", rate, err)
		}
		var action uint32
		if err := windows.QueryInformationJobObject(r.job, 6, uintptr(unsafe.Pointer(&action)), 4, nil); err != nil || action != 0 {
			observationErr = fmt.Errorf("time action %d: %w", action, err)
		}
		if !r.receipt.ResumedUTC.IsZero() {
			observationErr = errors.New("observation happened after resume")
		}
	}
	receipt, err := e.Run(context.Background(), request)
	assertReusable(t, receipt, err)
	if !observed || observationErr != nil {
		t.Fatalf("suspended native observation=%t: %v", observed, observationErr)
	}
	t.Logf("regular AppContainer zero capabilities, SID=%s, JOB_LIST, DETACHED_PROCESS, suspended controls read back; root and Job closed", receipt.ProfileSID)
}
func TestProfileGrantBoundary(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	dir := t.TempDir()
	core := testProtectedFile(t, dir, "core", "core-nonce")
	fresh := testProtectedFile(t, dir, "fresh", "fresh-nonce")
	broad := testProtectedFile(t, dir, "broad", "aap-nonce")
	owner, err := ownerSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := securePath(broad, owner, "S-1-15-2-1", "FR"); err != nil {
		t.Fatal(err)
	}
	var grantErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase == "profile_bound" {
			grantErr = securePath(fresh, owner, r.receipt.ProfileSID, "FR")
		}
	}
	receipt, err := e.Run(context.Background(), testRequest(t, "canaries", core, fresh, broad))
	assertReusable(t, receipt, err)
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	if !strings.Contains(string(receipt.Stdout), "canary-0=fresh-nonce\n") || !strings.Contains(string(receipt.Stdout), "canary-1=aap-nonce\n") {
		t.Fatalf("canary readbacks: %q", receipt.Stdout)
	}
}
func TestNetworkDenied(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	positive := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			positive <- err
			return
		}
		defer conn.Close()
		b := make([]byte, len("host-network-nonce"))
		_, err = io.ReadFull(conn, b)
		if err == nil && string(b) != "host-network-nonce" {
			err = errors.New("wrong host nonce")
		}
		positive <- err
	}()
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Write([]byte("host-network-nonce"))
	closeErr := conn.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	if err := <-positive; err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			accepted <- errors.New("sandbox delivered a connection")
			return
		}
		accepted <- err
	}()
	receipt, runErr := e.Run(context.Background(), testRequest(t, "network", listener.Addr().String()))
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener observed sandbox traffic: %v", err)
	}
	assertReusable(t, receipt, runErr)
	if !strings.Contains(string(receipt.Stdout), "network-denied") {
		t.Fatalf("missing attempted dial: %q", receipt.Stdout)
	}
	t.Logf("host nonce received; sandbox listener accepted no connection or bytes; child: %s", receipt.Stdout)
}

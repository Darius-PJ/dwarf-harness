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
	"syscall"
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
	// Shared native-run budgets: admission Wall=20s under Deadline=25s;
	// CPU=10s at 100%, commit=2GiB, processes=4, input=image+1MiB,
	// output=16KiB, StopGrace=10s for proof, pump retirement and cleanup.
	// Admission includes image copying/scanning, profile creation and durable
	// writes: setup reached 7.48s in the check and 11.81s in a heavier load.
	// Wall rows use this one admission pair; the resource grace stays 10s
	// until its loaded stop-path latency is attributed, not guessed.
	//
	// Per-row overrides, each preserving its tested boundary:
	// - natural-exit/wall: grace=shared Wall+StopGrace (30s), so natural exit
	//   can precede the real wall and the deliberate drain wait can outlast it;
	// - resource payload hold guard=shared Wall+StopGrace (30s), so its work-file
	//   wait cannot expire before the real wall; the old 10s guard did so;
	// - mapped input held through cleanup: grace=2s, ruled after the check's
	//   1.28s stop exhausted 1s before a native denial could be retained;
	// - closed-fence seed: grace=100ms, intentional expiry without a child;
	// - native CPU=2s, commit=256MiB, and process-count/resource processes=2;
	// - output boundary=256 bytes; expired admission Deadline=now-1s.
	// Validation-only finiteRequest uses 1s wall/deadline/grace and 100ns CPU;
	// its invalid/zero/overflow overrides do not launch a native workload.
	//
	// Readiness guards remain independent and unchanged: descendantPID=3s;
	// resourceWaitReady, testWaitLiveJob, testWaitHeldTree,
	// testWaitProcessCountProbe and the behavioral descendant report=5s.
	// These waits start after resume and can also lose headroom under load;
	// no measured check exceedance authorizes raising them. Crash cleanup/Open
	// contexts use 5s from recovery entry, independently of admission.
	// Loopback probes use 2s; mapped release waits 300ms after closed_fence.
	// Other fixture guards (not request limits) remain: awaitResult=30s;
	// supervisor parent/child=40s/35s, handshake=15s; native exit=3s/10s;
	// other work-file, root-exit, suspended and peer barriers=10s; Git=20s/10s;
	// junction creation=5s. Polls of 10ms and zero-time queries are not budgets.
	return Request{Program: "payload.exe", Args: args, Files: []InputFile{{Path: "payload.exe", Data: image}}, Deadline: time.Now().Add(25 * time.Second), Limits: Limits{Wall: 20 * time.Second, UserCPU: 10 * time.Second, CPUPercent: 100, JobCommitBytes: 2 << 30, Processes: 4, InputBytes: uint64(len(image)) + 1<<20, OutputBytes: 16384, StopGrace: 10 * time.Second}}
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

const testPhaseScope = "elapsed and retirement offsets are from admission; setup freezes at resume or pre-resume stop; wall remaining uses the effective admission deadline; stop offsets apply only when StopStarted; channel arrays are stdin/stdout/stderr; terminal errors precede EOF/cancellation normalization; pump_loop_exited may lack proof; finalization_handles_closed excludes the Job handle; closed_fence precedes Job close and cleanup"

type testPhaseSample struct {
	Phase                string
	UTC                  time.Time
	ElapsedNS            int64
	SetupElapsedNS       int64
	WallRemainingNS      int64
	StopStarted          bool
	StopElapsedNS        int64
	StopRemainingNS      int64
	PumpsKnown           bool
	PumpsRetired         int
	ChannelComplete      [3]bool
	RetiredUTC           [3]time.Time
	RetiredElapsedNS     [3]int64
	RetiredStopElapsedNS [3]int64
	TerminalErrors       [3]string
	TerminalNative       [3]bool
	TerminalCodes        [3]uint32
	PipeFailure          string
}

type testPhaseRecorder []testPhaseSample

func (phases *testPhaseRecorder) record(phase string, r *nativeRun) {
	sample := testPhaseSample{Phase: phase, StopStarted: !r.stopAt.IsZero()}
	now := time.Now()
	var retired [3]time.Time
	var terminal [3]error
	var failure error
	if r.pipes != nil {
		r.pipes.mu.Lock()
		sample.PumpsKnown = true
		sample.PumpsRetired = r.pipes.done
		sample.ChannelComplete = r.pipes.complete
		retired, terminal, failure = r.pipes.retiredAt, r.pipes.terminal, r.pipes.failure
		now = time.Now()
		r.pipes.mu.Unlock()
	}
	sample.UTC = now.UTC()
	sample.ElapsedNS = now.Sub(r.admitted).Nanoseconds()
	sample.WallRemainingNS = r.wallEnd.Sub(now).Nanoseconds()
	setupEnd := now
	if !r.resumed.IsZero() {
		setupEnd = r.resumed
	} else if sample.StopStarted {
		setupEnd = r.stopAt
	}
	sample.SetupElapsedNS = setupEnd.Sub(r.admitted).Nanoseconds()
	if sample.StopStarted {
		sample.StopElapsedNS = now.Sub(r.stopAt).Nanoseconds()
		sample.StopRemainingNS = r.stopAt.Add(r.request.Limits.StopGrace).Sub(now).Nanoseconds()
	}
	for i, at := range retired {
		if !at.IsZero() {
			sample.RetiredUTC[i] = at.UTC()
			sample.RetiredElapsedNS[i] = at.Sub(r.admitted).Nanoseconds()
			if sample.StopStarted {
				sample.RetiredStopElapsedNS[i] = at.Sub(r.stopAt).Nanoseconds()
			}
		}
		if terminal[i] != nil {
			sample.TerminalErrors[i] = terminal[i].Error()
			var code syscall.Errno
			sample.TerminalNative[i] = errors.As(terminal[i], &code)
			sample.TerminalCodes[i] = uint32(code)
		}
	}
	if failure != nil {
		sample.PipeFailure = failure.Error()
	}
	*phases = append(*phases, sample)
}

func (phases testPhaseRecorder) stopEvidenceError() error {
	required := []string{"tree_exited", "pump_loop_exited", "finalization_handles_closed", "closed_fence"}
	next := 0
	for _, sample := range phases {
		if next == len(required) || sample.Phase != required[next] {
			continue
		}
		if !sample.StopStarted || !sample.PumpsKnown {
			return fmt.Errorf("stop attribution lacks clock or pump state at %s", sample.Phase)
		}
		if sample.Phase == "pump_loop_exited" {
			if sample.PumpsRetired != 3 {
				return fmt.Errorf("stop attribution reached loop exit with %d retired pumps", sample.PumpsRetired)
			}
			for i, at := range sample.RetiredUTC {
				if at.IsZero() {
					return fmt.Errorf("stop attribution lacks retirement instant for channel %d", i)
				}
			}
		}
		next++
	}
	if next != len(required) {
		return fmt.Errorf("stop attribution missing ordered phase %s", required[next])
	}
	return nil
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
		if err := queryJob(r.job, 9, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
			observationErr = err
			return
		}
		want := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_TIME)
		if limits.BasicLimitInformation.LimitFlags != want || limits.BasicLimitInformation.ActiveProcessLimit != request.Limits.Processes || limits.JobMemoryLimit != uintptr(request.Limits.JobCommitBytes) || limits.BasicLimitInformation.PerJobUserTimeLimit != int64(request.Limits.UserCPU/(100*time.Nanosecond)) {
			observationErr = fmt.Errorf("native limits: %+v", limits)
		}
		var rate jobCPURate
		if err := queryJob(r.job, 15, unsafe.Pointer(&rate), unsafe.Sizeof(rate)); err != nil || rate.Flags != 5 || rate.Rate != 10000 {
			observationErr = fmt.Errorf("CPU rate %+v: %w", rate, err)
		}
		var action uint32
		if err := queryJob(r.job, 6, unsafe.Pointer(&action), unsafe.Sizeof(action)); err != nil || action != 0 {
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

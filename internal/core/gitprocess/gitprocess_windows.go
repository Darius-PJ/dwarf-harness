package gitprocess

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
	"golang.org/x/sys/windows"
)

type platformState struct {
	retainedResources []any
	// Native fault/observation seams are nil in production.
	observe            func(string, fence)
	queryFault         func() error
	reservationMu      sync.Mutex
	currentBatch       *slotBatch
	batches            map[uuid.UUID]*slotBatch
	reservationFailure error
	// Metadata hooks may run under reservationMu. They must not call Owner
	// APIs or await another Run; lifecycle observations never hold that mutex.
	metadataHook func(operation, path string, success bool) error
}

type slotBatch struct {
	data             slotsContainer
	identity         os.FileInfo
	writer           *os.File
	next, complete   int
	attempted, done  [slotCount]bool
	failed, retiring bool
	unsyncedTemps    map[uuid.UUID]bool
}

var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	openJobObject       = kernel32.NewProc("OpenJobObjectW")
	createJobObject     = kernel32.NewProc("CreateJobObjectW")
	isProcessInJob      = kernel32.NewProc("IsProcessInJob")
	getCompletionStatus = kernel32.NewProc("GetQueuedCompletionStatus")
)

const jobFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS

type accounting struct {
	User, Kernel, PeriodUser, PeriodKernel int64
	Faults, Total, Active, Terminated      uint32
}

func queryJob(job windows.Handle, class int32, p unsafe.Pointer, n uintptr) error {
	var pin runtime.Pinner
	pin.Pin(p)
	defer pin.Unpin()
	return windows.QueryInformationJobObject(job, class, uintptr(p), uint32(n), nil)
}
func setJob(job windows.Handle, info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
	var pin runtime.Pinner
	pin.Pin(info)
	defer pin.Unpin()
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)))
	return err
}
func verifyJob(job windows.Handle, limit int) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&info), unsafe.Sizeof(info)); err != nil {
		return err
	}
	if info.BasicLimitInformation.LimitFlags != jobFlags || info.BasicLimitInformation.ActiveProcessLimit != uint32(limit) {
		return ErrInvalid
	}
	return nil
}
func createJob(name string, limit int) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var pin runtime.Pinner
	pin.Pin(p)
	h, _, createErr := createJobObject.Call(0, uintptr(unsafe.Pointer(p)))
	pin.Unpin()
	job := windows.Handle(h)
	if job == 0 {
		return 0, createErr
	}
	if errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
		return 0, errors.Join(createErr, windows.CloseHandle(job))
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = jobFlags
	info.BasicLimitInformation.ActiveProcessLimit = uint32(limit)
	if err := setJob(job, &info); err != nil {
		return 0, errors.Join(err, windows.CloseHandle(job))
	}
	return job, nil
}
func openJob(name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var pin runtime.Pinner
	pin.Pin(p)
	defer pin.Unpin()
	h, _, err := openJobObject.Call(0x0004|0x0008, 0, uintptr(unsafe.Pointer(p))) // QUERY | TERMINATE; never inheritable.
	if h == 0 {
		return 0, err
	}
	return windows.Handle(h), nil
}
func memberOf(process, job windows.Handle) error {
	var member int32
	var pin runtime.Pinner
	pin.Pin(&member)
	defer pin.Unpin()
	ok, _, err := isProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&member)))
	if ok == 0 {
		return err
	}
	if member == 0 {
		return ErrInvalid
	}
	return nil
}
func jobZero(job windows.Handle) (accounting, bool, error) {
	var a accounting
	if err := queryJob(job, windows.JobObjectBasicAccountingInformation, unsafe.Pointer(&a), unsafe.Sizeof(a)); err != nil {
		return a, false, err
	}
	if a.Active != 0 {
		return a, false, nil
	}
	// After accounting reaches zero, a header and one PID suffice to verify
	// the empty list. A nonempty/overflow response is not closure evidence.
	var ids struct {
		Assigned, Count uint32
		PID             uintptr
	}
	if err := queryJob(job, windows.JobObjectBasicProcessIdList, unsafe.Pointer(&ids), unsafe.Sizeof(ids)); err != nil {
		return a, false, err
	}
	return a, ids.Assigned == 0 && ids.Count == 0, nil
}
func processCreated(process windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user)
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), err
}
func currentSession() (uint32, error) {
	var session uint32
	err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session)
	return session, err
}
func waitRootGone(ctx context.Context, f fence, grace time.Duration) error {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, f.RootPID)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	created, err := processCreated(h)
	if err != nil {
		return err
	}
	if created != f.RootCreated {
		return nil
	}
	return pollUntil(ctx, grace, func() (bool, error) {
		state, err := windows.WaitForSingleObject(h, 0)
		return state == windows.WAIT_OBJECT_0, err
	})
}
func pollUntil(ctx context.Context, grace time.Duration, check func() (bool, error)) error {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := check()
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrUnconfirmed
		case <-tick.C:
		}
	}
}

// Port messages are advisory, not a source of tree-closure proof. Windows does
// not guarantee delivery of ACTIVE_PROCESS_LIMIT; Git's exit remains the fallback.
func monitorJob(job windows.Handle) (windows.Handle, error) {
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 1)
	if err != nil {
		return 0, err
	}
	info := struct {
		Key  uintptr
		Port windows.Handle
	}{Key: 1, Port: port}
	var pin runtime.Pinner
	pin.Pin(&info)
	defer pin.Unpin()
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectAssociateCompletionPortInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return 0, errors.Join(err, windows.CloseHandle(port))
	}
	return port, nil
}
func drainNotifications(port windows.Handle) (bool, error) {
	if port == 0 {
		return false, nil
	}
	var message uint32
	var key, pid uintptr
	var pin runtime.Pinner
	pin.Pin(&message)
	pin.Pin(&key)
	pin.Pin(&pid)
	defer pin.Unpin()
	limited := false
	for {
		ok, _, err := getCompletionStatus.Call(uintptr(port), uintptr(unsafe.Pointer(&message)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&pid)), 0)
		if ok == 0 {
			if errors.Is(err, windows.WAIT_TIMEOUT) {
				return limited, nil
			}
			return limited, err
		}
		if message == 3 {
			limited = true
		} // JOB_OBJECT_MSG_ACTIVE_PROCESS_LIMIT
	}
}
func (o *Owner) reconcile(ctx context.Context, f fence) (Result, error) {
	return o.reconcileCandidate(ctx, f, false)
}

func (o *Owner) reconcileCandidate(ctx context.Context, f fence, reservedZero bool) (Result, error) {
	r := Result{RunID: f.RunID, ExitCode: -1}
	fail := func(reason string, err error) (Result, error) { return r, unconfirmed(f.RunID, reason, err) }
	if err := ctx.Err(); err != nil {
		return fail("cancelled", err)
	}
	if f.State != "closed" {
		session, err := currentSession()
		if err != nil || session != f.Session {
			return fail("session_mismatch", err)
		}
		job, err := openJob(f.JobName)
		if err == nil {
			defer func() {
				if job != 0 {
					_ = windows.CloseHandle(job)
				}
			}()
			if err := verifyJob(job, o.options.MaxDescendants); err != nil {
				return fail("limits_unconfirmed", err)
			}
			if err := windows.TerminateJobObject(job, 1); err != nil {
				return fail("terminate_failed", err)
			}
			var usage accounting
			err := pollUntil(ctx, o.options.StopGrace, func() (bool, error) {
				if o.queryFault != nil {
					if err := o.queryFault(); err != nil {
						return false, err
					}
				}
				a, zero, err := jobZero(job)
				usage = a
				return zero, err
			})
			if err != nil {
				return fail("job_unconfirmed", err)
			}
			f.Proof, f.TotalProcesses = "retained_job", &usage.Total
			if err := windows.CloseHandle(job); err != nil {
				return fail("job_close_failed", err)
			}
			job = 0
		} else if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			if reservedZero {
				r.RootExited, r.TreeClosed, r.IOJoined, r.Proof = true, true, true, "name_absent"
				return r, nil
			}
			if f.State == "running" {
				if err := waitRootGone(ctx, f, o.options.StopGrace); err != nil {
					return fail("root_unconfirmed", err)
				}
			}
			f.Proof, f.TotalProcesses = "name_absent", nil
		} else {
			return fail("job_open_failed", err)
		}
		f.State = "closed"
		if err := o.writeFence(f); err != nil {
			return fail("fence_write_failed", err)
		}
		o.observation("reconciled", f)
	}
	if err := o.removeFence(f); err != nil {
		return fail("cleanup_failed", err)
	}
	r.RootExited, r.TreeClosed, r.IOJoined, r.Proof = true, true, true, f.Proof
	if f.TotalProcesses != nil {
		r.TotalProcesses = *f.TotalProcesses
	}
	return r, nil
}

type nativePipes struct {
	child  [3]*os.File
	parent [3]*os.File
	done   chan error
}

func (p *nativePipes) closeChildren() error {
	var errs []error
	for i, f := range p.child {
		if f != nil {
			errs = append(errs, f.Close())
			p.child[i] = nil
		}
	}
	return errors.Join(errs...)
}
func (p *nativePipes) closeParents() {
	for _, f := range p.parent {
		if f != nil {
			_ = f.Close()
		}
	}
}
func newPipes(input bool) (*nativePipes, error) {
	p := &nativePipes{done: make(chan error, 3)}
	for i := range p.child {
		if i == 0 && !input {
			f, err := os.Open(os.DevNull)
			if err != nil {
				p.closeParents()
				_ = p.closeChildren()
				return nil, err
			}
			p.child[i] = f
			continue
		}
		r, w, err := os.Pipe()
		if err != nil {
			p.closeParents()
			_ = p.closeChildren()
			return nil, err
		}
		if i == 0 {
			p.child[i], p.parent[i] = r, w
		} else {
			p.child[i], p.parent[i] = w, r
		}
	}
	return p, nil
}
func (p *nativePipes) start(spec Spec) {
	copyStream := func(dst io.Writer, src io.Reader, file *os.File) {
		buffer := make([]byte, 32<<10)
		_, err := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, buffer)
		closeErr := file.Close()
		if errors.Is(err, os.ErrClosed) || errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_NO_DATA) {
			err = nil
		}
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil
		}
		p.done <- errors.Join(err, closeErr)
	}
	if spec.Stdin == nil {
		p.done <- nil
	} else {
		go copyStream(p.parent[0], spec.Stdin, p.parent[0])
	}
	if spec.Stdout == nil {
		spec.Stdout = io.Discard
	}
	if spec.Stderr == nil {
		spec.Stderr = io.Discard
	}
	go copyStream(spec.Stdout, p.parent[1], p.parent[1])
	go copyStream(spec.Stderr, p.parent[2], p.parent[2])
}
func environmentBlock(env []string) ([]uint16, error) {
	if env == nil {
		env = os.Environ()
	}
	// Match os/exec's last-value-wins Windows environment semantics without
	// changing the workspace's four environment builders.
	seen := make(map[string]bool, len(env))
	var values []string
	for i := len(env) - 1; i >= 0; i-- {
		value := env[i]
		eq := strings.IndexByte(value, '=')
		if eq == 0 {
			if next := strings.IndexByte(value[1:], '='); next >= 0 {
				eq = next + 1
			}
		}
		if eq < 0 {
			continue
		}
		key := strings.ToLower(value[:eq])
		if !seen[key] {
			values = append(values, value)
			seen[key] = true
		}
	}
	if !seen["systemroot"] {
		values = append(values, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	var block []uint16
	for i := len(values) - 1; i >= 0; i-- {
		part, err := windows.UTF16FromString(values[i])
		if err != nil {
			return nil, err
		}
		block = append(block, part...)
	}
	block = append(block, 0)
	if len(block) == 1 {
		block = append(block, 0)
	}
	return block, nil
}

func createSuspended(spec Spec, job windows.Handle, pipes *nativePipes) (windows.ProcessInformation, error) {
	var pi windows.ProcessInformation
	app, err := windows.UTF16PtrFromString(spec.Program)
	if err != nil {
		return pi, err
	}
	command, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{spec.Program}, spec.Args...)))
	if err != nil {
		return pi, err
	}
	var cwd *uint16
	if spec.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(spec.Dir)
		if err != nil {
			return pi, err
		}
	}
	env, err := environmentBlock(spec.Env)
	if err != nil {
		return pi, err
	}
	var handles [3]windows.Handle
	for i, file := range pipes.child {
		err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handles[i], 0, true, windows.DUPLICATE_SAME_ACCESS)
		if err != nil {
			for _, h := range handles {
				if h != 0 {
					_ = windows.CloseHandle(h)
				}
			}
			return pi, err
		}
	}
	defer func() {
		for _, h := range handles {
			_ = windows.CloseHandle(h)
		}
	}()
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return pi, err
	}
	jobs := [1]windows.Handle{job}
	var pin runtime.Pinner
	pin.Pin(&jobs[0])
	pin.Pin(&handles[0])
	defer func() { attributes.Delete(); pin.Unpin() }()
	if err := attributes.Update(0x0002000d, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(jobs)); err != nil {
		return pi, err
	}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), unsafe.Sizeof(handles)); err != nil {
		return pi, err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	startup.ProcThreadAttributeList = attributes.List()
	err = windows.CreateProcess(app, &command[0], nil, nil, true, windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &env[0], cwd, &startup.StartupInfo, &pi)
	return pi, err
}

func (o *Owner) run(ctx context.Context, spec Spec, result Result) (Result, bool, error) {
	batch, index, f, err := o.reserveSlot(result.RunID)
	result.RunID = f.RunID
	if err != nil {
		o.retain(f)
		return result, false, unconfirmed(f.RunID, "reservation_failed", err)
	}
	o.observation("launch_pending", f)
	job, err := createJob(f.JobName, o.options.MaxDescendants)
	if err != nil {
		o.retain(f)
		return result, false, unconfirmed(f.RunID, "job_create_failed", err)
	}
	port, setupErr := monitorJob(job)
	var pipes *nativePipes
	if setupErr == nil {
		pipes, setupErr = newPipes(spec.Stdin != nil)
	}
	var pi windows.ProcessInformation
	if setupErr == nil {
		pi, setupErr = createSuspended(spec, job, pipes)
	}
	if pipes != nil {
		setupErr = errors.Join(setupErr, pipes.closeChildren())
	}
	started := false
	var frameErr error
	if setupErr == nil {
		setupErr = memberOf(pi.Process, job)
	}
	if setupErr == nil {
		setupErr = verifyJob(job, o.options.MaxDescendants)
	}
	if setupErr == nil {
		f.RootCreated, setupErr = processCreated(pi.Process)
	}
	if setupErr == nil {
		f.RootPID, f.State = pi.ProcessId, "running"
		o.observation("root_created", f)
		frameErr = o.writeRootFrame(batch, index, f)
		setupErr = frameErr
	}
	if setupErr == nil {
		o.observation("suspended", f)
		setupErr = ctx.Err()
	}
	if setupErr == nil {
		pipes.start(spec)
		started = true
		var previous uint32
		previous, setupErr = windows.ResumeThread(pi.Thread)
		if setupErr == nil && previous != 1 {
			setupErr = ErrInvalid
		}
		if setupErr == nil {
			o.observation("resumed", f)
			if spec.Started != nil {
				spec.Started(int(pi.ProcessId))
			}
		}
	}
	if pi.Thread != 0 {
		if err := windows.CloseHandle(pi.Thread); err != nil {
			setupErr = errors.Join(setupErr, err)
		} else {
			pi.Thread = 0
		}
	}
	if !started && pipes != nil {
		pipes.closeParents()
	}
	result.RootExited, result.IOJoined = pi.Process == 0, !started
	type rootWaitResult struct {
		state uint32
		err   error
	}
	var rootDone <-chan rootWaitResult
	if pi.Process != 0 {
		done := make(chan rootWaitResult, 1)
		rootDone = done
		go func(process windows.Handle) {
			state, err := windows.WaitForSingleObject(process, windows.INFINITE)
			done <- rootWaitResult{state, err}
		}(pi.Process)
	}
	completed := 0
	var stopAt time.Time
	var runErr = setupErr
	var tick *time.Ticker
	var proofTick <-chan time.Time
	defer func() {
		if tick != nil {
			tick.Stop()
		}
	}()
	cancelled := ctx.Done()
	for {
		if ctx.Err() != nil && !errors.Is(runErr, ctx.Err()) {
			runErr = errors.Join(runErr, ctx.Err())
		}
		if (runErr != nil || result.RootExited) && stopAt.IsZero() {
			stopAt = time.Now()
			runErr = errors.Join(runErr, windows.TerminateJobObject(job, 1))
			if pipes != nil && pipes.parent[0] != nil {
				_ = pipes.parent[0].Close()
			}
			tick = time.NewTicker(time.Millisecond)
			proofTick = tick.C
		}
		if !stopAt.IsZero() {
			var a accounting
			var zero bool
			var queryErr error
			if o.queryFault != nil {
				queryErr = o.queryFault()
			}
			if queryErr == nil {
				a, zero, queryErr = jobZero(job)
			}
			if queryErr != nil {
				runErr = errors.Join(runErr, queryErr)
				break
			}
			result.TreeClosed = zero
			if zero {
				result.TotalProcesses = a.Total
				result.User100ns = uint64(a.User)
				result.Kernel100ns = uint64(a.Kernel)
			}
		}
		if result.RootExited && result.TreeClosed && result.IOJoined {
			break
		}
		if !stopAt.IsZero() && !time.Now().Before(stopAt.Add(o.options.StopGrace)) {
			break
		}
		var done <-chan error
		if started {
			done = pipes.done
		}
		select {
		case waited := <-rootDone:
			rootDone = nil
			if waited.err != nil {
				runErr = errors.Join(runErr, waited.err)
			} else if waited.state != windows.WAIT_OBJECT_0 {
				runErr = errors.Join(runErr, ErrInvalid)
			} else {
				var code uint32
				if err := windows.GetExitCodeProcess(pi.Process, &code); err != nil {
					runErr = errors.Join(runErr, err)
				} else {
					result.RootExited, result.ExitCode = true, int(code)
					o.observation("root_exited", f)
				}
			}
		case err := <-done:
			completed++
			runErr = errors.Join(runErr, err)
			result.IOJoined = completed == 3
		case <-proofTick:
		case <-cancelled:
			cancelled = nil
		}
	}
	if !result.RootExited || !result.TreeClosed || !result.IOJoined || pi.Thread != 0 {
		_ = windows.TerminateJobObject(job, 1)
		if pipes != nil {
			pipes.closeParents()
		}
		o.retain(f)
		o.mu.Lock()
		o.retainedResources = append(o.retainedResources, job, port, pi, pipes)
		o.mu.Unlock()
		result.TreeClosed = false
		return result, true, unconfirmed(f.RunID, "closure_unconfirmed", runErr)
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	usageErr := queryJob(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits))
	result.PeakJobMemoryBytes = uint64(limits.PeakJobMemoryUsed)
	limited, notificationErr := drainNotifications(port)
	runErr = errors.Join(runErr, notificationErr)
	if limited {
		runErr = errors.Join(runErr, ErrLimit)
	}
	if port != 0 {
		if err := windows.CloseHandle(port); err != nil {
			o.retain(f)
			return result, true, unconfirmed(f.RunID, "port_close_failed", errors.Join(runErr, err))
		}
	}
	if pi.Process != 0 {
		if err := windows.CloseHandle(pi.Process); err != nil {
			o.retain(f)
			return result, true, unconfirmed(f.RunID, "root_handle_unclosed", errors.Join(runErr, err))
		}
	}
	if err := windows.CloseHandle(job); err != nil {
		o.retain(f)
		return result, true, unconfirmed(f.RunID, "job_close_failed", err)
	}
	if frameErr != nil {
		o.retain(f)
		return result, false, unconfirmed(f.RunID, "frame_write_failed", errors.Join(runErr, frameErr))
	}
	f.State, f.Proof, f.TotalProcesses = "closed", "live", &result.TotalProcesses
	o.observation("closed", f)
	if err := o.completeSlot(batch, index, f); err != nil {
		o.retain(f)
		return result, false, unconfirmed(f.RunID, "cleanup_failed", err)
	}
	result.Proof = "live"
	if result.ExitCode != 0 && runErr == nil && usageErr == nil {
		runErr = ErrExit
	}
	return result, false, errors.Join(runErr, usageErr)
}
func (o *Owner) retain(f fence) {
	o.mu.Lock()
	o.pending[f.RunID] = f
	o.mu.Unlock()
}
func (o *Owner) observation(phase string, f fence) {
	if o.observe != nil {
		o.observe(phase, f)
	}
}
func (o *Owner) checkRoot() error {
	info, err := realDirectory(o.root)
	if err != nil || !os.SameFile(info, o.identity) {
		return errors.Join(ErrInvalid, err)
	}
	_, err = realDirectory(filepath.Join(o.root, "runs"))
	return err
}
func (o *Owner) writeFence(f fence) error {
	if err := o.checkRoot(); err != nil {
		return err
	}
	raw, err := canonicalJSON(f)
	if err != nil {
		return err
	}
	return writeMarkerBytes(o.fencePath(f.RunID), raw, o.metadataHook)
}
func (o *Owner) removeFence(f fence) error {
	if f.State != "closed" {
		return ErrUnconfirmed
	}
	if err := o.checkRoot(); err != nil {
		return err
	}
	path := o.fencePath(f.RunID)
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return errors.Join(ErrInvalid, err)
	}
	pending := path + ".pending"
	info, err := os.Lstat(pending)
	if err == nil {
		if !info.Mode().IsRegular() {
			return ErrInvalid
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(path)
}

func (o *Owner) hasUnreconciledReservations() bool { return len(o.loadedSlots) != 0 }
func (o *Owner) reservationCount() int             { return len(o.loadedSlots) }
func (o *Owner) reservedRun(id uuid.UUID) bool {
	for _, batch := range o.loadedSlots {
		if slices.Contains(batch.data.Header.RunIDs, id) {
			return true
		}
	}
	return false
}

func (o *Owner) reserveSlot(candidate uuid.UUID) (*slotBatch, int, fence, error) {
	session, err := currentSession()
	f := fence{Format: fenceFormat, Version: 1, RootID: o.rootID, RunID: candidate, JobName: jobName(o.rootID, candidate), Session: session, State: "launch_pending"}
	if err != nil {
		return nil, 0, f, err
	}
	o.reservationMu.Lock()
	batch, fresh := o.currentBatch, false
	if o.reservationFailure != nil {
		err = o.reservationFailure
		o.reservationMu.Unlock()
		return nil, 0, f, err
	}
	if batch == nil {
		fresh = true
		h := slotsHeader{Format: slotsFormat, Version: 1, RootID: o.rootID, BatchID: uuid.NewV7(), Session: session, RunIDs: []uuid.UUID{candidate}}
		for len(h.RunIDs) < slotCount {
			h.RunIDs = append(h.RunIDs, uuid.NewV7())
		}
		slices.SortFunc(h.RunIDs, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		batch = &slotBatch{data: slotsContainer{Header: h}}
		f = h.candidate(0)
		if o.batches == nil {
			o.batches = make(map[uuid.UUID]*slotBatch)
		}
		o.batches[h.BatchID] = batch
		var raw []byte
		raw, err = encodeSlots(batch.data)
		if err == nil {
			err = o.checkRoot()
		}
		if err == nil {
			err = writeMarkerBytes(o.slotsPath(h.BatchID), raw, o.metadataHook)
		}
		if err == nil {
			err = o.openBatchWriter(batch)
		}
		if err != nil {
			batch.failed, o.reservationFailure = true, err
			o.reservationMu.Unlock()
			return batch, 0, f, err
		}
		o.currentBatch = batch
	}
	index := batch.next
	f = batch.data.Header.candidate(index)
	batch.next++
	if batch.next == slotCount {
		o.currentBatch = nil
	}
	o.reservationMu.Unlock()
	if fresh {
		o.observation("reservation_published", f)
	}
	return batch, index, f, nil
}

func (o *Owner) openBatchWriter(batch *slotBatch) error {
	if err := o.checkRoot(); err != nil {
		return err
	}
	path := o.slotsPath(batch.data.Header.BatchID)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || batch.identity != nil && !os.SameFile(info, batch.identity) {
		return errors.Join(ErrInvalid, err)
	}
	if batch.writer == nil {
		if err := metadataCall(o.metadataHook, "writer_open", path, func() error {
			var err error
			batch.writer, err = os.OpenFile(path, os.O_RDWR, 0)
			return err
		}); err != nil {
			return err
		}
	}
	opened, err := batch.writer.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != slotsSize {
		return errors.Join(ErrInvalid, err)
	}
	raw := make([]byte, slotsSize)
	if _, err := batch.writer.ReadAt(raw, 0); err != nil {
		return err
	}
	if _, err := decodeSlots(raw); err != nil {
		return err
	}
	expected, err := encodeSlots(batch.data)
	if err != nil || !bytes.Equal(raw, expected) {
		return errors.Join(ErrInvalid, err)
	}
	batch.identity = opened
	return nil
}

func (o *Owner) writeRootFrame(batch *slotBatch, index int, f fence) error {
	frame, err := encodeSlotFrame(f)
	if err != nil {
		return err
	}
	o.reservationMu.Lock()
	err = func() error {
		if batch.failed || batch.retiring || batch.attempted[index] || batch.writer == nil {
			return ErrUnconfirmed
		}
		if err := o.checkRoot(); err != nil {
			return err
		}
		info, err := batch.writer.Stat()
		if err != nil || info.Size() != slotsSize || !os.SameFile(info, batch.identity) {
			return errors.Join(ErrInvalid, err)
		}
		zero := make([]byte, slotFrameSize)
		if _, err := batch.writer.ReadAt(zero, int64(slotHeaderSize+index*slotFrameSize)); err != nil {
			return err
		}
		var hint [1]byte
		if _, err := batch.writer.ReadAt(hint[:], int64(slotFooterOffset+index)); err != nil {
			return err
		}
		if !bytes.Equal(zero, make([]byte, slotFrameSize)) || hint[0] != 0 {
			return ErrInvalid
		}
		batch.attempted[index] = true
		err = metadataCall(o.metadataHook, "frame_write", o.slotsPath(batch.data.Header.BatchID), func() error {
			n, err := batch.writer.WriteAt(frame, int64(slotHeaderSize+index*slotFrameSize))
			if err == nil && n != len(frame) {
				err = io.ErrShortWrite
			}
			return err
		})
		if err == nil {
			batch.data.Frames[index] = f
		}
		return err
	}()
	if err != nil {
		batch.failed, o.reservationFailure = true, err
	}
	o.reservationMu.Unlock()
	if err != nil {
		return err
	}
	// Each root owes its own completion, even if a sibling Sync also flushes
	// these bytes. Outstanding members prevent closing this shared handle.
	err = metadataCall(o.metadataHook, "frame_sync", o.slotsPath(batch.data.Header.BatchID), batch.writer.Sync)
	if err != nil {
		o.reservationMu.Lock()
		batch.failed, o.reservationFailure = true, err
		o.reservationMu.Unlock()
	}
	return err
}

func (o *Owner) writeRetiredHint(batch *slotBatch, index int) error {
	if batch.data.Frames[index] == (fence{}) || batch.writer == nil {
		return ErrInvalid
	}
	if err := o.checkRoot(); err != nil {
		return err
	}
	err := metadataCall(o.metadataHook, "footer_write", o.slotsPath(batch.data.Header.BatchID), func() error {
		n, err := batch.writer.WriteAt([]byte{1}, int64(slotFooterOffset+index))
		if err == nil && n != 1 {
			err = io.ErrShortWrite
		}
		return err
	})
	if err == nil {
		batch.data.Retired[index] = 1
	}
	return err
}

func (o *Owner) completeSlot(batch *slotBatch, index int, f fence) error {
	o.reservationMu.Lock()
	if batch.failed || batch.retiring || batch.done[index] {
		o.reservationMu.Unlock()
		return ErrUnconfirmed
	}
	last := batch.next == slotCount && batch.complete+1 == slotCount
	if last {
		batch.retiring = true
	} else if batch.data.Frames[index] != (fence{}) {
		if err := o.writeRetiredHint(batch, index); err != nil {
			batch.failed, o.reservationFailure = true, err
			o.reservationMu.Unlock()
			return err
		}
	}
	if !last {
		batch.done[index] = true
		batch.complete++
	}
	o.reservationMu.Unlock()
	if !last {
		return nil
	}
	err := o.removeBatch(batch, f)
	o.reservationMu.Lock()
	defer o.reservationMu.Unlock()
	if err != nil {
		batch.failed, o.reservationFailure = true, err
		return err
	}
	batch.done[index] = true
	batch.complete++
	delete(o.batches, batch.data.Header.BatchID)
	return nil
}

func (o *Owner) removeBatch(batch *slotBatch, f fence) error {
	if err := o.checkRoot(); err != nil {
		return err
	}
	path := o.slotsPath(batch.data.Header.BatchID)
	if batch.writer != nil {
		if err := metadataCall(o.metadataHook, "writer_close", path, batch.writer.Close); err != nil {
			return err
		}
		batch.writer = nil
	}
	o.observation("retirement_before_remove", f)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(info, batch.identity) {
		return errors.Join(ErrInvalid, err)
	}
	pending := path + ".pending"
	if info, err := os.Lstat(pending); err == nil {
		if !info.Mode().IsRegular() {
			return ErrInvalid
		}
		if err := metadataCall(o.metadataHook, "remove", pending, func() error { return os.Remove(pending) }); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := metadataCall(o.metadataHook, "remove", path, func() error { return os.Remove(path) }); err != nil {
		return err
	}
	o.observation("retirement_after_remove", f)
	return nil
}

func (o *Owner) closeReservations() error {
	o.reservationMu.Lock()
	var batches []*slotBatch
	for _, batch := range o.batches {
		if batch.failed || batch.retiring || batch.complete != batch.next {
			o.reservationMu.Unlock()
			return ErrUnconfirmed
		}
		batch.retiring = true
		batches = append(batches, batch)
	}
	o.reservationMu.Unlock()
	for _, batch := range batches {
		f := batch.data.Header.candidate(batch.next - 1)
		if err := o.removeBatch(batch, f); err != nil {
			return err
		}
		o.reservationMu.Lock()
		delete(o.batches, batch.data.Header.BatchID)
		o.reservationMu.Unlock()
	}
	return nil
}

func (o *Owner) reconcileReservations(ctx context.Context) ([]Result, error) {
	var ids []uuid.UUID
	for id := range o.loadedSlots {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	var closed []Result
	var failures []error
	for _, id := range ids {
		batch := o.loadedSlots[id]
		if err := o.openBatchWriter(batch); err != nil {
			failures = append(failures, unconfirmed(id, "writer_open_failed", err))
			continue
		}
		processed := 0
		for i, run := range batch.data.Header.RunIDs {
			f := batch.data.Frames[i]
			zero := f == (fence{})
			if zero {
				f = batch.data.Header.candidate(i)
			}
			proof, readErr := readRegular(o.fencePath(run))
			if readErr == nil {
				decoded, err := decodeFence(proof)
				if err != nil || !closedSlotMatches(batch.data, decoded) {
					failures = append(failures, unconfirmed(run, "invalid_closed", errors.Join(ErrInvalid, err)))
					continue
				}
				f = decoded
			} else if !errors.Is(readErr, os.ErrNotExist) {
				failures = append(failures, unconfirmed(run, "closed_read_failed", readErr))
				continue
			}
			r, err := o.reconcileCandidate(ctx, f, zero)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if zero && r.Proof == "name_absent" {
				if err := o.removeZeroClosedTemp(batch, f); err != nil {
					failures = append(failures, unconfirmed(run, "cleanup_failed", err))
					continue
				}
			}
			report := r.Proof == "retained_job" || !zero && batch.data.Retired[i] == 0
			if !zero {
				o.reservationMu.Lock()
				err := o.writeRetiredHint(batch, i)
				o.reservationMu.Unlock()
				if err != nil {
					failures = append(failures, unconfirmed(run, "cleanup_failed", err))
					continue
				}
			}
			delete(o.pending, run)
			processed++
			if report {
				closed = append(closed, r)
			}
		}
		if processed == slotCount {
			if err := o.removeBatch(batch, batch.data.Header.candidate(slotCount-1)); err != nil {
				failures = append(failures, unconfirmed(id, "cleanup_failed", err))
				continue
			}
			delete(o.loadedSlots, id)
		}
	}
	return closed, errors.Join(failures...)
}

// An unpublished zero-slot closure is not proof. Exact name absence permits
// its removal, but the recovery-only directory barrier must finish before the
// container can disappear, so a lost removal cannot leave an orphan temp.
func (o *Owner) removeZeroClosedTemp(batch *slotBatch, f fence) error {
	if err := o.checkRoot(); err != nil {
		return err
	}
	path := o.fencePath(f.RunID) + ".pending"
	raw, err := readRegular(path)
	if err == nil {
		proof, err := decodeFence(raw)
		if err != nil || !closedSlotMatches(batch.data, proof) {
			return errors.Join(ErrInvalid, err)
		}
		if err := metadataCall(o.metadataHook, "remove", path, func() error { return os.Remove(path) }); err != nil {
			return err
		}
		if batch.unsyncedTemps == nil {
			batch.unsyncedTemps = make(map[uuid.UUID]bool)
		}
		batch.unsyncedTemps[f.RunID] = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if batch.unsyncedTemps[f.RunID] {
		if err := metadataCall(o.metadataHook, "directory_sync", filepath.Dir(path), func() error { return durable.SyncDir(filepath.Dir(path)) }); err != nil {
			return err
		}
		delete(batch.unsyncedTemps, f.RunID)
	}
	return nil
}

package winexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
	"golang.org/x/sys/windows"
)

type Executor struct {
	mu                     sync.Mutex
	root, owner            string
	rootID                 uuid.UUID
	rootHandle, runsHandle windows.Handle
	lock                   *durable.Lock
	active                 *nativeRun
	closed                 bool
	unavailable            error
	closeDone              chan struct{}
	closeErr               error
	// observe is a package-private synchronous observation of actual phases.
	// It cannot replace a native operation or report enforcement success.
	observe func(string, *nativeRun)
}
type nativeRun struct {
	executor                                                 *Executor
	request                                                  Request
	ctx                                                      context.Context
	stop, done                                               chan struct{}
	admitted, wallEnd, resumed, stopAt                       time.Time
	fence                                                    runFence
	dir                                                      string
	profile                                                  fileBinding
	sid                                                      *windows.SID
	profileMade, fenceWritten, created, ioStarted, uncertain bool
	job                                                      windows.Handle
	pi                                                       windows.ProcessInformation
	pins                                                     []windows.Handle
	pipes                                                    *pipeGroup
	receipt                                                  Receipt
	cause                                                    Outcome
	err                                                      error
}

// Open claims a dedicated local root without adopting unrelated contents.
// Recovery cleans validated closed runs only; uncertainty stays quarantined.
func Open(ctx context.Context, root string) (*Executor, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkLocalRoot(root); err != nil {
		return nil, errors.Join(ErrInvalidRoot, err)
	}
	if err := nativeAvailable(); err != nil {
		return nil, err
	}
	owner, err := ownerSID()
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	newRoot := false
	if err := os.Mkdir(root, 0o700); err == nil {
		newRoot = true
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if newRoot {
		if err := securePath(root, owner, "", ""); err != nil {
			return nil, err
		}
	}
	bound, err := openDirectory(root, false)
	if err != nil {
		return nil, err
	}
	e := &Executor{root: bound.Path, owner: owner, rootHandle: bound.Handle, closeDone: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			if e.lock != nil {
				_ = e.lock.Close()
			}
			if e.runsHandle != 0 {
				_ = windows.CloseHandle(e.runsHandle)
			}
			_ = windows.CloseHandle(e.rootHandle)
		}
	}()
	lockPath := filepath.Join(e.root, "lock")
	if info, err := os.Lstat(lockPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, ErrInvalidRoot
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	e.lock, err = durable.LockFile(lockPath)
	if err != nil {
		return nil, errors.Join(ErrBusy, err)
	}
	markerPath := filepath.Join(e.root, "executor.json")
	data, err := readMarker(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(e.root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Name() != "lock" {
				return nil, ErrInvalidRoot
			}
		}
		e.rootID = uuid.NewV7()
		if err := securePath(e.root, owner, "", ""); err != nil {
			return nil, err
		}
		if err := writeMarker(markerPath, rootMarker{Format: 1, RootID: e.rootID.String(), Owner: owner}); err != nil {
			return nil, err
		}
	} else {
		if err != nil {
			return nil, errors.Join(ErrInvalidRoot, err)
		}
		marker, err := decodeRoot(data, owner)
		if err != nil {
			return nil, err
		}
		e.rootID, _ = uuid.Parse(marker.RootID)
		if err := securePath(e.root, owner, "", ""); err != nil {
			return nil, err
		}
	}
	runs := filepath.Join(e.root, "runs")
	if err := os.Mkdir(runs, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	bound, err = openDirectory(runs, false)
	if err != nil {
		return nil, errors.Join(ErrQuarantined, err)
	}
	e.runsHandle = bound.Handle
	if err := durable.SyncDir(e.root); err != nil {
		return nil, err
	}
	// Reopen cleanup is finite even when the caller supplied Background.
	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dir, err := os.Open(runs)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	for {
		if err := cleanupCtx.Err(); err != nil {
			return nil, errors.Join(ErrCleanupPending, err)
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || !canonicalID(entry.Name()) {
				return nil, ErrQuarantined
			}
			path := fencePath(e.root, entry.Name())
			data, err := readMarker(path)
			if err != nil {
				return nil, errors.Join(ErrQuarantined, err)
			}
			f, err := decodeFence(data, e.rootID.String(), entry.Name())
			if err != nil || f.State != "closed" {
				return nil, fmt.Errorf("%w: run %s", ErrQuarantined, entry.Name())
			}
			if err := e.cleanup(cleanupCtx, f); err != nil {
				return nil, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if err := durable.SyncDir(runs); err != nil {
		return nil, errors.Join(ErrCleanupPending, err)
	}
	ok = true
	return e, nil
}

// Close prevents admission, stops an active Run and waits for its bounded
// teardown. Concurrent and repeated calls share the stored result, including
// retained quarantine or cleanup failure. The root lock is released last.
func (e *Executor) Close() error {
	if e == nil {
		return ErrClosed
	}
	e.mu.Lock()
	if e.closeDone == nil {
		e.mu.Unlock()
		return ErrClosed
	}
	if e.closed {
		done := e.closeDone
		e.mu.Unlock()
		<-done
		e.mu.Lock()
		err := e.closeErr
		e.mu.Unlock()
		return err
	}
	e.closed = true
	active := e.active
	if active != nil {
		close(active.stop)
	}
	e.mu.Unlock()
	if active != nil {
		<-active.done
	}
	e.mu.Lock()
	err := e.unavailable
	e.mu.Unlock()
	if e.runsHandle != 0 {
		err = errors.Join(err, nativeError("CloseHandle.runs", windows.CloseHandle(e.runsHandle)))
	}
	if e.rootHandle != 0 {
		err = errors.Join(err, nativeError("CloseHandle.root", windows.CloseHandle(e.rootHandle)))
	}
	// The operating-system slot lock is the final released resource.
	err = errors.Join(err, e.lock.Close())
	e.mu.Lock()
	e.closeErr = err
	close(e.closeDone)
	e.mu.Unlock()
	return err
}

// Run admits one request without queueing. The caller must not mutate request
// slices until it returns. The earliest wall, request or context deadline bounds
// setup and execution; StopGrace independently bounds teardown.
func (e *Executor) Run(ctx context.Context, request Request) (Receipt, error) {
	if e == nil {
		return Receipt{}, ErrClosed
	}
	e.mu.Lock()
	if e.closeDone == nil || e.closed {
		e.mu.Unlock()
		return Receipt{}, ErrClosed
	}
	if e.unavailable != nil {
		err := e.unavailable
		e.mu.Unlock()
		return Receipt{}, err
	}
	if e.active != nil {
		e.mu.Unlock()
		return Receipt{}, ErrBusy
	}
	if ctx == nil {
		e.mu.Unlock()
		return Receipt{}, ErrInvalid
	}
	charge, err := validateRequest(request)
	if err == nil {
		for _, file := range request.Files {
			local, localErr := filepath.Localize(file.Path)
			if localErr != nil || !filepath.IsLocal(local) {
				err = ErrInvalid
				break
			}
		}
	}
	if err != nil {
		e.mu.Unlock()
		return Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		outcome := Canceled
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = TimedOut
		}
		return Receipt{Outcome: outcome}, err
	}
	now := time.Now()
	budget := min(request.Limits.Wall, request.Deadline.Sub(now))
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, deadline.Sub(now))
	}
	if budget <= 0 {
		e.mu.Unlock()
		return Receipt{Outcome: TimedOut}, context.DeadlineExceeded
	}
	runID := uuid.NewV7()
	r := &nativeRun{executor: e, ctx: ctx, request: request, stop: make(chan struct{}), done: make(chan struct{}), admitted: now, wallEnd: now.Add(budget), receipt: Receipt{RootID: e.rootID, RunID: runID, InputBytes: charge}}
	r.dir = filepath.Join(e.root, "runs", runID.String())
	if _, _, err := commandLine(filepath.Join(r.dir, "input"), request); err != nil {
		e.mu.Unlock()
		return Receipt{}, err
	}
	r.fence = runFence{Format: 1, RootID: e.rootID.String(), RunID: runID.String(), ProfileName: "dwarf-harness-" + runID.String(), State: "unconfirmed"}
	e.active = r
	e.mu.Unlock()
	r.request.Args = append([]string(nil), request.Args...)
	r.request.Stdin = append([]byte(nil), request.Stdin...)
	r.request.Files = make([]InputFile, len(request.Files))
	for i, f := range request.Files {
		r.request.Files[i] = InputFile{Path: f.Path, Data: append([]byte(nil), f.Data...)}
	}
	err = r.setup()
	if err != nil {
		r.check(err)
	}
	r.finish()
	e.mu.Lock()
	// Close and receipt freezing share this lock; a subsequent Close cannot
	// retroactively change a frozen successful receipt.
	r.check(nil)
	if r.cause != NotStarted {
		r.receipt.Outcome = r.cause
		r.receipt.OutputState = Unpublishable
	}
	if !r.receipt.ResourcesReusable {
		retained := ErrCleanupPending
		if r.receipt.State == Quarantined {
			retained = ErrQuarantined
		}
		e.unavailable = errors.Join(retained, r.err)
	}
	r.receipt.Stop = time.Since(r.stopAt)
	e.active = nil
	close(r.done)
	e.mu.Unlock()
	return r.receipt, r.err
}
func (r *nativeRun) observe(phase string) {
	if r.executor.observe != nil {
		r.executor.observe(phase, r)
	}
}
func (r *nativeRun) beginStop() {
	if r.stopAt.IsZero() {
		r.stopAt = time.Now()
	}
}
func (r *nativeRun) check(native error) bool {
	if r.pipes != nil {
		native = errors.Join(native, r.pipes.takeFailure())
	}
	if errors.Is(native, ErrQuarantined) {
		r.uncertain = true
	}
	if r.cause != NotStarted {
		r.err = errors.Join(r.err, native)
		return true
	}
	var cause Outcome
	var failure error
	// Natural tree exit begins an independent StopGrace interval. Late caller
	// cancellation, shutdown or execution deadlines cannot rewrite that exit;
	// output overflow and native finalization failures still count below.
	if r.stopAt.IsZero() {
		select {
		case <-r.stop:
			cause = Shutdown
			failure = ErrClosed
		default:
		}
		if err := r.ctx.Err(); err != nil {
			if cause == NotStarted {
				cause = Canceled
				if errors.Is(err, context.DeadlineExceeded) {
					cause = TimedOut
				}
			}
			failure = errors.Join(failure, err)
		}
		if !time.Now().Before(r.wallEnd) {
			if cause == NotStarted {
				cause = TimedOut
			}
			failure = errors.Join(failure, context.DeadlineExceeded)
		}
	}
	if r.pipes != nil {
		_, _, overflow := r.pipes.snapshot()
		if overflow {
			if cause == NotStarted {
				cause = OutputExceeded
			}
			failure = errors.Join(failure, ErrOutputLimit)
		}
	}
	if native != nil {
		if cause == NotStarted {
			cause = NativeFailure
		}
		failure = errors.Join(failure, native)
	}
	if cause == NotStarted {
		return false
	}
	r.cause = cause
	r.err = errors.Join(r.err, failure)
	r.beginStop()
	return true
}
func (r *nativeRun) setup() error {
	if err := os.Mkdir(r.dir, 0o700); err != nil {
		r.uncertain = true
		return err
	}
	if err := durable.SyncDir(filepath.Dir(r.dir)); err != nil {
		r.uncertain = true
		return err
	}
	if err := writeMarker(filepath.Join(r.dir, "fence.json"), r.fence); err != nil {
		r.uncertain = true
		return err
	}
	r.fenceWritten = true
	r.observe("unconfirmed")
	if r.check(nil) {
		return nil
	}
	sid, err := createProfile(r.fence.ProfileName)
	if err != nil {
		return err
	}
	r.sid = sid
	r.profileMade = true
	r.receipt.ProfileSID = sid.String()
	path, err := profileFolder(sid)
	if err != nil {
		return err
	}
	r.profile, err = openDirectory(path, false)
	if err != nil {
		return err
	}
	if err := hasProfileWrite(r.profile.Handle, sid.String()); err != nil {
		return err
	}
	r.fence.ProfileCreated = true
	r.fence.ProfilePath = r.profile.Path
	r.fence.ProfileVolume = r.profile.Volume
	r.fence.ProfileFileID = r.profile.ID
	if err := writeMarker(filepath.Join(r.dir, "fence.json"), r.fence); err != nil {
		r.uncertain = true
		return err
	}
	r.observe("profile_bound")
	input := filepath.Join(r.dir, "input")
	work := filepath.Join(r.dir, "work")
	if err := os.Mkdir(input, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(work, 0o700); err != nil {
		return err
	}
	if err := securePath(work, r.executor.owner, sid.String(), "FA"); err != nil {
		return err
	}
	workBinding, err := openDirectory(work, false)
	if err != nil {
		return err
	}
	r.pins = append(r.pins, workBinding.Handle)
	if err := lowerLabel(workBinding.Handle); err != nil {
		return err
	}
	if err := lowerLabel(r.profile.Handle); err != nil {
		return err
	}
	pins, err := stageInput(input, r.executor.owner, sid.String(), r.request.Files)
	if err != nil {
		return err
	}
	r.pins = append(r.pins, pins...)
	r.job, err = createJob(r.request.Limits)
	if err != nil {
		return err
	}
	r.pipes, err = newPipes(r.executor.owner, r.fence.RunID, r.request.Limits.OutputBytes)
	if err != nil {
		if errors.Is(err, ErrQuarantined) {
			r.uncertain = true
		}
		return err
	}
	if r.check(nil) {
		return nil
	}
	r.pi, err = createSuspended(input, work, r.request, sid, r.job, r.pipes.child)
	if err != nil {
		return err
	}
	r.created = true
	r.observe("suspended_created")
	if err := r.pipes.closeChildren(); err != nil {
		return err
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(r.pi.Process, &creation, &exit, &kernel, &user); err != nil {
		return nativeError("GetProcessTimes", err)
	}
	r.receipt.CreatedUTC = time.Unix(0, creation.Nanoseconds()).UTC()
	if err := verifyToken(r.pi.Process, sid.String()); err != nil {
		return err
	}
	if err := processInJob(r.pi.Process, r.job); err != nil {
		return err
	}
	if err := verifyJob(r.job, r.request.Limits); err != nil {
		return err
	}
	r.observe("suspended_verified")
	if r.check(nil) {
		return nil
	}
	r.pipes.start(r.request.Stdin)
	r.ioStarted = true
	previous, err := windows.ResumeThread(r.pi.Thread)
	if err != nil {
		return nativeError("ResumeThread", err)
	}
	if previous != 1 {
		return fmt.Errorf("%w: initial suspend count mismatch", ErrUnsupported)
	}
	r.resumed = time.Now()
	r.receipt.ResumedUTC = r.resumed.UTC()
	if err := windows.CloseHandle(r.pi.Thread); err != nil {
		r.uncertain = true
		return nativeError("CloseHandle.thread", err)
	}
	r.pi.Thread = 0
	r.observe("resumed")
	return nil
}
func (r *nativeRun) pollTree() error {
	if !r.created {
		r.receipt.TreeExited = true
		return nil
	}
	if !r.receipt.RootExited {
		state, err := windows.WaitForSingleObject(r.pi.Process, 0)
		if err != nil {
			return nativeError("WaitForSingleObject.root", err)
		}
		if state == uint32(windows.WAIT_TIMEOUT) {
			return nil
		}
		if state != windows.WAIT_OBJECT_0 {
			return ErrUnsupported
		}
		r.receipt.RootExited = true
		var code uint32
		if err := windows.GetExitCodeProcess(r.pi.Process, &code); err != nil {
			r.check(nativeError("GetExitCodeProcess", err))
		} else {
			r.receipt.ExitCode = code
			r.receipt.ExitCodeKnown = true
		}
		for _, h := range []windows.Handle{r.pi.Thread, r.pi.Process} {
			if h != 0 {
				if err := windows.CloseHandle(h); err != nil {
					r.uncertain = true
					r.check(nativeError("CloseHandle.process-reference", err))
				}
			}
		}
		r.pi.Thread = 0
		r.pi.Process = 0
		r.observe("root_exited")
	}
	accounting, err := jobAccounting(r.job)
	if err != nil {
		return err
	}
	if accounting.Active == 0 {
		r.receipt.TreeExited = true
	} else {
		r.observe("job_nonempty")
	}
	return nil
}
func (r *nativeRun) finish() {
	terminated := false
	if !r.ioStarted && r.pipes != nil {
		r.check(r.pipes.closeUnstarted())
		r.pipes = nil
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if !r.stopAt.IsZero() && !time.Now().Before(r.stopAt.Add(r.request.Limits.StopGrace)) {
			r.uncertain = true
			r.check(fmt.Errorf("%w: stop proof deadline", ErrQuarantined))
			break
		}
		stopping := r.check(nil)
		if stopping && !terminated {
			terminated = true
			if r.created {
				r.check(nativeError("TerminateJobObject", windows.TerminateJobObject(r.job, 1)))
			}
			if r.pipes != nil {
				r.pipes.cancel()
			}
		}
		if err := r.pollTree(); err != nil {
			r.check(err)
			r.uncertain = true
			break
		}
		if r.receipt.TreeExited && r.receipt.ClosedUTC.IsZero() {
			r.receipt.ClosedUTC = time.Now().UTC()
			r.beginStop()
			r.observe("tree_exited")
		}
		done := 3
		if r.pipes != nil {
			done, _, _ = r.pipes.snapshot()
		}
		if !r.stopAt.IsZero() && !time.Now().Before(r.stopAt.Add(r.request.Limits.StopGrace)) {
			r.uncertain = true
			r.check(fmt.Errorf("%w: stop proof deadline", ErrQuarantined))
			break
		}
		if r.receipt.TreeExited && done == 3 {
			break
		}
		var event <-chan struct{}
		if r.pipes != nil {
			event = r.pipes.event
		}
		if stopping || !r.stopAt.IsZero() {
			select {
			case <-tick.C:
			case <-event:
			}
		} else {
			select {
			case <-tick.C:
			case <-event:
			case <-r.ctx.Done():
			case <-r.stop:
			}
		}
	}
	// Every completed pump's final close/error must be consumed before a
	// durable closed fence can authorize cleanup.
	r.check(nil)
	if !r.receipt.TreeExited {
		r.uncertain = true
	}
	if r.uncertain && r.pipes != nil {
		r.pipes.cancel()
	}
	if r.created && r.receipt.TreeExited {
		usage, err := jobUsage(r.job)
		if err != nil {
			r.check(err)
		} else {
			r.receipt.Usage = usage
			r.receipt.UsageKnown = true
		}
	}
	if r.pi.Thread != 0 {
		r.check(closeOwnedHandle("CloseHandle.thread", r.pi.Thread))
		r.pi.Thread = 0
	}
	if r.pi.Process != 0 {
		r.check(closeOwnedHandle("CloseHandle.process", r.pi.Process))
		r.pi.Process = 0
	}
	for _, h := range r.pins {
		r.check(closeOwnedHandle("CloseHandle.input", h))
	}
	r.pins = nil
	if r.profile.Handle != 0 {
		r.check(closeOwnedHandle("CloseHandle.profile", r.profile.Handle))
		r.profile.Handle = 0
	}
	if r.sid != nil {
		if err := windows.FreeSid(r.sid); err != nil {
			r.uncertain = true
			r.check(nativeError("FreeSid", err))
		}
		r.sid = nil
	}
	if !r.uncertain && r.profileMade && !r.fence.ProfileCreated {
		// A failed getter/binding before any child permits only native deletion
		// of the profile we just created, never walking its unproved path.
		if err := deleteProfile(r.fence.ProfileName); err != nil {
			r.uncertain = true
			r.check(err)
		}
	}
	if !r.uncertain && r.fenceWritten {
		r.fence.State = "closed"
		r.fence.Proof = "not_created"
		if r.created {
			r.fence.Proof = "job_empty"
		}
		if err := writeMarker(filepath.Join(r.dir, "fence.json"), r.fence); err != nil {
			r.uncertain = true
			r.check(err)
		} else {
			r.receipt.State = Closed
			r.observe("closed_fence")
			r.closeJob()
			if !r.uncertain {
				ctx, cancel := context.WithDeadline(context.Background(), r.stopAt.Add(r.request.Limits.StopGrace))
				err := r.executor.cleanup(ctx, r.fence)
				cancel()
				if err != nil {
					r.check(err)
				} else {
					r.receipt.ResourcesReusable = true
				}
			}
		}
	}
	r.closeJob()
	if r.uncertain || !r.fenceWritten {
		r.receipt.State = Quarantined
		r.receipt.ResourcesReusable = false
		r.err = errors.Join(r.err, ErrQuarantined)
	}
	if r.pipes != nil {
		r.receipt.Stdout, r.receipt.Stderr = r.pipes.freezeOutput()
		_, sealed, overflow := r.pipes.snapshot()
		r.check(nil)
		if sealed && !overflow && r.cause == NotStarted && r.receipt.ResourcesReusable && r.receipt.UsageKnown {
			r.receipt.OutputState = CompleteSealed
		}
	}
	if r.cause != NotStarted {
		r.receipt.Outcome = r.cause
	} else if r.created {
		r.receipt.Outcome = Exited
		if r.receipt.ExitCodeKnown && r.receipt.ExitCode != 0 {
			r.err = errors.Join(r.err, ErrProcessExit)
		}
	}
	if r.created && r.cause == NotStarted && r.receipt.OutputState != CompleteSealed && r.err == nil {
		r.check(fmt.Errorf("%w: incomplete stdio", ErrUnsupported))
		r.receipt.Outcome = r.cause
	}
	if r.stopAt.IsZero() {
		r.beginStop()
	}
	if r.resumed.IsZero() {
		r.receipt.Setup = r.stopAt.Sub(r.admitted)
	} else {
		r.receipt.Setup = r.resumed.Sub(r.admitted)
		r.receipt.Execution = r.stopAt.Sub(r.resumed)
	}
	r.receipt.Stop = time.Since(r.stopAt)
}
func (e *Executor) cleanup(ctx context.Context, f runFence) error {
	var active *nativeRun
	if e.observe != nil {
		e.mu.Lock()
		active = e.active
		e.mu.Unlock()
	}
	dir := filepath.Join(e.root, "runs", f.RunID)
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	binding, err := openDirectory(dir, false)
	if err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	if err := securePath(dir, e.owner, "", ""); err != nil {
		_ = windows.CloseHandle(binding.Handle)
		return errors.Join(ErrCleanupPending, err)
	}
	for _, name := range []string{"input", "work", "fence.json.tmp"} {
		if err := removeTree(ctx, filepath.Join(dir, name)); err != nil {
			_ = windows.CloseHandle(binding.Handle)
			return errors.Join(ErrCleanupPending, err)
		}
	}
	if f.ProfileCreated {
		profile, err := openDirectory(f.ProfilePath, true)
		if err == nil {
			if profile.Volume != f.ProfileVolume || profile.ID != f.ProfileFileID {
				_ = windows.CloseHandle(profile.Handle)
				_ = windows.CloseHandle(binding.Handle)
				return ErrCleanupPending
			}
			if !profile.Reparse {
				err = emptyDirectory(ctx, profile.Path)
			}
			err = errors.Join(err, windows.CloseHandle(profile.Handle))
			if err == nil && profile.Reparse {
				err = unlink(ctx, profile.Path)
			}
		} else if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		if err != nil {
			_ = windows.CloseHandle(binding.Handle)
			return errors.Join(ErrCleanupPending, err)
		}
		if err := ctx.Err(); err != nil {
			_ = windows.CloseHandle(binding.Handle)
			return errors.Join(ErrCleanupPending, err)
		}
		if err := deleteProfile(f.ProfileName); err != nil {
			_ = windows.CloseHandle(binding.Handle)
			return errors.Join(ErrCleanupPending, err)
		}
		if _, err := os.Lstat(f.ProfilePath); !errors.Is(err, os.ErrNotExist) {
			_ = windows.CloseHandle(binding.Handle)
			return errors.Join(ErrCleanupPending, err)
		}
	}
	if err := unlink(ctx, filepath.Join(dir, "fence.json")); err != nil {
		_ = windows.CloseHandle(binding.Handle)
		return errors.Join(ErrCleanupPending, err)
	}
	if active != nil {
		active.observe("fence_removed")
	}
	if err := windows.CloseHandle(binding.Handle); err != nil {
		return errors.Join(ErrQuarantined, err)
	}
	if err := unlink(ctx, dir); err != nil {
		return errors.Join(ErrQuarantined, err)
	}
	if active != nil {
		active.observe("run_removed")
	}
	if err := durable.SyncDir(filepath.Dir(dir)); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	return nil
}

func (r *nativeRun) closeJob() {
	if r.job == 0 {
		return
	}
	if r.uncertain {
		r.check(nativeError("TerminateJobObject", windows.TerminateJobObject(r.job, 1)))
	}
	r.check(closeOwnedHandle("CloseHandle.Job", r.job))
	r.job = 0
}

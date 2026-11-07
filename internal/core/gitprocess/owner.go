package gitprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
)

// Append-only: finalizers must not release a quarantined root's lock while a
// caller pump or unproved native resource can still access that root.
var unconfirmedOwners sync.Map

// Owner serializes root recovery and owns every admitted invocation until its
// native resources and stream pumps are joined. It must not be copied.
type Owner struct {
	platformState
	mu         sync.Mutex
	root       string
	rootID     uuid.UUID
	identity   os.FileInfo
	lock       *durable.Lock
	options    Options
	ctx        context.Context
	cancel     context.CancelFunc
	slots      chan struct{}
	ops        sync.WaitGroup
	closed     bool
	closeDone  chan struct{}
	closeErr   error
	runFailure error
	retained   bool
	pending    map[uuid.UUID]fence
	debris     Debris
}

// Open claims root without launching a process. A valid pending fence returns
// a usable recovery Owner, but Run refuses until Reconcile has proved closure.
func Open(ctx context.Context, root string, options Options) (*Owner, Report, error) {
	if err := checkContext(ctx); err != nil {
		return nil, Report{}, err
	}
	options, err := options.normalized()
	if err != nil {
		return nil, Report{}, err
	}
	if options.RequireTree && runtime.GOOS != "windows" {
		return nil, Report{}, ErrUnsupported
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, Report{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, Report{}, err
	}
	identity, err := realDirectory(root)
	if err != nil {
		return nil, Report{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, Report{}, err
	}
	lockPath := filepath.Join(root, "owner.lock")
	if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
		return nil, Report{}, ErrInvalid
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, Report{}, err
	}
	lock, err := durable.LockFile(lockPath)
	if err != nil {
		return nil, Report{}, err
	}
	o := &Owner{root: root, identity: identity, lock: lock, options: options, slots: make(chan struct{}, options.MaxConcurrent), closeDone: make(chan struct{}), pending: make(map[uuid.UUID]fence)}
	o.ctx, o.cancel = context.WithCancel(context.Background())
	if runtime.GOOS == "windows" {
		if err := o.loadFences(); err != nil {
			o.cancel()
			return nil, Report{}, errors.Join(err, lock.Close())
		}
	}
	return o, o.report(), nil
}
func (o *Owner) report() Report {
	r := Report{TreeProofSupported: runtime.GOOS == "windows", Debris: o.debris}
	for id := range o.pending {
		r.Pending = append(r.Pending, Pending{id, "closure_unconfirmed"})
	}
	slices.SortFunc(r.Pending, func(a, b Pending) int { return slices.Compare(a.RunID[:], b.RunID[:]) })
	return r
}

// Cancel closes process admission and signals all current calls without waiting.
// Close remains required to join them and release the root lock.
func (o *Owner) Cancel() { o.cancel() }

// Run waits cancellably for one slot. No slot is reusable on unconfirmed closure.
func (o *Owner) Run(ctx context.Context, spec Spec) (Result, error) {
	result := Result{ExitCode: -1}
	if err := checkContext(ctx); err != nil {
		return result, err
	}
	if !filepath.IsAbs(spec.Program) {
		return result, ErrInvalid
	}
	select {
	case o.slots <- struct{}{}:
	case <-ctx.Done():
		return result, ctx.Err()
	case <-o.ctx.Done():
		return result, ErrClosed
	}
	o.mu.Lock()
	if o.closed || o.ctx.Err() != nil || len(o.pending) != 0 {
		err := ErrClosed
		if len(o.pending) != 0 {
			err = ErrUnconfirmed
		}
		o.mu.Unlock()
		<-o.slots
		return result, err
	}
	o.ops.Add(1)
	o.mu.Unlock()
	defer o.ops.Done()
	runCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.ctx, cancel)
	defer func() { stop(); cancel() }()
	result.RunID = uuid.NewV7()
	result, retained, err := o.run(runCtx, spec, result)
	o.mu.Lock()
	if retained || errors.Is(err, ErrUnconfirmed) {
		o.retained = true
		o.runFailure = errors.Join(o.runFailure, err)
		unconfirmedOwners.Store(o, struct{}{})
	} else {
		<-o.slots
	}
	o.mu.Unlock()
	return result, err
}

// Reconcile never executes Git. Each closed result is resource evidence only.
func (o *Owner) Reconcile(ctx context.Context) (Report, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.ctx.Err() != nil {
		return o.report(), ErrClosed
	}
	if err := checkContext(ctx); err != nil {
		return o.report(), unconfirmed(uuid.UUID{}, "cancelled", err)
	}
	if o.retained || len(o.slots) != 0 {
		return o.report(), ErrUnconfirmed
	}
	var closed []Result
	var failures []error
	for _, p := range o.report().Pending {
		f := o.pending[p.RunID]
		result, err := o.reconcile(ctx, f)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		delete(o.pending, p.RunID)
		closed = append(closed, result)
	}
	r := o.report()
	r.Closed = closed
	return r, errors.Join(failures...)
}

// Close is idempotent, including failure. An unjoined pump or retained native
// resource keeps the root locked; durable pending fences continue to bar reuse.
func (o *Owner) Close() error {
	o.mu.Lock()
	if o.closed {
		done := o.closeDone
		o.mu.Unlock()
		<-done
		return o.closeErr
	}
	o.closed = true
	o.cancel()
	o.mu.Unlock()
	o.ops.Wait()
	o.mu.Lock()
	if o.retained || len(o.pending) != 0 {
		o.closeErr = errors.Join(ErrUnconfirmed, o.runFailure)
		o.retained = true
		unconfirmedOwners.Store(o, struct{}{})
	}
	if !o.retained {
		o.closeErr = errors.Join(o.closeErr, o.lock.Close())
	}
	close(o.closeDone)
	o.mu.Unlock()
	return o.closeErr
}

package workspace

import (
	"context"
	"fmt"
	"sync"
)

type scratchAccess struct {
	writers   int
	exclusive *reservation
	changed   chan struct{}
}
type reservationPool struct {
	mu        sync.Mutex
	profile   *CaptureProfile
	bytes     int64
	scratches map[string]*scratchAccess
}

// Only reserve mints this token. Capture verifies the token against the pool's
// live owner, so zero, released, reused and wrong-scratch tokens cannot capture.
type reservation struct {
	pool    *reservationPool
	scratch string
	bytes   int64
	used    bool
}

func newReservationPool(profile *CaptureProfile) (*reservationPool, error) {
	if err := validateProfile(profile); err != nil {
		return nil, err
	}
	p := &reservationPool{scratches: map[string]*scratchAccess{}}
	if profile != nil {
		copy := *profile
		p.profile = &copy
	}
	return p, nil
}

func (p *reservationPool) access(scratch string) *scratchAccess {
	a := p.scratches[scratch]
	if a == nil {
		a = &scratchAccess{changed: make(chan struct{})}
		p.scratches[scratch] = a
	}
	return a
}

// reserve never waits for a holder. capture=false is only for closure's initial
// exclusive claim; reserveCaptureBytes adds its pool claim before observation.
func (p *reservationPool) reserve(scratch string, capture bool) (*reservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if scratch == "" {
		return nil, fmt.Errorf("%w: missing scratch", ErrInvalid)
	}
	if capture && p.profile == nil {
		return nil, &UnavailableError{Reason: "no_capacity_profile"}
	}
	a := p.access(scratch)
	if a.exclusive != nil || a.writers > 0 {
		return nil, &BusyError{Reason: "scratch_busy"}
	}
	var amount int64
	if capture {
		amount = p.profile.MaxBytes
		if amount > p.profile.BytesInFlight-p.bytes {
			return nil, &BusyError{Reason: "capture_budget"}
		}
	}
	r := &reservation{pool: p, scratch: scratch, bytes: amount}
	a.exclusive = r
	p.bytes += amount
	return r, nil
}

func (r *reservation) reserveCaptureBytes() error {
	if r == nil || r.pool == nil {
		return &reservationError{reason: "missing reservation"}
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[r.scratch]
	if a == nil || a.exclusive != r || r.used {
		return &reservationError{reason: "inactive or used reservation"}
	}
	if p.profile == nil {
		return &UnavailableError{Reason: "no_capacity_profile"}
	}
	if r.bytes != 0 {
		return nil
	}
	if p.profile.MaxBytes > p.profile.BytesInFlight-p.bytes {
		return &BusyError{Reason: "capture_budget"}
	}
	r.bytes = p.profile.MaxBytes
	p.bytes += r.bytes
	return nil
}

func (r *reservation) beginCapture(scratch string) (CaptureProfile, error) {
	if r == nil || r.pool == nil {
		return CaptureProfile{}, &reservationError{reason: "capture requires a reservation"}
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[scratch]
	if p.profile == nil || a == nil || a.exclusive != r || r.scratch != scratch || r.bytes != p.profile.MaxBytes || r.used {
		return CaptureProfile{}, &reservationError{reason: "capture requires its unused live byte reservation"}
	}
	r.used = true
	return *p.profile, nil
}

// Capture returns only its I/O allowance. The calling operation keeps the
// exclusion until its durable record and any removal are complete.
func (r *reservation) releaseCaptureBytes() {
	if r == nil || r.pool == nil {
		return
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[r.scratch]
	if a == nil || a.exclusive != r {
		return
	}
	p.bytes -= r.bytes
	r.bytes = 0
}

func (r *reservation) release() {
	if r == nil || r.pool == nil {
		return
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[r.scratch]
	if a == nil || a.exclusive != r {
		return
	}
	p.bytes -= r.bytes
	r.bytes = 0
	a.exclusive = nil
	close(a.changed)
	a.changed = make(chan struct{})
}

func (p *reservationPool) beginWrite(ctx context.Context, scratch string) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		a := p.access(scratch)
		if a.exclusive == nil {
			a.writers++
			p.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					p.mu.Lock()
					defer p.mu.Unlock()
					a.writers--
					close(a.changed)
					a.changed = make(chan struct{})
				})
			}, nil
		}
		changed := a.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

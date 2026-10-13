package workspace

import (
	"context"
	"fmt"
	"sync"
)

type reservationKind uint8

const (
	closureReservation reservationKind = iota
	admissionReservation
	observationReservation
	soleWriterReservation
	subordinateReservation
)

type scratchAccess struct {
	writers   int
	live      int
	exclusive *reservation
	changed   chan struct{}
}
type reservationPool struct {
	mu        sync.Mutex
	profile   *CapacityProfile
	bytes     int64
	scratches map[string]*scratchAccess
}

// Tokens are minted and checked under the pool mutex. A subordinate capture is
// registered on its live exclusive guard, never a second scratch owner.
type reservation struct {
	pool    *reservationPool
	scratch string
	bytes   int64
	used    bool
	kind    reservationKind
	kept    bool
	guard   *reservation
	child   *reservation
}

func newReservationPool(profile *CapacityProfile) (*reservationPool, error) {
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

// reserve acquires all of a kind's parts or none and never waits for a holder.
// Closure adds its byte claim separately, before observation.
func (p *reservationPool) reserve(scratch string, kind reservationKind) (*reservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if scratch == "" || (kind != closureReservation && kind != admissionReservation && kind != observationReservation) {
		return nil, fmt.Errorf("%w: missing scratch or invalid reservation kind", ErrInvalid)
	}
	if kind != closureReservation && p.profile == nil {
		return nil, &UnavailableError{Reason: "no_capacity_profile"}
	}
	a := p.access(scratch)
	if a.exclusive != nil || a.writers > 0 {
		return nil, &BusyError{Reason: "scratch_busy"}
	}
	var amount int64
	if kind != closureReservation {
		if kind == admissionReservation && a.live >= p.profile.CheckpointsPerPair {
			return nil, &BusyError{Reason: "checkpoint_slots"}
		}
		amount = p.profile.MaxBytes
		if amount > p.profile.BytesInFlight-p.bytes {
			return nil, &BusyError{Reason: "capture_budget"}
		}
	}
	r := &reservation{pool: p, scratch: scratch, bytes: amount, kind: kind}
	a.exclusive = r
	p.bytes += amount
	return r, nil
}

// beginSoleWrite waits on the writer side, not in the immediate capture path.
func (p *reservationPool) beginSoleWrite(ctx context.Context, scratch string) (*reservation, error) {
	if scratch == "" {
		return nil, fmt.Errorf("%w: missing scratch", ErrInvalid)
	}
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		a := p.access(scratch)
		if a.exclusive == nil && a.writers == 0 {
			r := &reservation{pool: p, scratch: scratch, kind: soleWriterReservation}
			a.exclusive = r
			p.mu.Unlock()
			return r, nil
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

func (p *reservationPool) reserveCaptureUnder(guard *reservation) (*reservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if guard == nil || guard.pool != p {
		return nil, &reservationError{reason: "capture requires its live guard"}
	}
	a := p.scratches[guard.scratch]
	if a == nil || a.exclusive != guard || guard.child != nil || guard.bytes != 0 {
		return nil, &reservationError{reason: "inactive guard or outstanding capture"}
	}
	if p.profile == nil {
		return nil, &UnavailableError{Reason: "no_capacity_profile"}
	}
	if p.profile.MaxBytes > p.profile.BytesInFlight-p.bytes {
		return nil, &BusyError{Reason: "capture_budget"}
	}
	r := &reservation{pool: p, scratch: guard.scratch, bytes: p.profile.MaxBytes, kind: subordinateReservation, guard: guard}
	guard.child = r
	p.bytes += r.bytes
	return r, nil
}

// active is called only while holding the pool mutex.
func (r *reservation) active(a *scratchAccess) bool {
	if a == nil {
		return false
	}
	if r.kind == subordinateReservation {
		return r.guard != nil && a.exclusive == r.guard && r.guard.child == r
	}
	return a.exclusive == r
}

func (r *reservation) reserveCaptureBytes() error {
	if r == nil || r.pool == nil {
		return &reservationError{reason: "missing reservation"}
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[r.scratch]
	if a == nil || a.exclusive != r || r.used || r.child != nil || r.kind == soleWriterReservation {
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

func (r *reservation) beginCapture(scratch string) (CapacityProfile, error) {
	if r == nil || r.pool == nil {
		return CapacityProfile{}, &reservationError{reason: "capture requires a reservation"}
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[scratch]
	if p.profile == nil || !r.active(a) || r.scratch != scratch || r.bytes != p.profile.MaxBytes || r.used {
		return CapacityProfile{}, &reservationError{reason: "capture requires its unused live byte reservation"}
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
	if !r.active(a) {
		return
	}
	p.bytes -= r.bytes
	r.bytes = 0
	if r.kind == subordinateReservation {
		r.guard.child = nil
	}
}

func (r *reservation) release() {
	if r == nil || r.pool == nil {
		return
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[r.scratch]
	if !r.active(a) {
		return
	}
	p.bytes -= r.bytes
	r.bytes = 0
	if r.kind == subordinateReservation {
		r.guard.child = nil
		return
	}
	if r.child != nil {
		p.bytes -= r.child.bytes
		r.child.bytes = 0
		r.child = nil
	}
	a.exclusive = nil
	close(a.changed)
	a.changed = make(chan struct{})
}

// The exclusive admission token itself holds the provisional slot. A kept
// slot outlives release; the acknowledged ending's owner frees it separately.
func (r *reservation) keepSlot() {
	if r == nil || r.pool == nil {
		return
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.scratches[r.scratch]
	if a == nil || a.exclusive != r || r.kind != admissionReservation || r.kept {
		return
	}
	a.live++
	r.kept = true
}

// freeSlot is called once per acknowledged request ending, by its owner.
func (p *reservationPool) freeSlot(scratch string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.access(scratch)
	if a.live > 0 {
		a.live--
	}
}

// setLive restores the projection's counts at Open, before operations begin.
func (p *reservationPool) setLive(scratch string, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.access(scratch).live = n
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

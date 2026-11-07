package gitprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
	"uuid"
)

var (
	ErrUnsupported = errors.New("git process tree proof unsupported")
	ErrUnconfirmed = errors.New("git process closure unconfirmed")
	ErrClosed      = errors.New("git process owner closed")
	ErrInvalid     = errors.New("invalid git process state")
	ErrExit        = errors.New("git process exited unsuccessfully")
	ErrLimit       = errors.New("git process active-process limit exceeded")
)

// UnconfirmedError preserves a native or filesystem cause without authorizing reuse.
type UnconfirmedError struct {
	RunID  uuid.UUID
	Reason string
	Err    error
}

func (e *UnconfirmedError) Error() string   { return fmt.Sprintf("%s: %s", ErrUnconfirmed, e.Reason) }
func (e *UnconfirmedError) Unwrap() []error { return []error{ErrUnconfirmed, e.Err} }

// Options bounds simultaneous roots, Job membership and shutdown proof time.
// Zero values select the calibrated defaults, not a runtime capacity claim.
type Options struct {
	MaxConcurrent  int
	MaxDescendants int
	StopGrace      time.Duration
	RequireTree    bool
}

func (o Options) normalized() (Options, error) {
	if o.MaxConcurrent == 0 {
		o.MaxConcurrent = 8
	}
	if o.MaxDescendants == 0 {
		o.MaxDescendants = 64
	}
	if o.StopGrace == 0 {
		o.StopGrace = 5 * time.Second
	}
	if o.MaxConcurrent < 1 || o.MaxDescendants < 1 || uint64(o.MaxDescendants) > uint64(^uint32(0)) || o.StopGrace < 0 {
		return o, ErrInvalid
	}
	return o, nil
}

// Spec is one trusted workspace Git invocation. Streams remain caller-owned.
// Callers must not mutate arguments or environment while Run is in flight.
type Spec struct {
	Program        string
	Args, Env      []string
	Dir            string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Started observes the resumed root once, synchronously. It must return
	// promptly and must not retain or signal the process; see package docs.
	Started func(pid int)
}

// Result describes resource closure, never whether a Git effect was absent.
// Recovered runs have ExitCode -1; a missing-name proof has no usage totals.
type Result struct {
	RunID                                      uuid.UUID
	RootExited, TreeClosed, IOJoined           bool
	ExitCode                                   int
	TotalProcesses                             uint32
	User100ns, Kernel100ns, PeakJobMemoryBytes uint64
	Proof                                      string
}

type Pending struct {
	RunID  uuid.UUID
	Reason string
}

// Debris counts only shapes that cannot have launched a native child.
type Debris struct{ EmptyRuns, PendingRuns, PendingIdentities int }
type Report struct {
	Pending            []Pending
	Closed             []Result
	TreeProofSupported bool
	Debris             Debris
}

func unconfirmed(id uuid.UUID, reason string, err error) error {
	return &UnconfirmedError{RunID: id, Reason: reason, Err: err}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	return ctx.Err()
}

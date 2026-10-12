package workspace

import (
	"errors"
	"fmt"
)

var (
	ErrInvalid             = errors.New("workspace: invalid argument")
	ErrGit                 = errors.New("workspace: Git failed")
	ErrGitUnavailable      = errors.New("workspace: Git unavailable")
	ErrNotGitRepository    = errors.New("workspace: not a Git repository")
	ErrNoCanonical         = errors.New("workspace: main is absent")
	ErrCanonicalCheckedOut = errors.New("workspace: main is checked out")
	ErrInputsTooLarge      = errors.New("workspace: capture input exceeds 1 MiB")
	ErrOtherColony         = errors.New("workspace: repository belongs to another colony")
	ErrRemovalPending      = errors.New("workspace: removal pending")
	ErrUnavailable         = errors.New("workspace: unavailable")
	ErrBusy                = errors.New("workspace: busy")
	ErrCaptureRefused      = errors.New("workspace: capture refused")
)

// GitError describes an unexpected exit without interpreting Git's messages.
// Exit is -1 when the process could not start or did not report an exit code.
// Stderr is limited to its first 64 KiB. Error text is not an API.
type GitError struct {
	Args   []string
	Exit   int
	Stderr string
	// Err preserves cancellation, process-start and wait failures for errors.Is.
	Err error
}

func (e *GitError) Error() string {
	return fmt.Sprintf("%v: %q exited %d: %s", ErrGit, e.Args, e.Exit, e.Stderr)
}
func (e *GitError) Unwrap() []error {
	if e.Err != nil {
		return []error{ErrGit, e.Err}
	}
	return []error{ErrGit}
}

// UnavailableError gives a machine-readable reason. Detail names the pinned
// and running Git versions for inputs_stale.
type UnavailableError struct{ Reason, Detail string }

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrUnavailable, e.Reason, e.Detail)
}
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// BusyError reports immediate contention without a snapshot or a queued retry.
type BusyError struct{ Reason string }

func (e *BusyError) Error() string { return fmt.Sprintf("%v: %s", ErrBusy, e.Reason) }
func (e *BusyError) Unwrap() error { return ErrBusy }

// RefusalError names the candidate defect; no tree is sealed on a refusal.
type RefusalError struct{ Reason, Path, Detail string }

func (e *RefusalError) Error() string {
	return fmt.Sprintf("%v: %s: %s: %s", ErrCaptureRefused, e.Reason, e.Path, e.Detail)
}
func (e *RefusalError) Unwrap() error { return ErrCaptureRefused }

// reservationError is an internal misuse error, not a busy or unavailable
// operation. A token never becomes valid by copying its fields.
type reservationError struct{ reason string }

func (e *reservationError) Error() string { return fmt.Sprintf("%v: %s", ErrInvalid, e.reason) }
func (e *reservationError) Unwrap() error { return ErrInvalid }

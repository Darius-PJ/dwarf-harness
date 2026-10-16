package workspace

import (
	"errors"
	"fmt"
	"uuid"
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
	ErrExists              = errors.New("workspace: already exists")
	ErrStaleProposal       = errors.New("workspace: stale proposal")
	ErrRevision            = errors.New("workspace: inputs revision changed")
	ErrUnknownWorkspace    = errors.New("workspace: unknown workspace")
	ErrUnknownRequest      = errors.New("workspace: unknown request")
	ErrUnknownActivation   = errors.New("workspace: unknown activation")
	ErrActivationMismatch  = errors.New("workspace: activation ID already used for a different binding")
	ErrNoScratch           = errors.New("workspace: no open scratch")
	ErrNotBound            = errors.New("workspace: caller is not bound")
	ErrUnrepresentedEdits  = errors.New("workspace: unrepresented edits")
	ErrClosed              = errors.New("workspace: closed")
	ErrPoisoned            = errors.New("workspace: poisoned; recovery required")
	ErrInconsistent        = errors.New("workspace: inconsistent history")
	ErrRequestMismatch     = errors.New("workspace: request ID already used for a different request")
	ErrStaleHead           = errors.New("workspace: stale request head")
	ErrLiveRequests        = errors.New("workspace: scratch has live promotion requests")
	ErrWriterMismatch      = errors.New("workspace: writer ID already used for a different binding")
	ErrTemporaryChildren   = errors.New("workspace: scratch has open temporary children")
	ErrUncommittedEdits    = errors.New("workspace: scratch has uncommitted edits")
	ErrMergeBackPending    = errors.New("workspace: merge-back pending")
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

// InconsistentError identifies the first record whose service-owned rule fails.
type InconsistentError struct {
	Seq    uint64
	Detail string
}

func (e *InconsistentError) Error() string {
	return fmt.Sprintf("%v at sequence %d: %s", ErrInconsistent, e.Seq, e.Detail)
}
func (e *InconsistentError) Unwrap() error { return ErrInconsistent }

// HeadError reveals only this context's local head, never train state.
type HeadError struct {
	Head            uuid.UUID
	RestartRequired bool
}

func (e *HeadError) Error() string { return fmt.Sprintf("%v: %s", ErrStaleHead, e.Head) }
func (e *HeadError) Unwrap() error { return ErrStaleHead }

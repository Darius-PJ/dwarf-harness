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
)

// GitError describes an unexpected exit without interpreting Git's messages.
// Exit is -1 when the process could not start or did not report an exit code.
// Stderr is limited to its first 64 KiB. Error text is not an API.
type GitError struct {
	Args   []string
	Exit   int
	Stderr string
}

func (e *GitError) Error() string {
	return fmt.Sprintf("%v: %q exited %d: %s", ErrGit, e.Args, e.Exit, e.Stderr)
}
func (e *GitError) Unwrap() error { return ErrGit }

// UnavailableError gives a machine-readable reason. Detail names the pinned
// and running Git versions for inputs_stale.
type UnavailableError struct{ Reason, Detail string }

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrUnavailable, e.Reason, e.Detail)
}
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

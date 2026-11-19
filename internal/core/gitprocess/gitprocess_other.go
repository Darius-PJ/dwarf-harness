//go:build !windows

package gitprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"uuid"
)

type platformState struct {
	unjoined []*exec.Cmd
}

type slotBatch struct {
	data     slotsContainer
	identity os.FileInfo
}

func (o *Owner) run(ctx context.Context, spec Spec, result Result) (Result, bool, error) {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = spec.Dir, spec.Env, spec.Stdin, spec.Stdout, spec.Stderr
	cmd.WaitDelay = o.options.StopGrace
	err := cmd.Start()
	if err == nil {
		if spec.Started != nil {
			spec.Started(cmd.Process.Pid)
		}
		err = cmd.Wait()
	}
	if cmd.ProcessState != nil {
		result.RootExited = true
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	result.IOJoined = !errors.Is(err, exec.ErrWaitDelay)
	var exit *exec.ExitError
	if cancelled := ctx.Err(); cancelled != nil {
		err = errors.Join(err, cancelled)
	} else if errors.As(err, &exit) {
		err = errors.Join(ErrExit, err)
	}
	if !result.IOJoined {
		err = errors.Join(err, ErrUnconfirmed)
		o.mu.Lock()
		o.unjoined = append(o.unjoined, cmd)
		o.mu.Unlock()
	}
	return result, !result.IOJoined, err
}
func (o *Owner) reconcile(context.Context, fence) (Result, error) {
	return Result{ExitCode: -1}, ErrUnsupported
}

func (o *Owner) hasUnreconciledReservations() bool                       { return false }
func (o *Owner) reservationCount() int                                   { return 0 }
func (o *Owner) closeReservations() error                                { return nil }
func (o *Owner) reconcileReservations(context.Context) ([]Result, error) { return nil, nil }
func (o *Owner) reservedRun(uuid.UUID) bool                              { return false }

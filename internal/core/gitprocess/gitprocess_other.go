//go:build !windows

package gitprocess

import (
	"context"
	"errors"
	"os/exec"
)

type platformState struct {
	unjoined []*exec.Cmd
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

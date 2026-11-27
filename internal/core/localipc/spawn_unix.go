//go:build linux || darwin

package localipc

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// Spawn gives the detached child only a private startup-result channel.
func Spawn(args []string) (*os.File, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, false, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, false, err
	}
	cmd := exec.Command(executable, append(args, "--startup-handle", "3")...)
	cmd.ExtraFiles = []*os.File{writer}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	closeErr := writer.Close()
	if err != nil {
		return nil, false, errors.Join(err, closeErr, reader.Close())
	}
	releaseErr := cmd.Process.Release()
	if err = errors.Join(closeErr, releaseErr); err != nil {
		return nil, false, errors.Join(err, reader.Close())
	}
	return reader, false, nil
}
func StartupChannel(value string) (*os.File, error) {
	fd, err := strconv.ParseUint(value, 10, 32)
	if err != nil || fd != 3 {
		return nil, ErrUnavailable
	}
	return os.NewFile(uintptr(fd), "host-startup"), nil
}

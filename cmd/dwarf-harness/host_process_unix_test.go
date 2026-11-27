//go:build linux || darwin

package main

import (
	"errors"
	"syscall"
)

func init() {
	waitDetachedTestChild = func(pid int, waiting func()) error {
		// The helper bootstrap remains this detached process's real parent even
		// after Spawn releases Go's process handle. Reap before reopening the store.
		waiting()
		var status syscall.WaitStatus
		for {
			got, err := syscall.Wait4(pid, &status, 0, nil)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil {
				return err
			}
			if got != pid || !status.Signaled() {
				return errors.New("crash child was not killed")
			}
			return nil
		}
	}
}

//go:build windows

package agents

import (
	"context"
	"errors"
	"os/exec"
)

func loopTestDirectoryLink(link, target string) error {
	if err := exec.CommandContext(context.Background(), "cmd.exe", "/c", "mklink", "/J", link, target).Run(); err != nil {
		return errors.New("native junction fixture unavailable")
	}
	return nil
}

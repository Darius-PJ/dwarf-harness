//go:build !windows

package agents

import "os"

func loopTestDirectoryLink(link, target string) error { return os.Symlink(target, link) }

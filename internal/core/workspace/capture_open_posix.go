//go:build !windows

package workspace

import "os"

func openCaptureFile(path string) (*os.File, error) {
	return os.Open(path)
}

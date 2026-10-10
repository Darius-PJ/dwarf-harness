//go:build linux || darwin

package ledgerstore

import (
	"runtime"
	"syscall"
)

// peakMemory returns the peak resident set of this process in bytes. Peak
// commit is not reported on Linux and macOS, so the second result is 0.
// Getrusage reports Maxrss in KiB on Linux and in bytes on macOS.
func peakMemory() (workingSet, commit uint64, err error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0, err
	}
	maxrss := uint64(ru.Maxrss)
	if runtime.GOOS == "linux" {
		maxrss *= 1024
	}
	return maxrss, 0, nil
}

// freeDiskBytes returns the bytes available to this user on the file system
// that holds path.
func freeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

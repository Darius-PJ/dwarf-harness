package ledgerstore

import (
	"fmt"
	"syscall"
	"unsafe"
)

// The workload harness (workload_test.go) measures a process's peak memory and
// a volume's free space through kernel32, called with the standard library's
// syscall package, so that the measurement adds no dependency (D29) and keeps
// golang.org/x/sys inside internal/core/durable (B3).
var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	procGetDiskFreeSpaceEx   = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS: cb, PageFaultCount
// and eight SIZE_T fields, 72 bytes on amd64.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// peakMemory returns the peak working set of this process in bytes and its
// peak commit charge, the peak pagefile usage, in bytes.
func peakMemory() (workingSet, commit uint64, err error) {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, 0, err
	}
	var c processMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	if r, _, callErr := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r == 0 {
		return 0, 0, fmt.Errorf("K32GetProcessMemoryInfo: %w", callErr)
	}
	return uint64(c.peakWorkingSetSize), uint64(c.peakPagefileUsage), nil
}

// freeDiskBytes returns the bytes available to this user on the volume that
// holds path.
func freeDiskBytes(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	r, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceExW %s: %w", path, callErr)
	}
	return free, nil
}

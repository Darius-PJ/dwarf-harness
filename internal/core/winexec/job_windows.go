package winexec

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func setJob(job windows.Handle, class uint32, p unsafe.Pointer, n uintptr) error {
	// The x/sys wrapper takes uintptr, so KeepAlive alone cannot prevent a
	// stack move before its syscall. Pinning also forces the buffer to the heap.
	var pin runtime.Pinner
	pin.Pin(p)
	defer pin.Unpin()
	_, err := windows.SetInformationJobObject(job, class, uintptr(p), uint32(n))
	runtime.KeepAlive(p)
	return nativeError("SetInformationJobObject", err)
}
func queryJob(job windows.Handle, class int32, p unsafe.Pointer, n uintptr) error {
	var pin runtime.Pinner
	pin.Pin(p)
	defer pin.Unpin()
	err := windows.QueryInformationJobObject(job, class, uintptr(p), uint32(n), nil)
	runtime.KeepAlive(p)
	return nativeError("QueryInformationJobObject", err)
}
func jobLimits(l Limits) windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_TIME
	info.BasicLimitInformation.ActiveProcessLimit = l.Processes
	info.BasicLimitInformation.PerJobUserTimeLimit = int64(l.UserCPU / (100 * time.Nanosecond))
	info.JobMemoryLimit = uintptr(l.JobCommitBytes)
	return info
}
func createJob(l Limits) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, nativeError("CreateJobObject", err)
	}
	info := jobLimits(l)
	rate := jobCPURate{Flags: 5, Rate: l.CPUPercent * 100}
	var action uint32
	if err = setJob(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&info), unsafe.Sizeof(info)); err == nil {
		err = setJob(job, windows.JobObjectCpuRateControlInformation, unsafe.Pointer(&rate), unsafe.Sizeof(rate))
	}
	if err == nil {
		err = setJob(job, windows.JobObjectEndOfJobTimeInformation, unsafe.Pointer(&action), unsafe.Sizeof(action))
	}
	if err != nil {
		return 0, errors.Join(err, closeOwnedHandle("CloseHandle.Job", job))
	}
	return job, nil
}
func verifyJob(job windows.Handle, l Limits) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&info), unsafe.Sizeof(info)); err != nil {
		return err
	}
	want := jobLimits(l)
	if info.BasicLimitInformation.LimitFlags != want.BasicLimitInformation.LimitFlags || info.BasicLimitInformation.ActiveProcessLimit != l.Processes || info.BasicLimitInformation.PerJobUserTimeLimit != want.BasicLimitInformation.PerJobUserTimeLimit || info.JobMemoryLimit != want.JobMemoryLimit {
		return fmt.Errorf("%w: Job controls mismatch (actual/expected flags=%#x/%#x processes=%d/%d user100ns=%d/%d memoryBytes=%d/%d)",
			ErrUnsupported, info.BasicLimitInformation.LimitFlags, want.BasicLimitInformation.LimitFlags,
			info.BasicLimitInformation.ActiveProcessLimit, l.Processes,
			info.BasicLimitInformation.PerJobUserTimeLimit, want.BasicLimitInformation.PerJobUserTimeLimit,
			info.JobMemoryLimit, want.JobMemoryLimit)
	}
	var rate jobCPURate
	if err := queryJob(job, windows.JobObjectCpuRateControlInformation, unsafe.Pointer(&rate), unsafe.Sizeof(rate)); err != nil {
		return err
	}
	if rate.Flags != 5 || rate.Rate != l.CPUPercent*100 {
		return fmt.Errorf("%w: CPU rate mismatch", ErrUnsupported)
	}
	var action uint32
	if err := queryJob(job, windows.JobObjectEndOfJobTimeInformation, unsafe.Pointer(&action), unsafe.Sizeof(action)); err != nil {
		return err
	}
	if action != 0 {
		return fmt.Errorf("%w: Job time action mismatch", ErrUnsupported)
	}
	return nil
}
func jobAccounting(job windows.Handle) (jobBasicAccounting, error) {
	var a jobBasicAccounting
	err := queryJob(job, windows.JobObjectBasicAccountingInformation, unsafe.Pointer(&a), unsafe.Sizeof(a))
	return a, err
}
func jobUsage(job windows.Handle) (Usage, error) {
	var a jobBasicAndIO
	var e windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(job, windows.JobObjectBasicAndIoAccountingInformation, unsafe.Pointer(&a), unsafe.Sizeof(a)); err != nil {
		return Usage{}, err
	}
	if err := queryJob(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&e), unsafe.Sizeof(e)); err != nil {
		return Usage{}, err
	}
	if a.Basic.User < 0 || a.Basic.Kernel < 0 {
		return Usage{}, ErrUnsupported
	}
	return Usage{User100ns: uint64(a.Basic.User), Kernel100ns: uint64(a.Basic.Kernel), PeakJobMemoryBytes: uint64(e.PeakJobMemoryUsed), PeakProcessMemoryBytes: uint64(e.PeakProcessMemoryUsed), ReadBytes: a.IO.ReadTransferCount, WriteBytes: a.IO.WriteTransferCount, OtherBytes: a.IO.OtherTransferCount, TotalProcesses: a.Basic.Total}, nil
}
func commandLine(input string, r Request) (string, []uint16, error) {
	local, err := filepath.Localize(r.Program)
	if err != nil || !filepath.IsLocal(local) {
		return "", nil, ErrInvalid
	}
	program := filepath.Join(input, local)
	argv := append([]string{program}, r.Args...)
	command, err := windows.UTF16FromString(windows.ComposeCommandLine(argv))
	if err != nil || len(command) > 32767 {
		return "", nil, fmt.Errorf("%w: command line exceeds native representation", ErrInvalid)
	}
	return program, command, nil
}
func createSuspended(input, work string, r Request, sid *windows.SID, job windows.Handle, handles [3]windows.Handle) (windows.ProcessInformation, error) {
	var pi windows.ProcessInformation
	program, command, err := commandLine(input, r)
	if err != nil {
		return pi, err
	}
	app, err := windows.UTF16PtrFromString(program)
	if err != nil {
		return pi, err
	}
	system, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return pi, err
	}
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return pi, err
	}
	var environment []uint16
	// Sorted case-insensitively, with no ambient credentials, PATH or HOME.
	for _, value := range []string{"DWARF_WORK=" + work, "LOCALAPPDATA=" + localAppData, "SystemRoot=" + system, "TEMP=" + work, "TMP=" + work, "WINDIR=" + system} {
		part, err := windows.UTF16FromString(value)
		if err != nil {
			return pi, err
		}
		environment = append(environment, part...)
	}
	environment = append(environment, 0)
	cwd, err := windows.UTF16PtrFromString(input)
	if err != nil {
		return pi, err
	}
	attributes, err := windows.NewProcThreadAttributeList(3)
	if err != nil {
		return pi, nativeError("InitializeProcThreadAttributeList", err)
	}
	caps := securityCapabilities{SID: sid}
	jobs := [1]windows.Handle{job}
	var pin runtime.Pinner
	pin.Pin(&caps)
	pin.Pin(&jobs[0])
	pin.Pin(&handles[0])
	defer func() {
		attributes.Delete()
		runtime.KeepAlive(sid)
		runtime.KeepAlive(caps)
		runtime.KeepAlive(jobs)
		runtime.KeepAlive(handles)
		pin.Unpin()
	}()
	for _, a := range []struct {
		key uintptr
		p   unsafe.Pointer
		n   uintptr
	}{{0x00020009, unsafe.Pointer(&caps), unsafe.Sizeof(caps)}, {0x0002000d, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(jobs)}, {windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), unsafe.Sizeof(handles)}} {
		if err := attributes.Update(a.key, a.p, a.n); err != nil {
			return pi, nativeError("UpdateProcThreadAttribute", err)
		}
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = handles[0]
	startup.StdOutput = handles[1]
	startup.StdErr = handles[2]
	startup.ProcThreadAttributeList = attributes.List()
	err = windows.CreateProcess(app, &command[0], nil, nil, true, windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT|windows.DETACHED_PROCESS, &environment[0], cwd, &startup.StartupInfo, &pi)
	return pi, nativeError("CreateProcess", err)
}

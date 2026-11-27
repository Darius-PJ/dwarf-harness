package localipc

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func Spawn(args []string) (*os.File, bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, false, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, false, err
	}
	fail := func(err error) (*os.File, bool, error) {
		return nil, false, errors.Join(err, reader.Close(), writer.Close())
	}
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	var inJob int32
	query := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	ok, _, queryErr := query.Call(uintptr(windows.CurrentProcess()), 0, uintptr(unsafe.Pointer(&inJob)))
	if ok == 0 {
		return fail(queryErr)
	}
	jobBound := false
	if inJob != 0 {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			return fail(err)
		}
		if info.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK != 0 {
			flags |= windows.CREATE_BREAKAWAY_FROM_JOB
		} else {
			jobBound = true
		}
	}
	raw, err := writer.SyscallConn()
	if err != nil {
		return fail(err)
	}
	var inherited windows.Handle
	var duplicateErr error
	err = raw.Control(func(fd uintptr) {
		duplicateErr = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(fd), windows.CurrentProcess(), &inherited, 0, true, windows.DUPLICATE_SAME_ACCESS)
	})
	if err = errors.Join(err, duplicateErr); err != nil {
		return fail(err)
	}
	cmd := exec.Command(executable, append(args, "--startup-handle", strconv.FormatUint(uint64(inherited), 10))...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(inherited)}}
	startErr := cmd.Start()
	closeErr := errors.Join(windows.CloseHandle(inherited), writer.Close())
	if startErr != nil {
		return nil, false, errors.Join(startErr, closeErr, reader.Close())
	}
	releaseErr := cmd.Process.Release()
	if err = errors.Join(closeErr, releaseErr); err != nil {
		return nil, jobBound, errors.Join(err, reader.Close())
	}
	return reader, jobBound, nil
}
func StartupChannel(value string) (*os.File, error) {
	handle, err := strconv.ParseUint(value, 10, 64)
	if err != nil || handle == 0 {
		return nil, ErrUnavailable
	}
	if windows.SetHandleInformation(windows.Handle(handle), windows.HANDLE_FLAG_INHERIT, 0) != nil {
		return nil, ErrUnavailable
	}
	return os.NewFile(uintptr(handle), "host-startup"), nil
}

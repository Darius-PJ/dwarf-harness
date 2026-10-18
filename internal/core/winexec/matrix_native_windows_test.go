package winexec

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var testQueryProcessProc = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryInformationProcess")

// PROCESS_HANDLE_TABLE_ENTRY_INFO, returned by ProcessHandleInformation.
// uintptr preserves the native layout on both supported Windows word sizes.
type testHandleEntry struct {
	Handle                    windows.Handle
	HandleCount, PointerCount uintptr
	GrantedAccess, ObjectType uint32
	Attributes, Reserved      uint32
}

func testProcessHandles(process windows.Handle) ([]testHandleEntry, error) {
	if err := testQueryProcessProc.Find(); err != nil {
		return nil, err
	}
	// The child is still suspended. A fixed-size snapshot bounds the observer
	// and no user instruction can close/reuse an entry before duplication.
	buffer := make([]uintptr, 8192)
	bytes := uintptr(len(buffer)) * unsafe.Sizeof(buffer[0])
	var returned uint32
	status, _, _ := testQueryProcessProc.Call(uintptr(process), uintptr(windows.ProcessHandleInformation), uintptr(unsafe.Pointer(&buffer[0])), bytes, uintptr(unsafe.Pointer(&returned)))
	if status != 0 {
		return nil, fmt.Errorf("NtQueryInformationProcess(ProcessHandleInformation): 0x%08x: %w", uint32(status), windows.NTStatus(status))
	}
	headerBytes := 2 * unsafe.Sizeof(buffer[0])
	count := buffer[0]
	if uintptr(returned) > bytes || count > (bytes-headerBytes)/unsafe.Sizeof(testHandleEntry{}) {
		return nil, fmt.Errorf("invalid native handle snapshot: count=%d returned=%d capacity=%d", count, returned, bytes)
	}
	entries := unsafe.Slice((*testHandleEntry)(unsafe.Add(unsafe.Pointer(&buffer[0]), headerBytes)), int(count))
	return append([]testHandleEntry(nil), entries...), nil
}

func testInspectInheritedHandles(r *nativeRun, canary windows.ByHandleFileInformation) (int, error) {
	entries, err := testProcessHandles(r.pi.Process)
	if err != nil {
		return 0, err
	}
	expected := make(map[windows.Handle]bool, 3)
	for _, handle := range r.pipes.child {
		expected[handle] = false
	}
	var fileObjectType uint32
	for _, entry := range entries {
		if _, ok := expected[entry.Handle]; ok {
			expected[entry.Handle] = true
			fileObjectType = entry.ObjectType
		}
	}
	for handle, found := range expected {
		if !found {
			return len(entries), fmt.Errorf("explicit standard handle %x absent from native snapshot: %+v", handle, entries)
		}
	}
	inspect := func(entry testHandleEntry) (err error) {
		var duplicate windows.Handle
		if err := windows.DuplicateHandle(r.pi.Process, entry.Handle, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return fmt.Errorf("duplicate actual child entry %x: %w", entry.Handle, err)
		}
		defer func() { err = errors.Join(err, windows.CloseHandle(duplicate)) }()
		_, standard := expected[entry.Handle]
		if entry.ObjectType == fileObjectType {
			kind, err := windows.GetFileType(duplicate)
			if err != nil {
				return err
			}
			switch kind {
			case windows.FILE_TYPE_DISK:
				var info windows.ByHandleFileInformation
				if err := windows.GetFileInformationByHandle(duplicate, &info); err != nil {
					return err
				}
				if info.VolumeSerialNumber == canary.VolumeSerialNumber && info.FileIndexHigh == canary.FileIndexHigh && info.FileIndexLow == canary.FileIndexLow {
					return fmt.Errorf("inherited Core canary: native volume=%08x file=%08x%08x granted=%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, entry.GrantedAccess)
				}
			case windows.FILE_TYPE_PIPE:
				var flags, outSize, inSize, instances, serverPID, clientPID uint32
				if err := windows.GetNamedPipeInfo(duplicate, &flags, &outSize, &inSize, &instances); err != nil {
					return err
				}
				if err := windows.GetNamedPipeServerProcessId(duplicate, &serverPID); err != nil {
					return err
				}
				if err := windows.GetNamedPipeClientProcessId(duplicate, &clientPID); err != nil {
					return err
				}
				if serverPID == windows.GetCurrentProcessId() && flags&1 != 0 { // PIPE_SERVER_END
					return fmt.Errorf("inherited host pipe server: server=%d client=%d", serverPID, clientPID)
				}
				if standard && (flags&1 != 0 || serverPID != windows.GetCurrentProcessId() || clientPID != windows.GetCurrentProcessId()) {
					return fmt.Errorf("standard handle is not the owned pipe client: flags=%d server=%d client=%d", flags, serverPID, clientPID)
				}
			}
			if standard && kind != windows.FILE_TYPE_PIPE {
				return fmt.Errorf("standard handle has native file type %d", kind)
			}
		} else if standard {
			return fmt.Errorf("standard handle has native object type %d", entry.ObjectType)
		}
		if entry.Attributes&windows.OBJ_INHERIT != 0 && !standard {
			return fmt.Errorf("unlisted inheritable native object: %+v", entry)
		}
		return nil
	}
	for _, entry := range entries {
		if err := inspect(entry); err != nil {
			return len(entries), err
		}
	}
	return len(entries), nil
}

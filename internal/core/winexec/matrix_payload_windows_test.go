package winexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

type memoryProbe struct {
	SmallBytes uint64
	LargeBytes uint64
	NativeCode uint32
}

func payloadMemory(t *testing.T) {
	t.Helper()
	const smallBytes, largeBytes = 1 << 20, 512 << 20
	small, err := windows.VirtualAlloc(0, smallBytes, windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_READWRITE)
	if err != nil {
		t.Fatalf("positive native allocation: %v", err)
	}
	var page, readback [4096]byte
	for i := range page {
		page[i] = byte(i) ^ 0x5a
	}
	for offset := uintptr(0); offset < smallBytes; offset += uintptr(len(page)) {
		var written uintptr
		if err := windows.WriteProcessMemory(windows.CurrentProcess(), small+offset, &page[0], uintptr(len(page)), &written); err != nil || written != uintptr(len(page)) {
			t.Fatalf("positive native page write at %d: bytes=%d error=%v", offset, written, err)
		}
	}
	for offset := uintptr(0); offset < smallBytes; offset += uintptr(len(readback)) {
		var read uintptr
		if err := windows.ReadProcessMemory(windows.CurrentProcess(), small+offset, &readback[0], uintptr(len(readback)), &read); err != nil || read != uintptr(len(readback)) {
			t.Fatalf("positive native page read at %d: bytes=%d error=%v", offset, read, err)
		}
		if readback != page {
			t.Fatalf("positive native allocation did not retain page %d", offset)
		}
	}
	if err := windows.VirtualFree(small, 0, windows.MEM_RELEASE); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "memory-ready")
	// One finite request, not allocation until the host is exhausted. The
	// parent configures a Job commit quota below this request's size.
	large, err := windows.VirtualAlloc(0, largeBytes, windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_READWRITE)
	if err == nil {
		freeErr := windows.VirtualFree(large, 0, windows.MEM_RELEASE)
		t.Fatalf("over-quota native allocation succeeded: release=%v", freeErr)
	}
	var code syscall.Errno
	if !errors.As(err, &code) {
		t.Fatalf("allocation failure has no native code: %v", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(memoryProbe{SmallBytes: smallBytes, LargeBytes: largeBytes, NativeCode: uint32(code)}); err != nil {
		t.Fatal(err)
	}
	windows.ExitProcess(0)
}

type peerProbe struct {
	OwnNonce   string
	ReadCodes  [2]uint32
	WriteCodes [2]uint32
}

func payloadFileDenial(t *testing.T, path string) (uint32, uint32) {
	t.Helper()
	_, readErr := os.ReadFile(path)
	var readCode syscall.Errno
	if !errors.As(readErr, &readCode) {
		t.Fatalf("protected read had no denial code: path=%q error=%v", path, readErr)
	}
	writeErr := os.WriteFile(path, []byte("changed-by-payload"), 0o600)
	var writeCode syscall.Errno
	if !errors.As(writeErr, &writeCode) {
		t.Fatalf("protected write had no denial code: path=%q error=%v", path, writeErr)
	}
	return uint32(readCode), uint32(writeCode)
}

func payloadPeer(t *testing.T, args []string) {
	t.Helper()
	if len(args) != 1 {
		t.Fatal("missing own nonce")
	}
	own, err := os.ReadFile("nonce")
	if err != nil || string(own) != args[0] {
		t.Fatalf("own authorized input: %q %v", own, err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("DWARF_WORK"), "peer-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var targets [2]string
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatal(err)
	}
	probe := peerProbe{OwnNonce: string(own)}
	for i, path := range targets {
		probe.ReadCodes[i], probe.WriteCodes[i] = payloadFileDenial(t, path)
	}
	if err := json.NewEncoder(os.Stdout).Encode(probe); err != nil {
		t.Fatal(err)
	}
	windows.ExitProcess(0)
}

func payloadInheritedHandles(t *testing.T) {
	t.Helper()
	stdin, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
	if err != nil || string(stdin) != "handle-positive-stdin" {
		t.Fatalf("explicit stdin was not usable: %q %v", stdin, err)
	}
	fmt.Fprintln(os.Stdout, "explicit-handles-only")
	fmt.Fprintln(os.Stderr, "handle-positive-stderr")
	windows.ExitProcess(0)
}

type targetDenial struct {
	ReadCode, WriteCode uint32
}

type protectedTargets struct {
	Files, Directories []string
}

type protectedProbe struct {
	OwnNonce    string
	Files       []targetDenial
	Directories []uint32
	PipeCode    uint32
}

func payloadProtectedTargets(t *testing.T, args []string) {
	t.Helper()
	if len(args) != 1 {
		t.Fatal("missing real owner pipe")
	}
	if err := json.NewEncoder(os.Stdout).Encode(payloadProtectedProbe(t, args[0])); err != nil {
		t.Fatal(err)
	}
	windows.ExitProcess(0)
}

func payloadProtectedProbe(t *testing.T, pipe string) protectedProbe {
	t.Helper()
	nonce, err := os.ReadFile("nonce")
	if err != nil || string(nonce) != "authorized-targets-nonce" {
		t.Fatalf("authorized input was not readable: %q %v", nonce, err)
	}
	data, err := os.ReadFile("targets.json")
	if err != nil {
		t.Fatal(err)
	}
	var targets protectedTargets
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatal(err)
	}
	probe := protectedProbe{OwnNonce: string(nonce)}
	for _, path := range targets.Files {
		read, write := payloadFileDenial(t, path)
		probe.Files = append(probe.Files, targetDenial{read, write})
	}
	for _, path := range targets.Directories {
		_, err := os.ReadDir(path)
		var code syscall.Errno
		if !errors.As(err, &code) {
			t.Fatalf("protected enumeration had no denial code: path=%q error=%v", path, err)
		}
		probe.Directories = append(probe.Directories, uint32(code))
	}
	name, err := windows.UTF16PtrFromString(pipe)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err == nil {
		closeErr := windows.CloseHandle(handle)
		t.Fatalf("connected to the real Core-only owner pipe: close=%v", closeErr)
	}
	var code syscall.Errno
	if !errors.As(err, &code) {
		t.Fatalf("owner pipe denial lacks native code: %v", err)
	}
	probe.PipeCode = uint32(code)
	return probe
}

func payloadLinks(t *testing.T) {
	t.Helper()
	nonce, err := os.ReadFile("nonce")
	if err != nil || string(nonce) != "authorized-link-nonce" {
		t.Fatalf("authorized input through its own root: %q %v", nonce, err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("DWARF_WORK"), "links.json"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	var proof []targetDenial
	for _, path := range paths {
		read, write := payloadFileDenial(t, path)
		proof = append(proof, targetDenial{read, write})
	}
	if err := json.NewEncoder(os.Stdout).Encode(proof); err != nil {
		t.Fatal(err)
	}
	windows.ExitProcess(0)
}

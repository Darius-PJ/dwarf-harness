package winexec

import (
	"fmt"
	"io/fs"
	"math"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"
)

// InputFile is an authorized regular file copied into a fresh sealed input tree.
// The caller must not mutate Data until Run returns.
type InputFile struct {
	Path string
	Data []byte
}

// Limits has no unlimited defaults. UserCPU is aggregate user-mode CPU, in exact
// multiples of 100ns. JobCommitBytes bounds native Job commitment, not RAM or disk.
// InputBytes charges UTF-8 program, arguments and file paths, file data and stdin.
// OutputBytes is the shared stdout/stderr retention limit. StopGrace independently
// bounds positive closure, pipe completion and cleanup after stopping.
type Limits struct {
	Wall           time.Duration
	UserCPU        time.Duration
	CPUPercent     uint32
	JobCommitBytes uint64
	Processes      uint32
	InputBytes     uint64
	OutputBytes    uint64
	StopGrace      time.Duration
}

// Request contains immutable authorized bytes, never host source paths or handles.
// Program exactly names one .exe in Files. Deadline and every limit are required.
type Request struct {
	Program  string
	Args     []string
	Files    []InputFile
	Stdin    []byte
	Limits   Limits
	Deadline time.Time
}

// Usage retains exact native counters; memory peaks are the Job's reported
// counters, not a physical-RAM measurement. CPU counters are in 100ns units.
type Usage struct {
	User100ns, Kernel100ns                     uint64
	PeakJobMemoryBytes, PeakProcessMemoryBytes uint64
	ReadBytes, WriteBytes, OtherBytes          uint64
	TotalProcesses                             uint32
}

type RunState uint8

const (
	NotAdmitted RunState = iota
	Closed
	Quarantined
)

type Outcome uint8

const (
	NotStarted Outcome = iota
	Exited
	Canceled
	TimedOut
	OutputExceeded
	Shutdown
	NativeFailure
)

type OutputState uint8

const (
	Unpublishable OutputState = iota
	CompleteSealed
)

// Receipt contains host-private diagnostics, not validation or publication
// authority. Closed describes native closure; ResourcesReusable additionally
// requires durable proof and cleanup. CompleteSealed describes bounded complete
// stdio, independently of a nonzero native exit. Scratch/profile storage has no
// hard disk quota and is never exported as an artifact.
// CompleteSealed does not claim that the child consumed every stdin byte.
type Receipt struct {
	RootID, RunID                     uuid.UUID
	ProfileSID                        string
	State                             RunState
	Outcome                           Outcome
	ExitCode                          uint32
	ExitCodeKnown                     bool
	RootExited, TreeExited            bool
	ResourcesReusable                 bool
	OutputState                       OutputState
	Stdout, Stderr                    []byte
	InputBytes                        uint64
	CreatedUTC, ResumedUTC, ClosedUTC time.Time
	Setup, Execution, Stop            time.Duration
	Usage                             Usage
	UsageKnown                        bool
}

func validateRequest(r Request) (uint64, error) {
	l := r.Limits
	if l.Wall <= 0 || l.UserCPU <= 0 || l.UserCPU%(100*time.Nanosecond) != 0 || l.CPUPercent < 1 || l.CPUPercent > 100 || l.JobCommitBytes == 0 || l.JobCommitBytes > uint64(^uintptr(0)) || l.Processes == 0 || l.InputBytes == 0 || l.OutputBytes == 0 || l.OutputBytes > uint64(math.MaxInt) || l.StopGrace <= 0 || r.Deadline.IsZero() {
		return 0, fmt.Errorf("%w: finite limits and deadline required", ErrInvalid)
	}
	validText := func(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
	var total uint64
	charge := func(n uint64) bool {
		if n > math.MaxUint64-total {
			return false
		}
		total += n
		return total <= l.InputBytes
	}
	if !validInputPath(r.Program) || !strings.HasSuffix(strings.ToLower(r.Program), ".exe") || !charge(uint64(len(r.Program))+1) || !charge(uint64(len(r.Stdin))) {
		return 0, ErrInvalid
	}
	for _, a := range r.Args {
		if !validText(a) || !charge(uint64(len(a))+1) {
			return 0, ErrInvalid
		}
	}
	programs := 0
	for _, f := range r.Files {
		if !validInputPath(f.Path) || !charge(uint64(len(f.Path))+1) || !charge(uint64(len(f.Data))) {
			return 0, ErrInvalid
		}
		if f.Path == r.Program {
			programs++
		}
	}
	if programs != 1 {
		return 0, fmt.Errorf("%w: Program must name exactly one input", ErrInvalid)
	}
	return total, nil
}

func validInputPath(path string) bool {
	if path == "." || !utf8.ValidString(path) || !fs.ValidPath(path) || strings.ContainsAny(path, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

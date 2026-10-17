package winexec

import (
	"errors"
	"math"
	"testing"
	"time"
)

func finiteRequest() Request {
	return Request{Program: "p.exe", Args: []string{"é", ""}, Files: []InputFile{{Path: "p.exe", Data: []byte{1, 2, 3}}}, Stdin: []byte{4, 5}, Deadline: time.Now().Add(time.Second), Limits: Limits{Wall: time.Second, UserCPU: 100 * time.Nanosecond, CPUPercent: 1, JobCommitBytes: 1, Processes: 1, InputBytes: 100, OutputBytes: 1, StopGrace: time.Second}}
}
func TestInputChargeIsExactUTF8Budget(t *testing.T) {
	t.Parallel()
	r := finiteRequest()
	// stdin(2), program(5+1), arguments(2+1 and 0+1), file(5+1+3).
	r.Limits.InputBytes = 21
	charge, err := validateRequest(r)
	if err != nil || charge != 21 {
		t.Fatalf("exact budget: %d %v", charge, err)
	}
	r.Limits.InputBytes--
	if _, err := validateRequest(r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-budget request accepted: %v", err)
	}
}
func TestLimitsNeverDefaultToUnlimited(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*Request){
		"wall":                 func(r *Request) { r.Limits.Wall = 0 },
		"cpu-time":             func(r *Request) { r.Limits.UserCPU = 0 },
		"cpu-unit":             func(r *Request) { r.Limits.UserCPU = 101 * time.Nanosecond },
		"cpu-percent":          func(r *Request) { r.Limits.CPUPercent = 0 },
		"cpu-percent-overflow": func(r *Request) { r.Limits.CPUPercent = 101 },
		"commit":               func(r *Request) { r.Limits.JobCommitBytes = 0 },
		"processes":            func(r *Request) { r.Limits.Processes = 0 },
		"input":                func(r *Request) { r.Limits.InputBytes = 0 },
		"output":               func(r *Request) { r.Limits.OutputBytes = 0 },
		"output-native-width":  func(r *Request) { r.Limits.OutputBytes = uint64(math.MaxInt) + 1 },
		"stop-grace":           func(r *Request) { r.Limits.StopGrace = 0 },
		"deadline":             func(r *Request) { r.Deadline = time.Time{} },
		"missing-program":      func(r *Request) { r.Program = "other.exe" },
		"duplicate-program":    func(r *Request) { r.Files = append(r.Files, r.Files[0]) },
		"argument-nul":         func(r *Request) { r.Args = []string{"a\x00b"} },
		"argument-utf8":        func(r *Request) { r.Args = []string{"\xff"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := finiteRequest()
			mutate(&r)
			if _, err := validateRequest(r); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid finite profile accepted: %v", err)
			}
		})
	}
}

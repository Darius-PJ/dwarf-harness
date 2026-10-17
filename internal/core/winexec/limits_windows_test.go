package winexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutputBudgetIsSharedAndBounded(t *testing.T) {
	t.Parallel()
	for _, overflow := range []bool{false, true} {
		name := "exact"
		if overflow {
			name = "overflow"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := testExecutor(t)
			amount := "128"
			if overflow {
				amount = "192"
			}
			request := testRequest(t, "output", amount)
			request.Limits.OutputBytes = 256
			r, err := e.Run(context.Background(), request)
			if !overflow {
				assertReusable(t, r, err)
				if string(r.Stdout) != strings.Repeat("o", 128) || string(r.Stderr) != strings.Repeat("e", 128) {
					t.Fatalf("exact output: stdout=%q stderr=%q", r.Stdout, r.Stderr)
				}
				return
			}
			if !errors.Is(err, ErrOutputLimit) || r.Outcome != OutputExceeded || r.State != Closed || !r.TreeExited || !r.ResourcesReusable || r.OutputState != Unpublishable || len(r.Stdout)+len(r.Stderr) != 256 {
				t.Fatalf("shared overflow: %+v %v", r, err)
			}
		})
	}
}
func TestWallDeadlineStopsWholeTree(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	observed := false
	var liveErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase == "root_exited" {
			observed = true
			liveErr = liveHeldDescendant(r)
		}
	}
	request := testRequest(t, "descendant-held")
	request.Limits.Wall = 4 * time.Second
	// The child tree never reads stdin; cancellation must retire this pending
	// overlapped write as well as both concurrently drained output operations.
	request.Stdin = make([]byte, 128*1024)
	r, err := e.Run(context.Background(), request)
	if !observed || liveErr != nil {
		t.Fatalf("held native tree was not observed: observed=%t error=%v", observed, liveErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) || r.Outcome != TimedOut || r.State != Closed || !r.RootExited || !r.TreeExited || !r.ResourcesReusable || r.OutputState != Unpublishable || !r.UsageKnown || !strings.Contains(string(r.Stdout), "held-ready\n") {
		t.Fatalf("wall stopped tree: %+v %v", r, err)
	}
}
func TestRejectedAdmissionCreatesNothing(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	cases := map[string]func(*Request){
		"device":           func(r *Request) { r.Files = append(r.Files, InputFile{Path: "NUL.txt"}) },
		"traversal":        func(r *Request) { r.Files = append(r.Files, InputFile{Path: "../escape"}) },
		"alternate-stream": func(r *Request) { r.Files = append(r.Files, InputFile{Path: "file:stream"}) },
		"backslash":        func(r *Request) { r.Files = append(r.Files, InputFile{Path: `dir\file`}) },
		"trailing-dot":     func(r *Request) { r.Files = append(r.Files, InputFile{Path: "dir./file"}) },
		"command-line":     func(r *Request) { r.Args = append(r.Args, strings.Repeat("界", 32768)) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := testRequest(t, "held")
			change(&r)
			receipt, err := e.Run(context.Background(), r)
			if !errors.Is(err, ErrInvalid) || receipt.State != NotAdmitted || receipt.RunID != (Receipt{}).RunID || !receipt.CreatedUTC.IsZero() {
				t.Fatalf("invalid admission: %+v %v", receipt, err)
			}
		})
	}
}
func TestExpiredAndCanceledAdmissionHasNoEffects(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	request := testRequest(t, "held")
	request.Deadline = time.Now().Add(-time.Second)
	r, err := e.Run(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) || r.State != NotAdmitted || r.Outcome != TimedOut || r.RunID != (Receipt{}).RunID {
		t.Fatalf("expired admission: %+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = e.Run(ctx, testRequest(t, "held"))
	if !errors.Is(err, context.Canceled) || r.State != NotAdmitted || r.Outcome != Canceled || r.RunID != (Receipt{}).RunID {
		t.Fatalf("canceled admission: %+v %v", r, err)
	}
	entries, err := os.ReadDir(filepath.Join(e.root, "runs"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("admission created run resources: %v %v", entries, err)
	}
}

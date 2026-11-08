package providers

// Ticket 145's temporary hosted stress diagnostic. It never lands: it runs only
// on throwaway build/145-diag-* branches, so that a hosted job's signed-out
// annotations show how often TestPaidHTTP1OneShot fails under repetition.
//
// The 1,000 iterations run in a child copy of this test binary, and only the
// one summary subtest below can fail in this process. The workflow annotates at
// most ten failing test lines per job, so failures of the iterations themselves
// would crowd the summary out.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const t145Iterations = 1000

// Counters for the two instrumented failure sites in transport_test.go on the
// before-branch. On the after-branch nothing increments them.
var (
	t145Nil400, t145Drain         atomic.Int64
	t145DialsMax, t145RequestsMax atomic.Int64
	t145Leaves                    = struct {
		sync.Mutex
		m map[string]int
	}{m: map[string]int{}}
)

type t145Result struct {
	Failed      int            `json:"failed"`
	Unexplained int            `json:"unexplained"`
	Nil400      int64          `json:"nil400"`
	Drain       int64          `json:"drain"`
	DialsMax    int64          `json:"dialsMax"`
	RequestsMax int64          `json:"requestsMax"`
	Leaves      map[string]int `json:"leaves"`
}

// TestT145StressChild runs only in the child the summary test starts. Each
// iteration wraps TestPaidHTTP1OneShot in one more t.Run, so that iteration's
// parallel leaves all finish before the next iteration begins.
func TestT145StressChild(t *testing.T) {
	out := os.Getenv("DWARF_T145_STRESS_OUT")
	if out == "" {
		t.Skip("runs only as the child of TestT145HostedStress")
	}
	var r t145Result
	for i := range t145Iterations {
		before := t145Nil400.Load() + t145Drain.Load()
		passed := t.Run(fmt.Sprintf("iter_%04d", i), func(t *testing.T) {
			t.Run("faults", TestPaidHTTP1OneShot)
		})
		if !passed {
			r.Failed++
			if t145Nil400.Load()+t145Drain.Load() == before {
				r.Unexplained++
			}
		}
	}
	r.Nil400, r.Drain = t145Nil400.Load(), t145Drain.Load()
	r.DialsMax, r.RequestsMax = t145DialsMax.Load(), t145RequestsMax.Load()
	t145Leaves.Lock()
	r.Leaves = t145Leaves.m
	t145Leaves.Unlock()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestT145HostedStress starts the child and reports its result through one
// subtest name, which fails if any iteration failed or the child gave no result.
// The name also counts the child's failure messages by kind, so a failure the
// two instrumented sites don't explain still shows what it was.
func TestT145HostedStress(t *testing.T) {
	out := filepath.Join(t.TempDir(), "result.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestT145StressChild$", "-test.count=1", "-test.timeout=20m")
	cmd.Env = append(os.Environ(), "DWARF_T145_STRESS_OUT="+out)
	output, runErr := cmd.CombinedOutput()
	exit := 0
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exit = exitErr.ExitCode()
	} else if runErr != nil {
		exit = -1
	}
	var r t145Result
	raw, readErr := os.ReadFile(out)
	if readErr == nil {
		readErr = json.Unmarshal(raw, &r)
	}
	leaves := make([]string, 0, len(r.Leaves))
	for leaf, n := range r.Leaves {
		leaves = append(leaves, fmt.Sprintf("%s-%d", leaf, n))
	}
	sort.Strings(leaves)
	name := fmt.Sprintf("race%d_failed_%d_of_%d_nil400_%d_drain_%d_unexplained_%d_dialsmax_%d_requestsmax_%d_exit_%d_result_%t_leaves_%s_msgs_%s",
		t145Race, r.Failed, t145Iterations, r.Nil400, r.Drain, r.Unexplained, r.DialsMax, r.RequestsMax, exit, readErr == nil, strings.Join(leaves, "_"), t145Messages(string(output)))
	t.Run(name, func(t *testing.T) {
		if readErr != nil || r.Failed > 0 || (exit != 0 && exit != 1) {
			t.Fail()
		}
	})
}

// t145Messages counts the child's transport_test.go failure lines by kind.
func t145Messages(output string) string {
	kinds := []struct{ prefix, label string }{
		{"faulted attempt unexpectedly succeeded", "nil400"},
		{"response drain failed", "drain"},
		{"fault did not hit warm connection", "warm"},
		{"redial after fault", "redial"},
		{"server terminal=", "counts"},
		{"warm exchange did not use one real connection", "warmx"},
		{"server negotiated HTTP/2", "h2"},
		{"unsealed idempotency header", "idem"},
		{"hijack failed", "hijack"},
		{"warm connection not pooled", "notpooled"},
		{"one-shot counts changed", "oneshotcounts"},
		{"faulted attempt returned a nil error", "nilerr"},
	}
	counts := map[string]int{}
	for _, line := range strings.Split(output, "\n") {
		_, text, found := strings.Cut(line, "transport_test.go:")
		if !found {
			continue
		}
		if _, msg, ok := strings.Cut(text, ": "); ok {
			text = msg
		}
		label := "other"
		for _, k := range kinds {
			if strings.HasPrefix(text, k.prefix) {
				label = k.label
				break
			}
		}
		counts[label]++
	}
	if strings.Contains(output, "WARNING: DATA RACE") {
		counts["datarace"]++
	}
	parts := make([]string, 0, len(counts))
	for label, n := range counts {
		parts = append(parts, fmt.Sprintf("%s-%d", label, n))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "_")
}

//go:build windows

package gitprocess

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
	"uuid"

	"golang.org/x/sys/windows"
)

// Throwaway ticket 16 diagnostic: deliberately fails; must never land in main.
// Counts, not elapsed time, bound the loops: five fresh Owners, 32 pairs each,
// and 200 fence cycles. The deadline is only an overall failure safety net.
// As in diag-2, no warmup is discarded, pair order alternates, medians average
// the middle pair, and p95 uses nearest rank. QPC avoids Windows clock steps.
func TestZZT16F3Diag(t *testing.T) {
	if t16F3DiagRaceBuild {
		t.Skip("hosted cost diagnostic reports only in the plain tests step")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 165*time.Second)
	defer cancel()
	clock := t16F3DiagNewClock(t)
	samples := make(map[string][]int64)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempVolume := t16F3DiagVolume(t, os.TempDir())
	checkoutVolume := t16F3DiagVolume(t, checkout)
	program, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	program, err = filepath.Abs(program)
	if err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	var expected []byte
	for batch := range 5 {
		owner, report, err := Open(ctx, filepath.Join(root, fmt.Sprintf("owner-%d", batch)), Options{RequireTree: true, MaxConcurrent: 1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		})
		if len(report.Pending) != 0 || report.Reservations != 0 {
			t.Fatalf("fresh Owner contains recovery work: %+v", report)
		}
		// These callbacks are synchronous and per Owner. The metadata seam
		// takes no Owner lock and calls no Owner API, even under reservationMu.
		starts := make(map[string]int64)
		owner.metadataHook = func(operation, _ string, success bool) error {
			at := clock.now()
			if !success {
				starts[operation] = at
			} else {
				samples[operation] = append(samples[operation], at-starts[operation])
				delete(starts, operation)
			}
			return nil
		}
		var ownerStart int64
		owner.observe = func(phase string, _ fence) {
			if phase == "reservation_published" {
				samples["publication"] = append(samples["publication"], clock.now()-ownerStart)
			}
		}
		for index := range slotCount {
			owner.reservationMu.Lock()
			actual := 0
			if owner.currentBatch != nil {
				actual = owner.currentBatch.next
			}
			owner.reservationMu.Unlock()
			if actual != index {
				t.Fatalf("unexpected slot index: got %d want %d", actual, index)
			}
			var elapsed [2]int64
			for j := range 2 {
				mode := (batch*slotCount + index + j) % 2
				var stdout, stderr bytes.Buffer
				if mode == 0 {
					at := clock.now()
					cmd := exec.CommandContext(ctx, program, "version")
					cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = root, env, &stdout, &stderr
					cmd.WaitDelay = 5 * time.Second
					err = cmd.Run()
					elapsed[mode] = clock.now() - at
				} else {
					ownerStart = clock.now()
					result, runErr := owner.Run(ctx, Spec{Program: program, Args: []string{"version"}, Dir: root, Env: env, Stdout: &stdout, Stderr: &stderr})
					elapsed[mode] = clock.now() - ownerStart
					err = runErr
					if err == nil && (result.ExitCode != 0 || !result.RootExited || !result.TreeClosed || !result.IOJoined) {
						t.Fatalf("incomplete Owner result: %+v", result)
					}
				}
				if err != nil {
					t.Fatalf("batch %d slot %d mode %d: %v stderr=%q", batch, index, mode, err, stderr.String())
				}
				if expected == nil {
					expected = bytes.Clone(stdout.Bytes())
				}
				if !bytes.HasPrefix(expected, []byte("git version ")) || !bytes.Equal(stdout.Bytes(), expected) || stderr.Len() != 0 {
					t.Fatalf("child output mismatch: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			}
			delta := elapsed[1] - elapsed[0]
			samples["paired"] = append(samples["paired"], delta)
			group := "later"
			if actual == 0 {
				group = "first"
			}
			samples[group] = append(samples[group], delta)
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		if len(starts) != 0 {
			t.Fatalf("metadata operation did not complete: %v", starts)
		}
	}
	control := filepath.Join(root, "control")
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	session, err := currentSession()
	if err != nil {
		t.Fatal(err)
	}
	rootID := uuid.NewV7()
	for range 200 {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		at := clock.now()
		id := uuid.NewV7()
		f := fence{Format: fenceFormat, Version: 1, RootID: rootID, RunID: id, JobName: jobName(rootID, id), Session: session, State: "launch_pending"}
		path := filepath.Join(control, id.String()+".json")
		// Same cycle as fd7fa24: UUID/JSON, pending Lstat, create/write/Sync/
		// close, write-through rename plus SyncDir, removal without a flush.
		if err := writeMarker(path, f); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		samples["fence"] = append(samples["fence"], clock.now()-at)
	}
	if clock.err != nil {
		t.Fatal(clock.err)
	}
	for key, count := range map[string]int{"paired": 160, "first": 5, "later": 155, "publication": 5, "frame_sync": 160, "writer_open": 5, "writer_close": 5, "footer_write": 155, "remove": 5, "fence": 200} {
		if len(samples[key]) != count {
			t.Fatalf("incomplete %s sample: got %d want %d", key, len(samples[key]), count)
		}
	}
	t.Logf("T16F3_DIAG QPC_hz=%d; units=rounded_us; negatives=negN; median=middle_pair; p95=nearest_rank; no_warmup_discarded", clock.frequency)
	t.Log("Publication spans Owner.Run entry to reservation_published: IDs/session/encoding, temp open/write/Sync/close, rename, held-writer open and validation. Other metadata timings bracket the real I/O seam calls. Owner.Open/Close are outside paired Run timing; exhausted-batch retirement remains inside Run. Owner timings include observer overhead.")
	emit := func(name string) {
		if len(name) > 200 {
			t.Fatalf("diagnostic annotation too long: %s", name)
		}
		t.Run(name, func(t *testing.T) { t.Error("intentional hosted cost figure; timings are microseconds") })
	}
	metric := func(label, key string, tail bool) string {
		values := slices.Clone(samples[key])
		slices.Sort(values)
		n := len(values)
		micros := func(ticks float64) string {
			value := int64(math.Round(ticks * 1e6 / float64(clock.frequency)))
			if value < 0 {
				return "neg" + strconv.FormatInt(-value, 10)
			}
			return strconv.FormatInt(value, 10)
		}
		name := fmt.Sprintf("%s_n%d_p50_%s", label, n, micros((float64(values[(n-1)/2])+float64(values[n/2]))/2))
		if tail {
			name += "_p95_" + micros(float64(values[(95*n+99)/100-1]))
		}
		return name
	}
	emit(metric("paired", "paired", true))
	emit(metric("first", "first", true) + "_" + metric("later", "later", true))
	emit(metric("frameSync", "frame_sync", true))
	emit(metric("publication", "publication", false))
	emit(metric("writerOpen", "writer_open", false) + "_" + metric("writerClose", "writer_close", false) + "_" + metric("footer", "footer_write", false) + "_" + metric("delete", "remove", false))
	emit(metric("fenceCycle", "fence", true))
	environment := fmt.Sprintf("image_%s_ver_%s_cpu%d_gmp%d_temp_%s_checkout_%s", t16F3DiagName(os.Getenv("ImageOS")), t16F3DiagName(os.Getenv("ImageVersion")), runtime.NumCPU(), runtime.GOMAXPROCS(0), tempVolume, checkoutVolume)
	tools := "git_" + t16F3DiagName(strings.TrimPrefix(strings.TrimSpace(string(expected)), "git version ")) + "_sha_" + t16F3DiagName(os.Getenv("GITHUB_SHA"))
	if len(environment)+1+len(tools) <= 200 {
		emit(environment + "_" + tools)
	} else {
		emit(environment)
		emit(tools)
	}
}

type t16F3DiagClock struct {
	counter   *windows.LazyProc
	frequency int64
	err       error
}

func t16F3DiagNewClock(t *testing.T) *t16F3DiagClock {
	t.Helper()
	dll := windows.NewLazySystemDLL("kernel32.dll")
	clock := &t16F3DiagClock{counter: dll.NewProc("QueryPerformanceCounter")}
	frequency := dll.NewProc("QueryPerformanceFrequency")
	if err := clock.counter.Find(); err != nil {
		t.Fatal(err)
	}
	if err := frequency.Find(); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := frequency.Call(uintptr(unsafe.Pointer(&clock.frequency))); ok == 0 || clock.frequency <= 0 {
		t.Fatalf("QueryPerformanceFrequency: %v", err)
	}
	_ = clock.now()
	if clock.err != nil {
		t.Fatal(clock.err)
	}
	return clock
}

func (c *t16F3DiagClock) now() int64 {
	var value int64
	if ok, _, err := c.counter.Call(uintptr(unsafe.Pointer(&value))); ok == 0 {
		// Report after Run rather than terminating from a hook under a lock.
		c.err = fmt.Errorf("QueryPerformanceCounter: %w", err)
	}
	return value
}

func t16F3DiagVolume(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := windows.UTF16PtrFromString(resolved)
	if err != nil {
		t.Fatal(err)
	}
	mount := make([]uint16, 32768)
	if err := windows.GetVolumePathName(wide, &mount[0], uint32(len(mount))); err != nil {
		t.Fatal(err)
	}
	fs := make([]uint16, 64)
	if err := windows.GetVolumeInformation(&mount[0], nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		t.Fatal(err)
	}
	return t16F3DiagName(strings.TrimSuffix(filepath.VolumeName(windows.UTF16ToString(mount)), ":")) + "_" + t16F3DiagName(windows.UTF16ToString(fs))
}

func t16F3DiagName(value string) string {
	if value == "" {
		return "unset"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, value)
}

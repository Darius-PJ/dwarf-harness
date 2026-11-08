//go:build windows

package gitprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/durable"
	"golang.org/x/sys/windows"
)

// This deliberately failing, throwaway diagnostic must not land in main.
// All clocks and observers belong to this serial test's own files and Owners.
// Medians average the middle pair; p95 uses the nearest-rank definition.
// A 55s admission deadline reserves the Owner's existing 5s shutdown grace.
// Shares are 15s/fence location, 10s/Owner path (including Open), and 5s/direct.
// These are diagnostic admission budgets, not product guards. In-flight calls
// finish normally; no Sync, MoveFileEx, or child is interrupted for timing.
// Incomplete counts and the exhausted share are reported without extrapolation.
func TestHostedCostDiagnostic(t *testing.T) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	fenceKeys := []string{"create", "write", "file_sync", "close", "rename", "dir_sync", "remove", "total"}
	ownerKeys := []string{"launch_pending", "suspended", "resumed", "root_exited", "closed", "return", "total"}
	type location struct {
		name, path                       string
		fences, children, overhead       hostedCostSamples
		owner                            *Owner
		fenceSpent, ownerSpent, openTime time.Duration
	}
	var locations []*location
	direct := hostedCostSamples{}
	var directSpent time.Duration
	exhausted := map[string]bool{}
	environment := map[string]any{
		"NUMBER_OF_PROCESSORS": os.Getenv("NUMBER_OF_PROCESSORS"),
		"GOMAXPROCS":           runtime.GOMAXPROCS(0),
	}
	var tempPath, volume, dStatus string
	var expected []byte
	measureErr := func() error {
		var err error
		tempPath, err = filepath.EvalSymlinks(os.TempDir())
		if err != nil {
			return err
		}
		tempPath, err = filepath.Abs(tempPath)
		if err != nil {
			return err
		}
		volume, err = hostedCostVolume(tempPath)
		if err != nil {
			return err
		}
		environment["TEMP_resolved"], environment["TEMP_volume"] = tempPath, volume
		addLocation := func(name, parent string) error {
			path, err := os.MkdirTemp(parent, "dwarf-hosted-cost-")
			if err != nil {
				return err
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(path); err != nil {
					t.Error(err)
				}
			})
			if err := os.Mkdir(filepath.Join(path, "runs"), 0o700); err != nil {
				return err
			}
			probe := filepath.Join(path, "write-check")
			if err := os.WriteFile(probe, []byte("writable\n"), 0o600); err != nil {
				return err
			}
			if err := os.Remove(probe); err != nil {
				return err
			}
			locations = append(locations, &location{name: name, path: path, fences: hostedCostSamples{}, children: hostedCostSamples{}, overhead: hostedCostSamples{}})
			return nil
		}
		if err := addLocation("T", tempPath); err != nil {
			return err
		}
		info, dErr := os.Stat(`D:\`)
		switch {
		case errors.Is(dErr, os.ErrNotExist):
			dStatus = "absent"
		case dErr != nil:
			dStatus = "stat-error"
			environment["D_error"] = dErr.Error()
		case !info.IsDir():
			dStatus = "not-directory"
		default:
			if err := addLocation("D", `D:\`); err != nil {
				dStatus = "not-writable"
				environment["D_error"] = err.Error()
			} else {
				dStatus = "writable"
			}
		}
		environment["D_status"] = dStatus
		rootID := uuid.NewV7()
		session, err := currentSession()
		if err != nil {
			return err
		}
		// Rotate location order; each cycle uses a fresh flat run-file name.
		for i := range 200 {
			for j := range locations {
				loc := locations[(i+j)%len(locations)]
				key := loc.name + ".fence"
				remaining := 15*time.Second - loc.fenceSpent
				if exhausted[key] || ctx.Err() != nil || remaining <= 0 {
					exhausted[key] = true
					continue
				}
				share, stop := context.WithTimeout(ctx, remaining)
				at := time.Now()
				err := hostedCostFence(share, filepath.Join(loc.path, "runs"), rootID, session, loc.fences)
				loc.fenceSpent += time.Since(at)
				stop()
				if errors.Is(err, context.DeadlineExceeded) {
					exhausted[key] = true
				} else if err != nil {
					return fmt.Errorf("%s fence %d: %w", loc.name, i, err)
				}
			}
		}
		program, err := exec.LookPath("git")
		if err != nil {
			return err
		}
		program, err = filepath.Abs(program)
		if err != nil {
			return err
		}
		environment["git"] = program
		for _, loc := range locations {
			if ctx.Err() != nil {
				exhausted[loc.name+".owner"] = true
				continue
			}
			at := time.Now()
			o, report, err := Open(ctx, filepath.Join(loc.path, "owner"), Options{RequireTree: true, MaxConcurrent: 1})
			loc.openTime = time.Since(at)
			loc.ownerSpent += loc.openTime
			if err != nil {
				return err
			}
			t.Cleanup(func() {
				if err := o.Close(); err != nil {
					t.Error(err)
				}
			})
			if len(report.Pending) != 0 {
				return fmt.Errorf("new %s Owner has pending fences", loc.name)
			}
			loc.owner = o
		}
		// Target 100 measured children per path; no warmup is discarded.
		// Direct and both Owner locations rotate within each paired round.
		// Only the Owner root changes on D:; every child has the same TEMP cwd.
		for i := range 100 {
			elapsed := make([]time.Duration, len(locations)+1)
			measured := make([]bool, len(elapsed))
			for j := range elapsed {
				mode := (i + j) % len(elapsed)
				key, used, allowance := "direct", directSpent, 5*time.Second
				if mode > 0 {
					loc := locations[mode-1]
					key, used, allowance = loc.name+".owner", loc.ownerSpent, 10*time.Second
				}
				if ctx.Err() != nil || used >= allowance || exhausted[key] {
					exhausted[key] = true
					continue
				}
				var owner *Owner
				samples := direct
				if mode > 0 {
					owner, samples = locations[mode-1].owner, locations[mode-1].children
				}
				at := time.Now()
				wall, output, err := hostedCostChild(ctx, owner, program, locations[0].path, samples)
				if mode == 0 {
					directSpent += time.Since(at)
				} else {
					locations[mode-1].ownerSpent += time.Since(at)
				}
				if errors.Is(err, context.DeadlineExceeded) {
					exhausted[key] = true
					continue
				}
				if err != nil {
					return fmt.Errorf("child mode %d round %d: %w", mode, i, err)
				}
				elapsed[mode], measured[mode] = wall, true
				if expected == nil {
					expected = bytes.Clone(output)
				}
				if !bytes.Equal(expected, output) || !bytes.HasPrefix(output, []byte("git version ")) {
					return fmt.Errorf("child mode %d round %d: version output mismatch: %q", mode, i, output)
				}
			}
			for j, loc := range locations {
				if measured[0] && measured[j+1] {
					loc.overhead["paired_delta"] = append(loc.overhead["paired_delta"], elapsed[j+1]-elapsed[0])
				}
			}
		}
		return nil
	}()
	status := "complete"
	if len(exhausted) != 0 {
		status = "budget-exhausted"
	}
	if measureErr != nil {
		status = "error"
		if errors.Is(measureErr, context.DeadlineExceeded) {
			status = "budget-exhausted"
		}
	}
	var compact []string
	raw := map[string]any{
		"environment": environment, "status": status, "elapsed_ms": float64(time.Since(started)) / float64(time.Millisecond),
		"targets": "200 fence cycles/location;100 children/path", "version": strings.TrimSpace(string(expected)),
		"fence_order": fenceKeys, "owner_order": ownerKeys,
		"phase_definition": "Owner intervals end at named observations; root_exited starts at resumed; return starts at closed. Fence total includes canonical JSON and pending Lstat. No removal flush. Phase means are nanoseconds; n is observed count. Paired delta is Owner minus same-round direct.",
		"direct":           direct.means(),
		"budget_exhausted": exhausted,
		"budget_seconds":   map[string]int{"global_admission": 55, "fence_per_location": 15, "owner_per_location_including_open": 10, "direct": 5},
		"direct_spent_ns":  directSpent,
	}
	if measureErr != nil {
		raw["error"] = measureErr.Error()
	}
	for _, loc := range locations {
		compact = append(compact, fmt.Sprintf("%s F%d[%s] O%d[%s] delta[%s]", loc.name, len(loc.fences["total"]), loc.fences.compact(fenceKeys), len(loc.children["total"]), loc.children.compact(ownerKeys), loc.overhead.compact([]string{"paired_delta"})))
		raw[loc.name] = map[string]any{"path": loc.path, "fence": loc.fences.means(), "owner": loc.children.means(), "overhead": loc.overhead.means(), "fence_spent_ns": loc.fenceSpent, "owner_spent_ns": loc.ownerSpent, "owner_open_ns": loc.openTime}
	}
	metrics := fmt.Sprintf("%s direct%d[%s] %s", status, len(direct["total"]), direct.compact([]string{"total"}), strings.Join(compact, " "))
	prefix := fmt.Sprintf("HC1 ms med/p95 cpu=%s gmp=%d temp=%q vol=%q D=%s ", os.Getenv("NUMBER_OF_PROCESSORS"), runtime.GOMAXPROCS(0), tempPath, volume, dStatus)
	summary := prefix + metrics
	if len(summary) >= 900 {
		// Preserve every statistic; the second annotation retains full metadata.
		summary = fmt.Sprintf("HC1 ms med/p95 metadata=raw gmp=%d D=%s ", runtime.GOMAXPROCS(0), dStatus) + metrics
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Fatal's Goexit runs defers: summary first, raw means second, then cleanup.
	defer t.Errorf("HC1_MEANS %s", encoded)
	if len(summary) >= 900 {
		t.Fatalf("HC1 summary overflow (%d bytes); full measurements in raw means", len(summary))
	}
	t.Fatalf("%s", summary)
}

type hostedCostSamples map[string][]time.Duration

func (s hostedCostSamples) means() map[string]any {
	out := make(map[string]any, len(s))
	for name, values := range s {
		var total time.Duration
		for _, value := range values {
			total += value
		}
		if len(values) > 0 {
			out[name] = map[string]any{"n": len(values), "mean_ns": float64(total) / float64(len(values))}
		}
	}
	return out
}

func (s hostedCostSamples) compact(names []string) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		values := slices.Clone(s[name])
		if len(values) == 0 {
			parts = append(parts, "-")
			continue
		}
		slices.Sort(values)
		n := len(values)
		median := (float64(values[(n-1)/2]) + float64(values[n/2])) / 2
		p95 := values[(95*n+99)/100-1]
		parts = append(parts, fmt.Sprintf("%.3g/%.3g", median/float64(time.Millisecond), float64(p95)/float64(time.Millisecond)))
	}
	return strings.Join(parts, ",")
}

func hostedCostVolume(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	mount := make([]uint16, 32768)
	if err := windows.GetVolumePathName(p, &mount[0], uint32(len(mount))); err != nil {
		return "", err
	}
	name := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(&mount[0], &name[0], uint32(len(name))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(mount) + "=" + windows.UTF16ToString(name), nil
}

func hostedCostWide(path string) (*uint16, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(abs, `\\?\`), strings.HasPrefix(abs, `\\.\`):
	case strings.HasPrefix(abs, `\\`):
		abs = `\\?\UNC\` + abs[2:]
	default:
		abs = `\\?\` + abs
	}
	return windows.UTF16PtrFromString(abs)
}

func hostedCostFence(ctx context.Context, dir string, rootID uuid.UUID, session uint32, samples hostedCostSamples) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start := time.Now()
	id := uuid.NewV7()
	f := fence{Format: fenceFormat, Version: 1, RootID: rootID, RunID: id, JobName: jobName(rootID, id), Session: session, State: "launch_pending"}
	payload, err := canonicalJSON(f)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id.String()+".json")
	pending := path + ".pending"
	// Match writeMarker, splitting durable.Rename into its native rename and
	// SyncDir to time them separately. File.Close precedes that rename.
	if info, err := os.Lstat(pending); err == nil && !info.Mode().IsRegular() {
		return ErrInvalid
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	phase := func(name string, fn func() error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		at := time.Now()
		err := fn()
		samples[name] = append(samples[name], time.Since(at))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if err := phase("create", func() error {
		var err error
		file, err = os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		return err
	}); err != nil {
		return err
	}
	if err := phase("write", func() error { _, err := file.Write(payload); return err }); err != nil {
		return err
	}
	if err := phase("file_sync", file.Sync); err != nil {
		return err
	}
	if err := phase("close", func() error { err := file.Close(); file = nil; return err }); err != nil {
		return err
	}
	if err := phase("rename", func() error {
		from, err := hostedCostWide(pending)
		if err != nil {
			return err
		}
		to, err := hostedCostWide(path)
		if err != nil {
			return err
		}
		return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	}); err != nil {
		return err
	}
	if err := phase("dir_sync", func() error { return durable.SyncDir(dir) }); err != nil {
		return err
	}
	if err := phase("remove", func() error { return os.Remove(path) }); err != nil {
		return err
	}
	samples["total"] = append(samples["total"], time.Since(start))
	return nil
}

func hostedCostChild(ctx context.Context, owner *Owner, program, dir string, samples hostedCostSamples) (time.Duration, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	var output, stderr bytes.Buffer
	start := time.Now()
	var ended time.Time
	if owner == nil {
		cmd := exec.Command(program, "version")
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, os.Environ(), &output, &stderr
		cmd.WaitDelay = 5 * time.Second
		if err := cmd.Run(); err != nil {
			return 0, nil, fmt.Errorf("direct: %w stderr=%q", err, stderr.String())
		}
		ended = time.Now()
	} else {
		phases := []string{"launch_pending", "suspended", "resumed", "root_exited", "closed"}
		values := make([]time.Duration, 0, len(phases))
		last := start
		var observationErr error
		owner.observe = func(name string, _ fence) {
			now := time.Now()
			if len(values) >= len(phases) || phases[len(values)] != name {
				observationErr = fmt.Errorf("unexpected phase %s at %d", name, len(values))
			}
			values = append(values, now.Sub(last))
			last = now
		}
		defer func() { owner.observe = nil }()
		result, err := owner.Run(context.Background(), Spec{Program: program, Args: []string{"version"}, Dir: dir, Env: os.Environ(), Stdout: &output, Stderr: &stderr})
		ended = time.Now()
		if err != nil || result.ExitCode != 0 || !result.RootExited || !result.TreeClosed || !result.IOJoined || observationErr != nil || len(values) != len(phases) {
			return 0, nil, fmt.Errorf("Owner: result=%+v error=%v observation=%v phases=%d stderr=%q", result, err, observationErr, len(values), stderr.String())
		}
		for i, name := range phases {
			samples[name] = append(samples[name], values[i])
		}
		samples["return"] = append(samples["return"], ended.Sub(last))
	}
	elapsed := ended.Sub(start)
	samples["total"] = append(samples["total"], elapsed)
	return elapsed, output.Bytes(), nil
}

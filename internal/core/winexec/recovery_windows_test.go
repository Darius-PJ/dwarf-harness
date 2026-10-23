package winexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestUnconfirmedFenceNeverReused(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var held windows.Handle
	var marker string
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase != "unconfirmed" {
			return
		}
		marker = filepath.Join(r.dir, "fence.json")
		name, err := windows.UTF16PtrFromString(marker)
		if err == nil {
			held, err = windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
		}
		observationErr = err
		cancel()
	}
	r, err := e.Run(ctx, testRequest(t, "held"))
	if held != 0 {
		if closeErr := windows.CloseHandle(held); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuarantined) || r.State != Quarantined || !r.TreeExited || r.RootExited || r.ResourcesReusable || r.OutputState != Unpublishable || r.ProfileSID != "" {
		t.Fatalf("failed fence transition: %+v %v", r, err)
	}
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	f, err := decodeFence(before, e.rootID.String(), r.RunID.String())
	if err != nil || f.State != "unconfirmed" || f.ProfileCreated {
		t.Fatalf("canonical proof: %+v %v", f, err)
	}
	if _, err := e.Run(context.Background(), testRequest(t, "output", "1")); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("same executor reused uncertainty: %v", err)
	}
	if err := e.Close(); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("Close lost quarantine: %v", err)
	}
	reopened, err := Open(context.Background(), e.root)
	if reopened != nil {
		_ = reopened.Close()
		t.Fatal("reopened an unconfirmed slot")
	}
	if !errors.Is(err, ErrQuarantined) {
		t.Fatalf("reopen: %v", err)
	}
	after, err := os.ReadFile(marker)
	if err != nil || string(after) != string(before) {
		t.Fatalf("reopen changed uncertain proof: %q %v", after, err)
	}
}
func TestClosedCleanupDoesNotFollowReparseOrReuseEarly(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	external := t.TempDir()
	canary := testProtectedFile(t, external, "keep", "external-owner-during-pending-cleanup")
	var held windows.Handle
	var marker string
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase != "tree_exited" {
			return
		}
		marker = filepath.Join(r.dir, "fence.json")
		path, err := windows.UTF16PtrFromString(filepath.Join(r.dir, "input", "payload.exe"))
		if err == nil {
			held, err = windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
		}
		observationErr = err
		if observationErr == nil {
			observationErr = testMakeJunction(filepath.Join(r.dir, "work", "external-junction"), external)
		}
	}
	r, err := e.Run(context.Background(), testRequest(t, "output", "1"))
	if held != 0 {
		if closeErr := windows.CloseHandle(held); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	if !errors.Is(err, ErrCleanupPending) || r.State != Closed || !r.RootExited || !r.TreeExited || r.ResourcesReusable || r.OutputState != Unpublishable {
		t.Fatalf("held-file cleanup: %+v %v", r, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	f, err := decodeFence(data, e.rootID.String(), r.RunID.String())
	if err != nil || f.State != "closed" || f.Proof != "job_empty" {
		t.Fatalf("closed proof: %+v %v", f, err)
	}
	if _, err := e.Run(context.Background(), testRequest(t, "output", "1")); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("cleanup-pending admission: %v", err)
	}
	if err := e.Close(); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("Close lost cleanup-pending: %v", err)
	}
	reopened, err := Open(context.Background(), e.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed proof was not cleaned: %v", err)
	}
	next, nextErr := reopened.Run(context.Background(), testRequest(t, "output", "1"))
	assertReusable(t, next, nextErr)
	if next.RunID == r.RunID || next.ProfileSID == r.ProfileSID {
		t.Fatal("replayed or adopted former run")
	}
	if data, err := os.ReadFile(canary); err != nil || string(data) != "external-owner-during-pending-cleanup" {
		t.Fatalf("closed cleanup traversed the private reparse target: %q %v", data, err)
	}
}

func TestMappedInputCleanup(t *testing.T) {
	t.Parallel()
	for _, held := range []bool{false, true} {
		name := "released-during-cleanup"
		if held {
			name = "held-through-grace"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := testExecutor(t)
			request := testRequest(t, "output", "1")
			if held {
				// The same grace covers post-proof finalization before cleanup can
				// encounter the mapped image, including under the full race load.
				request.Limits.StopGrace = 2 * time.Second
			}
			var payload, marker string
			var release func() error
			var observationErr error
			type phaseSample struct {
				Phase           string
				UTC             time.Time
				StopStarted     bool
				StopElapsedNS   int64
				StopRemainingNS int64
			}
			var phases []phaseSample
			var observedRun *nativeRun
			recordPhase := func(phase string, r *nativeRun) {
				now := time.Now()
				sample := phaseSample{Phase: phase, UTC: now.UTC(), StopStarted: !r.stopAt.IsZero()}
				if sample.StopStarted {
					sample.StopElapsedNS = now.Sub(r.stopAt).Nanoseconds()
					sample.StopRemainingNS = r.stopAt.Add(r.request.Limits.StopGrace).Sub(now).Nanoseconds()
				}
				phases = append(phases, sample)
			}
			type releaseResult struct {
				boundary, released time.Time
				err                error
			}
			var released chan releaseResult
			returned := make(chan struct{})
			t.Cleanup(func() {
				if release != nil {
					if err := release(); err != nil {
						t.Error(err)
					}
				}
			})
			e.observe = func(phase string, r *nativeRun) {
				observedRun = r
				recordPhase(phase, r)
				defer recordPhase(phase+"_observer_return", r)
				if phase == "tree_exited" {
					payload = filepath.Join(r.dir, "input", "payload.exe")
					marker = filepath.Join(r.dir, "fence.json")
					path, err := windows.UTF16PtrFromString(payload)
					if err != nil {
						observationErr = err
						return
					}
					file, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
					recordPhase("mapping_file_open_return", r)
					if err != nil {
						observationErr = err
						return
					}
					const secImage = 0x01000000
					mapping, mapErr := windows.CreateFileMapping(file, nil, windows.PAGE_READONLY|secImage, 0, 0, nil)
					recordPhase("mapping_create_return", r)
					closeErr := windows.CloseHandle(file)
					recordPhase("mapping_file_close_return", r)
					if mapErr != nil {
						observationErr = errors.Join(mapErr, closeErr)
						return
					}
					view, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ, 0, 0, 0)
					recordPhase("mapping_view_return", r)
					if err != nil {
						observationErr = errors.Join(err, closeErr, windows.CloseHandle(mapping))
						return
					}
					release = sync.OnceValue(func() error {
						return errors.Join(windows.UnmapViewOfFile(view), windows.CloseHandle(mapping))
					})
					observationErr = closeErr
				}
				if phase == "closed_fence" && !held && observationErr == nil && release != nil {
					released = make(chan releaseResult, 1)
					started := time.Now()
					go func() {
						timer := time.NewTimer(300 * time.Millisecond)
						defer timer.Stop()
						var early error
						select {
						case <-timer.C:
						case <-returned:
							early = errors.New("Run returned before the mapped image release")
						}
						err := errors.Join(early, release())
						released <- releaseResult{started, time.Now(), err}
					}()
				}
			}
			r, runErr := e.Run(context.Background(), request)
			close(returned)
			if observedRun != nil {
				recordPhase("run_returned", observedRun)
			}
			errorText := ""
			if runErr != nil {
				errorText = runErr.Error()
			}
			evidence, err := json.Marshal(map[string]any{
				"heldThroughGrace": held, "stopGraceNS": request.Limits.StopGrace.Nanoseconds(),
				"receipt": r, "error": errorText, "phases": phases,
				"cleanupPending": errors.Is(runErr, ErrCleanupPending), "deadlineExceeded": errors.Is(runErr, context.DeadlineExceeded),
				"accessDenied": errors.Is(runErr, windows.ERROR_ACCESS_DENIED), "sharingViolation": errors.Is(runErr, windows.ERROR_SHARING_VIOLATION),
				"phaseScope": "observer entry/return and mapping API returns; stop fields apply only when StopStarted; closed_fence precedes Job close and cleanup, not a direct cleanup-entry observation",
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("MAPPED_CLEANUP_EVIDENCE %s", evidence)
			if released != nil {
				result := <-released
				t.Logf("mapping cleanup boundary=%s release=%s elapsed=%s error=%v", result.boundary.UTC().Format(time.RFC3339Nano), result.released.UTC().Format(time.RFC3339Nano), result.released.Sub(result.boundary), result.err)
				observationErr = errors.Join(observationErr, result.err)
			}
			_, retainedErr := os.Stat(payload)
			fence, fenceErr := os.ReadFile(marker)
			if release != nil {
				observationErr = errors.Join(observationErr, release())
			}
			closeErr := e.Close()
			// Recover the real fixture even on the deliberate no-retry rule break.
			if r.State == Closed && !r.ResourcesReusable {
				reopened, err := Open(context.Background(), e.root)
				if err != nil {
					t.Fatal(err)
				}
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if !held {
				assertReusable(t, r, runErr)
				if released == nil || observationErr != nil || closeErr != nil {
					t.Fatalf("mapped-image release: observed=%t error=%v close=%v", released != nil, observationErr, closeErr)
				}
				return
			}
			if observationErr != nil {
				t.Fatal(observationErr)
			}
			if !errors.Is(runErr, ErrCleanupPending) || !errors.Is(runErr, context.DeadlineExceeded) || !errors.Is(runErr, windows.ERROR_ACCESS_DENIED) || r.State != Closed || !r.RootExited || !r.TreeExited || r.ResourcesReusable || r.OutputState != Unpublishable {
				t.Fatalf("mapped-image cleanup bound: %+v error=%v", r, runErr)
			}
			if !errors.Is(closeErr, ErrCleanupPending) || retainedErr != nil || fenceErr != nil {
				t.Fatalf("mapped image or proof not retained: file=%v fence=%v close=%v", retainedErr, fenceErr, closeErr)
			}
			f, err := decodeFence(fence, e.rootID.String(), r.RunID.String())
			if err != nil || f.State != "closed" || f.Proof != "job_empty" {
				t.Fatalf("retained closed proof: %+v error=%v", f, err)
			}
			for _, path := range []string{payload, marker} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("explicit recovery retained %q: %v", path, err)
				}
			}
		})
	}
}
func TestRootOwnershipAndLock(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	if other, err := Open(context.Background(), e.root); other != nil || !errors.Is(err, ErrBusy) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("second owner: %v", err)
	}
	root := t.TempDir()
	canary := testProtectedFile(t, root, "unrelated", "do-not-adopt")
	if other, err := Open(context.Background(), root); other != nil || !errors.Is(err, ErrInvalidRoot) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("unbranded root: %v", err)
	}
	if b, err := os.ReadFile(canary); err != nil || string(b) != "do-not-adopt" {
		t.Fatalf("unbranded bytes changed: %q %v", b, err)
	}
}

func TestOwnedProfileCleanupDoesNotFollowJunctions(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
	target := t.TempDir()
	canary := testProtectedFile(t, target, "keep", "external-owner-nonce")
	before, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	var observationErr error
	e.observe = func(phase string, r *nativeRun) {
		if phase != "tree_exited" {
			return
		}
		for _, parent := range []string{r.profile.Path, filepath.Join(r.dir, "work")} {
			link := filepath.Join(parent, "external")
			out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
			if err != nil {
				observationErr = fmt.Errorf("mklink /J: %w: %s", err, out)
				return
			}
			info, err := os.Lstat(link)
			if err != nil || info.Mode()&os.ModeIrregular == 0 {
				observationErr = fmt.Errorf("junction type: %v: %w", info, err)
				return
			}
		}
	}
	r, runErr := e.Run(context.Background(), testRequest(t, "output", "1"))
	assertReusable(t, r, runErr)
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	b, err := os.ReadFile(canary)
	if err != nil || string(b) != "external-owner-nonce" {
		t.Fatalf("external target changed: %q %v", b, err)
	}
	after, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil || before.String() != after.String() {
		t.Fatalf("external permissions changed: %v", err)
	}
}

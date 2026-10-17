package winexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
func TestClosedCleanupRequiresExplicitReopen(t *testing.T) {
	t.Parallel()
	e := testExecutor(t)
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

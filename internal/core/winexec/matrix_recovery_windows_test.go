package winexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProfileOwnershipIsNotAdopted(t *testing.T) {
	t.Parallel()
	t.Run("colliding-profile", func(t *testing.T) {
		t.Parallel()
		e := testExecutor(t)
		var name, canary string
		var sid *windows.SID
		var observationErr error
		t.Cleanup(func() {
			if sid != nil {
				if err := windows.FreeSid(sid); err != nil {
					t.Error(err)
				}
			}
			if name != "" {
				if err := deleteProfile(name); err != nil {
					t.Error(err)
				}
			}
		})
		e.observe = func(phase string, r *nativeRun) {
			if phase != "unconfirmed" {
				return
			}
			var err error
			sid, err = createProfile(r.fence.ProfileName)
			if err != nil {
				observationErr = err
				return
			}
			name = r.fence.ProfileName
			path, err := profileFolder(sid)
			if err == nil {
				canary = filepath.Join(path, "collision-canary")
				err = os.WriteFile(canary, []byte("preexisting-profile-owner"), 0o600)
			}
			observationErr = err
		}
		r, err := e.Run(context.Background(), testRequest(t, "output", "1"))
		if observationErr != nil || name == "" {
			t.Fatalf("real collision fixture: name=%q error=%v", name, observationErr)
		}
		var native *NativeError
		if !errors.As(err, &native) || !native.HRESULT || native.Code != 0x800700b7 || r.Outcome != NativeFailure || r.State != Closed || r.ProfileSID != "" || !r.CreatedUTC.IsZero() || !r.ResumedUTC.IsZero() || r.RootExited || !r.TreeExited || !r.ResourcesReusable || r.OutputState != Unpublishable {
			t.Fatalf("existing profile was adopted or lost native refusal: %+v %v", r, err)
		}
		data, err := os.ReadFile(canary)
		if err != nil || string(data) != "preexisting-profile-owner" {
			t.Fatalf("collision profile changed: %q %v", data, err)
		}
		if _, err := profileFolder(sid); err != nil {
			t.Fatalf("collision profile registration was removed: %v", err)
		}
	})
	t.Run("foreign-owner", func(t *testing.T) {
		t.Parallel()
		e := testExecutor(t)
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(e.root, "executor.json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var marker rootMarker
		if err := json.Unmarshal(original, &marker); err != nil {
			t.Fatal(err)
		}
		marker.Owner = "S-1-5-21-1-2-3-4"
		if marker.Owner == e.owner {
			t.Fatal("foreign owner fixture accidentally matches current owner")
		}
		if err := writeMarker(path, marker); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		other, err := Open(context.Background(), e.root)
		if other != nil {
			_ = other.Close()
			t.Fatal("foreign owner acquired the root")
		}
		if !errors.Is(err, ErrInvalidRoot) {
			t.Fatalf("foreign owner refusal: %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatalf("foreign owner proof was rewritten: %q %v", after, err)
		}
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(context.Background(), e.root)
		if err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("replaced-native-folder", func(t *testing.T) {
		t.Parallel()
		e := testExecutor(t)
		var original, displaced, canary, marker string
		var moved, replacement bool
		var observationErr error
		restore := func() error {
			if !moved {
				return nil
			}
			if replacement {
				if err := os.Remove(canary); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err := os.Remove(original); err != nil {
					return err
				}
				replacement = false
			}
			if err := os.Rename(displaced, original); err != nil {
				return err
			}
			moved = false
			return nil
		}
		t.Cleanup(func() {
			if original == "" {
				return
			}
			_ = e.Close()
			if err := restore(); err != nil {
				t.Errorf("restore owned native folder: %v", err)
				return
			}
			reopened, err := Open(context.Background(), e.root)
			if err != nil {
				t.Error(err)
				return
			}
			if err := reopened.Close(); err != nil {
				t.Error(err)
			}
		})
		e.observe = func(phase string, r *nativeRun) {
			if phase != "closed_fence" {
				return
			}
			original = r.fence.ProfilePath
			displaced = original + "-owned-original"
			marker = filepath.Join(r.dir, "fence.json")
			if err := os.Rename(original, displaced); err != nil {
				observationErr = err
				return
			}
			moved = true
			if err := os.Mkdir(original, 0o700); err != nil {
				observationErr = err
				return
			}
			replacement = true
			canary = filepath.Join(original, "foreign-identity-canary")
			if err := os.WriteFile(canary, []byte("replacement-owner"), 0o600); err != nil {
				observationErr = err
				return
			}
			binding, err := openDirectory(original, false)
			if err != nil {
				observationErr = err
				return
			}
			observationErr = windows.CloseHandle(binding.Handle)
			if binding.Volume == r.fence.ProfileVolume && binding.ID == r.fence.ProfileFileID {
				observationErr = fmt.Errorf("replacement did not change native folder identity")
			}
		}
		r, err := e.Run(context.Background(), testRequest(t, "output", "1"))
		if observationErr != nil || !moved || !replacement {
			t.Fatalf("real folder replacement: moved=%t replacement=%t error=%v", moved, replacement, observationErr)
		}
		if !errors.Is(err, ErrCleanupPending) || r.State != Closed || !r.TreeExited || !r.RootExited || r.ResourcesReusable || r.OutputState != Unpublishable {
			t.Fatalf("replacement was adopted for cleanup: %+v %v", r, err)
		}
		proof, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); !errors.Is(err, ErrCleanupPending) {
			t.Fatalf("Close lost identity refusal: %v", err)
		}
		other, err := Open(context.Background(), e.root)
		if other != nil {
			_ = other.Close()
			t.Fatal("reopen adopted the replacement folder")
		}
		if !errors.Is(err, ErrCleanupPending) {
			t.Fatalf("reopen identity refusal: %v", err)
		}
		data, err := os.ReadFile(canary)
		if err != nil || string(data) != "replacement-owner" {
			t.Fatalf("replacement canary changed: %q %v", data, err)
		}
		after, err := os.ReadFile(marker)
		if err != nil || string(after) != string(proof) {
			t.Fatalf("closed proof changed during identity refusal: %q %v", after, err)
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(context.Background(), e.root)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		next, err := reopened.Run(context.Background(), testRequest(t, "output", "1"))
		assertReusable(t, next, err)
		if next.RunID == r.RunID || next.ProfileSID == r.ProfileSID {
			t.Fatal("restored cleanup replayed or reused the former run")
		}
	})
}

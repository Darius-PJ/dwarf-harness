package gitprocess

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"golang.org/x/sys/windows"
)

func TestGitProcessFencePublicationCrash(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{"pending", "identity", "invalid-identity", "rewrite", "running-only", "wrong-id", "extra-file", "extra-directory"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			o := nativeOwner(t, root)
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			f := fence{Format: fenceFormat, Version: 1, RootID: o.rootID, RunID: uuid.NewV7(), State: "launch_pending"}
			f.JobName = jobName(f.RootID, f.RunID)
			var err error
			f.Session, err = currentSession()
			if err != nil {
				t.Fatal(err)
			}
			path := o.fencePath(f.RunID)
			extra := filepath.Join(filepath.Dir(path), "unknown")
			var job windows.Handle
			var pi windows.ProcessInformation
			switch shape {
			case "identity", "invalid-identity":
				identity := filepath.Join(root, "identity.json")
				if err := os.Rename(identity, identity+".pending"); err != nil {
					t.Fatal(err)
				}
				if shape == "invalid-identity" {
					if err := os.WriteFile(identity+".pending", []byte("partial"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "rewrite":
				if err := o.writeFence(f); err != nil {
					t.Fatal(err)
				}
				job, err = createJob(f.JobName, o.options.MaxDescendants)
				if err != nil {
					t.Fatal(err)
				}
				pipes, err := newPipes(false)
				if err != nil {
					t.Fatal(err)
				}
				pi, err = createSuspended(Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()}, job, pipes)
				_ = pipes.closeChildren()
				pipes.closeParents()
				if err != nil {
					t.Fatal(err)
				}
				f.State, f.RootPID = "running", pi.ProcessId
				f.RootCreated, err = processCreated(pi.Process)
				if err != nil {
					t.Fatal(err)
				}
				if err := o.writeFence(f); err != nil {
					t.Fatal(err)
				}
				fallthrough
			default:
				if shape == "running-only" {
					f.State, f.RootPID, f.RootCreated = "running", 1, 1
				}
				if shape == "wrong-id" {
					f.RunID = uuid.NewV7()
					f.JobName = jobName(f.RootID, f.RunID)
				}
				raw, err := canonicalJSON(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+".pending", raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if shape == "extra-file" {
					if err := os.WriteFile(extra, []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if shape == "extra-directory" {
					extra = filepath.Join(filepath.Dir(path), uuid.NewV7().String()+".json")
					if err := os.Mkdir(extra, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			defer func() {
				if job != 0 {
					_ = windows.TerminateJobObject(job, 1)
					_ = windows.CloseHandle(job)
				}
				if pi.Process != 0 {
					assertExited(t, pi.Process)
					_ = windows.CloseHandle(pi.Process)
					_ = windows.CloseHandle(pi.Thread)
				}
			}()
			negative := shape == "running-only" || shape == "wrong-id" || shape == "extra-file" || shape == "extra-directory"
			var before []byte
			if negative {
				before, err = os.ReadFile(path + ".pending")
				if err != nil {
					t.Fatal(err)
				}
			}
			opened, report, err := Open(context.Background(), root, Options{RequireTree: true})
			if negative {
				if err == nil || opened != nil {
					t.Fatal("unsafe publication debris admitted", err)
				}
				after, err := os.ReadFile(path + ".pending")
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("refused Open swept unknown state", err)
				}
				if shape == "extra-file" {
					data, err := os.ReadFile(extra)
					if err != nil || string(data) != "keep" {
						t.Fatal("refused Open changed an unknown file", err)
					}
				}
				if shape == "extra-directory" {
					info, err := os.Lstat(extra)
					if err != nil || !info.IsDir() {
						t.Fatal("refused Open swept a directory", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			want := Debris{}
			switch shape {
			case "pending":
				want.PendingRuns = 1
			case "identity", "invalid-identity":
				want.PendingIdentities = 1
			case "rewrite":
				if len(report.Pending) != 1 {
					t.Fatal("rewrite lost authoritative fence")
				}
				state, err := windows.WaitForSingleObject(pi.Process, 0)
				if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
					t.Fatal("Open executed recovery instead of reporting it", err)
				}
				report, err = opened.Reconcile(context.Background())
				if err != nil || len(report.Closed) != 1 || report.Closed[0].Proof != "retained_job" {
					t.Fatalf("rewrite not positively closed: %+v %v", report, err)
				}
			}
			if report.Debris != want {
				t.Fatalf("debris shape count: %+v want %+v", report.Debris, want)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("confirmed authoritative fence retained", err)
			}
			if _, err := os.Stat(path + ".pending"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("confirmed publication debris retained", err)
			}
			result, err := opened.Run(context.Background(), Spec{Program: gitProgram(t), Args: []string{"version"}, Env: os.Environ()})
			if err != nil || !result.TreeClosed || result.ExitCode != 0 {
				t.Fatalf("fresh Run after publication crash: %+v %v", result, err)
			}
		})
	}
	for _, shape := range []string{"slots-initial", "slots-stale"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			o := nativeOwner(t, root)
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			c := fixtureSlots(t, o)
			raw, err := encodeSlots(c)
			if err != nil {
				t.Fatal(err)
			}
			path := o.slotsPath(c.Header.BatchID)
			if shape == "slots-stale" {
				if err := writeMarkerBytes(path, raw, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path+".pending", raw, 0o600); err != nil {
				t.Fatal(err)
			}
			opened := nativeOwner(t, root)
			report := opened.report()
			if len(report.Pending) != 0 {
				t.Fatal("reserved names became fictional invocations", report)
			}
			if shape == "slots-initial" {
				if report.Debris.PendingRuns != 1 || report.Reservations != 0 {
					t.Fatal("initial temp not classified as debris", report)
				}
			} else {
				if report.Reservations != 1 || report.Debris.PendingRuns != 0 {
					t.Fatal("authoritative container was swept", report)
				}
				if _, err := os.Stat(path + ".pending"); err != nil {
					t.Fatal("stale temp swept before native proof", err)
				}
				report, err = opened.Reconcile(context.Background())
				if err != nil || len(report.Closed) != 0 || report.Reservations != 0 {
					t.Fatal("unused reservations failed exact-name recovery", report, err)
				}
			}
			for _, file := range []string{path, path + ".pending"} {
				if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("proved publication state survived", file, err)
				}
			}
			assertFreshSlotRun(t, opened, c.Header.RunIDs)
		})
	}
}

func fixtureSlots(t *testing.T, o *Owner) slotsContainer {
	t.Helper()
	c := seedSlots()
	c.Header.RootID, c.Header.BatchID = o.rootID, uuid.NewV7()
	for i := range c.Header.RunIDs {
		c.Header.RunIDs[i] = uuid.NewV7()
	}
	var err error
	c.Header.Session, err = currentSession()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func slotLayoutErrors(t *testing.T) {
	for name, raw := range slotCodecCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			o := nativeOwner(t, root)
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			c := seedSlots()
			if err := writeMarker(filepath.Join(root, "identity.json"), rootIdentity{identityFormat, 1, c.Header.RootID}); err != nil {
				t.Fatal(err)
			}
			path := o.slotsPath(c.Header.BatchID)
			assertSlotLayoutRefused(t, root, map[string][]byte{path: raw})
		})
	}
	for _, name := range []string{"wrong-root", "wrong-batch", "missing-identity", "duplicate-membership", "batch-is-run", "competing-running", "closed-root-link", "closed-session-link", "closed-temp-link", "lone-running-temp", "nonzero-initial-temp", "nonzero-stale-temp", "unknown-file", "unknown-directory"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			o := nativeOwner(t, root)
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			c := fixtureSlots(t, o)
			path := o.slotsPath(c.Header.BatchID)
			files := make(map[string][]byte)
			var extraDir string
			switch name {
			case "wrong-root":
				c.Header.RootID = uuid.NewV7()
			case "wrong-batch":
				path = o.slotsPath(uuid.NewV7())
			case "missing-identity":
				if err := os.Remove(filepath.Join(root, "identity.json")); err != nil {
					t.Fatal(err)
				}
			case "duplicate-membership", "batch-is-run":
				second := c
				second.Header.BatchID = uuid.NewV7()
				if name == "batch-is-run" {
					second.Header.BatchID = c.Header.RunIDs[0]
				}
				raw, _ := canonicalJSON(second.Header)
				container := make([]byte, slotsSize)
				copy(container, raw)
				files[o.slotsPath(second.Header.BatchID)] = container
			case "competing-running", "closed-root-link", "closed-session-link", "closed-temp-link", "lone-running-temp":
				f := c.Header.candidate(0)
				f.State, f.RootPID, f.RootCreated = "running", 1, 1
				sidecar := o.fencePath(f.RunID)
				if name != "competing-running" && name != "lone-running-temp" {
					f.State, f.Proof = "closed", "name_absent"
					if name == "closed-session-link" {
						f.RootPID, f.RootCreated = 0, 0
						f.Session++
					}
				}
				if name == "closed-temp-link" || name == "lone-running-temp" {
					sidecar += ".pending"
				}
				files[sidecar], _ = canonicalJSON(f)
			case "nonzero-initial-temp", "nonzero-stale-temp":
				c.Frames[0] = runningSlot(c, 0)
				if name == "nonzero-initial-temp" {
					path += ".pending"
				} else {
					files[path+".pending"], _ = encodeSlots(c)
					c.Frames[0] = fence{}
				}
			case "unknown-file":
				files[filepath.Join(root, "runs", "unknown")] = []byte("keep")
			case "unknown-directory":
				extraDir = filepath.Join(root, "runs", uuid.NewV7().String()+".slots")
				if err := os.Mkdir(extraDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			files[path], _ = encodeSlots(c)
			assertSlotLayoutRefused(t, root, files)
			if extraDir != "" {
				info, err := os.Lstat(extraDir)
				if err != nil || !info.IsDir() {
					t.Fatal("refusal swept unknown directory", err)
				}
			}
		})
	}
}

func assertSlotLayoutRefused(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	identity, err := readRegular(filepath.Join(root, "identity.json"))
	var id rootIdentity
	if err == nil {
		if err := decodeCanonical(identity, &id); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if !validID(id.RootID) {
		id.RootID = uuid.NewV7()
	}
	f := seedFence()
	f.RootID, f.RunID = id.RootID, uuid.NewV7()
	f.JobName = jobName(f.RootID, f.RunID)
	debris, _ := canonicalJSON(f)
	debrisPath := filepath.Join(root, "runs", f.RunID.String()+".json.pending")
	files[debrisPath] = debris
	for path, raw := range files {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opened, _, err := Open(context.Background(), root, Options{RequireTree: true})
	if opened != nil || !errors.Is(err, ErrInvalid) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("invalid slot layout admitted or swept: %v", err)
	}
	for path, before := range files {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("refused slot Open changed bytes", path, err)
		}
	}
}

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
}

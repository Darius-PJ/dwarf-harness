package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"
)

func init() { startupUnconfirmedFence = testStartupUnconfirmedFence }

func startupFixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	program := ""
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(directory, "git.exe")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			program = candidate
			break
		}
	}
	if program == "" {
		t.Fatal("fixture Git is unavailable")
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
			env = append(env, value)
		}
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	command := append([]string{program, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)
	process, err := os.StartProcess(program, command, &os.ProcAttr{Dir: dir, Env: env, Files: []*os.File{null, null, null}})
	if err != nil {
		t.Fatal("fixture Git could not start")
	}
	state, err := process.Wait()
	if err != nil || !state.Success() {
		t.Fatal("fixture Git did not succeed")
	}
}

func testStartupUnconfirmedFence(t *testing.T) {
	root, _, origin := restartPrefix(t, 99)
	settings := integratedStartupSettings(t, root, origin)
	rootID, runID := uuid.NewV7(), uuid.NewV7()
	processRoot := filepath.Join(root, "workspaces", "processes")
	runs := filepath.Join(processRoot, "runs")
	if err := os.MkdirAll(runs, 0700); err != nil {
		t.Fatal(err)
	}
	identity := []byte(fmt.Sprintf("{\"format\":\"dwarf-harness/git-process-root\",\"version\":1,\"root_id\":%q}\n", rootID))
	// 0xffffffff is the invalid Windows session identifier, not this process's
	// session. Reconciliation must refuse before consulting any PID or sweeping.
	fence := []byte(fmt.Sprintf("{\"format\":\"dwarf-harness/git-process\",\"version\":1,\"root_id\":%q,\"run_id\":%q,\"job_name\":%q,\"windows_session_id\":4294967295,\"state\":\"running\",\"root_pid\":1,\"root_created_100ns\":1}\n", rootID, runID, `Local\dwarf-harness-git-`+rootID.String()+"-"+runID.String()))
	scratch := filepath.Join(root, "scratch", "1234abcd")
	if err := os.MkdirAll(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	startupFixtureGit(t, scratch, "init", "-b", "main")
	scratchPath := filepath.Join(scratch, "synthetic.txt")
	scratchBytes := []byte("synthetic scratch bytes\n")
	if err := os.WriteFile(scratchPath, scratchBytes, 0600); err != nil {
		t.Fatal(err)
	}
	startupFixtureGit(t, scratch, "add", "--", "synthetic.txt")
	startupFixtureGit(t, scratch, "commit", "-m", "synthetic fixture")
	refPath := filepath.Join(scratch, ".git", "refs", "heads", "main")
	refBytes, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		filepath.Join(processRoot, "identity.json"): identity,
		filepath.Join(runs, runID.String()+".json"): fence,
		scratchPath: scratchBytes,
		refPath:     refBytes,
	}
	for name, raw := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	code, output := integratedStartupChild(t, root, settings)
	if code != 1 || strings.Count(output, "[terminal] startup refused: workspace:") != 1 || strings.Contains(output, "colony opened") || strings.Contains(output, "ready") {
		t.Fatalf("unconfirmed running fence did not stop workspace startup: code=%d output=%s", code, output)
	}
	for name, before := range files {
		after, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatal("refused startup changed a scratch, ref, identity or pending fence", err)
		}
		if strings.Contains(output, name) {
			t.Fatal("startup refusal disclosed a private path")
		}
	}
}

// An unconfirmed owner intentionally retains the root lock until process
// exit. Exercise this startup refusal in the executable's real lifetime.
func integratedStartupChild(t *testing.T, root, settings string) (int, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	outputName := filepath.Join(t.TempDir(), "startup-output.txt")
	output, err := os.Create(outputName)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	env := append(os.Environ(), "DWARF_T16_STARTUP_FENCE_CHILD=1", "DWARF_T16_STARTUP_ROOT="+root, "DWARF_T16_STARTUP_SETTINGS="+settings)
	process, err := os.StartProcess(executable, []string{executable, "-test.run=^TestRuntimeRestartStartupFenceChild$"}, &os.ProcAttr{Env: env, Files: []*os.File{input, output, output}})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Kill()
	state, err := process.Wait()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(outputName)
	if err != nil {
		t.Fatal(err)
	}
	return state.ExitCode(), string(raw)
}

func TestRuntimeRestartStartupFenceChild(t *testing.T) {
	if os.Getenv("DWARF_T16_STARTUP_FENCE_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	root, settings := os.Getenv("DWARF_T16_STARTUP_ROOT"), os.Getenv("DWARF_T16_STARTUP_SETTINGS")
	if root == "" || settings == "" {
		t.Fatal("missing startup child fixture")
	}
	os.Exit(run([]string{"-colony-dir", root, "-settings", settings}, os.Stdin, os.Stdout, nil))
}

package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func gitVariables(env []string) map[string]string {
	out := map[string]string{}
	for _, s := range env {
		key, value, _ := strings.Cut(s, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			out[key] = value
		}
	}
	return out
}

func TestControlledEnvironmentIgnoresHostGit(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	host := []string{"PATH=kept", "HOME=kept", "GIT_DIR=bogus", "GIT_WORK_TREE=bogus", "GIT_INDEX_FILE=bogus", "GIT_OBJECT_DIRECTORY=bogus", "GIT_ALTERNATE_OBJECT_DIRECTORIES=bogus", "GIT_COMMON_DIR=bogus", "GIT_NAMESPACE=bogus", "GIT_CEILING_DIRECTORIES=bogus", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1", "GIT_CONFIG_PARAMETERS='core.autocrlf=input'", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.excludesFile", "GIT_CONFIG_VALUE_0=host-ignore", "GIT_CONFIG_GLOBAL=host-config", "GIT_CONFIG_SYSTEM=host-system", "GIT_ATTR_NOSYSTEM=1", "GIT_TRACE=1"}
	inspection := map[string]string{"GIT_CONFIG_PARAMETERS": "'core.autocrlf=input'", "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.excludesFile", "GIT_CONFIG_VALUE_0": "host-ignore", "GIT_CONFIG_GLOBAL": "host-config", "GIT_CONFIG_SYSTEM": "host-system", "GIT_ATTR_NOSYSTEM": "1", "GIT_TRACE": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0"}
	repository := map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(f.colony, "workspaces", "empty"), "GIT_NO_REPLACE_OBJECTS": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0"}
	capture := map[string]string{}
	for k, v := range repository {
		capture[k] = v
	}
	capture["GIT_DIR"] = filepath.Join("inputs", "git")
	capture["GIT_WORK_TREE"] = "scratch"
	capture["GIT_INDEX_FILE"] = "index"
	capture["GIT_OBJECT_DIRECTORY"] = filepath.Join("common", "objects")
	capture["GIT_ATTR_NOSYSTEM"] = "1"
	for _, tc := range []struct {
		name string
		env  []string
		want map[string]string
	}{
		{"inspection", inspectionEnvironment(host), inspection}, {"repository", repositoryEnvironment(host, f.colony), repository}, {"capture", captureEnvironment(host, f.colony, "inputs", "scratch", "index", "common"), capture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitVariables(tc.env); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("variables got %#v want %#v", got, tc.want)
			}
			if tc.env[0] != "PATH=kept" || tc.env[1] != "HOME=kept" {
				t.Fatalf("lost non-Git environment: %v", tc.env)
			}
		})
	}
	// Exercise conversion and exclusions through the actual private Git directory.
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	p.Normalization.AutoCRLF = "true"
	inputs := filepath.Join(f.colony, "workspaces", "inputs", "1")
	if err := materializeInputs(f.colony, inputs, p); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	writeTestFile(t, filepath.Join(work, "x.txt"), []byte("a\r\nb\r\n"))
	ignore := filepath.Join(t.TempDir(), "ignore")
	writeTestFile(t, ignore, []byte("x.txt\n"))
	global := filepath.Join(t.TempDir(), "config")
	writeTestFile(t, global, []byte("[core]\n excludesFile = "+filepath.ToSlash(ignore)+"\n"))
	f.env = append(f.env, "GIT_DIR=bogus", "GIT_CONFIG_PARAMETERS='core.autocrlf=input'", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.excludesFile", "GIT_CONFIG_VALUE_0="+ignore, "GIT_CONFIG_GLOBAL="+global)
	env := captureEnvironment(f.env, f.colony, inputs, work, filepath.Join(t.TempDir(), "index"), p.CommonDir)
	listed, _, err := f.git.run(context.Background(), work, env, nil, []int{0}, "ls-files", "-z", "-o", "--exclude-standard")
	if err != nil || string(listed) != "x.txt\x00" {
		t.Fatalf("excluded candidate: %q, %v", listed, err)
	}
	if _, _, err = f.git.run(context.Background(), work, env, strings.NewReader("x.txt\x00"), []int{0}, "update-index", "-z", "--add", "--stdin"); err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(string(gitCommand(t, env, work, nil, "rev-parse", "--verify", "--end-of-options", ":x.txt")))
	blob := gitCommand(t, env, work, nil, "cat-file", "blob", oid)
	if string(blob) != "a\nb\n" {
		t.Fatalf("host changed conversion: %q", blob)
	}
}

func TestUnexpectedGitExitIsTyped(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	b, _, err := f.git.repository(context.Background(), f.colony, filepath.Join(f.root, ".git"), nil, []int{0}, "read-tree", "--end-of-options", strings.Repeat("0", 40))
	var ge *GitError
	if b != nil || !errors.Is(err, ErrGit) || !errors.As(err, &ge) || ge.Exit <= 0 || len(ge.Stderr) == 0 {
		t.Fatalf("got %T %v", err, err)
	}
}

func TestGitCancellation(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := f.git.run(ctx, f.root, inspectionEnvironment(f.env), nil, []int{0}, "version")
	if !errors.Is(err, ErrGit) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled command lost its cause: %v", err)
	}
}

func TestObjectArgumentDoesNotBecomeOption(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	b, err := f.git.readBlob(context.Background(), f.colony, filepath.Join(f.root, ".git"), "--filters")
	var ge *GitError
	if len(b) != 0 || !errors.As(err, &ge) || ge.Exit != 128 {
		t.Fatalf("argument became an option: %q, %v", b, err)
	}
}

func TestMissingGitProgram(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Inspect(context.Background(), t.TempDir()); !errors.Is(err, ErrGitUnavailable) {
		t.Fatalf("missing executable: %v", err)
	}
}

func TestRefArgumentDoesNotBecomeOption(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	common := filepath.Join(f.root, ".git")
	oid := strings.TrimSpace(string(gitCommand(t, f.env, f.root, []byte("ref boundary\n"), "hash-object", "-w", "--stdin", "--no-filters")))
	// Exercise the raw ref-argument boundary, before service-generated names.
	// Git accepts this pseudoref; without the boundary it becomes -d options.
	if err := f.git.createRef(context.Background(), f.colony, common, "-dash-ref", oid); err != nil {
		t.Fatal(err)
	}
	got, err := f.git.readRef(context.Background(), f.colony, common, "-dash-ref", 40)
	if err != nil || got != oid {
		t.Fatalf("literal ref argument: %q, %v", got, err)
	}
}

func TestGitAfterStartCancellationAndWaitDelay(t *testing.T) {
	t.Parallel()
	for _, waitDelay := range []bool{false, true} {
		t.Run(map[bool]string{false: "context-kill", true: "wait-delay-kill"}[waitDelay], func(t *testing.T) {
			t.Parallel()
			g := newGitRunner(nil)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var observed *exec.Cmd
			g.afterStart = func(cmd *exec.Cmd) {
				observed = cmd
				if waitDelay {
					// Delay, but do not fake, termination: WaitDelay must kill
					// this real Git process blocked on the open stdin pipe.
					cmd.Cancel = func() error { return nil }
				}
				cancel()
			}
			_, _, err = g.run(ctx, "", inspectionEnvironment(os.Environ()), reader, []int{0, -1, 1, 128}, "hash-object", "--stdin")
			var ge *GitError
			if !errors.As(err, &ge) || !errors.Is(err, context.Canceled) || observed == nil || observed.ProcessState == nil || observed.ProcessState.Success() {
				t.Fatalf("cancelled child was not waited or lost cause: cmd=%v err=%v", observed, err)
			}
		})
	}
}

func TestGitAfterStartExpectedExitAndStartFailure(t *testing.T) {
	t.Parallel()
	f := newGitFixture(t, "sha1")
	calls := 0
	var observed *exec.Cmd
	f.git.afterStart = func(cmd *exec.Cmd) { calls++; observed = cmd }
	_, exit, err := f.git.run(context.Background(), f.root, inspectionEnvironment(f.env), nil, []int{1}, "symbolic-ref", "-q", "HEAD")
	if err != nil || exit != 1 || calls != 1 || observed.ProcessState == nil || observed.ProcessState.ExitCode() != 1 {
		t.Fatalf("expected real exit changed: exit=%d calls=%d err=%v", exit, calls, err)
	}
	f.git.path = filepath.Join(t.TempDir(), "missing-git")
	_, exit, err = f.git.run(context.Background(), "", inspectionEnvironment(f.env), nil, []int{-1}, "version")
	var ge *GitError
	if !errors.As(err, &ge) || exit != -1 || calls != 1 {
		t.Fatalf("failed Start invoked hook or became expected: exit=%d calls=%d err=%v", exit, calls, err)
	}
}

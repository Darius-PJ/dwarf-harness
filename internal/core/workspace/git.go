package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"strings"

	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
)

type gitRunner struct {
	path, version      string
	unavailable        error
	environ            func() []string
	beforeAnchorDelete func()
	owner              *gitprocess.Owner
	afterStart         func(args []string, pid int)
	afterRun           func(gitprocess.Result)
}

func newGitRunner(owner *gitprocess.Owner, environ func() []string) *gitRunner {
	if environ == nil {
		environ = os.Environ
	}
	g := &gitRunner{owner: owner, environ: environ}
	path, err := exec.LookPath("git")
	if err == nil {
		path, err = filepath.Abs(path)
	}
	if err != nil {
		g.unavailable = fmt.Errorf("%w: %v", ErrGitUnavailable, err)
		return g
	}
	g.path = path
	b, _, err := g.run(context.Background(), "", inspectionEnvironment(environ()), nil, []int{0}, "version")
	if err == nil {
		g.version, err = parseGitVersion(b)
	}
	if err != nil {
		g.unavailable = fmt.Errorf("%w: %v", ErrGitUnavailable, err)
	}
	return g
}

func envKey(s string) string {
	key, _, _ := strings.Cut(s, "=")
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func inspectionEnvironment(host []string) []string {
	var out []string
	for _, s := range host {
		switch envKey(s) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS":
			continue
		}
		out = append(out, s)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

func repositoryEnvironment(host []string, colony string) []string {
	var out []string
	for _, s := range host {
		if !strings.HasPrefix(envKey(s), "GIT_") {
			out = append(out, s)
		}
	}
	return append(out, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(colony, "workspaces", "empty"), "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

func captureEnvironment(host []string, colony, inputs, scratch, index, common string) []string {
	return append(repositoryEnvironment(host, colony), "GIT_DIR="+filepath.Join(inputs, "git"), "GIT_WORK_TREE="+scratch, "GIT_INDEX_FILE="+index, "GIT_OBJECT_DIRECTORY="+filepath.Join(common, "objects"), "GIT_ATTR_NOSYSTEM=1")
}

func integrationEnvironment(host []string, colony, inputs, common string) []string {
	return append(repositoryEnvironment(host, colony), "GIT_DIR="+filepath.Join(inputs, "git"), "GIT_OBJECT_DIRECTORY="+filepath.Join(common, "objects"), "GIT_ATTR_NOSYSTEM=1")
}

type limitedStderr struct{ bytes.Buffer }

func (b *limitedStderr) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (64 << 10) - b.Len()
	if remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func (g *gitRunner) run(ctx context.Context, dir string, env []string, in io.Reader, exits []int, args ...string) ([]byte, int, error) {
	var out bytes.Buffer
	exit, err := g.runOutput(ctx, dir, env, in, &out, exits, args...)
	if err != nil {
		return nil, exit, err
	}
	return out.Bytes(), exit, nil
}

// runOutput streams stdout to the caller while preserving run's process,
// cancellation, exit-code and stderr rules. It never buffers object bodies.
func (g *gitRunner) runOutput(ctx context.Context, dir string, env []string, in io.Reader, out io.Writer, exits []int, args ...string) (int, error) {
	if g.unavailable != nil {
		return -1, g.unavailable
	}
	var stderr limitedStderr
	spec := gitprocess.Spec{Program: g.path, Args: args, Env: env, Dir: dir, Stdin: in, Stdout: out, Stderr: &stderr}
	if g.afterStart != nil {
		spec.Started = func(pid int) { g.afterStart(append([]string{g.path}, args...), pid) }
	}
	result, err := g.owner.Run(ctx, spec)
	if g.afterRun != nil {
		g.afterRun(result)
	}
	exit := result.ExitCode
	// Setup, cancellation and unconfirmed closure are never expected exits.
	if ctx.Err() == nil && (err == nil || errors.Is(err, gitprocess.ErrExit)) && !errors.Is(err, gitprocess.ErrUnconfirmed) {
		for _, want := range exits {
			if exit == want {
				return exit, nil
			}
		}
	}
	detail := stderr.String()
	if detail == "" && err != nil {
		detail = err.Error()
	}
	cause := err
	if ctx.Err() != nil {
		cause = errors.Join(cause, ctx.Err())
	}
	return exit, &GitError{Args: append([]string(nil), args...), Exit: exit, Stderr: detail, Err: cause}
}

func (g *gitRunner) repository(ctx context.Context, colony, common string, in io.Reader, exits []int, args ...string) ([]byte, int, error) {
	prefix := []string{"--git-dir=" + common, "-c", "core.hooksPath=" + filepath.Join(colony, "workspaces", "nohooks"), "-c", "core.fsmonitor=false", "-c", "core.fsync=committed", "-c", "core.fsyncMethod=fsync"}
	return g.run(ctx, "", repositoryEnvironment(g.environ(), colony), in, exits, append(prefix, args...)...)
}

func (g *gitRunner) checkInputsVersion(pinned string) error {
	if g.unavailable != nil {
		return &UnavailableError{Reason: "git_unavailable", Detail: g.unavailable.Error()}
	}
	if g.version != pinned {
		return &UnavailableError{Reason: "inputs_stale", Detail: fmt.Sprintf("pinned Git %q; running Git %q", pinned, g.version)}
	}
	return nil
}

func prepareGitLayout(colony string) error {
	if err := os.MkdirAll(filepath.Join(colony, "workspaces", "nohooks"), 0o700); err != nil {
		return err
	}
	return writePinnedFile(filepath.Join(colony, "workspaces", "empty"), nil)
}

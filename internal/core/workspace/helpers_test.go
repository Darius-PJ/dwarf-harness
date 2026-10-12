package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type gitFixture struct {
	root, colony string
	env          []string
	git          *gitRunner
}

func testEnv(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
	var env []string
	for _, s := range os.Environ() {
		key, _, _ := strings.Cut(s, "=")
		switch strings.ToUpper(key) {
		case "HOME", "USERPROFILE", "XDG_CONFIG_HOME":
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env = append(env, s)
		}
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, "config"), "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_AUTHOR_NAME=Workspace Test", "GIT_AUTHOR_EMAIL=workspace@example.invalid", "GIT_COMMITTER_NAME=Workspace Test", "GIT_COMMITTER_EMAIL=workspace@example.invalid", "GIT_AUTHOR_DATE=2001-02-03T04:05:06Z", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z")
}

func gitCommand(t testing.TB, env []string, dir string, input []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %q: %v; %s", args, err, stderr.Bytes())
	}
	return out
}

func writeTestFile(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newGitFixture(t testing.TB, format string) gitFixture {
	t.Helper()
	f := gitFixture{root: t.TempDir(), colony: t.TempDir(), env: testEnv(t)}
	gitCommand(t, f.env, f.root, nil, "init", "--object-format="+format, "-b", "main")
	gitCommand(t, f.env, f.root, nil, "config", "core.autocrlf", "false")
	writeTestFile(t, filepath.Join(f.root, "base.txt"), []byte("one\ntwo\n"))
	gitCommand(t, f.env, f.root, nil, "add", "--", "base.txt")
	gitCommand(t, f.env, f.root, nil, "commit", "-m", "base")
	gitCommand(t, f.env, f.root, nil, "checkout", "--detach")
	if err := prepareGitLayout(f.colony); err != nil {
		t.Fatal(err)
	}
	f.git = newGitRunner(func() []string { return f.env })
	if f.git.unavailable != nil {
		t.Fatal(f.git.unavailable)
	}
	return f
}

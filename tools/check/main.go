// Command check is the integration gate (D42), run as go run ./tools/check
// from the module.
//
// It runs every step in order, even after a failure, prints each step's
// commands and its PASS or FAIL verdict, and exits non-zero if any step
// failed. Every child go command sets GOTOOLCHAIN=local and an explicit
// CGO_ENABLED itself instead of inheriting them (D28, D45).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func main() {
	os.Exit(run(os.Stdout))
}

// run locates the module, runs every step and returns the exit status.
func run(w io.Writer) int {
	root, err := moduleRoot(w)
	if err != nil {
		fmt.Fprintf(w, "FAIL: locating the module: %v\n", err)
		return 1
	}
	fmt.Fprintf(w, "module root: %s\n", root)
	return runAll(w, steps(root))
}

// testTimeout is the per-package limit of the tests and race steps (D122).
// Go's own default is 10 minutes per test binary.
const testTimeout = "20m"

// step is one check. run returns nil when the step passes.
type step struct {
	title string
	run   func(w io.Writer) error
}

func steps(root string) []step {
	goStep := func(cgo string, args ...string) func(io.Writer) error {
		return func(w io.Writer) error { return stream(w, goCommand(root, cgo, args...)) }
	}
	return []step{
		{"toolchain", func(w io.Writer) error { return checkToolchain(w, root) }},
		{"gofmt", func(w io.Writer) error { return checkFormat(w, root) }},
		{"vet", goStep("0", "vet", "./...")},
		{"staticcheck", goStep("0", "tool", "staticcheck", "./...")},
		{"govulncheck", goStep("0", "tool", "govulncheck", "-db", "https://vuln.go.dev", "./...")},
		{"import boundary", func(w io.Writer) error { return checkBoundary(w, root) }},
		{"tests", goStep("0", "test", "-timeout", testTimeout, "./...")},
		{"race", func(w io.Writer) error { return checkRace(w, root) }},
		{"cross builds", func(w io.Writer) error { return checkCrossBuilds(w, root) }},
	}
}

// runAll runs every step, whatever the earlier verdicts, and returns 1 if any
// step failed and 0 otherwise.
func runAll(w io.Writer, steps []step) int {
	failed := make([]bool, len(steps))
	for i, s := range steps {
		fmt.Fprintf(w, "\n== Step %d: %s\n", i, s.title)
		if err := s.run(w); err != nil {
			failed[i] = true
			fmt.Fprintf(w, "FAIL step %d (%s): %v\n", i, s.title, err)
			continue
		}
		fmt.Fprintf(w, "PASS step %d (%s)\n", i, s.title)
	}

	fmt.Fprintf(w, "\n== Summary\n")
	n := 0
	for i, s := range steps {
		verdict := "PASS"
		if failed[i] {
			verdict = "FAIL"
			n++
		}
		fmt.Fprintf(w, "%s  %d %s\n", verdict, i, s.title)
	}
	if n > 0 {
		fmt.Fprintf(w, "check FAILED: %d of %d steps failed\n", n, len(steps))
		return 1
	}
	fmt.Fprintf(w, "check PASSED: all %d steps passed\n", len(steps))
	return 0
}

// command is a child process together with the environment overrides it
// sets, so that the printed command line is the one that runs.
type command struct {
	dir  string
	env  []string // KEY=VALUE overrides applied on top of the inherited environment
	name string
	args []string
}

// goCommand returns a go command that sets GOTOOLCHAIN=local and
// CGO_ENABLED=cgo itself, and clears GOFLAGS so that flags from the caller's
// environment cannot change go.mod or the files a step sees.
func goCommand(dir, cgo string, args ...string) command {
	return command{
		dir:  dir,
		env:  []string{"GOTOOLCHAIN=local", "CGO_ENABLED=" + cgo, "GOFLAGS="},
		name: "go",
		args: args,
	}
}

// with returns c with further environment overrides.
func (c command) with(env ...string) command {
	c.env = slices.Concat(c.env, env)
	return c
}

func (c command) String() string {
	parts := slices.Concat(c.env, []string{c.name}, c.args)
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t") {
			parts[i] = `"` + p + `"`
		}
	}
	return strings.Join(parts, " ")
}

func (c command) cmd() *exec.Cmd {
	cmd := exec.Command(c.name, c.args...)
	cmd.Dir = c.dir
	// For a duplicated key, exec.Cmd uses the last value, so the overrides win
	// over anything inherited.
	cmd.Env = append(os.Environ(), c.env...)
	return cmd
}

// stream prints c, runs it and copies its output to w as it runs.
func stream(w io.Writer, c command) error {
	fmt.Fprintf(w, "$ %s\n", c)
	cmd := c.cmd()
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

// capture prints c, runs it and returns its standard output. Standard error
// is copied to w on success and returned in the error on failure.
func capture(w io.Writer, c command) ([]byte, error) {
	fmt.Fprintf(w, "$ %s\n", c)
	cmd := c.cmd()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%s: %w\n%s", c.name, err, msg)
		}
		return out, fmt.Errorf("%s: %w", c.name, err)
	}
	w.Write(stderr.Bytes())
	return out, nil
}

// moduleRoot returns the directory of the go.mod that governs the current
// directory.
func moduleRoot(w io.Writer) (string, error) {
	out, err := capture(w, goCommand("", "0", "env", "GOMOD"))
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("the current directory is not inside a Go module")
	}
	return filepath.Dir(gomod), nil
}

// checkToolchain is step 0: go version must report the toolchain pinned by
// the toolchain line in go.mod, and the go env file must not set GOFLAGS.
func checkToolchain(w io.Writer, root string) error {
	return errors.Join(checkPin(w, root), checkGoEnvFlags(w, root))
}

// checkPin compares go version with the toolchain line in go.mod.
// GOTOOLCHAIN=local means nothing is downloaded.
func checkPin(w io.Writer, root string) error {
	out, err := capture(w, goCommand(root, "0", "mod", "edit", "-json"))
	if err != nil {
		return err
	}
	var mod struct{ Toolchain string }
	if err := json.Unmarshal(out, &mod); err != nil {
		return fmt.Errorf("reading go mod edit -json: %w", err)
	}
	if mod.Toolchain == "" {
		return errors.New("go.mod has no toolchain line")
	}

	out, err = capture(w, goCommand(root, "0", "version"))
	if err != nil {
		return err
	}
	fmt.Fprint(w, string(out))
	got, err := parseGoVersion(string(out))
	if err != nil {
		return err
	}
	if got != mod.Toolchain {
		return fmt.Errorf("go version reports %s, but go.mod pins %s; install %s (GOTOOLCHAIN=local downloads nothing)", got, mod.Toolchain, mod.Toolchain)
	}
	fmt.Fprintf(w, "%s is the toolchain pinned in go.mod\n", got)
	return nil
}

// checkGoEnvFlags fails when the go env file (go env -w) sets GOFLAGS. Every
// child go command clears GOFLAGS in its environment, but the go command
// treats an empty variable as unset and falls back to the go env file.
func checkGoEnvFlags(w io.Writer, root string) error {
	out, err := capture(w, goCommand(root, "0", "env", "-json", "GOFLAGS", "GOENV"))
	if err != nil {
		return err
	}
	var env struct{ GOFLAGS, GOENV string }
	if err := json.Unmarshal(out, &env); err != nil {
		return fmt.Errorf("reading go env -json: %w", err)
	}
	if env.GOFLAGS != "" {
		return fmt.Errorf("the go env file %s sets GOFLAGS=%s, which would apply to every go command the check runs; remove it with go env -u GOFLAGS", env.GOENV, env.GOFLAGS)
	}
	fmt.Fprintln(w, "GOFLAGS is not set in the go env file")
	return nil
}

// parseGoVersion returns the toolchain name, such as go1.27.1, from the
// output of go version, for example "go version go1.27.1 windows/amd64".
func parseGoVersion(out string) (string, error) {
	line, _, _ := strings.Cut(out, "\n")
	f := strings.Fields(line)
	if len(f) < 4 || f[0] != "go" || f[1] != "version" || !strings.HasPrefix(f[2], "go1.") {
		return "", fmt.Errorf("unexpected go version output %q", strings.TrimSpace(line))
	}
	return f[2], nil
}

// checkFormat is step 1: the pinned toolchain's gofmt, not the one on PATH,
// must list no file of the module. gofmt walks every directory it is given,
// so it receives every top-level directory and .go file except .git.
func checkFormat(w io.Writer, root string) error {
	out, err := capture(w, goCommand(root, "0", "env", "GOROOT"))
	if err != nil {
		return err
	}
	gofmt := filepath.Join(strings.TrimSpace(string(out)), "bin", "gofmt"+exeSuffix())

	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	args := []string{"-l"}
	for _, e := range entries {
		if e.Name() != ".git" && (e.IsDir() || strings.HasSuffix(e.Name(), ".go")) {
			args = append(args, e.Name())
		}
	}
	out, err = capture(w, command{dir: root, name: gofmt, args: args})
	if err != nil {
		return err
	}
	if listed := strings.TrimSpace(string(out)); listed != "" {
		fmt.Fprintln(w, listed)
		return fmt.Errorf("%d files are not gofmt-formatted", len(strings.Split(listed, "\n")))
	}
	return nil
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// checkCrossBuilds is step 8: the module builds without cgo for every target.
// A null -o builds and writes nothing; without it, go build writes an
// executable into the module whenever ./... matches a single main package.
func checkCrossBuilds(w io.Writer, root string) error {
	var failed []string
	for _, t := range targets {
		c := goCommand(root, "0", "build", "-o", os.DevNull, "./...").with("GOOS="+t.goos, "GOARCH="+t.goarch)
		if err := stream(w, c); err != nil {
			failed = append(failed, t.String())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("go build failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

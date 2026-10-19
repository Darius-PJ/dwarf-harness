package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestParseGoVersion(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string // empty when the output must be rejected
	}{
		{"release", "go version go1.27.1 windows/amd64\n", "go1.27.1"},
		{"release with experiment", "go version go1.27.1 X:nodwarf5 linux/arm64\n", "go1.27.1"},
		{"other release", "go version go1.27.10 darwin/arm64\n", "go1.27.10"},
		{"development build", "go version devel go1.28-0d2d5a8 Mon Oct 5 12:00:00 2026 +0000 windows/amd64\n", ""},
		{"missing platform", "go version go1.27.1\n", ""},
		{"not go version", "gofmt version go1.27.1 windows/amd64\n", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGoVersion(tt.out)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("parseGoVersion(%q) = %q, want an error", tt.out, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseGoVersion(%q) = %q, %v; want %q", tt.out, got, err, tt.want)
			}
		})
	}
}

func TestToolchainGoEnvFlags(t *testing.T) {
	// A module pinned to the installed toolchain, so that only GOFLAGS decides
	// the verdict of step 0.
	out, err := goCommand("", "0", "env", "GOVERSION").cmd().Output()
	if err != nil {
		t.Fatalf("go env GOVERSION: %v", err)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.27\n\ntoolchain "+strings.Fields(string(out))[0]+"\n")

	tests := []struct {
		name    string
		absent  bool   // no go env file at all
		goenv   string // content of the go env file
		ambient string // GOFLAGS in the environment, which the check clears
		ok      bool
	}{
		{name: "no go env file", absent: true, ok: true},
		{name: "empty go env file", ok: true},
		{name: "GOFLAGS only in the environment", ambient: "-tags=planted", ok: true},
		{name: "GOFLAGS in the go env file", goenv: "GOFLAGS=-mod=mod\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "go.env")
			if !tt.absent {
				writeFile(t, file, tt.goenv)
			}
			t.Setenv("GOENV", file)
			t.Setenv("GOFLAGS", tt.ambient)

			var log strings.Builder
			err := checkToolchain(&log, root)
			if tt.ok {
				if err != nil {
					t.Fatalf("step 0 failed: %v\n%s", err, log.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("step 0 passed; want a failure naming %s\n%s", file, log.String())
			}
			for _, want := range []string{file, "GOFLAGS=-mod=mod", "go env -u GOFLAGS"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

func TestRunAll(t *testing.T) {
	tests := []struct {
		name     string
		failing  []int
		wantCode int
	}{
		{"all pass", nil, 0},
		{"first fails", []int{0}, 1},
		{"middle fails", []int{1}, 1},
		{"last fails", []int{3}, 1},
		{"all fail", []int{0, 1, 2, 3}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran []int
			steps := make([]step, 4)
			for i := range len(steps) {
				steps[i] = step{title: "s", run: func(io.Writer) error {
					ran = append(ran, i)
					if slices.Contains(tt.failing, i) {
						return errors.New("planted failure")
					}
					return nil
				}}
			}
			var out strings.Builder
			code := runAll(&out, steps, true)
			if code != tt.wantCode {
				t.Errorf("exit status %d, want %d", code, tt.wantCode)
			}
			if want := []int{0, 1, 2, 3}; !slices.Equal(ran, want) {
				t.Errorf("steps run %v, want %v", ran, want)
			}
			log := out.String()
			for i := range len(steps) {
				verdict := "PASS"
				detail := ""
				if slices.Contains(tt.failing, i) {
					verdict = "FAIL"
					detail = ": planted failure"
				}
				pattern := fmt.Sprintf(`(?m)^%s step %d \(s\) in ([0-9]+\.[0-9]s)%s$`, verdict, i, detail)
				match := regexp.MustCompile(pattern).FindStringSubmatch(log)
				if match == nil {
					t.Errorf("output lacks timed verdict matching %q:\n%s", pattern, log)
					continue
				}
				if want := fmt.Sprintf("\n%s  %d s in %s\n", verdict, i, match[1]); !strings.Contains(log, want) {
					t.Errorf("summary lacks verdict's duration %q:\n%s", want, log)
				}
			}
		})
	}
}

func TestRaceFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		moveRace bool
		failLast bool
		wantRan  []int
		wantCode int
		wantLast string
	}{
		{
			name:     "default runs all nine steps",
			wantRan:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8},
			wantLast: "check PASSED: all 9 steps passed",
		},
		{
			name:     "race disabled",
			args:     []string{"-race=false"},
			wantRan:  []int{0, 1, 2, 3, 4, 5, 6, 8},
			wantLast: "check PASSED: 8 of 9 steps passed, race skipped (D129)",
		},
		{
			name:     "race disabled after reordering",
			args:     []string{"-race=false"},
			moveRace: true,
			wantRan:  []int{1, 2, 3, 4, 5, 6, 7, 8},
			wantLast: "check PASSED: 8 of 9 steps passed, race skipped (D129)",
		},
		{
			name:     "failure after skipped race",
			args:     []string{"-race=false"},
			failLast: true,
			wantRan:  []int{0, 1, 2, 3, 4, 5, 6, 8},
			wantCode: 1,
			wantLast: "check FAILED: 1 of 9 steps failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DWARF_CC", "")
			var out strings.Builder
			race, err := parseArgs(&out, tt.args)
			if err != nil {
				t.Fatalf("parseArgs: %v\n%s", err, out.String())
			}
			checks, cleanup := steps("")
			t.Cleanup(func() {
				if err := cleanup(); err != nil {
					t.Error(err)
				}
			})
			raceIndex := 7
			if tt.moveRace {
				checks[0], checks[7] = checks[7], checks[0]
				raceIndex = 0
			}
			var ran []int
			// Exercise the steps without launching any checks, especially
			// the full repository's race tests.
			for i := range checks {
				checks[i].run = func(io.Writer) error {
					ran = append(ran, i)
					if tt.failLast && i == 8 {
						return errors.New("planted cross-build failure")
					}
					return nil
				}
			}
			if code := runAll(&out, checks, race); code != tt.wantCode {
				t.Errorf("exit status %d, want %d", code, tt.wantCode)
			}
			if !slices.Equal(ran, tt.wantRan) {
				t.Errorf("steps run %v, want %v", ran, tt.wantRan)
			}
			log := out.String()
			lastPattern := `(?m)^` + regexp.QuoteMeta(tt.wantLast) + ` in [0-9]+\.[0-9]s\n\z`
			if !regexp.MustCompile(lastPattern).MatchString(log) {
				t.Errorf("output lacks timed final line matching %q:\n%s", lastPattern, log)
			}
			wantRace := []string{
				fmt.Sprintf("PASS step %d (race)", raceIndex),
				fmt.Sprintf("PASS  %d race", raceIndex),
			}
			if !race {
				wantRace = []string{
					fmt.Sprintf("SKIP step %d (race): -race=false (D129)", raceIndex),
					fmt.Sprintf("SKIP  %d race", raceIndex),
				}
				for _, verdict := range []string{"PASS", "FAIL"} {
					if strings.Contains(log, fmt.Sprintf("%s step %d ", verdict, raceIndex)) ||
						strings.Contains(log, fmt.Sprintf("%s  %d ", verdict, raceIndex)) {
						t.Errorf("skipped race reported as %s:\n%s", verdict, log)
					}
				}
			}
			for _, want := range wantRace {
				pattern := `(?m)^` + regexp.QuoteMeta(want)
				if race {
					pattern += ` in [0-9]+\.[0-9]s`
				}
				pattern += `$`
				if !regexp.MustCompile(pattern).MatchString(log) {
					t.Errorf("output lacks line matching %q:\n%s", pattern, log)
				}
			}
			verdict := "PASS"
			if tt.failLast {
				verdict = "FAIL"
			}
			pattern := fmt.Sprintf(`(?m)^%s  8 cross builds in [0-9]+\.[0-9]s$`, verdict)
			if !regexp.MustCompile(pattern).MatchString(log) {
				t.Errorf("summary lacks timed line matching %q:\n%s", pattern, log)
			}
		})
	}
}

func TestRunRejectsArguments(t *testing.T) {
	// Even a parser regression must not start the real check or its race step.
	t.Chdir(t.TempDir())
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"-unknown"}},
		{"malformed boolean", []string{"-race=maybe"}},
		{"positional argument", []string{"-race=false", "extra"}},
		{"argument after terminator", []string{"--", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if code := run(&out, tt.args); code != 2 {
				t.Errorf("exit status %d, want 2", code)
			}
			log := out.String()
			if !strings.Contains(log, "usage:") || !strings.Contains(log, "-race") {
				t.Errorf("output lacks usage with the supported flag:\n%s", log)
			}
			if strings.Contains(log, "$ ") || strings.Contains(log, "== Step") {
				t.Errorf("invalid arguments launched a check:\n%s", log)
			}
		})
	}
}

package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
			code := runAll(&out, steps)
			if code != tt.wantCode {
				t.Errorf("exit status %d, want %d", code, tt.wantCode)
			}
			if want := []int{0, 1, 2, 3}; !slices.Equal(ran, want) {
				t.Errorf("steps run %v, want %v", ran, want)
			}
			for i := range len(steps) {
				verdict := "PASS"
				if slices.Contains(tt.failing, i) {
					verdict = "FAIL"
				}
				if want := fmt.Sprintf("%s step %d ", verdict, i); !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out.String())
				}
			}
		})
	}
}

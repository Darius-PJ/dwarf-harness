package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCCSource is a stand-in compiler: for --version it prints the value of
// FAKE_CC_VERSION.
const fakeCCSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--version" {
		fmt.Fprintln(os.Stderr, "usage: fakecc --version")
		os.Exit(2)
	}
	fmt.Print(os.Getenv("FAKE_CC_VERSION"))
}
`

// buildFakeCC builds the stand-in compiler in a temporary directory and
// returns its absolute path.
func buildFakeCC(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module fakecc\n\ngo 1.27\n")
	writeFile(t, filepath.Join(dir, "main.go"), fakeCCSource)
	exe := filepath.Join(dir, "fakecc"+exeSuffix())
	c := goCommand(dir, "0", "build", "-o", exe, ".").with("GOWORK=off")
	if out, err := c.cmd().CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", c, err, out)
	}
	return exe
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDwarfCC(t *testing.T) {
	cc := buildFakeCC(t)
	const pinned = "clang version 23.1.2 (https://github.com/llvm/llvm-project.git 0123456789abcdef0123456789abcdef01234567)\n" +
		"Target: x86_64-w64-windows-gnu\n" +
		"Thread model: posix\n" +
		"InstalledDir: C:/llvm-mingw/bin\n"

	tests := []struct {
		name    string
		cc      func(t *testing.T) string
		version string
		ok      bool
	}{
		{"unset", func(*testing.T) string { return "" }, pinned, false},
		{
			// A relative path to a matching compiler, which would run if it
			// were accepted.
			"relative path",
			func(t *testing.T) string {
				t.Chdir(filepath.Dir(cc))
				return "." + string(filepath.Separator) + filepath.Base(cc)
			},
			pinned, false,
		},
		{"missing file", func(t *testing.T) string { return filepath.Join(t.TempDir(), "clang"+exeSuffix()) }, pinned, false},
		{"directory", func(t *testing.T) string { return t.TempDir() }, pinned, false},
		{
			"wrong version",
			func(*testing.T) string { return cc },
			strings.Replace(pinned, "23.1.2", "22.1.0", 1), false,
		},
		{
			"version with the pinned version as prefix",
			func(*testing.T) string { return cc },
			strings.Replace(pinned, "23.1.2", "23.1.20", 1), false,
		},
		{
			"wrong target",
			func(*testing.T) string { return cc },
			strings.Replace(pinned, "x86_64-w64-windows-gnu", "x86_64-pc-windows-msvc", 1), false,
		},
		{"no target line", func(*testing.T) string { return cc }, "clang version 23.1.2", false},
		{"full match", func(*testing.T) string { return cc }, pinned, true},
		{
			"full match with CRLF line endings",
			func(*testing.T) string { return cc },
			strings.ReplaceAll(pinned, "\n", "\r\n"), true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FAKE_CC_VERSION", tt.version)
			out, err := checkDwarfCC(tt.cc(t))
			if tt.ok {
				if err != nil || out != tt.version {
					t.Fatalf("checkDwarfCC = %q, %v; want %q, nil", out, err, tt.version)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkDwarfCC accepted the compiler; want a failure")
			}
			for _, want := range []string{"DWARF_CC", wantClangVersion, wantClangTarget} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

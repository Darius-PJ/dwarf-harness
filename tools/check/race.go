package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The pinned Windows C compiler, llvm-mingw 20260922 (D28), reports these as
// the first two lines of --version. The rest of the first line is a
// git-revision suffix.
const (
	wantClangVersion = "clang version 23.1.2"
	wantClangTarget  = "Target: x86_64-w64-windows-gnu"
)

// checkRace is step 7: the tests again with the race detector, which needs
// cgo and the C compiler named explicitly through CC.
func checkRace(w io.Writer, root string) error {
	cc, err := raceCompiler(w)
	if err != nil {
		return err
	}
	return stream(w, goCommand(root, "1", "test", "-race", "./...").with("CC="+cc))
}

// raceCompiler returns this platform's C compiler for race-detector tests
// after printing its version. It never searches for, guesses or installs one.
func raceCompiler(w io.Writer) (string, error) {
	switch runtime.GOOS {
	case "windows":
		cc := os.Getenv("DWARF_CC")
		out, err := checkDwarfCC(cc)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "$ %s --version\n%s", command{name: cc}, out)
		return cc, nil
	case "linux":
		cc := "/usr/bin/gcc"
		return cc, printVersion(w, cc)
	case "darwin":
		out, err := capture(w, command{name: "xcrun", args: []string{"--find", "clang"}})
		if err != nil {
			return "", err
		}
		cc := strings.TrimSpace(string(out))
		return cc, printVersion(w, cc)
	}
	return "", fmt.Errorf("no race-test C compiler is defined for %s", runtime.GOOS)
}

func printVersion(w io.Writer, cc string) error {
	out, err := capture(w, command{name: cc, args: []string{"--version"}})
	fmt.Fprint(w, string(out))
	return err
}

// checkDwarfCC checks that cc, the value of DWARF_CC, is an absolute path to
// the pinned llvm-mingw clang and returns its --version output.
func checkDwarfCC(cc string) (string, error) {
	if cc == "" {
		return "", dwarfCCError("DWARF_CC is not set")
	}
	if !filepath.IsAbs(cc) {
		return "", dwarfCCError(fmt.Sprintf("DWARF_CC=%s is not an absolute path", cc))
	}
	info, err := os.Stat(cc)
	if err != nil {
		return "", dwarfCCError(fmt.Sprintf("DWARF_CC=%s: %v", cc, err))
	}
	if !info.Mode().IsRegular() {
		return "", dwarfCCError(fmt.Sprintf("DWARF_CC=%s is not a file", cc))
	}
	out, err := exec.Command(cc, "--version").Output()
	if err != nil {
		return "", dwarfCCError(fmt.Sprintf("DWARF_CC=%s --version: %v", cc, err))
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if len(lines) < 2 || !isPinnedClang(lines[0]) || lines[1] != wantClangTarget {
		return "", dwarfCCError(fmt.Sprintf("DWARF_CC=%s reports %q", cc, lines[:min(2, len(lines))]))
	}
	return string(out), nil
}

// isPinnedClang reports whether line is wantClangVersion, alone or followed by
// a space and a suffix, so that clang version 23.1.20 does not match.
func isPinnedClang(line string) bool {
	rest, ok := strings.CutPrefix(line, wantClangVersion)
	return ok && (rest == "" || rest[0] == ' ')
}

func dwarfCCError(problem string) error {
	return fmt.Errorf("%s; set DWARF_CC to the absolute path of the pinned llvm-mingw 20260922 clang (D28), whose --version reports %q and %q",
		problem, wantClangVersion, wantClangTarget)
}

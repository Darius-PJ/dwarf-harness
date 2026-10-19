package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoStandardDirectoryJunctionIsNotExempt(t *testing.T) {
	t.Parallel()
	standardRoot := filepath.Join(t.TempDir(), "src")
	if err := os.Mkdir(standardRoot, 0700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	writeFixtureFile(t, filepath.Join(target, "standard.go"), []byte("package standard\n"))
	junction := filepath.Join(standardRoot, "standard")
	output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
	if info, err := os.Lstat(junction); err != nil || info.Mode()&os.ModeIrregular == 0 {
		t.Fatalf("junction fixture: %v, %v", info, err)
	}
	r, out := standardContainmentResult(t, standardRoot, junction, filepath.Join(junction, "standard.go"))
	assertFileCoverage(t, r, out, map[string][]string{"consumer.go": {"unresolved_reference"}})
}

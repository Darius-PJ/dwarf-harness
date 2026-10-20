package graph

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func goTempAlias(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(real)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, len(real)+1)
	n, err := syscall.GetShortPathName(name, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatalf("short temporary path: size %d, %v", n, err)
	}
	alias := syscall.UTF16ToString(buffer[:n])
	if strings.EqualFold(alias, real) {
		t.Skip("temporary volume has no 8.3 alias for this test directory")
	}
	return alias
}

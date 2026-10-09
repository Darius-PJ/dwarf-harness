//go:build windows

package durable

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestExtendedPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, in, want string }{
		{"relative name", "colony.json", `\\?\` + filepath.Join(wd, "colony.json")},
		{"drive path with dot-dot", `C:/a/../b`, `\\?\C:\b`},
		{"UNC path", `\\server\share\x`, `\\?\UNC\server\share\x`},
		{"extended path", `\\?\C:\x`, `\\?\C:\x`},
		{"device path", `\\.\pipe\x`, `\\.\pipe\x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := extendedPath(tt.in)
			if err != nil {
				t.Fatalf("extendedPath(%q) = %v", tt.in, err)
			}
			if got := windows.UTF16PtrToString(p); got != tt.want {
				t.Errorf("extendedPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

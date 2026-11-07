package gitprocess

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"uuid"
)

func seedFence() fence {
	root, run := uuid.UUID{6: 0x70, 8: 0x80, 15: 1}, uuid.UUID{6: 0x70, 8: 0x80, 15: 2}
	return fence{Format: fenceFormat, Version: 1, RootID: root, RunID: run, JobName: jobName(root, run), State: "launch_pending"}
}
func TestGitProcessFenceCodec(t *testing.T) {
	t.Parallel()
	f := seedFence()
	raw, err := canonicalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"duplicate":            bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"unknown":              bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"extra":true`), 1),
		"null":                 bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":null`), 1),
		"missing newline":      bytes.TrimSuffix(raw, []byte("\n")),
		"running without root": bytes.Replace(raw, []byte("launch_pending"), []byte("running"), 1),
		"closed without proof": bytes.Replace(raw, []byte("launch_pending"), []byte("closed"), 1),
		"unknown format":       bytes.Replace(raw, []byte(fenceFormat), []byte("other"), 1),
		"oversize":             []byte(strings.Repeat(" ", 4097)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeFence(bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("accepted invalid fence", err)
			}
		})
	}
	for _, state := range []string{"launch_pending", "running", "closed"} {
		f.State = state
		if state != "launch_pending" {
			f.RootPID, f.RootCreated = 1, 2
		}
		if state == "closed" {
			f.Proof = "name_absent"
		}
		raw, _ := canonicalJSON(f)
		got, err := decodeFence(raw)
		if err != nil || got != f {
			t.Fatalf("valid state %s refused: %+v %v", state, got, err)
		}
	}
}
func FuzzGitProcessFence(f *testing.F) {
	raw, _ := canonicalJSON(seedFence())
	f.Add(raw)
	f.Add([]byte("{}\n"))
	f.Add([]byte("null\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		value, err := decodeFence(raw)
		if err != nil {
			return
		}
		encoded, err := canonicalJSON(value)
		if err != nil || !bytes.Equal(raw, encoded) {
			t.Fatal("accepted a noncanonical or lossy fence")
		}
	})
}
func TestGitProcessUnsupported(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("portable refusal")
	}
	root := filepath.Join(t.TempDir(), "untouched")
	owner, _, err := Open(context.Background(), root, Options{RequireTree: true})
	if !errors.Is(err, ErrUnsupported) || owner != nil {
		t.Fatalf("unsupported mode admitted: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsupported mode changed filesystem", err)
	}
}

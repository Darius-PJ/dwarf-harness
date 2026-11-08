//go:build !windows

package gitprocess

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitProcessPortable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	program, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	program, err = filepath.Abs(program)
	if err != nil {
		t.Fatal(err)
	}
	o, report, err := Open(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if report.TreeProofSupported || len(report.Pending) != 0 {
		t.Fatalf("portable tree claim: %+v", report)
	}
	_, err = o.Run(context.Background(), Spec{Program: program, Args: []string{"not-a-git-command"}, Env: os.Environ()})
	if !errors.Is(err, ErrExit) {
		t.Fatal("Git exit cause lost", err)
	}
	var output bytes.Buffer
	r, err := o.Run(context.Background(), Spec{Program: program, Args: []string{"version"}, Env: os.Environ(), Stdout: &output})
	if err != nil || !r.RootExited || !r.IOJoined || r.TreeClosed || r.ExitCode != 0 || !bytes.HasPrefix(output.Bytes(), []byte("git version ")) {
		t.Fatalf("portable root execution: %+v %v %q", r, err, output.String())
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err = o.Run(ctx, Spec{Program: program, Args: []string{"hash-object", "--stdin"}, Env: os.Environ(), Stdin: reader, Started: func(int) { cancel(); _ = writer.Close() }})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrExit) || !r.RootExited || !r.IOJoined {
		t.Fatalf("portable cancellation became an expected exit: %+v %v", r, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "lock" {
		t.Fatal("portable execution wrote a fence", entries)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, report, err := Open(context.Background(), root, Options{})
	if err != nil || len(report.Pending) != 0 {
		t.Fatalf("portable reopen fenced: %+v %v", report, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

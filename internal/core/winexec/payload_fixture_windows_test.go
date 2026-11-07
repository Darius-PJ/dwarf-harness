package winexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type testPayloadImage struct {
	path  string
	image []byte
}

// Supervisors borrow the parent's image; only the process that builds it owns
// its directory. Both remain live until TestMain has joined all tests.
var testPayloadSource string
var testPayloadDir string
var testPayloadCache struct {
	once  sync.Once
	value testPayloadImage
	err   error
}

func testPayload(t *testing.T) (testPayloadImage, error) {
	t.Helper()
	testPayloadCache.once.Do(func() {
		testPayloadCache.value, testPayloadCache.err = testLoadPayload(t)
	})
	return testPayloadCache.value, testPayloadCache.err
}

func testLoadPayload(t *testing.T) (testPayloadImage, error) {
	t.Helper()
	path := testPayloadSource
	if path == "" {
		var err error
		if testRaceBuild {
			path, err = testBuildPayload(t)
		} else {
			path, err = os.Executable()
		}
		if err != nil {
			return testPayloadImage{}, err
		}
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return testPayloadImage{}, fmt.Errorf("read payload %q: %w", path, err)
	}
	return testPayloadImage{path: path, image: image}, nil
}

func testBuildPayload(t *testing.T) (string, error) {
	t.Helper()
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("companion command go version: lookup: %w; output=%q", err, "")
	}
	goPath, err = filepath.Abs(goPath)
	if err != nil {
		return "", fmt.Errorf("companion command go version: absolute path: %w; output=%q", err, "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if deadline, ok := t.Deadline(); ok {
		cancel()
		ctx, cancel = context.WithDeadline(context.Background(), deadline)
	}
	defer cancel()
	env := append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=", "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	version := exec.CommandContext(ctx, goPath, "version")
	version.Env = env
	out, err := version.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("companion command %q: %w; output=%s", version.Args, errors.Join(err, ctx.Err()), out)
	}
	want := "go version " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
	if strings.TrimSpace(string(out)) != want {
		return "", fmt.Errorf("companion command %q: version mismatch, want %q; output=%s", version.Args, want, out)
	}
	testPayloadDir, err = os.MkdirTemp("", "winexec-payload-")
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(testPayloadDir)
	if err != nil {
		return "", err
	}
	testPayloadDir = dir
	path := filepath.Join(dir, "payload.exe")
	command := exec.CommandContext(ctx, goPath, "test", "-c", "-vet=off", "-race=false", "-o", path, ".")
	command.Env = env
	started := time.Now()
	out, err = command.CombinedOutput()
	t.Logf("companion build command=%q elapsed=%s outputBytes=%d", command.Args, time.Since(started), len(out))
	if err != nil {
		return "", fmt.Errorf("companion command %q: %w; output=%s", command.Args, errors.Join(err, ctx.Err()), out)
	}
	return path, nil
}

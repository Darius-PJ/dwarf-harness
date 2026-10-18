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
	"unsafe"
	"uuid"

	"golang.org/x/sys/windows"
)

var testGitSeedDir string

type testGitFixture struct {
	root, path, version, main string
	env                       []string
}

func TestMain(m *testing.M) {
	code := m.Run()
	if testGitSeedDir != "" {
		if err := os.RemoveAll(testGitSeedDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}

func testGitEnvironment(home string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if key == "HOME" || key == "USERPROFILE" || key == "XDG_CONFIG_HOME" || strings.HasPrefix(key, "GIT_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, "config"), "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Winexec Test", "GIT_AUTHOR_EMAIL=winexec@example.invalid", "GIT_COMMITTER_NAME=Winexec Test", "GIT_COMMITTER_EMAIL=winexec@example.invalid",
		"GIT_AUTHOR_DATE=2001-02-03T04:05:06Z", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z")
}

func (f testGitFixture) command(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, f.path, args...)
	cmd.Dir, cmd.Env = f.root, f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %q: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

var testGitSeed = sync.OnceValues(func() (testGitFixture, error) {
	var f testGitFixture
	var err error
	f.path, err = exec.LookPath("git")
	if err != nil {
		return f, err
	}
	testGitSeedDir, err = os.MkdirTemp("", "winexec-git-seed-")
	if err != nil {
		return f, err
	}
	f.root = filepath.Join(testGitSeedDir, "repo")
	home := filepath.Join(testGitSeedDir, "home")
	for _, path := range []string{f.root, home} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return f, err
		}
	}
	f.env = testGitEnvironment(home)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if f.version, err = f.command(ctx, "version"); err != nil {
		return f, err
	}
	for _, args := range [][]string{{"init", "--object-format=sha1", "-b", "noncanonical"}, {"config", "core.autocrlf", "false"}} {
		if _, err := f.command(ctx, args...); err != nil {
			return f, err
		}
	}
	if err := os.WriteFile(filepath.Join(f.root, "base.txt"), []byte("protected-git-file\n"), 0o600); err != nil {
		return f, err
	}
	for _, args := range [][]string{{"add", "--", "base.txt"}, {"commit", "-m", "winexec denial fixture"}} {
		if _, err := f.command(ctx, args...); err != nil {
			return f, err
		}
	}
	f.main, err = f.command(ctx, "rev-parse", "refs/heads/noncanonical")
	if err == nil {
		_, err = f.command(ctx, "update-ref", "refs/heads/main", f.main)
	}
	return f, err
})

func testPrivateGit(t *testing.T) testGitFixture {
	t.Helper()
	seed, err := testGitSeed()
	if err != nil {
		t.Fatal(err)
	}
	f := seed
	f.root, f.env = t.TempDir(), testGitEnvironment(t.TempDir())
	if err := os.CopyFS(f.root, os.DirFS(seed.root)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	branch, err := f.command(ctx, "symbolic-ref", "HEAD")
	if err != nil || branch != "refs/heads/noncanonical" {
		t.Fatalf("real checked-out noncanonical branch: %q %v", branch, err)
	}
	main, err := f.command(ctx, "rev-parse", "refs/heads/main")
	if err != nil || main != f.main {
		t.Fatalf("private Git copy changed main: %q %v", main, err)
	}
	return f
}

// The authorized host connects first. After reset, a pinned pending native
// connect proves that the sandbox delivered no connection to this real owner
// endpoint, rather than merely reporting failure against a nonexistent name.
func testOwnerPipe(t *testing.T, owner string) (string, func()) {
	t.Helper()
	name := `\\.\pipe\LOCAL\dwarf-harness-owner-` + uuid.NewV7().String()
	nativeName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := securityDescriptor(owner, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	server, err := windows.CreateNamedPipe(nativeName, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 1024, 1024, 0, &sa)
	runtime.KeepAlive(sd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(server); err != nil {
			t.Error(err)
		}
	})
	client, err := windows.CreateFile(nativeName, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatalf("authorized host pipe connection: %v", err)
	}
	if err := windows.CloseHandle(client); err != nil {
		t.Fatal(err)
	}
	if err := windows.DisconnectNamedPipe(server); err != nil {
		t.Fatal(err)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ov := &windows.Overlapped{HEvent: event}
	var pin runtime.Pinner
	pin.Pin(ov)
	pending := false
	t.Cleanup(func() {
		if pending {
			if err := windows.CancelIoEx(server, ov); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				t.Error(err)
			}
			var transferred uint32
			if err := windows.GetOverlappedResult(server, ov, &transferred, true); err != nil && !errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				t.Error(err)
			}
		}
		pin.Unpin()
		if err := windows.CloseHandle(event); err != nil {
			t.Error(err)
		}
	})
	err = windows.ConnectNamedPipe(server, ov)
	pending = errors.Is(err, windows.ERROR_IO_PENDING)
	if !pending {
		t.Fatalf("owner listener did not reset to a pending native connection: %v", err)
	}
	return name, func() {
		t.Helper()
		state, err := windows.WaitForSingleObject(event, 0)
		if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("sandbox delivered to owner pipe: native wait=%d error=%v", state, err)
		}
	}
}

func testMakeJunction(link, target string) error {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, filepath.Join(system, "cmd.exe"), "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("native junction creation: %w: %s", err, output)
	}
	return nil
}

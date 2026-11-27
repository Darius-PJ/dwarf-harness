package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/host"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/localipc"
)

type childNotice struct {
	PID    int
	HostID uuid.UUID
}

var waitDetachedTestChild = func(pid int, waiting func()) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	waiting()
	state, err := process.Wait()
	if err == nil && state.Success() {
		return errors.New("crash child succeeded")
	}
	return err
}

func TestMain(m *testing.M) {
	if os.Getenv("DWARF_HOST_TEST_HELPER") == "view" {
		control, err := host.Connect(os.Args[1])
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "connected")
		_, _ = io.Copy(io.Discard, os.Stdin)
		_ = control.Close()
		os.Exit(0)
	}
	if os.Getenv("DWARF_HOST_TEST_HELPER") == "command" {
		if address := os.Getenv("DWARF_HOST_TEST_LIFELINE"); address != "" {
			hostCandidateOverride = func(candidate *host.Host) {
				line, err := net.Dial("tcp", address)
				if err != nil {
					os.Exit(2)
				}
				if json.NewEncoder(line).Encode(childNotice{os.Getpid(), candidate.Status().HostID}) != nil {
					os.Exit(2)
				}
				var goAhead [1]byte
				if _, err := io.ReadFull(line, goAhead[:]); err != nil {
					os.Exit(2)
				}
				// Only helper mode has a parent lifeline. A killed test process closes
				// this socket, so even an assertion failure cannot orphan a host.
				go func() { _, _ = io.Copy(io.Discard, line); os.Exit(1) }()
			}
		}
		reaper := os.Getenv("DWARF_HOST_TEST_REAPER") == "1"
		_ = os.Unsetenv("DWARF_HOST_TEST_REAPER")
		code := run(os.Args[1:], os.Stdin, os.Stdout, nil)
		if code == 0 && reaper {
			var pid int
			if _, err := fmt.Fscan(os.Stdin, &pid); err != nil {
				os.Exit(2)
			}
			if err := waitDetachedTestChild(pid, func() { fmt.Fprintln(os.Stdout, "awaiting-host-exit") }); err != nil {
				os.Exit(2)
			}
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

type testHostChild struct {
	childNotice
	line  net.Conn
	ended <-chan struct{}
}
type hostLifeline struct {
	listener   net.Listener
	notices    chan testHostChild
	mu         sync.Mutex
	lines      []net.Conn
	acceptDone chan struct{}
}

func newHostLifeline(t *testing.T, barrier bool) *hostLifeline {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lifeline := &hostLifeline{listener: listener, notices: make(chan testHostChild, 8), acceptDone: make(chan struct{})}
	go func() {
		defer close(lifeline.acceptDone)
		for {
			line, err := listener.Accept()
			if err != nil {
				return
			}
			lifeline.mu.Lock()
			lifeline.lines = append(lifeline.lines, line)
			lifeline.mu.Unlock()
			go func() {
				var notice childNotice
				if json.NewDecoder(line).Decode(&notice) != nil {
					_ = line.Close()
					return
				}
				ended := make(chan struct{})
				if !barrier {
					_, _ = line.Write([]byte{'g'})
				}
				lifeline.notices <- testHostChild{notice, line, ended}
				_, _ = io.Copy(io.Discard, line)
				close(ended)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-lifeline.acceptDone
		lifeline.mu.Lock()
		defer lifeline.mu.Unlock()
		for _, line := range lifeline.lines {
			_ = line.Close()
		}
	})
	return lifeline
}
func (l *hostLifeline) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), "DWARF_HOST_TEST_HELPER=command", "DWARF_HOST_TEST_LIFELINE="+l.listener.Addr().String())
	return cmd
}
func hostFixture(t *testing.T) (string, string) {
	t.Helper()
	root, _, origin := restartPrefix(t, 99)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, integratedStartupSettings(t, root, origin)
}
func requireCommand(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("host command: %v\n%s", err, output)
	}
	return string(output)
}
func connectHost(t *testing.T, root string) *host.Control {
	t.Helper()
	control, err := host.Connect(root)
	if err != nil {
		t.Fatal(err)
	}
	return control
}
func shutdownTestHost(t *testing.T, l *hostLifeline, root string, child testHostChild) string {
	t.Helper()
	output := requireCommand(t, l.command(t, "--shutdown", "--colony-dir", root))
	<-child.ended
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal("shutdown retained writer lock", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return output
}
func TestHostConcurrentStart(t *testing.T) {
	// Helpers own process lifetimes and the fixture uses a credential environment.
	root, settings := hostFixture(t)
	line := newHostLifeline(t, false)
	start := make(chan struct{})
	type result struct {
		output []byte
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		cmd := line.command(t, "--host", "--no-terminal", "--colony-dir", root, "--settings", settings)
		go func() { <-start; output, err := cmd.CombinedOutput(); results <- result{output, err} }()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("simultaneous starts: %v %v\n%s\n%s", first.err, second.err, first.output, second.output)
	}
	child := <-line.notices
	control := connectHost(t, root)
	id := control.Welcome.HostID
	_ = control.Close()
	for _, output := range [][]byte{first.output, second.output} {
		if !bytes.Contains(output, []byte(id.String()+" ready")) {
			t.Fatalf("bootstrap did not report winner %s: %s", id, output)
		}
	}
	if id != child.HostID {
		t.Fatal("different host incarnation")
	}
	if store, _, err := ledgerstore.Open(root, ledger.Defined()); !errors.Is(err, ledgerstore.ErrLocked) {
		if store != nil {
			store.Close()
		}
		t.Fatalf("live host lock = %v", err)
	}
	shutdownTestHost(t, line, root, child)
}
func TestDetachedHostLifecycle(t *testing.T) {
	t.Run("managed", func(t *testing.T) {
		h := newSetupCLI(t)
		_, settings := h.agent(t, "Scout")
		name := "DWARF_MANAGED_HOST_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "_")
		t.Setenv(name, "synthetic-managed-host-credential")
		h.apply(t, h.review(t, "credential bind", map[string]any{"provider": "anthropic", "source_kind": "environment", "source_locator": name}, "--context", "Scout/General work"))
		_, before := h.snapshot(t)
		line := newHostLifeline(t, false)
		output := requireCommand(t, line.command(t, "--host", "--no-terminal", "--colony-dir", h.colony))
		child := <-line.notices
		if !strings.Contains(output, child.HostID.String()+" ready") || strings.Contains(output, name) || strings.Contains(output, "synthetic-managed-host-credential") {
			t.Fatal("managed host did not reach ready with sanitized output", output)
		}
		shutdownTestHost(t, line, h.colony, child)
		_, after := h.snapshot(t)
		if len(after.Credentials) != 1 || after.Credentials[0].CredentialVersion == before.Credentials[0].CredentialVersion || after.CredentialVersions[settings.CredentialScopeID] != before.CredentialVersions[settings.CredentialScopeID]+1 {
			t.Fatal("detached managed startup did not rebind the selected credential once")
		}
		t.Setenv(name, "")
		refused, err := line.command(t, "--host", "--no-terminal", "--colony-dir", h.colony).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("missing managed credential did not refuse: %v %s", err, refused)
		}
		requireSetupFailure(t, exit.ExitCode(), string(refused), "credential_invalid", "credential bind --input --review-out")
		if strings.Contains(string(refused), name) || !strings.Contains(string(refused), settings.CredentialScopeID.String()) {
			t.Fatal("managed host refusal leaked locator or omitted credential scope", string(refused))
		}
		failed := <-line.notices
		<-failed.ended
		_, unchanged := h.snapshot(t)
		if unchanged.ThroughSequence != after.ThroughSequence {
			t.Fatal("refused managed host startup published a credential or turn")
		}
	})
	root, settings := hostFixture(t)
	line := newHostLifeline(t, true)
	cmd := line.command(t, "--host", "--no-terminal", "--colony-dir", root, "--settings", settings)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := <-line.notices
	watcher := connectHost(t, root)
	states := []string{watcher.Welcome.State}
	observed := make(chan error, 1)
	go func() {
		_, err := watcher.AwaitReady(func(status launcher.HostStatusResult) {
			if states[len(states)-1] != status.State {
				states = append(states, status.State)
			}
		})
		observed <- err
	}()
	if _, err := child.line.Write([]byte{'g'}); err != nil {
		t.Fatal(err)
	}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("bootstrap: %v %s", err, output.String())
	}
	if !slices.Equal(states, []string{"booting", "recovering", "ready"}) {
		t.Fatalf("lifecycle = %v", states)
	}
	if !strings.Contains(output.String(), child.HostID.String()+" ready") || !strings.Contains(output.String(), "colony opened") || !strings.Contains(output.String(), "credential") {
		t.Fatal("ready bootstrap omitted startup summary", output.String())
	}
	_ = watcher.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	view := exec.Command(executable, root)
	view.Env = append(os.Environ(), "DWARF_HOST_TEST_HELPER=view")
	input, err := view.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	stdout, err := view.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = view.Process.Kill() })
	if text, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || text != "connected\n" {
		t.Fatalf("view did not attach: %q %v", text, err)
	}
	if err := view.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := view.Wait(); err == nil {
		t.Fatal("killed view reported success")
	}
	survivor := connectHost(t, root)
	if survivor.Welcome.HostID != child.HostID {
		t.Fatal("closing and killing views replaced the host")
	}
	_ = survivor.Close()
	final := shutdownTestHost(t, line, root, child)
	quiescing, stopped := strings.Index(final, " quiescing"), strings.Index(final, " stopped")
	if quiescing < 0 || stopped <= quiescing {
		t.Fatal("shutdown states out of order", final)
	}
}
func TestHostDiscoveryIsAdvisory(t *testing.T) {
	root, settings := hostFixture(t)
	line := newHostLifeline(t, false)
	bootstrap := line.command(t, "--host", "--no-terminal", "--colony-dir", root, "--settings", settings)
	bootstrap.Env = append(bootstrap.Env, "DWARF_HOST_TEST_REAPER=1")
	input, err := bootstrap.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	outputPipe, err := bootstrap.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = bootstrap.Process.Kill()
			_ = bootstrap.Wait()
		}
	})
	first := <-line.notices
	if _, err := fmt.Fprintln(input, first.PID); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(outputPipe)
	waiting := false
	for scanner.Scan() {
		if scanner.Text() == "awaiting-host-exit" {
			waiting = true
			break
		}
	}
	if !waiting {
		t.Fatal("bootstrap did not retain the crash child's wait", scanner.Err())
	}
	process, err := os.FindProcess(first.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Release()
	if err := bootstrap.Wait(); err != nil {
		t.Fatal("bootstrap did not positively wait the crash child", err)
	}
	waited = true
	<-first.ended
	output := requireCommand(t, line.command(t, "--host", "--no-terminal", "--colony-dir", root, "--settings", settings))
	second := <-line.notices
	if first.HostID == second.HostID || !strings.Contains(output, "previous_host_unclean") {
		t.Fatal("unclean restart did not change incarnation and report recovery", output)
	}
	owner, err := localipc.CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	colony, err := ledgerstore.ReadColony(root)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := owner.ReadDiscovery(root, colony)
	if err != nil || discovery.HostID != second.HostID {
		t.Fatal("stale discovery was not replaced", err)
	}
	path := filepath.Join(root, "run", "host.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	forged := discovery
	forged.Endpoint = "another-endpoint"
	altered, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, altered, 0600); err != nil {
		t.Fatal(err)
	}
	refused, err := line.command(t, "--host", "--no-terminal", "--colony-dir", root, "--settings", settings).CombinedOutput()
	if err == nil || !bytes.Contains(refused, []byte("non_host_holder")) {
		t.Fatalf("forged discovery start: %v %s", err, refused)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	control := connectHost(t, root)
	if control.Welcome.HostID != second.HostID {
		t.Fatal("forged hint started another Core")
	}
	_ = control.Close()
	shutdownTestHost(t, line, root, second)
}
func TestStartRefusalLines(t *testing.T) {
	line := newHostLifeline(t, false)
	root, settings := hostFixture(t)
	for _, row := range []struct{ name, colony, settings, token string }{
		{"not_colony", t.TempDir(), settings, "startup refused: colony: code=storage_unavailable "},
		{"settings_unreadable", root, filepath.Join(t.TempDir(), "missing.json"), "startup refused: settings: code=review_invalid "},
	} {
		t.Run(row.name, func(t *testing.T) {
			output, err := line.command(t, "--host", "--no-terminal", "--colony-dir", row.colony, "--settings", row.settings).CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !bytes.Contains(output, []byte(row.token)) {
				t.Fatalf("startup refusal: %v %s", err, output)
			}
			if bytes.Contains(output, []byte(row.colony)) || bytes.Contains(output, []byte(row.settings)) {
				t.Fatal("startup refusal disclosed path")
			}
		})
	}
	output, err := line.command(t, "--shutdown", "--colony-dir", root).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !bytes.Contains(output, []byte("no_host")) {
		t.Fatalf("absent shutdown: %v %s", err, output)
	}
}

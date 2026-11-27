package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/launcher"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func TestMain(m *testing.M) {
	switch os.Getenv("DWARF_HOST_FAULT_HELPER") {
	case "shutdown":
		os.Exit(Shutdown(os.Args[1], os.Stdout))
	case "serve":
		// Only this helper has a parent lifeline, on its inherited stdin pipe.
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); os.Exit(2) }()
		settings, err := ReadSettings(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		var captured *agents.Service
		calls := 0
		workspaceCloseOverride = func(service *agents.Service, closeWorkspace func() error) error {
			calls++
			captured = service
			if err := closeWorkspace(); err != nil {
				return err
			}
			return errors.New("synthetic unconfirmed closure detail")
		}
		candidate, failure := NewCandidate(os.Args[1])
		if failure != nil {
			os.Exit(2)
		}
		configuration := Configuration{Load: func(context.Context, *ledgerstore.Store) (terminal.Settings, bool, error) {
			settings, err := ReadSettings(os.Args[2])
			return settings, false, err
		}}
		if failure := candidate.Start(context.Background(), configuration); failure != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "ready")
		err = candidate.Wait()
		if err == nil || calls != 1 || captured == nil || candidate.Status().Code != launcher.ClosureUnconfirmed {
			os.Exit(2)
		}
		binding := agents.Binding{AgentID: settings.Contexts[0].AgentID, ContextID: settings.Contexts[0].ContextID}
		view, closedErr := captured.OpenSession(context.Background(), binding)
		if view != nil {
			view.Close()
		}
		if !errors.Is(closedErr, agents.ErrClosed) {
			os.Exit(2)
		}
		store, _, openErr := ledgerstore.Open(os.Args[1], ledger.Defined())
		if openErr != nil || store.Close() != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func compositionFixture(t *testing.T) (string, terminal.Settings, agents.Binding) {
	t.Helper()
	ctx := context.Background()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "colony")
	store, err := ledgerstore.Create(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	service, _, err := agents.Open(ctx, store, root, agents.DefaultLimits())
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	binding := agents.Binding{AgentID: uuid.NewV7(), ContextID: uuid.NewV7()}
	_, err = service.RegisterAgent(ctx, agents.AgentRegistration{CommandID: uuid.NewV7(), AgentID: binding.AgentID, SessionID: uuid.NewV7(), ContextID: binding.ContextID, Directory: "Scout-0", AgentMarkdown: []byte("# Synthetic instructions\n")})
	if err := errors.Join(err, service.Close(), store.Close()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DWARF_HOST_SYNTHETIC_SOURCE", "synthetic-host-test-only")
	scope := uuid.NewV7()
	settings := terminal.Settings{Contexts: []terminal.ContextSettings{{AgentID: binding.AgentID, ContextID: binding.ContextID, RouteID: uuid.NewV7(), ExpectedRouteVersion: 1, CredentialScopeID: scope, MaxOutputTokens: 32, DisplayTitle: terminal.GeneralTitle, Policy: terminal.DefaultPolicy()}}, CredentialBindings: []terminal.CredentialBinding{{ScopeID: scope, Provider: "anthropic", SourceKind: "environment", SourceLocator: "DWARF_HOST_SYNTHETIC_SOURCE"}}}
	return root, settings, binding
}
func recordCount(t *testing.T, root string) uint64 {
	t.Helper()
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reader, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: ledger.ToEnd, AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var count uint64
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return count
		}
		if err != nil {
			t.Fatal(err)
		}
		count += uint64(len(commit.Records))
	}
}
func TestServiceStartupRefusal(t *testing.T) {
	t.Run("workspace_unconfirmed", func(t *testing.T) {
		// Migrated from cmd: the native failure must stop before any recovery,
		// credential publication or scratch sweep. The hook is private to host.
		root, settings, _ := compositionFixture(t)
		before := recordCount(t, root)
		file := filepath.Join(root, "scratch", "startup-sentinel", "keep.txt")
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("retained scratch"), 0600); err != nil {
			t.Fatal(err)
		}
		workspaceOpenOverride = func(context.Context, *ledgerstore.Store, string, workspace.Config) (*workspace.Service, workspace.Reconciliation, error) {
			return nil, workspace.Reconciliation{}, &gitprocess.UnconfirmedError{Reason: "private native detail"}
		}
		defer func() { workspaceOpenOverride = nil }()
		var output bytes.Buffer
		core := NewComposition(root, Configuration{Load: func(context.Context, *ledgerstore.Store) (terminal.Settings, bool, error) {
			return settings, false, nil
		}}, &output)
		failure := core.Compose(context.Background(), nil)
		if err := core.Close(); err != nil {
			t.Fatal(err)
		}
		if failure == nil || failure.Stage != "workspace" || failure.Class != "process_unconfirmed" || !slices.Contains(failure.Conditions, "process_unconfirmed") {
			t.Fatalf("workspace startup refusal: %+v", failure)
		}
		if strings.Contains(output.String(), "colony opened") || strings.Contains(output.String(), "private native detail") || !strings.Contains(output.String(), "process_unconfirmed") {
			t.Fatal("workspace refusal entered recovery or leaked native detail", output.String())
		}
		if raw, err := os.ReadFile(file); err != nil || string(raw) != "retained scratch" {
			t.Fatal("refused startup swept private state", err)
		}
		if recordCount(t, root) != before {
			t.Fatal("refused startup appended recovery, binding or invented evidence")
		}
	})
}
func TestHostShutdownRefusals(t *testing.T) {
	// One real helper host covers stale incarnation without draining, then the
	// existing ticket-16 close override with the production shutdown entry.
	root, settings, _ := compositionFixture(t)
	ctx := settings.Contexts[0]
	raw := fmt.Sprintf(`{"schema":1,"contexts":[{"agent_id":%q,"context_id":%q,"route_id":%q,"expected_route_version":1,"credential_scope_id":%q,"max_output_tokens":32,"display_title":"General work","turn_policy":{}}],"credential_bindings":[{"scope_id":%q,"provider":"anthropic","source_kind":"environment","source_locator":"DWARF_HOST_SYNTHETIC_SOURCE"}]}`, ctx.AgentID, ctx.ContextID, ctx.RouteID, ctx.CredentialScopeID, ctx.CredentialScopeID)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, root, path)
	child.Env = append(os.Environ(), "DWARF_HOST_FAULT_HELPER=serve")
	lifeline, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer lifeline.Close()
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("host did not reach ready: %q %v", line, err)
	}
	control, err := Connect(root)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	t.Run("stale_host", func(t *testing.T) {
		response, err := control.Call("host.shutdown", launcher.HostShutdown{}, uuid.NewV7(), &launcher.Expected{Kind: "host", ID: uuid.NewV7(), Revision: 3}, nil)
		if err != nil || response.Code != launcher.StaleRevision {
			t.Fatalf("foreign incarnation: %+v %v", response, err)
		}
		response, err = control.Call("host.status", launcher.HostStatus{}, uuid.UUID{}, nil, nil)
		if err != nil || response.Code != launcher.OK || response.Result.(launcher.HostStatusResult).State != "ready" {
			t.Fatal("stale shutdown drained host", response, err)
		}
		if store, _, err := ledgerstore.Open(root, ledger.Defined()); !errors.Is(err, ledgerstore.ErrLocked) {
			if store != nil {
				store.Close()
			}
			t.Fatal("stale shutdown released writer lock", err)
		}
	})
	_ = control.Close()
	t.Run("shutdown_unconfirmed", func(t *testing.T) {
		shutdown := exec.Command(executable, root)
		shutdown.Env = append(os.Environ(), "DWARF_HOST_FAULT_HELPER=shutdown")
		output, err := shutdown.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || bytes.Count(output, []byte("closure_unconfirmed:")) != 1 || bytes.Contains(output, []byte(" stopped")) {
			t.Fatalf("closure refusal: %v %s", err, output)
		}
		for _, forbidden := range []string{root, path, "synthetic unconfirmed closure detail"} {
			if bytes.Contains(output, []byte(forbidden)) {
				t.Fatal("closure refusal disclosed private detail")
			}
		}
		err = child.Wait()
		waited = true
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal("host did not preserve close count, service-before-workspace order, store drain and closure failure", err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal("failed drain retained store", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

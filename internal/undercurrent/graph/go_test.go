package graph

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func verifyGoFixtureTool(t *testing.T, name, version string) *VerifiedTool {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewToolVerification(Tool{Path: name, SHA256: ledger.Digest(sha256.Sum256(body)), Version: version})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	for {
		progress, err := v.Step(t.Context(), maxItems)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Verified != nil {
			return progress.Verified
		}
	}
}

func installedGoTools(t *testing.T) *GoTools {
	t.Helper()
	helper, tool, goroot := os.Getenv("DWARF_GRAPH_GO_HELPER"), os.Getenv("DWARF_GRAPH_GO"), os.Getenv("DWARF_GRAPH_GOROOT")
	if helper == "" && tool == "" && goroot == "" {
		t.Skip("native Go profile not exercised: all three DWARF_GRAPH_GO variables are unset")
	}
	if !filepath.IsAbs(helper) || !filepath.IsAbs(tool) || !filepath.IsAbs(goroot) {
		t.Fatal("partial or non-absolute native Go profile")
	}
	g := &GoTools{Helper: verifyGoFixtureTool(t, helper, goHelperVersion), Toolchain: verifyGoFixtureTool(t, tool, "go1.27.2"), GOROOT: goroot, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if !validGoConfig(*g.config()) {
		t.Fatal("native Go profile violates its root/bin relation")
	}
	return g
}

func TestGoConfigurationIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	tool := filepath.Join(root, "bin", "go"+goExecutableSuffix())
	if err := os.MkdirAll(filepath.Dir(tool), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("configuration-only tool"), 0600); err != nil {
		t.Fatal(err)
	}
	verified := verifyGoFixtureTool(t, tool, "go1.27.2")
	g := &GoTools{Helper: verified, Toolchain: verified, GOROOT: root, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	config := ToolConfig{Go: g.config()}
	want := config.ConfigurationDigest()
	if got := ConfigurationDigest(Tools{Go: g}); got != want || got == ConfigurationDigest(Tools{}) {
		t.Fatalf("verified/unverified identities disagree or omit Go: %s %s", got, want)
	}
	for name, change := range map[string]func(*GoToolConfig){
		"helper bytes":   func(c *GoToolConfig) { c.Helper.SHA256 = digest("changed helper") },
		"helper version": func(c *GoToolConfig) { c.Helper.Version = "go-analyzer/v2" },
		"go bytes":       func(c *GoToolConfig) { c.Toolchain.SHA256 = digest("changed go") },
		"go version":     func(c *GoToolConfig) { c.Toolchain.Version = "go1.27.3" },
		"root and path": func(c *GoToolConfig) {
			c.GOROOT += "-other"
			c.Toolchain.Path = filepath.Join(c.GOROOT, "bin", "go"+goExecutableSuffix())
		},
		"target": func(c *GoToolConfig) {
			if c.GOARCH == "arm64" {
				c.GOARCH = "amd64"
			} else {
				c.GOARCH = "arm64"
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed := *config.Go
			change(&changed)
			if (ToolConfig{Go: &changed}).ConfigurationDigest() == want {
				t.Fatal("Go configuration change retained its analyzer epoch identity")
			}
		})
	}
	for name, change := range map[string]func(*GoToolConfig){
		"missing helper":    func(c *GoToolConfig) { c.Helper = Tool{} },
		"missing toolchain": func(c *GoToolConfig) { c.Toolchain = Tool{} },
		"missing root":      func(c *GoToolConfig) { c.GOROOT = "" },
		"wrong root":        func(c *GoToolConfig) { c.GOROOT += "-other" },
		"relative tool":     func(c *GoToolConfig) { c.Toolchain.Path = "go" },
		"missing target":    func(c *GoToolConfig) { c.GOOS = "" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newGraphFixture(t)
			options := fixture.options()
			changed := *config.Go
			change(&changed)
			options.Tools.Go = &changed
			service, err := Open(options)
			if service != nil {
				_ = service.Close()
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("partial Go configuration was not refused: %v", err)
			}
		})
	}
	fixture := newGraphFixture(t)
	options := fixture.options()
	missing := *config.Go
	missing.Helper.Path = filepath.Join(root, "uninstalled-helper"+goExecutableSuffix())
	options.Tools.Go = &missing
	service, err := Open(options)
	if err != nil {
		t.Fatalf("Open performed a forbidden tool lookup: %v", err)
	}
	defer service.Close()
	if _, err := service.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func fixtureGoTools(t *testing.T) *GoTools {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	tool := filepath.Join(root, "bin", "go"+goExecutableSuffix())
	if err := os.Mkdir(filepath.Dir(tool), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, body, 0700); err != nil {
		t.Fatal(err)
	}
	// The copy satisfies the actual pin verifier; the protocol child never
	// executes it or substitutes a parsed import for native resolution.
	return &GoTools{Helper: verifyGoFixtureTool(t, executable, goHelperVersion), Toolchain: verifyGoFixtureTool(t, tool, "go1.27.2"), GOROOT: root, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func assertGoScopeUnknown(t *testing.T, request AnalysisRequest, result AnalysisResult, reason string) {
	t.Helper()
	if len(result.Nodes) != 0 || len(result.Edges) != 0 || len(result.Coverage) != 1 {
		t.Fatalf("scope failure leaked partial output: %+v", result)
	}
	want := ledger.CoverageEntry{Scope: workspaceScope(request.Source.WorkspaceID), Adapter: "go", State: ledger.CoverageUnknown, Reasons: []string{reason}}
	got := result.Coverage[0]
	if got.Scope != want.Scope || got.Adapter != want.Adapter || got.State != want.State || !slices.Equal(got.Reasons, want.Reasons) {
		t.Fatalf("scope failure: %+v, want %+v", got, want)
	}
}

func TestGoForcedStopFencesHost(t *testing.T) {
	t.Parallel()
	if mode := os.Getenv("DWARF_GRAPH_GO_FENCE_HOST"); mode != "" {
		goFenceHost(t, mode)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cancel", "overflow", "go-crash", "go-unframed", "go-malformed", "go-exit-2", "go-unknown", "go-refused"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			ownedRoot := t.TempDir()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestGoForcedStopFencesHost$", "-test.v")
			command.Env = append(os.Environ(), "DWARF_GRAPH_GO_FENCE_HOST="+mode, "TEMP="+ownedRoot, "TMP="+ownedRoot, "TMPDIR="+ownedRoot)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("isolated fence host: %v\n%s", err, output)
			}
			retained, err := filepath.Glob(filepath.Join(ownedRoot, "graph-helper-*"))
			if err != nil || len(retained) != 0 {
				t.Fatalf("reaped fence fixture left helper directories: %v, %v", retained, err)
			}
			t.Logf("isolated %s:\n%s", mode, output)
		})
	}
}

func goFenceHost(t *testing.T, mode string) {
	t.Helper()
	tools := Tools{Go: fixtureGoTools(t)}
	request := analysisFixture(map[string]string{"a.go": "package a\n", "go.mod": "module example.test/a\n\ngo 1.27\n"})
	request.ConfigurationSHA256 = ConfigurationDigest(tools)
	fixture := newGraphFixture(t)
	id := graphFixtureID(1)
	fixture.register(id)
	tree := fixture.tree("old readable image", map[string]string{"stable.md": "stable"})
	fixture.accept(id, fixture.tree("empty", nil), tree, "stable.md")
	service := fixture.open(serviceDeps{})
	before := driveGraph(t, service)
	oldPageRequest := PageRequest{Version: before.Version, Digest: before.Digest, Through: before.Through, ChainHash: before.ChainHash, Kind: "nodes", Limit: 256}
	oldPage, err := service.Page(oldPageRequest)
	if err != nil || len(oldPage.Nodes) != 1 {
		t.Fatalf("initial readable image: %+v, %v", oldPage, err)
	}
	analyzer := NewAnalyzer(tools)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	childMode := mode
	var listener net.Listener
	if mode == "cancel" {
		childMode = "go-pause"
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
	}
	var owned string
	analyzer.deps.environment = func(dir string) []string {
		owned = dir
		env := append(helperEnvironment(dir), "DWARF_GRAPH_TEST_CHILD="+childMode)
		if listener != nil {
			env = append(env, "DWARF_GRAPH_TEST_READY="+listener.Addr().String())
		}
		return env
	}
	if listener != nil {
		analyzer.deps.afterChildStart = func() {
			if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			connection, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if err := connection.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if line, err := bufio.NewReader(connection).ReadString('\n'); err != nil || line == "" {
				t.Fatalf("held helper handshake: %q, %v", line, err)
			}
			// These operations run while the helper is known to be held.
			if page, err := service.Page(oldPageRequest); err != nil || !slices.Equal(page.Nodes, oldPage.Nodes) {
				t.Fatalf("held helper blocked the old image: %+v, %v", page, err)
			}
			fixture.register(graphFixtureID(2))
			cancel()
		}
	}
	var concurrentDone chan error
	var releaseConcurrent chan struct{}
	if listener != nil {
		// A second instance starts before cancellation, but cannot return its
		// valid result until the first invocation has installed the host fence.
		concurrent := NewAnalyzer(tools)
		concurrent.deps.environment = func(dir string) []string {
			return append(helperEnvironment(dir), "DWARF_GRAPH_TEST_CHILD=go-valid")
		}
		started := make(chan struct{})
		releaseConcurrent = make(chan struct{})
		concurrentCtx, stop := context.WithCancel(t.Context())
		concurrent.deps.afterChildStart = func() {
			close(started)
			select {
			case <-releaseConcurrent:
			case <-concurrentCtx.Done():
			}
		}
		concurrentDone = make(chan error, 1)
		go func() {
			out, err := concurrent.Analyze(concurrentCtx, "go", request)
			if !errors.Is(err, ErrRecovery) || len(out.Nodes) != 0 || len(out.Edges) != 0 || len(out.Coverage) != 0 {
				concurrentDone <- fmt.Errorf("in-flight analysis escaped the host fence: %+v, %v", out, err)
				return
			}
			concurrentDone <- nil
		}()
		defer func() {
			stop()
			if concurrentDone != nil {
				<-concurrentDone
			}
		}()
		select {
		case <-started:
		case err := <-concurrentDone:
			concurrentDone = nil
			t.Fatalf("concurrent invocation failed before Start: %v", err)
		}
	}
	result, err := analyzer.Analyze(ctx, "go", request)
	// Analyze has synchronously reaped this re-exec fixture, which starts no
	// descendants. Keep asserting quarantine below, then remove only its files.
	t.Cleanup(func() {
		if err := os.RemoveAll(owned); err != nil {
			t.Errorf("remove reaped helper fixture: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if concurrentDone != nil {
		close(releaseConcurrent)
		concurrentErr := <-concurrentDone
		concurrentDone = nil
		if concurrentErr != nil {
			t.Fatal(concurrentErr)
		}
	}
	if mode == "go-unknown" || mode == "go-refused" {
		reason := ledger.ReasonHelperLimit
		if mode == "go-refused" {
			reason = ledger.ReasonAdapterUnavailable
		}
		assertGoScopeUnknown(t, request, result, reason)
		if analysisFence.Load() {
			t.Fatal("joined unknown or pre-native refusal fenced the host")
		}
		if _, err := os.Stat(owned); !os.IsNotExist(err) {
			t.Fatalf("joined invocation retained its directory: %v", err)
		}
		next := NewAnalyzer(tools)
		next.deps.environment = func(dir string) []string { return append(helperEnvironment(dir), "DWARF_GRAPH_TEST_CHILD=go-valid") }
		valid, err := next.Analyze(t.Context(), "go", request)
		if err != nil {
			t.Fatal(err)
		}
		assertCoverage(t, request, valid, "go", map[string]string{"a.go": ""})
		return
	}
	assertGoScopeUnknown(t, request, result, ledger.ReasonAdapterUnavailable)
	if _, err := os.Stat(filepath.Join(owned, "corpus", "a.go")); err != nil {
		t.Fatalf("uncertain invocation was not quarantined: %v", err)
	}
	t.Logf("quarantined until reaped fixture cleanup: %s", owned)
	fresh := NewAnalyzer(tools)
	fresh.deps.afterChildStart = func() { t.Fatal("a fresh analyzer launched after the host fence") }
	if _, err := fresh.Analyze(t.Context(), "go", request); !errors.Is(err, ErrRecovery) {
		t.Fatalf("new analyzer cleared the host fence: %v", err)
	}
	if _, err := fresh.Analyze(t.Context(), ledger.AdapterPython, request); !errors.Is(err, ErrRecovery) {
		t.Fatalf("Python escaped the host fence: %v", err)
	}
	pure := analysisFixture(map[string]string{"query.sql": "CREATE TABLE widgets (id INT); SELECT * FROM widgets;"})
	opaque, err := AnalyzeOpaque(t.Context(), pure)
	if err != nil || len(opaque.Nodes) != 1 || opaque.Nodes[0].Key != pure.Corpus[0].Key || opaque.Nodes[0].VersionSHA256 != pure.Corpus[0].SHA256 {
		t.Fatalf("host fence suppressed pure artifact presence: %+v, %v", opaque, err)
	}
	sql, err := AnalyzeSQL(t.Context(), pure)
	if err != nil || len(sql.Nodes) != 1 || len(sql.Edges) != 1 || sql.Nodes[0].Key.Kind != ledger.NodeKindModule || sql.Edges[0].From != sql.Nodes[0].Key || sql.Edges[0].To != pure.Corpus[0].Key {
		t.Fatalf("host fence suppressed pure SQL dependency evidence: %+v, %v", sql, err)
	}
	fixture.register(graphFixtureID(3)) // The independent writer stays usable.
	options := fixture.options()
	options.ReadArtifacts = func(context.Context, ArtifactRequest) (ArtifactBatch, error) {
		t.Fatal("fenced graph read Git")
		return ArtifactBatch{}, ErrRecovery
	}
	reopened, err := Open(options)
	if err != nil {
		t.Fatalf("fence prevented ledger-only replay: %v", err)
	}
	defer reopened.Close()
	durable := fixture.writer.Durable()
	status, err := reopened.Step(t.Context())
	if !errors.Is(err, ErrRecovery) || status.State != "recovery_pending" || fixture.writer.Durable() != durable {
		t.Fatalf("new service admitted fenced work: %+v, %v", status, err)
	}
	if _, err := reopened.Resume(t.Context(), uuid.NewV7()); !errors.Is(err, ErrRecovery) || fixture.writer.Durable() != durable {
		t.Fatalf("Resume admitted fenced work: %v", err)
	}
	status, err = reopened.Observe(t.Context())
	if err != nil || status.Version != before.Version || status.Digest != before.Digest {
		t.Fatalf("fence damaged replay or observation: %+v, %v", status, err)
	}
	page, err := reopened.Page(PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: "nodes", Limit: 256})
	if err != nil || !slices.Equal(page.Nodes, oldPage.Nodes) {
		t.Fatalf("fence damaged historical pages: %+v, %v", page, err)
	}
}

func TestGoAnalyzerTempAliases(t *testing.T) {
	t.Parallel()
	tools := Tools{Go: installedGoTools(t)}
	if os.Getenv("DWARF_GRAPH_GO_TEMP_ALIAS_CHILD") == "1" {
		request := analysisFixture(map[string]string{
			"go.mod":     "module example.test/alias\n\ngo 1.27\n",
			"a.go":       "package alias\nimport _ \"example.test/alias/dep\"\n",
			"dep/dep.go": "package dep\n",
		})
		request.ConfigurationSHA256 = ConfigurationDigest(tools)
		result, err := NewAnalyzer(tools).Analyze(t.Context(), "go", request)
		if err != nil || len(result.Nodes) != 1 || len(result.Edges) != 1 {
			t.Fatalf("aliased temporary root lost native dependencies: %+v, %v", result, err)
		}
		assertCoverage(t, request, result, "go", map[string]string{"a.go": "", "dep/dep.go": ""})
		entries, err := os.ReadDir(os.TempDir())
		if err != nil || len(entries) != 0 {
			t.Fatalf("analysis retained owned temporary files: %v, %v", entries, err)
		}
		return
	}
	kinds := []string{"symlink"}
	if runtime.GOOS == "windows" {
		kinds = []string{"short"}
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			alias := goTempAlias(t)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestGoAnalyzerTempAliases$", "-test.v", "-test.count=1")
			command.Env = append(os.Environ(), "DWARF_GRAPH_GO_TEMP_ALIAS_CHILD=1", "TEMP="+alias, "TMP="+alias, "TMPDIR="+alias)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("native analysis through %s: %v\n%s", kind, err, output)
			}
		})
	}
}

func TestGoPinsAndEnvironmentAreBinding(t *testing.T) {
	t.Parallel()
	good := installedGoTools(t)
	if !slices.Contains(goConfiguration(good.config()).Environment, [2]string{"GODEBUG", "goindex=0"}) {
		t.Fatal("Go configuration must bind GODEBUG=goindex=0")
	}
	request := analysisFixture(map[string]string{
		"go.mod":      "module example.test/pins\n\ngo 1.27\n",
		"a.go":        "package pins\nimport _ \"example.test/pins/dep\"\n",
		"dep/dep.go":  "package dep\n",
		"standard.go": "package pins\nimport _ \"unsafe\"\n",
	})
	checkNative := func(t *testing.T) {
		t.Helper()
		tools := Tools{Go: good}
		request.ConfigurationSHA256 = ConfigurationDigest(tools)
		result, err := NewAnalyzer(tools).Analyze(t.Context(), "go", request)
		if err != nil || len(result.Nodes) != 1 || len(result.Edges) != 1 {
			t.Fatalf("pinned native topology: %+v, %v", result, err)
		}
		assertCoverage(t, request, result, "go", map[string]string{"a.go": "", "dep/dep.go": "", "standard.go": ""})
	}
	if os.Getenv("DWARF_GRAPH_GO_ENV_HOST") != "" {
		checkNative(t)
		return
	}
	checkNative(t)
	for _, role := range []string{"helper", "toolchain"} {
		t.Run(role+" bad digest", func(t *testing.T) {
			t.Parallel()
			identity := good.Helper.Identity()
			if role == "toolchain" {
				identity = good.Toolchain.Identity()
			}
			identity.SHA256 = digest("wrong pin")
			verification, err := NewToolVerification(identity)
			if err != nil {
				t.Fatal(err)
			}
			defer verification.Close()
			for {
				progress, err := verification.Step(t.Context(), maxItems)
				if progress.Verified != nil {
					t.Fatal("incorrect digest yielded a launchable tool")
				}
				if err != nil {
					if !errors.Is(err, ErrInvalid) {
						t.Fatal(err)
					}
					break
				}
			}
		})
	}
	for _, role := range []string{"helper", "toolchain"} {
		t.Run(role+" wrong version", func(t *testing.T) {
			t.Parallel()
			changed := *good
			if role == "helper" {
				changed.Helper = verifyGoFixtureTool(t, good.Helper.Identity().Path, "go-analyzer/v99")
			} else {
				changed.Toolchain = verifyGoFixtureTool(t, good.Toolchain.Identity().Path, "go1.27.3")
			}
			tools := Tools{Go: &changed}
			r := request
			r.ConfigurationSHA256 = ConfigurationDigest(tools)
			out, err := NewAnalyzer(tools).Analyze(t.Context(), "go", r)
			if err != nil {
				t.Fatal(err)
			}
			assertGoScopeUnknown(t, r, out, ledger.ReasonAdapterUnavailable)
		})
	}
	for _, mode := range []string{"missing", "wrong"} {
		t.Run("telemetry "+mode, func(t *testing.T) {
			t.Parallel()
			fake := fixtureGoTools(t)
			fake.Helper = good.Helper
			tools := Tools{Go: fake}
			r := request
			r.ConfigurationSHA256 = ConfigurationDigest(tools)
			a := NewAnalyzer(tools)
			a.deps.environment = func(root string) []string {
				name := filepath.Join(root, filepath.FromSlash(goTelemetryRelative()))
				var err error
				if mode == "missing" {
					err = os.Remove(name)
				} else {
					err = os.WriteFile(name, []byte("local"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				return goEnvironment(*fake.config(), root)
			}
			out, err := a.Analyze(t.Context(), "go", r)
			if err != nil {
				t.Fatal(err)
			}
			assertGoScopeUnknown(t, r, out, ledger.ReasonAdapterUnavailable)
			if _, err := os.Stat(filepath.Join(fake.GOROOT, "bin", "probe-touched")); !os.IsNotExist(err) {
				t.Fatalf("native tool ran without telemetry-off scaffold: %v", err)
			}
		})
	}
	t.Run("replacement after verification", func(t *testing.T) {
		t.Parallel()
		fake := fixtureGoTools(t)
		fake.Helper = good.Helper
		if err := os.WriteFile(fake.Toolchain.Identity().Path, []byte("replaced"), 0600); err != nil {
			t.Fatal(err)
		}
		tools := Tools{Go: fake}
		r := request
		r.ConfigurationSHA256 = ConfigurationDigest(tools)
		a := NewAnalyzer(tools)
		a.deps.afterChildStart = func() { t.Fatal("replaced tool reached helper launch") }
		out, err := a.Analyze(t.Context(), "go", r)
		if err != nil {
			t.Fatal(err)
		}
		assertGoScopeUnknown(t, r, out, ledger.ReasonAdapterUnavailable)
	})
	t.Run("polluted ambient environment", func(t *testing.T) {
		t.Parallel()
		fake := fixtureGoTools(t)
		executable := fake.Helper.Identity().Path
		driver := filepath.Join(fake.GOROOT, "bin", "driver"+goExecutableSuffix())
		body, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(driver, body, 0700); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), executable, "-test.run=^TestGoPinsAndEnvironmentAreBinding$", "-test.v")
		command.Env = append(os.Environ(), "DWARF_GRAPH_GO_ENV_HOST=1", "PATH="+filepath.Dir(driver), "GOPACKAGESDRIVER="+driver, "GOFLAGS=-not-a-valid-flag", "GOWORK="+filepath.Join(fake.GOROOT, "untrusted.work"), "GODEBUG=goindex=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("ambient overrides changed native resolution: %v\n%s", err, output)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(driver), "probe-touched")); !os.IsNotExist(err) {
			t.Fatalf("ambient tool/driver executed: %v", err)
		}
	})
}

func TestGoHelperBoundsAndCoverageExpansion(t *testing.T) {
	t.Parallel()
	// P9 deliberately supersedes expansion: one scope entry covers even the
	// final inventory file. The consumer must never interpret absence as complete.
	for _, count := range []int{maxItems, maxItems + 1} {
		t.Run(fmt.Sprintf("inventory_%d", count), func(t *testing.T) {
			t.Parallel()
			files := make(map[string]string, count)
			for i := range count {
				files[fmt.Sprintf("f%05d.go", i)] = ""
			}
			request := analysisFixture(files)
			request.ConfigurationSHA256 = ConfigurationDigest(Tools{})
			result, err := NewAnalyzer(Tools{}).Analyze(t.Context(), "go", request)
			if err != nil {
				t.Fatal(err)
			}
			reason := ledger.ReasonAdapterUnavailable
			if count > maxItems {
				reason = ledger.ReasonHelperLimit
			}
			assertGoScopeUnknown(t, request, result, reason)
			for _, file := range request.Corpus {
				state := result.Coverage[0]
				for _, entry := range result.Coverage {
					if entry.Scope == keyScope(file.Key) {
						state = entry
					}
				}
				if state.State != ledger.CoverageUnknown {
					t.Fatalf("unreported inventory file became complete: %s", file.Path)
				}
			}
		})
	}
	for _, size := range []int{maxBlob, maxBlob + 1} {
		t.Run(fmt.Sprintf("blob_%d", size), func(t *testing.T) {
			t.Parallel()
			request := analysisFixture(map[string]string{"a.go": strings.Repeat(" ", size)})
			err := validateRequest(request)
			if size == maxBlob && err != nil || size > maxBlob && !errors.Is(err, errCorpusLimit) {
				t.Fatalf("blob boundary %d: %v", size, err)
			}
		})
	}
	for _, missing := range []string{"a.go", "go.mod", "nested/go.sum", "go.work", "vendor/license.txt", "nested/vendor/data.bin", "readme.md"} {
		t.Run("unavailable "+missing, func(t *testing.T) {
			t.Parallel()
			fixture := newGraphFixture(t)
			id := graphFixtureID(1)
			fixture.register(id)
			files := map[string]string{"a.go": "package a\n", "go.mod": "module example.test/a\n\ngo 1.27\n", missing: ""}
			tree := fixture.tree("unavailable "+missing, files)
			for i := range fixture.trees[tree] {
				if fixture.trees[tree][i].Path == missing {
					fixture.trees[tree][i].Reason = ledger.ReasonSourceUnavailable
					fixture.trees[tree][i].SHA256 = ledger.Digest{}
					fixture.trees[tree][i].Bytes = nil
				}
			}
			var paths []string
			for path := range files {
				paths = append(paths, path)
			}
			fixture.accept(id, fixture.tree("empty", nil), tree, paths...)
			service := fixture.open(serviceDeps{afterChildStart: func() { t.Fatal("unavailable Go input launched a helper") }})
			status := driveGraph(t, service)
			page, err := service.Page(PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: "coverage", Limit: 256})
			if err != nil {
				t.Fatal(err)
			}
			var entries []ledger.CoverageEntry
			for _, entry := range page.Coverage {
				if entry.Adapter == "go" {
					entries = append(entries, entry)
				}
			}
			reason := ledger.ReasonSourceUnavailable
			if missing == "readme.md" {
				reason = ledger.ReasonAdapterUnavailable // Unrelated omission did not refuse Go.
			}
			assertGoScopeUnknown(t, AnalysisRequest{Source: ledger.GraphSource{WorkspaceID: id}}, AnalysisResult{Coverage: entries}, reason)
		})
	}
	t.Run("no case-sensitive Go inventory", func(t *testing.T) {
		t.Parallel()
		fixture := newGraphFixture(t)
		id := graphFixtureID(1)
		fixture.register(id)
		tree := fixture.tree("not Go", map[string]string{"a.GO": "package a\n", "go.mod": "module example.test/a\n"})
		fixture.accept(id, fixture.tree("empty", nil), tree, "a.GO", "go.mod")
		service := fixture.open(serviceDeps{afterChildStart: func() { t.Fatal("no Go inventory launched a helper") }})
		status := driveGraph(t, service)
		page, err := service.Page(PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: "coverage", Limit: 256})
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(page.Coverage, func(entry ledger.CoverageEntry) bool { return entry.Adapter == "go" }) {
			t.Fatal("Go scope was fabricated without a Go inventory")
		}
	})
}

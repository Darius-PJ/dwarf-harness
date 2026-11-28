package validation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

var toolchainFixture struct {
	once     sync.Once
	root     string
	manifest []byte
	err      error
}

func pinnedToolchain(t *testing.T) (string, []byte) {
	t.Helper()
	toolchainFixture.once.Do(func() {
		program, err := exec.LookPath("go")
		if err != nil {
			toolchainFixture.err = err
			return
		}
		cmd := exec.Command(program, "env", "GOROOT")
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=", "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			toolchainFixture.err = err
			return
		}
		toolchainFixture.root, err = filepath.EvalSymlinks(strings.TrimSpace(string(out)))
		if err != nil {
			toolchainFixture.err = err
			return
		}
		files := []ledger.ValidationFile{}
		toolchainFixture.err = filepath.WalkDir(toolchainFixture.root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(toolchainFixture.root, path)
			if err != nil {
				return err
			}
			sum, n, err := fileDigest(context.Background(), path, 1<<63-1)
			if err != nil {
				return err
			}
			files = append(files, ledger.ValidationFile{Path: filepath.ToSlash(rel), Mode: "100644", Bytes: n, SHA256: sum})
			return nil
		})
		if toolchainFixture.err == nil {
			slices.SortFunc(files, func(a, b ledger.ValidationFile) int { return strings.Compare(a.Path, b.Path) })
			toolchainFixture.manifest, toolchainFixture.err = manifestBytes(files)
		}
	})
	if toolchainFixture.err != nil {
		t.Fatal(toolchainFixture.err)
	}
	return toolchainFixture.root, bytes.Clone(toolchainFixture.manifest)
}

type goFixture struct {
	runner  *Runner
	attempt Attempt
	store   *ledgerstore.Store
}

func testWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func newGoFixture(t *testing.T, required, subjectTest string) goFixture {
	t.Helper()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	gitEnv := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "empty"), "GIT_AUTHOR_NAME=Validation Test", "GIT_AUTHOR_EMAIL=validation@example.invalid", "GIT_COMMITTER_NAME=Validation Test", "GIT_COMMITTER_EMAIL=validation@example.invalid")
	runGit := func(args ...string) {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Env, cmd.Dir = gitEnv, repo
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, b)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "core.autocrlf", "false")
	mod := []byte("module example.test/check\n\ngo 1.27.0\n")
	testWrite(t, filepath.Join(repo, "go.mod"), mod)
	testWrite(t, filepath.Join(repo, "subject.go"), []byte("package check\n"))
	if subjectTest != "" {
		testWrite(t, filepath.Join(repo, "required_test.go"), []byte(subjectTest))
	}
	runGit("add", "--all")
	runGit("commit", "-m", "validation fixture")
	runGit("checkout", "-b", "work")
	colony := filepath.Join(root, "colony")
	store, err := ledgerstore.Create(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	capacity := workspace.CapacityProfile{MaxFiles: 100000, MaxBytes: 1 << 24, BytesInFlight: 1 << 25, CheckpointsPerPair: 4, GenerationWindow: 4, Validation: &workspace.ValidationCapacity{Lanes: 1, QueuedJobs: 1, JobsPerPair: 1, MaterializedBytes: 1 << 24, OutputBytes: 4 << 20, MemoryBytes: 1 << 30, Processes: 64, CPUUnits: 2, PerJob: workspace.ValidationResources{MaterializedBytes: 1 << 24, OutputBytes: 4 << 20, MemoryBytes: 1 << 30, Processes: 64, CPUUnits: 2}}}
	ws, _, err := workspace.Open(ctx, store, colony, workspace.Config{Capacity: &capacity})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	proposal, err := workspace.Inspect(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := ws.Register(ctx, proposal)
	if err != nil {
		t.Fatal(err)
	}
	contextID, agentID := uuid.NewV7(), uuid.NewV7()
	scratch, err := ws.Scratch(ctx, contextID, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := ws.BeginWrite(ctx, workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: contextID, AgentID: agentID, TaskAgentID: agentID, TaskScratchID: scratch.ID})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := writer.CommitObserved(ctx)
	writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	subject, err := ws.ResolveValidationSubject(ctx, workspace.ValidationSelector{Kind: "observation", WorkspaceID: registered.ID, ContextID: contextID, ObservationID: observed.ID})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(b []byte) ledger.Digest {
		h, _, err := store.PublishBody(ctx, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	canaryHash := publish(CanaryTemplate())
	requiredBytes := []byte(required)
	check := ledger.CheckSpec{CheckID: "required", Adapter: "go-test-json", AdapterVersion: 1, ExecutableID: "go", Package: ".", WorkingDirectory: ".", DefiningFiles: []ledger.ValidationFile{{Path: "required_test.go", Mode: "100644", Bytes: int64(len(requiredBytes)), SHA256: publish(requiredBytes)}}, Inventory: []ledger.ValidationInventoryNode{{Package: "example.test/check", Name: "TestRequired", Expected: "passed"}}, Canary: ledger.ValidationCanarySpec{Protocol: "go-fail-v1", PackageName: "check", File: "dwarf_canary_v1_test.go", TestName: "TestDwarfCanaryV1", TemplateKind: "testing-error-v1", TemplateSHA256: canaryHash}, EnvironmentID: "go", NetworkGrantRefs: []uuid.UUID{}, SecretGrantRefs: []uuid.UUID{}, WallMS: 900000, OutputBytes: 4 << 20, Processes: 64}
	toolchainRoot, toolchainManifest := pinnedToolchain(t)
	executable := filepath.Join(toolchainRoot, "bin", "go.exe")
	executableHash, _, err := fileDigest(ctx, executable, 1<<63-1)
	if err != nil {
		t.Fatal(err)
	}
	emptyManifest, err := manifestBytes([]ledger.ValidationFile{})
	if err != nil {
		t.Fatal(err)
	}
	systemRoot := os.Getenv("SystemRoot")
	env := ledger.EnvironmentSpec{EnvironmentID: "go", OS: runtime.GOOS, Arch: runtime.GOARCH, ExecutableID: "go", ExecutablePath: filepath.ToSlash(executable), ExecutableSHA256: executableHash, ToolchainVersion: runtime.Version(), ToolchainRoot: filepath.ToSlash(toolchainRoot), ToolchainManifestSHA256: publish(toolchainManifest), DependencyManifestSHA256: publish(emptyManifest), LockFiles: []ledger.ValidationFile{{Path: "go.mod", Mode: "100644", Bytes: int64(len(mod)), SHA256: sha256.Sum256(mod)}}, Variables: []ledger.ValidationVariable{{Name: "SystemRoot", Value: &systemRoot}}, GOMAXPROCS: 2, MemoryReservationBytes: 1 << 30}
	deps, runs := filepath.Join(root, "dependencies"), filepath.Join(root, "runs")
	for _, dir := range []string{deps, runs} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := New(Config{Workspace: ws, Store: store, RunsRoot: runs, Capacity: capacity})
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runnerHash, _, err := fileDigest(ctx, self, 1<<63-1)
	if err != nil {
		t.Fatal(err)
	}
	queued := ledger.ValidationAttemptQueued{ValidationID: uuid.NewV7(), AttemptID: uuid.NewV7(), RunID: uuid.NewV7(), DefinitionID: uuid.NewV7(), Ordinal: 1, Subject: subject.Descriptor(), RunnerSHA256: runnerHash, AdapterSHA256: AdapterDigest(runnerHash), BindingSHA256: sha256.Sum256([]byte("test binding"))}
	a := Attempt{Queued: queued, Subject: subject, Check: check, Environment: env, DependencyRoot: deps}
	bindAttempt(t, &a)
	return goFixture{runner: runner, attempt: a, store: store}
}

func bindAttempt(t *testing.T, a *Attempt) {
	t.Helper()
	c, err := a.Check.CheckPreimage()
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Environment.EnvironmentPreimage()
	if err != nil {
		t.Fatal(err)
	}
	i, err := a.Check.InventoryPreimage()
	if err != nil {
		t.Fatal(err)
	}
	a.Queued.CheckSHA256, a.Queued.EnvironmentSHA256, a.Queued.InventorySHA256 = sha256.Sum256(c), sha256.Sum256(e), sha256.Sum256(i)
}

func finishAttempt(t *testing.T, f goFixture, ctx context.Context, n Notices, want string) ledger.ValidationAttemptFinished {
	t.Helper()
	result, err := f.runner.Run(ctx, f.attempt, n)
	t.Cleanup(func() {
		if err := result.Release(); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := result.Finish()
	if finished.Outcome != want {
		for _, artifact := range finished.Artifacts {
			if artifact.Kind == "events" {
				stream := result.Bodies()[artifact.SHA256]
				if len(stream) > 8192 {
					t.Logf("event stream first/last 4KiB:\n%s\n[...]\n%s", stream[:4096], stream[len(stream)-4096:])
				} else {
					t.Logf("event stream:\n%s", stream)
				}
			}
		}
	}
	if _, err := finished.Encode(); err != nil {
		t.Fatalf("finish violates frozen record: %v: %+v", err, finished)
	}
	return finished
}

func TestGoAdmissibility(t *testing.T) {
	// The passing row intentionally alters process environment, so this parent
	// remains sequential. Every independent remaining row owns parallel fixtures.
	const passing = `package check
import ("testing"; "os"; "strings"; "path/filepath")
func TestRequired(t *testing.T) {
	if os.Getenv("GOFLAGS") != "" || strings.Contains(os.Getenv("PATH"), "dwarf-hostile-parent-path") {
		t.Fatal("inherited parent")
	}
	if os.Getenv("PATH") != filepath.Join(os.Getenv("GOROOT"), "bin") {
		t.Fatal("unexpected derived PATH")
	}
	cwd, err := os.Getwd()
	if err != nil || os.Getenv("PWD") != cwd { t.Fatal("unexpected derived PWD") }
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "CGO_ENABLED", "GOENV", "GOTOOLCHAIN", "GOFLAGS", "GOWORK",
			"GOPROXY", "GOSUMDB", "GOVCS", "GOOS", "GOARCH", "GOMAXPROCS",
			"GOROOT", "GOCACHE", "GOPATH", "GOMODCACHE", "GOTMPDIR",
			"TMP", "TEMP", "USERPROFILE", "SYSTEMROOT", "PATH", "PWD":
		default:
			t.Fatal("unexpected suite environment variable")
		}
	}
	t.Attr("contract", "required")
}
func TestExcluded(t *testing.T) { t.Fatal("not inventoried") }
`
	for _, row := range []struct {
		name, source, want string
		hostile, cancel    bool
	}{
		{"pass hostile parent", passing, "passed", true, false},
		{"required failure", "package check\nimport \"testing\"\nfunc TestRequired(t *testing.T){t.Error(\"required assertion\")}\n", "failed", false, false},
		{"no execution", "package check\nimport (\"testing\";\"os\")\nfunc TestMain(m *testing.M){os.Exit(0)}\nfunc TestRequired(t *testing.T){}\n", "no_status", false, false},
		{"required skip", "package check\nimport \"testing\"\nfunc TestRequired(t *testing.T){t.Skip(\"skip\")}\n", "skipped", false, false},
		{"cancel on started", "package check\nimport \"testing\"\nfunc TestRequired(t *testing.T){}\n", "incomplete", false, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			if !row.hostile {
				t.Parallel()
			}
			f := newGoFixture(t, row.source, "")
			if row.hostile {
				t.Setenv("GOFLAGS", "-exec=nonexistent-hostile-program")
				t.Setenv("PATH", "C:/dwarf-hostile-parent-path")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n := Notices{}
			if row.cancel {
				n.Started = func(int) { cancel() }
			}
			got := finishAttempt(t, f, ctx, n, row.want)
			if got.Outcome != row.want {
				t.Fatalf("outcome=%s reason=%s canary=%s", got.Outcome, got.Reason, got.Canary.State)
			}
			if row.want == "passed" && (!got.ExitKnown || *got.ExitCode != 1 || got.Canary.State != "expected_failed" || !got.TreeClosed) {
				t.Fatalf("pass without native/canary evidence: %+v", got)
			}
		})
	}
}

func TestValidationRequiredSet(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, source, want, integrity string }{
		{"replaces weakened test", "package check\nimport \"testing\"\nfunc TestRequired(t *testing.T){t.Fatal(\"pinned failure\")}\n", "failed", "verified"},
		{"detects defining file mutation", "package check\nimport (\"testing\";\"os\")\nfunc TestRequired(t *testing.T){if err:=os.Chmod(\"required_test.go\",0600);err!=nil {t.Fatal(err)};if err:=os.WriteFile(\"required_test.go\",[]byte(\"package check\\n\"),0600);err!=nil {t.Fatal(err)}}\n", "incomplete", "changed"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newGoFixture(t, row.source, "package check\nimport \"testing\"\nfunc TestRequired(t *testing.T){}\n")
			got := finishAttempt(t, f, context.Background(), Notices{}, row.want)
			if got.Outcome != row.want || got.InputIntegrity != row.integrity {
				t.Fatalf("outcome=%s integrity=%s reason=%s", got.Outcome, got.InputIntegrity, got.Reason)
			}
		})
	}
}

func TestValidationEnvironmentUnavailable(t *testing.T) {
	t.Parallel()
	f := newGoFixture(t, "package check\nimport \"testing\"\nfunc TestRequired(t *testing.T){}\n", "")
	seed := []byte("pinned dependency")
	manifest, err := manifestBytes([]ledger.ValidationFile{{Path: "seed", Mode: "100644", Bytes: int64(len(seed)), SHA256: sha256.Sum256(seed)}})
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := f.store.PublishBody(context.Background(), bytes.NewReader(manifest))
	if err != nil {
		t.Fatal(err)
	}
	f.attempt.Environment.DependencyManifestSHA256 = h
	testWrite(t, filepath.Join(f.attempt.DependencyRoot, "seed"), []byte("changed dependency"))
	bindAttempt(t, &f.attempt)
	launched := false
	result, err := f.runner.Run(context.Background(), f.attempt, Notices{Started: func(int) { launched = true }})
	if err == nil {
		t.Fatal("changed manifest accepted")
	}
	got := result.Finish()
	if launched || got.Started || got.Outcome != "infra_failure" || got.Reason != "environment_unavailable" || got.Canary.NonceSHA256 != (ledger.Digest{}) {
		t.Fatalf("environment failure launched or fabricated proof: %+v", got)
	}
	if _, err := got.Encode(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(f.runner.runsRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unavailable environment materialized: %v %v", entries, err)
	}
}

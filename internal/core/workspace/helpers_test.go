package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type gitFixture struct {
	root, colony string
	env          []string
	git          *gitRunner
	seed         *gitFixtureSeed
}

type gitFixtureSeed struct {
	once     sync.Once
	root     string
	proposal Proposal
}

var (
	gitFixtureSeedDir string
	gitFixtureSeeds   = map[string]*gitFixtureSeed{
		"sha1":   {},
		"sha256": {},
	}
)

func TestMain(m *testing.M) {
	var err error
	gitFixtureSeedDir, err = os.MkdirTemp("", "workspace-git-seeds-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(gitFixtureSeedDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

func testEnv(t testing.TB) []string {
	t.Helper()
	return testEnvAt(t.TempDir())
}

func testEnvAt(home string) []string {
	var env []string
	for _, s := range os.Environ() {
		key, _, _ := strings.Cut(s, "=")
		switch strings.ToUpper(key) {
		case "HOME", "USERPROFILE", "XDG_CONFIG_HOME":
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env = append(env, s)
		}
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, "config"), "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_AUTHOR_NAME=Workspace Test", "GIT_AUTHOR_EMAIL=workspace@example.invalid", "GIT_COMMITTER_NAME=Workspace Test", "GIT_COMMITTER_EMAIL=workspace@example.invalid", "GIT_AUTHOR_DATE=2001-02-03T04:05:06Z", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z")
}

func gitCommand(t testing.TB, env []string, dir string, input []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %q: %v; %s", args, err, stderr.Bytes())
	}
	return out
}

func writeTestFile(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixtureSeed(t testing.TB, format string) *gitFixtureSeed {
	t.Helper()
	seed := gitFixtureSeeds[format]
	if seed == nil {
		t.Fatalf("unsupported fixture object format %q", format)
	}
	seed.once.Do(func() {
		root := filepath.Join(gitFixtureSeedDir, format, "repo")
		home := filepath.Join(gitFixtureSeedDir, format, "home")
		for _, dir := range []string{root, home} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		env := testEnvAt(home)
		gitCommand(t, env, root, nil, "init", "--object-format="+format, "-b", "main")
		gitCommand(t, env, root, nil, "config", "core.autocrlf", "false")
		writeTestFile(t, filepath.Join(root, "base.txt"), []byte("one\ntwo\n"))
		gitCommand(t, env, root, nil, "add", "--", "base.txt")
		gitCommand(t, env, root, nil, "commit", "-m", "base")
		gitCommand(t, env, root, nil, "checkout", "--detach")
		git := newGitRunner(func() []string { return env })
		proposal, err := git.inspect(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}

		// A private copy must preserve every inspected input, not just its objects.
		copyRoot, copyEnv := t.TempDir(), testEnv(t)
		if err := os.CopyFS(copyRoot, os.DirFS(root)); err != nil {
			t.Fatal(err)
		}
		copyGit := newGitRunner(func() []string { return copyEnv })
		got, err := copyGit.inspect(context.Background(), copyRoot)
		if err != nil {
			t.Fatal(err)
		}
		want := proposal
		want.CommonDir = filepath.ToSlash(filepath.Join(copyRoot, ".git"))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("copied Git seed changed proposal: got %#v want %#v", got, want)
		}
		seed.root, seed.proposal = root, proposal
	})
	if seed.root == "" {
		t.Fatal("Git fixture seed construction failed")
	}
	return seed
}

func newGitFixture(t testing.TB, format string) gitFixture {
	t.Helper()
	seed := fixtureSeed(t, format)
	f := gitFixture{root: t.TempDir(), colony: t.TempDir(), env: testEnv(t), seed: seed}
	if err := os.CopyFS(f.root, os.DirFS(seed.root)); err != nil {
		t.Fatal(err)
	}
	if err := prepareGitLayout(f.colony); err != nil {
		t.Fatal(err)
	}
	f.git = newGitRunner(func() []string { return f.env })
	if f.git.unavailable != nil {
		t.Fatal(f.git.unavailable)
	}
	return f
}

func (f gitFixture) fixtureProposal(t testing.TB) Proposal {
	t.Helper()
	if f.seed != nil {
		p := f.seed.proposal
		p.CommonDir = filepath.ToSlash(filepath.Join(f.root, ".git"))
		home := environmentValue(f.env, "HOME")
		unchanged := true
		// Git's template may contain info/exclude. Preserve it, and only reuse
		// the seed's inspection when the fixture still has exactly its inputs.
		for _, file := range []struct {
			path string
			want HostFile
		}{
			{filepath.Join(p.CommonDir, "info", "exclude"), p.LocalExcludes},
			{filepath.Join(p.CommonDir, "info", "attributes"), p.LocalAttributes},
			{filepath.Join(home, "git", "ignore"), p.GlobalExcludes},
			{filepath.Join(home, "git", "attributes"), p.GlobalAttributes},
		} {
			info, err := os.Stat(file.path)
			if os.IsNotExist(err) && !file.want.Present {
				continue
			}
			if err != nil || !file.want.Present || info.Size() != int64(len(file.want.Bytes)) {
				unchanged = false
				break
			}
			b, err := os.ReadFile(file.path)
			if err != nil || !bytes.Equal(b, file.want.Bytes) {
				unchanged = false
				break
			}
		}
		if unchanged {
			// Callers edit reviewable inputs; never let them mutate the seed.
			for _, file := range []*HostFile{&p.LocalExcludes, &p.GlobalExcludes, &p.LocalAttributes, &p.GlobalAttributes, &p.SystemAttributes} {
				file.Bytes = bytes.Clone(file.Bytes)
			}
			return p
		}
	}
	p, err := f.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type captureSideEffects struct {
	Objects, Refs string
	Tmp           map[string]string
}

func snapshotCaptureSideEffects(t *testing.T, f gitFixture, p Proposal) captureSideEffects {
	t.Helper()
	s := captureSideEffects{
		Objects: string(gitCommand(t, f.env, f.root, nil, "--git-dir="+p.CommonDir, "count-objects", "-v")),
		Refs:    string(gitCommand(t, f.env, f.root, nil, "--git-dir="+p.CommonDir, "for-each-ref", "--format=%(refname)%00%(objectname)%00", "--end-of-options", "refs/dwarf-harness/captures/", "refs/dwarf-harness/generations/")),
		Tmp:     map[string]string{},
	}
	err := filepath.WalkDir(filepath.Join(f.colony, "workspaces"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filepath.Base(path) == "tmp" || strings.Contains(filepath.ToSlash(path), "/tmp/") {
			if d.IsDir() {
				s.Tmp[path] = "directory"
			} else {
				s.Tmp[path] = "file\x00" + string(readTestFile(t, path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertCaptureSideEffects(t *testing.T, f gitFixture, p Proposal, before captureSideEffects) {
	t.Helper()
	if after := snapshotCaptureSideEffects(t, f, p); !reflect.DeepEqual(before, after) {
		t.Fatalf("busy changed objects, refs or capture tmp: before %#v after %#v", before, after)
	}
}

type serviceFixture struct {
	gitFixture
	store *ledgerstore.Store
	svc   *Service
	p     Proposal
	w     Workspace
}

func serviceConfig() Config {
	return Config{Capacity: &CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}}
}

func newUnregisteredService(t *testing.T) *serviceFixture {
	t.Helper()
	return openFixtureService(t, newGitFixture(t, "sha1"))
}

func openFixtureService(t *testing.T, fixture gitFixture) *serviceFixture {
	t.Helper()
	f := &serviceFixture{gitFixture: fixture}
	var err error
	f.store, err = ledgerstore.Create(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.svc != nil {
			if err := f.svc.Close(); err != nil {
				t.Error(err)
			}
		}
		if err := f.store.Close(); err != nil {
			t.Error(err)
		}
	})
	f.svc, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
	if err != nil {
		t.Fatal(err)
	}
	f.p = f.fixtureProposal(t)
	return f
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	f := newUnregisteredService(t)
	var err error
	f.w, err = f.svc.Register(context.Background(), f.p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *serviceFixture) reopen(t *testing.T, cfg Config) Reconciliation {
	t.Helper()
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	var err error
	f.svc, report, err = openService(context.Background(), f.store, f.colony, cfg, serviceDeps{environ: func() []string { return f.env }})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func mustScratch(t *testing.T, f *serviceFixture, c uuid.UUID) Scratch {
	t.Helper()
	s, err := f.svc.Scratch(context.Background(), c, f.w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ownerClosure(s Scratch) Closure {
	return Closure{ScratchID: s.ID, Actor: ledger.ActorOwner, Reason: "abandoned"}
}

func (f *serviceFixture) capture(scratch Scratch, mode captureMode) (captureResult, error) {
	f.svc.mu.Lock()
	w := f.svc.workspaces[scratch.WorkspaceID]
	f.svc.mu.Unlock()
	r, err := f.svc.pool.reserve(scratch.Dir, admissionReservation)
	if err != nil {
		return captureResult{}, err
	}
	defer r.release()
	return f.svc.engine.capture(context.Background(), f.svc.captureScratch(w, scratch), scratch.AnchorTree, r, mode)
}

func serviceCommits(t *testing.T, store *ledgerstore.Store) []ledger.Commit {
	t.Helper()
	r, err := ledger.OpenReader(store.LogDir(), ledger.ReadOptions{Colony: store.Colony(), Through: store.Durable(), Consume: workspaceConsumption(), AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var result []ledger.Commit
	for {
		c, err := r.Next()
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, c)
	}
}

type requestEffects struct {
	Capture captureSideEffects
	Durable uint64
}

func snapshotRequestEffects(t *testing.T, f *serviceFixture) requestEffects {
	t.Helper()
	return requestEffects{snapshotCaptureSideEffects(t, f.gitFixture, f.p), f.store.Durable()}
}

func assertRequestEffects(t *testing.T, f *serviceFixture, before requestEffects) {
	t.Helper()
	if after := snapshotRequestEffects(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("request changed effects: before %+v after %+v", before, after)
	}
}

func testWriteBinding(scratch Scratch) WriteBinding {
	agent := uuid.NewV7()
	return WriteBinding{WriterID: uuid.NewV7(), ContextID: scratch.ContextID, AgentID: agent, TaskAgentID: agent, TaskScratchID: scratch.ID}
}

func leaseTestWriter(ctx context.Context, service *Service, scratch Scratch) (func(), error) {
	writer, err := service.BeginWrite(ctx, testWriteBinding(scratch))
	if err != nil {
		return nil, err
	}
	release, err := writer.Lease(ctx)
	if err != nil {
		writer.Close()
		return nil, err
	}
	return func() { release(); writer.Close() }, nil
}

func mustWriter(t *testing.T, f *serviceFixture, binding WriteBinding) *Writer {
	t.Helper()
	writer, err := f.svc.BeginWrite(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	return writer
}

func mustObserve(t *testing.T, writer *Writer) Observation {
	t.Helper()
	observation, err := writer.CommitObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	return observation
}

// Registered repositories carry colony markers and linked-worktree paths.
// Only an unregistered history can be copied without importing that ownership.
func copyGitHistory(t testing.TB, history gitFixture, env []string, root string) *gitFixtureSeed {
	t.Helper()
	for _, name := range []string{"worktrees", filepath.Join("refs", "dwarf-harness")} {
		_, err := os.Stat(filepath.Join(history.root, ".git", name))
		if err == nil {
			gitCommand(t, env, history.root, nil, "clone", "--no-hardlinks", "--no-checkout", "-b", "main", history.root, root)
			gitCommand(t, env, root, nil, "checkout", "--detach")
			return nil
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(root, os.DirFS(history.root)); err != nil {
		t.Fatal(err)
	}
	return history.seed
}

func serviceFromHistory(t *testing.T, history gitFixture) *serviceFixture {
	t.Helper()
	fixture := gitFixture{root: filepath.Join(t.TempDir(), "repo"), colony: t.TempDir(), env: history.env}
	fixture.seed = copyGitHistory(t, history, fixture.env, fixture.root)
	fixture.git = newGitRunner(func() []string { return fixture.env })
	f := openFixtureService(t, fixture)
	var err error
	f.w, err = f.svc.Register(context.Background(), f.p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A real foreign update-ref transaction holds the canonical lock after prepare.
// EOF/abort releases its own lock; the service must not remove it or retry.
func holdCanonicalTransaction(t *testing.T, f *serviceFixture, old, target string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	cmd := exec.CommandContext(ctx, "git", "update-ref", "--stdin")
	cmd.Dir, cmd.Env = f.root, f.env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	abort := func() {
		once.Do(func() {
			_, _ = io.WriteString(stdin, "abort\n")
			_ = stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Errorf("foreign update-ref: %v: %s", err, stderr.String())
			}
			cancel()
		})
	}
	t.Cleanup(abort)
	if _, err := io.WriteString(stdin, "start\nupdate refs/heads/main "+target+" "+old+"\nprepare\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	for _, want := range []string{"start: ok\n", "prepare: ok\n"} {
		if got, err := reader.ReadString('\n'); err != nil || got != want {
			t.Fatalf("foreign transaction handshake: %q %v", got, err)
		}
	}
	return abort
}

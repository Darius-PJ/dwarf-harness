package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type gitFixture struct {
	root, colony string
	env          []string
	git          *gitRunner
}

func testEnv(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
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

func newGitFixture(t testing.TB, format string) gitFixture {
	t.Helper()
	f := gitFixture{root: t.TempDir(), colony: t.TempDir(), env: testEnv(t)}
	gitCommand(t, f.env, f.root, nil, "init", "--object-format="+format, "-b", "main")
	gitCommand(t, f.env, f.root, nil, "config", "core.autocrlf", "false")
	writeTestFile(t, filepath.Join(f.root, "base.txt"), []byte("one\ntwo\n"))
	gitCommand(t, f.env, f.root, nil, "add", "--", "base.txt")
	gitCommand(t, f.env, f.root, nil, "commit", "-m", "base")
	gitCommand(t, f.env, f.root, nil, "checkout", "--detach")
	if err := prepareGitLayout(f.colony); err != nil {
		t.Fatal(err)
	}
	f.git = newGitRunner(func() []string { return f.env })
	if f.git.unavailable != nil {
		t.Fatal(f.git.unavailable)
	}
	return f
}

type captureSideEffects struct {
	Objects, Refs string
	Tmp           map[string]string
}

func snapshotCaptureSideEffects(t *testing.T, f gitFixture, p Proposal) captureSideEffects {
	t.Helper()
	s := captureSideEffects{
		Objects: string(gitCommand(t, f.env, f.root, nil, "--git-dir="+p.CommonDir, "count-objects", "-v")),
		Refs:    string(gitCommand(t, f.env, f.root, nil, "--git-dir="+p.CommonDir, "for-each-ref", "--format=%(refname)%00%(objectname)%00", "--end-of-options", "refs/dwarf-harness/captures/")),
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
	return Config{Capture: &CaptureProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20}}
}

func newUnregisteredService(t *testing.T) *serviceFixture {
	t.Helper()
	f := &serviceFixture{gitFixture: newGitFixture(t, "sha1")}
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
	f.p, err = f.svc.git.inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
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
	r, err := f.svc.pool.reserve(scratch.Dir, true)
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

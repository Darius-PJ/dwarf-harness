package graphfoundation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
	"github.com/Darius-PJ/dwarf-harness/internal/undercurrent/graph"
)

type foundationFixture struct {
	t                         *testing.T
	root, colony, base, empty string
	env                       []string
	store                     *ledgerstore.Store
	workspace                 *workspace.Service
	registered                workspace.Workspace
	graphs                    []*graph.Service
	blobs                     map[string]string
}

type fixtureBlob struct{ body, mode, oid string }

func newFoundationFixture(t *testing.T) *foundationFixture {
	t.Helper()
	f := &foundationFixture{t: t, root: t.TempDir(), colony: t.TempDir(), blobs: make(map[string]string)}
	home := t.TempDir()
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		name = strings.ToUpper(name)
		if name != "HOME" && name != "USERPROFILE" && name != "XDG_CONFIG_HOME" && !strings.HasPrefix(name, "GIT_") {
			f.env = append(f.env, value)
		}
	}
	f.env = append(f.env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, "config"), "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_AUTHOR_NAME=Graph Test", "GIT_COMMITTER_NAME=Graph Test", "GIT_AUTHOR_EMAIL=graph@example.invalid", "GIT_COMMITTER_EMAIL=graph@example.invalid", "GIT_AUTHOR_DATE=2001-02-03T04:05:06Z", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z")
	f.git(nil, "init", "--object-format=sha1", "-b", "main")
	f.git(nil, "config", "core.autocrlf", "false")
	f.empty = f.git(nil, "mktree")
	f.base = f.git([]byte("graph fixture\n"), "commit-tree", f.empty)
	f.git(nil, "update-ref", "refs/heads/main", f.base)
	f.git(nil, "checkout", "--detach", f.base)
	var err error
	f.store, err = ledgerstore.Create(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, service := range f.graphs {
			_ = service.Close()
		}
		if f.workspace != nil {
			_ = f.workspace.Close()
		}
		if f.store != nil {
			_ = f.store.Close()
		}
	})
	f.workspace, _, err = workspace.Open(context.Background(), f.store, f.colony, workspace.Config{})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := workspace.Inspect(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.registered, err = f.workspace.Register(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *foundationFixture) git(input []byte, args ...string) string {
	f.t.Helper()
	command := exec.CommandContext(context.Background(), "git", args...)
	command.Dir, command.Env, command.Stdin = f.root, f.env, bytes.NewReader(input)
	out, err := command.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *foundationFixture) tree(files map[string]fixtureBlob) string {
	f.t.Helper()
	var rows []string
	for path, entry := range files {
		if strings.Contains(path, "/") {
			f.t.Fatalf("flat fixture tree received %q", path)
		}
		if entry.mode == "" {
			entry.mode = "100644"
		}
		if entry.oid == "" {
			entry.oid = f.blobs[entry.body]
			if entry.oid == "" {
				entry.oid = f.git([]byte(entry.body), "hash-object", "-w", "--stdin")
				f.blobs[entry.body] = entry.oid
			}
		}
		kind := "blob"
		if entry.mode == "160000" {
			kind = "commit"
		}
		rows = append(rows, fmt.Sprintf("%s %s %s\t%s\x00", entry.mode, kind, entry.oid, path))
	}
	slices.Sort(rows)
	return f.git([]byte(strings.Join(rows, "")), "mktree", "-z", "--missing")
}

func foundationEncoded(t *testing.T, value interface {
	Encode() (json.RawMessage, error)
}) json.RawMessage {
	t.Helper()
	payload, err := value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (f *foundationFixture) accept(id uuid.UUID, predecessor, tree string, paths ...string) appender.Committed {
	f.t.Helper()
	slices.Sort(paths)
	request, contextID := uuid.NewV7(), uuid.NewV7()
	digest := ledger.Digest(sha256.Sum256([]byte("frozen graph fixture admission")))
	admitted := ledger.PromotionAdmitted{RequestID: request, WorkspaceID: id, ScratchID: uuid.NewV7(), ContextID: contextID, LineageID: uuid.NewV7(), Checkpoint: 1, ScratchBase: f.base, BaselineTree: predecessor, DeltaBaseTree: predecessor, CaptureID: uuid.NewV7(), SealedTree: tree, ManifestSHA256: digest, InputsRevision: 1, CaptureInputsSHA256: digest, PinID: uuid.NewV7()}
	if _, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypePromotionAdmitted, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: contextID, Payload: foundationEncoded(f.t, admitted)}}); err != nil {
		f.t.Fatal(err)
	}
	commit := f.git([]byte("accepted graph fixture\n"), "commit-tree", tree, "-p", f.base)
	accepted := ledger.PromotionAccepted{IntentID: uuid.NewV7(), RequestID: request, GenerationID: uuid.NewV7(), PredecessorCommit: f.base, PredecessorTree: predecessor, AcceptedCommit: commit, AcceptedTree: tree, ChangedPaths: paths}
	ack, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypePromotionAccepted, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, Payload: foundationEncoded(f.t, accepted)}})
	if err != nil {
		f.t.Fatal(err)
	}
	return ack
}

func (f *foundationFixture) open(options graph.Options) *graph.Service {
	f.t.Helper()
	service, err := graph.Open(options)
	if err != nil {
		f.t.Fatal(err)
	}
	f.graphs = append(f.graphs, service)
	return service
}

func (f *foundationFixture) options() graph.Options {
	return Options(f.store, f.workspace, graph.ToolConfig{})
}

func foundationDrive(t *testing.T, service *graph.Service) graph.Status {
	t.Helper()
	for range 205 {
		status, err := service.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "pending" {
			return status
		}
	}
	t.Fatal("graph did not finish its bounded attempt")
	return graph.Status{}
}

func foundationState(t *testing.T, service *graph.Service, status graph.Status) graph.State {
	t.Helper()
	var state graph.State
	for _, kind := range []string{"scopes", "nodes", "edges", "coverage"} {
		request := graph.PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: kind, Limit: 256}
		for {
			page, err := service.Page(request)
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range page.Scopes {
				state.Scopes = append(state.Scopes, scope.Scope)
			}
			state.Nodes = append(state.Nodes, page.Nodes...)
			state.Edges = append(state.Edges, page.Edges...)
			state.Coverage = append(state.Coverage, page.Coverage...)
			if page.Next == nil {
				break
			}
			request.After = page.Next
		}
	}
	_, digest, err := state.Canonical()
	if err != nil || digest != status.Digest {
		t.Fatalf("paged image digest: %s, %v; want %s", digest, err, status.Digest)
	}
	return state
}

func foundationTool(t *testing.T, variable, version string) *graph.Tool {
	t.Helper()
	path := os.Getenv(variable)
	if path == "" {
		t.Skipf("not exercised: %s is unset", variable)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return &graph.Tool{Path: path, Version: version, SHA256: ledger.Digest(hash.Sum(nil))}
}

func (f *foundationFixture) registerClone() (workspace.Workspace, string) {
	f.t.Helper()
	root := f.t.TempDir()
	f.git(nil, "clone", "--no-hardlinks", "--no-checkout", "-b", "main", f.root, root)
	prior := f.root
	f.root = root
	f.git(nil, "checkout", "--detach")
	f.root = prior
	proposal, err := workspace.Inspect(context.Background(), root)
	if err != nil {
		f.t.Fatal(err)
	}
	registered, err := f.workspace.Register(context.Background(), proposal)
	if err != nil {
		f.t.Fatal(err)
	}
	return registered, root
}

func foundationArtifact(t *testing.T, state graph.State, id uuid.UUID, path string) ledger.GraphNode {
	t.Helper()
	for _, node := range state.Nodes {
		if node.Key.OriginID != id || node.Key.Kind != ledger.NodeKindArtifact {
			continue
		}
		var tuple []json.RawMessage
		if err := json.Unmarshal([]byte(node.Key.LocalKey), &tuple); err != nil || len(tuple) != 2 {
			t.Fatalf("artifact tuple: %s, %v", node.Key.LocalKey, err)
		}
		var candidate string
		if err := json.Unmarshal(tuple[1], &candidate); err != nil {
			t.Fatal(err)
		}
		if candidate == path {
			return node
		}
	}
	t.Fatalf("artifact %s:%s is absent", id, path)
	return ledger.GraphNode{}
}

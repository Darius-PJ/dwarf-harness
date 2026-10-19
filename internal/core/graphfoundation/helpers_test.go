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

func (f *foundationFixture) admit(id uuid.UUID, predecessor, tree string) (uuid.UUID, uuid.UUID) {
	f.t.Helper()
	request, contextID := uuid.NewV7(), uuid.NewV7()
	digest := ledger.Digest(sha256.Sum256([]byte("frozen graph fixture admission")))
	admitted := ledger.PromotionAdmitted{RequestID: request, WorkspaceID: id, ScratchID: uuid.NewV7(), ContextID: contextID, LineageID: uuid.NewV7(), Checkpoint: 1, ScratchBase: f.base, BaselineTree: predecessor, DeltaBaseTree: predecessor, CaptureID: uuid.NewV7(), SealedTree: tree, ManifestSHA256: digest, InputsRevision: 1, CaptureInputsSHA256: digest, PinID: uuid.NewV7()}
	if _, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypePromotionAdmitted, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: contextID, Payload: foundationEncoded(f.t, admitted)}}); err != nil {
		f.t.Fatal(err)
	}
	return request, contextID
}

func (f *foundationFixture) accept(id uuid.UUID, predecessor, tree string, paths ...string) appender.Committed {
	f.t.Helper()
	slices.Sort(paths)
	request, contextID := f.admit(id, predecessor, tree)
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

func foundationRecords(t *testing.T, f *foundationFixture, after, through uint64, consume ledger.Consumption) []ledger.Record {
	t.Helper()
	reader, err := ledger.OpenReader(f.store.LogDir(), ledger.ReadOptions{Colony: f.store.Colony(), After: after, Through: through, Consume: consume})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var records []ledger.Record
	for {
		commit, err := reader.Next()
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, commit.Records...)
	}
}

func foundationGraphRecords(t *testing.T, f *foundationFixture, after uint64) []ledger.Record {
	t.Helper()
	return foundationRecords(t, f, after, f.store.Durable(), ledger.Consumption{
		ledger.TypeGraphAnalysisStarted: {1}, ledger.TypeGraphAnalysisProgress: {1},
		ledger.TypeGraphDeltaChunk: {1}, ledger.TypeGraphVersionCommitted: {1},
	})
}

func foundationRecordRef(t *testing.T, record ledger.Record) ledger.RecordRef {
	t.Helper()
	hash, err := ledger.ParseDigest(record.Hash)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.RecordRef{Seq: int64(record.Seq), Hash: hash}
}

func foundationImmutableCorpus(t *testing.T, f *foundationFixture, id uuid.UUID, tree string, listOnly bool) []graph.Artifact {
	t.Helper()
	read := f.options().ReadArtifacts
	request := graph.ArtifactRequest{WorkspaceID: id, Tree: tree, MaxBytes: 8 << 20, WorkAllowance: 10000, ListOnly: listOnly}
	var entries []graph.Artifact
	for {
		batch, err := read(context.Background(), request)
		if err != nil || batch.Reason != "" {
			t.Fatalf("oracle immutable corpus: %+v, %v", batch, err)
		}
		entries = append(entries, batch.Entries...)
		if batch.Complete {
			return entries
		}
		if batch.NextPath <= request.AfterPath {
			t.Fatal("oracle corpus reader did not advance")
		}
		request.AfterPath = batch.NextPath
	}
}

func foundationVerifiedTools(t *testing.T, config graph.ToolConfig) graph.Tools {
	t.Helper()
	var tools graph.Tools
	for _, pair := range []struct {
		tool *graph.Tool
		out  **graph.VerifiedTool
	}{{config.Python, &tools.Python}, {config.Node, &tools.Node}, {config.TypeScriptParser, &tools.TypeScriptParser}} {
		if pair.tool == nil {
			continue
		}
		verification, err := graph.NewToolVerification(*pair.tool)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = verification.Close() })
		remaining := int64(2000000)
		for *pair.out == nil && remaining > 0 {
			progress, err := verification.Step(context.Background(), min(10000, remaining))
			if err != nil || progress.WorkUsed == 0 && progress.Verified == nil {
				t.Fatalf("oracle tool verification: %+v, %v", progress, err)
			}
			remaining -= progress.WorkUsed
			*pair.out = progress.Verified
		}
		if *pair.out == nil {
			t.Fatal("oracle tool exceeded its finite verification budget")
		}
	}
	return tools
}

// foundationIncrementalOracle consumes only C2 records, never graph records or
// the service's image. It derives births from accepted refs and real immutable
// trees, invokes every exercised adapter through its public entry point, then
// replaces only the accepted workspace's facts. Helpers use verified tools too.
func foundationIncrementalOracle(t *testing.T, f *foundationFixture, through uint64, config graph.ToolConfig, epoch int64) graph.State {
	t.Helper()
	records := foundationRecords(t, f, 0, through, ledger.Consumption{
		ledger.TypeWorkspaceRegistered: {1}, ledger.TypePromotionAdmitted: {1}, ledger.TypePromotionAccepted: {1},
	})
	registered := make(map[uuid.UUID]ledger.RecordRef)
	admitted := make(map[uuid.UUID]uuid.UUID)
	births := make(map[uuid.UUID]map[string]int64)
	states := make(map[uuid.UUID]graph.State)
	analyzer := graph.NewAnalyzer(foundationVerifiedTools(t, config))
	// Trees are immutable, but acceptance events are not interchangeable: reuse
	// only Git reads and still derive births and run adapters for every event.
	type corpusKey struct {
		workspace uuid.UUID
		tree      string
		listOnly  bool
	}
	corpora := make(map[corpusKey][]graph.Artifact)
	corpus := func(id uuid.UUID, tree string, listOnly bool) []graph.Artifact {
		key := corpusKey{id, tree, listOnly}
		if entries, found := corpora[key]; found {
			return entries
		}
		entries := foundationImmutableCorpus(t, f, id, tree, listOnly)
		corpora[key] = entries
		return entries
	}
	configuration := config.ConfigurationDigest()
	for _, record := range records {
		switch record.Type {
		case ledger.TypeWorkspaceRegistered:
			p, err := ledger.DecodeWorkspaceRegistered(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			registered[p.WorkspaceID] = foundationRecordRef(t, record)
		case ledger.TypePromotionAdmitted:
			p, err := ledger.DecodePromotionAdmitted(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			admitted[p.RequestID] = p.WorkspaceID
		case ledger.TypePromotionAccepted:
			p, err := ledger.DecodePromotionAccepted(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			id := admitted[p.RequestID]
			if births[id] == nil {
				births[id] = make(map[string]int64)
				for _, entry := range corpus(id, p.PredecessorTree, true) {
					births[id][entry.Path] = registered[id].Seq
				}
			}
			entries := corpus(id, p.AcceptedTree, false)
			present := make(map[string]bool)
			request := graph.AnalysisRequest{AnalysisKey: foundationRecordRef(t, record).Hash, Source: ledger.GraphSource{WorkspaceID: id, Registered: registered[id], Subject: foundationRecordRef(t, record), Tree: p.AcceptedTree}, ConfigurationSHA256: configuration, Allowance: 10000}
			var python, sql, typescript []graph.CorpusArtifact
			for _, entry := range entries {
				if entry.Reason != "" {
					t.Fatalf("oracle fixture has an unsupported immutable entry: %+v", entry)
				}
				present[entry.Path] = true
				if _, exists := births[id][entry.Path]; !exists {
					births[id][entry.Path] = int64(record.Seq)
				}
				key, err := graph.ArtifactKey(id, births[id][entry.Path], entry.Path)
				if err != nil {
					t.Fatal(err)
				}
				artifact := graph.CorpusArtifact{Path: entry.Path, Key: key, SHA256: entry.SHA256, Bytes: entry.Bytes}
				request.Corpus = append(request.Corpus, artifact)
				switch filepath.Ext(entry.Path) {
				case ".py":
					python = append(python, artifact)
				case ".sql":
					sql = append(sql, artifact)
				case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs", ".json":
					typescript = append(typescript, artifact)
				}
			}
			for path := range births[id] {
				if !present[path] {
					delete(births[id], path)
				}
			}
			state := graph.State{Scopes: []graph.Scope{{Source: request.Source, Scope: ledger.GraphScopeCanonical, AnalyzerEpoch: epoch, ConfigurationSHA256: configuration}}}
			add := func(result graph.AnalysisResult, err error) {
				if err != nil {
					t.Fatal(err)
				}
				state.Nodes = append(state.Nodes, result.Nodes...)
				state.Edges = append(state.Edges, result.Edges...)
				state.Coverage = append(state.Coverage, result.Coverage...)
			}
			add(graph.AnalyzeOpaque(context.Background(), request))
			for _, adapter := range []struct {
				name   string
				corpus []graph.CorpusArtifact
			}{{ledger.AdapterPython, python}, {ledger.AdapterSQL, sql}, {ledger.AdapterTypeScript, typescript}} {
				if len(adapter.corpus) == 0 {
					continue
				}
				request.Corpus = adapter.corpus
				if adapter.name == ledger.AdapterSQL {
					add(graph.AnalyzeSQL(context.Background(), request))
				} else {
					add(analyzer.Analyze(context.Background(), adapter.name, request))
				}
			}
			state.Coverage = append(state.Coverage, ledger.CoverageEntry{Scope: "workspace:" + id.String() + ":canonical", Adapter: "go", State: ledger.CoverageUnknown, Reasons: []string{ledger.ReasonDependencyUnapproved}})
			states[id] = state
		}
	}
	var all graph.State
	for _, state := range states {
		all.Scopes = append(all.Scopes, state.Scopes...)
		all.Nodes = append(all.Nodes, state.Nodes...)
		all.Edges = append(all.Edges, state.Edges...)
		all.Coverage = append(all.Coverage, state.Coverage...)
	}
	if _, _, err := all.Canonical(); err != nil {
		t.Fatal(err)
	}
	return all
}

func foundationRebuildHistory(t *testing.T) (*foundationFixture, *graph.Service, string, map[string]fixtureBlob) {
	t.Helper()
	f := newFoundationFixture(t)
	other, otherRoot := f.registerClone()
	id := f.registered.ID
	files := map[string]fixtureBlob{
		"definition.sql": {body: "CREATE TABLE users (id INTEGER);"},
		"read.sql":       {body: "SELECT id FROM users;"},
		"stable.md":      {body: "stable bytes"},
	}
	tree := f.empty
	service := f.open(f.options())
	apply := func(paths ...string) appender.Committed {
		next := f.tree(files)
		accepted := f.accept(id, tree, next, paths...)
		tree = next
		status := foundationDrive(t, service)
		if status.State != "ready" {
			t.Fatalf("incremental history became incomplete: %+v", status)
		}
		return accepted
	}
	apply("definition.sql", "read.sql", "stable.md")
	files["read.sql"] = fixtureBlob{body: "SELECT * FROM users;"}
	apply("read.sql")
	files["groups.sql"] = fixtureBlob{body: "CREATE TABLE groups (id INTEGER);"}
	files["read.sql"] = fixtureBlob{body: "SELECT * FROM users; SELECT * FROM groups;"}
	apply("groups.sql", "read.sql")
	delete(files, "groups.sql")
	files["read.sql"] = fixtureBlob{body: "SELECT * FROM users;"}
	apply("groups.sql", "read.sql")
	delete(files, "read.sql")
	apply("read.sql")
	files["read.sql"] = fixtureBlob{body: "SELECT * FROM users;"}
	reborn := apply("read.sql")
	status, err := service.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	artifact := foundationArtifact(t, foundationState(t, service, status), id, "read.sql")
	expected, err := graph.ArtifactKey(id, int64(reborn.First), "read.sql")
	if err != nil || artifact.Key != expected {
		t.Fatalf("incremental control lost delete/recreate: %+v, %v", artifact, err)
	}
	files["renamed.sql"] = files["definition.sql"]
	delete(files, "definition.sql")
	apply("definition.sql", "renamed.sql")
	priorRoot := f.root
	f.root, f.blobs = otherRoot, make(map[string]string)
	otherTree := f.tree(map[string]fixtureBlob{"copy.md": {body: "stable bytes"}, "external.sql": {body: "SELECT * FROM external;"}})
	f.accept(other.ID, f.empty, otherTree, "copy.md", "external.sql")
	f.root, f.blobs = priorRoot, make(map[string]string)
	status = foundationDrive(t, service)
	if status.State != "ready" || len(foundationState(t, service, status).Scopes) != 2 {
		t.Fatalf("incremental control omitted the second workspace: %+v", status)
	}
	return f, service, tree, files
}

func foundationAssertSameState(t *testing.T, expected, actual graph.State) ledger.Digest {
	t.Helper()
	want, wantDigest, err := expected.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	got, gotDigest, err := actual.Canonical()
	if err != nil || wantDigest != gotDigest || !bytes.Equal(want, got) {
		t.Fatalf("canonical topology or coverage differs:\nwant %s\ngot  %s\n%v", want, got, err)
	}
	return gotDigest
}

func foundationNonstructuralRecords(t *testing.T, f *foundationFixture, tree string) {
	t.Helper()
	for _, noChange := range []bool{false, true} {
		target := tree
		if !noChange {
			target = f.tree(map[string]fixtureBlob{"unaccepted.md": {body: "not an accepted effect"}})
		}
		request, contextID := f.admit(f.registered.ID, tree, target)
		generation := uuid.NewV7()
		commit := f.git([]byte("nonstructural graph fixture\n"), "commit-tree", target, "-p", f.base)
		derived := ledger.GenerationDerived{GenerationID: generation, RequestID: request, PredecessorCommit: f.base, PredecessorTree: tree, InputsRevision: 1, IntegrationRules: "integration-rules/1", Outcome: ledger.GenerationClean, DerivedCommit: commit, DerivedTree: target}
		if _, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypeGenerationDerived, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, Payload: foundationEncoded(t, derived)}}); err != nil {
			t.Fatal(err)
		}
		var draft ledger.Draft
		if noChange {
			draft = ledger.Draft{Type: ledger.TypePromotionNoChange, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, Payload: foundationEncoded(t, ledger.PromotionNoChange{RequestID: request, GenerationID: generation, PredecessorCommit: f.base, PredecessorTree: tree})}
		} else {
			draft = ledger.Draft{Type: ledger.TypeFinalizationIntended, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, Payload: foundationEncoded(t, ledger.FinalizationIntended{IntentID: uuid.NewV7(), RequestID: request, GenerationID: generation, PredecessorCommit: f.base, PredecessorTree: tree, TargetCommit: commit, TargetTree: target, FinalizationRules: "finalization-rules/1"})}
		}
		if _, err := f.store.Append(context.Background(), []ledger.Draft{draft}); err != nil {
			t.Fatal(err)
		}
	}
}

func foundationStartedAfter(t *testing.T, f *foundationFixture, after uint64) ledger.GraphAnalysisStarted {
	t.Helper()
	records := foundationGraphRecords(t, f, after)
	if len(records) != 1 || records[0].Type != ledger.TypeGraphAnalysisStarted {
		t.Fatalf("Step did not append exactly the selected start: %+v", records)
	}
	started, err := ledger.DecodeGraphAnalysisStarted(records[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	return started
}

func foundationFinalAfter(t *testing.T, f *foundationFixture, after uint64) (ledger.Record, ledger.GraphVersionCommitted) {
	t.Helper()
	records := foundationGraphRecords(t, f, after)
	if len(records) == 0 || records[len(records)-1].Type != ledger.TypeGraphVersionCommitted {
		t.Fatal("attempt has no final")
	}
	record := records[len(records)-1]
	final, err := ledger.DecodeGraphVersionCommitted(record.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return record, final
}

package graphfoundation

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/undercurrent/graph"
)

func TestArtifactIdentityAndCausalDirection(t *testing.T) {
	t.Parallel()
	t.Run("immutable Git identities and causal evidence", func(t *testing.T) {
		t.Parallel()
		f := newFoundationFixture(t)
		other, otherRoot := f.registerClone()
		id := f.registered.ID
		files := map[string]fixtureBlob{
			"a.sql":     {body: "CREATE TABLE users (id INTEGER);"},
			"b.sql":     {body: "SELECT id FROM users;"},
			"readme.md": {body: "[not causality](a.sql)"},
			"link":      {mode: "120000", body: "a.sql"},
			"submodule": {mode: "160000", oid: f.base},
		}
		tree := f.tree(files)
		accepted := f.accept(id, f.empty, tree, "a.sql", "b.sql", "readme.md", "link", "submodule")
		service := f.open(f.options())
		status := foundationDrive(t, service)
		state := foundationState(t, service, status)
		first := foundationArtifact(t, state, id, "b.sql")
		markdown := foundationArtifact(t, state, id, "readme.md")
		key, err := graph.ArtifactKey(id, int64(accepted.First), "b.sql")
		if err != nil || first.Key != key {
			t.Fatalf("birth key: %+v, %v; want %+v", first.Key, err, key)
		}
		causal := false
		for _, edge := range state.Edges {
			if edge.To == markdown.Key || edge.From == markdown.Key {
				t.Fatal("hyperlink became a causal edge")
			}
			if edge.To == first.Key && edge.From.Kind == ledger.NodeKindModule && edge.Type == "static_dependency" && edge.Evidence.Artifact == first.Key && edge.Evidence.ArtifactSHA256 == first.VersionSHA256 {
				causal = true
			}
		}
		if !causal {
			t.Fatalf("SQL CREATE/SELECT has no module-to-artifact evidence: %+v", state.Edges)
		}
		for _, path := range []string{"readme.md", "link", "submodule"} {
			key, err := graph.ArtifactKey(id, int64(accepted.First), path)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			covered := false
			for _, entry := range state.Coverage {
				if entry.Scope == string(encoded) && slices.Contains(entry.Reasons, ledger.ReasonUnsupportedSyntax) {
					covered = true
				}
			}
			if !covered {
				t.Fatalf("no unsupported coverage for %s", path)
			}
			if path != "readme.md" {
				for _, node := range state.Nodes {
					if node.Key == key {
						t.Fatalf("no-byte %s became a node", path)
					}
				}
			}
		}
		files["b.sql"] = fixtureBlob{body: "SELECT * FROM users;"}
		editedTree := f.tree(files)
		f.accept(id, tree, editedTree, "b.sql")
		status = foundationDrive(t, service)
		edited := foundationArtifact(t, foundationState(t, service, status), id, "b.sql")
		if edited.Key != first.Key || edited.VersionSHA256 == first.VersionSHA256 || edited.VersionSHA256 != ledger.Digest(sha256.Sum256([]byte(files["b.sql"].body))) {
			t.Fatalf("edit changed identity or lost bytes: before=%+v after=%+v", first, edited)
		}
		delete(files, "b.sql")
		deletedTree := f.tree(files)
		f.accept(id, editedTree, deletedTree, "b.sql")
		files["b.sql"] = fixtureBlob{body: "SELECT * FROM users;"}
		recreatedTree := f.tree(files)
		recreated := f.accept(id, deletedTree, recreatedTree, "b.sql")
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		service = f.open(f.options())
		status = foundationDrive(t, service)
		reborn := foundationArtifact(t, foundationState(t, service, status), id, "b.sql")
		key, err = graph.ArtifactKey(id, int64(recreated.First), "b.sql")
		if err != nil || reborn.Key != key || reborn.Key == edited.Key || reborn.VersionSHA256 != edited.VersionSHA256 {
			t.Fatalf("delete/recreate inherited image birth: %+v, %v", reborn, err)
		}
		priorDefinition := foundationArtifact(t, foundationState(t, service, status), id, "a.sql")
		files["renamed.sql"] = files["a.sql"]
		delete(files, "a.sql")
		renamedTree := f.tree(files)
		renamed := f.accept(id, recreatedTree, renamedTree, "a.sql", "renamed.sql")
		status = foundationDrive(t, service)
		state = foundationState(t, service, status)
		definition := foundationArtifact(t, state, id, "renamed.sql")
		key, err = graph.ArtifactKey(id, int64(renamed.First), "renamed.sql")
		if err != nil || definition.Key != key || definition.VersionSHA256 != priorDefinition.VersionSHA256 {
			t.Fatalf("rename was not remove/add: %+v, %v", definition, err)
		}
		for _, node := range state.Nodes {
			if node.Key == priorDefinition.Key {
				t.Fatal("rename retained the old artifact")
			}
		}
		priorRoot := f.root
		f.root, f.blobs = otherRoot, make(map[string]string)
		otherTree := f.tree(map[string]fixtureBlob{"b.sql": files["b.sql"]})
		otherAccepted := f.accept(other.ID, f.empty, otherTree, "b.sql")
		f.root = priorRoot
		status = foundationDrive(t, service)
		state = foundationState(t, service, status)
		otherNode := foundationArtifact(t, state, other.ID, "b.sql")
		key, err = graph.ArtifactKey(other.ID, int64(otherAccepted.First), "b.sql")
		if err != nil || otherNode.Key != key || otherNode.Key == reborn.Key || otherNode.VersionSHA256 != reborn.VersionSHA256 {
			t.Fatalf("workspace aliases: %+v, %v", otherNode, err)
		}
	})
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			var tools graph.ToolConfig
			files := map[string]fixtureBlob{}
			importer := "a.py"
			if language == "python" {
				tools.Python = foundationTool(t, "DWARF_GRAPH_PYTHON", "3.13.15")
				files["a.py"], files["b.py"] = fixtureBlob{body: "import b\n"}, fixtureBlob{body: "value = 42\n"}
			} else {
				tools.Node = foundationTool(t, "DWARF_GRAPH_NODE", "v24.18.0")
				tools.TypeScriptParser = foundationTool(t, "DWARF_GRAPH_TYPESCRIPT", "6.0.3")
				importer = "a.ts"
				files["a.ts"], files["b.ts"] = fixtureBlob{body: "import {value} from './b'; export const answer = value;"}, fixtureBlob{body: "export const value = 42;"}
			}
			f := newFoundationFixture(t)
			paths := make([]string, 0, len(files))
			for path := range files {
				paths = append(paths, path)
			}
			f.accept(f.registered.ID, f.empty, f.tree(files), paths...)
			service := f.open(Options(f.store, f.workspace, tools))
			state := foundationState(t, service, foundationDrive(t, service))
			node := foundationArtifact(t, state, f.registered.ID, importer)
			found := false
			for _, edge := range state.Edges {
				if edge.To == node.Key && edge.From.Kind == ledger.NodeKindModule && edge.Type == "static_dependency" && edge.Evidence.Artifact == node.Key && edge.Evidence.ArtifactSHA256 == node.VersionSHA256 {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s produced no native causal evidence: %+v", language, state.Coverage)
			}
		})
	}
}

func TestGraphWorkerDoesNotBlockCore(t *testing.T) {
	t.Parallel()
	f := newFoundationFixture(t)
	f.accept(f.registered.ID, f.empty, f.tree(map[string]fixtureBlob{"a.py": {body: ""}}), "a.py")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce, enteredOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	options := f.options()
	read := options.ReadArtifacts
	options.ReadArtifacts = func(ctx context.Context, request graph.ArtifactRequest) (graph.ArtifactBatch, error) {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
			return graph.ArtifactBatch{}, ctx.Err()
		case <-release:
		}
		return read(ctx, request)
	}
	service := f.open(options)
	if _, err := service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.Step(context.Background()); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("worker ended before gated Git read: %v", err)
	}
	body, _, err := f.store.PublishBody(context.Background(), bytes.NewBufferString("unrelated durable work"))
	if err != nil {
		t.Fatal(err)
	}
	ack, err := f.store.Append(context.Background(), []ledger.Draft{{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorCore, BodyRefs: []ledger.Digest{body}, Payload: foundationEncoded(t, ledger.BodyPin{PinID: uuid.NewV7()})}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Observe(context.Background())
	if err != nil || status.State != "busy" || status.Through >= ack.Last {
		t.Fatalf("worker blocked or mixed the Core frontier: %+v, %v", status, err)
	}
	page, err := service.Page(graph.PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: "nodes", Limit: 1})
	if err != nil || page.State != "busy" || len(page.Nodes) != 0 {
		t.Fatalf("Core-busy page: %+v, %v", page.Status, err)
	}
	if _, err := service.Step(context.Background()); !errors.Is(err, graph.ErrBusy) {
		t.Fatalf("concurrent worker: %v", err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	status, err = service.Observe(context.Background())
	if err != nil || status.Through != ack.Last {
		t.Fatalf("unrelated commit not hydrated: %+v, %v", status, err)
	}
}

type foundationAppender func(context.Context, []ledger.Draft) (appender.Committed, error)

func (a foundationAppender) Append(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
	return a(ctx, drafts)
}

func TestGraphLostAckTornTailAndResume(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"started", "reserved", "chunks", "changed source"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			f := newFoundationFixture(t)
			files := map[string]fixtureBlob{"a.py": {body: "value=1\n"}}
			if phase == "chunks" || phase == "changed source" {
				for i := range 2000 {
					files[fmt.Sprintf("p%04d.py", i)] = fixtureBlob{body: "value=1\n"}
				}
			}
			paths := make([]string, 0, len(files))
			for path := range files {
				paths = append(paths, path)
			}
			f.accept(f.registered.ID, f.empty, f.tree(files), paths...)
			options := f.options()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "reserved" {
				read := options.ReadArtifacts
				options.ReadArtifacts = func(ctx context.Context, request graph.ArtifactRequest) (graph.ArtifactBatch, error) {
					cancel()
					return read(ctx, request)
				}
			}
			service := f.open(options)
			prefix, err := service.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "reserved" {
				prefix, err = service.Step(ctx)
				if !errors.Is(err, context.Canceled) || prefix.Charged != 10000 {
					t.Fatalf("interrupted reservation: %+v, %v", prefix, err)
				}
			} else if phase != "started" {
				for steps := 0; prefix.VerifiedCursor.OutputIndex == 0 && steps < 205; steps++ {
					prefix, err = service.Step(ctx)
					if err != nil {
						t.Fatal(err)
					}
				}
				if prefix.VerifiedCursor.OutputIndex == 0 || prefix.Version != 0 {
					t.Fatalf("no durable chunk prefix: %+v", prefix)
				}
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			if phase == "changed source" {
				oid := f.blobs["value=1\n"]
				path := filepath.Join(f.registered.CommonDir, "objects", oid[:2], oid[2:])
				var compressed bytes.Buffer
				z := zlib.NewWriter(&compressed)
				if _, err := fmt.Fprint(z, "blob 8\x00value=2\n"); err != nil {
					t.Fatal(err)
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, compressed.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			options = f.options()
			read := options.ReadArtifacts
			options.ReadArtifacts = func(ctx context.Context, request graph.ArtifactRequest) (graph.ArtifactBatch, error) {
				calls++
				return read(ctx, request)
			}
			before := f.store.Durable()
			service = f.open(options)
			status, err := service.Observe(context.Background())
			if err != nil || status.State != "recovery_pending" || status.Attempt != prefix.Attempt || status.Charged != prefix.Charged || status.Version != 0 || calls != 0 || f.store.Durable() != before {
				t.Fatalf("Open executed or misread recovery: %+v, %v, calls=%d", status, err, calls)
			}
			if _, err := service.Step(context.Background()); !errors.Is(err, graph.ErrRecovery) {
				t.Fatalf("automatic continuation: %v", err)
			}
			if f.store.Durable() != before || calls != 0 {
				t.Fatal("Step spent recovery-pending work")
			}
			status, err = service.Resume(context.Background(), prefix.Attempt)
			if err != nil || status.Charged != prefix.Charged+10000 || status.Attempt != prefix.Attempt {
				t.Fatalf("Resume reset or refunded reservation: %+v, %v", status, err)
			}
			status = foundationDrive(t, service)
			if phase == "changed source" {
				if status.Version != 0 || status.Digest != ledger.EmptyGraphDigest || !slices.Contains(status.Reasons, ledger.ReasonRecoveryUnknown) {
					t.Fatalf("mismatched committed chunks published: %+v", status)
				}
			} else {
				state := foundationState(t, service, status)
				if status.Version == 0 || len(state.Nodes) != len(files) || status.Charged <= prefix.Charged {
					t.Fatalf("resumed image: %+v nodes=%d", status, len(state.Nodes))
				}
			}
		})
	}
	for _, uncertain := range []bool{false, true} {
		name := "delayed final acknowledgment"
		if uncertain {
			name = "uncertain committed final"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFoundationFixture(t)
			f.accept(f.registered.ID, f.empty, f.tree(map[string]fixtureBlob{"a.py": {body: ""}}), "a.py")
			committed, release := make(chan appender.Committed, 1), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			options := f.options()
			options.Appender = foundationAppender(func(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
				ack, err := f.store.Append(ctx, drafts)
				if err != nil || drafts[len(drafts)-1].Type != ledger.TypeGraphVersionCommitted {
					return ack, err
				}
				committed <- ack
				if uncertain {
					return appender.Committed{}, appender.ErrUncertain
				}
				select {
				case <-release:
					return ack, nil
				case <-ctx.Done():
					return appender.Committed{}, ctx.Err()
				}
			})
			service := f.open(options)
			type outcome struct {
				status graph.Status
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				for range 205 {
					status, err := service.Step(context.Background())
					if err != nil || status.Version != 0 {
						done <- outcome{status, err}
						return
					}
				}
				done <- outcome{err: errors.New("final never reached the real store")}
			}()
			ack := <-committed
			if !uncertain {
				status, err := service.Observe(context.Background())
				if err != nil || status.State != "busy" || status.Version != 0 {
					t.Fatalf("unacknowledged final became visible to its writer: %+v, %v", status, err)
				}
			}
			before := f.store.Durable()
			observer := f.open(f.options())
			visible, err := observer.Observe(context.Background())
			if err != nil || visible.Version != int64(ack.Last) || f.store.Durable() != before {
				t.Fatalf("durable final hidden from reopen: %+v, %v", visible, err)
			}
			state := foundationState(t, observer, visible)
			if len(state.Nodes) != 1 {
				t.Fatalf("durable final topology: %+v", state)
			}
			unblock()
			result := <-done
			if uncertain {
				if !errors.Is(result.err, graph.ErrRecovery) || result.status.State != "recovery_pending" || result.status.Version != 0 {
					t.Fatalf("uncertain writer did not fence: %+v, %v", result.status, result.err)
				}
				if _, err := service.Step(context.Background()); !errors.Is(err, graph.ErrRecovery) {
					t.Fatalf("fenced Step: %v", err)
				}
				if _, err := service.Resume(context.Background(), result.status.Attempt); !errors.Is(err, graph.ErrRecovery) {
					t.Fatalf("fenced Resume: %v", err)
				}
				if f.store.Durable() != before {
					t.Fatal("fenced writer blindly reappended")
				}
			} else if result.err != nil || result.status.Version != visible.Version || result.status.Digest != visible.Digest {
				t.Fatalf("acknowledged view differs: %+v, %v", result.status, result.err)
			}
		})
	}
	t.Run("torn final is discarded before explicit resume", func(t *testing.T) {
		t.Parallel()
		f := newFoundationFixture(t)
		f.accept(f.registered.ID, f.empty, f.tree(map[string]fixtureBlob{"a.py": {body: ""}}), "a.py")
		service := f.open(f.options())
		final := foundationDrive(t, service)
		if final.Version == 0 {
			t.Fatalf("no final to tear: %+v", final)
		}
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		logDir := f.store.LogDir()
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		segments, err := os.ReadDir(logDir)
		if err != nil || len(segments) == 0 {
			t.Fatalf("log segments: %v", err)
		}
		path := filepath.Join(logDir, segments[len(segments)-1].Name())
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, info.Size()-1); err != nil {
			t.Fatal(err)
		}
		store, recovery, err := ledgerstore.Open(f.colony, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		f.store = store
		if recovery.TruncatedBytes == 0 || store.Durable() != uint64(final.Version)-1 {
			t.Fatalf("torn final recovery: %+v durable=%d final=%d", recovery, store.Durable(), final.Version)
		}
		// The still-open workspace service is used only for immutable Git reads;
		// it never replays the deliberately synthetic promotion prefix.
		service = f.open(f.options())
		status, err := service.Observe(context.Background())
		if err != nil || status.State != "recovery_pending" || status.Version != 0 || status.Attempt != final.Attempt || status.Charged != final.Charged {
			t.Fatalf("torn prefix view: %+v, %v", status, err)
		}
		status, err = service.Resume(context.Background(), final.Attempt)
		if err != nil || status.Charged != final.Charged+10000 {
			t.Fatalf("torn prefix reservation: %+v, %v", status, err)
		}
		status = foundationDrive(t, service)
		if status.Version <= final.Version || status.Digest != final.Digest {
			t.Fatalf("torn recovery changed the image: before=%+v after=%+v", final, status)
		}
	})
}

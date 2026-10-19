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

func TestGraphRebuildMatchesIncremental(t *testing.T) {
	t.Parallel()
	t.Run("load confirmation and exact structural cadence", func(t *testing.T) {
		t.Parallel()
		if raceEnabled {
			t.Skip("B28 item 8: production 100-acceptance cadence runs without race; race covers the scaled cadence through the private service seam")
		}
		f, service, tree, files := foundationRebuildHistory(t)
		before, err := service.Observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		incremental := foundationState(t, service, before)
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		mark := f.store.Durable()
		readCalls := 0
		options := f.options()
		read := options.ReadArtifacts
		options.ReadArtifacts = func(ctx context.Context, request graph.ArtifactRequest) (graph.ArtifactBatch, error) {
			readCalls++
			return read(ctx, request)
		}
		service = f.open(options)
		observed, err := service.Observe(context.Background())
		if err != nil || observed.State != "pending" || f.store.Durable() != mark || readCalls != 0 {
			t.Fatalf("load polled or ran without Step: %+v, %v; reads=%d", observed, err, readCalls)
		}
		foundationAssertSameState(t, incremental, foundationState(t, service, observed))
		if _, err := service.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		if started := foundationStartedAfter(t, f, mark); started.Reason != ledger.GraphReasonLoad {
			t.Fatalf("load selected %s", started.Reason)
		}
		confirmed := foundationDrive(t, service)
		_, final := foundationFinalAfter(t, f, mark)
		if confirmed.Version != before.Version || confirmed.Digest != before.Digest || final.ChunkCount != 0 || final.ChunksSHA256 != ledger.EmptyChunksDigest || final.StructuralSinceRebuild != 0 || final.GraphDigest != final.BaseDigest {
			t.Fatalf("load did not confirm zero net change: %+v final=%+v", confirmed, final)
		}
		foundationAssertSameState(t, incremental, foundationState(t, service, confirmed))
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		mark = f.store.Durable()
		service = f.open(f.options())
		if status, err := service.Step(context.Background()); err != nil || status.State != "ready" || status.Version != confirmed.Version || f.store.Durable() != mark {
			t.Fatalf("unchanged restart minted another rebuild: %+v, %v", status, err)
		}
		foundationNonstructuralRecords(t, f, tree)
		var alternating [2]string
		for i := range alternating {
			files["tick.md"] = fixtureBlob{body: fmt.Sprintf("tick %d", i)}
			alternating[i] = f.tree(files)
		}
		for i := 1; i <= 99; i++ {
			next := alternating[i%2]
			f.accept(f.registered.ID, tree, next, "tick.md")
			tree = next
		}
		mark = f.store.Durable()
		if _, err := service.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		if started := foundationStartedAfter(t, f, mark); started.Reason != ledger.GraphReasonIncremental {
			t.Fatalf("intents/no-change or the 99th acceptance triggered %s", started.Reason)
		}
		ninetyNine := foundationDrive(t, service)
		_, final = foundationFinalAfter(t, f, mark)
		if final.StructuralSinceRebuild != 99 {
			t.Fatalf("structural counter included non-acceptances: %d", final.StructuralSinceRebuild)
		}
		hundredth := f.accept(f.registered.ID, tree, alternating[0], "tick.md")
		mark = f.store.Durable()
		if _, err := service.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		started := foundationStartedAfter(t, f, mark)
		if started.Reason != ledger.GraphReasonStructural100 || started.Source.Subject.Seq != int64(hundredth.First) || started.Source.Subject != started.InputFrontier {
			t.Fatalf("100th acceptance did not select rebuild before incremental work: %+v", started)
		}
		rebuilt := foundationDrive(t, service)
		record, final := foundationFinalAfter(t, f, mark)
		if rebuilt.Version != int64(record.Seq) || rebuilt.Version == ninetyNine.Version || final.ChunkCount == 0 || final.StructuralSinceRebuild != 0 {
			t.Fatalf("cadence did not publish the new subject and reset its counter: %+v final=%+v", rebuilt, final)
		}
		expected := foundationIncrementalOracle(t, f, hundredth.Last, graph.ToolConfig{}, 1)
		foundationAssertSameState(t, expected, foundationState(t, service, rebuilt))
		mark = f.store.Durable()
		if _, err := service.Step(context.Background()); err != nil || f.store.Durable() != mark {
			t.Fatalf("cadence stayed due after its eligible final: %v", err)
		}
	})
	t.Run("new analyzer configuration matches the independent incremental oracle", func(t *testing.T) {
		t.Parallel()
		tools := graph.ToolConfig{
			Python:           foundationTool(t, "DWARF_GRAPH_PYTHON", "3.13.15"),
			Node:             foundationTool(t, "DWARF_GRAPH_NODE", "v24.18.0"),
			TypeScriptParser: foundationTool(t, "DWARF_GRAPH_TYPESCRIPT", "6.0.3"),
		}
		// Load/cadence cover the edit/delete/recreate/rename history. This row
		// needs two published scopes and real native dependencies, not that
		// entire history again before changing analyzer identities.
		f := newFoundationFixture(t)
		files := map[string]fixtureBlob{
			"a.py":           {body: "import b\n"},
			"b.py":           {body: "value = 42\n"},
			"a.ts":           {body: "import {value} from './b'; export const answer = value;"},
			"b.ts":           {body: "export const value = 42;"},
			"definition.sql": {body: "CREATE TABLE users (id INTEGER);"},
			"read.sql":       {body: "SELECT * FROM users;"},
		}
		tree := f.tree(files)
		f.accept(f.registered.ID, f.empty, tree, "a.py", "a.ts", "b.py", "b.ts", "definition.sql", "read.sql")
		service := f.open(f.options())
		foundationDrive(t, service)
		other, otherRoot := f.registerClone()
		primaryRoot := f.root
		f.root, f.blobs = otherRoot, make(map[string]string)
		otherTree := f.tree(map[string]fixtureBlob{"other.md": {body: "other workspace"}})
		f.accept(other.ID, f.empty, otherTree, "other.md")
		f.root, f.blobs = primaryRoot, make(map[string]string)
		foundationDrive(t, service)
		before, err := service.Observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		oldState := foundationState(t, service, before)
		oldEpoch := oldState.Scopes[0].AnalyzerEpoch
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		mark := f.store.Durable()
		options := f.options()
		options.Tools = tools
		service = f.open(options)
		if f.store.Durable() != mark {
			t.Fatal("configuration change polled without Step")
		}
		if _, err := service.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		started := foundationStartedAfter(t, f, mark)
		if started.Reason != ledger.GraphReasonAnalyzerEpoch || started.AnalyzerEpoch != oldEpoch+1 || started.ConfigurationSHA256 != options.Tools.ConfigurationDigest() {
			t.Fatalf("configuration did not win over load with one epoch increment: %+v", started)
		}
		rebuilt := foundationDrive(t, service)
		record, final := foundationFinalAfter(t, f, mark)
		if rebuilt.Version != int64(record.Seq) || rebuilt.Version == before.Version || final.ChunkCount == 0 || final.StructuralSinceRebuild != 0 {
			t.Fatalf("new configuration did not publish/reset: %+v final=%+v", rebuilt, final)
		}
		expected := foundationIncrementalOracle(t, f, mark, options.Tools, oldEpoch+1)
		state := foundationState(t, service, rebuilt)
		foundationAssertSameState(t, expected, state)
		for _, adapter := range []string{ledger.AdapterPython, ledger.AdapterTypeScript} {
			if !slices.ContainsFunc(state.Edges, func(edge ledger.GraphEdge) bool {
				return edge.Type == "static_dependency" && edge.Evidence.Adapter == adapter
			}) {
				t.Fatalf("configured %s produced no native dependency", adapter)
			}
		}
		mark = f.store.Durable()
		if _, err := service.Step(context.Background()); err != nil || f.store.Durable() != mark {
			t.Fatalf("configuration or load stayed due: %v", err)
		}
	})
}

func TestOldRebuildCannotReplaceNewAcceptance(t *testing.T) {
	t.Parallel()
	for _, pause := range []string{"before reservation", "before final"} {
		t.Run(pause, func(t *testing.T) {
			t.Parallel()
			f := newFoundationFixture(t)
			other, otherRoot := f.registerClone()
			first := f.tree(map[string]fixtureBlob{"first.md": {body: "old"}})
			f.accept(f.registered.ID, f.empty, first, "first.md")
			service := f.open(f.options())
			foundationDrive(t, service)
			priorRoot := f.root
			f.root, f.blobs = otherRoot, make(map[string]string)
			otherOld := f.tree(map[string]fixtureBlob{"other.md": {body: "old"}})
			otherNew := f.tree(map[string]fixtureBlob{"other.md": {body: "new"}})
			f.accept(other.ID, f.empty, otherOld, "other.md")
			f.root, f.blobs = priorRoot, make(map[string]string)
			before := foundationDrive(t, service)
			current := foundationState(t, service, before)
			next := f.tree(map[string]fixtureBlob{"first.md": {body: "candidate"}})
			f.accept(f.registered.ID, first, next, "first.md")
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			options := f.options()
			readCalls := 0
			read := options.ReadArtifacts
			options.ReadArtifacts = func(ctx context.Context, request graph.ArtifactRequest) (graph.ArtifactBatch, error) {
				readCalls++
				return read(ctx, request)
			}
			if pause == "before final" {
				options.Appender = foundationAppender(func(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
					if drafts[len(drafts)-1].Type == ledger.TypeGraphVersionCommitted {
						close(entered)
						select {
						case <-ctx.Done():
							return appender.Committed{}, ctx.Err()
						case <-release:
						}
					}
					return f.store.Append(ctx, drafts)
				})
			}
			service = f.open(options)
			mark := f.store.Durable()
			startStatus, err := service.Step(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if started := foundationStartedAfter(t, f, mark); started.Source.WorkspaceID != f.registered.ID || started.Reason != ledger.GraphReasonLoad {
				t.Fatalf("wrong rebuild control: %+v", started)
			}
			type outcome struct {
				status graph.Status
				err    error
			}
			done := make(chan outcome, 1)
			if pause == "before final" {
				go func() {
					for range 205 {
						status, err := service.Step(context.Background())
						if err != nil {
							done <- outcome{status, err}
							return
						}
						select {
						case <-entered:
							done <- outcome{status, nil}
							return
						default:
						}
					}
					done <- outcome{err: errors.New("rebuild never reached the final gate")}
				}()
				select {
				case <-entered:
				case result := <-done:
					t.Fatalf("rebuild ended before final gate: %+v", result)
				}
			}
			f.root = otherRoot
			newAcceptance := f.accept(other.ID, otherOld, otherNew, "other.md")
			f.root = priorRoot
			var stale graph.Status
			if pause == "before final" {
				unblock()
				result := <-done
				stale, err = result.status, result.err
			} else {
				stale, err = service.Step(context.Background())
				if stale.Charged != 0 || readCalls != 0 {
					t.Fatalf("doomed rebuild reserved or read: %+v reads=%d", stale, readCalls)
				}
				records := foundationGraphRecords(t, f, mark)
				if len(records) != 2 || records[1].Type != ledger.TypeGraphAnalysisProgress {
					t.Fatalf("early supersession performed more work: %+v", records)
				}
				progress, decodeErr := ledger.DecodeGraphAnalysisProgress(records[1].Payload)
				if decodeErr != nil || progress.Phase != ledger.GraphPhaseSuperseded {
					t.Fatalf("early rebuild was not superseded: %+v, %v", progress, decodeErr)
				}
			}
			if err != nil || stale.Attempt != startStatus.Attempt || stale.Version != before.Version || stale.Digest != before.Digest || stale.Through < newAcceptance.Last || !slices.Contains(stale.Pending, other.ID) {
				t.Fatalf("old rebuild replaced the current image: %+v, %v", stale, err)
			}
			foundationAssertSameState(t, current, foundationState(t, service, stale))
			observer := f.open(f.options())
			observed, err := observer.Observe(context.Background())
			if err != nil || observed.Version != before.Version || observed.Digest != before.Digest || !slices.Contains(observed.Pending, other.ID) {
				t.Fatalf("replay installed stale work: %+v, %v", observed, err)
			}
			foundationAssertSameState(t, current, foundationState(t, observer, observed))
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			service = observer
			// One accepted subject followed the initial load, then the queued
			// first-workspace subject and the racing subject: the counter is 3.
			// Ninety-seven more must select structural_100, not load. A stale
			// rebuild is forbidden to reset that derived counter.
			tree := otherNew
			f.root = otherRoot
			for range 97 {
				target := otherOld
				if tree == otherOld {
					target = otherNew
				}
				f.accept(other.ID, tree, target, "other.md")
				tree = target
			}
			f.root = priorRoot
			mark = f.store.Durable()
			if _, err := service.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if started := foundationStartedAfter(t, f, mark); started.Reason != ledger.GraphReasonStructural100 {
				t.Fatalf("stale rebuild reset the counter: %+v", started)
			}
			eligible := foundationDrive(t, service)
			_, final := foundationFinalAfter(t, f, mark)
			if eligible.Version == before.Version || final.StructuralSinceRebuild != 0 {
				t.Fatalf("fresh frontier was not published: %+v final=%+v", eligible, final)
			}
			newest := f.tree(map[string]fixtureBlob{"first.md": {body: "newest"}})
			f.accept(f.registered.ID, next, newest, "first.md")
			mark = f.store.Durable()
			if _, err := service.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if started := foundationStartedAfter(t, f, mark); started.Reason != ledger.GraphReasonIncremental {
				t.Fatalf("eligible rebuild did not clear all due triggers: %+v", started)
			}
			latest := foundationDrive(t, service)
			replayed := f.open(f.options())
			observed, err = replayed.Observe(context.Background())
			if err != nil || observed.Version != latest.Version || observed.Digest != latest.Digest || latest.Version <= eligible.Version {
				t.Fatalf("old candidate replaced a later incremental version: %+v, %v", observed, err)
			}
			node := foundationArtifact(t, foundationState(t, replayed, observed), f.registered.ID, "first.md")
			if node.VersionSHA256 != ledger.Digest(sha256.Sum256([]byte("newest"))) {
				t.Fatalf("replay lost the newest immutable content: %+v", node)
			}
		})
	}
}

func TestGoGraphVersionRebuildAndReplay(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"dependency lifetimes",
		"import changes",
		"load confirmation",
		"analyzer epoch with tool-free replay",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			goConfig := foundationGoConfig(t)
			f := newFoundationFixture(t)
			options := f.options()
			options.Tools.Go = &goConfig
			service := f.open(options)
			id := f.registered.ID
			files := map[string]fixtureBlob{
				"go.mod":          {body: "module example.test/history\n\ngo 1.27\n"},
				"main.go":         {body: "package history\nimport _ \"example.test/history/dep\"\nfunc init() { panic(\"target executed\") }\n"},
				"standard.go":     {body: "package history\nimport _ \"unsafe\"\n"},
				"ignored_test.go": {body: "package history\nimport _ \"example.invalid/test-only\"\n"},
				"dep/dep.go":      {body: "package dep\nconst Value = 1\n"},
			}
			predecessor := f.empty
			var status graph.Status
			var state graph.State
			var firstBirth, dependencyBirth int64
			module, err := graph.ModuleKey(id, "go", "workspace", "example.test/history/dep")
			if err != nil {
				t.Fatal(err)
			}
			apply := func(wantEdges int, paths ...string) appender.Committed {
				t.Helper()
				tree := f.tree(files)
				accepted := f.accept(id, predecessor, tree, paths...)
				predecessor = tree
				if firstBirth == 0 {
					firstBirth, dependencyBirth = int64(accepted.First), int64(accepted.First)
				}
				oldVersion := status.Version
				status = foundationDrive(t, service)
				if status.State != "ready" || status.Version == oldVersion {
					t.Fatalf("accepted Go state did not publish: %+v", status)
				}
				state = foundationState(t, service, status)
				if len(state.Edges) != wantEdges {
					t.Fatalf("Go topology: %+v; want %d edges; coverage=%+v", state.Edges, wantEdges, state.Coverage)
				}
				if wantEdges == 1 {
					consumer, _ := graph.ArtifactKey(id, firstBirth, "main.go")
					edge := state.Edges[0]
					wantID := ledger.EdgeID(module, consumer, "static_dependency")
					if edge.EdgeID != wantID || edge.From != module || edge.To != consumer || edge.Evidence.Artifact != consumer || edge.Evidence.ArtifactSHA256 != ledger.Digest(sha256.Sum256([]byte(files["main.go"].body))) || edge.Evidence.Reference != "example.test/history/dep" {
						t.Fatalf("Go edge changed identity, direction or proof: %+v", edge)
					}
					version, err := graph.ModuleVersion("example.test/history/dep", []graph.ModuleMember{{Path: "dep/dep.go", SHA256: ledger.Digest(sha256.Sum256([]byte(files["dep/dep.go"].body)))}})
					if err != nil || !slices.ContainsFunc(state.Nodes, func(node ledger.GraphNode) bool { return node.Key == module && node.VersionSHA256 == version }) {
						t.Fatalf("native members did not determine module version: %s, %v", version, err)
					}
				}
				return accepted
			}
			apply(1, "go.mod", "main.go", "standard.go", "ignored_test.go", "dep/dep.go")
			if name == "dependency lifetimes" {
				firstDependency, _ := graph.ArtifactKey(id, dependencyBirth, "dep/dep.go")
				firstEdge := state.Edges[0].EdgeID
				files["dep/dep.go"] = fixtureBlob{body: "package dep\nconst Value = 2\n"}
				apply(1, "dep/dep.go")
				if state.Edges[0].EdgeID != firstEdge || !slices.ContainsFunc(state.Nodes, func(node ledger.GraphNode) bool { return node.Key == firstDependency }) {
					t.Fatal("editing a Go dependency changed stable identities")
				}
				delete(files, "dep/dep.go")
				apply(0, "dep/dep.go")
				if slices.ContainsFunc(state.Nodes, func(node ledger.GraphNode) bool { return node.Key == firstDependency || node.Key == module }) {
					t.Fatal("removed dependency retained a node")
				}
				files["dep/dep.go"] = fixtureBlob{body: "package dep\nconst Value = 2\n"}
				recreated := apply(1, "dep/dep.go")
				reborn, _ := graph.ArtifactKey(id, int64(recreated.First), "dep/dep.go")
				if reborn == firstDependency || !slices.ContainsFunc(state.Nodes, func(node ledger.GraphNode) bool { return node.Key == reborn }) || state.Edges[0].EdgeID != firstEdge {
					t.Fatal("recreation failed to change artifact birth while preserving module/edge identity")
				}
				expected := foundationIncrementalOracle(t, f, f.store.Durable(), options.Tools, 1)
				foundationAssertSameState(t, expected, state)
				return
			}
			if name == "import changes" {
				files["main.go"] = fixtureBlob{body: "package history\nimport _ \"unsafe\"\n"}
				apply(0, "main.go")
				files["main.go"] = fixtureBlob{body: "package history\nimport _ \"example.test/history/dep\"\n"}
				apply(1, "main.go")
				expected := foundationIncrementalOracle(t, f, f.store.Durable(), options.Tools, 1)
				foundationAssertSameState(t, expected, state)
				return
			}

			// Each rebuild starts from a real incremental publication, not the
			// initial load's image.
			files["dep/dep.go"] = fixtureBlob{body: "package dep\nconst Value = 2\n"}
			apply(1, "dep/dep.go")
			if name == "load confirmation" {
				expected := foundationIncrementalOracle(t, f, f.store.Durable(), options.Tools, 1)
				foundationAssertSameState(t, expected, state)
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
				service = f.open(options)
				mark := f.store.Durable()
				if _, err := service.Step(t.Context()); err != nil {
					t.Fatal(err)
				}
				if started := foundationStartedAfter(t, f, mark); started.Reason != ledger.GraphReasonLoad {
					t.Fatalf("reopen did not select load: %+v", started)
				}
				confirmed := foundationDrive(t, service)
				if confirmed.Version != status.Version || confirmed.Digest != status.Digest || confirmed.State != "ready" {
					t.Fatalf("load changed the incremental image: %+v", confirmed)
				}
				return
			}
			// Relocating the identical prebuilt helper is a new configured identity;
			// no analyzed target or helper build runs inside this test.
			copyPath := filepath.Join(t.TempDir(), filepath.Base(goConfig.Helper.Path))
			body, err := os.ReadFile(goConfig.Helper.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(copyPath, body, 0700); err != nil {
				t.Fatal(err)
			}
			changed := goConfig
			changed.Helper.Path = copyPath
			options.Tools.Go = &changed
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			mark := f.store.Durable()
			service = f.open(options)
			if _, err := service.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
			started := foundationStartedAfter(t, f, mark)
			if started.Reason != ledger.GraphReasonAnalyzerEpoch || started.AnalyzerEpoch != 2 {
				t.Fatalf("Go identity change did not select one epoch rebuild: %+v", started)
			}
			rebuilt := foundationDrive(t, service)
			expected := foundationIncrementalOracle(t, f, mark, options.Tools, 2)
			foundationAssertSameState(t, expected, foundationState(t, service, rebuilt))
			mark = f.store.Durable()
			if _, err := service.Step(t.Context()); err != nil || f.store.Durable() != mark {
				t.Fatalf("epoch rebuild remained due: %v", err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			toolFree := f.options()
			toolFree.ReadArtifacts = func(context.Context, graph.ArtifactRequest) (graph.ArtifactBatch, error) {
				t.Fatal("tool-free replay read Git")
				return graph.ArtifactBatch{}, graph.ErrRecovery
			}
			replayed := f.open(toolFree)
			observed, err := replayed.Observe(t.Context())
			if err != nil || observed.Version != rebuilt.Version || observed.Digest != rebuilt.Digest || f.store.Durable() != mark {
				t.Fatalf("tool-free reopen changed the published image: %+v, %v", observed, err)
			}
			foundationAssertSameState(t, expected, foundationState(t, replayed, observed))
		})
	}
}

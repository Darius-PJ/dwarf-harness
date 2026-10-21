package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestGraphPendingWinsOverTerminalServiceReason(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	first, other := graphFixtureID(1), graphFixtureID(2)
	f.register(first)
	f.register(other)
	empty := f.tree("empty", nil)
	one := f.tree("one", map[string]string{"a.md": "one"})
	f.accept(first, empty, one, "a.md")
	service := f.open(serviceDeps{nodeCap: 1})
	published := driveGraph(t, service)
	f.accept(first, one, f.tree("two", map[string]string{"a.md": "one", "b.md": "two"}), "b.md")
	blocked := driveGraph(t, service)
	if blocked.State != "unknown" || !slices.Contains(blocked.Reasons, ledger.ReasonStateCap) || blocked.Version != published.Version {
		t.Fatalf("candidate cap did not preserve the image: %+v", blocked)
	}
	f.accept(other, empty, f.tree("other", map[string]string{"c.md": "other"}), "c.md")
	before := f.writer.Durable()
	status, err := service.Observe(context.Background())
	if err != nil || status.State != "pending" || !slices.Contains(status.Reasons, ledger.ReasonStateCap) || status.Version != published.Version || f.writer.Durable() != before {
		t.Fatalf("terminal reason hid runnable work: %+v, %v", status, err)
	}
}

func TestGraphRebuildIndependentOfImage(t *testing.T) {
	t.Parallel()
	t.Run("births come from history and publication is a net delta", func(t *testing.T) {
		t.Parallel()
		f := newGraphFixture(t)
		first, other := graphFixtureID(1), graphFixtureID(2)
		f.register(first)
		f.register(other)
		empty := f.tree("empty", nil)
		one := f.tree("one", map[string]string{"stable.md": "stable"})
		f.accept(first, empty, one, "stable.md")
		var service *Service
		service = f.open(serviceDeps{incrementalBirths: func(id uuid.UUID, births map[string]int64) {
			if id == first {
				births["recreated.md"] = service.replay.registrations[first].ref.Seq
			}
		}})
		driveGraph(t, service)
		two := f.tree("two", map[string]string{"stable.md": "stable", "recreated.md": "new lifetime"})
		accepted := f.accept(first, one, two, "recreated.md")
		driveGraph(t, service)
		f.accept(other, empty, f.tree("other", map[string]string{"other.md": "independent"}), "other.md")
		before := driveGraph(t, service)
		wrong, err := ArtifactKey(first, service.replay.registrations[first].ref.Seq, "recreated.md")
		if err != nil {
			t.Fatal(err)
		}
		right, err := ArtifactKey(first, int64(accepted.First), "recreated.md")
		if err != nil {
			t.Fatal(err)
		}
		expected := service.replay.current.state
		expected.Nodes = slices.Clone(expected.Nodes)
		found := false
		for i := range expected.Nodes {
			if expected.Nodes[i].Key == wrong {
				expected.Nodes[i].Key, found = right, true
			}
		}
		expected.Coverage = slices.Clone(expected.Coverage)
		for i := range expected.Coverage {
			if expected.Coverage[i].Scope == string(keyBytes(wrong)) {
				expected.Coverage[i].Scope = string(keyBytes(right))
			}
		}
		if !found {
			t.Fatal("incremental seam did not produce the wrong-birth durable control")
		}
		want, wantDigest, err := expected.Canonical()
		if err != nil || wantDigest == before.Digest {
			t.Fatalf("invalid independent expectation: %v", err)
		}
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		rebuild := f.open(serviceDeps{})
		after := driveGraph(t, rebuild)
		got, _, err := rebuild.replay.current.state.Canonical()
		if err != nil || after.State != "ready" || after.Version == before.Version || after.Digest != wantDigest || !bytes.Equal(got, want) || rebuild.replay.structural != 0 {
			t.Fatalf("rebuild copied or lost prior facts: before=%+v after=%+v error=%v", before, after, err)
		}
		var removed []ledger.NodeKey
		var put []ledger.NodeKey
		for _, record := range graphAttemptRecords(t, f, after.Attempt) {
			if record.Type != ledger.TypeGraphDeltaChunk {
				continue
			}
			delta, err := ledger.DecodeGraphDeltaChunk(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			removed = append(removed, delta.RemoveNodes...)
			for _, node := range delta.PutNodes {
				put = append(put, node.Key)
			}
			for _, coverage := range delta.Coverage {
				if id, _ := coverageWorkspace(rebuild.replay.current.index.scopes, coverage); id == other {
					t.Fatal("publishing rebuild re-emitted untouched workspace coverage")
				}
			}
		}
		if !slices.Equal(removed, []ledger.NodeKey{wrong}) || !slices.Equal(put, []ledger.NodeKey{right}) {
			t.Fatalf("rebuild replaced unchanged nodes: removes=%v puts=%v", removed, put)
		}
		replayed := f.open(serviceDeps{})
		got, digest, err := replayed.replay.current.state.Canonical()
		if err != nil || digest != wantDigest || !bytes.Equal(got, want) {
			t.Fatalf("corrective delta could not be replayed: %v", err)
		}
		confirmed := driveGraph(t, replayed)
		if confirmed.Version != after.Version || confirmed.Digest != after.Digest {
			t.Fatalf("corrected history changed on confirmation: %+v", confirmed)
		}
	})
	t.Run("projection identity mismatch confirms the unmodified durable base", func(t *testing.T) {
		t.Parallel()
		f := newGraphFixture(t)
		id := graphFixtureID(1)
		f.register(id)
		f.accept(id, f.tree("empty", nil), f.tree("one", map[string]string{"a.md": "stable"}), "a.md")
		service := f.open(serviceDeps{})
		before := driveGraph(t, service)
		expected, _, err := service.replay.current.state.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		perturbed := false
		rebuild := f.open(serviceDeps{projectionIdentity: func(version int64, digest ledger.Digest) (int64, ledger.Digest) {
			if !perturbed {
				perturbed = true
				digest[0] ^= 0xff
			}
			return version, digest
		}})
		mark := f.writer.Durable()
		if _, err := rebuild.Observe(context.Background()); err != nil || f.writer.Durable() != mark {
			t.Fatalf("projection checking appended without Step: %v", err)
		}
		started, err := rebuild.Step(context.Background())
		if err != nil || rebuild.replay.attempt == nil || rebuild.replay.attempt.started.Reason != ledger.GraphReasonDigestMismatch {
			t.Fatalf("mismatch did not win over load: %+v, %v", started, err)
		}
		after := driveGraph(t, rebuild)
		actual, _, err := rebuild.replay.current.state.Canonical()
		if err != nil || !bytes.Equal(expected, actual) || after.Version != before.Version || after.Digest != before.Digest || rebuild.replay.structural != 0 {
			t.Fatalf("projection mismatch repaired durable facts: %+v, %v", after, err)
		}
		final := graphAttemptFinal(t, f, after.Attempt)
		if final.ChunkCount != 0 || final.ChunksSHA256 != ledger.EmptyChunksDigest || final.GraphDigest != final.BaseDigest || final.StructuralSinceRebuild != 0 {
			t.Fatalf("mismatch was not a zero-net confirmation: %+v", final)
		}
		mark = f.writer.Durable()
		if _, err := rebuild.Step(context.Background()); err != nil || f.writer.Durable() != mark {
			t.Fatalf("eligible confirmation left a trigger due: %v", err)
		}
		reopened := f.open(serviceDeps{})
		if status, err := reopened.Step(context.Background()); err != nil || status.State != "ready" || status.Version != after.Version || f.writer.Durable() != mark {
			t.Fatalf("terminal rebuild received new credit on load: %+v, %v", status, err)
		}
	})
}

func TestGraphRebuildReadsEachObjectOnce(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	first, other := graphFixtureID(1), graphFixtureID(2)
	f.register(first)
	f.register(other)
	empty := f.tree("empty", nil)
	one := f.tree("one", map[string]string{"a.md": "same object"})
	firstAcceptance := f.accept(first, empty, one, "a.md")
	secondAcceptance := f.accept(other, empty, f.tree("other", map[string]string{"b.md": "same object"}), "b.md")
	counts := make(map[string]int)
	options := f.options()
	options.ReadArtifacts = func(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
		batch, err := f.read(ctx, request)
		if !request.ListOnly {
			for _, entry := range batch.Entries {
				counts[entry.GitOID]++
			}
		}
		return batch, err
	}
	service, err := openService(options, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	status := driveGraph(t, service)
	firstKey, _ := ArtifactKey(first, int64(firstAcceptance.First), "a.md")
	secondKey, _ := ArtifactKey(other, int64(secondAcceptance.First), "b.md")
	page, err := service.Page(graphPageRequest(status, "nodes"))
	if err != nil || len(page.Nodes) != 2 || page.Nodes[0].Key != firstKey || page.Nodes[1].Key != secondKey {
		t.Fatalf("OID memoization coalesced acceptance identities: %+v, %v", page.Nodes, err)
	}
	hash := sha256.Sum256([]byte("same object"))
	oid := fmt.Sprintf("%x", hash[:20])
	if counts[oid] != 1 {
		t.Fatalf("same immutable object was read %d times in one rebuild", counts[oid])
	}
	two := f.tree("two", map[string]string{"a.md": "same object", "new.md": "new object"})
	f.accept(first, one, two, "new.md")
	driveGraph(t, service)
	if counts[oid] != 2 {
		t.Fatalf("object bytes leaked across attempt boundaries: reads=%d", counts[oid])
	}
}

func TestGraphExhaustedKeyNeverRefreshes(t *testing.T) {
	t.Parallel()
	for _, recovery := range []string{"marked acceptance", "new epoch"} {
		t.Run(recovery, func(t *testing.T) {
			t.Parallel()
			f := newGraphFixture(t)
			marked, other := graphFixtureID(1), graphFixtureID(2)
			f.register(marked)
			f.register(other)
			empty := f.tree("empty", nil)
			one := f.tree("one", map[string]string{"a.md": "one"})
			two := f.tree("two", map[string]string{"b.md": "two"})
			f.accept(marked, empty, one, "a.md")
			f.accept(other, empty, two, "b.md")
			service := f.open(serviceDeps{})
			current := driveGraph(t, service)
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			options := f.options()
			costly := true
			options.ReadArtifacts = func(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
				if costly && request.WorkspaceID == marked && request.ListOnly && len(request.Paths) == 0 && request.Tree == empty {
					next := request.AfterPath + "a"
					return ArtifactBatch{Entries: []Artifact{{Path: next, Mode: "100644", GitOID: one}}, NextPath: next, WorkUsed: request.WorkAllowance}, nil
				}
				return f.read(ctx, request)
			}
			open := func(options Options) *Service {
				service, err := openService(options, serviceDeps{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = service.Close() })
				return service
			}
			service = open(options)
			exhausted := driveGraph(t, service)
			if exhausted.State != "unknown" || exhausted.Charged != attemptLimit || exhausted.Version != current.Version || !slices.Contains(exhausted.Reasons, ledger.ReasonWorkExhausted) {
				t.Fatalf("rebuild did not retain its exhausted key: %+v", exhausted)
			}
			records := graphAttemptRecords(t, f, exhausted.Attempt)
			terminal, err := ledger.DecodeGraphAnalysisProgress(records[len(records)-1].Payload)
			if err != nil || len(terminal.Coverage) != 1 || terminal.Coverage[0].Scope != workspaceScope(marked) {
				t.Fatalf("exhaustion was attributed to the frontier workspace: %+v, %v", terminal, err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			service = open(options)
			mark := f.writer.Durable()
			if status, err := service.Resume(context.Background(), uuid.NewV7()); !errors.Is(err, ErrInvalid) || status.Version != current.Version || f.writer.Durable() != mark {
				t.Fatalf("new UUID refreshed exhaustion: %+v, %v", status, err)
			}
			if status, err := service.Step(context.Background()); err != nil || status.State != "unknown" || f.writer.Durable() != mark {
				t.Fatalf("reopen refreshed exhaustion: %+v, %v", status, err)
			}
			next := f.tree("other changed", map[string]string{"b.md": "changed"})
			f.accept(other, two, next, "b.md")
			if status, err := service.Step(context.Background()); err != nil || service.replay.attempt == nil || service.replay.attempt.started.Reason != ledger.GraphReasonIncremental || service.replay.attempt.started.Source.WorkspaceID != other {
				t.Fatalf("unrelated acceptance refreshed the rebuild or blocked its own scope: %+v, %v", status, err)
			}
			unrelated := driveGraph(t, service)
			if unrelated.State != "unknown" || unrelated.Version == current.Version || !slices.Contains(unrelated.Reasons, ledger.ReasonWorkExhausted) {
				t.Fatalf("unrelated publication cleared the marker: %+v", unrelated)
			}
			mark = f.writer.Durable()
			if _, err := service.Step(context.Background()); err != nil || f.writer.Durable() != mark {
				t.Fatalf("unrelated version gave the rebuild fresh credit: %v", err)
			}
			costly = false
			if recovery == "marked acceptance" {
				f.accept(marked, one, f.tree("marked changed", map[string]string{"a.md": "smaller"}), "a.md")
			} else {
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
				options.Tools.Python = &Tool{Path: filepath.Join(t.TempDir(), "unused-python.exe"), SHA256: ledger.Digest(sha256.Sum256([]byte("new identity"))), Version: "new"}
				service = open(options)
			}
			if status, err := service.Step(context.Background()); err != nil || service.replay.attempt == nil || service.replay.attempt.started.Reason == ledger.GraphReasonIncremental || status.AttemptKey == exhausted.AttemptKey || status.Charged != 0 {
				t.Fatalf("changed marked source or epoch did not grant a fresh key: %+v, %v", status, err)
			}
			recovered := driveGraph(t, service)
			if recovered.State != "ready" || recovered.Version == unrelated.Version || slices.Contains(recovered.Reasons, ledger.ReasonWorkExhausted) {
				t.Fatalf("fresh key did not recover the complete rebuild: %+v", recovered)
			}
		})
	}
}

func TestGraphScaledStructuralCadenceAndLoadConfirmation(t *testing.T) {
	t.Parallel()
	// The real-Git foundation row keeps the frozen 100-acceptance boundary.
	// This private threshold exercises the same transitions under race.
	deps := serviceDeps{structuralCadence: 3}
	f := newGraphFixture(t)
	workspace := graphFixtureID(1)
	f.register(workspace)
	trees := [2]string{
		f.tree("cadence-zero", map[string]string{"tick.md": "zero"}),
		f.tree("cadence-one", map[string]string{"tick.md": "one"}),
	}
	f.accept(workspace, f.tree("cadence-empty", nil), trees[0], "tick.md")
	service := f.open(deps)
	before := driveGraph(t, service)
	for i := 1; i <= 2; i++ {
		f.accept(workspace, trees[(i-1)%2], trees[i%2], "tick.md")
		if _, err := service.Step(t.Context()); err != nil || service.replay.attempt == nil || service.replay.attempt.started.Reason != ledger.GraphReasonIncremental {
			t.Fatalf("acceptance below the threshold selected a rebuild: %v", err)
		}
		before = driveGraph(t, service)
	}
	if final := graphAttemptFinal(t, f, before.Attempt); final.StructuralSinceRebuild != 2 {
		t.Fatalf("counter before the threshold: %+v", final)
	}
	threshold := f.accept(workspace, trees[0], trees[1], "tick.md")
	selected, err := service.Step(t.Context())
	if err != nil || service.replay.attempt == nil {
		t.Fatalf("threshold acceptance did not start work: %+v, %v", selected, err)
	}
	started := service.replay.attempt.started
	if started.Reason != ledger.GraphReasonStructural100 || started.Source.Subject.Seq != int64(threshold.First) || started.Source.Subject != started.InputFrontier || selected.Version != before.Version {
		t.Fatalf("threshold did not select rebuild before incremental work: %+v status=%+v", started, selected)
	}
	rebuilt := driveGraph(t, service)
	final := graphAttemptFinal(t, f, rebuilt.Attempt)
	records := graphAttemptRecords(t, f, rebuilt.Attempt)
	if rebuilt.State != "ready" || rebuilt.Version != int64(records[len(records)-1].Seq) || rebuilt.Version == before.Version || final.ChunkCount == 0 || final.StructuralSinceRebuild != 0 {
		t.Fatalf("cadence did not publish and reset: %+v final=%+v", rebuilt, final)
	}
	page, err := service.Page(PageRequest{Version: rebuilt.Version, Digest: rebuilt.Digest, Through: rebuilt.Through, ChainHash: rebuilt.ChainHash, Kind: "nodes", Limit: 256})
	if err != nil || len(page.Nodes) != 1 || page.Nodes[0].VersionSHA256 != ledger.Digest(sha256.Sum256([]byte("one"))) {
		t.Fatalf("cadence published the wrong accepted content: %+v, %v", page, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service = f.open(deps)
	mark := f.writer.Durable()
	if observed, err := service.Observe(t.Context()); err != nil || observed.State != "pending" || f.writer.Durable() != mark {
		t.Fatalf("load observation ran work: %+v, %v", observed, err)
	}
	if _, err := service.Step(t.Context()); err != nil || service.replay.attempt == nil || service.replay.attempt.started.Reason != ledger.GraphReasonLoad {
		t.Fatalf("load confirmation was not selected: %v", err)
	}
	confirmed := driveGraph(t, service)
	final = graphAttemptFinal(t, f, confirmed.Attempt)
	if confirmed.State != "ready" || confirmed.Version != rebuilt.Version || confirmed.Digest != rebuilt.Digest || final.ChunkCount != 0 || final.ChunksSHA256 != ledger.EmptyChunksDigest || final.StructuralSinceRebuild != 0 || final.GraphDigest != final.BaseDigest {
		t.Fatalf("load did not confirm the unchanged image: %+v final=%+v", confirmed, final)
	}
	mark = f.writer.Durable()
	if status, err := service.Step(t.Context()); err != nil || status.State != "ready" || f.writer.Durable() != mark {
		t.Fatalf("load remained due after confirmation: %+v, %v", status, err)
	}
}

func TestGraphRebuildTriggerPrecedence(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, want      string
		epoch, mismatch bool
	}{
		{"epoch over mismatch cadence and load", ledger.GraphReasonAnalyzerEpoch, true, true},
		{"mismatch over cadence and load", ledger.GraphReasonDigestMismatch, false, true},
		{"cadence over load", ledger.GraphReasonStructural100, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newGraphFixture(t)
			workspace := uuid.NewV7()
			f.register(workspace)
			empty := f.tree("precedence-empty", nil)
			trees := [2]string{
				f.tree("precedence-zero", map[string]string{"tick.md": "zero"}),
				f.tree("precedence-one", map[string]string{"tick.md": "one"}),
			}
			f.accept(workspace, empty, trees[0], "tick.md")
			service := f.open(serviceDeps{})
			before := driveGraph(t, service)
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			previous := trees[0]
			for i := 1; i <= 100; i++ {
				next := trees[i%2]
				f.accept(workspace, previous, next, "tick.md")
				previous = next
			}
			options := f.options()
			if row.epoch {
				options.Tools.Python = &Tool{Path: filepath.Join(f.writer.directory, "unused-python.exe"), SHA256: ledger.Digest(sha256.Sum256([]byte("new identity"))), Version: "3.13.15"}
			}
			var deps serviceDeps
			if row.mismatch {
				corrupt := true
				deps.projectionIdentity = func(version int64, digest ledger.Digest) (int64, ledger.Digest) {
					if corrupt {
						digest[0] ^= 1
						corrupt = false
					}
					return version, digest
				}
			}
			var err error
			service, err = openService(options, deps)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			if status, err := service.Step(context.Background()); err != nil || service.replay.attempt == nil || service.replay.attempt.started.Reason != row.want {
				t.Fatalf("wrong rebuild precedence: %+v, %v", status, err)
			}
			after := driveGraph(t, service)
			if after.State != "ready" || after.Version == before.Version || graphAttemptFinal(t, f, after.Attempt).StructuralSinceRebuild != 0 {
				t.Fatalf("eligible rebuild did not publish/reset: %+v", after)
			}
			mark := f.writer.Durable()
			if _, err := service.Step(context.Background()); err != nil || f.writer.Durable() != mark {
				t.Fatalf("one eligible rebuild did not clear all triggers: %v", err)
			}
		})
	}
}

func TestGraphEdgeCapDoesNotPublishPartialState(t *testing.T) {
	t.Parallel()
	// The production memory caps dominate the 200,000-edge boundary. This private
	// scaled row isolates the same guard without weakening any production cap.
	cap := int64(4)
	deps := serviceDeps{edgeCap: &cap}
	f := newGraphFixture(t)
	workspace := uuid.NewV7()
	f.register(workspace)
	empty := f.tree("edge-empty", nil)
	files := map[string]string{"definition.sql": "CREATE TABLE t (id INTEGER);"}
	for i := 0; i < 4; i++ {
		files[fmt.Sprintf("use%d.sql", i)] = "SELECT * FROM t;"
	}
	first := f.tree("edge-boundary", files)
	accepted := f.accept(workspace, empty, first, "definition.sql", "use0.sql", "use1.sql", "use2.sql", "use3.sql")
	service := f.open(deps)
	before := driveGraph(t, service)
	page, err := service.Page(graphPageRequest(before, "edges"))
	if err != nil || before.State != "ready" || len(page.Edges) != 4 || page.Next != nil {
		t.Fatalf("edge boundary was not published: %+v, %+v, %v", before, page, err)
	}
	files["use4.sql"] = "SELECT * FROM t;"
	second := f.tree("edge-overflow", files)
	f.accept(workspace, first, second, "use4.sql")
	after := driveGraph(t, service)
	page, err = service.Page(graphPageRequest(after, "edges"))
	if err != nil || after.State != "unknown" || !slices.Contains(after.Reasons, ledger.ReasonStateCap) || after.Version != before.Version || after.Digest != before.Digest || len(page.Edges) != 4 {
		t.Fatalf("edge overflow replaced the old image: %+v, %+v, %v", after, page, err)
	}

	t.Run("state_cap replay mutation", func(t *testing.T) {
		t.Parallel()
		commits := graphHistory(t, f.writer)
		var target ledger.Record
		for _, commit := range commits {
			for _, record := range commit.Records {
				if record.Type == ledger.TypeGraphDeltaChunk {
					target = record
					break
				}
			}
			if target.Seq != 0 {
				break
			}
		}
		definition, err := ArtifactKey(workspace, int64(accepted.First), "definition.sql")
		if err != nil {
			t.Fatal(err)
		}
		mutation := graphMutation{"formed edge cap", "state_cap", target, func(t testing.TB, draft ledger.Draft, _ byte) ledger.Draft {
			chunk, err := ledger.DecodeGraphDeltaChunk(draft.Payload)
			if err != nil || len(chunk.PutEdges) != 4 {
				t.Fatalf("edge-cap mutation fixture: %+v, %v", chunk, err)
			}
			edge := chunk.PutEdges[0]
			edge.To, edge.Evidence.Artifact = definition, definition
			edge.Evidence.ArtifactSHA256 = ledger.Digest(sha256.Sum256([]byte(files["definition.sql"])))
			edge.EdgeID = ledger.EdgeID(edge.From, edge.To, edge.Type)
			chunk.PutEdges = append(chunk.PutEdges, edge)
			slices.SortFunc(chunk.PutEdges, func(a, b ledger.GraphEdge) int { return bytes.Compare(a.EdgeID[:], b.EdgeID[:]) })
			draft.Payload = graphEncoded(t, chunk)
			return draft
		}}
		assertGraphMutation(t, commits, mutation, 1, deps)
	})
}

func graphAttemptRecords(t testing.TB, f *graphFixture, id uuid.UUID) []ledger.Record {
	t.Helper()
	var records []ledger.Record
	for _, commit := range graphHistory(t, f.writer) {
		for _, record := range commit.Records {
			if graphType(record.Type) && record.OperationID == id {
				records = append(records, record)
			}
		}
	}
	return records
}

func graphAttemptFinal(t testing.TB, f *graphFixture, id uuid.UUID) ledger.GraphVersionCommitted {
	t.Helper()
	records := graphAttemptRecords(t, f, id)
	if len(records) == 0 || records[len(records)-1].Type != ledger.TypeGraphVersionCommitted {
		t.Fatal("analysis did not produce a durable final")
	}
	final, err := ledger.DecodeGraphVersionCommitted(records[len(records)-1].Payload)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func TestGraphScopeOnlyRebuild(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	id := graphFixtureID(1)
	f.register(id)
	files := map[string]string{"a.md": "unchanged"}
	tree := f.tree("regular", files)
	f.accept(id, f.tree("empty", nil), tree, "a.md")
	service := f.open(serviceDeps{})
	before := driveGraph(t, service)
	old := service.replay.current.state
	_ = service.Close()
	executableTree := f.tree("executable", files)
	f.trees[executableTree][0].Mode = "100755"
	accepted := f.accept(id, tree, executableTree, "a.md")
	rebuild := f.open(serviceDeps{})
	after := driveGraph(t, rebuild)
	final := graphAttemptFinal(t, f, after.Attempt)
	if after.Version != int64(f.writer.Durable()) || after.Version == before.Version || after.Digest == before.Digest || final.ChunkCount != 1 || rebuild.replay.structural != 0 {
		t.Fatalf("scope-only publication was lost: before=%+v after=%+v final=%+v", before, after, final)
	}
	current := rebuild.replay.current.state
	if current.Scopes[0].Source.Subject.Seq != int64(accepted.First) || current.Scopes[0].Source.Tree != executableTree || !slices.Equal(old.Nodes, current.Nodes) || !slices.Equal(old.Edges, current.Edges) || !slices.EqualFunc(old.Coverage, current.Coverage, sameCoverage) {
		t.Fatal("scope-only publication changed facts or failed to advance subject")
	}
	var chunks, checkpoints int
	for _, record := range graphAttemptRecords(t, f, after.Attempt) {
		if record.Type == ledger.TypeGraphDeltaChunk {
			chunk, err := ledger.DecodeGraphDeltaChunk(record.Payload)
			if err != nil || chunk.ChunkIndex != 0 || len(chunk.RemoveEdges)+len(chunk.RemoveNodes)+len(chunk.PutNodes)+len(chunk.PutEdges)+len(chunk.Coverage) != 0 {
				t.Fatalf("scope-only chunk was not an ordinary empty mutation: %+v, %v", chunk, err)
			}
			chunks++
		}
		if record.Type == ledger.TypeGraphAnalysisProgress {
			progress, err := ledger.DecodeGraphAnalysisProgress(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if progress.Phase == ledger.GraphPhaseCheckpoint {
				checkpoints++
			}
		}
	}
	if chunks != 1 || checkpoints != 1 {
		t.Fatalf("scope-only durable boundary: chunks=%d checkpoints=%d", chunks, checkpoints)
	}
	assertGraphExactReplay(t, f, rebuild)
	assertGraphHistoryCursors(t, f)
	_ = rebuild.Close()
	confirmation := f.open(serviceDeps{})
	confirmed := driveGraph(t, confirmation)
	if confirmed.Version != after.Version || confirmed.Digest != after.Digest || graphAttemptFinal(t, f, confirmed.Attempt).ChunkCount != 0 {
		t.Fatalf("unchanged control emitted a mutation: %+v", confirmed)
	}
}

func TestGraphCoverageRebuildComparesStateAndReasons(t *testing.T) {
	t.Parallel()
	f, _, original, _ := graphCoverageHistory(t, false, false, false)
	service := f.open(serviceDeps{})
	status := driveGraph(t, service)
	if status.State != "ready" || status.Digest != original.Digest {
		t.Fatalf("coverage state/reasons were not corrected: %+v", status)
	}
	var replaced []ledger.CoverageEntry
	for _, record := range graphAttemptRecords(t, f, status.Attempt) {
		if record.Type != ledger.TypeGraphDeltaChunk {
			continue
		}
		chunk, err := ledger.DecodeGraphDeltaChunk(record.Payload)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range chunk.Coverage {
			id, _ := coverageWorkspace(service.replay.current.index.scopes, entry)
			if id != graphFixtureID(1) {
				t.Fatal("unchanged workspace coverage was not omitted")
			}
			replaced = append(replaced, entry)
		}
	}
	var expected []ledger.CoverageEntry
	for _, entry := range service.replay.current.state.Coverage {
		if id, _ := coverageWorkspace(service.replay.current.index.scopes, entry); id == graphFixtureID(1) {
			expected = append(expected, entry)
		}
	}
	if !slices.EqualFunc(replaced, expected, sameCoverage) {
		t.Fatal("changed workspace did not emit its entire replacement set")
	}
	assertGraphExactReplay(t, f, service)
}

func TestGraphCoverageProducerEmptyFallback(t *testing.T) {
	t.Parallel()
	f, _, _, _ := graphCoverageHistory(t, false, false, false)
	service := f.open(serviceDeps{})
	if _, err := service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := service.work
	expected := service.replay.current.state
	expected.Coverage = nil
	for _, entry := range service.replay.current.state.Coverage {
		if id, _ := coverageWorkspace(w.scopes, entry); id != graphFixtureID(1) {
			expected.Coverage = append(expected.Coverage, entry)
		}
	}
	// Valid rebuilt facts at the diff boundary isolate the encoding's empty-set
	// case; ordinary corpus construction also emits upstream-unknown entries.
	w.sorted = expected
	w.stage = newGraphIndex()
	for _, node := range expected.Nodes {
		w.stage.putNode(node)
	}
	for _, entry := range expected.Coverage {
		w.stage.putCoverage(entry)
	}
	w.phase = 8
	status := driveGraph(t, service)
	want, digest, err := expected.Canonical()
	got, _, actualErr := service.replay.current.state.Canonical()
	if err != nil || actualErr != nil || status.State != "ready" || status.Digest != digest || !bytes.Equal(want, got) {
		t.Fatalf("empty replacement did not select legacy publication: %+v errors=%v/%v", status, err, actualErr)
	}
	assertGraphExactReplay(t, f, service)
}

func graphPrefixFixture(t *testing.T, source *graphFixture, through uint64, mutation graphMutation) *graphFixture {
	t.Helper()
	var prefix []ledger.Commit
	for _, commit := range graphHistory(t, source.writer) {
		if commit.Last <= through {
			prefix = append(prefix, commit)
		}
	}
	options := graphMutatedOptions(t, prefix, mutation, 1)
	f := &graphFixture{t: t, trees: source.trees, writer: &graphFileAppender{colony: options.Colony, directory: options.LogDir, chain: ledger.Chain{Seq: through}}}
	commits := graphHistory(t, f.writer)
	last := commits[len(commits)-1].Records
	record := last[len(last)-1]
	f.writer.chain.Hash, f.writer.chain.EventTimeNS = record.Hash, record.EventTimeNS
	return f
}

func graphChunkPayloads(t *testing.T, f *graphFixture, id uuid.UUID) ([][]byte, []uint64) {
	t.Helper()
	var payloads [][]byte
	var checkpoints []uint64
	for _, commit := range graphHistory(t, f.writer) {
		for _, record := range commit.Records {
			if record.OperationID == id && record.Type == ledger.TypeGraphDeltaChunk {
				payloads = append(payloads, record.Payload)
				checkpoints = append(checkpoints, commit.Last)
			}
		}
	}
	return payloads, checkpoints
}

func TestGraphRebuildResumeEncodings(t *testing.T) {
	t.Parallel()
	base, _, original, _ := graphCoverageHistorySize(t, false, false, false, true)
	starting := base.open(serviceDeps{})
	start, err := starting.Step(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = starting.Close()
	startThrough := base.writer.Durable()
	type encodingControl struct {
		fixture     *graphFixture
		chunks      [][]byte
		checkpoints []uint64
		reads       int
	}
	controls := make(map[bool]encodingControl)
	for _, legacy := range []bool{false, true} {
		f := graphPrefixFixture(t, base, startThrough, graphMutation{})
		options := f.options()
		reads := 0
		options.ReadArtifacts = func(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
			reads++
			return f.read(ctx, request)
		}
		service, err := openService(options, serviceDeps{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Resume(t.Context(), start.Attempt); err != nil {
			t.Fatal(err)
		}
		encodingReached := false
		for range 200 {
			if service.work != nil && service.work.phase == 9 {
				if legacy {
					service.work.useLegacyEncoding()
				}
				encodingReached = true
				break
			}
			if _, err := service.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if !encodingReached {
			t.Fatal("fixture failed before encoding selection")
		}
		status := driveGraph(t, service)
		if status.State != "ready" || status.Digest != original.Digest {
			t.Fatalf("encoding control did not restore independent facts: %+v", status)
		}
		chunks, checkpoints := graphChunkPayloads(t, f, start.Attempt)
		controls[legacy] = encodingControl{f, chunks, checkpoints, reads}
		_ = service.Close()
	}
	selective, legacy := controls[false], controls[true]
	if len(selective.chunks) < 2 || len(legacy.chunks) < 2 || !bytes.Equal(selective.chunks[0], legacy.chunks[0]) || bytes.Equal(selective.chunks[1], legacy.chunks[1]) {
		t.Fatal("fixture lacks a common-first, distinguishing-later prefix")
	}
	for _, row := range []struct {
		name    string
		legacy  bool
		prefix  int
		corrupt bool
	}{
		{"legacy distinguishing later", true, 2, false},
		{"selective distinguishing later", false, 2, false},
		{"both match prefers selective", true, 1, false},
		{"neither matches", false, 2, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			control := controls[row.legacy]
			through := control.checkpoints[row.prefix-1]
			var mutation graphMutation
			if row.corrupt {
				for _, record := range graphAttemptRecords(t, control.fixture, start.Attempt) {
					if record.Type != ledger.TypeGraphDeltaChunk {
						continue
					}
					chunk, err := ledger.DecodeGraphDeltaChunk(record.Payload)
					if err != nil {
						t.Fatal(err)
					}
					if chunk.ChunkIndex == 1 {
						mutation = graphMutation{target: record, change: func(t testing.TB, draft ledger.Draft, _ byte) ledger.Draft {
							chunk.PutNodes[0].VersionSHA256[0] ^= 1
							draft.Payload = graphEncoded(t, chunk)
							return draft
						}}
					}
				}
			}
			f := graphPrefixFixture(t, control.fixture, through, mutation)
			persisted, _ := graphChunkPayloads(t, f, start.Attempt)
			options := f.options()
			reads := 0
			options.ReadArtifacts = func(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
				reads++
				return f.read(ctx, request)
			}
			service, err := openService(options, serviceDeps{})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			prior := service.replay.attempt.attemptSummary
			if _, err := service.Resume(t.Context(), start.Attempt); err != nil {
				t.Fatal(err)
			}
			status := driveGraph(t, service)
			actual, _ := graphChunkPayloads(t, f, start.Attempt)
			if status.Attempt != start.Attempt || status.AttemptKey != start.AttemptKey || status.Charged <= prior.reserved || reads != selective.reads {
				t.Fatalf("Resume reset identity/charges or rebuilt facts to select encoding: %+v prior=%+v reads=%d/%d", status, prior, reads, selective.reads)
			}
			for index, payload := range persisted {
				if !bytes.Equal(payload, actual[index]) {
					t.Fatal("Resume rewrote a persisted payload")
				}
			}
			if row.corrupt {
				if status.State != "unknown" || !slices.Contains(status.Reasons, ledger.ReasonRecoveryUnknown) || len(actual) != len(persisted) || status.Digest != prior.started.BaseDigest {
					t.Fatalf("unmatched prefix appended or published: %+v", status)
				}
				return
			}
			want := control.chunks
			if row.prefix == 1 {
				want = selective.chunks
			}
			if status.State != "ready" || status.Digest != original.Digest || !slices.EqualFunc(actual, want, bytes.Equal) || status.VerifiedCursor.ArtifactIndex != prior.cursor.ArtifactIndex {
				t.Fatalf("Resume changed encoding or lost reconstructed state: %+v", status)
			}
			assertGraphExactReplay(t, f, service)
			lastReserved := int64(0)
			for _, record := range graphAttemptRecords(t, f, start.Attempt) {
				if record.Type != ledger.TypeGraphAnalysisProgress {
					continue
				}
				progress, err := ledger.DecodeGraphAnalysisProgress(record.Payload)
				if err != nil || progress.ReservedTotal < lastReserved {
					t.Fatalf("Resume refunded original reservations: %+v, %v", progress, err)
				}
				lastReserved = progress.ReservedTotal
			}
		})
	}
}

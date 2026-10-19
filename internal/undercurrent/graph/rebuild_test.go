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

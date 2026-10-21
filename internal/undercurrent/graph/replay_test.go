package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestGraphReplayRefusesFrozenGolden(t *testing.T) {
	t.Parallel()
	directory, err := filepath.Abs("../../../contract/ledger/testdata/u3/61/log")
	if err != nil {
		t.Fatal(err)
	}
	service, err := Open(Options{Colony: uuid.MustParse("0199c000-0000-7000-8000-000000000006"), LogDir: directory, Through: 62})
	var failure *HistoryError
	if service != nil || !errors.Is(err, ErrHistory) || !errors.As(err, &failure) || failure.Seq != 11 || failure.Rule != "analysis_key" {
		t.Fatalf("encoding-only fixture became a service history: service=%v error=%v", service, err)
	}
}

type graphMutation struct {
	name, rule string
	target     ledger.Record
	change     func(testing.TB, ledger.Draft, byte) ledger.Draft
}

func graphMutationHistory(t testing.TB) ([]ledger.Commit, Status) {
	t.Helper()
	fixture := newGraphFixture(t)
	workspace := graphFixtureID(1)
	fixture.register(workspace)
	first := fixture.tree("first", map[string]string{"create.sql": "CREATE TABLE t(id INTEGER);", "use.sql": "SELECT * FROM t;", "gone.py": ""})
	fixture.accept(workspace, fixture.tree("empty", nil), first, "create.sql", "gone.py", "use.sql")
	service := fixture.open(serviceDeps{})
	driveGraph(t, service)
	second := fixture.tree("second", map[string]string{"create.sql": "CREATE TABLE u(id INTEGER);", "use.sql": "SELECT * FROM u;", "new.py": ""})
	fixture.accept(workspace, first, second, "create.sql", "gone.py", "new.py", "use.sql")
	status := driveGraph(t, service)
	if status.Version == 0 || len(status.Pending) != 0 {
		t.Fatalf("invalid mutation control: %+v", status)
	}
	return graphHistory(t, fixture.writer), status
}

func graphMutations(t testing.TB, commits []ledger.Commit) []graphMutation {
	t.Helper()
	var starts, reserved, chunks, checkpoints, finals []ledger.Record
	for _, commit := range commits {
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypeGraphAnalysisStarted:
				starts = append(starts, record)
			case ledger.TypeGraphAnalysisProgress:
				p, err := ledger.DecodeGraphAnalysisProgress(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if p.Phase == ledger.GraphPhaseReserved {
					reserved = append(reserved, record)
				}
				if p.Phase == ledger.GraphPhaseCheckpoint {
					checkpoints = append(checkpoints, record)
				}
			case ledger.TypeGraphDeltaChunk:
				chunks = append(chunks, record)
			case ledger.TypeGraphVersionCommitted:
				finals = append(finals, record)
			}
		}
	}
	if len(starts) != 2 || len(reserved) < 2 || len(chunks) < 2 || len(checkpoints) < 2 || len(finals) != 2 {
		t.Fatal("mutation control did not produce two complete analyses")
	}
	start := func(change func(*ledger.GraphAnalysisStarted, byte)) func(testing.TB, ledger.Draft, byte) ledger.Draft {
		return func(t testing.TB, draft ledger.Draft, value byte) ledger.Draft {
			p, err := ledger.DecodeGraphAnalysisStarted(draft.Payload)
			if err != nil {
				t.Fatal(err)
			}
			change(&p, value)
			draft.Payload = graphEncoded(t, p)
			return draft
		}
	}
	progress := func(change func(*ledger.GraphAnalysisProgress, byte)) func(testing.TB, ledger.Draft, byte) ledger.Draft {
		return func(t testing.TB, draft ledger.Draft, value byte) ledger.Draft {
			p, err := ledger.DecodeGraphAnalysisProgress(draft.Payload)
			if err != nil {
				t.Fatal(err)
			}
			change(&p, value)
			draft.Payload = graphEncoded(t, p)
			return draft
		}
	}
	chunk := func(change func(*ledger.GraphDeltaChunk, byte)) func(testing.TB, ledger.Draft, byte) ledger.Draft {
		return func(t testing.TB, draft ledger.Draft, value byte) ledger.Draft {
			p, err := ledger.DecodeGraphDeltaChunk(draft.Payload)
			if err != nil {
				t.Fatal(err)
			}
			change(&p, value)
			draft.Payload = graphEncoded(t, p)
			return draft
		}
	}
	final := func(change func(*ledger.GraphVersionCommitted, byte)) func(testing.TB, ledger.Draft, byte) ledger.Draft {
		return func(t testing.TB, draft ledger.Draft, value byte) ledger.Draft {
			p, err := ledger.DecodeGraphVersionCommitted(draft.Payload)
			if err != nil {
				t.Fatal(err)
			}
			change(&p, value)
			draft.Payload = graphEncoded(t, p)
			return draft
		}
	}
	foreignDigest := ledger.Digest(sha256.Sum256([]byte("not the current base")))
	missing, err := ArtifactKey(graphFixtureID(1), 1, "missing.py")
	if err != nil {
		t.Fatal(err)
	}
	rows := []graphMutation{
		{"analysis key", "analysis_key", starts[0], start(func(p *ledger.GraphAnalysisStarted, v byte) { p.AnalysisKey[0] ^= v })},
		{"registration reference", "source_binding", starts[0], start(func(p *ledger.GraphAnalysisStarted, v byte) {
			p.Source.Registered.Hash[0] ^= v
			p.AnalysisKey = AnalysisKey(*p)
		})},
		{"subject reference", "source_binding", starts[0], start(func(p *ledger.GraphAnalysisStarted, v byte) {
			p.Source.Subject.Hash[0] ^= v
			p.AnalysisKey = AnalysisKey(*p)
		})},
		{"frontier reference", "source_binding", starts[0], start(func(p *ledger.GraphAnalysisStarted, v byte) {
			p.InputFrontier.Hash[0] ^= v
			p.AnalysisKey = AnalysisKey(*p)
		})},
		{"foreign workspace", "source_binding", starts[0], start(func(p *ledger.GraphAnalysisStarted, _ byte) {
			p.Source.WorkspaceID = graphFixtureID(90)
			p.AnalysisKey = AnalysisKey(*p)
		})},
		{"started base", "version", starts[0], start(func(p *ledger.GraphAnalysisStarted, _ byte) {
			p.BaseVersion, p.BaseDigest = 1, foreignDigest
			p.AnalysisKey = AnalysisKey(*p)
		})},
		{"started while open", "attempt_state", reserved[0], func(_ testing.TB, _ ledger.Draft, _ byte) ledger.Draft { return starts[0].Draft }},
		{"duplicate analysis ID", "attempt_state", starts[1], func(t testing.TB, d ledger.Draft, _ byte) ledger.Draft {
			p, err := ledger.DecodeGraphAnalysisStarted(d.Payload)
			if err != nil {
				t.Fatal(err)
			}
			p.AnalysisID, d.OperationID = starts[0].OperationID, starts[0].OperationID
			d.Payload = graphEncoded(t, p)
			return d
		}},
		{"progress revision", "attempt_state", reserved[0], progress(func(p *ledger.GraphAnalysisProgress, v byte) { p.Revision += int64(v) })},
		{"reservation decrease", "reservation", reserved[1], progress(func(p *ledger.GraphAnalysisProgress, _ byte) { p.ReservedTotal = 0 })},
		{"reservation oversized quantum", "reservation", reserved[0], progress(func(p *ledger.GraphAnalysisProgress, v byte) { p.ReservedTotal += 10000 + int64(v) })},
		{"reservation over total cap", "reservation", reserved[0], progress(func(p *ledger.GraphAnalysisProgress, v byte) { p.ReservedTotal = 2000000 + int64(v) })},
		{"reservation without credit", "reservation", reserved[0], progress(func(p *ledger.GraphAnalysisProgress, _ byte) { p.ReservedTotal = 0 })},
		{"checkpoint count", "checkpoint_cursor", checkpoints[0], progress(func(p *ledger.GraphAnalysisProgress, v byte) { p.VerifiedCursor.OutputIndex += int64(v) })},
		{"standalone checkpoint", "commit_shape", reserved[0], progress(func(p *ledger.GraphAnalysisProgress, _ byte) { p.Phase = ledger.GraphPhaseCheckpoint })},
		{"checkpoint changed to reserve", "commit_shape", checkpoints[0], progress(func(p *ledger.GraphAnalysisProgress, _ byte) { p.Phase = ledger.GraphPhaseReserved })},
		{"nonconsecutive chunk", "chunk_order", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, v byte) { p.ChunkIndex += int64(v) })},
		{"absent removed node", "dangling", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, _ byte) { p.RemoveNodes = []ledger.NodeKey{missing} })},
		{"absent removed edge", "dangling", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, _ byte) { p.RemoveEdges = []ledger.Digest{foreignDigest} })},
		{"dangling edge source", "dangling", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, _ byte) {
			p.PutEdges[0].From = missing
			p.PutEdges[0].EdgeID = ledger.EdgeID(p.PutEdges[0].From, p.PutEdges[0].To, p.PutEdges[0].Type)
		})},
		{"dangling edge target", "dangling", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, _ byte) {
			p.PutEdges[0].To = missing
			p.PutEdges[0].EdgeID = ledger.EdgeID(p.PutEdges[0].From, p.PutEdges[0].To, p.PutEdges[0].Type)
		})},
		{"dangling evidence", "dangling", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, _ byte) { p.PutEdges[0].Evidence.Artifact = missing })},
		{"foreign put node", "source_binding", chunks[0], chunk(func(p *ledger.GraphDeltaChunk, _ byte) {
			p.PutNodes[0].Key.OriginID = graphFixtureID(90)
			slices.SortFunc(p.PutNodes, func(a, b ledger.GraphNode) int { return bytes.Compare(keyBytes(a.Key), keyBytes(b.Key)) })
		})},
		{"final base", "version", finals[0], final(func(p *ledger.GraphVersionCommitted, _ byte) { p.BaseVersion, p.BaseDigest = 1, foreignDigest })},
		{"final chunk count", "chunk_order", finals[0], final(func(p *ledger.GraphVersionCommitted, v byte) { p.ChunkCount += int64(v) })},
		{"chunk digest", "chunk_digest", finals[0], final(func(p *ledger.GraphVersionCommitted, v byte) { p.ChunksSHA256[0] ^= v })},
		{"graph digest", "graph_digest", finals[0], final(func(p *ledger.GraphVersionCommitted, v byte) { p.GraphDigest[0] ^= v })},
		{"structural counter", "counter", finals[0], final(func(p *ledger.GraphVersionCommitted, v byte) { p.StructuralSinceRebuild += int64(v) })},
		{"undeclared consumed version", "", starts[0], func(_ testing.TB, d ledger.Draft, v byte) ledger.Draft { d.TypeVersion = 1 + int(v); return d }},
	}
	for _, field := range []string{"actor", "operation", "measurement", "agent", "execution", "context", "turn", "bodies"} {
		rows = append(rows, graphMutation{"envelope " + field, "record_envelope", starts[0], func(_ testing.TB, draft ledger.Draft, _ byte) ledger.Draft {
			switch field {
			case "actor":
				draft.Actor = ledger.ActorOwner
			case "operation":
				draft.OperationID = graphFixtureID(90)
			case "measurement":
				draft.MeasurementPath = "graph.other"
			case "agent":
				draft.AgentID = graphFixtureID(90)
			case "execution":
				draft.ExecutionSessionID = graphFixtureID(90)
			case "context":
				draft.ContextID = graphFixtureID(90)
			case "turn":
				draft.TurnID = graphFixtureID(90)
			case "bodies":
				draft.BodyRefs = []ledger.Digest{foreignDigest}
			}
			return draft
		}})
	}
	return rows
}

func graphMutatedOptions(t testing.TB, commits []ledger.Commit, mutation graphMutation, value byte) Options {
	t.Helper()
	directory := t.TempDir()
	chain := ledger.Genesis()
	for _, commit := range commits {
		drafts := make([]ledger.Draft, len(commit.Records))
		for index, record := range commit.Records {
			drafts[index] = record.Draft
			drafts[index].Payload = slices.Clone(record.Payload)
			drafts[index].BodyRefs = slices.Clone(record.BodyRefs)
			if record.Seq == mutation.target.Seq {
				drafts[index] = mutation.change(t, drafts[index], value)
			}
		}
		first := commit.Records[0]
		data, next, err := ledger.EncodeCommit(chain, ledger.CommitHeader{ColonyID: first.ColonyID, CommitID: first.CommitID, EventTimeNS: first.EventTimeNS, ClockHeld: first.ClockHeld}, drafts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, ledger.SegmentName(commit.First)), data, 0600); err != nil {
			t.Fatal(err)
		}
		chain = next
	}
	last := commits[len(commits)-1]
	return Options{Colony: last.Records[0].ColonyID, LogDir: directory, Through: last.Last}
}

func assertGraphMutation(t *testing.T, commits []ledger.Commit, mutation graphMutation, value byte, deps serviceDeps) {
	t.Helper()
	service, err := openService(graphMutatedOptions(t, commits, mutation, value), deps)
	if service != nil {
		_ = service.Close()
		t.Fatal("malformed history published a service")
	}
	if !errors.Is(err, ErrHistory) {
		t.Fatalf("%s: expected ErrHistory, got %v", mutation.name, err)
	}
	if mutation.rule == "" {
		var unsupported *ledger.UnsupportedVersionError
		if !errors.As(err, &unsupported) || unsupported.Seq != mutation.target.Seq {
			t.Fatalf("consumed version error: %v", err)
		}
		return
	}
	var failure *HistoryError
	if !errors.As(err, &failure) || failure.Seq != mutation.target.Seq || failure.Rule != mutation.rule {
		t.Fatalf("%s: got %v, want HistoryError{%d, %s}", mutation.name, err, mutation.target.Seq, mutation.rule)
	}
}

func TestGraphHistoryRefusesMalformedMutation(t *testing.T) {
	t.Parallel()
	commits, expected := graphMutationHistory(t)
	t.Run("unchanged framing control", func(t *testing.T) {
		t.Parallel()
		service, err := Open(graphMutatedOptions(t, commits, graphMutation{}, 1))
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		status, err := service.Observe(context.Background())
		if err != nil || status.Version != expected.Version || status.Digest != expected.Digest || status.ChainHash != expected.ChainHash {
			t.Fatalf("round-trip framing changed history: %+v, %v", status, err)
		}
	})
	t.Run("stale subject with a current frontier", func(t *testing.T) {
		t.Parallel()
		var first ledger.GraphSource
		var target ledger.Record
		var prefix []ledger.Commit
		for _, commit := range commits {
			prefix = append(prefix, commit)
			for _, record := range commit.Records {
				if record.Type != ledger.TypeGraphAnalysisStarted {
					continue
				}
				p, err := ledger.DecodeGraphAnalysisStarted(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if first.WorkspaceID == (uuid.UUID{}) {
					first = p.Source
				} else {
					target = record
				}
			}
			if target.Seq != 0 {
				break
			}
		}
		mutation := graphMutation{target: target, change: func(t testing.TB, draft ledger.Draft, _ byte) ledger.Draft {
			p, err := ledger.DecodeGraphAnalysisStarted(draft.Payload)
			if err != nil {
				t.Fatal(err)
			}
			p.Source = first
			p.AnalysisKey = AnalysisKey(p)
			draft.Payload = graphEncoded(t, p)
			return draft
		}}
		options := graphMutatedOptions(t, prefix, mutation, 1)
		historical, err := Open(options)
		if err != nil {
			t.Fatal(err)
		}
		before, err := historical.Observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := historical.Close(); err != nil {
			t.Fatal(err)
		}
		writer := &graphFileAppender{colony: options.Colony, directory: options.LogDir, chain: ledger.Chain{Seq: before.Through, Hash: before.ChainHash, EventTimeNS: target.EventTimeNS}}
		options.Through, options.Appender, options.Durable = 0, writer, writer.Durable
		service, err := Open(options)
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		status, err := service.Resume(context.Background(), before.Attempt)
		if err != nil || status.Charged != 0 || status.Version != before.Version || status.Digest != before.Digest || writer.Durable() != before.Through+1 {
			t.Fatalf("stale subject was funded despite its current frontier: %+v, %v", status, err)
		}
		history := graphHistory(t, writer)
		last := history[len(history)-1].Records[0]
		progress, err := ledger.DecodeGraphAnalysisProgress(last.Payload)
		if err != nil || progress.Phase != ledger.GraphPhaseSuperseded {
			t.Fatalf("stale subject ending: %+v, %v", progress, err)
		}
	})
	t.Run("neither coverage interpretation matches", func(t *testing.T) {
		t.Parallel()
		f, _, _, _ := graphCoverageHistory(t, true, false, false)
		commits := graphHistory(t, f.writer)
		target := commits[len(commits)-1].Records[0]
		mutation := graphMutation{"neither coverage digest", "graph_digest", target, func(t testing.TB, draft ledger.Draft, _ byte) ledger.Draft {
			final, err := ledger.DecodeGraphVersionCommitted(draft.Payload)
			if err != nil {
				t.Fatal(err)
			}
			final.GraphDigest[0] ^= 1
			draft.Payload = graphEncoded(t, final)
			return draft
		}}
		assertGraphMutation(t, commits, mutation, 1, serviceDeps{})
	})
	for _, mutation := range graphMutations(t, commits) {
		t.Run(mutation.name, func(t *testing.T) { t.Parallel(); assertGraphMutation(t, commits, mutation, 1, serviceDeps{}) })
	}
}

// state_cap is covered by deterministic boundary regressions; seeds cover every other rule and undeclared consumed versions.
func FuzzGraphReplayPrefix(f *testing.F) {
	commits, expected := graphMutationHistory(f)
	mutations := graphMutations(f, commits)
	for index := range mutations {
		f.Add(uint8(index), uint8(1))
	}
	f.Add(uint8(255), uint8(0))
	f.Fuzz(func(t *testing.T, selector, value uint8) {
		t.Parallel()
		if value != 0 {
			assertGraphMutation(t, commits, mutations[int(selector)%len(mutations)], value, serviceDeps{})
			return
		}
		service, err := Open(graphMutatedOptions(t, commits, graphMutation{}, 0))
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		status, err := service.Observe(context.Background())
		if err != nil || status.Version != expected.Version || status.Digest != expected.Digest {
			t.Fatalf("unchanged prefix image: %+v, %v", status, err)
		}
	})
}

func assertGraphStateCapMutation(t *testing.T, writer *graphFileAppender, deps serviceDeps) {
	t.Helper()
	commits := graphHistory(t, writer)
	var target ledger.Record
	for _, commit := range commits {
		for _, record := range commit.Records {
			if record.Type != ledger.TypeGraphDeltaChunk {
				continue
			}
			chunk, err := ledger.DecodeGraphDeltaChunk(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if len(chunk.PutNodes) != 0 {
				target = record
			}
		}
	}
	if target.Seq == 0 {
		t.Fatal("full-cap history contains no node chunk")
	}
	mutation := graphMutation{"formed node cap", "state_cap", target, func(t testing.TB, draft ledger.Draft, _ byte) ledger.Draft {
		chunk, err := ledger.DecodeGraphDeltaChunk(draft.Payload)
		if err != nil {
			t.Fatal(err)
		}
		node := chunk.PutNodes[len(chunk.PutNodes)-1]
		var tuple [2]json.RawMessage
		if err := json.Unmarshal([]byte(node.Key.LocalKey), &tuple); err != nil {
			t.Fatal(err)
		}
		var birth int64
		if err := json.Unmarshal(tuple[0], &birth); err != nil {
			t.Fatal(err)
		}
		node.Key, err = ArtifactKey(node.Key.OriginID, birth, "zz-over-cap.py")
		if err != nil {
			t.Fatal(err)
		}
		chunk.PutNodes = append(chunk.PutNodes, node)
		slices.SortFunc(chunk.PutNodes, func(a, b ledger.GraphNode) int { return bytes.Compare(keyBytes(a.Key), keyBytes(b.Key)) })
		draft.Payload = graphEncoded(t, chunk)
		if len(draft.Payload) > chunkLimit {
			t.Fatal("state-cap mutation accidentally exceeds the chunk-byte limit")
		}
		return draft
	}}
	assertGraphMutation(t, commits, mutation, 1, deps)
}

// graphCoverageHistory supplies service-valid frozen records independently of
// the current producer. In particular, legacy empty replacements cannot be
// obtained by treating the encoding-only contract golden as a service log.
func graphCoverageHistory(t *testing.T, selective, empty, stale bool) (*graphFixture, State, Status, uint64) {
	t.Helper()
	return graphCoverageHistorySize(t, selective, empty, stale, false)
}

func graphCoverageHistorySize(t *testing.T, selective, empty, stale, large bool) (*graphFixture, State, Status, uint64) {
	t.Helper()
	count, pathPrefix := 16, ""
	if large {
		count, pathPrefix = 200, strings.Repeat("segment/", 350)
	}
	f := newGraphFixture(t)
	first, other := graphFixtureID(1), graphFixtureID(2)
	emptyTree := f.tree("empty", nil)
	for _, id := range []uuid.UUID{first, other} {
		f.register(id)
		_, paths := graphFiles(count, pathPrefix)
		files := make(map[string]string, len(paths))
		for i, path := range paths {
			paths[i] = strings.TrimSuffix(path, ".py") + ".md"
			files[paths[i]] = ""
		}
		f.accept(id, emptyTree, f.tree(id.String(), files), paths...)
	}
	service := f.open(serviceDeps{})
	before := driveGraph(t, service)
	base := service.replay.current.state
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service = f.open(serviceDeps{})
	if _, err := service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	a := service.replay.attempt
	if a == nil || a.started.Reason == ledger.GraphReasonIncremental {
		t.Fatal("fixture did not start an eligible rebuild")
	}
	want := base
	want.Coverage = nil
	var replacement []ledger.CoverageEntry
	for _, entry := range base.Coverage {
		id, _ := coverageWorkspace(a.scopes, entry)
		if id == first {
			if empty {
				continue
			}
			entry.State, entry.Reasons = ledger.CoverageUnknown, []string{ledger.ReasonHelperLimit}
			replacement = append(replacement, entry)
		} else if !selective {
			replacement = append(replacement, entry)
		}
		want.Coverage = append(want.Coverage, entry)
	}
	var changedNodes []ledger.GraphNode
	if large {
		want.Nodes = slices.Clone(base.Nodes)
		for i := range want.Nodes {
			if want.Nodes[i].Key.OriginID == first {
				want.Nodes[i].VersionSHA256 = ledger.Digest(sha256.Sum256([]byte("incorrect retained artifact version")))
				changedNodes = append(changedNodes, want.Nodes[i])
			}
		}
	}
	_, digest, err := want.Canonical()
	if err != nil || digest == before.Digest {
		t.Fatalf("invalid coverage expectation: %v", err)
	}
	progress := ledger.GraphAnalysisProgress{AnalysisID: a.started.AnalysisID, AnalysisKey: a.started.AnalysisKey, Revision: 1, Phase: ledger.GraphPhaseReserved, ReservedTotal: quantum}
	f.append(graphDraft(ledger.TypeGraphAnalysisProgress, a.started.AnalysisID, graphEncoded(t, progress)))
	hash := sha256.New()
	chunkCount := int64(0)
	appendChunk := func(chunk ledger.GraphDeltaChunk) {
		chunk.AnalysisID, chunk.AnalysisKey, chunk.ChunkIndex = a.started.AnalysisID, a.started.AnalysisKey, chunkCount
		payload := graphEncoded(t, chunk)
		if len(payload) > chunkLimit {
			t.Fatal("compatibility fixture exceeded frozen chunk bound")
		}
		_, _ = hash.Write(payload)
		progress.Revision++
		progress.Phase = ledger.GraphPhaseCheckpoint
		progress.VerifiedCursor = ledger.GraphCursor{ArtifactIndex: int64(2 * count), OutputIndex: chunkCount + 1}
		f.append(graphDraft(ledger.TypeGraphDeltaChunk, a.started.AnalysisID, payload), graphDraft(ledger.TypeGraphAnalysisProgress, a.started.AnalysisID, graphEncoded(t, progress)))
		chunkCount++
	}
	for len(changedNodes) != 0 {
		n := min(100, len(changedNodes))
		appendChunk(ledger.GraphDeltaChunk{PutNodes: changedNodes[:n]})
		changedNodes = changedNodes[n:]
	}
	// Deliberately split one workspace's replacement set across chunks.
	part := min(100, max(1, len(replacement)/2))
	for len(replacement) != 0 {
		n := min(part, len(replacement))
		appendChunk(ledger.GraphDeltaChunk{Coverage: replacement[:n]})
		replacement = replacement[n:]
	}
	prefix := f.writer.Durable()
	if stale {
		files, paths := graphFiles(17, "")
		f.accept(other, a.scopes[other].Source.Tree, f.tree("racing", files), paths...)
	}
	final := ledger.GraphVersionCommitted{AnalysisID: a.started.AnalysisID, AnalysisKey: a.started.AnalysisKey, BaseVersion: a.started.BaseVersion, BaseDigest: a.started.BaseDigest, ChunkCount: chunkCount, ChunksSHA256: ledger.Digest(hash.Sum(nil)), GraphDigest: digest}
	f.append(graphDraft(ledger.TypeGraphVersionCommitted, a.started.AnalysisID, graphEncoded(t, final)))
	return f, want, before, prefix
}

func TestGraphCoverageReplayCompatibility(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                    string
		selective, empty, stale bool
	}{
		{"legacy replacement", false, false, false},
		{"legacy empty replacement", false, true, false},
		{"selective split replacement", true, false, false},
		{"stale final", true, false, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, expected, before, prefix := graphCoverageHistory(t, row.selective, row.empty, row.stale)
			for _, commit := range graphHistory(t, f.writer) {
				if commit.Last <= before.Through {
					continue
				}
				options := f.options()
				options.Through = commit.Last
				service, err := openService(options, serviceDeps{})
				if err != nil {
					t.Fatal(err)
				}
				current := service.replay.current
				if commit.Last <= prefix || row.stale {
					if current.version != before.Version || current.digest != before.Digest {
						t.Fatalf("partial/stale coverage published at %d", commit.Last)
					}
				} else {
					got, digest, err := current.state.Canonical()
					want, wantDigest, wantErr := expected.Canonical()
					if err != nil || wantErr != nil || digest != wantDigest || !bytes.Equal(got, want) || current.version != int64(commit.Last) {
						t.Fatalf("coverage interpretation lost exact state: %v/%v", err, wantErr)
					}
				}
				if row.stale && commit.Last == f.writer.Durable() && service.replay.lastAttempt.phase != ledger.GraphPhaseSuperseded {
					t.Fatal("stale final was not superseded")
				}
				_ = service.Close()
			}
		})
	}
}

func TestGraphLegacyReplayRetainedBoundary(t *testing.T) {
	t.Parallel()
	f, expected, _, prefix := graphCoverageHistory(t, false, false, false)
	options := f.options()
	options.Through = prefix
	prior, err := openService(options, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	// Closing the legacy final retains exactly one terminal summary. There is
	// no room for a selective presence set before that close.
	cap := prior.replay.retained() + 512
	options.Through = f.writer.Durable()
	bounded, err := openService(options, serviceDeps{retainedCap: &cap})
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.Close()
	want, digest, err := expected.Canonical()
	got, actualDigest, actualErr := bounded.replay.current.state.Canonical()
	if err != nil || actualErr != nil || bounded.overLimit || digest != actualDigest || !bytes.Equal(got, want) || bounded.replay.current.version != int64(f.writer.Durable()) {
		t.Fatalf("legacy final acquired fallback headroom: overLimit=%v errors=%v/%v", bounded.overLimit, err, actualErr)
	}
}

func TestGraphSelectiveFallbackRetainedCap(t *testing.T) {
	t.Parallel()
	f, expected, before, prefix := graphCoverageHistory(t, true, false, false)
	options := f.options()
	options.Through = prefix
	prior, err := openService(options, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	var restoration uint64
	for _, entry := range expected.Coverage {
		id, _ := coverageWorkspace(prior.replay.attempt.scopes, entry)
		if id == graphFixtureID(2) {
			restoration += coverageMemory(entry)
		}
	}
	cap := prior.replay.retained() + restoration + 512 - 1
	boundedPrefix, err := openService(options, serviceDeps{retainedCap: &cap})
	if err != nil {
		t.Fatal(err)
	}
	defer boundedPrefix.Close()
	if boundedPrefix.overLimit || boundedPrefix.replay.current.digest != before.Digest {
		t.Fatal("fixture cap did not admit the complete legacy prefix")
	}
	options.Through = f.writer.Durable()
	bounded, err := openService(options, serviceDeps{retainedCap: &cap})
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.Close()
	status, err := bounded.Observe(t.Context())
	if err != nil || !bounded.overLimit || status.Through != prefix || status.State != "unknown" || !slices.Contains(status.Reasons, ledger.ReasonStateCap) {
		t.Fatalf("selective restoration escaped its retained bound: %+v, %v", status, err)
	}
	if _, err := bounded.Page(graphPageRequest(status, "coverage")); !errors.Is(err, ErrRevision) {
		t.Fatalf("partly restored coverage exposed: %v", err)
	}
	if prior.replay.current.digest != before.Digest {
		t.Fatal("failed restoration mutated the published base")
	}
}

func TestGraphSelectiveProducerRetainedCap(t *testing.T) {
	t.Parallel()
	f, _, _, _ := graphCoverageHistory(t, false, false, false)
	service := f.open(serviceDeps{})
	before := service.replay.current
	for range 205 {
		if service.work != nil && service.work.phase == 9 {
			break
		}
		if _, err := service.Step(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	w := service.work
	if w == nil || w.phase != 9 || len(w.changedCoverage) != 1 {
		t.Fatal("fixture did not reach selective publication")
	}
	chunk := w.diff
	chunk.AnalysisID, chunk.AnalysisKey = w.started.AnalysisID, w.started.AnalysisKey
	encoded := graphEncoded(t, chunk)
	chunkCost := uint64(2*len(encoded) + 512)
	service.replay.retainedCap = service.replay.retained() + w.formedMemory + w.memory + w.generation.memory + service.generationMemory + chunkCost + 512 - 1
	status := driveGraph(t, service)
	if status.Version != before.version || status.Digest != before.digest || !slices.Contains(status.Reasons, ledger.ReasonStateCap) {
		t.Fatalf("producer omitted its selected fallback peak: %+v", status)
	}
	for _, record := range graphAttemptRecords(t, f, status.Attempt) {
		if record.Type == ledger.TypeGraphDeltaChunk || record.Type == ledger.TypeGraphVersionCommitted {
			t.Fatal("producer appended beyond its admitted selected peak")
		}
	}
	page, err := service.Page(graphPageRequest(status, "coverage"))
	if err != nil || !slices.EqualFunc(page.Coverage, before.state.Coverage, sameCoverage) {
		t.Fatalf("refused producer changed the available old page: %v", err)
	}
	assertGraphExactReplay(t, f, service)
}

package graph

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

type graphFileAppender struct {
	mu        sync.Mutex
	colony    uuid.UUID
	directory string
	chain     ledger.Chain
}

func (a *graphFileAppender) Durable() uint64 { a.mu.Lock(); defer a.mu.Unlock(); return a.chain.Seq }
func (a *graphFileAppender) Append(ctx context.Context, drafts []ledger.Draft) (appender.Committed, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return appender.Committed{}, err
	}
	data, next, err := ledger.EncodeCommit(a.chain, ledger.CommitHeader{ColonyID: a.colony, CommitID: uuid.NewV7(), EventTimeNS: 1791000500000000000 + int64(a.chain.Seq)}, drafts)
	if err != nil {
		return appender.Committed{}, err
	}
	file, err := os.OpenFile(filepath.Join(a.directory, ledger.SegmentName(a.chain.Seq+1)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return appender.Committed{}, err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return appender.Committed{}, err
	}
	ack := appender.Committed{First: a.chain.Seq + 1, Last: next.Seq}
	a.chain = next
	return ack, nil
}

type graphFixture struct {
	t      testing.TB
	writer *graphFileAppender
	mu     sync.Mutex
	trees  map[string][]Artifact
}

func graphFixtureID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("0199c000-0000-7000-8000-%012x", n))
}
func newGraphFixture(t testing.TB) *graphFixture {
	t.Helper()
	return &graphFixture{t: t, writer: &graphFileAppender{colony: uuid.NewV7(), directory: t.TempDir(), chain: ledger.Genesis()}, trees: make(map[string][]Artifact)}
}
func graphEncoded(t testing.TB, value interface {
	Encode() (json.RawMessage, error)
}) []byte {
	t.Helper()
	encoded, err := value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
func (f *graphFixture) append(drafts ...ledger.Draft) appender.Committed {
	f.t.Helper()
	committed, err := f.writer.Append(context.Background(), drafts)
	if err != nil {
		f.t.Fatal(err)
	}
	return committed
}
func (f *graphFixture) register(id uuid.UUID) {
	f.t.Helper()
	payload, err := (ledger.WorkspaceRegistered{WorkspaceID: id, CommonDir: filepath.ToSlash(filepath.Join(f.writer.directory, id.String(), ".git")), ObjectFormat: "sha1"}).Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	f.append(ledger.Draft{Type: ledger.TypeWorkspaceRegistered, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: payload})
}
func (f *graphFixture) tree(label string, files map[string]string) string {
	f.t.Helper()
	hash := sha256.Sum256([]byte(label))
	id := fmt.Sprintf("%x", hash[:20])
	var artifacts []Artifact
	for path, body := range files {
		digest := sha256.Sum256([]byte(body))
		artifacts = append(artifacts, Artifact{Path: path, Mode: "100644", GitOID: fmt.Sprintf("%x", digest[:20]), SHA256: ledger.Digest(digest), Bytes: []byte(body)})
	}
	slices.SortFunc(artifacts, func(a, b Artifact) int { return strings.Compare(a.Path, b.Path) })
	f.mu.Lock()
	f.trees[id] = artifacts
	f.mu.Unlock()
	return id
}
func (f *graphFixture) accept(workspace uuid.UUID, predecessor, tree string, paths ...string) appender.Committed {
	f.t.Helper()
	slices.Sort(paths)
	request, contextID, pin := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	digest := ledger.Digest(sha256.Sum256([]byte("synthetic frozen C2 input")))
	admitted, err := (ledger.PromotionAdmitted{RequestID: request, WorkspaceID: workspace, ScratchID: uuid.NewV7(), ContextID: contextID, LineageID: uuid.NewV7(), Checkpoint: 1, ScratchBase: strings.Repeat("a", 40), BaselineTree: predecessor, DeltaBaseTree: predecessor, CaptureID: uuid.NewV7(), SealedTree: tree, ManifestSHA256: digest, InputsRevision: 1, CaptureInputsSHA256: digest, PinID: pin}).Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	pinned, err := (ledger.BodyPin{PinID: pin}).Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	f.append(ledger.Draft{Type: ledger.TypePromotionAdmitted, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: contextID, BodyRefs: []ledger.Digest{digest}, Payload: admitted}, ledger.Draft{Type: ledger.TypeBodyPin, TypeVersion: 1, Actor: ledger.ActorAgent, ContextID: contextID, BodyRefs: []ledger.Digest{digest}, Payload: pinned})
	accepted, err := (ledger.PromotionAccepted{IntentID: uuid.NewV7(), RequestID: request, GenerationID: uuid.NewV7(), PredecessorCommit: strings.Repeat("a", 40), PredecessorTree: predecessor, AcceptedCommit: strings.Repeat("b", 40), AcceptedTree: tree, ChangedPaths: paths}).Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	unpinned, err := (ledger.BodyUnpin{PinID: pin}).Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	return f.append(ledger.Draft{Type: ledger.TypePromotionAccepted, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, Payload: accepted}, ledger.Draft{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: contextID, BodyRefs: []ledger.Digest{digest}, Payload: unpinned})
}
func (f *graphFixture) options() Options {
	return Options{Colony: f.writer.colony, LogDir: f.writer.directory, Appender: f.writer, Durable: f.writer.Durable, ReadArtifacts: f.read}
}
func (f *graphFixture) read(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
	if err := ctx.Err(); err != nil {
		return ArtifactBatch{}, err
	}
	f.mu.Lock()
	entries, exists := f.trees[request.Tree]
	f.mu.Unlock()
	if !exists {
		return ArtifactBatch{Reason: ledger.ReasonSourceUnavailable}, nil
	}
	paths := request.Paths
	if len(paths) == 0 {
		for _, entry := range entries {
			paths = append(paths, entry.Path)
		}
	}
	batch := ArtifactBatch{NextPath: request.AfterPath}
	var bytes uint64
	for _, path := range paths {
		if path <= request.AfterPath {
			continue
		}
		index, found := slices.BinarySearchFunc(entries, path, func(entry Artifact, path string) int { return strings.Compare(entry.Path, path) })
		units := uint64(2 + (len(path)+255)/256)
		var entry Artifact
		if found {
			entry = entries[index]
			if !request.ListOnly && entry.Reason == "" {
				units += 1 + (uint64(len(entry.Bytes))+255)/256
			}
		}
		if units > request.WorkAllowance-batch.WorkUsed {
			batch.WorkUsed = request.WorkAllowance
			batch.Reason = ledger.ReasonWorkExhausted
			return batch, nil
		}
		if !request.ListOnly && found && uint64(len(entry.Bytes)) > request.MaxBytes-bytes {
			batch.Reason = ledger.ReasonTemporaryCap
			return batch, nil
		}
		batch.WorkUsed += units
		batch.NextPath = path
		if !found {
			continue
		}
		if request.ListOnly {
			entry.Bytes, entry.SHA256 = nil, ledger.Digest{}
		} else {
			bytes += uint64(len(entry.Bytes))
		}
		batch.Entries = append(batch.Entries, entry)
	}
	batch.Complete = true
	return batch, nil
}
func (f *graphFixture) open(deps serviceDeps) *Service {
	f.t.Helper()
	service, err := openService(f.options(), deps)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = service.Close() })
	return service
}
func driveGraph(t testing.TB, service *Service) Status {
	t.Helper()
	for steps := 0; steps < 205; steps++ {
		status, err := service.Step(context.Background())
		if err != nil {
			t.Fatalf("step %d: %v", steps, err)
		}
		if service.replay.attempt == nil {
			return status
		}
	}
	t.Fatal("graph attempt did not reach a durable ending")
	return Status{}
}

func TestGraphStatusSeparatesReadyFromDeclaredGaps(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	workspace := graphFixtureID(1)
	f.register(workspace)
	tree := f.tree("covered", map[string]string{"readme.md": "opaque"})
	f.accept(workspace, f.tree("empty", nil), tree, "readme.md")
	service := f.open(serviceDeps{})
	pending, err := service.Observe(context.Background())
	if err != nil || pending.State != "pending" || !slices.Equal(pending.Pending, []uuid.UUID{workspace}) {
		t.Fatalf("runnable source: %+v, %v", pending, err)
	}
	ready := driveGraph(t, service)
	reasons := []string{ledger.ReasonUnsupportedSyntax, ledger.ReasonUpstreamUnfrozen}
	if ready.State != "ready" || ready.Version == 0 || len(ready.Pending) != 0 || !slices.Equal(ready.Reasons, reasons) {
		t.Fatalf("declared gaps prevented readiness: %+v", ready)
	}
	reopened := f.open(serviceDeps{})
	observed, err := reopened.Observe(context.Background())
	if err != nil || observed.State != "pending" || observed.Digest != ready.Digest || !slices.Equal(observed.Reasons, ready.Reasons) {
		t.Fatalf("load did not retain the readable image and declared gaps: %+v, %v", observed, err)
	}
	confirmed := driveGraph(t, reopened)
	if confirmed.State != "ready" || confirmed.Version != ready.Version || confirmed.Digest != ready.Digest || !slices.Equal(confirmed.Reasons, ready.Reasons) {
		t.Fatalf("zero-net confirmation changed readiness: %+v", confirmed)
	}
	// A failed source remains pending, but its terminal key cannot be run again.
	f.accept(workspace, tree, strings.Repeat("e", 40), "readme.md")
	blocked := driveGraph(t, service)
	if blocked.State != "unknown" || !slices.Equal(blocked.Pending, []uuid.UUID{workspace}) || blocked.Version != ready.Version || !slices.Equal(blocked.Reasons, ready.Reasons) {
		t.Fatalf("terminal pending source: %+v", blocked)
	}
	before := f.writer.Durable()
	observed, err = service.Step(context.Background())
	if err != nil || observed.State != "unknown" || f.writer.Durable() != before {
		t.Fatalf("terminal source was retried: %+v, %v", observed, err)
	}
}

func TestGraphFailedOutputRestartsChargedWork(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ledger.TypeGraphDeltaChunk, ledger.TypeGraphVersionCommitted, ledger.GraphPhaseUnknown} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newGraphFixture(t)
			workspace := graphFixtureID(1)
			f.register(workspace)
			body := "-- no dependencies"
			f.accept(workspace, f.tree("empty", nil), f.tree("source", map[string]string{"a.sql": body}), "a.sql")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelled, unavailable, reads := false, kind == ledger.GraphPhaseUnknown, 0
			opts := f.options()
			opts.ReadArtifacts = func(ctx context.Context, request ArtifactRequest) (ArtifactBatch, error) {
				reads++
				if unavailable && !request.ListOnly {
					return ArtifactBatch{Reason: ledger.ReasonSourceUnavailable}, nil
				}
				return f.read(ctx, request)
			}
			service, err := openService(opts, serviceDeps{beforeAppend: func(drafts []ledger.Draft) {
				matches := drafts[0].Type == kind
				if kind == ledger.GraphPhaseUnknown && drafts[0].Type == ledger.TypeGraphAnalysisProgress {
					progress, err := ledger.DecodeGraphAnalysisProgress(drafts[0].Payload)
					if err != nil {
						t.Fatal(err)
					}
					matches = progress.Phase == ledger.GraphPhaseUnknown
				}
				if !cancelled && matches {
					cancelled, unavailable = true, false
					cancel()
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			var failed Status
			for steps := 0; steps < 205; steps++ {
				failed, err = service.Step(ctx)
				if err != nil {
					break
				}
			}
			if !cancelled || !errors.Is(err, context.Canceled) || failed.Version != 0 || failed.Charged == 0 {
				t.Fatalf("output append did not fail without publication: %+v, %v", failed, err)
			}
			before, beforeReads := f.writer.Durable(), reads
			restarted, err := service.Step(context.Background())
			if err != nil || restarted.Attempt != failed.Attempt || restarted.Charged != failed.Charged+quantum || reads <= beforeReads || f.writer.Durable() != before+1 {
				t.Fatalf("retry did not reserve and recompute: %+v, %v, reads %d -> %d, durable %d -> %d", restarted, err, beforeReads, reads, before, f.writer.Durable())
			}
			final := driveGraph(t, service)
			page, err := service.Page(graphPageRequest(final, "nodes"))
			if err != nil || final.State != "ready" || final.Attempt != failed.Attempt || final.Charged <= restarted.Charged || len(page.Nodes) != 1 || page.Nodes[0].VersionSHA256 != ledger.Digest(sha256.Sum256([]byte(body))) {
				t.Fatalf("recomputed output was not published intact: %+v, %v, nodes=%+v", final, err, page.Nodes)
			}
		})
	}
}

func TestGraphHistoryAtEveryCursor(t *testing.T) {
	t.Parallel()
	fixture := newGraphFixture(t)
	workspace, other := graphFixtureID(1), graphFixtureID(2)
	fixture.register(workspace)
	fixture.register(other)
	empty := fixture.tree("empty", nil)
	tree := fixture.tree("initial", map[string]string{"readme.md": "[not a dependency](elsewhere.md)"})
	accepted := fixture.accept(workspace, empty, tree, "readme.md")
	service := fixture.open(serviceDeps{})
	started, err := service.Step(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service = fixture.open(serviceDeps{})
	if _, err := service.Resume(context.Background(), started.Attempt); err != nil {
		t.Fatal(err)
	}
	status := driveGraph(t, service)
	if status.Version <= int64(accepted.Last) || len(status.Pending) != 0 || len(service.replay.current.state.Nodes) != 1 || len(service.replay.current.state.Edges) != 0 || service.replay.structural != 0 {
		t.Fatalf("completed immutable graph=%+v state=%+v counter=%d", status, service.replay.current.state, service.replay.structural)
	}
	canonical, digest, err := service.replay.current.state.Canonical()
	if err != nil || digest != status.Digest || !bytes.Contains(canonical, []byte("readme.md")) {
		t.Fatalf("canonical final mismatch %s %v", canonical, err)
	}
	changed := fixture.tree("changed", map[string]string{"readme.md": "changed"})
	fixture.accept(workspace, tree, changed, "readme.md")
	otherTree := fixture.tree("other", map[string]string{"other.py": ""})
	raced := false
	service.deps.beforeAppend = func(drafts []ledger.Draft) {
		if !raced && drafts[len(drafts)-1].Type == ledger.TypeGraphVersionCommitted {
			raced = true
			fixture.accept(other, empty, otherTree, "other.py")
		}
	}
	stale := driveGraph(t, service)
	if !raced || stale.Version != status.Version || stale.Digest != status.Digest || service.replay.lastAttempt.phase != ledger.GraphPhaseSuperseded {
		t.Fatalf("racing final published: %+v", stale)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	options := fixture.options()
	options.ReadArtifacts = func(_ context.Context, request ArtifactRequest) (ArtifactBatch, error) {
		return ArtifactBatch{WorkUsed: request.WorkAllowance}, nil
	}
	service, err = openService(options, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	unknown := driveGraph(t, service)
	if service.replay.lastAttempt.phase != ledger.GraphPhaseUnknown || unknown.Version != status.Version {
		t.Fatalf("stalled source published: %+v", unknown)
	}
	unknownCommit := graphHistory(t, fixture.writer)
	progress, err := ledger.DecodeGraphAnalysisProgress(unknownCommit[len(unknownCommit)-1].Records[0].Payload)
	if err != nil || len(progress.Coverage) != 1 || !slices.Equal(progress.Coverage[0].Reasons, []string{ledger.ReasonHelperLimit}) {
		t.Fatalf("stalled callback reason: %+v, %v", progress, err)
	}
	racedStart := false
	service.deps.beforeAppend = func(drafts []ledger.Draft) {
		if !racedStart && drafts[0].Type == ledger.TypeGraphAnalysisStarted {
			racedStart = true
			next := fixture.tree("other changed", map[string]string{"other.py": "# changed"})
			fixture.accept(other, otherTree, next, "other.py")
		}
	}
	before := fixture.writer.Durable()
	stale, err = service.Step(context.Background())
	if err != nil || !racedStart || stale.Charged != 0 || service.replay.lastAttempt.phase != ledger.GraphPhaseSuperseded || fixture.writer.Durable() != before+6 {
		t.Fatalf("racing start reserved or published: %+v, %v", stale, err)
	}
	assertGraphHistoryCursors(t, fixture)
}

func assertGraphHistoryCursors(t *testing.T, fixture *graphFixture) {
	t.Helper()
	latest, covered := make(map[uuid.UUID]ledger.RecordRef), make(map[uuid.UUID]ledger.RecordRef)
	admitted := make(map[uuid.UUID]uuid.UUID)
	var started ledger.GraphAnalysisStarted
	var frontier ledger.RecordRef
	digest := ledger.EmptyGraphDigest
	var version, structural, reserved int64
	var cursor ledger.GraphCursor
	var phase string
	open := false
	for _, commit := range graphHistory(t, fixture.writer) {
		for _, record := range commit.Records {
			switch record.Type {
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
				hash, err := ledger.ParseDigest(record.Hash)
				if err != nil {
					t.Fatal(err)
				}
				frontier = ledger.RecordRef{Seq: int64(record.Seq), Hash: hash}
				latest[admitted[p.RequestID]] = frontier
				structural++
			case ledger.TypeGraphAnalysisStarted:
				p, err := ledger.DecodeGraphAnalysisStarted(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				started, reserved, cursor, phase, open = p, 0, ledger.GraphCursor{}, "", true
			case ledger.TypeGraphAnalysisProgress:
				p, err := ledger.DecodeGraphAnalysisProgress(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				reserved, cursor, phase = p.ReservedTotal, p.VerifiedCursor, p.Phase
				if phase == ledger.GraphPhaseUnknown || phase == ledger.GraphPhaseSuperseded {
					open = false
				}
			case ledger.TypeGraphVersionCommitted:
				p, err := ledger.DecodeGraphVersionCommitted(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				phase, open = ledger.GraphPhaseSuperseded, false
				if started.InputFrontier == frontier && latest[started.Source.WorkspaceID] == started.Source.Subject {
					if p.ChunkCount != 0 {
						version = int64(record.Seq)
					}
					digest = p.GraphDigest
					if started.Reason == ledger.GraphReasonIncremental {
						covered[started.Source.WorkspaceID] = started.Source.Subject
					} else {
						for id, subject := range latest {
							covered[id] = subject
						}
						structural = 0
					}
					phase = ledger.TypeGraphVersionCommitted
				}
			}
		}
		options := fixture.options()
		options.Through = commit.Last
		options.ReadArtifacts = func(context.Context, ArtifactRequest) (ArtifactBatch, error) {
			t.Fatal("historical Open invoked the producer")
			return ArtifactBatch{}, nil
		}
		historical, err := openService(options, serviceDeps{afterChildStart: func() { t.Fatal("historical Open launched a helper") }})
		if err != nil {
			t.Fatalf("cursor %d: %v", commit.Last, err)
		}
		status, err := historical.Observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var pending []uuid.UUID
		for id, subject := range latest {
			if covered[id] != subject {
				pending = append(pending, id)
			}
		}
		slices.SortFunc(pending, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		gotPhase := historical.replay.lastAttempt.phase
		if historical.replay.attempt != nil {
			gotPhase = historical.replay.attempt.phase
		}
		if status.Through != commit.Last || status.ChainHash != commit.Records[len(commit.Records)-1].Hash || status.Version != version || status.Digest != digest || !slices.Equal(status.Pending, pending) || status.Attempt != started.AnalysisID || status.Charged != reserved || status.VerifiedCursor != cursor || historical.replay.structural != structural || (historical.replay.attempt != nil) != open || gotPhase != phase {
			t.Fatalf("cursor %d: status=%+v phase=%q; want version=%d digest=%s pending=%v counter=%d phase=%q", commit.Last, status, gotPhase, version, digest, pending, structural, phase)
		}
		if open && status.State != "recovery_pending" {
			t.Fatalf("cursor %d lost recovery state: %+v", commit.Last, status)
		}
		if err := historical.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func graphHistory(t testing.TB, writer *graphFileAppender) []ledger.Commit {
	t.Helper()
	reader, err := ledger.OpenReader(writer.directory, ledger.ReadOptions{Colony: writer.colony, Through: writer.Durable(), Consume: graphConsumption(), AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var commits []ledger.Commit
	for {
		commit, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return commits
		}
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, commit)
	}
}

func TestGraphCapsDoNotPublishPartialState(t *testing.T) {
	t.Parallel()
	t.Run("retained accounting survives mixed mutations", func(t *testing.T) {
		t.Parallel()
		f := newGraphFixture(t)
		workspace := graphFixtureID(1)
		f.register(workspace)
		previous := f.tree("empty", nil)
		var service *Service
		for index, files := range []map[string]string{
			{"create.sql": "CREATE TABLE t(id INTEGER);", "use.sql": "SELECT * FROM t;", "gone.py": ""},
			{"create.sql": "CREATE TABLE u(id INTEGER);", "use.sql": "SELECT * FROM u;", "new.py": ""},
			{"new.py": "", "second.py": ""},
			{},
		} {
			tree := f.tree(fmt.Sprintf("mixed-%d", index), files)
			f.accept(workspace, previous, tree, "create.sql", "gone.py", "new.py", "second.py", "use.sql")
			previous = tree
			if service == nil {
				service = f.open(serviceDeps{})
			}
			status := driveGraph(t, service)
			if status.Version == 0 || len(status.Pending) != 0 {
				t.Fatalf("mixed mutation %d did not publish: %+v", index, status)
			}
		}
		for _, commit := range graphHistory(t, f.writer) {
			options := f.options()
			options.Through = commit.Last
			historical, err := openService(options, serviceDeps{})
			if err != nil {
				t.Fatal(err)
			}
			current := historical.replay.current
			if current.memory != current.index.memory || current.index.memory != graphMemory(current.index) {
				t.Fatalf("published charge drift at %d: charged=%d full=%d", commit.Last, current.memory, graphMemory(current.index))
			}
			if attempt := historical.replay.attempt; attempt != nil && attempt.stage != nil && attempt.stage.memory != graphMemory(attempt.stage) {
				t.Fatalf("forming charge drift at %d: charged=%d full=%d", commit.Last, attempt.stage.memory, graphMemory(attempt.stage))
			}
			if err := historical.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	for _, row := range []struct {
		name      string
		nodes     int
		override  int64
		plainOnly bool
	}{
		{"node boundary keeps the old image on overflow", 50000, 0, true},
		{"scaled node boundary keeps the old image on overflow", 500, 500, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if row.plainOnly && raceEnabled {
				t.Skip("B28 item 8: production 50,000/50,001 boundary runs without race; race covers 500/501 through the node-cap seam")
			}
			f := newGraphFixture(t)
			perWorkspace := row.nodes / 5
			files, paths := graphFiles(perWorkspace, "")
			empty, tree := f.tree("empty", nil), f.tree("boundary", files)
			deps := serviceDeps{nodeCap: row.override}
			var service *Service
			var status Status
			for group := 1; group <= 5; group++ {
				workspace := graphFixtureID(group)
				f.register(workspace)
				f.accept(workspace, empty, tree, paths...)
				if service == nil {
					service = f.open(deps)
				}
				status = driveGraph(t, service)
				if len(service.replay.current.state.Nodes) != group*perWorkspace || len(status.Pending) != 0 {
					t.Fatalf("group %d failed to publish: %+v nodes=%d", group, status, len(service.replay.current.state.Nodes))
				}
			}
			oldVersion, oldDigest := status.Version, status.Digest
			workspace := graphFixtureID(6)
			f.register(workspace)
			f.accept(workspace, empty, f.tree("overflow", map[string]string{"overflow.py": ""}), "overflow.py")
			status = driveGraph(t, service)
			if status.Version != oldVersion || status.Digest != oldDigest || len(service.replay.current.state.Nodes) != row.nodes {
				t.Fatalf("overflow published partial state: %+v", status)
			}
			assertGraphTerminalReason(t, f, ledger.ReasonStateCap)
			t.Run("malformed state_cap history", func(t *testing.T) {
				t.Parallel()
				assertGraphStateCapMutation(t, f.writer, deps)
			})
		})
	}
	t.Run("temporary staging is conservative", func(t *testing.T) {
		t.Parallel()
		f := newGraphFixture(t)
		workspace := graphFixtureID(1)
		f.register(workspace)
		files, _ := graphFiles(3000, strings.Repeat("component/", 400))
		baseline := make(map[string]string, len(files)+1)
		for path, body := range files {
			baseline[path] = body
		}
		baseline["sentinel.py"] = ""
		f.accept(workspace, f.tree("baseline", baseline), f.tree("large keys", files), "sentinel.py")
		service := f.open(serviceDeps{})
		status := driveGraph(t, service)
		if status.Version != 0 || status.Digest != ledger.EmptyGraphDigest || len(service.replay.current.state.Nodes) != 0 {
			t.Fatalf("temporary overflow published: %+v", status)
		}
		assertGraphTerminalReason(t, f, ledger.ReasonTemporaryCap)
	})
	t.Run("maximal keys split greedily without truncation", func(t *testing.T) {
		t.Parallel()
		f := newGraphFixture(t)
		workspace := graphFixtureID(1)
		f.register(workspace)
		files, _ := graphFiles(300, strings.Repeat("component/", 400))
		baseline := make(map[string]string, len(files)+1)
		for path, body := range files {
			baseline[path] = body
		}
		baseline["sentinel.py"] = ""
		f.accept(workspace, f.tree("baseline", baseline), f.tree("large keys", files), "sentinel.py")
		service := f.open(serviceDeps{})
		status := driveGraph(t, service)
		if status.Version == 0 || len(service.replay.current.state.Nodes) != len(files) {
			t.Fatalf("maximal keys were truncated: %+v nodes=%d", status, len(service.replay.current.state.Nodes))
		}
		chunks, nodes := 0, 0
		var previous ledger.GraphDeltaChunk
		for _, commit := range graphHistory(t, f.writer) {
			for _, record := range commit.Records {
				if record.Type != ledger.TypeGraphDeltaChunk {
					continue
				}
				chunk, err := ledger.DecodeGraphDeltaChunk(record.Payload)
				if err != nil || len(record.Payload) > 512<<10 {
					t.Fatalf("oversized chunk: %d, %v", len(record.Payload), err)
				}
				if chunks != 0 && len(chunk.PutNodes) != 0 {
					probe := previous
					probe.PutNodes = append(slices.Clone(previous.PutNodes), chunk.PutNodes[0])
					encoded, err := probe.Encode()
					if err == nil && len(encoded) <= 512<<10 {
						t.Fatalf("non-greedy split: another maximal node fits in %d bytes", len(encoded))
					}
				}
				chunks++
				nodes += len(chunk.PutNodes)
				previous = chunk
			}
		}
		if chunks < 2 || nodes != len(files) {
			t.Fatalf("split lost nodes: chunks=%d nodes=%d", chunks, nodes)
		}
	})
	t.Run("retained history stops at the last complete commit", func(t *testing.T) {
		t.Parallel()
		f := newGraphFixture(t)
		workspace := graphFixtureID(1)
		f.register(workspace)
		f.accept(workspace, f.tree("empty", nil), f.tree("first", map[string]string{"a.md": "first"}), "a.md")
		commits := graphHistory(t, f.writer)
		cap := uint64(1100)
		service, err := openService(f.options(), serviceDeps{retainedCap: &cap})
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		status, err := service.Observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		last := commits[1]
		if status.State != "unknown" || !slices.Equal(status.Reasons, []string{ledger.ReasonStateCap}) || status.Through != last.Last || status.ChainHash != last.Records[len(last.Records)-1].Hash || status.Version != 0 || status.Digest != (ledger.Digest{}) {
			t.Fatalf("retained-cap status: %+v", status)
		}
		if _, err := service.Page(PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: "nodes", Limit: 1}); !errors.Is(err, ErrRevision) {
			t.Fatalf("cap page: %v", err)
		}
		before := f.writer.Durable()
		for _, call := range []func() (Status, error){func() (Status, error) { return service.Step(context.Background()) }, func() (Status, error) { return service.Resume(context.Background(), graphFixtureID(99)) }} {
			got, err := call()
			if err != nil || got.State != "unknown" || got.Through != status.Through || got.ChainHash != status.ChainHash || !slices.Equal(got.Reasons, status.Reasons) {
				t.Fatalf("cap work response: %+v, %v", got, err)
			}
		}
		if f.writer.Durable() != before {
			t.Fatal("retained-cap service appended work")
		}
	})
}

func graphFiles(count int, prefix string) (map[string]string, []string) {
	files := make(map[string]string, count)
	paths := make([]string, count)
	for i := range paths {
		paths[i] = fmt.Sprintf("%s%05d.py", prefix, i)
		files[paths[i]] = ""
	}
	return files, paths
}

func graphPageRequest(status Status, kind string) PageRequest {
	return PageRequest{Version: status.Version, Digest: status.Digest, Through: status.Through, ChainHash: status.ChainHash, Kind: kind, Limit: 256}
}

func TestGraphVersionAtEveryCursor(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	workspace := graphFixtureID(1)
	f.register(workspace)
	files, paths := graphFiles(2500, strings.Repeat("p", 80))
	f.accept(workspace, f.tree("empty", nil), f.tree("large", files), paths...)
	chunks := 0
	service := f.open(serviceDeps{beforeAppend: func(drafts []ledger.Draft) {
		if drafts[0].Type == ledger.TypeGraphDeltaChunk {
			chunks++
			if chunks == 2 {
				f.register(graphFixtureID(2))
			}
		}
	}})
	var prior Status
	for steps := 0; steps < 205; steps++ {
		status, err := service.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		page, err := service.Page(graphPageRequest(status, "nodes"))
		if err != nil || page.Version != status.Version || page.Digest != status.Digest || page.Through != status.Through || page.ChainHash != status.ChainHash {
			t.Fatalf("mixed page: %+v, %v", page.Status, err)
		}
		if service.replay.attempt != nil {
			if status.Version != 0 || status.Digest != ledger.EmptyGraphDigest || len(page.Nodes) != 0 {
				t.Fatalf("partial chunk was visible: %+v nodes=%d", status, len(page.Nodes))
			}
			prior = status
			continue
		}
		if chunks < 2 || status.Version != int64(f.writer.Durable()) {
			t.Fatalf("did not publish a multi-chunk version: chunks=%d status=%+v", chunks, status)
		}
		if _, err := service.Page(graphPageRequest(prior, "nodes")); !errors.Is(err, ErrRevision) {
			t.Fatalf("stale page revision: %v", err)
		}
		count := 0
		var previous []byte
		request := graphPageRequest(status, "nodes")
		for {
			page, err = service.Page(request)
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range page.Nodes {
				key := keyBytes(node.Key)
				if previous != nil && bytes.Compare(previous, key) >= 0 {
					t.Fatal("page duplicated or reordered a key")
				}
				previous = key
				count++
			}
			if page.Next == nil {
				break
			}
			request.After = page.Next
		}
		if count != len(files) {
			t.Fatalf("whole final has %d nodes, want %d", count, len(files))
		}
		assertGraphHistoryCursors(t, f)
		return
	}
	t.Fatal("multi-chunk work did not finish")
}

func TestGraphStartedSupersededByRacingAcceptance(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	workspace := graphFixtureID(1)
	f.register(workspace)
	first := f.tree("first", map[string]string{"a.py": ""})
	f.accept(workspace, f.tree("empty", nil), first, "a.py")
	called := false
	service := f.open(serviceDeps{beforeAppend: func(drafts []ledger.Draft) {
		if !called && drafts[0].Type == ledger.TypeGraphAnalysisStarted {
			called = true
			f.accept(workspace, first, f.tree("next", map[string]string{"a.py": "# next"}), "a.py")
		}
	}})
	before := f.writer.Durable()
	status, err := service.Step(context.Background())
	if err != nil || !called || status.Charged != 0 || status.Version != 0 || status.Digest != ledger.EmptyGraphDigest || service.replay.lastAttempt.phase != ledger.GraphPhaseSuperseded || f.writer.Durable() != before+6 {
		t.Fatalf("started race: %+v, %v", status, err)
	}
}

func TestGraphBudgetSurvivesRestartAndFreshIDs(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	workspace, other := graphFixtureID(1), graphFixtureID(2)
	f.register(workspace)
	empty, tree := f.tree("empty", nil), f.tree("first", map[string]string{"a.py": ""})
	f.accept(workspace, empty, tree, "a.py")
	calls := 0
	options := f.options()
	options.ReadArtifacts = func(_ context.Context, request ArtifactRequest) (ArtifactBatch, error) {
		if request.WorkspaceID != workspace {
			return ArtifactBatch{Reason: ledger.ReasonHelperLimit}, nil
		}
		calls++
		path := fmt.Sprintf("p%06d", calls)
		return ArtifactBatch{Entries: []Artifact{{Path: path, Mode: "100644", GitOID: strings.Repeat("a", 40)}}, NextPath: path, WorkUsed: request.WorkAllowance}, nil
	}
	service, err := openService(options, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	started, err := service.Step(context.Background())
	if err != nil || started.Charged != 0 || calls != 0 {
		t.Fatalf("start performed work: %+v, %v, calls=%d", started, err, calls)
	}
	for quantum := int64(1); quantum <= 200; quantum++ {
		var status Status
		if quantum == 101 {
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			service, err = openService(options, serviceDeps{})
			if err != nil {
				t.Fatal(err)
			}
			before := f.writer.Durable()
			recovery, stepErr := service.Step(context.Background())
			if !errors.Is(stepErr, ErrRecovery) || recovery.Charged != 1000000 || f.writer.Durable() != before {
				t.Fatalf("automatic crash restart: %+v, %v", recovery, stepErr)
			}
			status, err = service.Resume(context.Background(), started.Attempt)
		} else {
			status, err = service.Step(context.Background())
		}
		if err != nil || status.Charged != quantum*10000 || status.Attempt != started.Attempt || calls != int(quantum) {
			t.Fatalf("quantum %d: %+v, %v, calls=%d", quantum, status, err, calls)
		}
	}
	exhausted, err := service.Step(context.Background())
	if err != nil || exhausted.Charged != 2000000 || calls != 200 || service.replay.attempt != nil {
		t.Fatalf("step 202: %+v, %v, calls=%d", exhausted, err, calls)
	}
	commits := graphHistory(t, f.writer)
	progress, err := ledger.DecodeGraphAnalysisProgress(commits[len(commits)-1].Records[0].Payload)
	if err != nil || progress.Phase != ledger.GraphPhaseUnknown || !slices.Equal(progress.Coverage[0].Reasons, []string{ledger.ReasonWorkExhausted}) {
		t.Fatalf("exhaustion result: %+v, %v", progress, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = openService(options, serviceDeps{})
	if err != nil {
		t.Fatal(err)
	}
	before := f.writer.Durable()
	if _, err := service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resume(context.Background(), uuid.NewV7()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("fresh UUID resumed exhausted work: %v", err)
	}
	if f.writer.Durable() != before || calls != 200 {
		t.Fatal("reopen restored spent credit")
	}
	f.register(other)
	f.accept(other, empty, f.tree("other", map[string]string{"b.py": ""}), "b.py")
	driveGraph(t, service)
	before = f.writer.Durable()
	if _, err := service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.writer.Durable() != before || calls != 200 {
		t.Fatal("unrelated frontier restored exhausted workspace credit")
	}
	f.accept(workspace, tree, f.tree("next", map[string]string{"a.py": "# next"}), "a.py")
	next, err := service.Step(context.Background())
	if err != nil || next.Attempt == started.Attempt || next.Charged != 0 {
		t.Fatalf("new subject did not start fresh: %+v, %v", next, err)
	}
	next, err = service.Step(context.Background())
	if err != nil || next.Charged != 10000 || calls != 201 {
		t.Fatalf("new subject credit: %+v, %v, calls=%d", next, err, calls)
	}
}

func graphPausedTool(t *testing.T) (Tool, uint64) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path := filepath.Join(t.TempDir(), "graph-child.exe")
	target, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	writer := io.MultiWriter(target, hash)
	size, err := io.Copy(writer, source)
	if err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	// A valid executable overlay makes skipped streaming verification observable
	// independently of the few quanta spent on source enumeration.
	var padding [64 << 10]byte
	for range 512 {
		n, err := writer.Write(padding[:])
		if err != nil {
			_ = target.Close()
			t.Fatal(err)
		}
		size += int64(n)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	return Tool{Path: path, SHA256: ledger.Digest(hash.Sum(nil)), Version: "test"}, uint64(size+255) / 256
}

func TestGraphWorkerDoesNotBlockReads(t *testing.T) {
	t.Parallel()
	f := newGraphFixture(t)
	workspace := graphFixtureID(1)
	f.register(workspace)
	f.accept(workspace, f.tree("empty", nil), f.tree("python", map[string]string{"a.py": ""}), "a.py")
	tool, verificationUnits := graphPausedTool(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type childReady struct {
		address string
		err     error
	}
	ready := make(chan childReady, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			ready <- childReady{err: err}
			return
		}
		defer connection.Close()
		address, err := bufio.NewReader(connection).ReadString('\n')
		ready <- childReady{address: strings.TrimSpace(address), err: err}
	}()
	options := f.options()
	options.Tools.Python = &tool
	type childLaunch struct {
		Status
		step, reservedStep int64
	}
	launch := make(chan childLaunch, 1)
	var step, reservedStep atomic.Int64
	var service *Service
	service, err = openService(options, serviceDeps{
		environment: func(directory string) []string {
			return append(helperEnvironment(directory), "DWARF_GRAPH_TEST_CHILD=pause", "DWARF_GRAPH_TEST_READY="+listener.Addr().String())
		},
		beforeAppend: func(drafts []ledger.Draft) {
			if drafts[0].Type != ledger.TypeGraphAnalysisProgress {
				return
			}
			progress, err := ledger.DecodeGraphAnalysisProgress(drafts[0].Payload)
			if err == nil && progress.Phase == ledger.GraphPhaseReserved {
				reservedStep.Store(step.Load())
			}
		},
		afterChildStart: func() {
			status, _ := service.Observe(context.Background())
			launch <- childLaunch{Status: status, step: step.Load(), reservedStep: reservedStep.Load()}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		for range 205 {
			step.Add(1)
			_, err := service.Step(ctx)
			if err != nil {
				done <- err
				return
			}
		}
		done <- errors.New("worker never launched the paused child")
	}()
	var child childReady
	select {
	case child = <-ready:
		if child.err != nil {
			t.Fatal(child.err)
		}
	case err := <-done:
		t.Fatalf("worker ended before handshake: %v", err)
	}
	started := <-launch
	if started.State != "busy" || uint64(started.Charged) < verificationUnits || started.step != started.reservedStep {
		t.Fatalf("unreserved or uncharged helper: %+v, tool units=%d", started, verificationUnits)
	}
	commits := graphHistory(t, f.writer)
	last := commits[len(commits)-1].Records[0]
	progress, err := ledger.DecodeGraphAnalysisProgress(last.Payload)
	if last.Type != ledger.TypeGraphAnalysisProgress || err != nil || progress.Phase != ledger.GraphPhaseReserved || progress.ReservedTotal != started.Charged {
		t.Fatalf("helper launched before durable reservation: %+v, %v", progress, err)
	}
	status, err := service.Observe(context.Background())
	if err != nil || status.State != "busy" || status.Version != 0 || status.Digest != ledger.EmptyGraphDigest {
		t.Fatalf("busy observation: %+v, %v", status, err)
	}
	page, err := service.Page(graphPageRequest(status, "nodes"))
	if err != nil || page.State != "busy" || len(page.Nodes) != 0 || page.Through != status.Through || page.ChainHash != status.ChainHash {
		t.Fatalf("busy page: %+v, %v", page.Status, err)
	}
	if _, err := service.Step(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent step: %v", err)
	}
	cancel()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled worker: %v", err)
	}
	if connection, err := net.Dial("tcp", child.address); err == nil {
		_ = connection.Close()
		t.Fatal("Close returned with the helper still listening")
	}
	if _, err := service.Observe(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed observe: %v", err)
	}
	if _, err := service.Page(graphPageRequest(status, "nodes")); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed page: %v", err)
	}
	if _, err := service.Step(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed step: %v", err)
	}
	if _, err := service.Resume(context.Background(), status.Attempt); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed resume: %v", err)
	}
	if err := service.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed close: %v", err)
	}
}

func assertGraphTerminalReason(t *testing.T, fixture *graphFixture, reason string) {
	t.Helper()
	commits := graphHistory(t, fixture.writer)
	last := commits[len(commits)-1]
	progress, err := ledger.DecodeGraphAnalysisProgress(last.Records[len(last.Records)-1].Payload)
	if err != nil || progress.Phase != ledger.GraphPhaseUnknown || len(progress.Coverage) != 1 || !slices.Equal(progress.Coverage[0].Reasons, []string{reason}) {
		t.Fatalf("terminal reason: %+v, %v; want %s", progress, err, reason)
	}
}
func graphMemory(index *graphIndex) uint64 {
	var n uint64
	for _, scope := range index.scopes {
		n += scopeMemory(scope)
	}
	for _, node := range index.nodes {
		n += nodeMemory(node)
	}
	for _, edge := range index.edges {
		n += edgeMemory(edge)
	}
	for _, coverage := range index.coverage {
		n += coverageMemory(coverage)
	}
	return n
}

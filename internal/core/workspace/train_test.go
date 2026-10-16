package workspace

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// advanceMain moves only a disposable fixture's canonical branch. Trees are
// built through integrationTree's private index, never the scratch's index.
func advanceMain(t *testing.T, f *serviceFixture, files map[string]string) string {
	t.Helper()
	parent := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "--verify", "--end-of-options", "refs/heads/main^{commit}")))
	var tree string
	if files == nil {
		env := append(slices.Clone(f.env), "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"))
		gitCommand(t, env, f.root, nil, "read-tree", "--end-of-options", parent)
		tree = strings.TrimSpace(string(gitCommand(t, env, f.root, nil, "write-tree")))
	} else {
		tree = integrationTree(t, f.gitFixture, files)
	}
	commit := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-p", parent, "-m", "fixture main advance", "--end-of-options", tree)))
	gitCommand(t, f.env, f.root, nil, "update-ref", "--end-of-options", "refs/heads/main", commit, parent)
	return commit
}

func admittedRecord(t *testing.T, f *serviceFixture, id uuid.UUID) ledger.PromotionAdmitted {
	t.Helper()
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			if record.Type == ledger.TypePromotionAdmitted {
				p, err := ledger.DecodePromotionAdmitted(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if p.RequestID == id {
					return p
				}
			}
		}
	}
	t.Fatalf("missing admission %s", id)
	return ledger.PromotionAdmitted{}
}

func generations(t *testing.T, f *serviceFixture, id uuid.UUID) []ledger.GenerationDerived {
	t.Helper()
	var found []ledger.GenerationDerived
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			if record.Type == ledger.TypeGenerationDerived {
				p, err := ledger.DecodeGenerationDerived(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if p.RequestID == id {
					found = append(found, p)
				}
			}
		}
	}
	return found
}

func mustIntegrate(t *testing.T, f *serviceFixture) {
	t.Helper()
	if err := f.svc.integrate(context.Background(), f.w.ID); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalCandidatesOverForeignPredecessor(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	a, b := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	a1, b1 := rootRequest(a), rootRequest(b)
	writeTestFile(t, filepath.Join(a.Dir, "a.txt"), []byte("a\n"))
	first := mustAdmit(t, f, a1)
	writeTestFile(t, filepath.Join(b.Dir, "f.txt"), []byte("f\n"))
	mustAdmit(t, f, b1)
	a2 := rootRequest(a)
	a2.Head = a1.RequestID
	writeTestFile(t, filepath.Join(a.Dir, "b.txt"), []byte("b\n"))
	mustAdmit(t, f, a2)
	mustIntegrate(t, f)
	files := map[string]string{"base.txt": "one\ntwo\n"}
	var predecessor ledger.GenerationDerived
	for i, r := range []Request{a1, b1, a2} {
		name := []string{"a", "f", "b"}[i]
		files[name+".txt"] = name + "\n"
		gs := generations(t, f, r.RequestID)
		if len(gs) != 1 || gs[0].Outcome != ledger.GenerationClean || gs[0].DerivedTree != integrationTree(t, f.gitFixture, files) {
			t.Fatalf("candidate %d: %+v", i, gs)
		}
		g := gs[0]
		if i > 0 && (g.PredecessorGenerationID != predecessor.GenerationID || g.PredecessorCommit != predecessor.DerivedCommit) {
			t.Fatalf("candidate %d predecessor: %+v", i, g)
		}
		if i == 2 {
			diff := string(gitCommand(t, f.env, f.root, nil, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", predecessor.DerivedTree, g.DerivedTree))
			if diff != "b.txt\x00" {
				t.Fatalf("successor resubmitted old change: %q", diff)
			}
		}
		predecessor = g
	}
	if p := admittedRecord(t, f, a2.RequestID); p.DeltaBaseTree != first.SealedTree {
		t.Fatalf("delta base: %+v", p)
	}
	before := snapshotRequestEffects(t, f)
	mustIntegrate(t, f)
	assertRequestEffects(t, f, before)
}

func TestLocalParentFailureEndsSuffix(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	var requests []Request
	var admissions []ledger.PromotionAdmitted
	r := rootRequest(s)
	for i := range 4 {
		writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("local\ntwo\n"))
		writeTestFile(t, filepath.Join(s.Dir, string(rune('a'+i))+".txt"), []byte("only this failed lineage "+string(rune('a'+i))))
		mustAdmit(t, f, r)
		requests = append(requests, r)
		admissions = append(admissions, admittedRecord(t, f, r.RequestID))
		r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	}
	advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
	mustIntegrate(t, f)
	for i, r := range requests {
		state, err := f.svc.RequestPromotion(context.Background(), r)
		want := RequestDependencyFailed
		if i == 0 {
			want = RequestConflict
		}
		if err != nil || state.State != want || (i > 0 && state.FailedRequest != requests[0].RequestID) {
			t.Fatalf("ending %d: %+v %v", i, state, err)
		}
		if i > 0 && len(generations(t, f, r.RequestID)) != 0 {
			t.Fatal("failed descendant integrated")
		}
		ref := "refs/dwarf-harness/captures/" + admissions[i].CaptureID.String()
		if oid, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || oid != "" {
			t.Fatalf("failed ref retained: %s %v", oid, err)
		}
	}
	commits := serviceCommits(t, f.store)
	last := commits[len(commits)-1]
	var ended, unpinned []uuid.UUID
	for _, record := range last.Records {
		switch record.Type {
		case ledger.TypePromotionFailed:
			p, _ := ledger.DecodePromotionFailed(record.Payload)
			ended = append(ended, p.RequestID)
		case ledger.TypeBodyUnpin:
			p, _ := ledger.DecodeBodyUnpin(record.Payload)
			unpinned = append(unpinned, p.PinID)
		}
	}
	for i := range requests {
		if len(ended) != 4 || len(unpinned) != 4 || ended[i] != requests[i].RequestID || unpinned[i] != admissions[i].PinID {
			t.Fatalf("cascade not atomic and ordered: %v %v", ended, unpinned)
		}
	}
	blob := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "--verify", "--end-of-options", admissions[3].SealedTree+":d.txt")))
	gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
	_, exit, err := f.git.repository(context.Background(), f.colony, f.p.CommonDir, nil, []int{0, 1}, "cat-file", "-e", "--end-of-options", blob)
	if err != nil || exit == 0 {
		t.Fatalf("ended-only blob survived: exit=%d %v", exit, err)
	}
	if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 {
		t.Fatalf("ended retention replay: %+v", report)
	}
	state, err := f.svc.RequestPromotion(context.Background(), requests[2])
	if err != nil || state.State != RequestDependencyFailed {
		t.Fatalf("ended replay: %+v %v", state, err)
	}
	r = rootRequest(s)
	r.RestartFrom = requests[3].RequestID
	assertUnavailable(t, promotionError(context.Background(), f.svc, r), "no_validator")
	if !slices.Equal(ended, []uuid.UUID{requests[0].RequestID, requests[1].RequestID, requests[2].RequestID, requests[3].RequestID}) {
		t.Fatal("cascade order changed")
	}
}

func TestConcurrentSuccessorJoinsFailureEnding(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("local\ntwo\n"))
	requests := []Request{rootRequest(s)}
	for range 3 {
		r := requests[len(requests)-1]
		mustAdmit(t, f, r)
		next := rootRequest(s)
		next.Head = r.RequestID
		requests = append(requests, next)
	}
	advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
	before := len(serviceCommits(t, f.store))
	f.svc.deps.beforeAppend = func() {
		f.svc.deps.beforeAppend = nil
		mustAdmit(t, f, requests[3])
	}
	mustIntegrate(t, f)
	commits := serviceCommits(t, f.store)
	if len(commits) != before+2 {
		t.Fatalf("want successor admission and one ending commit, got %d commits", len(commits)-before)
	}
	var ended []uuid.UUID
	for _, record := range commits[len(commits)-1].Records {
		if record.Type == ledger.TypePromotionFailed {
			p, err := ledger.DecodePromotionFailed(record.Payload)
			if err != nil {
				t.Fatal(err)
			}
			ended = append(ended, p.RequestID)
		}
	}
	want := []uuid.UUID{requests[0].RequestID, requests[1].RequestID, requests[2].RequestID, requests[3].RequestID}
	if !slices.Equal(ended, want) {
		t.Fatalf("ending omitted or reordered a checkpoint: %v want %v", ended, want)
	}
	for i, r := range requests {
		state, err := f.svc.RequestPromotion(context.Background(), r)
		outcome := RequestDependencyFailed
		if i == 0 {
			outcome = RequestConflict
		}
		if err != nil || state.State != outcome || (i > 0 && state.FailedRequest != requests[0].RequestID) {
			t.Fatalf("ending %d: %+v %v", i, state, err)
		}
	}
}

func TestForeignRemovalRebuildsSurvivors(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	b, a := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	b0 := rootRequest(b)
	writeTestFile(t, filepath.Join(b.Dir, "base.txt"), []byte("foreign\ntwo\n"))
	mustAdmit(t, f, b0)
	b1 := rootRequest(b)
	b1.Head = b0.RequestID
	writeTestFile(t, filepath.Join(b.Dir, "foreign.txt"), []byte("withdrawn\n"))
	mustAdmit(t, f, b1)
	a1 := rootRequest(a)
	writeTestFile(t, filepath.Join(a.Dir, "survives.txt"), []byte("survives\n"))
	mustAdmit(t, f, a1)
	mustIntegrate(t, f)
	old := generations(t, f, a1.RequestID)[0]
	main := advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
	mustIntegrate(t, f)
	gs := generations(t, f, a1.RequestID)
	want := integrationTree(t, f.gitFixture, map[string]string{"base.txt": "canonical\ntwo\n", "survives.txt": "survives\n"})
	if len(gs) != 2 || gs[1].Supersedes != old.GenerationID || gs[1].GenerationID == old.GenerationID || gs[1].PredecessorCommit != main || gs[1].DerivedTree != want {
		t.Fatalf("foreign removal did not rebuild survivor: %+v", gs)
	}
	state, err := f.svc.RequestPromotion(context.Background(), a1)
	if err != nil || state.State != RequestPending {
		t.Fatalf("foreign failure ended survivor: %+v %v", state, err)
	}
	if ref, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, "refs/dwarf-harness/generations/"+old.GenerationID.String(), 40); err != nil || ref != "" {
		t.Fatalf("obsolete generation retained: %s %v", ref, err)
	}
}

func TestSpeculativeConflictWaits(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	b, a := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	b1, a1 := rootRequest(b), rootRequest(a)
	writeTestFile(t, filepath.Join(b.Dir, "base.txt"), []byte("B\ntwo\n"))
	mustAdmit(t, f, b1)
	writeTestFile(t, filepath.Join(a.Dir, "base.txt"), []byte("A\ntwo\n"))
	mustAdmit(t, f, a1)
	a2 := rootRequest(a)
	a2.Head = a1.RequestID
	writeTestFile(t, filepath.Join(a.Dir, "next.txt"), []byte("next\n"))
	mustAdmit(t, f, a2)
	mustIntegrate(t, f)
	gs := generations(t, f, a1.RequestID)
	if len(gs) != 1 || gs[0].Outcome != ledger.GenerationConflict || len(generations(t, f, a2.RequestID)) != 0 {
		t.Fatalf("speculative conflict advanced: %+v", gs)
	}
	old := gs[0]
	state, err := f.svc.RequestPromotion(context.Background(), a1)
	if err != nil || state.State != RequestPending || state.ConflictReport != (ledger.Digest{}) {
		t.Fatalf("speculative disclosure: %+v %v", state, err)
	}
	advanceMain(t, f, map[string]string{"base.txt": "A\ntwo\n"})
	mustIntegrate(t, f)
	gs = generations(t, f, a1.RequestID)
	if len(gs) != 2 || gs[1].Outcome != ledger.GenerationClean || gs[1].Supersedes != old.GenerationID || len(generations(t, f, a2.RequestID)) != 1 {
		t.Fatalf("speculative conflict not rebuilt: %+v", gs)
	}
	found := false
	for _, commit := range serviceCommits(t, f.store) {
		supersedes := false
		unpins := false
		for _, record := range commit.Records {
			if record.Type == ledger.TypeGenerationDerived {
				p, _ := ledger.DecodeGenerationDerived(record.Payload)
				supersedes = supersedes || p.Supersedes == old.GenerationID
			}
			if record.Type == ledger.TypeBodyUnpin {
				p, _ := ledger.DecodeBodyUnpin(record.Payload)
				unpins = unpins || p.PinID == old.ReportPinID
			}
		}
		if supersedes {
			found = unpins
		}
	}
	if !found {
		t.Fatal("supersession did not release the old report in its commit")
	}
	// B's terminal conflict names the same path and therefore the same body.
	// Release that independent obligation before testing A's superseded pin.
	writeTestFile(t, filepath.Join(b.Dir, "base.txt"), []byte("one\ntwo\n"))
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(b)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Collect(context.Background(), ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReadBody(context.Background(), old.ConflictSHA256); !errors.Is(err, ledgerstore.ErrBodyUnavailable) {
		t.Fatalf("superseded report stayed pinned: %v", err)
	}
}

func TestConflictIsVisibleWork(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("local\ntwo\n"))
	mustAdmit(t, f, r)
	advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
	mustIntegrate(t, f)
	state, err := f.svc.RequestPromotion(context.Background(), r)
	if err != nil || state.State != RequestConflict || state.ConflictReport == (ledger.Digest{}) {
		t.Fatalf("visible conflict: %+v %v", state, err)
	}
	want := "{\"format\":\"dwarf-harness/conflict-report\",\"version\":1,\"cause\":\"merge\",\"paths\":1}\n{\"path\":\"base.txt\"}\n"
	for _, reopen := range []bool{false, true} {
		if reopen {
			f.reopen(t, serviceConfig())
		}
		if _, err := f.store.Collect(context.Background(), ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64}); err != nil {
			t.Fatal(err)
		}
		body, err := f.store.ReadBody(context.Background(), state.ConflictReport)
		if err != nil || string(body) != want {
			t.Fatalf("terminal conflict report lost: %q %v", body, err)
		}
	}
	refs, err := f.svc.listRefs(context.Background(), f.svc.workspaces[f.w.ID], "refs/dwarf-harness/generations/")
	if err != nil || len(refs) != 0 {
		t.Fatalf("conflict retained candidate: %v %v", refs, err)
	}
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("one\ntwo\n"))
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(s)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Collect(context.Background(), ledgerstore.CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReadBody(context.Background(), state.ConflictReport); !errors.Is(err, ledgerstore.ErrBodyUnavailable) {
		t.Fatalf("closed scratch report retained: %v", err)
	}
	if replay, err := f.svc.RequestPromotion(context.Background(), r); err != nil || replay != state {
		t.Fatalf("closed-scratch replay: %+v %v", replay, err)
	}
}

func TestNewRootAfterFailure(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	var requests []Request
	r := rootRequest(s)
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("failed change\ntwo\n"))
	var lineage uuid.UUID
	for range 4 {
		state := mustAdmit(t, f, r)
		lineage = state.LineageID
		requests = append(requests, r)
		r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	}
	advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
	mustIntegrate(t, f)
	for _, candidate := range []Request{r, rootRequest(s), {RequestID: uuid.NewV7(), Actor: ledger.ActorAgent, ContextID: s.ContextID, WorkspaceID: s.WorkspaceID, RestartFrom: requests[1].RequestID}} {
		var head *HeadError
		before := snapshotRequestEffects(t, f)
		if err := promotionError(context.Background(), f.svc, candidate); !errors.As(err, &head) || !head.RestartRequired || head.Head != requests[3].RequestID {
			t.Fatalf("restart refusal: %v", err)
		}
		assertRequestEffects(t, f, before)
	}
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("one\ntwo\n"))
	writeTestFile(t, filepath.Join(s.Dir, "fresh.txt"), []byte("fresh\n"))
	r = rootRequest(s)
	r.RestartFrom = requests[3].RequestID
	state := mustAdmit(t, f, r)
	p := admittedRecord(t, f, r.RequestID)
	if state.LineageID == lineage || state.Checkpoint != 1 || p.BaselineTree != s.AnchorTree || p.DeltaBaseTree != s.AnchorTree || p.RestartFrom != requests[3].RequestID {
		t.Fatalf("new root reused failed lineage: %+v", p)
	}
	mustIntegrate(t, f)
	g := generations(t, f, r.RequestID)
	want := integrationTree(t, f.gitFixture, map[string]string{"base.txt": "canonical\ntwo\n", "fresh.txt": "fresh\n"})
	if len(g) != 1 || g[0].DerivedTree != want {
		t.Fatalf("new root resurrected failed change: %+v", g)
	}
	for i, old := range requests {
		got, err := f.svc.RequestPromotion(context.Background(), old)
		if err != nil || got.State == RequestPending || len(generations(t, f, old.RequestID)) != map[bool]int{true: 1, false: 0}[i == 0] {
			t.Fatalf("old request reopened: %+v %v", got, err)
		}
	}
}

func TestWindowBoundsGenerations(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	var requests []Request
	for i := range 6 {
		s := mustScratch(t, f, uuid.NewV7())
		r := rootRequest(s)
		if i == 0 {
			writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("first\ntwo\n"))
		} else {
			writeTestFile(t, filepath.Join(s.Dir, fmt.Sprintf("independent-%d.txt", i)), []byte(fmt.Sprint(i)))
		}
		mustAdmit(t, f, r)
		requests = append(requests, r)
	}
	mustIntegrate(t, f)
	for i, r := range requests {
		want := 0
		if i < 4 {
			want = 1
		}
		if got := len(generations(t, f, r.RequestID)); got != want {
			t.Fatalf("initial position %d: %d generations", i, got)
		}
	}
	advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
	mustIntegrate(t, f)
	// C2 removal is failure, not acceptance: changed predecessors MUST rebuild.
	for i, r := range requests {
		want := 2
		if i == 4 {
			want = 1
		}
		if i == 5 {
			want = 0
		}
		if got := len(generations(t, f, r.RequestID)); got != want {
			t.Fatalf("post-removal position %d: %d generations want %d", i, got, want)
		}
	}
	var live []uuid.UUID
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypePromotionAdmitted:
				p, _ := ledger.DecodePromotionAdmitted(record.Payload)
				live = append(live, p.RequestID)
			case ledger.TypeGenerationDerived:
				p, _ := ledger.DecodeGenerationDerived(record.Payload)
				if pos := slices.Index(live, p.RequestID); pos < 0 || pos >= 4 {
					t.Fatalf("generation outside leading window: %+v at %d", p, pos)
				}
			case ledger.TypePromotionFailed:
				p, _ := ledger.DecodePromotionFailed(record.Payload)
				live = slices.DeleteFunc(live, func(id uuid.UUID) bool { return id == p.RequestID })
			}
		}
	}
}

func TestIntegrationUsesPinnedEnvironment(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	writeTestFile(t, filepath.Join(s.Dir, "derived.txt"), []byte("integrated\n"))
	state := mustAdmit(t, f, r)
	generation := uuid.MustParse("018bcfe5-687b-7000-8000-000000000002")
	f.svc.deps.newID = func() uuid.UUID { return generation }
	global := filepath.Join(t.TempDir(), "config")
	writeTestFile(t, global, []byte("[user]\n name = Host\n email = host@example.invalid\n[commit]\n gpgSign = true\n"))
	gitCommand(t, f.env, f.root, nil, "config", "user.name", "Repository Host")
	gitCommand(t, f.env, f.root, nil, "config", "commit.gpgSign", "true")
	host := append(slices.Clone(f.env), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_PARAMETERS='commit.gpgSign=true'", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z")
	f.svc.git.environ = func() []string { return host }
	mustIntegrate(t, f)
	g := generations(t, f, r.RequestID)[0]
	want := fmt.Sprintf("tree %s\nparent %s\nauthor DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\ncommitter DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\n\ndwarf-harness integration\n\nrequest %s\ngeneration %s\n", state.SealedTree, s.BaseCommit, r.RequestID, generation)
	if got := string(gitCommand(t, f.env, f.root, nil, "cat-file", "commit", g.DerivedCommit)); got != want {
		t.Fatalf("host settings changed service integration: %q", got)
	}
}

func TestConcurrentIntegrationWaitsWithinContext(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	mustAdmit(t, f, r)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.svc.deps.beforeAppend = func() { once.Do(func() { close(entered); <-release }) }
	first := make(chan error, 1)
	go func() { first <- f.svc.integrate(context.Background(), f.w.ID) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.svc.integrate(ctx, f.w.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting integration ignored context: %v", err)
	}
	second := make(chan error, 1)
	go func() { second <- f.svc.integrate(context.Background(), f.w.ID) }()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if got := generations(t, f, r.RequestID); len(got) != 1 {
		t.Fatalf("concurrent integration duplicated generation: %+v", got)
	}
}

func TestTrainMutationLockShared(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	f, w := finalizationFixture(t, h, false)
	// Register a second repository in the same colony, not another Service.
	other := filepath.Join(t.TempDir(), "other")
	gitCommand(t, h.env, h.gitFixture.root, nil, "clone", "--no-hardlinks", "--no-checkout", "-b", "main", h.gitFixture.root, other)
	gitCommand(t, h.env, other, nil, "checkout", "--detach")
	otherProposal, err := f.svc.git.inspect(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	otherWorkspace, err := f.svc.Register(context.Background(), otherProposal)
	if err != nil {
		t.Fatal(err)
	}
	scratch := mustScratch(t, f, uuid.NewV7())
	mustAdmit(t, f, rootRequest(scratch))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	entered, continueIntegration := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.svc.deps.beforeAppend = func() {
		once.Do(func() {
			close(entered)
			select {
			case <-continueIntegration:
			case <-ctx.Done():
			}
		})
	}
	integrated := make(chan error, 1)
	go func() { integrated <- f.svc.integrate(ctx, w.ID) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiter := &mergeParentWaitContext{Context: ctx, waiting: make(chan struct{})}
	finalized := make(chan error, 1)
	go func() {
		release, err := f.svc.lockTrain(waiter, w.ID)
		if err == nil {
			before := f.store.Durable()
			var state mainUpdateState
			state, err = f.svc.guardedMainUpdate(ctx, w, h.old, h.target, h.tree)
			if err == nil && (state != mainUpdateMoved || f.store.Durable() != before) {
				err = fmt.Errorf("record-free finalization: state=%v durable=%d/%d", state, before, f.store.Durable())
			}
			release()
		}
		finalized <- err
	}()
	select {
	case <-waiter.waiting:
	case err := <-finalized:
		t.Fatalf("finalization bypassed integration lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// This must finish while the first workspace is still held.
	if err := f.svc.integrate(ctx, otherWorkspace.ID); err != nil {
		t.Fatalf("other workspace serialized behind first: %v", err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if release, err := f.svc.lockTrain(cancelled, w.ID); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("cancelled wait acquired a lock: %v", err)
	}
	assertCanonicalTip(t, f, h.old)
	close(continueIntegration)
	if err := <-integrated; err != nil {
		t.Fatal(err)
	}
	if err := <-finalized; err != nil {
		t.Fatal(err)
	}
	assertCanonicalTip(t, f, h.target)
	// Also exercise the opposite order: integration must wait for the same
	// channel while a finalizer owns it, and cancellation must not release it.
	release, err := f.svc.lockTrain(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, stop = context.WithCancel(ctx)
	stop()
	if err := f.svc.integrate(cancelled, w.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("integration bypassed finalizer lock: %v", err)
	}
	release()
}

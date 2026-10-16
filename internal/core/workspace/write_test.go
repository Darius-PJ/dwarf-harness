package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func TestObservationCommitDeterministic(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	ctx := context.Background()
	scratch := uuid.MustParse("018bcfe5-687b-7000-8000-000000000001")
	observation := uuid.MustParse("018bcfe5-687b-7000-8000-000000000002")
	parent, tree := f.s.Worktree.BaseCommit, f.s.Worktree.AnchorTree
	want := fmt.Sprintf("tree %s\nparent %s\nauthor DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\ncommitter DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\n\ndwarf-harness scratch observation\n\nscratch %s\nobservation %s\n", tree, parent, scratch, observation)
	wantID := strings.TrimSpace(string(gitCommand(t, f.f.env, f.f.root, []byte(want), "hash-object", "-t", "commit", "--stdin", "--no-filters")))
	for range 2 {
		got, err := f.engine.observationCommit(ctx, f.s, tree, parent, scratch, observation)
		if err != nil || got != wantID || got == parent {
			t.Fatalf("deterministic unchanged-tree observation: %s want %s: %v", got, wantID, err)
		}
		if object := string(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "commit", got)); object != want {
			t.Fatalf("observation bytes: %q", object)
		}
	}
	merge, err := f.engine.mergeBackCommit(ctx, f.s, tree, parent, scratch, observation)
	if err != nil {
		t.Fatal(err)
	}
	mergeWant := strings.ReplaceAll(strings.ReplaceAll(want, "scratch observation", "scratch merge-back"), "\nobservation ", "\nmerge-back ")
	if object := string(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "commit", merge)); object != mergeWant {
		t.Fatalf("merge-back bytes: %q", object)
	}
}

func TestScratchCommitRefCompareAndSwap(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			f := newGitFixture(t, format)
			p, err := f.git.inspect(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			id := uuid.NewV7()
			main := fixtureMain(t, f)
			tree := integrationTree(t, f, map[string]string{"private": "retained\n"})
			commit := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-p", main, "-m", "private", tree)))
			ref := "refs/dwarf-harness/scratch-commits/" + id.String()
			if err := f.git.createScratchCommitRef(ctx, f.colony, p, id, commit); err != nil {
				t.Fatal(err)
			}
			if err := f.git.createScratchCommitRef(ctx, f.colony, p, id, main); !errors.Is(err, ErrGit) {
				t.Fatalf("scratch ref overwritten: %v", err)
			}
			if err := f.git.deleteScratchCommitRef(ctx, f.colony, p, id, main); !errors.Is(err, ErrGit) {
				t.Fatalf("wrong old value deleted scratch ref: %v", err)
			}
			gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
			got, err := f.git.readRef(ctx, f.colony, p.CommonDir, ref+"^{tree}", len(commit))
			if err != nil || got != tree {
				t.Fatalf("private history lost after gc: %s %v", got, err)
			}
			if err := f.git.deleteScratchCommitRef(ctx, f.colony, p, id, commit); err != nil {
				t.Fatal(err)
			}
			got, err = f.git.readRef(ctx, f.colony, p.CommonDir, ref, len(commit))
			if err != nil || got != "" || fixtureMain(t, f) != main {
				t.Fatalf("scratch retention deletion changed wrong ref: %s %v", got, err)
			}
		})
	}
}

func TestSecondWriterForksLatestCommittedTask(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	agent := uuid.NewV7()
	binding := WriteBinding{WriterID: uuid.NewV7(), ContextID: task.ContextID, AgentID: agent, TaskAgentID: agent, TaskScratchID: task.ID}
	first, err := f.svc.BeginWrite(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	writeTestFile(t, filepath.Join(first.Scratch().Dir, "base.txt"), []byte("committed task\n"))
	observed, err := first.CommitObserved(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	binding.WriterID = uuid.NewV7()
	first, err = f.svc.BeginWrite(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	release, err := first.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("uncommitted task\n"))
	binding.WriterID, binding.ContextID = uuid.NewV7(), uuid.NewV7()
	second, err := f.svc.BeginWrite(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	temporary := second.Scratch()
	if temporary.ID == task.ID || temporary.Dir == task.Dir || temporary.Branch == task.Branch || temporary.ParentScratchID != task.ID || temporary.BaseCommit != observed.Commit {
		t.Fatalf("second writer did not fork the acknowledged task: %+v", temporary)
	}
	if got := string(readTestFile(t, filepath.Join(temporary.Dir, "base.txt"))); got != "committed task\n" {
		t.Fatalf("temporary contains main or uncommitted bytes: %q", got)
	}
	if got := string(readTestFile(t, filepath.Join(task.Dir, "base.txt"))); got != "uncommitted task\n" {
		t.Fatalf("routing touched the task: %q", got)
	}
	before := f.store.Durable()
	repeated, err := f.svc.BeginWrite(ctx, binding)
	if err != nil || repeated != second || f.store.Durable() != before {
		t.Fatalf("repeated writer created work: %p %v", repeated, err)
	}
}

func TestWriterBindingAndContextExclusion(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	b := testWriteBinding(task)
	before := snapshotRequestEffects(t, f)
	for _, change := range []func(*WriteBinding){
		func(b *WriteBinding) { b.WriterID = uuid.UUID{} },
		func(b *WriteBinding) { b.ContextID = uuid.UUID{} },
		func(b *WriteBinding) { b.AgentID = uuid.UUID{} },
		func(b *WriteBinding) { b.TaskAgentID = uuid.UUID{} },
		func(b *WriteBinding) { b.TaskScratchID = uuid.UUID{} },
		func(b *WriteBinding) { b.ResumeScratchID = uuid.MustParse("00000000-0000-4000-8000-000000000001") },
	} {
		invalid := b
		change(&invalid)
		if _, err := f.svc.BeginWrite(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid binding accepted: %v", err)
		}
	}
	foreign := b
	foreign.AgentID, foreign.ContextID = uuid.NewV7(), uuid.NewV7()
	unauthorized, err := f.svc.BeginWrite(ctx, foreign)
	if unauthorized != nil {
		t.Cleanup(unauthorized.Close)
	}
	if !errors.Is(err, ErrNotBound) {
		t.Fatalf("another agent routed: %v", err)
	}
	assertRequestEffects(t, f, before)
	own := mustWriter(t, f, b)
	changed := b
	changed.AgentID, changed.TaskAgentID = foreign.AgentID, foreign.AgentID
	if _, err := f.svc.BeginWrite(ctx, changed); !errors.Is(err, ErrWriterMismatch) {
		t.Fatalf("writer binding changed: %v", err)
	}
	changed = b
	changed.WriterID = uuid.NewV7()
	_, err = f.svc.BeginWrite(ctx, changed)
	assertBusy(t, err, "context_busy")
	otherTask := mustScratch(t, f, uuid.NewV7())
	changed.TaskScratchID = otherTask.ID
	other := mustWriter(t, f, changed)
	if other.Scratch().ParentScratchID != otherTask.ID || own.Scratch().ID != task.ID {
		t.Fatal("one context cannot hold its own writer and another task's writer")
	}
	nested := changed
	nested.WriterID, nested.TaskScratchID = uuid.NewV7(), other.Scratch().ID
	if _, err := f.svc.BeginWrite(ctx, nested); !errors.Is(err, ErrNoScratch) {
		t.Fatalf("temporary accepted as task: %v", err)
	}
}

func TestTemporaryCoexistsWithDedicated(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	task, dedicated := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	b := testWriteBinding(task)
	b.ContextID = dedicated.ContextID
	writer := mustWriter(t, f, b)
	temporary := writer.Scratch()
	writer.Close()
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(task)); !errors.Is(err, ErrTemporaryChildren) {
		t.Fatalf("parent closed with child: %v", err)
	}
	if report := f.reopen(t, serviceConfig()); len(report.Pending) != 0 {
		t.Fatalf("temporary replay: %+v", report)
	}
	if got := mustScratch(t, f, dedicated.ContextID); got.ID != dedicated.ID {
		t.Fatalf("temporary replaced the dedicated pair: %+v", got)
	}
	b.WriterID, b.ContextID = uuid.NewV7(), uuid.NewV7()
	laterWriter := mustWriter(t, f, b)
	laterTemporary := laterWriter.Scratch()
	laterWriter.Close()
	laterDedicated := mustScratch(t, f, b.ContextID)
	if laterDedicated.ID == laterTemporary.ID || laterDedicated.ParentScratchID != (uuid.UUID{}) {
		t.Fatalf("dedicated-after-temporary returned foreign task work: %+v", laterDedicated)
	}
	if got := mustScratch(t, f, b.ContextID); got.ID != laterDedicated.ID {
		t.Fatal("dedicated-after-temporary failed to occupy its pair")
	}
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(laterTemporary)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(temporary)); err != nil {
		t.Fatal(err)
	}
	if got := mustScratch(t, f, dedicated.ContextID); got.ID != dedicated.ID {
		t.Fatal("temporary closure removed dedicated pair")
	}
	if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(task)); err != nil {
		t.Fatalf("parent remains blocked after child closure: %v", err)
	}
}

func TestInterruptedWriterResumesTemporary(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	b := testWriteBinding(task)
	b.ContextID = uuid.NewV7()
	writer := mustWriter(t, f, b)
	temporary := writer.Scratch()
	release, err := writer.Lease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(temporary.Dir, "unfinished.txt"), []byte("interrupted work\n"))
	release()
	writer.Close()
	if err := f.svc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f.reopen(t, serviceConfig())
	b.WriterID = uuid.NewV7()
	fresh := mustWriter(t, f, b)
	if fresh.Scratch().ID == temporary.ID {
		t.Fatal("fresh first write reused interrupted temporary")
	}
	fresh.Close()
	b.WriterID, b.ResumeScratchID = uuid.NewV7(), temporary.ID
	wrong := b
	wrong.ContextID = uuid.NewV7()
	before := f.store.Durable()
	unauthorized, err := f.svc.BeginWrite(ctx, wrong)
	if unauthorized != nil {
		t.Cleanup(unauthorized.Close)
	}
	if !errors.Is(err, ErrNotBound) {
		t.Fatalf("resumed another context's work: %v", err)
	}
	resumed := mustWriter(t, f, b)
	if resumed.Scratch().Dir != temporary.Dir || f.store.Durable() != before {
		t.Fatal("resume created or recorded another scratch")
	}
	observation := mustObserve(t, resumed)
	if got := string(gitCommand(t, f.env, f.root, nil, "show", observation.Commit+":unfinished.txt")); got != "interrupted work\n" {
		t.Fatalf("resumed observation lost edits: %q", got)
	}
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(temporary)); err != nil {
		t.Fatal(err)
	}
	b.WriterID = uuid.NewV7()
	if _, err := f.svc.BeginWrite(ctx, b); !errors.Is(err, ErrClosed) {
		t.Fatalf("resumed closed temporary: %v", err)
	}
}

func TestObservedCommitIgnoresMutableIndex(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	task := mustScratch(t, f, uuid.NewV7())
	writer := mustWriter(t, f, testWriteBinding(task))
	writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("staged lie\n"))
	gitCommand(t, f.env, task.Dir, nil, "add", "--", "base.txt")
	gitCommand(t, f.env, task.Dir, nil, "update-index", "--skip-worktree", "base.txt")
	writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("actual bytes\n"))
	writeTestFile(t, filepath.Join(task.Dir, "new.txt"), []byte("untracked bytes\n"))
	admin, err := scratchAdmin(task.worktree(), f.p.CommonDir)
	if err != nil {
		t.Fatal(err)
	}
	index := readTestFile(t, filepath.Join(admin, "index"))
	before := f.store.Durable()
	f.svc.deps.beforeAppend = func() {
		branch, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, task.Branch, 40)
		if err != nil || branch != task.BaseCommit || f.store.Durable() != before {
			t.Fatalf("private branch moved before acknowledgment: %s %v", branch, err)
		}
	}
	observation := mustObserve(t, writer)
	f.svc.deps.beforeAppend = nil
	if got := string(gitCommand(t, f.env, f.root, nil, "show", observation.Commit+":base.txt")); got != "actual bytes\n" {
		t.Fatalf("observation trusted mutable index: %q", got)
	}
	if got := string(gitCommand(t, f.env, f.root, nil, "show", observation.Commit+":new.txt")); got != "untracked bytes\n" {
		t.Fatalf("observation omitted eligible untracked file: %q", got)
	}
	parent := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", observation.Commit+"^")))
	if parent != task.BaseCommit || observation.Sequence != before+1 || !bytes.Equal(index, readTestFile(t, filepath.Join(admin, "index"))) {
		t.Fatal("observation parent, sequence or real index changed")
	}
	commits := serviceCommits(t, f.store)
	record := commits[len(commits)-1].Records[0]
	p, err := ledger.DecodeScratchChanged(record.Payload)
	if err != nil || !slices.Equal(p.WrittenPaths, []string{"base.txt", "new.txt"}) || !slices.Equal(p.ChangedPaths, p.WrittenPaths) || record.AgentID != (uuid.UUID{}) {
		t.Fatalf("observation path evidence: %+v %v", p, err)
	}
	manifest, err := f.store.ReadBody(ctx, p.ManifestSHA256)
	if err != nil || !bytes.Contains(manifest, []byte(`"path":"new.txt"`)) {
		t.Fatalf("raw observation manifest missing new file: %s %v", manifest, err)
	}
	second := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	if second.Tree != observation.Tree || second.Commit == observation.Commit {
		t.Fatal("unchanged observation was suppressed")
	}
}

func TestPrivateObservationEligibility(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	task := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(task.Dir, "new.txt"), []byte("first\n"))
	mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	if err := os.Remove(filepath.Join(task.Dir, "base.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(task.Dir, ".gitignore"), []byte("base.txt\nnew.txt\nunknown.txt\n"))
	writeTestFile(t, filepath.Join(task.Dir, "new.txt"), []byte("ignored but previously observed\n"))
	mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	writeTestFile(t, filepath.Join(task.Dir, "base.txt"), []byte("ignored but anchor-tracked\n"))
	writeTestFile(t, filepath.Join(task.Dir, "unknown.txt"), []byte("must remain private\n"))
	last := mustObserve(t, mustWriter(t, f, testWriteBinding(task)))
	names := string(gitCommand(t, f.env, f.root, nil, "ls-tree", "-r", "--name-only", last.Tree))
	if names != ".gitignore\nbase.txt\nnew.txt\n" {
		t.Fatalf("anchor/tip trusted names were lost or unknown file admitted: %q", names)
	}
	for path, want := range map[string]string{"base.txt": "ignored but anchor-tracked\n", "new.txt": "ignored but previously observed\n"} {
		if got := string(gitCommand(t, f.env, f.root, nil, "show", last.Commit+":"+path)); got != want {
			t.Fatalf("%s: %q", path, got)
		}
	}
}

func TestScratchPathsetBoundIsExplicit(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	// All paths reference one empty blob. Build the reference index in one
	// batch rather than one Git process or one history per path.
	blob := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "hash-object", "-w", "--stdin")))
	indexEnv := append(slices.Clone(f.env), "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"))
	gitCommand(t, indexEnv, f.root, nil, "read-tree", "--empty")
	var entries strings.Builder
	for i := range 6000 {
		fmt.Fprintf(&entries, "100644 %s\tpaths/%04d-%s\x00", blob, i, strings.Repeat("x", 80))
	}
	gitCommand(t, indexEnv, f.root, []byte(entries.String()), "update-index", "-z", "--index-info")
	tree := strings.TrimSpace(string(gitCommand(t, indexEnv, f.root, nil, "write-tree")))
	base := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-m", "pathset", tree)))
	gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", base)
	task := mustScratch(t, f, uuid.NewV7())
	if err := os.RemoveAll(filepath.Join(task.Dir, "paths")); err != nil {
		t.Fatal(err)
	}
	writer := mustWriter(t, f, testWriteBinding(task))
	before := f.store.Durable()
	_, err := writer.CommitObserved(ctx)
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.Reason != "pathset_cap" {
		t.Fatalf("large full pathset was not explicitly refused: %v", err)
	}
	if f.store.Durable() != before {
		t.Fatal("pathset refusal wrote records")
	}
	branch, err := f.git.readRef(ctx, f.colony, f.p.CommonDir, task.Branch, 40)
	refs, refsErr := f.svc.listRefs(ctx, f.svc.workspaces[f.w.ID], "refs/dwarf-harness/scratch-commits/")
	if err != nil || refsErr != nil || branch != base || len(refs) != 0 {
		t.Fatalf("pathset refusal moved a private ref: %s %v %v %v", branch, refs, err, refsErr)
	}
	release, err := writer.Lease(ctx)
	if err != nil {
		t.Fatalf("pathset refusal did not thaw the writer: %v", err)
	}
	release()
	writer.Close()

	// Payload alone fits, but hash framing plus the full envelope does not.
	p := ledger.ScratchChanged{ObservationID: uuid.NewV7(), WorkspaceID: f.w.ID, ScratchID: task.ID, ContextID: task.ContextID, PreviousCommit: base, PreviousTree: tree, ScratchCommit: strings.Repeat("1", 40), ScratchTree: strings.Repeat("2", 40), InputsRevision: 1, CaptureInputsSHA256: ledger.Digest{1}, ScratchRules: "scratch-rules/1", ManifestSHA256: ledger.Digest{2}, PinID: uuid.NewV7(), WrittenPaths: []string{"x"}, ChangedPaths: []string{"x"}}
	payload, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	p.WrittenPaths[0] += strings.Repeat("x", ledger.MaxRecordBytes-len(payload)-100)
	drafts, err := observationDrafts(p, uuid.UUID{})
	if err != nil || len(drafts[0].Payload) >= ledger.MaxRecordBytes {
		t.Fatalf("boundary fixture is not payload-below-cap: %v", err)
	}
	if err := preflightObservation(f.store.Colony(), p.ObservationID, drafts); !errors.As(err, &refusal) || refusal.Reason != "pathset_cap" {
		t.Fatalf("preflight measured only payload bytes: %v", err)
	}
}

func TestWriterRepairCancellationKeepsLease(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	writer := mustWriter(t, f, testWriteBinding(scratch))
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	binding := testWriteBinding(scratch)
	binding.ContextID = uuid.NewV7()
	if _, err := f.svc.BeginWrite(ctx, binding); !errors.Is(err, context.Canceled) {
		t.Fatalf("foreign cancelled writer: %v", err)
	}
	// Exercise cancellation inside repair, not merely at BeginWrite's entry.
	f.svc.mu.Lock()
	w, current := f.svc.workspaces[f.w.ID], f.svc.scratches[scratch.ID]
	f.svc.mu.Unlock()
	if err := f.svc.repairScratchTip(ctx, w, current); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled repair: %v", err)
	}
	release, err := writer.Lease(context.Background())
	if err != nil {
		t.Fatalf("foreign cancellation poisoned task lease: %v", err)
	}
	release()
	// A real refusal preserves its cause rather than inventing tip mismatch.
	f.svc.git.unavailable = ErrGitUnavailable
	err = f.svc.repairScratchTip(context.Background(), w, current)
	if err == nil {
		t.Fatal("unavailable Git repaired a scratch")
	}
	_, leaseErr := writer.Lease(context.Background())
	if !errors.Is(leaseErr, err) {
		t.Fatalf("lease lost refusing cause: repair=%v lease=%v", err, leaseErr)
	}
}

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

func integratedWrite(workspaceID, target uuid.UUID, path, previous, text string) string {
	expected := "absent"
	if previous != "" {
		expected = ledger.Digest(sha256.Sum256([]byte(previous))).String()
	}
	args := map[string]any{"schema": 1, "workspace_id": workspaceID, "path": path, "expected_sha256": expected, "text": text}
	if target != (uuid.UUID{}) {
		args["task_context_id"] = target
	}
	return string(modelJSON(args))
}

func integratedWaitPhase(t *testing.T, f *turnFixture, turn uuid.UUID, reason string) {
	t.Helper()
	var cursor uint64
	for {
		page, err := f.view.Since(context.Background(), cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		owner, err := f.service.currentContinuation(context.Background(), f.origin, turn)
		if err != nil {
			t.Fatal(err)
		}
		if owner.Snapshot.State == "waiting_resource" && owner.Snapshot.WaitReason == reason {
			continuationWait(t, f, turn, reason)
			return
		}
		if owner.Snapshot.State == "terminal" {
			t.Fatal("turn ended before the requested wait", reason)
		}
		cursor = page.ThroughSequence
		<-page.Wake
	}
}

func testIntegratedMergeOutcome(t *testing.T, outcome string, passive bool) {
	t.Helper()
	f := newPassiveFixture(t)
	// A newline-free committed seed is stable under the inspected host's
	// checkout normalization, so the source CAS names the actual fork bytes.
	{
		writer, err := f.workspace.BeginWrite(context.Background(), workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: f.parent.ContextID, AgentID: f.origin.AgentID, TaskAgentID: f.origin.AgentID, TaskScratchID: f.parent.ID})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(f.parent.Dir, "safe", "seed.txt"), []byte("parent seed"))
		_, err = writer.CommitObserved(context.Background())
		writer.Close()
		if err != nil {
			t.Fatal(err)
		}
		f.parent, err = f.workspace.Scratch(context.Background(), f.parent.ContextID, f.registered.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	sibling := f.workSetting(t, 83500, "Sibling writer")
	siblingScratch := f.finishBusyFiles(t, f.registered, sibling.Binding)
	capacity := paidPhysicalCapacity()
	capacity.MaxOperationBytes = 4 << 20
	_, err := f.service.IssueGrant(context.Background(), bindingOf(f.origin), GrantIssue{CommandID: uuid.NewV7(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: GrantSpec{GrantID: uuid.NewV7(), Grantee: sibling.Binding, Operations: []string{"file.write", "file.edit"}, Resources: []ledger.Resource{{Kind: "workspace", Binding: ledger.Binding(f.parentSetting.Binding), WorkspaceID: f.registered.ID}}, DataClass: "workspace", Recipients: []Binding{sibling.Binding}, Limits: capacity, Lifetime: "until_revoked"}})
	if err != nil {
		t.Fatal(err)
	}
	canonical := func() []byte {
		raw, err := exec.CommandContext(t.Context(), "git", "--git-dir", f.registered.CommonDir, "rev-parse", "refs/heads/main").Output()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	canonicalBefore := canonical()
	parentHeld, parentRelease := make(chan struct{}), make(chan struct{})
	var once sync.Once
	freeParent := func() { once.Do(func() { close(parentRelease) }) }
	defer freeParent()
	commands := []session.Command{turnMessage(f.origin.ContextID, "merge-source-a"), turnMessage(sibling.Binding.ContextID, "merge-source-c")}
	ids := []uuid.UUID{commands[0].Arguments.(session.MessageSend).TurnID, commands[1].Arguments.(session.MessageSend).TurnID}
	var held [2]atomic.Bool
	held[0].Store(true)
	held[1].Store(true)
	f.service.deps.beforeFinishCommit = func(turn uuid.UUID) error {
		for i, id := range ids {
			if id == turn && held[i].Load() {
				return &workspace.BusyError{Reason: "capture_budget"}
			}
		}
		return nil
	}
	defer func() {
		held[0].Store(false)
		held[1].Store(false)
		if f.service.loop != nil {
			f.service.loop.released()
		}
	}()
	paths := []string{"safe/from-a.txt", "safe/from-c.txt"}
	texts := []string{"source-a-conflict-data", "source-c-conflict-data"}
	prior := ""
	parentPath, parentBefore, parentText := "safe/parent-change.txt", "", "parent independent change"
	if outcome == "fast_forward" {
		parentPath, parentBefore, parentText = "safe/seed.txt", "parent seed", "parent seed"
	}
	if outcome == "conflict" {
		paths = []string{"safe/seed.txt", "safe/seed.txt"}
		prior = "parent seed"
		parentPath, parentBefore, parentText = "safe/seed.txt", "parent seed", "parent wins"
	}
	var posts [3]atomic.Int32
	naturalResponder := f.respond
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		model, raw := turnRequest(t, r)
		if bytes.Contains(raw, []byte("passive-natural")) {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			naturalResponder(w, r)
			return
		}
		index := -1
		for i, label := range []string{"merge-source-a", "merge-source-c", "merge-parent"} {
			if bytes.Contains(raw, []byte(label)) {
				index = i
				break
			}
		}
		if index < 0 {
			t.Error("unrecognized integration request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if posts[index].Add(1) == 1 {
			args := integratedWrite(f.registered.ID, uuid.UUID{}, parentPath, parentBefore, parentText)
			if index != 2 {
				args = integratedWrite(f.registered.ID, f.parent.ContextID, paths[index], prior, texts[index])
			}
			name := "file_write_v1"
			if index == 2 && outcome == "fast_forward" {
				name, args = "core_status_v1", `{"schema":1,"subject":"context"}`
			}
			_, _ = io.WriteString(w, turnAnthropic(model, turnCalls(name, args), ""))
			return
		}
		if index == 2 {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(parentHeld)
			select {
			case <-parentRelease:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "Sibling merge complete."))
	}
	f.enable(t, f.parentSetting, sibling)
	views := []session.Session{f.view}
	for _, binding := range []Binding{sibling.Binding, f.parentSetting.Binding} {
		view, err := f.service.OpenSession(context.Background(), binding)
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		views = append(views, view)
	}
	sourceViews := make([]*turnFixture, len(commands))
	for i, command := range commands {
		copy, paid := *f.turnFixture, *f.paidFixture
		paid.origin.ContextID = command.ContextID
		copy.paidFixture, copy.view = &paid, views[i]
		sourceViews[i] = &copy
	}
	parentStart, err := views[2].Do(context.Background(), turnMessage(f.parent.ContextID, "merge-parent"))
	if err != nil {
		t.Fatal(err)
	}
	<-parentHeld
	for i, command := range commands {
		if _, err := views[i].Do(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		continuationWait(t, sourceViews[i], ids[i], "workspace_capture")
	}
	created := []uuid.UUID{}
	for i, id := range ids {
		source := *f.continuationCaptureFixture
		source.turnFixture = sourceViews[i]
		records := foreignRecords(t, &source, id)
		if len(records.created) != 1 || len(records.observed) != 0 || len(records.prepared) != 0 || len(records.merged) != 0 || records.created[0].ContextID != commands[i].ContextID || records.created[0].ParentScratchID != f.parent.ID {
			t.Fatal("real sibling turn did not hold an independent unobserved temporary")
		}
		created = append(created, records.created[0].ScratchID)
		actual := readTestFile(t, filepath.Join(f.root, "scratch", records.created[0].Dir, filepath.FromSlash(paths[i])))
		if string(actual) != texts[i] {
			t.Fatalf("temporary bytes = %q, want %q; expected prior = %q", actual, texts[i], prior)
		}
		if outcome != "conflict" {
			for _, scratch := range []workspace.Scratch{f.parent, f.scratch, siblingScratch} {
				if _, err := os.Stat(filepath.Join(scratch.Dir, filepath.FromSlash(paths[i]))); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("temporary write leaked before parent release", err)
				}
			}
		}
	}
	if created[0] == created[1] || !bytes.Equal(canonical(), canonicalBefore) {
		t.Fatal("sibling writers shared a temporary or moved canonical work")
	}
	if outcome == "fast_forward" {
		freeParent()
	}
	for i := range ids {
		held[i].Store(false)
		f.service.loop.released()
		if outcome == "fast_forward" {
			f.finishedFor(t, views[i], ids[i])
		} else {
			integratedWaitPhase(t, sourceViews[i], ids[i], "workspace_merge_back")
		}
	}
	var cancelCommand uuid.UUID
	if outcome == "cancel_while_parent_held" {
		cancelCommand = uuid.NewV7()
		_, err := views[0].Do(context.Background(), session.Command{Schema: 1, CommandID: cancelCommand, ContextID: f.origin.ContextID, Operation: "turn.cancel", Arguments: session.TurnCancel{TurnID: ids[0]}})
		if err != nil {
			t.Fatal(err)
		}
	}
	freeParent()
	parentResult, _ := f.finishedFor(t, views[2], parentStart.TurnID)
	if parentResult.Outcome != "finished" || parentResult.ToolDispatches != 1 {
		t.Fatal(parentResult)
	}
	outcomes := map[string]int{}
	for i, id := range ids {
		result, events := f.finishedFor(t, views[i], id)
		want := "finished"
		if i == 0 && outcome == "cancel_while_parent_held" {
			want = "cancelled"
		}
		if result.Outcome != want || result.ToolDispatches != 1 || result.ModelCalls != 2 {
			t.Fatal(result)
		}
		source := *f.continuationCaptureFixture
		source.turnFixture = sourceViews[i]
		tools := source.toolResults(t, events)
		if len(tools) != 1 || tools[0].Error != nil {
			t.Fatalf("source write did not apply: %s", modelJSON(tools))
		}
		records := foreignRecords(t, &source, id)
		if len(records.observed) != 1 || len(records.merged) != 1 {
			t.Fatal("merge repeated or lost source observation/result")
		}
		merged := records.merged[0]
		if merged.AuthoringContextID != commands[i].ContextID || merged.ScratchID != created[i] || merged.ParentScratchID != f.parent.ID || !slices.Equal(merged.WrittenPaths, []string{paths[i]}) {
			t.Fatalf("merge lost author or exact write set: %+v", merged)
		}
		outcomes[merged.Outcome]++
		if merged.SourceObservationID != records.observed[0].ObservationID || merged.SourceTree != records.observed[0].ScratchTree || merged.SourceCommit != records.observed[0].ScratchCommit {
			t.Fatal("merge did not use its one acknowledged source tree")
		}
		sourceFiles := map[string]string{"safe/seed.txt": "parent seed"}
		sourceFiles[paths[i]] = texts[i]
		assertIntegratedTree(t, f.parent.Dir, merged.SourceTree, sourceFiles)
		if outcome == "conflict" {
			if merged.Outcome != ledger.MergeBackConflict || merged.ConflictSHA256 == (ledger.Digest{}) || len(merged.ChangedPaths) != 0 || len(records.prepared) != 0 || len(records.closed) != 0 {
				t.Fatal("conflict was applied, closed or lost its report")
			}
		} else {
			// F2 reports the parent's complete delta from its dedicated anchor,
			// not merely this source's write set.
			wantPaths := []string{"safe/seed.txt"}
			if parentPath != "safe/seed.txt" {
				wantPaths = append(wantPaths, parentPath)
			}
			for j, sourceID := range ids {
				other := *f.continuationCaptureFixture
				other.turnFixture = sourceViews[j]
				applied := foreignRecords(t, &other, sourceID)
				for _, sequence := range applied.mergeSequences {
					if sequence <= records.mergeSequences[0] {
						wantPaths = append(wantPaths, paths[j])
					}
				}
			}
			slices.Sort(wantPaths)
			if len(records.prepared) != 1 || len(records.closed) != 1 || !slices.Equal(merged.ChangedPaths, wantPaths) {
				t.Fatalf("clean merge did not apply and close exactly once: paths=%v want=%v", merged.ChangedPaths, wantPaths)
			}
		}
		if i == 0 && outcome == "cancel_while_parent_held" {
			assertIntegratedCancelledMerge(t, &source, id, cancelCommand)
		} else if outcome != "conflict" {
			assertContinuationMerge(t, &source, id, merged.Outcome == ledger.MergeBackMerged)
		}
	}
	if outcome == "conflict" {
		// finishedFor joined owner closure for the parent and both sources.
		// No turn can append while these explicit resumes check the whole tail.
		for i := range ids {
			before := f.store.Durable()
			resumed, err := f.workspace.BeginWrite(context.Background(), workspace.WriteBinding{WriterID: uuid.NewV7(), ContextID: commands[i].ContextID, AgentID: f.origin.AgentID, TaskAgentID: f.origin.AgentID, TaskScratchID: f.parent.ID, ResumeScratchID: created[i]})
			if err != nil {
				t.Fatal("conflicted source is not resumable", err)
			}
			defer resumed.Close()
			retained := readTestFile(t, filepath.Join(resumed.Scratch().Dir, filepath.FromSlash(paths[i])))
			resumed.Close()
			if string(retained) != texts[i] {
				t.Fatalf("resuming conflict changed source bytes: got %q, want %q", retained, texts[i])
			}
			if after := f.store.Durable(); after != before {
				t.Fatalf("quiescent conflict resume appended work: before=%d after=%d", before, after)
			}
		}
	}
	if outcome == "fast_forward" && (outcomes[ledger.MergeBackFastForward] != 1 || outcomes[ledger.MergeBackMerged] != 1) || (outcome == "clean_three_way" || outcome == "cancel_while_parent_held") && outcomes[ledger.MergeBackMerged] != 2 || outcome == "conflict" && outcomes[ledger.MergeBackConflict] != 2 {
		t.Fatal("wrong integrated merge outcomes", outcomes)
	}
	if got := string(readTestFile(t, filepath.Join(f.parent.Dir, filepath.FromSlash(parentPath)))); got != parentText {
		t.Fatal("merge overwrote the parent's own observation", got)
	}
	if outcome != "conflict" {
		for i, path := range paths {
			if got := string(readTestFile(t, filepath.Join(f.parent.Dir, filepath.FromSlash(path)))); got != texts[i] {
				t.Fatal("clean target lost sibling bytes", got)
			}
		}
	}
	if !bytes.Equal(canonical(), canonicalBefore) {
		t.Fatal("temporary merge promoted canonical work")
	}
	parentFiles := map[string]string{"safe/seed.txt": "parent seed"}
	parentFiles[parentPath] = parentText
	if outcome != "conflict" {
		for i, path := range paths {
			parentFiles[path] = texts[i]
		}
	}
	parentNow, err := f.workspace.Scratch(context.Background(), f.parent.ContextID, f.registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertIntegratedTree(t, f.parent.Dir, parentNow.TipTree, parentFiles)
	actualTree, err := exec.CommandContext(t.Context(), "git", "-C", f.parent.Dir, "rev-parse", "HEAD^{tree}").Output()
	if err != nil || strings.TrimSpace(string(actualTree)) != parentNow.TipTree {
		t.Fatal("parent ref does not name its acknowledged result tree", err)
	}
	beforePromotion := f.store.Durable()
	promotion, err := views[2].Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: f.parent.ContextID, Operation: "promotion.request", Arguments: session.PromotionRequest{WorkspaceID: f.registered.ID}})
	var unavailable *workspace.UnavailableError
	if !errors.As(err, &unavailable) || unavailable.Reason != "no_validator" || promotion.Outcome != "refused" || f.store.Durable() != beforePromotion || !bytes.Equal(canonical(), canonicalBefore) {
		t.Fatal("temporary result was accepted for promotion", err)
	}
	notices, err := f.workspace.ScratchNotices(context.Background(), f.parent.ContextID, f.parent.ID, 0)
	if err != nil || len(notices) != 2 {
		t.Fatal("parent lost its durable sibling notices", err)
	}
	if passive {
		_, result, input := f.natural(t, f.parentSetting.Binding)
		request := <-f.requests
		if result.Outcome != "finished" || input.Schema != 2 || len(input.TaskResults) != 2 || len(input.NoticeCursors) != 1 {
			t.Fatal("natural parent turn did not select both conflict references")
		}
		for i, notice := range notices {
			if !sameScopedValue(input.TaskResults[i], loopNoticeResult(notice)) || !strings.Contains(request, notice.ConflictSHA256.String()) || strings.Contains(request, texts[i]) {
				t.Fatal("passive conflict reference was changed, loaded or lost")
			}
		}
		if input.NoticeCursors[0].AfterSequence != int64(notices[1].Sequence) {
			t.Fatal("conflict cursor skipped or repeated a selected result")
		}
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.workspace.Close(); err != nil {
		t.Fatal(err)
	}
	throughClose := f.store.Durable()
	_, late := f.workspace.MergeBack(context.Background(), created[0])
	if !errors.Is(late, workspace.ErrClosed) || f.store.Durable() != throughClose || !bytes.Equal(canonical(), canonicalBefore) {
		t.Fatal("late workspace admission changed durable work", late)
	}
	if got := string(readTestFile(t, filepath.Join(f.parent.Dir, filepath.FromSlash(parentPath)))); got != parentText {
		t.Fatal("late refused call changed parent files", got)
	}
	reopened, report, err := Open(context.Background(), f.turnFixture.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(report.Issues) != 0 || len(report.PendingLoops) != 0 {
		t.Fatal("shutdown left interrupted bookkeeping", report)
	}
	if f.store.Durable() != throughClose {
		t.Fatal("Open reran a completed merge")
	}
}

func assertIntegratedCancelledMerge(t *testing.T, f *continuationCaptureFixture, turn, command uuid.UUID) {
	t.Helper()
	records := foreignRecords(t, f, turn)
	owners, observations, endings := continuationCaptureRecords(t, f, turn)
	var park, stop, resume uint64
	var parkGeneration, stopGeneration int64
	var nativeCancel uuid.UUID
	for _, owner := range owners {
		a := owner.Submission.Arguments
		switch {
		case a.State == "waiting_resource" && a.WaitReason == "workspace_merge_back":
			if park != 0 {
				t.Fatal("cancelled merge parked twice")
			}
			park, parkGeneration = uint64(owner.EffectSequence), a.Generation
		case owner.Submission.Operation == "owner.stop":
			if stop != 0 || owner.CommandID != command || a.OperationID != command || owner.Submission.ExpectedState != "waiting_resource" || a.State != "stopping" || a.StopReason != "cancelled" || !validID(a.CancelCommandID) || a.CancelCommandID == command {
				t.Fatalf("cancelled merge lost its exact stop transition: %+v", owner)
			}
			stop, stopGeneration = uint64(owner.EffectSequence), a.Generation
			nativeCancel = a.CancelCommandID
		case owner.Submission.ExpectedState == "stopping" && a.State == "finishing":
			if resume != 0 || a.StopReason != "cancelled" || a.CancelCommandID != nativeCancel {
				t.Fatal("cancelled merge lost or repeated its newer-stop resume")
			}
			resume = uint64(owner.EffectSequence)
		}
	}
	if len(records.prepared) != 1 || len(records.merged) != 1 || len(records.closed) != 1 || len(observations) != 1 || len(endings) != 1 {
		t.Fatalf("cancelled merge repeated or lost an effect: %+v observations=%v endings=%v", records, observations, endings)
	}
	if observations[0] >= park || park == 0 || park >= stop || stop >= records.preparationSequences[0] || records.preparationSequences[0] >= records.mergeSequences[0] || records.mergeSequences[0] >= resume || resume >= endings[0] || stopGeneration <= parkGeneration {
		t.Fatalf("cancelled merge order: observation=%d park=%d/%d stop=%d/%d prepared=%d applied=%d resume=%d end=%d", observations[0], park, parkGeneration, stop, stopGeneration, records.preparationSequences[0], records.mergeSequences[0], resume, endings[0])
	}
	if records.merged[0].Outcome != ledger.MergeBackMerged || records.prepared[0].MergeBackID != records.merged[0].MergeBackID || records.merged[0].ForkCommit == records.merged[0].ParentCommit {
		t.Fatal("cancelled merge applied the wrong prepared identity or parent base")
	}
	cancelCount := 0
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(commit ledger.Commit) error {
		for _, record := range commit.Records {
			if record.Type != ledger.TypeRuntimeScopedReceipt || record.Seq <= resume || record.Seq >= endings[0] {
				continue
			}
			receipt := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, record.Payload)
			if receipt.Submission.Operation == "turn.cancel" && receipt.WorkID == turn {
				cancelCount++
				if receipt.CommandID != nativeCancel {
					t.Fatal("native cancellation lost the stop's separate command identity")
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if cancelCount != 1 {
		t.Fatal("cancelled merge lost or repeated its native ending")
	}
}

func assertIntegratedTree(t *testing.T, repository, tree string, files map[string]string) {
	t.Helper()
	names, err := exec.CommandContext(t.Context(), "git", "-C", repository, "ls-tree", "-r", "-z", "--name-only", tree).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(files))
	for path := range files {
		want = append(want, path)
	}
	slices.Sort(want)
	if string(names) != strings.Join(want, "\x00")+"\x00" {
		t.Fatalf("tree paths = %q, want %v", names, want)
	}
	for _, path := range want {
		content, err := exec.CommandContext(t.Context(), "git", "-C", repository, "show", tree+":"+path).Output()
		if err != nil || string(content) != files[path] {
			t.Fatalf("tree %s path %s = %q, want %q: %v", tree, path, content, files[path], err)
		}
	}
}

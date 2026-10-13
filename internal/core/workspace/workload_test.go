package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

// DWARF_CAPTURE_WORKLOAD_OUT names an external JSONL results file. Measurements
// append to it, never set a pass mark, and skip under the race detector. Defender
// remains enabled; its state is recorded by the operator, not changed here.
const captureWorkloadOutEnv = "DWARF_CAPTURE_WORKLOAD_OUT"

func captureWorkloadRaceDetector() bool {
	bi, ok := debug.ReadBuildInfo()
	return ok && slices.ContainsFunc(bi.Settings, func(s debug.BuildSetting) bool { return s.Key == "-race" && s.Value == "true" })
}

func captureWorkloadModuleRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func captureWorkloadWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

type captureWorkloadRow struct {
	Phase                string  `json:"phase"`
	Files                int     `json:"files"`
	CoreRawBytes         int64   `json:"core_raw_bytes"`
	DurationMS           float64 `json:"duration_ms"`
	Tree                 string  `json:"tree"`
	GitVersion           string  `json:"git_version"`
	GoVersion            string  `json:"go_version"`
	GOOS                 string  `json:"goos"`
	GOARCH               string  `json:"goarch"`
	ManifestBytes        int     `json:"manifest_bytes"`
	LooseObjects         int64   `json:"loose_objects"`
	LooseKiB             int64   `json:"loose_kib"`
	RetainedScratchBytes int64   `json:"retained_scratch_bytes"`
	RetainedBodyBytes    int64   `json:"retained_body_bytes"`
	Unresolved           int     `json:"unresolved"`
}

func TestCaptureWorkload(t *testing.T) {
	out := os.Getenv(captureWorkloadOutEnv)
	if out == "" {
		t.Skipf("set %s to an external results file to run the workload measurements", captureWorkloadOutEnv)
	}
	if captureWorkloadRaceDetector() {
		t.Skip("the race detector distorts every figure; measure without it")
	}
	root, ok := captureWorkloadModuleRoot()
	if !ok {
		t.Fatal("cannot establish the repository boundary")
	}
	absolute, err := filepath.Abs(out)
	if err != nil {
		t.Fatal(err)
	}
	if captureWorkloadWithin(root, absolute) || captureWorkloadWithin(root, os.TempDir()) {
		t.Fatal("workload results and temporary work directories must be outside the repository")
	}
	output, err := os.OpenFile(absolute, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	for _, count := range []int{1000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newGitFixture(t, "sha1")
			if captureWorkloadWithin(root, f.root) || captureWorkloadWithin(root, f.colony) {
				t.Fatal("workload directory is inside the repository")
			}
			if err := os.Remove(filepath.Join(f.root, "base.txt")); err != nil {
				t.Fatal(err)
			}
			for i := range count {
				b := bytes.Repeat([]byte("x"), 800)
				copy(b, fmt.Sprintf("%08d", i))
				b[len(b)-1] = '\n'
				writeTestFile(t, filepath.Join(f.root, "src", fmt.Sprintf("%05d.txt", i)), b)
			}
			gitCommand(t, f.env, f.root, nil, "add", "-A")
			gitCommand(t, f.env, f.root, nil, "commit", "-m", "workload baseline")
			gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", "HEAD")
			p, err := f.git.inspect(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			p.Normalization.AutoCRLF = "true"
			store, err := ledgerstore.Create(f.colony, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := f.git.ensureMarker(context.Background(), f.colony, p, store.Colony().String()); err != nil {
				t.Fatal(err)
			}
			baseline := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "--verify", "--end-of-options", "main^{tree}")))
			inputs := filepath.Join(f.colony, "workspaces", "inputs", "1")
			dir := filepath.Join(f.colony, "scratch", fmt.Sprintf("%08x", count))
			footprint := func(root string) int64 {
				t.Helper()
				var total int64
				err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
					if os.IsNotExist(err) && path == root {
						return nil
					}
					if err != nil {
						return err
					}
					if entry.IsDir() && path == filepath.Join(root, ".tmp") {
						return filepath.SkipDir
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if info.Mode().IsRegular() {
						total += info.Size()
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return total
			}
			var workloadService *Service
			writeRow := func(phase string, duration time.Duration, result captureResult) {
				row := captureWorkloadRow{Phase: phase, Files: count, CoreRawBytes: result.RawBytes, DurationMS: float64(duration) / float64(time.Millisecond), Tree: result.Tree, GitVersion: f.git.version, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
				row.ManifestBytes = len(result.Manifest)
				row.RetainedScratchBytes = footprint(filepath.Join(f.colony, "scratch"))
				row.RetainedBodyBytes = footprint(filepath.Join(f.colony, "bodies"))
				if workloadService != nil {
					for _, attempt := range workloadService.mergeBacks {
						if attempt.pending() {
							row.Unresolved++
						}
					}
				}
				for _, line := range strings.Split(string(gitCommand(t, f.env, f.root, nil, "count-objects", "-v")), "\n") {
					key, value, ok := strings.Cut(line, ": ")
					if !ok || (key != "count" && key != "size") {
						continue
					}
					n, err := strconv.ParseInt(value, 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					if key == "count" {
						row.LooseObjects = n
					} else {
						row.LooseKiB = n
					}
				}
				b, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := output.Write(append(b, '\n')); err != nil {
					t.Fatal(err)
				}
				if err := output.Sync(); err != nil {
					t.Fatal(err)
				}
				t.Log(string(b))
			}
			base := fixtureMain(t, f)
			start := time.Now()
			worktree, err := f.git.createWorktree(context.Background(), f.colony, p, inputs, dir, uuid.NewV7().String(), base)
			duration := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if worktree.AnchorTree != baseline {
				t.Fatal("creation changed the baseline")
			}
			writeRow("creation", duration, captureResult{Tree: worktree.AnchorTree})
			profile := ProvisionalCapacityProfile()
			fixture := &captureFixture{f: f, p: p, s: captureScratch{Worktree: worktree, Inputs: p, Revision: 1, InputsDir: inputs, TmpDir: filepath.Join(f.colony, "workspaces", "00112233", "tmp"), ColonyID: store.Colony().String()}, engine: newCaptureEngine(f.git, f.colony), pool: testReservationPool(t, &profile)}
			for _, phase := range []string{"fresh_observe", "warm_observe"} {
				start = time.Now()
				got := mustCapture(t, fixture, observeCapture)
				duration = time.Since(start)
				if got.Tree != baseline || got.Files != count || got.RetentionRef != "" {
					t.Fatalf("%s changed the baseline or retained it", phase)
				}
				writeRow(phase, duration, got)
			}
			for i := range 10 {
				path := filepath.Join(dir, "src", fmt.Sprintf("%05d.txt", i))
				b := readTestFile(t, path)
				b[0] = 'Z'
				writeTestFile(t, path, b)
			}
			start = time.Now()
			sealed := mustCapture(t, fixture, sealCapture)
			duration = time.Since(start)
			want, _ := referenceTree(t, fixture)
			if sealed.Tree != want || sealed.Files != count {
				t.Fatal("seal differs from reference Git")
			}
			writeRow("seal_10_edits", duration, sealed)
			// This measures closure's observation only; S3 owns representability and
			// the durable close. The changed scratch is intentionally not closed here.
			start = time.Now()
			observed := mustCapture(t, fixture, observeCapture)
			duration = time.Since(start)
			if observed.Tree != sealed.Tree || observed.RetentionRef != "" {
				t.Fatal("closure observation changed or retained the candidate")
			}
			writeRow("closure_observe", duration, observed)
			// Service phases include manifest publication and durable records;
			// the preceding mechanism phases remain in the loose-object counts.
			gitCommand(t, f.env, f.root, nil, "config", "core.autocrlf", "true")
			p, err = f.git.inspect(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			svc, _, err := openService(context.Background(), store, f.colony, Config{Capacity: &profile}, serviceDeps{environ: func() []string { return f.env }})
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			workloadService = svc
			w, err := svc.Register(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			sf := &serviceFixture{gitFixture: f, store: store, svc: svc, p: p, w: w}
			scratch := mustScratch(t, sf, uuid.NewV7())
			request := rootRequest(scratch)
			start = time.Now()
			rootState := mustAdmit(t, sf, request)
			duration = time.Since(start)
			manifestResult := func(tree string, digest ledger.Digest) captureResult {
				body, err := store.ReadBody(context.Background(), digest)
				if err != nil {
					t.Fatal(err)
				}
				result := captureResult{Tree: tree, Manifest: body}
				for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'})[1:] {
					var entry manifestEntry
					if err := json.Unmarshal(line, &entry); err != nil {
						t.Fatal(err)
					}
					result.RawBytes += entry.Size
				}
				return result
			}
			admissionResult := func(state RequestState) captureResult {
				a := admittedRecord(t, sf, state.RequestID)
				return manifestResult(state.SealedTree, a.ManifestSHA256)
			}
			if rootState.SealedTree != baseline {
				t.Fatal("root admission differs from reference baseline")
			}
			writeRow("admit_root", duration, admissionResult(rootState))
			for i := range 10 {
				path := filepath.Join(scratch.Dir, "src", fmt.Sprintf("%05d.txt", i))
				b := readTestFile(t, path)
				b[0] = 'Y'
				writeTestFile(t, path, b)
			}
			request.Head, request.RequestID = request.RequestID, uuid.NewV7()
			start = time.Now()
			successor := mustAdmit(t, sf, request)
			duration = time.Since(start)
			refFixture := &captureFixture{f: f, p: p, s: svc.captureScratch(svc.workspaces[w.ID], scratch)}
			want, _ = referenceTree(t, refFixture)
			if successor.SealedTree != want {
				t.Fatal("successor admission differs from reference Git")
			}
			writeRow("admit_successor", duration, admissionResult(successor))
			for _, phase := range []string{"integrate_two", "rebuild_two"} {
				if phase == "rebuild_two" {
					advanceMain(t, sf, nil)
				}
				start = time.Now()
				mustIntegrate(t, sf)
				duration = time.Since(start)
				gs := generations(t, sf, successor.RequestID)
				g := gs[len(gs)-1]
				if g.DerivedTree != want {
					t.Fatal("integrated workload differs from reference Git")
				}
				writeRow(phase, duration, captureResult{Tree: g.DerivedTree})
			}
			observe := func(phase string, writer *Writer) Observation {
				t.Helper()
				actual := writer.Scratch()
				admin, err := scratchAdmin(actual.worktree(), p.CommonDir)
				if err != nil {
					t.Fatal(err)
				}
				indexBefore := readTestFile(t, filepath.Join(admin, "index"))
				mainBefore := fixtureMain(t, f)
				start := time.Now()
				observation, err := writer.CommitObserved(context.Background())
				duration := time.Since(start)
				writer.Close()
				if err != nil {
					t.Fatal(err)
				}
				reference := &captureFixture{f: f, p: p, s: svc.captureScratch(svc.workspaces[w.ID], actual)}
				want, _ := referenceTree(t, reference)
				branch, err := f.git.readRef(context.Background(), f.colony, p.CommonDir, actual.Branch, 40)
				if err != nil || observation.Tree != want || branch != observation.Commit || fixtureMain(t, f) != mainBefore || !bytes.Equal(indexBefore, readTestFile(t, filepath.Join(admin, "index"))) {
					t.Fatalf("%s changed files/index/main or disagreed with reference Git: %v", phase, err)
				}
				commits := serviceCommits(t, store)
				last := commits[len(commits)-1]
				record, err := ledger.DecodeScratchChanged(last.Records[0].Payload)
				if err != nil || record.ObservationID != observation.ID || record.ScratchCommit != observation.Commit || record.ScratchTree != want || record.ContextID != actual.ContextID || observation.Sequence != last.First {
					t.Fatalf("%s durable observation differs: %+v %v", phase, record, err)
				}
				writeRow(phase, duration, manifestResult(want, record.ManifestSHA256))
				return observation
			}
			taskWriter := mustWriter(t, sf, testWriteBinding(scratch))
			defer taskWriter.Close()
			if count == 10000 {
				const samples = 32
				start := time.Now()
				for range samples {
					release, err := taskWriter.Lease(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					release()
				}
				writeRow("lease", time.Since(start)/samples, captureResult{Tree: scratch.TipTree})
			}
			taskObservation := observe("observe_task", taskWriter)
			binding := testWriteBinding(scratch)
			binding.ContextID = uuid.NewV7()
			start = time.Now()
			temporaryWriter, err := svc.BeginWrite(context.Background(), binding)
			duration = time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			defer temporaryWriter.Close()
			temporary := temporaryWriter.Scratch()
			taskFiles, temporaryFiles := snapshotScratch(t, scratch.Dir), snapshotScratch(t, temporary.Dir)
			delete(taskFiles, ".git")
			delete(temporaryFiles, ".git")
			if temporary.BaseCommit != taskObservation.Commit || len(taskFiles) != len(temporaryFiles) {
				t.Fatal("temporary creation did not fork the committed task")
			}
			for path, want := range taskFiles {
				if !bytes.Equal(temporaryFiles[path], want) {
					t.Fatalf("temporary fork differs at %s", path)
				}
			}
			writeRow("temporary_first_write", duration, captureResult{Tree: temporary.AnchorTree})
			for i := range 10 {
				path := filepath.Join(temporary.Dir, "src", fmt.Sprintf("%05d.txt", i))
				b := readTestFile(t, path)
				b[0] = 'T'
				writeTestFile(t, path, b)
			}
			temporaryObservation := observe("observe_temporary", temporaryWriter)
			merge := func(phase string, source Scratch, expectedTree string, expectedOutcome string) {
				t.Helper()
				mainBefore := fixtureMain(t, f)
				start := time.Now()
				result, err := svc.MergeBack(context.Background(), source.ID)
				duration := time.Since(start)
				if err != nil || result.Outcome != expectedOutcome || result.TargetTree != expectedTree {
					t.Fatalf("%s: %+v %v", phase, result, err)
				}
				indexTree := strings.TrimSpace(string(gitCommand(t, f.env, scratch.Dir, nil, "write-tree")))
				branch := strings.TrimSpace(string(gitCommand(t, f.env, scratch.Dir, nil, "rev-parse", "HEAD")))
				reference := &captureFixture{f: f, p: p, s: svc.captureScratch(svc.workspaces[w.ID], scratch)}
				actual, _ := referenceTree(t, reference)
				if indexTree != expectedTree || actual != expectedTree || branch != result.TargetCommit || fixtureMain(t, f) != mainBefore {
					t.Fatalf("%s files/index/refs disagree with independent Git", phase)
				}
				prepared := mergeBackRecord(t, sf, result.ID, ledger.MergeBackPrepared)
				applied := mergeBackRecord(t, sf, result.ID, expectedOutcome)
				if prepared.TargetCommit != result.TargetCommit || applied.TargetTree != expectedTree || applied.SourceObservationID != source.ObservationID {
					t.Fatalf("%s records differ from materialized result", phase)
				}
				writeRow(phase, duration, manifestResult(expectedTree, prepared.ManifestSHA256))
			}
			temporary.ObservationID = temporaryObservation.ID
			merge("merge_back_ff", temporary, temporaryObservation.Tree, ledger.MergeBackFastForward)
			binding.WriterID, binding.ResumeScratchID = uuid.NewV7(), uuid.UUID{}
			temporaryWriter = mustWriter(t, sf, binding)
			temporary = temporaryWriter.Scratch()
			for i := range 10 {
				path := filepath.Join(temporary.Dir, "src", fmt.Sprintf("%05d.txt", i))
				b := readTestFile(t, path)
				b[0] = 'E'
				writeTestFile(t, path, b)
			}
			temporaryObservation = mustObserve(t, temporaryWriter)
			temporary.ObservationID = temporaryObservation.ID
			taskWriter = mustWriter(t, sf, testWriteBinding(scratch))
			for i := 10; i < 20; i++ {
				path := filepath.Join(scratch.Dir, "src", fmt.Sprintf("%05d.txt", i))
				b := readTestFile(t, path)
				b[0] = 'P'
				writeTestFile(t, path, b)
			}
			taskObservation = mustObserve(t, taskWriter)
			mergedTree := strings.Fields(string(gitCommand(t, f.env, f.root, nil, "merge-tree", "--write-tree", "--merge-base="+temporary.BaseCommit, taskObservation.Commit, temporaryObservation.Commit)))[0]
			merge("merge_back_explicit", temporary, mergedTree, ledger.MergeBackMerged)

			// Exercise simultaneous typed turns outside every measurement timer.
			for _, conflict := range []bool{false, true} {
				taskWriter = mustWriter(t, sf, testWriteBinding(scratch))
				binding.WriterID = uuid.NewV7()
				temporaryWriter = mustWriter(t, sf, binding)
				temporary = temporaryWriter.Scratch()
				taskLease, err := taskWriter.Lease(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				sourceLease, err := temporaryWriter.Lease(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				parentName, sourceName := "parallel-parent.txt", "parallel-source.txt"
				if conflict {
					parentName, sourceName = "parallel-conflict.txt", "parallel-conflict.txt"
				}
				writeTestFile(t, filepath.Join(scratch.Dir, parentName), []byte("task\n"))
				writeTestFile(t, filepath.Join(temporary.Dir, sourceName), []byte("author\n"))
				next := rootRequest(scratch)
				next.Head = request.RequestID
				assertBusy(t, promotionError(context.Background(), svc, next), "scratch_busy")
				sourceLease()
				taskLease()
				mustObserve(t, temporaryWriter)
				mustObserve(t, taskWriter)
				result, err := svc.MergeBack(context.Background(), temporary.ID)
				wantOutcome := ledger.MergeBackMerged
				if conflict {
					wantOutcome = ledger.MergeBackConflict
				}
				if err != nil || result.Outcome != wantOutcome {
					t.Fatalf("two-writer conflict=%t: %+v %v", conflict, result, err)
				}
				if conflict {
					if _, err := svc.CloseScratch(context.Background(), Closure{ScratchID: temporary.ID, Actor: ledger.ActorAgent, ContextID: temporary.ContextID, Reason: "abandoned"}); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

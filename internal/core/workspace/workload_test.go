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
	Phase         string  `json:"phase"`
	Files         int     `json:"files"`
	CoreRawBytes  int64   `json:"core_raw_bytes"`
	DurationMS    float64 `json:"duration_ms"`
	Tree          string  `json:"tree"`
	GitVersion    string  `json:"git_version"`
	GoVersion     string  `json:"go_version"`
	GOOS          string  `json:"goos"`
	GOARCH        string  `json:"goarch"`
	ManifestBytes int     `json:"manifest_bytes"`
	LooseObjects  int64   `json:"loose_objects"`
	LooseKiB      int64   `json:"loose_kib"`
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
			writeRow := func(phase string, duration time.Duration, result captureResult) {
				row := captureWorkloadRow{Phase: phase, Files: count, CoreRawBytes: result.RawBytes, DurationMS: float64(duration) / float64(time.Millisecond), Tree: result.Tree, GitVersion: f.git.version, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
				row.ManifestBytes = len(result.Manifest)
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
			start := time.Now()
			worktree, err := f.git.createWorktree(context.Background(), f.colony, p, inputs, dir, uuid.NewV7().String())
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
			admissionResult := func(state RequestState) captureResult {
				a := admittedRecord(t, sf, state.RequestID)
				body, err := store.ReadBody(context.Background(), a.ManifestSHA256)
				if err != nil {
					t.Fatal(err)
				}
				result := captureResult{Tree: state.SealedTree, Manifest: body}
				for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'})[1:] {
					var entry manifestEntry
					if err := json.Unmarshal(line, &entry); err != nil {
						t.Fatal(err)
					}
					result.RawBytes += entry.Size
				}
				return result
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
		})
	}
}

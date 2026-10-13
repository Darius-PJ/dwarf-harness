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
	"strings"
	"testing"
	"time"
	"uuid"
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
	Phase        string  `json:"phase"`
	Files        int     `json:"files"`
	CoreRawBytes int64   `json:"core_raw_bytes"`
	DurationMS   float64 `json:"duration_ms"`
	Tree         string  `json:"tree"`
	GitVersion   string  `json:"git_version"`
	GoVersion    string  `json:"go_version"`
	GOOS         string  `json:"goos"`
	GOARCH       string  `json:"goarch"`
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
			if err := f.git.ensureMarker(context.Background(), f.colony, p, "workload-colony"); err != nil {
				t.Fatal(err)
			}
			baseline := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "--verify", "--end-of-options", "main^{tree}")))
			inputs := filepath.Join(f.colony, "workspaces", "inputs", "1")
			dir := filepath.Join(f.colony, "scratch", fmt.Sprintf("%08x", count))
			writeRow := func(phase string, duration time.Duration, result captureResult) {
				row := captureWorkloadRow{Phase: phase, Files: count, CoreRawBytes: result.RawBytes, DurationMS: float64(duration) / float64(time.Millisecond), Tree: result.Tree, GitVersion: f.git.version, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
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
			fixture := &captureFixture{f: f, p: p, s: captureScratch{Worktree: worktree, Inputs: p, Revision: 1, InputsDir: inputs, TmpDir: filepath.Join(f.colony, "workspaces", "00112233", "tmp"), ColonyID: "workload-colony"}, engine: newCaptureEngine(f.git, f.colony), pool: testReservationPool(t, &profile)}
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
		})
	}
}

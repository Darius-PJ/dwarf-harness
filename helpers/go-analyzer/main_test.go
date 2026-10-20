package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--graph-helper" {
		os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func writeFixtureFile(t *testing.T, name string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func nativeRequest(t *testing.T, root string, files map[string]string) request {
	t.Helper()
	helper, tool, goroot := os.Getenv("DWARF_GRAPH_GO_HELPER"), os.Getenv("DWARF_GRAPH_GO"), os.Getenv("DWARF_GRAPH_GOROOT")
	if helper == "" && tool == "" && goroot == "" {
		t.Skip("native Go profile not exercised: all three DWARF_GRAPH_GO variables are unset")
	}
	if !filepath.IsAbs(helper) || !filepath.IsAbs(tool) || !filepath.IsAbs(goroot) {
		t.Fatal("partial or non-absolute native Go profile")
	}
	if info, err := os.Stat(helper); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("prebuilt production helper: %v", err)
	}
	info, err := os.Stat(tool)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("pinned go tool: %v", err)
	}
	body, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	r := requestFixture(root, files)
	r.Go.ToolchainPath, r.Go.GOROOT = tool, goroot
	r.Go.ToolchainSHA256 = hashBytes(body)
	r.Go.ToolchainFileSize, r.Go.ToolchainModtimeNS = info.Size(), info.ModTime().UnixNano()
	for _, directory := range []string{"corpus", "gopath", "gomodcache", "gocache", "tmp", "home"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range r.Corpus {
		writeFixtureFile(t, filepath.Join(r.MaterializationRoot, filepath.FromSlash(item.Path)), item.Bytes)
	}
	writeFixtureFile(t, telemetryMode(root), []byte("off"))
	return r
}

func runNativeFixture(t *testing.T, root string, files map[string]string) (request, response) {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	r := nativeRequest(t, root, files)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--graph-helper", helperVersion)
	env := profileEnvironment(r.Go, root)
	if !slices.Contains(env, "GODEBUG=goindex=0") {
		t.Fatal("native profile must bind GODEBUG=goindex=0")
	}
	cmd.Dir, cmd.Env, cmd.Stdin = root, env, bytes.NewReader(requestFrame(r))
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("native helper: %v\n%s", err, stderr.String())
	}
	if len(raw) < 4 || len(raw) > maxFrame || int(binary.BigEndian.Uint32(raw)) != len(raw)-4 {
		t.Fatalf("invalid native response frame: %d bytes\n%s", len(raw), stderr.String())
	}
	var out response
	if err := json.Unmarshal(raw[4:], &out); err != nil {
		t.Fatal(err)
	}
	if out.AnalysisKey != r.AnalysisKey || out.ConfigurationSHA256 != r.ConfigurationSHA256 || out.WorkUsed > r.Allowance || out.NextCursor != (cursor{int64(len(r.Corpus)), 0}) {
		t.Fatalf("unbound native result: %+v", out)
	}
	t.Logf("native profile %s %s/%s; work %d; diagnostics:\n%s", r.Go.ToolchainVersion, r.Go.GOOS, r.Go.GOARCH, out.WorkUsed, stderr.String())
	return r, out
}

func TestGoHelperResultBudgetIsAllOrNothing(t *testing.T) {
	t.Parallel()
	for _, over := range []bool{false, true} {
		name := "exact"
		if over {
			name = "one unit over"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := requestFixture(t.TempDir(), map[string]string{"a.go": "package a\n"})
			out := emptyResponse(r)
			key := nodeKey{"workspace", r.Source.WorkspaceID, "module", canonical([3]string{"go", "workspace", "example.test/dependency"})}
			out.Nodes = []node{{Key: key, VersionSHA256: hashBytes([]byte("native members")), Adapter: "go", ConfigurationSHA256: r.ConfigurationSHA256}}
			relation := edge{From: key, To: r.Corpus[0].Key, Type: "static_dependency", Geometry: "coupling", WeightPPM: 1000000, Evidence: evidence{Artifact: r.Corpus[0].Key, ArtifactSHA256: r.Corpus[0].SHA256, Adapter: "go", ConfigurationSHA256: r.ConfigurationSHA256, Reference: "example.test/dependency"}}
			relation.EdgeID = hashBytes([]byte(canonical([3]any{relation.From, relation.To, relation.Type})))
			out.Edges = []edge{relation}
			out.Coverage = []coverage{{Scope: canonical(r.Corpus[0].Key), Adapter: "go", State: "complete", Reasons: []string{}}}
			// The epoch start keeps WallNS at 19 decimal digits. Keep the frame
			// away from a byte-quantum boundary while WorkUsed reaches five.
			out.Cost.WallNS, out.WorkUsed = 1_000_000_000_000_000_000, r.Allowance
			for n := (len(canonical(out)) + 4) % 256; n < 16 || n > 240; n = (len(canonical(out)) + 4) % 256 {
				out.Edges[0].Evidence.Reference += "x"
			}
			used := r.Allowance - byteUnits(len(canonical(out))+4)
			if over {
				used++
			}
			var frame bytes.Buffer
			if code := emit(&frame, r, out, &budget{used: used, limit: r.Allowance}, time.Unix(0, 0)); code != 0 {
				t.Fatalf("bounded response exit %d", code)
			}
			raw := frame.Bytes()
			if len(raw) < 4 || len(raw) > maxFrame || int(binary.BigEndian.Uint32(raw[:4])) != len(raw)-4 {
				t.Fatalf("invalid bounded response: %d bytes", len(raw))
			}
			var got response
			if err := json.Unmarshal(raw[4:], &got); err != nil {
				t.Fatal(err)
			}
			if got.WorkUsed > r.Allowance {
				t.Fatalf("response exceeded its reservation: %d", got.WorkUsed)
			}
			if !over {
				if len(got.Nodes) != 1 || len(got.Edges) != 1 || len(got.Coverage) != 1 || got.Coverage[0].State != "complete" {
					t.Fatalf("exactly fitting facts were refused: %+v", got)
				}
			} else if len(got.Nodes) != 0 || len(got.Edges) != 0 || len(got.Coverage) != 1 || got.Coverage[0].Scope != "workspace:"+r.Source.WorkspaceID+":canonical" || got.Coverage[0].State != "unknown" || len(got.Coverage[0].Reasons) != 1 || got.Coverage[0].Reasons[0] != "helper_limit" {
				t.Fatalf("over-budget facts became apparent partial success: %+v", got)
			}
		})
	}
}

func TestGoOwnedDirectoryBoundaries(t *testing.T) {
	t.Parallel()
	for _, short := range []bool{false, true} {
		name := "work_exact"
		if short {
			name = "work_one_short"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFixtureFile(t, filepath.Join(root, "corpus", "input.go"), make([]byte, 4096))
			writeFixtureFile(t, filepath.Join(root, "gocache", "entry"), make([]byte, 8192))
			writeFixtureFile(t, filepath.Join(root, "mode"), []byte("off"))
			// Root, two directories and three files are six visited entries.
			work := budget{limit: 6}
			want := ""
			if short {
				work.limit--
				want = "helper_limit"
			}
			var diagnostics bytes.Buffer
			reason := inspectOwnedDirectory(t.Context(), root, &work, &diagnostics)
			if reason != want {
				t.Fatalf("walk boundary: reason %q, want %q: %s", reason, want, diagnostics.String())
			}
			var corpus, cache, scaffold uint64
			var complete bool
			if _, err := fmt.Sscanf(diagnostics.String(), "owned bytes corpus=%d cache=%d scaffold=%d complete=%t", &corpus, &cache, &scaffold, &complete); err != nil {
				t.Fatal(err)
			}
			if complete == short || !short && (corpus != 4096 || cache != 8192 || scaffold != 3) {
				t.Fatalf("walk measurements: %s", diagnostics.String())
			}
		})
	}
	for _, over := range []bool{false, true} {
		name := "temporary_exact"
		if over {
			name = "temporary_one_over"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			path := filepath.Join(root, "corpus", "input")
			writeFixtureFile(t, path, nil)
			size := int64(64 << 20)
			want := ""
			if over {
				size++
				want = "temporary_cap"
			}
			if err := os.Truncate(path, size); err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			reason := inspectOwnedDirectory(t.Context(), root, &budget{limit: 3}, &diagnostics)
			var corpus, cache, scaffold uint64
			var complete bool
			if _, err := fmt.Sscanf(diagnostics.String(), "owned bytes corpus=%d cache=%d scaffold=%d complete=%t", &corpus, &cache, &scaffold, &complete); err != nil {
				t.Fatal(err)
			}
			if reason != want || complete == over || corpus != uint64(size) || cache != 0 || scaffold != 0 {
				t.Fatalf("temporary boundary: reason %q, want %q: %s", reason, want, diagnostics.String())
			}
		})
	}
}

func FuzzGoVersionOutput(f *testing.F) {
	want := "go version go1.27.2 " + runtime.GOOS + "/" + runtime.GOARCH
	for _, value := range []string{want, want + "\n", want + "\n\n", want + " extra", strings.Replace(want, "go1.27.2", "go1.27.3", 1), "go version go1.27.2 other/amd64", "go version", strings.Repeat("x", 129)} {
		f.Add([]byte(value))
	}
	f.Fuzz(func(t *testing.T, output []byte) {
		err := parseGoVersion(output, "go1.27.2")
		valid := bytes.Equal(output, []byte(want)) || bytes.Equal(output, []byte(want+"\n"))
		if (err == nil) != valid {
			t.Fatalf("version output %q: %v; exact pinned host identity=%v", output, err, valid)
		}
		if err == nil && parseGoVersion(output, "go1.27.3") == nil {
			t.Fatal("version mismatch was defaulted")
		}
	})
}

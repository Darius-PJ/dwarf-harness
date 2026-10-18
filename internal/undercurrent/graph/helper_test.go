package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestToolVerificationResumesAndRefusesReplacement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "tool")
	data := bytes.Repeat([]byte("bound tool"), 100)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	tool := Tool{Path: file, SHA256: ledger.Digest(digest(string(data))), Version: "1"}
	v, err := NewToolVerification(tool)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	p, err := v.Step(context.Background(), 1)
	if err != nil || p.Verified != nil || p.WorkUsed != 1 {
		t.Fatalf("premature verification %+v %v", p, err)
	}
	var units int64 = 1
	for p.Verified == nil {
		p, err = v.Step(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		units += p.WorkUsed
	}
	if units != 4 || p.Verified.Identity() != tool {
		t.Fatalf("resumed result %+v %d", p, units)
	}
	whole, err := NewToolVerification(tool)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	all, err := whole.Step(context.Background(), 10000)
	if err != nil || all.Verified == nil || all.Verified.Identity() != p.Verified.Identity() {
		t.Fatalf("single pass %+v %v", all, err)
	}
	replaced, err := NewToolVerification(tool)
	if err != nil {
		t.Fatal(err)
	}
	defer replaced.Close()
	if _, err = replaced.Step(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	// Write-through replacement is portable even when Windows denies unlinking
	// an open executable. Its metadata and unread bytes differ.
	if err := os.WriteFile(file, []byte("different file"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := replaced.Step(context.Background(), 10000)
	if err == nil || got.Verified != nil {
		t.Fatalf("replaced file verified %+v %v", got, err)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("DWARF_GRAPH_TEST_CHILD") != "" {
		graphTestChild()
		return
	}
	os.Exit(m.Run())
}
func graphTestChild() {
	mode := os.Getenv("DWARF_GRAPH_TEST_CHILD")
	if mode == "lost" {
		return
	}
	var length [4]byte
	if _, err := io.ReadFull(os.Stdin, length[:]); err != nil {
		fmt.Fprintln(os.Stderr, "header:", err)
		os.Exit(2)
	}
	n := binary.BigEndian.Uint32(length[:])
	if n > maxFrame {
		os.Exit(2)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(os.Stdin, b); err != nil {
		fmt.Fprintln(os.Stderr, "body:", err)
		os.Exit(2)
	}
	var req helperRequest
	if err := decodeHelperJSON(b, &req); err != nil {
		fmt.Fprintln(os.Stderr, "request:", err)
		os.Exit(2)
	}
	if mode == "overflow" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), maxFrame+6))
		return
	}
	if mode == "stderr" || mode == "stderr-error" {
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("Z"), (64<<10)+4096))
		if mode == "stderr-error" {
			os.Exit(1)
		}
	}
	if mode == "cancel" || mode == "pause" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, "listen:", err)
			os.Exit(2)
		}
		defer listener.Close()
		_, _ = fmt.Fprint(os.Stderr, "ready")
		if mode == "pause" && os.Getenv("DWARF_GRAPH_TEST_READY") != "" {
			connection, err := net.Dial("tcp", os.Getenv("DWARF_GRAPH_TEST_READY"))
			if err != nil {
				fmt.Fprintln(os.Stderr, "ready:", err)
				os.Exit(2)
			}
			_, _ = fmt.Fprintln(connection, listener.Addr().String())
			_ = connection.Close()
		}
		connection, _ := listener.Accept()
		if connection != nil {
			_ = connection.Close()
		}
		if mode == "cancel" {
			return
		}
	}
	out := helperResponse{Protocol: helperProtocol, AnalysisKey: req.AnalysisKey, Adapter: req.Adapter, ConfigurationSHA256: req.ConfigurationSHA256, AnalysisResult: AnalysisResult{NextCursor: ledger.GraphCursor{ArtifactIndex: int64(len(req.Corpus))}, WorkUsed: 100, Nodes: []ledger.GraphNode{}, Edges: []ledger.GraphEdge{}, Coverage: []ledger.CoverageEntry{}, Cost: Cost{InputBytes: int64(len(b)), OutputBytes: 0}}}
	raw, _ := json.Marshal(out)
	binary.BigEndian.PutUint32(length[:], uint32(len(raw)))
	_, _ = os.Stdout.Write(length[:])
	_, _ = os.Stdout.Write(raw)
}

func testHelperRequest() helperRequest {
	r := analysisFixture(nil)
	return helperRequest{Protocol: helperProtocol, AnalysisKey: r.AnalysisKey, Adapter: ledger.AdapterPython, ConfigurationSHA256: r.ConfigurationSHA256, Source: r.Source, MaterializationRoot: "owned", Corpus: []CorpusArtifact{}, Allowance: 10000, Limits: helperLimits{maxFrame, maxFrame, maxBlob, maxItems}}
}
func responseFrame(v any) []byte { b, _ := json.Marshal(v); return frameJSON(b) }
func frameJSON(b []byte) []byte {
	out := make([]byte, 4, len(b)+4)
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	return append(out, b...)
}
func validResponse(req helperRequest) helperResponse {
	return helperResponse{Protocol: req.Protocol, AnalysisKey: req.AnalysisKey, Adapter: req.Adapter, ConfigurationSHA256: req.ConfigurationSHA256, AnalysisResult: AnalysisResult{NextCursor: ledger.GraphCursor{ArtifactIndex: int64(len(req.Corpus))}, WorkUsed: 100, Nodes: []ledger.GraphNode{}, Edges: []ledger.GraphEdge{}, Coverage: []ledger.CoverageEntry{}, Cost: Cost{}}}
}

func TestHelperProtocolCancellationOverflowAndLostHandshake(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	req := testHelperRequest()
	input, _ := json.Marshal(req)
	for _, mode := range []string{"valid", "lost", "overflow", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := false
			observe := func(b []byte) {
				if strings.Contains(string(b), "ready") {
					ready = true
					cancel()
				}
			}
			raw, _, err := runHelperProcess(ctx, cancel, executable, []string{"-test.run=^$"}, dir, append(helperEnvironment(dir), "DWARF_GRAPH_TEST_CHILD="+mode), input, observe, nil)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := decodeHelperResponse(raw, req); err != nil {
					t.Fatal(err)
				}
				return
			}
			if mode == "lost" {
				if err == nil {
					_, err = decodeHelperResponse(raw, req)
				}
				if err == nil {
					t.Fatal("lost handshake accepted")
				}
				return
			}
			if err == nil {
				t.Fatal("child failure accepted")
			}
			if mode == "cancel" && (!ready || !errors.Is(ctx.Err(), context.Canceled)) {
				t.Fatal("cancellation was not handshake-driven")
			}
			// Returning from runHelperProcess means Wait completed; the next actual
			// child can execute immediately without sharing a live predecessor.
			next, nextCancel := context.WithCancel(context.Background())
			defer nextCancel()
			raw, _, err = runHelperProcess(next, nextCancel, executable, []string{"-test.run=^$"}, dir, append(helperEnvironment(dir), "DWARF_GRAPH_TEST_CHILD=valid"), input, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeHelperResponse(raw, req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHelperResponseRejectsUnboundAndMalformedOutput(t *testing.T) {
	t.Parallel()
	req := testHelperRequest()
	valid := validResponse(req)
	raw, _ := json.Marshal(valid)
	for name, b := range map[string][]byte{
		"duplicate": bytes.Replace(raw, []byte(`"protocol":`), []byte(`"protocol":"graph-helper/v1","protocol":`), 1),
		"unknown":   bytes.Replace(raw, []byte(`"protocol":`), []byte(`"extra":0,"protocol":`), 1),
		"missing":   bytes.Replace(raw, []byte(`"work_used":100,`), nil, 1),
		"null":      bytes.Replace(raw, []byte(`"nodes":[]`), []byte(`"nodes":null`), 1),
		"fraction":  bytes.Replace(raw, []byte(`"work_used":100`), []byte(`"work_used":1.5`), 1),
		"overflow":  bytes.Replace(raw, []byte(`"work_used":100`), []byte(`"work_used":9223372036854775808`), 1),
		"binding":   bytes.Replace(raw, []byte(req.AnalysisKey.String()), []byte(digest("wrong").String()), 1),
		"trailer":   append(append([]byte{}, raw...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeHelperResponse(frameJSON(b), req); err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
	// A well-formed frozen value with forged evidence is still not helper proof.
	source := analysisFixture(map[string]string{"a.py": "import b", "b.py": ""})
	req.Corpus = source.Corpus
	key, _ := ModuleKey(testID(), ledger.AdapterPython, "workspace", "b")
	valid = validResponse(req)
	valid.Nodes = []ledger.GraphNode{{Key: key, VersionSHA256: digest("module"), Adapter: req.Adapter, ConfigurationSHA256: req.ConfigurationSHA256}}
	e := dependency(key, source.Corpus[0], req.Adapter, "b", req.ConfigurationSHA256)
	valid.Edges = []ledger.GraphEdge{e}
	for _, artifact := range req.Corpus {
		valid.Coverage = append(valid.Coverage, covered(artifact.Key, req.Adapter, ""))
	}
	sortResult(&valid.AnalysisResult)
	if _, err := decodeHelperResponse(responseFrame(valid), req); err != nil {
		t.Fatal(err)
	}
	valid.Edges[0].Evidence.ArtifactSHA256 = digest("forged")
	if _, err := decodeHelperResponse(responseFrame(valid), req); err == nil {
		t.Fatal("accepted out-of-corpus proof")
	}
}

func FuzzGraphHelperResponse(f *testing.F) {
	req := testHelperRequest()
	source := analysisFixture(map[string]string{"a.py": "import b", "b.py": ""})
	req.Corpus = source.Corpus
	base := validResponse(req)
	key, _ := ModuleKey(testID(), req.Adapter, "workspace", "b")
	base.Nodes = []ledger.GraphNode{{Key: key, VersionSHA256: digest("module"), Adapter: req.Adapter, ConfigurationSHA256: req.ConfigurationSHA256}}
	base.Edges = []ledger.GraphEdge{dependency(key, source.Corpus[0], req.Adapter, "b", req.ConfigurationSHA256)}
	for _, artifact := range req.Corpus {
		base.Coverage = append(base.Coverage, covered(artifact.Key, req.Adapter, ""))
	}
	sortResult(&base.AnalysisResult)
	valid := responseFrame(base)
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{255, 255, 255, 255})
	f.Add(append(append([]byte{}, valid...), 0))
	for _, s := range []string{`{}`, `{"nodes":null}`, `{"protocol":"x","protocol":"y"}`, `{"work_used":1e100}`, `{"nodes":[{}]}`} {
		f.Add(frameJSON([]byte(s)))
	}
	base.Edges = append(base.Edges, base.Edges[0])
	f.Add(responseFrame(base))
	base = validResponse(req)
	base.AnalysisResult = unknown(source, req.Adapter, ledger.ReasonHelperLimit, req.Allowance)
	base.NextCursor.ArtifactIndex = int64(len(req.Corpus))
	f.Add(responseFrame(base))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > maxFrame+5 {
			return
		}
		out, err := decodeHelperResponse(b, req)
		if err == nil && (out.WorkUsed < 0 || out.WorkUsed > req.Allowance) {
			t.Fatal("accepted excessive work")
		}
	})
}

func installedTool(t *testing.T, variable, version string) *VerifiedTool {
	t.Helper()
	file := os.Getenv(variable)
	if file == "" {
		t.Skip(variable + " unset: installed interpreter/compiler path not exercised")
	}
	if !filepath.IsAbs(file) {
		t.Fatal(variable + " must be absolute")
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	var sum ledger.Digest
	copy(sum[:], h.Sum(nil))
	v, err := NewToolVerification(Tool{Path: file, SHA256: sum, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	var charged int64
	for {
		p, err := v.Step(context.Background(), 10000)
		if err != nil {
			t.Fatal(err)
		}
		charged += p.WorkUsed
		if p.Verified != nil {
			t.Logf("tool=%s version=%s sha256=%s verification_units=%d", file, version, sum, charged)
			return p.Verified
		}
	}
}

func TestPythonAnalysisNeverImportsTarget(t *testing.T) {
	t.Parallel()
	tools := Tools{Python: installedTool(t, "DWARF_GRAPH_PYTHON", "3.13.15")}
	a := NewAnalyzer(tools)
	sentinel := filepath.Join(t.TempDir(), "executed")
	literal, _ := json.Marshal(sentinel)
	r := analysisFixture(map[string]string{
		"pkg/__init__.py":    "open(" + string(literal) + ", 'w').write('executed')\nraise RuntimeError('must not import')\n",
		"pkg/child.py":       "value = 1\n",
		"pkg/consumer.py":    "from . import child\n",
		"main.py":            "import pkg.child\nimport ns.part\nimport ns.nested.leaf\nimport ns\n",
		"ns/part.py":         "value = 2\n",
		"ns/nested/leaf.py":  "value = 3\n",
		"hooked/__init__.py": "__path__ = ['../other']\n",
		"hooked/child.py":    "value = 4\n",
		"hook_consumer.py":   "import hooked.child\n",
		"dynamic.py":         "__import__('pkg.child')\n",
		"missing.py":         "import unavailable\n",
		"conditional.py":     "if True:\n import pkg.child\n",
	})
	r.ConfigurationSHA256 = ConfigurationDigest(tools)
	out, err := a.Analyze(context.Background(), ledger.AdapterPython, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("target executed: %v", err)
	}
	expected := map[string]string{"main.py": "", "pkg/consumer.py": "", "dynamic.py": ledger.ReasonUnsupportedSyntax, "missing.py": ledger.ReasonUnresolvedReference, "conditional.py": ledger.ReasonUnsupportedSyntax}
	expected["hook_consumer.py"] = ledger.ReasonUnresolvedReference
	expected["hooked/__init__.py"] = ledger.ReasonUnsupportedSyntax
	assertCoverage(t, r, out, ledger.AdapterPython, expected)
	consumers := map[string]int{}
	for _, e := range out.Edges {
		if e.From.Kind != ledger.NodeKindModule || e.To.Kind != ledger.NodeKindArtifact || e.Evidence.Artifact != e.To {
			t.Fatalf("reversed or unevidenced import %+v", e)
		}
		for _, f := range r.Corpus {
			if f.Key == e.To {
				consumers[f.Path]++
			}
		}
	}
	if consumers["main.py"] != 4 || consumers["pkg/consumer.py"] != 1 {
		t.Fatalf("native module resolution %+v; result %+v", consumers, out)
	}
	t.Logf("helper_cost=%+v work=%d", out.Cost, out.WorkUsed)
}

func assertCoverage(t *testing.T, r AnalysisRequest, out AnalysisResult, adapter string, want map[string]string) {
	t.Helper()
	for name, reason := range want {
		found := false
		for _, artifact := range r.Corpus {
			if artifact.Path != name {
				continue
			}
			for _, c := range out.Coverage {
				if c.Scope == keyScope(artifact.Key) && c.Adapter == adapter {
					found = true
					expected := covered(artifact.Key, adapter, reason)
					one, _ := json.Marshal(c)
					two, _ := json.Marshal(expected)
					if !bytes.Equal(one, two) {
						t.Fatalf("%s coverage %s want %s", name, one, two)
					}
				}
			}
		}
		if !found {
			t.Fatalf("%s missing coverage: %+v", name, out)
		}
	}
}

func TestTypeScriptAnalysisUsesOnlyConfiguredParser(t *testing.T) {
	t.Parallel()
	tools := Tools{Node: installedTool(t, "DWARF_GRAPH_NODE", "v24.18.0"), TypeScriptParser: installedTool(t, "DWARF_GRAPH_TYPESCRIPT", "6.0.3")}
	a := NewAnalyzer(tools)
	sentinel := filepath.Join(t.TempDir(), "executed")
	literal, _ := json.Marshal(sentinel)
	bomb := "require('node:fs').writeFileSync(" + string(literal) + ",'executed'); throw Error('target executed');"
	r := analysisFixture(map[string]string{
		"tsconfig.json":                        `{"extends":"./base.json","compilerOptions":{"plugins":[{"name":"./plugin.cjs"}]},"references":[{"path":"./child"}]}`,
		"base.json":                            `{"compilerOptions":{"baseUrl":".","paths":{"alias":["./dep.ts"]}}}`,
		"child/tsconfig.json":                  `{"compilerOptions":{},"files":["other.ts"]}`,
		"child/other.ts":                       "export const y = 1;",
		"dep.ts":                               "export const x = 1;",
		"main.ts":                              "import {x} from './dep.js'; export {x} from 'alias';",
		"dynamic.ts":                           "import('./dep.js');",
		"missing.ts":                           "import x from 'unavailable';",
		"plugin.cjs":                           bomb,
		"node_modules/typescript/index.js":     bomb,
		"node_modules/typescript/package.json": `{"name":"typescript","main":"index.js"}`,
	})
	r.ConfigurationSHA256 = ConfigurationDigest(tools)
	out, err := a.Analyze(context.Background(), ledger.AdapterTypeScript, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("target parser/plugin executed: %v", err)
	}
	assertCoverage(t, r, out, ledger.AdapterTypeScript, map[string]string{"main.ts": "", "dynamic.ts": ledger.ReasonUnsupportedSyntax, "missing.ts": ledger.ReasonUnresolvedReference})
	count := 0
	for _, e := range out.Edges {
		if e.Evidence.Reference == "./dep.js" || e.Evidence.Reference == "alias" {
			count++
			if !strings.Contains(e.From.LocalKey, "dep.ts") {
				t.Fatalf("wrong native resolution %+v", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one deduplicated dependency: %+v", out)
	}
	t.Logf("helper_cost=%+v work=%d", out.Cost, out.WorkUsed)
	r = analysisFixture(map[string]string{"tsconfig.json": `{"extends":"../outside.json"}`, "main.ts": "import './dep.js';", "dep.ts": "export const x=1;"})
	r.ConfigurationSHA256 = ConfigurationDigest(tools)
	out, err = a.Analyze(context.Background(), ledger.AdapterTypeScript, r)
	if err != nil || len(out.Edges) != 0 || len(out.Coverage) != 1 || out.Coverage[0].State != ledger.CoverageUnknown {
		t.Fatalf("host escape became success %+v %v", out, err)
	}
}

func TestMissingAdaptersAndUnapprovedGoStayUnknown(t *testing.T) {
	t.Parallel()
	r := analysisFixture(map[string]string{"a.go": "package a\nimport \"net/http\"", "readme.md": "[dependency](a.go)", "data.json": `{"imports":["a.go"]}`, "upper.PY": "import hidden", "upper.JS": `import "hidden"`, "upper.SQL": "SELECT * FROM hidden;"})
	r.ConfigurationSHA256 = ConfigurationDigest(Tools{})
	for _, adapter := range []string{ledger.AdapterPython, ledger.AdapterTypeScript} {
		out, err := NewAnalyzer(Tools{}).Analyze(context.Background(), adapter, r)
		if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || len(out.Coverage) != 1 || out.Coverage[0].Reasons[0] != ledger.ReasonAdapterUnavailable {
			t.Fatalf("missing adapter %+v %v", out, err)
		}
	}
	out, err := AnalyzeOpaque(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Edges) != 0 {
		t.Fatal("Go or hyperlinks guessed causal edges")
	}
	assertCoverage(t, r, out, "go", map[string]string{"a.go": ledger.ReasonDependencyUnapproved})
	assertCoverage(t, r, out, ledger.AdapterOpaque, map[string]string{"readme.md": ledger.ReasonUnsupportedSyntax, "data.json": ledger.ReasonUnsupportedSyntax, "upper.PY": ledger.ReasonUnsupportedSyntax, "upper.JS": ledger.ReasonUnsupportedSyntax, "upper.SQL": ledger.ReasonUnsupportedSyntax})
	sql, err := AnalyzeSQL(context.Background(), r)
	if err != nil || len(sql.Nodes) != 0 || len(sql.Edges) != 0 || len(sql.Coverage) != 0 {
		t.Fatalf("uppercase SQL was selected as source: %+v, %v", sql, err)
	}
}

func TestHelperBoundsFailClosed(t *testing.T) {
	t.Parallel()
	req := testHelperRequest()
	source := analysisFixture(map[string]string{"a.py": "import b", "b.py": ""})
	req.Corpus = source.Corpus
	missingCoverage := validResponse(req)
	if _, err := decodeHelperResponse(responseFrame(missingCoverage), req); err == nil {
		t.Fatal("missing dependency coverage accepted")
	}
	key, _ := ModuleKey(testID(), ledger.AdapterPython, "workspace", "b")
	base := validResponse(req)
	base.Nodes = []ledger.GraphNode{{Key: key, VersionSHA256: digest("module"), Adapter: req.Adapter, ConfigurationSHA256: req.ConfigurationSHA256}}
	base.Edges = []ledger.GraphEdge{dependency(key, source.Corpus[0], req.Adapter, "b", req.ConfigurationSHA256)}
	for _, artifact := range req.Corpus {
		base.Coverage = append(base.Coverage, covered(artifact.Key, req.Adapter, ""))
	}
	sortResult(&base.AnalysisResult)
	if _, err := decodeHelperResponse(responseFrame(base), req); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		name   string
		change func(*helperResponse)
	}{
		{"long_reference", func(v *helperResponse) { v.Edges[0].Evidence.Reference = strings.Repeat("x", 4097) }},
		{"zero_weight", func(v *helperResponse) { v.Edges[0].WeightPPM = 0 }},
		{"heavy_weight", func(v *helperResponse) { v.Edges[0].WeightPPM = 1000001 }},
		{"duplicate_edge", func(v *helperResponse) { v.Edges = append(v.Edges, v.Edges[0]) }},
		{"unsorted_coverage", func(v *helperResponse) { v.Coverage[0], v.Coverage[1] = v.Coverage[1], v.Coverage[0] }},
		{"partial_limit", func(v *helperResponse) {
			v.Coverage = unknown(source, req.Adapter, ledger.ReasonHelperLimit, 0).Coverage
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			v := base
			v.Nodes = append([]ledger.GraphNode{}, base.Nodes...)
			v.Edges = append([]ledger.GraphEdge{}, base.Edges...)
			v.Coverage = append([]ledger.CoverageEntry{}, base.Coverage...)
			mutation.change(&v)
			if _, err := decodeHelperResponse(responseFrame(v), req); err == nil {
				t.Fatal("unsafe output accepted")
			}
		})
	}
	// The object count cap is enforced before allocating an unbounded decoded
	// graph, even though the framed byte length itself fits.
	oversized := validResponse(testHelperRequest())
	oversized.Coverage = make([]ledger.CoverageEntry, maxItems+1)
	if _, err := decodeHelperResponse(responseFrame(oversized), testHelperRequest()); err == nil {
		t.Fatal("output cap bypassed")
	}
}

func TestVerifiedVersionMismatchStaysUnavailable(t *testing.T) {
	t.Parallel()
	good := installedTool(t, "DWARF_GRAPH_PYTHON", "3.13.15")
	identity := good.Identity()
	identity.Version = "0.0.0"
	verification, err := NewToolVerification(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer verification.Close()
	p, err := verification.Step(context.Background(), 10000)
	if err != nil || p.Verified == nil {
		t.Fatal(err)
	}
	tools := Tools{Python: p.Verified}
	r := analysisFixture(map[string]string{"a.py": ""})
	r.ConfigurationSHA256 = ConfigurationDigest(tools)
	out, err := NewAnalyzer(tools).Analyze(context.Background(), ledger.AdapterPython, r)
	if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || out.Coverage[0].Reasons[0] != ledger.ReasonAdapterUnavailable {
		t.Fatalf("mismatched version %+v %v", out, err)
	}
}

func TestHelperStderrTruncation(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	req := testHelperRequest()
	input, _ := json.Marshal(req)
	for _, mode := range []string{"stderr", "stderr-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			raw, _, err := runHelperProcess(ctx, cancel, executable, []string{"-test.run=^$"}, dir, append(helperEnvironment(dir), "DWARF_GRAPH_TEST_CHILD="+mode), input, nil, nil)
			if ctx.Err() != nil {
				t.Fatalf("retention overflow cancelled child: %v", ctx.Err())
			}
			if mode == "stderr" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := decodeHelperResponse(raw, req); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Count(err.Error(), "Z") != 64<<10 {
				t.Fatalf("child failure did not retain exactly the bounded stderr prefix: %v", err)
			}
		})
	}
}

func TestPythonUnavailableCorpus(t *testing.T) {
	t.Parallel()
	tools := Tools{Python: installedTool(t, "DWARF_GRAPH_PYTHON", "3.13.15")}
	cases := map[string]map[string]string{
		"filesystem-error": {"pkg": "not a directory", "pkg/main.py": "value = 1"},
	}
	if os.PathSeparator == '\\' {
		cases["file-case-collision"] = map[string]string{"A.py": "a = 1", "a.py": "a = 2"}
		cases["directory-case-collision"] = map[string]string{"Pkg/a.py": "a = 1", "pkg/b.py": "b = 2"}
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			r := analysisFixture(files)
			r.ConfigurationSHA256 = ConfigurationDigest(tools)
			out, err := NewAnalyzer(tools).Analyze(context.Background(), ledger.AdapterPython, r)
			if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || len(out.Coverage) != 1 {
				t.Fatalf("unavailable corpus leaked partial facts: %+v %v", out, err)
			}
			entry := out.Coverage[0]
			if entry.Scope != workspaceScope(r.Source.WorkspaceID) || entry.State != ledger.CoverageUnknown || len(entry.Reasons) != 1 || entry.Reasons[0] != ledger.ReasonSourceUnavailable {
				t.Fatalf("filesystem unavailability misclassified: %+v", entry)
			}
		})
	}
}

func TestTypeScriptUnavailableCorpus(t *testing.T) {
	t.Parallel()
	tools := Tools{
		Node:             installedTool(t, "DWARF_GRAPH_NODE", "v24.18.0"),
		TypeScriptParser: installedTool(t, "DWARF_GRAPH_TYPESCRIPT", "6.0.3"),
	}
	r := analysisFixture(map[string]string{"pkg": "not a directory", "pkg/main.ts": "export const value = 1;"})
	r.ConfigurationSHA256 = ConfigurationDigest(tools)
	out, err := NewAnalyzer(tools).Analyze(context.Background(), ledger.AdapterTypeScript, r)
	if err != nil || len(out.Nodes) != 0 || len(out.Edges) != 0 || len(out.Coverage) != 1 {
		t.Fatalf("unavailable corpus leaked partial facts: %+v %v", out, err)
	}
	entry := out.Coverage[0]
	if entry.Scope != workspaceScope(r.Source.WorkspaceID) || entry.State != ledger.CoverageUnknown || len(entry.Reasons) != 1 || entry.Reasons[0] != ledger.ReasonSourceUnavailable {
		t.Fatalf("virtual path collision misclassified: %+v", entry)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestGoNativeResolutionAndNoTargetExecution(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sentinel := filepath.Join(root, "executed")
	files := map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.27\n",
		"main.go":     "//go:generate go run .\npackage main\nimport (\n alias \"example.com/m/dep\"\n _ `example.com/m/dep`\n . \"example.com/m/dep\"\n \"os\"\n)\nfunc init(){os.WriteFile(" + strconvQuote(sentinel) + ",nil,0600);panic(\"target executed\")}\nfunc main(){}\n",
		"dep/a.go":    "package dep\nconst Value = 1\n",
		"dep/b.go":    "package dep\nconst Other = 2\n",
		"std/only.go": "package std\nimport _ \"unsafe\"\n",
	}
	r, out := runNativeFixture(t, root, files)
	assertFileCoverage(t, r, out, map[string][]string{"main.go": {}, "dep/a.go": {}, "dep/b.go": {}, "std/only.go": {}})
	if len(out.Nodes) != 1 || len(out.Edges) != 1 {
		t.Fatalf("native duplicate/standard imports: nodes=%v edges=%v", out.Nodes, out.Edges)
	}
	wantKey := nodeKey{"workspace", r.Source.WorkspaceID, "module", `["go","workspace","example.com/m/dep"]`}
	wantVersion := hashBytes([]byte(canonical(struct {
		Name    string      `json:"module_name"`
		Members [][2]string `json:"members"`
	}{"example.com/m/dep", [][2]string{{"dep/a.go", hashBytes([]byte(files["dep/a.go"]))}, {"dep/b.go", hashBytes([]byte(files["dep/b.go"]))}}})))
	if out.Nodes[0].Key != wantKey || out.Nodes[0].VersionSHA256 != wantVersion {
		t.Fatalf("native selected membership: %v; want %v %s", out.Nodes[0], wantKey, wantVersion)
	}
	e := out.Edges[0]
	consumer := artifactKey(r.Source.WorkspaceID, 2, "main.go")
	if e.From != wantKey || e.To != consumer || e.Evidence.Artifact != consumer || e.Evidence.ArtifactSHA256 != hashBytes([]byte(files["main.go"])) || e.Evidence.Reference != "example.com/m/dep" || e.EdgeID != hashBytes([]byte(canonical([3]any{wantKey, consumer, "static_dependency"}))) {
		t.Fatalf("dependency direction/evidence: %+v", e)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("target initializer/generator executed: %v", err)
	}
}

func TestGoInventoryIsNeverCompleteEmpty(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"go.mod":             "module example.com/m\n\ngo 1.27\n",
		"zero.go":            "package m\n",
		"imports.go":         "package m\nimport _ \"unsafe\"\n",
		"excluded.go":        "//go:build never_enabled\n\npackage m\nimport _ \"example.com/m/missing\"\n",
		"excluded_plan9.go":  "package m\nimport _ \"example.com/m/missing\"\n",
		"cgo.go":             "package m\nimport \"C\"\n",
		"inside_test.go":     "package m\nimport _ \"example.com/m/missing\"\n",
		"outside_test.go":    "package m_test\nimport _ \"example.com/m/missing\"\n",
		"_skip.go":           "package m\n",
		".skip.go":           "package m\n",
		"testdata/hidden.go": "package hidden\n",
		"_hidden/a.go":       "package hidden\n",
		".hidden/a.go":       "package hidden\n",
		"nested/go.mod":      "module example.com/nested\n\ngo 1.27\n",
		"nested/a.go":        "package nested\n",
	}
	r, out := runNativeFixture(t, t.TempDir(), files)
	want := map[string][]string{}
	for name := range files {
		if strings.HasSuffix(name, ".go") {
			want[name] = []string{"unsupported_syntax"}
		}
	}
	want["zero.go"], want["imports.go"] = []string{}, []string{}
	assertFileCoverage(t, r, out, want)
	if len(out.Nodes) != 0 || len(out.Edges) != 0 {
		t.Fatalf("excluded/standard-only files made causal output: %v %v", out.Nodes, out.Edges)
	}
}

func TestGoOutsideTreeAndIncompleteStayUnknown(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(root, "owned", "corpus_outside")
	writeFixtureFile(t, filepath.Join(outside, "go.mod"), []byte("module example.com/outside\n\ngo 1.27\n"))
	writeFixtureFile(t, filepath.Join(outside, "outside.go"), []byte("package outside\n"))
	files := map[string]string{
		"go.mod":           "module example.com/m\n\ngo 1.27\nrequire (\nexample.com/outside v0.0.0\nexample.com/inside v0.0.0\nexample.com/unavailable v1.0.0\n)\nreplace example.com/outside => " + strconvQuote(filepath.ToSlash(outside)) + "\nreplace example.com/inside => ./inside\n",
		"outside/a.go":     "package outside\nimport (_ \"example.com/outside\"; _ \"example.com/m/good\")\n",
		"inside/go.mod":    "module example.com/inside\n\ngo 1.27\n",
		"inside/inside.go": "package inside\n",
		"consumer/a.go":    "package consumer\nimport _ \"example.com/inside\"\n",
		"unavailable/a.go": "package unavailable\nimport _ \"example.com/unavailable\"\n",
		"missing/a.go":     "package missing\nimport _ \"example.com/m/notpresent\"\n",
		"transitive/a.go":  "package transitive\nimport _ \"example.com/m/missing\"\n",
		"broken/a.go":      "this is not a Go header\n",
		"cyclea/a.go":      "package cyclea\nimport _ \"example.com/m/cycleb\"\n",
		"cycleb/b.go":      "package cycleb\nimport _ \"example.com/m/cyclea\"\n",
		"good/good.go":     "package good\n",
		// A lexical-prefix containment bug can falsely bind the outside file
		// to this hidden corpus path; it must remain explicitly unselected.
		"_outside/outside.go": "package outside\n",
	}
	r, out := runNativeFixture(t, filepath.Join(root, "owned"), files)
	assertFileCoverage(t, r, out, map[string][]string{
		"outside/a.go":     {"unresolved_reference"},
		"inside/inside.go": {}, "consumer/a.go": {}, "good/good.go": {},
		"unavailable/a.go": {"unresolved_reference"}, "missing/a.go": {"unresolved_reference"}, "transitive/a.go": {"unresolved_reference"},
		"broken/a.go":         {"unsupported_syntax"},
		"_outside/outside.go": {"unsupported_syntax"},
	})
	for _, path := range []string{"cyclea/a.go", "cycleb/b.go"} {
		c := coverageFor(t, r, out, path)
		if c.State != "unknown" || !slices.Contains(c.Reasons, "unresolved_reference") && !slices.Contains(c.Reasons, "unsupported_syntax") {
			t.Fatalf("import cycle became complete: %+v", c)
		}
	}
	if len(out.Nodes) != 1 || out.Nodes[0].Key.LocalKey != `["go","workspace","example.com/inside"]` || len(out.Edges) != 1 || out.Edges[0].To != artifactKey(r.Source.WorkspaceID, 2, "consumer/a.go") {
		t.Fatalf("outside/incomplete/partial file admitted a graph fact: nodes=%v edges=%v", out.Nodes, out.Edges)
	}
}

func coverageFor(t *testing.T, r request, out response, name string) coverage {
	t.Helper()
	scope := canonical(artifactKey(r.Source.WorkspaceID, 2, name))
	for _, c := range out.Coverage {
		if c.Scope == scope {
			return c
		}
	}
	t.Fatalf("missing file coverage for %s: %+v", name, out.Coverage)
	return coverage{}
}

func assertFileCoverage(t *testing.T, r request, out response, want map[string][]string) {
	t.Helper()
	for name, reasons := range want {
		c := coverageFor(t, r, out, name)
		state := "complete"
		if len(reasons) != 0 {
			state = "unknown"
		}
		if c.Adapter != "go" || c.State != state || !slices.Equal(c.Reasons, reasons) {
			t.Errorf("%s: %+v; want %s %v", name, c, state, reasons)
		}
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// These deliberately inconsistent public loader records exercise the trust
// boundary against real filesystem objects, not a replacement native resolver.
func standardContainmentResult(t *testing.T, standardRoot, directory, file string) (request, response) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := requestFixture(root, map[string]string{"consumer.go": "package consumer\nimport _ \"standard\"\n"})
	consumerFile := r.Corpus[0]
	writeFixtureFile(t, filepath.Join(r.MaterializationRoot, consumerFile.Path), consumerFile.Bytes)
	dependency := &packages.Package{ID: "standard", PkgPath: "standard", Dir: directory, GoFiles: []string{file}}
	consumer := &packages.Package{ID: "example.test/consumer", PkgPath: "example.test/consumer", Dir: r.MaterializationRoot, GoFiles: []string{filepath.Join(r.MaterializationRoot, consumerFile.Path)}, Module: &packages.Module{Path: "example.test/consumer"}, Imports: map[string]*packages.Package{"standard": dependency}}
	resolve := resolver{r: r, root: r.MaterializationRoot, standard: standardRoot, work: &budget{limit: maxItems}, files: map[string]artifact{consumerFile.Path: consumerFile}, selected: make(map[string]*packageInfo), packages: make(map[*packages.Package]*packageInfo), byID: make(map[string]*packageInfo), byPath: make(map[string]*packageInfo)}
	if err := resolve.collect(t.Context(), []*packages.Package{consumer}); err != nil {
		t.Fatal(err)
	}
	for _, info := range resolve.packages {
		if err := resolve.closure(t.Context(), info); err != nil {
			t.Fatal(err)
		}
	}
	out, err := resolve.result(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 0 || len(out.Edges) != 0 {
		t.Fatalf("standard exemption manufactured causal facts: %+v", out)
	}
	return r, out
}

func TestGoStandardFilesAreDirectRegularChildren(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"direct", "sibling", "directory"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			standardRoot := filepath.Join(t.TempDir(), "src")
			directory := filepath.Join(standardRoot, "standard")
			writeFixtureFile(t, filepath.Join(directory, "valid.go"), []byte("package standard\n"))
			file := filepath.Join(directory, "valid.go")
			switch kind {
			case "sibling":
				file = filepath.Join(standardRoot, "sibling", "other.go")
				writeFixtureFile(t, file, []byte("package sibling\n"))
			case "directory":
				file = filepath.Join(directory, "directory.go")
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			}
			r, out := standardContainmentResult(t, standardRoot, directory, file)
			reasons := []string{}
			if kind != "direct" {
				reasons = []string{"unresolved_reference"}
			}
			assertFileCoverage(t, r, out, map[string][]string{"consumer.go": reasons})
		})
	}
}

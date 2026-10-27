package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// modulePath is the root module's path (D43).
const modulePath = "github.com/Darius-PJ/dwarf-harness"

// target is an operating system and architecture the module is built for
// (step 8) and whose files the import-boundary step reads.
type target struct{ goos, goarch string }

func (t target) String() string { return t.goos + "/" + t.goarch }

// targets are the supported builds (D27, D42). The boundary step lists the
// module once per target, because go list sees only the files whose build
// constraints match its GOOS and GOARCH.
var targets = []target{
	{"windows", "amd64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

// row is one row of the import table: a package tree and what its packages
// may import besides the standard library and their own tree.
type row struct {
	tree     string   // module-relative pattern
	trees    []string // module-relative patterns
	external []string // import-path patterns outside the module
}

// importTable is D45's import table with D50's appender rule, encoded as
// recorded in B17. A pattern ending in "/..." matches its base and every path
// below it; any other pattern matches one path. Every exception or toolkit
// added to a row cites the decision that records it (D29, D45).
var importTable = []row{
	{tree: "contract/..."},
	{tree: "internal/undercurrent/...", trees: []string{"contract/..."}},
	{tree: "internal/router/...", trees: []string{"contract/..."}, external: []string{"golang.org/x/..."}},
	{
		tree:     "internal/core/...",
		trees:    []string{"contract/...", "internal/router/...", "internal/undercurrent/..."},
		external: []string{"golang.org/x/..."},
	},
	{tree: "cmd/dwarf-harness", trees: []string{"internal/core/...", "contract/..."}},
	// The tool dependencies pinned by go.mod tool lines (D42).
	{tree: "tools/...", external: []string{"golang.org/x/...", "honnef.co/go/tools/...", "golang.org/x/vuln/..."}},
	// D132 admits only this program and this direct external import.
	{tree: "helpers/go-analyzer", external: []string{"golang.org/x/tools/go/packages"}},
	{tree: "gui/...", trees: []string{"contract/..."}},
}

// testOnlyImports are the recorded test-only dependencies, which test files of
// every tree may import (D29, D42). None are recorded yet.
var testOnlyImports []string

// appender is the restricted append contract. Only appenderUsers and the
// appender's own test variants may import it, whatever the rows allow (D50).
const appender = "contract/ledger/appender"

var appenderUsers = []string{"internal/core/...", "internal/router/...", "internal/undercurrent/graph/..."}

// xSys is restricted to these importers regardless of the table's rows (D151).
const xSys = "golang.org/x/sys/..."

var xSysUsers = []string{
	"internal/core/durable",     // B3, B18 item 7.
	"internal/core/winexec",     // B30 item 4.
	"internal/core/credentials", // D151.
}

var xSysTestUsers = []string{
	"internal/core/agents", // D151: native credential tests only.
}

func match(pattern, path string) bool {
	if base, ok := strings.CutSuffix(pattern, "/..."); ok {
		return path == base || strings.HasPrefix(path, base+"/")
	}
	return path == pattern
}

func matchAny(patterns []string, path string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return match(p, path) })
}

// moduleRel returns path relative to the root module, and whether path is in
// the root module.
func moduleRel(path string) (string, bool) {
	if path == modulePath {
		return "", true
	}
	rel, ok := strings.CutPrefix(path, modulePath+"/")
	return rel, ok
}

func rowFor(rel string) *row {
	for i := range importTable {
		if match(importTable[i].tree, rel) {
			return &importTable[i]
		}
	}
	return nil
}

// allows reports whether a package of r, whose path or package under test is
// rel, may import imp, a non-standard import path. test marks a test variant.
func (r *row) allows(rel string, test bool, imp string) bool {
	if irel, ok := moduleRel(imp); ok {
		if irel == appender && rel != appender && !matchAny(appenderUsers, rel) {
			return false
		}
		return match(r.tree, irel) || matchAny(r.trees, irel)
	}
	if match(xSys, imp) && !matchAny(xSysUsers, rel) && !(test && matchAny(xSysTestUsers, rel)) {
		return false
	}
	return matchAny(r.external, imp) || test && matchAny(testOnlyImports, imp)
}

type violationKind int

const (
	forbiddenImport violationKind = iota
	unknownTree
	cgoFile
)

// violation is one breach of the import rules.
type violation struct {
	kind    violationKind
	pkg     string // the offending package, without any " [P.test]" suffix
	subject string // the forbidden import or the cgo file; empty for unknownTree
}

func (v violation) String() string {
	switch v.kind {
	case unknownTree:
		return v.pkg + ": package is in no tree of the import table (D45)"
	case cgoFile:
		return v.pkg + ": " + v.subject + " uses cgo; the core never uses cgo (D28)"
	}
	if rel, _ := moduleRel(v.subject); rel == appender {
		return fmt.Sprintf("%s imports %s: only %s may import it (D50)", v.pkg, v.subject, strings.Join(appenderUsers, ", "))
	}
	if match(xSys, v.subject) {
		return fmt.Sprintf("%s imports %s: only %s and test variants of %s may import it (D151)",
			v.pkg, v.subject, strings.Join(xSysUsers, ", "), strings.Join(xSysTestUsers, ", "))
	}
	return fmt.Sprintf("%s imports %s: its row of the import table does not allow it (D45)", v.pkg, v.subject)
}

// finding is a violation and the targets on which it appeared.
type finding struct {
	violation
	targets []string
}

func (f finding) String() string {
	return f.violation.String() + " [" + strings.Join(f.targets, ", ") + "]"
}

// listedPackage holds the fields of go list -json that the check reads.
type listedPackage struct {
	ImportPath string
	Name       string
	ForTest    string
	Standard   bool
	Module     *struct{ Main bool }
	CgoFiles   []string
	Imports    []string
}

// variantBase strips the " [P.test]" suffix of a package recompiled for a test.
func variantBase(importPath string) string {
	base, _, _ := strings.Cut(importPath, " [")
	return base
}

// checkImports returns the violations among the root module's packages in
// one go list -deps -test listing, and how many packages and test variants it
// checked.
func checkImports(pkgs []listedPackage) ([]violation, int) {
	standard := map[string]bool{}
	for _, p := range pkgs {
		if p.Standard {
			standard[p.ImportPath] = true
		}
	}

	var found []violation
	seen := map[violation]bool{}
	add := func(v violation) {
		if !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	}
	checked := 0
	for _, p := range pkgs {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		pkg := variantBase(p.ImportPath)
		// The synthesized main package of a test binary, P.test.
		if p.Name == "main" && p.ForTest == "" && strings.HasSuffix(pkg, ".test") {
			continue
		}
		checked++

		// P [P.test] and P_test [P.test] are test variants of P and take P's
		// row. Any other Q [P.test] is Q recompiled for P's test binary.
		underTest, test := pkg, false
		if p.ForTest != "" && (pkg == p.ForTest || pkg == p.ForTest+"_test") {
			underTest, test = p.ForTest, true
		}

		for _, f := range p.CgoFiles {
			add(violation{kind: cgoFile, pkg: pkg, subject: f})
		}
		rel, _ := moduleRel(underTest)
		r := rowFor(rel)
		if r == nil {
			add(violation{kind: unknownTree, pkg: pkg})
			continue
		}
		for _, imp := range p.Imports {
			// "C" is cgo's pseudo-package, reported above as a cgo file.
			if imp == "C" || standard[imp] {
				continue
			}
			if path := variantBase(imp); !r.allows(rel, test, path) {
				add(violation{kind: forbiddenImport, pkg: pkg, subject: path})
			}
		}
	}
	return found, checked
}

// listPackages runs go list -deps -test -json ./... in dir for t. cgo is on,
// which needs no C compiler for metadata, so that cgo files are listed as
// CgoFiles instead of being ignored. env holds further overrides.
func listPackages(w io.Writer, dir string, t target, env ...string) ([]listedPackage, error) {
	c := goCommand(dir, "1", "list", "-deps", "-test", "-json", "./...").
		with("GOOS="+t.goos, "GOARCH="+t.goarch).
		with(env...)
	out, err := capture(w, c)
	if err != nil {
		return nil, err
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if err == io.EOF {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// boundaryFindings checks the module in dir on every target and returns each
// violation once, with the targets where it appeared, sorted by package.
// A target whose listing fails is reported in the error; the others are
// still checked.
func boundaryFindings(w io.Writer, dir string, env ...string) ([]finding, error) {
	var found []finding
	index := map[violation]int{}
	var errs []error
	for _, t := range targets {
		pkgs, err := listPackages(w, dir, t, env...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t, err))
			continue
		}
		vs, checked := checkImports(pkgs)
		fmt.Fprintf(w, "%s: %d packages and test variants of the module checked, %d violations\n", t, checked, len(vs))
		for _, v := range vs {
			i, ok := index[v]
			if !ok {
				i = len(found)
				index[v] = i
				found = append(found, finding{violation: v})
			}
			found[i].targets = append(found[i].targets, t.String())
		}
	}
	slices.SortFunc(found, func(a, b finding) int {
		return cmp.Or(strings.Compare(a.pkg, b.pkg), cmp.Compare(a.kind, b.kind), strings.Compare(a.subject, b.subject))
	})
	return found, errors.Join(errs...)
}

// checkBoundary is step 5.
func checkBoundary(w io.Writer, root string) error {
	found, err := boundaryFindings(w, root)
	for _, f := range found {
		fmt.Fprintln(w, f)
	}
	if len(found) > 0 {
		err = errors.Join(err, fmt.Errorf("%d import-boundary violations", len(found)))
	}
	return err
}

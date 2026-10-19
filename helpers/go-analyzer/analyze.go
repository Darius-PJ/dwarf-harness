package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"
)

type packageInfo struct {
	pkg        *packages.Package
	members    []artifact
	inTree     bool
	standard   bool
	localError bool
	unresolved bool
	conflict   bool
	visit      uint8
	module     *node
}

func (p *packageInfo) incomplete() bool { return p.localError || p.unresolved || p.conflict }

type resolver struct {
	r            request
	root         string
	standard     string
	work         *budget
	files        map[string]artifact
	selected     map[string]*packageInfo
	packages     map[*packages.Package]*packageInfo
	byID         map[string]*packageInfo
	byPath       map[string]*packageInfo
	standardDirs map[string]bool
}

func analyze(ctx context.Context, r request, env []string, diagnostics io.Writer, work *budget) (response, error) {
	if err := work.charge(1); err != nil {
		return response{}, err
	}
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:  r.MaterializationRoot, Context: ctx, Env: env, BuildFlags: nil, Tests: false, Overlay: nil,
		Logf: func(format string, args ...any) { fmt.Fprintf(diagnostics, format+"\n", args...) },
	}, "./...")
	if err != nil {
		return response{}, err
	}
	root, err := filepath.EvalSymlinks(r.MaterializationRoot)
	if err != nil {
		return scopeUnknown(r, "source_unavailable"), nil
	}
	standard := filepath.Join(r.Go.GOROOT, "src")
	resolve := resolver{r: r, root: root, standard: standard, work: work, files: make(map[string]artifact), selected: make(map[string]*packageInfo), packages: make(map[*packages.Package]*packageInfo), byID: make(map[string]*packageInfo), byPath: make(map[string]*packageInfo)}
	for _, file := range r.Corpus {
		if err := work.charge(1); err != nil {
			return response{}, err
		}
		resolve.files[file.Path] = file
	}
	if err := resolve.collect(ctx, loaded); err != nil {
		return response{}, err
	}
	for _, info := range resolve.packages {
		if err := resolve.closure(ctx, info); err != nil {
			return response{}, err
		}
	}
	return resolve.result(ctx)
}

func (r *resolver) collect(ctx context.Context, roots []*packages.Package) error {
	if err := r.work.charge(int64(len(roots))); err != nil {
		return err
	}
	pending := slices.Clone(roots)
	for len(pending) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.work.charge(1); err != nil {
			return err
		}
		pkg := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if pkg == nil || r.packages[pkg] != nil {
			continue
		}
		info := &packageInfo{pkg: pkg, localError: len(pkg.Errors) != 0}
		r.packages[pkg] = info
		if pkg.ID == "" || !nameValid(pkg.PkgPath) {
			info.unresolved = true
		}
		if previous := r.byID[pkg.ID]; previous != nil && previous.pkg != pkg {
			info.conflict, previous.conflict = true, true
		}
		if previous := r.byPath[pkg.PkgPath]; previous != nil && previous.pkg != pkg {
			info.conflict, previous.conflict = true, true
		}
		r.byID[pkg.ID], r.byPath[pkg.PkgPath] = info, info
		if err := r.classify(info); err != nil {
			return err
		}
		if err := r.work.charge(int64(len(pkg.Imports))); err != nil {
			return err
		}
		for _, dependency := range pkg.Imports {
			pending = append(pending, dependency)
		}
	}
	return nil
}

func (r *resolver) classify(info *packageInfo) error {
	p := info.pkg
	inTree := false
	if inside(r.root, p.Dir) {
		if err := r.work.charge(1 + int64(strings.Count(p.Dir, string(filepath.Separator)))); err != nil {
			return err
		}
		_, inTree = resolvedWithin(r.root, p.Dir, true)
	}
	standardName, inStandard, err := r.standardDirectory(p.Dir)
	if err != nil {
		return err
	}
	info.inTree = inTree && len(p.GoFiles) != 0
	info.standard = !inTree && inStandard && p.Module == nil && p.PkgPath == standardName && len(p.Errors) == 0
	if !info.inTree && !info.standard {
		info.unresolved = true
	}
	seen := make(map[string]bool)
	standardPackage := info.standard
	for _, name := range p.GoFiles {
		visits := int64(1)
		if inTree {
			visits += int64(strings.Count(name, string(filepath.Separator)))
		}
		if err := r.work.charge(visits); err != nil {
			return err
		}
		if standardPackage {
			if !regularDirectChild(p.Dir, name) {
				info.standard, info.unresolved = false, true
			}
			continue
		}
		if !inTree {
			continue
		}
		relative, ok := resolvedWithin(r.root, name, false)
		file, present := r.files[relative]
		if !ok || !present || !strings.HasSuffix(relative, ".go") || seen[relative] {
			info.unresolved = true
			continue
		}
		seen[relative] = true
		info.members = append(info.members, file)
		if prior, selected := r.selected[relative]; selected {
			info.conflict = true
			if prior != nil {
				prior.conflict = true
			}
			r.selected[relative] = nil
		} else {
			r.selected[relative] = info
		}
	}
	for _, name := range p.IgnoredFiles {
		if err := r.work.charge(1); err != nil {
			return err
		}
		if strings.HasSuffix(name, ".go") {
			if relative, err := filepath.Rel(r.root, name); err == nil && seen[filepath.ToSlash(relative)] {
				info.conflict = true
			}
		}
	}
	visitedModules := make(map[*packages.Module]bool)
	for module := p.Module; module != nil; module = module.Replace {
		if err := r.work.charge(1); err != nil {
			return err
		}
		if visitedModules[module] {
			info.conflict = true
			break
		}
		visitedModules[module] = true
		if module.Error != nil {
			info.unresolved = true
		}
	}
	return nil
}

// Directory checks are invocation-local: shared ancestors are visited and
// charged once, and every component, including GOROOT/src, must be a real
// directory. In particular, a Windows junction is not an ordinary directory.
func (r *resolver) standardDirectory(name string) (string, bool, error) {
	if name != filepath.Clean(name) || !inside(r.standard, name) {
		return "", false, nil
	}
	relative, err := filepath.Rel(r.standard, name)
	if err != nil {
		return "", false, nil
	}
	paths := []string{r.standard}
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			paths = append(paths, filepath.Join(paths[len(paths)-1], part))
		}
	}
	if r.standardDirs == nil {
		r.standardDirs = make(map[string]bool)
	}
	for _, path := range paths {
		valid, cached := r.standardDirs[path]
		if !cached {
			if err := r.work.charge(1); err != nil {
				return "", false, err
			}
			info, err := os.Lstat(path)
			valid = err == nil && info.Mode().Type() == os.ModeDir
			r.standardDirs[path] = valid
		}
		if !valid {
			return "", false, nil
		}
	}
	return filepath.ToSlash(relative), true, nil
}

func regularDirectChild(directory, name string) bool {
	if filepath.Dir(name) != directory || name != filepath.Join(directory, filepath.Base(name)) {
		return false
	}
	info, err := os.Lstat(name)
	return err == nil && info.Mode().IsRegular()
}

// closure also traverses standard packages: a standard parent's public Errors
// may be empty while a non-exempt dependency is incomplete. Only a package
// itself meeting the pinned-root exception may have an empty GoFiles list.
func (r *resolver) closure(ctx context.Context, info *packageInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.work.charge(1); err != nil {
		return err
	}
	if info.visit == 2 {
		return nil
	}
	if info.visit == 1 {
		info.unresolved = true
		return nil
	}
	info.visit = 1
	for _, pkg := range info.pkg.Imports {
		if err := r.work.charge(1); err != nil {
			return err
		}
		dependency := r.packages[pkg]
		if dependency == nil {
			info.unresolved = true
			continue
		}
		if err := r.closure(ctx, dependency); err != nil {
			return err
		}
		if dependency.incomplete() {
			info.unresolved = true
		}
	}
	info.visit = 2
	return nil
}

func (r *resolver) result(ctx context.Context) (response, error) {
	out := emptyResponse(r.r)
	nodes := make(map[string]node)
	edges := make(map[string]edge)
	for _, file := range r.r.Corpus {
		if err := ctx.Err(); err != nil {
			return response{}, err
		}
		if err := r.work.charge(1); err != nil {
			return response{}, err
		}
		if !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		state := coverage{Scope: canonical(file.Key), Adapter: "go", State: "unknown", Reasons: []string{"unsupported_syntax"}}
		owner := r.selected[file.Path]
		if owner != nil {
			localNodes, localEdges, reasons, err := r.fileImports(file, owner)
			if err != nil {
				return response{}, err
			}
			state.Reasons = reasons
			if len(reasons) == 0 {
				state.State = "complete"
				if err := r.work.charge(int64(len(localNodes) + len(localEdges))); err != nil {
					return response{}, err
				}
				for key, n := range localNodes {
					nodes[key] = n
				}
				for key, e := range localEdges {
					if old, exists := edges[key]; !exists || canonical(e.Evidence) < canonical(old.Evidence) {
						edges[key] = e
					}
				}
			}
		}
		if err := r.work.charge(1); err != nil {
			return response{}, err
		}
		out.Coverage = append(out.Coverage, state)
		if len(nodes)+len(edges)+len(out.Coverage) > maxItems {
			return response{}, errLimit
		}
	}
	if err := r.work.charge(int64(len(nodes)+len(edges)) + sortingWork(len(nodes)) + sortingWork(len(edges)) + sortingWork(len(out.Coverage))); err != nil {
		return response{}, err
	}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, n)
	}
	for _, e := range edges {
		out.Edges = append(out.Edges, e)
	}
	slices.SortFunc(out.Nodes, func(a, b node) int { return strings.Compare(canonical(a.Key), canonical(b.Key)) })
	slices.SortFunc(out.Edges, func(a, b edge) int { return strings.Compare(a.EdgeID, b.EdgeID) })
	slices.SortFunc(out.Coverage, func(a, b coverage) int { return strings.Compare(a.Scope, b.Scope) })
	return out, nil
}

func (r *resolver) fileImports(file artifact, owner *packageInfo) (map[string]node, map[string]edge, []string, error) {
	if err := r.work.charge(1 + byteUnits(len(file.Bytes))); err != nil {
		return nil, nil, nil, err
	}
	local, unresolved := owner.localError, owner.unresolved || owner.conflict
	parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Bytes, parser.ImportsOnly)
	if err != nil {
		local = true
	}
	nodes := make(map[string]node)
	edges := make(map[string]edge)
	if parsed != nil {
		for _, declaration := range parsed.Imports {
			if err := r.work.charge(1); err != nil {
				return nil, nil, nil, err
			}
			literal, err := strconv.Unquote(declaration.Path.Value)
			if err != nil || literal == "C" || !nameValid(literal) {
				local = true
				continue
			}
			dependency := r.packages[owner.pkg.Imports[literal]]
			if dependency == nil || dependency.incomplete() {
				unresolved = true
				continue
			}
			if dependency.standard {
				continue
			}
			if !dependency.inTree || len(dependency.members) != len(dependency.pkg.GoFiles) {
				unresolved = true
				continue
			}
			n, err := r.moduleNode(dependency)
			if err != nil {
				return nil, nil, nil, err
			}
			if n == nil {
				unresolved = true
				continue
			}
			e := edge{From: n.Key, To: file.Key, Type: "static_dependency", Geometry: "coupling", WeightPPM: 1000000, Evidence: evidence{Artifact: file.Key, ArtifactSHA256: file.SHA256, Adapter: "go", ConfigurationSHA256: r.r.ConfigurationSHA256, Reference: literal}}
			e.EdgeID = hashBytes([]byte(canonical([3]any{e.From, e.To, e.Type})))
			if err := r.work.charge(2); err != nil {
				return nil, nil, nil, err
			}
			nodes[canonical(n.Key)] = *n
			if old, exists := edges[e.EdgeID]; !exists || canonical(e.Evidence) < canonical(old.Evidence) {
				edges[e.EdgeID] = e
			}
		}
	}
	reasons := []string{}
	if unresolved {
		reasons = append(reasons, "unresolved_reference")
	}
	if local {
		reasons = append(reasons, "unsupported_syntax")
	}
	return nodes, edges, reasons, nil
}

func (r *resolver) moduleNode(info *packageInfo) (*node, error) {
	if info.module != nil {
		return info.module, nil
	}
	if err := r.work.charge(int64(len(info.members)) + sortingWork(len(info.members))); err != nil {
		return nil, err
	}
	members := slices.Clone(info.members)
	sortCorpus(members)
	pairs := make([][2]string, 0, len(members))
	for _, file := range members {
		pairs = append(pairs, [2]string{file.Path, file.SHA256})
	}
	key := nodeKey{"workspace", r.r.Source.WorkspaceID, "module", canonical([3]string{"go", "workspace", info.pkg.PkgPath})}
	if len(key.LocalKey) > 4096 {
		return nil, nil
	}
	encoded := canonical(struct {
		Name    string      `json:"module_name"`
		Members [][2]string `json:"members"`
	}{info.pkg.PkgPath, pairs})
	if err := r.work.charge(1 + byteUnits(len(encoded))); err != nil {
		return nil, err
	}
	info.module = &node{Key: key, VersionSHA256: hashBytes([]byte(encoded)), Adapter: "go", ConfigurationSHA256: r.r.ConfigurationSHA256}
	return info.module, nil
}

func sortingWork(n int) int64 { return int64(n) * int64(bits.Len(uint(n))+1) }

func nameValid(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func inside(root, name string) bool {
	if root == "" || name == "" || !filepath.IsAbs(root) || !filepath.IsAbs(name) {
		return false
	}
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func noSymlink(root, name string) bool {
	if !inside(root, name) {
		return false
	}
	relative, err := filepath.Rel(root, name)
	if err != nil {
		return false
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func resolvedWithin(root, name string, directory bool) (string, bool) {
	if !noSymlink(root, name) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || !inside(root, resolved) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", false
	}
	relative, err := filepath.Rel(root, resolved)
	return filepath.ToSlash(relative), err == nil
}

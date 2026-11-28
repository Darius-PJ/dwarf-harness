package validation

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type concreteVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type environment struct {
	program   string
	variables []concreteVariable
	values    []string
	digest    ledger.Digest
}

func (r *Runner) verifyEnvironment(ctx context.Context, spec ledger.EnvironmentSpec, dependencies string) ([]ledger.ValidationFile, error) {
	if spec.OS != runtime.GOOS || spec.Arch != runtime.GOARCH {
		return nil, ledger.ErrUnsupportedValidation
	}
	if !filepath.IsAbs(spec.ToolchainRoot) || !filepath.IsAbs(spec.ExecutablePath) {
		return nil, ledger.ErrInvalid
	}
	for _, v := range spec.Variables {
		if strings.EqualFold(v.Name, "SystemRoot") || strings.EqualFold(v.Name, "WINDIR") {
			if v.Value == nil || *v.Value != os.Getenv(v.Name) {
				return nil, ledger.ErrUnsupportedValidation
			}
		}
	}
	toolchain, err := r.manifest(ctx, spec.ToolchainManifestSHA256)
	if err != nil {
		return nil, err
	}
	if err := verifyTree(ctx, spec.ToolchainRoot, toolchain, r.maxFiles); err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(spec.ToolchainRoot, spec.ExecutablePath)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	path, err := regularPath(spec.ToolchainRoot, rel, false)
	if err != nil {
		return nil, err
	}
	found := false
	for _, f := range toolchain {
		if f.Path == rel && f.SHA256 == spec.ExecutableSHA256 {
			found = true
		}
	}
	if !found {
		return nil, ledger.ErrInvalid
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.GoVersion != spec.ToolchainVersion {
		return nil, ledger.ErrUnsupportedValidation
	}
	files, err := r.manifest(ctx, spec.DependencyManifestSHA256)
	if err != nil {
		return nil, err
	}
	if err := verifyTree(ctx, dependencies, files, r.maxFiles); err != nil {
		return nil, err
	}
	return files, nil
}

func (r *Runner) prepareEnvironment(ctx context.Context, root, subject string, a Attempt, dependencies []ledger.ValidationFile) (environment, error) {
	var result environment
	spec := a.Environment
	cwd := filepath.Join(subject, filepath.FromSlash(a.Check.WorkingDirectory))
	if _, state := verifyFiles(ctx, cwd, spec.LockFiles, r.limits.MaterializedBytes); state != "verified" {
		return result, fmt.Errorf("lock files %s", state)
	}
	declaredSum := false
	for _, f := range spec.LockFiles {
		declaredSum = declaredSum || f.Path == "go.sum"
	}
	if _, err := os.Lstat(filepath.Join(cwd, "go.sum")); err == nil && !declaredSum {
		return result, ledger.ErrInvalid
	}
	if _, err := os.Lstat(filepath.Join(cwd, "go.work")); err == nil {
		return result, ledger.ErrUnsupportedValidation
	} else if !os.IsNotExist(err) {
		return result, err
	}
	// Local replace directives may escape both the subject and pinned module
	// cache. V1 cannot honor their external inputs, so refuses them, not downloads.
	mod, err := os.ReadFile(filepath.Join(cwd, "go.mod"))
	if err != nil {
		return result, err
	}
	for _, line := range strings.Split(string(mod), "\n") {
		line, _, _ = strings.Cut(line, "//")
		if _, replacement, ok := strings.Cut(line, "=>"); ok {
			fields := strings.Fields(replacement)
			if len(fields) != 2 || strings.ContainsAny(replacement, "\"`") {
				return result, ledger.ErrUnsupportedValidation
			}
		}
	}
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		return result, err
	}
	paths := map[string]string{}
	for _, name := range []string{"home", "temp", "gocache", "gopath", "gomodcache", "gotmp"} {
		path := filepath.Join(state, name)
		if err := os.Mkdir(path, 0700); err != nil {
			return result, err
		}
		paths[name] = path
	}
	var total int64
	for _, f := range dependencies {
		if f.Bytes > r.limits.MaterializedBytes-total {
			return result, ledger.ErrTooLarge
		}
		total += f.Bytes
		source, err := regularPath(a.DependencyRoot, f.Path, false)
		if err != nil {
			return result, err
		}
		target, err := regularPath(paths["gomodcache"], f.Path, true)
		if err != nil {
			return result, err
		}
		if err := copyDependency(ctx, source, target, f); err != nil {
			return result, err
		}
	}
	vars := []concreteVariable{
		{"CGO_ENABLED", "0"}, {"GOENV", "off"}, {"GOTOOLCHAIN", "local"}, {"GOFLAGS", ""}, {"GOWORK", "off"}, {"GOPROXY", "off"}, {"GOSUMDB", "off"}, {"GOVCS", "*:off"},
		{"GOOS", spec.OS}, {"GOARCH", spec.Arch}, {"GOMAXPROCS", strconv.FormatInt(spec.GOMAXPROCS, 10)}, {"GOROOT", spec.ToolchainRoot},
		{"GOCACHE", paths["gocache"]}, {"GOPATH", paths["gopath"]}, {"GOMODCACHE", paths["gomodcache"]}, {"GOTMPDIR", paths["gotmp"]}, {"TMP", paths["temp"]}, {"TEMP", paths["temp"]}, {"USERPROFILE", paths["home"]},
	}
	for _, v := range spec.Variables {
		value := paths[v.RunPath]
		if v.Value != nil {
			value = *v.Value
		}
		vars = append(vars, concreteVariable{v.Name, value})
	}
	slices.SortFunc(vars, func(a, b concreteVariable) int { return strings.Compare(a.Name, b.Name) })
	b, err := json.Marshal(struct {
		Schema    int                `json:"schema"`
		Variables []concreteVariable `json:"variables"`
	}{1, vars})
	if err != nil {
		return result, err
	}
	result = environment{program: spec.ExecutablePath, variables: vars, digest: sha256.Sum256(b)}
	for _, v := range vars {
		result.values = append(result.values, v.Name+"="+v.Value)
	}
	return result, nil
}

func copyDependency(ctx context.Context, source, target string, f ledger.ValidationFile) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	var n int64
	var copyErr error
	for n < f.Bytes {
		if copyErr = ctx.Err(); copyErr != nil {
			break
		}
		var copied int64
		copied, copyErr = io.CopyN(io.MultiWriter(out, h), in, min(f.Bytes-n, 64<<10))
		n += copied
		if copyErr != nil {
			break
		}
	}
	if copyErr == nil {
		var extra [1]byte
		if count, err := in.Read(extra[:]); count != 0 || err != io.EOF {
			copyErr = ledger.ErrInvalid
		}
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var sum ledger.Digest
	copy(sum[:], h.Sum(nil))
	if n != f.Bytes || sum != f.SHA256 {
		return ledger.ErrInvalid
	}
	mode := os.FileMode(0600)
	if f.Mode == "100755" {
		mode = 0700
	}
	return os.Chmod(target, mode)
}

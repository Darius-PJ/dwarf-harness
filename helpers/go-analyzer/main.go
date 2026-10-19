package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// Exit 3 is used only before the first native process. Once go has started,
// every return is either a joined bound frame (0) or an unjoined failure (1).
// Runtime panic/fatal exits (2) are never pre-native refusal.
func run(args []string, input io.Reader, output, diagnostics io.Writer) int {
	if len(args) != 2 || args[0] != "--graph-helper" || args[1] != helperVersion {
		return 3
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 3
	}
	r, used, err := readRequest(input, cwd)
	if err != nil || r.Go.HelperVersion != helperVersion {
		return 3
	}
	env := profileEnvironment(r.Go, cwd)
	if !environmentMatches(env) || !telemetryDisabled(cwd) {
		return 3
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	log := &limitedWriter{writer: diagnostics, limit: 64 << 10}
	work := budget{used: used, limit: r.Allowance}
	if err := work.charge(12); err != nil {
		return emit(output, r, scopeUnknown(r, "helper_limit"), &work, start)
	}
	for _, file := range r.Corpus {
		if err := work.charge(int64(2+strings.Count(file.Path, "/")) + byteUnits(len(file.Bytes))); err != nil {
			return emit(output, r, scopeUnknown(r, "helper_limit"), &work, start)
		}
	}
	info, err := os.Stat(r.Go.ToolchainPath)
	if err != nil || !toolMetadataMatches(info, r.Go) {
		return emit(output, r, scopeUnknown(r, "adapter_unavailable"), &work, start)
	}
	if err := checkMaterialization(r, cwd, &work); err != nil {
		reason := "source_unavailable"
		if errors.Is(err, errLimit) {
			reason = "helper_limit"
		}
		return emit(output, r, scopeUnknown(r, reason), &work, start)
	}
	if !slices.ContainsFunc(r.Corpus, func(a artifact) bool { return a.Path == "go.mod" }) {
		return emit(output, r, scopeUnknown(r, "unsupported_syntax"), &work, start)
	}
	if err := work.charge(1); err != nil {
		return emit(output, r, scopeUnknown(r, "helper_limit"), &work, start)
	}
	if current, err := os.Stat(r.Go.ToolchainPath); err != nil || !toolMetadataMatches(current, r.Go) || !os.SameFile(info, current) {
		return emit(output, r, scopeUnknown(r, "adapter_unavailable"), &work, start)
	}
	version := &limitedWriter{limit: 128}
	cmd := exec.CommandContext(ctx, r.Go.ToolchainPath, "version")
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = r.MaterializationRoot, env, version, log
	cmd.WaitDelay = 2 * time.Second
	fmt.Fprintf(log, "starting %s version\n", r.Go.ToolchainPath)
	err = cmd.Run()
	if cmd.Process != nil && (cmd.ProcessState == nil || errors.Is(err, exec.ErrWaitDelay)) {
		return 1
	}
	if err != nil || version.overflow || parseGoVersion(version.data.Bytes(), r.Go.ToolchainVersion) != nil {
		return emit(output, r, scopeUnknown(r, "adapter_unavailable"), &work, start)
	}
	if err := work.charge(byteUnits(version.data.Len())); err != nil {
		return emit(output, r, scopeUnknown(r, "helper_limit"), &work, start)
	}
	if current, err := os.Stat(r.Go.ToolchainPath); err != nil || !toolMetadataMatches(current, r.Go) || !os.SameFile(info, current) {
		return emit(output, r, scopeUnknown(r, "adapter_unavailable"), &work, start)
	}
	out, err := analyze(ctx, r, env, log, &work)
	if err != nil {
		reason := "unsupported_syntax"
		if errors.Is(err, errLimit) || ctx.Err() != nil {
			reason = "helper_limit"
		}
		fmt.Fprintf(log, "native analysis: %v\n", err)
		out = scopeUnknown(r, reason)
	}
	if reason := inspectOwnedDirectory(ctx, cwd, &work, log); reason != "" {
		out = scopeUnknown(r, reason)
	}
	after, err := os.Stat(r.Go.ToolchainPath)
	if err != nil || !toolMetadataMatches(after, r.Go) || !os.SameFile(info, after) {
		out = scopeUnknown(r, "adapter_unavailable")
	}
	return emit(output, r, out, &work, start)
}

func parseGoVersion(output []byte, expected string) error {
	if len(output) > 128 {
		return errRequest
	}
	line := strings.TrimSuffix(string(output), "\n")
	if line != "go version "+expected+" "+runtime.GOOS+"/"+runtime.GOARCH {
		return errRequest
	}
	return nil
}

func toolMetadataMatches(info os.FileInfo, g goProfile) bool {
	return info != nil && info.Mode().IsRegular() && info.Size() == g.ToolchainFileSize && info.ModTime().UnixNano() == g.ToolchainModtimeNS
}

func profileEnvironment(g goProfile, root string) []string {
	values := map[string]string{
		"PATH": filepath.Dir(g.ToolchainPath), "GOROOT": g.GOROOT,
		"GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOPROXY": "off", "GOPACKAGESDRIVER": "off",
		"GOFLAGS": "", "CGO_ENABLED": "0", "GOOS": g.GOOS, "GOARCH": g.GOARCH,
		"GOPATH": filepath.Join(root, "gopath"), "GOMODCACHE": filepath.Join(root, "gomodcache"), "GOCACHE": filepath.Join(root, "gocache"),
		"TEMP": filepath.Join(root, "tmp"), "TMP": filepath.Join(root, "tmp"), "TMPDIR": filepath.Join(root, "tmp"),
		"HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"), "APPDATA": filepath.Join(root, "home"), "LOCALAPPDATA": filepath.Join(root, "home"),
		"GOSUMDB": "off", "GO111MODULE": "on", "LANG": "C", "LC_ALL": "C",
	}
	if runtime.GOOS == "linux" {
		values["XDG_CONFIG_HOME"] = filepath.Join(root, "home")
	}
	for _, name := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(name); value != "" {
			values[name] = value
		}
	}
	out := make([]string, 0, len(values))
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	slices.Sort(out)
	return out
}

func environmentMatches(env []string) bool {
	for _, row := range env {
		name, want, _ := strings.Cut(row, "=")
		if got, present := os.LookupEnv(name); !present || got != want {
			return false
		}
	}
	return true
}

func telemetryMode(root string) string {
	config := filepath.Join(root, "home")
	if runtime.GOOS == "darwin" {
		config = filepath.Join(config, "Library", "Application Support")
	}
	return filepath.Join(config, "go", "telemetry", "mode")
}

func telemetryDisabled(root string) bool {
	name := telemetryMode(root)
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 3 {
		return false
	}
	body, err := os.ReadFile(name)
	return err == nil && bytes.Equal(body, []byte("off"))
}

func checkMaterialization(r request, cwd string, work *budget) error {
	root, err := filepath.EvalSymlinks(r.MaterializationRoot)
	if err != nil {
		return err
	}
	goroot, err := filepath.EvalSymlinks(r.Go.GOROOT)
	if err != nil || inside(goroot, root) || inside(root, goroot) {
		return errRequest
	}
	for _, name := range []string{"corpus", "gopath", "gomodcache", "gocache", "tmp", "home"} {
		if err := work.charge(1); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Join(cwd, name))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errRequest
		}
	}
	for _, item := range r.Corpus {
		if err := work.charge(1 + int64(strings.Count(item.Path, "/")) + byteUnits(len(item.Bytes))); err != nil {
			return err
		}
		name := filepath.Join(root, filepath.FromSlash(item.Path))
		if !noSymlink(root, name) {
			return errRequest
		}
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(item.Bytes)) {
			return errRequest
		}
		body, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(body, item.Bytes) {
			return errRequest
		}
	}
	return nil
}

// limitedWriter retains at most its bound. Diagnostics may be emitted by
// concurrent library queries; truncation never substitutes a response frame.
// A nil writer retains bytes for the small native-version query.
type limitedWriter struct {
	mu       sync.Mutex
	writer   io.Writer
	limit    int
	written  int
	data     bytes.Buffer
	overflow bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	keep := min(n, w.limit-w.written)
	w.overflow = w.overflow || keep < n
	w.written += keep
	if w.writer != nil {
		_, _ = w.writer.Write(p[:keep])
	} else {
		_, _ = w.data.Write(p[:keep])
	}
	return n, nil
}

func emit(output io.Writer, r request, out response, work *budget, start time.Time) int {
	out.WorkUsed = work.used
	out.Cost.WallNS = time.Since(start).Nanoseconds()
	base := work.used
	for {
		body := []byte(canonical(out))
		required := base + byteUnits(len(body)+4)
		if len(body) > maxFrame-4 || len(out.Nodes)+len(out.Edges)+len(out.Coverage) > maxItems || required > r.Allowance {
			out = scopeUnknown(r, "helper_limit")
			out.WorkUsed, out.Cost.WallNS = r.Allowance, time.Since(start).Nanoseconds()
			body = []byte(canonical(out))
			if _, err := output.Write(framed(body)); err != nil {
				return 1
			}
			return 0
		}
		if required == out.WorkUsed {
			if _, err := output.Write(framed(body)); err != nil {
				return 1
			}
			return 0
		}
		out.WorkUsed = required
	}
}

// The envelope remains graph-helper/v1. Stat-only disk measurements charge one
// unit per visited entry, not per byte. Sizes still enforce the temporary cap.
func inspectOwnedDirectory(ctx context.Context, root string, work *budget, diagnostics io.Writer) (reason string) {
	var corpus, cache, scaffold uint64
	defer func() {
		fmt.Fprintf(diagnostics, "owned bytes corpus=%d cache=%d scaffold=%d complete=%t\n", corpus, cache, scaffold, reason == "")
	}()
	if work.charge(1) != nil {
		return "helper_limit"
	}
	pending := []string{root}
	for len(pending) != 0 {
		if ctx.Err() != nil {
			return "helper_limit"
		}
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		info, err := os.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "source_unavailable"
		}
		if info.IsDir() {
			dir, err := os.Open(name)
			if err != nil {
				return "source_unavailable"
			}
			for {
				if ctx.Err() != nil {
					_ = dir.Close()
					return "helper_limit"
				}
				entries, err := dir.ReadDir(1)
				if err == io.EOF {
					break
				}
				if err != nil {
					_ = dir.Close()
					return "source_unavailable"
				}
				if work.charge(1) != nil {
					_ = dir.Close()
					return "helper_limit"
				}
				pending = append(pending, filepath.Join(name, entries[0].Name()))
			}
			if err := dir.Close(); err != nil {
				return "source_unavailable"
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return "source_unavailable"
		}
		size := info.Size()
		if size < 0 {
			return "source_unavailable"
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return "source_unavailable"
		}
		part, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
		switch part {
		case "corpus":
			corpus += uint64(size)
		case "gopath", "gomodcache", "gocache":
			cache += uint64(size)
		default:
			scaffold += uint64(size)
		}
		if corpus+cache+scaffold > 64<<20 {
			return "temporary_cap"
		}
	}
	return ""
}

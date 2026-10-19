package graph

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

const goHelperVersion = "go-analyzer/v1"

// GoToolConfig is a trusted host's opt-in for a D132-qualified helper/toolchain
// tuple. Open validates identities without looking up or launching either file.
// Qualification is external evidence, never a caller-supplied proof boolean.
type GoToolConfig struct {
	Helper    Tool
	Toolchain Tool
	GOROOT    string
	GOOS      string
	GOARCH    string
}

// GoTools carries only process-local, budget-verified tool files. The host must
// qualify this exact profile/target tuple before enabling native Go analysis.
type GoTools struct {
	Helper    *VerifiedTool
	Toolchain *VerifiedTool
	GOROOT    string
	GOOS      string
	GOARCH    string
}

// An unjoined Go helper is a process-lifetime uncertainty, not service state.
// The launch lock makes raising this fence atomic with every check-and-Start.
var analysisFence atomic.Bool
var helperLaunch sync.Mutex

func fenceAnalysis() {
	helperLaunch.Lock()
	analysisFence.Store(true)
	helperLaunch.Unlock()
}

func validGoConfig(c GoToolConfig) bool {
	validTool := func(t Tool) bool {
		return filepath.IsAbs(t.Path) && t.SHA256 != (ledger.Digest{}) && text(t.Version, 128)
	}
	return validTool(c.Helper) && validTool(c.Toolchain) && filepath.IsAbs(c.GOROOT) && c.Toolchain.Path == filepath.Join(filepath.Clean(c.GOROOT), "bin", "go"+goExecutableSuffix()) && goTargetWord(c.GOOS) && goTargetWord(c.GOARCH)
}

func goTargetWord(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func goExecutableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (g *GoTools) config() *GoToolConfig {
	if g == nil {
		return nil
	}
	c := &GoToolConfig{GOROOT: g.GOROOT, GOOS: g.GOOS, GOARCH: g.GOARCH}
	if g.Helper != nil {
		c.Helper = g.Helper.tool
	}
	if g.Toolchain != nil {
		c.Toolchain = g.Toolchain.tool
	}
	return c
}

func (g *GoTools) unchanged() bool {
	return g != nil && g.Helper.unchanged() && g.Toolchain.unchanged() && validGoConfig(*g.config())
}

type goHelperRequest struct {
	Profile            string        `json:"profile"`
	HelperVersion      string        `json:"helper_version"`
	ToolchainPath      string        `json:"toolchain_path"`
	ToolchainSHA256    ledger.Digest `json:"toolchain_sha256"`
	ToolchainVersion   string        `json:"toolchain_version"`
	ToolchainFileSize  int64         `json:"toolchain_file_size"`
	ToolchainModtimeNS int64         `json:"toolchain_modtime_ns"`
	GOROOT             string        `json:"goroot"`
	GOOS               string        `json:"goos"`
	GOARCH             string        `json:"goarch"`
	LoadTimeoutMS      int64         `json:"load_timeout_ms"`
}

func (g *GoTools) request() *goHelperRequest {
	return &goHelperRequest{Profile: "go-packages/v1", HelperVersion: g.Helper.tool.Version, ToolchainPath: g.Toolchain.tool.Path, ToolchainSHA256: g.Toolchain.tool.SHA256, ToolchainVersion: g.Toolchain.tool.Version, ToolchainFileSize: g.Toolchain.info.Size(), ToolchainModtimeNS: g.Toolchain.info.ModTime().UnixNano(), GOROOT: g.GOROOT, GOOS: g.GOOS, GOARCH: g.GOARCH, LoadTimeoutMS: 20000}
}

type goOptions struct {
	Profile          string      `json:"profile"`
	Library          string      `json:"library"`
	HelperVersion    string      `json:"helper_version"`
	ModuleMode       string      `json:"module_mode"`
	Pattern          string      `json:"pattern"`
	LoadMode         []string    `json:"load_mode"`
	BuildFlags       []string    `json:"build_flags"`
	Tests            bool        `json:"tests"`
	Overlay          bool        `json:"overlay"`
	GOROOT           string      `json:"goroot"`
	GOOS             string      `json:"goos"`
	GOARCH           string      `json:"goarch"`
	Environment      [][2]string `json:"environment"`
	Scaffold         [][2]string `json:"scaffold"`
	LoadTimeoutMS    int64       `json:"load_timeout_ms"`
	Classification   string      `json:"classification"`
	UnlistedFiles    string      `json:"unlisted_files"`
	FileImportPolicy string      `json:"file_import_policy"`
}

func goConfiguration(c *GoToolConfig) *goOptions {
	if c == nil {
		return nil
	}
	return &goOptions{
		Profile: "go-packages/v1", Library: "golang.org/x/tools@v0.50.0", HelperVersion: goHelperVersion, ModuleMode: "module", Pattern: "./...",
		LoadMode: []string{"NeedName", "NeedFiles", "NeedImports", "NeedDeps", "NeedModule"}, BuildFlags: []string{},
		GOROOT: c.GOROOT, GOOS: c.GOOS, GOARCH: c.GOARCH, Environment: goEnvironmentRows(*c, "$PRIVATE"), Scaffold: [][2]string{{goTelemetryRelative(), "off"}},
		LoadTimeoutMS: 20000, Classification: "public_package_conservative/v1", UnlistedFiles: "unknown", FileImportPolicy: "all_or_nothing",
	}
}

func goEnvironmentRows(c GoToolConfig, root string) [][2]string {
	join := func(part string) string {
		if root == "$PRIVATE" {
			return path.Join(root, part)
		}
		return filepath.Join(root, part)
	}
	values := map[string]string{
		"PATH": filepath.Dir(c.Toolchain.Path), "GOROOT": c.GOROOT,
		"GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOPROXY": "off", "GOPACKAGESDRIVER": "off",
		"GOFLAGS": "", "GODEBUG": "goindex=0", "CGO_ENABLED": "0", "GOOS": c.GOOS, "GOARCH": c.GOARCH,
		"GOPATH": join("gopath"), "GOMODCACHE": join("gomodcache"), "GOCACHE": join("gocache"),
		"TEMP": join("tmp"), "TMP": join("tmp"), "TMPDIR": join("tmp"),
		"HOME": join("home"), "USERPROFILE": join("home"), "APPDATA": join("home"), "LOCALAPPDATA": join("home"),
		"GOSUMDB": "off", "GO111MODULE": "on", "LANG": "C", "LC_ALL": "C",
	}
	if runtime.GOOS == "linux" {
		values["XDG_CONFIG_HOME"] = join("home")
	}
	for _, name := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(name); value != "" {
			values[name] = value
		}
	}
	rows := make([][2]string, 0, len(values))
	for name, value := range values {
		rows = append(rows, [2]string{name, value})
	}
	slices.SortFunc(rows, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	return rows
}

func goEnvironment(c GoToolConfig, root string) []string {
	rows := goEnvironmentRows(c, root)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row[0]+"="+row[1])
	}
	return out
}

func goTelemetryRelative() string {
	if runtime.GOOS == "darwin" {
		return "home/Library/Application Support/go/telemetry/mode"
	}
	return "home/go/telemetry/mode"
}

// goPreparationWork is charged by the helper as well as preflighted here: it
// covers parent-owned copies and path/scaffold creation before native launch.
func goPreparationWork(corpus []CorpusArtifact) int64 {
	work := int64(12)
	for _, file := range corpus {
		work += int64(2 + strings.Count(file.Path, "/") + (len(file.Bytes)+255)/256)
	}
	return work
}

func prepareGoCorpus(ctx context.Context, root string, corpus []CorpusArtifact) error {
	for _, name := range []string{"gopath", "gomodcache", "gocache", "tmp", "home"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			return err
		}
	}
	mode := filepath.Join(root, filepath.FromSlash(goTelemetryRelative()))
	if err := os.MkdirAll(filepath.Dir(mode), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(mode, []byte("off"), 0600); err != nil {
		return err
	}
	seen := make(map[string]string)
	directories := make(map[string]bool)
	for _, file := range corpus {
		if err := ctx.Err(); err != nil {
			return err
		}
		parts := strings.Split(file.Path, "/")
		for i, part := range parts {
			if runtime.GOOS == "windows" && !goWindowsPart(part) {
				return ErrInvalid
			}
			name := strings.Join(parts[:i+1], "/")
			identity := name
			if runtime.GOOS == "windows" {
				identity = strings.ToLower(name)
			}
			previous, exists := seen[identity]
			isDirectory := i < len(parts)-1
			if exists {
				if previous != name || !isDirectory || !directories[identity] {
					return ErrInvalid
				}
				continue
			}
			seen[identity], directories[identity] = name, isDirectory
			if isDirectory {
				// An unexpected existing directory is an alias, not permission
				// to merge two immutable path identities (including 8.3 names).
				if err := os.Mkdir(filepath.Join(root, "corpus", filepath.FromSlash(name)), 0700); err != nil {
					return err
				}
			}
		}
		name := filepath.Join(root, "corpus", filepath.FromSlash(file.Path))
		output, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := output.Write(file.Bytes)
		closeErr := output.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func goWindowsPart(part string) bool {
	device, _, _ := strings.Cut(strings.ToUpper(part), ".")
	return !strings.ContainsAny(part, ":<>\"|?*") && !strings.HasSuffix(part, ".") && !strings.HasSuffix(part, " ") && device != "CON" && device != "PRN" && device != "AUX" && device != "NUL" && !(len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '0' && device[3] <= '9')
}

func goResolutionInput(name string) bool {
	base := path.Base(name)
	return strings.HasSuffix(name, ".go") || base == "go.mod" || base == "go.sum" || base == "go.work" || name == "vendor" || strings.HasPrefix(name, "vendor/") || strings.Contains(name, "/vendor/")
}

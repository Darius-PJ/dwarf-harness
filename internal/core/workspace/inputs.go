package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxInputBytes = 1 << 20

// HostFile preserves the distinction between an absent and an empty input.
type HostFile struct {
	Present bool
	Bytes   []byte
}

// Normalization records Git's seven imported core settings as printed by Git.
type Normalization struct{ AutoCRLF, EOL, FileMode, IgnoreCase, LongPaths, PrecomposeUnicode, Symlinks string }

// Proposal is the owner's reviewable inspection. Files may be edited before
// approval; registration rechecks the observed directory, version and settings.
type Proposal struct {
	CommonDir, GitVersion, ObjectFormat                                                string
	Normalization                                                                      Normalization
	LocalExcludes, GlobalExcludes, LocalAttributes, GlobalAttributes, SystemAttributes HostFile
}

// Inspect reads the owner's live Git setup without creating a repository or
// changing it. A canonical main checked out anywhere is refused (D48).
func Inspect(ctx context.Context, root string) (Proposal, error) {
	return newGitRunner(nil).inspect(ctx, root)
}

func (g *gitRunner) inspect(ctx context.Context, root string) (Proposal, error) {
	var p Proposal
	if g.unavailable != nil {
		return p, g.unavailable
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	env := inspectionEnvironment(g.environ())
	b, exit, err := g.run(ctx, "", env, nil, []int{0, 128}, "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-object-format")
	if err != nil {
		return p, err
	}
	if exit != 0 {
		return p, ErrNotGitRepository
	}
	common, format, ok := strings.Cut(strings.TrimSuffix(string(b), "\n"), "\n")
	// The object format is the last line; directory names may themselves hold LF.
	if i := strings.LastIndex(strings.TrimSuffix(string(b), "\n"), "\n"); i >= 0 {
		common = string(b[:i])
		format = strings.TrimSuffix(string(b[i+1:]), "\n")
	}
	if !ok || (format != "sha1" && format != "sha256") || !filepath.IsAbs(common) {
		return p, fmt.Errorf("%w: invalid repository description", ErrGit)
	}
	p.CommonDir = common
	p.ObjectFormat = format
	p.GitVersion = g.version
	call := func(args ...string) ([]byte, int, error) {
		return g.run(ctx, "", env, nil, []int{0, 1}, append([]string{"--git-dir=" + p.CommonDir}, args...)...)
	}
	_, exit, err = call("rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/main^{commit}")
	if err != nil {
		return Proposal{}, err
	}
	if exit != 0 {
		return Proposal{}, ErrNoCanonical
	}
	b, _, err = call("worktree", "list", "--porcelain", "-z")
	if err != nil {
		return Proposal{}, err
	}
	entries, err := parseWorktreeList(b, oidLength(format))
	if err != nil {
		return Proposal{}, fmt.Errorf("%w: %v", ErrGit, err)
	}
	if err := canonicalNotCheckedOut(entries); err != nil {
		return Proposal{}, err
	}
	config := func(key, kind, fallback string) (string, error) {
		args := []string{"config", "-z"}
		if kind != "" {
			args = append(args, "--type="+kind)
		}
		args = append(args, "--get", "--end-of-options", key)
		b, exit, err := call(args...)
		if err != nil {
			return "", err
		}
		if exit == 1 {
			return fallback, nil
		}
		fields, err := parseNULList(b)
		if err != nil || len(fields) != 1 {
			return "", fmt.Errorf("%w: invalid config value for %s", ErrGit, key)
		}
		return fields[0], nil
	}
	for _, setting := range []struct {
		key, kind, fallback string
		dst                 *string
	}{
		{"core.autocrlf", "bool-or-str", "false", &p.Normalization.AutoCRLF}, {"core.eol", "", "native", &p.Normalization.EOL},
		{"core.filemode", "bool", "true", &p.Normalization.FileMode}, {"core.ignorecase", "bool", "false", &p.Normalization.IgnoreCase}, {"core.longpaths", "bool", "false", &p.Normalization.LongPaths}, {"core.precomposeunicode", "bool", "false", &p.Normalization.PrecomposeUnicode}, {"core.symlinks", "bool", "true", &p.Normalization.Symlinks},
	} {
		v, e := config(setting.key, setting.kind, setting.fallback)
		if e != nil {
			return Proposal{}, e
		}
		*setting.dst = v
	}
	globalExclude, err := config("core.excludesFile", "path", "")
	if err != nil {
		return Proposal{}, err
	}
	if globalExclude == "" {
		home := environmentValue(env, "HOME")
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return Proposal{}, err
			}
		}
		xdg := environmentValue(env, "XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		globalExclude = filepath.Join(xdg, "git", "ignore")
	}
	attrPath := func(name string) (string, error) {
		b, _, err := call("var", name)
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(string(b), "\n"), nil
	}
	globalAttr, err := attrPath("GIT_ATTR_GLOBAL")
	if err != nil {
		return Proposal{}, err
	}
	systemAttr, err := attrPath("GIT_ATTR_SYSTEM")
	if err != nil {
		return Proposal{}, err
	}
	for _, file := range []struct {
		path string
		dst  *HostFile
	}{{filepath.Join(p.CommonDir, "info", "exclude"), &p.LocalExcludes}, {globalExclude, &p.GlobalExcludes}, {filepath.Join(p.CommonDir, "info", "attributes"), &p.LocalAttributes}, {globalAttr, &p.GlobalAttributes}, {systemAttr, &p.SystemAttributes}} {
		hf, e := readHostFile(file.path)
		if e != nil {
			return Proposal{}, e
		}
		*file.dst = hf
	}
	if _, _, err := encodeCaptureInputs(p, "capture-rules/1"); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

func environmentValue(env []string, key string) string {
	var value string
	for _, s := range env {
		if envKey(s) == key {
			_, value, _ = strings.Cut(s, "=")
		}
	}
	return value
}
func oidLength(format string) int {
	if format == "sha256" {
		return 64
	}
	return 40
}
func canonicalNotCheckedOut(entries []worktreeEntry) error {
	for _, e := range entries {
		if e.Branch == "refs/heads/main" {
			return ErrCanonicalCheckedOut
		}
	}
	return nil
}

func readHostFile(path string) (HostFile, error) {
	if path == "" {
		return HostFile{}, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return HostFile{}, nil
	}
	if err != nil {
		return HostFile{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return HostFile{}, err
	}
	if len(b) > maxInputBytes {
		return HostFile{}, ErrInputsTooLarge
	}
	return HostFile{Present: true, Bytes: b}, nil
}

// inputsDocument is Core-internal. Its order and compact encoding define the
// capture-input identity; past versions must remain decodable forever.
type inputsDocument struct {
	Format            string `json:"format"`
	Version           int    `json:"version"`
	Rules             string `json:"rules"`
	GitVersion        string `json:"git_version"`
	ObjectFormat      string `json:"object_format"`
	AutoCRLF          string `json:"core.autocrlf"`
	EOL               string `json:"core.eol"`
	FileMode          string `json:"core.filemode"`
	IgnoreCase        string `json:"core.ignorecase"`
	LongPaths         string `json:"core.longpaths"`
	PrecomposeUnicode string `json:"core.precomposeunicode"`
	Symlinks          string `json:"core.symlinks"`
	LocalExcludes     string `json:"local_excludes,omitempty"`
	GlobalExcludes    string `json:"global_excludes,omitempty"`
	LocalAttributes   string `json:"local_attributes,omitempty"`
	GlobalAttributes  string `json:"global_attributes,omitempty"`
	SystemAttributes  string `json:"system_attributes,omitempty"`
}

func encodeCaptureInputs(p Proposal, rules string) ([]byte, [32]byte, error) {
	d := inputsDocument{Format: "dwarf-harness/capture-inputs", Version: 1, Rules: rules, GitVersion: p.GitVersion, ObjectFormat: p.ObjectFormat, AutoCRLF: p.Normalization.AutoCRLF, EOL: p.Normalization.EOL, FileMode: p.Normalization.FileMode, IgnoreCase: p.Normalization.IgnoreCase, LongPaths: p.Normalization.LongPaths, PrecomposeUnicode: p.Normalization.PrecomposeUnicode, Symlinks: p.Normalization.Symlinks}
	for _, file := range []struct {
		file HostFile
		dst  *string
	}{{p.LocalExcludes, &d.LocalExcludes}, {p.GlobalExcludes, &d.GlobalExcludes}, {p.LocalAttributes, &d.LocalAttributes}, {p.GlobalAttributes, &d.GlobalAttributes}, {p.SystemAttributes, &d.SystemAttributes}} {
		if len(file.file.Bytes) > maxInputBytes {
			return nil, [32]byte{}, ErrInputsTooLarge
		}
		if !file.file.Present {
			if len(file.file.Bytes) != 0 {
				return nil, [32]byte{}, fmt.Errorf("%w: absent input has bytes", ErrInvalid)
			}
			continue
		}
		sum := sha256.Sum256(file.file.Bytes)
		*file.dst = hex.EncodeToString(sum[:])
	}
	b, err := d.encode()
	if err != nil {
		return nil, [32]byte{}, err
	}
	return b, sha256.Sum256(b), nil
}

func (d inputsDocument) encode() ([]byte, error) {
	if d.Format != "dwarf-harness/capture-inputs" || d.Version != 1 || d.Rules == "" || strings.ContainsAny(d.Rules, "\x00\r\n") {
		return nil, fmt.Errorf("%w: unsupported capture inputs", ErrInvalid)
	}
	if _, err := parseGitVersionAtLeast([]byte("git version "+d.GitVersion), 42); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if d.ObjectFormat != "sha1" && d.ObjectFormat != "sha256" {
		return nil, fmt.Errorf("%w: invalid object format", ErrInvalid)
	}
	if d.AutoCRLF != "true" && d.AutoCRLF != "false" && d.AutoCRLF != "input" {
		return nil, fmt.Errorf("%w: invalid autocrlf", ErrInvalid)
	}
	if d.EOL != "native" && d.EOL != "lf" && d.EOL != "crlf" {
		return nil, fmt.Errorf("%w: invalid eol", ErrInvalid)
	}
	for _, value := range []string{d.FileMode, d.IgnoreCase, d.LongPaths, d.PrecomposeUnicode, d.Symlinks} {
		if value != "true" && value != "false" {
			return nil, fmt.Errorf("%w: invalid boolean setting", ErrInvalid)
		}
	}
	for _, digest := range []string{d.LocalExcludes, d.GlobalExcludes, d.LocalAttributes, d.GlobalAttributes, d.SystemAttributes} {
		if digest != "" && !validOID(digest, 64) {
			return nil, fmt.Errorf("%w: invalid input digest", ErrInvalid)
		}
	}
	return json.Marshal(d)
}

func decodeCaptureInputs(b []byte) (inputsDocument, error) {
	var d inputsDocument
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return inputsDocument{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	canonical, err := d.encode()
	if err != nil {
		return inputsDocument{}, err
	}
	// Besides compact order this rejects duplicate, absent, null and extra keys.
	if !bytes.Equal(b, canonical) {
		return inputsDocument{}, fmt.Errorf("%w: noncanonical capture inputs", ErrInvalid)
	}
	return d, nil
}

func materializeInputs(colony, dir string, p Proposal) error {
	if _, _, err := encodeCaptureInputs(p, "capture-rules/1"); err != nil {
		return err
	}
	if err := prepareGitLayout(colony); err != nil {
		return err
	}
	for _, d := range []string{filepath.Join(dir, "git", "refs"), filepath.Join(dir, "git", "info")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	global := filepath.Join(dir, "global-excludes")
	if !p.GlobalExcludes.Present {
		global = filepath.Join(colony, "workspaces", "empty")
	}
	attributes := append([]byte(nil), p.SystemAttributes.Bytes...)
	if len(attributes) > 0 && attributes[len(attributes)-1] != '\n' {
		attributes = append(attributes, '\n')
	}
	attributes = append(attributes, p.GlobalAttributes.Bytes...)
	var config strings.Builder
	version := "0"
	if p.ObjectFormat == "sha256" {
		version = "1"
	}
	fmt.Fprintf(&config, "[core]\n repositoryformatversion = %s\n bare = false\n", version)
	for _, kv := range [][2]string{{"autocrlf", p.Normalization.AutoCRLF}, {"eol", p.Normalization.EOL}, {"filemode", p.Normalization.FileMode}, {"ignorecase", p.Normalization.IgnoreCase}, {"longpaths", p.Normalization.LongPaths}, {"precomposeunicode", p.Normalization.PrecomposeUnicode}, {"symlinks", p.Normalization.Symlinks}, {"safecrlf", "false"}, {"excludesFile", strconv.Quote(filepath.ToSlash(global))}, {"attributesFile", strconv.Quote(filepath.ToSlash(filepath.Join(dir, "attributes")))}, {"fsmonitor", "false"}, {"untrackedCache", "false"}, {"splitIndex", "false"}, {"quotePath", "false"}, {"fsync", "committed"}, {"fsyncMethod", "fsync"}} {
		fmt.Fprintf(&config, " %s = %s\n", kv[0], kv[1])
	}
	if p.ObjectFormat == "sha256" {
		config.WriteString("[extensions]\n objectFormat = sha256\n")
	}
	for _, file := range []struct {
		name string
		b    []byte
	}{{"git/HEAD", []byte("ref: refs/heads/capture\n")}, {"git/info/exclude", p.LocalExcludes.Bytes}, {"git/info/attributes", p.LocalAttributes.Bytes}, {"git/config", []byte(config.String())}, {"global-excludes", p.GlobalExcludes.Bytes}, {"attributes", attributes}} {
		if err := writePinnedFile(filepath.Join(dir, filepath.FromSlash(file.name)), file.b); err != nil {
			return err
		}
	}
	return nil
}

func writePinnedFile(path string, b []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, b) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".input-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}

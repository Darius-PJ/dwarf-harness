package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// F4-17 configuration formats are frozen (D189).
const (
	WorkspaceChecksFormat   = "dwarf-harness/workspace-checks"
	EnvironmentPolicyFormat = "dwarf-harness/environment-policy"
	FileManifestFormat      = "dwarf-harness/file-manifest"
)

// ErrUnsupportedValidation distinguishes an unknown format, schema or adapter
// from malformed supported input. It never wraps ErrInvalid.
var ErrUnsupportedValidation = errors.New("ledger: unsupported validation format")

// WorkspaceChecks binds the complete required execution order to definitions.
type WorkspaceChecks struct {
	Format      string           `json:"format"`
	Schema      int64            `json:"schema"`
	WorkspaceID uuid.UUID        `json:"workspace_id"`
	Revision    int64            `json:"revision"`
	Required    []string         `json:"required"`
	Checks      []CheckSpec      `json:"checks"`
	Hooks       []ValidationHook `json:"hooks"`
}

type ValidationHook struct {
	HookID string `json:"hook_id"`
	Event  string `json:"event"`
}

// CheckSpec's common identity precedes its adapter's closed member set. V1
// defines only go-test-json/1; Package selects one package, never a pattern.
type CheckSpec struct {
	CheckID          string                    `json:"check_id"`
	Adapter          string                    `json:"adapter"`
	AdapterVersion   int64                     `json:"adapter_version"`
	ExecutableID     string                    `json:"executable_id"`
	Package          string                    `json:"package"`
	WorkingDirectory string                    `json:"working_directory"`
	DefiningFiles    []ValidationFile          `json:"defining_files"`
	Inventory        []ValidationInventoryNode `json:"inventory"`
	Canary           ValidationCanarySpec      `json:"canary"`
	EnvironmentID    string                    `json:"environment_id"`
	NetworkGrantRefs []uuid.UUID               `json:"network_grant_refs"`
	SecretGrantRefs  []uuid.UUID               `json:"secret_grant_refs"`
	WallMS           int64                     `json:"wall_ms"`
	OutputBytes      int64                     `json:"output_bytes"`
	Processes        int64                     `json:"processes"`
}

// ValidationFile describes exact regular-file bytes, not a Git object ID.
type ValidationFile struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	Bytes  int64  `json:"bytes"`
	SHA256 Digest `json:"sha256"`
}

type ValidationInventoryNode struct {
	Package  string `json:"package"`
	Name     string `json:"name"`
	Parent   string `json:"parent,omitempty"`
	Expected string `json:"expected"`
}

type ValidationCanarySpec struct {
	Protocol       string `json:"protocol"`
	PackageName    string `json:"package_name"`
	File           string `json:"file"`
	TestName       string `json:"test_name"`
	TemplateKind   string `json:"template_kind"`
	TemplateSHA256 Digest `json:"template_sha256"`
}

type EnvironmentPolicy struct {
	Format       string            `json:"format"`
	Schema       int64             `json:"schema"`
	WorkspaceID  uuid.UUID         `json:"workspace_id"`
	Revision     int64             `json:"revision"`
	Environments []EnvironmentSpec `json:"environments"`
}

type EnvironmentSpec struct {
	EnvironmentID            string               `json:"environment_id"`
	OS                       string               `json:"os"`
	Arch                     string               `json:"arch"`
	ExecutableID             string               `json:"executable_id"`
	ExecutablePath           string               `json:"executable_path"`
	ExecutableSHA256         Digest               `json:"executable_sha256"`
	ToolchainVersion         string               `json:"toolchain_version"`
	ToolchainRoot            string               `json:"toolchain_root"`
	ToolchainManifestSHA256  Digest               `json:"toolchain_manifest_sha256"`
	DependencyManifestSHA256 Digest               `json:"dependency_manifest_sha256"`
	LockFiles                []ValidationFile     `json:"lock_files"`
	Variables                []ValidationVariable `json:"variables"`
	GOMAXPROCS               int64                `json:"gomaxprocs"`
	MemoryReservationBytes   int64                `json:"memory_reservation_bytes"`
}

// Value is a pointer so an explicitly empty literal is distinct from absence.
type ValidationVariable struct {
	Name    string  `json:"name"`
	Value   *string `json:"value,omitempty"`
	RunPath string  `json:"run_path,omitempty"`
}

type FileManifest struct {
	Format string           `json:"format"`
	Schema int64            `json:"schema"`
	Files  []ValidationFile `json:"files"`
}

func validationClosed(m *members, keys ...string) {
	for key := range m.m {
		if !slices.Contains(keys, key) && m.err == nil {
			m.err = fmt.Errorf("unknown member %s", key)
		}
	}
}

func validationHeader(format string, schema int64, want string) error {
	if format != want || schema != 1 {
		return ErrUnsupportedValidation
	}
	return nil
}

func encodeValidationPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	if errors.Is(invalid, ErrUnsupportedValidation) {
		return nil, invalid
	}
	js, err := encodePayload(typ, p, invalid)
	if err != nil {
		return nil, err
	}
	if len(js) > MaxRecordBytes {
		return nil, fmt.Errorf("%w: %s exceeds record limit", ErrInvalid, typ)
	}
	return js, nil
}

func decodeValidationPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxRecordBytes {
		return fmt.Errorf("%w: %s exceeds record limit", ErrInvalid, typ)
	}
	if err := checkPayload(js); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, typ, err)
	}
	m := members{}
	if err := json.Unmarshal(js, &m.m); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, typ, err)
	}
	err := decode(&m)
	if errors.Is(m.err, ErrUnsupportedValidation) {
		return m.err
	}
	if m.err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, typ, m.err)
	}
	if errors.Is(err, ErrUnsupportedValidation) {
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, typ, err)
	}
	return nil
}

func validationPath(path string) bool {
	return path != "" && ValidResourcePath(path)
}

func validationDirectory(path string) bool {
	return path == "." || validationPath(path)
}

func validationPackage(path string) bool {
	return path == "." || strings.HasPrefix(path, "./") && validationPath(path[2:]) && !strings.Contains(path, "...")
}

func validationFile(f ValidationFile) bool {
	return validationPath(f.Path) && (f.Mode == "100644" || f.Mode == "100755") && f.Bytes >= 0 && f.SHA256 != (Digest{})
}

// A file's full path and every directory component must have one spelling on
// case-insensitive filesystems. Keep this separate from raw-byte row ordering.
func validationFileCase(path string, spellings map[string]string) bool {
	prefix := ""
	for part := range strings.SplitSeq(path, "/") {
		if prefix != "" {
			prefix += "/"
		}
		prefix += part
		key := strings.ToLower(prefix)
		if old, ok := spellings[key]; ok && old != prefix {
			return false
		}
		spellings[key] = prefix
	}
	return true
}

func validationFiles(files []ValidationFile) bool {
	if files == nil {
		return false
	}
	spellings := make(map[string]string)
	for i, f := range files {
		if !validationFile(f) || i > 0 && files[i-1].Path >= f.Path || !validationFileCase(f.Path, spellings) {
			return false
		}
	}
	return true
}

func readValidationFile(m *members) ValidationFile {
	validationClosed(m, "path", "mode", "bytes", "sha256")
	return ValidationFile{m.text("path"), m.text("mode"), m.nat("bytes", true), m.digest("sha256")}
}

func readValidationInventory(m *members) ValidationInventoryNode {
	validationClosed(m, "package", "name", "parent", "expected")
	p := ValidationInventoryNode{Package: m.text("package"), Name: m.text("name"), Expected: m.text("expected")}
	p.Parent, _ = m.optionalText("parent")
	return p
}

func readValidationCanarySpec(m *members) ValidationCanarySpec {
	validationClosed(m, "protocol", "package_name", "file", "test_name", "template_kind", "template_sha256")
	return ValidationCanarySpec{m.text("protocol"), m.text("package_name"), m.text("file"), m.text("test_name"), m.text("template_kind"), m.digest("template_sha256")}
}

func validationText(s string) bool {
	return s != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func validationInventory(nodes []ValidationInventoryNode) bool {
	if len(nodes) == 0 {
		return false
	}
	seen := make(map[string]bool, len(nodes))
	previous := ""
	for _, n := range nodes {
		key := n.Package + "\x00" + n.Name
		if !validationText(n.Package) || !validationText(n.Name) || n.Expected != "passed" || key <= previous {
			return false
		}
		if n.Parent != "" && (!seen[n.Package+"\x00"+n.Parent] || !strings.HasPrefix(n.Name, n.Parent+"/")) {
			return false
		}
		top, _, _ := strings.Cut(n.Name, "/")
		if !token.IsIdentifier(top) || !(strings.HasPrefix(top, "Test") || strings.HasPrefix(top, "Example")) {
			return false
		}
		seen[key] = true
		previous = key
	}
	return true
}

func (p FileManifest) Encode() (json.RawMessage, error) {
	if err := validationHeader(p.Format, p.Schema, FileManifestFormat); err != nil {
		return nil, err
	}
	if !validationFiles(p.Files) {
		return nil, fmt.Errorf("%w: invalid file manifest", ErrInvalid)
	}
	return json.Marshal(p)
}

// LoadFileManifest streams rows without retaining their contents. Rows delivered
// before an error are provisional: the caller must discard them on refusal.
// The caller supplies a finite row cap and bounds the underlying body byte size.
// The returned digest hashes the entire accepted input, including whitespace.
func LoadFileManifest(r io.Reader, maxRows int64, visit func(ValidationFile) error) (Digest, error) {
	if r == nil || maxRows <= 0 || visit == nil {
		return Digest{}, fmt.Errorf("%w: invalid manifest reader, cap or visitor", ErrInvalid)
	}
	hash := sha256.New()
	d := json.NewDecoder(io.TeeReader(r, hash))
	d.UseNumber()
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return Digest{}, fmt.Errorf("%w: manifest object: %v", ErrInvalid, err)
	}
	seen := map[string]bool{}
	format := ""
	var schema int64
	var rows int64
	previous := ""
	spellings := make(map[string]string)
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return Digest{}, fmt.Errorf("%w: manifest key: %v", ErrInvalid, err)
		}
		key, ok := tok.(string)
		if !ok || seen[key] {
			return Digest{}, fmt.Errorf("%w: repeated manifest key", ErrInvalid)
		}
		seen[key] = true
		switch key {
		case "format", "schema":
			var raw json.RawMessage
			if err := d.Decode(&raw); err != nil {
				return Digest{}, fmt.Errorf("%w: manifest header: %v", ErrInvalid, err)
			}
			m := members{m: map[string]json.RawMessage{key: raw}}
			if key == "format" {
				format = m.text(key)
			} else {
				schema = m.nat(key, true)
			}
			if m.err != nil {
				return Digest{}, fmt.Errorf("%w: %w", ErrInvalid, m.err)
			}
		case "files":
			tok, err = d.Token()
			if err != nil || tok != json.Delim('[') {
				return Digest{}, fmt.Errorf("%w: manifest files array", ErrInvalid)
			}
			for d.More() {
				if rows == maxRows {
					return Digest{}, fmt.Errorf("%w: manifest row cap", ErrInvalid)
				}
				var raw json.RawMessage
				if err := d.Decode(&raw); err != nil {
					return Digest{}, fmt.Errorf("%w: manifest row: %v", ErrInvalid, err)
				}
				var f ValidationFile
				err := decodeValidationPayload("manifest file", raw, func(m *members) error { f = readValidationFile(m); return nil })
				if err != nil {
					return Digest{}, err
				}
				if !validationFile(f) || f.Path <= previous || !validationFileCase(f.Path, spellings) {
					return Digest{}, fmt.Errorf("%w: invalid, unsorted or colliding manifest path", ErrInvalid)
				}
				if err := visit(f); err != nil {
					return Digest{}, err
				}
				previous = f.Path
				rows++
			}
			if _, err := d.Token(); err != nil {
				return Digest{}, fmt.Errorf("%w: manifest files ending: %v", ErrInvalid, err)
			}
		default:
			return Digest{}, fmt.Errorf("%w: unknown manifest member %s", ErrInvalid, key)
		}
	}
	if _, err := d.Token(); err != nil {
		return Digest{}, fmt.Errorf("%w: manifest ending: %v", ErrInvalid, err)
	}
	if _, err := d.Token(); err != io.EOF {
		return Digest{}, fmt.Errorf("%w: trailing manifest input: %v", ErrInvalid, err)
	}
	if !seen["format"] || !seen["schema"] || !seen["files"] {
		return Digest{}, fmt.Errorf("%w: missing manifest header or files", ErrInvalid)
	}
	if err := validationHeader(format, schema, FileManifestFormat); err != nil {
		return Digest{}, err
	}
	return Digest(hash.Sum(nil)), nil
}

func readCheckSpec(m *members) CheckSpec {
	p := CheckSpec{CheckID: m.text("check_id"), Adapter: m.text("adapter"), AdapterVersion: m.nat("adapter_version", true)}
	if m.err != nil {
		return p
	}
	if p.Adapter != "go-test-json" || p.AdapterVersion != 1 {
		m.err = ErrUnsupportedValidation
		return p
	}
	validationClosed(m, "check_id", "adapter", "adapter_version", "executable_id", "package", "working_directory", "defining_files", "inventory", "canary", "environment_id", "network_grant_refs", "secret_grant_refs", "wall_ms", "output_bytes", "processes")
	p.ExecutableID = m.text("executable_id")
	p.Package = m.text("package")
	p.WorkingDirectory = m.text("working_directory")
	p.DefiningFiles = readScopedObjects(m, "defining_files", readValidationFile)
	p.Inventory = readScopedObjects(m, "inventory", readValidationInventory)
	p.Canary = graphObject(m, "canary", readValidationCanarySpec)
	p.EnvironmentID = m.text("environment_id")
	p.NetworkGrantRefs, _ = m.sortedIDs("network_grant_refs")
	p.SecretGrantRefs, _ = m.sortedIDs("secret_grant_refs")
	p.WallMS = m.nat("wall_ms", true)
	p.OutputBytes = m.nat("output_bytes", true)
	p.Processes = m.nat("processes", true)
	return p
}

func readValidationVariable(m *members) ValidationVariable {
	validationClosed(m, "name", "value", "run_path")
	p := ValidationVariable{Name: m.text("name")}
	if _, ok := m.get("value", false); ok {
		v := m.text("value")
		p.Value = &v
	}
	p.RunPath, _ = m.optionalText("run_path")
	return p
}

func readEnvironmentSpec(m *members) EnvironmentSpec {
	validationClosed(m, "environment_id", "os", "arch", "executable_id", "executable_path", "executable_sha256", "toolchain_version", "toolchain_root", "toolchain_manifest_sha256", "dependency_manifest_sha256", "lock_files", "variables", "gomaxprocs", "memory_reservation_bytes")
	return EnvironmentSpec{
		EnvironmentID: m.text("environment_id"), OS: m.text("os"), Arch: m.text("arch"),
		ExecutableID: m.text("executable_id"), ExecutablePath: m.text("executable_path"), ExecutableSHA256: m.digest("executable_sha256"),
		ToolchainVersion: m.text("toolchain_version"), ToolchainRoot: m.text("toolchain_root"),
		ToolchainManifestSHA256: m.digest("toolchain_manifest_sha256"), DependencyManifestSHA256: m.digest("dependency_manifest_sha256"),
		LockFiles: readScopedObjects(m, "lock_files", readValidationFile), Variables: readScopedObjects(m, "variables", readValidationVariable),
		GOMAXPROCS: m.nat("gomaxprocs", true), MemoryReservationBytes: m.nat("memory_reservation_bytes", true),
	}
}

// LoadWorkspaceChecks validates the closed configuration and hashes its exact
// accepted bytes. It does not resolve a path, read a file or grant authority.
func LoadWorkspaceChecks(js []byte) (WorkspaceChecks, Digest, error) {
	var p WorkspaceChecks
	err := decodeValidationPayload("workspace checks", js, func(m *members) error {
		p.Format, p.Schema = m.text("format"), m.nat("schema", true)
		if m.err != nil {
			return m.err
		}
		if err := validationHeader(p.Format, p.Schema, WorkspaceChecksFormat); err != nil {
			return err
		}
		validationClosed(m, "format", "schema", "workspace_id", "revision", "required", "checks", "hooks")
		p.WorkspaceID, p.Revision = m.id("workspace_id"), m.nat("revision", true)
		p.Required = readScopedStrings(m, "required")
		p.Checks = readScopedObjects(m, "checks", readCheckSpec)
		p.Hooks = readScopedObjects(m, "hooks", func(b *members) ValidationHook {
			validationClosed(b, "hook_id", "event")
			return ValidationHook{b.text("hook_id"), b.text("event")}
		})
		return p.check()
	})
	if err != nil {
		return WorkspaceChecks{}, Digest{}, err
	}
	return p, Digest(sha256.Sum256(js)), nil
}

// LoadEnvironmentPolicy validates immutable declarations, not the availability
// of the described executable, toolchain or dependency files.
func LoadEnvironmentPolicy(js []byte) (EnvironmentPolicy, Digest, error) {
	var p EnvironmentPolicy
	err := decodeValidationPayload("environment policy", js, func(m *members) error {
		p.Format, p.Schema = m.text("format"), m.nat("schema", true)
		if m.err != nil {
			return m.err
		}
		if err := validationHeader(p.Format, p.Schema, EnvironmentPolicyFormat); err != nil {
			return err
		}
		validationClosed(m, "format", "schema", "workspace_id", "revision", "environments")
		p.WorkspaceID, p.Revision = m.id("workspace_id"), m.nat("revision", true)
		p.Environments = readScopedObjects(m, "environments", readEnvironmentSpec)
		return p.check()
	})
	if err != nil {
		return EnvironmentPolicy{}, Digest{}, err
	}
	return p, Digest(sha256.Sum256(js)), nil
}

func (p WorkspaceChecks) check() error {
	if err := validationHeader(p.Format, p.Schema, WorkspaceChecksFormat); err != nil {
		return err
	}
	if !isV7(p.WorkspaceID) || p.Revision <= 0 || len(p.Required) == 0 || len(p.Checks) != len(p.Required) || p.Hooks == nil {
		return errors.New("invalid workspace checks identity or required set")
	}
	required := make(map[string]bool, len(p.Required))
	for _, id := range p.Required {
		if !scopedToken(id) || required[id] {
			return errors.New("invalid or repeated required check")
		}
		required[id] = true
	}
	for i, check := range p.Checks {
		if !required[check.CheckID] || i > 0 && p.Checks[i-1].CheckID >= check.CheckID {
			return errors.New("check definitions differ from required set or order")
		}
		if err := check.check(); err != nil {
			return err
		}
	}
	for i, hook := range p.Hooks {
		if !scopedToken(hook.HookID) || hook.Event != "observation.committed" || i > 0 && p.Hooks[i-1].HookID >= hook.HookID {
			return errors.New("invalid validation hook")
		}
	}
	return nil
}

func (p EnvironmentPolicy) check() error {
	if err := validationHeader(p.Format, p.Schema, EnvironmentPolicyFormat); err != nil {
		return err
	}
	if !isV7(p.WorkspaceID) || p.Revision <= 0 || len(p.Environments) == 0 {
		return errors.New("invalid environment policy")
	}
	for i, e := range p.Environments {
		if i > 0 && p.Environments[i-1].EnvironmentID >= e.EnvironmentID {
			return errors.New("environment order")
		}
		if err := e.check(); err != nil {
			return err
		}
	}
	return nil
}

func (p WorkspaceChecks) Encode() (json.RawMessage, error) {
	return encodeValidationPayload("workspace checks", p, p.check())
}

func (p EnvironmentPolicy) Encode() (json.RawMessage, error) {
	return encodeValidationPayload("environment policy", p, p.check())
}

// CheckPreimage returns the exact check_sha256 preimage in house key order.
func (p CheckSpec) CheckPreimage() (json.RawMessage, error) {
	return encodeValidationPayload("check preimage", struct {
		Schema    int64     `json:"schema"`
		CheckSpec CheckSpec `json:"check_spec"`
	}{1, p}, p.check())
}

// EnvironmentPreimage returns the exact environment_sha256 preimage. Logical
// run_path names remain logical; concrete child paths are a separate digest.
func (p EnvironmentSpec) EnvironmentPreimage() (json.RawMessage, error) {
	return encodeValidationPayload("environment preimage", struct {
		Schema                   int64           `json:"schema"`
		EnvironmentSpec          EnvironmentSpec `json:"environment_spec"`
		ToolchainManifestSHA256  Digest          `json:"toolchain_manifest_sha256"`
		DependencyManifestSHA256 Digest          `json:"dependency_manifest_sha256"`
	}{1, p, p.ToolchainManifestSHA256, p.DependencyManifestSHA256}, p.check())
}

func (p CheckSpec) InventoryPreimage() (json.RawMessage, error) {
	return encodeValidationPayload("inventory preimage", struct {
		Schema    int64                     `json:"schema"`
		Inventory []ValidationInventoryNode `json:"inventory"`
	}{1, p.Inventory}, p.check())
}

func (p CheckSpec) check() error {
	if p.Adapter != "go-test-json" || p.AdapterVersion != 1 {
		return ErrUnsupportedValidation
	}
	if !scopedToken(p.CheckID) || !scopedToken(p.ExecutableID) || !validationPackage(p.Package) ||
		!validationDirectory(p.WorkingDirectory) || !validationFiles(p.DefiningFiles) ||
		!validationInventory(p.Inventory) || !scopedToken(p.EnvironmentID) ||
		!scopedIDs(p.NetworkGrantRefs, true) || !scopedIDs(p.SecretGrantRefs, true) ||
		p.WallMS <= 0 || p.OutputBytes <= 0 || p.Processes <= 0 {
		return errors.New("invalid Go check specification")
	}
	if len(p.NetworkGrantRefs) != 0 || len(p.SecretGrantRefs) != 0 {
		return ErrUnsupportedValidation
	}
	if err := p.Canary.check(); err != nil {
		return err
	}
	for _, f := range p.DefiningFiles {
		base := f.Path[strings.LastIndexByte(f.Path, '/')+1:]
		if strings.HasPrefix(strings.ToLower(base), "dwarf_canary_") {
			return errors.New("canary collides with a defining file")
		}
	}
	for _, n := range p.Inventory {
		if strings.HasPrefix(strings.SplitN(n.Name, "/", 2)[0], "TestDwarfCanary") {
			return errors.New("canary collides with inventory")
		}
	}
	return nil
}

func validationAbsolutePath(path string) bool {
	if !validCommonDir(path) {
		return false
	}
	if len(path) >= 3 && path[1:3] == ":/" {
		path = path[3:]
	}
	return !strings.Contains(path, ":")
}

func (p EnvironmentSpec) check() error {
	if !scopedToken(p.EnvironmentID) || !scopedToken(p.OS) || !scopedToken(p.Arch) || !scopedToken(p.ExecutableID) ||
		!validationAbsolutePath(p.ExecutablePath) || !validationAbsolutePath(p.ToolchainRoot) ||
		!validationText(p.ToolchainVersion) || !validationDigests(p.ExecutableSHA256, p.ToolchainManifestSHA256, p.DependencyManifestSHA256) ||
		!validationFiles(p.LockFiles) || p.Variables == nil || p.GOMAXPROCS <= 0 || p.MemoryReservationBytes <= 0 {
		return errors.New("invalid validation environment")
	}
	mod := false
	for _, f := range p.LockFiles {
		if f.Path != "go.mod" && f.Path != "go.sum" {
			return ErrUnsupportedValidation
		}
		mod = mod || f.Path == "go.mod"
	}
	if !mod {
		return errors.New("environment lacks go.mod binding")
	}
	return p.checkVariables()
}

func validationIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i, b := range []byte(s) {
		if b != '_' && !(b >= 'A' && b <= 'Z') && !(b >= 'a' && b <= 'z') && !(i > 0 && b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}

func (p ValidationCanarySpec) check() error {
	if p.Protocol != "go-fail-v1" || p.TemplateKind != "testing-error-v1" {
		return ErrUnsupportedValidation
	}
	if !validationIdentifier(p.PackageName) || p.PackageName == "_" || p.File != "dwarf_canary_v1_test.go" ||
		p.TestName != "TestDwarfCanaryV1" || p.TemplateSHA256 == (Digest{}) {
		return errors.New("invalid Go canary specification")
	}
	return nil
}

func (p EnvironmentSpec) checkVariables() error {
	seen := map[string]bool{}
	systemRoot := false
	for i, v := range p.Variables {
		name := strings.ToUpper(v.Name)
		if !validationIdentifier(v.Name) || (i > 0 && p.Variables[i-1].Name >= v.Name) || seen[name] {
			return errors.New("environment variables are not uniquely name-sorted")
		}
		seen[name] = true
		if strings.HasPrefix(name, "GO") || strings.HasPrefix(name, "CGO_") ||
			slices.Contains([]string{"PATH", "TMP", "TEMP", "TMPDIR", "HOME", "USERPROFILE"}, name) {
			return errors.New("reserved environment variable")
		}
		if (v.Value == nil) == (v.RunPath == "") {
			return errors.New("variable requires exactly one value or run_path")
		}
		if v.Value != nil {
			if len(*v.Value) == 0 || len(*v.Value) > 1024 || !utf8.ValidString(*v.Value) {
				return errors.New("invalid literal environment value")
			}
			for _, b := range []byte(*v.Value) {
				if b < 0x20 || b == 0x7f {
					return errors.New("control byte in environment value")
				}
			}
		} else if !slices.Contains([]string{"home", "temp", "gocache", "gopath", "gomodcache", "gotmp"}, v.RunPath) {
			return errors.New("unknown environment run_path")
		}
		if p.OS == "windows" && (name == "SYSTEMROOT" || name == "WINDIR") {
			if v.Value == nil {
				return errors.New("windows loader variable must be literal")
			}
			systemRoot = systemRoot || name == "SYSTEMROOT"
		}
	}
	if p.OS == "windows" && !systemRoot {
		return errors.New("windows environment lacks SystemRoot")
	}
	return nil
}

const ValidationAuthorizationFormat = "dwarf-harness/validation-authorization"

// ValidationAuthorizationDocument is public, immutable source-authority
// provenance, not a grant or a substitute for a current authorization check.
type ValidationAuthorizationDocument struct {
	Format           string    `json:"format"`
	Schema           int64     `json:"schema"`
	Principal        string    `json:"principal"`
	ContextID        uuid.UUID `json:"context_id"`
	CheckPolicyHash  Digest    `json:"check_policy_hash,omitzero"`
	HookID           string    `json:"hook_id,omitempty"`
	SourceSequence   int64     `json:"source_sequence,omitempty"`
	SourceRecordHash Digest    `json:"source_record_hash,omitzero"`
}

func (p ValidationRequested) AuthorizationDocument() ValidationAuthorizationDocument {
	d := ValidationAuthorizationDocument{Format: ValidationAuthorizationFormat, Schema: 1, Principal: "owner", ContextID: p.Origin.ContextID}
	if p.Trigger.Kind == "hook" {
		d.Principal, d.CheckPolicyHash, d.HookID = "hook", p.CheckPolicyHash, p.Trigger.HookID
		d.SourceSequence, d.SourceRecordHash = p.Trigger.SourceSequence, p.Trigger.SourceRecordHash
	}
	return d
}

func (p ValidationAuthorizationDocument) check() error {
	if err := validationHeader(p.Format, p.Schema, ValidationAuthorizationFormat); err != nil {
		return err
	}
	if !isV7(p.ContextID) {
		return errors.New("authorization context is not UUIDv7")
	}
	switch p.Principal {
	case "owner":
		if p.CheckPolicyHash != (Digest{}) || p.HookID != "" || p.SourceSequence != 0 || p.SourceRecordHash != (Digest{}) {
			return errors.New("owner authorization has hook members")
		}
	case "hook":
		if !validationDigests(p.CheckPolicyHash, p.SourceRecordHash) || !scopedToken(p.HookID) || p.SourceSequence <= 0 {
			return errors.New("hook authorization lacks its source")
		}
	default:
		return errors.New("unknown validation authorization principal")
	}
	return nil
}

func (p ValidationAuthorizationDocument) Encode() (json.RawMessage, error) {
	return encodeValidationPayload("validation authorization", p, p.check())
}

func LoadValidationAuthorization(data []byte) (ValidationAuthorizationDocument, Digest, error) {
	var p ValidationAuthorizationDocument
	err := decodeValidationPayload("validation authorization", data, func(m *members) error {
		p = ValidationAuthorizationDocument{Format: m.text("format"), Schema: m.nat("schema", true)}
		if err := validationHeader(p.Format, p.Schema, ValidationAuthorizationFormat); err != nil {
			return err
		}
		p.Principal, p.ContextID = m.text("principal"), m.id("context_id")
		p.CheckPolicyHash, _ = m.optionalDigest("check_policy_hash")
		p.HookID, _ = m.optionalText("hook_id")
		p.SourceSequence, _ = m.optionalNat("source_sequence")
		p.SourceRecordHash, _ = m.optionalDigest("source_record_hash")
		allowed := []string{"format", "schema", "principal", "context_id"}
		if p.Principal == "hook" {
			allowed = append(allowed, "check_policy_hash", "hook_id", "source_sequence", "source_record_hash")
		}
		validationClosed(m, allowed...)
		return p.check()
	})
	if err == nil {
		encoded, encodeErr := p.Encode()
		if encodeErr != nil {
			err = encodeErr
		} else if !bytes.Equal(encoded, data) {
			err = fmt.Errorf("%w: authorization body is not canonical", ErrInvalid)
		}
	}
	if err != nil {
		return ValidationAuthorizationDocument{}, Digest{}, err
	}
	return p, Digest(sha256.Sum256(data)), nil
}

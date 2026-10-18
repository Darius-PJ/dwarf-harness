package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

//go:embed python_analyzer.py
var pythonScript []byte

//go:embed typescript_analyzer.cjs
var typescriptScript []byte

// Tool is an absolute host-pinned executable or TypeScript compiler file.
// Version is the actual interpreter/compiler version, without a product prefix.
type Tool struct {
	Path    string
	SHA256  ledger.Digest
	Version string
}

// VerifiedTool can only be obtained after a budgeted complete file verification.
// It is process-local, never a replacement for a durable reservation or proof
// against hostile same-user mutation. Every launch rechecks its file metadata.
type VerifiedTool struct {
	tool Tool
	info os.FileInfo
}

func (v *VerifiedTool) Identity() Tool { return v.tool }
func (v *VerifiedTool) unchanged() bool {
	if v == nil {
		return false
	}
	info, err := os.Stat(v.tool.Path)
	return err == nil && sameToolFile(v.info, info)
}
func sameToolFile(a, b os.FileInfo) bool {
	return a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime() == b.ModTime()
}

// ToolVerification holds an open file and incremental SHA-256 state. After a
// process restart callers start again, without refunding previously charged work.
// Close releases an unfinished verifier. Step performs at most budget*256 bytes.
type ToolVerification struct {
	tool     Tool
	file     *os.File
	info     os.FileInfo
	hash     hash.Hash
	offset   int64
	verified *VerifiedTool
	failed   bool
}
type VerificationProgress struct {
	BytesRead, WorkUsed int64
	Verified            *VerifiedTool
}

func NewToolVerification(tool Tool) (*ToolVerification, error) {
	if !filepath.IsAbs(tool.Path) || tool.SHA256 == (ledger.Digest{}) || !text(tool.Version, 128) {
		return nil, ErrInvalid
	}
	file, err := os.Open(tool.Path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrInvalid
	}
	return &ToolVerification{tool: tool, file: file, info: info, hash: sha256.New()}, nil
}
func (v *ToolVerification) Close() error {
	if v.file != nil {
		err := v.file.Close()
		v.file = nil
		return err
	}
	return nil
}
func (v *ToolVerification) Step(ctx context.Context, budget int64) (p VerificationProgress, err error) {
	if budget < 1 || budget > maxItems || v.failed {
		return p, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if v.verified != nil {
		return VerificationProgress{BytesRead: v.offset, Verified: v.verified}, nil
	}
	if v.file == nil {
		return p, ErrInvalid
	}
	defer func() {
		p.BytesRead = v.offset
		if err != nil {
			v.failed = true
			_ = v.Close()
		}
	}()
	current, e := os.Stat(v.tool.Path)
	if e != nil || !sameToolFile(v.info, current) {
		return p, ErrInvalid
	}
	var buf [256]byte
	for p.WorkUsed < budget && v.offset < v.info.Size() {
		if err = ctx.Err(); err != nil {
			return p, err
		}
		n := min(int64(len(buf)), v.info.Size()-v.offset)
		var read int
		read, err = io.ReadFull(v.file, buf[:n])
		p.WorkUsed++
		v.offset += int64(read)
		_, _ = v.hash.Write(buf[:read])
		if err != nil {
			return p, err
		}
	}
	current, e = os.Stat(v.tool.Path)
	if e != nil || !sameToolFile(v.info, current) {
		return p, ErrInvalid
	}
	if v.offset == v.info.Size() {
		var sum ledger.Digest
		copy(sum[:], v.hash.Sum(nil))
		if sum != v.tool.SHA256 {
			return p, ErrInvalid
		}
		v.verified = &VerifiedTool{tool: v.tool, info: current}
		p.Verified = v.verified
		err = v.Close()
	}
	return p, err
}

// Tools contains only verified host tools; nil explicitly means unavailable.
// The parser path is lib/typescript.js itself, never a module directory to resolve.
type Tools struct{ Python, Node, TypeScriptParser *VerifiedTool }

// ToolConfig records unverified host identities without reading their files.
// Services reserve work before converting these identities into verified tools.
type ToolConfig struct{ Python, Node, TypeScriptParser *Tool }

func (c ToolConfig) ConfigurationDigest() ledger.Digest { return configurationDigest(c) }

type serviceDeps struct {
	environment     func(string) []string
	afterChildStart func()
	beforeAppend    func([]ledger.Draft)
	retainedCap     *uint64
	nodeCap         int64
}

// ConfigurationDigest binds fixed script bytes, host tool identities and the
// finite resolver options. It does not read target configuration or tool files.
func ConfigurationDigest(tools Tools) ledger.Digest {
	var config ToolConfig
	for _, pair := range []struct {
		verified *VerifiedTool
		target   **Tool
	}{{tools.Python, &config.Python}, {tools.Node, &config.Node}, {tools.TypeScriptParser, &config.TypeScriptParser}} {
		if pair.verified != nil {
			*pair.target = &pair.verified.tool
		}
	}
	return configurationDigest(config)
}

func configurationDigest(tools ToolConfig) ledger.Digest {
	entries := make([][4]any, 0, 3)
	for _, v := range []struct {
		role string
		tool *Tool
	}{{"node", tools.Node}, {"python", tools.Python}, {"typescript", tools.TypeScriptParser}} {
		row := [4]any{v.role, nil, nil, nil}
		if v.tool != nil {
			t := v.tool
			row = [4]any{v.role, t.Path, t.SHA256, t.Version}
		}
		entries = append(entries, row)
	}
	value := struct {
		Profile    string        `json:"helper_profile"`
		Tools      [][4]any      `json:"tools"`
		Python     ledger.Digest `json:"python_script_sha256"`
		TypeScript ledger.Digest `json:"typescript_script_sha256"`
		Root       string        `json:"python_root"`
		Options    struct {
			Module     string `json:"module"`
			Resolution string `json:"moduleResolution"`
			AllowJS    bool   `json:"allowJs"`
			NoEmit     bool   `json:"noEmit"`
		} `json:"typescript_options"`
		SQL string `json:"sql_grammar"`
	}{Profile: ledger.HelperProfileV1, Tools: entries, Python: ledger.Digest(sha256.Sum256(pythonScript)), TypeScript: ledger.Digest(sha256.Sum256(typescriptScript)), Root: ".", SQL: "sql-subset/v1"}
	value.Options.Module = "NodeNext"
	value.Options.Resolution = "NodeNext"
	value.Options.AllowJS = true
	value.Options.NoEmit = true
	b, _ := json.Marshal(value)
	return ledger.Digest(sha256.Sum256(b))
}

type helperLimits struct {
	FrameBytes  int64 `json:"frame_bytes"`
	CorpusBytes int64 `json:"corpus_bytes"`
	BlobBytes   int64 `json:"blob_bytes"`
	Items       int64 `json:"items"`
}
type helperRequest struct {
	Protocol            string             `json:"protocol"`
	AnalysisKey         ledger.Digest      `json:"analysis_key"`
	Adapter             string             `json:"adapter"`
	ConfigurationSHA256 ledger.Digest      `json:"configuration_sha256"`
	Source              ledger.GraphSource `json:"source"`
	MaterializationRoot string             `json:"materialization_root"`
	Corpus              []CorpusArtifact   `json:"corpus"`
	Cursor              ledger.GraphCursor `json:"cursor"`
	Allowance           int64              `json:"allowance"`
	Limits              helperLimits       `json:"limits"`
}
type helperResponse struct {
	Protocol            string        `json:"protocol"`
	AnalysisKey         ledger.Digest `json:"analysis_key"`
	Adapter             string        `json:"adapter"`
	ConfigurationSHA256 ledger.Digest `json:"configuration_sha256"`
	AnalysisResult
}

// Analyzer admits one direct child at a time; concurrent calls fail as busy.
// Context cancellation kills and waits before the owned directory is removed.
type Analyzer struct {
	tools         Tools
	configuration ledger.Digest
	deps          serviceDeps
	mu            sync.Mutex
	fenced        bool
}

var ErrBusy = errors.New("graph analyzer busy")

func NewAnalyzer(tools Tools) *Analyzer {
	return &Analyzer{tools: tools, configuration: ConfigurationDigest(tools)}
}

// Analyze runs the fixed Python or TypeScript helper against a complete bounded
// corpus. It never launches target code, a shell, an installer or a fallback.
func (a *Analyzer) Analyze(ctx context.Context, adapter string, r AnalysisRequest) (result AnalysisResult, err error) {
	if adapter != ledger.AdapterPython && adapter != ledger.AdapterTypeScript {
		return AnalysisResult{}, ErrInvalid
	}
	if err := validateRequest(r); err != nil {
		if errors.Is(err, errCorpusLimit) {
			return unknown(r, adapter, ledger.ReasonHelperLimit, r.Allowance), nil
		}
		return AnalysisResult{}, err
	}
	if r.ConfigurationSHA256 != a.configuration {
		return AnalysisResult{}, ErrInvalid
	}
	if !a.mu.TryLock() {
		return AnalysisResult{}, ErrBusy
	}
	defer a.mu.Unlock()
	if a.fenced {
		return unknown(r, adapter, ledger.ReasonAdapterUnavailable, 0), nil
	}
	var tool *VerifiedTool
	var script []byte
	var name string
	var args []string
	if adapter == ledger.AdapterPython {
		tool, script, name = a.tools.Python, pythonScript, "python_analyzer.py"
		args = []string{"-I", "-S", "-B"}
	} else {
		tool, script, name = a.tools.Node, typescriptScript, "typescript_analyzer.cjs"
		if !a.tools.TypeScriptParser.unchanged() {
			return unknown(r, adapter, ledger.ReasonAdapterUnavailable, 0), nil
		}
	}
	if !tool.unchanged() {
		return unknown(r, adapter, ledger.ReasonAdapterUnavailable, 0), nil
	}
	dir, err := os.MkdirTemp("", "graph-helper-")
	if err != nil {
		return AnalysisResult{}, err
	}
	defer func() {
		if !a.fenced {
			if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
				result = AnalysisResult{}
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	root := filepath.Join(dir, "corpus")
	if err := os.Mkdir(root, 0700); err != nil {
		return AnalysisResult{}, err
	}
	scriptPath := filepath.Join(dir, name)
	if err := os.WriteFile(scriptPath, script, 0600); err != nil {
		return AnalysisResult{}, err
	}
	args = append(args, scriptPath, tool.tool.Version)
	if adapter == ledger.AdapterTypeScript {
		args = append(args, a.tools.TypeScriptParser.tool.Path, a.tools.TypeScriptParser.tool.Version)
	}
	req := helperRequest{Protocol: helperProtocol, AnalysisKey: r.AnalysisKey, Adapter: adapter, ConfigurationSHA256: r.ConfigurationSHA256, Source: r.Source, MaterializationRoot: root, Corpus: r.Corpus, Allowance: r.Allowance, Limits: helperLimits{maxFrame, maxFrame, maxBlob, maxItems}}
	req.Corpus = append([]CorpusArtifact{}, r.Corpus...)
	for i := range req.Corpus {
		if req.Corpus[i].Bytes == nil {
			req.Corpus[i].Bytes = []byte{}
		}
	}
	input, err := json.Marshal(req)
	if err != nil {
		return AnalysisResult{}, err
	}
	minimum := int64(1 + (len(input)+4+255)/256 + len(req.Corpus))
	if len(input) > maxFrame-4 || minimum > r.Allowance {
		return unknown(r, adapter, ledger.ReasonHelperLimit, r.Allowance), nil
	}
	child, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	environment := helperEnvironment
	if a.deps.environment != nil {
		environment = a.deps.environment
	}
	raw, cost, err := runHelperProcess(child, cancel, tool.tool.Path, args, dir, environment(dir), input, nil, a.deps.afterChildStart)
	if err != nil {
		if errors.Is(err, errHelperUnjoined) {
			a.fenced = true
			return unknown(r, adapter, ledger.ReasonAdapterUnavailable, r.Allowance), nil
		}
		if ctx.Err() != nil {
			return AnalysisResult{}, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return unknown(r, adapter, ledger.ReasonAdapterUnavailable, r.Allowance), nil
		}
		return unknown(r, adapter, ledger.ReasonHelperLimit, r.Allowance), nil
	}
	out, err := decodeHelperResponse(raw, req)
	if err != nil {
		return unknown(r, adapter, ledger.ReasonHelperLimit, r.Allowance), nil
	}
	out.Cost = cost
	return out, nil
}

func helperEnvironment(dir string) []string {
	out := []string{"TEMP=" + dir, "TMP=" + dir, "LANG=C", "LC_ALL=C"}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if v := os.Getenv(key); v != "" {
			out = append(out, key+"="+v)
		}
	}
	return out
}

type helperOutput struct {
	data     bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
	observe  func([]byte)
}

func (w *helperOutput) Write(p []byte) (int, error) {
	n := len(p)
	if w.observe != nil {
		w.observe(p)
	}
	left := w.limit - w.data.Len()
	if len(p) > left {
		w.overflow = true
		_, _ = w.data.Write(p[:left])
		if w.cancel != nil {
			w.cancel()
		}
		return n, nil
	}
	_, _ = w.data.Write(p)
	return n, nil
}
func runHelperProcess(ctx context.Context, cancel context.CancelFunc, executable string, args []string, dir string, env []string, input []byte, stderrObserver func([]byte), afterStart func()) ([]byte, Cost, error) {
	var frame bytes.Buffer
	_ = binary.Write(&frame, binary.BigEndian, uint32(len(input)))
	_, _ = frame.Write(input)
	out := helperOutput{limit: maxFrame, cancel: cancel}
	stderr := helperOutput{limit: 64 << 10, observe: stderrObserver}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = &frame
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	err := cmd.Start()
	if err == nil {
		if afterStart != nil {
			afterStart()
		}
		err = cmd.Wait()
	}
	cost := Cost{WallNS: time.Since(start).Nanoseconds(), InputBytes: int64(len(input) + 4), OutputBytes: int64(out.data.Len())}
	if cmd.Process != nil && cmd.ProcessState == nil {
		return nil, cost, errHelperUnjoined
	}
	if out.overflow {
		return nil, cost, errors.New("helper stream overflow")
	}
	if err != nil {
		return nil, cost, fmt.Errorf("graph helper: %w: %s", err, stderr.data.String())
	}
	return out.data.Bytes(), cost, nil
}

// Strict private framing is deliberately stricter than additive ledger records.
// Every member is required, no unknown/null/repeated keys, and no trailing bytes.
func decodeHelperResponse(frame []byte, req helperRequest) (AnalysisResult, error) {
	if len(frame) < 4 || len(frame) > maxFrame {
		return AnalysisResult{}, ErrInvalid
	}
	n := binary.BigEndian.Uint32(frame[:4])
	if n > maxFrame-4 || int(n) != len(frame)-4 || !utf8.Valid(frame[4:]) {
		return AnalysisResult{}, ErrInvalid
	}
	input, _ := json.Marshal(req)
	if int64(1+(len(input)+4+255)/256+(len(frame)+255)/256) > req.Allowance {
		return AnalysisResult{}, ErrInvalid
	}
	remaining := int64(64<<20) - int64(len(input)+len(frame))*4
	for _, artifact := range req.Corpus {
		remaining -= int64(len(artifact.Bytes))*2 + 512
	}
	var value helperResponse
	if err := decodeHelperJSONBudget(frame[4:], &value, remaining); err != nil {
		return AnalysisResult{}, err
	}
	if value.Protocol != req.Protocol || value.AnalysisKey != req.AnalysisKey || value.Adapter != req.Adapter || value.ConfigurationSHA256 != req.ConfigurationSHA256 || value.NextCursor.ArtifactIndex != int64(len(req.Corpus)) || value.NextCursor.OutputIndex != 0 || value.WorkUsed < 0 || value.WorkUsed > req.Allowance || value.Cost.WallNS < 0 || value.Cost.InputBytes < 0 || value.Cost.OutputBytes < 0 {
		return AnalysisResult{}, ErrInvalid
	}
	floor := int64(1 + (len(input)+4+255)/256 + (len(frame)+255)/256 + len(req.Corpus) + len(value.Nodes) + len(value.Edges) + len(value.Coverage))
	if value.WorkUsed < floor || len(value.Nodes)+len(value.Edges)+len(value.Coverage) > maxItems {
		return AnalysisResult{}, ErrInvalid
	}
	artifacts := make(map[ledger.NodeKey]CorpusArtifact, len(req.Corpus))
	scopes := map[string]bool{workspaceScope(req.Source.WorkspaceID): true}
	keys := make(map[ledger.NodeKey]bool)
	for _, a := range req.Corpus {
		artifacts[a.Key] = a
		keys[a.Key] = true
		scopes[keyScope(a.Key)] = true
	}
	for i, n := range value.Nodes {
		if !nodeValid(n) || n.Key.Kind != ledger.NodeKindModule || n.Key.OriginID != req.Source.WorkspaceID || n.Adapter != req.Adapter || n.ConfigurationSHA256 != req.ConfigurationSHA256 || keys[n.Key] || i > 0 && bytes.Compare(keyBytes(value.Nodes[i-1].Key), keyBytes(n.Key)) >= 0 {
			return AnalysisResult{}, ErrInvalid
		}
		var tuple []string
		if json.Unmarshal([]byte(n.Key.LocalKey), &tuple) != nil || len(tuple) != 3 || tuple[0] != req.Adapter || tuple[1] != "workspace" {
			return AnalysisResult{}, ErrInvalid
		}
		canonical, err := ModuleKey(req.Source.WorkspaceID, tuple[0], tuple[1], tuple[2])
		if err != nil || canonical != n.Key {
			return AnalysisResult{}, ErrInvalid
		}
		keys[n.Key] = true
	}
	for i, e := range value.Edges {
		proof, ok := artifacts[e.Evidence.Artifact]
		if !edgeValid(e) || !keys[e.From] || !keys[e.To] || !ok || e.Evidence.ArtifactSHA256 != proof.SHA256 || e.Evidence.ConfigurationSHA256 != req.ConfigurationSHA256 || e.Evidence.Adapter != req.Adapter || e.To != proof.Key || e.From.Kind != ledger.NodeKindModule || e.Type != "static_dependency" || e.Geometry != ledger.GeometryCoupling || e.WeightPPM != 1000000 || i > 0 && bytes.Compare(value.Edges[i-1].EdgeID[:], e.EdgeID[:]) >= 0 {
			return AnalysisResult{}, ErrInvalid
		}
	}
	coveredScopes := make(map[string]bool)
	for i, c := range value.Coverage {
		if !coverageValid(c) || !scopes[c.Scope] || c.Adapter != req.Adapter || i > 0 && coverageOrder(value.Coverage[i-1], c) >= 0 {
			return AnalysisResult{}, ErrInvalid
		}
		coveredScopes[c.Scope] = true
		if c.Scope == workspaceScope(req.Source.WorkspaceID) && (c.State != ledger.CoverageUnknown || len(value.Nodes) != 0 || len(value.Edges) != 0) {
			return AnalysisResult{}, ErrInvalid
		}
	}
	if !coveredScopes[workspaceScope(req.Source.WorkspaceID)] {
		for _, artifact := range req.Corpus {
			ext := strings.ToLower(filepath.Ext(artifact.Path))
			relevant := req.Adapter == ledger.AdapterPython && ext == ".py" || req.Adapter == ledger.AdapterTypeScript && strings.Contains("|.ts|.tsx|.js|.jsx|.mts|.cts|.mjs|.cjs|", "|"+ext+"|")
			if relevant && !coveredScopes[keyScope(artifact.Key)] {
				return AnalysisResult{}, ErrInvalid
			}
		}
	}
	return value.AnalysisResult, nil
}

func strictJSONValue(d *json.Decoder, depth int, remaining *int64) (any, error) {
	*remaining -= 512
	if *remaining < 0 || depth > 32 {
		return nil, ErrInvalid
	}
	v, err := d.Token()
	if err != nil {
		return nil, err
	}
	if s, ok := v.(string); ok {
		*remaining -= int64(len(s)) * 2
		if *remaining < 0 {
			return nil, ErrInvalid
		}
	}
	switch v {
	case json.Delim('{'):
		out := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, ErrInvalid
			}
			*remaining -= int64(len(key))*2 + 512
			if *remaining < 0 {
				return nil, ErrInvalid
			}
			if _, exists := out[key]; exists {
				return nil, ErrInvalid
			}
			val, err := strictJSONValue(d, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			out[key] = val
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalid
		}
		return out, nil
	case json.Delim('['):
		out := []any{}
		for d.More() {
			val, err := strictJSONValue(d, depth+1, remaining)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalid
		}
		return out, nil
	}
	if _, ok := v.(json.Delim); ok {
		return nil, ErrInvalid
	}
	return v, nil
}

// ledger.Digest intentionally has no Unmarshal method: the frozen codecs use
// ParseDigest. This private decoder uses that same parser without a shadow type.
func decodeHelperJSON(raw []byte, destination any) error {
	return decodeHelperJSONBudget(raw, destination, int64(64<<20)-int64(len(raw))*8)
}
func decodeHelperJSONBudget(raw []byte, destination any, remaining int64) error {
	if !utf8.Valid(raw) || len(raw) > maxFrame-4 || remaining < 0 {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := strictJSONValue(d, 0, &remaining)
	if err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	return assignHelperValue(value, reflect.ValueOf(destination).Elem())
}

func helperFields(v reflect.Value, fields map[string]reflect.Value) {
	for i := range v.NumField() {
		field := v.Type().Field(i)
		if field.Anonymous {
			helperFields(v.Field(i), fields)
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		fields[name] = v.Field(i)
	}
}

func assignHelperValue(source any, target reflect.Value) error {
	if source == nil {
		return ErrInvalid
	}
	if target.Type() == reflect.TypeFor[ledger.Digest]() {
		s, ok := source.(string)
		if !ok {
			return ErrInvalid
		}
		d, err := ledger.ParseDigest(s)
		if err != nil {
			return err
		}
		target.Set(reflect.ValueOf(d))
		return nil
	}
	switch target.Kind() {
	case reflect.Struct:
		members, ok := source.(map[string]any)
		if !ok {
			return ErrInvalid
		}
		fields := map[string]reflect.Value{}
		helperFields(target, fields)
		if len(fields) != len(members) {
			return ErrInvalid
		}
		for name, field := range fields {
			if err := assignHelperValue(members[name], field); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if target.Type() == reflect.TypeFor[[]byte]() {
			raw, err := json.Marshal(source)
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, target.Addr().Interface())
		}
		items, ok := source.([]any)
		if !ok || len(items) > maxItems {
			return ErrInvalid
		}
		target.Set(reflect.MakeSlice(target.Type(), len(items), len(items)))
		for i, item := range items {
			if err := assignHelperValue(item, target.Index(i)); err != nil {
				return err
			}
		}
	case reflect.String:
		s, ok := source.(string)
		if !ok {
			return ErrInvalid
		}
		target.SetString(s)
	case reflect.Int64:
		n, ok := source.(json.Number)
		if !ok {
			return ErrInvalid
		}
		value, err := n.Int64()
		if err != nil {
			return err
		}
		target.SetInt(value)
	default:
		raw, err := json.Marshal(source)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, target.Addr().Interface())
	}
	return nil
}

var errHelperUnjoined = errors.New("graph helper termination unconfirmed")

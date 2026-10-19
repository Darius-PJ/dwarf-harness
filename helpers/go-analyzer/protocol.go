package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	protocolVersion = "graph-helper/v1"
	profileVersion  = "go-packages/v1"
	helperVersion   = "go-analyzer/v1"
	maxFrame        = 8 << 20
	maxBlob         = 1 << 20
	maxItems        = 10000
)

var (
	errRequest = errors.New("invalid Go helper request")
	errLimit   = errors.New("native Go helper work or result limit")
)

type recordRef struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

type source struct {
	WorkspaceID string    `json:"workspace_id"`
	Registered  recordRef `json:"registered"`
	Subject     recordRef `json:"subject"`
	Tree        string    `json:"tree"`
}

type nodeKey struct {
	Namespace string `json:"namespace"`
	OriginID  string `json:"origin_id"`
	Kind      string `json:"kind"`
	LocalKey  string `json:"local_key"`
}

type artifact struct {
	Path   string  `json:"path"`
	Key    nodeKey `json:"artifact_key"`
	SHA256 string  `json:"sha256"`
	Bytes  []byte  `json:"bytes_base64"`
}

type cursor struct {
	ArtifactIndex int64 `json:"artifact_index"`
	OutputIndex   int64 `json:"output_index"`
}

type limits struct {
	FrameBytes  int64 `json:"frame_bytes"`
	CorpusBytes int64 `json:"corpus_bytes"`
	BlobBytes   int64 `json:"blob_bytes"`
	Items       int64 `json:"items"`
}

type goProfile struct {
	Profile            string `json:"profile"`
	HelperVersion      string `json:"helper_version"`
	ToolchainPath      string `json:"toolchain_path"`
	ToolchainSHA256    string `json:"toolchain_sha256"`
	ToolchainVersion   string `json:"toolchain_version"`
	ToolchainFileSize  int64  `json:"toolchain_file_size"`
	ToolchainModtimeNS int64  `json:"toolchain_modtime_ns"`
	GOROOT             string `json:"goroot"`
	GOOS               string `json:"goos"`
	GOARCH             string `json:"goarch"`
	LoadTimeoutMS      int64  `json:"load_timeout_ms"`
}

type request struct {
	Protocol            string     `json:"protocol"`
	AnalysisKey         string     `json:"analysis_key"`
	Adapter             string     `json:"adapter"`
	ConfigurationSHA256 string     `json:"configuration_sha256"`
	Source              source     `json:"source"`
	MaterializationRoot string     `json:"materialization_root"`
	Corpus              []artifact `json:"corpus"`
	Cursor              cursor     `json:"cursor"`
	Allowance           int64      `json:"allowance"`
	Limits              limits     `json:"limits"`
	Go                  goProfile  `json:"go"`
}

type node struct {
	Key                 nodeKey `json:"key"`
	VersionSHA256       string  `json:"version_sha256"`
	Adapter             string  `json:"adapter"`
	ConfigurationSHA256 string  `json:"configuration_sha256"`
}

type evidence struct {
	Artifact            nodeKey `json:"artifact"`
	ArtifactSHA256      string  `json:"artifact_sha256"`
	Adapter             string  `json:"adapter"`
	ConfigurationSHA256 string  `json:"configuration_sha256"`
	Reference           string  `json:"reference"`
}

type edge struct {
	EdgeID    string   `json:"edge_id"`
	From      nodeKey  `json:"from"`
	To        nodeKey  `json:"to"`
	Type      string   `json:"type"`
	Geometry  string   `json:"geometry"`
	WeightPPM int64    `json:"weight_ppm"`
	Evidence  evidence `json:"evidence"`
}

type coverage struct {
	Scope   string   `json:"scope"`
	Adapter string   `json:"adapter"`
	State   string   `json:"state"`
	Reasons []string `json:"reasons"`
}

type cost struct {
	WallNS      int64 `json:"wall_ns"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}

type response struct {
	Protocol            string     `json:"protocol"`
	AnalysisKey         string     `json:"analysis_key"`
	Adapter             string     `json:"adapter"`
	ConfigurationSHA256 string     `json:"configuration_sha256"`
	NextCursor          cursor     `json:"next_cursor"`
	WorkUsed            int64      `json:"work_used"`
	Nodes               []node     `json:"nodes"`
	Edges               []edge     `json:"edges"`
	Coverage            []coverage `json:"coverage"`
	Cost                cost       `json:"cost"`
}

type budget struct {
	used  int64
	limit int64
}

func (b *budget) charge(n int64) error {
	if n < 0 || n > b.limit-b.used {
		return errLimit
	}
	b.used += n
	return nil
}

func byteUnits(n int) int64 { return int64((n + 255) / 256) }

func framed(body []byte) []byte {
	out := make([]byte, len(body)+4)
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out
}

// readRequest checks the advertised bound before allocating its frame. The one
// trailing-byte read also rejects a second request; this is not a stream server.
func readRequest(in io.Reader, cwd string) (request, int64, error) {
	var header [4]byte
	if _, err := io.ReadFull(in, header[:]); err != nil {
		return request{}, 0, errRequest
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > maxFrame-4 {
		return request{}, 0, errRequest
	}
	frame := make([]byte, int(n)+4)
	copy(frame, header[:])
	if _, err := io.ReadFull(in, frame[4:]); err != nil {
		return request{}, 0, errRequest
	}
	var trailer [1]byte
	if n, err := in.Read(trailer[:]); n != 0 || err != io.EOF {
		return request{}, 0, errRequest
	}
	return decodeRequest(frame, cwd)
}

// decodeRequest is pure: no file lookup, materialization or process can happen
// until the complete, strictly typed request and all immutable bindings pass.
func decodeRequest(frame []byte, cwd string) (request, int64, error) {
	var r request
	if len(frame) < 4 || len(frame) > maxFrame || int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-4 || !utf8.Valid(frame[4:]) {
		return r, 0, errRequest
	}
	work := budget{used: 1 + byteUnits(len(frame)), limit: maxItems}
	decoder := json.NewDecoder(bytes.NewReader(frame[4:]))
	decoder.UseNumber()
	if err := decodeValue(decoder, reflect.ValueOf(&r).Elem(), &work, 0); err != nil {
		return request{}, work.used, errors.Join(errRequest, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return request{}, work.used, errRequest
	}
	if r.Protocol != protocolVersion || r.Adapter != "go" || !digestValid(r.AnalysisKey) || !digestValid(r.ConfigurationSHA256) || !uuidValid(r.Source.WorkspaceID) || r.Source.Registered.Seq < 1 || r.Source.Subject.Seq <= r.Source.Registered.Seq || !digestValid(r.Source.Registered.Hash) || !digestValid(r.Source.Subject.Hash) || !gitIDValid(r.Source.Tree) || r.Cursor != (cursor{}) || r.Allowance < 1 || r.Allowance > maxItems || r.Limits != (limits{maxFrame, maxFrame, maxBlob, maxItems}) {
		return request{}, work.used, errRequest
	}
	if !filepath.IsAbs(cwd) || !filepath.IsAbs(r.MaterializationRoot) || r.MaterializationRoot != filepath.Join(cwd, "corpus") || !goProfileValid(r.Go) {
		return request{}, work.used, errRequest
	}
	work.limit = r.Allowance
	paths := make(map[string]string)
	files := make(map[string]bool)
	total := 0
	for i, item := range r.Corpus {
		if err := work.charge(1 + byteUnits(len(item.Bytes))); err != nil {
			return request{}, work.used, errors.Join(errRequest, err)
		}
		if len(item.Bytes) > maxBlob || len(item.Bytes) > maxFrame-total || !pathValid(item.Path) || i > 0 && r.Corpus[i-1].Path >= item.Path || !digestValid(item.SHA256) || hashBytes(item.Bytes) != item.SHA256 || item.Key != artifactKey(r.Source.WorkspaceID, artifactBirth(item.Key), item.Path) || artifactBirth(item.Key) < 1 || len(item.Key.LocalKey) > 4096 {
			return request{}, work.used, errRequest
		}
		if err := checkHostPath(item.Path, paths, files, &work); err != nil {
			return request{}, work.used, errors.Join(errRequest, err)
		}
		total += len(item.Bytes)
	}
	if work.used > r.Allowance {
		return request{}, work.used, errors.Join(errRequest, errLimit)
	}
	return r, work.used, nil
}

// decodeValue mirrors the existing private helper's strict schema rules, but
// fills these program-local types directly. Every member is required, unknown,
// duplicate and null values fail, and bounded arrays grow only after charging.
func decodeValue(d *json.Decoder, target reflect.Value, work *budget, depth int) error {
	if depth > 16 {
		return errRequest
	}
	if err := work.charge(1); err != nil {
		return err
	}
	value, err := d.Token()
	if err != nil || value == nil {
		return errRequest
	}
	switch target.Kind() {
	case reflect.Struct:
		if value != json.Delim('{') {
			return errRequest
		}
		fields := make(map[string]int, target.NumField())
		for i := range target.NumField() {
			fields[target.Type().Field(i).Tag.Get("json")] = i
		}
		seen := make(map[string]bool, len(fields))
		for d.More() {
			if err := work.charge(1); err != nil {
				return err
			}
			key, err := d.Token()
			name, ok := key.(string)
			index, exists := fields[name]
			if err != nil || !ok || !exists || seen[name] {
				return errRequest
			}
			seen[name] = true
			if err := decodeValue(d, target.Field(index), work, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') || len(seen) != len(fields) {
			return errRequest
		}
	case reflect.Slice:
		if target.Type() == reflect.TypeFor[[]byte]() {
			encoded, ok := value.(string)
			if !ok || len(encoded) > base64.StdEncoding.EncodedLen(maxBlob) {
				return errRequest
			}
			if err := work.charge(byteUnits(len(encoded))); err != nil {
				return err
			}
			decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
			if err != nil || len(decoded) > maxBlob || base64.StdEncoding.EncodeToString(decoded) != encoded {
				return errRequest
			}
			target.SetBytes(decoded)
			return nil
		}
		if value != json.Delim('[') {
			return errRequest
		}
		target.Set(reflect.MakeSlice(target.Type(), 0, 0))
		for d.More() {
			if target.Len() >= maxItems {
				return errRequest
			}
			item := reflect.New(target.Type().Elem()).Elem()
			if err := decodeValue(d, item, work, depth+1); err != nil {
				return err
			}
			target.Set(reflect.Append(target, item))
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errRequest
		}
	case reflect.String:
		text, ok := value.(string)
		if !ok {
			return errRequest
		}
		if err := work.charge(byteUnits(len(text))); err != nil {
			return err
		}
		target.SetString(text)
	case reflect.Int64:
		number, ok := value.(json.Number)
		if !ok || !digits(string(number)) {
			return errRequest
		}
		n, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return errRequest
		}
		target.SetInt(n)
	default:
		return errRequest
	}
	return nil
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func digestValid(value string) bool {
	return len(value) == 64 && hexValid(value) && value != strings.Repeat("0", 64)
}

func hexValid(value string) bool {
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func gitIDValid(value string) bool { return (len(value) == 40 || len(value) == 64) && hexValid(value) }

func uuidValid(value string) bool {
	return len(value) == 36 && strings.Count(value, "-") == 4 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-' && value[14] == '7' && strings.ContainsRune("89ab", rune(value[19])) && hexValid(strings.ReplaceAll(value, "-", ""))
}

func goProfileValid(g goProfile) bool {
	return g.Profile == profileVersion && g.HelperVersion != "" && len(g.HelperVersion) <= 128 && filepath.IsAbs(g.GOROOT) && filepath.IsAbs(g.ToolchainPath) && g.ToolchainPath == filepath.Join(filepath.Clean(g.GOROOT), "bin", "go"+exeSuffix()) && digestValid(g.ToolchainSHA256) && g.ToolchainVersion != "" && len(g.ToolchainVersion) <= 128 && word(g.GOOS) && word(g.GOARCH) && g.LoadTimeoutMS == 20000
}

func word(value string) bool {
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

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func pathValid(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\\") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func checkHostPath(name string, paths map[string]string, files map[string]bool, work *budget) error {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if err := work.charge(1); err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			device, _, _ := strings.Cut(strings.ToUpper(part), ".")
			if strings.ContainsAny(part, ":<>\"|?*") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '0' && device[3] <= '9' {
				return errRequest
			}
		}
		prefix := strings.Join(parts[:i+1], "/")
		identity := prefix
		if runtime.GOOS == "windows" {
			identity = strings.ToLower(identity)
		}
		old, present := paths[identity]
		if present && old != prefix || files[identity] || present && i == len(parts)-1 {
			return errRequest
		}
		paths[identity] = prefix
		if i == len(parts)-1 {
			files[identity] = true
		}
	}
	return nil
}

func artifactBirth(key nodeKey) int64 {
	var tuple []json.RawMessage
	if json.Unmarshal([]byte(key.LocalKey), &tuple) != nil || len(tuple) != 2 {
		return 0
	}
	var birth int64
	if json.Unmarshal(tuple[0], &birth) != nil {
		return 0
	}
	return birth
}

func artifactKey(workspace string, birth int64, name string) nodeKey {
	return nodeKey{"workspace", workspace, "artifact", canonical([2]any{birth, name})}
}

func canonical(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortCorpus(files []artifact) {
	slices.SortFunc(files, func(a, b artifact) int { return strings.Compare(a.Path, b.Path) })
}

func emptyResponse(r request) response {
	return response{Protocol: r.Protocol, AnalysisKey: r.AnalysisKey, Adapter: "go", ConfigurationSHA256: r.ConfigurationSHA256, NextCursor: cursor{int64(len(r.Corpus)), 0}, Nodes: []node{}, Edges: []edge{}, Coverage: []coverage{}}
}

func scopeUnknown(r request, reason string) response {
	out := emptyResponse(r)
	out.Coverage = []coverage{{Scope: "workspace:" + r.Source.WorkspaceID + ":canonical", Adapter: "go", State: "unknown", Reasons: []string{reason}}}
	return out
}

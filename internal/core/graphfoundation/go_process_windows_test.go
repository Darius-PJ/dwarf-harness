package graphfoundation

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/undercurrent/graph"
)

var proofKernel = syscall.NewLazyDLL("kernel32.dll")
var proofCreateJob = proofKernel.NewProc("CreateJobObjectW")
var proofSetJob = proofKernel.NewProc("SetInformationJobObject")
var proofQueryJob = proofKernel.NewProc("QueryInformationJobObject")
var proofAssignJob = proofKernel.NewProc("AssignProcessToJobObject")
var proofInJob = proofKernel.NewProc("IsProcessInJob")
var proofResume = proofKernel.NewProc("ResumeThread")
var proofCreateCompletionPort = proofKernel.NewProc("CreateIoCompletionPort")
var proofGetCompletionStatus = proofKernel.NewProc("GetQueuedCompletionStatus")

type proofLimits struct {
	ProcessTime, JobTime                           int64
	Flags                                          uint32
	MinWorkingSet, MaxWorkingSet                   uintptr
	ActiveLimit                                    uint32
	Affinity                                       uintptr
	Priority, Scheduling                           uint32
	IO                                             [6]uint64
	ProcessMemory, JobMemory, PeakProcess, PeakJob uintptr
}
type proofAccounting struct {
	User, Kernel, PeriodUser, PeriodKernel int64
	Faults, Total, Active, Terminated      uint32
}
type proofPIDs struct {
	Assigned, Count uint32
	IDs             [64]uintptr
}
type proofRun struct {
	PID            uint32
	Exit           uint32
	Accounting     proofAccounting
	Live           []uintptr
	Stdout, Stderr []byte
}

func proofJobBuffer(proc *syscall.LazyProc, job syscall.Handle, class uintptr, pointer unsafe.Pointer, size uintptr) error {
	var pin runtime.Pinner
	pin.Pin(pointer)
	defer pin.Unpin()
	args := []uintptr{uintptr(job), class, uintptr(pointer), size}
	if proc == proofQueryJob {
		args = append(args, 0)
	}
	ok, _, err := proc.Call(args...)
	runtime.KeepAlive(pointer)
	if ok == 0 {
		return err
	}
	return nil
}

// observeGoProcess assigns the still-suspended root. Lifetime Total, not the
// current PID list alone, detects a descendant which exited before its parent.
func observeGoProcess(t *testing.T, program string, args, env []string, cwd string, input []byte) proofRun {
	t.Helper()
	handle, _, err := proofCreateJob.Call(0, 0)
	if handle == 0 {
		t.Fatalf("CreateJobObject: %v", err)
	}
	job := syscall.Handle(handle)
	defer syscall.CloseHandle(job)
	limits := proofLimits{Flags: 0x2000} // KILL_ON_JOB_CLOSE only; no resource limits.
	if err := proofJobBuffer(proofSetJob, job, 9, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		t.Fatal(err)
	}
	var actual proofLimits
	// LimitFlags selects the applied limits; Windows also reports defaults for
	// inactive priority and scheduling fields.
	if err := proofJobBuffer(proofQueryJob, job, 9, unsafe.Pointer(&actual), unsafe.Sizeof(actual)); err != nil || actual.Flags != limits.Flags {
		t.Fatalf("unexpected Job limits: %+v, %v", actual, err)
	}
	portHandle, _, err := proofCreateCompletionPort.Call(^uintptr(0), 0, 0, 1)
	if portHandle == 0 {
		t.Fatalf("CreateIoCompletionPort: %v", err)
	}
	port := syscall.Handle(portHandle)
	defer syscall.CloseHandle(port)
	association := struct {
		Key  uintptr
		Port syscall.Handle
	}{Key: uintptr(job), Port: port}
	if err := proofJobBuffer(proofSetJob, job, 7, unsafe.Pointer(&association), unsafe.Sizeof(association)); err != nil {
		t.Fatalf("associate Job completion port: %v", err)
	}
	ioRoot := t.TempDir()
	paths := []string{filepath.Join(ioRoot, "stdin"), filepath.Join(ioRoot, "stdout"), filepath.Join(ioRoot, "stderr")}
	if err := os.WriteFile(paths[0], input, 0600); err != nil {
		t.Fatal(err)
	}
	var handles [3]syscall.Handle
	for i, path := range paths {
		name, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		attributes := syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), InheritHandle: 1}
		access, creation := uint32(syscall.GENERIC_WRITE), uint32(syscall.CREATE_ALWAYS)
		if i == 0 {
			access, creation = syscall.GENERIC_READ, syscall.OPEN_EXISTING
		}
		handles[i], err = syscall.CreateFile(name, access, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, &attributes, creation, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.CloseHandle(handles[i])
	}
	app, err := syscall.UTF16PtrFromString(program)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := syscall.UTF16PtrFromString(cwd)
	if err != nil {
		t.Fatal(err)
	}
	words := []string{syscall.EscapeArg(program)}
	for _, arg := range args {
		words = append(words, syscall.EscapeArg(arg))
	}
	command, err := syscall.UTF16FromString(strings.Join(words, " "))
	if err != nil {
		t.Fatal(err)
	}
	env = slices.Clone(env)
	slices.SortFunc(env, func(a, b string) int { return strings.Compare(strings.ToUpper(a), strings.ToUpper(b)) })
	var environment []uint16
	for _, entry := range env {
		value, err := syscall.UTF16FromString(entry)
		if err != nil {
			t.Fatal(err)
		}
		environment = append(environment, value...)
	}
	environment = append(environment, 0)
	startup := syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: syscall.STARTF_USESTDHANDLES, StdInput: handles[0], StdOutput: handles[1], StdErr: handles[2]}
	var process syscall.ProcessInformation
	const createSuspended = 0x00000004
	err = syscall.CreateProcess(app, &command[0], nil, nil, true, createSuspended|syscall.CREATE_UNICODE_ENVIRONMENT, &environment[0], directory, &startup, &process)
	runtime.KeepAlive(command)
	runtime.KeepAlive(environment)
	if err != nil {
		t.Fatalf("CreateProcess suspended: %v", err)
	}
	defer syscall.CloseHandle(process.Process)
	defer syscall.CloseHandle(process.Thread)
	joined := false
	defer func() {
		if !joined {
			_ = syscall.TerminateProcess(process.Process, 1)
			_, _ = syscall.WaitForSingleObject(process.Process, 30000)
		}
	}()
	if ok, _, err := proofAssignJob.Call(uintptr(job), uintptr(process.Process)); ok == 0 {
		t.Fatalf("AssignProcessToJobObject before resume: %v", err)
	}
	var member uint32
	var pin runtime.Pinner
	pin.Pin(&member)
	ok, _, memberErr := proofInJob.Call(uintptr(process.Process), uintptr(job), uintptr(unsafe.Pointer(&member)))
	pin.Unpin()
	if ok == 0 || member == 0 {
		t.Fatalf("suspended root membership: %d, %v", member, memberErr)
	}
	deadline := time.Now().Add(30 * time.Second)
	if previous, _, err := proofResume.Call(uintptr(process.Thread)); previous != 1 {
		t.Fatalf("ResumeThread suspend count %d: %v", previous, err)
	}
	event, err := syscall.WaitForSingleObject(process.Process, uint32(max(0, time.Until(deadline).Milliseconds())))
	if err != nil || event != syscall.WAIT_OBJECT_0 {
		t.Fatalf("root wait: event %d, %v", event, err)
	}
	joined = true
	result := proofRun{PID: process.ProcessId}
	if err := syscall.GetExitCodeProcess(process.Process, &result.Exit); err != nil {
		t.Fatal(err)
	}
	// Job notifications are best-effort wake-ups, not evidence. Wait for
	// ACTIVE_PROCESS_ZERO within the root's existing 30 s bound; if delivery
	// is lost, take the same snapshots at the bound. Only the exact lifetime
	// accounting and membership assertions below decide whether the Job joined.
	var message uint32
	var key, processID uintptr
	pin.Pin(&message)
	pin.Pin(&key)
	pin.Pin(&processID)
	defer pin.Unpin()
	for remaining := time.Until(deadline); remaining > 0; remaining = time.Until(deadline) {
		ok, _, err := proofGetCompletionStatus.Call(uintptr(port), uintptr(unsafe.Pointer(&message)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&processID)), uintptr(max(1, remaining.Milliseconds())))
		if ok == 0 {
			if err == syscall.Errno(syscall.WAIT_TIMEOUT) {
				break
			}
			t.Fatalf("Job completion notification: %v", err)
		}
		if key != association.Key {
			t.Fatalf("unexpected Job completion key: %d", key)
		}
		if message == 4 { // JOB_OBJECT_MSG_ACTIVE_PROCESS_ZERO
			break
		}
	}
	if err := proofJobBuffer(proofQueryJob, job, 1, unsafe.Pointer(&result.Accounting), unsafe.Sizeof(result.Accounting)); err != nil {
		t.Fatal(err)
	}
	var pids proofPIDs
	if err := proofJobBuffer(proofQueryJob, job, 3, unsafe.Pointer(&pids), unsafe.Sizeof(pids)); err != nil {
		t.Fatal(err)
	}
	if pids.Count > uint32(len(pids.IDs)) || pids.Assigned != pids.Count {
		t.Fatalf("truncated Job membership: %+v", pids)
	}
	result.Live = slices.Clone(pids.IDs[:pids.Count])
	result.Stdout, err = os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	result.Stderr, err = os.ReadFile(paths[2])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("program=%q args=%q environment=%q cwd=%q pid=%d exit=%d accounting=%+v live=%v limits=%+v\nstdout:\n%s\nstderr:\n%s", program, args, env, cwd, result.PID, result.Exit, result.Accounting, result.Live, actual, result.Stdout, result.Stderr)
	if result.Exit != 0 || result.Accounting.Active != 0 || len(result.Live) != 0 {
		t.Fatalf("profile did not join its Job: %+v", result)
	}
	return result
}

func TestGoProcessProofChild(t *testing.T) {
	t.Parallel()
	switch os.Getenv("DWARF_GRAPH_PROOF_CONTROL") {
	case "", "leaf":
		return
	case "child":
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "-test.run=^TestGoProcessProofChild$")
		command.Env = append(os.Environ(), "DWARF_GRAPH_PROOF_CONTROL=leaf")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("joined control child: %v\n%s", err, output)
		}
	default:
		t.Fatal("unknown observer control")
	}
}

func proofGoEnvironment(t *testing.T, config graph.GoToolConfig, root string, files map[string]string) []string {
	t.Helper()
	for _, name := range []string{"corpus", "gopath", "gomodcache", "gocache", "tmp", "home/go/telemetry"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "home", "go", "telemetry", "mode"), []byte("off"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(root, "corpus", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{"PATH": filepath.Dir(config.Toolchain.Path), "GOROOT": config.GOROOT, "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOPROXY": "off", "GOPACKAGESDRIVER": "off", "GOFLAGS": "", "CGO_ENABLED": "0", "GOOS": config.GOOS, "GOARCH": config.GOARCH, "GOPATH": filepath.Join(root, "gopath"), "GOMODCACHE": filepath.Join(root, "gomodcache"), "GOCACHE": filepath.Join(root, "gocache"), "TEMP": filepath.Join(root, "tmp"), "TMP": filepath.Join(root, "tmp"), "TMPDIR": filepath.Join(root, "tmp"), "HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"), "APPDATA": filepath.Join(root, "home"), "LOCALAPPDATA": filepath.Join(root, "home"), "GOSUMDB": "off", "GO111MODULE": "on", "LANG": "C", "LC_ALL": "C"}
	for _, name := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(name); value != "" {
			values[name] = value
		}
	}
	var env []string
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	slices.Sort(env)
	return env
}

func TestGoListHasNoDescendants(t *testing.T) {
	t.Parallel()
	config := foundationGoConfig(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range []struct {
		mode  string
		total uint32
	}{{"leaf", 1}, {"child", 2}} {
		root := t.TempDir()
		environment := append(proofGoEnvironment(t, config, root, nil), "DWARF_GRAPH_PROOF_CONTROL="+control.mode)
		result := observeGoProcess(t, executable, []string{"-test.run=^TestGoProcessProofChild$"}, environment, root, nil)
		if result.Accounting.Total != control.total {
			t.Fatalf("observer missed exited processes: %d, want %d", result.Accounting.Total, control.total)
		}
	}
	outside := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.test/outside\n\ngo 1.27\n", "outside.go": "package outside\n"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := []map[string]string{
		{"go.mod": "module example.test/proof\n\ngo 1.27\n", "main.go": "package main\nimport (_ \"unsafe\"; _ \"example.test/proof/dep\")\nfunc main() {}\n", "dep/dep.go": "package dep\n"},
		{"go.mod": "module example.test/proof\n\ngo 1.27\nrequire example.test/outside v0.0.0\nreplace example.test/outside => " + filepath.ToSlash(outside) + "\n", "main.go": "package proof\nimport _ \"example.test/outside\"\n"},
		{"go.mod": "module example.test/proof\n\ngo 1.27\nrequire example.invalid/missing v1.0.0\n", "main.go": "package proof\nimport _ \"example.invalid/missing\"\n"},
		{"go.mod": "module example.test/proof\n\ngo 1.27\n", "main.go": "package proof\n", "tagged.go": "//go:build excluded\n\npackage proof\n", "main_test.go": "package proof\nimport _ \"example.invalid/test\"\n", "_skip.go": "package proof\n"},
	}
	metadata := []string{"list", "-e", "-json=Name,ImportPath,Error,Dir,GoFiles,IgnoredGoFiles,IgnoredOtherFiles,CFiles,CgoFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,DepOnly,Imports,ImportMap,Module", "-compiled=false", "-test=false", "-export=false", "-deps=true", "-find=false", "-buildvcs=false", "-pgo=off", "--", "./..."}
	for index, files := range fixtures {
		root := t.TempDir()
		env := proofGoEnvironment(t, config, root, files)
		commands := [][]string{
			{"version"},
			{"list", "-e", "-f", "{{context.ReleaseTags}}", "--", "unsafe"},
			{"env", "-json", "GOMOD", "GOPATH"},
			metadata,
		}
		for _, args := range commands {
			commandEnv := slices.Clone(env)
			if slices.Contains(args, "{{context.ReleaseTags}}") {
				for i, value := range commandEnv {
					if strings.HasPrefix(value, "GO111MODULE=") {
						commandEnv[i] = "GO111MODULE=off"
					}
				}
			}
			result := observeGoProcess(t, config.Toolchain.Path, args, commandEnv, filepath.Join(root, "corpus"), nil)
			if result.Accounting.Total != 1 {
				t.Fatalf("native metadata started descendants: fixture=%d total=%d", index, result.Accounting.Total)
			}
		}
	}
	// This helper-in-Job uses exact immutable repository blobs, not edited files.
	files := foundationDerivedGoFiles(t)
	root := t.TempDir()
	env := proofGoEnvironment(t, config, root, files)
	input := proofHelperInput(t, config, root, files)
	result := observeGoProcess(t, config.Helper.Path, []string{"--graph-helper", "go-analyzer/v1"}, env, root, input)
	if len(result.Stdout) < 4 || int(binary.BigEndian.Uint32(result.Stdout[:4])) != len(result.Stdout)-4 {
		t.Fatal("helper did not return one joined frame")
	}
	// Private helper digests are hex strings, not ledger's durable decoder.
	var response struct {
		Adapter             string `json:"adapter"`
		AnalysisKey         string `json:"analysis_key"`
		ConfigurationSHA256 string `json:"configuration_sha256"`
		Work                int64  `json:"work_used"`
		Nodes               []struct {
			Key                 ledger.NodeKey `json:"key"`
			VersionSHA256       string         `json:"version_sha256"`
			ConfigurationSHA256 string         `json:"configuration_sha256"`
		} `json:"nodes"`
		Edges []struct {
			EdgeID   string         `json:"edge_id"`
			From     ledger.NodeKey `json:"from"`
			To       ledger.NodeKey `json:"to"`
			Evidence struct {
				ArtifactSHA256      string `json:"artifact_sha256"`
				ConfigurationSHA256 string `json:"configuration_sha256"`
			} `json:"evidence"`
		} `json:"edges"`
		Coverage []ledger.CoverageEntry `json:"coverage"`
	}
	if err := json.Unmarshal(result.Stdout[4:], &response); err != nil || response.Adapter != "go" || len(response.Edges) != 2 {
		t.Fatalf("derived helper profile failed: %+v, %v", response, err)
	}
	var request struct {
		Source struct {
			WorkspaceID uuid.UUID `json:"workspace_id"`
		} `json:"source"`
	}
	if err := json.Unmarshal(input[4:], &request); err != nil {
		t.Fatal(err)
	}
	digest := func(value string) {
		t.Helper()
		if _, err := hex.DecodeString(value); err != nil || len(value) != 64 || value != strings.ToLower(value) {
			t.Fatalf("invalid private digest %q", value)
		}
	}
	digest(response.AnalysisKey)
	digest(response.ConfigurationSHA256)
	for _, node := range response.Nodes {
		digest(node.VersionSHA256)
		digest(node.ConfigurationSHA256)
	}
	wantEdges := make(map[[2]ledger.NodeKey]bool)
	for _, pair := range [][2]string{
		{"github.com/Darius-PJ/dwarf-harness/contract/ledger", "internal/undercurrent/graph/model.go"},
		{"github.com/Darius-PJ/dwarf-harness/internal/undercurrent/graph", "probe/main.go"},
	} {
		from, err := graph.ModuleKey(request.Source.WorkspaceID, "go", "workspace", pair[0])
		if err != nil {
			t.Fatal(err)
		}
		to, err := graph.ArtifactKey(request.Source.WorkspaceID, 2, pair[1])
		if err != nil {
			t.Fatal(err)
		}
		wantEdges[[2]ledger.NodeKey{from, to}] = true
	}
	for _, edge := range response.Edges {
		digest(edge.EdgeID)
		digest(edge.Evidence.ArtifactSHA256)
		digest(edge.Evidence.ConfigurationSHA256)
		pair := [2]ledger.NodeKey{edge.From, edge.To}
		if !wantEdges[pair] {
			t.Fatalf("unexpected or duplicate native endpoints: %+v", edge)
		}
		delete(wantEdges, pair)
	}
	wantCoverage := make(map[string]ledger.CoverageEntry)
	for path := range files {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		key, err := graph.ArtifactKey(request.Source.WorkspaceID, 2, path)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		entry := ledger.CoverageEntry{Scope: string(raw), Adapter: "go", State: "complete", Reasons: []string{}}
		if path == "internal/undercurrent/graph/helper_test.go" {
			entry.State, entry.Reasons = "unknown", []string{"unsupported_syntax"}
		}
		wantCoverage[entry.Scope] = entry
	}
	for _, got := range response.Coverage {
		want, ok := wantCoverage[got.Scope]
		if !ok || got.Adapter != want.Adapter || got.State != want.State || !slices.Equal(got.Reasons, want.Reasons) {
			t.Fatalf("unexpected or duplicate native coverage: %+v", got)
		}
		delete(wantCoverage, got.Scope)
	}
	if len(wantCoverage) != 0 {
		t.Fatalf("missing native coverage: %+v", wantCoverage)
	}
	helperInfo, err := os.Stat(config.Helper.Path)
	if err != nil {
		t.Fatal(err)
	}
	toolInfo, err := os.Stat(config.Toolchain.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("qualified profile=go-packages/v1 helper total=%d (reported, not constrained); helper=%+v helper_bytes=%d toolchain=%+v toolchain_bytes=%d target=%s/%s GOROOT=%s configuration=%s", result.Accounting.Total, config.Helper, helperInfo.Size(), config.Toolchain, toolInfo.Size(), config.GOOS, config.GOARCH, config.GOROOT, (graph.ToolConfig{Go: &config}).ConfigurationDigest())
}

func proofHelperInput(t *testing.T, config graph.GoToolConfig, root string, files map[string]string) []byte {
	t.Helper()
	id := uuid.NewV7()
	source := ledger.GraphSource{WorkspaceID: id, Registered: ledger.RecordRef{Seq: 1, Hash: ledger.Digest(sha256.Sum256([]byte("registered")))}, Subject: ledger.RecordRef{Seq: 2, Hash: ledger.Digest(sha256.Sum256([]byte("accepted")))}, Tree: strings.Repeat("a", 40)}
	corpus := []graph.CorpusArtifact{}
	for path, body := range files {
		key, err := graph.ArtifactKey(id, 2, path)
		if err != nil {
			t.Fatal(err)
		}
		corpus = append(corpus, graph.CorpusArtifact{Path: path, Key: key, SHA256: ledger.Digest(sha256.Sum256([]byte(body))), Bytes: []byte(body)})
	}
	slices.SortFunc(corpus, func(a, b graph.CorpusArtifact) int { return strings.Compare(a.Path, b.Path) })
	info, err := os.Stat(config.Toolchain.Path)
	if err != nil {
		t.Fatal(err)
	}
	extension := struct {
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
	}{"go-packages/v1", config.Helper.Version, config.Toolchain.Path, config.Toolchain.SHA256, config.Toolchain.Version, info.Size(), info.ModTime().UnixNano(), config.GOROOT, config.GOOS, config.GOARCH, 20000}
	value := map[string]any{"protocol": "graph-helper/v1", "analysis_key": source.Subject.Hash, "adapter": "go", "configuration_sha256": (graph.ToolConfig{Go: &config}).ConfigurationDigest(), "source": source, "materialization_root": filepath.Join(root, "corpus"), "corpus": corpus, "cursor": ledger.GraphCursor{}, "allowance": 10000, "limits": map[string]int{"frame_bytes": 8 << 20, "corpus_bytes": 8 << 20, "blob_bytes": 1 << 20, "items": 10000}, "go": extension}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	if err := binary.Write(&frame, binary.BigEndian, uint32(len(raw))); err != nil {
		t.Fatal(err)
	}
	_, _ = frame.Write(raw)
	return frame.Bytes()
}

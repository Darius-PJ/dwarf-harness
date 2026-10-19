package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requestFixture(root string, files map[string]string) request {
	r := request{
		Protocol: protocolVersion, AnalysisKey: strings.Repeat("1", 64), Adapter: "go", ConfigurationSHA256: strings.Repeat("2", 64),
		Source:              source{WorkspaceID: "01900000-0000-7000-8000-000000000001", Registered: recordRef{1, strings.Repeat("3", 64)}, Subject: recordRef{2, strings.Repeat("4", 64)}, Tree: strings.Repeat("a", 40)},
		MaterializationRoot: filepath.Join(root, "corpus"), Corpus: []artifact{}, Allowance: maxItems,
		Limits: limits{maxFrame, maxFrame, maxBlob, maxItems},
		Go:     goProfile{Profile: profileVersion, HelperVersion: helperVersion, ToolchainPath: filepath.Join(root, "toolchain", "bin", "go"+exeSuffix()), ToolchainSHA256: strings.Repeat("5", 64), ToolchainVersion: "go1.27.2", ToolchainFileSize: 1, ToolchainModtimeNS: 1, GOROOT: filepath.Join(root, "toolchain"), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, LoadTimeoutMS: 20000},
	}
	for name, body := range files {
		r.Corpus = append(r.Corpus, artifact{Path: name, Key: artifactKey(r.Source.WorkspaceID, 2, name), SHA256: hashBytes([]byte(body)), Bytes: []byte(body)})
	}
	sortCorpus(r.Corpus)
	return r
}

func requestFrame(r request) []byte {
	body, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	return framed(body)
}

func TestGoHelperRequestRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "owned")
	base := requestFixture(root, map[string]string{"a.go": "package a\n", "go.mod": "module example.com/a\n\ngo 1.27\n"})
	valid := requestFrame(base)
	if _, _, err := decodeRequest(valid, root); err != nil {
		t.Fatal(err)
	}
	mutate := func(f func(*request)) []byte {
		r := requestFixture(root, map[string]string{"a.go": "package a\n", "go.mod": "module example.com/a\n\ngo 1.27\n"})
		f(&r)
		return requestFrame(r)
	}
	jsonEdit := func(old, replacement string) []byte {
		return framed(bytes.Replace(valid[4:], []byte(old), []byte(replacement), 1))
	}
	cases := map[string][]byte{
		"short header":             valid[:3],
		"short body":               valid[:len(valid)-1],
		"trailing bytes":           append(append([]byte{}, valid...), 'x'),
		"trailing value":           framed(append(append([]byte{}, valid[4:]...), []byte(" {}")...)),
		"bad UTF8":                 framed(bytes.Replace(valid[4:], []byte("a.go"), []byte{'a', 0xff}, 1)),
		"duplicate member":         jsonEdit(`"protocol":`, `"protocol":"graph-helper/v1","protocol":`),
		"unknown member":           jsonEdit(`"protocol":`, `"unexpected":0,"protocol":`),
		"missing member":           jsonEdit(`"cursor":{"artifact_index":0,"output_index":0},`, ""),
		"null member":              jsonEdit(`"artifact_index":0`, `"artifact_index":null`),
		"fractional integer":       jsonEdit(`"artifact_index":0`, `"artifact_index":0.0`),
		"exponent integer":         jsonEdit(`"artifact_index":0`, `"artifact_index":0e0`),
		"overflow integer":         jsonEdit(`"artifact_index":0`, `"artifact_index":9223372036854775808`),
		"negative integer":         jsonEdit(`"artifact_index":0`, `"artifact_index":-1`),
		"missing Go member":        jsonEdit(`"toolchain_file_size":1,`, ""),
		"duplicate Go member":      jsonEdit(`"goos":`, `"goos":"windows","goos":`),
		"unknown Go member":        jsonEdit(`"goos":`, `"driver":"off","goos":`),
		"protocol mismatch":        mutate(func(r *request) { r.Protocol = "graph-helper/v2" }),
		"adapter mismatch":         mutate(func(r *request) { r.Adapter = "python" }),
		"unbound source":           mutate(func(r *request) { r.Source.Subject.Seq = 1 }),
		"invalid UUID":             mutate(func(r *request) { r.Source.WorkspaceID = "01900000-0000-4000-8000-000000000001" }),
		"invalid tree":             mutate(func(r *request) { r.Source.Tree = "../HEAD" }),
		"wrong digest":             mutate(func(r *request) { r.Corpus[0].SHA256 = strings.Repeat("6", 64) }),
		"wrong artifact owner":     mutate(func(r *request) { r.Corpus[0].Key.OriginID = "01900000-0000-7000-8000-000000000002" }),
		"wrong artifact path":      mutate(func(r *request) { r.Corpus[0].Key.LocalKey = `[2,"other.go"]` }),
		"root escape":              mutate(func(r *request) { r.MaterializationRoot = filepath.Join(root, "..", "corpus") }),
		"corpus escape":            mutate(func(r *request) { r.Corpus[0].Path = "../outside.go" }),
		"unsorted corpus":          mutate(func(r *request) { r.Corpus[0], r.Corpus[1] = r.Corpus[1], r.Corpus[0] }),
		"duplicate corpus":         mutate(func(r *request) { r.Corpus[1] = r.Corpus[0] }),
		"nonzero cursor":           mutate(func(r *request) { r.Cursor.ArtifactIndex = 1 }),
		"relative tool":            mutate(func(r *request) { r.Go.ToolchainPath = "go" }),
		"wrong tool root":          mutate(func(r *request) { r.Go.GOROOT = filepath.Join(root, "elsewhere") }),
		"zero tool digest":         mutate(func(r *request) { r.Go.ToolchainSHA256 = strings.Repeat("0", 64) }),
		"changed profile limits":   mutate(func(r *request) { r.Limits.Items++ }),
		"file directory collision": requestFrame(requestFixture(root, map[string]string{"a.go": "package a", "a.go/b.go": "package b"})),
	}
	if runtime.GOOS == "windows" {
		cases["case alias"] = requestFrame(requestFixture(root, map[string]string{"A.go": "package a", "a.go": "package a"}))
		cases["directory case alias"] = requestFrame(requestFixture(root, map[string]string{"A/one.go": "package a", "a/two.go": "package a"}))
		for _, name := range []string{"nul.go", "x.go:stream", "trailing./a.go", "trailing /a.go"} {
			cases[name] = requestFrame(requestFixture(root, map[string]string{name: "package a"}))
		}
	}
	oversized := append([]byte{}, valid...)
	binary.BigEndian.PutUint32(oversized[:4], maxFrame)
	cases["over frame"] = oversized
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := decodeRequest(data, root); !errors.Is(err, errRequest) {
				t.Fatalf("malformed request accepted or misclassified: %v", err)
			}
		})
	}
}

func FuzzGoHelperRequest(f *testing.F) {
	root, err := filepath.Abs("go-request-fuzz-owned")
	if err != nil {
		f.Fatal(err)
	}
	for _, files := range []map[string]string{nil, {"a.go": "package a\n"}, {"go.mod": "module example.com/a\n\ngo 1.27\n", "a.go": "package a\n"}} {
		f.Add(requestFrame(requestFixture(root, files)))
	}
	f.Add([]byte{0, 0, 0, 1, 0xff})
	f.Add(framed([]byte(`{"protocol":null}`)))
	f.Add(framed([]byte(`{"protocol":1,"protocol":2}`)))
	base := requestFrame(requestFixture(root, map[string]string{"a.go": "package a\n"}))
	for _, replacement := range [][2]string{
		{`"protocol":`, `"extra":0,"protocol":`},
		{`"protocol":`, `"protocol":"graph-helper/v1","protocol":`},
		{`"cursor":{"artifact_index":0,"output_index":0},`, ""},
		{`"artifact_index":0`, `"artifact_index":null`},
		{`"artifact_index":0`, `"artifact_index":0.5`},
		{`"artifact_index":0`, `"artifact_index":0e0`},
		{`"artifact_index":0`, `"artifact_index":9223372036854775808`},
		{`"artifact_index":0`, `"artifact_index":-1`},
		{`"goos":`, `"driver":"off","goos":`},
		{`"toolchain_file_size":1,`, ""},
	} {
		f.Add(framed(bytes.Replace(base[4:], []byte(replacement[0]), []byte(replacement[1]), 1)))
	}
	f.Add(base[:3])
	f.Add(base[:len(base)-1])
	f.Add(append(append([]byte{}, base...), 0))
	f.Add(framed(append(append([]byte{}, base[4:]...), []byte("{}")...)))
	for _, change := range []func(*request){
		func(r *request) { r.Corpus[0].SHA256 = strings.Repeat("f", 64) },
		func(r *request) { r.Corpus[0].Path = "../outside.go" },
		func(r *request) { r.Corpus[0].Key.LocalKey = `[2,"different.go"]` },
		func(r *request) { r.MaterializationRoot = filepath.Dir(root) },
		func(r *request) { r.Allowance = 1 },
		func(r *request) { r.Allowance = maxItems + 1 },
		func(r *request) { r.Go.ToolchainPath = "go" },
		func(r *request) { r.Cursor.OutputIndex = 1 },
	} {
		r := requestFixture(root, map[string]string{"a.go": "package a\n"})
		change(&r)
		f.Add(requestFrame(r))
	}
	for _, files := range []map[string]string{{"A.go": "", "a.go": ""}, {"a.go": "", "a.go/b.go": ""}} {
		f.Add(requestFrame(requestFixture(root, files)))
	}
	for _, size := range []int{maxBlob, maxBlob + 1} {
		f.Add(requestFrame(requestFixture(root, map[string]string{"a.go": strings.Repeat(" ", size)})))
	}
	for _, count := range []int{maxItems, maxItems + 1} {
		files := make(map[string]string, count)
		for index := range count {
			files[fmt.Sprintf("f%05d.go", index)] = ""
		}
		f.Add(requestFrame(requestFixture(root, files)))
	}
	// The work cap dominates these frame-size seeds; it must still refuse
	// them as a whole, rather than silently accepting a truncated prefix.
	for _, size := range []int{maxFrame, maxFrame + 1} {
		body := append(append([]byte{}, base[4:]...), bytes.Repeat([]byte(" "), size-len(base))...)
		f.Add(framed(body))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		r, used, err := decodeRequest(input, root)
		if err != nil {
			return
		}
		if used > r.Allowance || len(input) > maxFrame || len(r.Corpus) > maxItems || r.MaterializationRoot != filepath.Join(root, "corpus") {
			t.Fatalf("accepted input violates its bound: work=%d allowance=%d", used, r.Allowance)
		}
		for _, item := range r.Corpus {
			if len(item.Bytes) > maxBlob || hashBytes(item.Bytes) != item.SHA256 || item.Key != artifactKey(r.Source.WorkspaceID, artifactBirth(item.Key), item.Path) {
				t.Fatal("accepted unbound corpus")
			}
		}
	})
}

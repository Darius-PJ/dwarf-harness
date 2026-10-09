package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// generated is the output of the real art tree. generate is a pure function
// of its input, so the tests that only read the output share one run.
var generated = sync.OnceValues(func() ([]outFile, error) { return generate(os.DirFS("art")) })

// outputs returns the generated sample of the real art tree by path.
func outputs(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := generated()
	if err != nil {
		t.Fatalf("generate on the real art tree: %v", err)
	}
	m := make(map[string][]byte, len(files))
	for _, f := range files {
		m[f.path] = f.data
	}
	return m
}

// loaded returns the parsed and checked real art tree.
func loaded(t *testing.T) *artSet {
	t.Helper()
	s, err := load(os.DirFS("art"))
	if err != nil {
		t.Fatalf("load the real art tree: %v", err)
	}
	return s
}

// realArt returns an in-memory copy of the art tree for a test to change.
func realArt(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	root := os.DirFS("art")
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(root, p)
		m[p] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// readTree returns every regular file under dir by slash-separated relative
// path.
func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		m[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// differing returns the sorted paths whose contents differ between a and b,
// including paths present in only one of them.
func differing(a, b map[string][]byte) []string {
	var d []string
	for p, data := range a {
		if other, ok := b[p]; !ok || !bytes.Equal(data, other) {
			d = append(d, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			d = append(d, p)
		}
	}
	slices.Sort(d)
	return d
}

func TestSampleIsReproducible(t *testing.T) {
	run := func() map[string][]byte {
		files, err := generate(os.DirFS("art"))
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		m := map[string][]byte{}
		for _, f := range files {
			m[f.path] = f.data
		}
		return m
	}
	first, second := run(), run()
	if d := differing(first, second); len(d) > 0 {
		t.Fatalf("nondeterministic: two runs differ in %s", strings.Join(d, ", "))
	}
	if d := differing(first, readTree(t, "sample")); len(d) > 0 {
		t.Fatalf("committed sample is stale; run the command (GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/artgen). Differing: %s", strings.Join(d, ", "))
	}
}

func TestMalformedInputLeavesOutputUntouched(t *testing.T) {
	tmp := t.TempDir()
	art := filepath.Join(tmp, "art")
	if err := os.CopyFS(art, os.DirFS("art")); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(art, "world", "body.stout.pxm")
	data, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, bytes.Replace(data, []byte("anchor 8 16"), []byte("anchor 8 99"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "out")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "sample.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = run(art, out, io.Discard)
	var list errorList
	if !errors.As(err, &list) {
		t.Fatalf("run = %v; want the validation errors", err)
	}
	if got := readTree(t, out); len(got) != 1 || string(got["sample.json"]) != "old" {
		t.Fatalf("output directory after a failed run holds %q; want only sample.json = old", got)
	}
}

func TestRunRemovesStaleOutputs(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "sample")
	write := func(p, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(out, "world", "old.png"), "stale")
	write(filepath.Join(out, "sample.json"), "stale")
	write(filepath.Join(tmp, "beside.txt"), "keep")
	if err := os.MkdirAll(filepath.Join(out, "empty", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := run("art", out, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := outputs(t)
	if d := differing(want, readTree(t, out)); len(d) > 0 {
		t.Fatalf("after run, these paths differ from the generated sample: %s", strings.Join(d, ", "))
	}
	wantDirs := map[string]bool{}
	for p := range want {
		for d := filepath.Dir(filepath.FromSlash(p)); d != "."; d = filepath.Dir(d) {
			wantDirs[d] = true
		}
	}
	err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == out {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		if !wantDirs[rel] {
			t.Errorf("directory %s survives the run", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readTree(t, tmp)["beside.txt"]; string(got) != "keep" {
		t.Errorf("file outside the output directory = %q; want it unchanged", got)
	}
}

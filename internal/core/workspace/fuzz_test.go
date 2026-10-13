package workspace

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func FuzzParseNULList(f *testing.F) {
	for _, s := range []string{"", "a\x00", "line\nname\t\x00", "a\x00broken", "\x00"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := parseNULList(b)
		if err != nil {
			if got != nil {
				t.Fatal("partial result")
			}
		} else {
			var encoded string
			if len(got) > 0 {
				encoded = strings.Join(got, "\x00") + "\x00"
			}
			if encoded != string(b) {
				t.Fatal("lost NUL-list bytes")
			}
		}
		path := strings.ReplaceAll(string(b), "\x00", "x")
		if path != "" {
			got, err := parseNULList([]byte(path + "\x00"))
			if err != nil || len(got) != 1 || got[0] != path {
				t.Fatalf("generated path: %q %v", got, err)
			}
		}
	})
}

func FuzzParseStageEntries(f *testing.F) {
	for _, n := range []int{40, 64} {
		f.Add([]byte("100644 "+strings.Repeat("a", n)+" 0\tline\nname\t\x00"), n == 64)
	}
	f.Add([]byte("broken"), false)
	f.Fuzz(func(t *testing.T, b []byte, sha256 bool) {
		n := 40
		if sha256 {
			n = 64
		}
		got, err := parseStageEntries(b, n)
		if err != nil {
			if got != nil {
				t.Fatal("partial result")
			}
		} else {
			var out strings.Builder
			for _, e := range got {
				fmt.Fprintf(&out, "%s %s %d\t%s\x00", e.Mode, e.OID, e.Stage, e.Path)
			}
			if out.String() != string(b) {
				t.Fatal("lost index bytes")
			}
		}
		path := strings.ReplaceAll(string(b), "\x00", "x")
		if path == "" {
			return
		}
		oid := strings.Repeat("f", n)
		want := []stageEntry{{Mode: "100755", OID: oid, Stage: 0, Path: path}}
		parsed, err := parseStageEntries([]byte("100755 "+oid+" 0\t"+path+"\x00"), n)
		if err != nil || !reflect.DeepEqual(parsed, want) {
			t.Fatalf("generated entry: %#v %v", parsed, err)
		}
	})
}

func FuzzParseCheckAttr(f *testing.F) {
	for _, s := range []string{"", "a\x00filter\x00unset\x00", "line\nname\x00ident\x00unspecified\x00", "a\x00filter\x00", "a\x00filter\x00\x00"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := parseCheckAttr(b)
		if err != nil {
			if got != nil {
				t.Fatal("partial result")
			}
		} else {
			var out strings.Builder
			for _, e := range got {
				fmt.Fprintf(&out, "%s\x00%s\x00%s\x00", e.Path, e.Name, e.Value)
			}
			if out.String() != string(b) {
				t.Fatal("lost attributes")
			}
		}
		path := strings.ReplaceAll(string(b), "\x00", "x")
		if path == "" {
			return
		}
		parsed, err := parseCheckAttr([]byte(path + "\x00filter\x00unset\x00"))
		want := []attribute{{Path: path, Name: "filter", Value: "unset"}}
		if err != nil || !reflect.DeepEqual(parsed, want) {
			t.Fatalf("generated attribute: %#v %v", parsed, err)
		}
	})
}

func FuzzParseWorktreeList(f *testing.F) {
	f.Add([]byte("worktree C:/bare.git\x00bare\x00\x00"), false)
	f.Add([]byte("worktree /scratch\x00HEAD "+strings.Repeat("a", 64)+"\x00branch refs/heads/main\x00\x00"), true)
	f.Add([]byte("worktree /truncated\x00"), false)
	f.Fuzz(func(t *testing.T, b []byte, sha256 bool) {
		n := 40
		if sha256 {
			n = 64
		}
		got, err := parseWorktreeList(b, n)
		if err != nil && got != nil {
			t.Fatal("partial result")
		}
		path := strings.ReplaceAll(string(b), "\x00", "x")
		if path == "" {
			return
		}
		oid := strings.Repeat("a", n)
		parsed, err := parseWorktreeList([]byte("worktree "+path+"\x00HEAD "+oid+"\x00detached\x00\x00"), n)
		want := []worktreeEntry{{Path: path, HEAD: oid, Detached: true}}
		if err != nil || !reflect.DeepEqual(parsed, want) {
			t.Fatalf("generated worktree: %#v %v", parsed, err)
		}
	})
}

func FuzzParseCatFileHeader(f *testing.F) {
	f.Add([]byte(strings.Repeat("1", 40)+" blob 0\n"), false, uint64(0))
	f.Add([]byte(strings.Repeat("f", 64)+" blob 123\n"), true, uint64(123))
	f.Add([]byte("missing\n"), false, uint64(1))
	f.Fuzz(func(t *testing.T, b []byte, sha256 bool, size uint64) {
		n := 40
		if sha256 {
			n = 64
		}
		_, _ = parseCatFileHeader(b, n)
		size &= (1 << 63) - 1
		oid := strings.Repeat("a", n)
		got, err := parseCatFileHeader(fmt.Appendf(nil, "%s blob %d\n", oid, size), n)
		if err != nil || got != (catFileHeader{OID: oid, Kind: "blob", Size: int64(size)}) {
			t.Fatalf("generated header: %#v %v", got, err)
		}
	})
}

func FuzzParseGitVersion(f *testing.F) {
	for _, s := range []string{"git version 2.55.0.windows.3\n", "git version 2.42.0", "git version 2.44.9", "git version 2.45.0", "git version 2.39.5 (Apple Git-154)", "git version x", ""} {
		f.Add([]byte(s), uint16(55), uint16(0))
	}
	f.Fuzz(func(t *testing.T, b []byte, minor, patch uint16) {
		version, err := parseGitVersion(b)
		if err == nil {
			again, err := parseGitVersion([]byte("git version " + version + "\n"))
			if err != nil || again != version {
				t.Fatal("version round trip")
			}
		}
		want := fmt.Sprintf("3.%d.%d.windows.3", minor, patch)
		got, err := parseGitVersion([]byte("git version " + want))
		if err != nil || got != want {
			t.Fatalf("generated version: %s %v", got, err)
		}
	})
}

func FuzzDecodeCaptureInputs(f *testing.F) {
	f.Add([]byte(`{"format":"dwarf-harness/capture-inputs","version":1,"rules":"capture-rules/1","git_version":"2.42.0","object_format":"sha1","core.autocrlf":"false","core.eol":"native","core.filemode":"true","core.ignorecase":"false","core.longpaths":"false","core.precomposeunicode":"false","core.symlinks":"true"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := decodeCaptureInputs(b)
		if err != nil {
			return
		}
		encoded, err := d.encode()
		if err != nil || !bytes.Equal(b, encoded) {
			t.Fatal("accepted noncanonical capture inputs")
		}
	})
}

func FuzzCRLFStreaming(f *testing.F) {
	f.Add([]byte("a\r\nb\n"), []byte{2, 1, 2})
	f.Add([]byte("\r\r\n\x00\n"), []byte{1, 0, 3})
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, b, chunks []byte) {
		h := newCRLFHasher()
		for offset, i := 0, 0; offset < len(b); i++ {
			n := 1
			if len(chunks) > 0 {
				n = int(chunks[i%len(chunks)])
			}
			if n == 0 {
				_, _ = h.Write(nil)
				n = 1
			}
			n = min(n, len(b)-offset)
			if written, err := h.Write(b[offset : offset+n]); err != nil || written != n {
				t.Fatalf("stream write: %d %v", written, err)
			}
			offset += n
		}
		if got, want := h.Sum(), sha256.Sum256(referenceCRLF(b)); got != want {
			t.Fatalf("chunked CRLF hash differs from reference")
		}
	})
}

func FuzzParseMergeTree(f *testing.F) {
	for _, n := range []int{40, 64} {
		oid := strings.Repeat("a", n)
		f.Add([]byte(oid+"\x00"), 0, n == 64)
		f.Add([]byte(oid+"\x00a.txt\x00b.txt\x00"), 1, n == 64)
		f.Add([]byte(oid+"\x00a\x00a\x00"), 1, n == 64)
	}
	f.Add([]byte("broken"), 2, false)
	f.Fuzz(func(t *testing.T, b []byte, exit int, sha256 bool) {
		n := 40
		if sha256 {
			n = 64
		}
		tree, paths, err := parseMergeTree(b, exit, n)
		if err != nil {
			if tree != "" || paths != nil {
				t.Fatal("partial merge result")
			}
		} else {
			out := tree + "\x00"
			seen := map[string]bool{}
			for _, path := range paths {
				if path == "" || seen[path] {
					t.Fatal("empty or repeated conflict path")
				}
				seen[path] = true
				out += path + "\x00"
			}
			if !validOID(tree, n) || out != string(b) || (exit == 0 && len(paths) != 0) || (exit == 1 && len(paths) == 0) || (exit != 0 && exit != 1) {
				t.Fatal("invalid merge round trip")
			}
		}
		path := strings.ReplaceAll(string(b), "\x00", "x")
		if path != "" {
			oid := strings.Repeat("f", n)
			tree, paths, err := parseMergeTree([]byte(oid+"\x00"+path+"\x00"), 1, n)
			if err != nil || tree != oid || !reflect.DeepEqual(paths, []string{path}) {
				t.Fatalf("generated conflict: %q %q %v", tree, paths, err)
			}
		}
	})
}

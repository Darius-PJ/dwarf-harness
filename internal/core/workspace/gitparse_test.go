package workspace

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseNULList(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		bad  bool
	}{
		{"", nil, false}, {"a\tline\nname\x00b\x00", []string{"a\tline\nname", "b"}, false},
		{"a\x00truncated", nil, true}, {"\x00", nil, true},
	} {
		got, err := parseNULList([]byte(tc.in))
		if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parse %q: %q, %v", tc.in, got, err)
		}
	}
}

func TestParseStageEntries(t *testing.T) {
	for _, n := range []int{40, 64} {
		oid := strings.Repeat("a", n)
		got, err := parseStageEntries([]byte("100644 "+oid+" 0\t-a\tline\nname\x00"), n)
		want := []stageEntry{{Mode: "100644", OID: oid, Stage: 0, Path: "-a\tline\nname"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, %v", got, err)
		}
		for _, bad := range []string{"100644 " + oid + " 0\tname", "100644 " + oid + " 4\tname\x00", "100644 " + oid + " 0\t\x00", "100644 " + strings.ToUpper(oid) + " 0\tname\x00", "100644 " + oid + " 0\tname\x00broken\x00"} {
			got, err := parseStageEntries([]byte(bad), n)
			if err == nil || got != nil {
				t.Fatalf("accepted %q: %#v, %v", bad, got, err)
			}
		}
	}
}

func TestParseCheckAttr(t *testing.T) {
	got, err := parseCheckAttr([]byte("line\nname\x00filter\x00unset\x00"))
	want := []attribute{{Path: "line\nname", Name: "filter", Value: "unset"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v", got, err)
	}
	for _, bad := range []string{"name\x00filter\x00", "name\x00filter\x00unset", "name\x00\x00unset\x00"} {
		got, err := parseCheckAttr([]byte(bad))
		if err == nil || got != nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestParseWorktreeList(t *testing.T) {
	oid := strings.Repeat("1", 40)
	in := "worktree /repo\nname\x00bare\x00\x00worktree /scratch\x00HEAD " + oid + "\x00branch refs/heads/main\x00locked reason\nline\x00\x00"
	got, err := parseWorktreeList([]byte(in), 40)
	want := []worktreeEntry{{Path: "/repo\nname", Bare: true}, {Path: "/scratch", HEAD: oid, Branch: "refs/heads/main", Locked: true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v", got, err)
	}
	for _, bad := range []string{"worktree /repo\x00bare\x00", in + "garbage\x00\x00", "worktree /repo\x00HEAD " + oid + "\x00\x00", "worktree /repo\x00bare\x00bare\x00\x00"} {
		got, err := parseWorktreeList([]byte(bad), 40)
		if err == nil || got != nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestParseCatFileHeader(t *testing.T) {
	oid := strings.Repeat("f", 64)
	got, err := parseCatFileHeader([]byte(oid+" blob 123\n"), 64)
	if err != nil || got != (catFileHeader{OID: oid, Kind: "blob", Size: 123}) {
		t.Fatalf("got %#v, %v", got, err)
	}
	for _, bad := range []string{oid + " blob -1\n", oid + " missing\n", oid + " blob 1", oid + " blob 99999999999999999999999\n", oid + " blob 1\nextra"} {
		if _, err := parseCatFileHeader([]byte(bad), 64); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestParseGitVersion(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"git version 2.55.0.windows.3\n", true}, {"git version 2.42.0\n", false}, {"git version 3.0.0", true},
		{"git version 2.44.9\n", false}, {"git version 2.45.0\n", true},
		{"git version 2.45.2 (Apple Git-200)\n", true},
		{"git version 2.39.5 (Apple Git-154)\n", false}, {"git version 2.41.9", false}, {"", false}, {"git version x", false}, {"git version 2.42", false}, {"git version 2.42.0\ntrailing", false},
	} {
		version, err := parseGitVersion([]byte(tc.in))
		if (err == nil) != tc.ok {
			t.Errorf("%q: %q, %v", tc.in, version, err)
		}
		if err == nil && version != strings.TrimSuffix(strings.TrimPrefix(tc.in, "git version "), "\n") {
			t.Errorf("lost vendor version: %q", version)
		}
	}
}

func TestParseCheckAttrEmptyValue(t *testing.T) {
	f := newGitFixture(t, "sha1")
	gitCommand(t, f.env, f.root, nil, "config", "core.attributesFile", "attributes")
	writeTestFile(t, filepath.Join(f.root, "attributes"), []byte("* filter=\n"))
	b := gitCommand(t, f.env, f.root, []byte("candidate\x00"), "check-attr", "-z", "--stdin", "filter")
	got, err := parseCheckAttr(b)
	want := []attribute{{Path: "candidate", Name: "filter", Value: ""}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Git's empty attribute value %q: %#v, %v", b, got, err)
	}
}

func TestParseMergeTree(t *testing.T) {
	for _, n := range []int{40, 64} {
		oid := strings.Repeat("a", n)
		for _, tc := range []struct {
			name, in string
			exit     int
			paths    []string
			bad      bool
		}{
			{"clean", oid + "\x00", 0, nil, false},
			{"one conflict", oid + "\x00a.txt\x00", 1, []string{"a.txt"}, false},
			{"two conflicts", oid + "\x00a.txt\x00b.txt\x00", 1, []string{"a.txt", "b.txt"}, false},
			{"path bytes", oid + "\x00-tab\tline\nname\x00", 1, []string{"-tab\tline\nname"}, false},
			{"empty", "", 0, nil, true},
			{"unterminated tree", oid, 0, nil, true},
			{"unterminated path", oid + "\x00a", 1, nil, true},
			{"empty path", oid + "\x00a\x00\x00", 1, nil, true},
			{"wrong length", oid + "a\x00", 0, nil, true},
			{"wrong hex", strings.Repeat("A", n) + "\x00", 0, nil, true},
			{"clean with paths", oid + "\x00a.txt\x00", 0, nil, true},
			{"conflict without paths", oid + "\x00", 1, nil, true},
			{"duplicate", oid + "\x00a\x00b\x00a\x00", 1, nil, true},
			{"unexpected exit", oid + "\x00", 2, nil, true},
		} {
			t.Run(fmt.Sprintf("%d/%s", n, tc.name), func(t *testing.T) {
				tree, paths, err := parseMergeTree([]byte(tc.in), tc.exit, n)
				if tc.bad {
					if err == nil || tree != "" || paths != nil {
						t.Fatalf("malformed output returned %q %q %v", tree, paths, err)
					}
				} else if err != nil || tree != oid || !reflect.DeepEqual(paths, tc.paths) {
					t.Fatalf("got %q %q %v", tree, paths, err)
				}
			})
		}
	}
}

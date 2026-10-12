package workspace

import (
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

func TestGitVersionGate(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"git version 2.55.0.windows.3\n", true}, {"git version 2.42.0\n", true}, {"git version 3.0.0", true},
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

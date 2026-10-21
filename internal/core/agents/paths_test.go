package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testContext = "019a0700-0000-7000-8000-000000000003"

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func testScope(t *testing.T, root, context string) privateScope {
	t.Helper()
	s, err := newPrivateScope(root, "Scout-a1", context)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBasenameRules(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a.b", "_agent", "é", "agent ", strings.Repeat("a", 129), "Con", "pRn", "AuX", "nUl", "COM1", "com9", "lPt1", "LPT9", "nul.txt"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateBasename(name, nil); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted unsafe basename %q: %v", name, err)
			}
		})
	}
	for _, name := range []string{"a", "Scout-a1", "0_9", "COM0", "COM10", strings.Repeat("a", 128)} {
		if err := validateBasename(name, nil); err != nil {
			t.Fatalf("refused valid basename %q: %v", name, err)
		}
	}
}

func TestBasenameCaseCollision(t *testing.T) {
	t.Parallel()
	if err := validateBasename("sCoUt-A1", []string{"Scout-a1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("case alias accepted: %v", err)
	}
	if err := validateBasename("Scout-a2", []string{"Scout-a1"}); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateScopePaths(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	rel, err := filepath.Rel(mustWorkingDirectory(t), root)
	if err != nil {
		t.Fatal(err)
	}
	s := testScope(t, rel, testContext)
	want := filepath.Join(root, "agents", "Scout-a1", "contexts", testContext)
	if s.directory() != want || !filepath.IsAbs(s.directory()) {
		t.Fatalf("scope directory = %q, want %q", s.directory(), want)
	}
	for _, context := range []string{"../escape", "019A0700-0000-7000-8000-000000000003", "019a0700000070008000000000000003", "00000000-0000-0000-0000-000000000000"} {
		if _, err := newPrivateScope(root, "Scout-a1", context); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted context %q: %v", context, err)
		}
	}
	if err := s.ensureObjects(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(want, "core", "objects")); err != nil || !info.IsDir() {
		t.Fatalf("missing owned object directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(want, "core", "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected projection file: %v", err)
	}
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

func TestPrivateScopeRejectsLinkedAncestors(t *testing.T) {
	t.Parallel()
	for _, component := range []int{0, 1, 2, 3, 4, 5, 6} {
		t.Run([]string{"colony", "agents", "agent", "contexts", "context", "core", "objects"}[component], func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(testRoot(t), "colony")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			s := testScope(t, root, testContext)
			if err := s.ensureObjects(); err != nil {
				t.Fatal(err)
			}
			parts := []string{root, "agents", "Scout-a1", "contexts", testContext, "core", "objects"}
			path := filepath.Join(parts[:component+1]...)
			moved := path + "-real"
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, path); err != nil {
				t.Fatal(err)
			}
			if _, err := s.publishObject([]byte("private")); !errors.Is(err, ErrInvalid) {
				t.Fatalf("publication accepted linked ancestor: %v", err)
			}
			if component <= 4 {
				if err := s.materialize("memory", nil); !errors.Is(err, ErrInvalid) {
					t.Fatalf("materialization accepted linked ancestor: %v", err)
				}
				head, err := hashObject(nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.classifyMaterialization("memory", head, nil); !errors.Is(err, ErrInvalid) {
					t.Fatalf("classification accepted linked ancestor: %v", err)
				}
			}
		})
	}
}

func TestPrivateScopeRejectsFileAncestor(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "agents"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testScope(t, root, "").ensureObjects(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted file ancestor: %v", err)
	}
}

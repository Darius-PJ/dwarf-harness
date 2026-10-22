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
	s, err := newPrivateScope(root, "Scout-a1", context, &directoryDurability{})
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
	s := testScope(t, root, testContext)
	want := filepath.Join(root, "agents", "Scout-a1", "contexts", testContext)
	if s.directory() != want || !filepath.IsAbs(s.directory()) {
		t.Fatalf("scope directory = %q, want %q", s.directory(), want)
	}
	for _, context := range []string{"../escape", "019A0700-0000-7000-8000-000000000003", "019a0700000070008000000000000003", "00000000-0000-0000-0000-000000000000"} {
		if _, err := newPrivateScope(root, "Scout-a1", context, &directoryDurability{}); !errors.Is(err, ErrInvalid) {
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

func TestPrivateScopeRelativeRoot(t *testing.T) {
	// Sequential because t.Chdir changes process-wide state.
	root := testRoot(t)
	t.Chdir(filepath.Dir(root))
	s := testScope(t, filepath.Base(root), testContext)
	want := filepath.Join(root, "agents", "Scout-a1", "contexts", testContext)
	if s.directory() != want || !filepath.IsAbs(s.directory()) {
		t.Fatalf("scope directory = %q, want %q", s.directory(), want)
	}
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

func TestPrivatePublicationRetriesAncestorDurability(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	s := testScope(t, root, testContext)
	fault := errors.New("ancestor flush refused")
	blocked := true
	s.dirs.beforeSyncDir = func(path string) error {
		if blocked && path == filepath.Join(root, "agents") {
			return fault
		}
		return nil
	}
	data := []byte("durable private bytes")
	digest, err := hashObject(data)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(s.objects(), digest.String())
	for attempt := range 2 {
		if _, err := s.publishObject(data); !errors.Is(err, fault) {
			t.Fatalf("attempt %d bypassed ancestor flush failure: %v", attempt+1, err)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("attempt %d published an object without durable ancestors: %v", attempt+1, err)
		}
	}
	// A different context shares the failed ancestor but creates its own
	// descendants. Its success must not depend on which publisher made agents/.
	sibling := testScope(t, root, "019a0700-0000-7000-8000-000000000004")
	sibling.dirs = s.dirs
	if _, err := sibling.publishObject(data); !errors.Is(err, fault) {
		t.Fatalf("sibling bypassed shared ancestor flush failure: %v", err)
	}
	if err := s.materialize("memory", data); !errors.Is(err, fault) {
		t.Fatalf("Markdown bypassed ancestor flush failure: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.directory(), "Memory.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Markdown became visible without durable ancestors: %v", err)
	}

	blocked = false
	for _, scope := range []privateScope{s, sibling} {
		got, err := scope.publishObject(data)
		if err != nil || got != digest {
			t.Fatalf("publication after clearing flush fault: %s, %v", got, err)
		}
		if got := string(readTestFile(t, filepath.Join(scope.objects(), digest.String()))); got != string(data) {
			t.Fatalf("publication after retry changed bytes: %q", got)
		}
	}
	if err := s.materialize("memory", data); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, filepath.Join(s.directory(), "Memory.md"))); got != string(data) {
		t.Fatalf("Markdown after retry changed bytes: %q", got)
	}
}

func TestPrivateDirectoryDurabilityInstance(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	s := testScope(t, root, testContext)
	if _, err := s.publishObject([]byte("first")); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("ancestor flush refused")
	s.dirs.beforeSyncDir = func(path string) error {
		if path == filepath.Join(root, "agents") {
			return fault
		}
		return nil
	}
	digest, err := s.publishObject([]byte("second"))
	if err != nil {
		t.Fatalf("already durable ancestor flushed again: %v", err)
	}
	if got := string(readTestFile(t, filepath.Join(s.objects(), digest.String()))); got != "second" {
		t.Fatalf("second object = %q", got)
	}
	if err := s.materialize("memory", []byte("second")); err != nil {
		t.Fatalf("Markdown did not reuse instance durability: %v", err)
	}
	fresh := testScope(t, root, testContext)
	fresh.dirs.beforeSyncDir = s.dirs.beforeSyncDir
	if _, err := fresh.publishObject([]byte("third")); !errors.Is(err, fault) {
		t.Fatalf("fresh instance trusted another instance's flushes: %v", err)
	}
}

func TestPrivateDirectoryDurabilityRechecksReplacement(t *testing.T) {
	t.Parallel()
	s := testScope(t, testRoot(t), testContext)
	if _, err := s.publishObject([]byte("first")); err != nil {
		t.Fatal(err)
	}
	objects := s.objects()
	if err := os.Rename(objects, objects+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("replacement parent flush refused")
	s.dirs.beforeSyncDir = func(path string) error {
		if path == filepath.Dir(objects) {
			return fault
		}
		return nil
	}
	if _, err := s.publishObject([]byte("second")); !errors.Is(err, fault) {
		t.Fatalf("replacement reused old directory durability: %v", err)
	}
	s.dirs.beforeSyncDir = nil
	digest, err := s.publishObject([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(readTestFile(t, filepath.Join(objects, digest.String()))); got != "second" {
		t.Fatalf("replacement object = %q", got)
	}
}

func TestPrivateDirectoryDurabilitySharedPublishers(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	first := testScope(t, root, testContext)
	second := testScope(t, root, "019a0700-0000-7000-8000-000000000004")
	second.dirs = first.dirs
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, scope := range []privateScope{first, second} {
		go func() {
			<-start
			_, err := scope.publishObject([]byte(scope.context))
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	for _, scope := range []privateScope{first, second} {
		digest, err := hashObject([]byte(scope.context))
		if err != nil {
			t.Fatal(err)
		}
		if got := string(readTestFile(t, filepath.Join(scope.objects(), digest.String()))); got != scope.context {
			t.Fatalf("concurrent publication = %q, want %q", got, scope.context)
		}
	}
}

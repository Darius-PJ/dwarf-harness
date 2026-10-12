package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRejectsNonUTF8Path(t *testing.T) {
	f := newCaptureFixture(t)
	writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "invalid-\xff"), []byte("unrepresentable name\n"))
	_, err := f.capture(context.Background(), sealCapture)
	assertRefusal(t, err, "invalid_path")
}

func TestCaptureFinalCaseCollisions(t *testing.T) {
	requireCaseSensitiveFilesystem(t)
	for _, paths := range [][]string{{"A.txt", "a.txt"}, {"Dir/x", "dir/y"}} {
		t.Run(strings.Join(paths, "+"), func(t *testing.T) {
			f := newCaptureFixture(t)
			f.p.Normalization.IgnoreCase = "false"
			for _, path := range paths {
				writeTestFile(t, filepath.Join(f.s.Worktree.Dir, filepath.FromSlash(path)), []byte("candidate\n"))
			}
			before := snapshotScratch(t, f.s.Worktree.Dir)
			refs := snapshotCaptureSideEffects(t, f.f, f.p).Refs
			_, err := f.capture(context.Background(), sealCapture)
			refusal := assertRefusal(t, err, "case_collision")
			for _, path := range paths {
				if !strings.Contains(refusal.Error(), path) {
					t.Fatalf("collision omitted %q: %v", path, err)
				}
			}
			assertRefusalHasNoEffects(t, f, before, refs)
		})
	}
}

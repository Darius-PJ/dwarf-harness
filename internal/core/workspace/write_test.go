package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"
)

func TestObservationCommitDeterministic(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	ctx := context.Background()
	scratch := uuid.MustParse("018bcfe5-687b-7000-8000-000000000001")
	observation := uuid.MustParse("018bcfe5-687b-7000-8000-000000000002")
	parent, tree := f.s.Worktree.BaseCommit, f.s.Worktree.AnchorTree
	want := fmt.Sprintf("tree %s\nparent %s\nauthor DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\ncommitter DwarfHarness Core <core@dwarf-harness.invalid> 1700000000 +0000\n\ndwarf-harness scratch observation\n\nscratch %s\nobservation %s\n", tree, parent, scratch, observation)
	wantID := strings.TrimSpace(string(gitCommand(t, f.f.env, f.f.root, []byte(want), "hash-object", "-t", "commit", "--stdin", "--no-filters")))
	for range 2 {
		got, err := f.engine.observationCommit(ctx, f.s, tree, parent, scratch, observation)
		if err != nil || got != wantID || got == parent {
			t.Fatalf("deterministic unchanged-tree observation: %s want %s: %v", got, wantID, err)
		}
		if object := string(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "commit", got)); object != want {
			t.Fatalf("observation bytes: %q", object)
		}
	}
	merge, err := f.engine.mergeBackCommit(ctx, f.s, tree, parent, scratch, observation)
	if err != nil {
		t.Fatal(err)
	}
	mergeWant := strings.ReplaceAll(strings.ReplaceAll(want, "scratch observation", "scratch merge-back"), "\nobservation ", "\nmerge-back ")
	if object := string(gitCommand(t, f.f.env, f.f.root, nil, "cat-file", "commit", merge)); object != mergeWant {
		t.Fatalf("merge-back bytes: %q", object)
	}
}

func TestScratchCommitRefCompareAndSwap(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			f := newGitFixture(t, format)
			p, err := f.git.inspect(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			id := uuid.NewV7()
			main := fixtureMain(t, f)
			tree := integrationTree(t, f, map[string]string{"private": "retained\n"})
			commit := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-p", main, "-m", "private", tree)))
			ref := "refs/dwarf-harness/scratch-commits/" + id.String()
			if err := f.git.createScratchCommitRef(ctx, f.colony, p, id, commit); err != nil {
				t.Fatal(err)
			}
			if err := f.git.createScratchCommitRef(ctx, f.colony, p, id, main); !errors.Is(err, ErrGit) {
				t.Fatalf("scratch ref overwritten: %v", err)
			}
			if err := f.git.deleteScratchCommitRef(ctx, f.colony, p, id, main); !errors.Is(err, ErrGit) {
				t.Fatalf("wrong old value deleted scratch ref: %v", err)
			}
			gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
			got, err := f.git.readRef(ctx, f.colony, p.CommonDir, ref+"^{tree}", len(commit))
			if err != nil || got != tree {
				t.Fatalf("private history lost after gc: %s %v", got, err)
			}
			if err := f.git.deleteScratchCommitRef(ctx, f.colony, p, id, commit); err != nil {
				t.Fatal(err)
			}
			got, err = f.git.readRef(ctx, f.colony, p.CommonDir, ref, len(commit))
			if err != nil || got != "" || fixtureMain(t, f) != main {
				t.Fatalf("scratch retention deletion changed wrong ref: %s %v", got, err)
			}
		})
	}
}

package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type finalizationHistory struct {
	gitFixture
	old, target, tree, other, root, merge string
}

func newFinalizationHistory(t *testing.T, format string) finalizationHistory {
	t.Helper()
	f := newGitFixture(t, format)
	h := finalizationHistory{gitFixture: f}
	h.old = strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "refs/heads/main")))
	h.tree = integrationTree(t, f, map[string]string{"base.txt": "one\ntwo\n", "accepted.txt": "derived change\n"})
	commit := func(message string, parents ...string) string {
		args := []string{"commit-tree", "-m", message}
		for _, parent := range parents {
			args = append(args, "-p", parent)
		}
		return strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, append(args, "--end-of-options", h.tree)...)))
	}
	h.target = commit("target", h.old)
	h.other = commit("foreign", h.old)
	h.root = commit("unrelated")
	h.merge = commit("merge", h.old, h.other)
	for name, oid := range map[string]string{"target": h.target, "other": h.other, "root": h.root, "merge": h.merge} {
		gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/"+name, oid)
	}
	return h
}

func finalizationFixture(t *testing.T, h finalizationHistory, packed bool) (*serviceFixture, workspaceState) {
	t.Helper()
	f := serviceFromHistory(t, h.gitFixture)
	if packed {
		gitCommand(t, f.env, f.root, nil, "pack-refs", "--all", "--prune")
	}
	return f, f.svc.workspaces[f.w.ID]
}

// Include missing files in the snapshot: a refusal must not create a loose ref
// over a packed one or consume a lock that belongs to another writer.
func canonicalFiles(t *testing.T, common string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range []string{"refs/heads/main", "refs/heads/main.lock", "packed-refs"} {
		b, err := os.ReadFile(filepath.Join(common, filepath.FromSlash(name)))
		if os.IsNotExist(err) {
			out[name] = "missing"
		} else if err != nil {
			t.Fatal(err)
		} else {
			out[name] = "file\x00" + string(b)
		}
	}
	return out
}

func countCanonicalUpdates(g *gitRunner) *int {
	count := new(int)
	g.afterStart = func(cmd *exec.Cmd) {
		if slices.Contains(cmd.Args, "update-ref") {
			*count++
		}
	}
	return count
}

func assertCanonicalTip(t *testing.T, f *serviceFixture, want string) {
	t.Helper()
	got, err := f.svc.git.readRef(context.Background(), f.colony, f.p.CommonDir, "refs/heads/main", oidLength(f.p.ObjectFormat))
	if err != nil || got != want {
		t.Fatalf("canonical tip = %q, %v; want %s", got, err, want)
	}
}

func TestGuardedMainUpdate(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			h := newFinalizationHistory(t, format)
			for _, packed := range []bool{false, true} {
				name := map[bool]string{false: "loose", true: "packed"}[packed]
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					f, w := finalizationFixture(t, h, packed)
					before := canonicalFiles(t, w.CommonDir)
					var argv []string
					var command *exec.Cmd
					f.svc.git.afterStart = func(cmd *exec.Cmd) {
						if slices.Contains(cmd.Args, "update-ref") {
							if argv != nil {
								t.Error("second canonical update")
							}
							argv = slices.Clone(cmd.Args)
							command = cmd
						}
					}
					state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
					if err != nil || state != mainUpdateMoved {
						t.Fatalf("guarded update: %v %v", state, err)
					}
					assertCanonicalTip(t, f, h.target)
					after := canonicalFiles(t, w.CommonDir)
					if after["packed-refs"] != before["packed-refs"] || after["refs/heads/main.lock"] != before["refs/heads/main.lock"] {
						t.Fatal("CAS changed packed refs or foreign lock")
					}
					t.Logf("Git %s; successful argv=%q; exit=%d; packed-refs unchanged=%t", f.svc.git.version, argv, command.ProcessState.ExitCode(), after["packed-refs"] == before["packed-refs"])
					start := time.Now()
					if err := f.svc.hardenCanonicalStorage(context.Background(), w, h.target, h.tree); err != nil {
						t.Fatal(err)
					}
					t.Logf("hardenCanonicalStorage wall_ms=%.3f", float64(time.Since(start))/float64(time.Millisecond))
					// The same guarded argv with a stale old cannot overwrite target.
					argv = nil
					state, err = f.svc.guardedMainUpdate(context.Background(), w, h.old, h.other, h.tree)
					if err != nil || state != mainUpdateThird || !reflect.DeepEqual(after, canonicalFiles(t, w.CommonDir)) {
						t.Fatalf("stale old overwrote canonical: %v %v", state, err)
					}
					t.Logf("refused argv=%q; exit=%d; canonical bytes unchanged=true; observed=third", argv, command.ProcessState.ExitCode())
				})
			}
			if format != "sha1" {
				return
			}
			for _, row := range []string{"symbolic", "wrong-tree", "wrong-parent", "non-fast-forward", "merge", "tag", "third-tip"} {
				t.Run(row, func(t *testing.T) {
					t.Parallel()
					f, w := finalizationFixture(t, h, true)
					old, target, tree := h.old, h.target, h.tree
					switch row {
					case "symbolic":
						gitCommand(t, f.env, f.root, nil, "symbolic-ref", "refs/heads/main", "refs/remotes/origin/main")
					case "wrong-tree":
						tree = strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", h.old+"^{tree}")))
					case "wrong-parent":
						old = h.other
					case "non-fast-forward":
						target = h.root
					case "merge":
						target = h.merge
					case "tag":
						gitCommand(t, f.env, f.root, nil, "tag", "-a", "target-tag", "-m", "annotated target", h.target)
						target = strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", "refs/tags/target-tag")))
					case "third-tip":
						gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", h.other, h.old)
					}
					before := canonicalFiles(t, w.CommonDir)
					updates := countCanonicalUpdates(f.svc.git)
					state, err := f.svc.guardedMainUpdate(context.Background(), w, old, target, tree)
					if row == "third-tip" {
						if err != nil || state != mainUpdateThird || *updates != 1 {
							t.Fatalf("changed main: %v %v, updates=%d", state, err, *updates)
						}
					} else if err == nil || state != mainUpdateRefused || *updates != 0 {
						t.Fatalf("unsafe target passed preflight: %v %v, updates=%d", state, err, *updates)
					}
					if !reflect.DeepEqual(before, canonicalFiles(t, w.CommonDir)) {
						t.Fatal("refusal changed canonical files")
					}
				})
			}
		})
	}
}

func TestCanonicalUpdateRefLockAndCheckout(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	for _, row := range []string{"foreign-lock", "checked-out"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f, w := finalizationFixture(t, h, true)
			if row == "foreign-lock" {
				writeTestFile(t, filepath.Join(w.CommonDir, "refs", "heads", "main.lock"), []byte("foreign writer owns this lock\n"))
			} else {
				gitCommand(t, f.env, f.root, nil, "worktree", "add", filepath.Join(t.TempDir(), "checked"), "main")
			}
			before := canonicalFiles(t, w.CommonDir)
			updates := countCanonicalUpdates(f.svc.git)
			state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
			if state != mainUpdateRefused || (row == "checked-out" && !errors.Is(err, ErrCanonicalCheckedOut)) || (row == "foreign-lock" && err != nil) {
				t.Fatalf("unsafe update: %v %v", state, err)
			}
			wantUpdates := map[string]int{"foreign-lock": 1, "checked-out": 0}[row]
			if *updates != wantUpdates || !reflect.DeepEqual(before, canonicalFiles(t, w.CommonDir)) {
				t.Fatalf("refusal mutated canonical storage; updates=%d", *updates)
			}
		})
	}
}

func TestCanonicalDurabilityFailuresHoldEffect(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	for _, row := range []string{"obstructed-pack", "missing-objects", "wrong-commit", "wrong-tree"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f, w := finalizationFixture(t, h, false)
			updates := countCanonicalUpdates(f.svc.git)
			state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
			if err != nil || state != mainUpdateMoved {
				t.Fatalf("move: %v %v", state, err)
			}
			target, tree := h.target, h.tree
			switch row {
			case "obstructed-pack":
				path := filepath.Join(w.CommonDir, "objects", "pack")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, path, []byte("not a directory"))
			case "missing-objects":
				path := filepath.Join(w.CommonDir, "objects")
				if err := os.Rename(path, path+"-held"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Rename(path+"-held", path) })
			case "wrong-commit":
				target = h.other
			case "wrong-tree":
				tree = strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "rev-parse", h.old+"^{tree}")))
			}
			before := canonicalFiles(t, w.CommonDir)
			if err := f.svc.hardenCanonicalStorage(context.Background(), w, target, tree); err == nil {
				t.Fatal("ref movement treated as completed durability")
			}
			if *updates != 1 || !reflect.DeepEqual(before, canonicalFiles(t, w.CommonDir)) {
				t.Fatal("durability failure retried or rolled back the effect")
			}
			if !bytes.Equal(readTestFile(t, filepath.Join(w.CommonDir, "refs", "heads", "main")), []byte(h.target+"\n")) {
				t.Fatal("moved main lost on durability failure")
			}
		})
	}
}

func TestGuardedMainUpdateAmbiguousExit(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	f, w := finalizationFixture(t, h, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := 0
	var observed *exec.Cmd
	f.svc.git.afterStart = func(cmd *exec.Cmd) {
		if slices.Contains(cmd.Args, "update-ref") {
			updates++
			observed = cmd
			cmd.Cancel = func() error { return nil }
			cancel()
		}
	}
	state, err := f.svc.guardedMainUpdate(ctx, w, h.old, h.target, h.tree)
	if err != nil || state != mainUpdateMoved || updates != 1 || observed == nil || observed.ProcessState == nil {
		t.Fatalf("ambiguous exit retried or hid real effect: state=%v updates=%d err=%v", state, updates, err)
	}
	assertCanonicalTip(t, f, h.target)
	if err := f.svc.hardenCanonicalStorage(context.Background(), w, h.target, h.tree); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Fatalf("durability retried CAS: %d updates", updates)
	}
}

func TestCanonicalRefBackendUnavailable(t *testing.T) {
	t.Parallel()
	h := newFinalizationHistory(t, "sha1")
	f, w := finalizationFixture(t, h, false)
	gitCommand(t, f.env, f.root, nil, "refs", "migrate", "--ref-format=reftable")
	updates := countCanonicalUpdates(f.svc.git)
	state, err := f.svc.guardedMainUpdate(context.Background(), w, h.old, h.target, h.tree)
	assertUnavailable(t, err, "ref_backend_unavailable")
	if state != mainUpdateRefused || *updates != 0 {
		t.Fatalf("unsupported ref backend mutated: %v, %d updates", state, *updates)
	}
	assertCanonicalTip(t, f, h.old)
}

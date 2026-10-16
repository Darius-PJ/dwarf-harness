package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func currentGeneration(t *testing.T, f *serviceFixture, r Request) ledger.GenerationDerived {
	t.Helper()
	gs := generations(t, f, r.RequestID)
	if len(gs) == 0 {
		t.Fatal("request has no generation")
	}
	return gs[len(gs)-1]
}

func mustFinalize(t *testing.T, f *serviceFixture, r Request) RequestState {
	t.Helper()
	g := currentGeneration(t, f, r)
	state, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func countMainUpdates(g *gitRunner) *int {
	count := new(int)
	g.afterStart = func(cmd *exec.Cmd) {
		if slices.Contains(cmd.Args, "update-ref") && slices.Contains(cmd.Args, "refs/heads/main") {
			*count++
		}
	}
	return count
}

func assertFinalizationHeld(t *testing.T, f *serviceFixture, r Request, g ledger.GenerationDerived, updates *int) {
	t.Helper()
	before := f.store.Durable()
	state, err := f.svc.RequestPromotion(context.Background(), r)
	if err != nil || state.State != RequestFinalizing {
		t.Fatalf("held state: %+v %v", state, err)
	}
	_, err = f.svc.finalize(context.Background(), r.RequestID, g.GenerationID)
	assertUnavailable(t, err, "finalization_pending")
	_, err = f.svc.noChange(context.Background(), r.RequestID)
	assertUnavailable(t, err, "finalization_pending")
	assertUnavailable(t, f.svc.integrate(context.Background(), f.w.ID), "finalization_pending")
	if f.store.Durable() != before || *updates != 1 {
		t.Fatalf("held intent retried: durable=%d/%d updates=%d", f.store.Durable(), before, *updates)
	}
}

func TestFinalizationOrderAndWitness(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, row := range []string{"accepted", "refused", "third", "durability"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			scratch := mustScratch(t, f, uuid.NewV7())
			r := rootRequest(scratch)
			requests := []Request{}
			for n := range 4 {
				writeTestFile(t, filepath.Join(scratch.Dir, fmt.Sprintf("%d.txt", n)), []byte("checkpoint\n"))
				mustAdmit(t, f, r)
				requests = append(requests, r)
				r.Head, r.RequestID = r.RequestID, uuid.NewV7()
			}
			mustIntegrate(t, f)
			first := requests[0]
			g := currentGeneration(t, f, first)
			before := f.store.Durable()
			updates := countMainUpdates(f.svc.git)
			var steps []string
			var foreign string
			var abortForeign func()
			f.svc.deps.beforeFinalizeStep = func(step string) {
				steps = append(steps, step)
				state, err := f.svc.RequestPromotion(context.Background(), first)
				if err != nil {
					t.Fatal(err)
				}
				switch step {
				case "preflight", "intent":
					if state.State != RequestPending || f.store.Durable() != before || *updates != 0 {
						t.Fatal("intent/effect preceded preflight or acknowledgement")
					}
				case "cas":
					if state.State != RequestFinalizing || f.store.Durable() != before+1 || *updates != 0 {
						t.Fatal("CAS preceded durable intent")
					}
					if row == "refused" {
						abortForeign = holdCanonicalTransaction(t, f, g.PredecessorCommit, g.DerivedCommit)
					}
					if row == "third" {
						foreign = advanceMain(t, f, nil)
					}
				case "durability", "acceptance":
					assertCanonicalTip(t, f, g.DerivedCommit)
					if state.State != RequestFinalizing || f.store.Durable() != before+1 || *updates != 1 {
						t.Fatal("ref movement became acceptance")
					}
					// Ref movement cannot witness the speculative descendant.
					child := currentGeneration(t, f, requests[1])
					intent := ledger.FinalizationIntended{IntentID: uuid.NewV7(), RequestID: requests[1].RequestID, GenerationID: child.GenerationID, PredecessorCommit: child.PredecessorCommit, PredecessorTree: child.PredecessorTree, TargetCommit: child.DerivedCommit, TargetTree: child.DerivedTree, FinalizationRules: "finalization-rules/1"}
					if err := f.svc.append(context.Background(), []ledger.Draft{{Type: ledger.TypeFinalizationIntended, TypeVersion: 1, Actor: ledger.ActorCore, ContextID: scratch.ContextID, Payload: encoded(t, intent)}}); !errors.Is(err, ErrInvalid) {
						t.Fatalf("moved ref witnessed descendant: %v", err)
					}
					if _, err := admission(f.svc, r); err == nil {
						t.Fatal("fifth checkpoint admitted before terminal acknowledgement")
					} else {
						assertBusy(t, err, "checkpoint_slots")
					}
					if step == "durability" && row == "durability" {
						pack := filepath.Join(f.p.CommonDir, "objects", "pack")
						if err := os.Remove(pack); err != nil {
							t.Fatal(err)
						}
						writeTestFile(t, pack, []byte("obstructed"))
					}
				case "release":
					if state.State != RequestAccepted && state.State != RequestInterrupted {
						t.Fatalf("release before ending acknowledgement: %+v", state)
					}
				}
			}
			state, err := f.svc.finalize(context.Background(), first.RequestID, g.GenerationID)
			f.svc.deps.beforeFinalizeStep = nil
			switch row {
			case "accepted":
				if err != nil || state.State != RequestAccepted || f.store.Durable() != before+3 || !slices.Equal(steps, []string{"preflight", "intent", "cas", "durability", "acceptance", "release"}) {
					t.Fatalf("ordered success: %+v %v %v", state, err, steps)
				}
				mustAdmit(t, f, r)
				beforeRepeat := f.store.Durable()
				if _, err := f.svc.finalize(context.Background(), first.RequestID, g.GenerationID); err != nil || f.store.Durable() != beforeRepeat || *updates != 1 {
					t.Fatalf("terminal retry repeated effect: %v", err)
				}
			case "refused":
				if err != nil || state.State != RequestInterrupted {
					t.Fatalf("refused CAS did not resolve: %+v %v", state, err)
				}
				assertCanonicalTip(t, f, g.PredecessorCommit)
				commits := serviceCommits(t, f.store)
				last := commits[len(commits)-1]
				if len(last.Records) != 8 {
					t.Fatal("interrupted suffix/unpins not atomic")
				}
				for i, request := range requests {
					p, e := ledger.DecodePromotionFailed(last.Records[i].Payload)
					want := ledger.FailedDependencyFailed
					if i == 0 {
						want = ledger.FailedInterrupted
					}
					if e != nil || p.RequestID != request.RequestID || p.Outcome != want {
						t.Fatalf("ending %d: %+v %v", i, p, e)
					}
				}
				abortForeign()
				r.Head, r.RestartFrom = uuid.UUID{}, requests[3].RequestID
				mustAdmit(t, f, r)
			case "third", "durability":
				if err == nil {
					t.Fatal("ambiguous effect reported success")
				}
				assertFinalizationHeld(t, f, first, g, updates)
				if row == "third" {
					assertCanonicalTip(t, f, foreign)
				}
			}
		})
	}
}

func TestFinalizationPublishesExactIncrementalDelta(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	a, b := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(a.Dir, "a.txt"), []byte("foreign\n"))
	ra := rootRequest(a)
	mustAdmit(t, f, ra)
	if err := os.Rename(filepath.Join(b.Dir, "base.txt"), filepath.Join(b.Dir, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	rb := rootRequest(b)
	sealed := mustAdmit(t, f, rb)
	mustIntegrate(t, f)
	ga, gb := currentGeneration(t, f, ra), currentGeneration(t, f, rb)
	mustFinalize(t, f, ra)
	mustFinalize(t, f, rb)
	commits := serviceCommits(t, f.store)
	accepted, err := ledger.DecodePromotionAccepted(commits[len(commits)-1].Records[0].Payload)
	if err != nil || accepted.PredecessorCommit != ga.DerivedCommit || accepted.AcceptedCommit != gb.DerivedCommit || accepted.AcceptedTree == sealed.SealedTree || !slices.Equal(accepted.ChangedPaths, []string{"base.txt", "renamed.txt"}) {
		t.Fatalf("acceptance is not exact incremental delta: %+v %v", accepted, err)
	}
	assertCanonicalTip(t, f, gb.DerivedCommit)
}

func TestFinalizationStoreFailure(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, gate := range []string{ledger.TypeFinalizationIntended, ledger.TypePromotionAccepted} {
		t.Run(gate, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			scratch := mustScratch(t, f, uuid.NewV7())
			r := rootRequest(scratch)
			writeTestFile(t, filepath.Join(scratch.Dir, "new.txt"), []byte("sealed\n"))
			mustAdmit(t, f, r)
			mustIntegrate(t, f)
			g := currentGeneration(t, f, r)
			updates := countMainUpdates(f.svc.git)
			before := f.store.Durable()
			f.svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
				if drafts[0].Type != gate {
					return
				}
				if gate == ledger.TypeFinalizationIntended && *updates != 0 {
					t.Error("CAS before intent acknowledgement")
				}
				if gate == ledger.TypePromotionAccepted {
					// The slot is still unavailable even though apply has run.
					f.svc.pool.profile.CheckpointsPerPair = 1
					token, err := f.svc.pool.reserve(scratch.Dir, admissionReservation)
					if token != nil {
						token.release()
						t.Error("slot released before acceptance acknowledgement")
					}
					var busy *BusyError
					if !errors.As(err, &busy) || busy.Reason != "checkpoint_slots" {
						t.Errorf("slot admission before acknowledgement: %v", err)
					}
				}
				if err := f.store.Close(); err != nil {
					t.Error(err)
				}
			}
			state, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID)
			if err == nil {
				t.Fatal("closed store reported success")
			}
			f.svc.deps.beforeStoreAppend = nil
			if gate == ledger.TypeFinalizationIntended {
				if state.State != RequestPending || *updates != 0 || f.store.Durable() != before {
					t.Fatalf("failed intent caused effect: %+v %d", state, *updates)
				}
				assertCanonicalTip(t, f, g.PredecessorCommit)
			} else {
				assertCanonicalTip(t, f, g.DerivedCommit)
				assertFinalizationHeld(t, f, r, g, updates)
			}
			if err := f.svc.Close(); err != nil {
				t.Fatal(err)
			}
			f.store, _, err = ledgerstore.Open(f.colony, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			f.svc, _, err = openService(context.Background(), f.store, f.colony, serviceConfig(), serviceDeps{environ: func() []string { return f.env }})
			if err != nil {
				t.Fatal(err)
			}
			if gate == ledger.TypePromotionAccepted {
				updates = countMainUpdates(f.svc.git)
				*updates = 1 // the original service issued the sole CAS
				assertFinalizationHeld(t, f, r, g, updates)
			}
		})
	}
}

func TestNoChangeUsesDurablePredecessor(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	parent, child := rootRequest(scratch), rootRequest(scratch)
	child.Head = parent.RequestID
	mustAdmit(t, f, parent)
	writeTestFile(t, filepath.Join(scratch.Dir, "new.txt"), []byte("child\n"))
	mustAdmit(t, f, child)
	mustIntegrate(t, f)
	gp, gc := currentGeneration(t, f, parent), currentGeneration(t, f, child)
	if gp.DerivedCommit == gp.PredecessorCommit || gp.DerivedTree != gp.PredecessorTree {
		t.Fatal("fixture lacks tree-equal different commits")
	}
	before := f.store.Durable()
	updates := countMainUpdates(f.svc.git)
	if _, err := f.svc.noChange(context.Background(), child.RequestID); err == nil {
		t.Fatal("speculative child treated as durable no-change")
	}
	state, err := f.svc.finalize(context.Background(), parent.RequestID, gp.GenerationID)
	if err != nil || state.State != RequestNoChange || *updates != 0 || f.store.Durable() != before+2 {
		t.Fatalf("tree-equal generation: %+v %v updates=%d", state, err, *updates)
	}
	assertCanonicalTip(t, f, gp.PredecessorCommit)
	if _, err := f.svc.finalize(context.Background(), child.RequestID, gc.GenerationID); err == nil {
		t.Fatal("child finalized over unaccepted no-change commit")
	}
	mustIntegrate(t, f)
	rebuilt := currentGeneration(t, f, child)
	if rebuilt.GenerationID == gc.GenerationID || rebuilt.PredecessorCommit != gp.PredecessorCommit {
		t.Fatal("no-change child not rebuilt against actual predecessor")
	}
	mustFinalize(t, f, child)
	next := rootRequest(scratch)
	next.Head = child.RequestID
	mustAdmit(t, f, next)
	mustIntegrate(t, f)
	witnessed := currentGeneration(t, f, next)
	if witnessed.PredecessorWitnessSeq == 0 {
		t.Fatal("canonical generation omitted accepted witness")
	}
	before = f.store.Durable()
	state, err = f.svc.noChange(context.Background(), next.RequestID)
	if err != nil || state.State != RequestNoChange || f.store.Durable() != before+2 || *updates != 1 {
		t.Fatalf("witnessed no-change: %+v %v", state, err)
	}
	assertCanonicalTip(t, f, rebuilt.DerivedCommit)
	before = f.store.Durable()
	if _, err := f.svc.noChange(context.Background(), next.RequestID); err != nil || f.store.Durable() != before {
		t.Fatalf("no-change repeated its ending: %v", err)
	}
}

func TestAcceptedPredecessorKeepsSpeculativeGeneration(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	first, second := rootRequest(scratch), rootRequest(scratch)
	second.Head = first.RequestID
	writeTestFile(t, filepath.Join(scratch.Dir, "a.txt"), []byte("a\n"))
	mustAdmit(t, f, first)
	writeTestFile(t, filepath.Join(scratch.Dir, "b.txt"), []byte("b\n"))
	mustAdmit(t, f, second)
	mustIntegrate(t, f)
	g := currentGeneration(t, f, second)
	f.svc.deps.beforeStoreAppend = func(drafts []ledger.Draft) {
		if drafts[0].Type != ledger.TypePromotionAccepted {
			return
		}
		// Another Core producer can append between Durable() and Append().
		pin, err := pinDraft(uuid.NewV7(), f.w.InputsSHA256, uuid.UUID{})
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := f.store.Append(context.Background(), []ledger.Draft{pin}); err != nil {
			t.Error(err)
		}
	}
	mustFinalize(t, f, first)
	f.svc.deps.beforeStoreAppend = nil
	commits := serviceCommits(t, f.store)
	actualWitness := commits[len(commits)-1].Records[0].Seq
	if f.svc.witnesses[f.w.ID].sequence != actualWitness {
		t.Fatal("witness kept guessed pre-acknowledgement sequence")
	}
	mustIntegrate(t, f)
	if after := currentGeneration(t, f, second); !reflect.DeepEqual(after, g) {
		t.Fatal("accepted predecessor needlessly rebuilt speculative generation")
	}
	mustFinalize(t, f, second)
	f.reopen(t, serviceConfig())
	// After an acceptance, an external main movement cannot reanchor publication.
	third := rootRequest(scratch)
	third.Head = second.RequestID
	writeTestFile(t, filepath.Join(scratch.Dir, "c.txt"), []byte("c\n"))
	mustAdmit(t, f, third)
	mustIntegrate(t, f)
	advanceMain(t, f, nil)
	before := f.store.Durable()
	if _, err := f.svc.finalize(context.Background(), third.RequestID, currentGeneration(t, f, third).GenerationID); err == nil || f.store.Durable() != before {
		t.Fatal("foreign main became an accepted predecessor")
	}
	if err := f.svc.integrate(context.Background(), f.w.ID); err == nil || f.store.Durable() != before {
		t.Error("integration reanchored after a foreign main movement")
	}
	if _, err := f.svc.finalize(context.Background(), third.RequestID, currentGeneration(t, f, third).GenerationID); err == nil || f.store.Durable() != before {
		t.Fatal("foreign predecessor was finalized after attempted integration")
	}
}

func TestFinalizationPathsetCapBeforeIntent(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	blob := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "hash-object", "-w", "--stdin")))
	env := append(slices.Clone(f.env), "GIT_INDEX_FILE="+filepath.Join(t.TempDir(), "index"))
	gitCommand(t, env, f.root, nil, "read-tree", "--empty")
	var entries strings.Builder
	for i := range 6000 {
		fmt.Fprintf(&entries, "100644 %s\tpaths/%04d-%s\x00", blob, i, strings.Repeat("界", 60))
	}
	gitCommand(t, env, f.root, []byte(entries.String()), "update-index", "-z", "--index-info")
	tree := strings.TrimSpace(string(gitCommand(t, env, f.root, nil, "write-tree")))
	base := strings.TrimSpace(string(gitCommand(t, f.env, f.root, nil, "commit-tree", "-m", "large baseline", tree)))
	gitCommand(t, f.env, f.root, nil, "update-ref", "refs/heads/main", base)
	scratch := mustScratch(t, f, uuid.NewV7())
	if err := os.RemoveAll(filepath.Join(scratch.Dir, "paths")); err != nil {
		t.Fatal(err)
	}
	r := rootRequest(scratch)
	mustAdmit(t, f, r)
	mustIntegrate(t, f)
	before := f.store.Durable()
	updates := countMainUpdates(f.svc.git)
	_, err := f.svc.finalize(context.Background(), r.RequestID, currentGeneration(t, f, r).GenerationID)
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.Reason != "pathset_cap" || f.store.Durable() != before || *updates != 0 {
		t.Fatalf("pathset refused after intent/effect: %v", err)
	}
	assertCanonicalTip(t, f, base)
}

func TestRepresentedTreePreservesPrivateWork(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, outcome := range []string{RequestAccepted, RequestNoChange} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			foreign, task := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
			writeTestFile(t, filepath.Join(foreign.Dir, "foreign.txt"), []byte("foreign\n"))
			if outcome == RequestNoChange {
				writeTestFile(t, filepath.Join(foreign.Dir, "private.txt"), []byte("private\n"))
			}
			a, b := rootRequest(foreign), rootRequest(task)
			mustAdmit(t, f, a)
			writeTestFile(t, filepath.Join(task.Dir, "private.txt"), []byte("private\n"))
			sealed := mustAdmit(t, f, b)
			mustIntegrate(t, f)
			mustFinalize(t, f, a)
			writer := mustWriter(t, f, testWriteBinding(task))
			release, err := writer.Lease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(task.Dir, "later.txt"), []byte("still private\n"))
			gitCommand(t, f.env, task.Dir, nil, "add", "--", "later.txt")
			index := string(gitCommand(t, f.env, task.Dir, nil, "ls-files", "--stage", "-z"))
			tip := fixtureMain(t, f.gitFixture)
			if got := mustFinalize(t, f, b); got.State != outcome {
				t.Fatalf("ending: %+v", got)
			}
			release()
			if got := writer.Scratch(); got != task {
				t.Fatalf("finalization rewrote private identity/tip: %+v", got)
			}
			if got := string(gitCommand(t, f.env, task.Dir, nil, "ls-files", "--stage", "-z")); got != index {
				t.Fatal("finalization changed mutable index")
			}
			if string(readTestFile(t, filepath.Join(task.Dir, "later.txt"))) != "still private\n" {
				t.Fatal("finalization changed later work")
			}
			if outcome == RequestNoChange {
				assertCanonicalTip(t, f, tip)
			}
			writer.Close()
			if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(task)); !errors.Is(err, ErrUnrepresentedEdits) {
				t.Fatalf("later work silently closed: %v", err)
			}
			// Failure makes the next submission a new root over representation,
			// not the original anchor or the cumulative derived tree.
			next := rootRequest(task)
			next.Head = b.RequestID
			mustAdmit(t, f, next)
			mustIntegrate(t, f)
			f.svc.deps.beforeFinalizeStep = func(step string) {
				if step == "cas" {
					writeTestFile(t, filepath.Join(f.p.CommonDir, "refs", "heads", "main.lock"), []byte("foreign\n"))
				}
			}
			if got := mustFinalize(t, f, next); got.State != RequestInterrupted {
				t.Fatalf("failed head: %+v", got)
			}
			f.svc.deps.beforeFinalizeStep = nil
			if err := os.Remove(filepath.Join(f.p.CommonDir, "refs", "heads", "main.lock")); err != nil {
				t.Fatal(err)
			}
			restart := rootRequest(task)
			restart.RestartFrom = next.RequestID
			mustAdmit(t, f, restart)
			admitted := admittedRecord(t, f, restart.RequestID)
			if admitted.BaselineTree != sealed.SealedTree || admitted.DeltaBaseTree != sealed.SealedTree {
				t.Fatalf("new root lost representation: %+v", admitted)
			}
			// End this fixture's last live request without changing main.
			f.svc.mu.Lock()
			drafts, ended, err := f.svc.endingDrafts(f.svc.requests[restart.RequestID], ledger.FailedInterrupted, ledger.ActorCore, task.ContextID, uuid.UUID{})
			f.svc.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.finishFinalization(context.Background(), f.w.ID, drafts, ended); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(task.Dir, "later.txt")); err != nil {
				t.Fatal(err)
			}
			f.reopen(t, serviceConfig())
			if _, err := f.svc.CloseScratch(context.Background(), ownerClosure(task)); err != nil {
				t.Fatalf("represented sealed work refused closure: %v", err)
			}
		})
	}
}

func TestSuccessfulCaptureRetention(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	foreign, task := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(foreign.Dir, "foreign.txt"), []byte("foreign\n"))
	a, b := rootRequest(foreign), rootRequest(task)
	mustAdmit(t, f, a)
	writeTestFile(t, filepath.Join(task.Dir, "private.txt"), []byte("private\n"))
	mustAdmit(t, f, b)
	mustIntegrate(t, f)
	g := currentGeneration(t, f, b)
	mustFinalize(t, f, a)
	mustFinalize(t, f, b)
	admitted := admittedRecord(t, f, b.RequestID)
	for _, failedHead := range []bool{false, true} {
		if failedHead {
			child := rootRequest(task)
			child.Head = b.RequestID
			writeTestFile(t, filepath.Join(task.Dir, "failed.txt"), []byte("failed head\n"))
			mustAdmit(t, f, child)
			f.svc.mu.Lock()
			drafts, ended, err := f.svc.endingDrafts(f.svc.requests[child.RequestID], ledger.FailedInterrupted, ledger.ActorCore, task.ContextID, uuid.UUID{})
			f.svc.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.finishFinalization(context.Background(), f.w.ID, drafts, ended); err != nil {
				t.Fatal(err)
			}
		}
		f.reopen(t, serviceConfig())
		gitCommand(t, f.env, f.root, nil, "gc", "--prune=now")
		ref := "refs/dwarf-harness/captures/" + admitted.CaptureID.String()
		if got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || got != admitted.SealedTree {
			t.Fatalf("represented capture lost after failedHead=%t: %s %v", failedHead, got, err)
		}
		gitCommand(t, f.env, f.root, nil, "cat-file", "-e", admitted.SealedTree)
		ref = "refs/dwarf-harness/generations/" + g.GenerationID.String()
		if got, err := f.git.readRef(context.Background(), f.colony, f.p.CommonDir, ref, 40); err != nil || got != "" {
			t.Fatalf("accepted generation retained separately from main: %s %v", got, err)
		}
	}
}

func TestFinalizationGenerationBinding(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(scratch)
	writeTestFile(t, filepath.Join(scratch.Dir, "one.txt"), []byte("one\n"))
	mustAdmit(t, f, r)
	mustIntegrate(t, f)
	old := currentGeneration(t, f, r)
	advanceMain(t, f, nil)
	mustIntegrate(t, f)
	current := currentGeneration(t, f, r)
	before := f.store.Durable()
	updates := countMainUpdates(f.svc.git)
	for _, id := range []uuid.UUID{old.GenerationID, uuid.NewV7()} {
		if _, err := f.svc.finalize(context.Background(), r.RequestID, id); err == nil || f.store.Durable() != before || *updates != 0 {
			t.Fatalf("stale or foreign generation finalized: %v", err)
		}
	}
	if current.GenerationID == old.GenerationID {
		t.Fatal("fixture did not supersede generation")
	}
	mustFinalize(t, f, r)
}

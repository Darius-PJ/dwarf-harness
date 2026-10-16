package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func TestSuccessorEligibilityFromParentTree(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	writeTestFile(t, filepath.Join(s.Dir, "n.txt"), []byte("first\n"))
	parent := mustAdmit(t, f, r)
	writeTestFile(t, filepath.Join(s.Dir, ".gitignore"), []byte("n.txt\n"))
	writeTestFile(t, filepath.Join(s.Dir, "n.txt"), []byte("second\n"))
	r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	child := mustAdmit(t, f, r)
	want := integrationTree(t, f.gitFixture, map[string]string{"base.txt": "one\ntwo\n", ".gitignore": "n.txt\n", "n.txt": "second\n"})
	if child.SealedTree != want {
		t.Fatalf("successor ignored tracked parent content: %s want %s", child.SealedTree, want)
	}
	p := admittedRecord(t, f, r.RequestID)
	if p.BaselineTree != parent.SealedTree || p.DeltaBaseTree != parent.SealedTree {
		t.Fatalf("parent bindings: %+v", p)
	}
	anchor, err := f.capture(s, observeCapture)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.Tree == child.SealedTree {
		t.Fatal("fixture does not distinguish anchor eligibility")
	}
}

func TestForeignPredecessorNeverSetsEligibility(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	a, b := mustScratch(t, f, uuid.NewV7()), mustScratch(t, f, uuid.NewV7())
	r := rootRequest(a)
	mustAdmit(t, f, r)
	writeTestFile(t, filepath.Join(b.Dir, "y.txt"), []byte("foreign\n"))
	mustAdmit(t, f, rootRequest(b))
	writeTestFile(t, filepath.Join(a.Dir, ".gitignore"), []byte("y.txt\n"))
	writeTestFile(t, filepath.Join(a.Dir, "y.txt"), []byte("private ignored\n"))
	r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	state := mustAdmit(t, f, r)
	want := integrationTree(t, f.gitFixture, map[string]string{"base.txt": "one\ntwo\n", ".gitignore": "y.txt\n"})
	if state.SealedTree != want {
		t.Fatal("foreign predecessor changed eligibility")
	}
}

func TestAdmissionFailureRollsBack(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	f.svc.pool.profile.CheckpointsPerPair = 1
	// Reissue the uncommitted identities on retry: rollback must not burn them.
	ids := [2]uuid.UUID{uuid.NewV7(), uuid.NewV7()}
	minted := 0
	f.svc.deps.newID = func() uuid.UUID {
		id := ids[minted%len(ids)]
		minted++
		return id
	}
	writeTestFile(t, filepath.Join(s.Dir, "not-committed.txt"), []byte("unique manifest body\n"))
	before := f.store.Durable()
	f.svc.deps.beforeAppend = func() {
		entries, err := os.ReadDir(filepath.Join(f.colony, "bodies"))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(f.colony, "bodies", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				path := filepath.Join(f.colony, "bodies", entry.Name(), file.Name())
				if strings.Contains(string(readTestFile(t, path)), "dwarf-harness/capture-manifest") {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	r := rootRequest(s)
	_, err := admission(f.svc, r)
	if !errors.Is(err, ledgerstore.ErrBodyUnpublished) {
		t.Fatalf("definite failure: %v", err)
	}
	f.svc.deps.beforeAppend = nil
	if f.store.Durable() != before {
		t.Fatal("failed append recorded admission")
	}
	refs, err := f.svc.listRefs(context.Background(), f.svc.workspaces[f.w.ID], "refs/dwarf-harness/captures/")
	if err != nil || len(refs) != 0 {
		t.Fatalf("failed capture retained: %v %v", refs, err)
	}
	assertUnavailable(t, promotionError(context.Background(), f.svc, r), "no_validator")
	if state := mustAdmit(t, f, r); state.State != RequestPending || state.Checkpoint != 1 {
		t.Fatalf("retry after rollback: %+v", state)
	}
}

func TestParentFailureDuringCaptureRefused(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("local\ntwo\n"))
	r1 := rootRequest(s)
	mustAdmit(t, f, r1)
	r2 := rootRequest(s)
	r2.Head = r1.RequestID
	mustAdmit(t, f, r2)
	r3 := rootRequest(s)
	r3.Head = r2.RequestID
	var once atomic.Bool
	f.svc.deps.beforeAppend = func() {
		if once.CompareAndSwap(false, true) {
			advanceMain(t, f, map[string]string{"base.txt": "canonical\ntwo\n"})
			mustIntegrate(t, f)
		}
	}
	_, err := admission(f.svc, r3)
	var head *HeadError
	if !errors.As(err, &head) || !head.RestartRequired || head.Head != r2.RequestID {
		t.Fatalf("parent failure refusal: %v", err)
	}
	refs, err := f.svc.listRefs(context.Background(), f.svc.workspaces[f.w.ID], "refs/dwarf-harness/captures/")
	if err != nil || len(refs) != 0 {
		t.Fatalf("ref leak: %v %v", refs, err)
	}
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			if strings.Contains(string(record.Payload), r3.RequestID.String()) {
				t.Fatal("refused successor recorded")
			}
		}
	}
	r := rootRequest(s)
	r.RestartFrom = r2.RequestID
	assertUnavailable(t, promotionError(context.Background(), f.svc, r), "no_validator")
}

func TestConcurrentRequestsOnePair(t *testing.T) {
	t.Parallel()
	t.Run("distinct_and_same_ids", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		admissions := 0
		for _, same := range []bool{false, true} {
			start := make(chan struct{})
			var wg sync.WaitGroup
			shared := uuid.NewV7()
			errs := make(chan error, 8)
			f.svc.mu.Lock()
			head := f.svc.scratches[s.ID].head
			f.svc.mu.Unlock()
			for range 8 {
				wg.Go(func() {
					<-start
					r := rootRequest(s)
					if same {
						r.RequestID = shared
					}
					r.Head = head
					_, err := admission(f.svc, r)
					errs <- err
				})
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil && !errors.Is(err, ErrBusy) && (!errors.Is(err, ErrStaleHead) || same) {
					t.Fatal(err)
				}
			}
			count := 0
			for _, commit := range serviceCommits(t, f.store) {
				for _, record := range commit.Records {
					if record.Type == ledger.TypePromotionAdmitted {
						count++
					}
				}
			}
			if (!same && count == 0) || (same && count != admissions+1) {
				t.Fatalf("same IDs %t: admissions %d -> %d", same, admissions, count)
			}
			admissions = count
		}
		var previous uuid.UUID
		var checkpoint int64
		for _, commit := range serviceCommits(t, f.store) {
			for _, record := range commit.Records {
				if record.Type != "workspace.promotion_admitted" {
					continue
				}
				p, err := ledger.DecodePromotionAdmitted(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				checkpoint++
				if p.Checkpoint != checkpoint || p.ParentRequestID != previous || checkpoint > 4 {
					t.Fatalf("fork or overrun: %+v", p)
				}
				previous = p.RequestID
			}
		}
	})
	t.Run("head_rechecked_under_exclusion", func(t *testing.T) {
		t.Parallel()
		f := newServiceFixture(t)
		s := mustScratch(t, f, uuid.NewV7())
		loser := rootRequest(s)
		var once atomic.Bool
		var before requestEffects
		f.svc.deps.beforeRequestReserve = func() {
			if once.CompareAndSwap(false, true) {
				mustAdmit(t, f, rootRequest(s))
				writeTestFile(t, filepath.Join(s.Dir, "must-not-read.txt"), []byte("loser's uncaptured content"))
				before = snapshotRequestEffects(t, f)
			}
		}
		_, err := admission(f.svc, loser)
		if !errors.Is(err, ErrStaleHead) {
			t.Fatalf("racing head: %v", err)
		}
		assertRequestEffects(t, f, before)
	})
}

func TestAdmissionUsesRecordedInputsRevision(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	writeTestFile(t, filepath.Join(s.Dir, "later.tmp"), []byte("captured before update\n"))
	f.svc.deps.beforeAppend = func() {
		f.svc.deps.beforeAppend = nil
		p := f.p
		p.LocalExcludes = HostFile{Present: true, Bytes: []byte("later.tmp\n")}
		if _, err := f.svc.UpdateInputs(context.Background(), f.w.ID, 1, p); err != nil {
			t.Fatal(err)
		}
	}
	want := mustAdmit(t, f, r)
	a := admittedRecord(t, f, r.RequestID)
	if a.InputsRevision != 1 || a.CaptureInputsSHA256 != f.w.InputsSHA256 {
		t.Fatalf("capture lost its historical inputs: %+v", a)
	}
	if got := string(gitCommand(t, f.env, f.root, nil, "cat-file", "blob", want.SealedTree+":later.tmp")); got != "captured before update\n" {
		t.Fatalf("capture used updated eligibility: %q", got)
	}
	old := f.w.InputsSHA256.String()
	if err := os.Remove(filepath.Join(f.colony, "bodies", old[:2], old)); err != nil {
		t.Fatal(err)
	}
	if report := f.reopen(t, serviceConfig()); len(report.Pending) != 1 || report.Pending[0] != "promotion_recovery_pending "+f.w.ID.String() {
		t.Fatalf("Open read superseded input bytes: %+v", report)
	}
	if got, err := f.svc.RequestPromotion(context.Background(), r); err != nil || got != want {
		t.Fatalf("historical admission replay: %+v %v", got, err)
	}
}

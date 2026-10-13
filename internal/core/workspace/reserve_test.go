package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

func testReservationPool(t *testing.T, profile *CapacityProfile) *reservationPool {
	t.Helper()
	p, err := newReservationPool(profile)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertBusy(t *testing.T, err error, reason string) {
	t.Helper()
	var busy *BusyError
	if !errors.Is(err, ErrBusy) || !errors.As(err, &busy) || busy.Reason != reason {
		t.Fatalf("want busy %s, got %v", reason, err)
	}
}

func TestExclusiveIsImmediateBusy(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	before := snapshotCaptureSideEffects(t, f.f, f.p)
	for _, shared := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			profile := CapacityProfile{MaxFiles: 100, MaxBytes: 1024, BytesInFlight: 2048, CheckpointsPerPair: 4, GenerationWindow: 4}
			f.pool = testReservationPool(t, &profile)
			var release func()
			if shared {
				var err error
				release, err = f.pool.beginWrite(context.Background(), f.s.Worktree.Dir)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				r, err := f.pool.reserve(f.s.Worktree.Dir, admissionReservation)
				if err != nil {
					t.Fatal(err)
				}
				release = r.release
			}
			defer release()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			returned := make(chan error, 1)
			go func() { _, err := f.capture(ctx, sealCapture); returned <- err }()
			synctest.Wait()
			select {
			case err := <-returned:
				assertBusy(t, err, "scratch_busy")
			default:
				t.Fatal("exclusive acquisition waited")
			}
		})
		if shared {
			path := filepath.Join(f.s.Worktree.Dir, "writer.txt")
			writeTestFile(t, path, []byte("writer continued\n"))
			if string(readTestFile(t, path)) != "writer continued\n" {
				t.Fatal("writer was interrupted")
			}
		}
	}
	assertCaptureSideEffects(t, f.f, f.p, before)
}

func TestWriterWaitsForCapture(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		profile := CapacityProfile{MaxFiles: 1, MaxBytes: 10, BytesInFlight: 10, CheckpointsPerPair: 4, GenerationWindow: 4}
		pool := testReservationPool(t, &profile)
		r, err := pool.reserve("scratch", admissionReservation)
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan func(), 1)
		failed := make(chan error, 1)
		go func() {
			release, err := pool.beginWrite(context.Background(), "scratch")
			if err != nil {
				failed <- err
				return
			}
			entered <- release
		}()
		synctest.Wait()
		select {
		case <-entered:
			t.Fatal("writer interrupted exclusive holder")
		default:
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := pool.beginWrite(ctx, "scratch"); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled writer: %v", err)
		}
		r.release()
		synctest.Wait()
		select {
		case release := <-entered:
			release()
		case err := <-failed:
			t.Fatal(err)
		default:
			t.Fatal("writer did not resume")
		}
	})
}

func TestPoolIsAtomic(t *testing.T) {
	t.Parallel()
	profile := CapacityProfile{MaxFiles: 1, MaxBytes: 10, BytesInFlight: 10, CheckpointsPerPair: 4, GenerationWindow: 4}
	pool := testReservationPool(t, &profile)
	first, err := pool.reserve("a", admissionReservation)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	_, err = pool.reserve("b", admissionReservation)
	assertBusy(t, err, "capture_budget")
	release, err := pool.beginWrite(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	release()
	first.release()
	second, err := pool.reserve("b", admissionReservation)
	if err != nil {
		t.Fatal(err)
	}
	second.release()
}

func TestCapacityProfileRefusals(t *testing.T) {
	t.Parallel()
	for _, change := range []func(*CapacityProfile){
		func(p *CapacityProfile) { p.MaxFiles = 0 },
		func(p *CapacityProfile) { p.MaxBytes = 0 },
		func(p *CapacityProfile) { p.BytesInFlight = p.MaxBytes - 1 },
		func(p *CapacityProfile) { p.CheckpointsPerPair = 0 },
		func(p *CapacityProfile) { p.GenerationWindow = 0 },
	} {
		profile := ProvisionalCapacityProfile()
		change(&profile)
		if _, err := newReservationPool(&profile); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid profile %#v: %v", profile, err)
		}
	}
	pool := testReservationPool(t, nil)
	if _, err := pool.reserve("a", admissionReservation); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing profile: %v", err)
	}
	// A missing scratch may close without a capture or a capacity profile.
	r, err := pool.reserve("a", closureReservation)
	if err != nil {
		t.Fatal(err)
	}
	r.release()
}

func TestReservationReleasedOnEveryPath(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"refusal", "git error", "cancelled context"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newCaptureFixture(t)
			f.pool = testReservationPool(t, &CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 4, GenerationWindow: 4})
			ctx := context.Background()
			switch kind {
			case "refusal":
				writeTestFile(t, filepath.Join(f.s.Worktree.Dir, "mixed.txt"), []byte("a\r\nb\n"))
			case "git error":
				f.s.Worktree.AnchorTree = strings.Repeat("0", len(f.s.Worktree.AnchorTree))
			case "cancelled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := f.capture(ctx, sealCapture)
			switch kind {
			case "refusal":
				assertRefusal(t, err, "lossy_normalization")
			case "git error":
				var gitErr *GitError
				if !errors.As(err, &gitErr) {
					t.Fatalf("want Git failure: %v", err)
				}
			case "cancelled context":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("want cancelled capture: %v", err)
				}
			}
			next, err := f.pool.reserve(f.s.Worktree.Dir, admissionReservation)
			if err != nil {
				t.Fatalf("failed capture leaked its reservation: %v", err)
			}
			next.release()
		})
	}
}

func TestObserveReturnsPoolButKeepsExclusion(t *testing.T) {
	t.Parallel()
	f := newCaptureFixture(t)
	other, err := f.f.git.createWorktree(context.Background(), f.f.colony, f.p, f.s.InputsDir, filepath.Join(f.f.colony, "scratch", "87654321"), "second-observer")
	if err != nil {
		t.Fatal(err)
	}
	second := f.s
	second.Worktree = other
	synctest.Test(t, func(t *testing.T) {
		profile := CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}
		pool := testReservationPool(t, &profile)
		r, err := pool.reserve(f.s.Worktree.Dir, closureReservation)
		if err != nil {
			t.Fatal(err)
		}
		defer r.release()
		_, err = f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, observeCapture)
		assertReservationInvalid(t, err)
		if err := r.reserveCaptureBytes(); err != nil {
			t.Fatal(err)
		}
		first, err := f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, observeCapture)
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan func(), 1)
		failed := make(chan error, 1)
		go func() {
			release, err := pool.beginWrite(context.Background(), f.s.Worktree.Dir)
			if err != nil {
				failed <- err
				return
			}
			entered <- release
		}()
		synctest.Wait()
		select {
		case release := <-entered:
			release()
			t.Fatal("observe released its caller's exclusion")
		case err := <-failed:
			t.Fatal(err)
		default:
		}
		_, err = f.engine.capture(context.Background(), f.s, f.s.Worktree.AnchorTree, r, observeCapture)
		assertReservationInvalid(t, err)
		assertReservationInvalid(t, r.reserveCaptureBytes())
		next, err := pool.reserve(second.Worktree.Dir, admissionReservation)
		if err != nil {
			t.Fatalf("completed observe retained the bytes pool: %v", err)
		}
		defer next.release()
		captured, err := f.engine.capture(context.Background(), second, second.Worktree.AnchorTree, next, observeCapture)
		if err != nil || captured.Tree != first.Tree {
			t.Fatalf("second scratch could not capture: %v", err)
		}
		r.release()
		synctest.Wait()
		select {
		case release := <-entered:
			release()
		case err := <-failed:
			t.Fatal(err)
		default:
			t.Fatal("writer did not resume after owner release")
		}
	})
}

func assertReservationInvalid(t *testing.T, err error) {
	t.Helper()
	var invalid *reservationError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &invalid) {
		t.Fatalf("want typed invalid reservation, got %v", err)
	}
}

func TestCheckpointSlotsPerPair(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		profile := ProvisionalCapacityProfile()
		pool := testReservationPool(t, &profile)
		for range profile.CheckpointsPerPair {
			r, err := pool.reserve("a", admissionReservation)
			if err != nil {
				t.Fatal(err)
			}
			r.keepSlot()
			r.release()
		}
		before := pool.bytes
		_, err := pool.reserve("a", admissionReservation)
		assertBusy(t, err, "checkpoint_slots")
		if pool.bytes != before {
			t.Fatal("slot refusal took bytes")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan func(), 1)
		go func() {
			release, err := pool.beginWrite(ctx, "a")
			if err == nil {
				done <- release
			}
		}()
		synctest.Wait()
		select {
		case release := <-done:
			release()
		default:
			cancel()
			t.Fatal("slot refusal retained exclusion")
		}
		other, err := pool.reserve("b", admissionReservation)
		if err != nil {
			t.Fatalf("pair A consumed pair B's capacity: %v", err)
		}
		other.release()
		pool.freeSlot("a")
		next, err := pool.reserve("a", admissionReservation)
		if err != nil {
			t.Fatalf("ended checkpoint retained its slot: %v", err)
		}
		next.release()
		// Replay restores counts, not reservations or the bytes pool.
		pool.setLive("b", profile.CheckpointsPerPair)
		_, err = pool.reserve("b", admissionReservation)
		assertBusy(t, err, "checkpoint_slots")
	})
}

func TestSlotReturnedUnlessKept(t *testing.T) {
	t.Parallel()
	profile := ProvisionalCapacityProfile()
	profile.CheckpointsPerPair = 1
	pool := testReservationPool(t, &profile)
	first, err := pool.reserve("a", admissionReservation)
	if err != nil {
		t.Fatal(err)
	}
	first.release()
	kept, err := pool.reserve("a", admissionReservation)
	if err != nil {
		t.Fatalf("unkept slot leaked: %v", err)
	}
	first.keepSlot() // A released token cannot acquire or retain anything.
	kept.keepSlot()
	kept.keepSlot()
	kept.release()
	kept.release()
	_, err = pool.reserve("a", admissionReservation)
	assertBusy(t, err, "checkpoint_slots")
	if pool.bytes != 0 {
		t.Fatal("kept slot retained capture bytes")
	}
	pool.freeSlot("a")
	next, err := pool.reserve("a", admissionReservation)
	if err != nil {
		t.Fatalf("keepSlot counted twice: %v", err)
	}
	next.keepSlot()
	next.release()
	kept.release() // A stale release must not decrement a newer live slot.
	_, err = pool.reserve("a", admissionReservation)
	assertBusy(t, err, "checkpoint_slots")
}

func TestAdmissionBusyIsAtomic(t *testing.T) {
	t.Parallel()
	profile := ProvisionalCapacityProfile()
	profile.CheckpointsPerPair = 1
	pool := testReservationPool(t, &profile)
	writer, err := pool.beginWrite(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.reserve("a", admissionReservation)
	assertBusy(t, err, "scratch_busy")
	writer()
	r, err := pool.reserve("a", admissionReservation)
	if err != nil {
		t.Fatalf("busy attempt leaked a provisional slot: %v", err)
	}
	_, err = pool.reserve("a", admissionReservation)
	assertBusy(t, err, "scratch_busy")
	if pool.bytes != profile.MaxBytes {
		t.Fatal("busy attempt changed the byte claim")
	}
	r.release()
	next, err := pool.reserve("a", admissionReservation)
	if err != nil {
		t.Fatalf("exclusive-busy attempt leaked a slot: %v", err)
	}
	next.release()
}

func TestConcurrentAdmissionReservations(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		profile := ProvisionalCapacityProfile()
		pool := testReservationPool(t, &profile)
		results := make(chan error, 16)
		start := make(chan struct{})
		for range 16 {
			go func() {
				<-start
				r, err := pool.reserve("a", admissionReservation)
				if err == nil {
					r.keepSlot()
					r.release()
				}
				results <- err
			}()
		}
		close(start)
		synctest.Wait()
		admitted := 0
		for range 16 {
			select {
			case err := <-results:
				if err == nil {
					admitted++
				} else {
					var busy *BusyError
					if !errors.As(err, &busy) || (busy.Reason != "scratch_busy" && busy.Reason != "checkpoint_slots") {
						t.Fatalf("loser: %v", err)
					}
				}
			default:
				t.Fatal("admission waited")
			}
		}
		if admitted < 1 || admitted > profile.CheckpointsPerPair {
			t.Fatalf("admitted %d, bound %d", admitted, profile.CheckpointsPerPair)
		}
		// Fill any remaining slots, then prove the first over-bound try is busy.
		for admitted < profile.CheckpointsPerPair {
			r, err := pool.reserve("a", admissionReservation)
			if err != nil {
				t.Fatalf("loser retained a slot: %v", err)
			}
			r.keepSlot()
			r.release()
			admitted++
		}
		_, err := pool.reserve("a", admissionReservation)
		assertBusy(t, err, "checkpoint_slots")
	})
}

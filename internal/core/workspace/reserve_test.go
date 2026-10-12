package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

func testReservationPool(t *testing.T, profile *CaptureProfile) *reservationPool {
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
	f := newCaptureFixture(t)
	before := snapshotCaptureSideEffects(t, f.f, f.p)
	for _, shared := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			profile := CaptureProfile{MaxFiles: 100, MaxBytes: 1024, BytesInFlight: 2048}
			f.pool = testReservationPool(t, &profile)
			var release func()
			if shared {
				var err error
				release, err = f.pool.beginWrite(context.Background(), f.s.Worktree.Dir)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				r, err := f.pool.reserve(f.s.Worktree.Dir, true)
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
	synctest.Test(t, func(t *testing.T) {
		profile := CaptureProfile{MaxFiles: 1, MaxBytes: 10, BytesInFlight: 10}
		pool := testReservationPool(t, &profile)
		r, err := pool.reserve("scratch", true)
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
	profile := CaptureProfile{MaxFiles: 1, MaxBytes: 10, BytesInFlight: 10}
	pool := testReservationPool(t, &profile)
	first, err := pool.reserve("a", true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	_, err = pool.reserve("b", true)
	assertBusy(t, err, "capture_budget")
	release, err := pool.beginWrite(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	release()
	first.release()
	second, err := pool.reserve("b", true)
	if err != nil {
		t.Fatal(err)
	}
	second.release()
}

func TestCaptureProfileRefusals(t *testing.T) {
	for _, profile := range []CaptureProfile{{MaxFiles: 0, MaxBytes: 1, BytesInFlight: 1}, {MaxFiles: 1, MaxBytes: 0, BytesInFlight: 1}, {MaxFiles: 1, MaxBytes: 2, BytesInFlight: 1}} {
		if _, err := newReservationPool(&profile); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid profile %#v: %v", profile, err)
		}
	}
	pool := testReservationPool(t, nil)
	if _, err := pool.reserve("a", true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing profile: %v", err)
	}
	// A missing scratch may close without a capture or a capacity profile.
	r, err := pool.reserve("a", false)
	if err != nil {
		t.Fatal(err)
	}
	r.release()
}

func TestReservationReleasedOnEveryPath(t *testing.T) {
	for _, kind := range []string{"refusal", "git error", "cancelled context"} {
		t.Run(kind, func(t *testing.T) {
			f := newCaptureFixture(t)
			f.pool = testReservationPool(t, &CaptureProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20})
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
			next, err := f.pool.reserve(f.s.Worktree.Dir, true)
			if err != nil {
				t.Fatalf("failed capture leaked its reservation: %v", err)
			}
			next.release()
		})
	}
}

func TestObserveReturnsPoolButKeepsExclusion(t *testing.T) {
	f := newCaptureFixture(t)
	other, err := f.f.git.createWorktree(context.Background(), f.f.colony, f.p, f.s.InputsDir, filepath.Join(f.f.colony, "scratch", "87654321"), "second-observer")
	if err != nil {
		t.Fatal(err)
	}
	second := f.s
	second.Worktree = other
	synctest.Test(t, func(t *testing.T) {
		profile := CaptureProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 1 << 20}
		pool := testReservationPool(t, &profile)
		r, err := pool.reserve(f.s.Worktree.Dir, false)
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
		next, err := pool.reserve(second.Worktree.Dir, true)
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

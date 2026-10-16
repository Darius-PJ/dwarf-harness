package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"
)

func assertUnavailable(t *testing.T, err error, reason string) {
	t.Helper()
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || !errors.Is(err, ErrUnavailable) || unavailable.Reason != reason {
		t.Fatalf("want unavailable %s, got %v", reason, err)
	}
}
func TestRequestPromotionUnavailableInC2(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(s.Dir, "new.txt"), []byte("must not be captured\n"))
	before := f.store.Durable()
	effects := snapshotCaptureSideEffects(t, f.gitFixture, f.p)
	main := string(gitCommand(t, f.env, f.root, nil, "rev-parse", "refs/heads/main"))
	r := rootRequest(s)
	f.svc.pool.profile.CheckpointsPerPair = 1
	err := promotionError(context.Background(), f.svc, r)
	assertUnavailable(t, err, "no_validator")
	assertUnavailable(t, promotionError(context.Background(), f.svc, r), "no_validator")
	if f.store.Durable() != before {
		t.Fatal("unavailable promotion wrote a record")
	}
	assertCaptureSideEffects(t, f.gitFixture, f.p, effects)
	if got := string(gitCommand(t, f.env, f.root, nil, "rev-parse", "refs/heads/main")); got != main {
		t.Fatal("unavailable promotion moved main")
	}
	if string(readTestFile(t, filepath.Join(s.Dir, "new.txt"))) != "must not be captured\n" {
		t.Fatal("unavailable promotion changed scratch")
	}
	mustAdmit(t, f, r)
	mustIntegrate(t, f)
	mustFinalize(t, f, r)
	for _, outcome := range []string{RequestAccepted, RequestNoChange} {
		got, err := f.svc.RequestPromotion(context.Background(), r)
		if err != nil || got.State != outcome {
			t.Fatalf("mechanical terminal lookup: %+v %v", got, err)
		}
		next := rootRequest(s)
		next.Head = r.RequestID
		before := snapshotRequestEffects(t, f)
		main := fixtureMain(t, f.gitFixture)
		assertUnavailable(t, promotionError(context.Background(), f.svc, next), "no_validator")
		assertRequestEffects(t, f, before)
		assertCanonicalTip(t, f, main)
		if outcome == RequestAccepted {
			r = next
			mustAdmit(t, f, r)
			mustIntegrate(t, f)
			mustFinalize(t, f, r)
		}
	}
	activation := activationForWorkspace(t, f, &activationClock{ns: 100})
	activation.Schedule = ActivationSchedule{Mode: "immediate"}
	mustActivation(t, f.svc, activation)
	if err := f.svc.advanceActivation(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := rootRequest(s)
	next.Head = r.RequestID
	afterActivation := snapshotRequestEffects(t, f)
	assertUnavailable(t, promotionError(context.Background(), f.svc, next), "no_validator")
	assertRequestEffects(t, f, afterActivation)
}

func TestRequestPromotionBusyBeforeUnavailable(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	release, err := leaseTestWriter(context.Background(), f.svc, s)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assertBusy(t, promotionError(ctx, f.svc, rootRequest(s)), "scratch_busy")
	f.svc.git.unavailable = ErrGitUnavailable
	assertBusy(t, promotionError(ctx, f.svc, rootRequest(s)), "scratch_busy")
	f.svc.git.unavailable = nil
	writeTestFile(t, filepath.Join(s.Dir, "base.txt"), []byte("writer continues\n"))
	release()
	assertUnavailable(t, promotionError(context.Background(), f.svc, rootRequest(s)), "no_validator")

	other := mustScratch(t, f, uuid.NewV7())
	entered := make(chan struct{})
	resume := make(chan struct{})
	f.svc.engine.afterRawRead = func(string) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-resume
	}
	f.svc.pool.profile.BytesInFlight = f.svc.pool.profile.MaxBytes
	done := make(chan error, 1)
	go func() {
		_, err := f.capture(other, sealCapture)
		done <- err
	}()
	<-entered
	assertBusy(t, promotionError(context.Background(), f.svc, rootRequest(s)), "capture_budget")
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertUnavailable(t, promotionError(context.Background(), f.svc, rootRequest(s)), "no_validator")
}

func TestRequestPromotionBinding(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	ctx := context.Background()
	c := uuid.NewV7()
	for _, row := range []struct {
		name               string
		context, workspace uuid.UUID
		want               error
	}{
		{"invalid_context", uuid.UUID{}, f.w.ID, ErrInvalid},
		{"invalid_workspace", c, uuid.UUID{}, ErrInvalid},
		{"unknown_workspace", c, uuid.NewV7(), ErrUnknownWorkspace},
		{"no_scratch", c, f.w.ID, ErrNoScratch},
	} {
		t.Run(row.name, func(t *testing.T) {
			if err := promotionError(ctx, f.svc, rootRequest(Scratch{ContextID: row.context, WorkspaceID: row.workspace})); !errors.Is(err, row.want) {
				t.Fatalf("got %v, want %v", err, row.want)
			}
		})
	}
	s := mustScratch(t, f, c)
	if _, err := f.svc.CloseScratch(ctx, ownerClosure(s)); err != nil {
		t.Fatal(err)
	}
	if err := promotionError(ctx, f.svc, rootRequest(s)); !errors.Is(err, ErrNoScratch) {
		t.Fatalf("closed pair: %v", err)
	}
	s = mustScratch(t, f, c)
	f.reopen(t, Config{})
	release, err := leaseTestWriter(ctx, f.svc, s)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	assertUnavailable(t, promotionError(ctx, f.svc, rootRequest(s)), "no_capacity_profile")
}

func TestRequestPromotionUnavailablePreconditions(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"git_unavailable", "inputs_stale", "workspace_unverified", "scratch_missing"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			f := newServiceFixture(t)
			s := mustScratch(t, f, uuid.NewV7())
			switch reason {
			case "git_unavailable":
				f.svc.git.unavailable = ErrGitUnavailable
			case "inputs_stale":
				f.svc.git.version = "2.99.0"
			case "workspace_unverified":
				gitCommand(t, f.env, f.root, nil, "update-ref", "-d", "refs/dwarf-harness/colony")
			case "scratch_missing":
				if err := os.RemoveAll(s.Dir); err != nil {
					t.Fatal(err)
				}
			}
			assertUnavailable(t, promotionError(context.Background(), f.svc, rootRequest(s)), reason)
		})
	}
}

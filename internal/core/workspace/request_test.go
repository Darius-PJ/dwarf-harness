package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func rootRequest(s Scratch) Request {
	return Request{RequestID: uuid.NewV7(), Actor: ledger.ActorAgent, ContextID: s.ContextID, WorkspaceID: s.WorkspaceID}
}

func admission(s *Service, r Request) (RequestState, error) {
	if err := s.start(); err != nil {
		return RequestState{}, err
	}
	defer s.ops.Done()
	plan, token, state, err := s.beginRequest(context.Background(), r)
	if err != nil || token == nil {
		return state, err
	}
	return s.admit(context.Background(), plan, token)
}

func mustAdmit(t *testing.T, f *serviceFixture, r Request) RequestState {
	t.Helper()
	state, err := admission(f.svc, r)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func promotionError(ctx context.Context, s *Service, r Request) error {
	_, err := s.RequestPromotion(ctx, r)
	return err
}

func TestFourCheckpointsThenBusy(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	var lineage uuid.UUID
	captures := map[uuid.UUID]bool{}
	for n := int64(1); n <= 4; n++ {
		writeTestFile(t, filepath.Join(s.Dir, fmt.Sprintf("checkpoint-%d.txt", n)), []byte(fmt.Sprint(n)))
		state := mustAdmit(t, f, r)
		if n == 1 {
			lineage = state.LineageID
		}
		if state.Checkpoint != n || state.LineageID != lineage || state.State != RequestPending {
			t.Fatalf("checkpoint %d: %+v", n, state)
		}
		commits := serviceCommits(t, f.store)
		p, err := ledger.DecodePromotionAdmitted(commits[len(commits)-1].Records[0].Payload)
		if err != nil || p.ParentRequestID != r.Head || p.RequestID != r.RequestID || captures[p.CaptureID] {
			t.Fatalf("checkpoint identity: %+v, %v", p, err)
		}
		captures[p.CaptureID] = true
		r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	}
	before := snapshotRequestEffects(t, f)
	assertBusy(t, promotionError(context.Background(), f.svc, r), "checkpoint_slots")
	assertRequestEffects(t, f, before)
}

func TestIdenticalContentStaysDistinct(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	first := mustAdmit(t, f, r)
	r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	second := mustAdmit(t, f, r)
	if first.RequestID == second.RequestID || first.SealedTree != second.SealedTree || second.Checkpoint != 2 {
		t.Fatalf("content coalesced: %+v %+v", first, second)
	}
	commits := serviceCommits(t, f.store)
	a, _ := ledger.DecodePromotionAdmitted(commits[len(commits)-2].Records[0].Payload)
	b, _ := ledger.DecodePromotionAdmitted(commits[len(commits)-1].Records[0].Payload)
	if a.CaptureID == b.CaptureID || a.PinID == b.PinID {
		t.Fatal("identical content reused an admission identity")
	}
}

func TestSameIDReplayDoesNotRecapture(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	want := mustAdmit(t, f, r)
	writeTestFile(t, filepath.Join(s.Dir, "later.txt"), []byte("not the submission"))
	for _, reopen := range []bool{false, true} {
		if reopen {
			f.reopen(t, serviceConfig())
		}
		beforeIdle := snapshotRequestEffects(t, f)
		if got, err := f.svc.RequestPromotion(context.Background(), r); err != nil || got != want {
			t.Fatalf("idle replay after reopen=%t: %+v %v", reopen, got, err)
		}
		assertRequestEffects(t, f, beforeIdle)
		release, err := f.svc.BeginWrite(context.Background(), s.ID)
		if err != nil {
			t.Fatal(err)
		}
		before := snapshotRequestEffects(t, f)
		got, err := f.svc.RequestPromotion(context.Background(), r)
		release()
		if err != nil || got != want {
			t.Fatalf("replay after reopen=%t: %+v %v", reopen, got, err)
		}
		assertRequestEffects(t, f, before)
	}
}

func TestSameIDMismatch(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	want := mustAdmit(t, f, r)
	other := newGitFixture(t, "sha1")
	p, err := f.svc.git.inspect(context.Background(), other.root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.svc.Register(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"context", "workspace", "head", "restart"} {
		changed := r
		switch field {
		case "context":
			changed.ContextID = uuid.NewV7()
		case "workspace":
			changed.WorkspaceID = w.ID
		case "head":
			changed.Head = uuid.NewV7()
		case "restart":
			changed.RestartFrom = uuid.NewV7()
		}
		before := snapshotRequestEffects(t, f)
		if err := promotionError(context.Background(), f.svc, changed); !errors.Is(err, ErrRequestMismatch) {
			t.Fatalf("%s: %v", field, err)
		}
		assertRequestEffects(t, f, before)
	}
	r.Actor = ledger.ActorOwner
	if got, err := f.svc.RequestPromotion(context.Background(), r); err != nil || got != want {
		t.Fatalf("actor became identity: %+v %v", got, err)
	}
}

func TestCapacityScalesWithPairs(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	other := newGitFixture(t, "sha1")
	p, err := f.svc.git.inspect(context.Background(), other.root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.svc.Register(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspaceID := range []uuid.UUID{f.w.ID, f.w.ID, f.w.ID, w.ID} {
		s, err := f.svc.Scratch(context.Background(), uuid.NewV7(), workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		r := rootRequest(s)
		for range 4 {
			mustAdmit(t, f, r)
			r.Head, r.RequestID = r.RequestID, uuid.NewV7()
		}
		assertBusy(t, promotionError(context.Background(), f.svc, r), "checkpoint_slots")
	}
}

func TestHeadChecks(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r1 := rootRequest(s)
	mustAdmit(t, f, r1)
	r2 := rootRequest(s)
	r2.Head = r1.RequestID
	mustAdmit(t, f, r2)
	for _, head := range []uuid.UUID{r1.RequestID, uuid.NewV7(), {}} {
		r := rootRequest(s)
		r.Head = head
		before := snapshotRequestEffects(t, f)
		var refusal *HeadError
		if err := promotionError(context.Background(), f.svc, r); !errors.As(err, &refusal) || !errors.Is(err, ErrStaleHead) || refusal.Head != r2.RequestID || refusal.RestartRequired {
			t.Fatalf("head refusal: %v", err)
		}
		assertRequestEffects(t, f, before)
	}
	r := rootRequest(s)
	r.RestartFrom = r2.RequestID
	var refusal *HeadError
	if err := promotionError(context.Background(), f.svc, r); !errors.As(err, &refusal) || refusal.RestartRequired {
		t.Fatalf("premature restart: %v", err)
	}
}

func TestRequestInFlightIsBusy(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	s := mustScratch(t, f, uuid.NewV7())
	r := rootRequest(s)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.svc.deps.beforeAppend = func() { once.Do(func() { close(entered); <-resume }) }
	done := make(chan error, 1)
	go func() { _, err := admission(f.svc, r); done <- err }()
	<-entered
	before := snapshotRequestEffects(t, f)
	assertBusy(t, promotionError(context.Background(), f.svc, r), "request_in_flight")
	assertRequestEffects(t, f, before)
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

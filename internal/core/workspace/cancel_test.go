package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestCancellationAuthorityAndCutoff(t *testing.T) {
	t.Parallel()
	history := newGitFixture(t, "sha1")
	for _, actor := range []ledger.Actor{ledger.ActorOwner, ledger.ActorAgent} {
		t.Run(string(actor), func(t *testing.T) {
			t.Parallel()
			f := serviceFromHistory(t, history)
			scratch := mustScratch(t, f, uuid.NewV7())
			r := rootRequest(scratch)
			mustAdmit(t, f, r)
			before := f.store.Durable()
			for _, denied := range []Cancellation{
				{RequestID: r.RequestID, Actor: ledger.ActorCore, ContextID: r.ContextID},
				{RequestID: r.RequestID, Actor: ledger.Actor("supervisor"), ContextID: r.ContextID},
				{RequestID: r.RequestID, Actor: ledger.ActorAgent, ContextID: uuid.NewV7()},
				{RequestID: r.RequestID, Actor: ledger.ActorAgent},
			} {
				if _, err := f.svc.CancelPromotion(context.Background(), denied); err == nil {
					t.Fatalf("unauthorized cancellation: %+v", denied)
				}
			}
			if f.store.Durable() != before {
				t.Fatal("unauthorized cancellation wrote an ending")
			}
			c := Cancellation{RequestID: r.RequestID, Actor: actor}
			if actor == ledger.ActorAgent {
				c.ContextID = r.ContextID
			}
			got, err := f.svc.CancelPromotion(context.Background(), c)
			if err != nil || got.Outcome != "cancelled" || got.State.State != RequestCancelled {
				t.Fatalf("authorized cancellation: %+v %v", got, err)
			}
			commits := serviceCommits(t, f.store)
			last := commits[len(commits)-1]
			if len(last.Records) != 2 || last.Records[0].Actor != actor || last.Records[0].ContextID != r.ContextID {
				t.Fatal("cancellation lost authorizing actor or admission context")
			}
			before = f.store.Durable()
			got, err = f.svc.CancelPromotion(context.Background(), c)
			if err != nil || got.Outcome != "cancelled" || f.store.Durable() != before {
				t.Fatalf("repeat cancellation: %+v %v", got, err)
			}
			if _, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: uuid.NewV7(), Actor: actor, ContextID: r.ContextID}); !errors.Is(err, ErrUnknownRequest) {
				t.Fatalf("unknown request: %v", err)
			}
		})
	}
	t.Run("intent_cutoff", func(t *testing.T) {
		t.Parallel()
		f := serviceFromHistory(t, history)
		scratch := mustScratch(t, f, uuid.NewV7())
		writeTestFile(t, filepath.Join(scratch.Dir, "change.txt"), []byte("sealed\n"))
		r := rootRequest(scratch)
		mustAdmit(t, f, r)
		mustIntegrate(t, f)
		g := currentGeneration(t, f, r)
		entered, resume := make(chan struct{}), make(chan struct{})
		f.svc.deps.beforeFinalizeStep = func(step string) {
			if step == "cas" {
				close(entered)
				<-resume
			}
		}
		done := make(chan error, 1)
		go func() { _, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID); done <- err }()
		<-entered
		assertCanonicalTip(t, f, g.PredecessorCommit)
		// The initial cutoff check must not wait for the finalizer, which may
		// itself be waiting for a host-controlled Git boundary.
		before := f.store.Durable()
		got, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner})
		if err != nil || got.Outcome != "too_late" || got.State.State != RequestFinalizing || f.store.Durable() != before {
			t.Errorf("intent cutoff: %+v %v", got, err)
		}
		close(resume)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		f.svc.deps.beforeFinalizeStep = nil
		before = f.store.Durable()
		got, err = f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner})
		if err != nil || got.Outcome != "too_late" || got.State.State != RequestAccepted || f.store.Durable() != before {
			t.Fatalf("accepted cutoff: %+v %v", got, err)
		}
	})
	t.Run("no_change_and_failure", func(t *testing.T) {
		t.Parallel()
		f := serviceFromHistory(t, history)
		scratch := mustScratch(t, f, uuid.NewV7())
		r := rootRequest(scratch)
		mustAdmit(t, f, r)
		mustIntegrate(t, f)
		if state := mustFinalize(t, f, r); state.State != RequestNoChange {
			t.Fatal(state)
		}
		before := f.store.Durable()
		got, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner})
		if err != nil || got.Outcome != "already_terminal" || f.store.Durable() != before {
			t.Fatalf("no-change: %+v %v", got, err)
		}
		r.Head, r.RequestID = r.RequestID, uuid.NewV7()
		mustAdmit(t, f, r)
		child := r
		child.Head, child.RequestID = r.RequestID, uuid.NewV7()
		mustAdmit(t, f, child)
		if _, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner}); err != nil {
			t.Fatal(err)
		}
		before = f.store.Durable()
		got, err = f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: child.RequestID, Actor: ledger.ActorOwner})
		if err != nil || got.Outcome != "already_terminal" || got.State.State != RequestDependencyFailed || f.store.Durable() != before {
			t.Fatalf("failed: %+v %v", got, err)
		}
	})
	t.Run("request_in_flight", func(t *testing.T) {
		t.Parallel()
		f := serviceFromHistory(t, history)
		scratch := mustScratch(t, f, uuid.NewV7())
		r := rootRequest(scratch)
		entered, resume := make(chan struct{}), make(chan struct{})
		f.svc.deps.beforeAppend = func() { close(entered); <-resume }
		done := make(chan error, 1)
		go func() { _, err := admission(f.svc, r); done <- err }()
		<-entered
		_, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner})
		close(resume)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		f.svc.deps.beforeAppend = nil
		assertBusy(t, err, "request_in_flight")
		state, err := f.svc.RequestPromotion(context.Background(), r)
		if err != nil || state.State != RequestPending {
			t.Fatalf("in-flight request cancelled: %+v %v", state, err)
		}
	})
}

func TestTurnAndWriterLifetimeDoNotCancelPromotion(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer, err := f.svc.BeginWrite(ctx, testWriteBinding(scratch))
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	r := rootRequest(scratch)
	err = func() error {
		if err := f.svc.start(); err != nil {
			return err
		}
		defer f.svc.ops.Done()
		plan, token, _, err := f.svc.beginRequest(ctx, r)
		if err != nil {
			return err
		}
		if token == nil {
			return errors.New("admission did not reserve a slot")
		}
		_, err = f.svc.admit(ctx, plan, token)
		return err
	}()
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	before := f.store.Durable()
	f.reopen(t, serviceConfig())
	state, err := f.svc.RequestPromotion(context.Background(), r)
	if err != nil || state.State != RequestPending || f.store.Durable() != before {
		t.Fatalf("originating lifetime ended promotion: %+v %v", state, err)
	}
	got, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: r.RequestID, Actor: ledger.ActorAgent, ContextID: r.ContextID})
	if err != nil || got.Outcome != "cancelled" {
		t.Fatalf("explicit cancellation: %+v %v", got, err)
	}
}

func TestCancellationFailsLocalSuffixNotForeign(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	scratch := mustScratch(t, f, uuid.NewV7())
	foreign := mustScratch(t, f, uuid.NewV7())
	var requests []Request
	r := rootRequest(scratch)
	for n := range 4 {
		writeTestFile(t, filepath.Join(scratch.Dir, fmt.Sprintf("local%d.txt", n)), []byte("local\n"))
		mustAdmit(t, f, r)
		requests = append(requests, r)
		r.Head, r.RequestID = r.RequestID, uuid.NewV7()
	}
	writeTestFile(t, filepath.Join(foreign.Dir, "foreign.txt"), []byte("foreign\n"))
	other := rootRequest(foreign)
	mustAdmit(t, f, other)
	// Put the foreign survivor inside the speculative window.
	f.svc.pool.profile.GenerationWindow = 8
	mustIntegrate(t, f)
	old := currentGeneration(t, f, other)
	if _, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: requests[0].RequestID, Actor: ledger.ActorOwner}); err != nil {
		t.Fatal(err)
	}
	commits := serviceCommits(t, f.store)
	last := commits[len(commits)-1]
	if len(last.Records) != 8 {
		t.Fatalf("non-atomic suffix: %d", len(last.Records))
	}
	for i, request := range requests {
		p, err := ledger.DecodePromotionFailed(last.Records[i].Payload)
		want, actor := ledger.FailedDependencyFailed, ledger.ActorCore
		if i == 0 {
			want, actor = ledger.FailedCancelled, ledger.ActorOwner
		}
		u, unpinErr := ledger.DecodeBodyUnpin(last.Records[i+4].Payload)
		if err != nil || unpinErr != nil || p.RequestID != request.RequestID || p.Outcome != want || last.Records[i].Actor != actor || u.PinID != admittedRecord(t, f, request.RequestID).PinID {
			t.Fatalf("suffix order/manifest %d: %+v %+v %v %v", i, p, u, err, unpinErr)
		}
	}
	state, err := f.svc.RequestPromotion(context.Background(), other)
	if err != nil || state.State != RequestPending || currentGeneration(t, f, other).GenerationID != old.GenerationID {
		t.Fatalf("foreign request changed during cancellation: %+v %v", state, err)
	}
	mustIntegrate(t, f)
	rebuilt := currentGeneration(t, f, other)
	if rebuilt.GenerationID == old.GenerationID || rebuilt.PredecessorCommit != fixtureMain(t, f.gitFixture) {
		t.Fatal("foreign generation not rebuilt explicitly")
	}
	paths, err := f.svc.scratchPaths(context.Background(), f.svc.workspaces[f.w.ID], rebuilt.PredecessorTree, rebuilt.DerivedTree)
	if err != nil || !slices.Equal(paths, []string{"foreign.txt"}) {
		t.Fatalf("cancelled content survived rebuild: %v %v", paths, err)
	}
	r.Head, r.RestartFrom = uuid.UUID{}, requests[3].RequestID
	mustAdmit(t, f, r) // acknowledged cancellation freed the local slots
}

type cancellationWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *cancellationWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestCancellationRechecksAfterTrainWait(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	r, _ := recoveryRequests(t, f)
	g := currentGeneration(t, f, r)
	entered, resume := make(chan struct{}), make(chan struct{})
	f.svc.deps.beforeFinalizeStep = func(step string) {
		if step == "preflight" {
			close(entered)
			<-resume
		}
	}
	finalized := make(chan error, 1)
	go func() { _, err := f.svc.finalize(context.Background(), r.RequestID, g.GenerationID); finalized <- err }()
	<-entered
	waiting := &cancellationWaitContext{Context: context.Background(), waiting: make(chan struct{})}
	type reply struct {
		result CancellationResult
		err    error
	}
	cancelled := make(chan reply, 1)
	go func() {
		result, err := f.svc.CancelPromotion(waiting, Cancellation{RequestID: r.RequestID, Actor: ledger.ActorOwner})
		cancelled <- reply{result, err}
	}()
	<-waiting.waiting // CancelPromotion reached the held train, before intent.
	close(resume)
	if err := <-finalized; err != nil {
		t.Fatal(err)
	}
	got := <-cancelled
	if got.err != nil || got.result.Outcome != "too_late" || got.result.State.State != RequestAccepted {
		t.Fatalf("stale pre-intent cancellation won after train wait: %+v %v", got.result, got.err)
	}
}

func TestCancellationRetainsConflictReport(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	foreign := mustScratch(t, f, uuid.NewV7())
	local := mustScratch(t, f, uuid.NewV7())
	writeTestFile(t, filepath.Join(foreign.Dir, "base.txt"), []byte("foreign\n"))
	writeTestFile(t, filepath.Join(local.Dir, "base.txt"), []byte("local\n"))
	first, cancelled := rootRequest(foreign), rootRequest(local)
	mustAdmit(t, f, first)
	mustAdmit(t, f, cancelled)
	mustIntegrate(t, f)
	g := currentGeneration(t, f, cancelled)
	if g.Outcome != ledger.GenerationConflict || g.ReportPinID == (uuid.UUID{}) {
		t.Fatal("fixture lacks speculative conflict report")
	}
	if _, err := f.svc.CancelPromotion(context.Background(), Cancellation{RequestID: cancelled.RequestID, Actor: ledger.ActorOwner}); err != nil {
		t.Fatal(err)
	}
	if _, kept := f.svc.reportPinScratch[g.ReportPinID]; !kept {
		t.Fatal("cancellation released conflict report")
	}
	for _, commit := range serviceCommits(t, f.store) {
		for _, record := range commit.Records {
			if record.Type != ledger.TypeBodyUnpin {
				continue
			}
			p, err := ledger.DecodeBodyUnpin(record.Payload)
			if err != nil || p.PinID == g.ReportPinID {
				t.Fatalf("ending unpinned conflict report: %+v %v", p, err)
			}
		}
	}
	if _, err := f.store.ReadBody(context.Background(), g.ConflictSHA256); err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.RequestPromotion(context.Background(), first)
	if err != nil || state.State != RequestPending {
		t.Fatalf("foreign predecessor cancelled: %+v %v", state, err)
	}
}

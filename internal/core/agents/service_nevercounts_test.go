package agents

import (
	"context"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// Compare consumer views, not just the service reducer's own output. The only
// discarded field is the read frontier, which necessarily moves on any commit.
func serviceNativeViews(t *testing.T, s *Service) map[string]any {
	t.Helper()
	ctx := context.Background()
	s.mu.Lock()
	agents := slices.Collect(maps.Keys(s.p.agents))
	contexts := maps.Clone(s.p.contexts)
	grants := slices.Collect(maps.Keys(s.p.grants))
	tasks := slices.Collect(maps.Keys(s.p.tasks))
	work := slices.Collect(maps.Keys(s.p.work))
	attempts := slices.Collect(maps.Keys(s.p.paidAttempts))
	money := slices.Collect(maps.Keys(s.p.book.paid))
	s.mu.Unlock()
	out := map[string]any{}
	capacity := []ledger.ScopeKey{{Kind: "colony"}}
	for _, id := range agents {
		capacity = append(capacity, ledger.ScopeKey{Kind: "agent", ID: id})
		v, err := s.EligibilityStatus(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["eligibility/"+id.String()] = v
		facts, err := s.ListTaskFacts(ctx, TaskFactsQuery{AgentID: id, Limit: s.limits.MaxLiveTasks})
		if err != nil {
			t.Fatal(err)
		}
		if facts.Next != nil {
			t.Fatal("native task snapshot was paginated")
		}
		facts.ThroughSequence = 0
		out["facts/"+id.String()] = facts
	}
	for id, agent := range contexts {
		capacity = append(capacity, ledger.ScopeKey{Kind: "context", ID: id})
		v, err := s.Snapshot(ctx, ReadBinding{Binding: Binding{AgentID: agent, ContextID: id}, Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["private/"+id.String()] = v
	}
	for _, id := range grants {
		capacity = append(capacity, ledger.ScopeKey{Kind: "grant", ID: id})
		v, err := s.GrantStatus(ctx, GrantLookup{GrantID: id})
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["grant/"+id.String()] = v
	}
	for _, scope := range capacity {
		v, err := s.CapacityStatus(ctx, CapacityLookup{Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["capacity/"+scope.Kind+scope.ID.String()] = v
	}
	for _, scope := range money {
		v, err := s.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["money/"+scope.Kind+scope.ID.String()] = v
	}
	for _, id := range attempts {
		v, err := s.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: id})
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["attempt/"+id.String()] = v
	}
	for _, id := range tasks {
		v, err := s.TaskStatus(ctx, TaskLookup{TaskID: id})
		if err != nil {
			t.Fatal(err)
		}
		v.ThroughSequence = 0
		out["task/"+id.String()] = v
	}
	for _, id := range work {
		v, err := s.TurnStatus(ctx, WorkLookup{WorkID: id})
		if err != nil {
			t.Fatal(err)
		}
		out["work/"+id.String()] = v
	}
	return out
}

func serviceNativeBaseline(t *testing.T, s *Service) *Service {
	t.Helper()
	baseline, _, err := Open(context.Background(), s.store, s.colony, s.limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := baseline.Close(); err != nil {
			t.Error(err)
		}
	})
	// Replay cannot recreate transient holds. Copy them independently before
	// the service transition, rather than excluding them from the invariant.
	s.mu.Lock()
	baseline.p.book.preparing = maps.Clone(s.p.book.preparing)
	baseline.p.book.originPreparing = maps.Clone(s.p.book.originPreparing)
	for origin, scopes := range baseline.p.book.originPreparing {
		baseline.p.book.originPreparing[origin] = maps.Clone(scopes)
	}
	s.mu.Unlock()
	return baseline
}

func serviceAssertOnlyProjection(t *testing.T, s, baseline *Service, before map[string]any) {
	t.Helper()
	if after := serviceNativeViews(t, s); !reflect.DeepEqual(before, after) {
		for key, value := range before {
			if !reflect.DeepEqual(value, after[key]) {
				t.Errorf("service changed native consumer view %s", key)
			}
		}
		t.Fatal("service changed native consumer views")
	}
	s.mu.Lock()
	a, b := s.p, baseline.p
	s.mu.Unlock()
	if !a.identities.valid(a.through) || !b.identities.valid(b.through) {
		t.Fatal("service damaged native identity coverage")
	}
	// The directory's accepted-prefix stamp moves, but native identity maxima
	// and the validity of its checksum must remain unchanged.
	a.identities.through, b.identities.through = 0, 0
	a.identities.checksum, b.identities.checksum = a.identities.sum(), b.identities.sum()
	a.through, a.maxCommand, a.reviewFloor, a.service = 0, uuid.UUID{}, 0, newServiceProjection()
	b.through, b.maxCommand, b.reviewFloor, b.service = 0, uuid.UUID{}, 0, newServiceProjection()
	if !sameProjectionState(a, b) {
		t.Fatal("service changed native authority, ownership, work or accounting")
	}
	if err := s.scan(context.Background(), baseline.p.through, s.store.Durable(), func(c ledger.Commit) error {
		if c.Records[len(c.Records)-1].Type != ledger.TypeRuntimeServiceReceipt {
			t.Fatal("service transition appended a native effect")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceNeverCounts(t *testing.T) {
	t.Parallel()
	testServiceLiveNeverCounts(t)
	t.Run("live_owner_slots_task_and_paid_hold", func(t *testing.T) {
		t.Parallel()
		entered, release := make(chan struct{}, 1), make(chan struct{})
		f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
			model, _ := turnRequest(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
		}, nil)
		t.Cleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		work := f.workSetting(t, 51000, "Service invariants")
		f.raiseDispatchBound(t)
		f.enable(t, work)
		started, err := f.view.Do(context.Background(), turnMessage(work.Binding.ContextID, "continue"))
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-entered:
		case <-t.Context().Done():
			t.Fatal("provider did not enter")
		}
		s := f.service
		pf := *f.paidFixture
		pf.origin.ContextID, pf.turn = work.Binding.ContextID, started.TurnID
		e := serviceMoneyRefusal(t, &pf, s.p.reviewFloor)
		var clock atomic.Int64
		clock.Store(s.p.reviewFloor)
		s.mu.Lock()
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		s.mu.Unlock()
		baseline := serviceNativeBaseline(t, s)
		before := serviceNativeViews(t, s)
		status := func() session.TurnView {
			result, err := f.view.Do(context.Background(), session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: work.Binding.ContextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: started.TurnID}})
			if err != nil {
				t.Fatal(err)
			}
			v := result.Data.(session.TurnView)
			if v.Eligibility != nil {
				v.Eligibility.ThroughSequence = 0
			}
			return v
		}
		slots := func() [6]int {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.loop.mu.Lock()
			defer s.loop.mu.Unlock()
			return [6]int{s.paid.limits.MaxActivePaid, len(s.paid.exchanges), len(s.paid.prepared), len(s.paid.calls), len(s.loop.paidTokens), s.preparing}
		}
		turn, occupied := status(), slots()
		if turn.SavedPhase != "model_pending" || occupied[1] != 1 {
			t.Fatalf("fixture did not hold a native owner and paid slot: phase=%s occupancy=%v", turn.SavedPhase, occupied)
		}
		serviceRecord(t, s, e)
		clock.Store(e.Source.ObservedUTCNS + ledger.ServiceGraceNS)
		if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "lost" || len(v.Active) != 1 {
			t.Fatalf("fixture never reached service loss: %+v", v)
		}
		serviceAssertOnlyProjection(t, s, baseline, before)
		if !reflect.DeepEqual(turn, status()) || occupied != slots() {
			t.Fatal("service loss suspended its owner or consumed/released a scheduler slot")
		}
		close(release)
		finished, _ := f.finishedFor(t, f.view, started.TurnID)
		if finished.Outcome != "finished" {
			t.Fatalf("service loss stopped existing work: %s", finished.Outcome)
		}
	})
	t.Run("multicause_restore_leaves_native_books_unchanged", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 184, serviceDeps{})
		s := f.service
		baseline := serviceNativeBaseline(t, s)
		before := serviceNativeViews(t, s)
		if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		v, err := s.ServiceStatus(context.Background(), serviceObservedAt(t, commits, 180).AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || len(v.Active) != 0 {
			t.Fatalf("fixture did not restore both memberships: %+v", v)
		}
		serviceAssertOnlyProjection(t, s, baseline, before)
	})
}

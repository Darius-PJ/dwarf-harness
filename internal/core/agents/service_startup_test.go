package agents

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func serviceLiveTurn(t *testing.T) *turnFixture {
	t.Helper()
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) { paidSuccess(t, w, r) }, nil)
	bound, err := f.service.BindAgent(context.Background(), bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	f.turn = uuid.NewV7()
	if _, err := bound.StartTurn(context.Background(), TurnAdmission{CommandID: uuid.NewV7(), GrantID: f.privateGrant, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	return f
}

func serviceReopenPaid(t *testing.T, f *turnFixture) Reconciliation {
	t.Helper()
	deps := f.service.deps
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	var err error
	f.service, report, err = openPaidService(context.Background(), f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func serviceProveLoopRuns(t *testing.T, f *turnFixture) {
	t.Helper()
	command, _ := f.send(t)
	result, _ := f.finished(t, command.Arguments.(session.MessageSend).TurnID)
	if result.Outcome != "finished" || result.ModelCalls != 1 {
		t.Fatalf("service recovery gated native work: outcome=%s reason=%s calls=%d", result.Outcome, result.Reason, result.ModelCalls)
	}
}

func testServiceStartup(t *testing.T) {
	t.Run("missing_completion_holds_only_its_member_while_loop_and_timer_run", func(t *testing.T) {
		t.Parallel()
		f := serviceLiveTurn(t)
		s := f.service
		var clock atomic.Int64
		now := s.p.reviewFloor + int64(time.Hour)
		clock.Store(now)
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		failure := servicePaidFailure(t, f.paidFixture, "overloaded", now)
		cause := serviceRecord(t, s, failure).Submission.CauseID
		success := serviceSuccessfulCall(t, f.paidFixture, cause, now+1)
		s.mu.Lock()
		_, err := s.recordServiceEvidenceLocked(context.Background(), success.ObservationID, success)
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(serviceCompletionPath(t, f.serviceFixture, success)); err != nil {
			t.Fatal(err)
		}
		serviceReopenPaid(t, f)
		s = f.service
		clock.Store(now + int64(4*time.Minute))
		version := uuid.NewV7()
		if _, err := s.BindCredential(context.Background(), bindingOf(f.origin), CredentialBinding{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: f.credential.Provider, CredentialVersion: version, SourceKind: f.credential.SourceKind, SourceLocator: f.credential.SourceLocator, RevokePreviousVersion: f.credential.CredentialVersion}); err != nil {
			t.Fatal(err)
		}
		f.credential = s.p.paidCredentials[version]
		independent := servicePaidFailure(t, f.paidFixture, "unreachable", clock.Load())
		if _, err := s.recordServiceEvidence(context.Background(), independent.ObservationID, independent); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("missing evidence not retained while later source applied: %v", err)
		}
		for _, attempt := range []uuid.UUID{failure.Source.AttemptID, success.Source.AttemptID, independent.Source.AttemptID} {
			if _, err := s.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: attempt, ExpectedVersion: 2}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.EndTurn(context.Background(), TurnEnding{CommandID: uuid.NewV7(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "released"}); err != nil {
			t.Fatal(err)
		}
		armed, expired := make(chan struct{}, 1), make(chan struct{}, 1)
		s.deps.afterServiceTimerArm = func() {
			select {
			case armed <- struct{}{}:
			default:
			}
		}
		s.deps.afterServiceCommit = func(g *serviceGroup) {
			if g.member != nil && g.member.CauseID == independent.CauseID && g.member.Phase == "lost" {
				select {
				case expired <- struct{}{}:
				default:
				}
			}
		}
		f.enable(t)
		<-armed
		view, err := s.ServiceStatus(context.Background(), f.origin.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if view.State != "unsupported" || len(view.Unsupported) != 1 || len(view.Pending) != 1 || len(view.Active) != 0 || view.Recovery.UncoveredObservations != 1 {
			t.Fatalf("overdue captured success was overtaken or unrelated cause held: %+v", view)
		}
		clock.Store(now + int64(6*time.Minute))
		s.mu.Lock()
		s.notifyServiceTimerLocked()
		s.mu.Unlock()
		<-expired
		view, err = s.ServiceStatus(context.Background(), f.origin.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Active) != 1 || view.Active[0].CauseID != independent.CauseID || len(view.Unsupported) != 1 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
			t.Fatalf("timer crossed blocked evidence or stalled independent cause: %+v", view)
		}
		serviceProveLoopRuns(t, f)
		worker := s.serviceTimer
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-worker.done:
		default:
			t.Fatal("Close returned before the service timer joined")
		}
	})
	t.Run("definite_service_append_failure_is_retained_without_gating_enable", func(t *testing.T) {
		t.Parallel()
		f := serviceLiveTurn(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		s.deps.reviewClock = func() (int64, error) { return now, nil }
		e := servicePaidFailure(t, f.paidFixture, "credential_denied", now)
		s.mu.Lock()
		_, err := s.recordServiceEvidenceLocked(context.Background(), e.ObservationID, e)
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: e.Source.AttemptID, ExpectedVersion: 2}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndTurn(context.Background(), TurnEnding{CommandID: uuid.NewV7(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "released"}); err != nil {
			t.Fatal(err)
		}
		fault := errors.New("injected definite service append failure")
		var failing atomic.Bool
		failing.Store(true)
		s.deps.beforeServiceSubmit = func() error {
			if failing.Load() {
				return fault
			}
			return nil
		}
		before := f.store.Durable()
		f.enable(t)
		view, err := s.ServiceStatus(context.Background(), f.origin.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if f.store.Durable() != before || view.Recovery.UncoveredObservations != 1 || view.Recovery.LastErrorClass == "" {
			t.Fatalf("definite failure lost its recovery condition: %+v", view.Recovery)
		}
		serviceProveLoopRuns(t, f)
		failing.Store(false)
		if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		view, err = s.ServiceStatus(context.Background(), f.origin.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if view.State != "lost" || view.Recovery.UncoveredObservations != 0 || view.Recovery.LastErrorClass != "" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
			t.Fatalf("definite failure did not retry exactly once: %+v", view)
		}
	})
	t.Run("context_integrity_still_refuses_before_service_clock_or_arming", func(t *testing.T) {
		t.Parallel()
		f := serviceLiveTurn(t)
		f.seedMemory(t, "selected private bytes")
		v, err := f.service.Snapshot(context.Background(), ReadBinding{Binding: bindingOf(f.origin), Scope: "context"})
		if err != nil {
			t.Fatal(err)
		}
		scope, err := f.service.scopePath(submissionIDs{f.origin.AgentID, f.origin.SessionID, f.origin.ContextID}, "context")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(scope.objects(), v.Heads["memory"].SHA256.String())); err != nil {
			t.Fatal(err)
		}
		var sampled atomic.Int64
		f.service.deps.reviewClock = func() (int64, error) { sampled.Add(1); return 1, nil }
		serviceReopenPaid(t, f)
		before := f.store.Durable()
		if err := f.service.EnableTurnLoop(context.Background(), f.workspace, LoopOptions{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("option precedence changed: %v", err)
		}
		if err := f.service.EnableTurnLoop(context.Background(), f.workspace, LoopOptions{Contexts: []LoopContextSettings{f.setting}}); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("context integrity gate changed: %v", err)
		}
		if sampled.Load() != 0 || f.store.Durable() != before || f.service.serviceTimer != nil {
			t.Fatal("refused enable sampled, appended, or armed")
		}
	})
}

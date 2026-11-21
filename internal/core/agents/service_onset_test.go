package agents

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestServiceOnset(t *testing.T) {
	t.Parallel()
	testServiceMoneyOnset(t)
	testServiceClockRestart(t)
	t.Run("captured_before_onset_clears_but_cannot_restore", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		first := s.p.reviewFloor + int64(time.Hour)
		failure := servicePaidFailure(t, f, "overloaded", first)
		serviceRecord(t, s, failure)
		captured := make(chan ledger.RuntimeServiceObserved, 1)
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			e := serviceSuccessfulCall(t, f, failure.CauseID, first+1)
			captured <- e
			<-release
			_, err := s.recordServiceEvidence(context.Background(), e.ObservationID, e)
			done <- err
		}()
		completion := <-captured
		serviceExpiryBarrier(t, s, first+ledger.ServiceGraceNS)
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		view, err := s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if view.State != "lost" || len(view.Active) != 1 || view.Active[0].Condition != "cleared" || view.SourceThrough < completion.Source.ObservedThrough || view.Recovery.UncoveredObservations != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
			t.Fatalf("pre-onset success restored or stalled: %+v", view)
		}
		serviceRecord(t, s, serviceSuccessfulCall(t, f, failure.CauseID, first+ledger.ServiceGraceNS+1))
		view, err = s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if view.State != "available" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 2 {
			t.Fatalf("fresh post-onset success failed to restore: %+v", view)
		}
	})
	t.Run("first_refusal_and_exact_due", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		first := s.p.reviewFloor + int64(time.Hour)
		var clock atomic.Int64
		clock.Store(first)
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		e := servicePaidFailure(t, f, "overloaded", first)
		receipt := serviceRecord(t, s, e)
		due := first + ledger.ServiceGraceNS
		duplicate := e
		duplicate.ObservationID = uuid.NewV7()
		duplicate.Source = cloneServiceSource(e.Source)
		duplicate.Source.ObservationID = duplicate.ObservationID
		duplicate.Source.ObservedUTCNS++
		before := f.store.Durable()
		again := serviceRecord(t, s, duplicate)
		if !sameScopedValue(receipt, again) || f.store.Durable() != before {
			t.Fatal("semantic duplicate appended or changed its receipt")
		}
		clock.Store(due - 1)
		if _, err := s.ReconcileServiceEvidence(context.Background()); err != nil {
			t.Fatal(err)
		}
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || len(v.Pending) != 1 || *v.Pending[0].DueUTCNS != due || v.Pending[0].FirstSource.ObservationID != e.ObservationID || len(v.Active) != 0 {
			t.Fatalf("before due: %+v", v)
		}
		clock.Store(due)
		if _, err := s.ReconcileServiceEvidence(context.Background()); err != nil {
			t.Fatal(err)
		}
		v, err = s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "lost" || len(v.Active) != 1 || v.Active[0].Generation != 1 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
			t.Fatalf("at due: %+v", v)
		}
		before = f.store.Durable()
		clock.Store(first - 1)
		if _, err := s.ReconcileServiceEvidence(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.store.Durable() != before {
			t.Fatal("backward clock created another expiry")
		}
	})
	for _, row := range []struct {
		name, reason   string
		reset, pending bool
	}{{"credential_immediate", "credential_denied", false, false}, {"quota_without_reset", "quota_exhausted", false, false}, {"quota_stated_reset", "quota_exhausted", true, true}, {"transport_grace", "unreachable", false, true}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newPaidFixture(t)
			now := f.service.p.reviewFloor + int64(time.Hour)
			f.service.deps.reviewClock = func() (int64, error) { return now, nil }
			e := servicePaidFailure(t, f, row.reason, now)
			if row.reset {
				e.Source.ResetUTCNS = new(now + int64(24*time.Hour))
			}
			serviceRecord(t, f.service, e)
			v, err := f.service.ServiceStatus(context.Background(), e.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if row.pending {
				if len(v.Pending) != 1 || *v.Pending[0].DueUTCNS != now+ledger.ServiceGraceNS || len(v.Active) != 0 {
					t.Fatalf("grace: %+v", v)
				}
			} else if len(v.Active) != 1 || v.State != "lost" {
				t.Fatalf("immediate: %+v", v)
			}
		})
	}
	for _, late := range []bool{false, true} {
		name := "clear_before_due"
		if late {
			name = "clear_after_due_before_timer"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newPaidFixture(t)
			s := f.service
			var clock atomic.Int64
			first := s.p.reviewFloor + int64(time.Hour)
			clock.Store(first)
			s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
			e := servicePaidFailure(t, f, "overloaded", first)
			serviceRecord(t, s, e)
			sample := first + ledger.ServiceGraceNS - 1
			if late {
				sample += 2
			}
			clock.Store(sample)
			success := serviceSuccessfulCall(t, f, e.CauseID, sample)
			recorded, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			s.deps.afterServiceCommit = func(g *serviceGroup) {
				if g.observed != nil && g.observed.ObservationID == success.ObservationID {
					once.Do(func() { close(recorded); <-release })
				}
			}
			done := make(chan error, 1)
			go func() {
				_, err := s.recordServiceEvidence(context.Background(), success.ObservationID, success)
				done <- err
			}()
			select {
			case <-recorded:
			case err := <-done:
				t.Fatalf("completion did not reach E barrier: %v", err)
			}
			// E is durable; no timer has run or loss been applied at this barrier.
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			s.deps.afterServiceCommit = nil
			v, err := s.ServiceStatus(context.Background(), e.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if late {
				if v.State != "lost" || len(v.Active) != 1 || v.Active[0].Condition != "cleared" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
					t.Fatalf("late clear erased loss: %+v", v)
				}
			} else if v.State != "available" || len(v.Pending) != 0 || len(v.Active) != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 {
				t.Fatalf("early clear failed to withdraw: %+v", v)
			}
		})
	}
	t.Run("money_recheck_at_expiry", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		first := s.p.reviewFloor + int64(time.Hour)
		var clock atomic.Int64
		clock.Store(first)
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		e := serviceMoneyRefusal(t, f, first)
		serviceRecord(t, s, e)
		// Prepare the real competing spend commit before the worker samples.
		drafts := f.drafts(t, ledger.RuntimeSpendConfigured{Scope: ledger.PaidScope{Kind: "colony"}, ExpectedVersion: 2, Version: 3, AuthorityVersion: s.p.authority, Terms: ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "unlimited"}})
		sampled, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		clock.Store(first + ledger.ServiceGraceNS)
		s.deps.reviewClock = func() (int64, error) { once.Do(func() { close(sampled); <-release }); return clock.Load(), nil }
		done := make(chan error, 1)
		go func() { _, err := s.ReconcileServiceEvidence(context.Background()); done <- err }()
		select {
		case <-sampled:
		case err := <-done:
			t.Fatalf("expiry did not reach sample barrier: %v", err)
		}
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			close(release)
			<-done
			t.Fatal(err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || len(v.Pending) != 0 || len(v.Active) != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 {
			t.Fatalf("available money falsely expired: %+v", v)
		}
		m := s.p.service.members[serviceMemberKey{e.AgentID, e.CauseID}]
		if m.snapshot.Phase != "withdrawn" || s.p.service.causes[e.CauseID].snapshot.Condition != "cleared" {
			t.Fatalf("money did not clear and withdraw: %+v", m.snapshot)
		}
	})
}

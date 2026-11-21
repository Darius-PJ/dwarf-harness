package agents

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func serviceExpiryBarrier(t *testing.T, s *Service, sample int64) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.deps.reviewClock = func() (int64, error) { once.Do(func() { close(entered); <-release }); return sample, nil }
	done := make(chan error, 1)
	go func() { _, err := s.ReconcileServiceEvidence(context.Background()); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("expiry did not sample: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func serviceRecordBarrier(t *testing.T, s *Service, e ledger.RuntimeServiceObserved) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	s.deps.afterServiceCommit = func(g *serviceGroup) {
		if g.observed != nil && g.observed.ObservationID == e.ObservationID {
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { _, err := s.recordServiceEvidence(context.Background(), e.ObservationID, e); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("source did not reach E barrier: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.deps.afterServiceCommit = nil
}

func testServiceMoneyOnset(t *testing.T) {
	t.Run("repeated_money_refusals_append_nothing", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		e := serviceMoneyRefusal(t, f, s.p.reviewFloor)
		first := serviceRecord(t, s, e)
		through := f.store.Durable()
		for range 3 {
			again := e
			again.ObservationID = uuid.NewV7()
			again.Source = cloneServiceSource(e.Source)
			again.Source.ObservationID = again.ObservationID
			again.Source.CommandID = uuid.NewV7()
			again.Source.ObservedThrough = int64(through)
			again.Source.ObservedUTCNS = s.p.reviewFloor
			if got := serviceRecord(t, s, again); !sameScopedValue(got, first) || f.store.Durable() != through {
				t.Fatal("a parked money demand appended on its next wake")
			}
		}
	})
	t.Run("zero_quote_at_exhausted_ceiling_proves_no_clear", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		s := f.service
		route := f.route
		route.ExpectedVersion, route.Version = route.Version, route.Version+1
		route.Profile.CapabilityVersion, route.Profile.TariffVersion = uuid.NewV7().String(), uuid.NewV7().String()
		route.Profile.Tariff.Rates = slices.Clone(route.Profile.Tariff.Rates)
		for i := range route.Profile.Tariff.Rates {
			route.Profile.Tariff.Rates[i].Numerator = "0"
		}
		route.ObservedUTCNS = s.p.reviewFloor
		f.append(t, route)
		f.route = route
		e := serviceMoneyRefusal(t, f, s.p.reviewFloor)
		serviceRecord(t, s, e)
		admission := serviceReserveSource(t, f)
		if admission.Quote.MaximumMicros != 0 {
			t.Fatal("zero-tariff native admission was not free")
		}
		through := f.store.Durable()
		id := uuid.NewV7()
		clear := ledger.RuntimeServiceObserved{ObservationID: id, AgentID: e.AgentID, CauseID: e.CauseID, Source: ledger.ServiceSource{
			Kind: "cause_clear", ObservationID: id, Reason: "budget_available", ObservedThrough: int64(through), ObservedUTCNS: s.p.reviewFloor,
			Origin: new(f.origin), TurnID: f.turn, SourceSequence: new(int64(through - 1)), Money: cloneServiceSource(e.Source).Money,
		}}
		if _, err := s.recordServiceEvidence(context.Background(), id, clear); !errors.Is(err, ErrInvalid) || f.store.Durable() != through {
			t.Fatalf("zero quote falsely cleared exhaustion: %v", err)
		}
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Pending) != 1 || v.Pending[0].Condition != "active" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 {
			t.Fatalf("zero quote altered the pending loss: %+v", v)
		}
	})
	for _, row := range []struct {
		name   string
		expire bool
	}{{"money_late_admission_withdraws_pending", false}, {"money_late_admission_cannot_restore_applied_loss", true}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newPaidFixture(t)
			s := f.service
			now := s.p.reviewFloor + int64(time.Hour)
			e := serviceMoneyRefusal(t, f, now)
			serviceRecord(t, s, e)
			due := now + ledger.ServiceGraceNS
			if row.expire {
				serviceExpiryBarrier(t, s, due)
			}
			clear := serviceAdmissionClear(t, f, e.CauseID, due+1)
			serviceRecordBarrier(t, s, clear)
			v, err := s.ServiceStatus(context.Background(), e.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			phase := s.p.service.members[serviceMemberKey{e.AgentID, e.CauseID}].snapshot.Phase
			if row.expire {
				if v.State != "lost" || phase != "lost" || len(v.Active) != 1 || v.Active[0].Condition != "cleared" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceAgentChanged) != 2 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
					t.Fatalf("money clear erased an applied expiry: %+v phase=%s", v, phase)
				}
			} else if v.State != "available" || phase != "withdrawn" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 {
				t.Fatalf("late money admission falsely expired: %+v phase=%s", v, phase)
			}
		})
	}
	t.Run("money_lifetime_is_immediate_without_sampling", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixturePeriod(t, "lifetime")
		var sampled atomic.Int64
		f.service.deps.reviewClock = func() (int64, error) { sampled.Add(1); return 1, nil }
		e := serviceMoneyRefusal(t, f, f.service.p.reviewFloor)
		serviceRecord(t, f.service, e)
		v, err := f.service.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "lost" || len(v.Active) != 1 || v.Active[0].DueUTCNS != nil || sampled.Load() != 0 {
			t.Fatalf("lifetime exhaustion acquired grace: %+v", v)
		}
	})
	t.Run("unknown_hold_survives_period_reset_and_expires", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		s := f.service
		p := serviceReserveSource(t, f)
		f.dispatch(t, p)
		if _, err := s.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: p.AttemptID, ExpectedVersion: 2}); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(0, s.p.reviewFloor).UTC().AddDate(0, 0, 1)
		reset := time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, time.UTC).UnixNano()
		e := serviceMoneyRefusal(t, f, reset-int64(time.Second))
		ceiling := p.Quote.MaximumMicros
		f.spend(t, ledger.PaidScope{Kind: "colony"}, ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "finite", CeilingMicros: &ceiling})
		e.Source.ObservedThrough = int64(f.store.Durable())
		e.Source.Money.SpendSequence = int64(f.store.Durable() - 1)
		e.Source.Money.CeilingMicros = new(strconv.FormatInt(ceiling, 10))
		serviceRecord(t, s, e)
		serviceExpiryBarrier(t, s, e.Source.ObservedUTCNS+ledger.ServiceGraceNS)
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "lost" || len(v.Active) != 1 || v.Active[0].Condition != "active" {
			t.Fatalf("period reset erased unknown exposure: %+v", v)
		}
		budget := f.budget(t, paidPeriod("utc_day", e.Source.ObservedUTCNS+ledger.ServiceGraceNS))
		if budget.HeldMicros != strconv.FormatInt(ceiling, 10) || budget.SettledMicros != "0" {
			t.Fatalf("service expiry changed unknown money: held=%s settled=%s", budget.HeldMicros, budget.SettledMicros)
		}
	})
	t.Run("admission_using_last_amount_clears_from_pre_admission_book", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		e := serviceMoneyRefusal(t, f, now)
		serviceRecord(t, s, e)
		p := f.reservation(t)
		ceiling := p.Quote.MaximumMicros
		f.spend(t, ledger.PaidScope{Kind: "colony"}, ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "finite", CeilingMicros: &ceiling})
		spend := int64(f.store.Durable() - 1)
		f.append(t, p)
		id := uuid.NewV7()
		src := ledger.ServiceSource{Kind: "cause_clear", ObservationID: id, Origin: new(f.origin), TurnID: f.turn, ObservedThrough: int64(f.store.Durable()), SourceSequence: new(int64(f.store.Durable() - 1)), ObservedUTCNS: now + ledger.ServiceGraceNS + 1, Reason: "budget_available", Money: &ledger.ServiceMoney{Scope: ledger.PaidScope{Kind: "colony"}, Period: p.AdmissionPeriod, SpendSequence: spend, Mode: "finite", CeilingMicros: new(strconv.FormatInt(ceiling, 10)), OccupiedMicros: "0", Coverage: "mandatory_scope"}}
		clear := ledger.RuntimeServiceObserved{ObservationID: id, AgentID: e.AgentID, CauseID: e.CauseID, Source: src}
		before := f.store.Durable()
		for _, invalid := range []string{"post_admission_occupancy", "spend_instead_of_admission", "future_admission"} {
			bad := clear
			bad.Source = cloneServiceSource(clear.Source)
			bad.ObservationID = uuid.NewV7()
			bad.Source.ObservationID = bad.ObservationID
			switch invalid {
			case "post_admission_occupancy":
				bad.Source.Money.OccupiedMicros = strconv.FormatInt(ceiling, 10)
			case "spend_instead_of_admission":
				bad.Source.SourceSequence = new(spend)
			case "future_admission":
				bad.Source.ObservedThrough = spend + 1
			}
			if _, err := s.recordServiceEvidence(context.Background(), bad.ObservationID, bad); err == nil || errors.Is(err, ErrNoEffect) || f.store.Durable() != before {
				t.Fatalf("invalid %s money witness accepted: %v", invalid, err)
			}
		}
		serviceRecordBarrier(t, s, clear)
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || len(v.Pending) != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 || paidOccupied(s.p.book.paid[ledger.PaidScope{Kind: "colony"}], p.AdmissionPeriod).String() != strconv.FormatInt(ceiling, 10) {
			t.Fatalf("last-amount admission failed to clear without releasing money: %+v", v)
		}
	})
}

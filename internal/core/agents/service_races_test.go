package agents

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"uuid"
)

func TestServiceRaces(t *testing.T) {
	t.Parallel()
	testServiceSharedGraceRestoration(t)
	t.Run("clear_during_cold_expiry_revalidates_condition", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		e := serviceMoneyRefusal(t, f, now)
		serviceRecord(t, s, e)
		due := now + ledger.ServiceGraceNS
		s.deps.reviewClock = func() (int64, error) { return due, nil }
		peer, _, err := openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) { return due, nil }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := peer.Close(); err != nil {
				t.Error(err)
			}
		})
		frontier := f.store.Durable()
		entered, release := make(chan struct{}), make(chan struct{})
		var armed atomic.Bool
		armed.Store(true)
		s.deps.beforeRangeRead = func(after, through uint64) {
			if after == 0 && through == frontier && armed.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
		}
		t.Cleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		done := make(chan error, 1)
		go func() { _, err := s.ReconcileServiceEvidence(context.Background(), nil); done <- err }()
		select {
		case <-entered:
		case err := <-done:
			t.Fatalf("expiry did not reach its unlocked history read: %v", err)
		}
		drafts := f.drafts(t, ledger.RuntimeSpendConfigured{Scope: ledger.PaidScope{Kind: "colony"}, ExpectedVersion: 2, Version: 3, AuthorityVersion: peer.p.authority, Terms: ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "unlimited"}})
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		stop := errors.New("retained clear boundary")
		peer.deps.afterServiceCommit = func(g *serviceGroup) {
			if g.cause != nil && g.cause.Condition == "cleared" {
				peer.deps.beforeServiceSubmit = func() error { return stop }
			}
		}
		if _, err := peer.ReconcileServiceEvidence(context.Background(), nil); !errors.Is(err, stop) {
			t.Fatalf("peer did not stop after clearing K: %v", err)
		}
		cleared := f.store.Durable()
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("stale timer rejected a committed clear: %v", err)
		}
		if f.store.Durable() != cleared || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 {
			t.Fatal("stale timer wrote after a concurrent clear")
		}
		if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || len(v.Pending) != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 0 {
			t.Fatalf("retained clear did not withdraw on the next drain: %+v", v)
		}
	})
	t.Run("final_sample_after_history_tail_change", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		e := serviceMoneyRefusal(t, f, now)
		serviceRecord(t, s, e)
		due := now + ledger.ServiceGraceNS
		var clock atomic.Int64
		clock.Store(due)
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		drafts := f.drafts(t, ledger.RuntimeSpendConfigured{Scope: ledger.PaidScope{Kind: "colony"}, ExpectedVersion: 2, Version: 3, AuthorityVersion: s.p.authority, Terms: ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "finite", CeilingMicros: new(int64)}})
		frontier := f.store.Durable()
		entered, release := make(chan struct{}), make(chan struct{})
		var armed atomic.Bool
		armed.Store(true)
		s.deps.beforeRangeRead = func(after, through uint64) {
			if after == 0 && through == frontier && armed.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
		}
		t.Cleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		done := make(chan error, 1)
		go func() { _, err := s.ReconcileServiceEvidence(context.Background(), nil); done <- err }()
		select {
		case <-entered:
		case err := <-done:
			t.Fatalf("expiry did not reach its unlocked history read: %v", err)
		}
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		clock.Store(due + 1)
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		member := s.p.service.members[serviceMemberKey{agent: e.AgentID, cause: e.CauseID}].snapshot
		s.mu.Unlock()
		if member.Phase != "lost" || member.Source.ObservedUTCNS != due+1 || member.Source.ObservedThrough <= int64(frontier) {
			t.Fatalf("expiry reused its pre-read sample or book frontier: %+v", member)
		}
	})
	t.Run("observations_reserve_one_cause_before_any_K", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 132, serviceDeps{})
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		s.deps.reviewClock = func() (int64, error) { return now, nil }
		source := serviceObservedAt(t, commits, 144)
		source.ObservationID, source.CauseID = uuid.NewV7(), uuid.NewV7()
		source.Source.ObservationID, source.Source.CommandID = source.ObservationID, uuid.NewV7()
		source.Source.ObservedThrough, source.Source.ObservedUTCNS = int64(f.store.Durable()), now
		second := serviceObservedAt(t, commits, 152)
		second.ObservationID, second.CauseID = uuid.NewV7(), uuid.NewV7()
		second.Source.ObservationID, second.Source.CommandID = second.ObservationID, uuid.NewV7()
		second.Source.ObservedThrough, second.Source.ObservedUTCNS = int64(f.store.Durable()), now+1
		peer, _, err := openService(context.Background(), f.store, f.root, DefaultLimits(), serviceDeps{reviewClock: func() (int64, error) { return now + 1, nil }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := peer.Close(); err != nil {
				t.Error(err)
			}
		})
		recorded, proceed := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-proceed:
			default:
				close(proceed)
			}
		}()
		s.deps.afterServiceCommit = func(g *serviceGroup) {
			if g.observed != nil && g.observed.ObservationID == source.ObservationID {
				close(recorded)
				<-proceed
			}
		}
		done := make(chan error, 1)
		go func() {
			_, err := s.recordServiceEvidence(context.Background(), source.ObservationID, source)
			done <- err
		}()
		<-recorded
		peer.mu.Lock()
		r, err := peer.recordServiceEvidenceLocked(context.Background(), second.ObservationID, second)
		peer.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if r.Submission.CauseID != source.CauseID {
			t.Fatal("second E allocated a cause instead of using the first reservation")
		}
		if serviceCountRecords(t, peer, ledger.TypeRuntimeServiceCauseChanged) != 0 {
			t.Fatal("reservation barrier already wrote K")
		}
		close(proceed)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		v := serviceSnapshot(t, s)
		if len(v.Outages) != 2 || v.Outages[0].OutageID != v.Outages[1].OutageID || len(v.Outages[0].Members) != 2 || v.Outages[0].BeginNoticeID != v.Outages[1].BeginNoticeID {
			t.Fatalf("split same-key incident: %+v", v.Outages)
		}
		if serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
			t.Fatal("duplicate first-loss notice")
		}
	})
	t.Run("live_same_handle_shared_command", func(t *testing.T) { t.Parallel(); testServiceLiveSameHandleRace(t, true) })
	t.Run("live_same_handle_distinct_commands", func(t *testing.T) { t.Parallel(); testServiceLiveSameHandleRace(t, false) })
	t.Run("stale_second_restoration_permit_never_rebases", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 198, serviceDeps{})
		s := f.service
		a := serviceNativePaidFixture(t, f, serviceObservedAt(t, commits, 180).Source, 9000)
		b := serviceNativePaidFixture(t, f, serviceObservedAt(t, commits, 200).Source, 10000)
		// Refresh the native route at today's ledger floor without lengthening
		// its frozen validity interval; the golden's old profile has expired.
		route := s.p.paidRoutes[a.route.RouteID]
		duration := route.Profile.ExpiresUTCNS - route.Profile.VerifiedUTCNS
		route.ExpectedVersion, route.Version = route.Version, route.Version+1
		route.Profile.CapabilityVersion = uuid.NewV7().String()
		route.ObservedUTCNS = max(s.p.reviewFloor, time.Now().UTC().UnixNano())
		route.Profile.VerifiedUTCNS = route.ObservedUTCNS
		route.Profile.ExpiresUTCNS = route.ObservedUTCNS + duration
		a.append(t, route)
		a.route, b.route = route, route
		var clock atomic.Int64
		now := s.p.reviewFloor + int64(time.Hour)
		clock.Store(now)
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		first := servicePaidFailure(t, a, "credential_denied", now)
		cause := serviceRecord(t, s, first).Submission.CauseID
		clock.Store(now + 1)
		other := servicePaidFailure(t, b, "credential_denied", now+1)
		if serviceRecord(t, s, other).Submission.CauseID != cause {
			t.Fatal("native calls did not share the credential cause")
		}
		one := serviceSuccessfulCall(t, a, cause, now+2)
		two := serviceSuccessfulCall(t, b, cause, now+3)
		s.mu.Lock()
		_, err := s.recordServiceEvidenceLocked(context.Background(), one.ObservationID, one)
		if err == nil {
			_, err = s.recordServiceEvidenceLocked(context.Background(), two.ObservationID, two)
		}
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		clock.Store(now + 3)
		if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		va, _ := s.ServiceStatus(context.Background(), a.origin.AgentID)
		vb, _ := s.ServiceStatus(context.Background(), b.origin.AgentID)
		if va.State != "available" || vb.State != "lost" || len(vb.Active) != 2 {
			t.Fatalf("stale permit restored another member: A=%+v B=%+v", va, vb)
		}
		if s.p.service.members[serviceMemberKey{b.origin.AgentID, cause}].snapshot.Phase != "lost" {
			t.Fatal("stale cause version rebased onto cleared cause")
		}
		fresh := serviceSuccessfulCall(t, b, cause, now+4)
		clock.Store(now + 4)
		serviceRecord(t, s, fresh)
		if s.p.service.members[serviceMemberKey{b.origin.AgentID, cause}].snapshot.Phase != "restored" {
			t.Fatal("fresh post-loss call failed to restore")
		}
	})
}

func testServiceSharedGraceRestoration(t *testing.T) {
	for _, row := range []string{"restoration_before_shared_due", "restoration_after_shared_due", "replay_rejects_late_shared_restoration"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			f, commits := serviceGolden(t, 198, serviceDeps{})
			s := f.service
			a := serviceNativePaidFixture(t, f, serviceObservedAt(t, commits, 180).Source, 9000)
			b := serviceNativePaidFixture(t, f, serviceObservedAt(t, commits, 200).Source, 10000)
			route := s.p.paidRoutes[a.route.RouteID]
			duration := route.Profile.ExpiresUTCNS - route.Profile.VerifiedUTCNS
			route.ExpectedVersion, route.Version = route.Version, route.Version+1
			route.Profile.CapabilityVersion = uuid.NewV7().String()
			route.ObservedUTCNS = max(s.p.reviewFloor, time.Now().UTC().UnixNano())
			route.Profile.VerifiedUTCNS = route.ObservedUTCNS
			route.Profile.ExpiresUTCNS = route.ObservedUTCNS + duration
			a.append(t, route)
			a.route, b.route = route, route
			var clock atomic.Int64
			now := s.p.reviewFloor + int64(time.Hour)
			clock.Store(now)
			s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
			cause := serviceRecord(t, s, servicePaidFailure(t, a, "overloaded", now)).Submission.CauseID
			clock.Store(now + ledger.ServiceGraceNS)
			if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			second := now + ledger.ServiceGraceNS + 1
			clock.Store(second)
			if serviceRecord(t, s, servicePaidFailure(t, b, "overloaded", second)).Submission.CauseID != cause {
				t.Fatal("native calls did not share the grace cause")
			}
			ka, kb := serviceMemberKey{a.origin.AgentID, cause}, serviceMemberKey{b.origin.AgentID, cause}
			ma, mb := s.p.service.members[ka], s.p.service.members[kb]
			if ma.snapshot.Phase != "lost" || mb.snapshot.Phase != "pending" {
				t.Fatal("shared fixture did not reach lost A and pending B")
			}
			sample := *mb.snapshot.DueUTCNS
			late := row != "restoration_before_shared_due"
			if !late {
				sample--
			}
			clock.Store(sample)
			success := serviceSuccessfulCall(t, a, cause, sample)
			// Persist E without a drain or timer: both actions are outstanding.
			s.mu.Lock()
			_, err := s.recordServiceEvidenceLocked(context.Background(), success.ObservationID, success)
			s.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			selected := cloneServiceSource(success.Source)
			selected.Completion.Permit = new(s.p.service.observations[success.ObservationID].completion.Permits[0])
			sub, err := s.serviceStateSubmission("service.complete", a.origin.AgentID, cause, selected)
			if err != nil {
				t.Fatal(err)
			}
			if row == "replay_rejects_late_shared_restoration" {
				// Forge the otherwise-valid K/M/N group that omits B's due loss.
				c := s.p.service.causes[cause].snapshot
				c.ExpectedVersion, c.Version = c.Version, c.Version+1
				c.Condition, c.Source = "cleared", selected
				m := ma.snapshot
				m.ExpectedVersion, m.Version = m.Version, m.Version+1
				m.Phase, m.Source = "restored", selected
				args := sub.Arguments.(ledger.ServiceStateArguments)
				first := f.store.Durable() + 1
				next := s.p.service.clone()
				next.through = int64(f.store.Durable())
				notice := ledger.RuntimeServiceOutageNotice{OutageID: args.OutageID, CauseID: cause, Edge: "ended", NoticeID: args.NoticeID, MemberSourceSequence: int64(first + 2)}
				drafts, err := serviceDrafts(uuid.NewV7(), sub, servicePlan{next: next, cause: &c, member: &m, notice: &notice}, first)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
					t.Fatal(err)
				}
				opened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
				if opened != nil {
					_ = opened.Close()
				}
				if err == nil {
					t.Fatal("replay accepted a restoration clearing past another member's due")
				}
				return
			}
			if late {
				before := f.store.Durable()
				if _, err := s.deriveService(context.Background(), nil, sub, before+1); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
					t.Fatalf("restoration bypassed another member's due loss: %v", err)
				}
			}
			notices := serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice)
			recovery, err := s.ReconcileServiceEvidence(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ma, mb = s.p.service.members[ka], s.p.service.members[kb]
			outage := s.p.service.outages[ma.snapshot.OutageID]
			if ma.snapshot.Phase != "restored" || recovery.UncoveredObservations != 0 || recovery.LastErrorClass != "" {
				t.Fatalf("restoration failed to converge: A=%+v recovery=%+v", ma.snapshot, recovery)
			}
			if late {
				if mb.snapshot.Phase != "lost" || mb.snapshot.OutageID != ma.snapshot.OutageID || mb.sequence >= ma.sequence || outage.endNotice != (uuid.UUID{}) || len(outage.members) != 2 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != notices {
					t.Fatalf("late restoration erased B or ended its incident: B=%+v outage=%+v", mb.snapshot, outage)
				}
			} else if mb.snapshot.Phase != "withdrawn" || mb.snapshot.OutageID != (uuid.UUID{}) || outage.endNotice == (uuid.UUID{}) || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != notices+1 {
				t.Fatalf("early restoration failed to withdraw B: B=%+v outage=%+v", mb.snapshot, outage)
			}
			clock.Store(sample + 1)
			serviceRecord(t, s, servicePaidFailure(t, b, "unreachable", sample+1))
			before := serviceSnapshot(t, s)
			serviceReopen(t, f, serviceDeps{})
			if after := serviceSnapshot(t, f.service); !sameScopedValue(before, after) {
				t.Fatalf("shared restoration changed on reopen: before=%+v after=%+v", before, after)
			}
		})
	}
}

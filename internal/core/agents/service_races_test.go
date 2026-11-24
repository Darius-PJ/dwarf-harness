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
	for _, row := range []string{"restoration_after_shared_due", "replay_rejects_late_shared_restoration"} {
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
			before := f.store.Durable()
			if _, err := s.deriveService(context.Background(), nil, sub, before+1); !errors.Is(err, ErrInvalid) || f.store.Durable() != before {
				t.Fatalf("restoration bypassed another member's due loss: %v", err)
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
			if mb.snapshot.Phase != "lost" || mb.snapshot.OutageID != ma.snapshot.OutageID || mb.sequence >= ma.sequence || outage.endNotice != (uuid.UUID{}) || len(outage.members) != 2 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != notices {
				t.Fatalf("late restoration erased B or ended its incident: B=%+v outage=%+v", mb.snapshot, outage)
			}
			clock.Store(sample + 1)
			serviceRecord(t, s, servicePaidFailure(t, b, "unreachable", sample+1))
			snapshot := serviceSnapshot(t, s)
			serviceReopen(t, f, serviceDeps{})
			if after := serviceSnapshot(t, f.service); !sameScopedValue(snapshot, after) {
				t.Fatalf("shared restoration changed on reopen: before=%+v after=%+v", snapshot, after)
			}
		})
	}
}

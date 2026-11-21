package agents

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testServiceClockRestart(t *testing.T) {
	for _, row := range []struct {
		name  string
		delta int64
	}{{"reopen_before_due", -1}, {"reopen_after_due", 1}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newPaidFixture(t)
			first := f.service.p.reviewFloor + int64(time.Hour)
			e := servicePaidFailure(t, f, "overloaded", first)
			serviceRecord(t, f.service, e)
			due := first + ledger.ServiceGraceNS
			through := f.store.Durable()
			var samples atomic.Int64
			serviceReopen(t, f.serviceFixture, serviceDeps{reviewClock: func() (int64, error) { samples.Add(1); return due + row.delta, nil }})
			s := f.service
			v, err := s.ServiceStatus(context.Background(), e.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if samples.Load() != 0 || s.serviceTimer != nil || f.store.Durable() != through || v.State != "available" || len(v.Pending) != 1 || *v.Pending[0].DueUTCNS != due || !sameScopedValue(v.Pending[0].FirstSource, e.Source) {
				t.Fatalf("Open restarted grace or expired: %+v samples=%d", v, samples.Load())
			}
			serviceExpiryBarrier(t, s, due+row.delta)
			if row.delta < 0 {
				if f.store.Durable() != through {
					t.Fatal("pre-due recovery wrote an expiry")
				}
				serviceExpiryBarrier(t, s, due)
			}
			v, err = s.ServiceStatus(context.Background(), e.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != "lost" || len(v.Active) != 1 || len(v.Pending) != 0 || !sameScopedValue(v.Active[0].FirstSource, e.Source) || *v.Active[0].DueUTCNS != due || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
				t.Fatalf("saved grace did not expire exactly once: %+v", v)
			}
		})
	}
	t.Run("held_clock", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		first := s.p.reviewFloor + int64(time.Hour)
		e := servicePaidFailure(t, f, "overloaded", first)
		serviceRecord(t, s, e)
		// A later accepted source establishes the service clock floor, but does
		// not itself run the timer or move paid dispatch's sampling watermark.
		later := first + ledger.ServiceGraceNS + 1
		serviceRecord(t, s, servicePaidFailure(t, f, "credential_denied", later))
		watermark := s.reviewSample
		serviceExpiryBarrier(t, s, first-1)
		m := s.p.service.members[serviceMemberKey{e.AgentID, e.CauseID}].snapshot
		if m.Phase != "lost" || !m.Source.ClockHeld || m.Source.ObservedUTCNS != later || s.reviewSample != watermark || !sameScopedValue(m.FirstSource, e.Source) {
			t.Fatalf("backward sample did not hold the accepted floor: phase=%s held=%v sample=%d watermark=%d/%d", m.Phase, m.Source.ClockHeld, m.Source.ObservedUTCNS, s.reviewSample, watermark)
		}
		if _, exists := s.p.service.observations[m.Source.ObservationID]; exists || m.Source.ObservationID == (uuid.UUID{}) {
			t.Fatal("timer source was reused as an observation")
		}
		if err := s.scan(context.Background(), 0, s.store.Durable(), func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				if record.Type != ledger.TypeRuntimeServiceReceipt {
					continue
				}
				r, err := ledger.DecodeRuntimeServiceReceipt(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if r.CommandID == m.Source.ObservationID {
					t.Fatal("timer command reused its sampled source identity")
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

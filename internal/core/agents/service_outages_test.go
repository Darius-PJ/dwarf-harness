package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestServiceOutageNotices(t *testing.T) {
	t.Parallel()
	t.Run("shared_incident_pages_and_final_members", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 223, serviceDeps{reviewClock: func() (int64, error) { t.Error("passive query sampled clock"); return 0, ErrPaidClock }})
		s := f.service
		before := f.store.Durable()
		q := ServiceOutageQuery{CallerID: serviceObservedAt(t, commits, 144).AgentID, Limit: 1}
		var rows []ServiceOutageRow
		page, err := s.ServiceOutages(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rows) != 1 || page.Next == nil {
			t.Fatalf("first page: %+v", page)
		}
		for _, changed := range []ServiceOutageQuery{{CallerID: uuid.NewV7(), Limit: 1, Next: page.Next}, {CallerID: q.CallerID, AgentID: q.CallerID, Limit: 1, Next: page.Next}} {
			if _, err := s.ServiceOutages(context.Background(), changed); !errors.Is(err, ErrServicePageStale) {
				t.Fatalf("cursor rebound to caller/filter: %v", err)
			}
		}
		for {
			rows = append(rows, page.Rows...)
			if page.Next == nil {
				break
			}
			q.Next = page.Next
			page, err = s.ServiceOutages(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(rows) != 4 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 4 {
			t.Fatalf("lost or repeated incident edges: rows=%d", len(rows))
		}
		process := rows[0]
		for i := 0; i < 3; i++ {
			r := rows[i]
			if r.OutageID != process.OutageID || r.BeginNoticeID != process.BeginNoticeID || r.EndNoticeID != process.EndNoticeID || r.EndNoticeID == (uuid.UUID{}) || len(r.Members) != 3 || len(r.ActiveMembers) != 0 || r.Active || !slices.IsSortedFunc(r.Members, compareServiceIDs) {
				t.Fatalf("incomplete final notice list: %+v", r)
			}
			if i > 0 && compareServiceIDs(rows[i-1].AgentID, r.AgentID) >= 0 {
				t.Fatal("unstable or repeated member tuple")
			}
		}
		provider := serviceObservedAt(t, commits, 134)
		if rows[3].Cause.AuthoritySHA256 != provider.Cause.AuthoritySHA256 {
			t.Fatal("query lost the immutable scope digest")
		}
		*rows[3].Cause.Region = "changed"
		again, err := s.ServiceOutages(context.Background(), ServiceOutageQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if *again.Rows[3].Cause.Region != *provider.Cause.Region {
			t.Fatal("caller mutated reducer scope")
		}
		provider.ObservationID = uuid.NewV7()
		provider.Source.ObservationID = provider.ObservationID
		original := serviceRecord(t, s, provider)
		if original.CommandID != serviceObservedAt(t, commits, 134).ObservationID || f.store.Durable() != before {
			t.Fatal("duplicate after incident end reopened it")
		}
		if _, err := s.ServiceOutages(context.Background(), ServiceOutageQuery{Limit: 129}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("oversized page accepted: %v", err)
		}
	})
	t.Run("new_generation_new_incident", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		s.deps.reviewClock = func() (int64, error) { return now, nil }
		e := servicePaidFailure(t, f, "credential_denied", now)
		serviceRecord(t, s, e)
		first := serviceSnapshot(t, s).Outages[0]
		now++
		serviceRecord(t, s, serviceSuccessfulCall(t, f, e.CauseID, now))
		now++
		next := servicePaidFailure(t, f, "credential_denied", now)
		r := serviceRecord(t, s, next)
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Submission.CauseID != e.CauseID || len(v.Active) != 1 || v.Active[0].Generation != 2 || v.Active[0].OutageID == first.OutageID || v.Active[0].FirstSource.ObservationID != next.ObservationID || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 3 {
			t.Fatalf("new demand reused an ended incident/generation: %+v", v)
		}
	})
	t.Run("member_rejoins_open_incident", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 192, serviceDeps{})
		e := serviceObservedAt(t, commits, 144)
		e.ObservationID = uuid.NewV7()
		e.Source.ObservationID = e.ObservationID
		e.Source.CommandID = uuid.NewV7()
		e.Source.SubmissionSHA256 = ledger.Digest(sha256.Sum256([]byte("new refused demand")))
		e.Source.ObservedThrough = int64(f.store.Durable())
		e.Source.ObservedUTCNS = f.service.p.reviewFloor + 1
		before := serviceSnapshot(t, f.service)
		serviceRecord(t, f.service, e)
		after := serviceSnapshot(t, f.service)
		if len(after.Outages) != len(before.Outages) || serviceCountRecords(t, f.service, ledger.TypeRuntimeServiceOutageNotice) != 3 {
			t.Fatal("rejoin created another incident edge")
		}
		v, err := f.service.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Active) != 1 || v.Active[0].Generation != 2 || v.Active[0].OutageID != before.Outages[0].OutageID {
			t.Fatalf("rejoin did not preserve incident: %+v", v)
		}
		page, err := f.service.ServiceOutages(context.Background(), ServiceOutageQuery{CauseID: e.CauseID, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rows[0].Members) != 3 || len(page.Rows[0].ActiveMembers) != 3 {
			t.Fatalf("rejoin shrank cumulative membership: %+v", page.Rows[0])
		}
	})
}

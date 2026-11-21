package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func serviceWithPermit(t *testing.T, s *Service, e ledger.RuntimeServiceObserved, permit ledger.ServiceCompletionPermit) ledger.RuntimeServiceObserved {
	t.Helper()
	object := ServiceCompletionObject{Schema: 1, Kind: "service_completion", ObservationID: e.ObservationID, ThroughSequence: e.Source.ObservedThrough, WorksetSHA256: e.Source.WorksetSHA256, Permits: []ledger.ServiceCompletionPermit{permit}}
	ref, err := s.publishServiceCompletion(context.Background(), *e.Source.Origin, object)
	if err != nil {
		t.Fatal(err)
	}
	e.CompletionPermitsObject = &ref
	return e
}

func testServiceCompletionPermits(t *testing.T) {
	for _, row := range []struct {
		name   string
		retire bool
	}{{"credential_replacement_retires_refused_version", true}, {"parallel_credential_binding_is_not_replacement", false}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			s := f.service
			now := s.p.reviewFloor + int64(time.Hour)
			var clock atomic.Int64
			clock.Store(now)
			s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
			failure := servicePaidFailure(t, f, "credential_denied", now)
			serviceRecord(t, s, failure)
			old := f.credential.CredentialVersion
			version := uuid.NewV7()
			bind := CredentialBinding{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: f.credential.Provider, CredentialVersion: version, SourceKind: f.credential.SourceKind, SourceLocator: f.credential.SourceLocator}
			if row.retire {
				bind.RevokePreviousVersion = old
			}
			if _, err := s.BindCredential(context.Background(), bindingOf(f.origin), bind); err != nil {
				t.Fatal(err)
			}
			proof := int64(f.store.Durable() - 1)
			f.credential.Version++
			f.credential.CredentialVersion = version
			clock.Store(now + 1)
			e := serviceSuccessfulCall(t, f, failure.CauseID, now+1)
			if s.p.paidAttempts[e.Source.AttemptID].admission.Request.Credential.Version != version {
				t.Fatal("success did not use the newly bound native credential")
			}
			c := s.p.service.causes[failure.CauseID]
			m := s.p.service.members[serviceMemberKey{failure.AgentID, failure.CauseID}]
			e = serviceWithPermit(t, s, e, ledger.ServiceCompletionPermit{CauseID: c.id, CauseVersion: c.snapshot.Version, MemberGeneration: m.snapshot.Generation, Kind: "credential_replaced", ProofSequence: &proof})
			_, err := s.recordServiceEvidence(context.Background(), e.ObservationID, e)
			if row.retire && err != nil || !row.retire && !errors.Is(err, ErrInvalid) {
				t.Fatalf("replacement permit result: %v", err)
			}
			v, err := s.ServiceStatus(context.Background(), failure.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if row.retire {
				if v.State != "available" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 2 {
					t.Fatalf("owner retirement did not restore: %+v", v)
				}
			} else if v.State != "lost" || len(v.Active) != 1 || v.Active[0].Condition != "active" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceAgentChanged) != 1 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
				t.Fatalf("parallel binding restored the refused credential: %+v", v)
			}
		})
	}
	t.Run("blocked_permit_is_covered_without_restoration", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		failure := servicePaidFailure(t, f, "credential_denied", now)
		serviceRecord(t, s, failure)
		e := serviceSuccessfulCall(t, f, failure.CauseID, now+1)
		c := s.p.service.causes[failure.CauseID]
		m := s.p.service.members[serviceMemberKey{failure.AgentID, failure.CauseID}]
		e = serviceWithPermit(t, s, e, ledger.ServiceCompletionPermit{CauseID: c.id, CauseVersion: c.snapshot.Version, MemberGeneration: m.snapshot.Generation, Kind: "blocked"})
		serviceRecord(t, s, e)
		v, err := s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "lost" || v.Recovery.UncoveredObservations != 0 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceAgentChanged) != 1 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 1 {
			t.Fatalf("blocked permit restored or stalled: %+v", v)
		}
		serviceRecord(t, s, serviceSuccessfulCall(t, f, failure.CauseID, now+2))
		v, err = s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" {
			t.Fatalf("fresh exact-scope success did not restore: %+v", v)
		}
	})
	t.Run("all_work_moved_restores_without_clearing_old_scope", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		var clock atomic.Int64
		clock.Store(now)
		s.deps.reviewClock = func() (int64, error) { return clock.Load(), nil }
		failure := servicePaidFailure(t, f, "scoped_denied", now)
		serviceRecord(t, s, failure)
		version := uuid.NewV7()
		if _, err := s.BindCredential(context.Background(), bindingOf(f.origin), CredentialBinding{CommandID: uuid.NewV7(), ScopeID: f.credential.ScopeID, ExpectedVersion: f.credential.Version, Provider: f.credential.Provider, CredentialVersion: version, SourceKind: f.credential.SourceKind, SourceLocator: f.credential.SourceLocator}); err != nil {
			t.Fatal(err)
		}
		f.credential.Version++
		f.credential.CredentialVersion = version
		clock.Store(now + 1)
		e := serviceSuccessfulCall(t, f, failure.CauseID, now+1)
		if s.p.paidAttempts[e.Source.AttemptID].admission.Request.Credential.Version != version {
			t.Fatal("moved work used the refused credential version")
		}
		e.Source.Reason = "all_work_moved"
		e.Source.WorksetSHA256 = ledger.Digest(sha256.Sum256([]byte("[]")))
		e.Source.FailedWorkCount = new(int64(0))
		c := s.p.service.causes[failure.CauseID]
		m := s.p.service.members[serviceMemberKey{failure.AgentID, failure.CauseID}]
		e = serviceWithPermit(t, s, e, ledger.ServiceCompletionPermit{CauseID: c.id, CauseVersion: c.snapshot.Version, MemberGeneration: m.snapshot.Generation, Kind: "all_work_moved", FailedWorkCount: new(int64(0))})
		serviceRecord(t, s, e)
		v, err := s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || s.p.service.causes[c.id].snapshot.Condition != "active" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceCauseChanged) != 1 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 2 {
			t.Fatalf("moving work incorrectly cleared the failed scope: %+v", v)
		}
	})
	t.Run("money_clear_requires_a_later_success_to_restore", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		s := f.service
		now := s.p.reviewFloor + int64(time.Hour)
		failure := serviceMoneyRefusal(t, f, now)
		serviceRecord(t, s, failure)
		serviceExpiryBarrier(t, s, now+ledger.ServiceGraceNS)
		serviceRecord(t, s, serviceAdmissionClear(t, f, failure.CauseID, now+ledger.ServiceGraceNS+1))
		v, err := s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "lost" || v.Active[0].Condition != "cleared" {
			t.Fatal("book availability itself restored money loss")
		}
		c := s.p.service.causes[failure.CauseID]
		m := s.p.service.members[serviceMemberKey{failure.AgentID, failure.CauseID}]
		e := serviceSuccessfulCall(t, f, failure.CauseID, now+ledger.ServiceGraceNS+2)
		e = serviceWithPermit(t, s, e, ledger.ServiceCompletionPermit{CauseID: c.id, CauseVersion: c.snapshot.Version, MemberGeneration: m.snapshot.Generation, Kind: "cleared", ProofSequence: new(int64(c.sequence))})
		serviceRecord(t, s, e)
		v, err = s.ServiceStatus(context.Background(), failure.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "available" || serviceCountRecords(t, s, ledger.TypeRuntimeServiceCauseChanged) != 2 || serviceCountRecords(t, s, ledger.TypeRuntimeServiceOutageNotice) != 2 {
			t.Fatalf("cleared money permit did not restore exactly its member: %+v", v)
		}
	})
}

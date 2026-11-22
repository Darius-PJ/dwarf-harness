package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func serviceCompletionPath(t testing.TB, f *serviceFixture, e ledger.RuntimeServiceObserved) string {
	t.Helper()
	origin := e.Source.Origin
	scope, err := f.service.scopePath(submissionIDs{origin.AgentID, origin.SessionID, origin.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(scope.objects(), e.CompletionPermitsObject.SHA256.String())
}
func testServiceMetadataRecovery(t *testing.T) {
	t.Run("missing_selected_metadata_is_unsupported_until_repaired", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 223, serviceDeps{})
		e := serviceObservedAt(t, commits, 180)
		name := serviceCompletionPath(t, f, e)
		raw := readTestFile(t, name)
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		var sampled atomic.Int64
		report := serviceReopen(t, f, serviceDeps{reviewClock: func() (int64, error) { sampled.Add(1); return 1, nil }})
		found := false
		for _, issue := range report.Issues {
			found = found || issue.ExpectedSHA256 == e.CompletionPermitsObject.SHA256
		}
		if !found || sampled.Load() != 0 || f.store.Durable() != before {
			t.Fatal("missing selected object was not an inert integrity issue")
		}
		v, err := f.service.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "unsupported" || len(v.Unsupported) != 2 || len(v.Active) != 0 {
			t.Fatalf("unverifiable restoration was reported healthy: %+v", v)
		}
		attempt := serviceObservedAt(t, commits, 134)
		attempt.ObservationID = uuid.NewV7()
		attempt.Source.ObservationID = attempt.ObservationID
		if _, err = f.service.recordServiceEvidence(context.Background(), attempt.ObservationID, attempt); !errors.Is(err, ErrUnavailable) || f.store.Durable() != before {
			t.Fatalf("unverifiable cause accepted submission: %v", err)
		}
		writeTestFile(t, name, raw)
		recovery, err := f.service.ReconcileServiceEvidence(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		v, err = f.service.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if recovery.UncoveredObservations != 0 || recovery.LastErrorClass != "" || v.State != "available" || len(v.Unsupported) != 0 || f.store.Durable() != before {
			t.Fatalf("metadata repair did not validate recorded effects without appending: recovery=%+v view=%+v", recovery, v)
		}
	})
	t.Run("exact_retry_precedes_unverifiable_recovery", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 223, serviceDeps{})
		completion := serviceObservedAt(t, commits, 180)
		if err := os.Remove(serviceCompletionPath(t, f, completion)); err != nil {
			t.Fatal(err)
		}
		serviceReopen(t, f, serviceDeps{})
		s := f.service
		independent := serviceIndependentIntegrityRefusal(t, f, commits)
		s.mu.Lock()
		_, err := s.recordServiceEvidenceLocked(context.Background(), independent.ObservationID, independent)
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		before := serviceSnapshot(t, s)
		through := f.store.Durable()
		for _, source := range []ledger.RuntimeServiceObserved{serviceObservedAt(t, commits, 134), completion} {
			want := s.p.service.observations[source.ObservationID].receipt
			got, err := s.recordServiceEvidence(context.Background(), source.ObservationID, source)
			if err != nil || !sameScopedValue(got, want) || f.store.Durable() != through {
				t.Fatalf("exact retry lost its receipt or drained recovery: err=%v frontier=%d/%d receipt=%+v", err, f.store.Durable(), through, got)
			}
			source.Source.ObservedUTCNS++
			if _, err := s.recordServiceEvidence(context.Background(), source.ObservationID, source); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != through {
				t.Fatalf("changed known command bypassed mismatch or drained recovery: %v", err)
			}
		}
		if after := serviceSnapshot(t, s); !sameScopedValue(before, after) {
			t.Fatalf("known-command retry changed retained recovery: before=%+v after=%+v", before, after)
		}
	})
	for _, row := range []struct {
		name    string
		through uint64
	}{{"missing_E_does_not_stall_independent_evidence", 181}, {"repair_after_independent_writes_reopens", 223}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, commits := serviceGolden(t, row.through, serviceDeps{})
			e := serviceObservedAt(t, commits, 180)
			name := serviceCompletionPath(t, f, e)
			raw := readTestFile(t, name)
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			serviceReopen(t, f, serviceDeps{})
			independent := serviceIndependentIntegrityRefusal(t, f, commits)
			if _, err := f.service.recordServiceEvidence(context.Background(), independent.ObservationID, independent); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("retained missing object result: %v", err)
			}
			v, err := f.service.ServiceStatus(context.Background(), independent.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			applied := false
			for _, active := range v.Active {
				applied = applied || active.CauseID == independent.CauseID
			}
			if !applied || v.SourceThrough != 165 || v.Recovery.LastErrorClass != "integrity" {
				t.Fatalf("later independent source stalled or coverage jumped: %+v", v)
			}
			writeTestFile(t, name, raw)
			if _, err = f.service.ReconcileServiceEvidence(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			snapshot := serviceSnapshot(t, f.service)
			if snapshot.SourceThrough <= 181 || snapshot.Recovery.UncoveredObservations != 0 {
				t.Fatalf("repaired source hole did not converge: %+v", snapshot.Recovery)
			}
			before := f.service.p
			serviceReopen(t, f, serviceDeps{})
			if !sameProjectionState(before, f.service.p) || !sameScopedValue(snapshot, serviceSnapshot(t, f.service)) {
				t.Fatal("repaired independent evidence changed state or coverage on reopen")
			}
		})
	}
}

func serviceIndependentIntegrityRefusal(t testing.TB, f *serviceFixture, commits []ledger.Commit) ledger.RuntimeServiceObserved {
	t.Helper()
	// Curie's already admitted turn supplies a later independent demand.
	e := serviceObservedAt(t, commits, 158)
	e.ObservationID = uuid.NewV7()
	e.Source.ObservationID = e.ObservationID
	e.Source.CommandID = uuid.NewV7()
	e.Source.ObservedThrough = int64(f.store.Durable())
	e.Source.ObservedUTCNS = f.service.p.reviewFloor + 1
	e.CauseID = uuid.NewV7()
	cause := ledger.ServiceCauseSpec{Family: "execution", Code: "private_integrity", ScopeKind: "private", AgentID: e.AgentID, PrivateScope: "context", ContextID: e.Source.Origin.ContextID, Revision: new(int64(1))}
	e.Cause = &cause
	e.CauseKey, _ = cause.Key()
	e.Source.Kind = "integrity_refusal"
	e.Source.Reason = "private_integrity"
	e.Source.ExecutionRunID = uuid.UUID{}
	return e
}

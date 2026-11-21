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
		recovery, err := f.service.ReconcileServiceEvidence(context.Background())
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
	t.Run("missing_E_does_not_stall_independent_evidence", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 181, serviceDeps{})
		e := serviceObservedAt(t, commits, 180)
		name := serviceCompletionPath(t, f, e)
		raw := readTestFile(t, name)
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		serviceReopen(t, f, serviceDeps{})
		// Curie's already admitted turn supplies a later independent demand.
		independent := serviceObservedAt(t, commits, 158)
		independent.ObservationID = uuid.NewV7()
		independent.Source.ObservationID = independent.ObservationID
		independent.Source.CommandID = uuid.NewV7()
		independent.Source.ObservedThrough = int64(f.store.Durable())
		independent.Source.ObservedUTCNS = f.service.p.reviewFloor + 1
		independent.CauseID = uuid.NewV7()
		cause := ledger.ServiceCauseSpec{Family: "execution", Code: "private_integrity", ScopeKind: "private", AgentID: independent.AgentID, PrivateScope: "context", ContextID: independent.Source.Origin.ContextID, Revision: new(int64(1))}
		independent.Cause = &cause
		independent.CauseKey, _ = cause.Key()
		independent.Source.Kind = "integrity_refusal"
		independent.Source.Reason = "private_integrity"
		independent.Source.ExecutionRunID = uuid.UUID{}
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
		if _, err = f.service.ReconcileServiceEvidence(context.Background()); err != nil {
			t.Fatal(err)
		}
		snapshot := serviceSnapshot(t, f.service)
		if snapshot.SourceThrough <= 181 || snapshot.Recovery.UncoveredObservations != 0 {
			t.Fatalf("repaired source hole did not converge: %+v", snapshot.Recovery)
		}
	})
}

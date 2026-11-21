package agents

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func testServiceReplayEdges(t *testing.T) {
	for _, token := range []string{"phase", "condition"} {
		t.Run("unknown_"+token+"_retained", func(t *testing.T) {
			t.Parallel()
			f, commits := serviceGolden(t, 138, serviceDeps{})
			d := draftsOf(serviceCommitAt(t, commits, 139))
			for i := range d {
				if token == "phase" && d[i].Type == ledger.TypeRuntimeServiceAgentChanged {
					m, err := ledger.DecodeRuntimeServiceAgentChanged(d[i].Payload)
					if err != nil {
						t.Fatal(err)
					}
					m.Phase = "future_phase"
					d[i].Payload, err = m.Encode()
					if err != nil {
						t.Fatal(err)
					}
				}
				if token == "condition" && d[i].Type == ledger.TypeRuntimeServiceCauseChanged {
					c, err := ledger.DecodeRuntimeServiceCauseChanged(d[i].Payload)
					if err != nil {
						t.Fatal(err)
					}
					c.Condition = "future_condition"
					d[i].Payload, err = c.Encode()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
				t.Fatal(err)
			}
			serviceReopen(t, f, serviceDeps{})
			e := serviceObservedAt(t, commits, 134)
			v, err := f.service.ServiceStatus(context.Background(), e.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != "unsupported" || len(v.Unsupported) != 1 || len(v.Pending) != 0 || len(v.Active) != 0 || len(f.service.serviceDeadlinesLocked()) != 0 {
				t.Fatalf("unknown state was interpreted: %+v", v)
			}
			through := f.store.Durable()
			if _, err := f.service.ReconcileServiceEvidence(context.Background()); err != nil {
				t.Fatal(err)
			}
			e.ObservationID = uuid.NewV7()
			e.Source.ObservationID = e.ObservationID
			if _, err := f.service.recordServiceEvidence(context.Background(), e.ObservationID, e); !errors.Is(err, ErrUnavailable) || f.store.Durable() != through {
				t.Fatalf("unknown state was advanced: %v", err)
			}
		})
	}
	t.Run("unknown_terminal_blocks_later_member_submission", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 178, serviceDeps{})
		e := serviceObservedAt(t, commits, 180)
		e.Source.Completion.Terminal = "future_terminal"
		d, err := serviceDrafts(e.ObservationID, serviceRecordSubmission(e), servicePlan{next: f.service.p.service, observed: &e}, 179)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
			t.Fatal(err)
		}
		serviceReopen(t, f, serviceDeps{})
		s := f.service
		v, err := s.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "unsupported" || len(v.Unsupported) != 2 || len(v.Active) != 0 {
			t.Fatalf("unknown completion was interpreted: %+v", v)
		}
		object := *s.p.service.observations[e.ObservationID].completion
		e.ObservationID = uuid.NewV7()
		e.Source.ObservationID = e.ObservationID
		e.Source.Completion.Terminal = "end_turn"
		object.ObservationID = e.ObservationID
		ref, err := s.publishServiceCompletion(context.Background(), *e.Source.Origin, object)
		if err != nil {
			t.Fatal(err)
		}
		e.CompletionPermitsObject = &ref
		through := f.store.Durable()
		if _, err := s.recordServiceEvidence(context.Background(), e.ObservationID, e); !errors.Is(err, ErrUnavailable) || f.store.Durable() != through {
			t.Fatalf("unsupported member accepted later completion: err=%v frontier=%d/%d", err, f.store.Durable(), through)
		}
	})
	t.Run("command_collision_precedes_semantic_retry", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 223, serviceDeps{})
		e := serviceObservedAt(t, commits, 134)
		for _, c := range commits {
			for _, r := range c.Records {
				if r.Seq == uint64(*e.Source.SourceSequence) {
					e.ObservationID = r.OperationID
				}
			}
		}
		e.Source.ObservationID = e.ObservationID
		if _, err := f.service.recordServiceEvidence(context.Background(), e.ObservationID, e); !errors.Is(err, ErrCommandMismatch) || f.store.Durable() != 223 {
			t.Fatalf("native command collision became a semantic retry: %v", err)
		}
	})
	t.Run("unknown_tokens_retained", func(t *testing.T) {
		t.Parallel()
		var sampled atomic.Int64
		f, commits := serviceGolden(t, 132, serviceDeps{reviewClock: func() (int64, error) { sampled.Add(1); return 1, nil }})
		e := serviceObservedAt(t, commits, 134)
		e.Source.Kind = "future_failure"
		e.Source.Reason = "future_denial"
		drafts, err := serviceDrafts(e.ObservationID, serviceRecordSubmission(e), servicePlan{next: f.service.p.service, observed: &e}, 133)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		serviceReopen(t, f, serviceDeps{reviewClock: func() (int64, error) { sampled.Add(1); return 1, nil }})
		before := f.store.Durable()
		v, err := f.service.ServiceStatus(context.Background(), e.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != "unsupported" || len(v.Unsupported) != 1 || len(v.Active) != 0 || len(v.Pending) != 0 {
			t.Fatalf("unknown source was classified: %+v", v)
		}
		if _, err = f.service.ReconcileServiceEvidence(context.Background()); err != nil {
			t.Fatal(err)
		}
		if sampled.Load() != 0 || f.service.serviceTimer != nil || len(f.service.serviceDeadlinesLocked()) != 0 || f.store.Durable() != before {
			t.Fatal("unsupported source armed, sampled or drained")
		}
		e.ObservationID = uuid.NewV7()
		e.Source.ObservationID = e.ObservationID
		if _, err = f.service.recordServiceEvidence(context.Background(), e.ObservationID, e); !errors.Is(err, ErrUnavailable) || f.store.Durable() != before {
			t.Fatalf("unsupported submission = %v; frontier %d/%d", err, f.store.Durable(), before)
		}
	})
	t.Run("malformed_service_group_fails_open", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 132, serviceDeps{})
		drafts := draftsOf(serviceCommitAt(t, commits, 133))
		drafts[1].Payload = []byte(`{"observation_id":"019a9100-0000-7000-8000-000000006028"}`)
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		f.service = nil
		service, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if service != nil {
			_ = service.Close()
		}
		if err == nil || service != nil {
			t.Fatalf("malformed F3-91 passed as unknown: %v", err)
		}
	})
}

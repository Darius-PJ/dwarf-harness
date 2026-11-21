package agents

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestServiceReplay(t *testing.T) {
	t.Parallel()
	testServiceReplayEdges(t)
	testServiceCompletionPermits(t)
	t.Run("coverage_rejects_known_incomplete_observation", func(t *testing.T) {
		t.Parallel()
		f, commits := serviceGolden(t, 184, serviceDeps{})
		drafts := draftsOf(serviceCommitAt(t, commits, 185))
		last := len(drafts) - 1
		receipt, err := ledger.DecodeRuntimeServiceReceipt(drafts[last].Payload)
		if err != nil {
			t.Fatal(err)
		}
		// This group completes only one of E180's two applicable members.
		receipt.SourceThrough = 181
		drafts[last].Payload, err = receipt.Encode()
		if err != nil {
			t.Fatal(err)
		}
		baseline := serviceNativeBaseline(t, f.service)
		if _, err := f.service.acceptCommitLocked(context.Background(), replayDraftCommit(drafts, 185), nil); err == nil {
			t.Fatal("coverage passed a verifiable incomplete observation")
		}
		if !sameProjectionState(f.service.p, baseline.p) || f.store.Durable() != 184 {
			t.Fatal("rejected coverage changed state or ledger")
		}
		if err := f.service.Close(); err != nil {
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
			t.Fatal("Open accepted coverage across a known incomplete observation")
		}
	})
	for _, row := range []struct {
		name  string
		claim int64
	}{{"zero", 0}, {"earlier_E_boundary", 181}, {"not_an_E_boundary", 180}} {
		t.Run("conservative_coverage_"+row.name, func(t *testing.T) {
			t.Parallel()
			f, commits := serviceGolden(t, 219, serviceDeps{})
			drafts := draftsOf(serviceCommitAt(t, commits, 220))
			last := len(drafts) - 1
			receipt, err := ledger.DecodeRuntimeServiceReceipt(drafts[last].Payload)
			if err != nil {
				t.Fatal(err)
			}
			receipt.SourceThrough = row.claim
			drafts[last].Payload, err = receipt.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
				t.Fatal(err)
			}
			opened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if opened != nil {
				defer opened.Close()
			}
			if row.claim == 180 {
				if err == nil {
					t.Fatal("conservative coverage accepted a non-receipt boundary")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot := serviceSnapshot(t, opened)
			for _, agent := range snapshot.Agents {
				if agent.State != "available" || agent.SourceThrough != 216 || agent.Recovery.UncoveredObservations != 0 {
					t.Fatalf("conservative receipt lost reconstructed coverage or state: %+v", agent)
				}
			}
		})
	}
	t.Run("foreign_source", func(t *testing.T) {
		t.Parallel()
		for _, row := range []string{"native_attempt", "non_core_actor"} {
			t.Run(row, func(t *testing.T) {
				t.Parallel()
				f, commits := serviceGolden(t, 132, serviceDeps{})
				d := draftsOf(serviceCommitAt(t, commits, 133))
				if row == "native_attempt" {
					e := serviceObservedAt(t, commits, 134)
					e.Source.AttemptID = uuid.NewV7()
					var err error
					d, err = serviceDrafts(e.ObservationID, serviceRecordSubmission(e), servicePlan{observed: &e}, 133)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					for i := range d {
						d[i].Actor = ledger.ActorOwner
					}
				}
				baseline := serviceNativeBaseline(t, f.service)
				if _, err := f.service.acceptCommitLocked(context.Background(), replayDraftCommit(d, 133), nil); err == nil {
					t.Fatal("foreign service source or actor accepted")
				}
				if !sameProjectionState(f.service.p, baseline.p) || f.store.Durable() != 132 {
					t.Fatal("rejected source changed native or service state")
				}
				if err := f.service.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.SubmitAt(context.Background(), 132, d[0], d[1:]); err != nil {
					t.Fatal(err)
				}
				opened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
				if opened != nil {
					_ = opened.Close()
				}
				if err == nil {
					t.Fatal("Open accepted foreign service evidence")
				}
			})
		}
	})
	t.Run("chapter91", func(t *testing.T) {
		t.Parallel()
		f, commits := scopedGoldenFixture(t, "91", -1)
		before := readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))
		var err error
		f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if len(commits) != 68 || f.service.p.through != 223 || f.store.Durable() != 223 {
			t.Fatalf("replay frontier: commits=%d projection=%d durable=%d", len(commits), f.service.p.through, f.store.Durable())
		}
		if after := readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1))); !reflect.DeepEqual(before, after) {
			t.Fatal("Open appended or rewrote frozen ledger")
		}
		coverage := map[uint64]int64{135: 0, 142: 135, 145: 135, 150: 145, 153: 145, 156: 153, 159: 153, 162: 159, 165: 159, 168: 165, 172: 165, 181: 165, 189: 165, 192: 181, 201: 181, 207: 201, 216: 201, 223: 216}
		for _, c := range commits {
			for _, r := range c.Records {
				if r.Type != ledger.TypeRuntimeServiceReceipt {
					continue
				}
				receipt, err := ledger.DecodeRuntimeServiceReceipt(r.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if receipt.SourceThrough != coverage[c.Last] {
					t.Fatalf("coverage at %d: %d", c.Last, receipt.SourceThrough)
				}
				match, err := f.service.findCommand(context.Background(), nil, receipt.CommandID, 0, 223)
				if err != nil || match.service == nil || match.service.receipt.Submission.Schema != 9 {
					t.Fatalf("schema-9 lookup at %d: %+v %v", c.Last, match, err)
				}
			}
		}
		view, err := f.service.ServiceStatusSnapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Agents) != 3 || len(view.Outages) != 4 {
			t.Fatalf("final service snapshot: %+v", view)
		}
		for _, agent := range view.Agents {
			if agent.State != "available" || len(agent.Active) != 0 || len(agent.Pending) != 0 || agent.SourceThrough != 216 {
				t.Fatalf("final agent service: %+v", agent)
			}
		}
	})
}

package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func activationGolden(t *testing.T) []ledger.Commit {
	t.Helper()
	log := filepath.Join("..", "..", "..", "contract", "ledger", "testdata", "f2", "09", "log")
	line, _, _ := bytes.Cut(readTestFile(t, filepath.Join(log, "00000000000000000001.jsonl")), []byte{'\n'})
	_, envelope, _ := bytes.Cut(line, []byte{'\t'})
	var header struct {
		Colony uuid.UUID `json:"colony_id"`
	}
	if err := json.Unmarshal(envelope, &header); err != nil {
		t.Fatal(err)
	}
	reader, err := ledger.OpenReader(log, ledger.ReadOptions{Colony: header.Colony, Through: ledger.ToEnd, Consume: workspaceConsumption(), AllTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var commits []ledger.Commit
	for {
		c, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return commits
		}
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, c)
	}
}

// Preserve the golden envelopes byte-for-byte, including every referenced hash.
func activationGoldenPrefix(t *testing.T, commits []ledger.Commit, through uint64, extra ...[]ledger.Draft) (*ledgerstore.Store, string) {
	t.Helper()
	colony := t.TempDir()
	var log bytes.Buffer
	chain := ledger.Genesis()
	for _, c := range commits {
		if c.Last > through {
			break
		}
		for _, record := range c.Records {
			fmt.Fprintf(&log, "%s\t%s\n", record.Hash, record.JSON)
			chain = ledger.Chain{Seq: record.Seq, Hash: record.Hash, EventTimeNS: record.EventTimeNS}
		}
	}
	for _, drafts := range extra {
		data, next, err := ledger.EncodeCommit(chain, ledger.CommitHeader{ColonyID: commits[0].Records[0].ColonyID, CommitID: uuid.NewV7(), EventTimeNS: chain.EventTimeNS}, drafts)
		if err != nil {
			t.Fatal(err)
		}
		log.Write(data)
		chain = next
	}
	if err := os.Mkdir(filepath.Join(colony, "bodies"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(colony, "colony.json"), fmt.Appendf(nil, "{\"format\":1,\"colony_id\":\"%s\"}\n", commits[0].Records[0].ColonyID))
	writeTestFile(t, filepath.Join(colony, "log", "00000000000000000001.jsonl"), log.Bytes())
	store, _, err := ledgerstore.Open(colony, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, colony
}

func activationDrafts(commits []ledger.Commit, seq uint64) []ledger.Draft {
	for _, c := range commits {
		if c.First == seq {
			drafts := make([]ledger.Draft, len(c.Records))
			for i, r := range c.Records {
				drafts[i] = r.Draft
				drafts[i].Payload = bytes.Clone(r.Payload)
				drafts[i].BodyRefs = slices.Clone(r.BodyRefs)
			}
			return drafts
		}
	}
	panic("missing activation golden commit")
}

func TestActivationReplayIsInertAndOverdue(t *testing.T) {
	t.Parallel()
	golden := activationGolden(t)
	t.Run("controls_before_resume", func(t *testing.T) {
		t.Parallel()
		f := newActivationFixture(t)
		r := f.request()
		state := mustActivation(t, f.svc, r)
		if state.Reason != "activation_recovery_pending" {
			t.Fatalf("new request was unfenced before resume: %+v", state)
		}
		next := f.request()
		next.Target = cloneActivationTarget(next.Target)
		next.Target.WorkspacePolicies = next.Target.WorkspacePolicies[1:]
		replaced, err := f.svc.ReplaceActivation(context.Background(), ActivationReplacement{Request: next, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision})
		if err != nil || replaced.Reason != "activation_recovery_pending" {
			t.Fatalf("replacement recovery fence: %+v %v", replaced, err)
		}
		f.svc.mu.Lock()
		_, oldErr := f.svc.guardActivationIntentLocked(r.Target.WorkspacePolicies[0].WorkspaceID)
		_, newErr := f.svc.guardActivationIntentLocked(next.Target.WorkspacePolicies[0].WorkspaceID)
		f.svc.mu.Unlock()
		if oldErr != nil {
			t.Fatalf("replacement retained old affected fence: %v", oldErr)
		}
		assertUnavailable(t, newErr, "activation_recovery_pending")
		if _, err := f.svc.ActivePolicy(context.Background(), ledger.ActorOwner); err == nil {
			t.Fatal("unresumed approval manufactured a manifest")
		} else {
			assertUnavailable(t, err, "no_active_policy")
		}
		if _, err := f.svc.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: next.RequestID, ExpectedPendingRevision: replaced.Revision}); err != nil {
			t.Fatal(err)
		}
		f.svc.mu.Lock()
		_, err = f.svc.guardActivationIntentLocked(next.Target.WorkspacePolicies[0].WorkspaceID)
		f.svc.mu.Unlock()
		if err != nil || f.svc.activationLoopStarted {
			t.Fatalf("ending retained fence or started execution: %v", err)
		}
	})
	for _, row := range []struct {
		name    string
		through uint64
		hold    string
		pending bool
	}{
		{"pending", 12, "", true}, {"untrusted", 13, ledger.HoldClockUntrusted, true}, {"none", 14, ledger.HoldNone, true}, {"due", 15, ledger.HoldDue, true}, {"applied", 27, "", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			store, colony := activationGoldenPrefix(t, golden, row.through)
			before := store.Durable()
			s, report, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{activationNow: func() (time.Time, error) {
				t.Error("Open read activation clock")
				return time.Time{}, errors.New("forbidden")
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if store.Durable() != before || s.activationLoopStarted {
				t.Fatal("replay executed activation")
			}
			pending := s.activations[s.pendingActivation]
			if (s.pendingActivation != (uuid.UUID{})) != row.pending || pending.state.Hold != row.hold {
				t.Fatalf("pending replay: %+v", pending.state)
			}
			fenced := row.pending && row.hold != ledger.HoldDue && row.hold != ledger.HoldClockUntrusted
			for _, id := range pending.request.AffectedWorkspaceIDs {
				if slices.Contains(report.Pending, "activation_recovery_pending "+id.String()) != fenced {
					t.Fatalf("recovery fence: %v", report.Pending)
				}
			}
			if !row.pending {
				if s.activePolicy.EffectiveSequence != 27 || s.activePolicy.Epoch != 3 {
					t.Fatalf("applied replay: %+v", s.activePolicy)
				}
				return
			}
			if row.hold == ledger.HoldNone && s.activationFloor != 1791000104000000000 {
				t.Fatalf("observed floor lost: %d", s.activationFloor)
			}
			// Even far overdue, Open did nothing. Resume records eligibility at live time.
			s.deps.activationNow = func() (time.Time, error) { return time.Unix(0, 1791000300000000000), nil }
			if err := s.resumeActivationControls(context.Background()); err != nil {
				t.Fatal(err)
			}
			state := activationStatus(t, s, pending.request.RequestID)
			if state.Hold != ledger.HoldDue || state.Reason == "activation_recovery_pending" {
				t.Fatalf("resume did not restore due hold: %+v", state)
			}
		})
	}
}

func TestActivationCancellationPreservesForeignFences(t *testing.T) {
	t.Parallel()
	golden := activationGolden(t)
	store, colony := activationGoldenPrefix(t, golden, 32)
	s, _, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{activationNow: func() (time.Time, error) { return time.Unix(0, 1791000500000000000), nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var workspaceID uuid.UUID
	for id := range s.intents {
		workspaceID = id
	}
	if workspaceID == (uuid.UUID{}) || !s.recoveryPending[workspaceID] {
		t.Fatal("fixture lacks foreign fences")
	}
	target, artifacts := activationTargetFor(t, workspaceID)
	r := ActivationRequest{RequestID: uuid.NewV7(), OwnerApprovalID: uuid.NewV7(), Actor: ledger.ActorOwner, Target: target, Artifacts: artifacts, ExpectedActiveRecordHash: s.activePolicy.RecordHash, Schedule: ActivationSchedule{Mode: ledger.ScheduleImmediate}}
	state := mustActivation(t, s, r)
	s.mu.Lock()
	err = s.changeActivationHoldLocked(context.Background(), ledger.HoldDue, 1791000500000000000)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	state = activationStatus(t, s, r.RequestID)
	if _, err := s.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: r.RequestID, ExpectedPendingRevision: state.Revision}); err != nil {
		t.Fatal(err)
	}
	if len(s.intents) != 1 || !s.recoveryPending[workspaceID] {
		t.Fatal("activation cancellation cleared promotion fences")
	}
	intent := s.intents[workspaceID]
	if _, err := s.finalizationCandidate(intent.RequestID, intent.GenerationID); err == nil {
		t.Fatal("foreign finalization fence bypassed")
	} else {
		assertUnavailable(t, err, "finalization_pending")
	}
	if got := activationStatus(t, s, r.RequestID); got.Hold != "" || got.State != "cancelled" {
		t.Fatalf("own hold survived cancellation: %+v", got)
	}
}

func TestActivationRecordRules(t *testing.T) {
	t.Parallel()
	golden := activationGolden(t)
	activationIntentScopeRules(t, golden)
	t.Run("golden", func(t *testing.T) {
		t.Parallel()
		store, colony := activationGoldenPrefix(t, golden, 34)
		s, _, err := Open(context.Background(), store, colony, serviceConfig())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.pendingActivation != (uuid.UUID{}) || s.activePolicy.EffectiveSequence != 27 || s.activePolicy.Epoch != 3 || s.activationFloor != 1791000408000000000 || len(s.intents) != 0 || len(s.witnesses) != 1 {
			t.Fatalf("golden projection: pending=%s active=%+v floor=%d intents=%d witnesses=%d", s.pendingActivation, s.activePolicy, s.activationFloor, len(s.intents), len(s.witnesses))
		}
	})
	type row struct {
		name         string
		seq, through uint64
		change       func([]ledger.Draft) []ledger.Draft
		valid        bool
	}
	var rows []row
	payloadAt := func(at int, key string, value any) func([]ledger.Draft) []ledger.Draft {
		return func(d []ledger.Draft) []ledger.Draft {
			var p map[string]json.RawMessage
			if err := json.Unmarshal(d[at].Payload, &p); err != nil {
				t.Fatal(err)
			}
			if value == nil {
				delete(p, key)
			} else {
				b, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				p[key] = b
			}
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			d[at].Payload = b
			return d
		}
	}
	add := func(name string, seq uint64, change func([]ledger.Draft) []ledger.Draft) {
		rows = append(rows, row{name: name, seq: seq, through: seq - 1, change: change})
	}
	for _, seq := range []uint64{7, 9, 10, 16, 19} {
		for _, field := range []string{"actor", "agent", "execution", "context", "turn", "operation"} {
			add(fmt.Sprintf("%d_%s", seq, field), seq, func(d []ledger.Draft) []ledger.Draft {
				switch field {
				case "actor":
					if d[0].Actor == ledger.ActorCore {
						d[0].Actor = ledger.ActorOwner
					} else {
						d[0].Actor = ledger.ActorCore
					}
				case "agent":
					d[0].AgentID = uuid.NewV7()
				case "execution":
					d[0].ExecutionSessionID = uuid.NewV7()
				case "context":
					d[0].ContextID = uuid.NewV7()
				case "turn":
					d[0].TurnID = uuid.NewV7()
				case "operation":
					d[0].OperationID = uuid.NewV7()
				}
				return d
			})
		}
		add(fmt.Sprintf("%d_body_refs", seq), seq, func(d []ledger.Draft) []ledger.Draft {
			if len(d[0].BodyRefs) == 0 {
				d[0].BodyRefs = []ledger.Digest{{1}}
			} else {
				d[0].BodyRefs = d[0].BodyRefs[:1]
			}
			return d
		})
	}
	add("request_missing_pin", 7, func(d []ledger.Draft) []ledger.Draft { return d[:1] })
	add("request_pin_actor", 7, func(d []ledger.Draft) []ledger.Draft { d[1].Actor = ledger.ActorCore; return d })
	add("request_pin_context", 7, func(d []ledger.Draft) []ledger.Draft { d[1].ContextID = uuid.NewV7(); return d })
	add("request_pin_union", 7, func(d []ledger.Draft) []ledger.Draft { d[1].BodyRefs = d[1].BodyRefs[:1]; return d })
	add("request_pin_identity", 7, payloadAt(1, "pin_id", uuid.NewV7()))
	add("request_unregistered_policy", 7, func(d []ledger.Draft) []ledger.Draft {
		p, _ := ledger.DecodePolicyActivationRequested(d[0].Payload)
		p.WorkspacePolicies[0].WorkspaceID = activationWorkspaceID(1)
		slices.SortFunc(p.WorkspacePolicies, func(a, b ledger.WorkspacePolicy) int { return bytes.Compare(a.WorkspaceID[:], b.WorkspaceID[:]) })
		d[0].Payload = encoded(t, p)
		return d
	})
	add("request_affected_set", 7, payloadAt(0, "affected_workspace_ids", []uuid.UUID{}))
	add("request_unregistered_affected", 7, payloadAt(0, "affected_workspace_ids", []uuid.UUID{activationWorkspaceID(1)}))
	add("request_genesis_hash", 7, payloadAt(0, "expected_active_record_hash", ledger.Digest{1}))
	add("request_missing_active_hash", 11, payloadAt(0, "expected_active_record_hash", nil))
	add("request_unknown_active_hash", 11, payloadAt(0, "expected_active_record_hash", ledger.Digest{1}))
	add("hold_request_hash", 9, payloadAt(0, "activation_request_record_hash", ledger.Digest{1}))
	add("hold_request_id", 9, payloadAt(0, "request_id", uuid.NewV7()))
	add("hold_due_before_time", 9, payloadAt(0, "observed_utc_ns", int64(1791000099000000000)))
	add("hold_none_below_floor", 14, payloadAt(0, "observed_utc_ns", int64(1791000100000000000)))
	add("hold_none_already_due", 14, payloadAt(0, "observed_utc_ns", int64(1791000200000000000)))
	add("hold_commit_shape", 9, func(d []ledger.Draft) []ledger.Draft {
		return append(d, ledger.Draft{Type: "fixture.unknown", TypeVersion: 1, Actor: ledger.ActorCore, Payload: []byte(`{}`)})
	})
	add("ended_request_hash", 19, payloadAt(0, "activation_request_record_hash", ledger.Digest{1}))
	add("ended_revision", 19, payloadAt(0, "expected_pending_revision", 18))
	add("ended_request_id", 19, payloadAt(0, "request_id", uuid.NewV7()))
	add("cancelled_commit_shape", 19, func(d []ledger.Draft) []ledger.Draft {
		return append(d, ledger.Draft{Type: "fixture.unknown", TypeVersion: 1, Actor: ledger.ActorCore, Payload: []byte(`{}`)})
	})
	add("replacement_split", 16, func(d []ledger.Draft) []ledger.Draft { return d[:1] })
	add("replacement_new_identity", 16, payloadAt(0, "replacement_request_id", uuid.NewV7()))
	add("replacement_repeated_cas", 16, payloadAt(1, "replaces_revision", 14))
	add("replacement_predecessor", 16, payloadAt(1, "replaces_request_id", uuid.NewV7()))
	add("replacement_reused_approval", 16, func(d []ledger.Draft) []ledger.Draft {
		p, _ := ledger.DecodePolicyActivationRequested(golden[2].Records[0].Payload)
		return payloadAt(1, "owner_approval_id", p.OwnerApprovalID)(d)
	})
	for key, value := range map[string]any{
		"activation_id": uuid.NewV7(), "owner_approval_id": uuid.NewV7(), "activation_request_record_hash": ledger.Digest{1}, "field_law_sha256": ledger.Digest{1}, "validation_policy_hash": ledger.Digest{1}, "workspace_policies": []ledger.WorkspacePolicy{}, "not_before_utc_ns": int64(1791000099000000000), "effective_utc_ns": int64(1791000100000000000),
	} {
		add("activated_"+key, 10, payloadAt(0, key, value))
	}
	add("activated_epoch", 23, payloadAt(0, "epoch", 3))
	add("activated_expected_hash", 23, payloadAt(0, "expected_active_record_hash", ledger.Digest{1}))
	add("activated_commit_shape", 10, func(d []ledger.Draft) []ledger.Draft {
		return append(d, ledger.Draft{Type: "fixture.unknown", TypeVersion: 1, Actor: ledger.ActorCore, Payload: []byte(`{}`)})
	})
	add("intent_missing_activation", 32, payloadAt(0, "activation_record_hash", nil))
	add("intent_wrong_activation", 32, payloadAt(0, "activation_record_hash", ledger.Digest{1}))
	rows = append(rows,
		row{name: "second_pending", seq: 11, through: 8},
		row{name: "hold_none_without_untrusted", seq: 14, through: 12},
		row{name: "hold_due_is_final", seq: 13, through: 15},
		row{name: "hold_repeated", seq: 13, through: 13},
		row{name: "activated_without_due", seq: 10, through: 8},
		row{name: "activated_twice", seq: 10, through: 10},
		row{name: "ended_twice", seq: 19, through: 19},
		row{name: "unknown_ending", seq: 19, through: 18, change: payloadAt(0, "outcome", "future_revoked"), valid: true},
		row{name: "unknown_ending_with_replacement", seq: 19, through: 18, change: func(d []ledger.Draft) []ledger.Draft {
			d = payloadAt(0, "outcome", "future_revoked")(d)
			d = payloadAt(0, "replacement_request_id", uuid.NewV7())(d)
			d[0].Actor = ledger.ActorCore
			return d
		}, valid: true},
		row{name: "permanent_pin", seq: 19, through: 18, change: func([]ledger.Draft) []ledger.Draft {
			p, _ := ledger.DecodePolicyActivationRequested(golden[2].Records[0].Payload)
			return []ledger.Draft{{Type: ledger.TypeBodyUnpin, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: encoded(t, ledger.BodyUnpin{PinID: p.PinID})}}
		}},
	)
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			drafts := activationDrafts(golden, row.seq)
			if row.change != nil {
				drafts = row.change(drafts)
			}
			store, colony := activationGoldenPrefix(t, golden, row.through, drafts)
			s, _, err := Open(context.Background(), store, colony, serviceConfig())
			if s != nil {
				defer s.Close()
			}
			if row.valid {
				if err != nil || s.pendingActivation != (uuid.UUID{}) || s.activePolicy.EffectiveSequence != 10 {
					t.Fatalf("unknown ending rejected or applied: %v", err)
				}
				p, err := ledger.DecodePolicyActivationEnded(drafts[0].Payload)
				if err != nil {
					t.Fatal(err)
				}
				state := activationStatus(t, s, p.RequestID)
				if state.State != p.Outcome || state.State == "cancelled" {
					t.Fatalf("unknown ending lost its token: %+v", state)
				}
				before := store.Durable()
				result, err := s.CancelActivation(context.Background(), ActivationCancellation{Actor: ledger.ActorOwner, ExpectedPendingID: p.RequestID, ExpectedPendingRevision: state.Revision})
				if err != nil || result.Outcome != "already_terminal" || store.Durable() != before {
					t.Fatalf("unknown ending was interpreted as cancellation: %+v %v", result, err)
				}
			} else if !errors.Is(err, ErrInconsistent) {
				t.Fatalf("rule break replayed: %v", err)
			}
		})
	}
}

func activationIntentScopeRules(t *testing.T, golden []ledger.Commit) {
	t.Helper()
	t.Run("switch_with_affected_intent", func(t *testing.T) {
		t.Parallel()
		intent := activationDrafts(golden, 32)
		p, err := ledger.DecodeFinalizationIntended(intent[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		p.ActivationRecordHash = ledger.Digest{}
		intent[0].Payload = encoded(t, p)
		store, colony := activationGoldenPrefix(t, golden, 8, activationDrafts(golden, 28), activationDrafts(golden, 29), activationDrafts(golden, 31), intent, activationDrafts(golden, 9), activationDrafts(golden, 10))
		s, _, err := Open(context.Background(), store, colony, serviceConfig())
		if s != nil {
			s.Close()
		}
		if !errors.Is(err, ErrInconsistent) {
			t.Fatalf("switch ignored affected intent in replay: %v", err)
		}
	})
	t.Run("intent_unconfigured", func(t *testing.T) {
		t.Parallel()
		store, colony := activationGoldenPrefix(t, golden, 31)
		s, _, err := openService(context.Background(), store, colony, serviceConfig(), serviceDeps{activationNow: func() (time.Time, error) { return time.Unix(0, 1791000500000000000), nil }})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		intent := activationDrafts(golden, 32)
		p, err := ledger.DecodeFinalizationIntended(intent[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		unlisted := s.requests[p.RequestID].admission.WorkspaceID
		var listed uuid.UUID
		for id := range s.workspaces {
			if id != unlisted {
				listed = id
			}
		}
		target, artifacts := activationTargetFor(t, listed)
		r := ActivationRequest{RequestID: uuid.NewV7(), OwnerApprovalID: uuid.NewV7(), Actor: ledger.ActorOwner, Target: target, Artifacts: artifacts, ExpectedActiveRecordHash: s.activePolicy.RecordHash, Schedule: ActivationSchedule{Mode: ledger.ScheduleImmediate}}
		mustActivation(t, s, r)
		if err := s.advanceActivation(context.Background()); err != nil {
			t.Fatal(err)
		}
		p.ActivationRecordHash = s.activePolicy.RecordHash
		intent[0].Payload = encoded(t, p)
		if _, err := store.Append(context.Background(), intent); err != nil {
			t.Fatal(err)
		}
		reopened, _, err := Open(context.Background(), store, colony, serviceConfig())
		if reopened != nil {
			reopened.Close()
		}
		if !errors.Is(err, ErrInconsistent) {
			t.Fatalf("unconfigured intent replayed: %v", err)
		}
	})
}

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestEligibilityReplay(t *testing.T) {
	t.Parallel()
	t.Run("unknown_receipt_outcome_grants_no_permission", func(t *testing.T) {
		t.Parallel()
		f, commits := eligibilityGolden(t, 72)
		var release ledger.Commit
		var native *scopedGroup
		for _, commit := range commits {
			if commit.First == 73 {
				release = commit
			}
			if commit.First == 67 {
				_, group, _, _, _, _, _, _, err := decodeFamily(draftsOf(commit), commit.First)
				if err != nil {
					t.Fatal(err)
				}
				native = group
			}
		}
		drafts := draftsOf(release)
		receipt := scopedDecode(t, ledger.DecodeRuntimeEligibilityReceipt, drafts[2].Payload)
		receipt.Outcome = "future_outcome"
		drafts[2].Payload = paidEncode(t, receipt)
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, DefaultLimits())
		if _, err := f.service.EndTurn(context.Background(), TurnEnding{CommandID: uuid.NewV7(), TurnID: native.admission.WorkID, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
			t.Fatal("opaque eligibility fenced an ending", err)
		}
		agent := scopedBound(t, f.service, bindingOf(native.receipt.Submission.Origin))
		through := f.store.Durable()
		_, err := agent.StartTurn(context.Background(), TurnAdmission{CommandID: uuid.NewV7(), TurnID: uuid.NewV7(), GrantID: native.admission.GrantID})
		requireEligibilityHeld(t, err)
		if f.store.Durable() != through {
			t.Fatal("opaque release outcome granted native admission")
		}
	})
	t.Run("every_historical_dispatch_fence", func(t *testing.T) {
		t.Parallel()
		wanted := map[string]bool{"turn.start": false, "task.start": false, "local.admit": false, "local.begin": false, "private.write": false, "paid.admit": false, "paid.dispatch": false, "tool.begin": false}
		for _, chapter := range []string{"11", "13", "15"} {
			_, commits := scopedGoldenFixture(t, chapter, 0)
			for index, commit := range commits {
				_, scoped, _, paid, _, loop, _, _, err := decodeFamily(draftsOf(commit), commit.First)
				if err != nil {
					t.Fatal(err)
				}
				var operation string
				var agent uuid.UUID
				switch {
				case scoped != nil:
					operation, agent = scoped.receipt.Submission.Operation, scoped.ids.agent
				case paid != nil:
					operation, agent = paid.receipt.Submission.Operation, paid.ids.agent
				case loop != nil:
					operation, agent = loop.receipt.Submission.Operation, loop.ids.agent
				}
				seen, required := wanted[operation]
				if !required || seen {
					continue
				}
				wanted[operation] = true
				t.Run(operation, func(t *testing.T) {
					t.Parallel()
					f, _ := scopedGoldenFixture(t, chapter, index)
					var err error
					f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
					if err != nil {
						t.Fatal(err)
					}
					f.service.mu.Lock()
					_, _, err = f.service.stageCommitLocked(context.Background(), nil, commit, nil)
					f.service.mu.Unlock()
					if err != nil {
						t.Fatal("unheld historical control was invalid", err)
					}
					if operation == "task.start" {
						agent = scoped.task.Assignment.Assignee.AgentID
					}
					holdEligibility(t, f.service, agent)
					through := f.store.Durable()
					drafts := draftsOf(commit)
					if loop != nil {
						last := len(drafts) - 1
						receipt := scopedDecode(t, ledger.DecodeRuntimeLoopReceipt, drafts[last].Payload)
						receipt.EffectSequence += int64(through + 1 - commit.First)
						drafts[last].Payload = paidEncode(t, receipt)
					}
					f.service.mu.Lock()
					_, _, err = f.service.stageCommitLocked(context.Background(), nil, replayDraftCommit(drafts, through+1), nil)
					f.service.mu.Unlock()
					if err == nil || f.store.Durable() != through {
						t.Fatal("held historical dispatch passed pure replay")
					}
					if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
						t.Fatal("forbidden replay vector was not codec-valid", err)
					}
					reopened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
					if reopened != nil {
						_ = reopened.Close()
					}
					if err == nil {
						t.Fatal("Open accepted historical dispatch under ordinary hold")
					}
				})
			}
		}
		for operation, seen := range wanted {
			if !seen {
				t.Fatal("missing reachable frozen dispatch vector", operation)
			}
		}
	})
	t.Run("historical_and_live_exact_tuple", func(t *testing.T) {
		t.Parallel()
		f, commits := eligibilityGolden(t, 84)
		var native ledger.Commit
		for _, c := range commits {
			if c.First == 85 {
				native = c
			}
		}
		_, g, _, _, _, _, _, _, err := decodeFamily(draftsOf(native), native.First)
		if err != nil || g == nil {
			t.Fatal(err)
		}
		f.service.mu.Lock()
		_, historical := f.service.planScoped(context.Background(), nil, g, native.First)
		f.service.mu.Unlock()
		if historical != nil {
			t.Fatal("exact historical first tuple refused", historical)
		}
		wrong := *g
		wrong.command.CommandID = uuid.NewV7()
		f.service.mu.Lock()
		_, err = f.service.planScoped(context.Background(), nil, &wrong, native.First)
		f.service.mu.Unlock()
		requireEligibilityHeld(t, err)
		agent := scopedBound(t, f.service, bindingOf(g.receipt.Submission.Origin))
		if _, err := agent.StartTurn(context.Background(), TurnAdmission{CommandID: g.command.CommandID, TurnID: g.turn, GrantID: g.admission.GrantID, TaskBinding: g.admission.TaskBinding}); err != nil {
			t.Fatal("exact live first tuple refused", err)
		}
		if f.store.Durable() != 87 {
			t.Fatal("live tuple did not admit exactly once")
		}
	})
	t.Run("bound_identity_and_native_source", func(t *testing.T) {
		t.Parallel()
		f, commits := eligibilityGolden(t, 90)
		var bind ledger.Commit
		for _, c := range commits {
			if c.First == 91 {
				bind = c
			}
		}
		_, _, _, _, _, g, _, _, err := decodeFamily(draftsOf(bind), bind.First)
		if err != nil || g == nil {
			t.Fatal(err)
		}
		view, err := f.service.EligibilityStatus(context.Background(), g.ids.agent)
		if err != nil || view.FirstAction == nil || view.FirstAction.Disposition != "admitted_unbound" || view.FirstAction.NativeSequence != 86 || view.FirstAction.FirstAdmittedTurn == nil || view.FirstAction.FirstAdmittedTurn.TurnID != g.bound.TurnID {
			t.Fatalf("native source lost: %+v %v", view, err)
		}
		f.service.mu.Lock()
		_, err = f.service.planLoop(context.Background(), nil, g, bind.First)
		f.service.mu.Unlock()
		if err != nil {
			t.Fatal("exact historical binding refused", err)
		}
		g.command.CommandID = uuid.NewV7()
		f.service.mu.Lock()
		_, err = f.service.planLoop(context.Background(), nil, g, bind.First)
		f.service.mu.Unlock()
		if err != nil {
			t.Fatal("fresh binding command refused by extra eligibility fence", err)
		}
	})
	t.Run("derived_snapshot_not_trusted_payload", func(t *testing.T) {
		t.Parallel()
		f, commits := eligibilityGolden(t, 69)
		var next ledger.Commit
		for _, c := range commits {
			if c.First == 70 {
				next = c
			}
		}
		drafts := draftsOf(next)
		change := scopedDecode(t, ledger.DecodeRuntimeEligibilityChanged, drafts[1].Payload)
		change.Reason = "unrequested_reason"
		drafts[1].Payload = paidEncode(t, change)
		before, err := f.service.EligibilityStatus(context.Background(), drafts[1].AgentID)
		if err != nil {
			t.Fatal(err)
		}
		f.service.mu.Lock()
		_, err = f.service.acceptCommitLocked(context.Background(), replayDraftCommit(drafts, next.First), nil)
		f.service.mu.Unlock()
		if err == nil {
			t.Fatal("replay trusted unrequested snapshot")
		}
		after, err := f.service.EligibilityStatus(context.Background(), drafts[1].AgentID)
		if err != nil || !reflect.DeepEqual(before, after) || f.store.Durable() != 69 {
			t.Fatal("rejected replay published partial eligibility", err)
		}
		if _, err := f.store.Submit(context.Background(), drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		reopened, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if reopened != nil {
			_ = reopened.Close()
		}
		if err == nil {
			t.Fatal("Open accepted forged but codec-valid snapshot")
		}
	})
	t.Run("unknown_state_and_mixed_family_fail_closed", func(t *testing.T) {
		t.Parallel()
		_, commits := eligibilityGolden(t, 69)
		var next ledger.Commit
		for _, c := range commits {
			if c.First == 70 {
				next = c
			}
		}
		for _, mixed := range []bool{false, true} {
			drafts := draftsOf(next)
			if mixed {
				drafts = append(drafts, ledger.Draft{Type: ledger.TypeRuntimeContinuationChanged})
			} else {
				var fields map[string]any
				if err := json.Unmarshal(drafts[1].Payload, &fields); err != nil {
					t.Fatal(err)
				}
				fields["state"] = "future_state"
				drafts[1].Payload, _ = json.Marshal(fields)
			}
			if _, _, _, _, _, _, _, _, err := decodeFamily(drafts, next.First); err == nil {
				t.Fatal("unsupported group was silently unconsumed")
			}
		}
	})
	t.Run("terminal_next_preserves_first_admitted", func(t *testing.T) {
		t.Parallel()
		f, _ := eligibilityGolden(t, 124)
		for id, agent := range f.service.p.agents {
			if agent.eligibility.current().FirstAction == nil {
				continue
			}
			view, err := f.service.EligibilityStatus(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			first := view.FirstAction
			if first.Disposition != "not_admitted" || first.FirstAdmittedSequence != 86 || first.FirstAdmittedTurn == nil || first.FirstAdmittedTurn.TurnID == first.Declaration.PermittedTurn.TurnID {
				t.Fatalf("next erased or invented admitted identity: %+v", first)
			}
		}
	})
	t.Run("cold_command_lookup_and_corrupt_directory_fallback", func(t *testing.T) {
		t.Parallel()
		f, binding, _, _ := scopedPrivateFixture(t, serviceDeps{}, scopedCapacity())
		r := EligibilityHold{CommandID: uuid.NewV7(), AgentID: binding.AgentID, Reason: "hold"}
		first, err := f.service.HoldOrdinaryWork(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f, DefaultLimits())
		f.service.history = newHistoryIndex()
		through := f.store.Durable()
		retry, err := f.service.HoldOrdinaryWork(context.Background(), r)
		if err != nil || retry != first || f.store.Durable() != through {
			t.Fatal("authoritative command fallback lost schema8 receipt", err)
		}
		_, err = f.service.ReleaseEligibility(context.Background(), EligibilityRelease{CommandID: r.CommandID, AgentID: binding.AgentID, ExpectedVersion: 1, ExpectedOperationID: first.OperationID})
		if !errors.Is(err, ErrCommandMismatch) {
			t.Fatal("fallback lost cross-operation mismatch", err)
		}
	})
}

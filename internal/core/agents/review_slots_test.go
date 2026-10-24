package agents

import (
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestReviewSlotTransitionsRefuseIllegalEdges(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"review", "failure"} {
		for _, state := range []string{"pending", "rejected", "approved", "failed", "superseded", "stale"} {
			for _, action := range []string{"approve", "reject", "fail", "supersede", "stale", "pending", "unknown"} {
				t.Run(kind+"/"+state+"/"+action, func(t *testing.T) {
					t.Parallel()
					current := reviewSlot{id: privateID(80), binding: reviewRuleBinding(), kind: kind, state: state}
					before := reviewSlot{id: current.id, binding: reviewRuleBinding(), kind: kind, state: state}
					replacement := uuid.UUID{}
					if action == "supersede" {
						replacement = privateID(81)
					}
					want := ""
					if state == "pending" || state == "rejected" {
						if action == "supersede" {
							want = "superseded"
						} else if action == "stale" {
							want = "stale"
						}
					}
					if state == "pending" {
						if kind == "review" && action == "approve" {
							want = "approved"
						} else if kind == "review" && action == "reject" {
							want = "rejected"
						} else if kind == "failure" && action == "fail" {
							want = "failed"
						}
					}
					next, err := transitionReviewSlot(current, current.id, action, replacement)
					if !reflect.DeepEqual(current, before) {
						t.Fatal("transition mutated the immutable request")
					}
					if want == "" {
						if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(next, before) {
							t.Fatalf("illegal edge changed slot: %+v, %v", next, err)
						}
						return
					}
					before.state = want
					if want == "superseded" {
						before.supersededBy = replacement
					}
					if err != nil || !reflect.DeepEqual(next, before) {
						t.Fatalf("legal edge = %+v, %v; want %+v", next, err, before)
					}
				})
			}
		}
	}
}

func TestReviewSlotReplacementFencesOldIdentity(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, action         string
		request, replacement uuid.UUID
	}{
		{"missing_replacement", "supersede", privateID(80), uuid.UUID{}},
		{"same_request_id", "supersede", privateID(80), privateID(80)},
		{"invalid_new_id", "supersede", privateID(80), uuid.UUID{1}},
		{"wrong_replaced_id", "supersede", privateID(79), privateID(81)},
		{"wrong_callback_id", "approve", privateID(79), uuid.UUID{}},
		{"answer_cannot_replace", "approve", privateID(80), privateID(81)},
		{"staleness_cannot_invent_successor", "stale", privateID(80), privateID(81)},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			current := reviewSlot{id: privateID(80), binding: reviewRuleBinding(), kind: "review", state: "pending"}
			next, err := transitionReviewSlot(current, row.request, row.action, row.replacement)
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(next, current) {
				t.Fatalf("invalid identity changed slot: %+v, %v", next, err)
			}
		})
	}
}

func TestReviewGuardBindsOwnerAndAgentButAllowsEscapes(t *testing.T) {
	t.Parallel()
	for _, actor := range []string{"owner", "agent"} {
		for _, state := range []string{"absent", "pending", "rejected", "approved", "failed", "superseded", "stale"} {
			for _, action := range []string{"complete", "fail", "cancel", "reassign"} {
				t.Run(actor+"/"+state+"/"+action, func(t *testing.T) {
					t.Parallel()
					current, facts := taskRuleFixture()
					current.state, current.submission, facts.actor = "submitted", taskArtifact(), actor
					slot := reviewSlot{}
					if state != "absent" {
						slot = reviewSlot{id: privateID(80), binding: reviewRuleBinding(), kind: "review", state: state}
					}
					err := guardReviewTask(slot, current, action)
					blocked := (state == "pending" || state == "rejected") && (action == "complete" || action == "fail")
					if blocked {
						if !errors.Is(err, ErrReviewRequired) {
							t.Fatalf("terminal bypass permitted: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("escape or unguarded task refused: %v", err)
					}
					request := taskAction{action: action, task: current.id}
					switch action {
					case "complete":
						request.submission = current.submission
					case "fail":
						request.failureCause, request.failureEvidence = "failed", taskArtifact()
					case "reassign":
						request.assignment = ledger.AssignmentInput{AssignmentID: privateID(52), Assignee: ledger.Binding{AgentID: privateID(1), ContextID: privateID(3)}}
					}
					next, err := transitionTask(current, facts, request, taskExpectation{current.version, current.assignment.id, current.assignment.generation})
					want := map[string]string{"complete": "completed", "fail": "failed", "cancel": "cancelled", "reassign": "waiting"}[action]
					if err != nil || next.state != want || next.version != current.version+1 {
						t.Fatalf("frozen transition changed: %+v, %v", next, err)
					}
				})
			}
		}
	}
}

func TestReviewGuardNeverTransfersToDifferentTaskBinding(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*taskState)
	}{
		{"task_identity", func(s *taskState) { s.id = privateID(99) }},
		{"version", func(s *taskState) { s.version++ }},
		{"assignment", func(s *taskState) { s.assignment.id = privateID(99) }},
		{"generation", func(s *taskState) { s.assignment.generation++ }},
		{"assignee_agent", func(s *taskState) { s.assignment.binding.AgentID = privateID(99) }},
		{"assignee_context", func(s *taskState) { s.assignment.binding.ContextID = privateID(99) }},
		{"cancelled", func(s *taskState) { s.state = "cancelled" }},
		{"completed", func(s *taskState) { s.state = "completed" }},
		{"failed", func(s *taskState) { s.state = "failed" }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			current, _ := taskRuleFixture()
			slot := reviewSlot{id: privateID(80), binding: reviewRuleBinding(), kind: "failure", state: "pending"}
			row.change(&current)
			if err := guardReviewTask(slot, current, "fail"); err != nil {
				t.Fatalf("old slot transferred to different task binding: %v", err)
			}
			stale, err := transitionReviewSlot(slot, slot.id, "stale", uuid.UUID{})
			if err != nil {
				t.Fatal(err)
			}
			original, _ := taskRuleFixture()
			if err := guardReviewTask(stale, original, "fail"); err != nil {
				t.Fatalf("stale slot revived against identical original binding: %v", err)
			}
		})
	}
}

func TestReviewRequestWatermarkDoesNotTreatEqualityAsNew(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		id, max uuid.UUID
		scan    bool
	}{
		{"above_needs_no_history", privateID(81), privateID(80), false},
		{"equal_requires_history", privateID(80), privateID(80), true},
		{"below_requires_history", privateID(79), privateID(80), true},
		{"empty_history", privateID(1), uuid.UUID{}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if scan := reviewRequestNeedsScan(row.id, row.max); scan != row.scan {
				t.Fatalf("scan = %v; want %v", scan, row.scan)
			}
		})
	}
}

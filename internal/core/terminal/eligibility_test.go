package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

func eligibilityDisplay() (lineIDs, session.EligibilityView) {
	ids := lineIDs{agent: uuid.NewV7(), context: uuid.NewV7(), turn: uuid.NewV7()}
	initial := ledger.EligibilityTurn{ContextID: ids.context, TurnID: uuid.NewV7(), StartCommandID: uuid.NewV7(), AdmissionCommandID: uuid.NewV7(), InputSHA256: ledger.Digest{1}}
	permitted := ledger.EligibilityTurn{ContextID: ids.context, TurnID: uuid.NewV7(), StartCommandID: uuid.NewV7(), AdmissionCommandID: uuid.NewV7(), InputSHA256: ledger.Digest{2}}
	return ids, session.EligibilityView{State: "first_action_required", Reason: "owner_review", Version: 3, OperationID: uuid.NewV7(), ThroughSequence: 42, FirstAction: &session.EligibilityFirstAction{Declaration: ledger.EligibilityFirstAction{ActionID: uuid.NewV7(), InitialTurn: initial, PermittedTurn: permitted}, Disposition: "not_admitted"}}
}

func requireDisplay(t *testing.T, output string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(output, part) {
			t.Fatalf("terminal omitted %q: %s", part, output)
		}
	}
}

// A refused send carries a gate even when the refusal is not eligibility-held.
// The gate's state and disposition, not Data/Refusal presence, control display.
func TestEligibilitySendRendering(t *testing.T) {
	t.Parallel()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held_%t", held), func(t *testing.T) {
			t.Parallel()
			ids, view := eligibilityDisplay()
			cause := errors.New("private error detail")
			if held {
				cause = fmt.Errorf("private error detail: %w", session.ErrEligibilityHeld)
			} else {
				view.State, view.Reason, view.FirstAction = "eligible", "none", nil
			}
			var output bytes.Buffer
			r := runner{cfg: Config{Output: &output, Describe: func(error) string { return "other refusal" }}}
			r.result(job{ids: ids, command: session.Command{Operation: "message.send"}}, session.CommandResult{Data: view}, cause)
			got := output.String()
			requireDisplay(t, got, "state="+view.State, "reason="+view.Reason, "version=3", "operation="+view.OperationID.String(), "through=42")
			if held {
				p := view.FirstAction.Declaration.PermittedTurn
				requireDisplay(t, got, "eligibility held", view.FirstAction.Declaration.ActionID.String(), "disposition=not_admitted", p.ContextID.String(), p.TurnID.String(), p.StartCommandID.String(), p.AdmissionCommandID.String())
				if strings.Contains(got, "other refusal") {
					t.Fatal("held class reached the host's generic error fallback")
				}
			} else if strings.Contains(got, "eligibility held") {
				t.Fatal("an eligible view was rendered as held")
			}
			for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
				if !strings.HasPrefix(line, ids.label()+" ") || strings.Contains(line, "private error detail") {
					t.Fatal("refusal lost attribution or disclosed its wrapped error", line)
				}
			}
		})
	}
}

func TestEligibilityStatusRendering(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"context", "eligibility_held", "recovery_pending"} {
		for _, redacted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/redacted_%t", state, redacted), func(t *testing.T) {
				t.Parallel()
				ids, view := eligibilityDisplay()
				action := view.FirstAction.Declaration.ActionID
				if redacted {
					view.FirstAction = nil
				}
				var data any = session.TurnView{TurnID: ids.turn, State: state, OwnerVersion: 7, SavedPhase: "model_pending", Eligibility: &view}
				if state == "context" {
					data = session.ContextView{ContextID: ids.context, Eligibility: &view}
				}
				var output bytes.Buffer
				r := runner{cfg: Config{Output: &output}}
				r.result(job{ids: ids}, session.CommandResult{Data: data}, nil)
				got := output.String()
				requireDisplay(t, got, "state=first_action_required", "reason=owner_review", "version=3", view.OperationID.String())
				if state != "context" {
					requireDisplay(t, got, "model_pending")
				}
				if state == "recovery_pending" {
					requireDisplay(t, got, "unresumable")
					if strings.Contains(got, "state=eligibility_held") {
						t.Fatal("recovery availability replaced with the live held overlay")
					}
				}
				if redacted {
					if strings.Contains(got, action.String()) || strings.Contains(got, "first-action") || strings.Contains(got, "admission=") {
						t.Fatal("redacted gate invented or disclosed a declaration", got)
					}
				} else {
					requireDisplay(t, got, action.String(), "disposition=not_admitted")
				}
			})
		}
	}
}

func TestEligibilityFirstActionRendering(t *testing.T) {
	t.Parallel()
	for _, disposition := range []string{"not_admitted", "admission_refused", "admitted_unbound", "bound", "terminal", "unsupported"} {
		t.Run(disposition, func(t *testing.T) {
			t.Parallel()
			ids, view := eligibilityDisplay()
			first := view.FirstAction
			first.Disposition = disposition
			first.FirstAdmittedTurn = &first.Declaration.InitialTurn
			first.FirstAdmittedSequence = 10
			permitted := &first.Declaration.PermittedTurn
			permitted.TaskBinding = &ledger.TaskBinding{TaskID: uuid.NewV7(), AssignmentID: uuid.NewV7(), Generation: 2}
			first.Declaration.InitialTurn.TaskBinding = permitted.TaskBinding
			if disposition != "not_admitted" {
				first.Declaration.Refusal = &ledger.EligibilityRefusal{TurnID: permitted.TurnID, StartCommandID: permitted.StartCommandID, Stage: "turn_admission", Code: "denied"}
			}
			switch disposition {
			case "admitted_unbound":
				first.NativeSequence = 20
			case "bound":
				first.NativeSequence, first.BoundSequence = 20, 24
			case "terminal":
				first.NativeSequence, first.BoundSequence, first.NativeEndingSequence = 20, 24, 37
			case "unsupported":
				first.Declaration.Refusal.Code = "future_refusal"
			}
			var output bytes.Buffer
			r := runner{cfg: Config{Output: &output}}
			r.result(job{ids: ids}, session.CommandResult{Data: session.ContextView{Eligibility: &view}}, nil)
			got := output.String()
			requireDisplay(t, got, "disposition="+disposition, "first_admitted_sequence=10")
			if first.NativeSequence != 0 {
				requireDisplay(t, got, "native_sequence=20")
			}
			if first.BoundSequence != 0 {
				requireDisplay(t, got, "bound_sequence=24")
			}
			if first.NativeEndingSequence != 0 {
				requireDisplay(t, got, "native_ending_sequence=37")
			}
			for _, turn := range []ledger.EligibilityTurn{first.Declaration.InitialTurn, first.Declaration.PermittedTurn, *first.FirstAdmittedTurn} {
				requireDisplay(t, got, turn.ContextID.String(), turn.TurnID.String(), turn.StartCommandID.String(), turn.AdmissionCommandID.String(), turn.InputSHA256.String())
			}
			binding := first.Declaration.PermittedTurn.TaskBinding
			requireDisplay(t, got, binding.TaskID.String(), binding.AssignmentID.String(), "generation=2")
			if disposition == "admission_refused" {
				requireDisplay(t, got, "turn_admission", "denied")
			} else if strings.Contains(got, "denied") {
				t.Fatal("a historical refusal overrode the current disposition", got)
			}
		})
	}
}

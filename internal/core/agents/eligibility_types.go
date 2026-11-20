package agents

import (
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// ErrEligibilityStale refuses a transition of another hold or a closed edge.
var ErrEligibilityStale = errors.New("agents: stale eligibility transition")

// EligibilityHold opens an ordinary hold, or withdraws a declared first turn.
// ExpectedOperationID is absent only when opening a hold from eligible.
type EligibilityHold struct {
	CommandID, AgentID  uuid.UUID
	ExpectedVersion     int64
	ExpectedOperationID uuid.UUID
	Reason              string
}

// EligibilityRelease releases only the named version of the named hold.
type EligibilityRelease struct {
	CommandID, AgentID  uuid.UUID
	ExpectedVersion     int64
	ExpectedOperationID uuid.UUID
}

type EligibilityReceipt struct {
	CommandID                uuid.UUID
	SubmissionSHA256         ledger.Digest
	Operation                string
	Version                  int64
	OperationID              uuid.UUID
	Sequence, EffectSequence uint64
}

// EligibilityHeldError carries a detached, disclosure-filtered snapshot. It is
// neither capacity exhaustion nor permission to retry an earlier refused start.
type EligibilityHeldError struct {
	AgentID uuid.UUID
	session.EligibilityView
}

func (*EligibilityHeldError) Error() string { return "agents: eligibility held" }
func (*EligibilityHeldError) Unwrap() error { return session.ErrEligibilityHeld }

type eligibilityState struct {
	snapshot                                                        ledger.RuntimeEligibilityChanged
	outcome                                                         string
	sequence                                                        uint64
	firstAdmitted                                                   ledger.EligibilityTurn
	firstAdmittedSequence                                           uint64
	nativeSequence, boundSequence                                   uint64
	nativeEndingSequence, loopEndingSequence, ownerTerminalSequence uint64
}

type eligibilityGroup struct {
	ids     submissionIDs
	first   uint64
	command ledger.Command
	change  ledger.RuntimeEligibilityChanged
	receipt ledger.RuntimeEligibilityReceipt
}

func (g *eligibilityGroup) result() EligibilityReceipt {
	return EligibilityReceipt{CommandID: g.command.CommandID, SubmissionSHA256: g.command.SubmissionSHA256,
		Operation: g.receipt.Submission.Operation, Version: g.change.Version, OperationID: g.change.OperationID,
		Sequence: g.first + 2, EffectSequence: uint64(g.receipt.EffectSequence)}
}

func cloneEligibilityFirst(first *ledger.EligibilityFirstAction) *ledger.EligibilityFirstAction {
	if first == nil {
		return nil
	}
	copy := *first
	if copy.InitialTurn.TaskBinding != nil {
		copy.InitialTurn.TaskBinding = new(*copy.InitialTurn.TaskBinding)
	}
	if copy.PermittedTurn.TaskBinding != nil {
		copy.PermittedTurn.TaskBinding = new(*copy.PermittedTurn.TaskBinding)
	}
	if copy.Refusal != nil {
		copy.Refusal = new(*copy.Refusal)
	}
	return &copy
}

func (e eligibilityState) current() ledger.RuntimeEligibilityChanged {
	v := e.snapshot
	if v.Version == 0 {
		v.State, v.Reason = "eligible", "none"
	}
	v.FirstAction = cloneEligibilityFirst(v.FirstAction)
	return v
}

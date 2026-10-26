package agents

import (
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type paidGroup struct {
	ids             submissionIDs
	turn            uuid.UUID
	first           uint64
	command         ledger.Command
	receipt         ledger.RuntimePaidReceipt
	route           *ledger.RuntimePaidRouteConfigured
	credential      *ledger.RuntimeCredentialChanged
	spend           *ledger.RuntimeSpendConfigured
	reserved        *ledger.RuntimePaidReserved
	intent          *ledger.RuntimePaidDispatchIntent
	result          *ledger.RuntimePaidResult
	acknowledgement *ledger.RuntimePaidOverrunAcknowledged
}

func isPaidType(typ string) bool {
	switch typ {
	case ledger.TypeRuntimePaidRouteConfigured, ledger.TypeRuntimeCredentialChanged, ledger.TypeRuntimeSpendConfigured, ledger.TypeRuntimePaidReserved, ledger.TypeRuntimePaidDispatchIntent, ledger.TypeRuntimePaidResult, ledger.TypeRuntimePaidOverrunAcknowledged, ledger.TypeRuntimePaidReceipt:
		return true
	}
	return false
}

func decodePaidGroup(drafts []ledger.Draft, first uint64) (*paidGroup, error) {
	bad := func(reason string) (*paidGroup, error) { return nil, inconsistent(first, reason) }
	if len(drafts) != 3 || drafts[0].Type != ledger.TypeCommand || drafts[2].Type != ledger.TypeRuntimePaidReceipt {
		return bad("paid group boundaries")
	}
	command, err := ledger.DecodeCommand(drafts[0].Payload)
	if err != nil {
		return bad("paid command")
	}
	receipt, err := ledger.DecodeRuntimePaidReceipt(drafts[2].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 {
		return bad("paid receipt identity")
	}
	sub := receipt.Submission
	g := &paidGroup{ids: submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}, turn: drafts[0].TurnID, first: first, command: command, receipt: receipt}
	effect := ""
	work := false
	switch sub.Operation {
	case "paid.route.configure":
		effect = ledger.TypeRuntimePaidRouteConfigured
		v, e := ledger.DecodeRuntimePaidRouteConfigured(drafts[1].Payload)
		g.route, err = &v, e
	case "paid.credential.bind", "paid.credential.revoke":
		effect = ledger.TypeRuntimeCredentialChanged
		v, e := ledger.DecodeRuntimeCredentialChanged(drafts[1].Payload)
		g.credential, err = &v, e
	case "paid.spend.configure", "paid.spend.delegate":
		effect = ledger.TypeRuntimeSpendConfigured
		v, e := ledger.DecodeRuntimeSpendConfigured(drafts[1].Payload)
		g.spend, err = &v, e
	case "paid.admit":
		effect, work = ledger.TypeRuntimePaidReserved, true
		v, e := ledger.DecodeRuntimePaidReserved(drafts[1].Payload)
		g.reserved, err = &v, e
	case "paid.dispatch":
		effect, work = ledger.TypeRuntimePaidDispatchIntent, true
		v, e := ledger.DecodeRuntimePaidDispatchIntent(drafts[1].Payload)
		g.intent, err = &v, e
	case "paid.observe", "paid.release", "paid.correct":
		effect, work = ledger.TypeRuntimePaidResult, true
		v, e := ledger.DecodeRuntimePaidResult(drafts[1].Payload)
		g.result, err = &v, e
	case "paid.overrun.acknowledge":
		effect = ledger.TypeRuntimePaidOverrunAcknowledged
		v, e := ledger.DecodeRuntimePaidOverrunAcknowledged(drafts[1].Payload)
		g.acknowledgement, err = &v, e
	default:
		return bad("paid operation")
	}
	if err != nil || work != (g.turn != (uuid.UUID{})) {
		return bad("paid effect or turn")
	}
	for i, draft := range drafts {
		actor := ledger.Actor(sub.Actor)
		if i == 1 && sub.Operation == "paid.admit" {
			actor = ledger.ActorCore
		}
		if draft.TypeVersion != 1 || draft.Actor != actor || draft.AgentID != g.ids.agent || draft.ExecutionSessionID != g.ids.session || draft.ContextID != g.ids.context || draft.TurnID != g.turn || draft.OperationID != command.CommandID || draft.MeasurementPath != "" || len(draft.BodyRefs) != 0 || i == 1 && draft.Type != effect {
			return bad("paid envelope or order")
		}
	}
	return g, nil
}

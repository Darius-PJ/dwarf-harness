package agents

import (
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type reviewGroup struct {
	command      ledger.Command
	receipt      ledger.RuntimeReviewReceipt
	request      *ledger.RuntimeReviewRequested
	result       *ledger.RuntimeReviewResult
	first        uint64
	receiptIndex int
	sources      map[int64]ledger.Record // prepared immutable witnesses; nil during replay
}

func isReviewType(typ string) bool {
	return typ == ledger.TypeRuntimeReviewRequested || typ == ledger.TypeRuntimeReviewResult || typ == ledger.TypeRuntimeReviewReceipt
}

func decodeReviewGroup(d []ledger.Draft, first uint64) (*reviewGroup, error) {
	bad := func(reason string) (*reviewGroup, error) { return nil, inconsistent(first, reason) }
	if len(d) < 3 || len(d) > 4 || d[0].Type != ledger.TypeCommand || d[len(d)-1].Type != ledger.TypeRuntimeReviewReceipt {
		return bad("review group boundaries")
	}
	command, err := ledger.DecodeCommand(d[0].Payload)
	if err != nil {
		return bad("review command")
	}
	receipt, err := ledger.DecodeRuntimeReviewReceipt(d[len(d)-1].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 {
		return bad("review receipt identity")
	}
	g := &reviewGroup{command: command, receipt: receipt, first: first, receiptIndex: len(d) - 1}
	sub := receipt.Submission
	want := []string{ledger.TypeCommand}
	var id uuid.UUID
	var binding ledger.ReviewBinding
	var version, taskVersion int64
	var outcome string
	switch args := sub.Arguments.(type) {
	case ledger.ReviewRequestArguments:
		q, e := ledger.DecodeRuntimeReviewRequested(d[1].Payload)
		if e != nil {
			return bad("review request payload")
		}
		if sub.Operation != q.Kind+".request" || q.RequesterActor != sub.Actor || q.Requester != sub.Origin || q.RequesterGrantID != sub.GrantID || !sameScopedValue(args, ledger.ReviewRequestArguments{RequestID: q.RequestID, Binding: q.Binding, ReplacesRequestID: q.ReplacesRequestID, FailureCause: q.FailureCause, FailingEvidence: q.FailingEvidence}) {
			return bad("review request submission")
		}
		g.request = &q
		id, binding, version, taskVersion, outcome = q.RequestID, q.Binding, 1, q.Binding.TaskVersion, "requested"
		want = append(want, ledger.TypeRuntimeReviewRequested)
	case ledger.ReviewResolveArguments, ledger.FailureResolveArguments:
		v, e := ledger.DecodeRuntimeReviewResult(d[1].Payload)
		if e != nil {
			return bad("review result payload")
		}
		var expected any
		if v.Verdict == "fail" {
			if sub.Operation != "failure.resolve" {
				return bad("failure result operation")
			}
			expected = ledger.FailureResolveArguments{RequestID: v.RequestID, RequestSequence: v.RequestSequence, Binding: v.Binding, Cause: v.Cause, FailingEvidence: *v.FailingEvidence}
		} else {
			if sub.Operation != "review.resolve" {
				return bad("review result operation")
			}
			expected = ledger.ReviewResolveArguments{RequestID: v.RequestID, RequestSequence: v.RequestSequence, Binding: v.Binding, Verdict: v.Verdict}
		}
		if !sameScopedValue(args, expected) || (ledger.ReviewPrincipal{Actor: sub.Actor, Origin: sub.Origin, GrantID: sub.GrantID}) != v.Binding.Reviewer {
			return bad("review result submission")
		}
		g.result = &v
		id, binding, version, taskVersion = v.RequestID, v.Binding, 2, v.TaskVersion
		outcome = reviewOutcome(v.Verdict)
		want = append(want, ledger.TypeRuntimeReviewResult)
		if v.Verdict != "reject" {
			want = append(want, ledger.TypeBodyUnpin)
		}
	default:
		return bad("review operation")
	}
	want = append(want, ledger.TypeRuntimeReviewReceipt)
	if len(d) != len(want) || receipt.Outcome != outcome || receipt.Version != version || receipt.RequestID != id || receipt.TaskVersion != taskVersion {
		return bad("review shape or outcome")
	}
	for i, r := range d {
		actor := ledger.Actor(sub.Actor)
		if r.Type == ledger.TypeBodyUnpin {
			actor = ledger.ActorCore
		}
		if r.Type != want[i] || r.TypeVersion != 1 || r.Actor != actor || r.AgentID != sub.Origin.AgentID || r.ExecutionSessionID != sub.Origin.SessionID || r.ContextID != sub.Origin.ContextID || r.OperationID != command.CommandID || r.TurnID != (uuid.UUID{}) || r.MeasurementPath != "" || len(r.BodyRefs) != 0 {
			return bad("review envelope or order")
		}
		if r.Type == ledger.TypeBodyUnpin {
			u, e := ledger.DecodeBodyUnpin(r.Payload)
			if e != nil || u.PinID != binding.TaskID {
				return bad("review task description unpin")
			}
		}
	}
	return g, nil
}

func reviewOutcome(verdict string) string {
	switch verdict {
	case "approve":
		return "approved"
	case "reject":
		return "rejected"
	case "fail":
		return "failed"
	}
	return ""
}

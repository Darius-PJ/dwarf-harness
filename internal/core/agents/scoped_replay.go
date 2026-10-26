package agents

import (
	"bytes"
	"encoding/json"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type scopedGroup struct {
	ids                       submissionIDs
	turn                      uuid.UUID
	first                     uint64
	command                   ledger.Command
	receipt                   ledger.RuntimeScopedReceipt
	issue                     *ledger.RuntimeGrantIssued
	revoke                    *ledger.RuntimeGrantRevoked
	task                      *ledger.RuntimeTaskChanged
	config                    *ledger.RuntimeCapacityConfigured
	admission                 *ledger.RuntimeWorkAdmitted
	dispatch                  *ledger.RuntimeWorkDispatched
	ending                    *ledger.RuntimeWorkEnded
	usage                     *ledger.RuntimeLocalUsed
	revision                  *ledger.PrivateScopedRevision
	effectIndex, receiptIndex int
}

func isScopedType(typ string) bool {
	switch typ {
	case ledger.TypeRuntimeGrantIssued, ledger.TypeRuntimeGrantRevoked, ledger.TypeRuntimeTaskChanged, ledger.TypeRuntimeCapacityConfigured,
		ledger.TypeRuntimeWorkAdmitted, ledger.TypeRuntimeWorkDispatched, ledger.TypeRuntimeWorkEnded, ledger.TypeRuntimeLocalUsed,
		ledger.TypePrivateScopedRevision, ledger.TypeRuntimeScopedReceipt:
		return true
	default:
		return false
	}
}

// Dispatch complete groups before any family validator. Shared F0 records do
// not select a family; no group may embed two F3 families.
func decodeFamily(drafts []ledger.Draft, first uint64) (*privateGroup, *scopedGroup, *reviewGroup, *paidGroup, error) {
	var private, scoped, review, paid bool
	for _, draft := range drafts {
		private = private || isPrivateType(draft.Type)
		scoped = scoped || isScopedType(draft.Type)
		review = review || isReviewType(draft.Type)
		paid = paid || isPaidType(draft.Type)
	}
	if private && scoped || review && (private || scoped) || paid && (private || scoped || review) {
		return nil, nil, nil, nil, inconsistent(first, "mixed record families")
	}
	if paid {
		g, err := decodePaidGroup(drafts, first)
		return nil, nil, nil, g, err
	}
	if review {
		g, err := decodeReviewGroup(drafts, first)
		return nil, nil, g, nil, err
	}
	if scoped {
		g, err := decodeScopedGroup(drafts, first)
		return nil, g, nil, nil, err
	}
	g, err := decodeGroup(drafts, first)
	return g, nil, nil, nil, err
}

func decodeScopedGroup(drafts []ledger.Draft, first uint64) (*scopedGroup, error) {
	bad := func(reason string) (*scopedGroup, error) { return nil, inconsistent(first, reason) }
	if len(drafts) < 3 || len(drafts) > 5 || drafts[0].Type != ledger.TypeCommand || drafts[len(drafts)-1].Type != ledger.TypeRuntimeScopedReceipt {
		return bad("scoped group boundaries")
	}
	command, err := ledger.DecodeCommand(drafts[0].Payload)
	if err != nil {
		return bad("scoped command")
	}
	receipt, err := ledger.DecodeRuntimeScopedReceipt(drafts[len(drafts)-1].Payload)
	if err != nil || receipt.CommandID != command.CommandID || receipt.SubmissionSHA256 != command.SubmissionSHA256 {
		return bad("scoped receipt identity")
	}
	sub := receipt.Submission
	g := &scopedGroup{ids: submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}, first: first, turn: drafts[0].TurnID, command: command, receipt: receipt, effectIndex: 1, receiptIndex: len(drafts) - 1}
	want := []string{ledger.TypeCommand}
	outcome := "applied"
	work := false
	switch sub.Operation {
	case "grant.issue", "grant.revoke":
		if sub.Actor != "owner" && sub.Actor != "agent" {
			return bad("grant principal")
		}
		if sub.Operation == "grant.issue" {
			want = append(want, ledger.TypeRuntimeGrantIssued)
			if sub.Actor == "agent" {
				want = append(want, ledger.TypeRuntimeLocalUsed)
			}
		} else {
			want = append(want, ledger.TypeRuntimeGrantRevoked)
		}
	case "capacity.configure":
		if sub.Actor != "owner" {
			return bad("capacity principal")
		}
		want = append(want, ledger.TypeRuntimeCapacityConfigured)
	case "task.create", "task.claim", "task.assign", "task.start", "task.wait", "task.resume", "task.submit", "task.complete", "task.fail", "task.cancel", "task.reassign":
		if sub.Actor != "owner" && sub.Actor != "agent" {
			return bad("task principal")
		}
		if sub.Target.Kind != "task" || !validID(sub.Target.TaskID) {
			return bad("task target")
		}
		if sub.Operation == "task.create" {
			want = append(want, ledger.TypeBodyPin)
			g.effectIndex = 2
		}
		want = append(want, ledger.TypeRuntimeTaskChanged)
		if sub.Operation == "task.complete" || sub.Operation == "task.fail" || sub.Operation == "task.cancel" {
			want = append(want, ledger.TypeBodyUnpin)
		}
		if sub.Actor == "agent" {
			want = append(want, ledger.TypeRuntimeLocalUsed)
		}
	case "turn.start", "local.admit":
		if sub.Actor != "agent" {
			return bad("admission principal")
		}
		want = append(want, ledger.TypeRuntimeWorkAdmitted)
		outcome, work = "admitted", true
	case "local.begin":
		if sub.Actor != "core" {
			return bad("dispatch principal")
		}
		want = append(want, ledger.TypeRuntimeWorkDispatched)
		outcome, work = "dispatched", true
	case "local.end", "turn.end", "turn.cancel":
		if sub.Operation == "turn.cancel" {
			if sub.Actor != "owner" && sub.Actor != "agent" {
				return bad("cancel principal")
			}
		} else if sub.Actor != "core" {
			return bad("ending principal")
		}
		want = append(want, ledger.TypeRuntimeWorkEnded)
		outcome, work = "recorded", true
	case "private.write":
		if sub.Actor != "agent" || sub.Target.Kind != "context" || len(sub.Target.Slots) == 0 {
			return bad("private write principal or target")
		}
		want = append(want, ledger.TypePrivateScopedRevision, ledger.TypeRuntimeLocalUsed)
	case "private.read", "artifact.read":
		if sub.Actor != "agent" || sub.Operation == "private.read" && (sub.Target.Kind != "context" || len(sub.Target.Slots) != 1) || sub.Operation == "artifact.read" && sub.Target.Kind != "artifact" {
			return bad("read principal or target")
		}
		want = append(want, ledger.TypeRuntimeLocalUsed)
	default:
		return bad("unknown scoped operation")
	}
	want = append(want, ledger.TypeRuntimeScopedReceipt)
	if len(want) != len(drafts) || receipt.Outcome != outcome || work != (g.turn != (uuid.UUID{})) {
		return bad("scoped shape or outcome")
	}
	if work && (sub.Operation == "turn.start" || sub.Operation == "turn.end" || sub.Operation == "turn.cancel") && sub.Target.Kind != "context" || work && (sub.Operation == "local.admit" || sub.Operation == "local.begin" || sub.Operation == "local.end") && sub.Target.Kind != "workspace" {
		return bad("work target")
	}
	for i, draft := range drafts {
		actor := ledger.Actor(sub.Actor)
		switch draft.Type {
		case ledger.TypeRuntimeWorkAdmitted, ledger.TypeRuntimeWorkDispatched, ledger.TypeRuntimeWorkEnded, ledger.TypePrivateScopedRevision, ledger.TypeRuntimeLocalUsed, ledger.TypeBodyPin, ledger.TypeBodyUnpin:
			actor = ledger.ActorCore
		}
		if draft.Type != want[i] || draft.TypeVersion != 1 || draft.Actor != actor || draft.AgentID != g.ids.agent || draft.ExecutionSessionID != g.ids.session || draft.ContextID != g.ids.context || draft.OperationID != command.CommandID || draft.TurnID != g.turn || draft.MeasurementPath != "" {
			return bad("scoped envelope or order")
		}
		switch draft.Type {
		case ledger.TypeRuntimeGrantIssued:
			p, e := ledger.DecodeRuntimeGrantIssued(draft.Payload)
			g.issue, err = &p, e
		case ledger.TypeRuntimeGrantRevoked:
			p, e := ledger.DecodeRuntimeGrantRevoked(draft.Payload)
			g.revoke, err = &p, e
		case ledger.TypeRuntimeTaskChanged:
			p, e := ledger.DecodeRuntimeTaskChanged(draft.Payload)
			g.task, err = &p, e
		case ledger.TypeRuntimeCapacityConfigured:
			p, e := ledger.DecodeRuntimeCapacityConfigured(draft.Payload)
			g.config, err = &p, e
		case ledger.TypeRuntimeWorkAdmitted:
			p, e := ledger.DecodeRuntimeWorkAdmitted(draft.Payload)
			g.admission, err = &p, e
		case ledger.TypeRuntimeWorkDispatched:
			p, e := ledger.DecodeRuntimeWorkDispatched(draft.Payload)
			g.dispatch, err = &p, e
		case ledger.TypeRuntimeWorkEnded:
			p, e := ledger.DecodeRuntimeWorkEnded(draft.Payload)
			g.ending, err = &p, e
		case ledger.TypeRuntimeLocalUsed:
			p, e := ledger.DecodeRuntimeLocalUsed(draft.Payload)
			g.usage, err = &p, e
		case ledger.TypePrivateScopedRevision:
			p, e := ledger.DecodePrivateScopedRevision(draft.Payload)
			g.revision, err = &p, e
		case ledger.TypeBodyPin:
			p, e := ledger.DecodeBodyPin(draft.Payload)
			err = e
			if p.PinID != sub.Target.TaskID {
				return bad("task description pin")
			}
		case ledger.TypeBodyUnpin:
			p, e := ledger.DecodeBodyUnpin(draft.Payload)
			err = e
			if p.PinID != sub.Target.TaskID {
				return bad("task description unpin")
			}
		}
		if err != nil {
			return bad("scoped effect payload")
		}
	}
	for _, draft := range drafts {
		var refs []ledger.Digest
		if g.task != nil && (draft.Type == ledger.TypeBodyPin || draft.Type == ledger.TypeRuntimeTaskChanged && !terminalTask(g.task.State)) {
			refs = []ledger.Digest{g.task.Description.SHA256}
		}
		if !slices.Equal(draft.BodyRefs, refs) {
			return bad("scoped body references")
		}
	}
	return g, nil
}

func terminalTask(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}

func sameScopedValue(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

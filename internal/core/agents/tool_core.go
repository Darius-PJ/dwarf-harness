package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type loopArtifact struct {
	SourceSequence int64  `json:"source_sequence"`
	SHA256         string `json:"sha256"`
	Bytes          int64  `json:"bytes"`
}
type loopPrivateRevision struct {
	Binding        ledger.Binding `json:"binding"`
	Scope          string         `json:"scope"`
	Slot           string         `json:"slot"`
	Revision       int64          `json:"revision"`
	SourceSequence int64          `json:"source_sequence"`
	SHA256         string         `json:"sha256"`
	Bytes          int64          `json:"bytes"`
}
type loopSubject struct {
	Kind            string               `json:"kind"`
	Artifact        *loopArtifact        `json:"artifact,omitempty"`
	PrivateRevision *loopPrivateRevision `json:"private_revision,omitempty"`
}

func loopDigest(raw string) (ledger.Digest, error) {
	var digest ledger.Digest
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != len(digest) {
		return digest, ErrInvalid
	}
	copy(digest[:], decoded)
	return digest, nil
}
func (a loopArtifact) value() (ledger.ArtifactRef, error) {
	digest, err := loopDigest(a.SHA256)
	return ledger.ArtifactRef{SourceSequence: a.SourceSequence, SHA256: digest, Bytes: a.Bytes}, err
}
func (s *loopSubject) value() (*ledger.SubjectRef, error) {
	if s == nil {
		return nil, nil
	}
	out := &ledger.SubjectRef{Kind: s.Kind}
	var err error
	if s.Artifact != nil {
		out.Artifact, err = s.Artifact.value()
	}
	if p := s.PrivateRevision; p != nil {
		digest, e := loopDigest(p.SHA256)
		err = e
		out.PrivateRevision = &ledger.PrivateRevisionRef{Binding: p.Binding, Scope: p.Scope, Slot: p.Slot, Revision: p.Revision, SourceSequence: p.SourceSequence, SHA256: digest, Bytes: p.Bytes}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

type loopPrivateChunk struct {
	Revision uint64        `json:"revision"`
	Sequence uint64        `json:"sequence"`
	SHA256   ledger.Digest `json:"sha256"`
	Bytes    int           `json:"bytes"`
	Text     string        `json:"text"`
}
type loopPrivateData struct {
	Receipt    ScopedReceipt      `json:"receipt"`
	Revision   uint64             `json:"revision"`
	Present    bool               `json:"present"`
	SHA256     ledger.Digest      `json:"sha256"`
	Bytes      int                `json:"bytes"`
	Text       *string            `json:"text,omitempty"`
	Chunks     []loopPrivateChunk `json:"chunks,omitempty"`
	NextCursor uint64             `json:"next_cursor"`
}
type loopTaskData struct {
	Receipt ScopedReceipt `json:"receipt"`
	State   string        `json:"state"`
}
type loopPrivateWriteData struct {
	Receipt  ScopedReceipt `json:"receipt"`
	Revision uint64        `json:"revision"`
}
type loopArtifactData struct {
	Receipt ScopedReceipt `json:"receipt"`
	Text    string        `json:"text"`
	SHA256  ledger.Digest `json:"sha256"`
	Bytes   int           `json:"bytes"`
}
type loopTaskFactsData struct {
	WaitingReady    []TaskFact     `json:"waiting_ready"`
	WaitingBlocked  []TaskFact     `json:"waiting_blocked"`
	Active          []TaskFact     `json:"active"`
	Counts          TaskFactCounts `json:"counts"`
	ThroughSequence uint64         `json:"through_sequence"`
	Incomplete      bool           `json:"incomplete"`
}
type loopCoreArgs struct {
	Schema               int                     `json:"schema"`
	Subject              string                  `json:"subject"`
	ID                   uuid.UUID               `json:"id"`
	Action               string                  `json:"action"`
	TaskID               uuid.UUID               `json:"task_id"`
	ExpectedVersion      uint64                  `json:"expected_version"`
	AssignmentID         uuid.UUID               `json:"assignment_id"`
	Generation           int64                   `json:"generation"`
	WaitReason           string                  `json:"wait_reason"`
	WaitArtifact         *loopSubject            `json:"wait_artifact"`
	Submission           *loopSubject            `json:"submission"`
	Scope                string                  `json:"scope"`
	Slot                 string                  `json:"slot"`
	Revision             uint64                  `json:"revision"`
	Cursor               uint64                  `json:"cursor"`
	MaxBytes             int64                   `json:"max_bytes"`
	Text                 string                  `json:"text"`
	Description          string                  `json:"description"`
	AcceptanceSHA256     string                  `json:"acceptance_sha256"`
	Dependencies         []uuid.UUID             `json:"dependencies"`
	Audience             []uuid.UUID             `json:"audience"`
	Assignment           *ledger.AssignmentInput `json:"assignment"`
	NewAssignmentID      uuid.UUID               `json:"new_assignment_id"`
	ExpectedAssignmentID uuid.UUID               `json:"expected_assignment_id"`
	ExpectedGeneration   int64                   `json:"expected_generation"`
	NewAssignment        ledger.AssignmentInput  `json:"new_assignment"`
	FailureCause         string                  `json:"failure_cause"`
	FailureEvidence      *loopSubject            `json:"failure_evidence"`
	Limit                int                     `json:"limit"`
	Artifact             *loopArtifact           `json:"artifact"`
	Query                string                  `json:"query"`
	After                string                  `json:"after"`
}

func (r *loopRuntime) coreTool(t *loopTurn, inv *loopInvocation) (data any, sequence uint64, err error) {
	var args loopCoreArgs
	raw, path := inv.normalized.Arguments, ""
	if inv.normalized.ToolName == "tools_invoke_v1" {
		var wrapper struct {
			Schema          int             `json:"schema"`
			ToolPath        string          `json:"tool_path"`
			OperationSchema int             `json:"operation_schema"`
			Arguments       json.RawMessage `json:"arguments"`
		}
		if decodeLoopJSON(raw, &wrapper) != nil {
			return nil, 0, ErrInvalid
		}
		raw, path = wrapper.Arguments, wrapper.ToolPath
	}
	if decodeLoopJSON(raw, &args) != nil {
		return nil, 0, ErrInvalid
	}
	s := r.service
	agent := &Agent{service: s, ids: submissionIDs{t.origin.AgentID, t.origin.SessionID, t.origin.ContextID}}
	if inv.started.start.EffectCommandID != (uuid.UUID{}) {
		defer func() {
			// Entrances may have committed before an object/readback error.
			// Select the actual receipt rather than guessing from a return error.
			s.mu.Lock()
			defer s.mu.Unlock()
			if e := s.refreshLocked(context.Background(), s.store.Durable()); e != nil {
				err = e
				return
			}
			match, e := s.findCommand(context.Background(), inv.effectID, 0, s.p.through)
			if e != nil {
				err = e
				return
			}
			if match.found {
				if match.scoped == nil {
					err = ErrIntegrity
					return
				}
				sequence = match.scoped.first + uint64(match.scoped.receiptIndex)
				if err == nil && match.scoped.task != nil {
					receipt, ok := data.(ScopedReceipt)
					if !ok {
						err = ErrIntegrity
						return
					}
					// Only the resulting state from this exact accepted mutation is
					// disclosed, not an unrelated TaskStatus or its private fields.
					data = loopTaskData{Receipt: receipt, State: match.scoped.task.State}
				}
			}
		}()
	}
	if err := r.recheckTool(t, inv); err != nil {
		return nil, 0, err
	}
	switch inv.normalized.Operation {
	case "context.inspect":
		view, err := agent.Snapshot(t.ctx, ScopedSnapshotLookup{GrantID: inv.grant, Scope: "context"})
		if err != nil {
			return nil, 0, err
		}
		return struct {
			Revision uint64          `json:"revision"`
			Heads    map[string]Head `json:"heads"`
		}{view.Revision, view.Heads}, 0, nil
	case "turn.inspect":
		view, err := agent.TurnStatus(t.ctx, WorkLookup{GrantID: inv.grant, WorkID: args.ID})
		return view, 0, err
	case "task.inspect":
		view, err := agent.TaskStatus(t.ctx, TaskLookup{GrantID: inv.grant, TaskID: args.ID})
		return view, 0, err
	case "task.list":
		view, err := agent.ListTaskFacts(t.ctx, TaskFactsQuery{AgentID: t.origin.AgentID, GrantID: inv.grant, Limit: min(args.Limit, 100)})
		return loopTaskFactsData{WaitingReady: view.WaitingReady, WaitingBlocked: view.WaitingBlocked, Active: view.Active, Counts: view.Counts, ThroughSequence: view.ThroughSequence, Incomplete: view.Next != nil}, 0, err
	case "tools.discover":
		return r.catalogue(t, inv, args)
	case "task.create":
		digest, err := loopDigest(args.AcceptanceSHA256)
		if err != nil {
			return nil, 0, err
		}
		receipt, err := agent.CreateTask(t.ctx, TaskCreation{CommandID: inv.effectID, GrantID: inv.grant, TaskID: args.TaskID, Description: []byte(args.Description), AcceptanceSHA256: digest, Dependencies: args.Dependencies, Audience: args.Audience, Assignment: args.Assignment})
		return receipt, receipt.Sequence, err
	case "task.claim":
		receipt, err := agent.ClaimTask(t.ctx, TaskAssignment{CommandID: inv.effectID, GrantID: inv.grant, TaskID: args.TaskID, ExpectedVersion: args.ExpectedVersion, Assignment: ledger.AssignmentInput{AssignmentID: args.NewAssignmentID, Assignee: ledger.Binding(bindingOf(t.origin))}})
		return receipt, receipt.Sequence, err
	case "task.assign", "task.reassign":
		request := TaskAssignment{CommandID: inv.effectID, GrantID: inv.grant, TaskID: args.TaskID, ExpectedVersion: args.ExpectedVersion, ExpectedAssignmentID: args.ExpectedAssignmentID, ExpectedGeneration: args.ExpectedGeneration, Assignment: args.NewAssignment}
		var receipt ScopedReceipt
		if inv.normalized.Operation == "task.assign" {
			receipt, err = agent.AssignTask(t.ctx, request)
		} else {
			receipt, err = agent.ReassignTask(t.ctx, request)
		}
		return receipt, receipt.Sequence, err
	case "task.start", "task.wait", "task.resume", "task.submit", "task.complete", "task.fail", "task.cancel":
		wait, e := args.WaitArtifact.value()
		if e != nil {
			return nil, 0, e
		}
		submission, e := args.Submission.value()
		if e != nil {
			return nil, 0, e
		}
		failure, e := args.FailureEvidence.value()
		if e != nil {
			return nil, 0, e
		}
		action := inv.normalized.Operation[len("task."):]
		receipt, err := agent.TransitionTask(t.ctx, TaskMutation{CommandID: inv.effectID, GrantID: inv.grant, TaskID: args.TaskID, ExpectedVersion: args.ExpectedVersion, ExpectedAssignmentID: args.AssignmentID, ExpectedGeneration: args.Generation, Action: action, WaitReason: args.WaitReason, WaitArtifact: wait, Submission: submission, FailureCause: args.FailureCause, FailureEvidence: failure})
		return receipt, receipt.Sequence, err
	case "private.read":
		scope := args.Scope
		if scope == "" {
			scope = "context"
		}
		page, err := agent.ReadPrivate(t.ctx, ScopedPrivateRead{CommandID: inv.effectID, GrantID: inv.grant, Scope: scope, Slot: args.Slot, Revision: args.Revision, Cursor: args.Cursor, MaxBytes: min(args.MaxBytes, t.policy.MaxResultBytes)})
		if err != nil {
			return nil, page.Receipt.Sequence, err
		}
		result := loopPrivateData{Receipt: page.Receipt, Revision: page.Page.Revision, Present: page.Page.Present, NextCursor: page.Page.NextCursor}
		if args.Slot == "transcript" {
			for _, chunk := range page.Page.Chunks {
				if !utf8.Valid(chunk.Bytes) {
					return nil, page.Receipt.Sequence, ErrUnavailable
				}
				result.Chunks = append(result.Chunks, loopPrivateChunk{Revision: chunk.Revision, Sequence: chunk.Sequence, SHA256: sha256.Sum256(chunk.Bytes), Bytes: len(chunk.Bytes), Text: string(chunk.Bytes)})
			}
		} else {
			if !utf8.Valid(page.Page.Bytes) {
				return nil, page.Receipt.Sequence, ErrUnavailable
			}
			text := string(page.Page.Bytes)
			result.Text, result.SHA256, result.Bytes = &text, sha256.Sum256(page.Page.Bytes), len(page.Page.Bytes)
		}
		return result, page.Receipt.Sequence, nil
	case "private.write":
		scope, slot, version := args.Scope, args.Slot, args.Revision
		if path == "core.context.handoff.write" {
			scope, slot, version = "context", "handoff", args.ExpectedVersion
		}
		result, err := agent.WritePrivate(t.ctx, ScopedPrivateWrite{CommandID: inv.effectID, GrantID: inv.grant, Scope: scope, ExpectedVersion: version, Changes: []Change{{Slot: slot, Mode: "replace", Bytes: []byte(args.Text)}}})
		return loopPrivateWriteData{Receipt: result, Revision: uint64(result.Version)}, result.Sequence, err
	case "artifact.read":
		if args.Artifact == nil {
			return nil, 0, ErrInvalid
		}
		artifact, err := args.Artifact.value()
		if err != nil {
			return nil, 0, err
		}
		page, err := agent.ReadArtifact(t.ctx, ArtifactRead{CommandID: inv.effectID, GrantID: inv.grant, Artifact: artifact, MaxBytes: min(args.MaxBytes, t.policy.MaxResultBytes)})
		if err != nil {
			return nil, page.Receipt.Sequence, err
		}
		if !utf8.Valid(page.Bytes) {
			return nil, page.Receipt.Sequence, ErrUnavailable
		}
		return loopArtifactData{Receipt: page.Receipt, Text: string(page.Bytes), SHA256: sha256.Sum256(page.Bytes), Bytes: len(page.Bytes)}, page.Receipt.Sequence, nil
	default:
		return nil, 0, ErrDenied
	}
}

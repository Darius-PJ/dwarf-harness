package agents

import (
	"bytes"
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) scopedAuthority(ctx context.Context, sub ledger.ScopedSubmission) error {
	if !s.p.registeredOrigin(sub.Origin) {
		return ErrNotBound
	}
	if sub.Actor == "owner" || sub.Actor == "core" && sub.Operation != "local.begin" {
		return nil
	}
	if sub.Actor == "core" {
		args, ok := sub.Arguments.(ledger.WorkDispatchArguments)
		if !ok {
			return ErrInvalid
		}
		work, found, err := s.loadWork(ctx, args.WorkID, s.p.through)
		if err != nil {
			return err
		}
		if !found || work.admission.Kind != "local" {
			return ErrUnavailable
		}
		_, err = s.authorize(ctx, work.origin, work.admission.GrantID, work.admission.Operation, []ledger.Resource{work.admission.Target}, nil, *work.admission.MaxBytes, s.p.through)
		return err
	}
	if sub.Actor != "agent" {
		return ErrDenied
	}
	targets, recipients, size := []ledger.Resource{sub.Target}, []Binding(nil), int64(0)
	operation := sub.Operation
	switch args := sub.Arguments.(type) {
	case ledger.GrantIssueArguments:
		targets = args.Resources
		for _, recipient := range args.Recipients {
			recipients = append(recipients, Binding(recipient))
		}
	case ledger.GrantRevokeArguments:
		grant, found, err := s.loadGrant(ctx, args.GrantID, s.p.through)
		if err != nil {
			return err
		}
		if !found || grant.issuer != "agent" || grant.origin != sub.Origin {
			return ErrDenied
		}
		targets, recipients = grant.definition.Resources, grant.rules().recipients
	case ledger.TaskCreateArguments:
		size = args.Description.Bytes
		if args.Assignment != nil {
			recipients = []Binding{Binding(args.Assignment.Assignee)}
		}
	case ledger.TaskMutationArguments:
		if args.NewAssignment != nil {
			recipients = []Binding{Binding(args.NewAssignment.Assignee)}
		}
	case ledger.LocalAdmissionArguments:
		operation, size = args.LocalOperation, args.MaxBytes
	case ledger.PrivateWriteArguments:
		for _, change := range args.Changes {
			size += change.Bytes
		}
	case ledger.ArtifactReadArguments:
		_, err := s.artifactAuthority(ctx, sub.Origin, args.Artifact, sub.GrantID, s.p.through, nil)
		return err
	}
	chain, err := s.authorize(ctx, sub.Origin, sub.GrantID, operation, targets, recipients, size, s.p.through)
	if err != nil {
		return err
	}
	if args, ok := sub.Arguments.(ledger.TaskCreateArguments); ok && !audienceContained(args.Audience, chain[0].recipients) {
		return ErrDenied
	}
	return nil
}

func (s *Service) refreshScopedLocked(ctx context.Context, sub ledger.ScopedSubmission) error {
	if err := s.usableLocked(ctx); err != nil {
		return err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return err
	}
	return s.scopedAuthority(ctx, sub)
}

// Like the owner private path, this returns with mu held. The authoritative old
// prefix is scanned without mu; refresh and a tail scan close duplicate races.
func (s *Service) lookupScoped(ctx context.Context, input scopedInput) (commandMatch, error) {
	s.mu.Lock()
	if err := s.refreshScopedLocked(ctx, input.sub); err != nil {
		return commandMatch{}, err
	}
	through := s.p.through
	old := bytes.Compare(input.id[:], s.p.maxCommand[:]) <= 0
	s.mu.Unlock()
	if s.deps.beforeLookup != nil {
		s.deps.beforeLookup()
	}
	var match commandMatch
	var err error
	if old {
		match, err = s.findCommand(ctx, input.id, 0, through)
	}
	s.mu.Lock()
	if err != nil {
		return match, err
	}
	if err := s.refreshScopedLocked(ctx, input.sub); err != nil {
		return match, err
	}
	if !match.found && through < s.p.through {
		match, err = s.findCommand(ctx, input.id, through, s.p.through)
	}
	return match, err
}

func scopedReceipt(g *scopedGroup) ScopedReceipt {
	return scopedReceiptAt(g.receipt, g.first+uint64(g.receiptIndex))
}

func scopedReceiptAt(p ledger.RuntimeScopedReceipt, sequence uint64) ScopedReceipt {
	sub := p.Submission
	target := sub.Target
	target.Slots = slices.Clone(target.Slots)
	return ScopedReceipt{CommandID: p.CommandID, SubmissionSHA256: p.SubmissionSHA256, Actor: sub.Actor, Origin: sub.Origin, Operation: sub.Operation, Target: target, GrantID: sub.GrantID, Outcome: p.Outcome, Version: p.Version, WorkID: p.WorkID, Sequence: sequence}
}

func matchScopedReceipt(match commandMatch, input scopedInput) (ScopedReceipt, error) {
	if match.scoped == nil {
		return ScopedReceipt{}, ErrCommandMismatch
	}
	sub := match.scoped.receipt.Submission
	if sub.Actor != input.sub.Actor || sub.Origin != input.sub.Origin {
		return ScopedReceipt{}, ErrDenied
	}
	if sub.Operation != input.sub.Operation || match.command.SubmissionSHA256 != input.digest {
		return ScopedReceipt{}, ErrCommandMismatch
	}
	return scopedReceipt(match.scoped), nil
}

func (s *Service) scopedProducerGuards(input scopedInput) error {
	switch args := input.sub.Arguments.(type) {
	case ledger.GrantIssueArguments:
		if args.Lifetime != "until_revoked" {
			return ErrInvalid
		}
		if len(s.p.grants) >= s.limits.MaxLiveGrants {
			return &BusyError{Reason: "live_grants"}
		}
		if args.DelegationDepth > s.limits.MaxDelegationDepth || len(args.Operations) > s.limits.MaxGrantOperations || len(args.Resources) > s.limits.MaxGrantResources || len(args.Recipients) > s.limits.MaxGrantRecipients {
			return ErrTooLarge
		}
	case ledger.TaskCreateArguments:
		// Audience registration is a new-producer guard, not a frozen replay
		// rule. Agent containment has already refused unauthorized recipients.
		for _, agent := range args.Audience {
			if _, found := s.p.agents[agent]; !found {
				return ErrNotBound
			}
		}
		live := 0
		for _, task := range s.p.tasks {
			if !terminalTask(task.record.State) {
				live++
			}
		}
		if live >= s.limits.MaxLiveTasks {
			return &BusyError{Reason: "live_tasks"}
		}
		if args.Description.Bytes > s.limits.MaxTaskDescriptionBytes || len(args.Audience) > s.limits.MaxTaskAudience || len(args.Dependencies) > s.limits.MaxTaskDependencies {
			return ErrTooLarge
		}
	case ledger.CapacityArguments:
		if args.Scope.Kind == "agent" && args.Limits.TurnSlots > s.limits.MaxAgentTurnSlots {
			return ErrTooLarge
		}
	case ledger.LocalAdmissionArguments:
		if args.MaxBytes > s.limits.MaxScopedBytes {
			return ErrTooLarge
		}
		pending := 0
		for _, work := range s.p.work {
			if work.admission.Kind == "local" && !work.released {
				pending++
			}
		}
		if pending >= s.limits.MaxPendingLocalWork {
			return &BusyError{Reason: "pending_local_work"}
		}
	case ledger.PrivateWriteArguments:
		total := int64(0)
		for _, change := range args.Changes {
			if change.Bytes > s.limits.MaxObjectBytes {
				return ErrTooLarge
			}
			total += change.Bytes
		}
		if total > s.limits.MaxOperationBytes || total > s.limits.MaxScopedBytes {
			return ErrTooLarge
		}
	case ledger.PrivateReadArguments:
		if args.Read.MaxBytes > s.limits.MaxScopedBytes || len(input.selection.heads) > s.limits.MaxSelectedObjects {
			return ErrTooLarge
		}
	case ledger.ArtifactReadArguments:
		if args.MaxBytes > s.limits.MaxScopedBytes {
			return ErrTooLarge
		}
	}
	return nil
}

func (s *Service) scopedDrafts(ctx context.Context, input scopedInput) ([]ledger.Draft, error) {
	sub := input.sub
	var drafts []ledger.Draft
	appendPayload := func(typ string, actor ledger.Actor, payload []byte, refs ...ledger.Digest) {
		drafts = append(drafts, ledger.Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, TurnID: input.turn, OperationID: input.id, BodyRefs: refs, Payload: payload})
	}
	command, err := (ledger.Command{CommandID: input.id, SubmissionSHA256: input.digest}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeCommand, ledger.Actor(sub.Actor), command)
	var version, size int64
	var chain []grantRules
	var workID uuid.UUID
	var usage ledger.RuntimeLocalUsed
	outcome := "applied"
	charge := sub.Actor == "agent" && sub.Operation != "grant.revoke"
	switch args := sub.Arguments.(type) {
	case ledger.GrantIssueArguments:
		version = *sub.Expected.AuthorityVersion + 1
		p := ledger.RuntimeGrantIssued{GrantID: args.GrantID, ExpectedAuthorityVersion: *sub.Expected.AuthorityVersion, AuthorityVersion: version, Grantee: args.Grantee, ParentGrantID: args.ParentGrantID, Operations: args.Operations, Resources: args.Resources, DataClass: args.DataClass, Recipients: args.Recipients, Limits: args.Limits, DelegationDepth: args.DelegationDepth, Lifetime: args.Lifetime, TaskBinding: args.TaskBinding}
		payload, err := p.Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeGrantIssued, ledger.Actor(sub.Actor), payload)
		if sub.Actor == "agent" {
			chain, err = s.currentChain(ctx, sub.Origin, sub.GrantID, s.p.through)
			if err != nil {
				return nil, err
			}
		}
	case ledger.GrantRevokeArguments:
		version = *sub.Expected.AuthorityVersion + 1
		payload, err := (ledger.RuntimeGrantRevoked{GrantID: args.GrantID, ExpectedAuthorityVersion: *sub.Expected.AuthorityVersion, AuthorityVersion: version}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeGrantRevoked, ledger.Actor(sub.Actor), payload)
	case ledger.CapacityArguments:
		version = sub.Expected.Version + 1
		payload, err := (ledger.RuntimeCapacityConfigured{Scope: args.Scope, ExpectedVersion: sub.Expected.Version, Version: version, Limits: args.Limits}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeCapacityConfigured, ledger.ActorOwner, payload)
	case ledger.TaskCreateArguments, ledger.TaskMutationArguments:
		plan, taskChain, taskErr := s.deriveTask(ctx, sub, s.p.through+1)
		if taskErr != nil {
			return nil, taskErr
		}
		chain = taskChain
		task := plan.task.record
		version = task.Version
		if task.Action == "create" {
			size = task.Description.Bytes
			payload, err := (ledger.BodyPin{PinID: task.TaskID}).Encode()
			if err != nil {
				return nil, err
			}
			appendPayload(ledger.TypeBodyPin, ledger.ActorCore, payload, task.Description.SHA256)
		}
		payload, err := task.Encode()
		if err != nil {
			return nil, err
		}
		var refs []ledger.Digest
		if !terminalTask(task.State) {
			refs = []ledger.Digest{task.Description.SHA256}
		}
		appendPayload(ledger.TypeRuntimeTaskChanged, ledger.Actor(sub.Actor), payload, refs...)
		if terminalTask(task.State) {
			payload, err := (ledger.BodyUnpin{PinID: task.TaskID}).Encode()
			if err != nil {
				return nil, err
			}
			appendPayload(ledger.TypeBodyUnpin, ledger.ActorCore, payload)
		}
	case ledger.TurnStartArguments, ledger.LocalAdmissionArguments:
		chain, err = s.currentChain(ctx, sub.Origin, sub.GrantID, s.p.through)
		if err != nil {
			return nil, err
		}
		scopes, err := chainScopes(sub.Origin, chain)
		if err != nil {
			return nil, err
		}
		if len(scopes) > s.limits.MaxBudgetScopes {
			return nil, ErrTooLarge
		}
		admission := ledger.RuntimeWorkAdmitted{Version: 1, AuthorityVersion: s.p.authority, GrantID: sub.GrantID, Scopes: scopes, Target: sub.Target}
		switch args := args.(type) {
		case ledger.TurnStartArguments:
			admission.WorkID, admission.Kind, admission.Operation, admission.TaskBinding = args.TurnID, "turn", "turn.start", args.TaskBinding
		case ledger.LocalAdmissionArguments:
			admission.WorkID, admission.Kind, admission.Operation, admission.TaskBinding = args.WorkID, "local", args.LocalOperation, args.TaskBinding
			admission.ParentTurnID, admission.ArgumentsSHA256 = args.ParentTurnID, args.ArgumentsSHA256
			admission.ToolSchema, admission.MaxBytes = &args.ToolSchema, &args.MaxBytes
		}
		payload, err := admission.Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeWorkAdmitted, ledger.ActorCore, payload)
		workID, version, outcome, charge = admission.WorkID, 1, "admitted", false
	case ledger.WorkDispatchArguments:
		version, workID, outcome, charge = sub.Expected.Version+1, args.WorkID, "dispatched", false
		payload, err := (ledger.RuntimeWorkDispatched{WorkID: args.WorkID, ExpectedVersion: sub.Expected.Version, Version: version, AuthorityVersion: s.p.authority, GrantID: sub.GrantID, ArgumentsSHA256: args.ArgumentsSHA256}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeWorkDispatched, ledger.ActorCore, payload)
	case ledger.WorkEndArguments:
		version, workID, outcome, charge = sub.Expected.Version+1, args.WorkID, "recorded", false
		payload, err := (ledger.RuntimeWorkEnded{WorkID: args.WorkID, ExpectedVersion: sub.Expected.Version, Version: version, Outcome: args.Outcome}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeWorkEnded, ledger.ActorCore, payload)
	case ledger.PrivateWriteArguments:
		chain, err = s.currentChain(ctx, sub.Origin, sub.GrantID, s.p.through)
		if err != nil {
			return nil, err
		}
		ids := submissionIDs{sub.Origin.AgentID, sub.Origin.SessionID, sub.Origin.ContextID}
		state := s.p.scopes[keyFor(ids, sub.Target.Scope)]
		if state == nil {
			return nil, ErrNotBound
		}
		changes := slices.Clone(args.Changes)
		for i := range changes {
			size += changes[i].Bytes
			if changes[i].Slot == "transcript" {
				changes[i].PreviousSHA256 = state.heads["transcript"].SHA256
			}
		}
		version = sub.Expected.Version + 1
		payload, err := (ledger.PrivateScopedRevision{Scope: sub.Target.Scope, ExpectedVersion: sub.Expected.Version, Revision: version, Changes: changes}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypePrivateScopedRevision, ledger.ActorCore, payload)
	case ledger.PrivateReadArguments:
		chain, err = s.currentChain(ctx, sub.Origin, sub.GrantID, s.p.through)
		if err != nil {
			return nil, err
		}
		size = input.selection.bytes
		usage.Read, usage.Selected = &args.Read, input.selection.selectedObjects()
		if input.selection.nextCursor != 0 {
			next := int64(input.selection.nextCursor)
			usage.NextCursor = &next
		}
	case ledger.ArtifactReadArguments:
		chain, err = s.artifactAuthority(ctx, sub.Origin, args.Artifact, sub.GrantID, s.p.through, nil)
		if err != nil {
			return nil, err
		}
		size, usage.Artifact = args.Artifact.Bytes, &args.Artifact
	default:
		return nil, ErrInvalid
	}
	if charge {
		scopes, err := chainScopes(sub.Origin, chain)
		if err != nil {
			return nil, err
		}
		if len(scopes) > s.limits.MaxBudgetScopes {
			return nil, ErrTooLarge
		}
		usage.AuthorityVersion, usage.GrantID, usage.Scopes, usage.Units, usage.Bytes = s.p.authority, sub.GrantID, scopes, 1, size
		payload, err := usage.Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeLocalUsed, ledger.ActorCore, payload)
	}
	payload, err := (ledger.RuntimeScopedReceipt{CommandID: input.id, SubmissionSHA256: input.digest, Submission: sub, Outcome: outcome, Version: version, WorkID: workID}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeRuntimeScopedReceipt, ledger.Actor(sub.Actor), payload)
	return drafts, nil
}

func (s *Service) preflightScoped(ctx context.Context, input scopedInput, drafts []ledger.Draft) (*scopedGroup, error) {
	private, group, review, paid, registration, loop, err := decodeFamily(drafts, s.p.through+1)
	if err != nil {
		return nil, err
	}
	if private != nil || review != nil || paid != nil || registration != nil || loop != nil || group == nil || group.command.CommandID != input.id || group.command.SubmissionSHA256 != input.digest || group.receipt.Submission.Actor != input.sub.Actor || group.receipt.Submission.Origin != input.sub.Origin {
		return nil, ErrInvalid
	}
	if _, err := s.planScoped(ctx, group, s.p.through+1); err != nil {
		return nil, err
	}
	return group, nil
}

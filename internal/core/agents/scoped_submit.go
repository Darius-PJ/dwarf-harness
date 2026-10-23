package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func (s *Service) scopedAuthority(ctx context.Context, sub ledger.ScopedSubmission) error {
	if !s.p.registeredOrigin(sub.Origin) {
		return ErrNotBound
	}
	if sub.Actor == "owner" {
		return nil
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
	case ledger.ArtifactReadArguments:
		_, err := s.artifactAuthority(ctx, sub.Origin, args.Artifact, sub.GrantID, s.p.through)
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

// Like the owner private path, this returns with mu held. The authoritative old
// prefix is scanned without mu; refresh and a tail scan close duplicate races.
func (s *Service) lookupScoped(ctx context.Context, input scopedInput) (commandMatch, error) {
	s.mu.Lock()
	if err := s.usableLocked(ctx); err != nil {
		return commandMatch{}, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return commandMatch{}, err
	}
	if err := s.scopedAuthority(ctx, input.sub); err != nil {
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
	if err := s.usableLocked(ctx); err != nil {
		return match, err
	}
	if err := s.refreshLocked(ctx, s.store.Durable()); err != nil {
		return match, err
	}
	if err := s.scopedAuthority(ctx, input.sub); err != nil {
		return match, err
	}
	if !match.found && through < s.p.through {
		match, err = s.findCommand(ctx, input.id, through, s.p.through)
	}
	return match, err
}

func scopedReceipt(g *scopedGroup) ScopedReceipt {
	p, sub := g.receipt, g.receipt.Submission
	target := sub.Target
	target.Slots = slices.Clone(target.Slots)
	return ScopedReceipt{CommandID: p.CommandID, SubmissionSHA256: p.SubmissionSHA256, Actor: sub.Actor, Origin: sub.Origin, Operation: sub.Operation, Target: target, GrantID: sub.GrantID, Outcome: p.Outcome, Version: p.Version, WorkID: p.WorkID, Sequence: g.first + uint64(g.receiptIndex)}
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
	}
	return nil
}

func (s *Service) scopedDrafts(ctx context.Context, input scopedInput) ([]ledger.Draft, error) {
	sub := input.sub
	var drafts []ledger.Draft
	appendPayload := func(typ string, actor ledger.Actor, payload []byte, refs ...ledger.Digest) {
		drafts = append(drafts, ledger.Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: sub.Origin.AgentID, ExecutionSessionID: sub.Origin.SessionID, ContextID: sub.Origin.ContextID, OperationID: input.id, BodyRefs: refs, Payload: payload})
	}
	command, err := (ledger.Command{CommandID: input.id, SubmissionSHA256: input.digest}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeCommand, ledger.Actor(sub.Actor), command)
	var version, size int64
	var chain []grantRules
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
	default:
		return nil, ErrInvalid
	}
	if sub.Actor == "agent" && sub.Operation != "grant.revoke" {
		scopes, err := chainScopes(sub.Origin, chain)
		if err != nil {
			return nil, err
		}
		if len(scopes) > s.limits.MaxBudgetScopes {
			return nil, ErrTooLarge
		}
		payload, err := (ledger.RuntimeLocalUsed{AuthorityVersion: s.p.authority, GrantID: sub.GrantID, Scopes: scopes, Units: 1, Bytes: size}).Encode()
		if err != nil {
			return nil, err
		}
		appendPayload(ledger.TypeRuntimeLocalUsed, ledger.ActorCore, payload)
	}
	payload, err := (ledger.RuntimeScopedReceipt{CommandID: input.id, SubmissionSHA256: input.digest, Submission: sub, Outcome: "applied", Version: version}).Encode()
	if err != nil {
		return nil, err
	}
	appendPayload(ledger.TypeRuntimeScopedReceipt, ledger.Actor(sub.Actor), payload)
	return drafts, nil
}

func (s *Service) preflightScoped(ctx context.Context, input scopedInput, drafts []ledger.Draft) (*scopedGroup, error) {
	private, group, err := decodeFamily(drafts, s.p.through+1)
	if err != nil {
		return nil, err
	}
	if private != nil || group == nil || group.command.CommandID != input.id || group.command.SubmissionSHA256 != input.digest || group.receipt.Submission.Actor != input.sub.Actor || group.receipt.Submission.Origin != input.sub.Origin {
		return nil, ErrInvalid
	}
	if _, err := s.planScoped(ctx, group, s.p.through+1); err != nil {
		return nil, err
	}
	return group, nil
}

func (s *Service) executeScoped(ctx context.Context, caller scopedCaller, operation string, request any) (ScopedReceipt, error) {
	if s == nil {
		return ScopedReceipt{}, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return ScopedReceipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, caller.binding, false)
	if err != nil {
		return ScopedReceipt{}, err
	}
	if caller.actor == "agent" && caller.fixed != ids {
		return ScopedReceipt{}, ErrNotBound
	}
	input, err := makeScopedInput(caller.actor, ids, operation, request)
	if err != nil {
		return ScopedReceipt{}, err
	}
	s.mu.Lock()
	err = s.usableLocked(ctx)
	if err == nil {
		err = s.refreshLocked(ctx, s.store.Durable())
	}
	if err == nil {
		err = s.resolveScopedTarget(ctx, &input.sub)
	}
	s.mu.Unlock()
	if err != nil {
		return ScopedReceipt{}, err
	}
	canonical, digest, err := encodeScopedSubmission(input.sub)
	if err != nil {
		return ScopedReceipt{}, err
	}
	input.sub, err = ledger.DecodeScopedSubmission(canonical)
	if err != nil {
		return ScopedReceipt{}, err
	}
	input.digest = digest
	match, err := s.lookupScoped(ctx, input)
	if err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	if match.found {
		result, err := matchScopedReceipt(match, input)
		s.mu.Unlock()
		return result, err
	}
	if err := s.scopedProducerGuards(input); err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	drafts, err := s.scopedDrafts(ctx, input)
	if err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	group, err := s.preflightScoped(ctx, input, drafts)
	if err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	body := operation == "task.create"
	if body && s.preparing >= s.limits.MaxConcurrentWrites {
		s.mu.Unlock()
		return ScopedReceipt{}, &BusyError{}
	}
	var scopes []ledger.ScopeKey
	var size int64
	if group.usage != nil {
		scopes, size = group.usage.Scopes, group.usage.Bytes
		if err := s.p.book.reservePreparation(input.sub.Origin, scopes, size); err != nil {
			s.mu.Unlock()
			return ScopedReceipt{}, err
		}
	}
	if body {
		s.preparing++
	}
	reserved := true
	release := func() {
		if !reserved {
			return
		}
		if len(scopes) != 0 {
			s.p.book.releasePreparation(input.sub.Origin, scopes, size)
		}
		if body {
			s.preparing--
		}
		reserved = false
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); release(); s.mu.Unlock() }()
	if body {
		retained := bytes.Clone(input.body)
		description := input.sub.Arguments.(ledger.TaskCreateArguments).Description
		if int64(len(retained)) != description.Bytes || ledger.Digest(sha256.Sum256(retained)) != description.SHA256 {
			return ScopedReceipt{}, ErrInvalid
		}
		if s.deps.beforePublish != nil {
			if err := s.deps.beforePublish(caller.binding, "task"); err != nil {
				return ScopedReceipt{}, err
			}
		}
		digest, size, err := s.store.PublishBody(ctx, bytes.NewReader(retained))
		if err != nil {
			return ScopedReceipt{}, err
		}
		if digest != description.SHA256 || size != description.Bytes {
			return ScopedReceipt{}, ErrInconsistent
		}
		if s.deps.afterPublish != nil {
			s.deps.afterPublish(caller.binding, "task")
		}
	}
	match, err = s.lookupScoped(ctx, input)
	if err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	if match.found {
		result, err := matchScopedReceipt(match, input)
		release()
		s.mu.Unlock()
		return result, err
	}
	if err := s.scopedProducerGuards(input); err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	drafts, err = s.scopedDrafts(ctx, input)
	if err == nil && s.deps.beforeSubmit != nil {
		drafts = s.deps.beforeSubmit(drafts)
	}
	if err == nil {
		_, err = s.preflightScoped(ctx, input, drafts)
	}
	if err == nil && len(scopes) != 0 {
		err = s.p.book.checkPreparation(scopes, size)
	}
	if err == nil {
		err = s.usableLocked(ctx)
	}
	if err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	committed, err := s.store.Submit(ctx, drafts[0], drafts[1:])
	if err != nil {
		if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) {
			s.poisoned = true
		}
		s.mu.Unlock()
		if errors.Is(err, ledgerstore.ErrCommandMismatch) {
			return ScopedReceipt{}, ErrCommandMismatch
		}
		return ScopedReceipt{}, err
	}
	if err := s.refreshLocked(context.Background(), max(s.p.through, committed.Last)); err != nil {
		s.mu.Unlock()
		return ScopedReceipt{}, err
	}
	actual, err := s.findCommand(context.Background(), input.id, committed.First-1, committed.Last)
	if err != nil || !actual.found {
		s.poisoned = true
		s.mu.Unlock()
		if err == nil {
			err = ErrInconsistent
		}
		return ScopedReceipt{}, err
	}
	result, err := matchScopedReceipt(actual, input)
	release()
	s.mu.Unlock()
	if err != nil {
		return ScopedReceipt{}, err
	}
	if !committed.Duplicate && s.deps.afterScopedCommit != nil {
		if err := s.deps.afterScopedCommit(result); err != nil {
			return ScopedReceipt{}, err
		}
	}
	return result, nil
}

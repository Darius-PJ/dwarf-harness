package agents

import (
	"context"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) currentChain(ctx context.Context, origin ledger.Origin, id uuid.UUID, through uint64) ([]grantRules, error) {
	if !s.p.registeredOrigin(origin) {
		return nil, ErrNotBound
	}
	var chain []grantRules
	seen := map[uuid.UUID]bool{}
	for id != (uuid.UUID{}) {
		if seen[id] {
			return nil, ErrDenied
		}
		seen[id] = true
		grant, found, err := s.loadGrant(ctx, id, through)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrDenied
		}
		rule := grant.rules()
		if !validGrant(rule) || rule.revoked {
			return nil, ErrDenied
		}
		if len(chain) == 0 && rule.grantee != bindingOf(origin) {
			return nil, ErrDenied
		}
		if rule.task != (ledger.TaskBinding{}) {
			task, found, err := s.loadTask(ctx, rule.task.TaskID, through)
			if err != nil {
				return nil, err
			}
			if !found || task.record.Assignment == nil || task.record.Assignment.AssignmentID != rule.task.AssignmentID || task.record.Assignment.Generation != rule.task.Generation {
				return nil, ErrDenied
			}
		}
		if len(chain) > 0 && !grantAttenuates(rule, chain[len(chain)-1]) {
			return nil, ErrDenied
		}
		chain = append(chain, rule)
		id = rule.parent
	}
	if len(chain) == 0 {
		return nil, ErrDenied
	}
	return chain, nil
}

func (s *Service) authorize(ctx context.Context, origin ledger.Origin, grant uuid.UUID, operation string, targets []ledger.Resource, recipients []Binding, size int64, through uint64) ([]grantRules, error) {
	chain, err := s.currentChain(ctx, origin, grant, through)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, ErrDenied
	}
	request := authorityRequest{grant: grant, origin: bindingOf(origin), operation: operation, dataClass: resourceClass(targets[0].Kind), targets: targets, recipients: recipients, bytes: size, task: chain[0].task}
	if !chainAuthorizes(chain, request) {
		return nil, ErrDenied
	}
	if operation != "private.read" && operation != "context.inspect" && operation != "turn.inspect" {
		task, bound, err := s.contextTask(ctx, origin, through)
		if err != nil {
			return nil, err
		}
		if bound && (task.record.Assignment == nil || task.record.Assignment.Assignee.ContextID != origin.ContextID) {
			return nil, ErrDenied
		}
	}
	return chain, nil
}

func chainScopes(origin ledger.Origin, chain []grantRules) ([]ledger.ScopeKey, error) {
	ids := make([]uuid.UUID, len(chain))
	for i, grant := range chain {
		ids[i] = grant.id
	}
	return budgetScopes(bindingOf(origin), ids)
}

func (s *Service) canInspectTask(ctx context.Context, origin ledger.Origin, id uuid.UUID, through uint64) (bool, error) {
	for grantID, grant := range s.p.grants {
		if grant.definition.Grantee != ledger.Binding(bindingOf(origin)) || grant.definition.DataClass != "task" {
			continue
		}
		_, err := s.authorize(ctx, origin, grantID, "task.inspect", []ledger.Resource{{Kind: "task", TaskID: id}}, nil, 0, through)
		if err == ErrDenied {
			continue
		}
		if err != nil {
			return false, err
		}
		task, found, err := s.loadTask(ctx, id, through)
		return found && slices.Contains(task.record.Audience, origin.AgentID), err
	}
	return false, nil
}

// A subject check is an intersection with mutation authority, never a union of
// partial grants. Each candidate independently authorizes the complete read.
// The artifact's source names its task: the source is resolved once, at the
// first task resource the caller may inspect, and only a resource naming that
// task loads the task.
func (s *Service) artifactAuthority(ctx context.Context, origin ledger.Origin, artifact ledger.ArtifactRef, selected uuid.UUID, through uint64, sources map[int64]ledger.Record) ([]grantRules, error) {
	var definition ledger.RuntimeTaskChanged // zero TaskID: the source names no task
	resolved := false
	for grantID, grant := range s.p.grants {
		if selected != (uuid.UUID{}) && grantID != selected || grant.definition.Grantee != ledger.Binding(bindingOf(origin)) {
			continue
		}
		if grant.definition.DataClass == "artifact" {
			chain, err := s.authorize(ctx, origin, grantID, "artifact.read", []ledger.Resource{{Kind: "artifact", Artifact: artifact}}, nil, artifact.Bytes, through)
			if err == nil {
				return chain, nil
			}
			if err != ErrDenied {
				return nil, err
			}
		}
		if grant.definition.DataClass != "task" {
			continue
		}
		for _, resource := range grant.definition.Resources {
			if resource.Kind != "task" || resource.TaskID == (uuid.UUID{}) {
				continue
			}
			chain, err := s.authorize(ctx, origin, grantID, "task.inspect", []ledger.Resource{resource}, nil, artifact.Bytes, through)
			if err == ErrDenied {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !resolved {
				resolved = true
				source, found := sources[artifact.SourceSequence]
				if sources == nil {
					if source, found, err = s.sourceRecord(ctx, artifact.SourceSequence, through); err != nil {
						return nil, err
					}
				}
				if found && source.Type == ledger.TypeRuntimeTaskChanged {
					if definition, err = ledger.DecodeRuntimeTaskChanged(source.Payload); err != nil {
						return nil, err
					}
				}
			}
			if definition.TaskID != resource.TaskID {
				continue
			}
			task, found, err := s.loadTask(ctx, resource.TaskID, through)
			if err != nil {
				return nil, err
			}
			if !found || !slices.Contains(task.record.Audience, origin.AgentID) || task.record.Description.SHA256 != artifact.SHA256 || task.record.Description.Bytes != artifact.Bytes {
				continue
			}
			if definition.Description == task.record.Description {
				return chain, nil
			}
		}
	}
	return nil, ErrDenied
}

func (s *Service) subjectAuthorized(ctx context.Context, actor string, origin ledger.Origin, subject *ledger.SubjectRef, through uint64) error {
	if subject == nil {
		return nil
	}
	if !validTaskSubject(*subject) {
		return ErrDenied
	}
	if actor != "owner" {
		if _, err := s.artifactAuthority(ctx, origin, subject.Artifact, uuid.UUID{}, through, nil); err != nil {
			return err
		}
	}
	return s.verifyArtifactSource(ctx, subject.Artifact, through)
}

func grantArguments(p ledger.RuntimeGrantIssued) ledger.GrantIssueArguments {
	return ledger.GrantIssueArguments{GrantID: p.GrantID, Grantee: p.Grantee, ParentGrantID: p.ParentGrantID, Operations: p.Operations, Resources: p.Resources, DataClass: p.DataClass, Recipients: p.Recipients, Limits: p.Limits, DelegationDepth: p.DelegationDepth, Lifetime: p.Lifetime, TaskBinding: p.TaskBinding}
}

func (s *Service) planGrant(ctx context.Context, g *scopedGroup, first uint64) (scopedPlan, []grantRules, error) {
	sub := g.receipt.Submission
	plan := scopedPlan{authority: s.p.authority}
	if sub.Expected.AuthorityVersion == nil {
		return plan, nil, ErrInvalid
	}
	if *sub.Expected.AuthorityVersion != s.p.authority || s.p.authority == math.MaxInt64 {
		return plan, nil, &RevisionError{Expected: uint64(*sub.Expected.AuthorityVersion), Current: uint64(s.p.authority)}
	}
	through := first - 1
	var chain []grantRules
	var err error
	if g.issue != nil {
		p := *g.issue
		if p.ExpectedAuthorityVersion != s.p.authority || p.AuthorityVersion != s.p.authority+1 || !sameScopedValue(grantArguments(p), sub.Arguments) || !sameScopedValue(sub.Target, p.Resources[0]) {
			return plan, nil, ErrInvalid
		}
		if _, found, err := s.loadGrant(ctx, p.GrantID, through); err != nil {
			return plan, nil, err
		} else if found {
			return plan, nil, ErrInvalid
		}
		if s.p.contexts[p.Grantee.ContextID] != p.Grantee.AgentID {
			return plan, nil, ErrNotBound
		}
		for _, recipient := range p.Recipients {
			if s.p.contexts[recipient.ContextID] != recipient.AgentID {
				return plan, nil, ErrNotBound
			}
		}
		for _, resource := range p.Resources {
			registered, err := s.registeredResource(ctx, resource, through)
			if err != nil {
				return plan, nil, err
			}
			if !registered {
				return plan, nil, ErrUnavailable
			}
		}
		if p.TaskBinding != nil {
			task, found, err := s.loadTask(ctx, p.TaskBinding.TaskID, through)
			if err != nil {
				return plan, nil, err
			}
			if !found || task.record.Assignment == nil || task.record.Assignment.AssignmentID != p.TaskBinding.AssignmentID || task.record.Assignment.Generation != p.TaskBinding.Generation {
				return plan, nil, ErrDenied
			}
			plan.dependencies = append(plan.dependencies, task)
		}
		grant := grantState{definition: p, issuer: sub.Actor, origin: sub.Origin, sequence: first + uint64(g.effectIndex)}
		if sub.Actor == "owner" {
			if p.ParentGrantID != (uuid.UUID{}) || sub.GrantID != (uuid.UUID{}) {
				return plan, nil, ErrInvalid
			}
		} else {
			if p.ParentGrantID != sub.GrantID {
				return plan, nil, ErrDenied
			}
			chain, err = s.authorize(ctx, sub.Origin, sub.GrantID, "grant.issue", p.Resources, grant.rules().recipients, 0, through)
			if err != nil {
				return plan, nil, err
			}
			if !grantAttenuates(chain[0], grant.rules()) {
				return plan, nil, ErrDenied
			}
		}
		plan.grant = &grant
		plan.budget = map[ledger.ScopeKey]budgetAccount{{Kind: "grant", ID: p.GrantID}: {version: 1, configured: true, limits: p.Limits}}
	} else {
		p := *g.revoke
		if p.ExpectedAuthorityVersion != s.p.authority || p.AuthorityVersion != s.p.authority+1 || !sameScopedValue(sub.Arguments, ledger.GrantRevokeArguments{GrantID: p.GrantID}) {
			return plan, nil, ErrInvalid
		}
		grant, found, err := s.loadGrant(ctx, p.GrantID, through)
		if err != nil {
			return plan, nil, err
		}
		if !found {
			return plan, nil, ErrUnavailable
		}
		if grant.revoked {
			return plan, nil, ErrAlreadyRevoked
		}
		if !sameScopedValue(sub.Target, grant.definition.Resources[0]) {
			return plan, nil, ErrInvalid
		}
		if sub.Actor == "agent" {
			if grant.issuer != "agent" || grant.origin != sub.Origin {
				return plan, nil, ErrDenied
			}
			chain, err = s.authorize(ctx, sub.Origin, sub.GrantID, "grant.revoke", grant.definition.Resources, grant.rules().recipients, 0, through)
			if err != nil {
				return plan, nil, err
			}
		} else if sub.GrantID != (uuid.UUID{}) {
			return plan, nil, ErrInvalid
		}
		grant.revoked = true
		plan.grant = &grant
	}
	plan.authority++
	if g.receipt.Version != plan.authority {
		return plan, nil, ErrInvalid
	}
	return plan, chain, nil
}

func (s *Service) planConfiguration(g *scopedGroup) (scopedPlan, error) {
	plan := scopedPlan{authority: s.p.authority}
	p, sub := *g.config, g.receipt.Submission
	account := s.p.book.accounts[p.Scope]
	if account.version != p.ExpectedVersion {
		return plan, &RevisionError{Expected: uint64(p.ExpectedVersion), Current: uint64(account.version)}
	}
	context := sub.Origin.ContextID
	if p.Scope.Kind == "agent" {
		if _, ok := s.p.agents[p.Scope.ID]; !ok {
			return plan, ErrNotBound
		}
	} else if p.Scope.Kind == "context" {
		if _, ok := s.p.contexts[p.Scope.ID]; !ok {
			return plan, ErrNotBound
		}
		context = p.Scope.ID
	}
	owner := s.p.contexts[context]
	target := ledger.Resource{Kind: "context", Binding: ledger.Binding{AgentID: owner, ContextID: context}, Scope: "context", Slots: []string{}}
	if sub.GrantID != (uuid.UUID{}) || !sameScopedValue(sub.Target, target) || sub.Expected.Version != account.version || !sameScopedValue(sub.Arguments, ledger.CapacityArguments{Scope: p.Scope, Limits: p.Limits}) || g.receipt.Version != p.Version {
		return plan, ErrInvalid
	}
	account.configured, account.version, account.limits = true, p.Version, p.Limits
	plan.budget = map[ledger.ScopeKey]budgetAccount{p.Scope: account}
	return plan, nil
}

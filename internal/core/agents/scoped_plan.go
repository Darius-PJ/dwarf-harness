package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) planScoped(ctx context.Context, g *scopedGroup, first uint64) (scopedPlan, error) {
	plan := scopedPlan{authority: s.p.authority}
	sub := g.receipt.Submission
	if !s.p.registeredOrigin(sub.Origin) {
		return plan, ErrNotBound
	}
	var chain []grantRules
	var size int64
	var err error
	switch {
	case g.issue != nil || g.revoke != nil:
		plan, chain, err = s.planGrant(ctx, g, first)
	case g.config != nil:
		plan, err = s.planConfiguration(g)
	case g.task != nil:
		plan, chain, err = s.planTask(ctx, g, first)
		if args, ok := sub.Arguments.(ledger.TaskCreateArguments); ok {
			size = args.Description.Bytes
		}
	case g.admission != nil || g.dispatch != nil || g.ending != nil:
		plan, err = s.planWork(ctx, g, first)
	default:
		plan, chain, size, err = s.planScopedPrivate(ctx, g, first)
	}
	if err != nil {
		return scopedPlan{}, err
	}
	if plan.work != nil {
		plan.work.receipt = scopedReceipt(g)
	}
	if g.usage != nil {
		usage := *g.usage
		scopes, err := chainScopes(sub.Origin, chain)
		if err != nil {
			return scopedPlan{}, err
		}
		if usage.AuthorityVersion != s.p.authority || usage.GrantID != sub.GrantID || !sameScopedValue(usage.Scopes, scopes) || usage.Units != 1 || usage.Bytes != size {
			return scopedPlan{}, ErrInvalid
		}
		if sub.Operation != "private.read" && sub.Operation != "artifact.read" && (usage.Read != nil || usage.Artifact != nil || usage.Selected != nil || usage.NextCursor != nil) {
			return scopedPlan{}, ErrInvalid
		}
		next, err := s.p.book.atomic(scopes, size)
		if err != nil {
			return scopedPlan{}, err
		}
		if plan.budget == nil {
			plan.budget = next
		} else {
			for scope, account := range next {
				plan.budget[scope] = account
			}
		}
	}
	return plan, nil
}

func (p *projection) installScoped(plan scopedPlan) {
	p.authority = plan.authority
	if plan.grant != nil {
		p.grants[plan.grant.definition.GrantID] = *plan.grant
	}
	if plan.task != nil {
		p.tasks[plan.task.record.TaskID] = *plan.task
		// Every direct task transition advances the version. A previous review
		// obligation can never transfer to that new version or assignment.
		delete(p.reviews, plan.task.record.TaskID)
	}
	for _, task := range plan.dependencies {
		p.tasks[task.record.TaskID] = task
	}
	if plan.work != nil {
		p.work[plan.work.admission.WorkID] = *plan.work
	}
	p.book.install(plan.budget)
	for key, state := range plan.private {
		p.scopes[key] = state
	}
	p.pruneScoped()
}

// Retain authority and unresolved exposure, not a lifetime terminal index.
// Historical lookups use bounded hints, then the ledger on a miss.
func (p *projection) pruneScoped() {
	live := map[uuid.UUID]bool{}
	visited := map[uuid.UUID]bool{}
	var current func(uuid.UUID) bool
	current = func(id uuid.UUID) bool {
		if visited[id] {
			return live[id]
		}
		visited[id] = true
		grant, exists := p.grants[id]
		if !exists {
			return false
		}
		rule := grant.rules()
		if !validGrant(rule) || rule.revoked {
			return false
		}
		if rule.task != (ledger.TaskBinding{}) {
			task, exists := p.tasks[rule.task.TaskID]
			if !exists || task.record.Assignment == nil || task.record.Assignment.AssignmentID != rule.task.AssignmentID || task.record.Assignment.Generation != rule.task.Generation {
				return false
			}
		}
		if rule.parent != (uuid.UUID{}) && (!current(rule.parent) || !grantAttenuates(p.grants[rule.parent].rules(), rule)) {
			return false
		}
		live[id] = true
		return true
	}
	for id := range p.grants {
		current(id)
	}
	for id := range p.grants {
		if !live[id] && p.book.paid[ledger.PaidScope{Kind: "grant", ID: id}].version == 0 {
			delete(p.grants, id)
		}
	}
	neededTasks := map[uuid.UUID]bool{}
	neededScopes := map[ledger.ScopeKey]bool{}
	for id, work := range p.work {
		if work.released {
			delete(p.work, id)
			continue
		}
		for _, scope := range work.admission.Scopes {
			neededScopes[scope] = true
		}
		if work.admission.TaskBinding != nil {
			neededTasks[work.admission.TaskBinding.TaskID] = true
		}
	}
	for _, grant := range p.grants {
		if grant.definition.TaskBinding != nil {
			neededTasks[grant.definition.TaskBinding.TaskID] = true
		}
	}
	for _, task := range p.tasks {
		if !terminalTask(task.record.State) {
			for _, id := range task.record.Dependencies {
				neededTasks[id] = true
			}
		}
	}
	for id, task := range p.tasks {
		if terminalTask(task.record.State) && !neededTasks[id] {
			delete(p.tasks, id)
		}
	}
	for scope := range p.book.accounts {
		if scope.Kind == "grant" && !live[scope.ID] && !neededScopes[scope] && p.book.paid[ledger.PaidScope{Kind: "grant", ID: scope.ID}].version == 0 {
			delete(p.book.accounts, scope)
		}
	}
}

func scopedRevisions(g *privateGroup, scoped *scopedGroup, loop *loopGroup) (submissionIDs, []ledger.PrivateRevision, []int) {
	if g != nil {
		return g.ids, g.revisions, g.revisionIndexes
	}
	if scoped != nil && scoped.revision != nil {
		p := *scoped.revision
		return scoped.ids, []ledger.PrivateRevision{{Scope: p.Scope, ExpectedVersion: p.ExpectedVersion, Revision: p.Revision, Changes: slices.Clone(p.Changes)}}, []int{scoped.effectIndex}
	}
	if loop != nil && loop.revision != nil {
		p := *loop.revision
		return loop.ids, []ledger.PrivateRevision{{Scope: p.Scope, ExpectedVersion: p.ExpectedVersion, Revision: p.Revision, Changes: slices.Clone(p.Changes)}}, []int{1}
	}
	return submissionIDs{}, nil, nil
}

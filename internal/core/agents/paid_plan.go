package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var paidOutcomes = []string{"configured", "bound", "revoked", "delegated", "reserved", "sent", "released_unsent", "unknown", "settled", "no_charge", "corrected", "acknowledged"}

func paidReceiptEffect(g *paidGroup, version int64, attempt uuid.UUID, outcome string) error {
	if g.receipt.Version != version || g.receipt.AttemptID != attempt || slices.Contains(paidOutcomes, g.receipt.Outcome) && g.receipt.Outcome != outcome {
		return ErrInvalid
	}
	return nil
}

// planPaid is shared by replay and subsequent paid producers. Nothing is
// installed until all identities, authority, arithmetic and receipt agree.
func (s *Service) planPaid(ctx context.Context, g *paidGroup, first uint64) (paidPlan, error) {
	if !s.p.registeredOrigin(originOf(g.ids)) {
		return paidPlan{}, ErrNotBound
	}
	switch {
	case g.route != nil:
		return s.planPaidRoute(g)
	case g.credential != nil:
		return s.planPaidCredential(ctx, g, first-1)
	case g.spend != nil:
		return s.planPaidSpend(ctx, g, first-1)
	default:
		return s.planPaidAttempt(ctx, g, first)
	}
}

func (s *Service) planPaidRoute(g *paidGroup) (paidPlan, error) {
	p, sub := *g.route, g.receipt.Submission
	prior := s.p.paidRoutes[p.RouteID]
	digest, err := p.Profile.SHA256()
	if err != nil {
		return paidPlan{}, err
	}
	tariff, err := p.Profile.TariffSHA256()
	if err != nil {
		return paidPlan{}, err
	}
	if prior.Version != p.ExpectedVersion || !sameScopedValue(sub.Arguments, ledger.PaidRouteArguments{RouteID: p.RouteID, ExpectedVersion: p.ExpectedVersion, Enabled: p.Enabled, CredentialScopeID: p.CredentialScopeID, ProfileSHA256: digest}) {
		return paidPlan{}, ErrInvalid
	}
	if prior.Version != 0 && !p.Enabled && (prior.CredentialScopeID != p.CredentialScopeID || !sameScopedValue(prior.Profile, p.Profile)) {
		return paidPlan{}, ErrInvalid
	}
	if old, ok := s.p.paidCapabilities[p.Profile.CapabilityVersion]; ok && old != digest {
		return paidPlan{}, ErrInvalid
	}
	if old, ok := s.p.paidTariffs[p.Profile.TariffVersion]; ok && old != tariff {
		return paidPlan{}, ErrInvalid
	}
	if scope, ok := s.p.paidCredentialScopes[p.CredentialScopeID]; ok && scope.provider != p.Profile.Provider {
		return paidPlan{}, ErrInvalid
	}
	if p.Enabled && (s.p.retiredProfiles[digest] || p.ObservedUTCNS < s.p.reviewFloor) {
		return paidPlan{}, ErrInvalid
	}
	if err := paidReceiptEffect(g, p.Version, uuid.UUID{}, "configured"); err != nil {
		return paidPlan{}, err
	}
	return paidPlan{route: &p, observed: p.ObservedUTCNS}, nil
}

func (s *Service) planPaidCredential(ctx context.Context, g *paidGroup, through uint64) (paidPlan, error) {
	p := *g.credential
	scope := s.p.paidCredentialScopes[p.ScopeID]
	if p.ExpectedVersion != scope.version || scope.version != 0 && scope.provider != p.Provider {
		return paidPlan{}, ErrInvalid
	}
	for _, route := range s.p.paidRoutes {
		if route.CredentialScopeID == p.ScopeID && route.Profile.Provider != p.Provider {
			return paidPlan{}, ErrInvalid
		}
	}
	outcome := "revoked"
	if p.Action == "bind" {
		if g.receipt.Submission.Operation != "paid.credential.bind" || !sameScopedValue(g.receipt.Submission.Arguments, ledger.PaidCredentialBindArguments{ScopeID: p.ScopeID, ExpectedVersion: p.ExpectedVersion, Provider: p.Provider, CredentialVersion: p.CredentialVersion, SourceKind: p.SourceKind, SourceLocator: p.SourceLocator, RevokePreviousVersion: p.RevokePreviousVersion}) {
			return paidPlan{}, ErrInvalid
		}
		exists, err := s.credentialVersionExists(ctx, p.CredentialVersion, through)
		if err != nil {
			return paidPlan{}, err
		}
		if exists {
			return paidPlan{}, ErrInvalid
		}
		if p.RevokePreviousVersion != (uuid.UUID{}) {
			old, ok := s.p.paidCredentials[p.RevokePreviousVersion]
			if !ok || old.ScopeID != p.ScopeID {
				return paidPlan{}, ErrInvalid
			}
		}
		outcome = "bound"
	} else {
		old, ok := s.p.paidCredentials[p.CredentialVersion]
		if !ok || old.ScopeID != p.ScopeID || g.receipt.Submission.Operation != "paid.credential.revoke" || !sameScopedValue(g.receipt.Submission.Arguments, ledger.PaidCredentialRevokeArguments{ScopeID: p.ScopeID, ExpectedVersion: p.ExpectedVersion, Provider: p.Provider, CredentialVersion: p.CredentialVersion}) {
			return paidPlan{}, ErrInvalid
		}
	}
	if err := paidReceiptEffect(g, p.Version, uuid.UUID{}, outcome); err != nil {
		return paidPlan{}, err
	}
	return paidPlan{credential: &p}, nil
}

func (s *Service) planPaidSpend(ctx context.Context, g *paidGroup, through uint64) (paidPlan, error) {
	p, sub := *g.spend, g.receipt.Submission
	plan := paidPlan{spend: &p}
	a := s.p.book.paid[p.Scope]
	if p.AuthorityVersion != s.p.authority || p.ExpectedVersion != a.version || !sameScopedValue(sub.Arguments, ledger.PaidSpendArguments{Scope: p.Scope, ExpectedVersion: p.ExpectedVersion, Terms: p.Terms, ParentGrantID: p.ParentGrantID}) {
		return plan, ErrInvalid
	}
	kind := s.p.book.paidPeriodKind
	if kind == "" && p.Scope.Kind != "colony" || kind != "" && p.Terms.PeriodKind != kind {
		return plan, ErrInvalid
	}
	outcome := "configured"
	switch p.Scope.Kind {
	case "agent":
		if _, ok := s.p.agents[p.Scope.ID]; !ok {
			return plan, ErrNotBound
		}
	case "credential":
		if _, ok := s.p.paidCredentialScopes[p.Scope.ID]; !ok {
			return plan, ErrNotBound
		}
	case "task":
		task, found, err := s.loadTask(ctx, p.Scope.ID, through)
		if err != nil {
			return plan, err
		}
		if !found {
			return plan, ErrNotBound
		}
		plan.tasks = append(plan.tasks, task)
	case "grant":
		grant, found, err := s.loadGrant(ctx, p.Scope.ID, through)
		if err != nil {
			return plan, err
		}
		if !found || grant.definition.ParentGrantID != p.ParentGrantID {
			return plan, ErrDenied
		}
		plan.grants = append(plan.grants, grant)
		if p.ParentGrantID != (uuid.UUID{}) {
			parent := s.p.book.paid[ledger.PaidScope{Kind: "grant", ID: p.ParentGrantID}]
			if parent.version == 0 || !paidTermsAttenuate(parent.terms, p.Terms) {
				return plan, ErrDenied
			}
		}
		if sub.Actor == "agent" {
			if grant.issuer != "agent" || grant.origin != sub.Origin || sub.GrantID != p.ParentGrantID {
				return plan, ErrDenied
			}
			chain, err := s.authorize(ctx, sub.Origin, sub.GrantID, "grant.issue", grant.definition.Resources, grant.rules().recipients, 0, through)
			if err != nil {
				return plan, err
			}
			for _, link := range chain {
				terms := s.p.book.paid[ledger.PaidScope{Kind: "grant", ID: link.id}]
				if terms.version == 0 || !paidTermsAttenuate(terms.terms, p.Terms) {
					return plan, ErrDenied
				}
			}
			scopes, err := chainScopes(sub.Origin, chain)
			if err != nil {
				return plan, err
			}
			u := g.receipt.Usage
			if u == nil || u.AuthorityVersion != s.p.authority || u.GrantID != sub.GrantID || !slices.Equal(u.Scopes, scopes) {
				return plan, ErrInvalid
			}
			plan.local, err = s.p.book.atomic(scopes, 0)
			if err != nil {
				return plan, err
			}
			outcome = "delegated"
		}
	}
	if sub.Actor == "agent" && p.Scope.Kind != "grant" {
		return plan, ErrDenied
	}
	if a.version == 0 && p.Scope.Kind == "task" {
		settled, held, _, err := s.paidHistoryTotal(ctx, p.Scope, s.p.book.paidPeriod, nil, through)
		if err != nil {
			return plan, err
		}
		a.settled, a.held, a.period = settled, held, s.p.book.paidPeriod
	}
	a.version, a.terms = p.Version, p.Terms
	plan.paid = map[ledger.PaidScope]paidAccount{p.Scope: a}
	if err := paidReceiptEffect(g, p.Version, uuid.UUID{}, outcome); err != nil {
		return plan, err
	}
	return plan, nil
}

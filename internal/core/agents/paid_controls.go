package agents

import (
	"context"
	"errors"
	"math"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/credentials"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func spendTerms(v SpendTermsInput) (ledger.SpendTerms, error) {
	t := ledger.SpendTerms{Enabled: v.Enabled, Mode: v.Mode, PeriodKind: v.PeriodKind}
	if v.WarningPPM != nil {
		t.WarningPPM = new(*v.WarningPPM)
	}
	if v.Mode == "finite" {
		n, err := providers.ParseMoney(v.USD)
		if err != nil {
			return t, err
		}
		t.CeilingMicros = &n
	} else if v.Mode != "unlimited" || v.USD != "" {
		return t, ErrInvalid
	}
	return t, nil
}
func (s *Service) ConfigurePaidRoute(ctx context.Context, origin Binding, r PaidRouteConfiguration) (PaidReceipt, error) {
	return s.paidControl(ctx, scopedCaller{actor: "owner", binding: origin}, r)
}
func (s *Service) BindCredential(ctx context.Context, origin Binding, r CredentialBinding) (PaidReceipt, error) {
	return s.paidControl(ctx, scopedCaller{actor: "owner", binding: origin}, r)
}
func (s *Service) RevokeCredential(ctx context.Context, origin Binding, r CredentialRevocation) (PaidReceipt, error) {
	return s.paidControl(ctx, scopedCaller{actor: "owner", binding: origin}, r)
}
func (s *Service) ConfigureSpend(ctx context.Context, origin Binding, r SpendConfiguration) (PaidReceipt, error) {
	return s.paidControl(ctx, scopedCaller{actor: "owner", binding: origin}, r)
}
func (a *Agent) DelegateSpend(ctx context.Context, r SpendDelegation) (PaidReceipt, error) {
	if a == nil || a.service == nil {
		return PaidReceipt{}, ErrNotBound
	}
	return a.service.paidControl(ctx, scopedCaller{actor: "agent", binding: Binding{a.ids.agent, a.ids.context}, fixed: a.ids}, r)
}

type paidControlInput struct {
	id, grant  uuid.UUID
	sub        ledger.PaidSubmission
	route      *ledger.RuntimePaidRouteConfigured
	credential *ledger.RuntimeCredentialChanged
	spend      *ledger.RuntimeSpendConfigured
}

func controlInput(request any) (paidControlInput, error) {
	var in paidControlInput
	var expected int64
	switch r := request.(type) {
	case PaidRouteConfiguration:
		in.id, in.grant, expected = r.CommandID, r.GrantID, r.ExpectedVersion
		// Decode historical shape first so a matched retry has no current-profile
		// or expiry dependency. Strict installation runs only for a new command.
		p, err := ledger.DecodePaidProfile(r.Profile)
		if err != nil {
			return in, err
		}
		digest, err := p.SHA256()
		if err != nil {
			return in, err
		}
		in.route = &ledger.RuntimePaidRouteConfigured{RouteID: r.RouteID, ExpectedVersion: expected, Version: expected + 1, Enabled: r.Enabled, CredentialScopeID: r.CredentialScopeID, Profile: p}
		in.sub.Operation, in.sub.Arguments = "paid.route.configure", ledger.PaidRouteArguments{RouteID: r.RouteID, ExpectedVersion: expected, Enabled: r.Enabled, CredentialScopeID: r.CredentialScopeID, ProfileSHA256: digest}
	case CredentialBinding:
		in.id, in.grant, expected = r.CommandID, r.GrantID, r.ExpectedVersion
		in.credential = &ledger.RuntimeCredentialChanged{ScopeID: r.ScopeID, Provider: r.Provider, ExpectedVersion: expected, Version: expected + 1, Action: "bind", CredentialVersion: r.CredentialVersion, SourceKind: r.SourceKind, SourceLocator: r.SourceLocator, RevokePreviousVersion: r.RevokePreviousVersion}
		in.sub.Operation, in.sub.Arguments = "paid.credential.bind", ledger.PaidCredentialBindArguments{ScopeID: r.ScopeID, ExpectedVersion: expected, Provider: r.Provider, CredentialVersion: r.CredentialVersion, SourceKind: r.SourceKind, SourceLocator: r.SourceLocator, RevokePreviousVersion: r.RevokePreviousVersion}
	case CredentialRevocation:
		in.id, in.grant, expected = r.CommandID, r.GrantID, r.ExpectedVersion
		in.credential = &ledger.RuntimeCredentialChanged{ScopeID: r.ScopeID, Provider: r.Provider, ExpectedVersion: expected, Version: expected + 1, Action: "revoke", CredentialVersion: r.CredentialVersion}
		in.sub.Operation, in.sub.Arguments = "paid.credential.revoke", ledger.PaidCredentialRevokeArguments{ScopeID: r.ScopeID, ExpectedVersion: expected, Provider: r.Provider, CredentialVersion: r.CredentialVersion}
	case SpendConfiguration:
		in.id, in.grant, expected = r.CommandID, r.GrantID, r.ExpectedVersion
		terms, err := spendTerms(r.Terms)
		if err != nil {
			return in, err
		}
		in.spend = &ledger.RuntimeSpendConfigured{Scope: r.Scope, ExpectedVersion: expected, Version: expected + 1, Terms: terms, ParentGrantID: r.ParentGrantID}
		in.sub.Operation, in.sub.Arguments = "paid.spend.configure", ledger.PaidSpendArguments{Scope: r.Scope, ExpectedVersion: expected, Terms: terms, ParentGrantID: r.ParentGrantID}
	case SpendDelegation:
		in.id, in.grant, expected = r.CommandID, r.GrantID, r.ExpectedVersion
		terms, err := spendTerms(r.Terms)
		if err != nil {
			return in, err
		}
		scope := ledger.PaidScope{Kind: "grant", ID: r.ChildGrantID}
		in.spend = &ledger.RuntimeSpendConfigured{Scope: scope, ExpectedVersion: expected, Version: expected + 1, Terms: terms, ParentGrantID: r.GrantID}
		in.sub.Operation, in.sub.Arguments = "paid.spend.delegate", ledger.PaidSpendArguments{Scope: scope, ExpectedVersion: expected, Terms: terms, ParentGrantID: r.GrantID}
	default:
		return in, ErrInvalid
	}
	if !validID(in.id) || expected < 0 || expected == math.MaxInt64 {
		return in, ErrInvalid
	}
	return in, nil
}

func (s *Service) controlAuthority(ctx context.Context, in paidControlInput) ([]grantRules, error) {
	if s.paid == nil {
		return nil, ErrUnavailable
	}
	if !s.p.registeredOrigin(in.sub.Origin) {
		return nil, ErrNotBound
	}
	if in.sub.Actor == "owner" {
		return nil, nil
	}
	p := in.spend
	grant, found, err := s.loadGrant(ctx, p.Scope.ID, s.p.through)
	if err != nil {
		return nil, err
	}
	if !found || grant.issuer != "agent" || grant.origin != in.sub.Origin || grant.definition.ParentGrantID != in.grant {
		return nil, ErrDenied
	}
	chain, err := s.authorize(ctx, in.sub.Origin, in.grant, "grant.issue", grant.definition.Resources, grant.rules().recipients, 0, s.p.through)
	if err != nil {
		return nil, err
	}
	for _, link := range chain {
		a := s.p.book.paid[ledger.PaidScope{Kind: "grant", ID: link.id}]
		if a.version == 0 || !a.terms.Enabled {
			return nil, ErrDenied
		}
	}
	return chain, nil
}
func (s *Service) paidControl(ctx context.Context, caller scopedCaller, request any) (PaidReceipt, error) {
	in, err := controlInput(request)
	if err != nil {
		return PaidReceipt{}, err
	}
	if caller.actor == "owner" && in.grant != (uuid.UUID{}) {
		return PaidReceipt{}, ErrInvalid
	}
	if s == nil {
		return PaidReceipt{}, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return PaidReceipt{}, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, caller.binding, false)
	if err != nil {
		return PaidReceipt{}, err
	}
	if caller.actor == "agent" && ids != caller.fixed {
		return PaidReceipt{}, ErrNotBound
	}
	in.sub.Schema, in.sub.Actor, in.sub.Origin = 4, caller.actor, originOf(ids)
	if caller.actor == "agent" {
		in.sub.GrantID = in.grant
	}
	digest, err := paidDigest(in.sub)
	if err != nil {
		return PaidReceipt{}, err
	}
	authority := func() error { _, err := s.controlAuthority(ctx, in); return err }
	match, err := s.lookupPaid(ctx, in.id, authority)
	if err == nil && match.found {
		r, err := s.matchPaid(match, in.sub, digest)
		s.mu.Unlock()
		return r, err
	}
	if err == nil {
		err = s.controlGuardsLocked(ctx, in, request)
	}
	if err != nil {
		s.mu.Unlock()
		return PaidReceipt{}, err
	}
	var secret *credentials.Secret
	if p := in.credential; p != nil && p.Action == "bind" {
		// Never hold the service lock across a native source read.
		s.mu.Unlock()
		var source credentials.Source
		switch p.SourceKind {
		case "environment":
			source = credentials.Environment(p.SourceLocator)
		case "windows_credential_manager":
			source = credentials.Native(p.SourceLocator)
		}
		secret, err = source.Bind()
		if err != nil {
			return PaidReceipt{}, err
		}
		defer func() {
			if secret != nil {
				secret.Release()
			}
		}()
		match, err = s.lookupPaid(ctx, in.id, authority)
		if err == nil && match.found {
			r, err := s.matchPaid(match, in.sub, digest)
			s.mu.Unlock()
			return r, err
		}
		if err == nil {
			err = s.controlGuardsLocked(ctx, in, request)
		}
	}
	var retiredSecret *credentials.Secret
	defer func() {
		s.mu.Unlock()
		// A dispatch borrows the secret before taking mu. Never wait for that
		// borrow under mu: revocation is already durable and bars new intents.
		if retiredSecret != nil {
			retiredSecret.Release()
		}
	}()
	if err != nil {
		return PaidReceipt{}, err
	}
	// Only an enabled route configuration carries a clock sample, which
	// another writer's later commit time would raise the review floor above;
	// it is written conditionally, and a refusal at a moved tail re-enters at
	// the refresh and samples again. Nothing was written.
	conditional := in.route != nil && in.route.Enabled
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			match, err := s.relookupPaidLocked(ctx, in.id, authority)
			if err == nil && match.found {
				return s.matchPaid(match, in.sub, digest)
			}
			if err == nil {
				err = s.controlGuardsLocked(ctx, in, request)
			}
			if err != nil {
				return PaidReceipt{}, err
			}
		}
		var effect paidEncoding
		var typ, outcome string
		var version int64
		var usage *ledger.PaidLocalUsage
		switch {
		case in.route != nil:
			p := in.route
			if p.Enabled {
				sample, e := s.samplePaidLocked()
				if e != nil {
					return PaidReceipt{}, e
				}
				if e = providerProfile(p.Profile).Current(sample); e != nil {
					return PaidReceipt{}, e
				}
				p.ObservedUTCNS = sample
			}
			effect, typ, outcome, version = *p, ledger.TypeRuntimePaidRouteConfigured, "configured", p.Version
		case in.credential != nil:
			p := in.credential
			outcome = "revoked"
			if p.Action == "bind" {
				outcome = "bound"
			}
			effect, typ, version = *p, ledger.TypeRuntimeCredentialChanged, p.Version
		case in.spend != nil:
			p := in.spend
			p.AuthorityVersion = s.p.authority
			if p.Terms.Enabled {
				if _, err := s.samplePaidLocked(); err != nil {
					return PaidReceipt{}, err
				}
			}
			outcome = "configured"
			if caller.actor == "agent" {
				chain, e := s.controlAuthority(ctx, in)
				if e != nil {
					return PaidReceipt{}, e
				}
				scopes, e := chainScopes(in.sub.Origin, chain)
				if e != nil {
					return PaidReceipt{}, e
				}
				if len(scopes) > s.limits.MaxBudgetScopes {
					return PaidReceipt{}, ErrTooLarge
				}
				usage = &ledger.PaidLocalUsage{AuthorityVersion: s.p.authority, GrantID: in.grant, Scopes: scopes, Units: 1}
				if e = s.p.book.checkPreparation(scopes, budgetPreparation{hold: [4]int64{0, 1, 0, 1}}, true); e != nil {
					return PaidReceipt{}, e
				}
				outcome = "delegated"
			}
			effect, typ, version = *p, ledger.TypeRuntimeSpendConfigured, p.Version
		}
		drafts, err := paidDrafts(in.id, in.sub, effect, typ, outcome, version, uuid.UUID{}, uuid.UUID{}, usage)
		if err != nil {
			return PaidReceipt{}, err
		}
		receipt, err := s.submitPaidGroupLocked(ctx, drafts, conditional)
		if errors.Is(err, ledgerstore.ErrTailMoved) {
			if attempt == tailAttempts {
				return PaidReceipt{}, &BusyError{Reason: "ledger_tail"}
			}
			continue
		}
		if err != nil {
			return PaidReceipt{}, err
		}
		if p := in.credential; p != nil {
			if secret != nil {
				s.paid.secrets[p.CredentialVersion], secret = secret, nil
			}
			revoked := p.RevokePreviousVersion
			if p.Action == "revoke" {
				revoked = p.CredentialVersion
			}
			if old := s.paid.secrets[revoked]; old != nil {
				retiredSecret = old
				delete(s.paid.secrets, revoked)
			}
		}
		return receipt, nil
	}
}

func (s *Service) controlGuardsLocked(ctx context.Context, in paidControlInput, request any) error {
	l := s.paid.limits
	if p := in.route; p != nil {
		prior := s.p.paidRoutes[p.RouteID]
		if prior.Version != p.ExpectedVersion {
			return ErrRevision
		}
		if p.Enabled {
			if prior.Version == 0 && len(s.p.paidRoutes) >= l.MaxPaidRoutes {
				return &BusyError{Reason: "paid_routes"}
			}
			raw := request.(PaidRouteConfiguration).Profile
			if len(raw) > l.MaxProfileBytes {
				return ErrTooLarge
			}
			profile, err := providers.ParseProfile(raw)
			if err != nil {
				return err
			}
			if err = s.paidDestination(profile); err != nil {
				return err
			}
			sample, err := s.samplePaidLocked()
			if err != nil {
				return err
			}
			if err = profile.Current(sample); err != nil {
				return err
			}
			p.ObservedUTCNS = sample
		} else if prior.Version == 0 {
			return ErrNoEffect
		}
	}
	if p := in.credential; p != nil {
		if p.ExpectedVersion != s.p.paidCredentialScopes[p.ScopeID].version {
			return ErrRevision
		}
		if p.Provider != "anthropic" && p.Provider != "openai" {
			return providers.ErrUnsupported
		}
		if p.Action == "bind" {
			if p.SourceKind != "environment" && p.SourceKind != "windows_credential_manager" {
				return credentials.ErrUnavailable
			}
			if p.SourceKind == "environment" && !paidEnvironmentName(p.SourceLocator) {
				return credentials.ErrInvalid
			}
			if _, err := p.Encode(); err != nil {
				return ErrInvalid
			}
			if _, found := s.p.paidCredentialScopes[p.ScopeID]; !found && len(s.p.paidCredentialScopes) >= l.MaxCredentialScopes {
				return &BusyError{Reason: "credential_scopes"}
			}
			n := len(s.p.paidCredentials)
			if p.RevokePreviousVersion != (uuid.UUID{}) {
				n--
			}
			if n >= l.MaxCredentialVersions {
				return &BusyError{Reason: "credential_versions"}
			}
		}
	}
	if p := in.spend; p != nil {
		if p.ExpectedVersion != s.p.book.paid[p.Scope].version {
			return ErrRevision
		}
		if kind := s.p.book.paidPeriodKind; kind == "" && p.Scope.Kind != "colony" || kind != "" && kind != p.Terms.PeriodKind {
			return ErrPaidPeriod
		}
		if s.p.book.paid[p.Scope].version == 0 {
			n := 0
			for _, a := range s.p.book.paid {
				if a.version != 0 {
					n++
				}
			}
			if n >= l.MaxPaidEnrolledScopes {
				return &BusyError{Reason: "paid_scopes"}
			}
		}
		if p.Terms.Enabled {
			if _, err := s.samplePaidLocked(); err != nil {
				return err
			}
		}
	}
	return nil
}
func paidEnvironmentName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i, c := range []byte(name) {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

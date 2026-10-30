package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// requestStorageBound walks caller-owned values without copying them. Six bytes
// per byte covers JSON escaping; structural allowances cover even empty values.
// Its bounded sum precedes all serialization and ownership copies.
func requestStorageBound(r providers.Request) (int64, error) {
	n := int64(1024)
	add := func(size int) bool {
		if size > providers.RequestLimit || int64(size) > (6*providers.RequestLimit-n)/6 {
			return false
		}
		n += int64(size) * 6
		return true
	}
	cache := func(c *providers.CacheControl) bool { return c == nil || add(len(c.Type)+len(c.TTL)+128) }
	text := func(t providers.Text) bool { return add(len(t.Type)+len(t.Text)+128) && cache(t.CacheControl) }
	if r.ToolChoice != nil && !add(len(r.ToolChoice.Type)+128) {
		return 0, ErrTooLarge
	}
	if len(r.Messages) > providers.RequestLimit/128 || len(r.System) > providers.RequestLimit/128 || len(r.Tools) > providers.RequestLimit/128 {
		return 0, ErrTooLarge
	}
	for _, t := range r.System {
		if !text(t) {
			return 0, ErrTooLarge
		}
	}
	for _, t := range r.Tools {
		if !add(len(t.Name)+len(t.Description)+len(t.InputSchema)+256) || !cache(t.CacheControl) {
			return 0, ErrTooLarge
		}
	}
	for _, m := range r.Messages {
		if !add(len(m.Role)+128) || len(m.Content) > providers.RequestLimit/128 {
			return 0, ErrTooLarge
		}
		for _, b := range m.Content {
			if !add(len(b.Type)+len(b.Text)+len(b.ID)+len(b.Name)+len(b.Input)+len(b.ToolUseID)+len(b.Replay)+256) || !cache(b.CacheControl) || len(b.Content) > providers.RequestLimit/128 {
				return 0, ErrTooLarge
			}
			for _, t := range b.Content {
				if !text(t) {
					return 0, ErrTooLarge
				}
			}
		}
	}
	return n, nil
}

func (s *Service) livePaidAuthority(ctx context.Context, p *preparedPaid) ([]grantRules, error) {
	if s.paid == nil {
		return nil, ErrUnavailable
	}
	chain, _, err := s.paidAuthority(ctx, paidAttempt{origin: p.origin, admission: p.admission}, s.p.through)
	if err == nil && s.paid.secrets[p.admission.Request.Credential.Version] == nil {
		err = ErrUnavailable
	}
	return chain, err
}

func (s *Service) PrepareAnthropic(ctx context.Context, origin Binding, input AnthropicInput) (*PreparedPaidCall, error) {
	return s.preparePaid(ctx, origin, input, "anthropic")
}

func (s *Service) PrepareOpenAI(ctx context.Context, origin Binding, input OpenAIInput) (*PreparedPaidCall, error) {
	return s.preparePaid(ctx, origin, AnthropicInput(input), "openai")
}

func (s *Service) preparePaid(ctx context.Context, origin Binding, input AnthropicInput, provider string) (*PreparedPaidCall, error) {
	if s == nil {
		return nil, ErrNotBound
	}
	if !validID(input.AttemptID) || !validID(input.GrantID) || !validID(input.ParentTurnID) || !validID(input.RouteID) || !validID(input.CredentialVersion) || input.ExpectedRouteVersion <= 0 {
		return nil, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, origin, false)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if err = s.paidUsableLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	route := s.p.paidRoutes[input.RouteID]
	digest, _ := route.Profile.SHA256()
	p := &preparedPaid{origin: originOf(ids)}
	p.admission = ledger.RuntimePaidReserved{AttemptID: input.AttemptID, Version: 1, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, RouteID: input.RouteID, RouteVersion: input.ExpectedRouteVersion, ProfileSHA256: digest}
	if input.TaskBinding != nil {
		p.admission.TaskBinding = new(*input.TaskBinding)
	}
	p.admission.Request = ledger.SealedDescriptor{Provider: route.Profile.Provider, Credential: ledger.CredentialRef{ScopeID: route.CredentialScopeID, Version: input.CredentialVersion}}
	chain, err := s.authorize(ctx, p.origin, input.GrantID, "model.call", []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + input.RouteID.String()}}, nil, 0, s.p.through)
	if err == nil {
		p.admission.Scopes = paidScopes(p.origin, route.CredentialScopeID, p.admission.TaskBinding, chain)
		_, err = s.livePaidAuthority(ctx, p)
	}
	var sample int64
	if err == nil {
		sample, err = s.samplePaidLocked()
	}
	if err == nil {
		err = providerProfile(route.Profile).Current(sample)
	}
	if err == nil && route.Profile.Provider != provider {
		err = providers.ErrUnsupported
	}
	if err == nil {
		err = s.paidDestination(providerProfile(route.Profile))
	}
	if err == nil && (len(s.paid.prepared) >= s.paid.limits.MaxPreparedPaid || len(p.admission.Scopes) > s.paid.limits.MaxPaidScopes) {
		err = &BusyError{Reason: "paid_preparing"}
	}
	var bound int64
	if err == nil {
		bound, err = requestStorageBound(input.Request)
	}
	var scopes []ledger.ScopeKey
	if err == nil {
		scopes, err = chainScopes(p.origin, chain)
	}
	if err == nil && len(scopes) > s.limits.MaxBudgetScopes {
		err = ErrTooLarge
	}
	p.reservation.origin = p.origin
	if err == nil {
		err = s.reserveScopedProcessLocked(&p.reservation)
	}
	if err == nil {
		// Compilation, sealing, framed hashing and publication may coexist with
		// copies of this input. Also reserve one 1-MiB event, the bounded 8-MiB
		// output and one 1-MiB publication chunk before retaining a call.
		preparation := budgetPreparation{hold: [4]int64{0, 0, 20*bound + (10 << 20), 0}}
		err = s.p.book.reservePreparation(p.origin, scopes, preparation)
		if err == nil {
			p.reservation.scopes, p.reservation.budget = scopes, preparation
			s.paid.prepared[p] = true
		}
	}
	if err != nil {
		s.releaseScopedLocked(&p.reservation)
		s.mu.Unlock()
		return nil, err
	}
	secret := s.paid.secrets[input.CredentialVersion]
	s.mu.Unlock()

	profile := providerProfile(route.Profile)
	var compiled providers.Compiled
	raw, err := json.Marshal(input.Request)
	if err == nil {
		err = secret.WithBytes(func(key []byte) error {
			if paidContainsSecret(raw, key) {
				return ErrInvalid
			}
			return nil
		})
	}
	if err == nil {
		switch provider {
		case "anthropic":
			compiled, err = providers.CompileAnthropic(profile, input.Request)
		case "openai":
			compiled, err = providers.CompileOpenAI(profile, input.Request)
		default:
			err = providers.ErrUnsupported
		}
	}
	var envelope providers.Envelope
	if err == nil {
		switch provider {
		case "anthropic":
			envelope, err = providers.Seal(profile, compiled, route.CredentialScopeID.String(), input.CredentialVersion.String(), []providers.Header{{Name: "accept", Value: "text/event-stream"}, {Name: "anthropic-version", Value: profile.APIVersion}, {Name: "content-type", Value: "application/json"}})
		case "openai":
			envelope, err = providers.Seal(profile, compiled, route.CredentialScopeID.String(), input.CredentialVersion.String(), []providers.Header{{Name: "accept", Value: "text/event-stream"}, {Name: "content-type", Value: "application/json"}})
		default:
			err = providers.ErrUnsupported
		}
	}
	var body []byte
	if err == nil {
		body = envelope.Body()
		if int64(len(body)) > s.paid.limits.MaxRequestBytes || int64(len(body)) > s.limits.MaxObjectBytes {
			err = ErrTooLarge
		}
	}
	var components []providers.Component
	var maximum int64
	if err == nil {
		components, maximum, err = providers.Bound(profile, compiled.OutputMaximum(), compiled.InputClasses())
	}
	var tariff, quote [32]byte
	if err == nil {
		tariff, err = profile.TariffSHA256()
	}
	if err == nil {
		quote, err = providers.QuoteSHA256(envelope.SHA256(), profile.CapabilityVersion, profile.TariffVersion, tariff, maximum, components)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = s.paidUsableLocked(ctx)
	}
	if err == nil {
		_, err = s.livePaidAuthority(ctx, p)
	}
	if err == nil {
		size := int64(len(body))
		p.reservation.budget.operationBytes = &size
		err = s.p.book.checkPreparation(scopes, p.reservation.budget, false)
	}
	if err != nil {
		s.releasePaidPreparation(p)
		return nil, err
	}
	p.envelope, p.body, p.compiled = envelope, body, compiled
	d := envelope.Descriptor()
	p.admission.Request = ledger.SealedDescriptor{Provider: d.Provider, Authority: d.Authority, Model: d.Model, Region: d.Region, Method: d.Method, Path: d.Path, RequestedTier: d.RequestedTier, Credential: p.admission.Request.Credential, BodySHA256: ledger.Digest(sha256.Sum256(body)), BodyBytes: int64(len(body))}
	for _, h := range d.Headers {
		p.admission.Request.Headers = append(p.admission.Request.Headers, ledger.SemanticHeader{Name: h.Name, Value: h.Value})
	}
	p.admission.InputObject = ledger.ObjectRef{SHA256: p.admission.Request.BodySHA256, Bytes: p.admission.Request.BodyBytes}
	p.admission.Quote = ledger.CostBound{Currency: "USD", MaximumMicros: maximum, RequestSHA256: ledger.Digest(envelope.SHA256()), CapabilityVersion: profile.CapabilityVersion, TariffVersion: profile.TariffVersion, TariffSHA256: ledger.Digest(tariff), QuoteSHA256: ledger.Digest(quote)}
	for _, c := range components {
		p.admission.Quote.Components = append(p.admission.Quote.Components, ledger.PricedDimension{Classes: c.Classes, MaxUnits: c.MaxUnits, RateNumerator: c.Numerator, RateDenominator: c.Denominator})
	}
	return &PreparedPaidCall{service: s, state: p}, nil
}

func paidAdmissionMatches(p *preparedPaid, ids submissionIDs, request PaidAdmission) bool {
	a := p.admission
	return p.origin == originOf(ids) && a.AttemptID == request.AttemptID && a.GrantID == request.GrantID && a.ParentTurnID == request.ParentTurnID && sameScopedValue(a.TaskBinding, request.TaskBinding)
}

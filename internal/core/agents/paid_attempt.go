package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"math/big"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func providerProfile(p ledger.Profile) providers.Profile {
	raw, _ := json.Marshal(p)
	var result providers.Profile
	_ = json.Unmarshal(raw, &result)
	return result
}
func paidComponents(q ledger.CostBound) []providers.Component {
	result := make([]providers.Component, len(q.Components))
	for i, c := range q.Components {
		result[i] = providers.Component{Classes: c.Classes, MaxUnits: c.MaxUnits, Numerator: c.RateNumerator, Denominator: c.RateDenominator}
	}
	return result
}
func paidAdmitArguments(p ledger.RuntimePaidReserved) ledger.PaidAdmitArguments {
	return ledger.PaidAdmitArguments{AttemptID: p.AttemptID, GrantID: p.GrantID, ParentTurnID: p.ParentTurnID, TaskBinding: p.TaskBinding, RouteID: p.RouteID, RouteVersion: p.RouteVersion, ProfileSHA256: p.ProfileSHA256, Request: p.Request, Quote: p.Quote, InputObject: p.InputObject}
}
func checkPaidQuote(p ledger.RuntimePaidReserved, profile ledger.Profile) error {
	d, q := p.Request, p.Quote
	digest, _ := profile.SHA256()
	tariff, _ := profile.TariffSHA256()
	if digest != p.ProfileSHA256 || d.Provider != profile.Provider || d.Authority != profile.Authority || d.Model != profile.ModelID || d.Region != profile.Region || d.Method != profile.Method || d.Path != profile.Path || d.RequestedTier != profile.RequestedTier || q.CapabilityVersion != profile.CapabilityVersion || q.TariffVersion != profile.TariffVersion || q.TariffSHA256 != tariff {
		return ErrInvalid
	}
	if d.Provider == "anthropic" && !slices.Equal(d.Headers, []ledger.SemanticHeader{{Name: "accept", Value: "text/event-stream"}, {Name: "anthropic-version", Value: profile.APIVersion}, {Name: "content-type", Value: "application/json"}}) {
		return ErrInvalid
	}
	if d.Provider == "openai" && !slices.Equal(d.Headers, []ledger.SemanticHeader{{Name: "accept", Value: "text/event-stream"}, {Name: "content-type", Value: "application/json"}}) {
		return ErrInvalid
	}
	components := paidComponents(q)
	quoted, err := providers.QuoteSHA256([32]byte(q.RequestSHA256), q.CapabilityVersion, q.TariffVersion, [32]byte(q.TariffSHA256), q.MaximumMicros, components)
	if err != nil || ledger.Digest(quoted) != q.QuoteSHA256 {
		return ErrInvalid
	}
	pp := providerProfile(profile)
	if pp.ValidateHistorical() != nil {
		return nil
	} // Unsupported history grants no financial release.
	var inputs []string
	var output int64
	for _, c := range components {
		if slices.Equal(c.Classes, paidOutputClasses(pp.Provider)) {
			output = c.MaxUnits
		} else {
			inputs = append(inputs, c.Classes...)
		}
	}
	bound, maximum, err := providers.BoundHistorical(pp, output, inputs)
	if err != nil || maximum != q.MaximumMicros || len(bound) != len(components) {
		return ErrInvalid
	}
	for _, c := range bound {
		if !slices.ContainsFunc(components, func(other providers.Component) bool { return sameScopedValue(c, other) }) {
			return ErrInvalid
		}
	}
	return nil
}

func paidOutputClasses(provider string) []string {
	switch provider {
	case "anthropic":
		return []string{providers.Generated}
	case "openai":
		return []string{"output.generated.long", "output.generated.short"}
	default:
		return nil
	}
}

// Normalization and historical pricing validate the guarded band first.
func paidOpenAITier(units []providers.Units) string {
	for _, u := range units {
		if u.Count > 0 && slices.Contains([]string{"input.cached.long", "input.uncached.long", "input.write.long"}, u.Class) {
			return "default_long"
		}
	}
	return "default_short"
}

func (s *Service) paidAuthority(ctx context.Context, a paidAttempt, through uint64) ([]grantRules, *scopedTaskState, error) {
	p := a.admission
	route, found := s.p.paidRoutes[p.RouteID]
	digest, _ := route.Profile.SHA256()
	if !found || !route.Enabled || route.Version != p.RouteVersion || digest != p.ProfileSHA256 || s.p.retiredProfiles[digest] || route.CredentialScopeID != p.Request.Credential.ScopeID {
		return nil, nil, ErrDenied
	}
	credential, found := s.p.paidCredentials[p.Request.Credential.Version]
	if !found || credential.ScopeID != route.CredentialScopeID || credential.Provider != p.Request.Provider {
		return nil, nil, ErrDenied
	}
	chain, err := s.authorize(ctx, a.origin, p.GrantID, "model.call", []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + p.RouteID.String()}}, nil, 0, through)
	if err != nil {
		return nil, nil, err
	}
	if chain[0].task != (ledger.TaskBinding{}) && (p.TaskBinding == nil || *p.TaskBinding != chain[0].task) {
		return nil, nil, ErrDenied
	}
	task, err := s.activeWorkTask(ctx, a.origin, p.TaskBinding, through)
	if err != nil {
		return nil, nil, err
	}
	parent, found, err := s.loadWork(ctx, p.ParentTurnID, through)
	if err != nil {
		return nil, nil, err
	}
	if !found || parent.admission.Kind != "turn" || parent.origin != a.origin || parent.ended || parent.released || !sameScopedValue(parent.admission.TaskBinding, p.TaskBinding) {
		return nil, nil, ErrDenied
	}
	if !slices.Equal(p.Scopes, paidScopes(a.origin, credential.ScopeID, p.TaskBinding, chain)) {
		return nil, nil, ErrInvalid
	}
	if err := s.p.book.paidAuthority(p.Scopes); err != nil {
		return nil, nil, err
	}
	return chain, task, nil
}

func (s *Service) paidCurrent(ctx context.Context, a paidAttempt, observed int64, through uint64, extra int64) ([]grantRules, *scopedTaskState, error) {
	chain, task, err := s.paidAuthority(ctx, a, through)
	if err != nil {
		return nil, nil, err
	}
	profile := s.p.paidRoutes[a.admission.RouteID].Profile
	if observed < s.p.reviewFloor || observed < profile.VerifiedUTCNS || observed >= profile.ExpiresUTCNS {
		return nil, nil, ErrDenied
	}
	period := paidPeriod(s.p.book.paidPeriodKind, observed)
	if period == "" {
		return nil, nil, ErrDenied
	}
	if err := s.p.book.checkPaid(a.admission.Scopes, period, extra); err != nil {
		return nil, nil, err
	}
	return chain, task, nil
}

func (s *Service) planPaidAttempt(ctx context.Context, g *paidGroup, first uint64) (paidPlan, error) {
	plan := paidPlan{}
	sub, through := g.receipt.Submission, first-1
	var a paidAttempt
	var err error
	if p := g.reserved; p != nil {
		if bytes.Compare(p.AttemptID[:], s.p.maxAttempt[:]) <= 0 {
			_, found, err := s.loadPaid(ctx, p.AttemptID, through, ledger.Digest{})
			if err != nil {
				return plan, err
			}
			if found {
				return plan, ErrInvalid
			}
		}
		if p.AuthorityVersion != s.p.authority || p.GrantID != sub.GrantID || p.ParentTurnID != g.turn || !sameScopedValue(sub.Arguments, paidAdmitArguments(*p)) || p.AdmissionPeriod != paidPeriod(s.p.book.paidPeriodKind, p.AdmissionUTCNS) {
			return plan, ErrInvalid
		}
		route := s.p.paidRoutes[p.RouteID]
		a = paidAttempt{admission: *p, profile: route.Profile, origin: sub.Origin, version: 1, sequence: first + 1, held: true, state: "reserved"}
		if err := checkPaidQuote(*p, a.profile); err != nil {
			return plan, err
		}
		_, task, err := s.paidCurrent(ctx, a, p.AdmissionUTCNS, through, p.Quote.MaximumMicros)
		if err != nil {
			return plan, err
		}
		if task != nil {
			plan.tasks = append(plan.tasks, *task)
		}
		plan.observed, plan.period = p.AdmissionUTCNS, p.AdmissionPeriod
		if err := paidReceiptEffect(g, 1, p.AttemptID, "reserved"); err != nil {
			return plan, err
		}
	} else {
		var found bool
		var evidence ledger.Digest
		if g.result != nil {
			evidence = g.result.EvidenceSHA256
		}
		a, found, err = s.loadPaid(ctx, g.receipt.AttemptID, through, evidence)
		if err != nil {
			return plan, err
		}
		if !found {
			return plan, ErrDenied
		}
		plan.prior = a
		if g.acknowledgement == nil && (a.origin != sub.Origin || a.admission.ParentTurnID != g.turn || a.admission.GrantID != sub.GrantID) {
			return plan, ErrDenied
		}
		switch {
		case g.intent != nil:
			v := *g.intent
			if a.version != 1 || a.ended || a.intent != nil || v.ExpectedVersion != a.version || v.AttemptID != a.admission.AttemptID || v.AuthorityVersion != s.p.authority || v.RequestSHA256 != a.admission.Quote.RequestSHA256 || v.QuoteSHA256 != a.admission.Quote.QuoteSHA256 || v.Credential != a.admission.Request.Credential || !sameScopedValue(sub.Arguments, ledger.PaidAttemptArguments{AttemptID: v.AttemptID, ExpectedVersion: v.ExpectedVersion}) {
				return plan, ErrInvalid
			}
			_, task, err := s.paidCurrent(ctx, a, v.ObservedUTCNS, through, 0)
			if err != nil {
				return plan, err
			}
			if task != nil {
				plan.tasks = append(plan.tasks, *task)
			}
			a.intent, a.version, a.state = &v, v.Version, "sent"
			plan.observed, plan.period = v.ObservedUTCNS, paidPeriod(s.p.book.paidPeriodKind, v.ObservedUTCNS)
			if err := paidReceiptEffect(g, a.version, v.AttemptID, "sent"); err != nil {
				return plan, err
			}
		case g.result != nil:
			v := *g.result
			var arguments any
			switch sub.Operation {
			case "paid.release":
				if v.Kind != "released_unsent" {
					return plan, ErrInvalid
				}
				arguments = ledger.PaidAttemptArguments{AttemptID: v.AttemptID, ExpectedVersion: v.ExpectedVersion}
			case "paid.observe":
				if v.Kind != "unknown" && v.Kind != "usage" && v.Kind != "no_charge" {
					return plan, ErrInvalid
				}
				arguments = ledger.PaidObserveArguments{AttemptID: v.AttemptID, ExpectedVersion: v.ExpectedVersion, Kind: v.Kind, EvidenceSHA256: v.EvidenceSHA256, EvidenceObject: *v.EvidenceObject, MessageObjects: v.MessageObjects}
			case "paid.correct":
				if v.Kind != "correction" {
					return plan, ErrInvalid
				}
				arguments = ledger.PaidCorrectArguments{AttemptID: v.AttemptID, ExpectedVersion: v.ExpectedVersion, PreviousResultSequence: v.PreviousResultSequence, PreviousEvidenceSHA256: v.PreviousEvidenceSHA256, EvidenceSHA256: v.EvidenceSHA256, EvidenceObject: *v.EvidenceObject, MessageObjects: v.MessageObjects}
			default:
				return plan, ErrInvalid
			}
			if !sameScopedValue(sub.Arguments, arguments) {
				return plan, ErrInvalid
			}
			a, err = paidResultState(a, v, first+1, g.receipt.Outcome)
			if err != nil {
				return plan, err
			}
			outcome := map[string]string{"released_unsent": "released_unsent", "unknown": "unknown", "usage": "settled", "no_charge": "no_charge", "correction": "corrected"}[v.Kind]
			if err := paidReceiptEffect(g, a.version, v.AttemptID, outcome); err != nil {
				return plan, err
			}
		case g.acknowledgement != nil:
			v := *g.acknowledgement
			if !a.fenced || a.result == nil || v.ExpectedVersion != a.version || v.AttemptID != a.admission.AttemptID || v.ResultSequence != int64(a.resultSequence) || v.EvidenceSHA256 != a.result.EvidenceSHA256 || v.RetiredProfileSHA256 != a.admission.ProfileSHA256 || !sameScopedValue(sub.Arguments, ledger.PaidOverrunArguments{AttemptID: v.AttemptID, ExpectedVersion: v.ExpectedVersion, ResultSequence: v.ResultSequence, EvidenceSHA256: v.EvidenceSHA256, RetiredProfileSHA256: v.RetiredProfileSHA256}) {
				return plan, ErrInvalid
			}
			a.version, a.fenced, a.acknowledged = v.Version, false, true
			plan.retired = v.RetiredProfileSHA256
			if err := paidReceiptEffect(g, a.version, v.AttemptID, "acknowledged"); err != nil {
				return plan, err
			}
		default:
			return plan, ErrInvalid
		}
	}
	a.receipt, a.receiptSequence = g.receipt, first+2
	plan.attempt = &a
	period := plan.period
	if period == "" {
		period = s.p.book.paidPeriod
	}
	plan.paid, err = s.p.book.planPaidMoney(plan.prior, a, period)
	return plan, err
}

func paidResultState(a paidAttempt, v ledger.RuntimePaidResult, sequence uint64, outcome string) (paidAttempt, error) {
	if v.ExpectedVersion != a.version || v.AttemptID != a.admission.AttemptID || a.state == "released_unsent" {
		return a, ErrInvalid
	}
	if v.Kind == "released_unsent" {
		if a.version != 1 || a.intent != nil || a.ended {
			return a, ErrInvalid
		}
		a.version, a.ended, a.held, a.state, a.result, a.resultSequence = v.Version, true, false, v.Kind, &v, sequence
		if outcome != "released_unsent" {
			a.held, a.state = true, "unknown"
		}
		return a, nil
	}
	if a.intent == nil {
		return a, ErrInvalid
	}
	if a.evidence[v.EvidenceSHA256] != 0 {
		return a, ErrNoEffect
	}
	if old := a.result; old != nil {
		if old.OutputStarted != nil && *old.OutputStarted && !*v.OutputStarted || old.ProviderMessageID != "" && old.ProviderMessageID != v.ProviderMessageID || old.ProviderRequestID != "" && old.ProviderRequestID != v.ProviderRequestID {
			return a, ErrInvalid
		}
	}
	if v.Kind == "correction" {
		if v.ExpectedVersion < 3 || a.result == nil || v.PreviousResultSequence != int64(a.resultSequence) || v.PreviousEvidenceSHA256 != a.result.EvidenceSHA256 || a.result.ProviderMessageID == "" || a.result.ProviderMessageID != v.ProviderMessageID {
			return a, ErrInvalid
		}
	} else if a.result != nil && a.result.Kind != "unknown" {
		return a, ErrInvalid
	}
	supported, cost, violation, err := paidResultCost(a, v)
	if err != nil {
		return a, err
	}
	a.version, a.ended, a.result, a.resultSequence = v.Version, true, &v, sequence
	a.evidence = maps.Clone(a.evidence)
	if a.evidence == nil {
		a.evidence = make(map[ledger.Digest]uint64)
	}
	a.evidence[v.EvidenceSHA256] = sequence + 1
	a.state = "unknown"
	if supported && slices.Contains(paidOutcomes, outcome) && v.Kind != "unknown" {
		a.cost, a.held, a.state = cost, false, "settled"
		if v.NoCharge != nil {
			a.state = "no_charge"
		}
		a.fenced = violation
	} else {
		// A future financial interpretation never refunds already established cost.
		// An unresolved attempt keeps its original full hold.
		a.fenced = a.fenced || v.BoundViolation
	}
	return a, nil
}

func paidResultCost(a paidAttempt, v ledger.RuntimePaidResult) (bool, int64, bool, error) {
	if v.Kind == "unknown" {
		return true, a.cost, v.BoundViolation, nil
	}
	pp := providerProfile(a.profile)
	if pp.ValidateHistorical() != nil {
		return false, 0, false, nil
	}
	if v.ErrorClass != "" && !slices.Contains([]string{"denied", "exhausted", "rate_limited", "overloaded", "invalid_request", "unavailable", "conflict", "provider_error", "timeout", "unreachable", "protocol_error", "refused", "tariff_ambiguous"}, v.ErrorClass) {
		return false, 0, false, nil
	}
	if v.NoCharge != nil {
		if !slices.Contains(a.profile.NoChargeRules, v.NoCharge.Rule) {
			return false, 0, false, ErrInvalid
		}
		if v.NoCharge.Rule != "anthropic_refusal_before_output_2026_09_24" {
			return false, 0, false, nil
		}
		return true, 0, false, nil
	}
	if v.Usage == nil {
		return false, 0, false, ErrInvalid
	}
	var classes []string
	switch pp.Provider {
	case "anthropic":
		if v.Usage.PricingTier != "standard" || v.Usage.ServedTier != "" && v.Usage.ServedTier != "standard" {
			return false, 0, false, nil
		}
		classes = []string{providers.CacheRead, providers.CacheWrite1h, providers.CacheWrite5m, providers.Uncached, providers.Generated}
	case "openai":
		if v.Usage.ServedTier != "default" {
			return false, 0, false, nil
		}
		classes = []string{"input.cached.long", "input.cached.short", "input.uncached.long", "input.uncached.short", "input.write.long", "input.write.short", "output.generated.long", "output.generated.short"}
	default:
		return false, 0, false, nil
	}
	units := make([]providers.Units, 0, len(v.Usage.Classes))
	for _, u := range v.Usage.Classes {
		if !slices.Contains(classes, u.Class) {
			return false, 0, false, nil
		}
		units = append(units, providers.Units{Class: u.Class, Count: u.Units})
	}
	if len(units) != len(classes) {
		return false, 0, false, ErrInvalid
	}
	cost, err := providers.PriceHistoricalUsage(pp, units)
	if err != nil {
		return false, 0, false, nil
	}
	if pp.Provider == "openai" && v.Usage.PricingTier != paidOpenAITier(units) {
		return false, 0, false, nil
	}
	if v.CostMicros == nil || cost != *v.CostMicros {
		return false, 0, false, ErrInvalid
	}
	violation := paidUsageViolation(a.admission.Quote, units, cost)
	if violation != v.BoundViolation {
		return false, 0, false, ErrInvalid
	}
	return true, cost, violation, nil
}

func paidUsageViolation(quote ledger.CostBound, units []providers.Units, cost int64) bool {
	violation := cost > quote.MaximumMicros
	for _, c := range quote.Components {
		total := new(big.Int)
		for _, u := range units {
			if slices.Contains(c.Classes, u.Class) {
				total.Add(total, big.NewInt(u.Count))
			}
		}
		violation = violation || total.Cmp(big.NewInt(c.MaxUnits)) > 0
	}
	for _, u := range units {
		included := slices.ContainsFunc(quote.Components, func(c ledger.PricedDimension) bool { return slices.Contains(c.Classes, u.Class) })
		violation = violation || u.Count > 0 && !included
	}
	return violation
}

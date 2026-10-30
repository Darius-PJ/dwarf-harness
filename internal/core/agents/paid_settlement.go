package agents

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func paidCounter(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || len(raw) > 19 {
		return 0, false
	}
	for _, b := range raw {
		if b < '0' || b > '9' {
			return 0, false
		}
	}
	var n int64
	err := json.Unmarshal(raw, &n)
	return n, err == nil && n >= 0
}

func paidNoCharge(profile ledger.Profile, o paidObservationObject) *ledger.NoChargeEvidence {
	const rule = "anthropic_refusal_before_output_2026_09_24"
	if !slices.Contains(profile.NoChargeRules, rule) || !o.Complete || o.OutputStarted || o.ContentBlocks != 0 || o.StopReason != "refusal" {
		return nil
	}
	var details struct {
		Type     string          `json:"type"`
		Category json.RawMessage `json:"category"`
	}
	if json.Unmarshal(o.StopDetails, &details) != nil || details.Type != "refusal" || len(details.Category) == 0 {
		return nil
	}
	n := &ledger.NoChargeEvidence{Rule: rule, StopReason: "refusal", StopDetailsType: "refusal", Complete: true, CategoryKind: "null"}
	if string(details.Category) != "null" {
		if json.Unmarshal(details.Category, &n.Category) != nil || !paidRuleToken(n.Category) || slices.Contains([]string{"bio", "frontier_llm", "reasoning_extraction"}, n.Category) {
			return nil
		}
		n.CategoryKind = "value"
	}
	if raw, present := o.RawUsageMerged["output_tokens"]; present {
		value, ok := paidCounter(raw)
		if !ok || value != 0 {
			return nil
		}
		n.OutputTokens = &value
	}
	return n
}

func paidRuleToken(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func derivePaidResult(a paidAttempt, o paidObservationObject) ledger.RuntimePaidResult {
	if o.ErrorClass != "" && !slices.Contains([]string{"denied", "exhausted", "rate_limited", "overloaded", "invalid_request", "unavailable", "conflict", "provider_error", "timeout", "unreachable", "protocol_error", "refused", "tariff_ambiguous"}, o.ErrorClass) {
		o.ErrorClass, o.Complete = "protocol_error", false
	}
	started := o.OutputStarted
	if a.result != nil && a.result.OutputStarted != nil {
		started = started || *a.result.OutputStarted
	}
	v := ledger.RuntimePaidResult{AttemptID: a.admission.AttemptID, ExpectedVersion: a.version, Version: a.version + 1, Kind: "unknown", OutputStarted: &started, BoundViolation: o.ProfileViolation, ErrorClass: o.ErrorClass}
	if paidProviderID(o.ProviderRequestID) {
		v.ProviderRequestID = o.ProviderRequestID
	}
	if paidProviderID(o.ProviderMessageID) {
		v.ProviderMessageID = o.ProviderMessageID
	}
	// Absence in a later capture does not erase an already known identity.
	if a.result != nil {
		if v.ProviderRequestID == "" {
			v.ProviderRequestID = a.result.ProviderRequestID
		}
		if v.ProviderMessageID == "" {
			v.ProviderMessageID = a.result.ProviderMessageID
		}
	}
	if v.ErrorClass == "" {
		v.ErrorClass = "protocol_error"
	}
	if !o.Complete || o.HTTPStatus != 200 || !paidProviderID(o.ProviderMessageID) || o.Model != a.profile.ModelID {
		return v
	}
	switch a.profile.Provider {
	case "anthropic":
	case "openai":
		return deriveOpenAIResult(a, o, v)
	default:
		return v
	}
	if o.StopReason == "refusal" {
		v.ErrorClass = "refused"
		if !started {
			if free := paidNoCharge(a.profile, o); free != nil {
				v.Kind, v.NoCharge, v.CostMicros, v.BoundViolation = "no_charge", free, new(int64), false
				return v
			}
		}
		var details struct {
			Category string `json:"category"`
		}
		_ = json.Unmarshal(o.StopDetails, &details)
		output, ok := paidCounter(o.RawUsageMerged["output_tokens"])
		billedCategory := slices.Contains([]string{"bio", "frontier_llm", "reasoning_extraction"}, details.Category)
		if !slices.Contains(a.profile.NoChargeRules, "anthropic_refusal_before_output_2026_09_24") || !ok || started && output == 0 || !started && (output != 0 || !billedCategory) {
			return v
		}
	} else if !slices.Contains([]string{"end_turn", "max_tokens", "stop_sequence", "tool_use", "model_context_window_exceeded"}, o.StopReason) {
		return v
	}
	var final providers.Usage
	if json.Unmarshal(o.RawUsageFinal, &final) != nil {
		return v
	}
	if _, ok := paidCounter(final["output_tokens"]); !ok {
		return v
	}
	var classes []string
	for _, component := range a.admission.Quote.Components {
		for _, class := range component.Classes {
			if class != providers.Generated {
				classes = append(classes, class)
			}
		}
	}
	profile := providerProfile(a.profile)
	units, violation, err := providers.NormalizeSealedUsage(profile, classes, o.RawUsageMerged)
	v.BoundViolation = v.BoundViolation || violation
	if err != nil {
		return v
	}
	cost, err := providers.PriceHistoricalUsage(profile, units)
	if err != nil {
		v.BoundViolation = true
		return v
	}
	usage := &ledger.PaidUsage{PricingTier: "standard"}
	_ = json.Unmarshal(o.RawUsageMerged["service_tier"], &usage.ServedTier)
	for _, unit := range units {
		usage.Classes = append(usage.Classes, ledger.PaidUnits{Class: unit.Class, Units: unit.Count})
	}
	v.Kind, v.Usage, v.CostMicros = "usage", usage, &cost
	v.BoundViolation = paidUsageViolation(a.admission.Quote, units, cost)
	v.ErrorClass = o.ErrorClass
	if o.ContentError {
		v.ErrorClass = "protocol_error"
	} else if o.StopReason != "refusal" && len(o.MessageObjects) != 0 {
		v.MessageObjects = slices.Clone(o.MessageObjects)
	}
	return v
}

// Late evidence is normalized again from the terminal, never from diagnostic
// snapshots or cached normalized counters. Provider errors do not prove zero.
func deriveOpenAIResult(a paidAttempt, o paidObservationObject, v ledger.RuntimePaidResult) ledger.RuntimePaidResult {
	if !slices.Contains([]string{"completed", "incomplete", "failed"}, o.StopReason) || len(o.UnknownEventTypes) != 0 || o.UnknownEventTypeOverflow != 0 || o.ProfileViolation {
		return v
	}
	if o.StopReason == "incomplete" {
		var details struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(o.StopDetails, &details) != nil || !slices.Contains([]string{"max_output_tokens", "max_messages", "content_filter"}, details.Reason) {
			return v
		}
	}
	var tier string
	if json.Unmarshal(o.ServedTier, &tier) != nil || tier != "default" {
		v.BoundViolation = tier != ""
		return v
	}
	if !paidEmptyToolUsage(o.ToolUsage) {
		v.BoundViolation = true
		return v
	}
	var final providers.Usage
	if json.Unmarshal(o.RawUsageFinal, &final) != nil {
		return v
	}
	var classes []string
	for _, component := range a.admission.Quote.Components {
		if !slices.Equal(component.Classes, paidOutputClasses("openai")) {
			classes = append(classes, component.Classes...)
		}
	}
	profile := providerProfile(a.profile)
	units, violation, err := providers.NormalizeSealedUsage(profile, classes, final)
	v.BoundViolation = v.BoundViolation || violation
	if errors.Is(err, providers.ErrTariffAmbiguous) {
		v.ErrorClass = "tariff_ambiguous"
		return v
	}
	if err != nil {
		return v
	}
	cost, err := providers.PriceHistoricalUsage(profile, units)
	if err != nil {
		v.BoundViolation = true
		return v
	}
	usage := &ledger.PaidUsage{PricingTier: paidOpenAITier(units), ServedTier: tier}
	for _, unit := range units {
		usage.Classes = append(usage.Classes, ledger.PaidUnits{Class: unit.Class, Units: unit.Count})
	}
	v.Kind, v.Usage, v.CostMicros = "usage", usage, &cost
	v.BoundViolation = paidUsageViolation(a.admission.Quote, units, cost)
	v.ErrorClass = o.ErrorClass
	if o.ContentError {
		v.ErrorClass = "protocol_error"
	} else if len(o.MessageObjects) != 0 {
		// The OpenAI reader retains only complete opaque reasoning for a
		// non-success terminal; these are evidence, not assistant commands.
		v.MessageObjects = slices.Clone(o.MessageObjects)
	}
	return v
}

func paidEmptyToolUsage(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for _, value := range fields {
		if string(value) != "0" {
			// Nested null is not a zero leaf; only the top-level null is
			// the documented absence of this separate billing surface.
			var object map[string]json.RawMessage
			if json.Unmarshal(value, &object) != nil || object == nil || !paidEmptyToolUsage(value) {
				return false
			}
		}
	}
	return true
}

func (s *Service) RecordPaidObservation(ctx context.Context, request PaidObservation) (PaidReceipt, error) {
	return s.recordPaidEvidence(ctx, request, nil)
}

func (s *Service) CorrectPaidSettlement(ctx context.Context, request PaidCorrection) (PaidReceipt, error) {
	return s.recordPaidEvidence(ctx, request.PaidObservation, &request)
}

func (s *Service) recordPaidEvidence(ctx context.Context, request PaidObservation, correction *PaidCorrection) (PaidReceipt, error) {
	if s == nil {
		return PaidReceipt{}, ErrNotBound
	}
	if !validID(request.CommandID) || !validID(request.AttemptID) || request.ExpectedVersion < 2 || request.ExpectedVersion == math.MaxInt64 || (request.Observed == nil) == (request.TrustedEvidence == nil) {
		return PaidReceipt{}, ErrInvalid
	}
	if err := s.enter(ctx); err != nil {
		return PaidReceipt{}, err
	}
	defer s.active.Done()
	var a paidAttempt
	load := func() error {
		var found bool
		var err error
		a, found, err = s.loadPaid(ctx, request.AttemptID, s.p.through, ledger.Digest{})
		if err == nil && (!found || a.intent == nil) {
			err = ErrDenied
		}
		return err
	}
	match, err := s.lookupPaid(ctx, request.CommandID, load)
	if err != nil {
		s.mu.Unlock()
		return PaidReceipt{}, err
	}
	var ref ledger.ObjectRef
	if request.Observed != nil {
		o := request.Observed
		if o.service != s || o.attempt != request.AttemptID || o.origin != a.origin {
			s.mu.Unlock()
			return PaidReceipt{}, ErrDenied
		}
		ref = o.evidence
	} else {
		ref = *request.TrustedEvidence
	}
	if ref.Bytes <= 0 || ref.Bytes > maxObjectBytes || ref.SHA256 == (ledger.Digest{}) {
		s.mu.Unlock()
		return PaidReceipt{}, ErrInvalid
	}
	if match.found {
		receipt, err := s.matchPaidObservation(match, a, request, ref, correction)
		s.mu.Unlock()
		return receipt, err
	}
	if s.paid != nil && s.paid.exchanges[request.AttemptID] != nil {
		s.mu.Unlock()
		return PaidReceipt{}, &BusyError{Reason: "paid_active"}
	}
	// Host factual reads are process-bounded, not newly authorized spend. They
	// remain possible after revocation, parent end, and through plain Open.
	if s.preparing >= s.limits.MaxConcurrentWrites {
		s.mu.Unlock()
		return PaidReceipt{}, &BusyError{Reason: "preparing"}
	}
	s.preparing++
	directory := s.p.agents[a.origin.AgentID].directory
	s.mu.Unlock()
	defer s.releasePreparation()
	scope, err := newPrivateScope(s.colony, directory, a.origin.ContextID.String(), &s.dirs)
	if err != nil {
		return PaidReceipt{}, err
	}
	raw, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
	var object paidObservationObject
	if err != nil || json.Unmarshal(raw, &object) != nil || object.Schema != 1 || object.Kind != "paid_observation" || object.AttemptID != a.admission.AttemptID || object.Origin != a.origin || object.ParentTurnID != a.admission.ParentTurnID || object.RequestSHA256 != a.admission.Quote.RequestSHA256 {
		return PaidReceipt{}, ErrIntegrity
	}
	total := int64(0)
	previousBlock, previousChunk := -1, -1
	for _, message := range object.MessageObjects {
		total += message.Bytes
		if message.Bytes <= 0 || message.Bytes > paidChunkLimit || total > paidOutputLimit {
			return PaidReceipt{}, ErrIntegrity
		}
		body, err := readObject(scope, Head{SHA256: message.SHA256, Bytes: message.Bytes})
		var output paidOutputObject
		if err != nil || json.Unmarshal(body, &output) != nil || output.Schema != 1 || output.Kind != "paid_output" || output.AttemptID != a.admission.AttemptID || output.Origin != a.origin || output.ParentTurnID != a.admission.ParentTurnID || output.BlockIndex < 0 || output.ChunkIndex < 0 {
			return PaidReceipt{}, ErrIntegrity
		}
		if output.BlockIndex == previousBlock {
			if output.ChunkIndex != previousChunk+1 {
				return PaidReceipt{}, ErrIntegrity
			}
		} else if output.BlockIndex != previousBlock+1 || output.ChunkIndex != 0 {
			return PaidReceipt{}, ErrIntegrity
		}
		block, ok := output.Block.(map[string]any)
		if !ok || block["kind"] != "text" && block["kind"] != "local_tool_call" && block["kind"] != "opaque_provider" {
			return PaidReceipt{}, ErrIntegrity
		}
		previousBlock, previousChunk = output.BlockIndex, output.ChunkIndex
	}
	match, err = s.lookupPaid(ctx, request.CommandID, load)
	defer s.mu.Unlock()
	if err != nil {
		return PaidReceipt{}, err
	}
	if match.found {
		return s.matchPaidObservation(match, a, request, ref, correction)
	}
	prior, _, err := s.loadPaid(ctx, request.AttemptID, s.p.through, ref.SHA256)
	if errors.Is(err, ErrNoEffect) {
		sequence := prior.evidence[ref.SHA256]
		var receipt PaidReceipt
		err = s.scan(ctx, sequence-3, sequence, func(c ledger.Commit) error {
			_, _, _, g, _, e := decodeFamily(draftsOf(c), c.First)
			if e == nil && g != nil && c.Last == sequence {
				receipt, e = copyPaidReceipt(g, s.p.through)
			}
			return e
		})
		return receipt, err
	}
	if err != nil {
		return PaidReceipt{}, err
	}
	result := derivePaidResult(a, object)
	result.ExpectedVersion, result.Version = request.ExpectedVersion, request.ExpectedVersion+1
	result.EvidenceSHA256, result.EvidenceObject = ref.SHA256, &ref
	sub := paidObservationSubmission(a, request, ref, result.Kind, result.MessageObjects, correction)
	outcome := map[string]string{"unknown": "unknown", "usage": "settled", "no_charge": "no_charge"}[result.Kind]
	if correction != nil {
		if result.Kind == "unknown" {
			return PaidReceipt{}, ErrInvalid
		}
		result.Kind, result.PreviousResultSequence, result.PreviousEvidenceSHA256 = "correction", correction.PreviousResultSequence, correction.PreviousEvidenceSHA256
		outcome = "corrected"
	}
	if a.version != request.ExpectedVersion {
		return PaidReceipt{}, ErrRevision
	}
	drafts, err := paidDrafts(request.CommandID, sub, result, ledger.TypeRuntimePaidResult, outcome, result.Version, request.AttemptID, a.admission.ParentTurnID, nil)
	if err != nil {
		return PaidReceipt{}, err
	}
	return s.submitPaidLocked(ctx, drafts)
}

func paidObservationSubmission(a paidAttempt, request PaidObservation, ref ledger.ObjectRef, kind string, messages []ledger.ObjectRef, correction *PaidCorrection) ledger.PaidSubmission {
	sub := ledger.PaidSubmission{Schema: 4, Actor: "core", Origin: a.origin, GrantID: a.admission.GrantID, Operation: "paid.observe", Arguments: ledger.PaidObserveArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, Kind: kind, EvidenceSHA256: ref.SHA256, EvidenceObject: ref, MessageObjects: messages}}
	if correction != nil {
		sub.Operation, sub.Arguments = "paid.correct", ledger.PaidCorrectArguments{AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedVersion, PreviousResultSequence: correction.PreviousResultSequence, PreviousEvidenceSHA256: correction.PreviousEvidenceSHA256, EvidenceSHA256: ref.SHA256, EvidenceObject: ref, MessageObjects: messages}
	}
	return sub
}

func (s *Service) matchPaidObservation(match commandMatch, a paidAttempt, request PaidObservation, ref ledger.ObjectRef, correction *PaidCorrection) (PaidReceipt, error) {
	operation := "paid.observe"
	if correction != nil {
		operation = "paid.correct"
	}
	if match.paid == nil || match.paid.result == nil || match.paid.receipt.Submission.Operation != operation {
		return PaidReceipt{}, ErrCommandMismatch
	}
	old := match.paid.result
	sub := paidObservationSubmission(a, request, ref, old.Kind, old.MessageObjects, correction)
	digest, err := paidDigest(sub)
	if err != nil {
		return PaidReceipt{}, err
	}
	return s.matchPaid(match, sub, digest)
}

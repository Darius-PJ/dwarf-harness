package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// MaxPaidPayloadBytes is F3-13's complete payload cap, frozen (D148).
const MaxPaidPayloadBytes = 64 << 10

// PaidScope adds paid scope kinds without changing F3-11's ScopeKey.
type PaidScope struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id,omitzero"`
}

// SpendTerms distinguishes an explicit unlimited mode from a finite zero cap.
type SpendTerms struct {
	Enabled       bool   `json:"enabled"`
	Mode          string `json:"mode"`
	CeilingMicros *int64 `json:"ceiling_micros,omitempty"`
	PeriodKind    string `json:"period_kind"`
	WarningPPM    *int64 `json:"warning_ppm,omitempty"`
}

// CredentialRef identifies a binding, never secret bytes or a secret-derived hash.
type CredentialRef struct {
	ScopeID uuid.UUID `json:"scope_id"`
	Version uuid.UUID `json:"version"`
}

type SemanticHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SealedDescriptor identifies the nonsecret envelope and exact private body.
type SealedDescriptor struct {
	Provider      string           `json:"provider"`
	Authority     string           `json:"authority"`
	Model         string           `json:"model"`
	Region        string           `json:"region"`
	Method        string           `json:"method"`
	Path          string           `json:"path"`
	RequestedTier string           `json:"requested_tier"`
	Credential    CredentialRef    `json:"credential"`
	Headers       []SemanticHeader `json:"headers"`
	BodySHA256    Digest           `json:"body_sha256"`
	BodyBytes     int64            `json:"body_bytes"`
}

// PricedDimension is one exact component; classes cannot overlap components.
type PricedDimension struct {
	Classes         []string `json:"classes"`
	MaxUnits        int64    `json:"max_units"`
	RateNumerator   string   `json:"rate_numerator"`
	RateDenominator string   `json:"rate_denominator"`
}

type CostBound struct {
	Currency          string            `json:"currency"`
	MaximumMicros     int64             `json:"maximum_micros"`
	Components        []PricedDimension `json:"components"`
	RequestSHA256     Digest            `json:"request_sha256"`
	CapabilityVersion string            `json:"capability_version"`
	TariffVersion     string            `json:"tariff_version"`
	TariffSHA256      Digest            `json:"tariff_sha256"`
	QuoteSHA256       Digest            `json:"quote_sha256"`
}

// PaidUsage contains exclusive units; supported usage schemas are producer rules.
type PaidUsage struct {
	Classes     []PaidUnits `json:"classes"`
	PricingTier string      `json:"pricing_tier"`
	ServedTier  string      `json:"served_tier,omitempty"`
}

type PaidUnits struct {
	Class string `json:"class"`
	Units int64  `json:"units"`
}

// NoChargeEvidence records every fact supporting a pre-output refusal exemption.
// The reducer additionally checks Rule against the pinned profile's set.
type NoChargeEvidence struct {
	Rule            string `json:"rule"`
	StopReason      string `json:"stop_reason"`
	StopDetailsType string `json:"stop_details_type"`
	CategoryKind    string `json:"category_kind"`
	Category        string `json:"category,omitempty"`
	Complete        bool   `json:"complete"`
	ContentBlocks   int64  `json:"content_blocks"`
	OutputStarted   bool   `json:"output_started"`
	OutputTokens    *int64 `json:"output_tokens,omitempty"`
}

// PaidLocalUsage charges the existing non-paid book on agent delegation only.
type PaidLocalUsage struct {
	AuthorityVersion int64      `json:"authority_version"`
	GrantID          uuid.UUID  `json:"grant_id"`
	Scopes           []ScopeKey `json:"scopes"`
	Units            int64      `json:"units"`
	Bytes            int64      `json:"bytes"`
}

func paidScopeRank(kind string) int {
	return slices.Index([]string{"colony", "agent", "credential", "task", "grant"}, kind)
}
func validPaidScope(s PaidScope) bool {
	return paidScopeRank(s.Kind) >= 0 && (s.Kind == "colony" && s.ID == (uuid.UUID{}) || s.Kind != "colony" && isV7(s.ID))
}
func validPaidScopes(ss []PaidScope) bool {
	if len(ss) < 4 {
		return false
	}
	for i, s := range ss {
		if !validPaidScope(s) {
			return false
		}
		if i > 0 {
			p := ss[i-1]
			if paidScopeRank(p.Kind) > paidScopeRank(s.Kind) || p.Kind == s.Kind && bytes.Compare(p.ID[:], s.ID[:]) >= 0 {
				return false
			}
		}
	}
	return true
}

func (s SpendTerms) check() error {
	if s.PeriodKind != "lifetime" && s.PeriodKind != "utc_day" && s.PeriodKind != "utc_month" {
		return errors.New("invalid spend period")
	}
	switch s.Mode {
	case "finite":
		if s.CeilingMicros == nil || *s.CeilingMicros < 0 || s.WarningPPM != nil && (*s.WarningPPM < 1 || *s.WarningPPM > 1000000) {
			return errors.New("invalid finite spend terms")
		}
	case "unlimited":
		if s.CeilingMicros != nil || s.WarningPPM != nil {
			return errors.New("unlimited spend has finite terms")
		}
	default:
		return errors.New("invalid spend mode")
	}
	return nil
}

func validCredentialRef(c CredentialRef) bool { return isV7(c.ScopeID) && isV7(c.Version) }
func validPaidObject(o ObjectRef) bool        { return o.SHA256 != (Digest{}) && o.Bytes > 0 }
func paidDecimal(s string, positive bool) bool {
	if len(s) == 0 || len(s) > 19 || len(s) > 1 && s[0] == '0' || positive && s == "0" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func (s SealedDescriptor) check() error {
	if !scopedToken(s.Provider) || !paidEndpoint(s.Authority, s.Path) || !paidASCII(s.Model, 128) || !scopedToken(s.Region) || s.Method != "POST" || !scopedToken(s.RequestedTier) || !validCredentialRef(s.Credential) || s.BodySHA256 == (Digest{}) || s.BodyBytes <= 0 || s.Headers == nil {
		return errors.New("invalid sealed descriptor")
	}
	for i, h := range s.Headers {
		if h.Name == "" || strings.ToLower(h.Name) != h.Name || i > 0 && s.Headers[i-1].Name >= h.Name || !utf8.ValidString(h.Value) || strings.ContainsFunc(h.Value, unicode.IsControl) {
			return errors.New("invalid semantic header")
		}
		for _, c := range h.Name {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
				return errors.New("invalid semantic header name")
			}
		}
		// Authentication and transport framing are never semantic inputs.
		if slices.Contains([]string{"authorization", "proxy-authorization", "x-api-key", "host", "content-length", "connection", "transfer-encoding"}, h.Name) {
			return errors.New("nonsemantic header")
		}
	}
	return nil
}

func (q CostBound) check() error {
	if q.Currency != "USD" || q.MaximumMicros < 0 || len(q.Components) == 0 || q.RequestSHA256 == (Digest{}) || !paidASCII(q.CapabilityVersion, 128) || !paidASCII(q.TariffVersion, 128) || q.TariffSHA256 == (Digest{}) || q.QuoteSHA256 == (Digest{}) {
		return errors.New("invalid cost bound")
	}
	seen := make(map[string]bool)
	for _, c := range q.Components {
		if !paidStrings(c.Classes, paidClass, false) || c.MaxUnits < 0 || !paidDecimal(c.RateNumerator, false) || !paidDecimal(c.RateDenominator, true) {
			return errors.New("invalid priced dimension")
		}
		for _, class := range c.Classes {
			if seen[class] {
				return errors.New("overlapping priced dimensions")
			}
			seen[class] = true
		}
	}
	return nil
}

func (u PaidUsage) check() error {
	if len(u.Classes) == 0 || !scopedToken(u.PricingTier) || u.ServedTier != "" && !scopedToken(u.ServedTier) {
		return errors.New("invalid paid usage")
	}
	for i, c := range u.Classes {
		if !paidClass(c.Class) || c.Units < 0 || i > 0 && u.Classes[i-1].Class >= c.Class {
			return errors.New("invalid paid usage classes")
		}
	}
	return nil
}

func (e NoChargeEvidence) check() error {
	if !scopedToken(e.Rule) || e.StopReason != "refusal" || e.StopDetailsType != "refusal" || !e.Complete || e.ContentBlocks != 0 || e.OutputStarted || e.OutputTokens != nil && *e.OutputTokens != 0 {
		return errors.New("invalid no-charge evidence")
	}
	switch e.CategoryKind {
	case "null":
		if e.Category != "" {
			return errors.New("null category has a value")
		}
	case "value":
		if !scopedToken(e.Category) || slices.Contains([]string{"bio", "frontier_llm", "reasoning_extraction"}, e.Category) {
			return errors.New("invalid no-charge category")
		}
	default:
		return errors.New("invalid no-charge category kind")
	}
	return nil
}

func readPaidScope(m *members) PaidScope {
	s := PaidScope{Kind: m.text("kind")}
	if s.Kind == "colony" {
		forbidScoped(m, "id")
	} else {
		s.ID = m.id("id")
	}
	return s
}
func readSpendTerms(m *members) SpendTerms {
	return SpendTerms{m.boolean("enabled"), m.text("mode"), optionalScopedNat(m, "ceiling_micros"), m.text("period_kind"), optionalScopedNat(m, "warning_ppm")}
}
func readCredentialRef(m *members) CredentialRef {
	return CredentialRef{m.id("scope_id"), m.id("version")}
}
func readSealedDescriptor(m *members) SealedDescriptor {
	return SealedDescriptor{m.text("provider"), m.text("authority"), m.text("model"), m.text("region"), m.text("method"), m.text("path"), m.text("requested_tier"), graphObject(m, "credential", readCredentialRef), readScopedObjects(m, "headers", func(b *members) SemanticHeader { return SemanticHeader{b.text("name"), b.text("value")} }), m.digest("body_sha256"), m.nat("body_bytes", true)}
}
func readCostBound(m *members) CostBound {
	return CostBound{m.text("currency"), m.nat("maximum_micros", true), readScopedObjects(m, "components", func(b *members) PricedDimension {
		return PricedDimension{readScopedStrings(b, "classes"), b.nat("max_units", true), b.text("rate_numerator"), b.text("rate_denominator")}
	}), m.digest("request_sha256"), m.text("capability_version"), m.text("tariff_version"), m.digest("tariff_sha256"), m.digest("quote_sha256")}
}
func readPaidUsage(m *members) PaidUsage {
	u := PaidUsage{Classes: readScopedObjects(m, "classes", func(b *members) PaidUnits { return PaidUnits{b.text("class"), b.nat("units", true)} }), PricingTier: m.text("pricing_tier")}
	u.ServedTier, _ = m.optionalText("served_tier")
	return u
}
func readNoChargeEvidence(m *members) NoChargeEvidence {
	e := NoChargeEvidence{Rule: m.text("rule"), StopReason: m.text("stop_reason"), StopDetailsType: m.text("stop_details_type"), CategoryKind: m.text("category_kind"), Complete: m.boolean("complete"), ContentBlocks: m.nat("content_blocks", true), OutputStarted: m.boolean("output_started"), OutputTokens: optionalScopedNat(m, "output_tokens")}
	if e.CategoryKind == "null" {
		forbidScoped(m, "category")
	} else {
		e.Category = m.text("category")
	}
	return e
}
func readPaidLocalUsage(m *members) PaidLocalUsage {
	return PaidLocalUsage{m.nat("authority_version", true), m.id("grant_id"), readScopedObjects(m, "scopes", readScope), m.nat("units", true), m.nat("bytes", true)}
}

func encodePaidPayload(typ string, p any, invalid error) (json.RawMessage, error) {
	js, err := encodePayload(typ, p, invalid)
	if err == nil && len(js) > MaxPaidPayloadBytes {
		err = fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxPaidPayloadBytes)
	}
	if err != nil {
		return nil, err
	}
	return js, nil
}
func decodePaidPayload(typ string, js json.RawMessage, decode func(*members) error) error {
	if len(js) > MaxPaidPayloadBytes {
		return fmt.Errorf("%w: %s payload exceeds %d bytes", ErrInvalid, typ, MaxPaidPayloadBytes)
	}
	return decodePayload(typ, js, decode)
}

func (m *members) boolean(key string) bool {
	raw, ok := m.get(key, true)
	if !ok {
		return false
	}
	if string(raw) == "true" {
		return true
	}
	if string(raw) != "false" {
		m.err = fmt.Errorf("%s is not a boolean", key)
	}
	return false
}

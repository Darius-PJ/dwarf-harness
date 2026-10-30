package providers

import (
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	ProfileLimit = 32 << 10
	APIVersion   = "2026-10-21"
	Haiku        = "claude-haiku-4-5-20251001"
	Sonnet       = "claude-sonnet-5-5"
	Opus         = "claude-opus-5-5"
)

// Profile contains nonsecret, explicitly supplied facts. It is not a live
// capability discovery mechanism. Synthetic facts are restricted to loopback.
// Struct order is the version-1 compact known-field digest order.
type Profile struct {
	Schema             int           `json:"schema"`
	CapabilityVersion  string        `json:"capability_version"`
	TariffVersion      string        `json:"tariff_version"`
	Provider           string        `json:"provider"`
	Authority          string        `json:"authority"`
	Method             string        `json:"method"`
	Path               string        `json:"path"`
	APIVersion         string        `json:"api_version"`
	ModelID            string        `json:"model_id"`
	Region             string        `json:"region"`
	RequestedTier      string        `json:"requested_tier"`
	AllowedServedTiers []string      `json:"allowed_served_tiers"`
	Speed              string        `json:"speed"`
	ContextMax         int64         `json:"context_max"`
	InputMax           int64         `json:"input_max"`
	OutputMax          int64         `json:"output_max"`
	UsageSchema        string        `json:"usage_schema"`
	CacheClasses       []string      `json:"cache_classes"`
	ToolChoices        []string      `json:"tool_choices"`
	NoChargeRules      []string      `json:"no_charge_rules"`
	Tariff             Tariff        `json:"tariff"`
	VerifiedUTCNS      int64         `json:"verified_utc_ns"`
	ExpiresUTCNS       int64         `json:"expires_utc_ns"`
	Sources            []SourceQuote `json:"sources"`
}

type SourceQuote struct {
	Topic          string `json:"topic"`
	URL            string `json:"url"`
	RetrievedUTCNS int64  `json:"retrieved_utc_ns"`
	Quote          string `json:"quote"`
}

var requiredTopics = []string{"endpoint_api_version", "model_limits", "inclusive_usage", "tariff", "tier_region_speed", "local_tool_billing", "lifecycle", "no_charge_billing"}

// ParseProfile is the strict installation boundary, not a ledger decoder.
func ParseProfile(b []byte) (Profile, error) {
	var p Profile
	if err := checkJSON(b, ProfileLimit, true, true); err != nil {
		return p, err
	}
	if err := decodeStrict(b, &p); err != nil {
		return Profile{}, err
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func sortedSet(ss []string) bool {
	if ss == nil {
		return false
	}
	for i, s := range ss {
		if !visible(s, 128) || i > 0 && ss[i-1] >= s {
			return false
		}
	}
	return true
}

func endpoint(provider, authority, path string) (*url.URL, error) {
	var host, wantPath string
	switch provider {
	case "anthropic":
		host, wantPath = "api.anthropic.com", "/v1/messages"
	case "openai":
		host, wantPath = "api.openai.com", "/v1/responses"
	default:
		return nil, ErrUnsupported
	}
	raw := "https://" + authority + path
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Host != authority || u.Path != path || u.RawPath != "" || strings.ContainsAny(raw, "%\\#?\r\n\t ") || path != wantPath {
		return nil, ErrInvalid
	}
	if authority != host {
		host, port, e := net.SplitHostPort(authority)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() || port == "" {
			return nil, ErrUnsupported
		}
		n, e := decimal(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, ErrInvalid
		}
	}
	return u, nil
}

// Validate checks supported semantics and bounded, complete source evidence.
// It cannot authenticate the supplied quotes; the trusted installer does that.
func (p Profile) Validate() error {
	return p.validate(false)
}

// ValidateHistorical checks the semantics needed to interpret recorded money.
// It is not permission to install or dispatch: source completeness, installation
// size and verification lifetime are intentionally left to the live boundary.
func (p Profile) ValidateHistorical() error {
	return p.validate(true)
}

func (p Profile) validate(historical bool) error {
	if p.Schema != 1 || !visible(p.CapabilityVersion, 128) || !visible(p.TariffVersion, 128) || p.Method != "POST" {
		return ErrUnsupported
	}
	if _, err := endpoint(p.Provider, p.Authority, p.Path); err != nil {
		return err
	}
	switch p.Provider {
	case "anthropic":
		if p.APIVersion != APIVersion || p.UsageSchema != "anthropic-messages-text/v1" || p.ModelID != Haiku && p.ModelID != Sonnet && p.ModelID != Opus {
			return ErrUnsupported
		}
		if p.Region != "global" && p.Region != "us" || p.Speed != "standard" || !slices.Equal(p.AllowedServedTiers, []string{"standard"}) {
			return ErrUnsupported
		}
		if p.ModelID == Haiku {
			if p.Region != "global" || p.RequestedTier != "standard_only" || len(p.NoChargeRules) != 0 {
				return ErrUnsupported
			}
		} else if p.RequestedTier != "auto" {
			return ErrUnsupported
		}
		if !slices.Equal(p.CacheClasses, []string{CacheRead, CacheWrite1h, CacheWrite5m}) {
			return ErrUnsupported
		}
	case "openai":
		if p.APIVersion != "v1" || !slices.Contains([]string{openAIUsageSchema, openAIUsageSchemaV2}, p.UsageSchema) || !slices.Contains([]string{"gpt-6-luna", "gpt-6-sol", "gpt-6-astra"}, p.ModelID) {
			return ErrUnsupported
		}
		if p.Region != "global" || p.Speed != "standard" || p.RequestedTier != "default" || !slices.Equal(p.AllowedServedTiers, []string{"default"}) || len(p.NoChargeRules) != 0 || !slices.Equal(p.CacheClasses, []string{"input.cached.long", "input.cached.short", "input.write.long", "input.write.short"}) {
			return ErrUnsupported
		}
		// Synthetic loopback profiles may exercise different rectangular limits.
		if !historical && p.Authority == "api.openai.com" && (p.ContextMax != 1050000 || p.InputMax != 922000 || p.OutputMax != 128000) {
			return ErrUnsupported
		}
	default:
		return ErrUnsupported
	}
	if p.ContextMax <= 0 || p.InputMax <= 0 || p.OutputMax <= 0 || !historical && (p.VerifiedUTCNS <= 0 || p.ExpiresUTCNS <= p.VerifiedUTCNS) {
		return ErrInvalid
	}
	if !slices.Equal(p.ToolChoices, []string{"auto", "none"}) || !sortedSet(p.NoChargeRules) {
		return ErrUnsupported
	}
	for _, rule := range p.NoChargeRules {
		if !token(rule) {
			return ErrInvalid
		}
	}
	_, _, classes, err := providerClasses(p.Provider)
	if err != nil {
		return err
	}
	if p.Tariff.Currency != "USD" || len(p.Tariff.Rates) != len(classes) {
		return ErrInvalid
	}
	for i, r := range p.Tariff.Rates {
		if r.Class != classes[i] {
			return ErrInvalid
		}
		if _, err := rational(r.Numerator, r.Denominator); err != nil {
			return err
		}
	}
	if historical {
		return nil
	}
	topics := make(map[string]bool)
	prev := ""
	for _, s := range p.Sources {
		key := s.Topic + "\x00" + s.URL
		if !token(s.Topic) || key <= prev || s.RetrievedUTCNS <= 0 || s.RetrievedUTCNS > p.VerifiedUTCNS || len(s.Quote) == 0 || len(s.Quote) > 4096 || !utf8.ValidString(s.Quote) || len(s.URL) > 2048 {
			return ErrInvalid
		}
		prev = key
		u, e := url.Parse(s.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(s.URL, "\r\n\t ") {
			return ErrInvalid
		}
		if p.Authority == "api.anthropic.com" && (u.Hostname() != "platform.claude.com" && u.Hostname() != "support.claude.com" && u.Hostname() != "api.anthropic.com") {
			return ErrUnsupported
		}
		if p.Provider == "openai" && p.Authority == "api.openai.com" && (u.Host != "developers.openai.com" && u.Host != "api.openai.com" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "") {
			return ErrUnsupported
		}
		topics[s.Topic] = true
	}
	for _, topic := range requiredTopics {
		if !topics[topic] {
			return ErrInvalid
		}
	}
	b, e := json.Marshal(p)
	if e != nil || len(b) > ProfileLimit {
		return ErrLimit
	}
	return nil
}

// Current uses the caller's trusted observation; this package reads no clock.
func (p Profile) Current(observed int64) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if observed < p.VerifiedUTCNS || observed >= p.ExpiresUTCNS {
		return ErrExpired
	}
	return nil
}

func (p Profile) Canonical() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func (p Profile) SHA256() ([32]byte, error) {
	b, err := p.Canonical()
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

func (p Profile) TariffSHA256() ([32]byte, error) {
	if err := p.Validate(); err != nil {
		return [32]byte{}, err
	}
	b, err := json.Marshal(p.Tariff)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

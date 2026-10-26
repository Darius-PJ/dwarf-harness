package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Profile is F3-13's source-quoted snapshot, frozen (D148).
// Its known-field encoding, not its source JSON, is the digest preimage.
// Installation limits and supported provider semantics belong to the producer.
type Profile struct {
	Schema             int64           `json:"schema"`
	CapabilityVersion  string          `json:"capability_version"`
	TariffVersion      string          `json:"tariff_version"`
	Provider           string          `json:"provider"`
	Authority          string          `json:"authority"`
	Method             string          `json:"method"`
	Path               string          `json:"path"`
	APIVersion         string          `json:"api_version"`
	ModelID            string          `json:"model_id"`
	Region             string          `json:"region"`
	RequestedTier      string          `json:"requested_tier"`
	AllowedServedTiers []string        `json:"allowed_served_tiers"`
	Speed              string          `json:"speed"`
	ContextMax         int64           `json:"context_max"`
	InputMax           int64           `json:"input_max"`
	OutputMax          int64           `json:"output_max"`
	UsageSchema        string          `json:"usage_schema"`
	CacheClasses       []string        `json:"cache_classes"`
	ToolChoices        []string        `json:"tool_choices"`
	NoChargeRules      []string        `json:"no_charge_rules"`
	Tariff             Tariff          `json:"tariff"`
	VerifiedUTCNS      int64           `json:"verified_utc_ns"`
	ExpiresUTCNS       int64           `json:"expires_utc_ns"`
	Sources            []ProfileSource `json:"sources"`
}

// Tariff records exact rational USD per million tokens, already region-adjusted.
type Tariff struct {
	Currency string       `json:"currency"`
	Rates    []TariffRate `json:"rates"`
}

type TariffRate struct {
	Class       string `json:"class"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}

// ProfileSource contains public provenance, never authentication material.
type ProfileSource struct {
	Topic          string `json:"topic"`
	URL            string `json:"url"`
	RetrievedUTCNS int64  `json:"retrieved_utc_ns"`
	Quote          string `json:"quote"`
}

func paidASCII(s string, maximum int) bool {
	if len(s) == 0 || len(s) > maximum {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func paidClass(s string) bool {
	_, ok := dottedTokens(s)
	return ok && len(s) <= 64
}

func paidStrings(ss []string, valid func(string) bool, empty bool) bool {
	if ss == nil || !empty && len(ss) == 0 || !strictScopedStrings(ss) {
		return false
	}
	for _, s := range ss {
		if !valid(s) {
			return false
		}
	}
	return true
}

func paidEndpoint(authority, path string) bool {
	if !paidASCII(authority, 255) || !paidASCII(path, 2048) || !strings.HasPrefix(path, "/") || strings.ContainsAny(authority+path, "%\\#?") {
		return false
	}
	u, err := url.Parse("https://" + authority + path)
	return err == nil && u.Scheme == "https" && u.Host == authority && u.Hostname() != "" && u.Path == path && u.RawPath == "" && u.User == nil && u.Opaque == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func (p Profile) check() error {
	if p.Schema != 1 || !paidASCII(p.CapabilityVersion, 128) || !paidASCII(p.TariffVersion, 128) || !scopedToken(p.Provider) || !paidEndpoint(p.Authority, p.Path) || p.Method != "POST" || !paidASCII(p.APIVersion, 128) || !paidASCII(p.ModelID, 128) || !paidASCII(p.UsageSchema, 128) || !scopedToken(p.Region) || !scopedToken(p.RequestedTier) || !scopedToken(p.Speed) || p.ContextMax <= 0 || p.InputMax <= 0 || p.OutputMax <= 0 || p.VerifiedUTCNS <= 0 || p.ExpiresUTCNS <= p.VerifiedUTCNS {
		return errors.New("invalid paid profile")
	}
	if !paidStrings(p.AllowedServedTiers, scopedToken, false) || !paidStrings(p.CacheClasses, paidClass, true) || !paidStrings(p.ToolChoices, scopedToken, true) || !paidStrings(p.NoChargeRules, scopedToken, true) || p.Tariff.check() != nil {
		return errors.New("invalid paid profile sets or tariff")
	}
	if len(p.Sources) == 0 {
		return errors.New("missing paid profile sources")
	}
	previous := ""
	for _, s := range p.Sources {
		key := s.Topic + "\x00" + s.URL
		if !scopedToken(s.Topic) || key <= previous || s.RetrievedUTCNS <= 0 || s.RetrievedUTCNS > p.VerifiedUTCNS || len(s.Quote) == 0 || len(s.Quote) > 4096 || !utf8.ValidString(s.Quote) || len(s.URL) > 2048 || strings.ContainsFunc(s.URL, unicode.IsControl) {
			return errors.New("invalid paid profile source")
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || strings.Contains(s.URL, " ") {
			return errors.New("invalid paid profile source URL")
		}
		previous = key
	}
	return nil
}

func (p Tariff) check() error {
	if p.Currency != "USD" || len(p.Rates) == 0 {
		return errors.New("invalid paid tariff")
	}
	for i, r := range p.Rates {
		if !paidClass(r.Class) || i > 0 && p.Rates[i-1].Class >= r.Class || !paidDecimal(r.Numerator, false) || !paidDecimal(r.Denominator, true) {
			return errors.New("invalid paid tariff rate")
		}
	}
	return nil
}

func (p Profile) Encode() (json.RawMessage, error) {
	return encodePaidPayload("paid profile", p, p.check())
}
func (p Tariff) Encode() (json.RawMessage, error) {
	return encodePaidPayload("paid tariff", p, p.check())
}

// SHA256 hashes json.Marshal's compact known-field encoding, with HTML escaping.
func (p Profile) SHA256() (Digest, error) {
	b, err := p.Encode()
	if err != nil {
		return Digest{}, err
	}
	return Digest(sha256.Sum256(b)), nil
}

func (p Profile) TariffSHA256() (Digest, error) {
	if err := p.check(); err != nil {
		return Digest{}, errors.Join(ErrInvalid, err)
	}
	b, err := p.Tariff.Encode()
	if err != nil {
		return Digest{}, err
	}
	return Digest(sha256.Sum256(b)), nil
}

func DecodePaidProfile(js json.RawMessage) (Profile, error) {
	var p Profile
	err := decodePaidPayload("paid profile", js, func(m *members) error { p = readPaidProfile(m); _, err := p.Encode(); return err })
	if err != nil {
		return Profile{}, err
	}
	return p, nil
}

func readPaidProfile(m *members) Profile {
	return Profile{
		Schema: m.nat("schema", true), CapabilityVersion: m.text("capability_version"), TariffVersion: m.text("tariff_version"), Provider: m.text("provider"), Authority: m.text("authority"), Method: m.text("method"), Path: m.text("path"), APIVersion: m.text("api_version"), ModelID: m.text("model_id"), Region: m.text("region"), RequestedTier: m.text("requested_tier"), AllowedServedTiers: readScopedStrings(m, "allowed_served_tiers"), Speed: m.text("speed"), ContextMax: m.nat("context_max", true), InputMax: m.nat("input_max", true), OutputMax: m.nat("output_max", true), UsageSchema: m.text("usage_schema"), CacheClasses: readScopedStrings(m, "cache_classes"), ToolChoices: readScopedStrings(m, "tool_choices"), NoChargeRules: readScopedStrings(m, "no_charge_rules"), Tariff: graphObject(m, "tariff", readPaidTariff), VerifiedUTCNS: m.nat("verified_utc_ns", true), ExpiresUTCNS: m.nat("expires_utc_ns", true), Sources: readScopedObjects(m, "sources", func(b *members) ProfileSource {
			return ProfileSource{b.text("topic"), b.text("url"), b.nat("retrieved_utc_ns", true), b.text("quote")}
		}),
	}
}

func readPaidTariff(m *members) Tariff {
	return Tariff{m.text("currency"), readScopedObjects(m, "rates", func(b *members) TariffRate {
		return TariffRate{b.text("class"), b.text("numerator"), b.text("denominator")}
	})}
}

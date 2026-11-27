package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// SetupSubmission is schema 12's canonical owner request,
// proposed for freeze (D125). Arguments is the matching concrete value.
type SetupSubmission struct {
	Schema           int64       `json:"schema"`
	Actor            string      `json:"actor"`
	Operation        string      `json:"operation"`
	Target           SetupTarget `json:"target"`
	ExpectedRevision int64       `json:"expected_revision"`
	Arguments        any         `json:"arguments"`
}

// SetupTarget identifies the configured entity. AgentID is present exactly
// when Kind is context. Identity implies neither existence nor permission.
type SetupTarget struct {
	Kind    string    `json:"kind"`
	ID      uuid.UUID `json:"id"`
	AgentID uuid.UUID `json:"agent_id,omitzero"`
}

type SetupColonyCreateArguments struct{}

type SetupWorkspaceArguments struct {
	CaptureInputsSHA256 Digest `json:"capture_inputs_sha256"`
}

type SetupContextArguments struct {
	DisplayTitle         string          `json:"display_title"`
	TaskBinding          *TaskBinding    `json:"task_binding,omitempty"`
	RouteID              uuid.UUID       `json:"route_id"`
	ExpectedRouteVersion int64           `json:"expected_route_version"`
	CredentialScopeID    uuid.UUID       `json:"credential_scope_id"`
	MaxOutputTokens      int64           `json:"max_output_tokens"`
	TurnPolicy           SetupTurnPolicy `json:"turn_policy"`
}

// SetupTurnPolicy records a complete template. The codec requires positive
// integers, not the current host's policy maxima or a running turn's deadline.
type SetupTurnPolicy struct {
	MaxModelCalls       int64 `json:"max_model_calls"`
	MaxToolCalls        int64 `json:"max_tool_calls"`
	TimeoutMS           int64 `json:"timeout_ms"`
	MaxArgumentBytes    int64 `json:"max_argument_bytes"`
	MaxResultBytes      int64 `json:"max_result_bytes"`
	MaxDiscoveryEntries int64 `json:"max_discovery_entries"`
	MaxSearchEntries    int64 `json:"max_search_entries"`
	MaxSearchMatches    int64 `json:"max_search_matches"`
	MaxLiveBytes        int64 `json:"max_live_bytes"`
}

func (p SetupTurnPolicy) valid() bool {
	return p.MaxModelCalls > 0 && p.MaxToolCalls > 0 && p.TimeoutMS > 0 &&
		p.MaxArgumentBytes > 0 && p.MaxResultBytes > 0 && p.MaxDiscoveryEntries > 0 &&
		p.MaxSearchEntries > 0 && p.MaxSearchMatches > 0 && p.MaxLiveBytes > 0
}

type SetupStartupArguments struct {
	SettingsSHA256 Digest `json:"settings_sha256"`
	SettingsBytes  int64  `json:"settings_bytes"`
}

type SetupChecksArguments struct {
	BodySetSHA256 Digest `json:"body_set_sha256"`
	BodyCount     int64  `json:"body_count"`
}

func (p SetupSubmission) check() error {
	if p.Schema != 12 || p.Actor != "owner" || !isV7(p.Target.ID) || p.ExpectedRevision < 0 || p.ExpectedRevision == math.MaxInt64 {
		return errors.New("invalid setup schema, actor, target or expected revision")
	}
	if p.Target.Kind == "context" {
		if !isV7(p.Target.AgentID) {
			return errors.New("setup context target requires agent_id")
		}
	} else if p.Target.AgentID != (uuid.UUID{}) {
		return errors.New("setup non-context target forbids agent_id")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case SetupColonyCreateArguments:
		valid = p.Operation == "colony.create" && p.Target.Kind == "colony" && p.ExpectedRevision == 0
	case SetupWorkspaceArguments:
		valid = p.Target.Kind == "workspace" && a.CaptureInputsSHA256 != (Digest{}) &&
			(p.Operation == "workspace.register" && p.ExpectedRevision == 0 || p.Operation == "workspace.inputs" && p.ExpectedRevision >= 1)
	case SetupContextArguments:
		valid = p.Operation == "context.set" && p.Target.Kind == "context" &&
			len(a.DisplayTitle) >= 1 && len(a.DisplayTitle) <= 128 && utf8.ValidString(a.DisplayTitle) && !strings.ContainsFunc(a.DisplayTitle, unicode.IsControl) &&
			validTaskBinding(a.TaskBinding) && isV7(a.RouteID) && a.ExpectedRouteVersion > 0 && isV7(a.CredentialScopeID) && a.MaxOutputTokens > 0 && a.TurnPolicy.valid()
	case SetupStartupArguments:
		valid = p.Operation == "startup.set" && p.Target.Kind == "colony" && a.SettingsSHA256 != (Digest{}) && a.SettingsBytes > 0
	case SetupChecksArguments:
		valid = p.Operation == "checks.publish" && p.Target.Kind == "colony" && a.BodySetSHA256 != (Digest{}) && a.BodyCount > 0
	}
	if !valid {
		return errors.New("setup operation/target/argument mismatch or invalid arguments")
	}
	return nil
}

// Encode returns the compact known-field digest preimage in schema order.
func (p SetupSubmission) Encode() (json.RawMessage, error) {
	return encodeSetupPayload("schema-12 submission", p, p.check())
}

func DecodeSetupSubmission(js json.RawMessage) (SetupSubmission, error) {
	var p SetupSubmission
	err := decodeSetupPayload("schema-12 submission", js, func(m *members) error {
		p = readSetupSubmission(m)
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return SetupSubmission{}, err
	}
	return p, nil
}

func readSetupTarget(m *members) SetupTarget {
	p := SetupTarget{Kind: m.text("kind"), ID: m.id("id")}
	if p.Kind == "context" {
		p.AgentID = m.id("agent_id")
	} else {
		forbidScoped(m, "agent_id")
	}
	return p
}

func readSetupTurnPolicy(m *members) SetupTurnPolicy {
	return SetupTurnPolicy{
		MaxModelCalls: m.nat("max_model_calls", true), MaxToolCalls: m.nat("max_tool_calls", true),
		TimeoutMS: m.nat("timeout_ms", true), MaxArgumentBytes: m.nat("max_argument_bytes", true),
		MaxResultBytes: m.nat("max_result_bytes", true), MaxDiscoveryEntries: m.nat("max_discovery_entries", true),
		MaxSearchEntries: m.nat("max_search_entries", true), MaxSearchMatches: m.nat("max_search_matches", true),
		MaxLiveBytes: m.nat("max_live_bytes", true),
	}
}

// Unknown extensions are ignored, but known arguments of a different operation
// are forbidden even when null.
func setupArgumentMembers(m *members, allowed ...string) {
	for _, key := range []string{"capture_inputs_sha256", "display_title", "task_binding", "route_id", "expected_route_version", "credential_scope_id", "max_output_tokens", "turn_policy", "settings_sha256", "settings_bytes", "body_set_sha256", "body_count"} {
		if !slices.Contains(allowed, key) {
			forbidScoped(m, key)
		}
	}
}

func readSetupSubmission(m *members) SetupSubmission {
	p := SetupSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Operation: m.text("operation"), Target: graphObject(m, "target", readSetupTarget), ExpectedRevision: m.nat("expected_revision", true)}
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		switch p.Operation {
		case "colony.create":
			setupArgumentMembers(b)
			return SetupColonyCreateArguments{}
		case "workspace.register", "workspace.inputs":
			setupArgumentMembers(b, "capture_inputs_sha256")
			return SetupWorkspaceArguments{CaptureInputsSHA256: b.digest("capture_inputs_sha256")}
		case "context.set":
			setupArgumentMembers(b, "display_title", "task_binding", "route_id", "expected_route_version", "credential_scope_id", "max_output_tokens", "turn_policy")
			return SetupContextArguments{DisplayTitle: b.text("display_title"), TaskBinding: optionalObject(b, "task_binding", readTaskBinding), RouteID: b.id("route_id"), ExpectedRouteVersion: b.nat("expected_route_version", true), CredentialScopeID: b.id("credential_scope_id"), MaxOutputTokens: b.nat("max_output_tokens", true), TurnPolicy: graphObject(b, "turn_policy", readSetupTurnPolicy)}
		case "startup.set":
			setupArgumentMembers(b, "settings_sha256", "settings_bytes")
			return SetupStartupArguments{SettingsSHA256: b.digest("settings_sha256"), SettingsBytes: b.nat("settings_bytes", true)}
		case "checks.publish":
			setupArgumentMembers(b, "body_set_sha256", "body_count")
			return SetupChecksArguments{BodySetSHA256: b.digest("body_set_sha256"), BodyCount: b.nat("body_count", true)}
		default:
			b.err = errors.New("unknown setup operation")
			return nil
		}
	})
	return p
}

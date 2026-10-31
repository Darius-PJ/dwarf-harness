package terminal

import (
	"fmt"
	"math"
	"strconv"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// MaxSettingsBytes bounds the settings file before it is parsed.
const MaxSettingsBytes = 1 << 20

// GeneralTitle is the fixed display title of an agent's General context.
const GeneralTitle = "General work"

// Settings is the host's external, nonsecret startup configuration. It is
// never committed, and its credential source locators are never displayed.
type Settings struct {
	Contexts           []ContextSettings
	CredentialBindings []CredentialBinding
}

// ContextSettings selects one enabled context's fixed route, output ceiling,
// credential scope, public display title, task binding and turn policy.
type ContextSettings struct {
	AgentID, ContextID   uuid.UUID
	RouteID              uuid.UUID
	ExpectedRouteVersion int64
	CredentialScopeID    uuid.UUID
	MaxOutputTokens      int64
	DisplayTitle         string
	TaskBinding          *ledger.TaskBinding
	Policy               Policy
}

// Policy is the per-turn safety template. TimeoutMS replaces the absolute
// deadline, which Core computes once at admission.
type Policy struct {
	MaxModelCalls, MaxToolCalls, TimeoutMS                  int64
	MaxArgumentBytes, MaxResultBytes                        int64
	MaxDiscoveryEntries, MaxSearchEntries, MaxSearchMatches int64
	MaxLiveBytes                                            int64
}

// DefaultPolicy returns the finite testing defaults a missing turn_policy
// member takes. They are safety settings, not money, capability or measured fit.
func DefaultPolicy() Policy {
	return Policy{MaxModelCalls: 16, MaxToolCalls: 64, TimeoutMS: 600000, MaxArgumentBytes: 64 << 10, MaxResultBytes: 64 << 10, MaxDiscoveryEntries: 8, MaxSearchEntries: 4096, MaxSearchMatches: 100, MaxLiveBytes: 8 << 20}
}

// CredentialBinding is one explicit owner-selected binding performed at
// startup. SourceLocator is host-only: formatting omits it and SourceKind.
type CredentialBinding struct {
	ScopeID               uuid.UUID
	Provider, SourceKind  string
	SourceLocator         string
	RevokePreviousVersion uuid.UUID
}

// Format prints only the scope and provider, whatever the verb.
func (b CredentialBinding) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "{scope %s provider %s}", b.ScopeID, b.Provider)
}

// catalogueEntries is Core's closed model catalogue size; a discovery page
// cannot exceed it.
const catalogueEntries = 10

// DecodeSettings strictly decodes one settings file. Unknown members, null,
// duplicate members, string or fractional numbers and out-of-range values are
// refused with the member path and allowed form, never the supplied value.
func DecodeSettings(raw []byte) (Settings, error) {
	r := &reader{kind: ErrSettings}
	if len(raw) > MaxSettingsBytes {
		return Settings{}, &RefusalError{Kind: ErrSettings, Path: "$", Allowed: "at most 1 MiB"}
	}
	root, err := parseStrict(raw)
	if err != nil {
		return Settings{}, &RefusalError{Kind: ErrSettings, Path: "$", Allowed: "one UTF-8 JSON object without duplicate members, null, deep nesting or trailing data"}
	}
	var s Settings
	if !r.object(root, "$", []string{"schema", "contexts", "credential_bindings"}, nil) {
		return Settings{}, r.err
	}
	r.integer(root.members["schema"], "schema", 1, 1)
	for i, item := range r.array(root.members["contexts"], "contexts", 1) {
		s.Contexts = append(s.Contexts, r.context(item, index("contexts", i)))
	}
	for i, item := range r.array(root.members["credential_bindings"], "credential_bindings", 1) {
		path := index("credential_bindings", i)
		if !r.object(item, path, []string{"scope_id", "provider", "source_kind", "source_locator"}, []string{"revoke_previous_version"}) {
			break
		}
		b := CredentialBinding{
			ScopeID:       r.id(item.members["scope_id"], join(path, "scope_id")),
			Provider:      r.choice(item.members["provider"], join(path, "provider"), "anthropic", "openai"),
			SourceKind:    r.choice(item.members["source_kind"], join(path, "source_kind"), "environment", "windows_credential_manager"),
			SourceLocator: r.text(item.members["source_locator"], join(path, "source_locator"), 1024),
		}
		if v := item.members["revoke_previous_version"]; v != nil {
			b.RevokePreviousVersion = r.id(v, join(path, "revoke_previous_version"))
		}
		s.CredentialBindings = append(s.CredentialBindings, b)
	}
	if r.err != nil {
		return Settings{}, r.err
	}
	contexts, scopes := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for i, c := range s.Contexts {
		if contexts[c.ContextID] {
			r.fail(join(index("contexts", i), "context_id"), "a context named once")
		}
		contexts[c.ContextID] = true
	}
	for i, b := range s.CredentialBindings {
		if scopes[b.ScopeID] {
			r.fail(join(index("credential_bindings", i), "scope_id"), "a scope bound once")
		}
		scopes[b.ScopeID] = true
	}
	used := map[uuid.UUID]bool{}
	for i, c := range s.Contexts {
		if !scopes[c.CredentialScopeID] {
			r.fail(join(index("contexts", i), "credential_scope_id"), "a scope named by credential_bindings")
		}
		used[c.CredentialScopeID] = true
	}
	for i, b := range s.CredentialBindings {
		if !used[b.ScopeID] {
			r.fail(join(index("credential_bindings", i), "scope_id"), "a scope some context uses")
		}
	}
	if r.err != nil {
		return Settings{}, r.err
	}
	return s, nil
}

func (r *reader) context(n *node, path string) ContextSettings {
	if !r.object(n, path, []string{"agent_id", "context_id", "route_id", "expected_route_version", "credential_scope_id", "max_output_tokens", "display_title", "turn_policy"}, []string{"task_binding"}) {
		return ContextSettings{}
	}
	m := n.members
	c := ContextSettings{
		AgentID:              r.id(m["agent_id"], join(path, "agent_id")),
		ContextID:            r.id(m["context_id"], join(path, "context_id")),
		RouteID:              r.id(m["route_id"], join(path, "route_id")),
		ExpectedRouteVersion: r.integer(m["expected_route_version"], join(path, "expected_route_version"), 1, math.MaxInt64),
		CredentialScopeID:    r.id(m["credential_scope_id"], join(path, "credential_scope_id")),
		MaxOutputTokens:      r.integer(m["max_output_tokens"], join(path, "max_output_tokens"), 1, math.MaxInt32),
		DisplayTitle:         r.text(m["display_title"], join(path, "display_title"), 128),
	}
	if v := m["task_binding"]; v != nil {
		p := join(path, "task_binding")
		if r.object(v, p, []string{"task_id", "assignment_id", "generation"}, nil) {
			c.TaskBinding = &ledger.TaskBinding{TaskID: r.id(v.members["task_id"], join(p, "task_id")), AssignmentID: r.id(v.members["assignment_id"], join(p, "assignment_id")), Generation: r.integer(v.members["generation"], join(p, "generation"), 1, math.MaxInt64)}
		}
	} else if r.err == nil && c.DisplayTitle != GeneralTitle {
		// Only the General context has no task binding, and its title is fixed.
		r.fail(join(path, "display_title"), strconv.Quote(GeneralTitle)+" when task_binding is absent")
	}
	c.Policy = r.policy(m["turn_policy"], join(path, "turn_policy"))
	return c
}

// policy applies DefaultPolicy to each missing member and refuses values
// outside Core's template bounds, which EnableTurnLoop checks again.
func (r *reader) policy(n *node, path string) Policy {
	p := DefaultPolicy()
	members := []struct {
		name     string
		field    *int64
		min, max int64
	}{
		{"max_model_calls", &p.MaxModelCalls, 1, math.MaxInt32},
		{"max_tool_calls", &p.MaxToolCalls, 1, math.MaxInt32},
		{"timeout_ms", &p.TimeoutMS, 1, math.MaxInt64 / 1000000},
		{"max_argument_bytes", &p.MaxArgumentBytes, 1, 64 << 10},
		{"max_result_bytes", &p.MaxResultBytes, 512, 64 << 10},
		{"max_discovery_entries", &p.MaxDiscoveryEntries, 1, catalogueEntries},
		{"max_search_entries", &p.MaxSearchEntries, 1, math.MaxInt32},
		{"max_search_matches", &p.MaxSearchMatches, 1, math.MaxInt32},
		{"max_live_bytes", &p.MaxLiveBytes, 1, 8 << 20},
	}
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.name
	}
	if !r.object(n, path, nil, names) {
		return Policy{}
	}
	for _, m := range members {
		if v := n.members[m.name]; v != nil {
			*m.field = r.integer(v, join(path, m.name), m.min, m.max)
		}
	}
	if r.err == nil && p.MaxSearchMatches > p.MaxSearchEntries {
		r.fail(join(path, "max_search_matches"), "at most max_search_entries")
	}
	return p
}

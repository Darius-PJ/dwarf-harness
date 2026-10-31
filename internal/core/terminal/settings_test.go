package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

const (
	testAgent   = "019a3f00-0000-7000-8000-00000000a001"
	testGeneral = "019a3f00-0000-7000-8000-00000000c001"
	testWork    = "019a3f00-0000-7000-8000-00000000c002"
	testRoute   = "019a3f00-0000-7000-8000-00000000d001"
	testScope   = "019a3f00-0000-7000-8000-00000000e001"
	testTask    = "019a3f00-0000-7000-8000-00000000f001"
	testAssign  = "019a3f00-0000-7000-8000-00000000f002"
	testPrior   = "019a3f00-0000-7000-8000-00000000f003"
	// A synthetic source name that matches no provider's credential variable.
	testLocator = "DWARF_TERMINAL_SYNTHETIC_SOURCE"
)

// settingsJSON builds a settings document from editable maps so each row
// changes exactly one member of an otherwise valid file.
func settingsJSON(t testing.TB, edit func(root, general, work, policy, binding map[string]any)) []byte {
	t.Helper()
	policy := map[string]any{}
	general := map[string]any{"agent_id": testAgent, "context_id": testGeneral, "route_id": testRoute, "expected_route_version": 1, "credential_scope_id": testScope, "max_output_tokens": 4096, "display_title": GeneralTitle, "turn_policy": map[string]any{}}
	work := map[string]any{"agent_id": testAgent, "context_id": testWork, "route_id": testRoute, "expected_route_version": 2, "credential_scope_id": testScope, "max_output_tokens": 2048, "display_title": "Synthetic task", "task_binding": map[string]any{"task_id": testTask, "assignment_id": testAssign, "generation": 3}, "turn_policy": policy}
	binding := map[string]any{"scope_id": testScope, "provider": "openai", "source_kind": "environment", "source_locator": testLocator, "revoke_previous_version": testPrior}
	root := map[string]any{"schema": 1, "contexts": []any{general, work}, "credential_bindings": []any{binding}}
	if edit != nil {
		edit(root, general, work, policy, binding)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Every refusal names a path and an allowed form; none may echo the value, so
// each refused value below carries this marker.
const refusedMarker = "refused-value-marker"

func TestTerminalSettings(t *testing.T) {
	t.Parallel()
	t.Run("defaults_and_explicit_values", func(t *testing.T) {
		t.Parallel()
		s, err := DecodeSettings(settingsJSON(t, func(_, _, _, policy, _ map[string]any) {
			policy["max_model_calls"], policy["max_result_bytes"], policy["max_discovery_entries"], policy["max_search_entries"], policy["max_search_matches"] = 2, 512, 10, 7, 7
		}))
		if err != nil {
			t.Fatal(err)
		}
		if s.Contexts[0].Policy != DefaultPolicy() {
			t.Fatal("an empty turn_policy did not take exactly the testing defaults")
		}
		want := DefaultPolicy()
		want.MaxModelCalls, want.MaxResultBytes, want.MaxDiscoveryEntries, want.MaxSearchEntries, want.MaxSearchMatches = 2, 512, 10, 7, 7
		if s.Contexts[1].Policy != want {
			t.Fatal("explicit policy members were not kept beside defaults for the rest")
		}
		work := s.Contexts[1]
		if work.TaskBinding == nil || *work.TaskBinding != (ledger.TaskBinding{TaskID: uuid.MustParse(testTask), AssignmentID: uuid.MustParse(testAssign), Generation: 3}) || work.ExpectedRouteVersion != 2 || work.MaxOutputTokens != 2048 || work.DisplayTitle != "Synthetic task" {
			t.Fatal("work context values changed in decoding")
		}
		b := s.CredentialBindings[0]
		if b.ScopeID != uuid.MustParse(testScope) || b.Provider != "openai" || b.SourceKind != "environment" || b.SourceLocator != testLocator || b.RevokePreviousVersion != uuid.MustParse(testPrior) {
			t.Fatal("credential binding values changed in decoding")
		}
	})
	type row struct {
		name, path string
		edit       func(root, general, work, policy, binding map[string]any)
	}
	rows := []row{
		{"unknown_root_member", "$", func(root, _, _, _, _ map[string]any) { root[refusedMarker] = 1 }},
		{"unknown_context_member", "contexts[0]", func(_, general, _, _, _ map[string]any) { general[refusedMarker] = 1 }},
		{"unknown_policy_member", "contexts[1].turn_policy", func(_, _, _, policy, _ map[string]any) { policy["deadline_utc_ns"] = 1 }},
		{"unknown_binding_member", "credential_bindings[0]", func(_, _, _, _, binding map[string]any) { binding["source_value"] = refusedMarker }},
		{"null_member", "$", func(_, _, work, _, _ map[string]any) { work["task_binding"] = nil }},
		{"string_number", "contexts[0].max_output_tokens", func(_, general, _, _, _ map[string]any) { general["max_output_tokens"] = "4096" }},
		{"fraction", "contexts[1].turn_policy.max_tool_calls", func(_, _, _, policy, _ map[string]any) { policy["max_tool_calls"] = 1.5 }},
		{"exponent", "contexts[1].turn_policy.timeout_ms", func(_, _, _, policy, _ map[string]any) { policy["timeout_ms"] = json.Number("6e5") }},
		{"zero_count", "contexts[1].turn_policy.max_model_calls", func(_, _, _, policy, _ map[string]any) { policy["max_model_calls"] = 0 }},
		{"result_below_floor", "contexts[1].turn_policy.max_result_bytes", func(_, _, _, policy, _ map[string]any) { policy["max_result_bytes"] = 511 }},
		{"result_above_cap", "contexts[1].turn_policy.max_result_bytes", func(_, _, _, policy, _ map[string]any) { policy["max_result_bytes"] = 64<<10 + 1 }},
		{"discovery_above_catalogue", "contexts[1].turn_policy.max_discovery_entries", func(_, _, _, policy, _ map[string]any) { policy["max_discovery_entries"] = 11 }},
		{"live_above_cap", "contexts[1].turn_policy.max_live_bytes", func(_, _, _, policy, _ map[string]any) { policy["max_live_bytes"] = 8<<20 + 1 }},
		{"matches_above_entries", "contexts[1].turn_policy.max_search_matches", func(_, _, _, policy, _ map[string]any) {
			policy["max_search_entries"], policy["max_search_matches"] = 5, 6
		}},
		{"uppercase_id", "contexts[0].agent_id", func(_, general, _, _, _ map[string]any) { general["agent_id"] = strings.ToUpper(testAgent) }},
		{"braced_id", "contexts[0].route_id", func(_, general, _, _, _ map[string]any) { general["route_id"] = "{" + testRoute + "}" }},
		{"version_4_id", "contexts[0].context_id", func(_, general, _, _, _ map[string]any) {
			general["context_id"] = "019a3f00-0000-4000-8000-00000000c001"
		}},
		{"missing_policy", "contexts[0].turn_policy", func(_, general, _, _, _ map[string]any) { delete(general, "turn_policy") }},
		{"general_title", "contexts[0].display_title", func(_, general, _, _, _ map[string]any) { general["display_title"] = refusedMarker }},
		{"title_control", "contexts[1].display_title", func(_, _, work, _, _ map[string]any) { work["display_title"] = refusedMarker + "\u001b]0;x\u0007" }},
		{"title_too_long", "contexts[1].display_title", func(_, _, work, _, _ map[string]any) {
			work["display_title"] = refusedMarker + strings.Repeat("x", 128)
		}},
		{"zero_generation", "contexts[1].task_binding.generation", func(_, _, work, _, _ map[string]any) {
			work["task_binding"] = map[string]any{"task_id": testTask, "assignment_id": testAssign, "generation": 0}
		}},
		{"duplicate_context", "contexts[1].context_id", func(_, _, work, _, _ map[string]any) { work["context_id"] = testGeneral }},
		{"unbound_scope", "contexts[1].credential_scope_id", func(_, _, work, _, _ map[string]any) { work["credential_scope_id"] = testPrior }},
		{"unused_binding", "credential_bindings[1].scope_id", func(root, _, _, _, binding map[string]any) {
			extra := maps.Clone(binding)
			extra["scope_id"] = testPrior
			root["credential_bindings"] = []any{binding, extra}
		}},
		{"duplicate_scope", "credential_bindings[1].scope_id", func(root, _, _, _, binding map[string]any) {
			root["credential_bindings"] = []any{binding, binding}
		}},
		{"provider", "credential_bindings[0].provider", func(_, _, _, _, binding map[string]any) { binding["provider"] = refusedMarker }},
		{"source_kind", "credential_bindings[0].source_kind", func(_, _, _, _, binding map[string]any) { binding["source_kind"] = refusedMarker }},
		{"empty_locator", "credential_bindings[0].source_locator", func(_, _, _, _, binding map[string]any) { binding["source_locator"] = "" }},
		{"schema", "schema", func(root, _, _, _, _ map[string]any) { root["schema"] = 2 }},
		{"no_contexts", "contexts", func(root, _, _, _, _ map[string]any) { root["contexts"] = []any{} }},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeSettings(settingsJSON(t, r.edit))
			var refusal *RefusalError
			if !errors.Is(err, ErrSettings) || !errors.As(err, &refusal) || refusal.Path != r.path {
				t.Fatalf("got %v, want a refusal at %s", err, r.path)
			}
			if strings.Contains(err.Error(), refusedMarker) || strings.Contains(err.Error(), testLocator) {
				t.Fatal("a refusal echoed a supplied value")
			}
		})
	}
	valid := settingsJSON(t, nil)
	for name, raw := range map[string][]byte{
		"duplicate_member": bytes.Replace(valid, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1),
		"trailing_data":    append(append([]byte{}, valid...), []byte(` {}`)...),
		"invalid_utf8":     bytes.Replace(valid, []byte("Synthetic task"), []byte("Synthetic \xff"), 1),
		"over_1_mib":       append(append([]byte{}, valid[:len(valid)-1]...), []byte(`,"pad":"`+strings.Repeat("x", MaxSettingsBytes)+`"}`)...),
		"deep_nesting":     []byte(strings.Repeat(`{"a":`, 12) + "1" + strings.Repeat("}", 12)),
		"not_an_object":    []byte(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeSettings(raw); !errors.Is(err, ErrSettings) {
				t.Fatalf("malformed settings accepted: %v", err)
			}
		})
	}
}

// The locator names a source; its value is the credential. Neither may reach
// startup output or formatted settings. Process environment: sequential.
func TestTerminalSettingsRedaction(t *testing.T) {
	const value = "synthetic-terminal-source-value"
	t.Setenv(testLocator, value)
	s, err := DecodeSettings(settingsJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	version, other := uuid.NewV7(), uuid.MustParse(testPrior)
	var out bytes.Buffer
	if err := WriteStartup(&out, s, []ScopeBinding{{ScopeID: s.CredentialBindings[0].ScopeID, Provider: "openai", Version: version, Others: []uuid.UUID{other}}}); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&out, "%v %+v %#v %s", s, s, s.CredentialBindings, s.CredentialBindings[0])
	text := out.String()
	for _, secret := range []string{testLocator, value, "environment"} {
		if strings.Contains(text, secret) {
			t.Fatal("startup output or settings formatting disclosed a credential source")
		}
	}
	for _, id := range []string{testScope, version.String(), other.String(), testRoute, short(uuid.MustParse(testWork))} {
		if !strings.Contains(text, id) {
			t.Fatal("startup output omitted a binding or context identity the owner needs")
		}
	}
}

func TestTerminalCommandJSON(t *testing.T) {
	t.Parallel()
	command := uuid.MustParse("019a3f00-0000-7000-8000-0000000b0001")
	turn := uuid.MustParse("019a3f00-0000-7000-8000-0000000b0002")
	digest := ledger.Digest{0xab, 0xcd, 0xef}
	accepted := map[string]struct {
		raw  string
		want any
	}{
		"multiline_send_with_artifact": {`{"schema":1,"command_id":"` + command.String() + `","context_id":"` + testGeneral + `","operation":"message.send","arguments":{"turn_id":"` + turn.String() + `","text":"first line\nsecond \"line\"","artifacts":[{"source_sequence":7,"sha256":"` + digest.String() + `","bytes":3}]}}`,
			session.MessageSend{TurnID: turn, Text: "first line\nsecond \"line\"", Artifacts: []ledger.ArtifactRef{{SourceSequence: 7, SHA256: digest, Bytes: 3}}}},
		"send_without_artifacts": {`{"schema":1,"command_id":"` + command.String() + `","context_id":"` + testGeneral + `","operation":"message.send","arguments":{"turn_id":"` + turn.String() + `","text":""}}`,
			session.MessageSend{TurnID: turn, Text: "", Artifacts: []ledger.ArtifactRef{}}},
		"cancel":         {`{"schema":1,"command_id":"` + command.String() + `","context_id":"` + testGeneral + `","operation":"turn.cancel","arguments":{"turn_id":"` + turn.String() + `"}}`, session.TurnCancel{TurnID: turn}},
		"context_status": {`{"schema":1,"command_id":"` + command.String() + `","context_id":"` + testGeneral + `","operation":"context.status","arguments":{}}`, session.ContextStatus{}},
	}
	for name, a := range accepted {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := DecodeCommand([]byte(a.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got.Schema != 1 || got.CommandID != command || got.ContextID != uuid.MustParse(testGeneral) || !reflect.DeepEqual(got.Arguments, a.want) {
				t.Fatalf("decoded command changed meaning: %+v", got)
			}
		})
	}
	base := `{"schema":1,"command_id":"` + command.String() + `","context_id":"` + testGeneral + `",`
	refused := map[string]struct{ raw, path string }{
		"unknown_operation":      {base + `"operation":"tools.invoke","arguments":{}}`, "operation"},
		"argument_mismatch":      {base + `"operation":"context.status","arguments":{"turn_id":"` + turn.String() + `"}}`, "arguments"},
		"unknown_argument":       {base + `"operation":"turn.cancel","arguments":{"turn_id":"` + turn.String() + `","reason":"` + refusedMarker + `"}}`, "arguments"},
		"null_text":              {base + `"operation":"message.send","arguments":{"turn_id":"` + turn.String() + `","text":null}}`, "$"},
		"number_text":            {base + `"operation":"message.send","arguments":{"turn_id":"` + turn.String() + `","text":7}}`, "arguments.text"},
		"string_sequence":        {base + `"operation":"message.send","arguments":{"turn_id":"` + turn.String() + `","text":"x","artifacts":[{"source_sequence":"7","sha256":"` + digest.String() + `","bytes":3}]}}`, "arguments.artifacts[0].source_sequence"},
		"uppercase_digest":       {base + `"operation":"message.send","arguments":{"turn_id":"` + turn.String() + `","text":"x","artifacts":[{"source_sequence":7,"sha256":"` + strings.ToUpper(digest.String()) + `","bytes":3}]}}`, "arguments.artifacts[0].sha256"},
		"version_4_turn":         {base + `"operation":"turn.status","arguments":{"turn_id":"019a3f00-0000-4000-8000-0000000b0002"}}`, "arguments.turn_id"},
		"missing_arguments":      {base + `"operation":"turn.status"}`, "arguments"},
		"schema_two":             {`{"schema":2,"command_id":"` + command.String() + `","context_id":"` + testGeneral + `","operation":"context.status","arguments":{}}`, "schema"},
		"duplicate_member":       {base + `"operation":"context.status","operation":"turn.status","arguments":{}}`, "$"},
		"trailing_shell_payload": {base + `"operation":"context.status","arguments":{}}; ` + refusedMarker, "$"},
	}
	for name, r := range refused {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeCommand([]byte(r.raw))
			var refusal *RefusalError
			if !errors.Is(err, ErrCommand) || !errors.As(err, &refusal) || refusal.Path != r.path {
				t.Fatalf("got %v, want a refusal at %s", err, r.path)
			}
			if strings.Contains(err.Error(), refusedMarker) {
				t.Fatal("a refusal echoed a supplied value")
			}
		})
	}
}

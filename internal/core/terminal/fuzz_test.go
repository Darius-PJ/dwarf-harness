package terminal

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

// encodeSettings writes accepted settings back in the file's own schema.
func encodeSettings(t *testing.T, s Settings) []byte {
	t.Helper()
	type policy struct {
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
	type task struct {
		TaskID       uuid.UUID `json:"task_id"`
		AssignmentID uuid.UUID `json:"assignment_id"`
		Generation   int64     `json:"generation"`
	}
	type context struct {
		AgentID              uuid.UUID `json:"agent_id"`
		ContextID            uuid.UUID `json:"context_id"`
		RouteID              uuid.UUID `json:"route_id"`
		ExpectedRouteVersion int64     `json:"expected_route_version"`
		CredentialScopeID    uuid.UUID `json:"credential_scope_id"`
		MaxOutputTokens      int64     `json:"max_output_tokens"`
		DisplayTitle         string    `json:"display_title"`
		TaskBinding          *task     `json:"task_binding,omitempty"`
		TurnPolicy           policy    `json:"turn_policy"`
	}
	type binding struct {
		ScopeID               uuid.UUID `json:"scope_id"`
		Provider              string    `json:"provider"`
		SourceKind            string    `json:"source_kind"`
		SourceLocator         string    `json:"source_locator"`
		RevokePreviousVersion uuid.UUID `json:"revoke_previous_version,omitzero"`
	}
	out := struct {
		Schema             int       `json:"schema"`
		Contexts           []context `json:"contexts"`
		CredentialBindings []binding `json:"credential_bindings"`
	}{Schema: 1}
	for _, c := range s.Contexts {
		e := context{c.AgentID, c.ContextID, c.RouteID, c.ExpectedRouteVersion, c.CredentialScopeID, c.MaxOutputTokens, c.DisplayTitle, nil, policy(c.Policy)}
		if c.TaskBinding != nil {
			e.TaskBinding = &task{c.TaskBinding.TaskID, c.TaskBinding.AssignmentID, c.TaskBinding.Generation}
		}
		out.Contexts = append(out.Contexts, e)
	}
	for _, b := range s.CredentialBindings {
		out.CredentialBindings = append(out.CredentialBindings, binding(b))
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Accepted settings must keep every documented invariant and decode to the
// same value from their canonical form; refusals must stay typed.
func FuzzTerminalSettings(f *testing.F) {
	f.Add(settingsJSON(f, nil))
	f.Add(settingsJSON(f, func(_, _, _, policy, _ map[string]any) {
		policy["max_search_entries"], policy["max_search_matches"] = 1, 1
	}))
	f.Add(settingsJSON(f, func(_, general, _, _, _ map[string]any) { general["max_output_tokens"] = "4096" }))
	f.Add(settingsJSON(f, func(_, _, work, _, _ map[string]any) { delete(work, "task_binding") }))
	f.Add([]byte(`{"schema":1,"schema":1}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := DecodeSettings(raw)
		if err != nil {
			var refusal *RefusalError
			if !errors.Is(err, ErrSettings) || !errors.As(err, &refusal) || refusal.Path == "" {
				t.Fatal("untyped settings refusal", err)
			}
			return
		}
		scopes := map[uuid.UUID]bool{}
		for _, b := range s.CredentialBindings {
			scopes[b.ScopeID] = true
		}
		for _, c := range s.Contexts {
			p := c.Policy
			if !scopes[c.CredentialScopeID] || c.TaskBinding == nil && c.DisplayTitle != GeneralTitle || p.MaxResultBytes < 512 || p.MaxResultBytes > 64<<10 || p.MaxDiscoveryEntries > catalogueEntries || p.MaxLiveBytes > 8<<20 || p.MaxSearchMatches > p.MaxSearchEntries || p.MaxModelCalls < 1 || p.TimeoutMS < 1 {
				t.Fatal("accepted settings break a documented invariant")
			}
		}
		again, err := DecodeSettings(encodeSettings(t, s))
		if err != nil || !reflect.DeepEqual(again, s) {
			t.Fatal("accepted settings changed meaning through their canonical form", err)
		}
	})
}

// An accepted /send command must be well-paired and survive JSON round trips
// unchanged, so one command ID cannot carry a second meaning.
func FuzzTerminalCommandJSON(f *testing.F) {
	command, turn := "019a3f00-0000-7000-8000-0000000b0001", "019a3f00-0000-7000-8000-0000000b0002"
	head := `{"schema":1,"command_id":"` + command + `","context_id":"` + testGeneral + `",`
	f.Add([]byte(head + `"operation":"message.send","arguments":{"turn_id":"` + turn + `","text":"a\nb","artifacts":[{"source_sequence":7,"sha256":"0102030000000000000000000000000000000000000000000000000000000000","bytes":3}]}}`))
	f.Add([]byte(head + `"operation":"turn.cancel","arguments":{"turn_id":"` + turn + `"}}`))
	f.Add([]byte(head + `"operation":"turn.status","arguments":{"turn_id":"` + turn + `"}}`))
	f.Add([]byte(head + `"operation":"context.status","arguments":{}}`))
	f.Add([]byte(head + `"operation":"promotion.request","arguments":{"workspace_id":"` + turn + `","head":"` + command + `","restart_from":"` + turn + `"}}`))
	f.Add([]byte(head + `"operation":"context.status","arguments":{"turn_id":"` + turn + `"}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := DecodeCommand(raw)
		if err != nil {
			var refusal *RefusalError
			if !errors.Is(err, ErrCommand) || !errors.As(err, &refusal) || refusal.Path == "" {
				t.Fatal("untyped command refusal", err)
			}
			return
		}
		pairs := map[string]any{"message.send": session.MessageSend{}, "turn.cancel": session.TurnCancel{}, "turn.status": session.TurnStatus{}, "context.status": session.ContextStatus{}, "promotion.request": session.PromotionRequest{}}
		if c.Schema != 1 || reflect.TypeOf(c.Arguments) != reflect.TypeOf(pairs[c.Operation]) {
			t.Fatal("accepted command pairs an operation with another value")
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodeCommand(encoded)
		if err != nil || !reflect.DeepEqual(again, c) {
			t.Fatal("accepted command changed meaning through JSON", err)
		}
	})
}

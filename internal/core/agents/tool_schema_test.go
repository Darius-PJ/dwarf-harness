package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func TestToolSchemaClosedDefinitions(t *testing.T) {
	t.Parallel()
	want := []string{"file_read_v1", "file_search_v1", "file_write_v1", "file_edit_v1", "core_status_v1", "core_task_v1", "core_memory_v1", "tools_discover_v1", "tools_invoke_v1"}
	definitions := modelToolDefinitions()
	names := make([]string, len(definitions))
	for i, tool := range definitions {
		names[i] = tool.Name
	}
	if !slices.Equal(names, want) {
		t.Fatal("model-visible vocabulary changed")
	}
	catalogueNames := make([]string, len(modelCatalogue))
	for i, tool := range modelCatalogue {
		catalogueNames[i] = tool.name
	}
	if !slices.Equal(catalogueNames, []string{"core.task.create", "core.task.claim", "core.task.assign", "core.task.complete", "core.task.fail", "core.task.reassign", "core.task.list", "core.context.read", "core.context.handoff.write", "core.artifact.read"}) {
		t.Fatal("closed discovery catalogue changed")
	}
	f := newPaidFixture(t)
	for _, provider := range []string{"anthropic", "openai"} {
		p := providerProfile(f.route.Profile)
		compile := providers.CompileAnthropic
		if provider == "openai" {
			p = paidOpenAIProfile(p)
			compile = providers.CompileOpenAI
		}
		request := providers.Request{MaxTokens: 16, Messages: []providers.Message{{Role: "user", Content: []providers.Block{{Type: "text", Text: "synthetic"}}}}, Tools: definitions}
		compiled, err := compile(p, request)
		if err != nil {
			t.Fatalf("provider refused tool definitions: %v", err)
		}
		var body struct {
			Tools []map[string]json.RawMessage `json:"tools"`
		}
		if json.Unmarshal(compiled.Body(), &body) != nil || len(body.Tools) != 9 {
			t.Fatal("provider lost local tools")
		}
		for i, tool := range body.Tools {
			member := "input_schema"
			if provider == "openai" {
				member = "parameters"
			}
			var compact bytes.Buffer
			if json.Compact(&compact, definitions[i].InputSchema) != nil || !bytes.Equal(tool[member], compact.Bytes()) {
				t.Fatal("provider changed concrete parameter schema")
			}
			var decoded map[string]any
			if json.Unmarshal(tool[member], &decoded) != nil {
				t.Fatal("invalid published schema")
			}
			assertClosedModelSchema(t, decoded)
		}
	}
	// Returned schemas are caller-owned; changing one request cannot change the
	// vocabulary or accepted meaning of a later sealed request.
	first := bytes.Clone(definitions[0].InputSchema)
	definitions[0].InputSchema[0] = '!'
	if !bytes.Equal(first, modelToolDefinitions()[0].InputSchema) {
		t.Fatal("tool definitions share writable request storage")
	}
}

func assertClosedModelSchema(t testing.TB, s map[string]any) {
	t.Helper()
	if choices, ok := s["oneOf"].([]any); ok {
		for _, choice := range choices {
			assertClosedModelSchema(t, choice.(map[string]any))
		}
		return
	}
	if s["type"] == "object" {
		if s["additionalProperties"] != false {
			t.Fatal("model schema exposes unbounded object members")
		}
		for _, child := range s["properties"].(map[string]any) {
			assertClosedModelSchema(t, child.(map[string]any))
		}
	}
	if item, ok := s["items"].(map[string]any); ok {
		assertClosedModelSchema(t, item)
	}
}

func TestRequestStorageBoundReplay(t *testing.T) {
	t.Parallel()
	request := providers.Request{Messages: []providers.Message{{Role: "assistant", Content: []providers.Block{{Type: "anthropic_replay", Replay: json.RawMessage(`[{"type":"text","text":"a"}]`)}}}}}
	bound, err := requestStorageBound(request)
	if err != nil || bound < int64(len(request.Messages[0].Content[0].Replay)) {
		t.Fatal("bounded replay cannot be reserved")
	}
	request.Messages[0].Content[0].Replay = bytes.Repeat([]byte(" "), providers.RequestLimit)
	if _, err := requestStorageBound(request); !errors.Is(err, ErrTooLarge) {
		t.Fatal("opaque replay bypassed storage admission")
	}
	request.Messages = append(request.Messages, request.Messages[0])
	request.Messages[0].Content[0].Replay = bytes.Repeat([]byte(" "), providers.RequestLimit/2)
	if _, err := requestStorageBound(request); !errors.Is(err, ErrTooLarge) {
		t.Fatal("aggregate replay bypassed storage admission")
	}
}

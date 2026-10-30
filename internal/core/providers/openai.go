package providers

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"
)

type openAIText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIMessage struct {
	Type    string       `json:"type"`
	Role    string       `json:"role"`
	Content []openAIText `json:"content"`
}

type openAIFunction struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type openAIRequest struct {
	// The leading fields and input are encoded separately to preserve replay.
	Stream             bool     `json:"stream"`
	Store              bool     `json:"store"`
	ServiceTier        string   `json:"service_tier"`
	Truncation         string   `json:"truncation"`
	Background         bool     `json:"background"`
	Include            []string `json:"include"`
	PromptCacheOptions struct {
		Mode    string `json:"mode"`
		Prewarm bool   `json:"prewarm"`
		TTL     string `json:"ttl"`
	} `json:"prompt_cache_options"`
	Instructions string `json:"instructions,omitempty"`
	Text         struct {
		Format struct {
			Type string `json:"type"`
		} `json:"format"`
	} `json:"text"`
	Tools             []openAIFunction `json:"tools,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool            `json:"parallel_tool_calls,omitempty"`
}

// CompileOpenAI serializes stateless text and same-provider stored output.
// Hosted tools, previous-response IDs and cache markers remain unsupported.
func CompileOpenAI(p Profile, r Request) (Compiled, error) {
	var c Compiled
	if p.Provider != "openai" {
		return c, ErrUnsupported
	}
	if err := p.Validate(); err != nil {
		return c, err
	}
	if r.MaxTokens < 16 || r.MaxTokens > p.OutputMax || len(r.Messages) == 0 || r.Messages[len(r.Messages)-1].Role != "user" {
		return c, ErrUnsupported
	}
	w := openAIRequest{Stream: true, ServiceTier: "default", Truncation: "disabled", Include: []string{"reasoning.encrypted_content"}}
	w.PromptCacheOptions.Mode = "implicit"
	w.PromptCacheOptions.TTL = "30m"
	w.Text.Format.Type = "text"
	var input []json.RawMessage
	var pending []string
	calls, itemIDs := map[string]bool{}, map[string]bool{}
	for _, message := range r.Messages {
		if message.Role != "user" && message.Role != "assistant" || len(message.Content) == 0 || len(pending) > 0 && message.Role != "user" {
			return c, ErrUnsupported
		}
		if message.Content[0].Type == "openai_replay" {
			b := message.Content[0]
			if message.Role != "assistant" || len(message.Content) != 1 || !replayOnly(b) {
				return c, ErrUnsupported
			}
			items, ids, err := openAIReplayItems(b.Replay, itemIDs)
			if err != nil {
				return c, err
			}
			for _, id := range ids {
				if calls[id] {
					return c, ErrUnsupported
				}
				calls[id] = true
			}
			pending = ids
			input = append(input, items...)
			continue
		}
		m := openAIMessage{Type: "message", Role: message.Role}
		for _, b := range message.Content {
			if b.Type == "tool_result" {
				if message.Role != "user" || len(pending) == 0 || pending[0] != b.ToolUseID || len(m.Content) != 0 || len(b.Content) != 1 || b.Content[0].Type != "text" || b.Content[0].CacheControl != nil || !utf8.ValidString(b.Content[0].Text) || len(b.Content[0].Text) > 64<<10 || b.Text != "" || b.ID != "" || b.Name != "" || b.Input != nil || b.CacheControl != nil || b.Replay != nil {
					return c, ErrUnsupported
				}
				raw, err := compact(struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Output string `json:"output"`
				}{"function_call_output", b.ToolUseID, b.Content[0].Text})
				if err != nil {
					return c, err
				}
				input = append(input, raw)
				pending = pending[1:]
				continue
			}
			if len(pending) != 0 || b.Type != "text" || !validText(b.Text) || b.ID != "" || b.Name != "" || b.Input != nil || b.ToolUseID != "" || b.Content != nil || b.IsError != nil || b.CacheControl != nil || b.Replay != nil {
				return c, ErrUnsupported
			}
			m.Content = append(m.Content, openAIText{"input_text", b.Text})
		}
		if len(pending) != 0 {
			return c, ErrUnsupported
		}
		if len(m.Content) != 0 {
			raw, err := compact(m)
			if err != nil {
				return c, err
			}
			input = append(input, raw)
		}
	}
	if len(pending) != 0 {
		return c, ErrUnsupported
	}
	instructions := make([]string, 0, len(r.System))
	for _, text := range r.System {
		if text.Type != "text" || !validText(text.Text) || text.CacheControl != nil {
			return c, ErrUnsupported
		}
		instructions = append(instructions, text.Text)
	}
	w.Instructions = strings.Join(instructions, "\n\n")
	for _, tool := range r.Tools {
		if !toolName(tool.Name) || slices.Contains(c.toolNames, tool.Name) || !utf8.ValidString(tool.Description) || !objectJSON(tool.InputSchema) || tool.CacheControl != nil {
			return Compiled{}, ErrUnsupported
		}
		schema, _ := rawObject(tool.InputSchema)
		if typ, ok := stringField(schema, "type"); !ok || typ != "object" {
			return Compiled{}, ErrUnsupported
		}
		c.toolNames = append(c.toolNames, tool.Name)
		w.Tools = append(w.Tools, openAIFunction{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema})
	}
	if len(r.Tools) == 0 {
		if r.ToolChoice != nil {
			return Compiled{}, ErrUnsupported
		}
	} else {
		w.ToolChoice = "auto"
		if r.ToolChoice != nil {
			if !slices.Contains(p.ToolChoices, r.ToolChoice.Type) {
				return Compiled{}, ErrUnsupported
			}
			w.ToolChoice = r.ToolChoice.Type
		}
		w.ParallelToolCalls = new(bool)
	}
	body, err := wireArrayRequest(struct {
		Model           string `json:"model"`
		MaxOutputTokens int64  `json:"max_output_tokens"`
	}{p.ModelID, r.MaxTokens}, "input", input, w)
	if err != nil || len(body) > RequestLimit {
		return Compiled{}, ErrLimit
	}
	c.body, c.output, c.classes = body, r.MaxTokens, slices.Clone(openAIClasses[:6])
	c.profile, err = p.SHA256()
	return c, err
}

func openAIReplayItems(raw json.RawMessage, itemIDs map[string]bool) ([]json.RawMessage, []string, error) {
	if checkJSON(raw, RequestLimit, false, false) != nil {
		return nil, nil, ErrInvalid
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil || len(items) > streamBlockLimit {
		return nil, nil, ErrUnsupported
	}
	var calls []string
	for _, raw := range items {
		item, err := rawObject(raw)
		if err != nil {
			return nil, nil, err
		}
		id, hasID := stringField(item, "id")
		if _, present := item["id"]; present {
			if !hasID || !visible(id, 256) || itemIDs[id] {
				return nil, nil, ErrUnsupported
			}
			itemIDs[id] = true
		}
		status, hasStatus := stringField(item, "status")
		if _, present := item["status"]; present && (!hasStatus || status != "completed") {
			return nil, nil, ErrUnsupported
		}
		kind, _ := stringField(item, "type")
		switch kind {
		case "function_call":
			id, _ := stringField(item, "call_id")
			name, _ := stringField(item, "name")
			args, ok := stringField(item, "arguments")
			if !visible(id, 256) || !modelToolName(name) || !ok || !objectJSON([]byte(args)) || !localOpenAICall(item) {
				return nil, nil, ErrUnsupported
			}
			calls = append(calls, id)
		case "message":
			role, _ := stringField(item, "role")
			var parts []json.RawMessage
			if !hasID || role != "assistant" || !hasStatus || json.Unmarshal(item["content"], &parts) != nil || parts == nil {
				return nil, nil, ErrUnsupported
			}
			if _, present := item["phase"]; present {
				phase, ok := stringField(item, "phase")
				if !ok || !slices.Contains([]string{"commentary", "final_answer"}, phase) {
					return nil, nil, ErrUnsupported
				}
			}
			for _, raw := range parts {
				part, err := rawObject(raw)
				typ, _ := stringField(part, "type")
				_, ok := stringField(part, "text")
				if err != nil || typ != "output_text" || !ok || !emptyJSONArray(part["annotations"]) || !emptyJSONArray(part["logprobs"]) {
					return nil, nil, ErrUnsupported
				}
			}
		case "reasoning":
			var summary []json.RawMessage
			encrypted, ok := stringField(item, "encrypted_content")
			if !hasID || !ok || encrypted == "" || json.Unmarshal(item["summary"], &summary) != nil || summary == nil {
				return nil, nil, ErrUnsupported
			}
			for _, raw := range summary {
				part, err := rawObject(raw)
				typ, _ := stringField(part, "type")
				_, ok := stringField(part, "text")
				if err != nil || typ != "summary_text" || !ok {
					return nil, nil, ErrUnsupported
				}
			}
		default:
			return nil, nil, ErrUnsupported
		}
	}
	return items, calls, nil
}

func localOpenAICall(item map[string]json.RawMessage) bool {
	// No asynchronous, hosted, namespaced or delegated function variant.
	return (len(item["async"]) == 0 || string(item["async"]) == "false") &&
		(len(item["caller"]) == 0 || string(item["caller"]) == "null") &&
		(len(item["namespace"]) == 0 || string(item["namespace"]) == "null")
}

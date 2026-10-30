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
	Model              string          `json:"model"`
	MaxOutputTokens    int64           `json:"max_output_tokens"`
	Input              []openAIMessage `json:"input"`
	Stream             bool            `json:"stream"`
	Store              bool            `json:"store"`
	ServiceTier        string          `json:"service_tier"`
	Truncation         string          `json:"truncation"`
	Background         bool            `json:"background"`
	Include            []string        `json:"include"`
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

// CompileOpenAI serializes the stateless text/local-function subset once. No
// previous response, opaque reasoning, hosted tool or cache marker is input.
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
	w := openAIRequest{Model: p.ModelID, MaxOutputTokens: r.MaxTokens, Stream: true, ServiceTier: "default", Truncation: "disabled", Include: []string{"reasoning.encrypted_content"}}
	w.PromptCacheOptions.Mode = "implicit"
	w.PromptCacheOptions.TTL = "30m"
	w.Text.Format.Type = "text"
	for _, message := range r.Messages {
		if message.Role != "user" && message.Role != "assistant" || len(message.Content) == 0 {
			return c, ErrUnsupported
		}
		m := openAIMessage{Type: "message", Role: message.Role}
		for _, b := range message.Content {
			if b.Type != "text" || !validText(b.Text) || b.ID != "" || b.Name != "" || b.Input != nil || b.ToolUseID != "" || b.Content != nil || b.IsError != nil || b.CacheControl != nil {
				return c, ErrUnsupported
			}
			m.Content = append(m.Content, openAIText{"input_text", b.Text})
		}
		w.Input = append(w.Input, m)
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
	body, err := compact(w)
	if err != nil || len(body) > RequestLimit {
		return Compiled{}, ErrLimit
	}
	c.body, c.output, c.classes = body, r.MaxTokens, slices.Clone(openAIClasses[:6])
	c.profile, err = p.SHA256()
	return c, err
}

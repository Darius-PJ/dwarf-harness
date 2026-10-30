package providers

import (
	"bytes"
	"encoding/json"
	"slices"
	"unicode/utf8"
)

const RequestLimit = 1 << 20

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl"`
}

type Text struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type Block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      []Text          `json:"content,omitempty"`
	IsError      *bool           `json:"is_error,omitempty"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
	// Replay holds a whole same-provider assistant array, never display fields.
	Replay json.RawMessage `json:"replay,omitempty"`
}

type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
}

type ToolChoice struct {
	Type string `json:"type"`
}

// Request exposes only the static local-tool/text subset. Routing and wire
// defaults are derived explicitly from the validated profile, never inherited.
type Request struct {
	MaxTokens  int64       `json:"max_tokens"`
	Messages   []Message   `json:"messages"`
	System     []Text      `json:"system,omitempty"`
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

type wireRequest struct {
	// The leading fields and messages are encoded separately to preserve replay.
	Stream       bool        `json:"stream"`
	System       []Text      `json:"system,omitempty"`
	Tools        []Tool      `json:"tools,omitempty"`
	ToolChoice   *ToolChoice `json:"tool_choice,omitempty"`
	ServiceTier  string      `json:"service_tier"`
	InferenceGeo string      `json:"inference_geo,omitempty"`
	Speed        string      `json:"speed,omitempty"`
}

// Compiled is immutable: accessors return copies of its owned bytes/classes.
type Compiled struct {
	body           []byte
	classes        []string
	output         int64
	profile        [32]byte
	cache5, cache1 bool
	toolNames      []string
}

func (c Compiled) Body() []byte           { return bytes.Clone(c.body) }
func (c Compiled) InputClasses() []string { return slices.Clone(c.classes) }
func (c Compiled) OutputMaximum() int64   { return c.output }

// ParseRequest refuses unknown fields, including old sampling parameters.
func ParseRequest(b []byte) (Request, error) {
	var r Request
	if e := checkJSON(b, RequestLimit, false, false); e != nil {
		return r, e
	}
	if e := decodeStrict(b, &r); e != nil {
		return Request{}, e
	}
	return r, nil
}

func toolName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func validText(s string) bool { return len(s) > 0 && len(s) <= RequestLimit && utf8.ValidString(s) }
func objectJSON(b []byte) bool {
	return checkJSON(b, RequestLimit, false, false) == nil && len(bytes.TrimSpace(b)) > 0 && bytes.TrimSpace(b)[0] == '{'
}

func CompileAnthropic(p Profile, r Request) (Compiled, error) {
	var c Compiled
	if p.Provider != "anthropic" {
		return c, ErrUnsupported
	}
	if e := p.Validate(); e != nil {
		return c, e
	}
	if r.MaxTokens <= 0 || r.MaxTokens > p.OutputMax || len(r.Messages) == 0 || r.Messages[len(r.Messages)-1].Role != "user" {
		return c, ErrUnsupported
	}
	// encoding/json replaces invalid UTF-8 in Go strings. Refuse it before the
	// ownership copy so a caller cannot silently change the serialized request.
	for _, tool := range r.Tools {
		if !utf8.ValidString(tool.Description) || !objectJSON(tool.InputSchema) {
			return c, ErrInvalid
		}
	}
	for _, text := range r.System {
		if !utf8.ValidString(text.Text) {
			return c, ErrInvalid
		}
	}
	for _, message := range r.Messages {
		for _, block := range message.Content {
			if !utf8.ValidString(block.Text) || block.Input != nil && !objectJSON(block.Input) {
				return c, ErrInvalid
			}
			for _, text := range block.Content {
				if !utf8.ValidString(text.Text) {
					return c, ErrInvalid
				}
			}
		}
	}
	// Clone first: normalizing TTLs must not change a caller-owned request.
	// Raw replay is restored afterwards: encoding/json compacts RawMessage.
	original := r.Messages
	b, e := compact(r)
	if e != nil || len(b) > RequestLimit {
		return c, ErrLimit
	}
	r = Request{}
	if e = decodeStrict(b, &r); e != nil {
		return c, e
	}
	for i := range r.Messages {
		for j := range r.Messages[i].Content {
			r.Messages[i].Content[j].Replay = bytes.Clone(original[i].Content[j].Replay)
		}
	}
	breaks := 0
	seen5 := false
	marker := func(cc *CacheControl) error {
		if cc == nil {
			return nil
		}
		if cc.Type != "ephemeral" {
			return ErrUnsupported
		}
		if cc.TTL == "" {
			cc.TTL = "5m"
		}
		breaks++
		if breaks > 4 || cc.TTL != "5m" && cc.TTL != "1h" || cc.TTL == "1h" && seen5 {
			return ErrUnsupported
		}
		if cc.TTL == "5m" {
			c.cache5 = true
			seen5 = true
		} else {
			c.cache1 = true
		}
		return nil
	}
	text := func(t Text) error {
		if t.Type != "text" || !validText(t.Text) {
			return ErrUnsupported
		}
		return marker(t.CacheControl)
	}
	names := map[string]bool{}
	for _, t := range r.Tools {
		if !toolName(t.Name) || names[t.Name] || !utf8.ValidString(t.Description) || !objectJSON(t.InputSchema) {
			return c, ErrUnsupported
		}
		names[t.Name] = true
		c.toolNames = append(c.toolNames, t.Name)
		var schema map[string]json.RawMessage
		_ = json.Unmarshal(t.InputSchema, &schema)
		if string(schema["type"]) != `"object"` {
			return c, ErrUnsupported
		}
		if e = marker(t.CacheControl); e != nil {
			return c, e
		}
	}
	for _, t := range r.System {
		if e = text(t); e != nil {
			return c, e
		}
	}
	calls := map[string]bool{}
	var pending []string
	var messages []json.RawMessage
	replayed := false
	for _, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" || len(m.Content) == 0 || len(pending) > 0 && m.Role != "user" {
			return c, ErrUnsupported
		}
		if m.Content[0].Type == "anthropic_replay" {
			b := m.Content[0]
			if m.Role != "assistant" || len(m.Content) != 1 || !replayOnly(b) {
				return c, ErrUnsupported
			}
			ids, err := anthropicReplayCalls(b.Replay)
			if err != nil {
				return c, err
			}
			for _, id := range ids {
				if calls[id] {
					return c, ErrUnsupported
				}
				calls[id] = true
			}
			pending, replayed = ids, true
			raw := append([]byte(`{"role":"assistant","content":`), b.Replay...)
			messages = append(messages, append(raw, '}'))
			continue
		}
		for _, b := range m.Content {
			if b.Replay != nil {
				return c, ErrUnsupported
			}
			switch b.Type {
			case "text":
				if m.Role == "user" && len(pending) != 0 || b.ID != "" || b.Name != "" || b.Input != nil || b.ToolUseID != "" || b.Content != nil || b.IsError != nil {
					return c, ErrUnsupported
				}
				if e = text(Text{b.Type, b.Text, b.CacheControl}); e != nil {
					return c, e
				}
			case "tool_use":
				if p.ModelID != Haiku || m.Role != "assistant" || !visible(b.ID, 256) || calls[b.ID] || !toolName(b.Name) || !objectJSON(b.Input) || b.Text != "" || b.ToolUseID != "" || b.Content != nil || b.IsError != nil || b.CacheControl != nil {
					return c, ErrUnsupported
				}
				calls[b.ID] = true
				pending = append(pending, b.ID)
				replayed = false
			case "tool_result":
				if p.ModelID != Haiku && !replayed || m.Role != "user" || len(pending) == 0 || pending[0] != b.ToolUseID || len(b.Content) == 0 || b.ID != "" || b.Name != "" || b.Input != nil || b.Text != "" || b.CacheControl != nil {
					return c, ErrUnsupported
				}
				pending = pending[1:]
				for _, t := range b.Content {
					if e = text(t); e != nil {
						return c, e
					}
				}
			default:
				return c, ErrUnsupported
			}
		}
		if m.Role == "user" && len(pending) != 0 {
			return c, ErrUnsupported
		}
		raw, err := compact(m)
		if err != nil {
			return c, err
		}
		messages = append(messages, raw)
	}
	if len(pending) != 0 {
		return c, ErrUnsupported
	}
	if len(r.Tools) == 0 {
		if r.ToolChoice != nil {
			return c, ErrUnsupported
		}
	} else {
		if r.ToolChoice == nil {
			r.ToolChoice = &ToolChoice{"auto"}
		}
		if !slices.Contains(p.ToolChoices, r.ToolChoice.Type) {
			return c, ErrUnsupported
		}
	}
	w := wireRequest{Stream: true, System: r.System, Tools: r.Tools, ToolChoice: r.ToolChoice, ServiceTier: p.RequestedTier}
	if p.ModelID != Haiku {
		w.InferenceGeo = p.Region
	}
	if p.ModelID == Opus {
		w.Speed = p.Speed
	}
	c.body, e = wireArrayRequest(struct {
		Model     string `json:"model"`
		MaxTokens int64  `json:"max_tokens"`
	}{p.ModelID, r.MaxTokens}, "messages", messages, w)
	if e != nil || len(c.body) > RequestLimit {
		return Compiled{}, ErrLimit
	}
	c.classes = []string{Uncached}
	if c.cache1 || c.cache5 {
		c.classes = append(c.classes, CacheRead)
	}
	if c.cache1 {
		c.classes = append(c.classes, CacheWrite1h)
	}
	if c.cache5 {
		c.classes = append(c.classes, CacheWrite5m)
	}
	slices.Sort(c.classes)
	c.output = r.MaxTokens
	c.profile, e = p.SHA256()
	return c, e
}

func replayOnly(b Block) bool {
	return b.Replay != nil && b.Text == "" && b.ID == "" && b.Name == "" && b.Input == nil && b.ToolUseID == "" && b.Content == nil && b.IsError == nil && b.CacheControl == nil
}

func anthropicReplayCalls(raw json.RawMessage) ([]string, error) {
	if checkJSON(raw, RequestLimit, false, false) != nil {
		return nil, ErrInvalid
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 || len(blocks) > streamBlockLimit {
		return nil, ErrUnsupported
	}
	var calls []string
	for _, raw := range blocks {
		b, err := rawObject(raw)
		if err != nil {
			return nil, err
		}
		typ, _ := stringField(b, "type")
		switch typ {
		case "text":
			if _, ok := stringField(b, "text"); !ok {
				return nil, ErrUnsupported
			}
		case "tool_use":
			id, _ := stringField(b, "id")
			name, _ := stringField(b, "name")
			if !visible(id, 256) || !modelToolName(name) || !objectJSON(b["input"]) {
				return nil, ErrUnsupported
			}
			calls = append(calls, id)
		case "thinking":
			_, textOK := stringField(b, "thinking")
			signature, signatureOK := stringField(b, "signature")
			if !textOK || !signatureOK || signature == "" {
				return nil, ErrUnsupported
			}
		case "redacted_thinking":
			data, ok := stringField(b, "data")
			if !ok || data == "" {
				return nil, ErrUnsupported
			}
		default:
			return nil, ErrUnsupported
		}
	}
	return calls, nil
}

// wireArrayRequest is the one serialization boundary for stored replay bytes.
// json.Encoder compacts even RawMessage, so the validated items are appended
// directly between independently encoded, fixed-shape request members.
func wireArrayRequest(head any, name string, items []json.RawMessage, tail any) ([]byte, error) {
	prefix, err := compact(head)
	if err != nil {
		return nil, err
	}
	suffix, err := compact(tail)
	if err != nil {
		return nil, err
	}
	body := append(prefix[:len(prefix)-1], []byte(`,"`+name+`":[`)...)
	for i, item := range items {
		if len(item) > RequestLimit-len(body) {
			return nil, ErrLimit
		}
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, item...)
	}
	body = append(body, ']', ',')
	body = append(body, suffix[1:]...)
	if len(body) > RequestLimit {
		return nil, ErrLimit
	}
	return body, nil
}

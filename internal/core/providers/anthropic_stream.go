package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	streamLineLimit   = 1 << 20
	streamBodyLimit   = 16 << 20
	streamOutputLimit = 8 << 20
	streamEventLimit  = 262144
	streamBlockLimit  = 256
)

// OutputBlock preserves local tools and opaque provider thinking without
// interpreting signatures or executing a tool. Only completed blocks escape.
type OutputBlock struct {
	Type          string          `json:"type"`
	Text          string          `json:"text,omitempty"`
	ID            string          `json:"id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	Thinking      string          `json:"thinking,omitempty"`
	Signature     string          `json:"signature,omitempty"`
	Data          string          `json:"data,omitempty"`
	OpaquePayload json.RawMessage `json:"-"`
	ArgumentsText *string         `json:"arguments_text,omitempty"`
}

// StreamResult distinguishes message completion from financial availability.
// Non-success terminals expose no assistant/tool commands; OpenAI may retain
// independently complete opaque reasoning. Raw usage is evidence only: it does
// not imply settlement or a no-charge result.
type StreamResult struct {
	MessageID string
	Model     string
	Blocks    []OutputBlock
	// Replay is a complete validated assistant array. It is absent when tool
	// arguments cannot safely be rendered, even though a correction can be made.
	Replay                   json.RawMessage
	StopReason               string
	StopSequence             json.RawMessage
	StopDetails              json.RawMessage
	ServedTier               json.RawMessage
	ToolUsage                json.RawMessage
	RawUsageStart            json.RawMessage
	RawUsageFinal            json.RawMessage
	RawUsageMerged           Usage
	Units                    []Units
	Complete                 bool
	Priceable                bool
	ProfileViolation         bool
	ContentError             bool
	OutputStarted            bool
	ContentBlocks            int
	UnknownEventTypes        []string
	UnknownEventTypeOverflow int
	EventCount               int
	ProviderError            json.RawMessage
}

type streamState struct {
	result                               StreamResult
	profile                              Profile
	started, delta, stopped, finalOutput bool
	open                                 *OutputBlock
	partial                              strings.Builder
	text, thinking, signature            strings.Builder
	partialSeen                          bool
	blocks                               []OutputBlock
	outputBytes                          int
	compiled                             Compiled
	observe                              func(string) error
	assembled                            []map[string]json.RawMessage
	rawBlock                             map[string]json.RawMessage
	renderable                           bool
}

func stringField(m map[string]json.RawMessage, key string) (string, bool) {
	var s string
	b, ok := m[key]
	if !ok || json.Unmarshal(b, &s) != nil || string(b) == "null" {
		return "", false
	}
	return s, true
}
func rawObject(raw []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, ErrProtocol
	}
	return m, nil
}

// ReadAnthropicStream follows WHATWG SSE line/event framing, without reconnect.
// Limits are checked while reading, not after retaining the entire entity.
func ReadAnthropicStream(r io.Reader, p Profile, c Compiled) (StreamResult, error) {
	return readAnthropicStream(r, p, c, nil)
}

func readAnthropicStream(r io.Reader, p Profile, c Compiled, observe func(string) error) (StreamResult, error) {
	s := streamState{profile: p, compiled: c, observe: observe, assembled: []map[string]json.RawMessage{}, renderable: true, result: StreamResult{RawUsageMerged: Usage{}}}
	if p.Provider != "anthropic" {
		return s.result, ErrUnsupported
	}
	if err := p.Validate(); err != nil {
		return s.result, err
	}
	digest, _ := p.SHA256()
	if c.profile != digest {
		return s.result, ErrInvalid
	}
	err := readEvents(r, s.event)
	if err == nil && !s.stopped {
		err = ErrProtocol
	}
	if err != nil {
		s.result.Complete = false
		s.result.Blocks = nil
		s.result.Replay = nil
		s.result.Priceable = false
		return s.result, err
	}
	s.result.Complete = true
	if !s.result.ContentError && s.result.StopReason != "refusal" {
		s.result.Blocks = s.blocks
		if s.renderable {
			s.result.Replay, err = compact(s.assembled)
			if err != nil || len(s.result.Replay) > streamOutputLimit {
				s.result.Blocks, s.result.Replay = nil, nil
				return s.result, ErrLimit
			}
		}
	}
	if s.finalOutput {
		units, violation, e := NormalizeUsage(p, c, s.result.RawUsageMerged)
		s.result.ProfileViolation = violation
		if e == nil {
			s.result.Units = units
			s.result.Priceable = true
		}
	}
	return s.result, nil
}

func readEvents(r io.Reader, emit func(string, []byte) error) error {
	reader := bufio.NewReaderSize(r, 4096)
	line := make([]byte, 0, 256)
	var data bytes.Buffer
	event := ""
	eventBytes, total := 0, 0
	first := true
	skipLF := false
	finishLine := func() error {
		if first {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			first = false
		}
		if !utf8.Valid(line) {
			return ErrProtocol
		}
		if len(line) == 0 {
			if data.Len() > 0 {
				b := data.Bytes()
				if err := emit(event, b[:len(b)-1]); err != nil {
					return err
				}
			}
			data.Reset()
			event = ""
			eventBytes = 0
			return nil
		}
		eventBytes += len(line) + 1
		if eventBytes > streamLineLimit {
			return ErrLimit
		}
		if line[0] == ':' {
			return nil
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte{' '})
		switch string(field) {
		case "event":
			event = string(value)
		case "data":
			data.Write(value)
			data.WriteByte('\n')
		}
		return nil
	}
	for {
		b, e := reader.ReadByte()
		if e == io.EOF {
			if len(line) != 0 || data.Len() != 0 || event != "" {
				return ErrProtocol
			}
			return nil
		}
		if e != nil {
			return e
		}
		total++
		if total > streamBodyLimit {
			return ErrLimit
		}
		if skipLF {
			skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' || b == '\n' {
			if e = finishLine(); e != nil {
				return e
			}
			line = line[:0]
			skipLF = b == '\r'
			continue
		}
		if len(line) >= streamLineLimit {
			return ErrLimit
		}
		line = append(line, b)
	}
}

func (s *streamState) event(name string, data []byte) error {
	s.result.EventCount++
	if s.result.EventCount > streamEventLimit {
		return ErrLimit
	}
	m, e := anthropicEventObject(data)
	if e != nil {
		return e
	}
	typ, ok := stringField(m, "type")
	if !ok || name != "" && name != typ {
		return ErrProtocol
	}
	if s.stopped {
		return ErrProtocol
	}
	switch typ {
	case "ping":
		return nil
	case "message_start":
		return s.start(m)
	case "content_block_start":
		return s.blockStart(m)
	case "content_block_delta":
		return s.blockDelta(m)
	case "content_block_stop":
		return s.blockStop(m)
	case "message_delta":
		return s.messageDelta(m)
	case "message_stop":
		if !s.started || s.open != nil || !s.delta {
			return ErrProtocol
		}
		if !slices.Contains([]string{"end_turn", "max_tokens", "stop_sequence", "tool_use", "model_context_window_exceeded", "refusal"}, s.result.StopReason) {
			s.result.ProfileViolation = true
			return ErrProtocol
		}
		if s.result.StopReason == "tool_use" {
			calls := 0
			for _, b := range s.blocks {
				if b.Type == "tool_use" || b.Type == "local_tool_request" {
					calls++
				}
			}
			if calls == 0 {
				s.result.ContentError = true
			}
		}
		s.stopped = true
		return nil
	case "error":
		if len(data) > 64<<10 {
			return ErrLimit
		}
		s.result.ProviderError = bytes.Clone(data)
		return ErrProtocol
	default:
		s.result.ContentError = true
		if len(typ) > 64 {
			s.result.UnknownEventTypeOverflow++
			return nil
		}
		if !slices.Contains(s.result.UnknownEventTypes, typ) {
			if len(s.result.UnknownEventTypes) < 16 {
				s.result.UnknownEventTypes = append(s.result.UnknownEventTypes, typ)
			} else {
				s.result.UnknownEventTypeOverflow++
			}
		}
		return nil
	}
}

func (s *streamState) start(m map[string]json.RawMessage) error {
	if s.started {
		return ErrProtocol
	}
	msg, e := rawObject(m["message"])
	if e != nil {
		return e
	}
	role, _ := stringField(msg, "role")
	typ, _ := stringField(msg, "type")
	model, _ := stringField(msg, "model")
	id, _ := stringField(msg, "id")
	var content []json.RawMessage
	if role != "assistant" || typ != "message" || model != s.profile.ModelID || !visible(id, 256) || json.Unmarshal(msg["content"], &content) != nil || content == nil || len(content) != 0 {
		return ErrProtocol
	}
	s.started = true
	s.result.MessageID = id
	s.result.Model = model
	s.result.RawUsageStart = bytes.Clone(msg["usage"])
	return mergeUsage(s.result.RawUsageMerged, msg["usage"])
}
func (s *streamState) index(m map[string]json.RawMessage) bool {
	n, e := decimal(string(m["index"]))
	return e == nil && n == int64(len(s.blocks))
}
func (s *streamState) retain(text string) error {
	// JSON escaping expands a byte by at most six. Count the actual encoding so
	// large printable text is not rejected by an invented sixfold bound.
	b, e := compact(text)
	if e != nil {
		return e
	}
	n := len(b) - 2
	if s.outputBytes > streamOutputLimit-n {
		return ErrLimit
	}
	s.outputBytes += n
	if text != "" {
		s.result.OutputStarted = true
	}
	return nil
}
func (s *streamState) blockStart(m map[string]json.RawMessage) error {
	if !s.started || s.delta || s.open != nil || !s.index(m) || len(s.blocks) >= streamBlockLimit {
		return ErrProtocol
	}
	raw, e := rawObject(m["content_block"])
	if e != nil {
		return e
	}
	typ, _ := stringField(raw, "type")
	b := OutputBlock{Type: typ}
	switch typ {
	case "text":
		var ok bool
		b.Text, ok = stringField(raw, "text")
		if !ok {
			return ErrProtocol
		}
		if e = s.retain(b.Text); e == nil {
			s.present(b.Text)
		}
	case "tool_use":
		b.ID, _ = stringField(raw, "id")
		b.Name, _ = stringField(raw, "name")
		b.Input = bytes.Clone(raw["input"])
		if !visible(b.ID, 256) || !modelToolName(b.Name) || len(b.Input) == 0 {
			return ErrProtocol
		}
		for _, old := range s.blocks {
			if (old.Type == "tool_use" || old.Type == "local_tool_request") && old.ID == b.ID {
				return ErrProtocol
			}
		}
		e = s.retain(b.ID + b.Name + string(b.Input))
	case "thinking":
		var ok bool
		b.Thinking, ok = stringField(raw, "thinking")
		if !ok {
			return ErrProtocol
		}
		b.Signature, _ = stringField(raw, "signature")
		e = s.retain(b.Thinking + b.Signature)
	case "redacted_thinking":
		var ok bool
		b.Data, ok = stringField(raw, "data")
		if !ok {
			return ErrProtocol
		}
		e = s.retain(b.Data)
		if len(m["content_block"]) > streamOutputLimit-s.outputBytes {
			return ErrLimit
		}
		s.outputBytes += len(m["content_block"])
		b.OpaquePayload = bytes.Clone(m["content_block"])
	default:
		s.result.ProfileViolation = true
		return ErrProtocol
	}
	if e != nil {
		return e
	}
	s.outputBytes += 128
	if s.outputBytes > streamOutputLimit {
		return ErrLimit
	}
	s.result.ContentBlocks++
	s.open = &b
	s.rawBlock = raw
	s.partial.Reset()
	s.text.Reset()
	s.text.WriteString(b.Text)
	s.thinking.Reset()
	s.thinking.WriteString(b.Thinking)
	s.signature.Reset()
	s.signature.WriteString(b.Signature)
	s.partialSeen = false
	return nil
}
func (s *streamState) blockDelta(m map[string]json.RawMessage) error {
	if s.open == nil || !s.index(m) {
		return ErrProtocol
	}
	d, e := rawObject(m["delta"])
	if e != nil {
		return e
	}
	typ, _ := stringField(d, "type")
	var key string
	var dst *strings.Builder
	switch {
	case typ == "text_delta" && s.open.Type == "text":
		key = "text"
		dst = &s.text
	case typ == "thinking_delta" && s.open.Type == "thinking":
		key = "thinking"
		dst = &s.thinking
	case typ == "signature_delta" && s.open.Type == "thinking":
		key = "signature"
		dst = &s.signature
	case typ == "input_json_delta" && s.open.Type == "tool_use":
		key = "partial_json"
	default:
		s.result.ProfileViolation = true
		return ErrProtocol
	}
	text, ok := stringField(d, key)
	if !ok {
		return ErrProtocol
	}
	if e = s.retain(text); e != nil {
		return e
	}
	if dst != nil {
		if typ == "text_delta" {
			s.present(text)
		}
		dst.WriteString(text)
	} else {
		if !s.partialSeen && string(bytes.TrimSpace(s.open.Input)) != "{}" {
			return ErrProtocol
		}
		if s.partial.Len() > streamLineLimit-len(text) {
			return ErrLimit
		}
		s.partial.WriteString(text)
		s.partialSeen = true
	}
	return nil
}
func (s *streamState) blockStop(m map[string]json.RawMessage) error {
	if s.open == nil || !s.index(m) {
		return ErrProtocol
	}
	if s.open.Type == "tool_use" {
		if s.partialSeen {
			s.open.Input = []byte(s.partial.String())
		}
		valid := objectJSON(s.open.Input)
		if !valid || !slices.Contains(s.compiled.toolNames, s.open.Name) {
			s.open.Type = "local_tool_request"
			arguments := string(s.open.Input)
			s.open.ArgumentsText = &arguments
			s.open.Input = nil
			s.renderable = s.renderable && valid
		}
	}
	s.open.Text = s.text.String()
	s.open.Thinking = s.thinking.String()
	s.open.Signature = s.signature.String()
	switch s.open.Type {
	case "text":
		s.rawBlock["text"], _ = compact(s.open.Text)
	case "thinking":
		s.rawBlock["thinking"], _ = compact(s.open.Thinking)
		s.rawBlock["signature"], _ = compact(s.open.Signature)
		// Keep the existing display artifact encoding; replay uses the complete
		// assembled array below, including provider extension members.
		s.open.OpaquePayload, _ = compact(struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}{s.open.Type, s.open.Thinking, s.open.Signature})
	case "tool_use":
		s.rawBlock["input"] = s.open.Input
	case "local_tool_request":
		if objectJSON([]byte(*s.open.ArgumentsText)) {
			s.rawBlock["input"] = json.RawMessage(*s.open.ArgumentsText)
		}
	}
	s.assembled = append(s.assembled, s.rawBlock)
	s.rawBlock = nil
	b, e := compact(s.open)
	if e != nil {
		return e
	}
	if s.open.Type != "text" && len(b) > streamLineLimit {
		return ErrLimit
	}
	s.blocks = append(s.blocks, *s.open)
	s.open = nil
	s.partial.Reset()
	return nil
}
func (s *streamState) messageDelta(m map[string]json.RawMessage) error {
	if !s.started || s.open != nil {
		return ErrProtocol
	}
	delta, e := rawObject(m["delta"])
	if e != nil {
		return e
	}
	if raw, ok := delta["stop_reason"]; ok {
		if string(raw) == "null" {
			s.result.StopReason = ""
		} else {
			reason, ok := stringField(delta, "stop_reason")
			if !ok {
				return ErrProtocol
			}
			s.result.StopReason = reason
		}
	}
	if v, ok := delta["stop_sequence"]; ok {
		s.result.StopSequence = bytes.Clone(v)
	}
	if v, ok := delta["stop_details"]; ok {
		s.result.StopDetails = bytes.Clone(v)
	}
	s.delta = true
	s.result.RawUsageFinal = bytes.Clone(m["usage"])
	var usage Usage
	if len(m["usage"]) > 0 && json.Unmarshal(m["usage"], &usage) != nil {
		return ErrProtocol
	}
	// The terminal delta itself must supply a non-null output counter; an earlier
	// delta or the message-start counter is not final billing evidence.
	_, _, s.finalOutput = counter(usage, "output_tokens")
	return mergeUsage(s.result.RawUsageMerged, m["usage"])
}

func modelToolName(name string) bool {
	return len(name) <= 64 && toolName(name)
}

func (s *streamState) present(text string) {
	if text != "" && s.observe != nil && !s.result.ContentError {
		if s.observe(text) != nil {
			s.result.ContentError = true
			s.observe = nil
		}
	}
}

// Arguments alone are untrusted data. Validate every surrounding member and
// duplicate key, but leave tool input's own duplicate keys for the normalizer.
func anthropicEventObject(data []byte) (map[string]json.RawMessage, error) {
	if len(data) > streamLineLimit || !utf8.Valid(data) {
		return nil, ErrProtocol
	}
	var object func([]byte, int) (map[string]json.RawMessage, error)
	object = func(raw []byte, level int) (map[string]json.RawMessage, error) {
		d := json.NewDecoder(bytes.NewReader(raw))
		token, err := d.Token()
		if err != nil || token != json.Delim('{') {
			return nil, ErrProtocol
		}
		fields := map[string]json.RawMessage{}
		for d.More() {
			token, err := d.Token()
			name, ok := token.(string)
			if err != nil || !ok || fields[name] != nil {
				return nil, ErrProtocol
			}
			var value json.RawMessage
			if d.Decode(&value) != nil {
				return nil, ErrProtocol
			}
			fields[name] = value
			if level == 0 && name == "content_block" {
				if _, err := object(value, 1); err != nil {
					return nil, err
				}
			} else if !(level == 1 && name == "input") && checkJSON(value, streamLineLimit, false, false) != nil {
				return nil, ErrProtocol
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return nil, ErrProtocol
		}
		if _, err = d.Token(); err != io.EOF {
			return nil, ErrProtocol
		}
		return fields, nil
	}
	return object(data, 0)
}

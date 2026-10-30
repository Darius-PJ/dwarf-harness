package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

var errOpenAITerminal = errors.New("providers: terminal response obtained")

type openAIStreamState struct {
	result                                  StreamResult
	profile                                 Profile
	compiled                                Compiled
	sequence                                int64
	hasSequence, hasUsage, unknown, refused bool
	items                                   map[int64]string
	itemIndexes                             map[string]int64
}

// ReadOpenAIStream uses bounded SSE framing without importing an ordering rule
// from another provider. Only a fully framed terminal owns usage and output.
// It stops at that terminal, even if the peer leaves the connection open.
func ReadOpenAIStream(r io.Reader, p Profile, c Compiled) (StreamResult, error) {
	s := openAIStreamState{profile: p, compiled: c, items: map[int64]string{}, itemIndexes: map[string]int64{}}
	if p.Provider != "openai" {
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
	if err != errOpenAITerminal {
		s.result.Blocks = nil
		if err == nil {
			err = ErrProtocol
		}
		return s.result, err
	}
	s.result.Complete = true
	if s.unknown {
		s.result.Blocks = nil
		return s.result, ErrProtocol
	}
	tier, tierOK := stringField(map[string]json.RawMessage{"tier": s.result.ServedTier}, "tier")
	if !tierOK || tier != "default" || !emptyOpenAIToolUsage(s.result.ToolUsage) {
		s.result.ProfileViolation = tierOK && tier != "default" || !emptyOpenAIToolUsage(s.result.ToolUsage)
		s.result.Blocks = nil
		return s.result, ErrUsage
	}
	units, violation, err := NormalizeUsage(p, c, s.result.RawUsageMerged)
	s.result.ProfileViolation = s.result.ProfileViolation || violation
	if err != nil {
		s.result.Blocks = nil
		return s.result, err
	}
	s.result.Units, s.result.Priceable = units, true
	return s.result, nil
}

func (s *openAIStreamState) associate(index int64, id string) error {
	if index < 0 || index >= streamBlockLimit || !visible(id, 256) {
		return ErrProtocol
	}
	if old, ok := s.items[index]; ok && old != id {
		return ErrProtocol
	}
	if old, ok := s.itemIndexes[id]; ok && old != index {
		return ErrProtocol
	}
	s.items[index], s.itemIndexes[id] = id, index
	return nil
}

func (s *openAIStreamState) event(name string, data []byte) error {
	s.result.EventCount++
	if s.result.EventCount > streamEventLimit {
		return ErrLimit
	}
	if checkJSON(data, streamLineLimit, false, false) != nil {
		return ErrProtocol
	}
	m, err := rawObject(data)
	if err != nil {
		return err
	}
	typ, ok := stringField(m, "type")
	if !ok || name != "" && typ != name {
		return ErrProtocol
	}
	if value, present := m["sequence_number"]; present {
		n, err := decimal(string(value))
		if err != nil || s.hasSequence && n <= s.sequence {
			return ErrProtocol
		}
		s.sequence, s.hasSequence = n, true
	}
	if _, present := m["response_id"]; present {
		id, ok := stringField(m, "response_id")
		if !ok || !visible(id, 256) || s.result.MessageID != "" && s.result.MessageID != id {
			return ErrProtocol
		}
		s.result.MessageID = id
	}
	for _, key := range []string{"output_index", "content_index", "summary_index", "annotation_index"} {
		if value, present := m[key]; present {
			n, err := decimal(string(value))
			if err != nil || n >= streamBlockLimit {
				return ErrProtocol
			}
		}
	}
	item, itemPresent := m["item"]
	id, idPresent := stringField(m, "item_id")
	if _, present := m["item_id"]; present && (!idPresent || !visible(id, 256)) {
		return ErrProtocol
	}
	if itemPresent {
		obj, err := rawObject(item)
		if err != nil {
			return err
		}
		if itemID, present := obj["id"]; present {
			value, ok := stringField(obj, "id")
			if !ok || !visible(value, 256) || idPresent && id != value || len(itemID) == 0 {
				return ErrProtocol
			}
			id, idPresent = value, true
		}
		kind, ok := stringField(obj, "type")
		if !ok {
			return ErrProtocol
		}
		if !slices.Contains([]string{"message", "reasoning", "function_call"}, kind) {
			s.unknown, s.result.ProfileViolation = true, true
		}
	}
	if idPresent {
		if index, present, ok := counter(m, "output_index"); present {
			if !ok || s.associate(index, id) != nil {
				return ErrProtocol
			}
		}
	}
	switch typ {
	case "response.created", "response.in_progress", "response.queued":
		return s.snapshot(m["response"], "")
	case "response.completed", "response.incomplete", "response.failed":
		if err := s.snapshot(m["response"], strings.TrimPrefix(typ, "response.")); err != nil {
			return err
		}
		return errOpenAITerminal
	case "error":
		if len(data) > 64<<10 {
			return ErrLimit
		}
		s.result.ProviderError = bytes.Clone(data)
		return ErrProtocol
	case "response.output_item.added", "response.output_item.done":
		if !itemPresent {
			return ErrProtocol
		}
		s.result.OutputStarted = true
	case "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		part, err := rawObject(m["part"])
		if err != nil {
			return err
		}
		kind, ok := stringField(part, "type")
		if !ok {
			return ErrProtocol
		}
		if !slices.Contains([]string{"output_text", "refusal", "summary_text"}, kind) {
			s.unknown, s.result.ProfileViolation = true, true
		}
		s.result.OutputStarted = true
	case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta", "response.function_call_arguments.delta":
		value, ok := stringField(m, "delta")
		if !ok {
			return ErrProtocol
		}
		s.result.OutputStarted = s.result.OutputStarted || value != ""
	case "response.output_text.done", "response.reasoning_summary_text.done", "response.reasoning_text.done", "response.refusal.done", "response.function_call_arguments.done":
		key := "text"
		if typ == "response.refusal.done" {
			key = "refusal"
		} else if typ == "response.function_call_arguments.done" {
			key = "arguments"
		}
		value, ok := stringField(m, key)
		if !ok {
			return ErrProtocol
		}
		s.result.OutputStarted = s.result.OutputStarted || value != ""
	case "response.output_text.annotation.added":
		s.result.ContentError = true
	default:
		s.unknown, s.result.ProfileViolation = true, true
		if len(typ) > 64 {
			s.result.UnknownEventTypeOverflow++
		} else if !slices.Contains(s.result.UnknownEventTypes, typ) {
			if len(s.result.UnknownEventTypes) < 32 {
				s.result.UnknownEventTypes = append(s.result.UnknownEventTypes, typ)
			} else {
				s.result.UnknownEventTypeOverflow++
			}
		}
	}
	return nil
}

func (s *openAIStreamState) snapshot(raw json.RawMessage, terminal string) error {
	m, err := rawObject(raw)
	if err != nil {
		return err
	}
	id, ok := stringField(m, "id")
	if !ok || !visible(id, 256) || s.result.MessageID != "" && s.result.MessageID != id {
		return ErrProtocol
	}
	s.result.MessageID = id
	model, ok := stringField(m, "model")
	previous := s.result.Model
	if ok {
		s.result.Model = model
	}
	if !ok || !visible(model, 128) || model != s.profile.ModelID || previous != "" && previous != model {
		return ErrProtocol
	}
	if usage, present := m["usage"]; present && !s.hasUsage {
		s.result.RawUsageStart, s.hasUsage = bytes.Clone(usage), true
	}
	// A snapshot can witness output, but never supplies chargeable counters.
	if usage, err := rawObject(m["usage"]); err == nil {
		if n, _, ok := counter(usage, "output_tokens"); ok && n > 0 {
			s.result.OutputStarted = true
		}
	}
	var output []json.RawMessage
	if value, present := m["output"]; present {
		if json.Unmarshal(value, &output) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || len(output) > streamBlockLimit {
			return ErrProtocol
		}
		s.result.OutputStarted = s.result.OutputStarted || len(output) != 0
	}
	if terminal == "" {
		for i, raw := range output {
			item, err := rawObject(raw)
			if err != nil {
				return err
			}
			if _, present := item["id"]; present {
				id, ok := stringField(item, "id")
				if !ok || s.associate(int64(i), id) != nil {
					return ErrProtocol
				}
			}
			kind, ok := stringField(item, "type")
			if !ok {
				return ErrProtocol
			}
			if !slices.Contains([]string{"message", "reasoning", "function_call"}, kind) {
				s.unknown, s.result.ProfileViolation = true, true
			}
		}
		return nil
	}
	status, ok := stringField(m, "status")
	if !ok || status != terminal {
		return ErrProtocol
	}
	s.result.StopReason = status
	s.result.StopDetails = bytes.Clone(m["incomplete_details"])
	s.result.ServedTier = bytes.Clone(m["service_tier"])
	s.result.ToolUsage = bytes.Clone(m["tool_usage"])
	s.result.RawUsageFinal = bytes.Clone(m["usage"])
	s.result.RawUsageMerged = nil
	if usage, err := rawObject(s.result.RawUsageFinal); err == nil {
		s.result.RawUsageMerged = usage
		if _, present := m["tool_usage"]; !present {
			s.result.ToolUsage = bytes.Clone(usage["tool_usage"])
		}
	}
	if value, present := m["error"]; present && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		if len(value) > 64<<10 {
			return ErrLimit
		}
		if _, err := rawObject(value); err != nil {
			return err
		}
		s.result.ProviderError = bytes.Clone(value)
	}
	if status == "incomplete" {
		details, err := rawObject(m["incomplete_details"])
		reason, ok := stringField(details, "reason")
		if err != nil || !ok || !slices.Contains([]string{"max_output_tokens", "max_messages", "content_filter"}, reason) {
			return ErrProtocol
		}
	}
	if _, present := m["output"]; !present {
		s.result.ContentError = true
	}
	blocks := make([]OutputBlock, 0, len(output))
	calls := map[string]bool{}
	for i, raw := range output {
		item, err := rawObject(raw)
		if err != nil {
			return err
		}
		if _, present := item["id"]; present {
			id, ok := stringField(item, "id")
			if !ok || s.associate(int64(i), id) != nil {
				return ErrProtocol
			}
		} else if _, witnessed := s.items[int64(i)]; witnessed {
			return ErrProtocol
		}
		kind, ok := stringField(item, "type")
		if !ok {
			return ErrProtocol
		}
		itemStatus, hasStatus := stringField(item, "status")
		if _, present := item["status"]; present && !hasStatus {
			s.result.ContentError = true
			continue
		}
		switch kind {
		case "message":
			if status != "completed" {
				continue
			}
			role, _ := stringField(item, "role")
			var parts []json.RawMessage
			if role != "assistant" || !hasStatus || itemStatus != "completed" || json.Unmarshal(item["content"], &parts) != nil || parts == nil {
				s.result.ContentError = true
				continue
			}
			for _, rawPart := range parts {
				part, err := rawObject(rawPart)
				if err != nil {
					s.result.ContentError = true
					continue
				}
				typ, _ := stringField(part, "type")
				if typ == "refusal" {
					s.refused = true
					continue
				}
				text, ok := stringField(part, "text")
				if typ != "output_text" || !ok || !emptyJSONArray(part["annotations"]) || !emptyJSONArray(part["logprobs"]) {
					s.result.ContentError = true
					continue
				}
				blocks = append(blocks, OutputBlock{Type: "text", Text: text})
			}
		case "function_call":
			if status != "completed" {
				continue
			}
			call, callOK := stringField(item, "call_id")
			name, nameOK := stringField(item, "name")
			args, argsOK := stringField(item, "arguments")
			asyncOK := len(item["async"]) == 0 || bytes.Equal(bytes.TrimSpace(item["async"]), []byte("false"))
			callerOK := len(item["caller"]) == 0 || bytes.Equal(bytes.TrimSpace(item["caller"]), []byte("null"))
			namespaceOK := len(item["namespace"]) == 0 || bytes.Equal(bytes.TrimSpace(item["namespace"]), []byte("null"))
			if !asyncOK || !callerOK || !namespaceOK || !callOK || !visible(call, 256) || calls[call] || !nameOK || !slices.Contains(s.compiled.toolNames, name) || !argsOK || !objectJSON([]byte(args)) || hasStatus && itemStatus != "completed" {
				s.result.ContentError = true
				continue
			}
			calls[call] = true
			blocks = append(blocks, OutputBlock{Type: "tool_use", ID: call, Name: name, Input: json.RawMessage(args)})
		case "reasoning":
			id, ok := stringField(item, "id")
			var summary []json.RawMessage
			valid := ok && visible(id, 256) && (!hasStatus || itemStatus == "completed") && json.Unmarshal(item["summary"], &summary) == nil && summary != nil
			for _, rawPart := range summary {
				part, err := rawObject(rawPart)
				typ, _ := stringField(part, "type")
				_, textOK := stringField(part, "text")
				valid = valid && err == nil && typ == "summary_text" && textOK
			}
			if value, present := item["encrypted_content"]; present && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				_, ok := stringField(item, "encrypted_content")
				valid = valid && ok
			}
			if !valid {
				s.result.ContentError = true
				continue
			}
			blocks = append(blocks, OutputBlock{Type: "responses_reasoning", OpaquePayload: bytes.Clone(raw)})
		default:
			s.unknown, s.result.ProfileViolation = true, true
		}
	}
	s.result.ContentBlocks = len(output)
	if s.refused && len(s.result.ProviderError) == 0 {
		s.result.ProviderError = json.RawMessage(`{"type":"refused"}`)
	}
	if status != "completed" || s.refused {
		blocks = slices.DeleteFunc(blocks, func(b OutputBlock) bool { return b.Type != "responses_reasoning" })
	}
	if !s.result.ContentError {
		s.result.Blocks = blocks
	}
	return nil
}

func emptyJSONArray(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && values != nil && len(values) == 0
}

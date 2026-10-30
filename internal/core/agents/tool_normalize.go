package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// modelToolCorrection contains only static schema vocabulary, never input text.
// Renderable distinguishes a schema error from arguments unsafe to replay.
type modelToolCorrection struct {
	Code       string `json:"code"`
	Field      string `json:"field,omitempty"`
	Allowed    string `json:"allowed,omitempty"`
	Renderable bool   `json:"-"`
}

func (c *modelToolCorrection) JSON() json.RawMessage {
	raw := modelJSON(c)
	if len(raw) > 512 {
		return json.RawMessage(`{"code":"invalid_argument","allowed":"declared tool argument object"}`)
	}
	return raw
}
func modelCorrection(field, allowed string, renderable bool) *modelToolCorrection {
	return &modelToolCorrection{Code: "invalid_argument", Field: field, Allowed: allowed, Renderable: renderable}
}

type modelToolInvocation struct {
	Schema          int64            `json:"schema"`
	ToolName        string           `json:"tool_name"`
	OperationSchema int64            `json:"operation_schema"`
	Operation       string           `json:"operation"`
	Target          *ledger.Resource `json:"target,omitempty"`
	Arguments       json.RawMessage  `json:"arguments"`
	Bytes           json.RawMessage  `json:"-"`
	SHA256          ledger.Digest    `json:"-"`
}

// normalizeModelTool establishes syntax and meaning only. Core must still
// authorize the target before recording/disclosing it or admitting an effect.
// No service state, clock, filesystem or provider call is consulted here.
func normalizeModelTool(name string, raw []byte, origin ledger.Binding, maxArgumentBytes int64) (modelToolInvocation, *modelToolCorrection) {
	var out modelToolInvocation
	value, ok := parseModelArguments(raw, maxArgumentBytes)
	if !ok {
		return out, modelCorrection("", "one bounded UTF-8 JSON object without duplicate keys; depth at most 32", false)
	}
	var schema *modelSchema
	for _, tool := range modelTools {
		if name == tool.name {
			schema = tool.schema
			break
		}
	}
	if schema == nil {
		return out, &modelToolCorrection{Code: "unrecognized", Renderable: true}
	}
	if !validID(origin.AgentID) || !validID(origin.ContextID) {
		return out, modelCorrection("", "valid trusted origin", true)
	}
	normalized, leaf, correction := normalizeModelValue(schema, value, "")
	if correction != nil {
		return out, correction
	}
	args := normalized.(map[string]any)
	concrete := args
	if name == "tools_invoke_v1" {
		concrete = args["arguments"].(map[string]any)
	}
	if leaf.target == "workspace" {
		if _, present := concrete["task_context_id"]; !present {
			concrete["task_context_id"] = origin.ContextID.String()
		}
	}
	if correction = modelSemanticArguments(leaf.operation, leaf.target, concrete); correction != nil {
		return out, correction
	}
	target := modelTarget(leaf.target, concrete, origin)
	out = modelToolInvocation{Schema: 1, ToolName: name, OperationSchema: 1, Operation: leaf.operation, Target: target, Arguments: modelCanonical(schema, args)}
	// Resource.MarshalJSON checks the frozen target grammar before hashing.
	if target != nil {
		if _, err := target.MarshalJSON(); err != nil {
			return modelToolInvocation{}, modelCorrection("", "valid concrete target", true)
		}
	}
	out.Bytes = modelJSON(out)
	if len(out.Bytes) > maxObjectBytes {
		return modelToolInvocation{}, modelCorrection("", "bounded normalized invocation", true)
	}
	out.SHA256 = sha256.Sum256(out.Bytes)
	return out, nil
}

func parseModelArguments(raw []byte, limit int64) (map[string]any, bool) {
	if limit <= 0 || int64(len(raw)) > limit || len(raw) > maxObjectBytes || !utf8.Valid(raw) || !modelUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var read func(int) (any, bool)
	read = func(depth int) (any, bool) {
		token, err := d.Token()
		if err != nil {
			return nil, false
		}
		switch token := token.(type) {
		case json.Delim:
			if depth >= 32 {
				return nil, false
			}
			switch token {
			case '{':
				out := map[string]any{}
				for d.More() {
					key, err := d.Token()
					name, ok := key.(string)
					if err != nil || !ok {
						return nil, false
					}
					if _, present := out[name]; present {
						return nil, false
					}
					value, ok := read(depth + 1)
					if !ok {
						return nil, false
					}
					out[name] = value
				}
				end, err := d.Token()
				return out, err == nil && end == json.Delim('}')
			case '[':
				out := []any{}
				for d.More() {
					value, ok := read(depth + 1)
					if !ok {
						return nil, false
					}
					out = append(out, value)
				}
				end, err := d.Token()
				return out, err == nil && end == json.Delim(']')
			default:
				return nil, false
			}
		default:
			return token, true
		}
	}
	value, ok := read(0)
	if !ok {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

// encoding/json replaces unpaired escaped surrogates. Refuse that implicit
// repair rather than giving a different string a canonical command identity.
func modelUnicode(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}

func modelSelection(s *modelSchema, object map[string]any) *modelSchema {
	if len(s.choices) == 0 {
		return s
	}
	value, ok := object[s.selector].(string)
	if !ok {
		return nil
	}
	for _, choice := range s.choices {
		if modelChoiceContains(choice, s.selector, value) {
			return modelSelection(choice, object)
		}
	}
	return nil
}
func modelChoiceContains(s *modelSchema, field, value string) bool {
	for _, f := range s.fields {
		if f.name == field && slices.Contains(f.schema.values, value) {
			return true
		}
	}
	for _, choice := range s.choices {
		if modelChoiceContains(choice, field, value) {
			return true
		}
	}
	return false
}
func modelFieldPath(parent, field string) string {
	if parent == "" {
		return field
	}
	return parent + "." + field
}

func normalizeModelValue(schema *modelSchema, value any, field string) (any, *modelSchema, *modelToolCorrection) {
	fail := func() (any, *modelSchema, *modelToolCorrection) {
		return nil, nil, modelCorrection(field, modelAllowed(schema), true)
	}
	if value == nil {
		return fail()
	}
	if schema.kind == "object" {
		object, ok := value.(map[string]any)
		if !ok {
			return fail()
		}
		for len(schema.choices) > 0 {
			selector, ok := object[schema.selector].(string)
			var selected *modelSchema
			for _, choice := range schema.choices {
				if ok && modelChoiceContains(choice, schema.selector, selector) {
					selected = choice
					break
				}
			}
			if selected == nil {
				return nil, nil, modelCorrection(modelFieldPath(field, schema.selector), "one declared selector value", true)
			}
			schema = selected
		}
		if schema.pathAlias {
			alias, hasAlias := object["file_path"]
			_, hasPath := object["path"]
			if hasAlias && hasPath {
				return nil, nil, modelCorrection(modelFieldPath(field, "path"), "path or file_path, never both", true)
			}
			if hasAlias {
				object["path"] = alias
				delete(object, "file_path")
			}
		}
		for name := range object {
			known := false
			for _, declared := range schema.fields {
				known = known || declared.name == name
			}
			if !known {
				return nil, nil, modelCorrection(field, "only declared fields", true)
			}
		}
		out := map[string]any{}
		for _, f := range schema.fields {
			value, present := object[f.name]
			if !present && f.optional {
				continue
			}
			if !present {
				return nil, nil, modelCorrection(modelFieldPath(field, f.name), modelAllowed(f.schema), true)
			}
			normalized, _, correction := normalizeModelValue(f.schema, value, modelFieldPath(field, f.name))
			if correction != nil {
				return nil, nil, correction
			}
			out[f.name] = normalized
		}
		return out, schema, nil
	}
	switch schema.kind {
	case "version":
		if number, ok := value.(json.Number); ok && number == "1" {
			return int64(1), schema, nil
		}
	case "uint", "integer":
		var text string
		switch value := value.(type) {
		case string:
			if schema.kind != "uint" {
				return fail()
			}
			text = value
		case json.Number:
			text = string(value)
		default:
			return fail()
		}
		if text == "" || len(text) > 19 || len(text) > 1 && text[0] == '0' {
			return fail()
		}
		for _, c := range text {
			if c < '0' || c > '9' {
				return fail()
			}
		}
		number, err := strconv.ParseInt(text, 10, 64)
		if err == nil && number >= schema.min && number <= schema.max {
			return number, schema, nil
		}
	case "ids":
		values, ok := value.([]any)
		if !ok || int64(len(values)) < schema.min {
			return fail()
		}
		out := make([]any, 0, len(values))
		previous := ""
		for _, value := range values {
			normalized, _, correction := normalizeModelValue(schema.element, value, field)
			if correction != nil {
				return nil, nil, correction
			}
			text := normalized.(string)
			if previous != "" && text <= previous {
				return fail()
			}
			previous = text
			out = append(out, text)
		}
		return out, schema, nil
	default:
		text, ok := value.(string)
		if !ok {
			return fail()
		}
		if schema.kind == "enum" {
			if slices.Contains(schema.values, text) {
				return text, schema, nil
			}
			return fail()
		}
		if int64(len(text)) < schema.min || int64(len(text)) > schema.max {
			return fail()
		}
		switch schema.kind {
		case "uuid":
			id, err := uuid.Parse(text)
			if err != nil || !validID(id) || id.String() != text {
				return fail()
			}
		case "digest":
			if _, err := ledger.ParseDigest(text); err != nil {
				return fail()
			}
		case "digest_or_absent":
			if text != "absent" {
				if _, err := ledger.ParseDigest(text); err != nil {
					return fail()
				}
			}
		case "path":
			if !ledger.ValidResourcePath(text) {
				return fail()
			}
		case "token":
			// F3-11 open token: contract/ledger/doc.go:1867-1872.
			for i, c := range text {
				if !(c >= 'a' && c <= 'z' || i > 0 && (c >= '0' && c <= '9' || c == '_')) {
					return fail()
				}
			}
		}
		return text, schema, nil
	}
	return fail()
}

func modelCanonical(s *modelSchema, value any) json.RawMessage {
	if s.kind != "object" {
		return modelJSON(value)
	}
	object := value.(map[string]any)
	s = modelSelection(s, object)
	out := []byte{'{'}
	first := true
	for _, field := range s.fields {
		value, present := object[field.name]
		if !present {
			continue
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		out = append(out, modelJSON(field.name)...)
		out = append(out, ':')
		out = append(out, modelCanonical(field.schema, value)...)
	}
	return append(out, '}')
}

func modelSemanticArguments(operation, target string, a map[string]any) *modelToolCorrection {
	bad := func(field, allowed string) *modelToolCorrection { return modelCorrection(field, allowed, true) }
	if operation == "task.assign" || operation == "task.reassign" {
		_, id := a["expected_assignment_id"]
		generation, hasGeneration := a["expected_generation"]
		if id != hasGeneration || operation == "task.reassign" && !id {
			return bad("expected_assignment_id", "complete prior assignment and positive generation pair")
		}
		if operation == "task.reassign" && generation == int64(math.MaxInt64) {
			return bad("expected_generation", "generation below maximum integer")
		}
	}
	if operation == "task.create" {
		for _, id := range a["dependencies"].([]any) {
			if id == a["task_id"] {
				return bad("dependencies", "sorted distinct other task IDs")
			}
		}
		if assignment, ok := a["assignment"].(map[string]any); ok {
			assignee := assignment["assignee"].(map[string]any)["agent_id"]
			found := false
			for _, id := range a["audience"].([]any) {
				found = found || id == assignee
			}
			if !found {
				return bad("assignment.assignee.agent_id", "an agent in the declared audience")
			}
		}
	}
	if target == "context_read" && a["slot"] != "transcript" && a["cursor"] != int64(0) {
		return bad("cursor", "zero for a non-transcript slot")
	}
	if target == "artifact" && a["artifact"].(map[string]any)["bytes"].(int64) > a["max_bytes"].(int64) {
		return bad("max_bytes", "at least the complete artifact byte count")
	}
	return nil
}

func modelTarget(kind string, a map[string]any, origin ledger.Binding) *ledger.Resource {
	if kind == "" {
		return nil
	}
	id := func(name string) uuid.UUID { value, _ := uuid.Parse(a[name].(string)); return value }
	r := ledger.Resource{Kind: kind}
	switch kind {
	case "workspace":
		r.WorkspaceID = id("workspace_id")
		r.Binding = ledger.Binding{AgentID: origin.AgentID, ContextID: id("task_context_id")}
		r.PathPrefix = a["path"].(string)
	case "task":
		r.TaskID = id("task_id")
	case "task_status":
		r.Kind = "task"
		r.TaskID = id("id")
	case "context":
		r.Binding = origin
		r.Scope = "context"
		r.Slots = []string{}
	case "private", "context_read", "handoff":
		r.Kind = "context"
		r.Binding = origin
		r.Scope = "context"
		if scope, ok := a["scope"].(string); ok {
			r.Scope = scope
		}
		slot := "handoff"
		if value, ok := a["slot"].(string); ok {
			slot = value
		}
		r.Slots = []string{slot}
	case "artifact":
		object := a["artifact"].(map[string]any)
		digest, _ := ledger.ParseDigest(object["sha256"].(string))
		r.Artifact = ledger.ArtifactRef{SourceSequence: object["source_sequence"].(int64), SHA256: digest, Bytes: object["bytes"].(int64)}
	}
	return &r
}

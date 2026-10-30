package agents

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

// One closed description drives generation and model-only normalization. It is
// not a general JSON Schema engine and is never used by a ledger/law decoder.
type modelSchema struct {
	kind              string
	min, max          int64
	values            []string
	fields            []modelField
	element           *modelSchema
	selector          string
	choices           []*modelSchema
	pathAlias         bool
	operation, target string
}
type modelField struct {
	name     string
	schema   *modelSchema
	optional bool
}
type modelTool struct {
	name, description string
	schema            *modelSchema
}

func modelString(kind string, min, max int64) *modelSchema {
	return &modelSchema{kind: kind, min: min, max: max}
}
func modelEnum(values ...string) *modelSchema { return &modelSchema{kind: "enum", values: values} }
func modelUint(min, max int64) *modelSchema   { return &modelSchema{kind: "uint", min: min, max: max} }
func modelInteger(min, max int64) *modelSchema {
	return &modelSchema{kind: "integer", min: min, max: max}
}
func modelObject(fields ...modelField) *modelSchema {
	return &modelSchema{kind: "object", fields: fields}
}
func modelRequired(name string, s *modelSchema) modelField { return modelField{name: name, schema: s} }
func modelOptional(name string, s *modelSchema) modelField {
	return modelField{name: name, schema: s, optional: true}
}
func modelArguments(op, target string, fields ...modelField) *modelSchema {
	s := modelObject(append([]modelField{modelRequired("schema", &modelSchema{kind: "version"})}, fields...)...)
	s.operation, s.target = op, target
	return s
}
func modelChoice(selector string, choices ...*modelSchema) *modelSchema {
	return &modelSchema{kind: "object", selector: selector, choices: choices}
}

var modelTools, modelCatalogue = makeModelTools()

func makeModelTools() ([]modelTool, []modelTool) {
	req, opt := modelRequired, modelOptional
	id := modelString("uuid", 36, 36)
	text := modelString("string", 0, maxObjectBytes)
	digest := modelString("digest", 64, 64)
	positive := modelUint(1, math.MaxInt64)
	version := modelUint(1, math.MaxInt64-1)
	readBytes := modelUint(1, maxOperationBytes)
	binding := modelObject(req("agent_id", id), req("context_id", id))
	assignment := modelObject(req("assignment_id", id), req("assignee", binding))
	// Nested frozen references keep plain-integer encodings; only table-declared
	// U members accept strings (contract/ledger/scoped_values.go:73-89,
	// contract/ledger/doc.go:2146-2165).
	referenceSequence := modelInteger(1, math.MaxInt64)
	referenceBytes := modelInteger(0, math.MaxInt64)
	artifact := modelObject(req("source_sequence", referenceSequence), req("sha256", digest), req("bytes", referenceBytes))
	privateRevision := modelChoice("scope",
		modelObject(req("binding", binding), req("scope", modelEnum("agent")), req("slot", modelEnum("instructions", "memory")), req("revision", referenceSequence), req("source_sequence", referenceSequence), req("sha256", digest), req("bytes", referenceBytes)),
		modelObject(req("binding", binding), req("scope", modelEnum("context")), req("slot", modelEnum("continuation", "handoff", "memory", "transcript")), req("revision", referenceSequence), req("source_sequence", referenceSequence), req("sha256", digest), req("bytes", referenceBytes)),
	)
	subject := modelChoice("kind", modelObject(req("kind", modelEnum("artifact")), req("artifact", artifact)), modelObject(req("kind", modelEnum("private_revision")), req("private_revision", privateRevision)))
	file := func(name, op, description string, fields ...modelField) modelTool {
		minPath := int64(1)
		if op == "file.search" {
			minPath = 0
		}
		base := []modelField{req("workspace_id", id), opt("task_context_id", id), req("path", modelString("path", minPath, 1024))}
		s := modelArguments(op, "workspace", append(base, fields...)...)
		s.pathAlias = true
		return modelTool{name, description, s}
	}
	common := []modelTool{
		file("file_read_v1", "file.read", "Read one complete scoped UTF-8 file.", req("max_bytes", readBytes)),
		file("file_search_v1", "file.search", "Search scoped paths or text by literal substring.", req("mode", modelEnum("path", "text")), req("query", text), req("max_bytes", readBytes)),
		file("file_write_v1", "file.write", "Write a scoped file with an exact previous digest or absent expectation.", req("expected_sha256", modelString("digest_or_absent", 1, 64)), req("text", text)),
		file("file_edit_v1", "file.edit", "Replace exactly one occurrence in a scoped file with an exact previous digest.", req("expected_sha256", digest), req("old_text", modelString("string", 1, maxObjectBytes)), req("new_text", text)),
	}
	status := modelChoice("subject", modelArguments("context.inspect", "context", req("subject", modelEnum("context"))), modelArguments("turn.inspect", "context", req("subject", modelEnum("turn")), req("id", id)), modelArguments("task.inspect", "task_status", req("subject", modelEnum("task")), req("id", id)))
	common = append(common, modelTool{"core_status_v1", "Inspect authorized context, turn or task metadata.", status})
	var taskChoices []*modelSchema
	for _, action := range []string{"start", "wait", "resume", "submit", "cancel"} {
		fields := []modelField{req("action", modelEnum(action)), req("task_id", id), req("expected_version", version), req("assignment_id", id), req("generation", positive)}
		if action == "wait" {
			fields = append(fields, req("wait_reason", modelEnum("dependency", "input", "review", "explicit_hold")), opt("wait_artifact", subject))
		}
		if action == "submit" {
			fields = append(fields, req("submission", subject))
		}
		taskChoices = append(taskChoices, modelArguments("task."+action, "task", fields...))
	}
	common = append(common, modelTool{"core_task_v1", "Apply a typed task transition with exact version and assignment expectations.", modelChoice("action", taskChoices...)})
	var memoryChoices []*modelSchema
	for _, action := range []string{"read", "write"} {
		var scopes []*modelSchema
		for _, scope := range []string{"agent", "context"} {
			slots := modelEnum("memory")
			if scope == "context" {
				slots = modelEnum("memory", "handoff")
			}
			rev := positive
			if action == "write" {
				rev = version
			}
			fields := []modelField{req("action", modelEnum(action)), req("scope", modelEnum(scope)), req("slot", slots), req("revision", rev)}
			if action == "read" {
				fields = append(fields, req("max_bytes", readBytes))
			} else {
				fields = append(fields, req("text", text))
			}
			scopes = append(scopes, modelArguments("private."+action, "private", fields...))
		}
		memoryChoices = append(memoryChoices, modelChoice("scope", scopes...))
	}
	common = append(common, modelTool{"core_memory_v1", "Read or compare-and-swap exact private memory or context handoff.", modelChoice("action", memoryChoices...)})
	catalogue := []modelTool{
		{"core.task.create", "Create a task with immutable description, acceptance digest and audience.", modelArguments("task.create", "task", req("task_id", id), req("description", text), req("acceptance_sha256", digest), req("dependencies", &modelSchema{kind: "ids", min: 0, element: id}), req("audience", &modelSchema{kind: "ids", min: 1, element: id}), opt("assignment", assignment))},
		{"core.task.claim", "Claim an unassigned task for the bound context.", modelArguments("task.claim", "task", req("task_id", id), req("expected_version", version), req("new_assignment_id", id))},
	}
	for _, action := range []string{"assign", "complete", "fail", "reassign"} {
		fields := []modelField{req("task_id", id), req("expected_version", version)}
		if action == "assign" || action == "reassign" {
			fields = append(fields, opt("expected_assignment_id", id), opt("expected_generation", positive), req("new_assignment", assignment))
		} else {
			fields = append(fields, req("assignment_id", id), req("generation", positive))
			if action == "complete" {
				fields = append(fields, req("submission", subject))
			} else {
				fields = append(fields, req("failure_cause", modelString("token", 1, 64)), req("failure_evidence", subject))
			}
		}
		catalogue = append(catalogue, modelTool{"core.task." + action, "Apply the concrete task operation with exact expectations.", modelArguments("task."+action, "task", fields...)})
	}
	catalogue = append(catalogue,
		modelTool{"core.task.list", "List authorized task facts without a resumable ledger cursor.", modelArguments("task.list", "", req("limit", positive))},
		modelTool{"core.context.read", "Read a whole-occurrence page of an exact own-context private revision.", modelArguments("private.read", "context_read", req("slot", modelEnum("continuation", "handoff", "memory", "transcript")), req("revision", positive), req("cursor", modelUint(0, math.MaxInt64)), req("max_bytes", readBytes))},
		modelTool{"core.context.handoff.write", "Replace the bound context handoff with an exact revision expectation.", modelArguments("private.write", "handoff", req("expected_version", version), req("text", text))},
		modelTool{"core.artifact.read", "Read exact authorized UTF-8 artifact bytes.", modelArguments("artifact.read", "artifact", req("artifact", artifact), req("max_bytes", readBytes))},
	)
	paths := make([]string, 0, len(catalogue))
	var invocations []*modelSchema
	for _, tool := range catalogue {
		paths = append(paths, tool.name)
		invocations = append(invocations, modelArguments(tool.schema.operation, tool.schema.target, req("tool_path", modelEnum(tool.name)), req("operation_schema", &modelSchema{kind: "version"}), req("arguments", tool.schema)))
	}
	common = append(common,
		modelTool{"tools_discover_v1", "Discover the permission-filtered local catalogue; discovery grants no authority.", modelArguments("tools.discover", "", req("query", text), opt("after", modelEnum(paths...)), req("limit", positive))},
		modelTool{"tools_invoke_v1", "Invoke one exact discovered local operation; permission is checked again.", modelChoice("tool_path", invocations...)},
	)
	return common, catalogue
}

func modelJSON(v any) []byte {
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	e.SetEscapeHTML(false)
	// Only validated Go values and generated schemas reach this encoder.
	if err := e.Encode(v); err != nil {
		panic("invalid model schema value")
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'})
}

func modelToolDefinitions() []providers.Tool {
	out := make([]providers.Tool, 0, len(modelTools))
	for _, tool := range modelTools {
		out = append(out, providers.Tool{Name: tool.name, Description: tool.description, InputSchema: modelSchemaJSON(tool.schema)})
	}
	return out
}

func modelSchemaJSON(s *modelSchema) json.RawMessage {
	if len(s.choices) > 0 {
		choices := make([]json.RawMessage, 0, len(s.choices))
		for _, choice := range s.choices {
			choices = append(choices, modelSchemaJSON(choice))
		}
		return modelJSON(struct {
			Type  string            `json:"type"`
			OneOf []json.RawMessage `json:"oneOf"`
		}{"object", choices})
	}
	if s.kind == "object" {
		if s.pathAlias {
			canonical, alias := *s, *s
			canonical.pathAlias, alias.pathAlias = false, false
			alias.fields = append([]modelField(nil), s.fields...)
			for i := range alias.fields {
				if alias.fields[i].name == "path" {
					alias.fields[i].name = "file_path"
				}
			}
			return modelJSON(struct {
				Type  string            `json:"type"`
				OneOf []json.RawMessage `json:"oneOf"`
			}{"object", []json.RawMessage{modelSchemaJSON(&canonical), modelSchemaJSON(&alias)}})
		}
		properties := []byte{'{'}
		required := []string{}
		for i, field := range s.fields {
			if i > 0 {
				properties = append(properties, ',')
			}
			properties = append(properties, modelJSON(field.name)...)
			properties = append(properties, ':')
			properties = append(properties, modelSchemaJSON(field.schema)...)
			if !field.optional {
				required = append(required, field.name)
			}
		}
		properties = append(properties, '}')
		return modelJSON(struct {
			Type                 string          `json:"type"`
			Properties           json.RawMessage `json:"properties"`
			Required             []string        `json:"required"`
			AdditionalProperties bool            `json:"additionalProperties"`
		}{"object", properties, required, false})
	}
	switch s.kind {
	case "version":
		return json.RawMessage(`{"type":"integer","const":1}`)
	case "integer":
		return modelJSON(struct {
			Type    string `json:"type"`
			Minimum int64  `json:"minimum"`
			Maximum int64  `json:"maximum"`
		}{"integer", s.min, s.max})
	case "uint":
		return modelJSON(struct {
			AnyOf []any `json:"anyOf"`
		}{[]any{
			struct {
				Type    string `json:"type"`
				Minimum int64  `json:"minimum"`
				Maximum int64  `json:"maximum"`
			}{"integer", s.min, s.max},
			struct {
				Type      string `json:"type"`
				Pattern   string `json:"pattern"`
				MaxLength int    `json:"maxLength"`
			}{"string", `^(0|[1-9][0-9]*)$`, 19},
		}})
	case "enum":
		return modelJSON(struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		}{"string", s.values})
	case "ids":
		return modelJSON(struct {
			Type     string          `json:"type"`
			Items    json.RawMessage `json:"items"`
			MinItems int64           `json:"minItems"`
			Unique   bool            `json:"uniqueItems"`
		}{"array", modelSchemaJSON(s.element), s.min, true})
	default:
		pattern := ""
		switch s.kind {
		case "uuid":
			pattern = `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
		case "digest":
			pattern = `^[0-9a-f]{64}$`
		case "digest_or_absent":
			pattern = `^(absent|[0-9a-f]{64})$`
		case "token":
			pattern = `^[a-z][a-z0-9_]*$`
		}
		return modelJSON(struct {
			Type      string `json:"type"`
			MinLength int64  `json:"minLength"`
			MaxLength int64  `json:"maxLength"`
			Pattern   string `json:"pattern,omitempty"`
		}{"string", s.min, s.max, pattern})
	}
}

func modelAllowed(s *modelSchema) string {
	switch s.kind {
	case "version":
		return "schema 1"
	case "uint":
		return "integer or canonical unsigned decimal string within the declared bounds"
	case "integer":
		return "plain unsigned JSON integer within the declared bounds"
	case "uuid":
		return "canonical lowercase UUIDv7"
	case "digest":
		return "nonzero lowercase SHA-256 digest"
	case "digest_or_absent":
		return "nonzero lowercase SHA-256 digest or absent"
	case "path":
		return "one strict relative path or exclusive file_path alias"
	case "token":
		return "lowercase operation token"
	case "enum":
		return strings.Join(s.values, " | ")
	case "ids":
		return "sorted distinct canonical UUIDv7 array"
	case "object":
		return "closed object with the declared fields"
	default:
		return "UTF-8 string within the declared byte bounds"
	}
}

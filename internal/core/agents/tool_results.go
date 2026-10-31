package agents

import (
	"encoding/json"
	"math"
)

// These are the ten concrete result shapes, not a permissive schema for any
// native method. The same ordered closed-object encoder describes arguments.
func loopResultSchema(path string) json.RawMessage {
	req, opt := modelRequired, modelOptional
	id := modelString("string", 36, 36) // Native owner metadata may contain zero UUIDs.
	text := modelString("string", 0, maxObjectBytes)
	digest := modelString("digest", 64, 64)
	number := modelInteger(0, math.MaxInt64)
	boolean := &modelSchema{kind: "boolean"}
	array := func(element *modelSchema) *modelSchema { return &modelSchema{kind: "array", element: element} }
	binding := modelObject(req("agent_id", id), req("context_id", id))
	origin := modelObject(req("agent_id", id), req("session_id", id), req("context_id", id))
	artifact := modelObject(req("source_sequence", number), req("sha256", digest), req("bytes", number))
	contextTarget := modelObject(req("kind", modelEnum("context")), req("binding", binding), req("scope", modelEnum("agent", "context")), req("slots", array(text)))
	taskTarget := modelObject(req("kind", modelEnum("task")), req("task_id", id))
	artifactTarget := modelObject(req("kind", modelEnum("artifact")), req("artifact", artifact))
	receipt := func(target *modelSchema) *modelSchema {
		return modelObject(req("CommandID", id), req("SubmissionSHA256", digest), req("Actor", modelEnum("agent")), req("Origin", origin), req("Operation", text), req("Target", target), req("GrantID", id), req("Outcome", text), req("Version", number), req("WorkID", id), req("Sequence", number))
	}
	var data *modelSchema
	switch path {
	case "core.task.create", "core.task.claim", "core.task.assign", "core.task.complete", "core.task.fail", "core.task.reassign":
		data = modelObject(req("receipt", receipt(taskTarget)), req("state", text))
	case "core.task.list":
		assignment := modelObject(req("assignment_id", id), req("generation", number), req("assignee", binding), req("assigner_actor", modelEnum("owner", "agent")), req("assigner", origin))
		fact := modelObject(req("TaskID", id), req("Version", number), req("State", text), req("Assignment", assignment), req("DescriptionRef", artifact), req("Sequence", number), req("Readiness", text), req("Reason", text))
		counts := modelObject(req("WaitingReady", number), req("WaitingBlocked", number), req("Active", number))
		data = modelObject(req("waiting_ready", array(fact)), req("waiting_blocked", array(fact)), req("active", array(fact)), req("counts", counts), req("through_sequence", number), req("incomplete", boolean))
	case "core.context.read":
		chunk := modelObject(req("revision", number), req("sequence", number), req("sha256", digest), req("bytes", number), req("text", text))
		data = modelObject(req("receipt", receipt(contextTarget)), req("revision", number), req("present", boolean), req("sha256", digest), req("bytes", number), opt("text", text), opt("chunks", array(chunk)), req("next_cursor", number))
	case "core.context.handoff.write":
		data = modelObject(req("receipt", receipt(contextTarget)), req("revision", number))
	case "core.artifact.read":
		data = modelObject(req("receipt", receipt(artifactTarget)), req("text", text), req("sha256", digest), req("bytes", number))
	default:
		panic("unregistered loop result schema")
	}
	errorData := modelObject(req("code", modelEnum("invalid_argument", "denied", "busy", "conflict", "too_large", "unavailable", "unknown_effect")), opt("field", text), opt("allowed", text))
	return modelSchemaJSON(modelChoice("status",
		modelObject(req("schema", &modelSchema{kind: "version"}), req("tool_id", id), req("status", modelEnum("ok")), req("data", data)),
		modelObject(req("schema", &modelSchema{kind: "version"}), req("tool_id", id), req("status", modelEnum("error")), req("error", errorData)),
	))
}

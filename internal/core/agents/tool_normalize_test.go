package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

const modelID1 = "019a1500-0000-7000-8000-000000000001"
const modelID2 = "019a1500-0000-7000-8000-000000000002"
const modelID3 = "019a1500-0000-7000-8000-000000000003"
const modelDigest = "1111111111111111111111111111111111111111111111111111111111111111"

func modelOrigin() ledger.Binding {
	return ledger.Binding{AgentID: uuid.MustParse(modelID1), ContextID: uuid.MustParse(modelID2)}
}
func modelReadArguments() string {
	return `{"schema":1,"workspace_id":"` + modelID3 + `","path":"notes.txt","max_bytes":64}`
}
func normalizeFixture(t testing.TB, name, raw string) modelToolInvocation {
	t.Helper()
	result, correction := normalizeModelTool(name, []byte(raw), modelOrigin(), 65536)
	if correction != nil {
		t.Fatalf("normalization refused declared form: %s", correction.JSON())
	}
	return result
}

func TestModelToolNormalizationIdentity(t *testing.T) {
	t.Parallel()
	base := normalizeFixture(t, "file_read_v1", modelReadArguments())
	for _, raw := range []string{
		strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":"64"`, 1),
		strings.Replace(modelReadArguments(), `"path"`, `"file_path"`, 1),
		strings.Replace(modelReadArguments(), `"path":`, `"task_context_id":"`+modelID2+`","path":`, 1),
		` { "max_bytes" : "64", "file_path":"notes.txt", "workspace_id":"` + modelID3 + `", "schema":1 } `,
	} {
		got := normalizeFixture(t, "file_read_v1", raw)
		if !bytes.Equal(got.Bytes, base.Bytes) || got.SHA256 != base.SHA256 {
			t.Fatal("equivalent spelling changed command meaning")
		}
	}
	for _, raw := range []string{strings.Replace(modelReadArguments(), "notes.txt", "other.txt", 1), strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":65`, 1), strings.Replace(modelReadArguments(), modelID3, modelID1, 1), strings.Replace(modelReadArguments(), `"path":`, `"task_context_id":"`+modelID3+`","path":`, 1)} {
		got := normalizeFixture(t, "file_read_v1", raw)
		if got.SHA256 == base.SHA256 {
			t.Fatal("changed concrete meaning reused command digest")
		}
	}
	if base.Operation != "file.read" || base.Target == nil || base.Target.Binding != modelOrigin() || base.Target.PathPrefix != "notes.txt" || base.SHA256 != sha256.Sum256(base.Bytes) {
		t.Fatal("normalized identity lost target or bytes")
	}
}

func TestModelToolNormalizationRejectsAmbiguity(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, raw  string
		renderable bool
	}{
		{"duplicate", strings.Replace(modelReadArguments(), `"schema":1`, `"schema":1,"schema":1`, 1), false},
		{"conflicting_alias", strings.Replace(modelReadArguments(), `"path":`, `"file_path":"notes.txt","path":`, 1), true},
		{"unknown", strings.Replace(modelReadArguments(), `"schema":1`, `"schema":1,"syntheticValue":1`, 1), true},
		{"null", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":null`, 1), true},
		{"exponent", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":64e0`, 1), true},
		{"fraction", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":64.0`, 1), true},
		{"plus", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":"+64"`, 1), true},
		{"leading_zero", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":"064"`, 1), true},
		{"integer_overflow", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":"9223372036854775808"`, 1), true},
		{"concrete_bound", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":4194305`, 1), true},
		{"boolean", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":true`, 1), true},
		{"singleton_array", strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":[64]`, 1), true},
		{"version_string", strings.Replace(modelReadArguments(), `"schema":1`, `"schema":"1"`, 1), true},
		{"version_unknown", strings.Replace(modelReadArguments(), `"schema":1`, `"schema":2`, 1), true},
		{"path_cleaning", strings.Replace(modelReadArguments(), "notes.txt", "a/../notes.txt", 1), true},
		{"reserved_path", strings.Replace(modelReadArguments(), "notes.txt", ".GiT/config", 1), true},
		{"uuid_repair", strings.Replace(modelReadArguments(), modelID3, strings.ToUpper(modelID3), 1), true},
		{"trailing", modelReadArguments() + `{}`, false},
		{"nonobject", `[]`, false},
		{"utf8", string([]byte{255}), false},
		{"surrogate", strings.Replace(modelReadArguments(), "notes.txt", `\ud800`, 1), false},
		{"depth", `{"x":` + strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32) + `}`, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out, c := normalizeModelTool("file_read_v1", []byte(row.raw), modelOrigin(), 65536)
			if c == nil || c.Renderable != row.renderable || out.Bytes != nil {
				t.Fatal("ambiguous input became a command")
			}
			if len(c.JSON()) > 512 || !json.Valid(c.JSON()) || bytes.Contains(c.JSON(), []byte("syntheticValue")) {
				t.Fatal("unsafe correction")
			}
		})
	}
	t.Run("size", func(t *testing.T) {
		t.Parallel()
		_, c := normalizeModelTool("file_read_v1", []byte(modelReadArguments()), modelOrigin(), int64(len(modelReadArguments())-1))
		if c == nil || c.Renderable {
			t.Fatal("argument cap ignored")
		}
	})
}

func modelValidInvocations() map[string]string {
	artifact := `{"source_sequence":1,"sha256":"` + modelDigest + `","bytes":1}`
	subject := `{"kind":"artifact","artifact":` + artifact + `}`
	expected := `"task_id":"` + modelID3 + `","expected_version":"1","assignment_id":"` + modelID1 + `","generation":"1"`
	assignment := `{"assignment_id":"` + modelID3 + `","assignee":{"agent_id":"` + modelID1 + `","context_id":"` + modelID2 + `"}}`
	return map[string]string{
		"file_read_v1":               modelReadArguments(),
		"file_search_v1":             `{"schema":1,"workspace_id":"` + modelID3 + `","path":"","mode":"text","query":"","max_bytes":"1"}`,
		"file_write_v1":              `{"schema":1,"workspace_id":"` + modelID3 + `","file_path":"a","expected_sha256":"absent","text":""}`,
		"file_edit_v1":               `{"schema":1,"workspace_id":"` + modelID3 + `","path":"a","expected_sha256":"` + modelDigest + `","old_text":"x","new_text":""}`,
		"core_status_v1":             `{"schema":1,"subject":"context"}`,
		"core_task_v1":               `{"schema":1,"action":"wait",` + expected + `,"wait_reason":"input","wait_artifact":` + subject + `}`,
		"core_memory_v1":             `{"schema":1,"action":"write","scope":"agent","slot":"memory","revision":"1","text":"a"}`,
		"tools_discover_v1":          `{"schema":1,"query":"","limit":"8"}`,
		"core.task.create":           `{"schema":1,"task_id":"` + modelID3 + `","description":"task","acceptance_sha256":"` + modelDigest + `","dependencies":[],"audience":["` + modelID1 + `"],"assignment":` + assignment + `}`,
		"core.task.claim":            `{"schema":1,"task_id":"` + modelID3 + `","expected_version":"1","new_assignment_id":"` + modelID1 + `"}`,
		"core.task.assign":           `{"schema":1,"task_id":"` + modelID3 + `","expected_version":"1","new_assignment":` + assignment + `}`,
		"core.task.complete":         `{"schema":1,` + expected + `,"submission":` + subject + `}`,
		"core.task.fail":             `{"schema":1,` + expected + `,"failure_cause":"synthetic_failure","failure_evidence":` + subject + `}`,
		"core.task.reassign":         `{"schema":1,"task_id":"` + modelID3 + `","expected_version":"1","expected_assignment_id":"` + modelID2 + `","expected_generation":"1","new_assignment":` + assignment + `}`,
		"core.task.list":             `{"schema":1,"limit":"1"}`,
		"core.context.read":          `{"schema":1,"slot":"transcript","revision":"1","cursor":"2","max_bytes":"64"}`,
		"core.context.handoff.write": `{"schema":1,"expected_version":"1","text":"handoff"}`,
		"core.artifact.read":         `{"schema":1,"artifact":` + artifact + `,"max_bytes":"1"}`,
	}
}
func modelInvocation(name, raw string) (string, string) {
	if strings.Contains(name, ".") {
		return "tools_invoke_v1", `{"schema":1,"tool_path":"` + name + `","operation_schema":1,"arguments":` + raw + `}`
	}
	return name, raw
}

func TestModelToolNormalizationClosedCatalogue(t *testing.T) {
	t.Parallel()
	for name, raw := range modelValidInvocations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tool, args := modelInvocation(name, raw)
			out := normalizeFixture(t, tool, args)
			if out.Operation == "" || out.SHA256 != sha256.Sum256(out.Bytes) {
				t.Fatal("missing concrete command identity")
			}
			// The same decoded values must normalize back to the exact byte identity.
			again := normalizeFixture(t, tool, string(out.Arguments))
			if !bytes.Equal(out.Bytes, again.Bytes) {
				t.Fatal("normalization was not idempotent")
			}
			bad := strings.Replace(args, `"schema":1`, `"schema":1,"injected":true`, 1)
			if _, c := normalizeModelTool(tool, []byte(bad), modelOrigin(), 65536); c == nil {
				t.Fatal("closed object accepted injected authority")
			}
		})
	}
	for _, row := range []struct{ name, raw string }{
		{"core_memory_v1", `{"schema":1,"action":"read","scope":"agent","slot":"handoff","revision":1,"max_bytes":1}`},
		{"core_memory_v1", `{"schema":1,"action":"read","scope":"context","slot":"memory","revision":1,"max_bytes":1,"text":"x"}`},
		{"core_task_v1", `{"schema":1,"action":"wait","task_id":"` + modelID1 + `","expected_version":1,"assignment_id":"` + modelID2 + `","generation":1}`},
		{"core_status_v1", `{"schema":1,"subject":"context","id":"` + modelID3 + `"}`},
		{"core.context.read", `{"schema":1,"slot":"memory","revision":1,"cursor":1,"max_bytes":1}`},
		{"core.task.assign", strings.Replace(modelValidInvocations()["core.task.assign"], `"new_assignment":`, `"expected_generation":1,"new_assignment":`, 1)},
		{"core.task.create", strings.Replace(modelValidInvocations()["core.task.create"], `"dependencies":[]`, `"dependencies":["`+modelID3+`"]`, 1)},
		{"core.task.create", strings.Replace(modelValidInvocations()["core.task.create"], `"audience":["`+modelID1+`"]`, `"audience":["`+modelID2+`","`+modelID1+`"]`, 1)},
		{"core.task.complete", strings.Replace(modelValidInvocations()["core.task.complete"], `"source_sequence":1`, `"source_sequence":1,"other":2`, 1)},
		{"core.task.complete", strings.Replace(modelValidInvocations()["core.task.complete"], `"source_sequence":1`, `"source_sequence":"1"`, 1)},
		{"core.task.complete", strings.Replace(modelValidInvocations()["core.task.complete"], `"kind":"artifact"`, `"kind":"unknown"`, 1)},
		{"core.artifact.read", strings.Replace(modelValidInvocations()["core.artifact.read"], `"bytes":1`, `"bytes":2`, 1)},
		{"workspace.promotion.request", `{"schema":1}`}, {"tools_invoke_v1", `{"schema":1,"tool_path":"tools_invoke_v1","operation_schema":1,"arguments":{}}`},
	} {
		tool, args := modelInvocation(row.name, row.raw)
		_, c := normalizeModelTool(tool, []byte(args), modelOrigin(), 65536)
		if c == nil {
			t.Fatalf("conditional or nested schema accepted: %s", row.name)
		}
	}
}

func FuzzModelToolArguments(f *testing.F) {
	for name, raw := range modelValidInvocations() {
		name, raw = modelInvocation(name, raw)
		f.Add(name, []byte(raw), uint32(65536))
	}
	for _, raw := range []string{`{}`, `[]`, `{"schema":1,"schema":1}`, `{"schema":null}`, `{"schema":1,"unknown":{"a":1,"a":2}}`, strings.Replace(modelReadArguments(), `"max_bytes":64`, `"max_bytes":"064"`, 1)} {
		f.Add("file_read_v1", []byte(raw), uint32(65536))
	}
	f.Fuzz(func(t *testing.T, name string, raw []byte, limit uint32) {
		cap := int64(limit % ((1 << 20) + 1))
		out, c := normalizeModelTool(name, raw, modelOrigin(), cap)
		if c != nil {
			if len(c.JSON()) > 512 || !json.Valid(c.JSON()) || out.Bytes != nil {
				t.Fatal("invalid correction boundary")
			}
			return
		}
		if int64(len(raw)) > cap || !json.Valid(out.Bytes) || out.SHA256 != sha256.Sum256(out.Bytes) {
			t.Fatal("normalization violated bound or identity")
		}
		again, other := normalizeModelTool(name, out.Arguments, modelOrigin(), maxObjectBytes)
		if other != nil || !bytes.Equal(out.Bytes, again.Bytes) {
			t.Fatalf("normalization not idempotent: %s", fmt.Sprint(other))
		}
	})
}

func TestModelToolNormalizationConditionalTargets(t *testing.T) {
	t.Parallel()
	for _, subject := range []string{"task", "turn"} {
		out := normalizeFixture(t, "core_status_v1", `{"schema":1,"subject":"`+subject+`","id":"`+modelID3+`"}`)
		if out.Operation != subject+".inspect" || out.Target == nil {
			t.Fatal("status escaped the concrete inspect operation")
		}
	}
	for _, action := range []string{"start", "resume", "submit", "cancel"} {
		raw := `{"schema":1,"action":"` + action + `","task_id":"` + modelID3 + `","expected_version":1,"assignment_id":"` + modelID1 + `","generation":1`
		if action == "submit" {
			raw += `,"submission":{"kind":"private_revision","private_revision":{"binding":{"agent_id":"` + modelID1 + `","context_id":"` + modelID2 + `"},"scope":"context","slot":"transcript","revision":1,"source_sequence":2,"sha256":"` + modelDigest + `","bytes":1}}`
		}
		raw += "}"
		out := normalizeFixture(t, "core_task_v1", raw)
		if out.Operation != "task."+action || out.Target == nil || out.Target.Kind != "task" {
			t.Fatal("task action lost its typed target")
		}
		if _, c := normalizeModelTool("core_task_v1", []byte(strings.Replace(raw, `"expected_version":1`, `"expected_version":9223372036854775807`, 1)), modelOrigin(), 65536); c == nil {
			t.Fatal("task mutation accepted overflowing successor")
		}
	}
	for _, scope := range []string{"agent", "context"} {
		for _, action := range []string{"read", "write"} {
			slot := "memory"
			if scope == "context" {
				slot = "handoff"
			}
			raw := `{"schema":1,"scope":"` + scope + `","action":"` + action + `","slot":"` + slot + `","revision":1`
			if action == "read" {
				raw += `,"max_bytes":1`
			} else {
				raw += `,"text":""`
			}
			raw += "}"
			out := normalizeFixture(t, "core_memory_v1", raw)
			if out.Operation != "private."+action || out.Target == nil || out.Target.Scope != scope || len(out.Target.Slots) != 1 || out.Target.Slots[0] != slot {
				t.Fatal("private action changed scope or slot")
			}
		}
	}
	_, correction := normalizeModelTool("undeclared", []byte(`{"syntheticValue":1}`), modelOrigin(), 65536)
	if correction == nil || correction.Code != "unrecognized" || bytes.Contains(correction.JSON(), []byte("syntheticValue")) {
		t.Fatal("unknown tool produced a command or echoed input")
	}
}

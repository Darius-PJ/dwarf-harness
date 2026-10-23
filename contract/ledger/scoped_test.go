package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"uuid"
)

func decodeF3_11(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeGrantIssued:
		return DecodeRuntimeGrantIssued(js)
	case TypeRuntimeGrantRevoked:
		return DecodeRuntimeGrantRevoked(js)
	case TypeRuntimeTaskChanged:
		return DecodeRuntimeTaskChanged(js)
	case TypeRuntimeCapacityConfigured:
		return DecodeRuntimeCapacityConfigured(js)
	case TypeRuntimeWorkAdmitted:
		return DecodeRuntimeWorkAdmitted(js)
	case TypeRuntimeWorkDispatched:
		return DecodeRuntimeWorkDispatched(js)
	case TypeRuntimeWorkEnded:
		return DecodeRuntimeWorkEnded(js)
	case TypeRuntimeLocalUsed:
		return DecodeRuntimeLocalUsed(js)
	case TypePrivateScopedRevision:
		return DecodePrivateScopedRevision(js)
	case TypeRuntimeScopedReceipt:
		return DecodeRuntimeScopedReceipt(js)
	}
	panic(typ)
}
func scopedHash(s string) Digest  { return Digest(sha256.Sum256([]byte(s))) }
func scopedOriginFixture() Origin { return Origin{fid("a1"), fid("b1"), fid("d1")} }
func scopedScopes() []ScopeKey {
	return []ScopeKey{{Kind: "colony"}, {"agent", fid("a1")}, {"context", fid("d1")}, {"grant", fid("11")}}
}
func scopedContext() Resource {
	return Resource{Kind: "context", Binding: Binding{fid("a1"), fid("d1")}, Scope: "context", Slots: []string{"memory"}}
}
func scopedGrantFixture() RuntimeGrantIssued {
	return RuntimeGrantIssued{GrantID: fid("11"), AuthorityVersion: 1, Grantee: Binding{fid("a1"), fid("d1")}, Operations: []string{"task.create"}, Resources: []Resource{{Kind: "task"}}, DataClass: "task", Recipients: []Binding{{fid("a1"), fid("d1")}}, Lifetime: "until_revoked"}
}
func scopedTaskFixture() RuntimeTaskChanged {
	return RuntimeTaskChanged{TaskID: fid("21"), Action: "create", Version: 1, CreatorActor: "owner", Creator: scopedOriginFixture(), Description: ObjectRef{goldenB1, 0}, AcceptanceSHA256: goldenSubmission, Dependencies: []uuid.UUID{}, Audience: []uuid.UUID{fid("a1")}, State: "waiting"}
}
func scopedIssueArguments(g RuntimeGrantIssued) GrantIssueArguments {
	return GrantIssueArguments{g.GrantID, g.Grantee, g.ParentGrantID, g.Operations, g.Resources, g.DataClass, g.Recipients, g.Limits, g.DelegationDepth, g.Lifetime, g.TaskBinding}
}
func scopedReceipt(s ScopedSubmission, version int64, outcome string, work uuid.UUID) RuntimeScopedReceipt {
	return RuntimeScopedReceipt{fid("31"), Digest(sha256.Sum256(mustEncode(s))), s, outcome, version, work}
}

type scopedFixture struct {
	name, typ string
	p         payload
	wire      string
}

func f3_11Payloads() []scopedFixture {
	id := func(s string) string { return `"` + fid(s).String() + `"` }
	binding := `{"agent_id":` + id("a1") + `,"context_id":` + id("d1") + `}`
	origin := `{"agent_id":` + id("a1") + `,"session_id":` + id("b1") + `,"context_id":` + id("d1") + `}`
	capacity := `{"turn_slots":0,"local_slots":0,"buffer_bytes":0,"local_units":0,"max_operation_bytes":0}`
	scopes := `[{"kind":"colony"},{"kind":"agent","id":` + id("a1") + `},{"kind":"context","id":` + id("d1") + `},{"kind":"grant","id":` + id("11") + `}]`
	context := `{"kind":"context","binding":` + binding + `,"scope":"context","slots":["memory"]}`
	s := ScopedSubmission{Schema: 2, Actor: "owner", Origin: scopedOriginFixture(), Operation: "grant.revoke", Target: Resource{Kind: "task"}, Expected: ScopedExpected{Version: 1, AuthorityVersion: new(int64(7))}, Arguments: GrantRevokeArguments{fid("11")}}
	swire := `{"schema":2,"actor":"owner","origin":` + origin + `,"operation":"grant.revoke","target":{"kind":"task"},"expected":{"version":1,"authority_version":7},"arguments":{"grant_id":` + id("11") + `}}`
	r := scopedReceipt(s, 8, "applied", uuid.UUID{})
	return []scopedFixture{
		{"issued", TypeRuntimeGrantIssued, scopedGrantFixture(), `{"grant_id":` + id("11") + `,"expected_authority_version":0,"authority_version":1,"grantee":` + binding + `,"operations":["task.create"],"resources":[{"kind":"task"}],"data_class":"task","recipients":[` + binding + `],"limits":` + capacity + `,"delegation_depth":0,"lifetime":"until_revoked"}`},
		{"revoked", TypeRuntimeGrantRevoked, RuntimeGrantRevoked{fid("11"), 1, 2}, `{"grant_id":` + id("11") + `,"expected_authority_version":1,"authority_version":2}`},
		{"task", TypeRuntimeTaskChanged, scopedTaskFixture(), `{"task_id":` + id("21") + `,"action":"create","expected_version":0,"version":1,"creator_actor":"owner","creator":` + origin + `,"description":{"sha256":"` + goldenB1.String() + `","bytes":0},"acceptance_sha256":"` + goldenSubmission.String() + `","dependencies":[],"audience":[` + id("a1") + `],"state":"waiting"}`},
		{"capacity", TypeRuntimeCapacityConfigured, RuntimeCapacityConfigured{ScopeKey{Kind: "colony"}, 0, 1, Capacity{}}, `{"scope":{"kind":"colony"},"expected_version":0,"version":1,"limits":` + capacity + `}`},
		{"admitted", TypeRuntimeWorkAdmitted, RuntimeWorkAdmitted{WorkID: fid("41"), Kind: "turn", Version: 1, AuthorityVersion: 1, GrantID: fid("11"), Scopes: scopedScopes(), Operation: "turn.start", Target: scopedContext()}, `{"work_id":` + id("41") + `,"kind":"turn","expected_version":0,"version":1,"authority_version":1,"grant_id":` + id("11") + `,"scopes":` + scopes + `,"operation":"turn.start","target":` + context + `}`},
		{"dispatched", TypeRuntimeWorkDispatched, RuntimeWorkDispatched{fid("42"), 1, 2, 1, fid("11"), goldenSubmission}, `{"work_id":` + id("42") + `,"expected_version":1,"version":2,"authority_version":1,"grant_id":` + id("11") + `,"arguments_sha256":"` + goldenSubmission.String() + `"}`},
		{"ended", TypeRuntimeWorkEnded, RuntimeWorkEnded{fid("42"), 2, 3, "unknown"}, `{"work_id":` + id("42") + `,"expected_version":2,"version":3,"outcome":"unknown"}`},
		{"used", TypeRuntimeLocalUsed, RuntimeLocalUsed{AuthorityVersion: 1, GrantID: fid("11"), Scopes: scopedScopes(), Units: 1}, `{"authority_version":1,"grant_id":` + id("11") + `,"scopes":` + scopes + `,"units":1,"bytes":0}`},
		{"revision", TypePrivateScopedRevision, PrivateScopedRevision{"context", 1, 2, []PrivateChange{{Slot: "memory", Mode: "replace", SHA256: goldenB1}}}, `{"scope":"context","expected_version":1,"revision":2,"changes":[{"slot":"memory","mode":"replace","sha256":"` + goldenB1.String() + `","bytes":0}]}`},
		{"receipt", TypeRuntimeScopedReceipt, r, `{"command_id":` + id("31") + `,"submission_sha256":"` + scopedHash(swire).String() + `","submission":` + swire + `,"outcome":"applied","version":8}`},
	}
}

// Every fixture pins public bytes, required keys and recursive strict JSON.
func TestF3_11Payloads(t *testing.T) {
	t.Parallel()
	for _, tc := range f3_11Payloads() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, err := tc.p.Encode()
			if err != nil || string(js) != tc.wire {
				t.Fatalf("encode %s: %s, %v; want %s", tc.typ, js, err, tc.wire)
			}
			for _, data := range []string{tc.wire, `{"future":{"a":[null,1]},` + tc.wire[1:]} {
				got, err := decodeF3_11(tc.typ, []byte(data))
				if err != nil || !reflect.DeepEqual(got, tc.p) {
					t.Fatalf("decode: %#v, %v; want %#v", got, err, tc.p)
				}
				if again, err := got.Encode(); err != nil || !bytes.Equal(again, js) {
					t.Fatalf("re-encode %s: %s, %v", tc.typ, again, err)
				}
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(js, &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				bad := strings.Replace(tc.wire, `"`+key+`":`+string(value), `"`+key+`":null`, 1)
				assertScopedInvalid(t, tc.typ, bad)
				copyFields := make(map[string]json.RawMessage, len(fields))
				for k, v := range fields {
					if k != key {
						copyFields[k] = v
					}
				}
				missing, _ := json.Marshal(copyFields)
				assertScopedInvalid(t, tc.typ, string(missing))
				assertScopedInvalid(t, tc.typ, `{"`+key+`":`+string(value)+`,`+tc.wire[1:])
			}
			for _, bad := range []string{`{"future":{"a":[{"x":1,"x":2}]},` + tc.wire[1:], tc.wire + ` {}`, `null`, `[]`, `{}`, tc.wire[:len(tc.wire)-1], string([]byte{0xff})} {
				assertScopedInvalid(t, tc.typ, bad)
			}
			for _, n := range []int{65536, 65537} {
				_, err := decodeF3_11(tc.typ, []byte(tc.wire+strings.Repeat(" ", n-len(tc.wire))))
				if n == 65536 && err != nil || n > 65536 && !errors.Is(err, ErrInvalid) {
					t.Fatalf("%d-byte input: %v", n, err)
				}
			}
		})
	}
	t.Run("producer_maxima", func(t *testing.T) {
		t.Parallel()
		g := scopedGrantFixture()
		g.DataClass = "workspace"
		g.Operations = []string{"file.edit", "file.read", "file.search", "file.write", "grant.issue", "grant.revoke"}
		g.DelegationDepth = 8
		g.Limits = Capacity{3, 4, 256 << 20, 100, 4 << 20}
		g.Resources = nil
		g.Recipients = nil
		for i := range 32 {
			g.Resources = append(g.Resources, Resource{Kind: "workspace", WorkspaceID: fid("51"), Binding: g.Grantee, PathPrefix: fmt.Sprintf("%02d", i) + strings.Repeat("x", 1022)})
		}
		for i := range 64 {
			g.Recipients = append(g.Recipients, Binding{fid("a1"), uuid.MustParse(fmt.Sprintf("0199c000-0000-7000-8000-%012x", i+1))})
		}
		s := ScopedSubmission{Schema: 2, Actor: "owner", Origin: scopedOriginFixture(), Operation: "grant.issue", Target: g.Resources[0], Expected: ScopedExpected{AuthorityVersion: new(int64(0))}, Arguments: scopedIssueArguments(g)}
		for _, p := range []payload{g, scopedReceipt(s, 1, "applied", uuid.UUID{})} {
			js, err := p.Encode()
			if err != nil || len(js) > MaxScopedPayloadBytes {
				t.Fatalf("maxima %T: %d bytes, %v", p, len(js), err)
			}
			t.Logf("%T: %d bytes", p, len(js))
		}
	})
}
func assertScopedInvalid(t *testing.T, typ, js string) {
	t.Helper()
	if _, err := decodeF3_11(typ, []byte(js)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%s accepted %s: %v", typ, js, err)
	}
}

func TestF3_11PayloadsValues(t *testing.T) {
	t.Parallel()
	fixtures := f3_11Payloads()
	for _, tc := range []struct {
		name     string
		fixture  int
		from, to string
	}{
		{"resource_kind", 0, `"kind":"task"`, `"kind":"future"`},
		{"data_class", 0, `"data_class":"task"`, `"data_class":"future"`},
		{"class_operation", 0, `"task.create"`, `"file.read"`},
		{"task_region_requires_id", 0, `"task.create"`, `"task.start"`},
		{"operation_order", 0, `["task.create"]`, `["task.create","grant.issue"]`},
		{"zero_recipient", 0, `"recipients":[{"agent_id":"` + fid("a1").String() + `","context_id":"` + fid("d1").String() + `"}]`, `"recipients":[]`},
		{"resource_forbidden_null", 0, `{"kind":"task"}`, `{"kind":"task","binding":null}`},
		{"colony_forbidden_id", 3, `{"kind":"colony"}`, `{"kind":"colony","id":null}`},
		{"scope_kind", 3, `"kind":"colony"`, `"kind":"future"`},
		{"scope_grant_configuration", 3, `{"kind":"colony"}`, `{"kind":"grant","id":"` + fid("11").String() + `"}`},
		{"null_dependencies", 2, `"dependencies":[]`, `"dependencies":null`},
		{"nonarray_dependencies", 2, `"dependencies":[]`, `"dependencies":{}`},
		{"self_dependency", 2, `"dependencies":[]`, `"dependencies":["` + fid("21").String() + `"]`},
		{"audience_binding", 2, `"audience":["` + fid("a1").String() + `"]`, `"audience":[{"agent_id":"` + fid("a1").String() + `","context_id":"` + fid("d1").String() + `"}]`},
		{"unknown_action", 2, `"action":"create"`, `"action":"retry"`},
		{"unknown_state", 2, `"state":"waiting"`, `"state":"ready_queued"`},
		{"creator_actor", 2, `"creator_actor":"owner"`, `"creator_actor":"core"`},
		{"task_state_transition", 2, `"state":"waiting"`, `"state":"active"`},
		{"work_kind", 4, `"kind":"turn"`, `"kind":"future"`},
		{"slot", 4, `["memory"]`, `["unknown"]`},
		{"slot_order", 4, `["memory"]`, `["memory","handoff"]`},
		{"empty_context_slots_null", 4, `["memory"]`, `null`},
		{"turn_local_field", 4, `"operation":"turn.start"`, `"parent_turn_id":"` + fid("41").String() + `","operation":"turn.start"`},
		{"units", 7, `"units":1`, `"units":2`},
		{"initial_scoped_revision", 8, `"expected_version":1,"revision":2`, `"expected_version":0,"revision":1`},
		{"clear_forbidden_sha", 8, `"mode":"replace"`, `"mode":"clear"`},
		{"private_predecessor", 8, `"bytes":0`, `"bytes":0,"previous_sha256":"` + goldenB1.String() + `"`},
		{"receipt_digest", 9, `"submission_sha256":"` + scopedHash(strings.Split(strings.Split(fixtures[9].wire, `"submission":`)[1], `,"outcome":`)[0]).String() + `"`, `"submission_sha256":"` + goldenB1.String() + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := fixtures[tc.fixture]
			bad := strings.Replace(src.wire, tc.from, tc.to, 1)
			if bad == src.wire {
				t.Fatal("mutation did not apply")
			}
			assertScopedInvalid(t, src.typ, bad)
		})
	}
	for _, value := range []string{"-1", "-0", "1.0", "1e0", "9223372036854775808", "null", "\"1\""} {
		t.Run("integer_"+value, func(t *testing.T) {
			t.Parallel()
			keys := []string{"expected_authority_version", "authority_version", "version", "version", "version", "version", "version", "units", "revision", "version"}
			for i, tc := range fixtures {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(tc.wire), &fields); err != nil {
					t.Fatal(err)
				}
				fields[keys[i]] = json.RawMessage(value)
				bad, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				assertScopedInvalid(t, tc.typ, string(bad))
			}
		})
	}
}

func TestF3_11OpenTokensAndBounds(t *testing.T) {
	t.Parallel()
	g := scopedGrantFixture()
	g.Lifetime = "future_lifetime"
	g.DelegationDepth = math.MaxInt64
	g.Limits = Capacity{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
	task := scopedTaskFixture()
	task.Action = "fail"
	task.ExpectedVersion = 1
	task.Version = 2
	task.State = "failed"
	task.Assignment = &Assignment{fid("22"), 1, g.Grantee, "owner", scopedOriginFixture()}
	task.FailureCause = "future_cause"
	task.FailureEvidence = &SubjectRef{Kind: "future_subject", Members: json.RawMessage(`{"z":null,"x":{"amount":9223372036854775807},"artifact":null}`)}
	for _, tc := range []struct {
		typ string
		p   payload
	}{{TypeRuntimeGrantIssued, g}, {TypeRuntimeTaskChanged, task}, {TypeRuntimeWorkEnded, RuntimeWorkEnded{fid("41"), math.MaxInt64 - 1, math.MaxInt64, "future_outcome"}}, {TypePrivateScopedRevision, PrivateScopedRevision{"context", math.MaxInt64 - 1, math.MaxInt64, []PrivateChange{{Slot: "memory", Mode: "replace", SHA256: goldenB1, Bytes: math.MaxInt64}}}}} {
		js, err := tc.p.Encode()
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeF3_11(tc.typ, js)
		if err != nil || !reflect.DeepEqual(got, tc.p) {
			t.Fatalf("open/boundary %s: %#v %v", tc.typ, got, err)
		}
	}
	for _, kind := range []string{"Bad", "two.words", "a-b", "", strings.Repeat("x", 65)} {
		bad := task
		s := *task.FailureEvidence
		s.Kind = kind
		bad.FailureEvidence = &s
		if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted subject kind %q", kind)
		}
	}
	for _, token := range []string{"Bad", "two.words", "a-b", "", strings.Repeat("x", 65)} {
		bad := g
		bad.Lifetime = token
		if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted lifetime %q", token)
		}
	}
	g.Resources = []Resource{{Kind: "artifact", Artifact: ArtifactRef{1, goldenB1, math.MaxInt64}}}
	g.DataClass = "artifact"
	g.Operations = []string{"artifact.read"}
	js := mustEncode(g)
	assertScopedInvalid(t, TypeRuntimeGrantIssued, strings.Replace(string(js), goldenB1.String(), strings.Repeat("0", 64), 1))
	g.Resources[0].Artifact.SHA256 = Digest{}
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero artifact digest encoded")
	}
	g = scopedGrantFixture()
	g.Resources = []Resource{{Kind: "workspace", WorkspaceID: fid("51"), Binding: g.Grantee, PathPrefix: strings.Repeat("x", 1024)}}
	g.DataClass = "workspace"
	g.Operations = []string{"file.read"}
	js = mustEncode(g)
	assertScopedInvalid(t, TypeRuntimeGrantIssued, strings.Replace(string(js), strings.Repeat("x", 1024), strings.Repeat("x", 1025), 1))
	// Output cap, independent of the input cap and runtime list ceilings.
	g.Resources = nil
	for i := range 80 {
		g.Resources = append(g.Resources, Resource{Kind: "workspace", WorkspaceID: fid("51"), Binding: g.Grantee, PathPrefix: fmt.Sprintf("%02d", i) + strings.Repeat("x", 1022)})
	}
	if _, err := g.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize output encoded")
	}
}

func TestF3_11PrivateSelections(t *testing.T) {
	t.Parallel()
	base := RuntimeLocalUsed{AuthorityVersion: 1, GrantID: fid("11"), Scopes: scopedScopes(), Units: 1, Read: &PrivateReadRef{"context", "transcript", 3, 0, 10}, Selected: []SelectedObject{}}
	for _, tc := range []struct {
		name  string
		edit  func(*RuntimeLocalUsed)
		valid bool
	}{
		{"empty_end_page", func(p *RuntimeLocalUsed) {}, true},
		{"whole_chunks", func(p *RuntimeLocalUsed) {
			p.Bytes = 3
			p.Selected = []SelectedObject{{goldenB1, 1, 2, 10}, {goldenSubmission, 2, 3, 20}}
			p.NextCursor = new(int64(20))
		}, true},
		{"absent_continuation", func(p *RuntimeLocalUsed) { p.Read = &PrivateReadRef{"context", "continuation", 3, 0, 10} }, true},
		{"missing_selected", func(p *RuntimeLocalUsed) { p.Selected = nil }, false},
		{"empty_advancing_page", func(p *RuntimeLocalUsed) { p.NextCursor = new(int64(1)) }, false},
		{"sum_mismatch", func(p *RuntimeLocalUsed) { p.Selected = []SelectedObject{{goldenB1, 1, 2, 10}} }, false},
		{"overflow", func(p *RuntimeLocalUsed) {
			p.Bytes = math.MaxInt64
			p.Read = &PrivateReadRef{"context", "transcript", 3, 0, math.MaxInt64}
			p.Selected = []SelectedObject{{goldenB1, math.MaxInt64, 2, 10}, {goldenSubmission, 1, 3, 20}}
		}, false},
		{"empty_memory", func(p *RuntimeLocalUsed) { p.Read = &PrivateReadRef{"context", "memory", 3, 0, 10} }, false},
		{"artifact_conflict", func(p *RuntimeLocalUsed) { p.Artifact = &ArtifactRef{1, goldenB1, 0} }, false},
		{"unrequested_head", func(p *RuntimeLocalUsed) { p.Selected = []SelectedObject{{goldenB1, 0, 4, 10}} }, false},
		{"cursor_mismatch", func(p *RuntimeLocalUsed) {
			p.Bytes = 1
			p.Selected = []SelectedObject{{goldenB1, 1, 2, 10}}
			p.NextCursor = new(int64(11))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := base
			tc.edit(&p)
			js, err := p.Encode()
			if !tc.valid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatal("invalid selection encoded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeRuntimeLocalUsed(js)
			if err != nil || !reflect.DeepEqual(p, got) {
				t.Fatalf("selection lost: %#v %v", got, err)
			}
		})
	}
}

func TestF3_11CoreWorkGrant(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"turn.start", "local.admit", "local.begin", "local.end", "turn.end", "turn.cancel"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			s := ScopedSubmission{Schema: 2, Actor: "core", Origin: scopedOriginFixture(), Operation: op, Target: scopedContext(), GrantID: fid("11")}
			switch op {
			case "turn.start":
				s.Arguments = TurnStartArguments{TurnID: fid("41")}
			case "local.admit":
				s.Arguments = LocalAdmissionArguments{WorkID: fid("42"), ParentTurnID: fid("41"), LocalOperation: "file.read", ArgumentsSHA256: goldenB1, ToolSchema: 1, MaxBytes: 1}
			case "local.begin":
				s.Expected.Version = 1
				s.Arguments = WorkDispatchArguments{fid("42"), goldenB1}
			default:
				s.Expected.Version = 2
				s.Arguments = WorkEndArguments{fid("42"), "unknown"}
			}
			js := mustEncode(s)
			s.GrantID = uuid.UUID{}
			if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("core work encoded without grant")
			}
			bad := strings.Replace(string(js), `,"grant_id":"`+fid("11").String()+`"`, "", 1)
			if _, err := DecodeScopedSubmission([]byte(bad)); !errors.Is(err, ErrInvalid) {
				t.Fatal("core work decoded without grant")
			}
		})
	}
}

func TestF3_11ConditionalSnapshots(t *testing.T) {
	t.Parallel()
	task := scopedTaskFixture()
	task.Action, task.State, task.ExpectedVersion, task.Version = "wait", "waiting", 2, 3
	task.Assignment = &Assignment{fid("22"), 1, Binding{fid("a1"), fid("d1")}, "agent", scopedOriginFixture()}
	task.WaitReason = "input"
	task.WaitArtifact = &SubjectRef{Kind: "artifact", Artifact: ArtifactRef{1, goldenB1, 1}}
	grant := scopedGrantFixture()
	grant.ParentGrantID = fid("12")
	grant.TaskBinding = &TaskBinding{fid("21"), fid("22"), 1}
	local := RuntimeWorkAdmitted{WorkID: fid("42"), Kind: "local", Version: 1, AuthorityVersion: 1, GrantID: fid("11"), Scopes: scopedScopes(), Operation: "file.read", Target: Resource{Kind: "workspace", WorkspaceID: fid("51"), Binding: grant.Grantee, PathPrefix: "a"}, TaskBinding: grant.TaskBinding, ParentTurnID: fid("41"), ArgumentsSHA256: goldenB1, ToolSchema: new(int64(1)), MaxBytes: new(int64(1))}
	for _, tc := range []struct {
		name, typ string
		p         payload
		from, to  string
	}{
		{"wait_reason", TypeRuntimeTaskChanged, task, `"wait_reason":"input"`, `"wait_reason":"future_reason"`},
		{"null_wait_subject", TypeRuntimeTaskChanged, task, `"wait_artifact":` + string(mustJSON(task.WaitArtifact)), `"wait_artifact":null`},
		{"wait_without_reason", TypeRuntimeTaskChanged, task, `"wait_reason":"input",`, ``},
		{"assignment_generation_zero", TypeRuntimeTaskChanged, task, `"generation":1`, `"generation":0`},
		{"assignment_origin_null", TypeRuntimeTaskChanged, task, `"assigner":` + string(mustJSON(task.Creator)), `"assigner":null`},
		{"assignment_core", TypeRuntimeTaskChanged, task, `"assigner_actor":"agent"`, `"assigner_actor":"core"`},
		{"wrong_task_state", TypeRuntimeTaskChanged, task, `"state":"waiting"`, `"state":"active"`},
		{"parent_null", TypeRuntimeGrantIssued, grant, `"parent_grant_id":"` + fid("12").String() + `"`, `"parent_grant_id":null`},
		{"task_binding_partial", TypeRuntimeGrantIssued, grant, `"generation":1`, `"generation":null`},
		{"local_parent_missing", TypeRuntimeWorkAdmitted, local, `"parent_turn_id":"` + fid("41").String() + `",`, ``},
		{"local_schema", TypeRuntimeWorkAdmitted, local, `"tool_schema":1`, `"tool_schema":2`},
		{"local_max_zero", TypeRuntimeWorkAdmitted, local, `"max_bytes":1`, `"max_bytes":0`},
		{"local_max_null", TypeRuntimeWorkAdmitted, local, `"max_bytes":1`, `"max_bytes":null`},
		{"local_arguments_zero", TypeRuntimeWorkAdmitted, local, goldenB1.String(), strings.Repeat("0", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(tc.p)
			got, err := decodeF3_11(tc.typ, wire)
			if err != nil || !reflect.DeepEqual(got, tc.p) {
				t.Fatalf("valid conditional payload %#v %v", got, err)
			}
			bad := strings.Replace(string(wire), tc.from, tc.to, 1)
			if bad == string(wire) {
				t.Fatal("mutation did not apply")
			}
			assertScopedInvalid(t, tc.typ, bad)
		})
	}
}

func mustJSON(value any) []byte {
	js, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return js
}

func TestF3_11NestedDuplicate(t *testing.T) {
	t.Parallel()
	wire := string(mustEncode(scopedGrantFixture()))
	duplicate := strings.Replace(wire, `"grantee":{`, `"grantee":{"agent_id":"`+fid("a1").String()+`",`, 1)
	assertScopedInvalid(t, TypeRuntimeGrantIssued, duplicate)
}

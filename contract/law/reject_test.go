package law

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type rejection struct {
	name                    string
	data                    []byte
	problem                 Problem
	path                    string
	violation               Violation
	field, value, supported string
}

// Fixture editing uses exact unique anchors, never the loader or its tree.
// Each replacement starts from an independent copy of the original fixture.
func replaceUnique(t testing.TB, data []byte, old, replacement string) []byte {
	t.Helper()
	if n := bytes.Count(data, []byte(old)); n != 1 {
		t.Fatalf("mutation anchor occurs %d times: %q", n, old)
	}
	return bytes.Replace(data, []byte(old), []byte(replacement), 1)
}

func rawAt(t testing.TB, data []byte, path string) []byte {
	t.Helper()
	raw := json.RawMessage(data)
	if path == "" {
		return bytes.TrimSpace(raw)
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if raw[0] == '[' {
			var entries []json.RawMessage
			if err := json.Unmarshal(raw, &entries); err != nil {
				t.Fatal(err)
			}
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(entries) {
				t.Fatalf("bad fixture index %q", part)
			}
			raw = entries[i]
		} else {
			var members map[string]json.RawMessage
			if err := json.Unmarshal(raw, &members); err != nil {
				t.Fatal(err)
			}
			var ok bool
			raw, ok = members[part]
			if !ok {
				t.Fatalf("missing fixture member %q in %q", part, path)
			}
		}
	}
	return raw
}

func editMember(t testing.TB, data []byte, path, key, replacement string) []byte {
	t.Helper()
	parent := rawAt(t, data, path)
	raw := rawAt(t, parent, "/"+key)
	// Include the object's member indentation so a kind's unit cannot match
	// the same spelling in one of its nested sources.
	indent := string(parent[1:bytes.IndexByte(parent, '"')])
	anchor := indent + strconv.Quote(key) + ": " + string(raw)
	if replacement != "" {
		changed := replaceUnique(t, parent, anchor, indent+strconv.Quote(key)+": "+replacement)
		return replaceUnique(t, data, string(parent), string(changed))
	}
	if bytes.Count(parent, []byte(anchor)) != 1 {
		t.Fatalf("nonunique member %q", key)
	}
	start := bytes.Index(parent, []byte(anchor))
	end := start + len(anchor)
	if end < len(parent) && parent[end] == ',' {
		end++
	} else {
		start = bytes.LastIndexByte(parent[:start], ',')
	}
	if start < 0 {
		t.Fatal("fixture removal must leave another member")
	}
	changed := append(bytes.Clone(parent[:start]), parent[end:]...)
	return replaceUnique(t, data, string(parent), string(changed))
}

func addMember(t testing.TB, data []byte, path, key, value string) []byte {
	t.Helper()
	parent := rawAt(t, data, path)
	return replaceUnique(t, data, string(parent), "{"+strconv.Quote(key)+":"+value+","+string(parent[1:]))
}

func editArray(t testing.TB, data []byte, path string, change func([]json.RawMessage) []json.RawMessage) []byte {
	t.Helper()
	raw := rawAt(t, data, path)
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	changed, err := json.Marshal(change(entries))
	if err != nil {
		t.Fatal(err)
	}
	return replaceUnique(t, data, string(raw), string(changed))
}

func lawRejections(t testing.TB) []rejection {
	t.Helper()
	full := fixture(t, "law/v1/full.json")
	rows := []rejection{}
	malformed := func(name string, data []byte, problem Problem) {
		rows = append(rows, rejection{name: name, data: data, problem: problem})
	}
	field := func(name string, data []byte, path string, violation Violation) {
		rows = append(rows, rejection{name: name, data: data, path: path, violation: violation})
	}
	unsupported := func(name string, data []byte, field, value, supported string) {
		rows = append(rows, rejection{name: name, data: data, field: field, value: value, supported: supported})
	}
	edit := func(path, key, value string) []byte { return editMember(t, full, path, key, value) }
	add := func(path, key, value string) []byte { return addMember(t, full, path, key, value) }
	quote := strconv.Quote
	malformed("M1-bom", append([]byte{0xef, 0xbb, 0xbf}, full...), "bom")
	malformed("M2-invalid-utf8", replaceUnique(t, full, "golden.full", "golden.\xff"), "invalid_utf8")
	malformed("M3-encoded-surrogate", replaceUnique(t, full, "golden.full", "golden.\xed\xa0\x80"), "invalid_utf8")
	malformed("M4-missing-comma", replaceUnique(t, full, `"dwarf-harness/undercurrent-law",`, `"dwarf-harness/undercurrent-law"`), "syntax")
	malformed("M5-empty", nil, "syntax")
	malformed("M5-whitespace", []byte("\n"), "syntax")
	malformed("M6-root-duplicate", add("", "law_id", `"x"`), "duplicate_key")
	malformed("M7-kind-duplicate", add("/kinds/0", "unit", `"affordance_units"`), "duplicate_key")
	malformed("M8-source-duplicate", add("/kinds/2/sources/1", "mode", `"level"`), "duplicate_key")
	// The coupling object repeats, so include its unique source parent as anchor.
	source0 := rawAt(t, full, "/kinds/0/sources/0")
	dupCoupling := replaceUnique(t, source0, `"profile": "dependency_downstream/v1",`, `"profile": "dependency_downstream/v1", "profile": "local/v1",`)
	malformed("M9-coupling-duplicate", replaceUnique(t, full, string(source0), string(dupCoupling)), "duplicate_key")
	malformed("M10-escaped-duplicate", replaceUnique(t, full, `"law_id": "golden.full",`, `"law_id": "golden.full", "law\u005fid": "x",`), "duplicate_key")
	malformed("M11-unknown-nested-duplicate", add("", "x", `{"a":1,"a":2}`), "duplicate_key")
	malformed("M12-trailing-object", append(bytes.Clone(full), []byte("{}")...), "trailing_data")
	malformed("M13-trailing-letter", append(bytes.Clone(full), 'x'), "trailing_data")
	malformed("M13-trailing-nul", append(bytes.Clone(full), 0), "trailing_data")
	malformed("M14-fraction", edit("", "schema_version", "1.0"), "not_unsigned_integer")
	malformed("M15-exponent", edit("/kinds/0", "cutoff_units", "1e3"), "not_unsigned_integer")
	malformed("M16-negative-zero", edit("/kinds/0", "hop_latency_ms", "-0"), "not_unsigned_integer")
	malformed("M16-negative", edit("/kinds/0", "hop_latency_ms", "-1"), "not_unsigned_integer")
	malformed("M17-unknown-fraction", add("", "x", "0.5"), "not_unsigned_integer")
	malformed("M18-leading-zero", edit("/kinds/0", "hop_latency_ms", "00"), "syntax")
	malformed("raw-control", edit("", "law_id", "\"x\x01\""), "syntax")
	unsupported("N1-format", edit("", "format", quote(PolicyFormat)), "format", PolicyFormat, LawFormat)
	for _, v := range []string{"0", "2", "99999999999999999999"} {
		unsupported("N2-schema-"+v, edit("", "schema_version", v), "schema_version", v, "1")
	}
	for _, v := range []string{"0", "2"} {
		unsupported("N3-evaluator-"+v, edit("", "evaluator_version", v), "evaluator_version", v, "1")
	}
	unsupported("N4-kernel", edit("", "kernel", `"bounded_best_arrival/v2"`), "kernel", "bounded_best_arrival/v2", "bounded_best_arrival/v1")
	unsupported("N5-policy", fixture(t, "validation-policy/v1/policy.json"), "format", PolicyFormat, LawFormat)
	field("I1-root-array", append(append([]byte{'['}, full...), ']'), "", "wrong_type")
	for _, tc := range []struct {
		name, parent, key, value string
		violation                Violation
	}{
		{"I2-missing-format", "", "format", "", "missing_field"},
		{"I2-format-type", "", "format", "1", "wrong_type"},
		{"I3-schema-type", "", "schema_version", `"1"`, "wrong_type"},
		{"I4-missing-evaluator", "", "evaluator_version", "", "missing_field"},
		{"I4-missing-kernel", "", "kernel", "", "missing_field"},
		{"I7-missing-id", "", "law_id", "", "missing_field"},
		{"I7-missing-kinds", "", "kinds", "", "missing_field"},
		{"I8-null-id", "", "law_id", "null", "wrong_type"},
		{"I8-version-type", "", "law_version", `"1"`, "wrong_type"},
		{"I12-kinds-type", "", "kinds", "{}", "wrong_type"},
		{"I16-kind-enum", "/kinds/0", "id", `"colony_regime"`, "unknown_value"},
		{"I17-class-enum", "/kinds/5", "quantity_class", `"derived"`, "unknown_value"},
		{"I18-unit-mismatch", "/kinds/0", "unit", `"strain_units"`, "mismatch"},
		{"I18-unit-enum", "/kinds/0", "unit", `"units"`, "unknown_value"},
		{"I20-missing-saturation", "/kinds/0", "saturation_units", "", "missing_field"},
		{"I21-no-sources", "/kinds/0", "sources", "[]", "count"},
		{"I24-hop-type", "/kinds/0", "hop_latency_ms", `"0"`, "wrong_type"},
		{"I28-f1", "/kinds/1/sources/0", "rule_id", `"validation_f1_unconfirmed/v1"`, "unknown_value"},
		{"I29-router-scheme", "/kinds/4/sources/3", "rule_id", `"router-headroom/v1"`, "unknown_value"},
		{"I30-rule-kind", "/kinds/5/sources/0", "rule_id", `"router_headroom/v1"`, "mismatch"},
		{"I31-mode-mismatch", "/kinds/2/sources/1", "mode", `"impulse"`, "mismatch"},
		{"I32-mode-enum", "/kinds/2/sources/1", "mode", `"decaying"`, "unknown_value"},
		{"I33-source-unit", "/kinds/0/sources/0", "unit", `"pressure_units"`, "mismatch"},
		{"I36-rising-age", "/kinds/2/sources/0", "age_rule_id", `"none"`, "mismatch"},
		{"I37-level-age", "/kinds/4/sources/0", "age_rule_id", `"linear_per_day/v1"`, "mismatch"},
		{"I38-age-enum", "/kinds/2/sources/0", "age_rule_id", `"exponential/v1"`, "unknown_value"},
		{"I42-missing-coupling", "/kinds/0/sources/0", "coupling", "", "missing_field"},
	} {
		field(tc.name, edit(tc.parent, tc.key, tc.value), tc.parent+"/"+tc.key, tc.violation)
	}
	for _, tc := range []struct {
		name, parent, key string
		values            []string
	}{
		{"I9-id-pattern", "", "law_id", []string{`"Golden"`, `".golden"`, `""`, quote(strings.Repeat("a", 65)), `"\ud800"`}},
		{"I10-version", "", "law_version", []string{"0", "2147483648", "99999999999999999999"}},
		{"I11-scale", "", "weight_scale_ppm", []string{"999999", "1000001"}},
		{"I24-hop", "/kinds/0", "hop_latency_ms", []string{"86400001"}},
		{"I25-attenuation", "/kinds/0", "attenuation_ppm", []string{"0", "1000000"}},
		{"I26-cutoff", "/kinds/0", "cutoff_units", []string{"0", "1000000000001", "18446744073709551616"}},
		{"I27-saturation", "/kinds/0", "saturation_units", []string{"1000000000001", "1000", "999"}},
		{"I34-impulse-half", "/kinds/5/sources/0", "half_life_seconds", []string{"0", "31536001"}},
		{"I35-level-half", "/kinds/4/sources/0", "half_life_seconds", []string{"1"}},
		{"I39-rise", "/kinds/2/sources/0", "rise_units_per_day", []string{"0", "1000000000001"}},
		{"I40-level-rise", "/kinds/4/sources/0", "rise_units_per_day", []string{"1"}},
	} {
		violation := Violation("out_of_range")
		if tc.key == "law_id" {
			violation = "pattern"
		}
		for i, value := range tc.values {
			field(fmt.Sprintf("%s-%d", tc.name, i), edit(tc.parent, tc.key, value), tc.parent+"/"+tc.key, violation)
		}
	}
	for _, tc := range []struct{ name, path, key, value string }{
		{"I5-notes", "", "notes", `"x"`}, {"I6-case", "", "LAW_ID", `"x"`},
		{"I19-kind-extra", "/kinds/0", "decay", "1"}, {"I41-amplitude", "/kinds/0/sources/0", "amplitude", "1"},
		{"pointer-escaping", "", "a~/b", "1"},
	} {
		path := tc.path + "/" + strings.ReplaceAll(strings.ReplaceAll(tc.key, "~", "~0"), "/", "~1")
		field(tc.name, add(tc.path, tc.key, tc.value), path, "unknown_field")
	}
	field("I13-five-kinds", editArray(t, full, "/kinds", func(a []json.RawMessage) []json.RawMessage { return a[:5] }), "/kinds", "count")
	field("I13-seven-kinds", editArray(t, full, "/kinds", func(a []json.RawMessage) []json.RawMessage { return append(a, a[5]) }), "/kinds", "count")
	field("I14-kind-order", editArray(t, full, "/kinds", func(a []json.RawMessage) []json.RawMessage { a[4], a[5] = a[5], a[4]; return a }), "/kinds/5/id", "unsorted")
	field("I15-kind-duplicate", editArray(t, full, "/kinds", func(a []json.RawMessage) []json.RawMessage { a[5] = a[4]; return a }), "/kinds/5/id", "duplicate")
	field("I21-nine-sources", editArray(t, full, "/kinds/2/sources", func(a []json.RawMessage) []json.RawMessage { return append(a, a[:3]...) }), "/kinds/2/sources", "count")
	field("I22-source-order", editArray(t, full, "/kinds/2/sources", func(a []json.RawMessage) []json.RawMessage { a[1], a[2] = a[2], a[1]; return a }), "/kinds/2/sources/2/rule_id", "unsorted")
	field("I23-source-duplicate", editArray(t, full, "/kinds/0/sources", func(a []json.RawMessage) []json.RawMessage { a[1] = a[0]; return a }), "/kinds/0/sources/1/rule_id", "duplicate")
	// Couplings repeat verbatim; edit inside their uniquely anchored source.
	for _, tc := range []struct {
		name, source, key, value, suffix string
		violation                        Violation
	}{
		{"I43-profile-mismatch", "/kinds/4/sources/0", "profile", `"local/v1"`, "", "mismatch"},
		{"I44-profile-enum", "/kinds/0/sources/0", "profile", `"diffusion/v1"`, "", "unknown_value"},
		{"I45-direction-mismatch", "/kinds/1/sources/0", "direction", `"dependency_to_consumer"`, "", "mismatch"},
		{"I46-direction-enum", "/kinds/0/sources/0", "direction", `"upstream"`, "", "unknown_value"},
		{"I47-edge-count", "/kinds/0/sources/0", "edge_types", `["declared_dataflow","registered_resource_use","static_dependency"]`, "", "count"},
		{"I48-edge-order", "/kinds/0/sources/0", "edge_types", `["static_dependency","declared_dataflow"]`, "/1", "unsorted"},
		{"I49-edge-duplicate", "/kinds/0/sources/0", "edge_types", `["declared_dataflow","declared_dataflow"]`, "/1", "duplicate"},
		{"I50-encounter-edge", "/kinds/1/sources/0", "edge_types", `["co_access"]`, "/0", "unknown_value"},
		{"I51-edge-missing", "/kinds/0/sources/0", "edge_types", `["declared_dataflow"]`, "", "mismatch"},
		{"I52-local-edge", "/kinds/1/sources/0", "edge_types", `["declared_dataflow"]`, "", "mismatch"},
		{"I54-edge-type", "/kinds/0/sources/0", "edge_types", `"declared_dataflow"`, "", "wrong_type"},
	} {
		parent := rawAt(t, full, tc.source)
		changed := editMember(t, parent, "/coupling", tc.key, tc.value)
		field(tc.name, replaceUnique(t, full, string(parent), string(changed)), tc.source+"/coupling/"+tc.key+tc.suffix, tc.violation)
	}
	changed := addMember(t, source0, "/coupling", "weight_ppm", "1")
	field("I53-coupling-extra", replaceUnique(t, full, string(source0), string(changed)), "/kinds/0/sources/0/coupling/weight_ppm", "unknown_field")
	return rows
}

func checkRejection(t testing.TB, err error, tc rejection) {
	t.Helper()
	switch {
	case tc.problem != "":
		var got *MalformedError
		if !errors.Is(err, ErrMalformed) || !errors.As(err, &got) {
			t.Fatalf("got %T %v, want MalformedError{%s}", err, err, tc.problem)
		}
		if got.Problem != tc.problem {
			t.Fatalf("problem = %s, want %s", got.Problem, tc.problem)
		}
		if tc.problem == "bom" && got.Offset != 0 {
			t.Fatalf("BOM offset = %d", got.Offset)
		}
	case tc.field != "":
		var got *UnsupportedError
		if !errors.Is(err, ErrUnsupported) || !errors.As(err, &got) {
			t.Fatalf("got %T %v, want UnsupportedError", err, err)
		}
		if got.Field != tc.field || got.Value != tc.value || got.Supported != tc.supported {
			t.Fatalf("unsupported = %+v, want %s/%s/%s", got, tc.field, tc.value, tc.supported)
		}
	default:
		var got *FieldError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &got) {
			t.Fatalf("got %T %v, want FieldError{%s,%s}", err, err, tc.path, tc.violation)
		}
		if got.Path != tc.path || got.Violation != tc.violation {
			t.Fatalf("field = %+v, want %s/%s", got, tc.path, tc.violation)
		}
	}
}

func TestLawRejects(t *testing.T) {
	for _, tc := range lawRejections(t) {
		for _, p := range profiles() {
			data := bytes.Clone(tc.data)
			t.Run(tc.name+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				got, err := Load(data, p.limits)
				checkRejection(t, err, tc)
				if !reflect.DeepEqual(got, Law{}) {
					t.Fatal("partial law returned on rejection")
				}
			})
		}
	}
}

func TestLawRefusesLocalData(t *testing.T) {
	full := fixture(t, "law/v1/full.json")
	for _, name := range []string{"workspace_id", "workspace_policies", "check_policy_hash", "environment_policy_hash", "validation_policy_hash", "field_law_sha256", "activation_id", "owner_approval_id", "epoch", "effective_sequence", "not_before_utc_ns", "grants", "credentials", "flake_rate_prior", "failure_repair_confidence", "quarantine_threshold"} {
		for _, parent := range []string{"", "/kinds/0"} {
			for _, p := range profiles() {
				data := addMember(t, full, parent, name, "1")
				t.Run(name+parent+"/"+p.name, func(t *testing.T) {
					t.Parallel()
					got, err := Load(data, p.limits)
					checkRejection(t, err, rejection{path: parent + "/" + name, violation: "unknown_field"})
					if !reflect.DeepEqual(got, Law{}) {
						t.Fatal("partial law with local data")
					}
				})
			}
		}
	}
}

func TestFieldViolationOrder(t *testing.T) {
	// This version's deterministic order is tested, not promised forever.
	head := `"format":"dwarf-harness/validation-policy","schema_version":1,`
	for _, tc := range []struct {
		name, root, path string
		violation        Violation
	}{
		{"head-before-unknown", `"schema_version":1,"z":0`, "/format", "missing_field"},
		{"unknown-input-order", head + `"z":0,"a":0`, "/z", "unknown_field"},
		{"missing-table-order", head + `"quarantine_threshold":null`, "/flake_rate_prior", "missing_field"},
		{"missing-before-type", head + `"flake_rate_prior":null,"quarantine_threshold":null`, "/failure_repair_confidence", "missing_field"},
		{"rational-unknown-first", head + `"flake_rate_prior":{"extra":0,"numerator":null},"failure_repair_confidence":null,"quarantine_threshold":null`, "/flake_rate_prior/extra", "unknown_field"},
		{"rational-missing-first", head + `"flake_rate_prior":{"numerator":null},"failure_repair_confidence":null,"quarantine_threshold":null`, "/flake_rate_prior/denominator", "missing_field"},
		{"member-table-order", head + `"flake_rate_prior":{"denominator":0,"numerator":null},"failure_repair_confidence":null,"quarantine_threshold":null`, "/flake_rate_prior/numerator", "wrong_type"},
		{"members-before-cross-check", head + `"flake_rate_prior":{"numerator":100,"denominator":null},"failure_repair_confidence":null,"quarantine_threshold":null`, "/flake_rate_prior/denominator", "wrong_type"},
	} {
		for _, p := range profiles() {
			t.Run(tc.name+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				got, err := LoadValidationPolicy([]byte("{"+tc.root+"}"), p.limits)
				checkRejection(t, err, rejection{path: tc.path, violation: tc.violation})
				if got != (ValidationPolicy{}) {
					t.Fatal("partial policy on ordered refusal")
				}
			})
		}
	}
}

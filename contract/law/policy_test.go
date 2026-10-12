package law

import (
	"bytes"
	"strconv"
	"testing"
)

func expectedPolicy() ValidationPolicy {
	// Provisional test values, not owner-selected or activated parameters.
	return ValidationPolicy{SchemaVersion: 1, FlakeRatePrior: Rational{1, 20}, FailureRepairConfidence: Rational{999, 1000}, QuarantineThreshold: Rational{1, 5}}
}

func TestValidationPolicyLoads(t *testing.T) {
	for _, p := range profiles() {
		data := fixture(t, "validation-policy/v1/policy.json")
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			got, err := LoadValidationPolicy(data, p.limits)
			if err != nil {
				t.Fatal(err)
			}
			want := expectedPolicy()
			want.SHA256 = pinnedDigest(t, goldenSHA256["validation-policy/v1/policy.json"])
			if got != want {
				t.Fatalf("policy = %+v, want %+v", got, want)
			}
		})
	}
}

func policyRejections(t testing.TB) []rejection {
	t.Helper()
	policy := fixture(t, "validation-policy/v1/policy.json")
	var rows []rejection
	for _, tc := range []struct {
		name, parent, key, value string
		violation                Violation
	}{
		{"VP1-zero-numerator", "/flake_rate_prior", "numerator", "0", "out_of_range"},
		{"VP2-zero-denominator", "/quarantine_threshold", "denominator", "0", "out_of_range"},
		{"VP3-equal", "/failure_repair_confidence", "numerator", "1000", "out_of_range"},
		{"VP4-above", "/flake_rate_prior", "numerator", "21", "out_of_range"},
		{"VP5-no-prior", "", "flake_rate_prior", "", "missing_field"},
		{"VP5-no-confidence", "", "failure_repair_confidence", "", "missing_field"},
		{"VP5-no-threshold", "", "quarantine_threshold", "", "missing_field"},
		{"VP6-no-denominator", "/flake_rate_prior", "denominator", "", "missing_field"},
		{"VP9-string-rational", "", "flake_rate_prior", `"1/20"`, "wrong_type"},
		{"numerator-overflow", "/flake_rate_prior", "numerator", "18446744073709551616", "out_of_range"},
		{"denominator-overflow", "/flake_rate_prior", "denominator", "18446744073709551616", "out_of_range"},
		{"null-rational", "", "flake_rate_prior", "null", "wrong_type"},
		{"string-numerator", "/flake_rate_prior", "numerator", `"1"`, "wrong_type"},
	} {
		rows = append(rows, rejection{name: tc.name, data: editMember(t, policy, tc.parent, tc.key, tc.value), path: tc.parent + "/" + tc.key, violation: tc.violation})
	}
	for _, tc := range []struct{ name, parent, key, value string }{
		{"VP7-kinds", "", "kinds", "[]"}, {"VP7-label", "", "law_id", `"x"`}, {"VP8-extra-rational", "/flake_rate_prior", "value", "1"},
		{"VP16-three-only", "", "max_fresh_executions_per_incident", "12"},
	} {
		rows = append(rows, rejection{name: tc.name, data: addMember(t, policy, tc.parent, tc.key, tc.value), path: tc.parent + "/" + tc.key, violation: "unknown_field"})
	}
	rows = append(rows,
		rejection{name: "VP10-fraction", data: editMember(t, policy, "", "flake_rate_prior", "0.05"), problem: "not_unsigned_integer"},
		rejection{name: "VP11-negative", data: editMember(t, policy, "/flake_rate_prior", "numerator", "-1"), problem: "not_unsigned_integer"},
		rejection{name: "VP12-duplicate", data: addMember(t, policy, "/flake_rate_prior", "numerator", "1"), problem: "duplicate_key"},
		rejection{name: "VP13-format", data: editMember(t, policy, "", "format", strconv.Quote(LawFormat)), field: "format", value: LawFormat, supported: PolicyFormat},
		rejection{name: "VP14-schema", data: editMember(t, policy, "", "schema_version", "2"), field: "schema_version", value: "2", supported: "1"},
		rejection{name: "VP17-law", data: fixture(t, "law/v1/full.json"), field: "format", value: LawFormat, supported: PolicyFormat},
	)
	return rows
}

func TestValidationPolicyRejects(t *testing.T) {
	for _, tc := range policyRejections(t) {
		for _, p := range profiles() {
			data := bytes.Clone(tc.data)
			t.Run(tc.name+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				got, err := LoadValidationPolicy(data, p.limits)
				checkRejection(t, err, tc)
				if got != (ValidationPolicy{}) {
					t.Fatal("partial policy on rejection")
				}
			})
		}
	}
}

func TestValidationPolicyExactRationals(t *testing.T) {
	policy := fixture(t, "validation-policy/v1/policy.json")
	for _, tc := range []struct {
		name, numerator, denominator string
		want                         Rational
	}{
		{"unreduced", "2", "40", Rational{2, 40}},
		{"uint64-bound", "18446744073709551614", "18446744073709551615", Rational{18446744073709551614, 18446744073709551615}},
	} {
		data := editMember(t, policy, "/flake_rate_prior", "numerator", tc.numerator)
		data = editMember(t, data, "/flake_rate_prior", "denominator", tc.denominator)
		for _, p := range profiles() {
			data := bytes.Clone(data)
			t.Run(tc.name+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				got, err := LoadValidationPolicy(data, p.limits)
				if err != nil {
					t.Fatal(err)
				}
				if got.FlakeRatePrior != tc.want {
					t.Fatalf("fraction rounded or reduced: %+v", got.FlakeRatePrior)
				}
				if got.SHA256 == pinnedDigest(t, goldenSHA256["validation-policy/v1/policy.json"]) {
					t.Fatal("different fraction spelling retained original identity")
				}
			})
		}
	}
}
